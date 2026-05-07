package sarvam

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aparna/opscore/internal/providers"
)

type LLMConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

type LLMAdapter struct {
	client *http.Client
	config LLMConfig
}

func NewLLMAdapter(config LLMConfig) *LLMAdapter {
	if config.BaseURL == "" {
		config.BaseURL = "https://api.sarvam.ai"
	}
	if config.Model == "" {
		config.Model = "sarvam-m"
	}

	return &LLMAdapter{
		client: &http.Client{Timeout: 120 * time.Second},
		config: config,
	}
}

// ExtractFields uses the LLM to extract structured fields from text
func (l *LLMAdapter) ExtractFields(ctx context.Context, text string, schema any) (json.RawMessage, float64, error) {
	type request struct {
		Schema   any                     `json:"schema,omitempty"`
		Model    string                  `json:"model"`
		Messages []providers.ChatMessage `json:"messages"`
	}

	type response struct {
		Content      string `json:"content"`
		FinishReason string `json:"finish_reason"`
		Usage        struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	// Build prompt with schema
	prompt := fmt.Sprintf("Extract structured data from the following text:\n\n%s", text)
	if schema != nil {
		schemaJSON, _ := json.Marshal(schema)
		prompt = fmt.Sprintf("%s\n\nUse this schema: %s", prompt, string(schemaJSON))
	}

	req := request{
		Messages: []providers.ChatMessage{
			{Role: "user", Content: prompt},
		},
		Schema: schema,
		Model:  l.config.Model,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, 0, fmt.Errorf("marshaling request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/chat/completions", l.config.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("api-subscription-key", l.config.APIKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := l.client.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("calling LLM: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("API error: %d - %s", resp.StatusCode, string(respBody))
	}

	var chatResp response
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, 0, fmt.Errorf("decoding response: %w", err)
	}

	var result json.RawMessage
	if err := json.Unmarshal([]byte(chatResp.Content), &result); err != nil {
		// Try wrapping as raw JSON
		result = json.RawMessage(chatResp.Content)
	}

	costPerToken := 0.0001 // INR per token estimate
	cost := float64(chatResp.Usage.TotalTokens) * costPerToken

	return result, cost, nil
}

// Reason uses the LLM to reason about a prompt
func (l *LLMAdapter) Reason(ctx context.Context, prompt string) (string, error) {
	type request struct {
		Model    string                  `json:"model"`
		Messages []providers.ChatMessage `json:"messages"`
	}

	type response struct {
		Choices []struct {
			Message providers.ChatMessage `json:"message"`
		} `json:"choices"`
	}

	req := request{
		Messages: []providers.ChatMessage{
			{Role: "user", Content: prompt},
		},
		Model: l.config.Model,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/chat/completions", l.config.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("api-subscription-key", l.config.APIKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := l.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("calling LLM: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error: %d - %s", resp.StatusCode, string(respBody))
	}

	var chatResp response
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no response from LLM")
	}

	return chatResp.Choices[0].Message.Content, nil
}

// Chat sends a chat completion request
func (l *LLMAdapter) Chat(ctx context.Context, messages []providers.ChatMessage) (string, error) {
	type request struct {
		Model    string                  `json:"model"`
		Messages []providers.ChatMessage `json:"messages"`
	}

	type response struct {
		Choices []struct {
			Message providers.ChatMessage `json:"message"`
		} `json:"choices"`
	}

	req := request{
		Messages: messages,
		Model:    l.config.Model,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/chat/completions", l.config.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("api-subscription-key", l.config.APIKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := l.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("calling LLM: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error: %d - %s", resp.StatusCode, string(respBody))
	}

	var chatResp response
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no response from LLM")
	}

	return chatResp.Choices[0].Message.Content, nil
}

var _ providers.LLMProvider = (*LLMAdapter)(nil)

// LLMAdapterMock implements LLMProvider for testing
type LLMAdapterMock struct {
	ExtractFieldsFunc func(ctx context.Context, text string, schema any) (json.RawMessage, float64, error)
	ReasonFunc        func(ctx context.Context, prompt string) (string, error)
	ChatFunc          func(ctx context.Context, messages []providers.ChatMessage) (string, error)
}

func (m *LLMAdapterMock) ExtractFields(ctx context.Context, text string, schema any) (json.RawMessage, float64, error) {
	if m.ExtractFieldsFunc != nil {
		return m.ExtractFieldsFunc(ctx, text, schema)
	}
	return json.RawMessage(`{}`), 0.0, nil
}

func (m *LLMAdapterMock) Reason(ctx context.Context, prompt string) (string, error) {
	if m.ReasonFunc != nil {
		return m.ReasonFunc(ctx, prompt)
	}
	return "mocked reasoning", nil
}

func (m *LLMAdapterMock) Chat(ctx context.Context, messages []providers.ChatMessage) (string, error) {
	if m.ChatFunc != nil {
		return m.ChatFunc(ctx, messages)
	}
	return "mocked response", nil
}

var _ providers.LLMProvider = (*LLMAdapterMock)(nil)
