package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// getAPIKey looks for the Gemini API key in environment variables,
// .env file, or a dedicated key file.
func getAPIKey() string {
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		return key
	}
	if key := os.Getenv("GOOGLE_API_KEY"); key != "" {
		return key
	}

	if key := loadKeyFromEnvFile(".env", "GEMINI_API_KEY", "GOOGLE_API_KEY"); key != "" {
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

// getElevenLabsKey looks for the ElevenLabs API key in environment variables,
// .env file, or a dedicated key file.
func getElevenLabsKey() string {
	if key := os.Getenv("ELEVENLABS_API_KEY"); key != "" {
		return key
	}
	if key := os.Getenv("XI_API_KEY"); key != "" {
		return key
	}

	if key := loadKeyFromEnvFile(".env", "ELEVENLABS_API_KEY", "XI_API_KEY"); key != "" {
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

func loadKeyFromEnvFile(filename string, targetKeys ...string) string {
	file, err := os.Open(filename)
	if err != nil {
		return ""
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

func selectOrMakePersonality(scanner *bufio.Scanner, list []*Personality) (*Personality, []*Personality) {
	for {
		fmt.Println("\n========================================")
		fmt.Println(" Choose a Personality to Chat With:")
		fmt.Println("========================================")
		for i, p := range list {
			hasMem := ""
			if strings.TrimSpace(p.Memory) != "" {
				hasMem = " [🧠 Has memories]"
			}
			voiceNote := ""
			if p.VoiceID != "" {
				voiceNote = fmt.Sprintf(" [🎙️ %s]", p.VoiceID[:min(8, len(p.VoiceID))])
			}
			fmt.Printf(" [%d] %s (%s)%s%s\n", i+1, p.Name, p.Tagline, voiceNote, hasMem)
		}
		fmt.Printf(" [%d] + Create a new custom personality\n", len(list)+1)
		fmt.Print("\nEnter choice: ")

		if !scanner.Scan() {
			return nil, list
		}
		choiceStr := strings.TrimSpace(scanner.Text())
		choice, err := strconv.Atoi(choiceStr)
		if err != nil || choice < 1 || choice > len(list)+1 {
			fmt.Println("Invalid choice, please try again.")
			continue
		}

		if choice == len(list)+1 {
			// Create new
			fmt.Print("\nEnter Personality Name (e.g. Daisy): ")
			if !scanner.Scan() {
				return nil, list
			}
			name := strings.TrimSpace(scanner.Text())
			if name == "" {
				name = "Custom Persona"
			}

			fmt.Print("Enter a short tagline/descriptor: ")
			if !scanner.Scan() {
				return nil, list
			}
			tagline := strings.TrimSpace(scanner.Text())

			fmt.Println("Enter character instructions (who are they? how do they talk?): ")
			if !scanner.Scan() {
				return nil, list
			}
			instructions := strings.TrimSpace(scanner.Text())

			fmt.Print("Enter ElevenLabs Voice ID (press Enter for default): ")
			if !scanner.Scan() {
				return nil, list
			}
			voiceID := strings.TrimSpace(scanner.Text())

			newP := &Personality{
				ID:          strings.ToLower(strings.ReplaceAll(name, " ", "_")),
				Name:        name,
				Tagline:     tagline,
				Instruction: instructions,
				Memory:      "",
				VoiceID:     voiceID,
			}
			list = append(list, newP)
			_ = SavePersonalities(list)
			fmt.Printf("Personality '%s' created!\n", name)
			return newP, list
		}

		return list[choice-1], list
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func main() {
	geminiKey := getAPIKey()
	if geminiKey == "" {
		fmt.Println("Error: Gemini API key not found.")
		fmt.Println("\nYou can provide the key using one of the following methods:")
		fmt.Println("  1. In '.env': GEMINI_API_KEY=your_key")
		fmt.Println("  2. In 'api_key.txt': your_key")
		fmt.Println("  3. export GEMINI_API_KEY=\"your_key\"")
		os.Exit(1)
	}

	elevenLabsKey := getElevenLabsKey()
	var ttsManager *TTSManager
	ttsEnabled := false

	if elevenLabsKey != "" {
		tm, err := NewTTSManager(elevenLabsKey)
		if err != nil {
			fmt.Printf("⚠️  ElevenLabs initialization warning: %v\n", err)
		} else {
			ttsManager = tm
			ttsEnabled = true
		}
	}

	ctx := context.Background()

	client, err := genai.NewClient(ctx, option.WithAPIKey(geminiKey))
	if err != nil {
		log.Fatalf("Failed to create Gemini client: %v", err)
	}
	defer client.Close()

	modelName := os.Getenv("GEMINI_MODEL")
	if modelName == "" {
		modelName = "models/gemini-3.8-flash"
	}

	personalities, err := LoadPersonalities()
	if err != nil {
		log.Fatalf("Failed to load personalities: %v", err)
	}

	scanner := bufio.NewScanner(os.Stdin)

	for {
		persona, updatedList := selectOrMakePersonality(scanner, personalities)
		if persona == nil {
			break
		}
		personalities = updatedList

		// Configure Gemini model with this personality's instructions and memories
		model := client.GenerativeModel(modelName)
		sysInstruction := BuildSystemInstruction(persona)
		model.SystemInstruction = &genai.Content{
			Parts: []genai.Part{genai.Text(sysInstruction)},
		}

		chat := model.StartChat()

		voiceStatus := "🔇 Disabled (no ELEVENLABS_API_KEY)"
		if ttsManager != nil {
			if ttsEnabled {
				voiceStatus = fmt.Sprintf("🔊 Enabled (%s)", ResolveVoiceID(persona)[:min(8, len(ResolveVoiceID(persona)))])
			} else {
				voiceStatus = "🔇 Muted"
			}
		}

		fmt.Printf("\n--- Now chatting with %s ---\n", persona.Name)
		fmt.Printf("Audio Voice: %s\n", voiceStatus)
		fmt.Println("Commands:")
		fmt.Println("  /memory       - View current memories for this persona")
		fmt.Println("  /clear-memory - Erase memory for this persona")
		fmt.Println("  /test-voice   - Play a test audio phrase in this persona's voice")
		fmt.Println("  /toggle-voice - Toggle audio voice output on/off")
		fmt.Println("  /switch       - Save memory and switch to another personality")
		fmt.Println("  exit or quit  - Save memory and exit program")
		fmt.Println("----------------------------------------\n")

		var sessionTurns []string
		exiting := false

		for {
			fmt.Printf("You: ")
			if !scanner.Scan() {
				exiting = true
				break
			}

			input := strings.TrimSpace(scanner.Text())
			if input == "" {
				continue
			}

			// Handle commands
			if strings.EqualFold(input, "exit") || strings.EqualFold(input, "quit") {
				exiting = true
				break
			}

			if strings.EqualFold(input, "/switch") {
				break
			}

			if strings.EqualFold(input, "/memory") {
				fmt.Printf("\n[🧠 Stored Memory for %s]:\n", persona.Name)
				if strings.TrimSpace(persona.Memory) == "" {
					fmt.Println("  (No memories recorded yet.)")
				} else {
					fmt.Println(persona.Memory)
				}
				fmt.Println()
				continue
			}

			if strings.EqualFold(input, "/clear-memory") {
				persona.Memory = ""
				_ = SavePersonalities(personalities)
				fmt.Printf("[Memory cleared for %s]\n\n", persona.Name)
				continue
			}

			if strings.EqualFold(input, "/toggle-voice") {
				if ttsManager == nil {
					fmt.Println("\n[ElevenLabs key not found. Add ELEVENLABS_API_KEY to your .env to enable audio]\n")
				} else {
					ttsEnabled = !ttsEnabled
					if ttsEnabled {
						fmt.Println("\n[🔊 Voice output turned ON]\n")
					} else {
						fmt.Println("\n[🔇 Voice output turned OFF]\n")
					}
				}
				continue
			}

			if strings.EqualFold(input, "/test-voice") {
				if ttsManager == nil {
					fmt.Println("\n[ElevenLabs key not found. Add ELEVENLABS_API_KEY to your .env to enable audio]\n")
				} else {
					testPhrase := fmt.Sprintf("Hello! I am %s. %s", persona.Name, persona.Tagline)
					fmt.Printf("\n[🔊 Playing sample voice for %s...]\n", persona.Name)
					if err := ttsManager.Speak(ctx, persona, testPhrase); err != nil {
						fmt.Printf("[Voice test error: %v]\n\n", err)
					} else {
						fmt.Println("[Audio playback finished]\n")
					}
				}
				continue
			}

			sessionTurns = append(sessionTurns, fmt.Sprintf("User: %s", input))

			fmt.Printf("\n%s: ", persona.Name)

			// Send message with automatic retry for 503 high demand
			var resp *genai.GenerateContentResponse
			var sendErr error
			for attempt := 0; attempt < 3; attempt++ {
				resp, sendErr = chat.SendMessage(ctx, genai.Text(input))
				if sendErr == nil {
					break
				}
				if strings.Contains(sendErr.Error(), "503") || strings.Contains(sendErr.Error(), "high demand") {
					if attempt < 2 {
						fmt.Print("[Model busy, retrying in 2s...] ")
						time.Sleep(2 * time.Second)
						continue
					}
				}
				break
			}

			if sendErr != nil {
				fmt.Printf("\nError generating response: %v\n\n", sendErr)
				continue
			}

			var replyBuilder strings.Builder
			for _, cand := range resp.Candidates {
				if cand.Content != nil {
					for _, part := range cand.Content.Parts {
						replyText := fmt.Sprintf("%v", part)
						fmt.Print(replyText)
						replyBuilder.WriteString(replyText)
					}
				}
			}
			fmt.Println()

			replyStr := strings.TrimSpace(replyBuilder.String())
			sessionTurns = append(sessionTurns, fmt.Sprintf("%s: %s", persona.Name, replyStr))

			// Play speech if TTS is active
			if ttsManager != nil && ttsEnabled && replyStr != "" {
				fmt.Printf("🔊 [Playing %s's voice...]\n", persona.Name)
				if err := ttsManager.Speak(ctx, persona, replyStr); err != nil {
					fmt.Printf("⚠️  [Audio playback error: %v]\n", err)
				}
			}
			fmt.Println()
		}

		// Save updated memory if any turns happened
		if len(sessionTurns) > 0 {
			fmt.Printf("\n[Saving and updating memories for %s...]\n", persona.Name)
			if err := UpdateMemory(ctx, client, modelName, persona, sessionTurns); err != nil {
				fmt.Printf("Warning: Could not summarize memories: %v\n", err)
			} else {
				if err := SavePersonalities(personalities); err != nil {
					fmt.Printf("Warning: Could not save personality file: %v\n", err)
				} else {
					fmt.Printf("[Memories successfully updated and saved!]\n")
				}
			}
		}

		if exiting {
			fmt.Println("Goodbye!")
			break
		}
	}
}
