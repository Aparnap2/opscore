package domain

import "time"

// AuditAction represents an auditable action type.
type AuditAction string

const (
	// Vendor actions
	ActionVendorCreated  AuditAction = "vendor:created"
	ActionVendorApproved AuditAction = "vendor:approved"
	ActionVendorRejected AuditAction = "vendor:rejected"

	// Job actions
	ActionJobQueued     AuditAction = "job:queued"
	ActionJobProcessing AuditAction = "job:processing"
	ActionJobCompleted  AuditAction = "job:completed"
	ActionJobFailed     AuditAction = "job:failed"

	// Document actions
	ActionDocumentUploaded  AuditAction = "document:uploaded"
	ActionDocumentProcessed AuditAction = "document:processed"

	// HITL actions
	ActionHITLRequested AuditAction = "hitl:requested"
	ActionHITLApproved  AuditAction = "hitl:approved"
	ActionHITLRejected  AuditAction = "hitl:rejected"

	// Compliance actions
	ActionComplianceCheckRun AuditAction = "compliance:check_run"

	// Unknown / fallback
	ActionUnknown AuditAction = "unknown"
)

// NewAuditEvent creates a new basic AuditEvent with the current timestamp.
func NewAuditEvent(tenantID, actor, action, targetType, targetID string) *AuditEvent {
	return &AuditEvent{
		TenantID:   tenantID,
		Actor:      actor,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Timestamp:  time.Now().UTC(),
	}
}

// NewStateTransitionEvent creates an AuditEvent for a state transition.
// The action is set to "state:transition" and old/new state fields are populated.
func NewStateTransitionEvent(tenantID, actor, targetType, targetID, oldState, newState string) *AuditEvent {
	return &AuditEvent{
		TenantID:   tenantID,
		Actor:      actor,
		Action:     "state:transition",
		TargetType: targetType,
		TargetID:   targetID,
		OldState:   oldState,
		NewState:   newState,
		Timestamp:  time.Now().UTC(),
	}
}

// NewErrorEvent creates an AuditEvent for an error.
// The action is set to "error" and the error message is populated.
func NewErrorEvent(tenantID, actor, targetType, targetID, errorMsg string) *AuditEvent {
	return &AuditEvent{
		TenantID:   tenantID,
		Actor:      actor,
		Action:     "error",
		TargetType: targetType,
		TargetID:   targetID,
		Error:      errorMsg,
		Timestamp:  time.Now().UTC(),
	}
}

// AuditEventFromJob creates an AuditEvent from a Job.
// It extracts TenantID, TargetID (job ID), NewState (job status),
// TargetType ("job"), TraceID, CorrelationID, and Error from the job.
func AuditEventFromJob(job *Job, action string) *AuditEvent {
	return &AuditEvent{
		TenantID:      job.TenantID,
		Actor:         "system",
		Action:        action,
		TargetType:    "job",
		TargetID:      job.ID,
		NewState:      string(job.Status),
		Error:         job.Error,
		TraceID:       job.TraceID,
		CorrelationID: job.CorrelationID,
		Timestamp:     time.Now().UTC(),
	}
}
