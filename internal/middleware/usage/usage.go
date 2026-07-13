// Package usage provides HTTP middleware for enforcing tenant usage limits
// and tracking usage metrics. It wraps handlers to check plan limits before
// processing requests and to increment usage counters after successful responses.
package usage

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/middleware/tenant"
	"github.com/aparna/opscore/internal/providers"
)

// Middleware enforces usage limits and tracks consumption per tenant.
type Middleware struct {
	provider providers.UsageProvider
}

// NewMiddleware creates a new usage Middleware backed by the given UsageProvider.
func NewMiddleware(provider providers.UsageProvider) *Middleware {
	return &Middleware{provider: provider}
}

// CheckLimit returns HTTP middleware that checks whether the tenant has
// remaining quota for the given metric before passing the request to the
// next handler. If the limit has been exceeded, it responds with 429 Too
// Many Requests and a JSON body containing the metric name and retry_at
// timestamp (start of next billing period).
//
// This should wrap the handler *before* IncrementOnResponse so that the
// limit check happens before any processing occurs.
func (m *Middleware) CheckLimit(metric domain.Metric) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t := tenant.FromContext(r.Context())
			if t == nil {
				// No tenant in context — pass through (e.g. for unauthenticated routes).
				next.ServeHTTP(w, r)
				return
			}

			withinLimit, current, limit, err := m.provider.CheckLimit(r.Context(), t.ID, metric)
			if err != nil {
				slog.Error("Usage limit check failed",
					"tenant_id", t.ID,
					"metric", metric,
					"error", err,
				)
				http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
				return
			}

			if !withinLimit {
				slog.Warn("Usage limit exceeded",
					"tenant_id", t.ID,
					"metric", metric,
					"current", current,
					"limit", limit,
				)

				// retry_at is the start of the next billing period (next month).
				now := time.Now().UTC()
				year, month, _ := now.Date()
				retryAt := time.Date(year, month+1, 1, 0, 0, 0, 0, time.UTC)

				resp := map[string]any{
					"error":    "usage limit exceeded",
					"metric":   string(metric),
					"retry_at": retryAt.Format(time.RFC3339),
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(resp)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// IncrementOnResponse returns HTTP middleware that increments the usage
// counter for the given metric after a successful response (HTTP status
// 2xx). It does nothing for 4xx or 5xx responses.
//
// This should wrap the handler *after* CheckLimit so that usage is only
// incremented when the request passes the limit check.
func (m *Middleware) IncrementOnResponse(metric domain.Metric) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
			next.ServeHTTP(sw, r)

			if sw.code >= 200 && sw.code < 300 {
				t := tenant.FromContext(r.Context())
				if t == nil {
					return
				}

				if err := m.provider.IncrementUsage(r.Context(), t.ID, metric, 1); err != nil {
					slog.Error("Failed to increment usage",
						"tenant_id", t.ID,
						"metric", metric,
						"error", err,
					)
				}
			}
		})
	}
}

// statusWriter captures the HTTP status code written by the next handler
// so that the middleware can inspect it after ServeHTTP returns.
type statusWriter struct {
	http.ResponseWriter
	code int
}

// WriteHeader captures the status code and delegates to the underlying
// ResponseWriter.
func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}
