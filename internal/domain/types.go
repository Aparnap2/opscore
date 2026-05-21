package domain

import (
	"time"
)

type TrustTier string

const (
	TrustTierProbation  TrustTier = "PROBATION"
	TrustTierStandard   TrustTier = "STANDARD"
	TrustTierPreferred  TrustTier = "PREFERRED"
	TrustTierStrategic  TrustTier = "STRATEGIC"
	TrustTierBlocked    TrustTier = "BLOCKED"
)

type RiskTier string

const (
	RiskTierLow    RiskTier = "LOW"
	RiskTierMedium RiskTier = "MEDIUM"
	RiskTierHigh   RiskTier = "HIGH"
)

type WorkflowType string

const (
	WorkflowDocumentIngestion WorkflowType = "DOCUMENT_INGESTION"
	WorkflowVendorOnboarding  WorkflowType = "VENDOR_ONBOARDING"
	WorkflowCompliance        WorkflowType = "COMPLIANCE"
)

type JobStatus string

const (
	JobStatusPending         JobStatus = "PENDING"
	JobStatusQueued          JobStatus = "QUEUED"
	JobStatusProcessing      JobStatus = "PROCESSING"
	JobStatusCompleted       JobStatus = "COMPLETED"
	JobStatusFailed          JobStatus = "FAILED"            // legacy - treat as terminal
	JobStatusRetryableFailed JobStatus = "RETRYABLE_FAILED" // can retry
	JobStatusTerminalFailed  JobStatus = "TERMINAL_FAILED"  // won't retry
	JobStatusDeadLettered    JobStatus = "DEAD_LETTERED"    // exceeded max retries
	JobStatusAwaitingHITL    JobStatus = "AWAITING_HITL"
)

// IsRetryable returns true if the job status allows retry
func (s JobStatus) IsRetryable() bool {
	return s == JobStatusRetryableFailed
}

// IsTerminal returns true if the job status is final and won't change
func (s JobStatus) IsTerminal() bool {
	return s == JobStatusCompleted ||
		s == JobStatusFailed ||
		s == JobStatusTerminalFailed ||
		s == JobStatusDeadLettered
}

// IsFailed returns true if the job status indicates failure
func (s JobStatus) IsFailed() bool {
	return s == JobStatusFailed ||
		s == JobStatusRetryableFailed ||
		s == JobStatusTerminalFailed ||
		s == JobStatusDeadLettered
}

type AuditEvent struct {
	ID            string    `json:"id,omitempty"`
	TenantID      string    `json:"tenant_id"`
	Actor         string    `json:"actor"`
	Action        string    `json:"action"`
	TargetType    string    `json:"target_type"`
	TargetID      string    `json:"target_id"`
	OldState      string    `json:"old_state,omitempty"`
	NewState      string    `json:"new_state,omitempty"`
	Timestamp     time.Time `json:"timestamp"`
	TraceID       string    `json:"trace_id,omitempty"`
	CorrelationID string    `json:"correlation_id,omitempty"`
}

type Job struct {
	ID            string       `json:"id"`
	TenantID      string       `json:"tenant_id"`
	WorkflowType  WorkflowType `json:"workflow_type"`
	Status        JobStatus    `json:"status"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
	TraceID       string       `json:"trace_id,omitempty"`
	CorrelationID string       `json:"correlation_id,omitempty"`
	Input         interface{}  `json:"input,omitempty"`
	Output        interface{}  `json:"output,omitempty"`
	Error         string       `json:"error,omitempty"`
	// Batch job fields for bundle splitting
	ParentBatchID string `json:"parent_batch_id,omitempty"` // for child jobs
	IsChildJob    bool   `json:"is_child_job,omitempty"`
	// PRD v4.0 fields
	BlobURL       string      `json:"blob_url,omitempty"`
	DocumentType  string      `json:"document_type,omitempty"`
	Confidence    float64     `json:"confidence,omitempty"`
	Extracted     interface{} `json:"extracted_data,omitempty"`
	RiskFlags     []string    `json:"risk_flags,omitempty"`
	HITLReason    string      `json:"hitl_reason,omitempty"`
}

// ComplianceChunk represents a chunk of a document for compliance processing
type ComplianceChunk struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	SourceURL    string    `json:"source_url"`
	SourceHash   string    `json:"source_hash"`
	Content      string    `json:"content"`
	ChunkIndex   int       `json:"chunk_index"`
	Severity     string    `json:"severity"`
	CreatedAt    time.Time `json:"created_at"`
	DocumentType string    `json:"document_type,omitempty"`
	PageNumber   int       `json:"page_number,omitempty"`
}

type Vendor struct {
	TrustBattery TrustBattery `json:"trust_battery"`
	UpdatedAt    time.Time    `json:"updated_at"`
	CreatedAt    time.Time    `json:"created_at"`
	GSTNumber    string       `json:"gst_number,omitempty"`
	PANNumber    string       `json:"pan_number,omitempty"`
	IFSCCode     string       `json:"ifsc_code,omitempty"`
	BankAccount  string       `json:"bank_account,omitempty"`
	RiskTier     RiskTier     `json:"risk_tier"`
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	TenantID     string       `json:"tenant_id"`
	RiskScore    int          `json:"risk_score"`
	Approved     bool         `json:"approved"`
	// PRD v4.0 fields
	RiskFlags          []string   `json:"risk_flags,omitempty"`
	Status             string     `json:"status,omitempty"`
	LastTransactionAt *time.Time `json:"last_transaction_at,omitempty"`
}

type Document struct {
	CreatedAt   time.Time   `json:"created_at"`
	Extracted   interface{} `json:"extracted,omitempty"`
	ID          string      `json:"id"`
	TenantID    string      `json:"tenant_id"`
	JobID       string      `json:"job_id"`
	Type        string      `json:"type"`
	FileName    string      `json:"file_name"`
	StoragePath string      `json:"storage_path"`
	Status      string      `json:"status"`
	ContentHash string      `json:"content_hash,omitempty"`
}

type HITLRequest struct {
	ID           string     `json:"id"`
	TenantID     string     `json:"tenant_id"`
	JobID        string     `json:"job_id"`
	Reason       string     `json:"reason"`
	Status       string     `json:"status"`
	SentAt       time.Time  `json:"sent_at"`
	RespondedAt *time.Time `json:"responded_at,omitempty"`
	Responder    string     `json:"responder,omitempty"`
	Decision     string     `json:"decision,omitempty"`
	SlackTS      string     `json:"slack_ts,omitempty"`
}
