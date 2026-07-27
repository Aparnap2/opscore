package domain

import (
	"testing"
	"time"
)

func TestNewTenant(t *testing.T) {
	t.Run("creates_tenant_with_all_fields", func(t *testing.T) {
		tenant := NewTenant("tenant-1", "Test Corp", "test-corp")

		if tenant.ID != "tenant-1" {
			t.Errorf("ID = %q, want tenant-1", tenant.ID)
		}
		if tenant.Name != "Test Corp" {
			t.Errorf("Name = %q, want Test Corp", tenant.Name)
		}
		if tenant.Slug != "test-corp" {
			t.Errorf("Slug = %q, want test-corp", tenant.Slug)
		}
		if tenant.Plan != "starter" {
			t.Errorf("Plan = %q, want starter", tenant.Plan)
		}
		if tenant.Status != "active" {
			t.Errorf("Status = %q, want active", tenant.Status)
		}
		if tenant.Config == nil {
			t.Error("Config should not be nil")
		}
		if tenant.CreatedAt.IsZero() {
			t.Error("CreatedAt should not be zero")
		}
		if tenant.UpdatedAt.IsZero() {
			t.Error("UpdatedAt should not be zero")
		}
	})

	t.Run("tenants_get_unique_timestamps", func(t *testing.T) {
		t1 := NewTenant("a", "A", "a")
		time.Sleep(1 * time.Millisecond)
		t2 := NewTenant("b", "B", "b")

		if t2.CreatedAt.Before(t1.CreatedAt) {
			t.Error("second tenant should have later CreatedAt")
		}
	})
}

func TestTenantDefaultPlan(t *testing.T) {
	t.Run("default_plan_is_starter", func(t *testing.T) {
		tenant := NewTenant("t-1", "Alpha Corp", "alpha-corp")
		if tenant.Plan != "starter" {
			t.Errorf("Plan = %q, want starter", tenant.Plan)
		}
	})

	t.Run("plan_can_be_overridden", func(t *testing.T) {
		tenant := NewTenant("t-2", "Beta Inc", "beta-inc")
		tenant.Plan = "pro"
		if tenant.Plan != "pro" {
			t.Errorf("Plan = %q, want pro", tenant.Plan)
		}
	})
}

func TestTenantStatusActive(t *testing.T) {
	t.Run("default_status_is_active", func(t *testing.T) {
		tenant := NewTenant("t-1", "Gamma LLC", "gamma-llc")
		if tenant.Status != "active" {
			t.Errorf("Status = %q, want active", tenant.Status)
		}
	})

	t.Run("status_can_be_set_to_suspended", func(t *testing.T) {
		tenant := NewTenant("t-2", "Delta Ltd", "delta-ltd")
		tenant.Status = "suspended"
		if tenant.Status != "suspended" {
			t.Errorf("Status = %q, want suspended", tenant.Status)
		}
	})
}
