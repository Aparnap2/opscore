package handlers

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/aparna/opscore/internal/domain"
)

// HTTPJobStatusHandler handles GET /jobs/{id}
// Reads job status from Cosmos DB
func HTTPJobStatusHandler(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	log.Println("Received job status request")

	// Validate request method
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Extract job ID from path
	// In Azure Functions: /api/jobs/{id} -> path param is "id"
	path := r.URL.Path
	parts := strings.Split(path, "/")
	var jobID string
	for i, part := range parts {
		if part == "jobs" && i+1 < len(parts) {
			jobID = parts[i+1]
			break
		}
	}

	if jobID == "" || jobID == "jobs" {
		writeError(w, http.StatusBadRequest, "Missing job ID")
		return
	}

	// Validate job ID format (basic UUID check)
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "Invalid job ID")
		return
	}

	// Get cosmos adapter
	cosmosAdapter, err := getCosmosAdapter(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Database service unavailable")
		return
	}

	// Extract tenant ID from header for authorization
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}

	// Fetch job from Cosmos DB using tenant ID for partition key
	job, err := cosmosAdapter.GetJob(ctx, jobID, tenantID)
	if err != nil {
		log.Printf("Failed to get job %s: %v", jobID, err)
		writeError(w, http.StatusNotFound, "Job not found")
		return
	}

	// Verify tenant access (in production, use proper RBAC)
	if job.TenantID != tenantID {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	// Build response
	response := JobStatusResponse{
		ID:           job.ID,
		WorkflowType: string(job.WorkflowType),
		Status:       string(job.Status),
		CreatedAt:    job.CreatedAt,
		UpdatedAt:    job.UpdatedAt,
		Output:       job.Output,
		Error:        job.Error,
	}

	log.Printf("Job status: id=%s, status=%s", jobID, job.Status)
	writeJSON(w, http.StatusOK, response)
}

// HTTPJobsListHandler handles GET /jobs
// Lists jobs for a tenant with optional filtering
func HTTPJobsListHandler(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	log.Println("Received jobs list request")

	// Extract tenant ID
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}

	// Get query parameters for filtering
	workflowType := r.URL.Query().Get("workflow_type")
	status := r.URL.Query().Get("status")

	// Get cosmos adapter
	cosmosAdapter, err := getCosmosAdapter(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Database service unavailable")
		return
	}

	// Convert filter params
	var wf domain.WorkflowType
	if workflowType != "" {
		wf = domain.WorkflowType(workflowType)
	}

	var st domain.JobStatus
	if status != "" {
		st = domain.JobStatus(status)
	}

	// Fetch jobs
	jobs, err := cosmosAdapter.ListJobs(ctx, tenantID, wf, st)
	if err != nil {
		log.Printf("Failed to list jobs: %v", err)
		writeError(w, http.StatusInternalServerError, "Failed to retrieve jobs")
		return
	}

	// Build response
	response := make([]JobStatusResponse, len(jobs))
	for i, job := range jobs {
		response[i] = JobStatusResponse{
			ID:           job.ID,
			WorkflowType: string(job.WorkflowType),
			Status:       string(job.Status),
			CreatedAt:    job.CreatedAt,
			UpdatedAt:    job.UpdatedAt,
			Output:       job.Output,
			Error:        job.Error,
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"jobs":   response,
		"count": len(response),
	})
}

// HTTPJobApprovalHandler handles POST /jobs/{id}/approve
// Approves a job awaiting HITL
func HTTPJobApprovalHandler(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	log.Println("Received job approval request")

	// Extract job ID from path
	path := r.URL.Path
	parts := strings.Split(path, "/")
	var jobID string
	for i, part := range parts {
		if part == "jobs" && i+1 < len(parts) {
			jobID = parts[i+1]
			break
		}
	}

	if jobID == "" {
		writeError(w, http.StatusBadRequest, "Missing job ID")
		return
	}

	// Extract tenant ID from header for authorization
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}

	// Get cosmos adapter
	cosmosAdapter, err := getCosmosAdapter(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Database service unavailable")
		return
	}

	// Fetch job using tenant ID from header
	job, err := cosmosAdapter.GetJob(ctx, jobID, tenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Job not found")
		return
	}

	// Verify job is awaiting HITL
	if job.Status != domain.JobStatusAwaitingHITL {
		writeError(w, http.StatusBadRequest, "Job is not awaiting approval")
		return
	}

	// Verify tenant access
	if job.TenantID != tenantID {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	// Extract approver from header
	approvedBy := r.Header.Get("X-Approved-By")
	if approvedBy == "" {
		approvedBy = "system"
	}

	// Update job status
	job.Status = domain.JobStatusCompleted
	job.UpdatedAt = time.Now()
	if err := cosmosAdapter.UpsertJob(ctx, job); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update job")
		return
	}

	// Update HITL request
	hitlReqs, err := cosmosAdapter.ListPendingHITL(ctx, job.TenantID)
	if err == nil {
		for _, hitl := range hitlReqs {
			if hitl.JobID == jobID {
				hitl.Status = "APPROVED"
				hitl.ApprovedBy = approvedBy
				now := time.Now()
				hitl.ApprovedAt = &now
				cosmosAdapter.UpsertHITLRequest(ctx, hitl)
				break
			}
		}
	}

	// Log audit event
	cosmosAdapter.AppendAuditEvent(ctx, &domain.AuditEvent{
		Actor:        approvedBy,
		Action:       "APPROVE",
		TargetType:   "job",
		TargetID:     jobID,
		OldState:     "AWAITING_HITL",
		NewState:     "COMPLETED",
		Timestamp:    time.Now(),
		CorrelationID: job.CorrelationID,
	})

	log.Printf("Job approved: id=%s, approved_by=%s", jobID, approvedBy)
	writeJSON(w, http.StatusOK, map[string]string{
		"status":      "APPROVED",
		"job_id":      jobID,
		"approved_by": approvedBy,
	})
}