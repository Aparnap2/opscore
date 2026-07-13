package agents

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aparna/opscore/internal/adapters/postgres"
	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
)

// VendorAgent handles vendor onboarding workflows
type VendorAgent struct {
	db        providers.DBProvider
	validator *domain.IndiaValidator
	llm       providers.LLMProvider
	tracer    providers.TracingProvider
}

// NewVendorAgent creates a new vendor agent
func NewVendorAgent(
	db providers.DBProvider,
	validator *domain.IndiaValidator,
	llm providers.LLMProvider,
	tracer providers.TracingProvider,
) *VendorAgent {
	return &VendorAgent{
		db:        db,
		validator: validator,
		llm:       llm,
		tracer:    tracer,
	}
}

// VendorJob represents a vendor onboarding job
type VendorJob struct {
	VendorData *VendorData `json:"vendor_data"`
	TenantID   string      `json:"tenant_id"`
	JobID      string      `json:"job_id"`
}

type VendorData struct {
	Name        string   `json:"name"`
	GSTNumber   string   `json:"gst_number,omitempty"`
	PANNumber   string   `json:"pan_number,omitempty"`
	IFSCCode    string   `json:"ifsc_code,omitempty"`
	BankAccount string   `json:"bank_account,omitempty"`
	Address     string   `json:"address,omitempty"`
	Documents   []string `json:"documents,omitempty"`
}

