package providers

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/aparna/opscore/internal/domain"
)

type OCRProvider interface {
	Extract(ctx context.Context, blobURL string) (*OCRResult, error)
}

type OCRResult struct {
	Text       string                 `json:"text"`
	Tables     []TableData            `json:"tables,omitempty"`
	KeyValues  map[string]string      `json:"key_values,omitempty"`
	Confidence float64                `json:"confidence"`
	Language   string                 `json:"language"`
	Provider   string                 `json:"provider"`
}

type TableData struct {
	Rows    [][]string `json:"rows"`
	Headers []string  `json:"headers"`
}

type LLMProvider interface {
	ExtractFields(ctx context.Context, text string, schema any) (json.RawMessage, float64, error)
	Reason(ctx context.Context, prompt string) (string, error)
	Chat(ctx context.Context, messages []ChatMessage) (string, error)
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type StorageProvider interface {
	Upload(ctx context.Context, container, key string, r io.Reader, contentType string) (string, error)
	Download(ctx context.Context, container, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, container, key string) error
	List(ctx context.Context, container, prefix string) ([]BlobItem, error)
}

type BlobItem struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

type QueueProvider interface {
	Enqueue(ctx context.Context, queueName string, message any) (string, error)
	Dequeue(ctx context.Context, queueName string) (*QueueMessage, error)
	Delete(ctx context.Context, queueName, messageID string) error
	Poison(ctx context.Context, queueName, messageID string) error
}

type QueueMessage struct {
	ID        string `json:"id"`
	Body      string `json:"body"`
	DequeueCount int  `json:"dequeue_count"`
}

type DBProvider interface {
	UpsertJob(ctx context.Context, job *domain.Job) error
	GetJob(ctx context.Context, id string) (*domain.Job, error)
	ListJobs(ctx context.Context, tenantID string, workflowType domain.WorkflowType, status domain.JobStatus) ([]*domain.Job, error)

	UpsertVendor(ctx context.Context, vendor *domain.Vendor) error
	GetVendor(ctx context.Context, id string) (*domain.Vendor, error)
	ListVendors(ctx context.Context, tenantID string) ([]*domain.Vendor, error)

	UpsertDocument(ctx context.Context, doc *domain.Document) error
	GetDocument(ctx context.Context, id string) (*domain.Document, error)

	UpsertHITLRequest(ctx context.Context, req *domain.HITLRequest) error
	GetHITLRequest(ctx context.Context, id string) (*domain.HITLRequest, error)
	ListPendingHITL(ctx context.Context, tenantID string) ([]*domain.HITLRequest, error)

	AppendAuditEvent(ctx context.Context, event *domain.AuditEvent) error
	ListAuditEvents(ctx context.Context, tenantID, targetType, targetID string, limit int) ([]*domain.AuditEvent, error)

	VectorSearch(ctx context.Context, collection string, embedding []float32, topK int) ([]VectorMatch, error)

	// Queue operations (optional)
	QueueEnqueue(ctx context.Context, queueName string, message any) (string, error)
}

type VectorMatch struct {
	ID        string                 `json:"id"`
	Score     float64                `json:"score"`
	Payload   map[string]interface{} `json:"payload"`
}

type HITLProvider interface {
	SendApprovalRequest(ctx context.Context, req *domain.HITLRequest) error
	SendMessage(ctx context.Context, tenantID, message string) error
}

type TracingProvider interface {
	StartSpan(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span)
	RecordLLMCall(ctx context.Context, model string, tokens int, costINR float64, durationMs int)
	RecordError(ctx context.Context, err error, opts ...SpanOption)
}

type Span interface {
	End(err error)
	SetAttribute(key string, value string)
	SetAttributes(attrs map[string]string)
}

type SpanOption func(*spanConfig)

type spanConfig struct {
	tenantID     string
	workflowType string
	jobID        string
}

func WithTenantID(id string) SpanOption {
	return func(c *spanConfig) { c.tenantID = id }
}

func WithWorkflowType(t string) SpanOption {
	return func(c *spanConfig) { c.workflowType = t }
}

func WithJobID(id string) SpanOption {
	return func(c *spanConfig) { c.jobID = id }
}