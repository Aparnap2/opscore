package telemetry

import (
	"context"

	"github.com/aparna/opscore/internal/providers"
)

// noopSpan is a silent span that does nothing.
type noopSpan struct{}

func (noopSpan) End(err error)                     {}
func (noopSpan) SetAttribute(key, value string)    {}
func (noopSpan) SetAttributes(attrs map[string]string) {}

// NoopTracer is a silent TracingProvider that discards all telemetry.
// Use when Langfuse credentials are not configured.
type NoopTracer struct{}

func (NoopTracer) StartSpan(ctx context.Context, name string, opts ...providers.SpanOption) (context.Context, providers.Span) {
	return ctx, noopSpan{}
}
func (NoopTracer) RecordLLMCall(ctx context.Context, model string, tokens int, costINR float64, durationMs int) {}
func (NoopTracer) RecordError(ctx context.Context, err error, opts ...providers.SpanOption) {}

// Compile-time interface check.
var _ providers.TracingProvider = (*NoopTracer)(nil)
