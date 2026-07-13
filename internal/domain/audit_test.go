package domain

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// NewAuditEvent tests
// ---------------------------------------------------------------------------

func TestNewAuditEvent_CreatesEvent(t *testing.T) {
	event := NewAuditEvent("tenant-1", "user-1", "vendor:created", "vendor", "vendor-123")

	if event == nil {
		t.Fatal("NewAuditEvent returned nil")
	}
	if event.TenantID != "tenant-1" {
		t.Errorf("TenantID = %q, want %q", event.TenantID, "tenant-1")
	}
	if event.Actor != "user-1" {
		t.Errorf("Actor = %q, want %q", event.Actor, "user-1")
	}
	if event.Action != "vendor:created" {
		t.Errorf("Action = %q, want %q", event.Action, "vendor:created")
	}
	if event.TargetType != "vendor" {
		t.Errorf("TargetType = %q, want %q", event.TargetType, "vendor")
	}
	if event.TargetID != "vendor-123" {
		t.Errorf("TargetID = %q, want %q", event.TargetID, "vendor-123")
	}
	// Should not set state transition fields
	if event.OldState != "" {
		t.Errorf("OldState = %q, want empty", event.OldState)
	}
	if event.NewState != "" {
		t.Errorf("NewState = %q, want empty", event.NewState)
	}
	// Should not set error field
	if event.Error != "" {
		t.Errorf("Error = %q, want empty", event.Error)
	}
}

func TestNewAuditEvent_SetsTimestamp(t *testing.T) {
	before := time.Now()
	event := NewAuditEvent("tenant-1", "user-1", "vendor:created", "vendor", "vendor-123")
	after := time.Now()

	if event.Timestamp.IsZero() {
		t.Fatal("NewAuditEvent Timestamp is zero")
	}
	if event.Timestamp.Before(before) {
		t.Errorf("Timestamp %v is before reference %v", event.Timestamp, before)
	}
	if event.Timestamp.After(after) {
		t.Errorf("Timestamp %v is after reference %v", event.Timestamp, after)
	}
}

// ---------------------------------------------------------------------------
// NewStateTransitionEvent tests
// ---------------------------------------------------------------------------

func TestAuditEvent_NewStateTransitionEvent(t *testing.T) {
	event := NewStateTransitionEvent("tenant-1", "system", "job", "job-42", "PENDING", "PROCESSING")

	if event == nil {
		t.Fatal("NewStateTransitionEvent returned nil")
	}
	if event.TenantID != "tenant-1" {
		t.Errorf("TenantID = %q, want %q", event.TenantID, "tenant-1")
	}
	if event.Actor != "system" {
		t.Errorf("Actor = %q, want %q", event.Actor, "system")
	}
	if event.TargetType != "job" {
		t.Errorf("TargetType = %q, want %q", event.TargetType, "job")
	}
	if event.TargetID != "job-42" {
		t.Errorf("TargetID = %q, want %q", event.TargetID, "job-42")
	}
	if event.Action != "state:transition" {
		t.Errorf("Action = %q, want %q", event.Action, "state:transition")
	}
	if event.OldState != "PENDING" {
		t.Errorf("OldState = %q, want %q", event.OldState, "PENDING")
	}
	if event.NewState != "PROCESSING" {
		t.Errorf("NewState = %q, want %q", event.NewState, "PROCESSING")
	}
	if !event.Timestamp.IsZero() && event.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
	_ = event.Timestamp.IsZero() // just to verify it's non-zero
	if event.Timestamp.IsZero() {
		t.Error("NewStateTransitionEvent Timestamp is zero")
	}
	// Error should be empty
	if event.Error != "" {
		t.Errorf("Error = %q, want empty", event.Error)
	}
}

func TestAuditEvent_NewStateTransitionEvent_SetsTimestamp(t *testing.T) {
	before := time.Now()
	event := NewStateTransitionEvent("t1", "actor", "type", "id", "old", "new")
	after := time.Now()

	if event.Timestamp.IsZero() {
		t.Fatal("Timestamp is zero")
	}
	if event.Timestamp.Before(before) {
		t.Errorf("Timestamp %v is before reference %v", event.Timestamp, before)
	}
	if event.Timestamp.After(after) {
		t.Errorf("Timestamp %v is after reference %v", event.Timestamp, after)
	}
}

// ---------------------------------------------------------------------------
// NewErrorEvent tests
// ---------------------------------------------------------------------------

func TestAuditEvent_NewErrorEvent(t *testing.T) {
	event := NewErrorEvent("tenant-1", "system", "job", "job-42", "something went wrong")

	if event == nil {
		t.Fatal("NewErrorEvent returned nil")
	}
	if event.TenantID != "tenant-1" {
		t.Errorf("TenantID = %q, want %q", event.TenantID, "tenant-1")
	}
	if event.Actor != "system" {
		t.Errorf("Actor = %q, want %q", event.Actor, "system")
	}
	if event.TargetType != "job" {
		t.Errorf("TargetType = %q, want %q", event.TargetType, "job")
	}
	if event.TargetID != "job-42" {
		t.Errorf("TargetID = %q, want %q", event.TargetID, "job-42")
	}
	if event.Action != "error" {
		t.Errorf("Action = %q, want %q", event.Action, "error")
	}
	if event.Error != "something went wrong" {
		t.Errorf("Error = %q, want %q", event.Error, "something went wrong")
	}
	if event.OldState != "" {
		t.Errorf("OldState = %q, want empty", event.OldState)
	}
	if event.NewState != "" {
		t.Errorf("NewState = %q, want empty", event.NewState)
	}
	if event.Timestamp.IsZero() {
		t.Error("NewErrorEvent Timestamp is zero")
	}
}

