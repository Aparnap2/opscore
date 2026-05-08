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
	client     *azcosmos.Client
	database   string
	databaseID string
	// partitionKeyPath defines the partition key for all collections
	partitionKeyPath string
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

	adapter := &CosmosAdapter{
		client:            client,
		database:          config.DatabaseName,
		databaseID:        config.DatabaseName,
		partitionKeyPath:  "/tenant_id",
	}

	// Ensure required containers exist with proper partition keys
	if err := adapter.ensureContainers(context.Background()); err != nil {
		// Log but don't fail - containers might already exist
		_ = err
	}

	return adapter, nil
}

// ensureContainers creates all required collections with /tenant_id partition key
func (c *CosmosAdapter) ensureContainers(ctx context.Context) error {
	containers := []string{"jobs", "vendors", "documents", "audit_events", "hitl_requests"}

	for _, name := range containers {
		db, err := c.client.NewDatabase(c.databaseID)
		if err != nil {
			continue
		}
		_, _ = db.CreateContainer(ctx,
			azcosmos.ContainerProperties{
				ID: name,
				PartitionKeyDefinition: azcosmos.PartitionKeyDefinition{
					Paths: []string{c.partitionKeyPath},
				},
			}, nil)
		// Ignore errors - container may already exist
	}
	return nil
}

// getPartitionKey returns the partition key for an entity using tenant_id
func (c *CosmosAdapter) getPartitionKey(tenantID string) azcosmos.PartitionKey {
	return azcosmos.NewPartitionKeyString(tenantID)
}

// getContainer returns a container client
func (c *CosmosAdapter) getContainer(containerName string) (*azcosmos.ContainerClient, error) {
	return c.client.NewContainer(c.databaseID, containerName)
}

// UpsertJob inserts or updates a job document
// Uses tenant_id as partition key
func (c *CosmosAdapter) UpsertJob(ctx context.Context, job *domain.Job) error {
	if c.client == nil {
		return fmt.Errorf("cosmos client not initialized")
	}

	if job.TenantID == "" {
		return fmt.Errorf("job missing tenant_id")
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

	// Use tenant_id as partition key
	pk := c.getPartitionKey(job.TenantID)

	// Upsert the item (UpsertItem handles create or replace)
	_, err = container.UpsertItem(ctx, pk, jobJSON, nil)
	if err != nil {
		return fmt.Errorf("upserting job: %w", err)
	}

	return nil
}

// GetJob retrieves a job by ID
// Uses tenant_id for partition lookup
func (c *CosmosAdapter) GetJob(ctx context.Context, id, tenantID string) (*domain.Job, error) {
	if c.client == nil {
		return nil, fmt.Errorf("cosmos client not initialized")
	}

	if tenantID == "" {
		return nil, fmt.Errorf("tenantID is required")
	}

	container, err := c.getContainer("jobs")
	if err != nil {
		return nil, fmt.Errorf("getting container: %w", err)
	}

	// Query to find by job ID using tenant partition key
	query := "SELECT * FROM c WHERE c.id = @id AND c.tenant_id = @tenantID"
	params := []azcosmos.QueryParameter{
		{Name: "@id", Value: id},
		{Name: "@tenantID", Value: tenantID},
	}

	pager := container.NewQueryItemsPager(query, c.getPartitionKey(tenantID), &azcosmos.QueryOptions{QueryParameters: params})

	var job *domain.Job
	for pager.More() {
		resp, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("querying job: %w", err)
		}

		for _, item := range resp.Items {
			var j domain.Job
			if err := json.Unmarshal(item, &j); err != nil {
				continue
			}
			job = &j
			break
		}
		if job != nil {
			break
		}
	}

	if job == nil {
		return nil, fmt.Errorf("job not found: %s", id)
	}

	return job, nil
}

