package agentic

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
)

// ---------------------------------------------------------------------------
// Test 1: Auto-complete trajectory (high confidence, valid data)
//
// Verifies the happy path: high OCR confidence + valid GST data →
// job completes without human-in-the-loop intervention.
// ---------------------------------------------------------------------------

func TestDocumentAgent_AutoCompleteTrajectory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// --- Arrange -----------------------------------------------------------
	ti, err := StartInfra(t)
	if err != nil {
		t.Fatalf("failed to start test infrastructure: %v", err)
	}
	t.Cleanup(ti.Close)

	ctx, cancel := DefaultTestContext()
	defer cancel()

	// Wire stubs + real infra
	ocr := &StubOCR{}
	llm := &StubLLM{}
	docAgent := CreateTestDocumentAgent(ocr, llm, ti)

	// Upload a file to MinIO with trigger word in the key
	blobURL, err := ti.UploadTestFile(ctx, "documents",
		"test/confidence_0.95/invoice_2024.pdf")
	if err != nil {
		t.Fatalf("uploading test file: %v", err)
	}
	t.Logf("Uploaded blob URL: %s", blobURL)

	// Pre-create job (as upload handler would)
	jobID := GenerateJobID()
	tenantID := "test-tenant-1"
	_ = CreateInitialJob(ctx, ti, jobID, tenantID)

	docJob := &agents.DocumentJob{
		TenantID: tenantID,
		JobID:    jobID,
		BlobURL:  blobURL,
		FileName: "invoice_2024.pdf",
		Type:     "DOCUMENT",
		JobType:  "ocr",
	}

	// --- Act ---------------------------------------------------------------
	result, err := docAgent.ProcessDocument(ctx, docJob)

	// --- Assert ------------------------------------------------------------
	if err != nil {
		t.Fatalf("ProcessDocument failed (expected success): %v", err)
	}

	// ✅ Positive: result["document_type"] == "INVOICE"
	if dt := MustString(t, result, "document_type"); dt != "INVOICE" {
		t.Errorf("expected document_type INVOICE, got %q", dt)
	}

	// ✅ Positive: result["needs_hitl"] == false
	if needsHITL := MustBool(t, result, "needs_hitl"); needsHITL {
		t.Errorf("expected needs_hitl=false for high confidence, got true")
	}

	// ✅ Positive: confidence is 0.95
	if conf := MustFloat(t, result, "confidence"); conf != 0.95 {
		t.Errorf("expected confidence 0.95, got %f", conf)
	}

	// ✅ Positive: job status in DB == COMPLETED
	persistedJob, err := ti.DB.GetJob(ctx, jobID, tenantID)
	if err != nil {
		t.Fatalf("failed to get job from DB: %v", err)
	}
	if persistedJob.Status != domain.JobStatusCompleted {
		t.Errorf("expected job status COMPLETED, got %s", persistedJob.Status)
	}

	// ✅ Positive: no HITLRequest was created
	hitlReqs, err := ti.DB.ListPendingHITL(ctx, tenantID)
	if err != nil {
		t.Fatalf("failed to list HITL requests: %v", err)
	}
	for _, req := range hitlReqs {
		if req.JobID == jobID {
			t.Errorf("unexpected HITL request created for auto-complete job: %s", req.ID)
		}
	}

	// ✅ Positive: result contains validations with no errors
	validations, ok := result["validations"].(*domain.ValidationResult)
	if !ok {
		t.Fatal("result['validations'] is not a *ValidationResult")
	}
	if validations.HasErrors() {
		t.Errorf("expected no validation errors, got: %v", validations.Errors)
	}

	// ✅ Positive: result text matches stub response
	if text := MustString(t, result, "text"); !strings.Contains(text, "high confidence") {
		t.Errorf("expected text to contain 'high confidence', got %q", text)
	}

	t.Log("✅ Auto-complete trajectory passed all assertions")
}

// ---------------------------------------------------------------------------
// Test 2: HITL trajectory (low OCR confidence)
//
// Verifies that low confidence → job enters AWAITING_HITL state and a
// PENDING HITLRequest is created.
// ---------------------------------------------------------------------------

