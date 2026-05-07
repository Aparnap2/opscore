package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
)

type CosmosConfig struct {
	Endpoint     string
	DatabaseName string
	Key           string
}

// CosmosAdapter implements providers.DBProvider for Azure Cosmos DB
type CosmosAdapter struct {
	client      *azcosmos.Client
	database    string
	databaseID  string
}

// NewCosmosAdapter creates a new Azure Cosmos DB adapter
func NewCosmosAdapter(config CosmosConfig) (*CosmosAdapter, error) {
	// Create key credential
	cred, err := azcosmos.NewKeyCredential(config.Key)
	if err != nil {
		return nil, fmt.Errorf("creating cosmos credential: %w", err)
	}

	// Create client with key credential
	client, err := azcosmos.NewClientWithKey(config.Endpoint, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("creating cosmos client: %w", err)
	}

	// Try to create database (may already exist)
	_, err = client.CreateDatabase(context.Background(), azcosmos.DatabaseProperties{ID: config.DatabaseName}, nil)
	if err != nil {
		// Database might already exist, continue
		_ = err
	}

	return &CosmosAdapter{
		client:     client,
		database:   config.DatabaseName,
		databaseID: config.DatabaseName,
	}, nil
}

// getContainer returns a container client
func (c *CosmosAdapter) getContainer(containerName string) (*azcosmos.ContainerClient, error) {
	return c.client.NewContainer(c.databaseID, containerName)
}

// UpsertJob inserts or updates a job document
func (c *CosmosAdapter) UpsertJob(ctx context.Context, job *domain.Job) error {
	if c.client == nil {
		return fmt.Errorf("cosmos client not initialized")
	}

	container, err := c.getContainer("jobs")
	if err != nil {
		return fmt.Errorf("getting container: %w", err)
	}

	// Marshal job to JSON
	jobJSON, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshaling job: %w", err)
	}

	// Partition key is the job ID
	pk := azcosmos.NewPartitionKeyString(job.ID)

	// Upsert the item
	_, err = container.UpsertItem(ctx, pk, jobJSON, nil)
	if err != nil {
		return fmt.Errorf("upserting job: %w", err)
	}

	return nil
}

// GetJob retrieves a job by ID
func (c *CosmosAdapter) GetJob(ctx context.Context, id string) (*domain.Job, error) {
	if c.client == nil {
		return nil, fmt.Errorf("cosmos client not initialized")
	}

	container, err := c.getContainer("jobs")
	if err != nil {
		return nil, fmt.Errorf("getting container: %w", err)
	}

	pk := azcosmos.NewPartitionKeyString(id)

	// Read item
	resp, err := container.ReadItem(ctx, pk, id, nil)
	if err != nil {
		return nil, fmt.Errorf("reading job: %w", err)
	}

	var job domain.Job
	if err := json.Unmarshal(resp.Value, &job); err != nil {
		return nil, fmt.Errorf("unmarshaling job: %w", err)
	}

	return &job, nil
}

// ListJobs retrieves jobs for a tenant with optional filters
func (c *CosmosAdapter) ListJobs(ctx context.Context, tenantID string, workflowType domain.WorkflowType, status domain.JobStatus) ([]*domain.Job, error) {
	if c.client == nil {
		return nil, fmt.Errorf("cosmos client not initialized")
	}

	container, err := c.getContainer("jobs")
	if err != nil {
		return nil, fmt.Errorf("getting container: %w", err)
	}

	// Build query
	query := "SELECT * FROM c WHERE c.tenant_id = @tenantID"
	if workflowType != "" {
		query += " AND c.workflow_type = @workflowType"
	}
	if status != "" {
		query += " AND c.status = @status"
	}

	params := []azcosmos.QueryParameter{
		{Name: "@tenantID", Value: tenantID},
	}
	if workflowType != "" {
		params = append(params, azcosmos.QueryParameter{Name: "@workflowType", Value: string(workflowType)})
	}
	if status != "" {
		params = append(params, azcosmos.QueryParameter{Name: "@status", Value: string(status)})
	}

	pager := container.NewQueryItemsPager(query, azcosmos.NullPartitionKey, &azcosmos.QueryOptions{QueryParameters: params})

	var jobs []*domain.Job
	for pager.More() {
		resp, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("querying jobs: %w", err)
		}

		for _, item := range resp.Items {
			var job domain.Job
			if err := json.Unmarshal(item, &job); err != nil {
				continue
			}
			jobs = append(jobs, &job)
		}
	}

	return jobs, nil
}

