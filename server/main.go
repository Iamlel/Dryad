// Dryad's server. The board runs it with -headless, which starts only the
// sensor bridge and the HTTP API. Without that flag you also get a terminal
// chat with the plants, typed or spoken (on a Mac).
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

	"dryad/ai"
)

// toggleOrSelectBackend switches between Gemini and a local LM Studio model.
func toggleOrSelectBackend(scanner *bufio.Scanner, current ai.AIBackend, gemini *ai.GeminiBackend, lm *ai.LMStudioBackend) ai.AIBackend {
	fmt.Println("\n----------------------------------------")
	fmt.Println(" Switch AI Backend:")
	fmt.Println("----------------------------------------")
	if gemini != nil {
		fmt.Printf("  [1] Google Gemini (%s)\n", gemini.ModelName())
	} else {
		fmt.Println("  [1] Google Gemini (Unavailable: GEMINI_API_KEY not set)")
	}
	fmt.Printf("  [2] Local LM Studio (%s @ %s)\n", lm.ModelName(), lm.BaseURL)
	fmt.Print("\nEnter choice [1 or 2, or press Enter to toggle]: ")

	if !scanner.Scan() {
		return current
	}
	ans := strings.TrimSpace(scanner.Text())

	var target ai.AIBackend
	switch ans {
	case "1":
		if gemini == nil {
			fmt.Println("Gemini is unavailable: GEMINI_API_KEY is not configured.")
			return current
		}
		target = gemini
	case "2":
		target = lm
	case "":
		if current.Type() == ai.BackendGemini {
			target = lm
		} else {
			if gemini == nil {
				fmt.Println("Cannot switch to Gemini: GEMINI_API_KEY is not configured.")
				return current
			}
			target = gemini
		}
	default:
		fmt.Println("Invalid choice. Keeping current backend.")
		return current
	}

	if target.Type() == ai.BackendLMStudio {
		fmt.Printf("Pinging LM Studio at %s...\n", lm.BaseURL)
		if err := lm.Ping(context.Background()); err != nil {
			fmt.Printf("LM Studio check failed: %v\n", err)
			fmt.Println("Make sure LM Studio has started a local server on port 1234.")
			fmt.Print("Switch anyway? (y/N): ")
			if scanner.Scan() && strings.EqualFold(strings.TrimSpace(scanner.Text()), "y") {
				fmt.Printf("Switched to: %s\n", target.DisplayName())
				return target
			}
			return current
		}
	}

	fmt.Printf("Switched AI backend to: %s\n", target.DisplayName())
	return target
}

