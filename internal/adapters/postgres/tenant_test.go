package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/aparna/opscore/internal/domain"
)

func TestTenantMethodSignatures(t *testing.T) {
	// Verify that tenant methods exist with correct signatures.
	adapter := &Adapter{}
	_ = adapter

	var _ func(context.Context, string) (*domain.Tenant, error) = adapter.GetTenant
	var _ func(context.Context, string) (*domain.Tenant, error) = adapter.GetTenantBySlug
	var _ func(context.Context, *domain.Tenant) error = adapter.CreateTenant
	var _ func(context.Context) ([]*domain.Tenant, error) = adapter.ListTenants
	var _ func(context.Context, string, string) error = adapter.UpdateTenantStatus
}

func TestTenantCRUD(t *testing.T) {
	// Integration test - requires real DB.
	connStr := GetTestDatabaseURL(t)
	if connStr == "" {
		t.Skip("Skipping integration test: no database URL configured")
	}

	ctx := context.Background()
	adapter, err := NewAdapter(ctx, connStr)
	if err != nil {
		t.Skipf("Skipping integration test: %v", err)
	}
	defer adapter.Close()

	tenantID := "test-tenant-" + uuid.New().String()[:8]
	slug := "test-org-" + uuid.New().String()[:8]

	// Create tenant.
	tenant := domain.NewTenant(tenantID, "Test Org", slug)

	if err := adapter.CreateTenant(ctx, tenant); err != nil {
		t.Fatalf("CreateTenant failed: %v", err)
	}

	// Get by ID.
	got, err := adapter.GetTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetTenant failed: %v", err)
	}
	if got.Name != "Test Org" {
		t.Errorf("Name = %q, want Test Org", got.Name)
	}
	if got.Slug != slug {
		t.Errorf("Slug = %q, want %q", got.Slug, slug)
	}
	if got.Plan != "starter" {
		t.Errorf("Plan = %q, want starter", got.Plan)
	}
	if got.Status != "active" {
		t.Errorf("Status = %q, want active", got.Status)
	}
	if got.Config == nil {
		t.Error("Config should not be nil")
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}

	// Get by slug.
	gotBySlug, err := adapter.GetTenantBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("GetTenantBySlug failed: %v", err)
	}
	if gotBySlug.ID != tenantID {
		t.Errorf("ID = %q, want %q", gotBySlug.ID, tenantID)
	}
	if gotBySlug.Name != "Test Org" {
		t.Errorf("Name = %q, want Test Org", gotBySlug.Name)
	}

	// List tenants.
	tenants, err := adapter.ListTenants(ctx)
	if err != nil {
		t.Fatalf("ListTenants failed: %v", err)
	}
	if len(tenants) == 0 {
		t.Fatal("expected at least 1 tenant")
	}
	found := false
	for _, tnt := range tenants {
		if tnt.ID == tenantID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("created tenant %s not found in list", tenantID)
	}

	// Update status.
	if err := adapter.UpdateTenantStatus(ctx, tenantID, "suspended"); err != nil {
		t.Fatalf("UpdateTenantStatus failed: %v", err)
	}
	got2, err := adapter.GetTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetTenant after update failed: %v", err)
	}
	if got2.Status != "suspended" {
		t.Errorf("Status after update = %q, want suspended", got2.Status)
	}

	// Update status back to active.
	if err := adapter.UpdateTenantStatus(ctx, tenantID, "active"); err != nil {
		t.Fatalf("UpdateTenantStatus (active) failed: %v", err)
	}
}