// UpsertVendor inserts or updates a vendor document
func (c *CosmosAdapter) UpsertVendor(ctx context.Context, vendor *domain.Vendor) error {
	if c.client == nil {
		return fmt.Errorf("cosmos client not initialized")
	}

	container, err := c.getContainer("vendors")
	if err != nil {
		return fmt.Errorf("getting container: %w", err)
	}

	vendorJSON, err := json.Marshal(vendor)
	if err != nil {
		return fmt.Errorf("marshaling vendor: %w", err)
	}

	pk := azcosmos.NewPartitionKeyString(vendor.ID)
	_, err = container.UpsertItem(ctx, pk, vendorJSON, nil)
	if err != nil {
		return fmt.Errorf("upserting vendor: %w", err)
	}

	return nil
}

// GetVendor retrieves a vendor by ID
func (c *CosmosAdapter) GetVendor(ctx context.Context, id string) (*domain.Vendor, error) {
	if c.client == nil {
		return nil, fmt.Errorf("cosmos client not initialized")
	}

	container, err := c.getContainer("vendors")
	if err != nil {
		return nil, fmt.Errorf("getting container: %w", err)
	}

	pk := azcosmos.NewPartitionKeyString(id)
	resp, err := container.ReadItem(ctx, pk, id, nil)
	if err != nil {
		return nil, fmt.Errorf("reading vendor: %w", err)
	}

	var vendor domain.Vendor
	if err := json.Unmarshal(resp.Value, &vendor); err != nil {
		return nil, fmt.Errorf("unmarshaling vendor: %w", err)
	}

	return &vendor, nil
}

// ListVendors retrieves all vendors for a tenant
func (c *CosmosAdapter) ListVendors(ctx context.Context, tenantID string) ([]*domain.Vendor, error) {
	if c.client == nil {
		return nil, fmt.Errorf("cosmos client not initialized")
	}

	container, err := c.getContainer("vendors")
	if err != nil {
		return nil, fmt.Errorf("getting container: %w", err)
	}

	query := "SELECT * FROM c WHERE c.tenant_id = @tenantID"
	params := []azcosmos.QueryParameter{
		{Name: "@tenantID", Value: tenantID},
	}

	pager := container.NewQueryItemsPager(query, azcosmos.NullPartitionKey, &azcosmos.QueryOptions{QueryParameters: params})

	var vendors []*domain.Vendor
	for pager.More() {
		resp, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("querying vendors: %w", err)
		}

		for _, item := range resp.Items {
			var vendor domain.Vendor
			if err := json.Unmarshal(item, &vendor); err != nil {
				continue
			}
			vendors = append(vendors, &vendor)
		}
	}

	return vendors, nil
}

// UpsertDocument inserts or updates a document record
func (c *CosmosAdapter) UpsertDocument(ctx context.Context, doc *domain.Document) error {
	if c.client == nil {
		return fmt.Errorf("cosmos client not initialized")
	}

	container, err := c.getContainer("documents")
	if err != nil {
		return fmt.Errorf("getting container: %w", err)
	}

	docJSON, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshaling document: %w", err)
	}

	pk := azcosmos.NewPartitionKeyString(doc.ID)
	_, err = container.UpsertItem(ctx, pk, docJSON, nil)
	if err != nil {
		return fmt.Errorf("upserting document: %w", err)
	}

	return nil
}

