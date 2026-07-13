package domain

import "testing"

func TestPlanLimits_Starter(t *testing.T) {
	limits, ok := PlanLimits["starter"]
	if !ok {
		t.Fatal("starter plan not found in PlanLimits")
	}

	tests := []struct {
		metric Metric
		want   int64
	}{
		{MetricDocumentsUploaded, 100},
		{MetricOCRPages, 100},
		{MetricLLMCalls, 0},
		{MetricComplianceChecks, 50},
	}

	for _, tt := range tests {
		t.Run(string(tt.metric), func(t *testing.T) {
			got := limits[tt.metric]
			if got != tt.want {
				t.Errorf("starter %s = %d, want %d", tt.metric, got, tt.want)
			}
		})
	}
}

func TestPlanLimits_Pro(t *testing.T) {
	limits, ok := PlanLimits["pro"]
	if !ok {
		t.Fatal("pro plan not found in PlanLimits")
	}

	tests := []struct {
		metric Metric
		want   int64
	}{
		{MetricDocumentsUploaded, 1000},
		{MetricOCRPages, 1000},
		{MetricLLMCalls, 100},
		{MetricComplianceChecks, 500},
	}

	for _, tt := range tests {
		t.Run(string(tt.metric), func(t *testing.T) {
			got := limits[tt.metric]
			if got != tt.want {
				t.Errorf("pro %s = %d, want %d", tt.metric, got, tt.want)
			}
		})
	}
}

func TestPlanLimits_Business(t *testing.T) {
	limits, ok := PlanLimits["business"]
	if !ok {
		t.Fatal("business plan not found in PlanLimits")
	}

	tests := []struct {
		metric Metric
		want   int64
	}{
		{MetricDocumentsUploaded, 10000},
		{MetricOCRPages, 10000},
		{MetricLLMCalls, -1},
		{MetricComplianceChecks, 5000},
	}

	for _, tt := range tests {
		t.Run(string(tt.metric), func(t *testing.T) {
			got := limits[tt.metric]
			if got != tt.want {
				t.Errorf("business %s = %d, want %d", tt.metric, got, tt.want)
			}
		})
	}
}

func TestGetLimit(t *testing.T) {
	tests := []struct {
		name   string
		plan   string
		metric Metric
		want   int64
	}{
		{"starter documents", "starter", MetricDocumentsUploaded, 100},
		{"pro documents", "pro", MetricDocumentsUploaded, 1000},
		{"business documents", "business", MetricDocumentsUploaded, 10000},
		{"starter llm", "starter", MetricLLMCalls, 0},
		{"pro llm", "pro", MetricLLMCalls, 100},
		{"business llm (unlimited)", "business", MetricLLMCalls, -1},
		{"unknown plan returns 0", "unknown", MetricDocumentsUploaded, 0},
		{"unknown metric returns 0", "starter", "unknown_metric", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetLimit(tt.plan, tt.metric)
			if got != tt.want {
				t.Errorf("GetLimit(%q, %q) = %d, want %d", tt.plan, tt.metric, got, tt.want)
			}
		})
	}
}

func TestIsUnlimited(t *testing.T) {
	tests := []struct {
		name  string
		limit int64
		want  bool
	}{
		{"negative one is unlimited", -1, true},
		{"negative values are unlimited", -100, true},
		{"zero is not unlimited", 0, false},
		{"positive is not unlimited", 100, false},
		{"large positive is not unlimited", 999999, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsUnlimited(tt.limit)
			if got != tt.want {
				t.Errorf("IsUnlimited(%d) = %v, want %v", tt.limit, got, tt.want)
			}
		})
	}
}
