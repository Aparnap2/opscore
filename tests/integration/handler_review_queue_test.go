package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aparna/opscore/internal/domain"
)

// ---------------------------------------------------------------------------
// Review-queue test server setup
// ---------------------------------------------------------------------------

type reviewQueueTestDeps struct {
	db    *mockHandlerDB
	slack *mockSlackParser
}

func newReviewQueueTestServer() *reviewQueueTestDeps {
	return &reviewQueueTestDeps{
		db:    newMockHandlerDB(),
		slack: &mockSlackParser{},
	}
}

// registerReviewQueueRoutes registers only the review-queue handlers on the given mux.
func (s *reviewQueueTestDeps) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/admin/review-queue", s.reviewQueueHandler)
	mux.HandleFunc("/admin/review-queue/", s.reviewQueueByIDHandler)
}

// parseInt converts a string to an int using fmt.Sscanf.
func parseInt(s string) int {
	var v int
	fmt.Sscanf(s, "%d", &v)
	return v
}

// --- Handler: /admin/review-queue ---

func (s *reviewQueueTestDeps) reviewQueueHandler(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
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

		allItems, err := s.db.ListHITLRequests(ctx, filterTenantID, status, 0, 0)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to list review requests")
			return
		}
		total := len(allItems)

		items, err := s.db.ListHITLRequests(ctx, filterTenantID, status, limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to list review requests")
			return
		}
		if items == nil {
			items = []*domain.HITLRequest{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": items,
			"total": total,
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

		hitlReq, err := s.db.GetHITLRequest(ctx, reqBody.HITLID, tenantID)
		if err != nil {
			writeError(w, http.StatusNotFound, "HITL request not found")
			return
		}
		if hitlReq.TenantID != tenantID {
			writeError(w, http.StatusForbidden, "Access denied")
			return
		}
		if hitlReq.Status != domain.HITLStatusPending {
			writeError(w, http.StatusConflict, "HITL request is not pending")
			return
		}

		var hitlStatus domain.HITLRequestStatus
		switch reqBody.Action {
		case "approve":
			hitlStatus = domain.HITLStatusApproved
		case "reject":
			hitlStatus = domain.HITLStatusRejected
		}

		now := time.Now()
		hitlReq.Status = hitlStatus
		hitlReq.RespondedAt = &now
		hitlReq.Decision = reqBody.Action
		_ = s.db.UpsertHITLRequest(ctx, hitlReq)

		_ = s.db.AppendAuditEvent(ctx, &domain.AuditEvent{
			TenantID:   tenantID,
			Actor:      "system",
			Action:     string(hitlStatus),
			TargetType: "hitl",
			TargetID:   reqBody.HITLID,
			OldState:   string(domain.HITLStatusPending),
			NewState:   string(hitlStatus),
			Timestamp:  time.Now(),
		})

		writeJSON(w, http.StatusOK, map[string]string{
			"status":  string(hitlStatus),
			"hitl_id": reqBody.HITLID,
		})

	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// --- Handler: /admin/review-queue/{id} ---

func (s *reviewQueueTestDeps) reviewQueueByIDHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}

	id := strings.TrimPrefix(r.URL.Path, "/admin/review-queue/")
	id = strings.TrimRight(id, "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Missing review request ID")
		return
	}

	ctx := r.Context()
	hitlReq, err := s.db.GetHITLRequest(ctx, id, tenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Review request not found")
		return
	}

	events, _ := s.db.ListAuditEvents(ctx, tenantID, "hitl", id, 50)
	if events == nil {
		events = []*domain.AuditEvent{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"request": hitlReq,
		"events":  events,
	})
}

// ---------------------------------------------------------------------------
// Auth helpers for testing
// ---------------------------------------------------------------------------

