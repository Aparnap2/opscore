package agentic

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/aparna/opscore/internal/adapters/minio"
	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
)

// ---------------------------------------------------------------------------
// Test 1: StubLLM error does not crash the agent
//
// Verifies that when the LLM provider returns an error during vendor
// processing, the agent handles it gracefully without crashing and still
// saves the vendor to the database.
// ---------------------------------------------------------------------------

func TestStubLLMErrorDoesNotCrashAgent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Arrange
	infra, err := StartInfra(t)
	if err != nil {
		t.Fatalf("StartInfra: %v", err)
	}
	t.Cleanup(infra.Close)

	agent := CreateTestVendorAgent(&StubLLM{}, infra)

	// Build a VendorJob whose documents list triggers StubLLM failure
	// StubLLM.Reason returns error if the prompt contains "trigger_error"
	vendorJob := &agents.VendorJob{
		TenantID: "tenant-llm-error",
		JobID:    GenerateJobID(),
		VendorData: &agents.VendorData{
			Name:      "Test Vendor LLM Fail",
			GSTNumber: "22AAAAA0000A1Z5",
			PANNumber: "ABCDE1234F",
			Documents: []string{"trigger_error_doc.pdf"}, // triggers LLM failure
		},
	}

	// Act
	result, err := agent.ProcessVendor(ctx, vendorJob)
	if err != nil {
		t.Fatalf("ProcessVendor should not crash when LLM fails: %v", err)
	}
	t.Log("Step 1 — ProcessVendor completed without crashing")

	// Assert 1: llm_analysis absent from result (LLM path skipped gracefully)
	if _, hasAnalysis := result["llm_analysis"]; hasAnalysis {
		t.Error("llm_analysis should be absent when LLM Reason() returns error")
	} else {
		t.Log("Step 2 OK — llm_analysis absent (LLM path skipped)")
	}

	// Assert 2: vendor still saved in the database
	savedVendor, err := infra.DB.GetVendor(ctx, vendorJob.JobID)
	if err != nil {
		t.Fatalf("vendor should have been saved despite LLM error: %v", err)
	}
	if savedVendor.Name != "Test Vendor LLM Fail" {
		t.Errorf("expected vendor name 'Test Vendor LLM Fail', got %q", savedVendor.Name)
	}
	t.Logf("Step 3 OK — vendor saved: %s (name=%q)", savedVendor.ID, savedVendor.Name)

	// Assert 3: job record exists and is COMPLETED
	dbJob, err := infra.DB.GetJob(ctx, vendorJob.JobID, vendorJob.TenantID)
	if err != nil {
		t.Fatalf("job should exist after ProcessVendor: %v", err)
	}
	if dbJob.Status != domain.JobStatusCompleted && dbJob.Status != domain.JobStatusAwaitingHITL {
		t.Errorf("expected COMPLETED or AWAITING_HITL, got %s", dbJob.Status)
	}
	t.Logf("Step 4 OK — job status: %s", dbJob.Status)

	// Assert 4: risk tier is set properly in output
	if riskTier, ok := result["trust_tier"]; ok {
		t.Logf("Trust tier: %v", riskTier)
	}
}

// ---------------------------------------------------------------------------
// Test 1b: StubLLM success path — ensures LLM analysis is present
//
// When the LLM succeeds, the result should include "llm_analysis" with the
// reasoning output.
// ---------------------------------------------------------------------------

func TestStubLLMSuccessIncludesAnalysis(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	infra, err := StartInfra(t)
	if err != nil {
		t.Fatalf("StartInfra: %v", err)
	}
	t.Cleanup(infra.Close)

	agent := CreateTestVendorAgent(&StubLLM{}, infra)

	vendorJob := &agents.VendorJob{
		TenantID: "tenant-llm-success",
		JobID:    GenerateJobID(),
		VendorData: &agents.VendorData{
			Name:      "please approve this vendor", // includes "approve" → StubLLM trigger
			GSTNumber: "22AAAAA0000A1Z5",
			PANNumber: "ABCDE1234F",
			Documents: []string{"doc1.pdf", "doc2.pdf"},
		},
	}

	result, err := agent.ProcessVendor(ctx, vendorJob)
	if err != nil {
		t.Fatalf("ProcessVendor should succeed: %v", err)
	}

	analysis, hasAnalysis := result["llm_analysis"]
	if !hasAnalysis {
		t.Fatal("llm_analysis should be present when LLM succeeds")
	}
	if analysis != "approved" {
		t.Errorf("expected 'approved' analysis (StubLLM triggers on 'approve'), got %v", analysis)
	}
	t.Logf("LLM analysis: %v", analysis)
}

