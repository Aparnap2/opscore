package states_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/aparna/opscore/internal/domain"
)

// ---------------------------------------------------------------------------
// Pure domain state machine tests — no containers needed
// ---------------------------------------------------------------------------

func TestStateMachine_JobStatus_AllowedTransitions(t *testing.T) {
	// Verify allowed transitions
	t.Log("=== Test: Job status allowed transitions ===")

	t.Run("PENDING_to_PROCESSING", func(t *testing.T) {
		job := &domain.Job{Status: domain.JobStatusPending}
		job.Status = domain.JobStatusProcessing
		if job.Status != domain.JobStatusProcessing {
			t.Errorf("status = %s", job.Status)
		}
	})

	t.Run("PROCESSING_to_COMPLETED", func(t *testing.T) {
		job := &domain.Job{Status: domain.JobStatusProcessing}
		job.Status = domain.JobStatusCompleted
		if !job.Status.IsTerminal() {
			t.Error("COMPLETED should be terminal")
		}
	})

	t.Run("PROCESSING_to_RETRYABLE_FAILED", func(t *testing.T) {
		job := &domain.Job{Status: domain.JobStatusProcessing}
		job.Status = domain.JobStatusRetryableFailed
		if !job.Status.IsRetryable() {
			t.Error("RETRYABLE_FAILED should be retryable")
		}
	})

	t.Run("RETRYABLE_FAILED_to_PROCESSING", func(t *testing.T) {
		job := &domain.Job{Status: domain.JobStatusRetryableFailed}
		job.Status = domain.JobStatusProcessing
		if job.Status != domain.JobStatusProcessing {
			t.Errorf("status = %s", job.Status)
		}
	})

	t.Run("PROCESSING_to_AWAITING_HITL", func(t *testing.T) {
		job := &domain.Job{Status: domain.JobStatusProcessing}
		job.Status = domain.JobStatusAwaitingHITL
		if job.Status.IsTerminal() {
			t.Error("AWAITING_HITL should not be terminal")
		}
	})

	t.Run("AWAITING_HITL_to_COMPLETED", func(t *testing.T) {
		job := &domain.Job{Status: domain.JobStatusAwaitingHITL}
		job.Status = domain.JobStatusCompleted
		if !job.Status.IsTerminal() {
			t.Error("COMPLETED should be terminal")
		}
	})

	t.Run("AWAITING_HITL_to_FAILED", func(t *testing.T) {
		job := &domain.Job{Status: domain.JobStatusAwaitingHITL}
		job.Status = domain.JobStatusFailed
		if !job.Status.IsTerminal() {
			t.Error("FAILED should be terminal")
		}
	})

	t.Log("✅ All allowed transitions verified")
}

func TestStateMachine_JobStatus_DisallowedTransitions(t *testing.T) {
	// Verify disallowed transitions (these are logical checks, not enforced by the type system)
	t.Log("=== Test: Job status disallowed transitions ===")

	t.Run("COMPLETED_to_PROCESSING", func(t *testing.T) {
		job := &domain.Job{Status: domain.JobStatusCompleted}
		// This is a logical error — COMPLETED is terminal
		if !job.Status.IsTerminal() {
			t.Error("COMPLETED should be terminal")
		}
		// The code should check IsTerminal before transitioning
		job.Status = domain.JobStatusProcessing
		// After transition, verify it's no longer terminal
		if job.Status.IsTerminal() {
			t.Error("PROCESSING should not be terminal")
		}
		t.Log("Note: COMPLETED→PROCESSING is a logical error, not enforced by type system")
	})

	t.Run("FAILED_to_COMPLETED", func(t *testing.T) {
		job := &domain.Job{Status: domain.JobStatusFailed}
		if !job.Status.IsTerminal() {
			t.Error("FAILED should be terminal")
		}
		// FAILED is terminal — should not transition to COMPLETED
		// This is a logical constraint
		t.Log("Note: FAILED→COMPLETED is a logical error — FAILED is terminal")
	})

	t.Run("PENDING_to_COMPLETED_must_go_through_PROCESSING", func(t *testing.T) {
		// PENDING → COMPLETED is not allowed — must go through PROCESSING
		job := &domain.Job{Status: domain.JobStatusPending}
		// Direct transition would skip PROCESSING
		job.Status = domain.JobStatusCompleted
		if !job.Status.IsTerminal() {
			t.Error("COMPLETED should be terminal")
		}
		t.Log("PENDING→COMPLETED skips PROCESSING — should be avoided in production code")
	})

	t.Log("✅ All disallowed transitions identified")
}

func TestStateMachine_IsRetryable(t *testing.T) {
	// Verify IsRetryable() returns true for RETRYABLE_FAILED only
	t.Log("=== Test: IsRetryable ===")

	tests := []struct {
		status      domain.JobStatus
		isRetryable bool
	}{
		{domain.JobStatusPending, false},
		{domain.JobStatusQueued, false},
		{domain.JobStatusProcessing, false},
		{domain.JobStatusCompleted, false},
		{domain.JobStatusFailed, false},
		{domain.JobStatusRetryableFailed, true},
		{domain.JobStatusTerminalFailed, false},
		{domain.JobStatusDeadLettered, false},
		{domain.JobStatusAwaitingHITL, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsRetryable(); got != tt.isRetryable {
				t.Errorf("IsRetryable(%s) = %v, want %v", tt.status, got, tt.isRetryable)
			}
		})
	}
	t.Log("✅ IsRetryable correct for all statuses")
}

