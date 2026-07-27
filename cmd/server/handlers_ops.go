package main

import (
	"net/http"
	"time"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/middleware/tenant"
)

// statusSummaryHandler handles GET /status/summary.
func (s *ServerDeps) statusSummaryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	t := tenant.FromContext(r.Context())
	tenantID := t.ID
	ctx := r.Context()

	// Job counts by status
	jobCounts := make(map[string]int)
	statuses := []domain.JobStatus{
		domain.JobStatusPending, domain.JobStatusQueued, domain.JobStatusProcessing,
		domain.JobStatusCompleted, domain.JobStatusFailed, domain.JobStatusRetryableFailed,
		domain.JobStatusAwaitingHITL,
	}
	for _, st := range statuses {
		jobs, _ := s.db.ListJobs(ctx, tenantID, "", st)
		jobCounts[string(st)] = len(jobs)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"job_counts": jobCounts,
		"timestamp":  time.Now().UTC(),
	})
}

// recentJobsHandler handles GET /jobs/recent.
func (s *ServerDeps) recentJobsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	t := tenant.FromContext(r.Context())
	tenantID := t.ID
	limit := 10
	if l := r.URL.Query().Get("limit"); l != "" {
		if v := parseInt(l); v > 0 && v <= 50 {
			limit = v
		}
	}
	jobs, err := s.db.GetRecentJobs(r.Context(), tenantID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

// jobAuditHandler handles GET /jobs/{id}/audit.
func (s *ServerDeps) jobAuditHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	t := tenant.FromContext(r.Context())
	tenantID := t.ID
	jobID := extractJobIDFromPath(r.URL.Path, "/jobs/", "/audit")
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "Missing job ID")
		return
	}
	events, err := s.db.ListAuditEvents(r.Context(), tenantID, "job", jobID, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

// riskyVendorsHandler handles GET /vendors/risky.
func (s *ServerDeps) riskyVendorsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	t := tenant.FromContext(r.Context())
	tenantID := t.ID
	vendors, err := s.db.GetRiskyVendors(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vendors": vendors})
}

// recentComplianceHandler handles GET /compliance/recent.
func (s *ServerDeps) recentComplianceHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	t := tenant.FromContext(r.Context())
	tenantID := t.ID
	limit := 10
	if l := r.URL.Query().Get("limit"); l != "" {
		if v := parseInt(l); v > 0 && v <= 50 {
			limit = v
		}
	}
	records, err := s.db.GetRecentCompliance(r.Context(), tenantID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": records})
}

// opsSummaryHandler handles GET /ops/summary.
//
// Returns a deterministic manufacturing control-tower summary for the tenant:
// counts of POs/GRNs/Invoices and open exceptions (total + by severity). No
// fields without a data source are invented.
func (s *ServerDeps) opsSummaryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	t := tenant.FromContext(r.Context())
	tenantID := t.ID
	ctx := r.Context()

	pos, _ := s.db.ListPurchaseOrders(ctx, tenantID, 1, 0)
	grns, _ := s.db.ListGoodsReceipts(ctx, tenantID, "", 1, 0)
	invs, _ := s.db.ListInvoices(ctx, tenantID, "", 1, 0)
	openCases, _ := s.db.ListExceptionCases(ctx, tenantID, domain.ExceptionStatusOpen, "", 1000, 0)

	bySeverity := map[string]int{}
	for _, ec := range openCases {
		bySeverity[string(ec.Severity)]++
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id":                   tenantID,
		"purchase_orders":             len(pos),
		"goods_receipts":              len(grns),
		"invoices":                    len(invs),
		"open_exceptions":             len(openCases),
		"open_exceptions_by_severity": bySeverity,
		"generated_at":                time.Now().UTC(),
	})
}
