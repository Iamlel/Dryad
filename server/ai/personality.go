package ai

// Personalities live in personalities.json next to the server.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/google/generative-ai-go/genai"
)

const personalitiesFile = "personalities.json"

type Personality struct {
	ID          string `json:"id"` // the same id as the plant in plants.sql
	Name        string `json:"name"`
	Tagline     string `json:"tagline"`
	Instruction string `json:"instruction"`        // who the character is and how it talks
	Memory      string `json:"memory"`             // a summary of past terminal chats, added to every prompt
	VoiceID     string `json:"voice_id,omitempty"` // the ElevenLabs voice; empty picks a default (tts.go)
}

// GetDefaultPersonalities is what LoadPersonalities writes when there's no file.
func GetDefaultPersonalities() []*Personality {
	return []*Personality{
		{
			ID:          "spike",
			Name:        "Spike",
			Tagline:     "Stoic windowsill cactus",
			Instruction: "You are Spike, a tough cactus. Keep spoken responses under two sentences. Report your soil dryness and physical firmness clearly without fluff. You prefer dry conditions and poke dry humor at Fern for being dramatic about needing water.",
			Memory:      "",
			VoiceID:     "pNInz6obpgDQGcFmaJgB",
		},
		{
			ID:          "fern",
			Name:        "Fern",
			Tagline:     "Expressive humidity loving fern",
			Instruction: "You are Fern, a sensitive houseplant. Keep spoken responses under two sentences. Clearly state when your soil is dry, your fronds droop, or you need humidity. You bounce off Spike by teasing his stubborn dryness while demanding your next drink.",
			Memory:      "",
			VoiceID:     "21m00Tcm4TlvDq8ikWAM",
		},
	}
}

// LoadPersonalities reads personalities from personalities.json or creates defaults.
func LoadPersonalities() ([]*Personality, error) {
	if _, err := os.Stat(personalitiesFile); os.IsNotExist(err) {
		defaults := GetDefaultPersonalities()
		if err := SavePersonalities(defaults); err != nil {
			return nil, err
		}
		return defaults, nil
	}

	data, err := os.ReadFile(personalitiesFile)
	if err != nil {
		return nil, err
	}

	var list []*Personality
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}

	return list, nil
}

func SavePersonalities(list []*Personality) error {
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(personalitiesFile, data, 0644)
}

// BuildSystemInstruction is the system prompt: the character, its memories and
// the rules for staying in character.
func BuildSystemInstruction(p *Personality) string {
	var memorySection string
	if strings.TrimSpace(p.Memory) == "" {
		memorySection = "(No prior memories recorded. This is your first interaction with this user.)"
	} else {
		memorySection = p.Memory
	}

	return fmt.Sprintf(`You are roleplaying as %s.

Character Profile & Guidelines:
%s

Your Accumulated Memories of Past Interactions with this User:
%s

Rules:
- Never break character. Never state you are an AI from Google unless the character itself is that.
- Naturally weave in facts, shared history, and callbacks from your memories whenever relevant.
- Retain your character's unique voice, attitude, idioms, and vocabulary.`, p.Name, p.Instruction, memorySection)
}

// UpdateMemory has Gemini merge the session into the personality's memory.
func UpdateMemory(ctx context.Context, client *genai.Client, modelName string, p *Personality, sessionTurns []string) error {
	if len(sessionTurns) == 0 {
		return nil
	}

	recentConversation := strings.Join(sessionTurns, "\n")
	currentMemory := p.Memory
	if currentMemory == "" {
		currentMemory = "(None yet)"
	}

	prompt := fmt.Sprintf(`You are an internal memory manager for the roleplay character "%s".
Below is the character's existing memory about the user and past interactions:
<existing_memory>
%s
</existing_memory>

Here is the dialogue transcript from the session that just took place:
<recent_session>
%s
</recent_session>

TASK:
Create an updated, consolidated memory file for "%s".
Guidelines:
1. Preserve important ongoing details, user preferences, names, facts, and key narrative events.
2. Integrate any new facts, topics discussed, secrets shared, or relationship developments from the recent session.
3. Keep the total memory concise, factual, and under 250 words (bullet points or a short narrative).
4. Output ONLY the updated memory content. Do not include markdown code fences, greetings, or meta commentary.`,
		p.Name, currentMemory, recentConversation, p.Name)

	model := client.GenerativeModel(modelName)
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return fmt.Errorf("failed to generate memory update: %w", err)
	}

	var sb strings.Builder
	for _, cand := range resp.Candidates {
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				sb.WriteString(fmt.Sprintf("%v", part))
			}
		}
	}

	updated := strings.TrimSpace(sb.String())
	if updated != "" {
		p.Memory = updated
	}

	return nil
}
