package ai

// Live tests against LM Studio and Gemini. Each skips when its backend isn't there.

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestLMStudioBackend(t *testing.T) {
	ctx := context.Background()
	lm := NewLMStudioBackend("http://127.0.0.1:1234", "google/gemma-4-e4b")
	if err := lm.Ping(ctx); err != nil {
		t.Skipf("LM Studio is not reachable: %v", err)
	}

	persona := &Personality{
		ID:          "spike",
		Name:        "Spike",
		Tagline:     "Stoic windowsill cactus",
		Instruction: "You are Spike, a tough cactus. Keep spoken responses under two sentences.",
	}

	reply, err := lm.Chat(ctx, persona, nil, "Hi Spike! Are you feeling thirsty?")
	if err != nil {
		t.Fatalf("LM Studio Chat failed: %v", err)
	}
	t.Logf("Spike (LM Studio reply): %s", reply)

	if len(reply) == 0 {
		t.Errorf("Expected non-empty reply from LM Studio")
	}
}

func TestGeminiBackend(t *testing.T) {
	key := GetAPIKey()
	if key == "" {
		t.Skip("Gemini API key not found, skipping test")
	}

	ctx := context.Background()
	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		model = "models/gemini-3.8-flash"
	}

	gemini, err := NewGeminiBackend(key, model)
	if err != nil {
		t.Fatalf("Gemini init failed: %v", err)
	}

	persona := &Personality{
		ID:          "spike",
		Name:        "Spike",
		Tagline:     "Stoic windowsill cactus",
		Instruction: "You are Spike, a tough cactus. Keep spoken responses under two sentences.",
	}

	reply, err := gemini.Chat(ctx, persona, nil, "Hi Spike! Are you feeling thirsty?")
	if err != nil {
		if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "Quota exceeded") {
			t.Skipf("Gemini quota exceeded: %v", err)
		}
		t.Fatalf("Gemini Chat failed: %v", err)
	}
	t.Logf("Spike (Gemini reply): %s", reply)

	if len(reply) == 0 {
		t.Errorf("Expected non-empty reply from Gemini")
	}
}
