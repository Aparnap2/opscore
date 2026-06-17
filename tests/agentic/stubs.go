package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
)

// ---------------------------------------------------------------------------
// StubOCR — deterministic test double for providers.OCRProvider
// ---------------------------------------------------------------------------

// StubOCR implements providers.OCRProvider with trigger-word-based responses.
// The blobURL is inspected for keywords that determine the returned result:
//
//	"confidence_0.45"   → Confidence: 0.45  (triggers HITL path)
//	"confidence_0.95"   → Confidence: 0.95  (auto-complete path)
//	"trigger_error"     → returns error     (OCR unavailable)
//	"key_values_error"  → returns invalid GST in KeyValues
//	default             → Confidence: 0.90
type StubOCR struct{}

var _ providers.OCRProvider = (*StubOCR)(nil)

func (s *StubOCR) Extract(_ context.Context, blobURL string) (*providers.OCRResult, error) {
	// Error trigger — OCR call fails entirely
	if strings.Contains(blobURL, "trigger_error") {
		return nil, fmt.Errorf("OCR API unavailable: simulated failure")
	}

	result := &providers.OCRResult{
		Text:       "stub extracted text",
		Language:   "en",
		Provider:   "stub-ocr",
		Confidence: 0.90, // default
		KeyValues: map[string]string{
			"amount": "500",
		},
	}

	switch {
	case strings.Contains(blobURL, "confidence_0.45"):
		// Low confidence — triggers HITL
		result.Confidence = 0.45
		result.KeyValues = map[string]string{"amount": "1000"}
		result.Text = "low confidence extraction"

	case strings.Contains(blobURL, "confidence_0.95"):
		// High confidence — auto-complete path
		result.Confidence = 0.95
		result.KeyValues = map[string]string{
			"amount": "1000",
			"gst":    "22AAAAA0000A1Z5", // valid GST per GSTRegex
		}
		result.Text = "high confidence extraction"

	case strings.Contains(blobURL, "key_values_error"):
		// Returns invalid data to trigger validation errors
		result.Confidence = 0.90
		result.KeyValues = map[string]string{
			"gst": "invalid_gst_123", // invalid format
			"pan": "short",           // invalid PAN
		}
		result.Text = "extraction with validation errors"
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// StubLLM — deterministic test double for providers.LLMProvider
// ---------------------------------------------------------------------------

// StubLLM implements providers.LLMProvider with trigger-word-based responses.
// The prompt/content is inspected for keywords:
//
//	"approve"       → returns "approved"
//	"reject"        → returns "rejected"
//	"trigger_error" → returns error
//	default         → returns "analyzed"
type StubLLM struct{}

var _ providers.LLMProvider = (*StubLLM)(nil)

func (s *StubLLM) ExtractFields(_ context.Context, text string, schema any) (json.RawMessage, float64, error) {
	if strings.Contains(text, "trigger_error") {
		return nil, 0, fmt.Errorf("LLM API unavailable: simulated failure")
	}
	return json.RawMessage(`{"analysis":"default"}`), 0.95, nil
}

func (s *StubLLM) Reason(_ context.Context, prompt string) (string, error) {
	if strings.Contains(prompt, "trigger_error") {
		return "", fmt.Errorf("LLM API unavailable: simulated failure")
	}
	if strings.Contains(prompt, "approve") {
		return "approved", nil
	}
	if strings.Contains(prompt, "reject") {
		return "rejected", nil
	}
	return "analyzed", nil
}

func (s *StubLLM) Chat(_ context.Context, messages []providers.ChatMessage) (string, error) {
	for _, msg := range messages {
		if strings.Contains(msg.Content, "trigger_error") {
			return "", fmt.Errorf("LLM API unavailable: simulated failure")
		}
		if strings.Contains(msg.Content, "approve") {
			return "approved", nil
		}
		if strings.Contains(msg.Content, "reject") {
			return "rejected", nil
		}
	}
	return "analyzed", nil
}

// ---------------------------------------------------------------------------
// StubDB — wraps postgres.Adapter with in-memory tracking for test assertions
// ---------------------------------------------------------------------------

// StubDB wraps a real postgres.Adapter and records all created jobs and
// HITL requests for later test assertions.
type StubDB struct {
	providers.DBProvider
	mu sync.Mutex

	CreatedJobs    []*domain.Job
	CreatedHITLReq []*domain.HITLRequest
}

// NewStubDB creates a StubDB wrapping the given DB provider.
func NewStubDB(db providers.DBProvider) *StubDB {
	return &StubDB{DBProvider: db}
}

// UpsertJob shadows the embedded provider's method to add tracking.
func (s *StubDB) UpsertJob(ctx context.Context, job *domain.Job) error {
	err := s.DBProvider.UpsertJob(ctx, job)
	if err == nil {
		s.mu.Lock()
		s.CreatedJobs = append(s.CreatedJobs, job)
		s.mu.Unlock()
	}
	return err
}

// UpsertHITLRequest shadows the embedded provider's method to add tracking.
func (s *StubDB) UpsertHITLRequest(ctx context.Context, req *domain.HITLRequest) error {
	err := s.DBProvider.UpsertHITLRequest(ctx, req)
	if err == nil {
		s.mu.Lock()
		s.CreatedHITLReq = append(s.CreatedHITLReq, req)
		s.mu.Unlock()
	}
	return err
}

// LastCreatedJob returns the most recently created job, or nil.
func (s *StubDB) LastCreatedJob() *domain.Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.CreatedJobs) == 0 {
		return nil
	}
	return s.CreatedJobs[len(s.CreatedJobs)-1]
}

// LastHITLRequest returns the most recently created HITL request, or nil.
func (s *StubDB) LastHITLRequest() *domain.HITLRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.CreatedHITLReq) == 0 {
		return nil
	}
	return s.CreatedHITLReq[len(s.CreatedHITLReq)-1]
}

// compile-time check
var _ providers.DBProvider = (*StubDB)(nil)
