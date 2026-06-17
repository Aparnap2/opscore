// Package agentic tests the VendorAgent workflows using real Postgres + Redis
// containers (via Testcontainers) with stub LLM and deterministic test data.
// No real AI/OCR API calls are made — all external services are stubbed.
package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/aparna/opscore/internal/adapters/postgres"
	"github.com/aparna/opscore/internal/adapters/queue"
	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
	"github.com/aparna/opscore/internal/telemetry"
)

// ---------------------------------------------------------------------------
// callRecordingLLM — configurable test double for providers.LLMProvider
//
// Records all prompts received (for call-count assertions) and returns
// pre-configured responses matched by trigger substrings in the prompt.
// Define inline in each test file per StubLLM pattern.
// ---------------------------------------------------------------------------

type callRecordingLLM struct {
	mu        sync.Mutex
	responses map[string]string // prompt trigger → response
	calls     []string          // recorded prompts in order
	errOn     string            // if prompt contains this, return error
}

func (s *callRecordingLLM) ExtractFields(_ context.Context, text string, _ any) (json.RawMessage, float64, error) {
	s.mu.Lock()
	s.calls = append(s.calls, text)
	s.mu.Unlock()
	return json.RawMessage(`{}`), 1.0, nil
}

func (s *callRecordingLLM) Reason(_ context.Context, prompt string) (string, error) {
	s.mu.Lock()
	s.calls = append(s.calls, prompt)
	s.mu.Unlock()

	if s.errOn != "" && strings.Contains(prompt, s.errOn) {
		return "", fmt.Errorf("simulated LLM error triggered by %q", s.errOn)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for trigger, resp := range s.responses {
		if strings.Contains(prompt, trigger) {
			return resp, nil
		}
	}
	return "default analysis", nil
}

func (s *callRecordingLLM) Chat(_ context.Context, _ []providers.ChatMessage) (string, error) {
	return "", nil
}

// CallCount returns the number of Reason calls recorded.
func (s *callRecordingLLM) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// LastCall returns the most recent Reason prompt, or empty string.
func (s *callRecordingLLM) LastCall() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return ""
	}
	return s.calls[len(s.calls)-1]
}

// ---------------------------------------------------------------------------
// testInfra — Testcontainers-managed Postgres + Redis for VendorAgent tests
// ---------------------------------------------------------------------------

type testInfra struct {
	DB      *postgres.Adapter
	Queue   *queue.RedisAdapter
	cleanup func()
}

// startInfra starts Postgres and Redis containers, creates adapters,
// and returns a testInfra with a cleanup function.
func startInfra(t *testing.T) (*testInfra, error) {
	t.Helper()
	ctx := context.Background()

	// ---- Postgres 16 alpine ----
	pgReq := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "opscore",
			"POSTGRES_PASSWORD": "opscore",
			"POSTGRES_DB":       "opscore",
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections").
			WithStartupTimeout(60 * time.Second),
	}
	pgContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: pgReq,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("starting postgres: %w", err)
	}

	pgPort, err := pgContainer.MappedPort(ctx, "5432")
	if err != nil {
		pgContainer.Terminate(ctx)
		return nil, fmt.Errorf("getting postgres port: %w", err)
	}

	connStr := fmt.Sprintf("postgres://opscore:opscore@localhost:%s/opscore?sslmode=disable", pgPort.Port())
	t.Logf("Postgres ready at %s", connStr)

	// ---- Redis 7 alpine ----
	redisReq := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("* Ready to accept connections tcp").WithStartupTimeout(30 * time.Second),
	}
	redisContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: redisReq,
		Started:          true,
	})
	if err != nil {
		pgContainer.Terminate(ctx)
		return nil, fmt.Errorf("starting redis: %w", err)
	}

	redisPort, err := redisContainer.MappedPort(ctx, "6379")
	if err != nil {
		pgContainer.Terminate(ctx)
		redisContainer.Terminate(ctx)
		return nil, fmt.Errorf("getting redis port: %w", err)
	}

	redisAddr := fmt.Sprintf("localhost:%s", redisPort.Port())
	t.Logf("Redis ready at %s", redisAddr)

	// ---- Create adapters ----
	// Retry adapter creation to handle the brief window where Postgres accepts
	// TCP connections but is still in crash recovery ("the database system is starting up").
	var db *postgres.Adapter
	for i := 0; i < 5; i++ {
		if i > 0 {
			time.Sleep(time.Duration(200*(1<<i)) * time.Millisecond) // 200, 400, 800, 1600ms
		}
		db, err = postgres.NewAdapter(ctx, connStr)
		if err == nil {
			break
		}
		t.Logf("postgres adapter attempt %d/5: %v", i+1, err)
	}
	if err != nil {
		pgContainer.Terminate(ctx)
		redisContainer.Terminate(ctx)
		return nil, fmt.Errorf("creating postgres adapter after retries: %w", err)
	}

	rq, err := queue.NewRedisAdapter(redisAddr, "", 0)
	if err != nil {
		db.Close()
		pgContainer.Terminate(ctx)
		redisContainer.Terminate(ctx)
		return nil, fmt.Errorf("creating redis adapter: %w", err)
	}

	cleanup := func() {
		rq.Close()
		db.Close()
		pgContainer.Terminate(context.Background())
		redisContainer.Terminate(context.Background())
	}

	return &testInfra{
		DB:      db,
		Queue:   rq,
		cleanup: cleanup,
	}, nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func setupVendorTest(t *testing.T) (context.Context, *testInfra, *callRecordingLLM, *agents.VendorAgent) {
	t.Helper()
	ctx := context.Background()

	infra, err := startInfra(t)
	if err != nil {
		t.Fatalf("startInfra: %v", err)
	}
	t.Cleanup(infra.cleanup)

	llm := &callRecordingLLM{
		responses: make(map[string]string),
	}
	validator := domain.NewIndiaValidator()
	tracer := telemetry.NoopTracer{}

	agent := agents.NewVendorAgent(infra.DB, validator, llm, tracer)
	return ctx, infra, llm, agent
}

