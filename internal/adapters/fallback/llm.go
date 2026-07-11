// Package fallback provides a composite LLM provider that tries multiple
// backends in sequence. If the primary provider fails (error, timeout,
// rate limit), it falls through to the next provider in the chain.
package fallback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aparna/opscore/internal/providers"
)

// Config defines the fallback chain for LLM providers.
type Config struct {
	// Providers are tried in order. The first successful response wins.
	Providers []providers.LLMProvider
	// Timeout for each individual provider attempt.
	Timeout time.Duration
	// LogProviderName when true, logs which provider was used in structured logs.
	LogProviderName bool
}

// Adapter implements providers.LLMProvider by trying multiple backends.
type Adapter struct {
	config Config
}

// NewAdapter creates a fallback LLM adapter.
func NewAdapter(config Config) *Adapter {
	if config.Timeout == 0 {
		config.Timeout = 120 * time.Second
	}
	return &Adapter{config: config}
}

func (a *Adapter) ExtractFields(ctx context.Context, text string, schema any) (json.RawMessage, float64, error) {
	result, confidence, err := a.tryAllRaw(ctx, func(attemptCtx context.Context, p providers.LLMProvider) (any, float64, error) {
		return p.ExtractFields(attemptCtx, text, schema)
	})
	if err != nil {
		return nil, 0, err
	}
	raw, _ := result.(json.RawMessage)
	return raw, confidence, nil
}

func (a *Adapter) Reason(ctx context.Context, prompt string) (string, error) {
	result, _, err := a.tryAllRaw(ctx, func(attemptCtx context.Context, p providers.LLMProvider) (any, float64, error) {
		r, e := p.Reason(attemptCtx, prompt)
		return r, 0, e
	})
	if err != nil {
		return "", err
	}
	s, _ := result.(string)
	return s, nil
}

func (a *Adapter) Chat(ctx context.Context, messages []providers.ChatMessage) (string, *json.RawMessage, error) {
	result, _, err := a.tryAllRaw(ctx, func(attemptCtx context.Context, p providers.LLMProvider) (any, float64, error) {
		r, reasoning, e := p.Chat(attemptCtx, messages)
		return chatResult{content: r, reasoning: reasoning}, 0, e
	})
	if err != nil {
		return "", nil, err
	}
	cr, _ := result.(chatResult)
	return cr.content, cr.reasoning, nil
}

// chatResult pairs content with optional reasoning details for the fallback chain.
type chatResult struct {
	content   string
	reasoning *json.RawMessage
}

// tryAllRaw tries the operation against each provider in order.
// Uses any instead of generics because Go 1.25 does not support generic methods.
func (a *Adapter) tryAllRaw(ctx context.Context, fn func(context.Context, providers.LLMProvider) (any, float64, error)) (any, float64, error) {
	var lastErr error

	if len(a.config.Providers) == 0 {
		return nil, 0, ErrNoProvidersConfigured
	}

	for i, p := range a.config.Providers {

		slog.Debug("Fallback: trying provider",
			"index", i,
			"provider_type", fmt.Sprintf("%T", p),
		)

		attemptCtx, cancel := context.WithTimeout(ctx, a.config.Timeout)
		result, confidence, err := fn(attemptCtx, p)
		cancel()

		if err == nil {
			if a.config.LogProviderName {
				slog.Info("Fallback: provider succeeded",
					"index", i,
					"provider_type", fmt.Sprintf("%T", p),
					"confidence", confidence,
				)
			}
			return result, confidence, nil
		}

		lastErr = err
		slog.Warn("Fallback: provider failed, trying next",
			"index", i,
			"provider_type", fmt.Sprintf("%T", p),
			"err", err,
		)
	}

	return nil, 0, lastErr
}

// Compile-time interface check.
var _ providers.LLMProvider = (*Adapter)(nil)

// ErrNoProvidersConfigured is returned when the fallback chain is empty.
var ErrNoProvidersConfigured = errors.New("fallback: no LLM providers configured")