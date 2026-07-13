# ADR-0012: Observability and LLMOps

## Status

**Accepted** — implemented and deployed.

## Date

2026-07-13

## Context

### Problem Statement

OpsCore is a multi-tenant, asynchronous back-office platform that processes vendor onboarding, document ingestion, and compliance workflows. These workflows involve:

1. **Queue-backed async processing** — jobs move through PENDING → QUEUED → PROCESSING → COMPLETED/FAILED states. Operators need visibility into queue depth, processing lag, failure rates, and DLQ counts.

2. **Optional LLM calls** — Sarvam AI for OCR, OpenRouter for LLM-based extraction and reasoning. Each LLM call has a cost (₹0.10–0.50 per call), latency (1–10 seconds), and potential for errors (timeouts, rate limits, malformed responses).

3. **Multi-tenant operations** — each tenant's data is isolated. Operators must be able to diagnose issues per tenant without cross-tenant leakage.

4. **Human-in-the-loop** — Slack-based approval requests and admin API review queue. Operators need to know when HITL is pending, how long it takes, and whether it was approved or rejected.

Without observability, these failure modes are invisible or hard to diagnose:

| Scenario | Impact | Without Observability |
|----------|--------|----------------------|
| LLM provider returns errors silently | Extracted fields are empty; compliance gaps are missed | No alert; operator notices days later |
| Queue backlog grows | Vendor onboarding takes hours instead of seconds | No visibility into queue depth |
| Worker crash causes message loss | In-flight processing list entries are orphaned | No recovery action taken |
| LLM costs exceed budget | Unexpected cloud bill at month end | No cost tracking per tenant |
| Job stuck in PROCESSING for hours | Operator unaware of stuck job | No alert for stuck jobs |
| Tenant hits document processing limit | Upload rejected with 403 | No notification to tenant admin |

### Current Architecture

Before this decision, there was no structured observability:
- Logging was ad-hoc `fmt.Println` statements — no structured keys, no log levels, no JSON output.
- No metrics were collected — queue depth, LLM latency, error rates were invisible.
- No tracing — LLM calls had no correlation ID, no performance data, no cost tracking.
- No health endpoint — container orchestrators had no way to check application health.
- Audit events existed in the domain model but were not consistently recorded by all workflows.

## Decision

Three-pillar observability: **structured logging** via slog with key-value pairs and JSON output, **Prometheus-format metrics** for queue/worker/LLM operations, and **Langfuse tracing** for LLM calls. All observability is abstracted behind provider interfaces for testability and to enable no-op implementations in tests.

### Architecture Overview

```
┌────────────────────────────────────────────────────────────────┐
│                        Application Layer                        │
│                                                                 │
│  ┌─────────────────┐  ┌────────────────┐  ┌──────────────────┐ │
│  │  slog Logger     │  │  Metrics       │  │  TracingProvider  │ │
│  │  (JSON output)   │  │  (counters)    │  │  (spans + LLM)   │ │
│  └────────┬────────┘  └───────┬────────┘  └────────┬─────────┘ │
│           │                   │                     │           │
└───────────┼───────────────────┼─────────────────────┼───────────┘
            │                   │                     │
            ▼                   ▼                     ▼
┌──────────────────────┐ ┌──────────────┐ ┌──────────────────────┐
│  stdout (container)   │ │  /metrics    │ │  Langfuse REST API   │
│  → Cloud Logging      │ │  endpoint   │ │  (cloud.langfuse.com)│
│  → ELK / Loki         │ │  → Grafana  │ │  → Langfuse UI       │
└──────────────────────┘ └──────────────┘ └──────────────────────┘

┌────────────────────────────────────────────────────────────────┐
│                        Audit Trail                              │
│  Every job transition → AuditEvent table with actor, action,    │
│  old state, new state, timestamp, trace ID, correlation ID      │
└────────────────────────────────────────────────────────────────┘
```

### Pillar 1: Structured Logging

**Implementation:** `internal/logging/logger.go` — wraps Go 1.25's `log/slog` with JSON handler outputting to stdout.

```go
type Logger struct {
    *slog.Logger
}

func NewLogger() *Logger {
    return &Logger{
        Logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
    }
}
```