// ListJobs retrieves jobs for a tenant with optional filters
// Uses tenant_id partition key
func (c *CosmosAdapter) ListJobs(ctx context.Context, tenantID string, workflowType domain.WorkflowType, status domain.JobStatus) ([]*domain.Job, error) {
	if c.client == nil {
		return nil, fmt.Errorf("cosmos client not initialized")
	}

	if tenantID == "" {
		return nil, fmt.Errorf("tenantID is required")
	}

	container, err := c.getContainer("jobs")
	if err != nil {
		return nil, fmt.Errorf("getting container: %w", err)
	}

	// Build query - always filter by tenant_id (partition key)
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

	// Use tenant_id as partition key for efficient querying
	pager := container.NewQueryItemsPager(query, c.getPartitionKey(tenantID), &azcosmos.QueryOptions{QueryParameters: params})

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
// Uses tenant_id as partition key
func (c *CosmosAdapter) UpsertVendor(ctx context.Context, vendor *domain.Vendor) error {
	if c.client == nil {
		return fmt.Errorf("cosmos client not initialized")
	}

	if vendor.TenantID == "" {
		return fmt.Errorf("vendor missing tenant_id")
	}

	container, err := c.getContainer("vendors")
	if err != nil {
		return fmt.Errorf("getting container: %w", err)
	}

	vendorJSON, err := json.Marshal(vendor)
	if err != nil {
		return fmt.Errorf("marshaling vendor: %w", err)
	}

	pk := c.getPartitionKey(vendor.TenantID)
	_, err = container.UpsertItem(ctx, pk, vendorJSON, nil)
	if err != nil {
		return fmt.Errorf("upserting vendor: %w", err)
	}

	return nil
}

// GetVendor retrieves a vendor by ID
// Queries by ID since partition key is tenant_id
func (c *CosmosAdapter) GetVendor(ctx context.Context, id string) (*domain.Vendor, error) {
	if c.client == nil {
		return nil, fmt.Errorf("cosmos client not initialized")
	}

	container, err := c.getContainer("vendors")
	if err != nil {
		return nil, fmt.Errorf("getting container: %w", err)
	}

	query := "SELECT * FROM c WHERE c.id = @id"
	params := []azcosmos.QueryParameter{
		{Name: "@id", Value: id},
	}

	pager := container.NewQueryItemsPager(query, azcosmos.NullPartitionKey, &azcosmos.QueryOptions{QueryParameters: params})

	var vendor *domain.Vendor
	for pager.More() {
		resp, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("querying vendor: %w", err)
		}

		for _, item := range resp.Items {
			var v domain.Vendor
			if err := json.Unmarshal(item, &v); err != nil {
				continue
			}
			vendor = &v
			break
		}
		if vendor != nil {
			break
		}
	}

	if vendor == nil {
		return nil, fmt.Errorf("vendor not found: %s", id)
	}

	return vendor, nil
}

// ListVendors retrieves all vendors for a tenant
// Uses tenant_id partition key
func (c *CosmosAdapter) ListVendors(ctx context.Context, tenantID string) ([]*domain.Vendor, error) {
	if c.client == nil {
		return nil, fmt.Errorf("cosmos client not initialized")
	}

	if tenantID == "" {
		return nil, fmt.Errorf("tenantID is required")
	}

	container, err := c.getContainer("vendors")
	if err != nil {
		return nil, fmt.Errorf("getting container: %w", err)
	}

	query := "SELECT * FROM c WHERE c.tenant_id = @tenantID"
	params := []azcosmos.QueryParameter{
		{Name: "@tenantID", Value: tenantID},
	}

	pager := container.NewQueryItemsPager(query, c.getPartitionKey(tenantID), &azcosmos.QueryOptions{QueryParameters: params})

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
// Uses tenant_id as partition key
func (c *CosmosAdapter) UpsertDocument(ctx context.Context, doc *domain.Document) error {
	if c.client == nil {
		return fmt.Errorf("cosmos client not initialized")
	}

	if doc.TenantID == "" {
		return fmt.Errorf("document missing tenant_id")
	}

	container, err := c.getContainer("documents")
	if err != nil {
		return fmt.Errorf("getting container: %w", err)
	}

	docJSON, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshaling document: %w", err)
	}

	pk := c.getPartitionKey(doc.TenantID)
	_, err = container.UpsertItem(ctx, pk, docJSON, nil)
	if err != nil {
		return fmt.Errorf("upserting document: %w", err)
	}

	return nil
}

// GetDocument retrieves a document by ID
// Queries by ID since partition key is tenant_id
func (c *CosmosAdapter) GetDocument(ctx context.Context, id string) (*domain.Document, error) {
	if c.client == nil {
		return nil, fmt.Errorf("cosmos client not initialized")
	}

	container, err := c.getContainer("documents")
	if err != nil {
		return nil, fmt.Errorf("getting container: %w", err)
	}

	query := "SELECT * FROM c WHERE c.id = @id"
	params := []azcosmos.QueryParameter{
		{Name: "@id", Value: id},
	}

	pager := container.NewQueryItemsPager(query, azcosmos.NullPartitionKey, &azcosmos.QueryOptions{QueryParameters: params})

	var doc *domain.Document
	for pager.More() {
		resp, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("querying document: %w", err)
		}

		for _, item := range resp.Items {
			var d domain.Document
			if err := json.Unmarshal(item, &d); err != nil {
				continue
			}
			doc = &d
			break
		}
		if doc != nil {
			break
		}
	}

	if doc == nil {
		return nil, fmt.Errorf("document not found: %s", id)
	}

	return doc, nil
}

