package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/middleware/tenant"
)

// vendorHandler handles POST /vendors and GET /vendors/{id}.
func (s *ServerDeps) vendorHandler(w http.ResponseWriter, r *http.Request) {
	t := tenant.FromContext(r.Context())
	tenantID := t.ID

	switch r.Method {
	case http.MethodPost:
		// Wrap the vendor creation with usage enforcement middleware.
		s.usageMW.CheckLimit(domain.MetricComplianceChecks)(
			s.usageMW.IncrementOnResponse(domain.MetricComplianceChecks)(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					s.handleVendorCreate(w, r, tenantID)
				}),
			),
		).ServeHTTP(w, r)
	case http.MethodGet:
		s.handleVendorGet(w, r, tenantID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *ServerDeps) handleVendorCreate(w http.ResponseWriter, r *http.Request, tenantID string) {
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Failed to parse form: %v", err))
		return
	}

	name := r.FormValue("name")
	gst := r.FormValue("gst")
	pan := r.FormValue("pan")
	ifsc := r.FormValue("ifsc")
	bankAccount := r.FormValue("bank_account")

	if name == "" {
		writeError(w, http.StatusBadRequest, "Name is required")
		return
	}

	vendorID := uuid.New().String()
	now := time.Now()
	ctx := r.Context()

	vendor := &domain.Vendor{
		ID:           vendorID,
		TenantID:     tenantID,
		Name:         name,
		GSTNumber:    gst,
		PANNumber:    pan,
		IFSCCode:     ifsc,
		BankAccount:  bankAccount,
		RiskScore:    0,
		RiskTier:     domain.RiskTierMedium,
		Approved:     false,
		TrustBattery: domain.TrustBattery{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.db.UpsertVendor(ctx, vendor); err != nil {
		slog.Error("Failed to create vendor", "err", err)
		writeError(w, http.StatusInternalServerError, "Failed to create vendor")
		return
	}

	vendorJob := &agents.VendorJob{
		TenantID: tenantID,
		JobID:    uuid.New().String(),
		VendorData: &agents.VendorData{
			Name:        name,
			GSTNumber:   gst,
			PANNumber:   pan,
			IFSCCode:    ifsc,
			BankAccount: bankAccount,
		},
	}

	if _, err := s.rq.Enqueue(ctx, QueueVendor, vendorJob); err != nil {
		slog.Error("Failed to enqueue vendor job", "err", err)
		_ = s.db.AppendAuditEvent(ctx, &domain.AuditEvent{
			TenantID:   tenantID,
			Actor:      "system",
			Action:     "ENQUEUE_FAILED",
			TargetType: "vendor",
			TargetID:   vendorID,
			NewState:   "FAILED",
			Error:      err.Error(),
			Timestamp:  now,
		})
		writeJSON(w, http.StatusAccepted, VendorResponse{
			VendorID: vendorID,
			Status:   "created",
			Message:  "Vendor created, but queuing for processing failed",
		})
		return
	}

	_ = s.db.AppendAuditEvent(ctx, &domain.AuditEvent{
		Actor:         "system",
		Action:        "VENDOR_CREATED",
		TargetType:    "vendor",
		TargetID:      vendorID,
		NewState:      "PENDING",
		Timestamp:     now,
		CorrelationID: r.Header.Get("X-Correlation-ID"),
		TraceID:       r.Header.Get("X-Trace-ID"),
	})

	slog.Info("Vendor created", "id", vendorID, "tenant", tenantID, "name", name)
	writeJSON(w, http.StatusCreated, VendorResponse{
		VendorID: vendorID,
		Status:   "queued",
		Message:  "Vendor created and queued for processing",
	})
}

func (s *ServerDeps) handleVendorGet(w http.ResponseWriter, r *http.Request, tenantID string) {
	vendorID := extractVendorID(r.URL.Path)
	if vendorID == "" {
		// List all vendors for the tenant.
		ctx := r.Context()
		vendors, err := s.db.ListVendors(ctx, tenantID)
		if err != nil {
			slog.Error("Failed to list vendors", "err", err)
			writeError(w, http.StatusInternalServerError, "Failed to list vendors")
			return
		}
		if vendors == nil {
			vendors = []*domain.Vendor{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"vendors": vendors})
		return
	}

	ctx := r.Context()
	vendor, err := s.db.GetVendor(ctx, vendorID, tenantID)
	if err != nil {
		slog.Error("Failed to get vendor", "err", err)
		writeError(w, http.StatusNotFound, "Vendor not found")
		return
	}

	if vendor.TenantID != tenantID {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	writeJSON(w, http.StatusOK, VendorGetResponse{
		VendorID:  vendor.ID,
		Name:      vendor.Name,
		Status:    vendorStatus(vendor),
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

func extractVendorID(path string) string {
	if idx := strings.LastIndex(path, "vendors/"); idx >= 0 {
		id := path[idx+len("vendors/"):]
		// Strip trailing slash.
		id = strings.TrimRight(id, "/")
		return id
	}
	return ""
}

func vendorStatus(v *domain.Vendor) string {
	if v.Approved {
		return "approved"
	}
	if v.TrustBattery.Tier == domain.TrustTierBlocked {
		return "blocked"
	}
	return "pending"
}