// ProcessVendor handles the complete vendor onboarding workflow
func (a *VendorAgent) ProcessVendor(ctx context.Context, job *VendorJob) (result map[string]any, err error) {
	ctx, span := a.tracer.StartSpan(ctx, "vendor_agent.process",
		providers.WithJobID(job.JobID),
		providers.WithTenantID(job.TenantID),
		providers.WithWorkflowType("vendor_onboarding"),
	)
	defer func() {
		span.End(err)
	}()

	data := job.VendorData

	// Step 1: Validate identifiers
	span.SetAttribute("step", "validate_identifiers")
	validations := a.validator.ValidateVendor(data.GSTNumber, data.PANNumber, data.IFSCCode)
	validationErrors := 0
	if validations != nil {
		validationErrors = len(validations.Errors)
	}
	span.SetAttribute("validation_errors", fmt.Sprintf("%d", validationErrors))

	// Step 2: Compute risk score using domain-level function
	span.SetAttribute("step", "compute_risk")
	vendorForRisk := &domain.Vendor{
		GSTNumber: data.GSTNumber,
		PANNumber: data.PANNumber,
		IFSCCode:  data.IFSCCode,
	}
	riskResult := domain.ComputeVendorRisk(vendorForRisk, nil, nil, job.TenantID)
	riskScore := riskResult.Score
	span.SetAttribute("risk_score", fmt.Sprintf("%d", riskScore))

	needsHITL := riskScore >= 60 || validationErrors > 0

	result = map[string]any{
		"name":        data.Name,
		"validations": validations,
		"risk_score":  riskScore,
		"trust_tier":  domain.TrustTierStandard,
		"needs_hitl":  needsHITL,
	}

	// Step 3: LLM analysis for non-standard vendors (optional)
	if a.llm != nil && len(data.Documents) > 0 {
		span.SetAttribute("step", "llm_analysis")
		prompt := fmt.Sprintf("Analyze vendor documents for %s: %v", data.Name, data.Documents)
		reasoning, llmErr := a.llm.Reason(ctx, prompt)
		if llmErr == nil {
			result["llm_analysis"] = reasoning
			a.tracer.RecordLLMCall(ctx, "sarvam-llm", 0, 0.0, 0)
		}
	}

	// Determine trust battery
	trustTier := domain.TrustTierProbation
	if riskScore < 30 && validationErrors == 0 {
		trustTier = domain.TrustTierPreferred
	} else if riskScore < 60 {
		trustTier = domain.TrustTierStandard
	}
	span.SetAttribute("trust_tier", string(trustTier))

	result["trust_tier"] = trustTier

	// Save vendor
	span.SetAttribute("step", "save_vendor")
	vendor := &domain.Vendor{
		ID:           job.JobID,
		TenantID:     job.TenantID,
		Name:         data.Name,
		GSTNumber:    data.GSTNumber,
		PANNumber:    data.PANNumber,
		IFSCCode:     data.IFSCCode,
		BankAccount:  data.BankAccount,
		RiskScore:    riskScore,
		RiskTier:     riskResult.Tier,
		TrustBattery: *domain.NewTrustBattery(),
		Approved:     riskScore < 60 && validationErrors == 0,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	// Set trust battery tier based on risk score
	if riskScore >= 80 {
		vendor.TrustBattery.Tier = domain.TrustTierPreferred
	} else if riskScore >= 50 {
		vendor.TrustBattery.Tier = domain.TrustTierStandard
	} else if riskScore >= 30 {
		vendor.TrustBattery.Tier = domain.TrustTierProbation
	} else {
		vendor.TrustBattery.Tier = domain.TrustTierBlocked
	}

	if err := a.db.UpsertVendor(ctx, vendor); err != nil {
		return nil, fmt.Errorf("saving vendor: %w", err)
	}

	// Create job record with optimistic locking
	span.SetAttribute("step", "upsert_job")
	existingJob, getErr := a.db.GetJob(ctx, job.JobID, job.TenantID)

	dbJob := &domain.Job{
		ID:           job.JobID,
		TenantID:     job.TenantID,
		WorkflowType: domain.WorkflowVendorOnboarding,
		Status:       domain.JobStatusCompleted,
		UpdatedAt:    time.Now(),
		Output:       result,
	}
	if getErr == nil {
		dbJob.Version = existingJob.Version
	}

	if err := a.db.UpsertJob(ctx, dbJob); err != nil {
		if errors.Is(err, postgres.ErrVersionConflict) {
			// Retry once
			existingJob, getErr := a.db.GetJob(ctx, job.JobID, job.TenantID)
			if getErr == nil {
				dbJob.Version = existingJob.Version
			}
			if retryErr := a.db.UpsertJob(ctx, dbJob); retryErr != nil {
				return nil, fmt.Errorf("updating job after conflict: %w", retryErr)
			}
		} else {
			return nil, fmt.Errorf("updating job: %w", err)
		}
	}

	// Create HITL if needed
	if needsHITL {
		hitlReq := &domain.HITLRequest{
			ID:       fmt.Sprintf("hitl-%s", job.JobID),
			TenantID: job.TenantID,
			JobID:    job.JobID,
			Reason:   fmt.Sprintf("Vendor %s requires approval: risk_score=%d", data.Name, riskScore),
			Status:   domain.HITLStatusPending,
			SentAt:   time.Now(),
		}

		if err := a.db.UpsertHITLRequest(ctx, hitlReq); err != nil {
			return nil, fmt.Errorf("creating HITL request: %w", err)
		}
	}

	return result, nil
}

// ValidateVendor validates vendor identifiers
func (a *VendorAgent) ValidateVendor(ctx context.Context, gst, pan, ifsc string) (*domain.ValidationResult, error) {
	result := a.validator.ValidateVendor(gst, pan, ifsc)
	return result, nil
}

// CheckDuplicate checks for duplicate vendors
func (a *VendorAgent) CheckDuplicate(ctx context.Context, tenantID, name string) (bool, error) {
	vendors, err := a.db.ListVendors(ctx, tenantID)
	if err != nil {
		return false, err
	}

	for _, v := range vendors {
		if v.Name == name {
			return true, nil
		}
	}

	return false, nil
}

// VendorAgentTools returns the list of tools available to this agent
func (a *VendorAgent) VendorAgentTools() []string {
	return []string{
		"process_vendor",
		"validate_vendor",
		"check_duplicate",
	}
}
