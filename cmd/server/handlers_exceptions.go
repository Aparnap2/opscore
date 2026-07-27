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

// resolveExceptionHandler handles POST /exceptions/{id}/resolve.
//
// It transitions an ExceptionCase to a target lifecycle status (RESOLVED,
// ACKNOWLEDGED, or DISMISSED), enforcing tenant isolation via
// GetExceptionCaseByID and recording an audit event on every successful
// transition. Deterministic — no LLM involvement.
func (s *ServerDeps) resolveExceptionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	t := tenant.FromContext(r.Context())
	tenantID := t.ID

	// Extract ID from path: /exceptions/{id}/resolve
	id := strings.TrimPrefix(r.URL.Path, "/exceptions/")
	id = strings.TrimSuffix(id, "/resolve")
	id = strings.TrimRight(id, "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Missing exception case ID")
		return
	}

	// Decode request body.
	var reqBody struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate the target status against the allowed lifecycle constants.
	switch reqBody.Status {
	case domain.ExceptionStatusResolved, domain.ExceptionStatusAck, domain.ExceptionStatusDismissed:
		// allowed
	default:
		writeError(w, http.StatusBadRequest, "status must be one of RESOLVED, ACKNOWLEDGED, DISMISSED")
		return
	}

	ctx := r.Context()

	// Fetch the current case (tenant-scoped; returns ErrNotFound for other
	// tenants, giving us 404 + tenant isolation for free).
	current, err := s.db.GetExceptionCaseByID(ctx, id, tenantID)
	if err != nil {
		slog.Error("Failed to get exception case", "id", id, "err", err)
		writeError(w, http.StatusNotFound, "Exception case not found")
		return
	}
	oldStatus := current.Status

	// Apply the status transition.
	if err := s.db.UpdateExceptionCaseStatus(ctx, id, tenantID, reqBody.Status); err != nil {
		slog.Error("Failed to update exception case status", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "Failed to update exception case")
		return
	}

	// Resolve the actor from the authenticated principal (fall back to "api").
	actor := "api"
	if u := auth.FromContext(ctx); u != nil {
		actor = u.ID
		if actor == "" {
			actor = u.Email
		}
	}

	// Record the transition in the audit log.
	auditEvent := &domain.AuditEvent{
		TenantID:   tenantID,
		Actor:      actor,
		Action:     "EXCEPTION_STATUS_CHANGED",
		TargetType: "exception_case",
		TargetID:   id,
		OldState:   oldStatus,
		NewState:   reqBody.Status,
		Timestamp:  time.Now().UTC(),
	}
	if reqBody.Note != "" {
		auditEvent.Error = reqBody.Note
	}
	if err := s.db.AppendAuditEvent(ctx, auditEvent); err != nil {
		// Audit is best-effort; log but do not fail the transition.
		slog.Error("Failed to append audit event for exception status change", "id", id, "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":     id,
		"status": reqBody.Status,
	})
}

// exceptionsHandler handles GET /exceptions.
//
// Lists ExceptionCase rows for the tenant, optionally filtered by `status` and
// `type` query params, with `limit`/`offset` pagination. Deterministic.
func (s *ServerDeps) exceptionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	t := tenant.FromContext(r.Context())
	tenantID := t.ID

	q := r.URL.Query()
	status := q.Get("status")
	mismatchType := q.Get("type")
	limit := 50
	if l := q.Get("limit"); l != "" {
		if v := parseInt(l); v > 0 && v <= 200 {
			limit = v
		}
	}
	offset := 0
	if o := q.Get("offset"); o != "" {
		if v := parseInt(o); v >= 0 {
			offset = v
		}
	}

	cases, err := s.db.ListExceptionCases(r.Context(), tenantID, status, mismatchType, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": cases})
}

// exceptionByIDHandler handles GET /exceptions/{id}.
func (s *ServerDeps) exceptionByIDHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	t := tenant.FromContext(r.Context())
	tenantID := t.ID

	id := strings.TrimPrefix(r.URL.Path, "/exceptions/")
	id = strings.TrimRight(id, "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Missing exception case ID")
		return
	}

	ec, err := s.db.GetExceptionCaseByID(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Exception case not found")
		return
	}
	writeJSON(w, http.StatusOK, ec)
}
