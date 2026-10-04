//go:build darwin

package ai

// Mac microphone capture through CoreAudio (the C code below). CoreAudio fills
// four 100 ms buffers of 16 kHz mono PCM in turn, and goAudioCallback passes
// each full one to the channel StartMicrophoneCapture returned.

/*
#cgo LDFLAGS: -framework AudioToolbox -framework CoreAudio
#include <AudioToolbox/AudioToolbox.h>
#include <stdlib.h>

extern void goAudioCallback(void *data, int size);

static void audioQueueInputCallback(
    void *custom_data,
    AudioQueueRef queue,
    AudioQueueBufferRef buffer,
    const AudioTimeStamp *start_time,
    UInt32 num_packets,
    const AudioStreamPacketDescription *packet_desc
) {
    if (num_packets > 0 && buffer->mAudioDataByteSize > 0) {
        goAudioCallback(buffer->mAudioData, (int)buffer->mAudioDataByteSize);
    }
    AudioQueueEnqueueBuffer(queue, buffer, 0, NULL);
}

typedef struct {
    AudioQueueRef queue;
    AudioQueueBufferRef buffers[4];
} AudioRecorder;

static AudioRecorder* createAudioRecorder() {
    AudioStreamBasicDescription format;
    memset(&format, 0, sizeof(format));
    format.mSampleRate = 16000.0;
    format.mFormatID = kAudioFormatLinearPCM;
    format.mFormatFlags = kLinearPCMFormatFlagIsSignedInteger | kLinearPCMFormatFlagIsPacked;
    format.mBitsPerChannel = 16;
    format.mChannelsPerFrame = 1;
    format.mBytesPerFrame = 2;
    format.mFramesPerPacket = 1;
    format.mBytesPerPacket = 2;

    AudioRecorder* rec = (AudioRecorder*)malloc(sizeof(AudioRecorder));
    if (!rec) return NULL;

    OSStatus status = AudioQueueNewInput(&format, audioQueueInputCallback, NULL, NULL, NULL, 0, &rec->queue);
    if (status != noErr) {
        free(rec);
        return NULL;
    }

    int bufferByteSize = 3200;
    for (int i = 0; i < 4; i++) {
        AudioQueueAllocateBuffer(rec->queue, bufferByteSize, &rec->buffers[i]);
        AudioQueueEnqueueBuffer(rec->queue, rec->buffers[i], 0, NULL);
    }

    return rec;
}

static int startAudioRecorder(AudioRecorder* rec) {
    if (!rec) return -1;
    OSStatus status = AudioQueueStart(rec->queue, NULL);
    return (int)status;
}

static void stopAudioRecorder(AudioRecorder* rec) {
    if (!rec) return;
    AudioQueueStop(rec->queue, true);
    AudioQueueDispose(rec->queue, true);
    free(rec);
}
*/
import "C"
import (
	"context"
	"fmt"
	"sync"
	"unsafe"
)

var (
	activeAudioChan   chan []byte
	activeAudioChanMu sync.Mutex
)

//export goAudioCallback
func goAudioCallback(data unsafe.Pointer, size C.int) {
	activeAudioChanMu.Lock()
	defer activeAudioChanMu.Unlock()

	if activeAudioChan == nil || size <= 0 {
		return
	}

	buf := C.GoBytes(data, size)
	select {
	case activeAudioChan <- buf:
	default:
	}
}

// StartMicrophoneCapture records 16 kHz 16-bit mono PCM from the default microphone.
func StartMicrophoneCapture(ctx context.Context) (<-chan []byte, func(), error) {
	activeAudioChanMu.Lock()
	ch := make(chan []byte, 100)
	activeAudioChan = ch
	activeAudioChanMu.Unlock()

	rec := C.createAudioRecorder()
	if rec == nil {
		return nil, nil, fmt.Errorf("failed to create CoreAudio recorder")
	}

	if status := C.startAudioRecorder(rec); status != 0 {
		C.stopAudioRecorder(rec)
		return nil, nil, fmt.Errorf("failed to start CoreAudio recorder: status %d", status)
	}

	stopOnce := sync.Once{}
	stop := func() {
		stopOnce.Do(func() {
			C.stopAudioRecorder(rec)
			activeAudioChanMu.Lock()
			if activeAudioChan == ch {
				close(activeAudioChan)
				activeAudioChan = nil
			}
			activeAudioChanMu.Unlock()
		})
	}

	go func() {
		<-ctx.Done()
		stop()
	}()

	return ch, stop, nil
}