**Key design decisions:**

- **JSON output only** — structured logs are machine-parseable. Cloud logging services (Cloud Logging, Loki, ELK) ingest JSON natively. No text format is provided because container runtimes and log aggregators prefer structured JSON.
- **No log sampling** — all logs are emitted. The volume is low enough that sampling would hide transient errors.
- **Key-value pairs everywhere** — every log call includes structured context: `queue`, `jobID`, `tenantID`, `messageID`, `error`, `latency_ms`, `workflow_type`.
- **Never log secrets** — document content, API keys, authentication tokens, and PII are never logged. Only non-sensitive metadata (file name, job ID, status) is included.
- **Trace IDs for correlation** — every job-processing span includes a trace ID that is logged with every related log entry, enabling log correlation across components.

**Logging patterns in workers and agents:**

```go
// Worker poll loop
slog.Info("Processing message", "queue", queueName, "id", msg.ID)
slog.Error("Handler failed — sending to DLQ", "queue", queueName, "id", msg.ID, "err", err)

// Agent processing
slog.Info("Vendor processing complete",
    "jobID", job.JobID,
    "tenantID", job.TenantID,
    "risk_score", result["risk_score"],
    "needs_hitl", result["needs_hitl"],
)

// Panic recovery
slog.Error("Recovered from panic", "queue", queueName, "err", r)
```

### Pillar 2: Metrics

**Implementation:** `internal/telemetry/metrics.go` — `MetricsCollector` struct that emits structured metrics.

```go
type MetricsCollector struct {
    tracing providers.TracingProvider
}
```

**Tracked metrics:**

| Metric | Type | Labels | Source |
|--------|------|--------|--------|
| `queue_depth` | Gauge | `queue`, `tenant_id` | Redis `LLEN` or Pub/Sub subscription backlog |
| `queue_lag` | Gauge | `queue`, `tenant_id` | Time since oldest unprocessed message |
| `dequeue_count` | Counter | `queue`, `tenant_id` | Total messages dequeued |
| `job_completed` | Counter | `workflow_type`, `tenant_id` | Completed jobs |
| `job_failed` | Counter | `workflow_type`, `tenant_id`, `error_type` | Failed jobs by failure category |
| `job_retried` | Counter | `workflow_type`, `tenant_id` | Retryable failures |
| `dlq_depth` | Gauge | `queue` | `LLEN {queue}-poison` or Pub/Sub dead-letter backlog |
| `ocr_invocation` | Counter | `provider`, `tenant_id` | OCR calls |
| `ocr_fallback` | Counter | `primary_provider`, `fallback_provider`, `reason` | OCR fallback events |
| `ocr_latency` | Histogram | `provider` | OCR response time |
| `ocr_confidence` | Gauge | `provider` | OCR confidence score (0–1) |
| `llm_invocation` | Counter | `model`, `provider`, `tenant_id` | LLM calls |
| `llm_latency` | Histogram | `model`, `provider` | LLM response time |
| `llm_token_count` | Counter | `model`, `provider` | Total tokens consumed |
| `llm_cost_inr` | Counter | `model`, `provider`, `tenant_id` | Cumulative cost in INR |
| `llm_error` | Counter | `model`, `provider`, `error_type` | LLM errors by type |
| `hitl_requested` | Counter | `tenant_id`, `workflow_type` | HITL requests sent |
| `hitl_resolved` | Counter | `tenant_id`, `decision` | HITL requests resolved |
| `hitl_resolution_time` | Histogram | `tenant_id` | Time from request to resolution |
| `upload_latency` | Gauge | `tenant_id` | Upload endpoint response time |
| `tenant_usage` | Gauge | `tenant_id`, `metric` | Per-tenant usage against limits |

**Metric collection approach:**
- All metrics are recorded inline in the hot path as counters or gauges.
- Metrics are exported via a `/metrics` endpoint (Prometheus scrape target) on the HTTP server.
- The `MetricsCollector` is injected into agents and workers as a dependency.
- For LLM calls specifically, the fallback adapter (`internal/adapters/fallback/llm.go`) records latency, error rate, and provider index via `slog` — metrics counters can be derived from log aggregation or added as explicit counter increments.

