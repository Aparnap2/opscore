package usage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/middleware/tenant"
)

// ---------------------------------------------------------------------------
// Mock UsageProvider
// ---------------------------------------------------------------------------

type mockUsageProvider struct {
	mu          sync.Mutex
	usage       map[string]int64       // key: "tenantID:metric"
	tenantPlan  string                 // plan for the mock tenant
}

func newMockUsageProvider(plan string) *mockUsageProvider {
	return &mockUsageProvider{
		usage:      make(map[string]int64),
		tenantPlan: plan,
	}
}

func (m *mockUsageProvider) IncrementUsage(_ context.Context, tenantID string, metric domain.Metric, delta int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := tenantID + ":" + string(metric)
	m.usage[key] += delta
	return nil
}

func (m *mockUsageProvider) GetUsage(_ context.Context, tenantID string, metric domain.Metric) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := tenantID + ":" + string(metric)
	return m.usage[key], nil
}

func (m *mockUsageProvider) GetCurrentPeriodUsage(_ context.Context, tenantID string) (map[domain.Metric]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[domain.Metric]int64)
	for k, v := range m.usage {
		if strings.HasPrefix(k, tenantID+":") {
			result[domain.Metric(strings.TrimPrefix(k, tenantID+":"))] = v
		}
	}
	return result, nil
}

func (m *mockUsageProvider) CheckLimit(_ context.Context, tenantID string, metric domain.Metric) (bool, int64, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	limit := domain.GetLimit(m.tenantPlan, metric)
	if domain.IsUnlimited(limit) {
		return true, 0, limit, nil
	}
	key := tenantID + ":" + string(metric)
	current := m.usage[key]
	return current < limit, current, limit, nil
}

// PreSeedUsage sets the usage counter for a metric without going through IncrementUsage.
// Useful to simulate an already-at-limit scenario.
func (m *mockUsageProvider) PreSeedUsage(tenantID string, metric domain.Metric, count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := tenantID + ":" + string(metric)
	m.usage[key] = count
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// okHandler always returns 200 with a body.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
}

