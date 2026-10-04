package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
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

func toggleOrSelectBackend(scanner *bufio.Scanner, current AIBackend, gemini *GeminiBackend, lm *LMStudioBackend) AIBackend {
	fmt.Println("\n----------------------------------------")
	fmt.Println(" Switch AI Backend:")
	fmt.Println("----------------------------------------")
	if gemini != nil {
		fmt.Printf("  [1] ☁️  Google Gemini (%s)\n", gemini.ModelName())
	} else {
		fmt.Println("  [1] ☁️  Google Gemini (Unavailable: GEMINI_API_KEY not set)")
	}
	fmt.Printf("  [2] 🏠 Local LM Studio (%s @ %s)\n", lm.ModelName(), lm.baseURL)
	fmt.Print("\nEnter choice [1 or 2, or press Enter to toggle]: ")

	if !scanner.Scan() {
		return current
	}
	ans := strings.TrimSpace(scanner.Text())

	var target AIBackend
	switch ans {
	case "1":
		if gemini == nil {
			fmt.Println("⚠️  Gemini is unavailable: GEMINI_API_KEY is not configured.")
			return current
		}
		target = gemini
	case "2":
		target = lm
	case "":
		// Quick toggle
		if current.Type() == BackendGemini {
			target = lm
		} else {
			if gemini == nil {
				fmt.Println("⚠️  Cannot switch to Gemini: GEMINI_API_KEY is not configured.")
				return current
			}
			target = gemini
		}
	default:
		fmt.Println("Invalid choice. Keeping current backend.")
		return current
	}

	if target.Type() == BackendLMStudio {
		fmt.Printf("Pinging LM Studio at %s...\n", lm.baseURL)
		if err := lm.Ping(context.Background()); err != nil {
			fmt.Printf("⚠️  LM Studio check failed: %v\n", err)
			fmt.Println("Make sure LM Studio has started a local server on port 1234.")
			fmt.Print("Switch anyway? (y/N): ")
			if scanner.Scan() && strings.EqualFold(strings.TrimSpace(scanner.Text()), "y") {
				fmt.Printf("✅ Switched to: %s\n", target.DisplayName())
				return target
			}
			return current
		}
	}

	fmt.Printf("✅ Switched AI backend to: %s\n", target.DisplayName())
	return target
}

