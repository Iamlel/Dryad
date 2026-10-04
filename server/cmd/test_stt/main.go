// test_stt checks that an ElevenLabs key can do speech to text. From server/:
//
//	go run ./cmd/test_stt
package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// loadKey reads a key from a .env file here or up to two folders up.
func loadKey(filename string, targetKeys ...string) string {
	file, err := os.Open(filename)
	if err != nil {
		file, err = os.Open("../" + filename)
		if err != nil {
			file, err = os.Open("../../" + filename)
			if err != nil {
				return ""
			}
		}
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
			for _, target := range targetKeys {
				if strings.EqualFold(k, target) && v != "" {
					return v
				}
			}
		}
	}
	return ""
}

func main() {
	key := os.Getenv("ELEVENLABS_API_KEY")
	if key == "" {
		key = loadKey(".env", "ELEVENLABS_API_KEY", "XI_API_KEY")
	}
	if key == "" {
		fmt.Println("No ElevenLabs API key found in environment or .env file.")
		return
	}

	fmt.Printf("Checking ElevenLabs Speech to Text status for key length %d...\n\n", len(key))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Only keys with speech_to_text get a realtime token, so asking is the check.
	tokenURL := "https://api.elevenlabs.io/v1/single-use-token/realtime_scribe"
	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, nil)
	if err != nil {
		fmt.Printf("Request error: %v\n", err)
		return
	}
	req.Header.Set("xi-api-key", key)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("Connection error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	respStr := string(body)

	if resp.StatusCode == http.StatusOK {
		fmt.Println("Success: Speech to text permission is active on your ElevenLabs account.")
		fmt.Println("Realtime STT is ready for use.")
		return
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		if strings.Contains(respStr, "missing_permissions") || strings.Contains(respStr, "speech_to_text") {
			fmt.Println("Speech to text is blocked by API key permissions.")
			fmt.Println()
			fmt.Println("Reason reported by ElevenLabs:")
			fmt.Printf("  %s\n\n", respStr)
			fmt.Println("How to resolve:")
			fmt.Println("  1. Log into your ElevenLabs account at elevenlabs.io")
			fmt.Println("  2. Navigate to Profile > API Keys")
			fmt.Println("  3. Create a new API key with full access or enable speech_to_text")
			fmt.Println("  4. Update ELEVENLABS_API_KEY in your .env file")
			return
		}

		fmt.Println("Authentication error with ElevenLabs API key.")
		fmt.Printf("Response (%d): %s\n", resp.StatusCode, respStr)
		return
	}

	fmt.Printf("Unexpected ElevenLabs response (%d): %s\n", resp.StatusCode, respStr)
}