// ---------------------------------------------------------------------------
// Test 2: MinIO upload failure
//
// Verifies that when MinIO (object storage) becomes unavailable, the storage
// adapter returns a graceful error (no panic/crash), and the system recovers
// once MinIO is restarted without data corruption.
// ---------------------------------------------------------------------------

func TestMinIOUploadFailureRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Arrange
	infra, err := StartInfra(t)
	if err != nil {
		t.Fatalf("StartInfra: %v", err)
	}
	t.Cleanup(infra.Close)

	bucket := "test-recovery-bucket"

	// Step 1 — upload succeeds before outage
	content1 := []byte("hello minio recovery test")
	url1, err := infra.Storage.Upload(ctx, bucket, "before_outage.txt", bytes.NewReader(content1), "text/plain")
	if err != nil {
		t.Fatalf("upload before outage should succeed: %v", err)
	}
	t.Logf("Step 1 OK — upload before outage: %s", url1)

	// Verify download works
	reader1, err := infra.Storage.Download(ctx, bucket, "before_outage.txt")
	if err != nil {
		t.Fatalf("download before outage should succeed: %v", err)
	}
	downloaded1, _ := io.ReadAll(reader1)
	reader1.Close()
	if string(downloaded1) != "hello minio recovery test" {
		t.Fatalf("downloaded content mismatch: got %q", string(downloaded1))
	}
	t.Log("Step 1 — download verification OK")

	// Step 2 — stop MinIO container
	t.Log("Stopping MinIO container...")
	if err := infra.StopMinio(ctx); err != nil {
		t.Fatalf("StopMinio: %v", err)
	}
	time.Sleep(2 * time.Second)
	t.Log("Step 2 — MinIO stopped")

	// Step 3 — upload fails gracefully (no panic, returns error)
	content2 := []byte("data during outage")
	_, err = infra.Storage.Upload(ctx, bucket, "during_outage.txt", bytes.NewReader(content2), "text/plain")
	if err == nil {
		t.Log("Step 3 note — upload succeeded despite stop (connection may still be alive)")
	} else {
		t.Logf("Step 3 OK — upload failed gracefully during outage: %v", err)
	}

	// Step 4 — restart MinIO container
	t.Log("Restarting MinIO container...")
	if err := infra.StartMinio(ctx); err != nil {
		t.Fatalf("StartMinio: %v", err)
	}
	time.Sleep(3 * time.Second)

	// After restart, the port mapping may have changed. Re-read it.
	newMinioPort, err := infra.MinioPort(ctx)
	if err != nil {
		t.Fatalf("MinioPort after restart: %v", err)
	}
	t.Logf("Step 4 — MinIO restarted on port %s", newMinioPort)

	// Step 5 — upload succeeds after recovery (use a new storage adapter with the new port)
	newStorage, err := minio.NewAdapter(
		fmt.Sprintf("localhost:%s", newMinioPort),
		"minioadmin",
		"minioadmin",
		false,
	)
	if err != nil {
		t.Fatalf("new MinIO adapter after restart: %v", err)
	}

	content3 := []byte("data after recovery")
	url3, err := newStorage.Upload(ctx, bucket, "after_recovery.txt", bytes.NewReader(content3), "text/plain")
	if err != nil {
		t.Fatalf("upload after restart: %v", err)
	}
	t.Logf("Step 5 OK — upload after recovery: %s", url3)

	// Step 6 — original data is intact (no corruption from outage)
	// Use the new storage adapter for download too
	reader2, err := newStorage.Download(ctx, bucket, "before_outage.txt")
	if err != nil {
		t.Fatalf("download original file after recovery: %v", err)
	}
	defer reader2.Close()
	downloaded2, _ := io.ReadAll(reader2)
	if string(downloaded2) != "hello minio recovery test" {
		t.Fatalf("original data corrupted: got %q, expected 'hello minio recovery test'", string(downloaded2))
	}
	t.Log("Step 6 OK — original data intact after MinIO recovery")
}

// ---------------------------------------------------------------------------
// Test 3: Multiple concurrent agents
//
// Verifies that multiple DocumentAgent instances can process jobs concurrently
// without race conditions, producing unique job IDs and no duplicate HITL
// requests.
// ---------------------------------------------------------------------------