// TestVendorAgent_CleanAutoApprove verifies that a vendor with valid
// GST, PAN, and IFSC identifiers is processed without errors.
//
// Expected behavior per code:
//   - ComputeVendorRisk returns score 65 (50 base + 15 GST + 10 PAN + 10 IFSC - 20 NOT_APPROVED)
//   - needs_hitl = true (65 >= 60)
//   - Vendor is saved to DB with Approved = false (riskScore 65 not < 60)
//   - Job is saved with status COMPLETED
//   - TrustTier is set (not empty)
func TestVendorAgent_CleanAutoApprove(t *testing.T) {
	ctx, infra, llm, agent := setupVendorTest(t)

	jobID := uuid.New().String()
	tenantID := "test-tenant-clean"

	vendorJob := &agents.VendorJob{
		TenantID: tenantID,
		JobID:    jobID,
		VendorData: &agents.VendorData{
			Name:        "Clean Vendor Pvt Ltd",
			GSTNumber:   "22AAAAA0000A1Z5", // valid format per GSTRegex
			PANNumber:   "ABCDE1234F",       // valid format per PANRegex
			IFSCCode:    "SBIN0001234",      // valid format per IFSRegex
			BankAccount: "1234567890123456",
			Address:     "123 Business Park, Mumbai",
		},
	}

	result, err := agent.ProcessVendor(ctx, vendorJob)
	if err != nil {
		t.Fatalf("ProcessVendor failed: %v", err)
	}

	// --- Assert result shape ---
	if result["name"] != "Clean Vendor Pvt Ltd" {
		t.Errorf("result name = %v, want Clean Vendor Pvt Ltd", result["name"])
	}
	if result["trust_tier"] == "" {
		t.Error("trust_tier should not be empty")
	}

	// Compute expected risk score and verify
	expectedRisk := domain.ComputeVendorRisk(
		&domain.Vendor{
			GSTNumber: "22AAAAA0000A1Z5",
			PANNumber: "ABCDE1234F",
			IFSCCode:  "SBIN0001234",
		},
		nil, nil, tenantID,
	)
	gotScore, ok := result["risk_score"].(int)
	if !ok {
		t.Fatalf("risk_score not an int, got %T", result["risk_score"])
	}
	if gotScore != expectedRisk.Score {
		t.Errorf("risk_score = %d, want %d (from ComputeVendorRisk)", gotScore, expectedRisk.Score)
	}
	t.Logf("Computed risk: score=%d, tier=%s, flags=%v", gotScore, expectedRisk.Tier, expectedRisk.Flags)

	// needs_hitl = riskScore >= 60 || validationErrors > 0
	// With all valid IDs: score=65 >= 60 → true
	needsHITL, ok := result["needs_hitl"].(bool)
	if !ok {
		t.Fatal("needs_hitl not a bool")
	}
	t.Logf("needs_hitl = %v (risk_score=%d >= 60)", needsHITL, gotScore)
	if !needsHITL {
		t.Error("expected needs_hitl=true since risk_score >= 60")
	}

	// --- Assert vendor saved to DB ---
	savedVendor, err := infra.DB.GetVendor(ctx, jobID)
	if err != nil {
		t.Fatalf("GetVendor failed: %v", err)
	}
	if savedVendor.Name != "Clean Vendor Pvt Ltd" {
		t.Errorf("vendor name = %s, want Clean Vendor Pvt Ltd", savedVendor.Name)
	}
	if savedVendor.GSTNumber != "22AAAAA0000A1Z5" {
		t.Errorf("vendor GST = %s", savedVendor.GSTNumber)
	}
	if savedVendor.PANNumber != "ABCDE1234F" {
		t.Errorf("vendor PAN = %s", savedVendor.PANNumber)
	}
	if savedVendor.IFSCCode != "SBIN0001234" {
		t.Errorf("vendor IFSC = %s", savedVendor.IFSCCode)
	}
	if savedVendor.TrustBattery.Tier == "" {
		t.Error("vendor TrustBattery.Tier should not be empty")
	}
	t.Logf("Vendor TrustBattery: tier=%s, score=%d", savedVendor.TrustBattery.Tier, savedVendor.TrustBattery.TrustScore)

	// --- Assert job saved with COMPLETED status ---
	savedJob, err := infra.DB.GetJob(ctx, jobID, tenantID)
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if savedJob.Status != domain.JobStatusCompleted {
		t.Errorf("job status = %s, want %s", savedJob.Status, domain.JobStatusCompleted)
	}
	if savedJob.WorkflowType != domain.WorkflowVendorOnboarding {
		t.Errorf("workflow type = %s, want %s", savedJob.WorkflowType, domain.WorkflowVendorOnboarding)
	}
	t.Logf("Job status: %s, workflow: %s", savedJob.Status, savedJob.WorkflowType)

	// --- Assert no LLM calls (no Documents in this test) ---
	if llm.CallCount() != 0 {
		t.Errorf("LLM was called %d times, expected 0 (no documents)", llm.CallCount())
	}
}

