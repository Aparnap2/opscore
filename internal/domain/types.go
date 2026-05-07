package domain

import (
	"time"
)

type TrustTier string

const (
	TrustTierProbation  TrustTier = "PROBATION"
	TrustTierStandard   TrustTier = "STANDARD"
	TrustTierCore       TrustTier = "CORE"
	TrustTierStrategic  TrustTier = "STRATEGIC"
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
	WorkflowVendorOnboarding   WorkflowType = "VENDOR_ONBOARDING"
	WorkflowCompliance        WorkflowType = "COMPLIANCE"
)

type JobStatus string

const (
	JobStatusPending   JobStatus = "PENDING"
	JobStatusRunning   JobStatus = "RUNNING"
	JobStatusCompleted JobStatus = "COMPLETED"
	JobStatusFailed    JobStatus = "FAILED"
	JobStatusAwaitingHITL JobStatus = "AWAITING_HITL"
)

type AuditEvent struct {
	Actor        string    `json:"actor"`
	Action       string    `json:"action"`
	TargetType   string    `json:"target_type"`
	TargetID     string    `json:"target_id"`
	OldState     string    `json:"old_state,omitempty"`
	NewState     string    `json:"new_state,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
	TraceID      string    `json:"trace_id,omitempty"`
	CorrelationID string   `json:"correlation_id,omitempty"`
}

type Job struct {
	ID             string       `json:"id"`
	TenantID       string       `json:"tenant_id"`
	WorkflowType   WorkflowType `json:"workflow_type"`
	Status         JobStatus    `json:"status"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
	TraceID        string       `json:"trace_id,omitempty"`
	CorrelationID  string       `json:"correlation_id,omitempty"`
	Input          interface{}  `json:"input,omitempty"`
	Output         interface{}  `json:"output,omitempty"`
	Error          string       `json:"error,omitempty"`
}

type Vendor struct {
	ID            string     `json:"id"`
	TenantID      string     `json:"tenant_id"`
	Name          string     `json:"name"`
	GSTNumber     string     `json:"gst_number,omitempty"`
	PANNumber     string     `json:"pan_number,omitempty"`
	IFSCCode      string     `json:"ifsc_code,omitempty"`
	BankAccount   string     `json:"bank_account,omitempty"`
	RiskScore     int        `json:"risk_score"`
	RiskTier      RiskTier   `json:"risk_tier"`
	TrustBattery  TrustBattery `json:"trust_battery"`
	Approved      bool       `json:"approved"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type Document struct {
	ID          string      `json:"id"`
	TenantID    string      `json:"tenant_id"`
	JobID       string      `json:"job_id"`
	Type        string      `json:"type"`
	FileName    string      `json:"file_name"`
	StoragePath string      `json:"storage_path"`
	Status      string      `json:"status"`
	Extracted   interface{} `json:"extracted,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
}

type HITLRequest struct {
	ID          string      `json:"id"`
	TenantID    string      `json:"tenant_id"`
	JobID       string      `json:"job_id"`
	Type        string      `json:"type"`
	Message     string      `json:"message"`
	Status      string      `json:"status"`
	ApprovedBy  string      `json:"approved_by,omitempty"`
	ApprovedAt  *time.Time  `json:"approved_at,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
}