package ai

// Keys and settings come from the environment, then .env, then a key file.

import (
	"bufio"
	"os"
	"strings"
)

// GetAPIKey returns the Gemini API key.
func GetAPIKey() string {
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		return key
	}
	if key := os.Getenv("GOOGLE_API_KEY"); key != "" {
		return key
	}

	if key := LoadKeyFromEnvFile(".env", "GEMINI_API_KEY", "GOOGLE_API_KEY"); key != "" {
		return key
	}

	candidates := []string{"api_key.txt", "key.txt", "gemini_key.txt"}
	for _, filename := range candidates {
		if content, err := os.ReadFile(filename); err == nil {
			trimmed := strings.TrimSpace(string(content))
			if trimmed != "" {
				return trimmed
			}
		}
	}

	return ""
}

// GetElevenLabsKey returns the ElevenLabs API key.
func GetElevenLabsKey() string {
	if key := os.Getenv("ELEVENLABS_API_KEY"); key != "" {
		return key
	}
	if key := os.Getenv("XI_API_KEY"); key != "" {
		return key
	}

	if key := LoadKeyFromEnvFile(".env", "ELEVENLABS_API_KEY", "XI_API_KEY"); key != "" {
		return key
	}

	candidates := []string{"elevenlabs_key.txt", "xi_key.txt"}
	for _, filename := range candidates {
		if content, err := os.ReadFile(filename); err == nil {
			trimmed := strings.TrimSpace(string(content))
			if trimmed != "" {
				return trimmed
			}
		}
	}

	return ""
}

// LoadKeyFromEnvFile returns the first of targetKeys set in filename, looking
// in this folder and then the one above.
func LoadKeyFromEnvFile(filename string, targetKeys ...string) string {
	file, err := os.Open(filename)
	if err != nil {
		if !strings.HasPrefix(filename, "..") {
			file, err = os.Open("../" + filename)
			if err != nil {
				return ""
			}
		} else {
			return ""
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