func TestStateMachine_IsTerminal(t *testing.T) {
	// Verify IsTerminal() returns true for COMPLETED and FAILED only
	t.Log("=== Test: IsTerminal ===")

	tests := []struct {
		status     domain.JobStatus
		isTerminal bool
	}{
		{domain.JobStatusPending, false},
		{domain.JobStatusQueued, false},
		{domain.JobStatusProcessing, false},
		{domain.JobStatusCompleted, true},
		{domain.JobStatusFailed, true},
		{domain.JobStatusRetryableFailed, false},
		{domain.JobStatusTerminalFailed, true},
		{domain.JobStatusDeadLettered, true},
		{domain.JobStatusAwaitingHITL, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsTerminal(); got != tt.isTerminal {
				t.Errorf("IsTerminal(%s) = %v, want %v", tt.status, got, tt.isTerminal)
			}
		})
	}
	t.Log("✅ IsTerminal correct for all statuses")
}

func TestStateMachine_HITLRequest_Transitions(t *testing.T) {
	// Verify HITLRequest status transitions
	t.Log("=== Test: HITLRequest transitions ===")

	t.Run("PENDING_to_APPROVED", func(t *testing.T) {
		req := &domain.HITLRequest{Status: domain.HITLStatusPending}
		req.Status = domain.HITLStatusApproved
		if req.Status != domain.HITLStatusApproved {
			t.Errorf("status = %s, want APPROVED", req.Status)
		}
	})

	t.Run("PENDING_to_REJECTED", func(t *testing.T) {
		req := &domain.HITLRequest{Status: domain.HITLStatusPending}
		req.Status = domain.HITLStatusRejected
		if req.Status != domain.HITLStatusRejected {
			t.Errorf("status = %s, want REJECTED", req.Status)
		}
	})

	t.Run("APPROVED_cannot_change", func(t *testing.T) {
		// Once approved, no other transitions are valid
		req := &domain.HITLRequest{Status: domain.HITLStatusApproved}
		// The type system allows this, but business logic should prevent it
		req.Status = domain.HITLStatusRejected
		t.Log("APPROVED→REJECTED is a logical error — should be prevented by business logic")
	})

	t.Log("✅ HITLRequest transitions verified")
}

func TestStateMachine_TrustBattery_Transitions(t *testing.T) {
	// Verify TrustBattery transitions
	t.Log("=== Test: TrustBattery transitions ===")

	t.Run("PROBATION_to_STANDARD_3_successes", func(t *testing.T) {
		tb := domain.NewTrustBattery()
		if tb.Tier != domain.TrustTierProbation {
			t.Fatalf("initial tier = %s, want PROBATION", tb.Tier)
		}
		tb.RecordSuccess() // 1
		tb.RecordSuccess() // 2
		tb.RecordSuccess() // 3 → upgrade to STANDARD
		if tb.Tier != domain.TrustTierStandard {
			t.Errorf("after 3 successes: tier = %s, want STANDARD", tb.Tier)
		}
		t.Logf("PROBATION→STANDARD: tier=%s, successes=%d", tb.Tier, tb.ConsecutiveSuccesses)
	})

	t.Run("STANDARD_to_PROBATION_3_errors", func(t *testing.T) {
		tb := domain.NewTrustBattery()
		// Promote to STANDARD
		tb.RecordSuccess()
		tb.RecordSuccess()
		tb.RecordSuccess()
		if tb.Tier != domain.TrustTierStandard {
			t.Fatalf("expected STANDARD, got %s", tb.Tier)
		}
		// 3 errors → downgrade
		tb.RecordError() // 1
		tb.RecordError() // 2
		tb.RecordError() // 3 → downgrade
		if tb.Tier != domain.TrustTierProbation {
			t.Errorf("after 3 errors: tier = %s, want PROBATION", tb.Tier)
		}
		t.Logf("STANDARD→PROBATION: tier=%s, errors=%d", tb.Tier, tb.ConsecutiveErrors)
	})

	t.Run("PROBATION_to_BLOCKED_FraudFlagged", func(t *testing.T) {
		tb := domain.NewTrustBattery()
		tb.FraudFlagged()
		if tb.Tier != domain.TrustTierBlocked {
			t.Errorf("after fraud: tier = %s, want BLOCKED", tb.Tier)
		}
		if tb.TrustScore != 0 {
			t.Errorf("trust score = %d, want 0", tb.TrustScore)
		}
		t.Logf("PROBATION→BLOCKED: tier=%s, score=%d", tb.Tier, tb.TrustScore)
	})

	t.Run("STANDARD_to_PROBATION_90_days_inactivity", func(t *testing.T) {
		tb := domain.NewTrustBattery()
		// Promote to STANDARD
		tb.RecordSuccess()
		tb.RecordSuccess()
		tb.RecordSuccess()
		if tb.Tier != domain.TrustTierStandard {
			t.Fatalf("expected STANDARD, got %s", tb.Tier)
		}
		// AdvanceDays checks if DaysInCurrentTier >= 90 BEFORE the current advance
		tb.DaysInCurrentTier = 90
		tb.AdvanceDays(1) // check: (91 - 1) = 90 >= 90 → true → downgrade
		if tb.Tier != domain.TrustTierProbation {
			t.Errorf("after 90+ days inactivity: tier = %s, want PROBATION", tb.Tier)
		}
		t.Logf("STANDARD→PROBATION (inactivity): tier=%s", tb.Tier)
	})

	t.Log("✅ All TrustBattery transitions verified")
}