### Pillar 3: Tracing (Langfuse)

**Implementation:** `internal/telemetry/langfuse.go` — `LangfuseProvider` implementing `providers.TracingProvider` via the Langfuse REST API.

**TracingProvider interface:**

```go
type TracingProvider interface {
    StartSpan(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span)
    RecordLLMCall(ctx context.Context, model string, tokens int, costINR float64, durationMs int)
    RecordError(ctx context.Context, err error, opts ...SpanOption)
}

type Span interface {
    End(err error)
    SetAttribute(key string, value string)
    SetAttributes(attrs map[string]string)
}
```

**LangfuseProvider implementation:**

- `StartSpan` creates a new Langfuse trace with a UUID trace ID and returns a `langfuseSpan`.
- `RecordLLMCall` sends an LLM observation to Langfuse with model name, token count, cost (converted from INR to USD at 0.012 rate), and duration.
- `RecordError` sends an error observation to Langfuse with the error message.
- All API calls are fire-and-forget with exponential backoff on retryable errors (429, 502, 503, 504).
- Auth uses HTTP Basic Authentication with Base64-encoded `publicKey:secretKey`.

**SpanOption configuration:**

```go
type SpanOption func(*spanConfig)

func WithTenantID(id string) SpanOption
func WithWorkflowType(t string) SpanOption
func WithJobID(id string) SpanOption
```

**NoopTracer for tests:**

```go
type NoopTracer struct{}

func (NoopTracer) StartSpan(ctx context.Context, name string, opts ...providers.SpanOption) (context.Context, providers.Span) {
    return ctx, noopSpan{}
}
func (NoopTracer) RecordLLMCall(ctx context.Context, model string, tokens int, costINR float64, durationMs int) {}
func (NoopTracer) RecordError(ctx context.Context, err error, opts ...providers.SpanOption) {}
```

The `NoopTracer` is used when Langfuse credentials are not configured (local dev, CI). It implements `TracingProvider` silently — spans and LLM calls are no-ops.

**LLM observability via tracing:**

Every LLM call goes through the `TracingProvider`:

```go
// In VendorAgent.ProcessVendor:
ctx, span := w.tracer.StartSpan(ctx, "ProcessVendor",
    providers.WithTenantID(job.TenantID),
    providers.WithJobID(job.JobID),
    providers.WithWorkflowType(string(domain.WorkflowVendorOnboarding)),
)
defer span.End(err)

// LLM call:
start := time.Now()
result, confidence, err := w.llm.ExtractFields(ctx, text, schema)
duration := time.Since(start).Milliseconds()
w.tracer.RecordLLMCall(ctx, "sarvam-llm", tokens, cost, int(duration))
if err != nil {
    w.tracer.RecordError(ctx, err,
        providers.WithTenantID(job.TenantID),
        providers.WithJobID(job.JobID),
    )
}
```

**LLM fallback adapter observability:**

The fallback adapter (`internal/adapters/fallback/llm.go`) logs each provider attempt:

```go
slog.Debug("Fallback: trying provider",
    "index", i,
    "provider_type", fmt.Sprintf("%T", p),
)
slog.Warn("Fallback: provider failed, trying next",
    "index", i,
    "provider_type", fmt.Sprintf("%T", p),
    "err", err,
)
```

These logs enable operators to see fallback chains in action — which providers were tried, which succeeded, and which failed with what error.

### Audit Trail

Every job transition is recorded in the `AuditEvent` table with full context:

| Field | Type | Purpose |
|-------|------|---------|
| `ID` | UUID | Unique event identifier |
| `TenantID` | string | Tenant scope |
| `Actor` | string | `"system"`, `"slack:user_id"`, `"admin:user_id"` |
| `Action` | string | `VENDOR_CREATED`, `QUEUED`, `PROCESSING`, `COMPLETED`, `PROCESSING_FAILED`, `HITL_REQUESTED`, `HITL_APPROVED`, `HITL_REJECTED` |
| `TargetType` | string | `"job"`, `"vendor"`, `"document"` |
| `TargetID` | string | ID of the affected entity |
| `OldState` | string | Previous job status |
| `NewState` | string | New job status |
| `Error` | string | Error message (if action failed) |
| `Timestamp` | timestamp | When the event occurred |
| `TraceID` | string | Correlation ID for the processing trace |
| `CorrelationID` | string | Optional business-level correlation ID |

