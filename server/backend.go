package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

type BackendType string

const (
	BackendGemini   BackendType = "gemini"
	BackendLMStudio BackendType = "lmstudio"
)

// ChatMessage represents a single message in a multi-turn conversation.
type ChatMessage struct {
	Role    string `json:"role"` // "system", "user", "assistant"
	Content string `json:"content"`
}

// AIBackend defines the interface implemented by both cloud Gemini and local LM Studio.
type AIBackend interface {
	Type() BackendType
	DisplayName() string
	ModelName() string
	Ping(ctx context.Context) error
	Chat(ctx context.Context, persona *Personality, history []ChatMessage, userMessage string) (string, error)
	UpdateMemory(ctx context.Context, persona *Personality, sessionTurns []string) error
}

// -------------------------------------------------------------
// Gemini Backend Implementation
// -------------------------------------------------------------

type GeminiBackend struct {
	client    *genai.Client
	modelName string
}

func NewGeminiBackend(apiKey, modelName string) (*GeminiBackend, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("no Gemini API key provided")
	}
	if modelName == "" {
		modelName = "models/gemini-3.8-flash"
	}
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	return &GeminiBackend{
		client:    client,
		modelName: modelName,
	}, nil
}

func (g *GeminiBackend) Type() BackendType {
	return BackendGemini
}

func (g *GeminiBackend) DisplayName() string {
	return fmt.Sprintf("☁️  Gemini (%s)", g.modelName)
}

func (g *GeminiBackend) ModelName() string {
	return g.modelName
}

func (g *GeminiBackend) Ping(ctx context.Context) error {
	if g.client == nil {
		return fmt.Errorf("gemini client not initialized")
	}
	return nil
}

func (g *GeminiBackend) Chat(ctx context.Context, persona *Personality, history []ChatMessage, userMessage string) (string, error) {
	model := g.client.GenerativeModel(g.modelName)
	sysInstruction := BuildSystemInstruction(persona)
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(sysInstruction)},
	}

	chat := model.StartChat()
	for _, m := range history {
		if m.Role == "user" {
			chat.History = append(chat.History, &genai.Content{
				Role:  "user",
				Parts: []genai.Part{genai.Text(m.Content)},
			})
		} else if m.Role == "assistant" {
			chat.History = append(chat.History, &genai.Content{
				Role:  "model",
				Parts: []genai.Part{genai.Text(m.Content)},
			})
		}
	}

	var resp *genai.GenerateContentResponse
	var sendErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, sendErr = chat.SendMessage(ctx, genai.Text(userMessage))
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
		return "", sendErr
	}

	var sb strings.Builder
	for _, cand := range resp.Candidates {
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				sb.WriteString(fmt.Sprintf("%v", part))
			}
		}
	}
	return sb.String(), nil
}

func (g *GeminiBackend) UpdateMemory(ctx context.Context, persona *Personality, sessionTurns []string) error {
	return UpdateMemory(ctx, g.client, g.modelName, persona, sessionTurns)
}

// -------------------------------------------------------------
// LM Studio Backend Implementation (OpenAI-compatible local server)
// -------------------------------------------------------------

type LMStudioBackend struct {
	baseURL    string
	modelName  string
	httpClient *http.Client
}

func NewLMStudioBackend(baseURL, modelName string) *LMStudioBackend {
	if baseURL == "" {
		baseURL = os.Getenv("LM_STUDIO_URL")
		if baseURL == "" {
			baseURL = "http://127.0.0.1:1234"
		}
	}
	baseURL = strings.TrimRight(baseURL, "/")

	if modelName == "" {
		modelName = os.Getenv("LM_STUDIO_MODEL")
		if modelName == "" {
			modelName = "llama-3.2-3b-instruct"
		}
	}

	return &LMStudioBackend{
		baseURL:   baseURL,
		modelName: modelName,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (l *LMStudioBackend) Type() BackendType {
	return BackendLMStudio
}

func (l *LMStudioBackend) DisplayName() string {
	return fmt.Sprintf("🏠 Local LM Studio (%s @ %s)", l.modelName, l.baseURL)
}

func (l *LMStudioBackend) ModelName() string {
	return l.modelName
}

func (l *LMStudioBackend) Ping(ctx context.Context) error {
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(pingCtx, "GET", l.baseURL+"/v1/models", nil)
	if err != nil {
		return err
	}
	resp, err := l.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("unable to reach LM Studio at %s: %w", l.baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("LM Studio returned status %s", resp.Status)
	}
	return nil
}

type openAIChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
}

type openAIChatResponse struct {
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role             string `json:"role"`
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (l *LMStudioBackend) Chat(ctx context.Context, persona *Personality, history []ChatMessage, userMessage string) (string, error) {
	// Construct the full prompt messages
	var messages []ChatMessage

	// System instruction
	sysInstruction := BuildSystemInstruction(persona)
	messages = append(messages, ChatMessage{
		Role:    "system",
		Content: sysInstruction,
	})

	// Accumulated history
	messages = append(messages, history...)

	// Current user turn
	messages = append(messages, ChatMessage{
		Role:    "user",
		Content: userMessage,
	})

	reqBody := openAIChatRequest{
		Model:       l.modelName,
		Messages:    messages,
		Temperature: 0.7,
		MaxTokens:   1500, // accommodate reasoning tokens + reply
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", l.baseURL+"/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("LM Studio request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read LM Studio response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("LM Studio returned status %s: %s", resp.Status, string(body))
	}

	var chatResp openAIChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return "", fmt.Errorf("failed to parse LM Studio response: %w", err)
	}

	if chatResp.Error != nil && chatResp.Error.Message != "" {
		return "", fmt.Errorf("LM Studio error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("LM Studio returned no choices")
	}

	reply := chatResp.Choices[0].Message.Content
	reply = StripThinkingTags(reply)
	return strings.TrimSpace(reply), nil
}

func (l *LMStudioBackend) UpdateMemory(ctx context.Context, p *Personality, sessionTurns []string) error {
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

	reqBody := openAIChatRequest{
		Model: l.modelName,
		Messages: []ChatMessage{
			{Role: "user", Content: prompt},
		},
		Temperature: 0.3,
		MaxTokens:   1500,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to encode memory request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", l.baseURL+"/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("LM Studio memory request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var chatResp openAIChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return fmt.Errorf("LM Studio returned empty response for memory update")
	}

	updated := StripThinkingTags(chatResp.Choices[0].Message.Content)
	updated = strings.TrimSpace(updated)
	if updated != "" {
		p.Memory = updated
	}
	return nil
}

// StripThinkingTags removes any internal <think>...</think> tags if present in model output.
func StripThinkingTags(s string) string {
	for {
		start := strings.Index(s, "<think>")
		if start == -1 {
			break
		}
		end := strings.Index(s, "</think>")
		if end == -1 {
			s = s[:start]
			break
		}
		s = s[:start] + s[end+len("</think>"):]
	}
	return strings.TrimSpace(s)
}