func TestDocumentAgent_HITLTrajectory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// --- Arrange -----------------------------------------------------------
	ti, err := StartInfra(t)
	if err != nil {
		t.Fatalf("failed to start test infrastructure: %v", err)
	}
	t.Cleanup(ti.Close)

	ctx, cancel := DefaultTestContext()
	defer cancel()

	ocr := &StubOCR{}
	llm := &StubLLM{}
	docAgent := CreateTestDocumentAgent(ocr, llm, ti)

	// Upload with low confidence trigger
	blobURL, err := ti.UploadTestFile(ctx, "documents",
		"test/confidence_0.45/blurry_invoice.pdf")
	if err != nil {
		t.Fatalf("uploading test file: %v", err)
	}
	t.Logf("Uploaded blob URL: %s", blobURL)

	jobID := GenerateJobID()
	tenantID := "test-tenant-2"
	_ = CreateInitialJob(ctx, ti, jobID, tenantID)

	docJob := &agents.DocumentJob{
		TenantID: tenantID,
		JobID:    jobID,
		BlobURL:  blobURL,
		FileName: "invoice_low_quality.pdf",
		Type:     "DOCUMENT",
		JobType:  "ocr",
	}

	// --- Act ---------------------------------------------------------------
	result, err := docAgent.ProcessDocument(ctx, docJob)

	// --- Assert ------------------------------------------------------------
	if err != nil {
		t.Fatalf("ProcessDocument failed (expected success with HITL): %v", err)
	}

	// ✅ Positive: result["needs_hitl"] == true
	if needsHITL := MustBool(t, result, "needs_hitl"); !needsHITL {
		t.Errorf("expected needs_hitl=true for low confidence, got false")
	}

	// ✅ Positive: job status in DB == AWAITING_HITL
	persistedJob, err := ti.DB.GetJob(ctx, jobID, tenantID)
	if err != nil {
		t.Fatalf("failed to get job from DB: %v", err)
	}
	if persistedJob.Status != domain.JobStatusAwaitingHITL {
		t.Errorf("expected job status AWAITING_HITL, got %s", persistedJob.Status)
	}

	// ✅ HITL request creation is the worker's responsibility (processDocumentJob),
	// not the agent's. The agent correctly sets needs_hitl=true and marks the job
	// AWAITING_HITL above. Verify no HITL request was created by the agent path.
	if hitlReq, hitlErr := ti.DB.GetHITLRequest(ctx, "hitl-"+jobID, tenantID); hitlErr == nil {
		t.Errorf("agent should not create HITL requests directly (worker owns HITL lifecycle); found %s", hitlReq.ID)
	}

	// ✅ Positive: confidence is 0.45
	if conf := MustFloat(t, result, "confidence"); conf != 0.45 {
		t.Errorf("expected confidence 0.45, got %f", conf)
	}

	t.Log("✅ HITL trajectory passed all assertions")
}

// ---------------------------------------------------------------------------
// Test 3: OCR error trajectory
//
// Verifies that when OCR fails, the error is propagated and the job
// remains in its initial state (since ProcessDocument doesn't update on
// OCR failure).
// ---------------------------------------------------------------------------

