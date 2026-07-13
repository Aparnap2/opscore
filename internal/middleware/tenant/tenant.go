// Package tenant provides HTTP middleware for resolving and injecting
// tenant information into request context.
//
// It reads X-Tenant-ID or X-Tenant-Slug from request headers, resolves
// the tenant via a TenantProvider, and makes it available via FromContext.
// When no header is set, it falls back to a "default" tenant for backward
// compatibility.
package tenant

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
)

// contextKey is an unexported type used for context keys in this package
// to avoid collisions with keys from other packages.
type contextKey string

const tenantKey contextKey = "tenant"

// FromContext extracts the Tenant from the context. Returns nil if no
// tenant was injected.
func FromContext(ctx context.Context) *domain.Tenant {
	t, _ := ctx.Value(tenantKey).(*domain.Tenant)
	return t
}

// WithTenant returns a new context with the given Tenant attached.
func WithTenant(ctx context.Context, t *domain.Tenant) context.Context {
	return context.WithValue(ctx, tenantKey, t)
}

// TenantMiddleware resolves tenants from request headers and injects
// them into the request context.
type TenantMiddleware struct {
	provider         providers.TenantProvider
	setTenantContext func(ctx context.Context, tenantID string) error // optional RLS hook
}

// New creates a TenantMiddleware backed by the given TenantProvider.
func New(provider providers.TenantProvider) *TenantMiddleware {
	return &TenantMiddleware{provider: provider}
}

// WithSetTenantContext registers a function to set the tenant context on
// the database connection after resolving the tenant. This enables
// PostgreSQL Row-Level Security (RLS) policies to filter queries to
// the current tenant.
func (m *TenantMiddleware) WithSetTenantContext(fn func(ctx context.Context, tenantID string) error) *TenantMiddleware {
	m.setTenantContext = fn
	return m
}

// Wrap returns an http.Handler that wraps the next handler with tenant
// resolution logic.
//
// Behaviour:
//   - If X-Tenant-ID is set and non-empty, the provider resolves it.
//     Returns 401 if the tenant is not found.
//   - If X-Tenant-Slug is set and non-empty (and X-Tenant-ID is absent),
//     the provider resolves it. Returns 401 if not found.
//   - If neither header is set, a "default" tenant is injected for
//     backward compatibility.
func (m *TenantMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.Header.Get("X-Tenant-ID")
		tenantSlug := r.Header.Get("X-Tenant-Slug")

		var t *domain.Tenant

		switch {
		case tenantID != "":
			resolved, err := m.provider.GetTenant(r.Context(), tenantID)
			if err != nil || resolved == nil {
				http.Error(w, "tenant not found", http.StatusUnauthorized)
				return
			}
			t = resolved

		case tenantSlug != "":
			resolved, err := m.provider.GetTenantBySlug(r.Context(), tenantSlug)
			if err != nil || resolved == nil {
				http.Error(w, "tenant not found", http.StatusUnauthorized)
				return
			}
			t = resolved

		default:
			// Fall back to "default" tenant for backward compatibility.
			t = &domain.Tenant{
				ID:     "default",
				Name:   "Default",
				Slug:   "default",
				Plan:   "starter",
				Status: "active",
				Config: make(map[string]any),
			}
		}

		// Set the database-level tenant context for RLS if a hook is configured.
		if m.setTenantContext != nil {
			if err := m.setTenantContext(r.Context(), t.ID); err != nil {
				slog.Error("Failed to set tenant context for RLS",
					"tenant_id", t.ID,
					"error", err,
				)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
		}

		ctx := WithTenant(r.Context(), t)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