// GetDocument retrieves a document by ID
func (c *CosmosAdapter) GetDocument(ctx context.Context, id string) (*domain.Document, error) {
	if c.client == nil {
		return nil, fmt.Errorf("cosmos client not initialized")
	}

	container, err := c.getContainer("documents")
	if err != nil {
		return nil, fmt.Errorf("getting container: %w", err)
	}

	pk := azcosmos.NewPartitionKeyString(id)
	resp, err := container.ReadItem(ctx, pk, id, nil)
	if err != nil {
		return nil, fmt.Errorf("reading document: %w", err)
	}

	var doc domain.Document
	if err := json.Unmarshal(resp.Value, &doc); err != nil {
		return nil, fmt.Errorf("unmarshaling document: %w", err)
	}

	return &doc, nil
}

// UpsertHITLRequest inserts or updates a HITL request
func (c *CosmosAdapter) UpsertHITLRequest(ctx context.Context, req *domain.HITLRequest) error {
	if c.client == nil {
		return fmt.Errorf("cosmos client not initialized")
	}

	container, err := c.getContainer("hitl_requests")
	if err != nil {
		return fmt.Errorf("getting container: %w", err)
	}

	reqJSON, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshaling HITL request: %w", err)
	}

	pk := azcosmos.NewPartitionKeyString(req.ID)
	_, err = container.UpsertItem(ctx, pk, reqJSON, nil)
	if err != nil {
		return fmt.Errorf("upserting HITL request: %w", err)
	}

	return nil
}

// GetHITLRequest retrieves a HITL request by ID
func (c *CosmosAdapter) GetHITLRequest(ctx context.Context, id string) (*domain.HITLRequest, error) {
	if c.client == nil {
		return nil, fmt.Errorf("cosmos client not initialized")
	}

	container, err := c.getContainer("hitl_requests")
	if err != nil {
		return nil, fmt.Errorf("getting container: %w", err)
	}

	pk := azcosmos.NewPartitionKeyString(id)
	resp, err := container.ReadItem(ctx, pk, id, nil)
	if err != nil {
		return nil, fmt.Errorf("reading HITL request: %w", err)
	}

	var req domain.HITLRequest
	if err := json.Unmarshal(resp.Value, &req); err != nil {
		return nil, fmt.Errorf("unmarshaling HITL request: %w", err)
	}

	return &req, nil
}

// ListPendingHITL retrieves all pending HITL requests for a tenant
func (c *CosmosAdapter) ListPendingHITL(ctx context.Context, tenantID string) ([]*domain.HITLRequest, error) {
	if c.client == nil {
		return nil, fmt.Errorf("cosmos client not initialized")
	}

	container, err := c.getContainer("hitl_requests")
	if err != nil {
		return nil, fmt.Errorf("getting container: %w", err)
	}

	query := "SELECT * FROM c WHERE c.tenant_id = @tenantID AND c.status = 'pending'"
	params := []azcosmos.QueryParameter{
		{Name: "@tenantID", Value: tenantID},
	}

	pager := container.NewQueryItemsPager(query, azcosmos.NullPartitionKey, &azcosmos.QueryOptions{QueryParameters: params})

	var requests []*domain.HITLRequest
	for pager.More() {
		resp, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("querying HITL requests: %w", err)
		}

		for _, item := range resp.Items {
			var req domain.HITLRequest
			if err := json.Unmarshal(item, &req); err != nil {
				continue
			}
			requests = append(requests, &req)
		}
	}

	return requests, nil
}

// AppendAuditEvent appends an audit event
func (c *CosmosAdapter) AppendAuditEvent(ctx context.Context, event *domain.AuditEvent) error {
	if c.client == nil {
		return fmt.Errorf("cosmos client not initialized")
	}

	container, err := c.getContainer("audit_events")
	if err != nil {
		return fmt.Errorf("getting container: %w", err)
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling audit event: %w", err)
	}

	pk := azcosmos.NewPartitionKeyString(event.Timestamp.Format(time.RFC3339Nano))
	_, err = container.CreateItem(ctx, pk, eventJSON, nil)
	if err != nil {
		return fmt.Errorf("creating audit event: %w", err)
	}

	return nil
}

