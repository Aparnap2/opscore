package main

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/aparna/opscore/internal/middleware/tenant"
)

// jobStatusHandler handles GET /jobs/{id}.
func (s *ServerDeps) jobStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

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

	t := tenant.FromContext(r.Context())
	tenantID := t.ID

	ctx := r.Context()
	job, err := s.db.GetJob(ctx, jobID, tenantID)
	if err != nil {
		slog.Error("Failed to get job", "jobID", jobID, "err", err)
		writeError(w, http.StatusNotFound, "Job not found")
		return
	}

	if job.TenantID != tenantID {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	writeJSON(w, http.StatusOK, JobStatusResponse{
		ID:           job.ID,
		WorkflowType: string(job.WorkflowType),
		Status:       string(job.Status),
		CreatedAt:    job.CreatedAt,
		UpdatedAt:    job.UpdatedAt,
		Output:       job.Output,
		Error:        job.Error,
	})
}
