package domain

import "time"

// BatchJob represents a parent job for multiple documents
type BatchJob struct {
	ID              string    `json:"id"`
	TenantID        string    `json:"tenant_id"`
	Status          JobStatus `json:"status"`
	ChildCount      int       `json:"child_count"`
	CompletedCount  int       `json:"completed_count"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

const (
	BatchStatusPending    = "PENDING"
	BatchStatusProcessing = "PROCESSING"
	BatchStatusCompleted  = "COMPLETED"
	BatchStatusPartial    = "PARTIAL" // some failed
	BatchStatusFailed     = "FAILED"
)

// BatchJobFromJob converts a Job to BatchJob representation
func BatchJobFromJob(job *Job) *BatchJob {
	return &BatchJob{
		ID:        job.ID,
		TenantID:  job.TenantID,
		Status:    job.Status,
		CreatedAt: job.CreatedAt,
		UpdatedAt: job.UpdatedAt,
	}
}