// createdHandler returns 201.
func createdHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"created"}`))
	})
}

// badRequestHandler returns 400.
func badRequestHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	})
}

// serverErrorHandler returns 500.
func serverErrorHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	})
}

// requestWithTenant creates a new GET request with a tenant injected into context.
func requestWithTenant(tenantID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	t := &domain.Tenant{
		ID:   tenantID,
		Name: "Test",
		Slug: "test",
		Plan: "starter",
	}
	ctx := tenant.WithTenant(req.Context(), t)
	return req.WithContext(ctx)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestCheckLimit_PassesThroughWhenUnderLimit(t *testing.T) {
	mock := newMockUsageProvider("starter")
	mw := NewMiddleware(mock)

	handler := mw.CheckLimit(domain.MetricDocumentsUploaded)(okHandler())

	req := requestWithTenant("tenant-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected body to pass through, got %v", body)
	}
}

func TestCheckLimit_Returns429WhenLimitExceeded(t *testing.T) {
	mock := newMockUsageProvider("starter")
	mw := NewMiddleware(mock)

	// Seed usage at or above the limit (starter plan allows 100 documents).
	mock.PreSeedUsage("tenant-1", domain.MetricDocumentsUploaded, 100)

	handler := mw.CheckLimit(domain.MetricDocumentsUploaded)(okHandler())

	req := requestWithTenant("tenant-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 Too Many Requests, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if body["error"] != "usage limit exceeded" {
		t.Errorf("expected error message 'usage limit exceeded', got %v", body["error"])
	}
	if body["metric"] != string(domain.MetricDocumentsUploaded) {
		t.Errorf("expected metric %q, got %v", domain.MetricDocumentsUploaded, body["metric"])
	}
	if _, ok := body["retry_at"]; !ok {
		t.Error("expected retry_at field in response")
	}
}

func TestCheckLimit_PassesThroughOnUnlimitedPlan(t *testing.T) {
	mock := newMockUsageProvider("business")
	mw := NewMiddleware(mock)

	// Business plan has unlimited LLM calls (limit == -1).
	// Even with high usage, it should pass through.
	mock.PreSeedUsage("tenant-biz", domain.MetricLLMCalls, 999999)

	handler := mw.CheckLimit(domain.MetricLLMCalls)(okHandler())

	req := requestWithTenant("tenant-biz")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK for unlimited plan, got %d", rec.Code)
	}
}

func TestCheckLimit_PassesThroughWhenNoTenantInContext(t *testing.T) {
	mock := newMockUsageProvider("starter")
	mw := NewMiddleware(mock)

	handler := mw.CheckLimit(domain.MetricDocumentsUploaded)(okHandler())

	// Request without a tenant in context.
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK when no tenant is in context, got %d", rec.Code)
	}
}

func TestIncrementOnResponse_IncrementsOn2xx(t *testing.T) {
	mock := newMockUsageProvider("starter")
	mw := NewMiddleware(mock)

	handler := mw.IncrementOnResponse(domain.MetricDocumentsUploaded)(createdHandler())

	req := requestWithTenant("tenant-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Should be 201 created.
	if rec.Code != http.StatusCreated {
		t.Errorf("expected 201 Created, got %d", rec.Code)
	}

	// Verify usage was incremented.
	count, err := mock.GetUsage(context.Background(), "tenant-1", domain.MetricDocumentsUploaded)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected usage count 1, got %d", count)
	}
}

func TestIncrementOnResponse_IncrementsOn200(t *testing.T) {
	mock := newMockUsageProvider("starter")
	mw := NewMiddleware(mock)

	handler := mw.IncrementOnResponse(domain.MetricComplianceChecks)(okHandler())

	req := requestWithTenant("tenant-2")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}

	// Verify usage was incremented.
	count, err := mock.GetUsage(context.Background(), "tenant-2", domain.MetricComplianceChecks)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected usage count 1, got %d", count)
	}
}

func TestIncrementOnResponse_DoesNotIncrementOn4xx(t *testing.T) {
	mock := newMockUsageProvider("starter")
	mw := NewMiddleware(mock)

	handler := mw.IncrementOnResponse(domain.MetricOCRPages)(badRequestHandler())

	req := requestWithTenant("tenant-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rec.Code)
	}

	// Verify usage was NOT incremented.
	count, err := mock.GetUsage(context.Background(), "tenant-1", domain.MetricOCRPages)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected usage count 0 (no increment on 4xx), got %d", count)
	}
}

func TestIncrementOnResponse_DoesNotIncrementOn5xx(t *testing.T) {
	mock := newMockUsageProvider("starter")
	mw := NewMiddleware(mock)

	handler := mw.IncrementOnResponse(domain.MetricLLMCalls)(serverErrorHandler())

	req := requestWithTenant("tenant-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 Internal Server Error, got %d", rec.Code)
	}

	// Verify usage was NOT incremented.
	count, err := mock.GetUsage(context.Background(), "tenant-1", domain.MetricLLMCalls)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected usage count 0 (no increment on 5xx), got %d", count)
	}
}

func TestIncrementOnResponse_DoesNotIncrementWhenNoTenant(t *testing.T) {
	mock := newMockUsageProvider("starter")
	mw := NewMiddleware(mock)

	handler := mw.IncrementOnResponse(domain.MetricOCRPages)(okHandler())

	// Request without tenant in context.
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Should still pass through.
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}

	// Verify no usage was recorded (no tenant means no key to increment).
	// Just verify no panic/error occurred.
}

func TestCheckLimit_RetryAtIsFutureTimestamp(t *testing.T) {
	mock := newMockUsageProvider("starter")
	mw := NewMiddleware(mock)

	mock.PreSeedUsage("tenant-1", domain.MetricDocumentsUploaded, 100)

	handler := mw.CheckLimit(domain.MetricDocumentsUploaded)(okHandler())

	req := requestWithTenant("tenant-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	retryAtStr, ok := body["retry_at"].(string)
	if !ok {
		t.Fatal("expected retry_at as string")
	}

	retryAt, err := time.Parse(time.RFC3339, retryAtStr)
	if err != nil {
		t.Fatalf("retry_at is not a valid RFC3339 timestamp: %v", err)
	}

	// retry_at should be some time in the future.
	if retryAt.Before(time.Now()) {
		t.Error("retry_at should be in the future")
	}
}

func TestCheckLimitAndIncrementOnResponse_Combined(t *testing.T) {
	mock := newMockUsageProvider("starter")
	mw := NewMiddleware(mock)

	// Chain: CheckLimit first, then IncrementOnResponse wraps the handler.
	handler := mw.CheckLimit(domain.MetricDocumentsUploaded)(
		mw.IncrementOnResponse(domain.MetricDocumentsUploaded)(okHandler()),
	)

	req := requestWithTenant("tenant-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}

	count, err := mock.GetUsage(context.Background(), "tenant-1", domain.MetricDocumentsUploaded)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected usage count 1 after combined middleware, got %d", count)
	}
}

func TestCheckLimitAndIncrementOnResponse_BlockWhenExceeded(t *testing.T) {
	mock := newMockUsageProvider("starter")
	mw := NewMiddleware(mock)

	// Seed at limit.
	mock.PreSeedUsage("tenant-1", domain.MetricDocumentsUploaded, 100)

	handler := mw.CheckLimit(domain.MetricDocumentsUploaded)(
		mw.IncrementOnResponse(domain.MetricDocumentsUploaded)(okHandler()),
	)

	req := requestWithTenant("tenant-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 Too Many Requests, got %d", rec.Code)
	}

	// IncrementOnResponse should NOT have fired since CheckLimit blocked.
	count, err := mock.GetUsage(context.Background(), "tenant-1", domain.MetricDocumentsUploaded)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if count != 100 {
		t.Errorf("expected usage count 100 (unchanged after block), got %d", count)
	}
}