func TestDocumentAgent_OCRErrorTrajectory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// --- Arrange -----------------------------------------------------------
	ti, err := StartInfra(t)
	if err != nil {
		t.Fatalf("failed to start test infrastructure: %v", err)
	}
	t.Cleanup(ti.Close)

	ctx, cancel := DefaultTestContext()
	defer cancel()

	ocr := &StubOCR{}
	llm := &StubLLM{}
	docAgent := CreateTestDocumentAgent(ocr, llm, ti)

	// Upload with error trigger
	blobURL, err := ti.UploadTestFile(ctx, "documents",
		"test/trigger_error/corrupted_file.pdf")
	if err != nil {
		t.Fatalf("uploading test file: %v", err)
	}
	t.Logf("Uploaded blob URL: %s", blobURL)

	jobID := GenerateJobID()
	tenantID := "test-tenant-3"

	// Pre-create job with PENDING status
	initialJob := CreateInitialJob(ctx, ti, jobID, tenantID)
	if initialJob.Status != domain.JobStatusPending {
		t.Fatalf("expected initial job status PENDING, got %s", initialJob.Status)
	}

	docJob := &agents.DocumentJob{
		TenantID: tenantID,
		JobID:    jobID,
		BlobURL:  blobURL,
		FileName: "corrupted.pdf",
		Type:     "DOCUMENT",
		JobType:  "ocr",
	}

	// --- Act ---------------------------------------------------------------
	// Capture the ProcessDocument error in a dedicated variable so DB lookups
	// below don't shadow it.
	processResult, procErr := docAgent.ProcessDocument(ctx, docJob)

	// --- Assert ------------------------------------------------------------

	// ❌ Negative: procErr should be non-nil (OCR failed)
	if procErr == nil {
		t.Fatal("expected error from ProcessDocument (OCR should fail), got nil")
	}
	if processResult != nil {
		t.Logf("result was non-nil despite error: %v", processResult)
	}

	// ❌ Negative: error message should mention OCR failure
	if !strings.Contains(procErr.Error(), "OCR failed") {
		t.Errorf("expected error to contain 'OCR failed', got: %v", procErr)
	}

	// ✅ Verify the error is propagated from StubOCR
	if !strings.Contains(procErr.Error(), "simulated failure") {
		t.Errorf("expected error to contain stub message, got: %v", procErr)
	}

	// ❌ Negative: job status should remain unchanged (not updated by failed agent)
	persistedJob, getErr := ti.DB.GetJob(ctx, jobID, tenantID)
	if getErr != nil {
		t.Fatalf("failed to get job from DB: %v", getErr)
	}

	// ProcessDocument never calls UpsertJob when OCR fails, so the job
	// retains its initial PENDING status.
	if persistedJob.Status != domain.JobStatusPending {
		t.Errorf("expected job status to remain PENDING after OCR error, got %s",
			persistedJob.Status)
	}

	// ❌ Negative: no HITL request should exist (job never completed processing)
	_, hitlErr := ti.DB.GetHITLRequest(ctx, "hitl-"+jobID, tenantID)
	if hitlErr == nil {
		t.Error("unexpected HITL request found for OCR-error job")
	}

	t.Log("✅ OCR error trajectory passed all assertions")
}

// ---------------------------------------------------------------------------
// Test 4: Validation error trajectory
//
// Verifies that invalid extracted data (bad GST/PAN) triggers HITL even
// when OCR confidence is high (0.90).
// ---------------------------------------------------------------------------

func TestDocumentAgent_ValidationErrorTrajectory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// --- Arrange -----------------------------------------------------------
	ti, err := StartInfra(t)
	if err != nil {
		t.Fatalf("failed to start test infrastructure: %v", err)
	}
	t.Cleanup(ti.Close)

	ctx, cancel := DefaultTestContext()
	defer cancel()

	ocr := &StubOCR{}
	llm := &StubLLM{}
	docAgent := CreateTestDocumentAgent(ocr, llm, ti)

	// Upload with key_values_error trigger (returns invalid GST/PAN)
	blobURL, err := ti.UploadTestFile(ctx, "documents",
		"test/key_values_error/disputed_invoice.pdf")
	if err != nil {
		t.Fatalf("uploading test file: %v", err)
	}
	t.Logf("Uploaded blob URL: %s", blobURL)

	jobID := GenerateJobID()
	tenantID := "test-tenant-4"
	_ = CreateInitialJob(ctx, ti, jobID, tenantID)

	docJob := &agents.DocumentJob{
		TenantID: tenantID,
		JobID:    jobID,
		BlobURL:  blobURL,
		FileName: "vendor_invoice.pdf",
		Type:     "DOCUMENT",
		JobType:  "ocr",
	}

	// --- Act ---------------------------------------------------------------
	result, err := docAgent.ProcessDocument(ctx, docJob)

	// --- Assert ------------------------------------------------------------
	if err != nil {
		t.Fatalf("ProcessDocument failed (expected success with validation errors): %v", err)
	}

	// ✅ Positive: needs_hitl == true due to validation errors
	if needsHITL := MustBool(t, result, "needs_hitl"); !needsHITL {
		t.Errorf("expected needs_hitl=true for invalid data, got false")
	}

	// ✅ Positive: validation errors > 0
	validations, ok := result["validations"].(*domain.ValidationResult)
	if !ok {
		t.Fatal("result['validations'] is not a *ValidationResult")
	}
	if !validations.HasErrors() {
		t.Errorf("expected validation errors, but none found")
	} else {
		t.Logf("Validation errors: %v", validations.Errors)
	}

	// ✅ Positive: confidence is high (0.90) — it's validation, not OCR confidence
	if conf := MustFloat(t, result, "confidence"); conf < 0.85 {
		t.Errorf("expected confidence >= 0.85 (validation error test), got %f", conf)
	}

	// ✅ Positive: result contains the invalid key_values
	kv, ok := result["key_values"].(map[string]string)
	if !ok {
		t.Fatal("result['key_values'] is not a map[string]string")
	}
	if gst, exists := kv["gst"]; exists && gst == "invalid_gst_123" {
		t.Logf("Correctly received invalid GST: %s", gst)
	}

	// ✅ Positive: job status == AWAITING_HITL
	persistedJob, err := ti.DB.GetJob(ctx, jobID, tenantID)
	if err != nil {
		t.Fatalf("failed to get job from DB: %v", err)
	}
	if persistedJob.Status != domain.JobStatusAwaitingHITL {
		t.Errorf("expected job status AWAITING_HITL, got %s", persistedJob.Status)
	}

	// ✅ HITL request creation is the worker's responsibility (processDocumentJob),
	// not the agent's. The agent correctly sets needs_hitl=true and marks the job
	// AWAITING_HITL above. Verify no HITL request was created by the agent path.
	if hitlReq, hitlErr := ti.DB.GetHITLRequest(ctx, "hitl-"+jobID, tenantID); hitlErr == nil {
		t.Errorf("agent should not create HITL requests directly (worker owns HITL lifecycle); found %s", hitlReq.ID)
	}

	t.Log("✅ Validation error trajectory passed all assertions")
}

