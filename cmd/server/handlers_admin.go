package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/middleware/auth"
	"github.com/aparna/opscore/internal/middleware/tenant"
)

// reviewQueueHandler handles GET /admin/review-queue (list with filters) and
// POST /admin/review-queue (approve/reject a HITL request).
func (s *ServerDeps) reviewQueueHandler(w http.ResponseWriter, r *http.Request) {
	t := tenant.FromContext(r.Context())
	tenantID := t.ID
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		// Parse query params for filtering and pagination.
		status := r.URL.Query().Get("status")
		filterTenantID := r.URL.Query().Get("tenant_id")
		if filterTenantID == "" {
			filterTenantID = tenantID
		}

		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			if v := parseInt(l); v > 0 && v <= 100 {
				limit = v
			}
		}
		offset := 0
		if o := r.URL.Query().Get("offset"); o != "" {
			if v := parseInt(o); v >= 0 {
				offset = v
			}
		}

		items, err := s.db.ListHITLRequests(ctx, filterTenantID, status, limit, offset)
		if err != nil {
			slog.Error("Failed to list HITL requests", "err", err)
			writeError(w, http.StatusInternalServerError, "Failed to list review requests")
			return
		}
		if items == nil {
			items = []*domain.HITLRequest{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": items,
			"total": len(items),
		})

	case http.MethodPost:
		var reqBody struct {
			HITLID string `json:"hitl_id"`
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		if reqBody.HITLID == "" {
			writeError(w, http.StatusBadRequest, "hitl_id is required")
			return
		}
		if reqBody.Action != "approve" && reqBody.Action != "reject" {
			writeError(w, http.StatusBadRequest, "action must be 'approve' or 'reject'")
			return
		}

		// Fetch the HITL request.
		hitlReq, err := s.db.GetHITLRequest(ctx, reqBody.HITLID, tenantID)
		if err != nil {
			slog.Error("Failed to get HITL request", "hitl_id", reqBody.HITLID, "err", err)
			writeError(w, http.StatusNotFound, "HITL request not found")
			return
		}
		// Verify tenant ownership.
		if hitlReq.TenantID != tenantID {
			writeError(w, http.StatusForbidden, "Access denied")
			return
		}
		// Verify it's still pending.
		if hitlReq.Status != domain.HITLStatusPending {
			writeError(w, http.StatusConflict, "HITL request is not pending")
			return
		}

		var newJobStatus domain.JobStatus
		var hitlStatus domain.HITLRequestStatus
		switch reqBody.Action {
		case "approve":
			newJobStatus = domain.JobStatusCompleted
			hitlStatus = domain.HITLStatusApproved
		case "reject":
			newJobStatus = domain.JobStatusFailed
			hitlStatus = domain.HITLStatusRejected
		}

		// Resolve the acting user for audit.
		user := auth.FromContext(r.Context())
		responderID := "system"
		if user != nil {
			responderID = user.ID
		}

		// Update the associated job status.
		job, err := s.db.GetJob(ctx, hitlReq.JobID, tenantID)
		if err == nil {
			job.Status = newJobStatus
			job.UpdatedAt = time.Now()
			_ = s.db.UpsertJob(ctx, job)
		}

		// Update the HITL request record.
		now := time.Now()
		hitlReq.Status = hitlStatus
		hitlReq.RespondedAt = &now
		hitlReq.Responder = responderID
		hitlReq.Decision = reqBody.Action
		_ = s.db.UpsertHITLRequest(ctx, hitlReq)

		// Record an audit event.
		_ = s.db.AppendAuditEvent(ctx, &domain.AuditEvent{
			TenantID:   tenantID,
			Actor:      responderID,
			Action:     string(hitlStatus),
			TargetType: "hitl",
			TargetID:   reqBody.HITLID,
			OldState:   string(domain.HITLStatusPending),
			NewState:   string(hitlStatus),
			Timestamp:  time.Now(),
		})

		slog.Info("HITL decision via admin API", "hitl_id", reqBody.HITLID, "action", reqBody.Action, "user", responderID)
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  string(hitlStatus),
			"hitl_id": reqBody.HITLID,
		})

	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// reviewQueueByIDHandler handles GET /admin/review-queue/{id}.