// ListAuditEvents retrieves audit events for a target
func (c *CosmosAdapter) ListAuditEvents(ctx context.Context, tenantID, targetType, targetID string, limit int) ([]*domain.AuditEvent, error) {
	if c.client == nil {
		return nil, fmt.Errorf("cosmos client not initialized")
	}

	container, err := c.getContainer("audit_events")
	if err != nil {
		return nil, fmt.Errorf("getting container: %w", err)
	}

	query := "SELECT * FROM c WHERE c.tenant_id = @tenantID"
	params := []azcosmos.QueryParameter{
		{Name: "@tenantID", Value: tenantID},
	}

	if targetType != "" {
		query += " AND c.target_type = @targetType"
		params = append(params, azcosmos.QueryParameter{Name: "@targetType", Value: targetType})
	}
	if targetID != "" {
		query += " AND c.target_id = @targetID"
		params = append(params, azcosmos.QueryParameter{Name: "@targetID", Value: targetID})
	}
	query += " ORDER BY c.timestamp DESC"
	if limit > 0 {
		query += " LIMIT @limit"
		params = append(params, azcosmos.QueryParameter{Name: "@limit", Value: limit})
	}

	pager := container.NewQueryItemsPager(query, azcosmos.NullPartitionKey, &azcosmos.QueryOptions{QueryParameters: params})

	var events []*domain.AuditEvent
	for pager.More() {
		resp, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("querying audit events: %w", err)
		}

		for _, item := range resp.Items {
			var event domain.AuditEvent
			if err := json.Unmarshal(item, &event); err != nil {
				continue
			}
			events = append(events, &event)
		}
	}

	return events, nil
}

// VectorSearch performs a vector similarity search
func (c *CosmosAdapter) VectorSearch(ctx context.Context, collection string, embedding []float32, topK int) ([]providers.VectorMatch, error) {
	return nil, fmt.Errorf("VectorSearch not fully implemented - requires vector search index setup")
}

// QueueEnqueue adds a message to a queue (stub for queue integration)
func (c *CosmosAdapter) QueueEnqueue(ctx context.Context, queueName string, message any) (string, error) {
	return "", fmt.Errorf("QueueEnqueue not implemented - use Azure Queue Storage adapter")
}

var _ providers.DBProvider = (*CosmosAdapter)(nil)

// CosmosAdapterMock implements DBProvider for testing
type CosmosAdapterMock struct {
	UpsertJobFunc         func(ctx context.Context, job *domain.Job) error
	GetJobFunc           func(ctx context.Context, id string) (*domain.Job, error)
	ListJobsFunc         func(ctx context.Context, tenantID string, workflowType domain.WorkflowType, status domain.JobStatus) ([]*domain.Job, error)
	UpsertVendorFunc     func(ctx context.Context, vendor *domain.Vendor) error
	GetVendorFunc        func(ctx context.Context, id string) (*domain.Vendor, error)
	ListVendorsFunc      func(ctx context.Context, tenantID string) ([]*domain.Vendor, error)
	UpsertDocumentFunc   func(ctx context.Context, doc *domain.Document) error
	GetDocumentFunc      func(ctx context.Context, id string) (*domain.Document, error)
	UpsertHITLRequestFunc func(ctx context.Context, req *domain.HITLRequest) error
	GetHITLRequestFunc    func(ctx context.Context, id string) (*domain.HITLRequest, error)
	ListPendingHITLFunc  func(ctx context.Context, tenantID string) ([]*domain.HITLRequest, error)
	AppendAuditEventFunc  func(ctx context.Context, event *domain.AuditEvent) error
	ListAuditEventsFunc  func(ctx context.Context, tenantID, targetType, targetID string, limit int) ([]*domain.AuditEvent, error)
	VectorSearchFunc    func(ctx context.Context, collection string, embedding []float32, topK int) ([]providers.VectorMatch, error)
}