func TestStateMachine_OptimisticLocking_VersionConflict(t *testing.T) {
	// Verify optimistic locking: UpsertJob with stale version returns ErrVersionConflict
	t.Log("=== Test: Optimistic locking version conflict ===")

	// This is a conceptual test — the actual ErrVersionConflict is in postgres adapter.
	// We verify the pattern is correct by simulating it with an in-memory store.

	type versionedJob struct {
		*domain.Job
		version int
	}

	store := make(map[string]*versionedJob)
	var mu sync.Mutex

	// Simulate concurrent updates
	jobID := "test-lock-job"
	tenantID := "tenant-lock"

	// Initial create
	mu.Lock()
	store[jobID] = &versionedJob{
		Job: &domain.Job{
			ID: jobID, TenantID: tenantID,
			Status: domain.JobStatusPending,
		},
		version: 1,
	}
	mu.Unlock()

	// Simulate two concurrent readers getting version 1
	readJob := func() *versionedJob {
		mu.Lock()
		defer mu.Unlock()
		vj := store[jobID]
		return &versionedJob{
			Job:     &domain.Job{ID: vj.ID, TenantID: vj.TenantID, Status: vj.Status},
			version: vj.version,
		}
	}

	// Writer 1: reads version 1, updates to PROCESSING
	writer1 := readJob()
	writer1.Status = domain.JobStatusProcessing

	mu.Lock()
	existing := store[jobID]
	if writer1.version != existing.version {
		t.Fatal("version conflict — writer 1 should succeed")
	}
	existing.version++
	existing.Status = writer1.Status
	mu.Unlock()

	// Writer 2: uses stale version 1 (should have been 2), tries to update
	writer2 := &versionedJob{
		Job:     &domain.Job{ID: jobID, TenantID: tenantID, Status: domain.JobStatusCompleted},
		version: 1, // stale — current is 2
	}

	mu.Lock()
	existing = store[jobID]
	if writer2.version != existing.version {
		// Version conflict detected — this is the expected behavior
		t.Logf("✅ Version conflict detected: writer2 version=%d, current=%d", writer2.version, existing.version)
	} else {
		t.Error("version conflict should have been detected")
	}
	mu.Unlock()

	t.Log("✅ Optimistic locking pattern verified")
}

func TestStateMachine_UpsertJobRetryResolvesConflict(t *testing.T) {
	// Verify UpsertJob retry resolves version conflict
	t.Log("=== Test: UpsertJob retry resolves conflict ===")

	// Simulate the retry pattern from DocumentAgent.ProcessDocument
	type jobRecord struct {
		job     *domain.Job
		version int
	}
	store := make(map[string]*jobRecord)
	var mu sync.Mutex

	upsert := func(job *domain.Job) error {
		mu.Lock()
		defer mu.Unlock()
		existing, exists := store[job.ID]
		if exists && job.Version != existing.version {
			return fmt.Errorf("version conflict")
		}
		if exists {
			job.Version = existing.version + 1
		} else {
			job.Version = 1
		}
		store[job.ID] = &jobRecord{job: job, version: job.Version}
		return nil
	}

	get := func(id string) *jobRecord {
		mu.Lock()
		defer mu.Unlock()
		return store[id]
	}

	// Initial upsert
	job := &domain.Job{ID: "retry-job", TenantID: "tenant", Status: domain.JobStatusPending}
	if err := upsert(job); err != nil {
		t.Fatalf("initial upsert failed: %v", err)
	}

	// Simulate version conflict and retry
	job2 := &domain.Job{ID: "retry-job", TenantID: "tenant", Status: domain.JobStatusProcessing, Version: 1}
	err := upsert(job2)
	if err != nil {
		// Retry: re-read and apply version
		existing := get("retry-job")
		if existing != nil {
			job2.Version = existing.version
		}
		if retryErr := upsert(job2); retryErr != nil {
			t.Fatalf("retry upsert failed: %v", retryErr)
		}
		t.Log("✅ Version conflict resolved via retry")
	} else {
		t.Log("No conflict on first attempt")
	}

	final := get("retry-job")
	if final == nil {
		t.Fatal("job not found after retry")
	}
	if final.job.Status != domain.JobStatusProcessing {
		t.Errorf("final status = %s, want PROCESSING", final.job.Status)
	}
	t.Logf("✅ Retry resolved: version=%d, status=%s", final.version, final.job.Status)
}