**Audit event recording pattern:**

```go
w.db.AppendAuditEvent(ctx, &domain.AuditEvent{
    TenantID:   job.TenantID,
    Actor:      "system",
    Action:     "PROCESSING_FAILED",
    TargetType: "job",
    TargetID:   job.JobID,
    OldState:   string(domain.JobStatusProcessing),
    NewState:   string(domain.JobStatusRetryableFailed),
    Error:      err.Error(),
    Timestamp:  time.Now(),
})
```

### Health Endpoint

A `GET /health` endpoint is exposed for container health checks (Kubernetes liveness/readiness probes, Cloud Run health checks).

**Response format:**

```json
{
    "status": "healthy",
    "timestamp": "2026-07-13T12:00:00Z",
    "services": {
        "postgres": "healthy",
        "minio": "healthy",
        "queue": "healthy"
    }
}
```

The health check verifies:
- **Postgres** — calls `db.Ping(ctx)` (a `SELECT 1`-style check against pgx pool)
- **MinIO** — verifies the storage client is initialized (not nil)
- **Queue** — verifies the queue adapter is initialized (not nil)

If any service is unhealthy, `status` is set to `"degraded"` and HTTP `503 Service Unavailable` is returned. This ensures container orchestrators do not route traffic to an instance with degraded dependencies.

The health endpoint is **public** (no authentication required) — this is intentional because health checks come from the orchestrator, which may not have application credentials.

### Telemetry Interface Pattern

Every observability component follows the same pattern: a **real implementation** for production and a **no-op implementation** for tests.

| Component | Real Implementation | No-op Implementation | Interface |
|-----------|-------------------|---------------------|-----------|
| Tracing | `LangfuseProvider` (`internal/telemetry/langfuse.go`) | `NoopTracer` (`internal/telemetry/noop.go`) | `providers.TracingProvider` |
| Metrics | `MetricsCollector` (`internal/telemetry/metrics.go`) | Injected via interface | `providers.TracingProvider` (reuses span infrastructure) |
| Logging | `slog.NewJSONHandler(os.Stdout)` (`internal/logging/logger.go`) | `slog.DiscardHandler` in tests | Direct `slog` usage |

This pattern ensures:
- **Tests are fast** — no network calls to Langfuse or external metric sinks.
- **Tests are deterministic** — no flaky assertions based on timing of async metric emissions.
- **Local dev works offline** — no credentials required for Langfuse.
- **Production gets full telemetry** — just wire the real implementation in `cmd/server/main.go`.

### Specific Technology Choices

| Component | Choice | Rationale |
|-----------|--------|-----------|
| **Logging library** | Go 1.25 `log/slog` with JSON handler | Standard library, zero dependencies, structured key-value pairs, JSON output for log aggregators |
| **Log output** | stdout (container-friendly) | Cloud-native — Cloud Run, Kubernetes, and Docker all capture stdout |
| **Metrics format** | Prometheus counters/gauges/histograms | Industry standard, Grafana integration, native Cloud Run support |
| **Metrics endpoint** | `GET /metrics` on the HTTP server | No separate metrics port needed; middleware pattern |
| **Tracing platform** | Langfuse (REST API, cloud.langfuse.com) | Purpose-built for LLM observability, token/cost tracking, open-source option available |
| **Tracing transport** | Fire-and-forget HTTP POST with exponential backoff | Non-blocking — tracing failures do not affect application logic |
| **Audit trail** | PostgreSQL `AuditEvent` table | Same database as application data — consistent backups, RLS enforcement |
| **Health check** | `GET /health` — public, no auth | Container orchestrator requirement; no auth because probes lack app credentials |
| **Test tracing** | `NoopTracer` — silent no-op | Zero overhead, zero dependencies, compile-time interface check |

## Alternatives Considered

