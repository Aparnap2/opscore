package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/aparna/opscore/internal/providers"
)

// Config holds configuration for the OpenRouter LLM adapter.
type Config struct {
	APIKey           string
	BaseURL          string
	Model            string
	ReasoningEnabled bool
	MaxTokens        int
	Temperature      float64
}

// Adapter implements providers.LLMProvider using OpenRouter's OpenAI-compatible API.
type Adapter struct {
	client *http.Client
	config Config
}

// NewAdapter creates a new OpenRouter adapter.
func NewAdapter(config Config) *Adapter {
	if config.BaseURL == "" {
		config.BaseURL = "https://openrouter.ai/api/v1"
	}
	if config.Model == "" {
		config.Model = "tencent/hy3:free"
	}

	return &Adapter{
		client: &http.Client{Timeout: 120 * time.Second},
		config: config,
	}
}

// chatCompletionRequest is the request body for the chat completion endpoint.
type chatCompletionRequest struct {
	Model       string                  `json:"model"`
	Messages    []providers.ChatMessage `json:"messages"`
	ExtraBody   map[string]any          `json:"extra_body,omitempty"`
	Stream      bool                    `json:"stream,omitempty"`
	MaxTokens   int                     `json:"max_tokens,omitempty"`
	Temperature float64                 `json:"temperature,omitempty"`
}

// choiceMessage is the per-choice message in the chat completion response,
// including optional reasoning_details for supported models.
type choiceMessage struct {
	Content          string           `json:"content"`
	ReasoningDetails *json.RawMessage `json:"reasoning_details,omitempty"`
}

// chatCompletionResponse is the response body from the chat completion endpoint.
type chatCompletionResponse struct {
	Choices []struct {
		Message choiceMessage `json:"message"`
	} `json:"choices"`
}

// doRequest sends a chat completion request and returns the response content
// and optional reasoning details.
func (a *Adapter) doRequest(ctx context.Context, messages []providers.ChatMessage) (string, *json.RawMessage, error) {
	req := chatCompletionRequest{
		Model:    a.config.Model,
		Messages: messages,
	}

	// Populate extra_body when reasoning is enabled.
	if a.config.ReasoningEnabled {
		req.ExtraBody = map[string]any{
			"reasoning": map[string]any{
				"enabled": true,
			},
		}
	}

	// Populate optional fields when non-zero.
	if a.config.MaxTokens > 0 {
		req.MaxTokens = a.config.MaxTokens
	}
	if a.config.Temperature > 0 {
		req.Temperature = a.config.Temperature
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", nil, fmt.Errorf("marshaling request: %w", err)
	}

	url := fmt.Sprintf("%s/chat/completions", a.config.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", nil, fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.config.APIKey)
	httpReq.Header.Set("HTTP-Referer", "https://github.com/Aparnap2/opscore")
	httpReq.Header.Set("X-Title", "OpsCore")
	httpReq.Header.Set("Accept", "application/json")

	slog.Debug("OpenRouter LLM call",
		"model", a.config.Model,
		"messages", len(messages),
		"reasoning_enabled", a.config.ReasoningEnabled,
		"max_tokens", a.config.MaxTokens,
	)

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return "", nil, fmt.Errorf("calling OpenRouter LLM: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", nil, fmt.Errorf("OpenRouter API error: %d - %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", nil, fmt.Errorf("decoding OpenRouter response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", nil, fmt.Errorf("no response from OpenRouter LLM")
	}

	msg := chatResp.Choices[0].Message
	return msg.Content, msg.ReasoningDetails, nil
}

// ExtractFields uses the LLM to extract structured fields from text as JSON.
// Returns the parsed JSON with confidence 1.0 if successfully parsed as JSON,
// or confidence 0.0 with the raw content wrapped as json.RawMessage otherwise.
func (a *Adapter) ExtractFields(ctx context.Context, text string, schema any) (json.RawMessage, float64, error) {
	prompt := fmt.Sprintf("Extract structured data from the following text:\n\n%s", text)
	if schema != nil {
		schemaJSON, err := json.Marshal(schema)
		if err == nil {
			prompt = fmt.Sprintf("%s\n\nRespond ONLY with valid JSON matching this schema:\n%s", prompt, string(schemaJSON))
		}
	}

	messages := []providers.ChatMessage{
		{
			Role:    "system",
			Content: "You are a data extraction assistant. Respond only with valid JSON matching the requested schema. Do not include any explanatory text before or after the JSON.",
		},
		{
			Role:    "user",
			Content: prompt,
		},
	}

	content, _, err := a.doRequest(ctx, messages)
	if err != nil {
		return nil, 0, fmt.Errorf("extracting fields: %w", err)
	}

	var result json.RawMessage
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		slog.Warn("OpenRouter response was not valid JSON, returning raw content",
			"error", err,
			"content_length", len(content),
		)
		result = json.RawMessage(content)
		return result, 0.0, nil
	}

	return result, 1.0, nil
}

// Reason uses the LLM to reason about a prompt.
func (a *Adapter) Reason(ctx context.Context, prompt string) (string, error) {
	messages := []providers.ChatMessage{
		{
			Role:    "user",
			Content: prompt,
		},
	}

	content, _, err := a.doRequest(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("reasoning: %w", err)
	}

	return content, nil
}

// Chat sends a chat completion request with the given messages.
// Returns the response content and optional reasoning_details (nil if not supported by model).
func (a *Adapter) Chat(ctx context.Context, messages []providers.ChatMessage) (string, *json.RawMessage, error) {
	content, reasoning, err := a.doRequest(ctx, messages)
	if err != nil {
		return "", nil, fmt.Errorf("chat: %w", err)
	}

	return content, reasoning, nil
}

// Compile-time interface check.
var _ providers.LLMProvider = (*Adapter)(nil)