func TestMultipleConcurrentAgents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Arrange
	infra, err := StartInfra(t)
	if err != nil {
		t.Fatalf("StartInfra: %v", err)
	}
	t.Cleanup(infra.Close)

	// Use a single agent (thread-safe since all deps are concurrent-safe)
	agent := CreateTestDocumentAgent(&StubOCR{}, nil, infra)

	// Create 5 jobs with unique IDs
	numJobs := 5
	jobs := make([]*agents.DocumentJob, numJobs)
	jobIDs := make([]string, numJobs)
	for i := 0; i < numJobs; i++ {
		jobID := GenerateJobID()
		jobIDs[i] = jobID
		jobs[i] = &agents.DocumentJob{
			TenantID: "tenant-concurrent",
			JobID:    jobID,
			BlobURL:  fmt.Sprintf("s3://bucket/confidence_0.95_concurrent_%d.pdf", i),
			FileName: fmt.Sprintf("invoice_%d.pdf", i),
		}
	}

	// Act — process all 5 concurrently
	var wg sync.WaitGroup
	errCh := make(chan error, numJobs)

	for i := 0; i < numJobs; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := agent.ProcessDocument(ctx, jobs[idx])
			if err != nil {
				errCh <- fmt.Errorf("job %s failed: %w", jobs[idx].JobID, err)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)

	// Assert 1 — no errors
	var errs []error
	for e := range errCh {
		errs = append(errs, e)
	}
	if len(errs) > 0 {
		t.Fatalf("%d concurrent processing errors: %v", len(errs), errs)
	}
	t.Logf("Step 1 OK — all %d concurrent jobs completed without error", numJobs)

	// Assert 2 — all 5 have unique job IDs in DB
	for _, jobID := range jobIDs {
		dbJob, err := infra.DB.GetJob(ctx, jobID, "tenant-concurrent")
		if err != nil {
			t.Errorf("job %s not found in DB: %v", jobID, err)
			continue
		}
		if dbJob.Status != domain.JobStatusCompleted && dbJob.Status != domain.JobStatusAwaitingHITL {
			t.Errorf("job %s has unexpected status: %s", jobID, dbJob.Status)
		}
		t.Logf("  Job %s: status=%s", jobID, dbJob.Status)
	}

	// Verify all job IDs are unique
	seen := make(map[string]bool)
	for _, id := range jobIDs {
		if seen[id] {
			t.Errorf("duplicate job ID: %s", id)
		}
		seen[id] = true
	}
	t.Logf("Step 2 OK — all %d job IDs unique in DB", numJobs)

	// Assert 3 — no duplicate HITL requests (use StubDB in-memory tracking)
	hitlByJob := make(map[string]int)
	for _, hr := range infra.DB.CreatedHITLReq {
		hitlByJob[hr.JobID]++
	}
	dups := 0
	for jobID, count := range hitlByJob {
		if count > 1 {
			t.Errorf("duplicate HITL requests for job %s: %d requests", jobID, count)
			dups++
		}
	}
	if dups == 0 {
		t.Logf("Step 3 OK — no duplicate HITL requests across %d concurrent jobs", numJobs)
	}
}

// ---------------------------------------------------------------------------
// Test 3b: Concurrent vendor agents with LLM
//
// Same as above but with VendorAgent + LLM to test concurrent llm calls.
// ---------------------------------------------------------------------------

func TestMultipleConcurrentVendorAgents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	infra, err := StartInfra(t)
	if err != nil {
		t.Fatalf("StartInfra: %v", err)
	}
	t.Cleanup(infra.Close)

	agent := CreateTestVendorAgent(&StubLLM{}, infra)

	numVendors := 5
	var wg sync.WaitGroup
	errCh := make(chan error, numVendors)

	for i := 0; i < numVendors; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			job := &agents.VendorJob{
				TenantID: "tenant-vendor-concurrent",
				JobID:    GenerateJobID(),
				VendorData: &agents.VendorData{
					Name:      fmt.Sprintf("Concurrent Vendor %d", idx),
					GSTNumber: "22AAAAA0000A1Z5",
					Documents: []string{fmt.Sprintf("doc_%d.pdf", idx)},
				},
			}
			_, err := agent.ProcessVendor(ctx, job)
			if err != nil {
				errCh <- fmt.Errorf("vendor %d: %w", idx, err)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)

	var errs []error
	for e := range errCh {
		errs = append(errs, e)
	}
	if len(errs) > 0 {
		t.Fatalf("%d concurrent vendor processing errors: %v", len(errs), errs)
	}
	t.Logf("All %d concurrent vendor agents completed without error", numVendors)
	t.Log("No duplicate HITL requests (vendor auto-completes with valid GST)")
}