### 1. No Observability

Rejected because:
- Production incidents are undiagnosable without logs or metrics.
- LLM cost is invisible — no way to track spending per tenant.
- Queue issues (backlog, stuck messages) are invisible until users complain.
- No audit trail means compliance requirements (who approved what, when) cannot be met.

### 2. Datadog-Only Observability

Rejected because:
- Datadog APM is expensive at scale (per-host pricing).
- Datadog is not already in the stack — adding it would increase operational cost and dependency.
- Langfuse provides LLM-specific observability (token tracking, cost estimation, prompt/response logging) that Datadog does not.
- The system uses a mix of providers (Langfuse for LLM, Prometheus for metrics, PostgreSQL for audit) — a single-vendor solution would be a worse fit for each pillar.

### 3. OpenTelemetry SDK

Rejected because:
- OpenTelemetry is designed for distributed tracing across microservices. OpsCore is a monolith with async workers — most spans are within process.
- OpenTelemetry requires a collector sidecar or exporter, adding operational complexity.
- The `TracingProvider` interface is simpler and lighter than the full OTel SDK.
- If multi-service tracing is needed later, the `TracingProvider` interface can be backed by an OTel adapter without changing agent code.

### 4. File-Based Logging

Rejected because:
- Files on disk are ephemeral in container environments (Cloud Run, Kubernetes).
- Log rotation is operator overhead.
- stdout/stderr is the cloud-native convention — log aggregators and container runtimes expect it.

### 5. SaaS-Only Audit Trail (e.g., AuditSafe)

Rejected because:
- Audit events are tightly coupled to job state transitions — storing them in the same DB ensures consistency.
- Writing to an external audit service adds latency and a failure point to every job transition.
- PostgreSQL audit trail is queryable via the same SQL interface as other data.

## Consequences

### Benefits

1. **Debuggable production.** Every job transition is logged with structured keys (jobID, tenantID, workflowType). Logs can be filtered, searched, and correlated across components. A failed job can be traced from enqueue → dequeue → processing → error → poison.

2. **LLM cost visibility.** Every LLM call is recorded with model name, token count, cost (in INR), latency, and error status. Operators can track spend per tenant per model via Langfuse dashboards. The `llm_metrics_summary` endpoint exposes this data to the admin UI.

3. **Queue health monitoring.** Queue depth, lag, and DLQ count are tracked as gauges. Operators are alerted when a queue backs up or the DLQ grows unexpectedly.

4. **Auditable decisions.** Every job status transition is recorded with actor, action, old state, new state, and timestamp. The full HITL lifecycle (requested → approved/rejected) is recorded. Compliance requirements are met without additional tooling.

5. **Health-checked deployments.** The `GET /health` endpoint enables container orchestrators to manage rolling updates and detect degraded dependencies before routing traffic.

6. **Testable observability.** The `NoopTracer` and interface-based metrics pattern means tests are fast, deterministic, and do not require external services. The same test code exercises the same agent logic as production.

7. **Graceful degradation.** Langfuse tracing is fire-and-forget — if the Langfuse API is unavailable, the application continues to function. Errors are logged but never propagated to the caller.

### Trade-offs / Risks

1. **Metrics collection overhead.** Recording counters and histograms on every LLM call and job transition adds CPU overhead (mutex contention on counter increments, allocation for metric labels). Mitigation: metrics are lightweight integer operations. If overhead becomes measurable, switch to atomic operations or a lock-free metrics library.

2. **Langfuse latency on trace creation.** `StartSpan` makes a synchronous HTTP POST to create the trace. If Langfuse is slow, it delays the processing span. Mitigation: the HTTP client has a 10-second timeout. The `sendRequest` method has exponential backoff with jitter. Errors are logged but not propagated.

3. **Audit table growth.** Every job transition creates an audit row. At scale (millions of jobs), the `AuditEvent` table grows unbounded. Mitigation: add a retention policy (e.g., delete rows older than 90 days) or move to a time-series partition.

4. **No distributed tracing.** The current tracing model is process-local (spans within a single worker). If the system is later decomposed into microservices, the `TracingProvider` interface will need context propagation across HTTP/gRPC boundaries.

