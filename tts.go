package main

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

// Default premade ElevenLabs voice IDs for standard characters
// Uses premade voices that are fully supported on ElevenLabs free tier accounts
const (
	DefaultVoiceSpike = "pNInz6obpgDQGcFmaJgB" // Adam - deep, stoic, dry
	DefaultVoiceFern  = "cgSgspJ2msm6clMCkdW9" // Jessica - playful, bright, warm
)

// LegacyLibraryVoiceRachel is a community library voice that causes 402 Payment Required on free tier
const LegacyLibraryVoiceRachel = "21m00Tcm4TlvDq8ikWAM"

// TTSManager handles text-to-speech conversion and playback.
type TTSManager struct {
	client *elevenlabs.Client
}

// NewTTSManager initializes the ElevenLabs client if an API key is available.
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

	return &TTSManager{client: client}, nil
}

// CleanSpokenText strips markdown symbols and persona prefixes so the voice sounds natural.
func CleanSpokenText(name, text string) string {
	cleaned := strings.TrimSpace(text)

	// Remove persona name prefixes like "Fern: " or "**Fern**: "
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

	// Remove markdown asterisks (e.g. bold or italic notation)
	cleaned = strings.ReplaceAll(cleaned, "**", "")
	cleaned = strings.ReplaceAll(cleaned, "*", "")
	return strings.TrimSpace(cleaned)
}

// ResolveVoiceID returns the voice ID for a given personality.
func ResolveVoiceID(p *Personality) string {
	voice := strings.TrimSpace(p.VoiceID)

	// If using the legacy library voice that requires paid subscription, redirect to Jessica
	if voice == LegacyLibraryVoiceRachel {
		return DefaultVoiceFern
	}

	if voice != "" {
		return voice
	}

	// Match by ID or Name defaults
	switch strings.ToLower(p.ID) {
	case "spike":
		return DefaultVoiceSpike
	case "fern":
		return DefaultVoiceFern
	default:
		// Default to Jessica if not matched
		return DefaultVoiceFern
	}
}

// Speak converts text into speech and plays it through the system's audio output.
func (tm *TTSManager) Speak(ctx context.Context, p *Personality, text string) error {
	cleanText := CleanSpokenText(p.Name, text)
	if cleanText == "" {
		return nil
	}

	voiceID := ResolveVoiceID(p)

	audioStream, err := tm.client.TTS().Simple(ctx, voiceID, cleanText)
	if err != nil {
		// If custom voice failed (e.g. library voice requiring paid plan), attempt fallback
		fallbackID := DefaultVoiceFern
		if strings.EqualFold(p.ID, "spike") {
			fallbackID = DefaultVoiceSpike
		}
		if voiceID != fallbackID {
			fmt.Printf("⚠️  [Voice %s failed (%v); falling back to default voice]\n", voiceID, err)
			audioStream, err = tm.client.TTS().Simple(ctx, fallbackID, cleanText)
		}
	}

	if err != nil {
		return fmt.Errorf("TTS generation failed: %w", err)
	}

	// Save to a temporary MP3 file for playback
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

	// Play audio through system player
	return playAudioFile(tmpPath)
}

// playAudioFile plays the specified audio file using native OS utilities.
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