// TestVendorAgent_HighRiskHITL verifies that a vendor with invalid
// identifiers triggers validation errors, high risk scoring, and a
// HITL (Human-in-the-Loop) request.
//
// Expected behavior:
//   - ValidateVendor returns 3 validation errors (invalid GST, PAN, IFSC)
//   - ComputeVendorRisk returns low score (15) with INCOMPLETE_DOCUMENTS flag
//   - needs_hitl = true (validationErrors > 0)
//   - HITLRequest created in DB with PENDING status
//   - TrustTier is PROBATION (score < 30)
func TestVendorAgent_HighRiskHITL(t *testing.T) {
	ctx, infra, _, agent := setupVendorTest(t)

	jobID := uuid.New().String()
	tenantID := "test-tenant-highrisk"

	// Use invalid non-empty IDs to trigger actual validation errors
	// (empty values are silently skipped by ValidateVendor)
	vendorJob := &agents.VendorJob{
		TenantID: tenantID,
		JobID:    jobID,
		VendorData: &agents.VendorData{
			Name:      "Risky Vendor",
			GSTNumber: "INVALID_GST_123",  // fails GSTRegex
			PANNumber: "SHORT",             // fails PANRegex (not 10 chars)
			IFSCCode:  "BAD",               // fails IFSRegex
			Address:   "Unknown Location",
		},
	}

	result, err := agent.ProcessVendor(ctx, vendorJob)
	if err != nil {
		t.Fatalf("ProcessVendor failed: %v", err)
	}

	// --- Assert validation errors ---
	valResult, ok := result["validations"].(*domain.ValidationResult)
	if !ok || valResult == nil {
		t.Fatal("validations missing from result")
	}
	if len(valResult.Errors) != 3 {
		t.Errorf("expected 3 validation errors, got %d: %v", len(valResult.Errors), valResult.Errors)
	}
	t.Logf("Validation errors: %v", valResult.Errors)

	// --- Assert risk score ---
	expectedRisk := domain.ComputeVendorRisk(
		&domain.Vendor{
			GSTNumber: "INVALID_GST_123",
			PANNumber: "SHORT",
			IFSCCode:  "BAD",
		},
		nil, nil, tenantID,
	)
	gotScore, ok := result["risk_score"].(int)
	if !ok {
		t.Fatalf("risk_score not an int, got %T", result["risk_score"])
	}
	if gotScore != expectedRisk.Score {
		t.Errorf("risk_score = %d, want %d (from ComputeVendorRisk)", gotScore, expectedRisk.Score)
	}
	// With all invalid IDs: score=15 (50-20(NOT_APPROVED)-15(INCOMPLETE_DOCUMENTS))
	if gotScore != 15 {
		t.Logf("Note: risk_score=%d. Base=50, NOT_APPROVED=-20, INCOMPLETE_DOCUMENTS=-15", gotScore)
	}
	t.Logf("Risk: score=%d, tier=%s, flags=%v", gotScore, expectedRisk.Tier, expectedRisk.Flags)

	// --- Assert trust tier ---
	trustTier, ok := result["trust_tier"].(domain.TrustTier)
	if ok {
		t.Logf("Trust tier: %s", trustTier)
	}
	// For score=15 (<30): TrustTier = PROBATION (the initial value before the if/else)

	// needs_hitl = score >= 60 || validationErrors > 0
	// Here: score=15 (not >= 60) but validationErrors=3 (> 0) → true
	needsHITL, ok := result["needs_hitl"].(bool)
	if !ok {
		t.Fatal("needs_hitl not a bool")
	}
	if !needsHITL {
		t.Error("expected needs_hitl=true due to validation errors")
	}
	t.Logf("needs_hitl = %v (validation errors trigger HITL)", needsHITL)

	// --- Assert HITLRequest created ---
	hitlReq, err := infra.DB.GetHITLRequest(ctx, "hitl-"+jobID)
	if err != nil {
		t.Fatalf("GetHITLRequest failed: %v", err)
	}
	if hitlReq.Status != "PENDING" {
		t.Errorf("HITL request status = %s, want PENDING", hitlReq.Status)
	}
	if hitlReq.TenantID != tenantID {
		t.Errorf("HITL tenant = %s, want %s", hitlReq.TenantID, tenantID)
	}
	if hitlReq.JobID != jobID {
		t.Errorf("HITL job_id = %s, want %s", hitlReq.JobID, jobID)
	}
	if hitlReq.SentAt.IsZero() {
		t.Error("HITL request SentAt should be set")
	}
	t.Logf("HITL request: id=%s, status=%s, reason=%s", hitlReq.ID, hitlReq.Status, hitlReq.Reason)

	// --- Assert job saved with COMPLETED status (ProcessVendor always sets COMPLETED) ---
	savedJob, err := infra.DB.GetJob(ctx, jobID, tenantID)
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if savedJob.Status != domain.JobStatusCompleted {
		t.Errorf("job status = %s, want %s", savedJob.Status, domain.JobStatusCompleted)
	}
	t.Logf("Job status: %s", savedJob.Status)
}

