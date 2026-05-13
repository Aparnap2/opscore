package domain

import "testing"

func TestJobStatus_IsRetryable(t *testing.T) {
	tests := []struct {
		status      JobStatus
		isRetryable bool
	}{
		{JobStatusPending, false},
		{JobStatusQueued, false},
		{JobStatusProcessing, false},
		{JobStatusCompleted, false},
		{JobStatusFailed, false},
		{JobStatusRetryableFailed, true},
		{JobStatusTerminalFailed, false},
		{JobStatusDeadLettered, false},
		{JobStatusAwaitingHITL, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsRetryable(); got != tt.isRetryable {
				t.Errorf("IsRetryable() = %v, want %v", got, tt.isRetryable)
			}
		})
	}
}

func TestJobStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		status    JobStatus
		isTerminal bool
	}{
		{JobStatusPending, false},
		{JobStatusQueued, false},
		{JobStatusProcessing, false},
		{JobStatusCompleted, true},
		{JobStatusFailed, true},
		{JobStatusRetryableFailed, false},
		{JobStatusTerminalFailed, true},
		{JobStatusDeadLettered, true},
		{JobStatusAwaitingHITL, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsTerminal(); got != tt.isTerminal {
				t.Errorf("IsTerminal() = %v, want %v", got, tt.isTerminal)
			}
		})
	}
}

func TestJobStatus_IsFailed(t *testing.T) {
	tests := []struct {
		status  JobStatus
		isFailed bool
	}{
		{JobStatusPending, false},
		{JobStatusQueued, false},
		{JobStatusProcessing, false},
		{JobStatusCompleted, false},
		{JobStatusFailed, true},
		{JobStatusRetryableFailed, true},
		{JobStatusTerminalFailed, true},
		{JobStatusDeadLettered, true},
		{JobStatusAwaitingHITL, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsFailed(); got != tt.isFailed {
				t.Errorf("IsFailed() = %v, want %v", got, tt.isFailed)
			}
		})
	}
}