func (m *CosmosAdapterMock) UpsertJob(ctx context.Context, job *domain.Job) error {
	if m.UpsertJobFunc != nil {
		return m.UpsertJobFunc(ctx, job)
	}
	return nil
}

func (m *CosmosAdapterMock) GetJob(ctx context.Context, id string) (*domain.Job, error) {
	if m.GetJobFunc != nil {
		return m.GetJobFunc(ctx, id)
	}
	return nil, nil
}

func (m *CosmosAdapterMock) ListJobs(ctx context.Context, tenantID string, workflowType domain.WorkflowType, status domain.JobStatus) ([]*domain.Job, error) {
	if m.ListJobsFunc != nil {
		return m.ListJobsFunc(ctx, tenantID, workflowType, status)
	}
	return nil, nil
}

func (m *CosmosAdapterMock) UpsertVendor(ctx context.Context, vendor *domain.Vendor) error {
	if m.UpsertVendorFunc != nil {
		return m.UpsertVendorFunc(ctx, vendor)
	}
	return nil
}

func (m *CosmosAdapterMock) GetVendor(ctx context.Context, id string) (*domain.Vendor, error) {
	if m.GetVendorFunc != nil {
		return m.GetVendorFunc(ctx, id)
	}
	return nil, nil
}

func (m *CosmosAdapterMock) ListVendors(ctx context.Context, tenantID string) ([]*domain.Vendor, error) {
	if m.ListVendorsFunc != nil {
		return m.ListVendorsFunc(ctx, tenantID)
	}
	return nil, nil
}

func (m *CosmosAdapterMock) UpsertDocument(ctx context.Context, doc *domain.Document) error {
	if m.UpsertDocumentFunc != nil {
		return m.UpsertDocumentFunc(ctx, doc)
	}
	return nil
}

func (m *CosmosAdapterMock) GetDocument(ctx context.Context, id string) (*domain.Document, error) {
	if m.GetDocumentFunc != nil {
		return m.GetDocumentFunc(ctx, id)
	}
	return nil, nil
}

func (m *CosmosAdapterMock) UpsertHITLRequest(ctx context.Context, req *domain.HITLRequest) error {
	if m.UpsertHITLRequestFunc != nil {
		return m.UpsertHITLRequestFunc(ctx, req)
	}
	return nil
}

func (m *CosmosAdapterMock) GetHITLRequest(ctx context.Context, id string) (*domain.HITLRequest, error) {
	if m.GetHITLRequestFunc != nil {
		return m.GetHITLRequestFunc(ctx, id)
	}
	return nil, nil
}

func (m *CosmosAdapterMock) ListPendingHITL(ctx context.Context, tenantID string) ([]*domain.HITLRequest, error) {
	if m.ListPendingHITLFunc != nil {
		return m.ListPendingHITLFunc(ctx, tenantID)
	}
	return nil, nil
}

func (m *CosmosAdapterMock) AppendAuditEvent(ctx context.Context, event *domain.AuditEvent) error {
	if m.AppendAuditEventFunc != nil {
		return m.AppendAuditEventFunc(ctx, event)
	}
	return nil
}

func (m *CosmosAdapterMock) ListAuditEvents(ctx context.Context, tenantID, targetType, targetID string, limit int) ([]*domain.AuditEvent, error) {
	if m.ListAuditEventsFunc != nil {
		return m.ListAuditEventsFunc(ctx, tenantID, targetType, targetID, limit)
	}
	return nil, nil
}

func (m *CosmosAdapterMock) VectorSearch(ctx context.Context, collection string, embedding []float32, topK int) ([]providers.VectorMatch, error) {
	if m.VectorSearchFunc != nil {
		return m.VectorSearchFunc(ctx, collection, embedding, topK)
	}
	return nil, nil
}

func (m *CosmosAdapterMock) QueueEnqueue(ctx context.Context, queueName string, message any) (string, error) {
	return "", nil
}

var _ providers.DBProvider = (*CosmosAdapterMock)(nil)