func (s *ServerDeps) reviewQueueByIDHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	t := tenant.FromContext(r.Context())
	tenantID := t.ID

	// Extract ID from path: /admin/review-queue/{id}
	id := strings.TrimPrefix(r.URL.Path, "/admin/review-queue/")
	id = strings.TrimRight(id, "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Missing review request ID")
		return
	}

	ctx := r.Context()
	hitlReq, err := s.db.GetHITLRequest(ctx, id, tenantID)
	if err != nil {
		slog.Error("Failed to get HITL request", "id", id, "err", err)
		writeError(w, http.StatusNotFound, "Review request not found")
		return
	}

	// Get audit events for this HITL request.
	events, err := s.db.ListAuditEvents(ctx, tenantID, "hitl", id, 50)
	if err != nil {
		slog.Error("Failed to list audit events for HITL request", "id", id, "err", err)
		events = []*domain.AuditEvent{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"request": hitlReq,
		"events":  events,
	})
}

// adminDashboardHandler handles GET /admin/dashboard — consolidated tenant dashboard summary.
func (s *ServerDeps) adminDashboardHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	t := tenant.FromContext(r.Context())
	tenantID := t.ID
	ctx := r.Context()

	// Job counts.
	allJobs, err := s.db.ListJobs(ctx, tenantID, "", "")
	if err != nil {
		slog.Error("Failed to list jobs for dashboard", "err", err)
		allJobs = []*domain.Job{}
	}
	totalJobs := len(allJobs)
	completedJobs := 0
	failedJobs := 0
	for _, j := range allJobs {
		switch j.Status {
		case domain.JobStatusCompleted:
			completedJobs++
		case domain.JobStatusFailed, domain.JobStatusTerminalFailed, domain.JobStatusDeadLettered:
			failedJobs++
		}
	}

	// Vendor count.
	vendors, err := s.db.ListVendors(ctx, tenantID)
	if err != nil {
		slog.Error("Failed to list vendors for dashboard", "err", err)
		vendors = []*domain.Vendor{}
	}

	// Pending HITL reviews.
	pendingHITL, err := s.db.ListPendingHITL(ctx, tenantID)
	if err != nil {
		slog.Error("Failed to list pending HITL for dashboard", "err", err)
		pendingHITL = []*domain.HITLRequest{}
	}

	// Recent activity from audit events (tenant-wide, no target filter).
	recentEvents, err := s.db.ListAuditEvents(ctx, tenantID, "", "", 10)
	if err != nil {
		slog.Error("Failed to list audit events for dashboard", "err", err)
		recentEvents = []*domain.AuditEvent{}
	}
	recentActivity := make([]map[string]any, 0, len(recentEvents))
	for _, ev := range recentEvents {
		recentActivity = append(recentActivity, map[string]any{
			"time":      ev.Timestamp,
			"event":     ev.Action,
			"target_id": ev.TargetID,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tenant": map[string]any{
			"id":     t.ID,
			"name":   t.Name,
			"plan":   t.Plan,
			"status": t.Status,
		},
		"summary": map[string]any{
			"total_jobs":      totalJobs,
			"total_vendors":   len(vendors),
			"pending_reviews": len(pendingHITL),
			"completed_jobs":  completedJobs,
			"failed_jobs":     failedJobs,
		},
		"recent_activity": recentActivity,
	})
}

// adminUsageHandler handles GET /admin/usage.
// Returns current usage for the authenticated tenant, including plan limits.
func (s *ServerDeps) adminUsageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	t := tenant.FromContext(r.Context())
	tenantID := t.ID
	ctx := r.Context()

	// Get tenant details for plan info.
	tenant, err := s.db.GetTenant(ctx, tenantID)
	if err != nil {
		slog.Error("Failed to get tenant for usage", "err", err)
		writeError(w, http.StatusInternalServerError, "Failed to get tenant")
		return
	}

	// Get current period usage.
	usage, err := s.db.GetCurrentPeriodUsage(ctx, tenantID)
	if err != nil {
		slog.Error("Failed to get current period usage", "err", err)
		writeError(w, http.StatusInternalServerError, "Failed to get usage")
		return
	}

	// Build response with all known metrics.
	type metricInfo struct {
		Current int64 `json:"current"`
		Limit   int64 `json:"limit"`
	}
	usageResp := make(map[string]metricInfo)

	allMetrics := []domain.Metric{
		domain.MetricDocumentsUploaded,
		domain.MetricOCRPages,
		domain.MetricLLMCalls,
		domain.MetricComplianceChecks,
	}
	for _, m := range allMetrics {
		limit := domain.GetLimit(tenant.Plan, m)
		current := usage[m]
		usageResp[string(m)] = metricInfo{Current: current, Limit: limit}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id": tenantID,
		"plan":      tenant.Plan,
		"usage":     usageResp,
	})
}