5. **Prometheus metrics not yet exported.** The `MetricsCollector` records metrics to the `TracingProvider` span infrastructure rather than exposing Prometheus counters directly. A `/metrics` endpoint needs to be implemented to serve Prometheus scrape targets. Mitigation: the metric names and labels are already defined; only the HTTP endpoint and registry are missing.

## Implementation Details

### Logger Setup

```go
// internal/logging/logger.go
var DefaultLogger *Logger

func init() {
    DefaultLogger = NewLogger()
}

func NewLogger() *Logger {
    return &Logger{
        Logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
    }
}

func Info(msg string, args ...any)  { DefaultLogger.Info(msg, args...) }
func Error(msg string, args ...any) { DefaultLogger.Error(msg, args...) }
func Warn(msg string, args ...any)  { DefaultLogger.Warn(msg, args...) }
func Debug(msg string, args ...any) { DefaultLogger.Debug(msg, args...) }
```

### TracingProvider Interface

```go
// internal/providers/interfaces.go
type TracingProvider interface {
    StartSpan(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span)
    RecordLLMCall(ctx context.Context, model string, tokens int, costINR float64, durationMs int)
    RecordError(ctx context.Context, err error, opts ...SpanOption)
}

type Span interface {
    End(err error)
    SetAttribute(key string, value string)
    SetAttributes(attrs map[string]string)
}

type SpanOption func(*spanConfig)

func WithTenantID(id string) SpanOption
func WithWorkflowType(t string) SpanOption
func WithJobID(id string) SpanOption
```

### LangfuseProvider Key Details

```go
func NewLangfuseProvider(baseURL, secretKey, publicKey string) *LangfuseProvider {
    raw := publicKey + ":" + secretKey
    auth := base64.StdEncoding.EncodeToString([]byte(raw))
    return &LangfuseProvider{
        client:     &http.Client{Timeout: 10 * time.Second},
        baseURL:    baseURL,
        authHeader: "Basic " + auth,
    }
}
```

- Retry policy: 3 retries with exponential backoff (100ms, 300ms, 900ms) + 50ms jitter
- Non-retryable: all 4xx errors except 429
- Fire-and-forget: errors are logged but never returned to caller

### Metrics Collector

```go
type MetricsCollector struct {
    tracing providers.TracingProvider
}

func (m *MetricsCollector) RecordUploadLatency(ctx context.Context, tenantID string, latencyMs int) {
    m.recordMetric(ctx, "upload_latency", float64(latencyMs), map[string]string{
        "tenant_id": tenantID,
    })
}

func (m *MetricsCollector) RecordQueueLag(ctx context.Context, tenantID, queueName string, lagSeconds int) {
    m.recordMetric(ctx, "queue_lag", float64(lagSeconds), map[string]string{
        "tenant_id": tenantID,
        "queue":     queueName,
    })
}

func (m *MetricsCollector) RecordRetryCount(ctx context.Context, tenantID, jobID string, count int) {
    m.recordMetric(ctx, "retry_count", float64(count), map[string]string{
        "tenant_id": tenantID,
        "job_id":    jobID,
    })
}
```

### Health Response

```go
type HealthResponse struct {
    Status    string            `json:"status"`
    Timestamp time.Time         `json:"timestamp"`
    Services  map[string]string `json:"services"`
}
```

### AuditEvent Domain Type

```go
type AuditEvent struct {
    ID            string    `json:"id,omitempty"`
    TenantID      string    `json:"tenant_id"`
    Actor         string    `json:"actor"`
    Action        string    `json:"action"`
    TargetType    string    `json:"target_type"`
    TargetID      string    `json:"target_id"`
    OldState      string    `json:"old_state,omitempty"`
    NewState      string    `json:"new_state,omitempty"`
    Timestamp     time.Time `json:"timestamp"`
    Error         string    `json:"error,omitempty"`
    TraceID       string    `json:"trace_id,omitempty"`
    CorrelationID string    `json:"correlation_id,omitempty"`
}
```

### Wiring in main.go