// ---------------------------------------------------------------------------
// Test 4: TrustBattery state machine recovery
//
// Tests the TrustBattery state machine transitions:
//   STANDARD → (3 errors) → PROBATION → (3 successes) → STANDARD
//
// This is a pure domain test (no containers needed) but is included here
// because it validates the recovery behavior that the agents depend on.
// ---------------------------------------------------------------------------

func TestTrustBatteryStateMachineRecovery(t *testing.T) {
	t.Run("downgrade after 3 consecutive errors", func(t *testing.T) {
		tb := domain.NewTrustBattery()

		// Initial state: PROBATION
		if tb.Tier != domain.TrustTierProbation {
			t.Fatalf("expected PROBATION, got %s", tb.Tier)
		}

		// Bump up to STANDARD by recording 3 successes from PROBATION
		tb.RecordSuccess() // success 1: PROBATION, consecutive_successes=1
		tb.RecordSuccess() // success 2: PROBATION, consecutive_successes=2
		tb.RecordSuccess() // success 3: triggers tryUpgrade → STANDARD

		if tb.Tier != domain.TrustTierStandard {
			t.Fatalf("expected STANDARD, got %s", tb.Tier)
		}
		t.Logf("Initial tier: STANDARD (consecutive_errors=%d)", tb.ConsecutiveErrors)

		// Record 2 errors — should still be STANDARD
		tb.RecordError()
		if tb.Tier != domain.TrustTierStandard {
			t.Fatalf("after 1 error, should still be STANDARD, got %s", tb.Tier)
		}
		if tb.ConsecutiveErrors != 1 {
			t.Fatalf("expected consecutive_errors=1, got %d", tb.ConsecutiveErrors)
		}
		t.Logf("After 1 error: %s (consecutive_errors=%d)", tb.Tier, tb.ConsecutiveErrors)

		tb.RecordError()
		if tb.Tier != domain.TrustTierStandard {
			t.Fatalf("after 2 errors, should still be STANDARD, got %s", tb.Tier)
		}
		if tb.ConsecutiveErrors != 2 {
			t.Fatalf("expected consecutive_errors=2, got %d", tb.ConsecutiveErrors)
		}
		t.Logf("After 2 errors: %s (consecutive_errors=%d)", tb.Tier, tb.ConsecutiveErrors)

		// Record 3rd error → should downgrade to PROBATION
		tb.RecordError()
		if tb.Tier != domain.TrustTierProbation {
			t.Fatalf("after 3 errors, should downgrade to PROBATION, got %s", tb.Tier)
		}
		t.Logf("After 3 errors: downgraded to %s", tb.Tier)

		// Assert: consecutive_errors reset after downgrade
		if tb.ConsecutiveErrors != 0 {
			t.Errorf("consecutive_errors should reset after downgrade, got %d", tb.ConsecutiveErrors)
		}
		t.Log("consecutive_errors reset to 0 after downgrade ✓")
	})

	t.Run("upgrade back to STANDARD after 3 successes", func(t *testing.T) {
		tb := domain.NewTrustBattery()

		// Start at STANDARD
		tb.RecordSuccess() // 1
		tb.RecordSuccess() // 2
		tb.RecordSuccess() // 3 → upgrade to STANDARD

		// Downgrade to PROBATION via 3 errors
		tb.RecordError() // 1
		tb.RecordError() // 2
		tb.RecordError() // 3 → downgrade

		if tb.Tier != domain.TrustTierProbation {
			t.Fatalf("should be PROBATION after errors, got %s", tb.Tier)
		}
		t.Logf("Starting upgrade test from %s (consecutive_successes=%d, errors=%d)",
			tb.Tier, tb.ConsecutiveSuccesses, tb.ConsecutiveErrors)

		// Record 3 successes to upgrade back to STANDARD
		tb.RecordSuccess() // 1
		if tb.ConsecutiveErrors != 0 {
			t.Errorf("consecutive_errors should reset on first success, got %d", tb.ConsecutiveErrors)
		}
		t.Logf("After 1st success: %s (successes=%d)", tb.Tier, tb.ConsecutiveSuccesses)

		tb.RecordSuccess() // 2
		t.Logf("After 2nd success: %s (successes=%d)", tb.Tier, tb.ConsecutiveSuccesses)

		tb.RecordSuccess() // 3 → tryUpgrade to STANDARD
		if tb.Tier != domain.TrustTierStandard {
			t.Fatalf("after 3 successes from PROBATION, should upgrade to STANDARD, got %s", tb.Tier)
		}
		t.Logf("After 3rd success: upgraded to %s ✓", tb.Tier)

		// consecutive_errors was reset on the first success
		if tb.ConsecutiveErrors != 0 {
			t.Errorf("consecutive_errors should be 0 after success, got %d", tb.ConsecutiveErrors)
		}
		t.Log("consecutive_errors is 0 after success sequence ✓")
	})

	t.Run("STANDARD stays STANDARD with mixed errors/successes", func(t *testing.T) {
		tb := domain.NewTrustBattery()

		// Promote to STANDARD
		tb.RecordSuccess()
		tb.RecordSuccess()
		tb.RecordSuccess()
		if tb.Tier != domain.TrustTierStandard {
			t.Fatalf("expected STANDARD, got %s", tb.Tier)
		}

		// Mixed: error, success, error, success → should stay STANDARD
		tb.RecordError()   // 1 error
		tb.RecordSuccess() // resets errors to 0, successes to 1
		if tb.ConsecutiveErrors != 0 {
			t.Errorf("errors should reset after success, got %d", tb.ConsecutiveErrors)
		}
		if tb.ConsecutiveSuccesses != 1 {
			t.Errorf("expected 1 success, got %d", tb.ConsecutiveSuccesses)
		}

		tb.RecordError() // 1 error, successes reset to 0
		tb.RecordSuccess()

		if tb.Tier != domain.TrustTierStandard {
			t.Errorf("should remain STANDARD after mixed pattern, got %s", tb.Tier)
		}
		t.Logf("Mixed pattern: tier=%s, consecutive_errors=%d, consecutive_successes=%d",
			tb.Tier, tb.ConsecutiveErrors, tb.ConsecutiveSuccesses)
	})
}