func authHeader(token string) string {
	return "Bearer " + token
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestReviewQueue_List_Pending(t *testing.T) {
	srv := newReviewQueueTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Pre-populate HITL requests with mixed statuses
	now := time.Now()
	req1 := &domain.HITLRequest{
		ID:       "hitl-001",
		TenantID: "default",
		JobID:    "job-001",
		Reason:   "High risk vendor onboarding",
		Status:   domain.HITLStatusPending,
		SentAt:   now.Add(-1 * time.Hour),
	}
	req2 := &domain.HITLRequest{
		ID:       "hitl-002",
		TenantID: "default",
		JobID:    "job-002",
		Reason:   "Document OCR confidence too low",
		Status:   domain.HITLStatusApproved,
		SentAt:   now.Add(-2 * time.Hour),
	}
	req3 := &domain.HITLRequest{
		ID:       "hitl-003",
		TenantID: "default",
		JobID:    "job-003",
		Reason:   "GSTIN verification failed",
		Status:   domain.HITLStatusPending,
		SentAt:   now.Add(-3 * time.Hour),
	}
	_ = srv.db.UpsertHITLRequest(nil, req1)
	_ = srv.db.UpsertHITLRequest(nil, req2)
	_ = srv.db.UpsertHITLRequest(nil, req3)

	resp, err := ts.Client().Get(ts.URL + "/admin/review-queue")
	if err != nil {
		t.Fatalf("GET /admin/review-queue: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}

	var listResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		t.Fatalf("Decode response: %v", err)
	}

	total, ok := listResp["total"].(float64)
	if !ok {
		t.Fatal("total field missing or not a number")
	}
	if int(total) != 3 {
		t.Errorf("total = %d, want 3", int(total))
	}

	items, ok := listResp["items"].([]any)
	if !ok {
		t.Fatal("items field missing or not an array")
	}
	if len(items) != 3 {
		t.Errorf("len(items) = %d, want 3", len(items))
	}
}

func TestReviewQueue_List_ByStatus(t *testing.T) {
	srv := newReviewQueueTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	now := time.Now()
	req1 := &domain.HITLRequest{
		ID:       "hitl-001",
		TenantID: "default",
		JobID:    "job-001",
		Reason:   "Pending request",
		Status:   domain.HITLStatusPending,
		SentAt:   now.Add(-1 * time.Hour),
	}
	req2 := &domain.HITLRequest{
		ID:       "hitl-002",
		TenantID: "default",
		JobID:    "job-002",
		Reason:   "Approved request",
		Status:   domain.HITLStatusApproved,
		SentAt:   now.Add(-2 * time.Hour),
	}
	req3 := &domain.HITLRequest{
		ID:       "hitl-003",
		TenantID: "default",
		JobID:    "job-003",
		Reason:   "Rejected request",
		Status:   domain.HITLStatusRejected,
		SentAt:   now.Add(-3 * time.Hour),
	}
	_ = srv.db.UpsertHITLRequest(nil, req1)
	_ = srv.db.UpsertHITLRequest(nil, req2)
	_ = srv.db.UpsertHITLRequest(nil, req3)

	// Filter by pending
	resp, err := ts.Client().Get(ts.URL + "/admin/review-queue?status=PENDING")
	if err != nil {
		t.Fatalf("GET /admin/review-queue?status=PENDING: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var listResp map[string]any
	json.NewDecoder(resp.Body).Decode(&listResp)

	total, _ := listResp["total"].(float64)
	if int(total) != 1 {
		t.Errorf("total = %d, want 1 for PENDING filter", int(total))
	}

	// Filter by approved
	resp2, err := ts.Client().Get(ts.URL + "/admin/review-queue?status=APPROVED")
	if err != nil {
		t.Fatalf("GET /admin/review-queue?status=APPROVED: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp2.StatusCode)
	}

	var listResp2 map[string]any
	json.NewDecoder(resp2.Body).Decode(&listResp2)

	total2, _ := listResp2["total"].(float64)
	if int(total2) != 1 {
		t.Errorf("total = %d, want 1 for APPROVED filter", int(total2))
	}
}

func TestReviewQueue_List_ByTenant(t *testing.T) {
	srv := newReviewQueueTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	now := time.Now()
	// Requests for tenant-a
	req1 := &domain.HITLRequest{
		ID:       "hitl-a-001",
		TenantID: "tenant-a",
		JobID:    "job-a-001",
		Reason:   "Tenant A request",
		Status:   domain.HITLStatusPending,
		SentAt:   now.Add(-1 * time.Hour),
	}
	// Requests for tenant-b
	req2 := &domain.HITLRequest{
		ID:       "hitl-b-001",
		TenantID: "tenant-b",
		JobID:    "job-b-001",
		Reason:   "Tenant B request",
		Status:   domain.HITLStatusPending,
		SentAt:   now.Add(-2 * time.Hour),
	}
	_ = srv.db.UpsertHITLRequest(nil, req1)
	_ = srv.db.UpsertHITLRequest(nil, req2)

	// List with tenant_id filter
	resp, err := ts.Client().Get(ts.URL + "/admin/review-queue?tenant_id=tenant-a")
	if err != nil {
		t.Fatalf("GET /admin/review-queue?tenant_id=tenant-a: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var listResp map[string]any
	json.NewDecoder(resp.Body).Decode(&listResp)

	total, _ := listResp["total"].(float64)
	if int(total) != 1 {
		t.Errorf("total = %d, want 1 for tenant-a", int(total))
	}

	items, _ := listResp["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatal("item is not a map")
	}
	if item["tenant_id"] != "tenant-a" {
		t.Errorf("tenant_id = %v, want tenant-a", item["tenant_id"])
	}
}

func TestReviewQueue_List_Pagination(t *testing.T) {
	srv := newReviewQueueTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	now := time.Now()
	// Create 5 HITL requests
	for i := 1; i <= 5; i++ {
		req := &domain.HITLRequest{
			ID:       uuid.New().String(),
			TenantID: "default",
			JobID:    uuid.New().String(),
			Reason:   "Request",
			Status:   domain.HITLStatusPending,
			SentAt:   now.Add(-time.Duration(i) * time.Hour),
		}
		_ = srv.db.UpsertHITLRequest(nil, req)
	}

	// Get first 2 items
	resp, err := ts.Client().Get(ts.URL + "/admin/review-queue?limit=2&offset=0")
	if err != nil {
		t.Fatalf("GET /admin/review-queue?limit=2&offset=0: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var listResp map[string]any
	json.NewDecoder(resp.Body).Decode(&listResp)

	items, _ := listResp["items"].([]any)
	if len(items) != 2 {
		t.Errorf("len(items) = %d, want 2 (limit=2)", len(items))
	}

	total, _ := listResp["total"].(float64)
	if int(total) != 5 {
		t.Errorf("total = %d, want 5", int(total))
	}

	// Get next 2 items (offset=2, limit=2)
	resp2, err := ts.Client().Get(ts.URL + "/admin/review-queue?limit=2&offset=2")
	if err != nil {
		t.Fatalf("GET /admin/review-queue?limit=2&offset=2: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp2.StatusCode)
	}

	var listResp2 map[string]any
	json.NewDecoder(resp2.Body).Decode(&listResp2)

	items2, _ := listResp2["items"].([]any)
	if len(items2) != 2 {
		t.Errorf("len(items) = %d, want 2 (limit=2, offset=2)", len(items2))
	}
}

func TestReviewQueue_Get_Single(t *testing.T) {
	srv := newReviewQueueTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Pre-populate a HITL request with associated audit events
	now := time.Now()
	hitlReq := &domain.HITLRequest{
		ID:       "hitl-single-001",
		TenantID: "default",
		JobID:    "job-001",
		Reason:   "Single request detail test",
		Status:   domain.HITLStatusPending,
		SentAt:   now.Add(-1 * time.Hour),
	}
	_ = srv.db.UpsertHITLRequest(nil, hitlReq)

	// Add some audit events
	_ = srv.db.AppendAuditEvent(nil, &domain.AuditEvent{
		TenantID:   "default",
		Actor:      "system",
		Action:     "SENT_FOR_REVIEW",
		TargetType: "hitl",
		TargetID:   "hitl-single-001",
		NewState:   "PENDING",
		Timestamp:  now.Add(-30 * time.Minute),
	})

	resp, err := ts.Client().Get(ts.URL + "/admin/review-queue/hitl-single-001")
	if err != nil {
		t.Fatalf("GET /admin/review-queue/hitl-single-001: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}

	var getResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&getResp); err != nil {
		t.Fatalf("Decode response: %v", err)
	}

	reqData, ok := getResp["request"].(map[string]any)
	if !ok {
		t.Fatal("request field missing or not a map")
	}
	if reqData["id"] != "hitl-single-001" {
		t.Errorf("request.id = %v, want hitl-single-001", reqData["id"])
	}
	if reqData["reason"] != "Single request detail test" {
		t.Errorf("request.reason = %v, want 'Single request detail test'", reqData["reason"])
	}
	if reqData["status"] != "PENDING" {
		t.Errorf("request.status = %v, want PENDING", reqData["status"])
	}

	events, ok := getResp["events"].([]any)
	if !ok {
		t.Fatal("events field missing or not an array")
	}
	if len(events) < 1 {
		t.Error("expected at least 1 audit event")
	}
}

func TestReviewQueue_Get_NotFound(t *testing.T) {
	srv := newReviewQueueTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/admin/review-queue/nonexistent-id")
	if err != nil {
		t.Fatalf("GET /admin/review-queue/nonexistent-id: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}

	var errResp map[string]string
	json.NewDecoder(resp.Body).Decode(&errResp)
	if errResp["error"] == "" {
		t.Error("expected error message for not found")
	}
}

func TestReviewQueue_List_WrongMethod(t *testing.T) {
	srv := newReviewQueueTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// DELETE is not allowed
	req, _ := http.NewRequest("DELETE", ts.URL+"/admin/review-queue", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("DELETE /admin/review-queue: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestReviewQueue_Get_WrongMethod(t *testing.T) {
	srv := newReviewQueueTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// POST on single-item endpoint is not allowed
	body := strings.NewReader(`{"hitl_id":"test","action":"approve"}`)
	resp, err := ts.Client().Post(ts.URL+"/admin/review-queue/some-id", "application/json", body)
	if err != nil {
		t.Fatalf("POST /admin/review-queue/some-id: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestReviewQueue_List_Empty(t *testing.T) {
	srv := newReviewQueueTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// No HITL requests created — should return empty list
	resp, err := ts.Client().Get(ts.URL + "/admin/review-queue")
	if err != nil {
		t.Fatalf("GET /admin/review-queue: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var listResp map[string]any
	json.NewDecoder(resp.Body).Decode(&listResp)

	total, _ := listResp["total"].(float64)
	if int(total) != 0 {
		t.Errorf("total = %d, want 0 for empty list", int(total))
	}

	items, _ := listResp["items"].([]any)
	if items == nil {
		t.Error("items should be empty array, not nil")
	}
	if len(items) != 0 {
		t.Errorf("len(items) = %d, want 0", len(items))
	}
}

func TestReviewQueue_Get_MissingID(t *testing.T) {
	srv := newReviewQueueTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Accessing the subtree root (no ID) should return 400
	resp, err := ts.Client().Get(ts.URL + "/admin/review-queue/")
	if err != nil {
		t.Fatalf("GET /admin/review-queue/: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestReviewQueue_POST_Approve(t *testing.T) {
	srv := newReviewQueueTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Pre-populate a pending HITL request
	now := time.Now()
	hitlReq := &domain.HITLRequest{
		ID:       "hitl-approve-001",
		TenantID: "default",
		JobID:    "job-001",
		Reason:   "Approve me",
		Status:   domain.HITLStatusPending,
		SentAt:   now.Add(-1 * time.Hour),
	}
	_ = srv.db.UpsertHITLRequest(nil, hitlReq)

	// Approve via POST
	payload := map[string]string{
		"hitl_id": "hitl-approve-001",
		"action":  "approve",
	}
	bodyBytes, _ := json.Marshal(payload)
	resp, err := ts.Client().Post(ts.URL+"/admin/review-queue", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("POST /admin/review-queue: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}

	var postResp map[string]string
	json.NewDecoder(resp.Body).Decode(&postResp)
	if postResp["status"] != "APPROVED" {
		t.Errorf("status = %s, want APPROVED", postResp["status"])
	}

	// Verify the HITL request was updated
	updated, err := srv.db.GetHITLRequest(nil, "hitl-approve-001", "default")
	if err != nil {
		t.Fatalf("GetHITLRequest: %v", err)
	}
	if updated.Status != domain.HITLStatusApproved {
		t.Errorf("HITL status = %s, want APPROVED", updated.Status)
	}
	if updated.RespondedAt == nil {
		t.Error("RespondedAt should be set")
	}
}

func TestReviewQueue_POST_Reject(t *testing.T) {
	srv := newReviewQueueTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	now := time.Now()
	hitlReq := &domain.HITLRequest{
		ID:       "hitl-reject-001",
		TenantID: "default",
		JobID:    "job-002",
		Reason:   "Reject me",
		Status:   domain.HITLStatusPending,
		SentAt:   now.Add(-1 * time.Hour),
	}
	_ = srv.db.UpsertHITLRequest(nil, hitlReq)

	payload := map[string]string{
		"hitl_id": "hitl-reject-001",
		"action":  "reject",
	}
	bodyBytes, _ := json.Marshal(payload)
	resp, err := ts.Client().Post(ts.URL+"/admin/review-queue", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("POST /admin/review-queue: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var postResp map[string]string
	json.NewDecoder(resp.Body).Decode(&postResp)
	if postResp["status"] != "REJECTED" {
		t.Errorf("status = %s, want REJECTED", postResp["status"])
	}

	updated, _ := srv.db.GetHITLRequest(nil, "hitl-reject-001", "default")
	if updated.Status != domain.HITLStatusRejected {
		t.Errorf("HITL status = %s, want REJECTED", updated.Status)
	}
}

func TestReviewQueue_POST_AlreadyProcessed(t *testing.T) {
	srv := newReviewQueueTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	now := time.Now()
	respondedAt := now.Add(-1 * time.Hour)
	hitlReq := &domain.HITLRequest{
		ID:          "hitl-done-001",
		TenantID:    "default",
		JobID:       "job-003",
		Reason:      "Already done",
		Status:      domain.HITLStatusApproved,
		SentAt:      now.Add(-2 * time.Hour),
		RespondedAt: &respondedAt,
		Decision:    "approve",
	}
	_ = srv.db.UpsertHITLRequest(nil, hitlReq)

	payload := map[string]string{
		"hitl_id": "hitl-done-001",
		"action":  "approve",
	}
	bodyBytes, _ := json.Marshal(payload)
	resp, err := ts.Client().Post(ts.URL+"/admin/review-queue", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("POST /admin/review-queue: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

func TestReviewQueue_POST_NotFound(t *testing.T) {
	srv := newReviewQueueTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	payload := map[string]string{
		"hitl_id": "nonexistent",
		"action":  "approve",
	}
	bodyBytes, _ := json.Marshal(payload)
	resp, err := ts.Client().Post(ts.URL+"/admin/review-queue", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("POST /admin/review-queue: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestReviewQueue_POST_MissingFields(t *testing.T) {
	srv := newReviewQueueTestServer()
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Missing hitl_id
	payload := map[string]string{
		"action": "approve",
	}
	bodyBytes, _ := json.Marshal(payload)
	resp, err := ts.Client().Post(ts.URL+"/admin/review-queue", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("POST /admin/review-queue: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (missing hitl_id)", resp.StatusCode)
	}

	// Invalid action
	payload2 := map[string]string{
		"hitl_id": "test-id",
		"action":  "invalid",
	}
	bodyBytes2, _ := json.Marshal(payload2)
	resp2, err := ts.Client().Post(ts.URL+"/admin/review-queue", "application/json", bytes.NewReader(bodyBytes2))
	if err != nil {
		t.Fatalf("POST /admin/review-queue: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (invalid action)", resp2.StatusCode)
	}
}
