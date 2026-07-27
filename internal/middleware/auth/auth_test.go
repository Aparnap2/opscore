package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aparna/opscore/internal/domain"
)

// ---------------------------------------------------------------------------
// mockAuthProvider for testing
// ---------------------------------------------------------------------------

type mockAuthProvider struct {
	users map[string]*domain.User
}

func (m *mockAuthProvider) Authenticate(_ context.Context, token string) (*domain.User, error) {
	u, ok := m.users[token]
	if !ok {
		return nil, nil
	}
	return u, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newTestHandler(t *testing.T, wantUserID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := FromContext(r.Context())
		if user == nil {
			http.Error(w, "no user in context", http.StatusInternalServerError)
			return
		}
		if user.ID != wantUserID {
			http.Error(w, "unexpected user: "+user.ID, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

func newTestHandlerCheckPermission(t *testing.T, perm domain.Permission, shouldAllow bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := FromContext(r.Context())
		if user == nil {
			http.Error(w, "no user in context", http.StatusInternalServerError)
			return
		}
		if user.HasPermission(perm) != shouldAllow {
			if shouldAllow {
				http.Error(w, "expected permission to be granted", http.StatusInternalServerError)
			} else {
				http.Error(w, "expected permission to be denied", http.StatusInternalServerError)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

func newTestProvider() *mockAuthProvider {
	return &mockAuthProvider{
		users: map[string]*domain.User{
			"owner-key": {
				ID:       "user-owner",
				TenantID: "tenant-alpha",
				Email:    "owner@alpha.com",
				Role:     domain.RoleOwner,
				Name:     "Owner User",
			},
			"ops-admin-key": {
				ID:       "user-opsadmin",
				TenantID: "tenant-alpha",
				Email:    "opsadmin@alpha.com",
				Role:     domain.RoleOpsAdmin,
				Name:     "Ops Admin User",
			},
			"reviewer-key": {
				ID:       "user-reviewer",
				TenantID: "tenant-alpha",
				Email:    "reviewer@alpha.com",
				Role:     domain.RoleReviewer,
				Name:     "Reviewer User",
			},
			"auditor-key": {
				ID:       "user-auditor",
				TenantID: "tenant-alpha",
				Email:    "auditor@alpha.com",
				Role:     domain.RoleAuditor,
				Name:     "Auditor User",
			},
			"viewer-key": {
				ID:       "user-viewer",
				TenantID: "tenant-alpha",
				Email:    "viewer@alpha.com",
				Role:     domain.RoleViewer,
				Name:     "Viewer User",
			},
			"beta-key": {
				ID:       "user-beta",
				TenantID: "tenant-beta",
				Email:    "beta@beta.com",
				Role:     domain.RoleOpsAdmin,
				Name:     "Beta User",
			},
		},
	}
}

// ---------------------------------------------------------------------------
// AuthMiddleware tests
// ---------------------------------------------------------------------------

func TestAuthMiddleware_ValidBearerToken(t *testing.T) {
	provider := newTestProvider()
	mw := New(provider)
	handler := mw.Wrap(newTestHandler(t, "user-owner"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer owner-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthMiddleware_ValidXAPIKey(t *testing.T) {
	provider := newTestProvider()
	mw := New(provider)
	handler := mw.Wrap(newTestHandler(t, "user-owner"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "owner-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthMiddleware_NoAuthHeader(t *testing.T) {
	provider := newTestProvider()
	mw := New(provider)

	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should not be called without auth")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_InvalidKey(t *testing.T) {
	provider := newTestProvider()
	mw := New(provider)

	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should not be called with invalid key")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer invalid-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_EmptyBearer(t *testing.T) {
	provider := newTestProvider()
	mw := New(provider)

	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should not be called with empty bearer")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_TenantMismatch(t *testing.T) {
	provider := newTestProvider()
	mw := New(provider)

	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should not be called on tenant mismatch")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer owner-key")
	req.Header.Set("X-Tenant-ID", "tenant-beta") // user is in tenant-alpha
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestAuthMiddleware_TenantMatch(t *testing.T) {
	provider := newTestProvider()
	mw := New(provider)
	handler := mw.Wrap(newTestHandler(t, "user-owner"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer owner-key")
	req.Header.Set("X-Tenant-ID", "tenant-alpha") // matches user's tenant
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthMiddleware_NoTenantHeaderMeansNoCheck(t *testing.T) {
	// When X-Tenant-ID is not set, no tenant check is performed.
	provider := newTestProvider()
	mw := New(provider)
	handler := mw.Wrap(newTestHandler(t, "user-beta"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer beta-key")
	// No X-Tenant-ID header
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthMiddleware_BearerTakesPriority(t *testing.T) {
	// When both Bearer token and X-API-Key are set, Bearer should win.
	provider := newTestProvider()
	mw := New(provider)
	handler := mw.Wrap(newTestHandler(t, "user-owner"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer owner-key")
	req.Header.Set("X-API-Key", "invalid-key-should-be-ignored")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// FromContext tests
// ---------------------------------------------------------------------------

func TestFromContext_NoUser(t *testing.T) {
	ctx := context.Background()
	user := FromContext(ctx)
	if user != nil {
		t.Error("expected nil user from empty context")
	}
}

func TestFromContext_WithUser(t *testing.T) {
	u := &domain.User{ID: "test-user", TenantID: "t1", Role: domain.RoleViewer}
	ctx := WithUser(context.Background(), u)
	got := FromContext(ctx)
	if got == nil {
		t.Fatal("expected user in context")
	}
	if got.ID != "test-user" {
		t.Errorf("expected user ID 'test-user', got %s", got.ID)
	}
}

// ---------------------------------------------------------------------------
// RequirePermission tests
// ---------------------------------------------------------------------------

func TestRequirePermission_AllowsWithCorrectPermission(t *testing.T) {
	provider := newTestProvider()
	authMW := New(provider)

	// Ops admin has PermissionDocumentUpload
	handler := authMW.Wrap(RequirePermission(domain.PermissionDocumentUpload)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	))

	req := httptest.NewRequest(http.MethodPost, "/upload", nil)
	req.Header.Set("Authorization", "Bearer ops-admin-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRequirePermission_DeniesWithoutCorrectPermission(t *testing.T) {
	provider := newTestProvider()
	authMW := New(provider)

	// Reviewer does NOT have PermissionDocumentUpload
	handler := authMW.Wrap(RequirePermission(domain.PermissionDocumentUpload)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("next handler should not be called without required permission")
		}),
	))

	req := httptest.NewRequest(http.MethodPost, "/upload", nil)
	req.Header.Set("Authorization", "Bearer reviewer-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestRequirePermission_OwnerAlwaysAllowed(t *testing.T) {
	provider := newTestProvider()
	authMW := New(provider)

	// Owner should have access to all endpoints via admin:*
	handler := authMW.Wrap(RequirePermission(domain.PermissionVendorApprove)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	))

	req := httptest.NewRequest(http.MethodGet, "/vendors/approve", nil)
	req.Header.Set("Authorization", "Bearer owner-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRequirePermission_NoUserReturns401(t *testing.T) {
	// If RequirePermission is used without auth middleware (no user in context),
	// it should return 401.
	handler := RequirePermission(domain.PermissionDocumentView)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("next handler should not be called")
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// StaticAPIKeyProvider tests
// ---------------------------------------------------------------------------

func TestStaticAPIKeyProvider_ValidKey(t *testing.T) {
	users := map[string]*domain.User{
		"static-key-1": {ID: "static-user", TenantID: "t1", Role: domain.RoleViewer, Name: "Static"},
	}
	p := NewStaticAPIKeyProvider(users)
	user, err := p.Authenticate(context.Background(), "static-key-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user == nil {
		t.Fatal("expected non-nil user")
	}
	if user.ID != "static-user" {
		t.Errorf("expected 'static-user', got %s", user.ID)
	}
}

func TestStaticAPIKeyProvider_InvalidKey(t *testing.T) {
	users := map[string]*domain.User{}
	p := NewStaticAPIKeyProvider(users)
	user, err := p.Authenticate(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user != nil {
		t.Error("expected nil user for invalid key")
	}
}

// ---------------------------------------------------------------------------
// Integration-style test: full auth + permission chain via middleware
// ---------------------------------------------------------------------------

func TestAuthMiddleware_UserInContext(t *testing.T) {
	provider := newTestProvider()
	mw := New(provider)

	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := FromContext(r.Context())
		if user == nil {
			http.Error(w, "no user", http.StatusInternalServerError)
			return
		}
		if user.ID != "user-owner" {
			http.Error(w, "wrong user", http.StatusInternalServerError)
			return
		}
		if user.Role != domain.RoleOwner {
			http.Error(w, "wrong role", http.StatusInternalServerError)
			return
		}
		if user.TenantID != "tenant-alpha" {
			http.Error(w, "wrong tenant", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer owner-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkAuthMiddleware(b *testing.B) {
	provider := NewStaticAPIKeyProvider(map[string]*domain.User{
		"bench-key": {ID: "bench-user", TenantID: "t1", Role: domain.RoleViewer},
	})
	mw := New(provider)
	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer bench-key")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}
