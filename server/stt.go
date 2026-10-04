package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
	elevenlabs "github.com/plexusone/elevenlabs-go"
	"github.com/plexusone/elevenlabs-go/realtime"
)

// STTManager handles live speech to text transcription sessions using ElevenLabs.
type STTManager struct {
	client *elevenlabs.Client
}

// NewSTTManager initializes the ElevenLabs realtime speech to text manager.
func NewSTTManager(apiKey string) (*STTManager, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("no ElevenLabs API key provided")
	}

	client, err := elevenlabs.NewClient(
		elevenlabs.WithAPIKey(apiKey),
	)
	if err != nil {
		return nil, err
	}

	return &STTManager{client: client}, nil
}

// StartSession connects to ElevenLabs WebSocket STT with voice activity detection.
func (sm *STTManager) StartSession(ctx context.Context, silenceThresholdSecs float64) (*realtime.STTConnection, error) {
	if silenceThresholdSecs <= 0 {
		silenceThresholdSecs = 1.5
	}

	opts := &realtime.STTOptions{
		ModelID:                 "scribe_v2_realtime",
		AudioFormat:             "pcm_16000",
		CommitStrategy:          "vad",
		VADSilenceThresholdSecs: silenceThresholdSecs,
		IncludeTimestamps:       false,
	}

	return sm.client.Realtime().ConnectSTT(ctx, opts)
}

// ProcessSpeechTurn streams audio from a channel, transcribes it, and queries the LLM.
func ProcessSpeechTurn(
	ctx context.Context,
	sttManager *STTManager,
	backend AIBackend,
	persona *Personality,
	history []ChatMessage,
	audioStream <-chan []byte,
) (string, string, error) {
	conn, err := sttManager.StartSession(ctx, 1.5)
	if err != nil {
		return "", "", fmt.Errorf("failed to connect to ElevenLabs STT: %w", err)
	}
	defer conn.Close()

	errChan := make(chan error, 8)
	go func() {
		for err := range conn.Errors() {
			if err != nil {
				select {
				case errChan <- err:
				default:
				}
			}
		}
	}()

	// Forward incoming audio buffers to the ElevenLabs WebSocket
	go func() {
		for chunk := range audioStream {
			if len(chunk) > 0 {
				if err := conn.SendAudio(chunk); err != nil {
					break
				}
			}
		}
	}()

	// Wait for the final speech transcript
	for transcript := range conn.Transcripts() {
		if !transcript.IsFinal {
			continue
		}

		userText := strings.TrimSpace(transcript.Text)
		if userText == "" {
			continue
		}

		replyText, err := backend.Chat(ctx, persona, history, userText)
		if err != nil {
			return userText, "", fmt.Errorf("LLM error: %w", err)
		}

		return userText, replyText, nil
	}

	select {
	case err := <-errChan:
		return "", "", fmt.Errorf("ElevenLabs STT error: %w", err)
	default:
	}

	return "", "", fmt.Errorf("speech recognition ended with no speech")
}

// RecordSpeechTurn captures microphone audio until ElevenLabs detects end of speech and returns the transcript.
func RecordSpeechTurn(ctx context.Context, sttManager *STTManager) (string, error) {
	if sttManager == nil {
		return "", fmt.Errorf("STT manager is not initialized (check ELEVENLABS_API_KEY)")
	}

	audioChan, stopMic, err := StartMicrophoneCapture(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to open microphone: %w", err)
	}
	defer stopMic()

	conn, err := sttManager.StartSession(ctx, 1.5)
	if err != nil {
		return "", fmt.Errorf("failed to connect to ElevenLabs STT: %w", err)
	}
	defer conn.Close()

	errChan := make(chan error, 8)
	go func() {
		for err := range conn.Errors() {
			if err != nil {
				select {
				case errChan <- err:
				default:
				}
			}
		}
	}()

	// Stream microphone audio to ElevenLabs
	go func() {
		for chunk := range audioChan {
			if len(chunk) > 0 {
				if err := conn.SendAudio(chunk); err != nil {
					break
				}
			}
		}
	}()

	fmt.Println("Listening... speak now (ElevenLabs VAD will detect when you finish)")

	for transcript := range conn.Transcripts() {
		if !transcript.IsFinal {
			fmt.Printf("\rRecognizing: %s", transcript.Text)
			continue
		}

		userText := strings.TrimSpace(transcript.Text)
		if userText == "" {
			continue
		}

		fmt.Printf("\rRecognized: %s\n", userText)
		return userText, nil
	}

	select {
	case err := <-errChan:
		errMsg := strings.ToLower(err.Error())
		if strings.Contains(errMsg, "auth") || strings.Contains(errMsg, "permission") || strings.Contains(errMsg, "unauthorized") {
			return "", fmt.Errorf("ElevenLabs STT authentication error (API key is missing speech_to_text permission): %w", err)
		}
		return "", fmt.Errorf("ElevenLabs STT error: %w", err)
	default:
	}

	return "", fmt.Errorf("speech recognition ended without speech input")
}

var sttUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// SpeechTurnResult represents the response payload sent back to a WebSocket client.
type SpeechTurnResult struct {
	Type        string `json:"type"`
	UserText    string `json:"user_text,omitempty"`
	ReplyText   string `json:"reply_text,omitempty"`
	AudioBase64 string `json:"audio_base64,omitempty"`
	Error       string `json:"error,omitempty"`
}

// HandleSpeechWebSocket streams incoming client audio to ElevenLabs STT and returns the LLM answer.
func HandleSpeechWebSocket(
	w http.ResponseWriter,
	r *http.Request,
	sttManager *STTManager,
	backend AIBackend,
	ttsManager *TTSManager,
	persona *Personality,
	history *[]ChatMessage,
) {
	clientConn, err := sttUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("stt ws: upgrade error: %v", err)
		return
	}
	defer clientConn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	elevenConn, err := sttManager.StartSession(ctx, 1.5)
	if err != nil {
		_ = clientConn.WriteJSON(SpeechTurnResult{
			Type:  "error",
			Error: fmt.Sprintf("stt session error: %v", err),
		})
		return
	}
	defer elevenConn.Close()

	// Read audio chunks from client and pipe into ElevenLabs STT
	go func() {
		for {
			messageType, data, err := clientConn.ReadMessage()
			if err != nil {
				cancel()
				return
			}

			if messageType == websocket.BinaryMessage && len(data) > 0 {
				if err := elevenConn.SendAudio(data); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	// Listen for transcription events from ElevenLabs
	for {
		select {
		case <-ctx.Done():
			return
		case transcript, ok := <-elevenConn.Transcripts():
			if !ok {
				return
			}

			if !transcript.IsFinal {
				_ = clientConn.WriteJSON(SpeechTurnResult{
					Type:     "partial",
					UserText: transcript.Text,
				})
				continue
			}

			userText := strings.TrimSpace(transcript.Text)
			if userText == "" {
				continue
			}

			// Send final transcript to the active LLM
			replyText, err := backend.Chat(ctx, persona, *history, userText)
			if err != nil {
				_ = clientConn.WriteJSON(SpeechTurnResult{
					Type:  "error",
					Error: fmt.Sprintf("llm error: %v", err),
				})
				continue
			}

			*history = append(*history, ChatMessage{Role: "user", Content: userText})
			*history = append(*history, ChatMessage{Role: "assistant", Content: replyText})

			var audioB64 string
			if ttsManager != nil {
				voiceID := ResolveVoiceID(persona)
				audioStream, err := ttsManager.client.TTS().Simple(ctx, voiceID, CleanSpokenText(persona.Name, replyText))
				if err == nil {
					audioBytes, _ := io.ReadAll(audioStream)
					if len(audioBytes) > 0 {
						audioB64 = base64.StdEncoding.EncodeToString(audioBytes)
					}
				}
			}

			_ = clientConn.WriteJSON(SpeechTurnResult{
				Type:        "final",
				UserText:    userText,
				ReplyText:   replyText,
				AudioBase64: audioB64,
			})
		}
	}
}