// selectOrMakePersonality is the start menu. New personalities are saved to
// personalities.json.
func selectOrMakePersonality(scanner *bufio.Scanner, list []*ai.Personality, current ai.AIBackend, gemini *ai.GeminiBackend, lm *ai.LMStudioBackend) (*ai.Personality, []*ai.Personality, ai.AIBackend) {
	for {
		fmt.Println("\n========================================")
		fmt.Println(" Choose a Personality to Chat With:")
		fmt.Println("========================================")
		for i, p := range list {
			hasMem := ""
			if strings.TrimSpace(p.Memory) != "" {
				hasMem = " [Has memories]"
			}
			voiceNote := ""
			if p.VoiceID != "" {
				voiceNote = fmt.Sprintf(" [Voice: %s]", p.VoiceID[:min(8, len(p.VoiceID))])
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

			newP := &ai.Personality{
				ID:          strings.ToLower(strings.ReplaceAll(name, " ", "_")),
				Name:        name,
				Tagline:     tagline,
				Instruction: instructions,
				Memory:      "",
				VoiceID:     voiceID,
			}
			list = append(list, newP)
			_ = ai.SavePersonalities(list)
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
	voiceFlag := flag.Bool("voice", false, "start in voice conversation mode using microphone and STT")
	flag.Bool("stt", false, "same as -voice")
	flag.Bool("local", false, "start on the local LM Studio backend")
	flag.Bool("lmstudio", false, "same as -local")
	flag.Bool("gemini", false, "start on the Gemini backend")
	flag.Parse()
	serverErr := startPlantServer() // see httpapi.go
	if *headless {
		log.Fatal(<-serverErr) // the server only stops on failure; keep-running.sh restarts it
	}

	// The rest is the terminal chat. Its flags are also matched by hand, so any
	// capitalization works.
	forceVoice := *voiceFlag
	forceLocal := false
	forceGemini := false
	for _, arg := range os.Args[1:] {
		if strings.EqualFold(arg, "-voice") || strings.EqualFold(arg, "--voice") ||
			strings.EqualFold(arg, "-stt") || strings.EqualFold(arg, "--stt") {
			forceVoice = true
		}
		if strings.EqualFold(arg, "-local") || strings.EqualFold(arg, "--local") ||
			strings.EqualFold(arg, "-lmstudio") || strings.EqualFold(arg, "--lmstudio") {
			forceLocal = true
		}
		if strings.EqualFold(arg, "-gemini") || strings.EqualFold(arg, "--gemini") {
			forceGemini = true
		}
	}

	// GEMINI_MODEL, LM_STUDIO_URL and LM_STUDIO_MODEL override the defaults.
	geminiKey := ai.GetAPIKey()
	modelName := os.Getenv("GEMINI_MODEL")
	if modelName == "" {
		modelName = "models/gemini-3.8-flash"
	}

	lmStudioURL := os.Getenv("LM_STUDIO_URL")
	if lmStudioURL == "" {
		lmStudioURL = ai.LoadKeyFromEnvFile(".env", "LM_STUDIO_URL")
		if lmStudioURL == "" {
			lmStudioURL = "http://127.0.0.1:1234"
		}
	}

	lmStudioModel := os.Getenv("LM_STUDIO_MODEL")
	if lmStudioModel == "" {
		lmStudioModel = ai.LoadKeyFromEnvFile(".env", "LM_STUDIO_MODEL")
		if lmStudioModel == "" {
			lmStudioModel = "google/gemma-4-e4b"
		}
	}

	lmStudioBackend := ai.NewLMStudioBackend(lmStudioURL, lmStudioModel)

	var geminiBackend *ai.GeminiBackend
	if geminiKey != "" {
		gb, err := ai.NewGeminiBackend(geminiKey, modelName)
		if err != nil {
			fmt.Printf("Gemini client initialization warning: %v\n", err)
		} else {
			geminiBackend = gb
		}
	}

	// The flags win, then AI_PROVIDER, then Gemini if there's a key.
	providerEnv := strings.ToLower(os.Getenv("AI_PROVIDER"))
	if providerEnv == "" {
		providerEnv = strings.ToLower(os.Getenv("AI_BACKEND"))
	}
	if providerEnv == "" {
		providerEnv = strings.ToLower(ai.LoadKeyFromEnvFile(".env", "AI_PROVIDER", "AI_BACKEND"))
	}

	var activeBackend ai.AIBackend
	if forceLocal || providerEnv == "local" || providerEnv == "lmstudio" {
		activeBackend = lmStudioBackend
	} else if forceGemini || providerEnv == "gemini" {
		if geminiBackend != nil {
			activeBackend = geminiBackend
		} else {
			fmt.Println("Gemini provider requested, but GEMINI_API_KEY is missing. Falling back to local LM Studio.")
			activeBackend = lmStudioBackend
		}
	} else {
		if geminiBackend != nil {
			activeBackend = geminiBackend
		} else {
			activeBackend = lmStudioBackend
		}
	}

	if geminiBackend == nil {
		if err := lmStudioBackend.Ping(context.Background()); err != nil {
			fmt.Println("Error: No AI backend available.")
			fmt.Println("1. To use Gemini: Add GEMINI_API_KEY to your .env or environment.")
			fmt.Printf("2. To use Local LM Studio: Start LM Studio local server on %s (error: %v).\n", lmStudioURL, err)
			os.Exit(1)
		}
		fmt.Printf("Running in Local AI mode (LM Studio @ %s)\n", lmStudioURL)
	}

	// ElevenLabs does the plant's voice and the speech to text.
	elevenLabsKey := ai.GetElevenLabsKey()
	var ttsManager *ai.TTSManager
	ttsEnabled := false
	var sttManager *ai.STTManager

	if elevenLabsKey != "" {
		tm, err := ai.NewTTSManager(elevenLabsKey)
		if err != nil {
			fmt.Printf("ElevenLabs TTS warning: %v\n", err)
		} else {
			ttsManager = tm
			ttsEnabled = true
			fmt.Println("ElevenLabs Voice Audio: Enabled")
		}

		sm, err := ai.NewSTTManager(elevenLabsKey)
		if err != nil {
			fmt.Printf("ElevenLabs STT warning: %v\n", err)
		} else {
			sttManager = sm
			fmt.Println("ElevenLabs Speech to Text: Ready")
		}
	} else {
		fmt.Println("ElevenLabs Audio: Disabled (no ELEVENLABS_API_KEY found)")
	}

	fmt.Printf("Active AI Backend: %s\n", activeBackend.DisplayName())

	ctx := context.Background()

	personalities, err := ai.LoadPersonalities()
	if err != nil {
		log.Fatalf("Failed to load personalities: %v", err)
	}

	scanner := bufio.NewScanner(os.Stdin)

	// One chat session per personality: /switch goes back to the menu, exit quits.
	for {
		var persona *ai.Personality
		var updatedList []*ai.Personality
		persona, updatedList, activeBackend = selectOrMakePersonality(scanner, personalities, activeBackend, geminiBackend, lmStudioBackend)
		if persona == nil {
			break
		}
		personalities = updatedList

		voiceMode := forceVoice
		voiceStatus := "Disabled (no ELEVENLABS_API_KEY)"
		if ttsManager != nil {
			if ttsEnabled {
				voiceStatus = fmt.Sprintf("Enabled (%s)", ai.ResolveVoiceID(persona)[:min(8, len(ai.ResolveVoiceID(persona)))])
			} else {
				voiceStatus = "Muted"
			}
		}

		sttStatus := "Disabled (no ELEVENLABS_API_KEY)"
		if sttManager != nil {
			if voiceMode {
				sttStatus = "Active (listening to microphone)"
			} else {
				sttStatus = "Ready (type /voice to activate)"
			}
		}

		fmt.Printf("\nNow chatting with %s\n", persona.Name)
		fmt.Printf("AI Backend:   %s\n", activeBackend.DisplayName())
		fmt.Printf("Audio Voice:  %s\n", voiceStatus)
		fmt.Printf("Speech Input: %s\n", sttStatus)
		fmt.Println("Commands:")
		fmt.Println("  /voice        Toggle speech to text microphone mode")
		fmt.Println("  /model        Switch AI backend (Gemini or Local LM Studio)")
		fmt.Println("  /toggle-voice Toggle audio speaker voice output on or off")
		fmt.Println("  /test-voice   Play a test audio phrase in this persona voice")
		fmt.Println("  /memory       View current memories for this persona")
		fmt.Println("  /clear-memory Erase memory for this persona")
		fmt.Println("  /switch       Save memory and switch to another personality")
		fmt.Println("  exit or quit  Save memory and exit program")
		fmt.Println("----------------------------------------")
		fmt.Println()

		// history goes to the AI each turn; sessionTurns becomes the memory summary.
		var sessionTurns []string
		var history []ai.ChatMessage
		exiting := false

		for {
			var input string

			// Voice mode listens until you stop talking, and falls back to typing on errors.
			if voiceMode && sttManager != nil {
				spoken, err := ai.RecordSpeechTurn(ctx, sttManager)
				if err != nil {
					fmt.Println()
					fmt.Printf("Speech recognition note: %v\n", err)
					fmt.Println("Switched to keyboard text mode. Type your message below:")
					fmt.Println()
					voiceMode = false
					fmt.Printf("You: ")
					if !scanner.Scan() {
						exiting = true
						break
					}
					input = strings.TrimSpace(scanner.Text())
				} else {
					input = spoken
				}
			} else {
				fmt.Printf("You: ")
				if !scanner.Scan() {
					exiting = true
					break
				}
				input = strings.TrimSpace(scanner.Text())
			}

			if input == "" {
				continue
			}

			if strings.EqualFold(input, "exit") || strings.EqualFold(input, "quit") {
				exiting = true
				break
			}

			if strings.EqualFold(input, "/voice") || strings.EqualFold(input, "/mic") || strings.EqualFold(input, "/talk") {
				if sttManager == nil {
					fmt.Println("ElevenLabs key not found. Add ELEVENLABS_API_KEY to your .env to enable speech to text.")
					fmt.Println()
					continue
				}
				voiceMode = !voiceMode
				if voiceMode {
					fmt.Println("Speech to text mode enabled. Speak into your microphone.")
					fmt.Println()
				} else {
					fmt.Println("Keyboard mode enabled. Type your messages.")
					fmt.Println()
				}
				continue
			}

			if strings.EqualFold(input, "/switch") {
				break
			}

			if strings.EqualFold(input, "/memory") {
				fmt.Printf("\n[Stored Memory for %s]:\n", persona.Name)
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
				_ = ai.SavePersonalities(personalities)
				fmt.Printf("[Memory cleared for %s]\n\n", persona.Name)
				continue
			}

			if strings.EqualFold(input, "/model") || strings.EqualFold(input, "/backend") {
				if activeBackend.Type() == ai.BackendGemini {
					if err := lmStudioBackend.Ping(ctx); err != nil {
						fmt.Printf("\n[Cannot connect to LM Studio at %s: %v]\n", lmStudioBackend.BaseURL, err)
						fmt.Println("Make sure LM Studio local server is running on port 1234. Staying on Gemini.")
						fmt.Println()
						continue
					}
					activeBackend = lmStudioBackend
					fmt.Printf("\n[Switched AI backend to: %s]\n\n", activeBackend.DisplayName())
				} else {
					if geminiBackend == nil {
						fmt.Println("\n[Gemini is unavailable: GEMINI_API_KEY was not found. Staying on LM Studio]")
						fmt.Println()
						continue
					}
					activeBackend = geminiBackend
					fmt.Printf("\n[Switched AI backend to: %s]\n\n", activeBackend.DisplayName())
				}
				continue
			}

			if strings.EqualFold(input, "/toggle-voice") {
				if ttsManager == nil {
					fmt.Println("\n[ElevenLabs key not found. Add ELEVENLABS_API_KEY to your .env to enable audio]")
					fmt.Println()
				} else {
					ttsEnabled = !ttsEnabled
					if ttsEnabled {
						fmt.Println("\n[Voice output turned ON]")
						fmt.Println()
					} else {
						fmt.Println("\n[Voice output turned OFF]")
						fmt.Println()
					}
				}
				continue
			}

			if strings.EqualFold(input, "/test-voice") {
				if ttsManager == nil {
					fmt.Println("\n[ElevenLabs key not found. Add ELEVENLABS_API_KEY to your .env to enable audio]")
					fmt.Println()
				} else {
					testPhrase := fmt.Sprintf("Hello! I am %s. %s", persona.Name, persona.Tagline)
					fmt.Printf("\n[Playing sample voice for %s...]\n", persona.Name)
					if err := ttsManager.Speak(ctx, persona, testPhrase); err != nil {
						fmt.Printf("[Voice test error: %v]\n\n", err)
					} else {
						fmt.Println("[Audio playback finished]")
						fmt.Println()
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

			history = append(history, ai.ChatMessage{Role: "user", Content: input})
			history = append(history, ai.ChatMessage{Role: "assistant", Content: replyStr})
			sessionTurns = append(sessionTurns, fmt.Sprintf("%s: %s", persona.Name, replyStr))

			if ttsManager != nil && ttsEnabled && replyStr != "" {
				fmt.Printf("[Playing %s's voice...]\n", persona.Name)
				if err := ttsManager.Speak(ctx, persona, replyStr); err != nil {
					fmt.Printf("[Audio playback error: %v]\n", err)
				}
			}
			fmt.Println()
		}

		// After a session, the AI summarizes it into the personality's memory.
		if len(sessionTurns) > 0 {
			fmt.Printf("\n[Saving and updating memories for %s using %s...]\n", persona.Name, activeBackend.DisplayName())
			if err := activeBackend.UpdateMemory(ctx, persona, sessionTurns); err != nil {
				fmt.Printf("Warning: Could not summarize memories: %v\n", err)
			} else {
				if err := ai.SavePersonalities(personalities); err != nil {
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
