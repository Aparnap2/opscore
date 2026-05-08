package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
	"github.com/google/uuid"
)

// HTTPVendorHandler handles POST /api/vendors and GET /api/vendors/{id}
func HTTPVendorHandler(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	log.Printf("Received vendor request: %s %s", r.Method, r.URL.Path)

	// Extract tenant ID from header
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}

	switch r.Method {
	case http.MethodPost:
		handleVendorCreate(ctx, w, r, tenantID)
	case http.MethodGet:
		handleVendorGet(ctx, w, r, tenantID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleVendorCreate handles POST /api/vendors
// Creates vendor in Cosmos DB and enqueues to vendor-queue for processing
func handleVendorCreate(ctx context.Context, w http.ResponseWriter, r *http.Request, tenantID string) {
	// Parse form data (application/x-www-form-urlencoded or multipart/form-data)
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Failed to parse form: %v", err))
		return
	}

	// Extract vendor form data
	name := r.FormValue("name")
	gst := r.FormValue("gst")
	pan := r.FormValue("pan")
	ifsc := r.FormValue("ifsc")
	bankAccount := r.FormValue("bank_account")
	address := r.FormValue("address")

	// Validate required fields
	if name == "" {
		writeError(w, http.StatusBadRequest, "Name is required")
		return
	}

	// Get adapters
	cosmosAdapter, err := getCosmosAdapter(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Database service unavailable")
		return
	}

	queueAdapter, err := getQueueAdapter(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Queue service unavailable")
		return
	}

	// Generate vendor ID
	vendorID := uuid.New().String()
	now := time.Now()

	// Create vendor in Cosmos DB
	vendor := &domain.Vendor{
		ID:           vendorID,
		TenantID:     tenantID,
		Name:         name,
		GSTNumber:    gst,
		PANNumber:    pan,
		IFSCCode:     ifsc,
		BankAccount: bankAccount,
		RiskScore:   0,
		RiskTier:    domain.RiskTierMedium,
		Approved:    false,
		TrustBattery: domain.TrustBattery{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := cosmosAdapter.UpsertVendor(ctx, vendor); err != nil {
		log.Printf("Failed to create vendor: %v", err)
		writeError(w, http.StatusInternalServerError, "Failed to create vendor")
		return
	}

	// Enqueue vendor processing job
	vendorJob := &agents.VendorJob{
		TenantID: tenantID,
		JobID:    uuid.New().String(),
		VendorData: &agents.VendorData{
			Name:        name,
			GSTNumber:   gst,
			PANNumber:   pan,
			IFSCCode:    ifsc,
			BankAccount: bankAccount,
			Address:     address,
		},
	}

	if _, err := queueAdapter.Enqueue(ctx, QueueVendor, vendorJob); err != nil {
		log.Printf("Failed to enqueue vendor job: %v", err)
		// Vendor is created, but queueing failed - still return success but with warning
		writeJSON(w, http.StatusAccepted, VendorResponse{
			VendorID: vendorID,
			Status:   "created",
			Message:  "Vendor created, but queuing for processing failed",
		})
		return
	}

	// Log audit event
	cosmosAdapter.AppendAuditEvent(ctx, &domain.AuditEvent{
		Actor:         "system",
		Action:        "VENDOR_CREATED",
		TargetType:    "vendor",
		TargetID:      vendorID,
		NewState:      "PENDING",
		Timestamp:     now,
		CorrelationID: r.Header.Get("X-Correlation-ID"),
		TraceID:       r.Header.Get("X-Trace-ID"),
	})

	log.Printf("Vendor created successfully: id=%s, tenant=%s, name=%s", vendorID, tenantID, name)
	writeJSON(w, http.StatusCreated, VendorResponse{
		VendorID: vendorID,
		Status:   "queued",
		Message:  "Vendor created and queued for processing",
	})
}

// handleVendorGet handles GET /api/vendors/{id}
func handleVendorGet(ctx context.Context, w http.ResponseWriter, r *http.Request, tenantID string) {
	// Extract vendor ID from path - the route is "vendors/{vendorId?}"
	vendorID := extractVendorID(r.URL.Path)
	if vendorID == "" {
		writeError(w, http.StatusBadRequest, "Vendor ID is required")
		return
	}

	// Get Cosmos adapter
	cosmosAdapter, err := getCosmosAdapter(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Database service unavailable")
		return
	}

	// Get vendor from Cosmos
	vendor, err := cosmosAdapter.GetVendor(ctx, vendorID)
	if err != nil {
		log.Printf("Failed to get vendor: %v", err)
		writeError(w, http.StatusNotFound, "Vendor not found")
		return
	}

	// Verify tenant ownership
	if vendor.TenantID != tenantID && tenantID != "default" {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	writeJSON(w, http.StatusOK, VendorGetResponse{
		VendorID:  vendor.ID,
		Name:      vendor.Name,
		Status:    getVendorStatus(vendor),
		GSTNumber: vendor.GSTNumber,
		PANNumber: vendor.PANNumber,
		IFSCCode:  vendor.IFSCCode,
		RiskScore: vendor.RiskScore,
		RiskTier:  string(vendor.RiskTier),
		Approved:  vendor.Approved,
		TrustTier: string(vendor.TrustBattery.Tier),
		CreatedAt: vendor.CreatedAt,
		UpdatedAt: vendor.UpdatedAt,
	})
}

// extractVendorID extracts vendor ID from URL path
func extractVendorID(path string) string {
	// Path format: /api/vendors/{id} or /vendors/{id}
	// Need to extract the last segment after "vendors/"
	if idx := findLastSegment(path, "vendors/"); idx >= 0 {
		return path[idx+len("vendors/"):]
	}
	return ""
}

// findLastSegment finds the last occurrence of a prefix and returns the index
func findLastSegment(path, prefix string) int {
	for i := len(path) - 1; i >= len(prefix); i-- {
		if i >= len(prefix)-1 && path[i-len(prefix)+1:i+1] == prefix {
			return i - len(prefix) + 1
		}
	}
	// Simple check for prefix at end
	if len(path) > len(prefix) && path[len(path)-len(prefix)-1:] == "/"+prefix[:len(prefix)-1] {
		return len(path) - len(prefix) - 1
	}
	// Direct check
	for i := 0; i <= len(path)-len(prefix)-1; i++ {
		if path[i:i+len(prefix)] == prefix {
			return i
		}
	}
	return -1
}

// getVendorStatus returns vendor status string
func getVendorStatus(vendor *domain.Vendor) string {
	if vendor.Approved {
		return "approved"
	}
	if vendor.TrustBattery.Tier == domain.TrustTierBlocked {
		return "blocked"
	}
	return "pending"
}

// Response types
type VendorResponse struct {
	VendorID string `json:"vendor_id"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
}

type VendorGetResponse struct {
	VendorID  string    `json:"vendor_id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	GSTNumber string    `json:"gst_number,omitempty"`
	PANNumber string    `json:"pan_number,omitempty"`
	IFSCCode  string    `json:"ifsc_code,omitempty"`
	RiskScore int       `json:"risk_score"`
	RiskTier  string    `json:"risk_tier"`
	Approved  bool      `json:"approved"`
	TrustTier string    `json:"trust_tier"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