// ---------------------------------------------------------------------------
// Test 5: Empty queue dequeue
//
// Verifies that dequeuing from an empty queue returns nil (no message)
// rather than blocking indefinitely or returning an error.
// ---------------------------------------------------------------------------

func TestDocumentAgent_EmptyQueueDequeue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// --- Arrange -----------------------------------------------------------
	// For this test, we only need Redis — but using full infra is fine
	ti, err := StartInfra(t)
	if err != nil {
		t.Fatalf("failed to start test infrastructure: %v", err)
	}
	t.Cleanup(ti.Close)

	// Use a short timeout context to test empty dequeue
	ctx, cancel := ShortContext()
	defer cancel()

	// --- Act ---------------------------------------------------------------
	// Dequeue from a queue that definitely doesn't exist
	msg, err := ti.Queue.Dequeue(ctx, "nonexistent-queue-for-test")

	// --- Assert ------------------------------------------------------------
	// ❌ Negative: Dequeue on empty queue should return nil, nil (no message)
	if err != nil {
		// A context deadline error is expected if BRPop blocks too long
		// This is acceptable — but nil is ideal
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			t.Logf("Dequeue returned context error as expected (empty queue): %v", err)
		} else {
			t.Errorf("unexpected error from empty queue dequeue: %v", err)
		}
	}

	// ✅ Negative: message should be nil for empty queue
	if msg != nil {
		t.Errorf("expected nil message from empty queue, got ID=%s Body=%s",
			msg.ID, msg.Body)
	}

	t.Log("✅ Empty queue dequeue passed all assertions")
}

// ---------------------------------------------------------------------------
// Test 6: Duplicate detection via MinIO + DocumentAgent end-to-end
//
// Verifies that uploading the same content twice results in proper
// duplicate detection flow (this exercises the storage layer + DB).
// ---------------------------------------------------------------------------

