//go:build !darwin

package main

import (
	"context"
	"fmt"
)

// StartMicrophoneCapture returns an error on non-darwin platforms where CoreAudio is unavailable.
func StartMicrophoneCapture(ctx context.Context) (<-chan []byte, func(), error) {
	return nil, nil, fmt.Errorf("microphone capture is only implemented for macOS via CoreAudio")
}