// ---------------------------------------------------------------------------
// AuditEventFromJob tests
// ---------------------------------------------------------------------------

func TestAuditEventFromJob(t *testing.T) {
	job := &Job{
		ID:       "job-99",
		TenantID: "tenant-1",
		Status:   JobStatusCompleted,
		TraceID:  "trace-abc",
	}
	event := AuditEventFromJob(job, "job:completed")

	if event == nil {
		t.Fatal("AuditEventFromJob returned nil")
	}
	if event.TenantID != "tenant-1" {
		t.Errorf("TenantID = %q, want %q", event.TenantID, "tenant-1")
	}
	if event.TargetType != "job" {
		t.Errorf("TargetType = %q, want %q", event.TargetType, "job")
	}
	if event.TargetID != "job-99" {
		t.Errorf("TargetID = %q, want %q", event.TargetID, "job-99")
	}
	if event.Action != "job:completed" {
		t.Errorf("Action = %q, want %q", event.Action, "job:completed")
	}
	if event.NewState != string(JobStatusCompleted) {
		t.Errorf("NewState = %q, want %q", event.NewState, string(JobStatusCompleted))
	}
	if event.TraceID != "trace-abc" {
		t.Errorf("TraceID = %q, want %q", event.TraceID, "trace-abc")
	}
	if event.Timestamp.IsZero() {
		t.Error("AuditEventFromJob Timestamp is zero")
	}
}

func TestAuditEventFromJob_EmptyTraceID(t *testing.T) {
	job := &Job{
		ID:       "job-100",
		TenantID: "tenant-2",
		Status:   JobStatusFailed,
	}
	event := AuditEventFromJob(job, "job:failed")

	if event.TraceID != "" {
		t.Errorf("TraceID = %q, want empty", event.TraceID)
	}
	if event.NewState != string(JobStatusFailed) {
		t.Errorf("NewState = %q, want %q", event.NewState, string(JobStatusFailed))
	}
}

func TestAuditEventFromJob_Error(t *testing.T) {
	job := &Job{
		ID:       "job-101",
		TenantID: "tenant-3",
		Status:   JobStatusFailed,
		Error:    "timeout processing document",
	}
	event := AuditEventFromJob(job, "job:failed")

	if event.Error != "timeout processing document" {
		t.Errorf("Error = %q, want %q", event.Error, "timeout processing document")
	}
}

func TestAuditEventFromJob_CorrelationID(t *testing.T) {
	job := &Job{
		ID:            "job-102",
		TenantID:      "tenant-4",
		CorrelationID: "corr-xyz",
		Status:        JobStatusQueued,
	}
	event := AuditEventFromJob(job, "job:queued")

	if event.CorrelationID != "corr-xyz" {
		t.Errorf("CorrelationID = %q, want %q", event.CorrelationID, "corr-xyz")
	}
}

// ---------------------------------------------------------------------------
// AuditAction constants tests
// ---------------------------------------------------------------------------

func TestAuditActionConstants_Vendor(t *testing.T) {
	if ActionVendorCreated == "" {
		t.Error("ActionVendorCreated is empty")
	}
	if ActionVendorApproved == "" {
		t.Error("ActionVendorApproved is empty")
	}
	if ActionVendorRejected == "" {
		t.Error("ActionVendorRejected is empty")
	}
}

func TestAuditActionConstants_Job(t *testing.T) {
	if ActionJobQueued == "" {
		t.Error("ActionJobQueued is empty")
	}
	if ActionJobProcessing == "" {
		t.Error("ActionJobProcessing is empty")
	}
	if ActionJobCompleted == "" {
		t.Error("ActionJobCompleted is empty")
	}
	if ActionJobFailed == "" {
		t.Error("ActionJobFailed is empty")
	}
}

func TestAuditActionConstants_Document(t *testing.T) {
	if ActionDocumentUploaded == "" {
		t.Error("ActionDocumentUploaded is empty")
	}
	if ActionDocumentProcessed == "" {
		t.Error("ActionDocumentProcessed is empty")
	}
}

func TestAuditActionConstants_HITL(t *testing.T) {
	if ActionHITLRequested == "" {
		t.Error("ActionHITLRequested is empty")
	}
	if ActionHITLApproved == "" {
		t.Error("ActionHITLApproved is empty")
	}
	if ActionHITLRejected == "" {
		t.Error("ActionHITLRejected is empty")
	}
}

func TestAuditActionConstants_Compliance(t *testing.T) {
	if ActionComplianceCheckRun == "" {
		t.Error("ActionComplianceCheckRun is empty")
	}
}

func TestAuditActionConstants_Unknown(t *testing.T) {
	if ActionUnknown == "" {
		t.Error("ActionUnknown is empty")
	}
}

func TestAuditActionConstants_Distinct(t *testing.T) {
	all := []AuditAction{
		ActionVendorCreated,
		ActionVendorApproved,
		ActionVendorRejected,
		ActionJobQueued,
		ActionJobProcessing,
		ActionJobCompleted,
		ActionJobFailed,
		ActionDocumentUploaded,
		ActionDocumentProcessed,
		ActionHITLRequested,
		ActionHITLApproved,
		ActionHITLRejected,
		ActionComplianceCheckRun,
		ActionUnknown,
	}

	seen := make(map[AuditAction]bool)
	for _, a := range all {
		if a == "" {
			t.Error("found empty AuditAction constant")
			continue
		}
		if seen[a] {
			t.Errorf("duplicate AuditAction constant: %q", a)
		}
		seen[a] = true
	}
}