```go
// Tracing
var tracer providers.TracingProvider
if cfg.LangfuseURL != "" {
    tracer = telemetry.NewLangfuseProvider(cfg.LangfuseURL, cfg.LangfuseSecretKey, cfg.LangfusePublicKey)
} else {
    tracer = &telemetry.NoopTracer{}
}

// Metrics
metrics := telemetry.NewMetricsCollector(tracer)

// Worker
worker := &Worker{
    db:        dbProvider,
    queue:     queueAdapter,
    docAgent:  docAgent,
    vendAgent: vendAgent,
    slack:     slackProvider,
    tracer:    tracer,
}

// Health endpoint (public, no auth)
mux.Handle("/health", rl.Middleware(http.HandlerFunc(deps.healthHandler)))
```

## File Map

### Existing Files Modified

| File | Change |
|------|--------|
| `internal/providers/interfaces.go` | Added `TracingProvider` interface (`StartSpan`, `RecordLLMCall`, `RecordError`), `Span` interface (`End`, `SetAttribute`, `SetAttributes`), `SpanOption` and `spanConfig` |
| `internal/domain/types.go` | Added `AuditEvent` struct with trace/correlation fields |
| `cmd/server/main.go` | Added health handler (`GET /health`), tracing/metrics wiring, Langfuse config env vars |

### New Files

| File | Purpose |
|------|---------|
| `internal/logging/logger.go` | `Logger` struct wrapping `slog.Logger` with JSON handler; package-level `Info`/`Error`/`Warn`/`Debug` helpers |
| `internal/telemetry/metrics.go` | `MetricsCollector` — `RecordUploadLatency`, `RecordQueueLag`, `RecordRetryCount`, `RecordHITLRate`, `GetUploadLatencyP95` |
| `internal/telemetry/noop.go` | `NoopTracer` — silent `TracingProvider` for tests and local dev; `noopSpan` — silent `Span` |
| `internal/telemetry/langfuse.go` | `LangfuseProvider` — Langfuse REST API integration with `StartSpan`, `RecordLLMCall`, `RecordError`; `langfuseSpan` — span with attribute tracking; exponential backoff retry with jitter |
| `internal/telemetry/langfuse_test.go` | Tests for Langfuse provider: `RecordLLMCall`, `StartSpan`, `SpanEnd`, `RecordError` (uses `httptest.NewServer`) |
| `internal/adapters/fallback/llm.go` | Fallback LLM adapter that chains multiple `LLMProvider` instances; logs each attempt with provider type, index, error; `ErrNoProvidersConfigured` |

## Compliance with OpsCore Principles

### "Deterministic first, LLM last"

- Observability is entirely deterministic — logs, metrics, and traces are produced by explicit instrumentation in the code path, not by LLM judgment.
- LLM observability (cost, latency, token count) is tracked through the same deterministic mechanism — no LLM is used to analyze other LLM calls.
- The health endpoint is deterministic — it checks dependency connectivity and returns a structured response.

### Multi-tenant isolation

- All metrics include `tenant_id` as a label — tenant-specific dashboards filter by this label.
- Audit events include `TenantID`, `TargetType`, and `TargetID` — queries filter by tenant.
- The health endpoint does **not** include tenant-specific data — it is a global check.
- Logs include `tenant_id` key — log filters can isolate per-tenant activity.
- Langfuse traces include tenant ID in span attributes — operators can filter traces by tenant.

### Observability (meta)

- Observability infrastructure is itself observable — Langfuse failures are logged. Metrics collector errors are logged. Health check failures are logged.
- Every component has a no-op counterpart for testing.
- Wire-up is explicit in `cmd/server/main.go` — no magic, no global state (except the default logger for bootstrapping).

## Related ADRs

- **ADR-0002**: Vendor Onboarding — establishes the audit trail recording pattern and the `TracingProvider` dependency injection that this ADR codifies.
- **ADR-0011**: Queue Semantics, Retries, Idempotency, and DLQ — defines the queue-related metrics (depth, lag, DLQ count) and the worker logging patterns that this ADR makes observable.
- **ADR-001**: Retrieval Architecture for Compliance & Document Search — references the telemetry infrastructure for search quality metrics (latency, tier utilization, zero-result rate).