// UpsertHITLRequest inserts or updates a HITL request
// Uses tenant_id as partition key
func (c *CosmosAdapter) UpsertHITLRequest(ctx context.Context, req *domain.HITLRequest) error {
	if c.client == nil {
		return fmt.Errorf("cosmos client not initialized")
	}

	if req.TenantID == "" {
		return fmt.Errorf("HITL request missing tenant_id")
	}

	container, err := c.getContainer("hitl_requests")
	if err != nil {
		return fmt.Errorf("getting container: %w", err)
	}

	reqJSON, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshaling HITL request: %w", err)
	}

	pk := c.getPartitionKey(req.TenantID)
	_, err = container.UpsertItem(ctx, pk, reqJSON, nil)
	if err != nil {
		return fmt.Errorf("upserting HITL request: %w", err)
	}

	return nil
}

// GetHITLRequest retrieves a HITL request by ID
// Queries by ID since partition key is tenant_id
func (c *CosmosAdapter) GetHITLRequest(ctx context.Context, id string) (*domain.HITLRequest, error) {
	if c.client == nil {
		return nil, fmt.Errorf("cosmos client not initialized")
	}

	container, err := c.getContainer("hitl_requests")
	if err != nil {
		return nil, fmt.Errorf("getting container: %w", err)
	}

	query := "SELECT * FROM c WHERE c.id = @id"
	params := []azcosmos.QueryParameter{
		{Name: "@id", Value: id},
	}

	pager := container.NewQueryItemsPager(query, azcosmos.NullPartitionKey, &azcosmos.QueryOptions{QueryParameters: params})

	var req *domain.HITLRequest
	for pager.More() {
		resp, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("querying HITL request: %w", err)
		}

		for _, item := range resp.Items {
			var r domain.HITLRequest
			if err := json.Unmarshal(item, &r); err != nil {
				continue
			}
			req = &r
			break
		}
		if req != nil {
			break
		}
	}

	if req == nil {
		return nil, fmt.Errorf("HITL request not found: %s", id)
	}

	return req, nil
}

// ListPendingHITL retrieves all pending HITL requests for a tenant
// Uses tenant_id partition key
func (c *CosmosAdapter) ListPendingHITL(ctx context.Context, tenantID string) ([]*domain.HITLRequest, error) {
	if c.client == nil {
		return nil, fmt.Errorf("cosmos client not initialized")
	}

	if tenantID == "" {
		return nil, fmt.Errorf("tenantID is required")
	}

	container, err := c.getContainer("hitl_requests")
	if err != nil {
		return nil, fmt.Errorf("getting container: %w", err)
	}

	query := "SELECT * FROM c WHERE c.tenant_id = @tenantID AND c.status = 'pending'"
	params := []azcosmos.QueryParameter{
		{Name: "@tenantID", Value: tenantID},
	}

	pager := container.NewQueryItemsPager(query, c.getPartitionKey(tenantID), &azcosmos.QueryOptions{QueryParameters: params})

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
// Uses tenant_id as partition key, timestamp as item ID for uniqueness
func (c *CosmosAdapter) AppendAuditEvent(ctx context.Context, event *domain.AuditEvent) error {
	if c.client == nil {
		return fmt.Errorf("cosmos client not initialized")
	}

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	container, err := c.getContainer("audit_events")
	if err != nil {
		return fmt.Errorf("getting container: %w", err)
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling audit event: %w", err)
	}

	// Use tenant_id as partition key
	pk := c.getPartitionKey(event.TenantID)

	_, err = container.CreateItem(ctx, pk, eventJSON, nil)
	if err != nil {
		return fmt.Errorf("creating audit event: %w", err)
	}

	return nil
}

// ListAuditEvents retrieves audit events for a target
// Uses tenant_id partition key
func (c *CosmosAdapter) ListAuditEvents(ctx context.Context, tenantID, targetType, targetID string, limit int) ([]*domain.AuditEvent, error) {
	if c.client == nil {
		return nil, fmt.Errorf("cosmos client not initialized")
	}

	if tenantID == "" {
		return nil, fmt.Errorf("tenantID is required")
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

	pager := container.NewQueryItemsPager(query, c.getPartitionKey(tenantID), &azcosmos.QueryOptions{QueryParameters: params})

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
	GetJobFunc           func(ctx context.Context, id, tenantID string) (*domain.Job, error)
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

func (m *CosmosAdapterMock) GetJob(ctx context.Context, id, tenantID string) (*domain.Job, error) {
	if m.GetJobFunc != nil {
		return m.GetJobFunc(ctx, id, tenantID)
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