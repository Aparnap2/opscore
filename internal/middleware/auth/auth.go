// Package auth provides HTTP middleware for authenticating requests and
// enforcing role-based access control (RBAC).
//
// It reads the Authorization: Bearer <token> or X-API-Key header to
// authenticate the request via an AuthProvider. On success, the resolved
// User is injected into the request context and is accessible via
// FromContext. The RequirePermission helper can then be used to restrict
// access based on the user's role.
package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/aparna/opscore/internal/domain"
)

// contextKey is an unexported type used for context keys in this package
// to avoid collisions with keys from other packages.
type contextKey string

const userKey contextKey = "auth-user"

// AuthProvider resolves an API token to a domain User.
type AuthProvider interface {
	Authenticate(ctx context.Context, token string) (*domain.User, error)
}

// AuthMiddleware authenticates requests by resolving tokens via an
// AuthProvider and injecting the resolved User into the request context.
type AuthMiddleware struct {
	provider AuthProvider
}

// New creates an AuthMiddleware backed by the given AuthProvider.
func New(provider AuthProvider) *AuthMiddleware {
	return &AuthMiddleware{provider: provider}
}

// FromContext extracts the User from the context. Returns nil if no user
// was injected.
func FromContext(ctx context.Context) *domain.User {
	u, _ := ctx.Value(userKey).(*domain.User)
	return u
}

// WithUser returns a new context with the given User attached.
func WithUser(ctx context.Context, u *domain.User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// Wrap returns an http.Handler that wraps the next handler with
// authentication logic.
//
// Behaviour:
//   - Reads the Authorization: Bearer <token> header. If absent, falls
//     back to X-API-Key.
//   - Resolves the token via the AuthProvider. Returns 401 if the token
//     is missing or invalid.
//   - If X-Tenant-ID is set and does not match the user's TenantID,
//     returns 403.
//   - Injects the resolved User into the context via WithUser.
func (m *AuthMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)
		if token == "" {
			http.Error(w, "missing authorization", http.StatusUnauthorized)
			return
		}

		user, err := m.provider.Authenticate(r.Context(), token)
		if err != nil || user == nil {
			http.Error(w, "invalid authorization", http.StatusUnauthorized)
			return
		}

		// If X-Tenant-ID is explicitly set, verify it matches the user's tenant.
		if tenantID := r.Header.Get("X-Tenant-ID"); tenantID != "" && user.TenantID != tenantID {
			http.Error(w, "tenant mismatch", http.StatusForbidden)
			return
		}

		ctx := WithUser(r.Context(), user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequirePermission returns middleware that checks the authenticated user
// has the given permission. It must be used AFTER the auth middleware
// (i.e., after Wrap) so that the User is available in the context.
//
// Returns 401 if no user is in context (not authenticated).
// Returns 403 if the user lacks the required permission.
func RequirePermission(perm domain.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := FromContext(r.Context())
			if user == nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if !user.HasPermission(perm) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// extractToken reads the bearer token from the Authorization header or
// falls back to the X-API-Key header.
func extractToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token != "" {
			return token
		}
	}
	return r.Header.Get("X-API-Key")
}

// StaticAPIKeyProvider is an in-memory AuthProvider that maps API keys
// to domain users. It is intended for development and testing; a
// database-backed provider should be used in production.
type StaticAPIKeyProvider struct {
	keys map[string]*domain.User
}

// NewStaticAPIKeyProvider creates a StaticAPIKeyProvider from a map of
// API keys to domain users.
func NewStaticAPIKeyProvider(keys map[string]*domain.User) *StaticAPIKeyProvider {
	return &StaticAPIKeyProvider{keys: keys}
}

// Authenticate looks up the token in the static key map.
func (p *StaticAPIKeyProvider) Authenticate(_ context.Context, token string) (*domain.User, error) {
	user, ok := p.keys[token]
	if !ok {
		return nil, nil
	}
	return user, nil
}
