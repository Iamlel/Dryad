package ai

// The plants' ElevenLabs voices. Speak plays them here for the terminal chat;
// the website gets the MP3 from talk.go.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	elevenlabs "github.com/plexusone/elevenlabs-go"
)

// The default voices are premade ElevenLabs voices, which work on the free plan.
const (
	DefaultVoiceSpike = "pNInz6obpgDQGcFmaJgB" // Adam, deep and dry
	DefaultVoiceFern  = "cgSgspJ2msm6clMCkdW9" // Jessica, bright and warm
)

// LegacyLibraryVoiceRachel is a library voice; free accounts get 402 Payment Required.
const LegacyLibraryVoiceRachel = "21m00Tcm4TlvDq8ikWAM"

type TTSManager struct {
	Client *elevenlabs.Client
}

func NewTTSManager(apiKey string) (*TTSManager, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("no ElevenLabs API key provided")
	}

	client, err := elevenlabs.NewClient(
		elevenlabs.WithAPIKey(apiKey),
	)
	if err != nil {
		return nil, err
	}

	return &TTSManager{Client: client}, nil
}

// CleanSpokenText strips markdown and a leading "Name:" so the voice sounds natural.
func CleanSpokenText(name, text string) string {
	cleaned := strings.TrimSpace(text)

	prefixes := []string{
		name + ":",
		"**" + name + "**:",
		"*" + name + "*:",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(strings.ToLower(cleaned), strings.ToLower(p)) {
			cleaned = strings.TrimSpace(cleaned[len(p):])
			break
		}
	}

	cleaned = strings.ReplaceAll(cleaned, "**", "")
	cleaned = strings.ReplaceAll(cleaned, "*", "")
	return strings.TrimSpace(cleaned)
}

// ResolveVoiceID is the personality's voice, or Spike's or Fern's default. The
// paid-only Rachel voice becomes Fern's.
func ResolveVoiceID(p *Personality) string {
	voice := strings.TrimSpace(p.VoiceID)

	if voice == LegacyLibraryVoiceRachel {
		return DefaultVoiceFern
	}

	if voice != "" {
		return voice
	}

	switch strings.ToLower(p.ID) {
	case "spike":
		return DefaultVoiceSpike
	case "fern":
		return DefaultVoiceFern
	default:
		return DefaultVoiceFern
	}
}

// Speak says text in the personality's voice on this computer's speakers.
func (tm *TTSManager) Speak(ctx context.Context, p *Personality, text string) error {
	cleanText := CleanSpokenText(p.Name, text)
	if cleanText == "" {
		return nil
	}

	voiceID := ResolveVoiceID(p)

	audioStream, err := tm.Client.TTS().Simple(ctx, voiceID, cleanText)
	if err != nil {
		// A voice this account can't use (like a paid one): try the default instead.
		fallbackID := DefaultVoiceFern
		if strings.EqualFold(p.ID, "spike") {
			fallbackID = DefaultVoiceSpike
		}
		if voiceID != fallbackID {
			fmt.Printf("⚠️  [Voice %s failed (%v); falling back to default voice]\n", voiceID, err)
			audioStream, err = tm.Client.TTS().Simple(ctx, fallbackID, cleanText)
		}
	}

	if err != nil {
		return fmt.Errorf("TTS generation failed: %w", err)
	}

	tmpFile, err := os.CreateTemp("", fmt.Sprintf("voice_%s_*.mp3", p.ID))
	if err != nil {
		return fmt.Errorf("failed to create temp audio file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmpFile, audioStream); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write audio data: %w", err)
	}
	tmpFile.Close()

	return playAudioFile(tmpPath)
}

func playAudioFile(filePath string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin": // macOS has built-in afplay
		cmd = exec.Command("afplay", filePath)
	case "linux":
		if _, err := exec.LookPath("paplay"); err == nil {
			cmd = exec.Command("paplay", filePath)
		} else if _, err := exec.LookPath("aplay"); err == nil {
			cmd = exec.Command("aplay", filePath)
		} else if _, err := exec.LookPath("ffplay"); err == nil {
			cmd = exec.Command("ffplay", "-nodisp", "-autoexit", filePath)
		} else {
			return fmt.Errorf("no audio player found (install afplay, paplay, aplay, or ffplay)")
		}
	case "windows":
		cmd = exec.Command("powershell", "-c", fmt.Sprintf(`(New-Object Media.SoundPlayer "%s").PlaySync()`, filePath))
	default:
		return fmt.Errorf("unsupported platform for audio playback: %s", runtime.GOOS)
	}

	return cmd.Run()
}
