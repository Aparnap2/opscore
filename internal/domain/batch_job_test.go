package domain

import (
	"testing"
	"time"
)

func TestBatchJobFromJob(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name string
		job  *Job
		want *BatchJob
	}{
		{
			name: "full job to batch",
			job: &Job{
				ID:        "job-1",
				TenantID:  "tenant-1",
				Status:    JobStatusPending,
				CreatedAt: now,
				UpdatedAt: now,
			},
			want: &BatchJob{
				ID:        "job-1",
				TenantID:  "tenant-1",
				Status:    JobStatusPending,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
		{
			name: "processing job",
			job: &Job{
				ID:        "job-2",
				TenantID:  "tenant-2",
				Status:    JobStatusProcessing,
				CreatedAt: now,
				UpdatedAt: now,
			},
			want: &BatchJob{
				ID:        "job-2",
				TenantID:  "tenant-2",
				Status:    JobStatusProcessing,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
		{
			name: "completed job",
			job: &Job{
				ID:        "job-3",
				TenantID:  "tenant-3",
				Status:    JobStatusCompleted,
				CreatedAt: now,
				UpdatedAt: now,
			},
			want: &BatchJob{
				ID:        "job-3",
				TenantID:  "tenant-3",
				Status:    JobStatusCompleted,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BatchJobFromJob(tt.job)
			if got.ID != tt.want.ID {
				t.Errorf("BatchJobFromJob().ID = %v, want %v", got.ID, tt.want.ID)
			}
			if got.TenantID != tt.want.TenantID {
				t.Errorf("BatchJobFromJob().TenantID = %v, want %v", got.TenantID, tt.want.TenantID)
			}
			if got.Status != tt.want.Status {
				t.Errorf("BatchJobFromJob().Status = %v, want %v", got.Status, tt.want.Status)
			}
			if !got.CreatedAt.Equal(tt.want.CreatedAt) {
				t.Errorf("BatchJobFromJob().CreatedAt = %v, want %v", got.CreatedAt, tt.want.CreatedAt)
			}
			if !got.UpdatedAt.Equal(tt.want.UpdatedAt) {
				t.Errorf("BatchJobFromJob().UpdatedAt = %v, want %v", got.UpdatedAt, tt.want.UpdatedAt)
			}
		})
	}
}

func TestBatchJobFromJob_ZeroValues(t *testing.T) {
	job := &Job{}
	got := BatchJobFromJob(job)
	if got.ID != "" {
		t.Errorf("expected empty ID, got %v", got.ID)
	}
	if got.Status != "" {
		t.Errorf("expected empty Status, got %v", got.Status)
	}
}