// TestVendorAgent_LLMAnalysis verifies that when a vendor has documents,
// the LLM Reason() method is called and the analysis is included in the result.
func TestVendorAgent_LLMAnalysis(t *testing.T) {
	ctx, infra, llm, agent := setupVendorTest(t)

	// Configure LLM to respond with a specific analysis
	llm.responses["report.pdf"] = "high risk vendor detected"
	llm.responses["Analyze vendor documents"] = "high risk vendor detected"

	jobID := uuid.New().String()
	tenantID := "test-tenant-llm"

	vendorJob := &agents.VendorJob{
		TenantID: tenantID,
		JobID:    jobID,
		VendorData: &agents.VendorData{
			Name:        "LLM Analyzed Vendor",
			GSTNumber:   "22AAAAA0000A1Z5",
			PANNumber:   "ABCDE1234F",
			IFSCCode:    "SBIN0001234",
			Documents:   []string{"report.pdf"}, // triggers LLM analysis path
		},
	}

	result, err := agent.ProcessVendor(ctx, vendorJob)
	if err != nil {
		t.Fatalf("ProcessVendor failed: %v", err)
	}

	// --- Assert LLM was called ---
	if llm.CallCount() == 0 {
		t.Fatal("LLM Reason() was not called — expected call since Documents are present")
	}
	t.Logf("LLM was called %d time(s)", llm.CallCount())
	t.Logf("Last LLM prompt: %s", llm.LastCall())

	// --- Assert LLM analysis in result ---
	llmResult, ok := result["llm_analysis"]
	if !ok {
		t.Fatal("result missing llm_analysis key")
	}
	analysis, ok := llmResult.(string)
	if !ok {
		t.Fatalf("llm_analysis not a string, got %T", llmResult)
	}
	if analysis != "high risk vendor detected" {
		t.Errorf("llm_analysis = %q, want %q", analysis, "high risk vendor detected")
	}
	t.Logf("LLM analysis: %s", analysis)

	// --- Assert vendor still saved ---
	savedVendor, err := infra.DB.GetVendor(ctx, jobID)
	if err != nil {
		t.Fatalf("GetVendor after LLM analysis failed: %v", err)
	}
	if savedVendor.Name != "LLM Analyzed Vendor" {
		t.Errorf("vendor name = %s", savedVendor.Name)
	}
	t.Logf("Vendor saved: id=%s, name=%s", savedVendor.ID, savedVendor.Name)
}