func selectOrMakePersonality(scanner *bufio.Scanner, list []*Personality, current AIBackend, gemini *GeminiBackend, lm *LMStudioBackend) (*Personality, []*Personality, AIBackend) {
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
		fmt.Printf(" [m] Switch AI Backend (Active: %s)\n", current.DisplayName())
		fmt.Print("\nEnter choice: ")

		if !scanner.Scan() {
			return nil, list, current
		}
		choiceStr := strings.TrimSpace(scanner.Text())

		if strings.EqualFold(choiceStr, "m") || strings.EqualFold(choiceStr, "model") || strings.EqualFold(choiceStr, "/model") {
			current = toggleOrSelectBackend(scanner, current, gemini, lm)
			continue
		}

		choice, err := strconv.Atoi(choiceStr)
		if err != nil || choice < 1 || choice > len(list)+1 {
			fmt.Println("Invalid choice, please try again.")
			continue
		}

		if choice == len(list)+1 {
			// Create new
			fmt.Print("\nEnter Personality Name (e.g. Daisy): ")
			if !scanner.Scan() {
				return nil, list, current
			}
			name := strings.TrimSpace(scanner.Text())
			if name == "" {
				name = "Custom Persona"
			}

			fmt.Print("Enter a short tagline/descriptor: ")
			if !scanner.Scan() {
				return nil, list, current
			}
			tagline := strings.TrimSpace(scanner.Text())

			fmt.Println("Enter character instructions (who are they? how do they talk?): ")
			if !scanner.Scan() {
				return nil, list, current
			}
			instructions := strings.TrimSpace(scanner.Text())

			fmt.Print("Enter ElevenLabs Voice ID (press Enter for default): ")
			if !scanner.Scan() {
				return nil, list, current
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
			return newP, list, current
		}

		return list[choice-1], list, current
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func main() {
	headless := flag.Bool("headless", false, "only run the plant server (sensor bridge + HTTP API), without the chat")
	// -local/-lmstudio/-gemini are read from os.Args below; declared here so flag.Parse() doesn't reject them
	flag.Bool("local", false, "start on the local LM Studio backend")
	flag.Bool("lmstudio", false, "same as -local")
	flag.Bool("gemini", false, "start on the Gemini backend")
	flag.Parse()
	serverErr := startPlantServer() // see httpapi.go
	if *headless {
		log.Fatal(<-serverErr) // the server only stops on failure; systemd restarts it
	}

	// Check for command line flags: e.g. -local or --local
	forceLocal := false
	forceGemini := false
	for _, arg := range os.Args[1:] {
		if strings.EqualFold(arg, "-local") || strings.EqualFold(arg, "--local") ||
			strings.EqualFold(arg, "-lmstudio") || strings.EqualFold(arg, "--lmstudio") {
			forceLocal = true
		}
		if strings.EqualFold(arg, "-gemini") || strings.EqualFold(arg, "--gemini") {
			forceGemini = true
		}
	}

	geminiKey := getAPIKey()
	modelName := os.Getenv("GEMINI_MODEL")
	if modelName == "" {
		modelName = "models/gemini-3.8-flash"
	}

	lmStudioURL := os.Getenv("LM_STUDIO_URL")
	if lmStudioURL == "" {
		lmStudioURL = loadKeyFromEnvFile(".env", "LM_STUDIO_URL")
		if lmStudioURL == "" {
			lmStudioURL = "http://127.0.0.1:1234"
		}
	}

	lmStudioModel := os.Getenv("LM_STUDIO_MODEL")
	if lmStudioModel == "" {
		lmStudioModel = loadKeyFromEnvFile(".env", "LM_STUDIO_MODEL")
		if lmStudioModel == "" {
			lmStudioModel = "google/gemma-4-e4b"
		}
	}

	// Initialize LM Studio backend
	lmStudioBackend := NewLMStudioBackend(lmStudioURL, lmStudioModel)

	// Initialize Gemini backend if key is present
	var geminiBackend *GeminiBackend
	if geminiKey != "" {
		gb, err := NewGeminiBackend(geminiKey, modelName)
		if err != nil {
			fmt.Printf("⚠️  Gemini client initialization warning: %v\n", err)
		} else {
			geminiBackend = gb
		}
	}

	// Determine starting AI backend
	providerEnv := strings.ToLower(os.Getenv("AI_PROVIDER"))
	if providerEnv == "" {
		providerEnv = strings.ToLower(os.Getenv("AI_BACKEND"))
	}
	if providerEnv == "" {
		providerEnv = strings.ToLower(loadKeyFromEnvFile(".env", "AI_PROVIDER", "AI_BACKEND"))
	}

	var activeBackend AIBackend
	if forceLocal || providerEnv == "local" || providerEnv == "lmstudio" {
		activeBackend = lmStudioBackend
	} else if forceGemini || providerEnv == "gemini" {
		if geminiBackend != nil {
			activeBackend = geminiBackend
		} else {
			fmt.Println("⚠️  Gemini provider requested, but GEMINI_API_KEY is missing. Falling back to local LM Studio.")
			activeBackend = lmStudioBackend
		}
	} else {
		// Default behavior: use Gemini if key configured, otherwise use LM Studio
		if geminiBackend != nil {
			activeBackend = geminiBackend
		} else {
			activeBackend = lmStudioBackend
		}
	}

	// Verify at least one backend is available
	if geminiBackend == nil {
		if err := lmStudioBackend.Ping(context.Background()); err != nil {
			fmt.Println("Error: No AI backend available.")
			fmt.Println("1. To use Gemini: Add GEMINI_API_KEY to your .env or environment.")
			fmt.Printf("2. To use Local LM Studio: Start LM Studio local server on %s (error: %v).\n", lmStudioURL, err)
			os.Exit(1)
		}
		fmt.Printf("ℹ️  Running in Local AI mode (LM Studio @ %s)\n", lmStudioURL)
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
			fmt.Println("🎙️  ElevenLabs Voice Audio: Enabled")
		}
	} else {
		fmt.Println("🎙️  ElevenLabs Voice Audio: Disabled (no ELEVENLABS_API_KEY found)")
	}

	fmt.Printf("🤖 Active AI Backend: %s\n", activeBackend.DisplayName())

	ctx := context.Background()

	personalities, err := LoadPersonalities()
	if err != nil {
		log.Fatalf("Failed to load personalities: %v", err)
	}

	scanner := bufio.NewScanner(os.Stdin)

	for {
		var persona *Personality
		var updatedList []*Personality
		persona, updatedList, activeBackend = selectOrMakePersonality(scanner, personalities, activeBackend, geminiBackend, lmStudioBackend)
		if persona == nil {
			break
		}
		personalities = updatedList

		voiceStatus := "🔇 Disabled (no ELEVENLABS_API_KEY)"
		if ttsManager != nil {
			if ttsEnabled {
				voiceStatus = fmt.Sprintf("🔊 Enabled (%s)", ResolveVoiceID(persona)[:min(8, len(ResolveVoiceID(persona)))])
			} else {
				voiceStatus = "🔇 Muted"
			}
		}

		fmt.Printf("\n--- Now chatting with %s ---\n", persona.Name)
		fmt.Printf("AI Backend:  %s\n", activeBackend.DisplayName())
		fmt.Printf("Audio Voice: %s\n", voiceStatus)
		fmt.Println("Commands:")
		fmt.Println("  /memory       - View current memories for this persona")
		fmt.Println("  /clear-memory - Erase memory for this persona")
		fmt.Println("  /model        - Switch AI model/backend (Gemini <-> Local LM Studio)")
		fmt.Println("  /test-voice   - Play a test audio phrase in this persona's voice")
		fmt.Println("  /toggle-voice - Toggle audio voice output on/off")
		fmt.Println("  /switch       - Save memory and switch to another personality")
		fmt.Println("  exit or quit  - Save memory and exit program")
		fmt.Println("----------------------------------------\n")

		var sessionTurns []string
		var history []ChatMessage
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

			if strings.EqualFold(input, "/model") || strings.EqualFold(input, "/backend") {
				if activeBackend.Type() == BackendGemini {
					if err := lmStudioBackend.Ping(ctx); err != nil {
						fmt.Printf("\n⚠️  [Cannot connect to LM Studio at %s: %v]\n", lmStudioBackend.baseURL, err)
						fmt.Println("Make sure LM Studio local server is running on port 1234. Staying on Gemini.\n")
						continue
					}
					activeBackend = lmStudioBackend
					fmt.Printf("\n🔄 [Switched AI backend to: %s]\n\n", activeBackend.DisplayName())
				} else {
					if geminiBackend == nil {
						fmt.Println("\n⚠️  [Gemini is unavailable: GEMINI_API_KEY was not found. Staying on LM Studio]\n")
						continue
					}
					activeBackend = geminiBackend
					fmt.Printf("\n🔄 [Switched AI backend to: %s]\n\n", activeBackend.DisplayName())
				}
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

			replyStr, sendErr := activeBackend.Chat(ctx, persona, history, input)
			if sendErr != nil {
				fmt.Printf("\nError generating response: %v\n\n", sendErr)
				continue
			}

			fmt.Println(replyStr)

			// Record history for multi-turn conversation
			history = append(history, ChatMessage{Role: "user", Content: input})
			history = append(history, ChatMessage{Role: "assistant", Content: replyStr})
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
			fmt.Printf("\n[Saving and updating memories for %s using %s...]\n", persona.Name, activeBackend.DisplayName())
			if err := activeBackend.UpdateMemory(ctx, persona, sessionTurns); err != nil {
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
