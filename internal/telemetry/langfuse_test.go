package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aparna/opscore/internal/providers"
)

func TestLangfuse_RecordLLMCall(t *testing.T) {
	obsCh := make(chan map[string]any, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var data map[string]any
		if err := json.Unmarshal(body, &data); err != nil {
			t.Errorf("unmarshaling body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		obsCh <- data
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	provider := NewLangfuseProvider(server.URL, "sk-secret", "pk-public")
	provider.RecordLLMCall(context.Background(), "gpt-4", 100, 1.5, 500)

	obs := <-obsCh
	if obs["type"] != "LLM" {
		t.Errorf("expected type LLM, got %v", obs["type"])
	}
	if obs["model"] != "gpt-4" {
		t.Errorf("expected model gpt-4, got %v", obs["model"])
	}
}

func TestLangfuse_StartSpan(t *testing.T) {
	traceCh := make(chan map[string]any, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var data map[string]any
		json.Unmarshal(body, &data)

		switch r.URL.Path {
		case "/api/public/traces":
			traceCh <- data
		case "/api/public/observations":
			// Span.End sends observations — drain to avoid deadlock
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	provider := NewLangfuseProvider(server.URL, "sk-secret", "pk-public")
	ctx, span := provider.StartSpan(context.Background(), "test-span",
		providers.WithTenantID("tenant-1"),
		providers.WithWorkflowType("extraction"),
	)

	if ctx == nil {
		t.Error("expected non-nil context")
	}
	if span == nil {
		t.Fatal("expected non-nil span")
	}

	trace := <-traceCh
	if trace["name"] != "test-span" {
		t.Errorf("expected trace name 'test-span', got %v", trace["name"])
	}

	// Cleanup: end the span (will send observation to the server)
	span.End(nil)
}

func TestLangfuse_SpanEnd(t *testing.T) {
	obsCh := make(chan map[string]any, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/public/observations" {
			body, _ := io.ReadAll(r.Body)
			var data map[string]any
			json.Unmarshal(body, &data)
			obsCh <- data
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	provider := NewLangfuseProvider(server.URL, "sk-secret", "pk-public")
	_, span := provider.StartSpan(context.Background(), "end-test")

	// End without error — must not panic.
	span.End(nil)

	obs := <-obsCh
	if obs["name"] != "end-test" {
		t.Errorf("expected observation name 'end-test', got %v", obs["name"])
	}
}

func TestLangfuse_RecordError(t *testing.T) {
	obsCh := make(chan map[string]any, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var data map[string]any
		json.Unmarshal(body, &data)
		obsCh <- data
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	provider := NewLangfuseProvider(server.URL, "sk-secret", "pk-public")
	provider.RecordError(context.Background(), errors.New("test error"))

	obs := <-obsCh
	if obs["level"] != "ERROR" {
		t.Errorf("expected level ERROR, got %v", obs["level"])
	}
	if obs["statusMessage"] != "test error" {
		t.Errorf("expected statusMessage 'test error', got %v", obs["statusMessage"])
	}
}
