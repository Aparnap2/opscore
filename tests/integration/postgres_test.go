package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/aparna/opscore/internal/adapters/postgres"
	"github.com/aparna/opscore/internal/domain"
	"github.com/google/uuid"
)

func TestPostgresAdapter(t *testing.T) {
	connStr := os.Getenv("TEST_DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://opscore:opscore@localhost:5432/opscore?sslmode=disable"
	}

	ctx := context.Background()
	adapter, err := postgres.NewAdapter(ctx, connStr)
	if err != nil {
		t.Fatalf("Failed to create adapter: %v", err)
	}
	defer adapter.Close()

	// Test Ping
	if err := adapter.Ping(ctx); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
	t.Log("✓ Ping OK")

	// Test UpsertJob + GetJob
	jobID := uuid.New().String()
	now := time.Now().UTC()
	job := &domain.Job{
		ID:            jobID,
		TenantID:      "test-tenant",
		WorkflowType:  domain.WorkflowDocumentIngestion,
		Status:        domain.JobStatusPending,
		CreatedAt:     now,
		UpdatedAt:     now,
		TraceID:       "trace-123",
		CorrelationID: "corr-123",
		Input:         map[string]string{"filename": "test.pdf"},
	}
	if err := adapter.UpsertJob(ctx, job); err != nil {
		t.Fatalf("UpsertJob failed: %v", err)
	}
	t.Log("✓ UpsertJob OK")

	got, err := adapter.GetJob(ctx, jobID, "test-tenant")
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if got.ID != jobID {
		t.Fatalf("GetJob returned wrong ID: %s", got.ID)
	}
	if got.Status != domain.JobStatusPending {
		t.Fatalf("GetJob returned wrong status: %s", got.Status)
	}
	t.Log("✓ GetJob OK")

	// Test UpsertVendor + GetVendor
	vendorID := uuid.New().String()
	vendor := &domain.Vendor{
		ID:        vendorID,
		TenantID:  "test-tenant",
		Name:      "Test Vendor",
		GSTNumber: "22AAAAA0000A1Z5",
		RiskScore: 50,
		RiskTier:  domain.RiskTierMedium,
		Approved:  false,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := adapter.UpsertVendor(ctx, vendor); err != nil {
		t.Fatalf("UpsertVendor failed: %v", err)
	}
	t.Log("✓ UpsertVendor OK")

	gotV, err := adapter.GetVendor(ctx, vendorID, "test-tenant")
	if err != nil {
		t.Fatalf("GetVendor failed: %v", err)
	}
	if gotV.Name != "Test Vendor" {
		t.Fatalf("GetVendor returned wrong name: %s", gotV.Name)
	}
	t.Log("✓ GetVendor OK")

	// Test AppendAuditEvent + ListAuditEvents
	event := &domain.AuditEvent{
		TenantID:   "test-tenant",
		Actor:      "test",
		Action:     "TEST_ACTION",
		TargetType: "job",
		TargetID:   jobID,
		NewState:   "PENDING",
		Timestamp:  now,
	}
	if err := adapter.AppendAuditEvent(ctx, event); err != nil {
		t.Fatalf("AppendAuditEvent failed: %v", err)
	}
	t.Log("✓ AppendAuditEvent OK")

	events, err := adapter.ListAuditEvents(ctx, "test-tenant", "job", jobID, 10)
	if err != nil {
		t.Fatalf("ListAuditEvents failed: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("ListAuditEvents returned 0 events")
	}
	t.Logf("✓ ListAuditEvents OK (%d events)", len(events))

	t.Log("\n✅ All Postgres integration tests passed")
}
