package tenant

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aparna/opscore/internal/domain"
)

// ---------------------------------------------------------------------------
// mockTenantProvider
// ---------------------------------------------------------------------------

type mockTenantProvider struct {
	tenants map[string]*domain.Tenant
	slugs   map[string]*domain.Tenant
}

func (m *mockTenantProvider) GetTenant(_ context.Context, id string) (*domain.Tenant, error) {
	t, ok := m.tenants[id]
	if !ok {
		return nil, fmt.Errorf("tenant not found: %s", id)
	}
	return t, nil
}

func (m *mockTenantProvider) GetTenantBySlug(_ context.Context, slug string) (*domain.Tenant, error) {
	t, ok := m.slugs[slug]
	if !ok {
		return nil, fmt.Errorf("tenant not found: %s", slug)
	}
	return t, nil
}

func (m *mockTenantProvider) CreateTenant(_ context.Context, tenant *domain.Tenant) error {
	m.tenants[tenant.ID] = tenant
	m.slugs[tenant.Slug] = tenant
	return nil
}

func (m *mockTenantProvider) ListTenants(_ context.Context) ([]*domain.Tenant, error) {
	var result []*domain.Tenant
	for _, t := range m.tenants {
		result = append(result, t)
	}
	return result, nil
}

func (m *mockTenantProvider) UpdateTenantStatus(_ context.Context, id, status string) error {
	t, ok := m.tenants[id]
	if !ok {
		return fmt.Errorf("tenant not found: %s", id)
	}
	t.Status = status
	return nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newTestHandler(t *testing.T, wantTenantID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant := FromContext(r.Context())
		if tenant == nil {
			http.Error(w, "no tenant in context", http.StatusInternalServerError)
			return
		}
		if tenant.ID != wantTenantID {
			http.Error(w, fmt.Sprintf("unexpected tenant: %s", tenant.ID), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

func newTestProvider() *mockTenantProvider {
	return &mockTenantProvider{
		tenants: map[string]*domain.Tenant{
			"tenant-alpha": {
				ID:     "tenant-alpha",
				Name:   "Alpha Corp",
				Slug:   "alpha-corp",
				Plan:   "pro",
				Status: "active",
				Config: map[string]any{"region": "us-east-1"},
			},
			"tenant-beta": {
				ID:     "tenant-beta",
				Name:   "Beta Inc",
				Slug:   "beta-inc",
				Plan:   "starter",
				Status: "active",
				Config: map[string]any{},
			},
		},
		slugs: map[string]*domain.Tenant{
			"alpha-corp": {
				ID:     "tenant-alpha",
				Name:   "Alpha Corp",
				Slug:   "alpha-corp",
				Plan:   "pro",
				Status: "active",
				Config: map[string]any{"region": "us-east-1"},
			},
			"beta-inc": {
				ID:     "tenant-beta",
				Name:   "Beta Inc",
				Slug:   "beta-inc",
				Plan:   "starter",
				Status: "active",
				Config: map[string]any{},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestTenantMiddleware_ValidTenantID(t *testing.T) {
	provider := newTestProvider()
	mw := New(provider)
	handler := mw.Wrap(newTestHandler(t, "tenant-alpha"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "tenant-alpha")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTenantMiddleware_MissingHeader(t *testing.T) {
	provider := newTestProvider()
	mw := New(provider)
	handler := mw.Wrap(newTestHandler(t, "default"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for default fallback, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTenantMiddleware_UnknownTenantID(t *testing.T) {
	provider := newTestProvider()
	mw := New(provider)

	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should not be called for unknown tenant")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "nonexistent")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestTenantMiddleware_SlugResolution(t *testing.T) {
	provider := newTestProvider()
	mw := New(provider)
	handler := mw.Wrap(newTestHandler(t, "tenant-beta"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-Slug", "beta-inc")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTenantMiddleware_UnknownSlug(t *testing.T) {
	provider := newTestProvider()
	mw := New(provider)

	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should not be called for unknown slug")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-Slug", "unknown-slug")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestTenantMiddleware_IDTakesPriority(t *testing.T) {
	// When both X-Tenant-ID and X-Tenant-Slug are set, ID should win.
	provider := newTestProvider()
	mw := New(provider)
	handler := mw.Wrap(newTestHandler(t, "tenant-alpha"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "tenant-alpha")
	req.Header.Set("X-Tenant-Slug", "beta-inc") // different tenant
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestFromContext_NoTenant(t *testing.T) {
	// FromContext on a context without tenant should return nil.
	ctx := context.Background()
	tenant := FromContext(ctx)
	if tenant != nil {
		t.Error("expected nil tenant from empty context")
	}
}
