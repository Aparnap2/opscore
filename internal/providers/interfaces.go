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
	KeyValues  map[string]string `json:"key_values,omitempty"`
	Text       string            `json:"text"`
	Language   string            `json:"language"`
	Provider   string            `json:"provider"`
	Tables     []TableData       `json:"tables,omitempty"`
	Confidence float64           `json:"confidence"`
}

type TableData struct {
	Rows    [][]string `json:"rows"`
	Headers []string   `json:"headers"`
}

type LLMProvider interface {
	ExtractFields(ctx context.Context, text string, schema any) (json.RawMessage, float64, error)
	Reason(ctx context.Context, prompt string) (string, error)
	Chat(ctx context.Context, messages []ChatMessage) (string, *json.RawMessage, error)
}

type ChatMessage struct {
	Role             string           `json:"role"`
	Content          string           `json:"content"`
	ReasoningDetails *json.RawMessage `json:"reasoning_details,omitempty"`
}

type StorageProvider interface {
	Upload(ctx context.Context, container, key string, r io.Reader, contentType string) (string, error)
	Download(ctx context.Context, container, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, container, key string) error
	List(ctx context.Context, container, prefix string) ([]BlobItem, error)
}

type BlobItem struct {
	Modified time.Time `json:"modified"`
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
}

type QueueProvider interface {
	Enqueue(ctx context.Context, queueName string, message any) (string, error)
	Dequeue(ctx context.Context, queueName string) (*QueueMessage, error)
	Delete(ctx context.Context, queueName, messageID string) error
	Poison(ctx context.Context, queueName, messageID string) error
}

type QueueMessage struct {
	ID           string `json:"id"`
	Body         string `json:"body"`
	DequeueCount int    `json:"dequeue_count"`
}

type DBProvider interface {
	UpsertJob(ctx context.Context, job *domain.Job) error
	GetJob(ctx context.Context, id, tenantID string) (*domain.Job, error)
	ListJobs(ctx context.Context, tenantID string, workflowType domain.WorkflowType, status domain.JobStatus) ([]*domain.Job, error)

	UpsertVendor(ctx context.Context, vendor *domain.Vendor) error
	GetVendor(ctx context.Context, id, tenantID string) (*domain.Vendor, error)
	ListVendors(ctx context.Context, tenantID string) ([]*domain.Vendor, error)

	UpsertDocument(ctx context.Context, doc *domain.Document) error
	GetDocument(ctx context.Context, id, tenantID string) (*domain.Document, error)
	FindBySHA256(ctx context.Context, tenantID, contentHash string) (*domain.Document, error)

	UpsertHITLRequest(ctx context.Context, req *domain.HITLRequest) error
	GetHITLRequest(ctx context.Context, id, tenantID string) (*domain.HITLRequest, error)
	ListPendingHITL(ctx context.Context, tenantID string) ([]*domain.HITLRequest, error)
	ListHITLRequests(ctx context.Context, tenantID, status string, limit, offset int) ([]*domain.HITLRequest, error)

	// --- Manufacturing pivot (Phase 1.4A) -------------------------------------
	// All methods are tenant-scoped. Upsert performs optimistic-locking-aware
	// writes using the entity's Version field. Get* requires the tenant_id for
	// RLS correctness. List* methods take explicit, non-generic filter params
	// and simple limit/offset pagination.

	// Purchase Orders
	UpsertPurchaseOrder(ctx context.Context, po *domain.PurchaseOrder) error
	GetPurchaseOrderByID(ctx context.Context, id, tenantID string) (*domain.PurchaseOrder, error)
	ListPurchaseOrders(ctx context.Context, tenantID string, limit, offset int) ([]*domain.PurchaseOrder, error)

	// Goods Receipts (GRN)
	UpsertGoodsReceipt(ctx context.Context, gr *domain.GoodsReceipt) error
	GetGoodsReceiptByID(ctx context.Context, id, tenantID string) (*domain.GoodsReceipt, error)
	ListGoodsReceipts(ctx context.Context, tenantID, poNumber string, limit, offset int) ([]*domain.GoodsReceipt, error)

	// Invoices
	UpsertInvoice(ctx context.Context, inv *domain.Invoice) error
	GetInvoiceByID(ctx context.Context, id, tenantID string) (*domain.Invoice, error)
	ListInvoices(ctx context.Context, tenantID, poNumber string, limit, offset int) ([]*domain.Invoice, error)

	// Exception Cases (mismatch records)
	UpsertExceptionCase(ctx context.Context, ec *domain.ExceptionCase) error
	GetExceptionCaseByID(ctx context.Context, id, tenantID string) (*domain.ExceptionCase, error)
	ListExceptionCases(ctx context.Context, tenantID, status, mismatchType string, limit, offset int) ([]*domain.ExceptionCase, error)
	UpdateExceptionCaseStatus(ctx context.Context, id, tenantID, status string) error

	AppendAuditEvent(ctx context.Context, event *domain.AuditEvent) error
	ListAuditEvents(ctx context.Context, tenantID, targetType, targetID string, limit int) ([]*domain.AuditEvent, error)

	// Ops endpoints
	GetRecentJobs(ctx context.Context, tenantID string, limit int) ([]*domain.Job, error)
	GetRiskyVendors(ctx context.Context, tenantID string) ([]*domain.Vendor, error)
	GetRecentCompliance(ctx context.Context, tenantID string, limit int) ([]*domain.ComplianceRecord, error)
}

type TenantProvider interface {
	GetTenant(ctx context.Context, id string) (*domain.Tenant, error)
	GetTenantBySlug(ctx context.Context, slug string) (*domain.Tenant, error)
	CreateTenant(ctx context.Context, tenant *domain.Tenant) error
	ListTenants(ctx context.Context) ([]*domain.Tenant, error)
	UpdateTenantStatus(ctx context.Context, id, status string) error
}

type HITLProvider interface {
	SendApprovalRequest(ctx context.Context, req *domain.HITLRequest) error
	SendMessage(ctx context.Context, tenantID, message string) error
}

// UsageProvider defines the interface for tracking and checking tenant usage limits.
type UsageProvider interface {
	IncrementUsage(ctx context.Context, tenantID string, metric domain.Metric, delta int64) error
	GetUsage(ctx context.Context, tenantID string, metric domain.Metric) (int64, error)
	GetCurrentPeriodUsage(ctx context.Context, tenantID string) (map[domain.Metric]int64, error)
	CheckLimit(ctx context.Context, tenantID string, metric domain.Metric) (bool, int64, int64, error)
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
