package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/aparna/opscore/internal/domain"
)

func TestGetRecentJobs(t *testing.T) {
	// This test uses a mock DB or integration container
	// For unit testing, we verify the method exists and signature is correct
	adapter := &Adapter{}
	_ = adapter

	// Verify that GetRecentJobs method exists with correct signature
	var _ func(context.Context, string, int) ([]*domain.Job, error) = adapter.GetRecentJobs
}

func TestGetRiskyVendors(t *testing.T) {
	// Verify that GetRiskyVendors method exists with correct signature
	adapter := &Adapter{}
	_ = adapter

	var _ func(context.Context, string) ([]*domain.Vendor, error) = adapter.GetRiskyVendors
}

func TestGetRecentCompliance(t *testing.T) {
	// Verify that GetRecentCompliance method exists with correct signature
	adapter := &Adapter{}
	_ = adapter

	var _ func(context.Context, string, int) ([]*domain.ComplianceRecord, error) = adapter.GetRecentCompliance
}

func TestOpsEndpointsIntegration(t *testing.T) {
	// Integration test - requires real DB
	connStr := GetTestDatabaseURL(t)
	ctx := context.Background()

	adapter, err := NewAdapter(ctx, connStr)
	if err != nil {
		t.Skipf("Skipping integration test: %v", err)
	}
	defer adapter.Close()

	tenantID := "test-ops-" + uuid.New().String()[:8]

	// Test GetRecentJobs - should return empty when no jobs
	jobs, err := adapter.GetRecentJobs(ctx, tenantID, 10)
	if err != nil {
		t.Fatalf("GetRecentJobs failed: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("Expected 0 jobs, got %d", len(jobs))
	}

	// Test GetRiskyVendors - should return empty when no vendors
	vendors, err := adapter.GetRiskyVendors(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetRiskyVendors failed: %v", err)
	}
	if len(vendors) != 0 {
		t.Errorf("Expected 0 risky vendors, got %d", len(vendors))
	}

	// Test GetRecentCompliance - should return empty when no records
	records, err := adapter.GetRecentCompliance(ctx, tenantID, 10)
	if err != nil {
		t.Fatalf("GetRecentCompliance failed: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("Expected 0 compliance records, got %d", len(records))
	}
}

// GetTestDatabaseURL returns the database connection string from the
// DATABASE_URL environment variable. Integration tests are gated on this:
// they run only when DATABASE_URL is set, and skip otherwise.
func GetTestDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("Skipping integration test: DATABASE_URL is not set")
	}
	return url
}
