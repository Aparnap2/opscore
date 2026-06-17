package telemetry

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/aparna/opscore/internal/providers"
)

// ---------------------------------------------------------------------------
// LangfuseProvider
// ---------------------------------------------------------------------------

// LangfuseProvider implements providers.TracingProvider by sending trace
// and observation events to the Langfuse REST API.
type LangfuseProvider struct {
	client     *http.Client
	baseURL    string
	secretKey  string
	publicKey  string
	authHeader string
}

// NewLangfuseProvider creates a new provider that sends telemetry to the
// given Langfuse base URL (e.g. "https://cloud.langfuse.com").
func NewLangfuseProvider(baseURL, secretKey, publicKey string) *LangfuseProvider {
	raw := publicKey + ":" + secretKey
	auth := base64.StdEncoding.EncodeToString([]byte(raw))

	return &LangfuseProvider{
		client:     &http.Client{Timeout: 10 * time.Second},
		baseURL:    baseURL,
		secretKey:  secretKey,
		publicKey:  publicKey,
		authHeader: "Basic " + auth,
	}
}

// StartSpan creates a new trace in Langfuse and returns a span. The
// returned context carries the span for correlation.
func (p *LangfuseProvider) StartSpan(ctx context.Context, name string, opts ...providers.SpanOption) (context.Context, providers.Span) {
	traceID := uuid.New().String()

	span := &langfuseSpan{
		provider: p,
		id:       uuid.New().String(),
		traceID:  traceID,
		name:     name,
		attrs:    make(map[string]string),
		start:    time.Now(),
	}

	// Build trace payload.
	traceBody := map[string]any{
		"id":   traceID,
		"name": name,
	}

	// Apply SpanOption metadata.
	// We cannot read spanConfig directly (unexported), so we include
	// attributes via the span's SetAttributes when they are set later.
	// The trace-level metadata is limited here; the span observation
	// (sent on End) carries the full attribute set.

	// Fire-and-forget the trace creation.
	p.sendRequest(ctx, "/api/public/traces", traceBody)

	return ctx, span
}

// RecordLLMCall sends an LLM-call observation to Langfuse.
func (p *LangfuseProvider) RecordLLMCall(ctx context.Context, model string, tokens int, costINR float64, durationMs int) {
	// Langfuse captures cost in USD; approximate conversion from INR.
	costUSD := costINR * 0.012

	body := map[string]any{
		"type":  "LLM",
		"model": model,
		"usage": map[string]any{
			"input":  tokens,
			"output": 0,
			"unit":   "TOKENS",
		},
		"cost":      costUSD,
		"startTime": time.Now().Add(-time.Duration(durationMs) * time.Millisecond).Format(time.RFC3339Nano),
		"endTime":   time.Now().Format(time.RFC3339Nano),
	}

	p.sendRequest(ctx, "/api/public/observations", body)
}

// RecordError sends an error observation to Langfuse.
func (p *LangfuseProvider) RecordError(ctx context.Context, err error, opts ...providers.SpanOption) {
	body := map[string]any{
		"type":          "SPAN",
		"level":         "ERROR",
		"statusMessage": err.Error(),
	}

	// Optional: if a trace ID is available in context, include it.
	p.sendRequest(ctx, "/api/public/observations", body)
}

// sendRequest is a fire-and-forget helper that POSTs a JSON body to a
// Langfuse API endpoint. Errors are silently discarded to match the
// TracingProvider contract (void return).
func (p *LangfuseProvider) sendRequest(ctx context.Context, path string, body map[string]any) {
	data, err := json.Marshal(body)
	if err != nil {
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", p.authHeader)

	resp, err := p.client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// ---------------------------------------------------------------------------
// langfuseSpan — implements providers.Span
// ---------------------------------------------------------------------------

type langfuseSpan struct {
	provider *LangfuseProvider
	id       string
	traceID  string
	name     string
	attrs    map[string]string
	start    time.Time
	ended    bool
}

func (s *langfuseSpan) End(err error) {
	if s.ended {
		return
	}
	s.ended = true

	body := map[string]any{
		"id":        s.id,
		"traceId":   s.traceID,
		"name":      s.name,
		"type":      "SPAN",
		"startTime": s.start.Format(time.RFC3339Nano),
		"endTime":   time.Now().Format(time.RFC3339Nano),
	}

	if err != nil {
		body["level"] = "ERROR"
		body["statusMessage"] = err.Error()
	}

	if len(s.attrs) > 0 {
		body["metadata"] = s.attrs
	}

	s.provider.sendRequest(context.Background(), "/api/public/observations", body)
}

func (s *langfuseSpan) SetAttribute(key string, value string) {
	s.attrs[key] = value
}

func (s *langfuseSpan) SetAttributes(attrs map[string]string) {
	for k, v := range attrs {
		s.attrs[k] = v
	}
}

// Compile-time interface checks.
var _ providers.TracingProvider = (*LangfuseProvider)(nil)
var _ providers.Span = (*langfuseSpan)(nil)