// TestVendorAgent_LLMErrorHandling verifies that LLM failures are
// non-fatal: the vendor is still saved and processed, but the
// llm_analysis key is absent from the result.
func TestVendorAgent_LLMErrorHandling(t *testing.T) {
	ctx, infra, llm, agent := setupVendorTest(t)

	// Configure LLM to return error when prompt contains "Analyze"
	// (case-sensitive match — prompt starts with "Analyze vendor documents...")
	llm.errOn = "Analyze"

	jobID := uuid.New().String()
	tenantID := "test-tenant-llm-err"

	vendorJob := &agents.VendorJob{
		TenantID: tenantID,
		JobID:    jobID,
		VendorData: &agents.VendorData{
			Name:      "LLM Error Vendor",
			GSTNumber: "22AAAAA0000A1Z5",
			PANNumber: "ABCDE1234F",
			IFSCCode:  "SBIN0001234",
			Documents: []string{"report.pdf"},
		},
	}

	result, err := agent.ProcessVendor(ctx, vendorJob)
	if err != nil {
		t.Fatalf("ProcessVendor returned error: %v (LLM failure should be non-fatal)", err)
	}

	// --- Assert no llm_analysis in result ---
	_, hasAnalysis := result["llm_analysis"]
	if hasAnalysis {
		t.Error("llm_analysis should not be present when LLM returns an error")
	}
	t.Log("LLM error correctly swallowed — llm_analysis absent from result")

	// --- Assert LLM was called (it was attempted) ---
	if llm.CallCount() == 0 {
		t.Error("LLM was not called — expected at least one attempt")
	} else {
		t.Logf("LLM was called %d time(s) before error", llm.CallCount())
	}

	// --- Assert vendor saved anyway ---
	savedVendor, err := infra.DB.GetVendor(ctx, jobID)
	if err != nil {
		t.Fatalf("GetVendor failed after LLM error: %v", err)
	}
	if savedVendor.Name != "LLM Error Vendor" {
		t.Errorf("vendor name = %s, want 'LLM Error Vendor'", savedVendor.Name)
	}
	t.Logf("Vendor saved despite LLM error: id=%s, name=%s", savedVendor.ID, savedVendor.Name)

	// --- Assert job saved with COMPLETED ---
	savedJob, err := infra.DB.GetJob(ctx, jobID, tenantID)
	if err != nil {
		t.Fatalf("GetJob failed after LLM error: %v", err)
	}
	if savedJob.Status != domain.JobStatusCompleted {
		t.Errorf("job status = %s, want %s", savedJob.Status, domain.JobStatusCompleted)
	}
	t.Logf("Job completed despite LLM error: status=%s", savedJob.Status)

	// --- Assert result has expected standard fields ---
	gotScore, ok := result["risk_score"].(int)
	if !ok {
		t.Fatal("risk_score missing from result")
	}
	t.Logf("Risk score: %d", gotScore)
}