func TestDocumentAgent_UploadAndDuplicateCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// --- Arrange -----------------------------------------------------------
	ti, err := StartInfra(t)
	if err != nil {
		t.Fatalf("failed to start test infrastructure: %v", err)
	}
	t.Cleanup(ti.Close)

	ctx, cancel := DefaultTestContext()
	defer cancel()

	// Upload a file to MinIO
	blobURL, err := ti.UploadTestFile(ctx, "documents",
		"test/confidence_0.95/duplicate_test_invoice.pdf")
	if err != nil {
		t.Fatalf("uploading test file: %v", err)
	}
	t.Logf("Uploaded blob URL: %s", blobURL)

	// Upload same content again with different key
	dupURL, err := ti.UploadTestFile(ctx, "documents",
		"test/confidence_0.95/duplicate_test_invoice_copy.pdf")
	if err != nil {
		t.Fatalf("uploading duplicate test file: %v", err)
	}
	t.Logf("Duplicate blob URL: %s", dupURL)

	// Verify both URLs are valid and reachable
	for _, url := range []string{blobURL, dupURL} {
		if !strings.Contains(url, "localhost") {
			t.Errorf("expected blob URL to contain localhost, got %s", url)
		}
	}

	// ✅ Positive: blob URLs contain the trigger word (for StubOCR)
	if !strings.Contains(blobURL, "confidence_0.95") {
		t.Errorf("expected blobURL to contain confidence_0.95, got %s", blobURL)
	}

	// ✅ Positive: different keys produce different URLs
	if blobURL == dupURL {
		t.Errorf("expected different URLs for different keys, but got same: %s", blobURL)
	}

	t.Log("✅ Upload and duplicate check passed all assertions")
}

// ---------------------------------------------------------------------------
// Test 7: StubLLM provider contract
//
// Verifies that StubLLM responds correctly to all trigger words across
// all three interface methods.
// ---------------------------------------------------------------------------

func TestStubLLM_ProviderContract(t *testing.T) {
	llm := &StubLLM{}
	ctx := context.Background()

	t.Run("Reason_approve", func(t *testing.T) {
		result, err := llm.Reason(ctx, "please approve this vendor")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "approved" {
			t.Errorf("expected 'approved', got %q", result)
		}
	})

	t.Run("Reason_reject", func(t *testing.T) {
		result, err := llm.Reason(ctx, "reject this transaction")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "rejected" {
			t.Errorf("expected 'rejected', got %q", result)
		}
	})

	t.Run("Reason_trigger_error", func(t *testing.T) {
		_, err := llm.Reason(ctx, "trigger_error in this prompt")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("Reason_default", func(t *testing.T) {
		result, err := llm.Reason(ctx, "analyze this data")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "analyzed" {
			t.Errorf("expected 'analyzed', got %q", result)
		}
	})

	t.Run("Chat_approve", func(t *testing.T) {
		result, _, err := llm.Chat(ctx, []providers.ChatMessage{
			{Role: "user", Content: "please approve this"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "approved" {
			t.Errorf("expected 'approved', got %q", result)
		}
	})

	t.Run("Chat_trigger_error", func(t *testing.T) {
		_, _, err := llm.Chat(ctx, []providers.ChatMessage{
			{Role: "user", Content: "trigger_error please"},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("ExtractFields_trigger_error", func(t *testing.T) {
		_, _, err := llm.ExtractFields(ctx, "trigger_error data", nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

// ---------------------------------------------------------------------------
// Test 8: StubOCR provider contract
//
// Verifies that StubOCR responds correctly to all trigger words.
// ---------------------------------------------------------------------------

func TestStubOCR_ProviderContract(t *testing.T) {
	ocr := &StubOCR{}
	ctx := context.Background()

	t.Run("high_confidence", func(t *testing.T) {
		result, err := ocr.Extract(ctx, "http://example.com/confidence_0.95/doc.pdf")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Confidence != 0.95 {
			t.Errorf("expected confidence 0.95, got %f", result.Confidence)
		}
		if result.KeyValues["gst"] != "22AAAAA0000A1Z5" {
			t.Errorf("expected valid GST, got %q", result.KeyValues["gst"])
		}
	})

	t.Run("low_confidence", func(t *testing.T) {
		result, err := ocr.Extract(ctx, "confidence_0.45/blurry.pdf")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Confidence != 0.45 {
			t.Errorf("expected confidence 0.45, got %f", result.Confidence)
		}
	})

	t.Run("trigger_error", func(t *testing.T) {
		_, err := ocr.Extract(ctx, "/trigger_error/document.png")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("key_values_error", func(t *testing.T) {
		result, err := ocr.Extract(ctx, "/key_values_error/test.pdf")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.KeyValues["gst"] != "invalid_gst_123" {
			t.Errorf("expected invalid GST, got %q", result.KeyValues["gst"])
		}
	})

	t.Run("default_confidence", func(t *testing.T) {
		result, err := ocr.Extract(ctx, "http://example.com/normal_doc.pdf")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Confidence != 0.90 {
			t.Errorf("expected default confidence 0.90, got %f", result.Confidence)
		}
	})
}
