//go:build !darwin

package ai

// Only macOS has microphone capture (see mic_darwin.go).

import (
	"context"
	"fmt"
)

func StartMicrophoneCapture(ctx context.Context) (<-chan []byte, func(), error) {
	return nil, nil, fmt.Errorf("microphone capture is only implemented for macOS via CoreAudio")
}
