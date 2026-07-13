package agents

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aparna/opscore/internal/adapters/postgres"
	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
)

// DocumentAgent handles document ingestion workflows
type DocumentAgent struct {
	storage    providers.StorageProvider
	queue      providers.QueueProvider
	db         providers.DBProvider
	ocr        providers.OCRProvider
	classifier *DocumentClassifierWrapper
	validator  *domain.IndiaValidator
	tracer     providers.TracingProvider
}

// DocumentClassifierWrapper wraps the classifier for use with agents
type DocumentClassifierWrapper struct{}

func (c *DocumentClassifierWrapper) Classify(ctx context.Context, filename, contentType string) (string, error) {
	// Simple classification based on filename
	filename = filenameLower(filename)
	if contains(filename, "invoice") {
		return "INVOICE", nil
	}
	if contains(filename, "contract") {
		return "CONTRACT", nil
	}
	if contains(filename, "gst") {
		return "GST", nil
	}
	if contains(filename, "po") || contains(filename, "purchase") {
		return "PURCHASE_ORDER", nil
	}
	return "OTHER", nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(s) > 0 && indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func filenameLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		result[i] = c
	}
	return string(result)
}

// NewDocumentAgent creates a new document agent
func NewDocumentAgent(
	storage providers.StorageProvider,
	queue providers.QueueProvider,
	db providers.DBProvider,
	ocr providers.OCRProvider,
	validator *domain.IndiaValidator,
	tracer providers.TracingProvider,
) *DocumentAgent {
	return &DocumentAgent{
		storage:    storage,
		queue:      queue,
		db:         db,
		ocr:        ocr,
		classifier: &DocumentClassifierWrapper{},
		validator:  validator,
		tracer:     tracer,
	}
}

// DocumentJob represents a document processing job
type DocumentJob struct {
	TenantID string `json:"tenant_id"`
	JobID    string `json:"job_id"`
	BlobURL  string `json:"blob_url"`
	FileName string `json:"file_name"`
	Type     string `json:"type"`
	JobType  string `json:"job_type"`
}

// ProcessDocument handles the complete document ingestion workflow
func (a *DocumentAgent) ProcessDocument(ctx context.Context, job *DocumentJob) (result map[string]any, err error) {
	ctx, span := a.tracer.StartSpan(ctx, "document_agent.process",
		providers.WithJobID(job.JobID),
		providers.WithTenantID(job.TenantID),
		providers.WithWorkflowType("document_ingestion"),
	)
	defer func() {
		span.End(err)
	}()

	// Step 1: Classify document
	span.SetAttribute("step", "classify")
	docType, err := a.classifier.Classify(ctx, job.FileName, "pdf")
	if err != nil {
		return nil, fmt.Errorf("classifying document: %w", err)
	}
	span.SetAttribute("document_type", docType)

	// Step 2: Run OCR
	span.SetAttribute("step", "ocr")
	if a.ocr == nil {
		return nil, fmt.Errorf("OCR provider not configured (missing SARVAM_API_KEY)")
	}
	ocrResult, err := a.ocr.Extract(ctx, job.BlobURL)
	if err != nil {
		return nil, fmt.Errorf("OCR failed: %w", err)
	}
	span.SetAttribute("ocr_confidence", fmt.Sprintf("%.2f", ocrResult.Confidence))

	// Record LLM call for OCR
	a.tracer.RecordLLMCall(ctx, "sarvam-ocr", 0, 0.0, 0)

	// Step 3: Validate extracted data
	span.SetAttribute("step", "validate")
	validations := a.validator.ValidateAll(ocrResult.KeyValues)
	span.SetAttribute("validation_errors", fmt.Sprintf("%d", len(validations.Errors)))

	// Step 4: Check if HITL is needed
	needsHITL := ocrResult.Confidence < 0.85 || len(validations.Errors) > 0
	span.SetAttribute("needs_hitl", fmt.Sprintf("%t", needsHITL))

	result = map[string]any{
		"document_type": docType,
		"text":          ocrResult.Text,
		"confidence":    ocrResult.Confidence,
		"key_values":    ocrResult.KeyValues,
		"validations":   validations,
		"needs_hitl":    needsHITL,
	}

	// Update job in database with optimistic locking
	dbJob := &domain.Job{
		ID:           job.JobID,
		TenantID:     job.TenantID,
		WorkflowType: domain.WorkflowDocumentIngestion,
		Status:       domain.JobStatusCompleted,
		UpdatedAt:    time.Now(),
		Output:       result,
	}

	if needsHITL {
		dbJob.Status = domain.JobStatusAwaitingHITL
	}

	// Get current version for optimistic locking
	existingJob, getErr := a.db.GetJob(ctx, job.JobID, job.TenantID)
	if getErr == nil {
		dbJob.Version = existingJob.Version
	}

	if err := a.db.UpsertJob(ctx, dbJob); err != nil {
		if errors.Is(err, postgres.ErrVersionConflict) {
			// Retry once: re-GET, re-apply version, Upsert again
			existingJob, getErr := a.db.GetJob(ctx, job.JobID, job.TenantID)
			if getErr == nil {
				dbJob.Version = existingJob.Version
			}
			if retryErr := a.db.UpsertJob(ctx, dbJob); retryErr != nil {
				return nil, fmt.Errorf("updating job after conflict: %w", retryErr)
			}
		} else {
			return nil, fmt.Errorf("updating job: %w", err)
		}
	}

	return result, nil
}

// QueueDocument adds a document to the processing queue
func (a *DocumentAgent) QueueDocument(ctx context.Context, job *DocumentJob) (string, error) {
	return a.queue.Enqueue(ctx, "document-queue", job)
}

// GetDocumentStatus retrieves the status of a document job
func (a *DocumentAgent) GetDocumentStatus(ctx context.Context, jobID, tenantID string) (*domain.Job, error) {
	return a.db.GetJob(ctx, jobID, tenantID)
}

// DocumentAgentTools returns the list of tools available to this agent
func (a *DocumentAgent) DocumentAgentTools() []string {
	return []string{
		"process_document",
		"queue_document",
		"get_document_status",
	}
}
