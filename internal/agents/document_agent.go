package agents

import (
	"context"
	"fmt"
	"time"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
)

// DocumentAgent handles document ingestion workflows
type DocumentAgent struct {
	storage   providers.StorageProvider
	queue    providers.QueueProvider
	db       providers.DBProvider
	ocr      providers.OCRProvider
	classifier *DocumentClassifierWrapper
	validator *domain.IndiaValidator
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
	if contains(filename, "po") || contains(filename, "purchase") {
		return "PURCHASE_ORDER", nil
	}
	if contains(filename, "gst") {
		return "GST", nil
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
) *DocumentAgent {
	return &DocumentAgent{
		storage:   storage,
		queue:    queue,
		db:       db,
		ocr:      ocr,
		classifier: &DocumentClassifierWrapper{},
		validator: validator,
	}
}

// DocumentJob represents a document processing job
type DocumentJob struct {
	TenantID  string `json:"tenant_id"`
	JobID    string `json:"job_id"`
	BlobURL  string `json:"blob_url"`
	FileName string `json:"file_name"`
	Type    string `json:"type"`
	JobType string `json:"job_type"`
}

// ProcessDocument handles the complete document ingestion workflow
func (a *DocumentAgent) ProcessDocument(ctx context.Context, job *DocumentJob) (map[string]any, error) {
	// Step 1: Classify document
	docType, err := a.classifier.Classify(ctx, job.FileName, "pdf")
	if err != nil {
		return nil, fmt.Errorf("classifying document: %w", err)
	}

	// Step 2: Run OCR
	ocrResult, err := a.ocr.Extract(ctx, job.BlobURL)
	if err != nil {
		return nil, fmt.Errorf("OCR failed: %w", err)
	}

	// Step 3: Validate extracted data
	validations := a.validator.ValidateAll(ocrResult.KeyValues)

	// Step 4: Check if HITL is needed
	needsHITL := ocrResult.Confidence < 0.85 || len(validations.Errors) > 0

	result := map[string]any{
		"document_type": docType,
		"text":        ocrResult.Text,
		"confidence":  ocrResult.Confidence,
		"key_values":  ocrResult.KeyValues,
		"validations":  validations,
		"needs_hitl":  needsHITL,
	}

	// Update job in database
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

	if err := a.db.UpsertJob(ctx, dbJob); err != nil {
		return nil, fmt.Errorf("updating job: %w", err)
	}

	// Create HITL request if needed
	if needsHITL {
		hitlReq := &domain.HITLRequest{
			ID:        fmt.Sprintf("hitl-%s", job.JobID),
			TenantID:  job.TenantID,
			JobID:    job.JobID,
			Type:     "DOCUMENT_APPROVAL",
			Message:  fmt.Sprintf("Document %s requires approval: confidence=%.2f, errors=%d", job.FileName, ocrResult.Confidence, len(validations.Errors)),
			Status:   "PENDING",
			CreatedAt: time.Now(),
		}

		if err := a.db.UpsertHITLRequest(ctx, hitlReq); err != nil {
			return nil, fmt.Errorf("creating HITL request: %w", err)
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