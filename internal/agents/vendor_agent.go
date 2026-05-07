package agents

import (
	"context"
	"fmt"
	"time"

	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
)

// VendorAgent handles vendor onboarding workflows
type VendorAgent struct {
	db       providers.DBProvider
	validator *domain.IndiaValidator
	scorer    *RiskScorerWrapper
	llm      providers.LLMProvider
}

// RiskScorerWrapper wraps risk scoring for agents
type RiskScorerWrapper struct{}

func (r *RiskScorerWrapper) ComputeScore(gst, pan, name, address string) int {
	score := 0

	// Simple scoring
	if gst != "" {
		score += 20
	}
	if pan != "" {
		score += 20
	}
	if address != "" {
		score += 10
	}
	if name != "" {
		score += 10
	}

	// Add some risk for missing items
	if gst == "" {
		score -= 10
	}
	if pan == "" {
		score -= 10
	}

	// Default to medium
	if score < 30 {
		score = 30
	} else if score > 90 {
		score = 90
	}

	return score
}

// NewVendorAgent creates a new vendor agent
func NewVendorAgent(
	db providers.DBProvider,
	validator *domain.IndiaValidator,
	llm providers.LLMProvider,
) *VendorAgent {
	return &VendorAgent{
		db:       db,
		validator: validator,
		scorer:    &RiskScorerWrapper{},
		llm:     llm,
	}
}

// VendorJob represents a vendor onboarding job
type VendorJob struct {
	TenantID    string     `json:"tenant_id"`
	JobID     string     `json:"job_id"`
	VendorData *VendorData `json:"vendor_data"`
}

type VendorData struct {
	Name         string   `json:"name"`
	GSTNumber   string   `json:"gst_number,omitempty"`
	PANNumber  string   `json:"pan_number,omitempty"`
	IFSCCode   string   `json:"ifsc_code,omitempty"`
	BankAccount string   `json:"bank_account,omitempty"`
	Address    string   `json:"address,omitempty"`
	Documents []string  `json:"documents,omitempty"`
}

// ProcessVendor handles the complete vendor onboarding workflow
// TOOL: process_vendor
func (a *VendorAgent) ProcessVendor(ctx context.Context, job *VendorJob) (map[string]any, error) {
	data := job.VendorData

	// Step 1: Validate identifiers
	validations := a.validator.ValidateVendor(data.GSTNumber, data.PANNumber, data.IFSCCode)

	// Step 2: Compute risk score
	riskScore := a.scorer.ComputeScore(data.GSTNumber, data.PANNumber, data.Name, data.Address)

	result := map[string]any{
		"name":         data.Name,
		"validations": validations,
		"risk_score":  riskScore,
		"trust_tier": domain.TrustTierStandard,
		"needs_hitl":  riskScore >= 60 || (validations != nil && len(validations.Errors) > 0),
	}

	// Step 3: LLM analysis for non-standard vendors (optional)
	if a.llm != nil && len(data.Documents) > 0 {
		prompt := fmt.Sprintf("Analyze vendor documents for %s: %v", data.Name, data.Documents)
		reasoning, err := a.llm.Reason(ctx, prompt)
		if err == nil {
			result["llm_analysis"] = reasoning
		}
	}

	// Determine trust battery
	trustTier := domain.TrustTierProbation
	if riskScore < 30 && (validations == nil || len(validations.Errors) == 0) {
		trustTier = domain.TrustTierCore
	} else if riskScore < 60 {
		trustTier = domain.TrustTierStandard
	}

	result["trust_tier"] = trustTier

	// Save vendor
	vendor := &domain.Vendor{
		ID:           job.JobID,
		TenantID:     job.TenantID,
		Name:         data.Name,
		GSTNumber:    data.GSTNumber,
		PANNumber:   data.PANNumber,
		IFSCCode:    data.IFSCCode,
		BankAccount: data.BankAccount,
		RiskScore:   riskScore,
		RiskTier:    domain.RiskTierHigh,
		TrustBattery: domain.TrustBattery{},
		Approved:    riskScore < 60 && (validations == nil || len(validations.Errors) == 0),
		CreatedAt:   time.Now(),
		UpdatedAt:  time.Now(),
	}

	if vendor.RiskScore < 30 {
		vendor.RiskTier = domain.RiskTierLow
	} else if vendor.RiskScore < 60 {
		vendor.RiskTier = domain.RiskTierMedium
	}

	if err := a.db.UpsertVendor(ctx, vendor); err != nil {
		return nil, fmt.Errorf("saving vendor: %w", err)
	}

	// Create job record
	dbJob := &domain.Job{
		ID:           job.JobID,
		TenantID:     job.TenantID,
		WorkflowType: domain.WorkflowVendorOnboarding,
		Status:      domain.JobStatusCompleted,
		UpdatedAt:   time.Now(),
		Output:     result,
	}

	if err := a.db.UpsertJob(ctx, dbJob); err != nil {
		return nil, fmt.Errorf("updating job: %w", err)
	}

	// Create HITL if needed
	if result["needs_hitl"].(bool) {
		hitlReq := &domain.HITLRequest{
			ID:        fmt.Sprintf("hitl-%s", job.JobID),
			TenantID: job.TenantID,
			JobID:   job.JobID,
			Type:    "VENDOR_APPROVAL",
			Message: fmt.Sprintf("Vendor %s requires approval: risk_score=%d", data.Name, riskScore),
			Status:  "PENDING",
			CreatedAt: time.Now(),
		}

		if err := a.db.UpsertHITLRequest(ctx, hitlReq); err != nil {
			return nil, fmt.Errorf("creating HITL request: %w", err)
		}
	}

	return result, nil
}

// ValidateVendor validates vendor identifiers
// TOOL: validate_vendor
func (a *VendorAgent) ValidateVendor(ctx context.Context, gst, pan, ifsc string) (*domain.ValidationResult, error) {
	result := a.validator.ValidateVendor(gst, pan, ifsc)
	return result, nil
}

// CheckDuplicate checks for duplicate vendors
// TOOL: check_duplicate
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

// QueueVendor adds a vendor to the processing queue
// TOOL: queue_vendor
func (a *VendorAgent) QueueVendor(ctx context.Context, job *VendorJob) (string, error) {
	return a.db.QueueEnqueue(ctx, "vendor-queue", job)
}

// VendorAgentTools returns the list of tools available to this agent
func (a *VendorAgent) VendorAgentTools() []string {
	return []string{
		"process_vendor",
		"validate_vendor",
		"check_duplicate",
		"queue_vendor",
	}
}