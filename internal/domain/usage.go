package domain

import "time"

// Metric represents a usage metric that is tracked per tenant.
type Metric string

const (
	MetricDocumentsUploaded Metric = "documents_uploaded"
	MetricOCRPages          Metric = "ocr_pages"
	MetricLLMCalls          Metric = "llm_calls"
	MetricComplianceChecks  Metric = "compliance_checks"
)

// UsageRecord represents a single usage metric record for a tenant within a billing period.
type UsageRecord struct {
	TenantID    string    `json:"tenant_id"`
	Metric      Metric    `json:"metric"`
	Count       int64     `json:"count"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// PlanLimits defines the maximum allowed usage per plan per metric.
// A value of -1 means unlimited.
var PlanLimits = map[string]map[Metric]int64{
	"starter": {
		MetricDocumentsUploaded: 100,
		MetricOCRPages:          100,
		MetricLLMCalls:          0,
		MetricComplianceChecks:  50,
	},
	"pro": {
		MetricDocumentsUploaded: 1000,
		MetricOCRPages:          1000,
		MetricLLMCalls:          100,
		MetricComplianceChecks:  500,
	},
	"business": {
		MetricDocumentsUploaded: 10000,
		MetricOCRPages:          10000,
		MetricLLMCalls:          -1,
		MetricComplianceChecks:  5000,
	},
}

// GetLimit returns the limit for a given plan and metric.
// Returns -1 for unlimited.
func GetLimit(plan string, metric Metric) int64 {
	planLimits, ok := PlanLimits[plan]
	if !ok {
		return 0
	}
	limit, ok := planLimits[metric]
	if !ok {
		return 0
	}
	return limit
}

// IsUnlimited returns true if the limit value indicates unlimited usage.
func IsUnlimited(limit int64) bool {
	return limit < 0
}