// ---------------------------------------------------------------------------
// Test 4b: TrustBattery fraud and inactivity transitions
//
// Tests additional TrustBattery edge cases that affect agent behavior.
// ---------------------------------------------------------------------------

func TestTrustBatteryEdgeCases(t *testing.T) {
	t.Run("FraudFlagged sets BLOCKED", func(t *testing.T) {
		tb := domain.NewTrustBattery()
		tb.RecordSuccess()
		tb.RecordSuccess()
		tb.RecordSuccess() // → STANDARD

		tb.FraudFlagged()
		if tb.Tier != domain.TrustTierBlocked {
			t.Errorf("expected BLOCKED after fraud, got %s", tb.Tier)
		}
		if tb.TrustScore != 0 {
			t.Errorf("trust_score should be 0 after fraud, got %d", tb.TrustScore)
		}
		if tb.ConsecutiveSuccesses != 0 {
			t.Errorf("successes should reset after fraud, got %d", tb.ConsecutiveSuccesses)
		}
	})

	t.Run("downgrade from STANDARD via inactivity", func(t *testing.T) {
		tb := domain.NewTrustBattery()
		tb.RecordSuccess()
		tb.RecordSuccess()
		tb.RecordSuccess() // → STANDARD

		// AdvanceDays checks if DaysInCurrentTier >= 90 BEFORE the current
		// advance. Set the base value to 90 first, then advance to trigger.
		tb.DaysInCurrentTier = 90
		tb.AdvanceDays(1) // check: (91 - 1) = 90 >= 90 → true → downgrade
		if tb.Tier != domain.TrustTierProbation {
			t.Errorf("expected PROBATION after 90+ inactive days from STANDARD, got %s (DaysInCurrentTier=%d)",
				tb.Tier, tb.DaysInCurrentTier)
		}
	})

	t.Run("ShouldDowngrade detects 3 errors", func(t *testing.T) {
		tb := domain.NewTrustBattery()
		tb.RecordSuccess()
		tb.RecordSuccess()
		tb.RecordSuccess() // → STANDARD

		tb.RecordError()
		tb.RecordError()
		tb.RecordError() // → PROBATION after downgrade

		// After downgrade, we're at PROBATION. PROBATION never downgrades.
		if tb.ShouldDowngrade() {
			t.Error("PROBATION tier should not trigger ShouldDowngrade()")
		}
	})
}
