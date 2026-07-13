package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/aparna/opscore/internal/domain"
)

func TestUsageMethodSignatures(t *testing.T) {
	// Verify that usage methods exist with correct signatures.
	adapter := &Adapter{}
	_ = adapter

	var _ func(context.Context, string, domain.Metric, int64) error = adapter.IncrementUsage
	var _ func(context.Context, string, domain.Metric) (int64, error) = adapter.GetUsage
	var _ func(context.Context, string) (map[domain.Metric]int64, error) = adapter.GetCurrentPeriodUsage
	var _ func(context.Context, string, domain.Metric) (bool, int64, int64, error) = adapter.CheckLimit
}

func TestUsageIntegration(t *testing.T) {
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

	tenantID := "test-usage-" + uuid.New().String()[:8]

	// Create a tenant for usage tracking.
	tenant := domain.NewTenant(tenantID, "Usage Test Org", "usage-"+uuid.New().String()[:8])
	if err := adapter.CreateTenant(ctx, tenant); err != nil {
		t.Fatalf("CreateTenant failed: %v", err)
	}

	// Test IncrementUsage creates a new record.
	if err := adapter.IncrementUsage(ctx, tenantID, domain.MetricDocumentsUploaded, 1); err != nil {
		t.Fatalf("IncrementUsage failed: %v", err)
	}

	// Test GetUsage returns correct count.
	count, err := adapter.GetUsage(ctx, tenantID, domain.MetricDocumentsUploaded)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if count != 1 {
		t.Errorf("GetUsage = %d, want 1", count)
	}

	// Test IncrementUsage updates existing record.
	if err := adapter.IncrementUsage(ctx, tenantID, domain.MetricDocumentsUploaded, 3); err != nil {
		t.Fatalf("IncrementUsage (second) failed: %v", err)
	}

	count, err = adapter.GetUsage(ctx, tenantID, domain.MetricDocumentsUploaded)
	if err != nil {
		t.Fatalf("GetUsage after increment failed: %v", err)
	}
	if count != 4 {
		t.Errorf("GetUsage after increment = %d, want 4", count)
	}

	// Test GetCurrentPeriodUsage.
	usage, err := adapter.GetCurrentPeriodUsage(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetCurrentPeriodUsage failed: %v", err)
	}
	if usage[domain.MetricDocumentsUploaded] != 4 {
		t.Errorf("GetCurrentPeriodUsage[documents_uploaded] = %d, want 4", usage[domain.MetricDocumentsUploaded])
	}

	// Test CheckLimit with starter plan (100 docs limit).
	within, current, limit, err := adapter.CheckLimit(ctx, tenantID, domain.MetricDocumentsUploaded)
	if err != nil {
		t.Fatalf("CheckLimit failed: %v", err)
	}
	if !within {
		t.Errorf("CheckLimit: within = false, want true (current=%d, limit=%d)", current, limit)
	}
	if current != 4 {
		t.Errorf("CheckLimit current = %d, want 4", current)
	}
	if limit != 100 {
		t.Errorf("CheckLimit limit = %d, want 100", limit)
	}

	// Test CheckLimit with LLM calls (starter plan has 0 limit).
	var within2 bool
	var current2, limit2 int64
	within2, current2, limit2, err = adapter.CheckLimit(ctx, tenantID, domain.MetricLLMCalls)
	if err != nil {
		t.Fatalf("CheckLimit (LLM) failed: %v", err)
	}
	if within2 {
		t.Errorf("CheckLimit LLM: within = true, want false (starter has 0 LLM limit)")
	}
	if limit2 != 0 {
		t.Errorf("CheckLimit LLM limit = %d, want 0", limit2)
	}
	_ = current2
}
