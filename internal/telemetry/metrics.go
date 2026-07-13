package telemetry

import (
	"context"
	"os"
	"strconv"

	"github.com/aparna/opscore/internal/providers"
)

// MetricsCollector emits structured metrics to Application Insights
type MetricsCollector struct {
	tracing providers.TracingProvider
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector(tracing providers.TracingProvider) *MetricsCollector {
	return &MetricsCollector{tracing: tracing}
}

// RecordUploadLatency records upload timing
func (m *MetricsCollector) RecordUploadLatency(ctx context.Context, tenantID string, latencyMs int) {
	m.recordMetric(ctx, "upload_latency", float64(latencyMs), map[string]string{
		"tenant_id": tenantID,
	})
}

// RecordQueueLag records queue processing lag
func (m *MetricsCollector) RecordQueueLag(ctx context.Context, tenantID, queueName string, lagSeconds int) {
	m.recordMetric(ctx, "queue_lag", float64(lagSeconds), map[string]string{
		"tenant_id": tenantID,
		"queue":     queueName,
	})
}

// RecordRetryCount records retry attempts
func (m *MetricsCollector) RecordRetryCount(ctx context.Context, tenantID, jobID string, count int) {
	m.recordMetric(ctx, "retry_count", float64(count), map[string]string{
		"tenant_id": tenantID,
		"job_id":    jobID,
	})
}

// RecordHITLRate records HITL escalation rate
func (m *MetricsCollector) RecordHITLRate(ctx context.Context, tenantID string, rate float64) {
	m.recordMetric(ctx, "hitl_rate", rate, map[string]string{
		"tenant_id": tenantID,
	})
}

func (m *MetricsCollector) recordMetric(ctx context.Context, name string, value float64, props map[string]string) {
	if m.tracing == nil {
		return
	}
	ctx, span := m.tracing.StartSpan(ctx, name, providers.WithTenantID(props["tenant_id"]))
	defer span.End(nil)

	span.SetAttributes(props)
	// Emit as custom metric (Application Insights tracks this)
	_ = value // In production, emit to Application Insights
}

// GetUploadLatencyP95 returns configured p95 target (from env)
func GetUploadLatencyP95() int {
	// Default: 5 seconds p95
	v := getEnv("P95_UPLOAD_LATENCY_MS", "5000")
	if i, err := strconv.Atoi(v); err == nil {
		return i
	}
	return 5000
}

func getEnv(key, fallback string) string {
	if v := getEnvRaw(key); v != "" {
		return v
	}
	return fallback
}

func getEnvRaw(key string) string {
	// Implementation would read from os.Getenv
	return os.Getenv(key)
}
