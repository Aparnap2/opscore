# ADR-0005: Audit Trail and Usage Metering

## Status

**Proposed** — usage metering exists in `internal/domain/usage.go` with plan limits and metric definitions. Audit trail domain model does not exist yet. Implementation is planned for the current sprint.

## Date

2026-07-13

## Context

### Problem Statement

OpsCore processes multi-tenant B2B workflows with financial and compliance consequences. Every vendor onboarding, document processing, and compliance check must be auditable — who did what, when, and what changed. Usage metering is required to track per-tenant consumption for billing, rate limiting, and plan enforcement. The system must address:

1. **Audit compliance** — regulatory and financial workflows require a complete, immutable history of all state transitions. An operator must be able to answer "who approved this vendor?" and "what changed when this job failed?"

2. **Multi-tenant billing readiness** — each tenant consumes resources (document storage, LLM calls, OCR pages) that must be tracked against their plan limits. OpsCore must know when a tenant exceeds their plan and enforce limits before processing continues.

3. **Observability for operators** — the admin UI and support team must be able to inspect job-level audit trails, tenant-level usage summaries, and identify abusive or anomalous consumption patterns.

4. **Idempotent metering** — usage counters must be idempotent under retry. A job that retries must not double-count the same LLM call or document upload.

### Current Architecture

The current system has partial foundations but no integrated audit or usage metering:

| Area | Current State | Limitation |
|------|--------------|------------|
| **Audit trail** | `types.go:76-89` defines `AuditEvent` struct with `tenant_id`, `actor`, `action`, `old_state`, `new_state`, timestamps | No `audit_events` table exists. Events are appended in-memory only. No persistence, no query API. |
| **Usage metering** | `domain/usage.go` defines `Metric` enum, `UsageRecord` struct, `PlanLimits` map, and `GetLimit()`/`IsUnlimited()` functions | Domain types exist but no database schema, no increment logic, no middleware integration, no enforcement at the application layer. |
| **State transitions** | `JobStatus` machine in `types.go:41-53` with explicit transitions (PENDING → QUEUED → PROCESSING → COMPLETED/FAILED) | Transitions happen but are not recorded to an audit log. No `old_state` → `new_state` persistence exists. |
| **HITL decisions** | `HITLRequest` struct with `Approved/Rejected` status | Decision events are not recorded as audit entries. No cross-referencing between job history and approval events. |
| **Document processing** | Documents uploaded and processed with `ContentHash` for dedup | No per-document audit of who uploaded it and what pipeline processed it. |

### Requirements

1. **Append-only audit log** — every state transition must be recorded immutably. No updates, no deletes. Retention policy truncates, never mutates.
2. **Single-writer per event** — each audit event is written once by the component that performed the transition. No concurrent writers to the same event.
3. **Plan-based usage enforcement** — each tenant has a plan (starter/pro/business). Before processing starts, check whether the tenant has remaining quota. Exceeded limits produce a clear rejection reason.
4. **Monthly rolling counters** — usage resets at month boundaries. Counters are pre-aggregated for fast reads at decision time.
5. **Actor identification** — every audit event records who performed the action: a user ID for HITL decisions, "system" for automated transitions, or "queue-worker:N" for worker-processed jobs.
6. **Queryable for operators** — support `GET /admin/usage-summary` for per-tenant monthly usage and `GET /jobs/{id}/audit` for per-job event history.

## Decision

Implement two complementary systems:

1. **Audit trail**: An append-only event log recording all workflow state transitions with actor, action, old state, new state, timestamp, and tenant ID. Stored in an `audit_events` table with JSONB metadata for extensibility.
2. **Usage metering**: Monthly rolling counters per tenant tracking document count, LLM calls, OCR pages, and compliance checks. Plan-based limits enforced at the application layer before processing begins.

### Architecture Overview

```
Job / Workflow Component
         │
         ▼
┌─────────────────────────────────────────────┐
│  State Transition Occurs                     │
│  (ProcessVendor, DocumentUpload, HITL, etc.) │
└──────────┬──────────────────────────────────┘
           │
           ├────► 1. Audit Middleware
           │        ┌─────────────────────┐
           │        │  AppendAuditEvent()  │
           │        │  - actor             │
           │        │  - old_state         │
           │        │  - new_state         │
           │        │  - metadata (JSONB)  │
           │        │  - created_at        │
           │        └──────────┬──────────┘
           │                   │ INSERT INTO audit_events
           │                   ▼
           │              ┌─────────┐
           │              │  PostgreSQL │
           │              │  audit_events│
           │              └─────────┘
           │
           ├────► 2. Usage Metering Middleware
           │        ┌─────────────────────┐
           │        │  IncrementUsage()    │
           │        │  - tenant_id         │
           │        │  - metric            │
           │        │  - count (+1)        │
           │        │  - period_start      │
           │        └──────────┬──────────┘
           │                   │ UPSERT INTO usage_records
           │                   ▼
           │              ┌─────────┐
           │              │  PostgreSQL │
           │              │usage_records│
           │              └─────────┘
           │
           ├────► 3. Plan Enforcement (pre-check)
           │        ┌─────────────────────┐
           │        │  CheckPlanLimit()   │
           │        │  - tenant_id        │
           │        │  - metric           │
           │        │  - returns: OK or   │
           │        │    ErrLimitExceeded │
           │        └──────────┬──────────┘
           │                   │ SELECT FROM usage_records
           │                   ▼
           │              ┌─────────┐
           │              │  Application  │
           │              │  rejects with │
           │              │  429 / 403    │
           │              └─────────┘
           │
           ▼
    Processing Continues
         or Rejected
```

### Why Append-Only Audit Instead of Per-Row Change Capture

1. **Simplicity.** Append-only inserts are the simplest write pattern in PostgreSQL. No triggers, no CDC pipelines, no WAL parsing. Every state transition produces a single `INSERT`.
2. **Immutability by design.** Once written, audit events are never updated or deleted. This satisfies compliance requirements for financial and regulatory workflows.
3. **Idempotent-friendly.** `INSERT ON CONFLICT DO NOTHING` with a deterministic `event_id` (derived from job ID + sequence number) prevents duplicate audit entries under retry.
4. **Query performance is acceptable.** Audit events are queried by `(tenant_id, job_id)` or `(tenant_id, created_at range)`. An index on `(tenant_id, job_id, created_at)` makes these queries fast even at millions of rows. Retention policy keeps table size bounded.

### Why Usage Metering Is Application-Layer (Not Database Trigger)

1. **Plan enforcement is a business rule.** `CheckPlanLimit()` runs before processing begins and returns a clear machine-readable error. A database trigger cannot easily distinguish between permanent failures and transient retries.
2. **Usage counts must be idempotent.** The application layer knows whether a job is a retry or a first attempt. It can skip incrementing the counter for duplicate processing. A trigger has no access to this context.
3. **Metrics composition.** One job may increment multiple metrics (e.g., document upload increments `documents_uploaded` and `ocr_pages`). Application logic can atomically increment all relevant counters in a single transaction.
4. **Testability.** `CheckPlanLimit()` and `IncrementUsage()` are pure domain functions that can be unit tested without database fixtures. Trigger logic would require integration tests for even simple limit checks.

### Specific Technology Choices

| Component | Choice | Rationale |
|-----------|--------|-----------|
| **Audit event storage** | PostgreSQL `audit_events` table, append-only | Already in the stack; no new infrastructure; RLS provides tenant isolation |
| **Audit event ID** | Deterministic: `"audit-{job_id}-{seq}"` — SHA256-prefixed | Prevents duplicate inserts under retry via `ON CONFLICT DO NOTHING` |
| **Audit metadata** | JSONB column for extensible structured data | Regulator-specific fields, error codes, HITL reasons — schema-free |
| **Usage counter storage** | PostgreSQL `usage_records` table, monthly period | Pre-aggregated by month for fast reads; upsert pattern for idempotence |
| **Usage metric types** | Go enum in `domain/usage.go`: `documents_uploaded`, `ocr_pages`, `llm_calls`, `compliance_checks` | Extensible; new metrics added as const values |
| **Plan limits** | Go map in `domain/usage.go`: `PlanLimits["starter"|"pro"|"business"]` | Deterministic, testable, no DB read needed at enforcement time |
| **Plan enforcement** | `CheckPlanLimit()` called at entry point of each workflow | Returns `ErrPlanLimitExceeded` with current usage and limit; logged and traced |
| **Audit middleware** | Wrapper function called after every state transition in agent code | Consistent pattern: `agent.ProcessX()` → `audit.Append()` in same function |
| **Usage middleware** | `IncrementUsage()` called after successful processing completion | Only increments on terminal success — retries don't double-count |
| **Admin reporting API** | `GET /admin/usage-summary` — aggregates by tenant and month | Supports billing review and operational dashboards |
| **Job audit API** | `GET /jobs/{id}/audit` — returns ordered event list | Used by support team and admin UI for workflow debugging |
| **Retention policy** | Configurable TTL (default 90 days) via periodic cron job | Deletes from `audit_events` and `usage_records` older than TTL; logged |

## Alternatives Considered

### 1. No Audit Trail (Status Quo)

Rejected because:
- Compliance requirements mandate immutable history for financial and regulatory workflows.
- Support team cannot debug customer issues without knowing what happened in each job.
- No ability to replay or investigate failed workflows.
- Auditors would reject the system outright for regulated B2B use cases.

### 2. External Audit Service (eg. AuditDB, Event Store)

Rejected because:
- Adds infrastructure complexity and latency for a simple append-only write pattern.
- OpsCore already has PostgreSQL — adding a separate event store duplicates operational burden.
- The audit volume is modest (< 1M events/month at target scale) and well within PostgreSQL capabilities.
- Transactional consistency between audit events and domain state is harder with an external service.

### 3. Per-Row Change Data Capture (CDC via Debezium / PostgreSQL Triggers)

Rejected because:
- CDC captures every column change, not semantic state transitions. An audit event should say "vendor approved by operator" not "`row.approved = false → true`". The latter requires interpretation.
- Triggers add hidden complexity to schema migrations and can cause unexpected failures.
- CDC pipelines require Kafka or similar infrastructure — disproportionate for the audit volume.
- Application-layer audit allows richer metadata: reason codes, trace IDs, correlation IDs that CDC cannot provide.

### 4. LLM-Generated Audit Summaries

Rejected because:
- Audit data must be exact, not summarized. An LLM might hallucinate a transition that never happened.
- The "deterministic first, LLM last" principle applies: audit is pure record-keeping with zero ambiguity.
- Generating an audit summary from raw events (for human reading) is a valid post-hoc use of LLM, but the raw events themselves must be deterministic.

### 5. In-Memory Usage Counters with Periodic Persistence

Rejected because:
- Worker crashes would lose in-memory counters, allowing tenants to exceed their plan between restarts.
- Multi-worker deployments (future) would have inconsistent counters across instances.
- Redis atomic counters could work, but PostgreSQL upserts are already fast (< 5ms) and provide durable, consistent counters accessible to all workers.

## Consequences

### Benefits

1. **Complete audit trail.** Every state transition across all workflows is recorded immutably with actor, action, old state, new state, and metadata. This satisfies compliance requirements and enables operational debugging.

2. **Billing readiness.** Monthly usage counters per metric per tenant provide the raw data for invoice generation. The `PlanLimits` map encodes tier definitions in domain code, making plan changes auditable through normal Git workflows.

3. **Application-layer enforcement.** `CheckPlanLimit()` runs before any expensive operation (document upload, LLM call). Tenants receive a clear `429 Too Many Requests` or `403 Plan Limit Exceeded` response with current usage and limit values.

4. **Idempotent by design.** Audit events use deterministic IDs (`"audit-{job_id}-{seq}"`) so retries produce `ON CONFLICT DO NOTHING` instead of duplicates. Usage counters use upsert with `period_start` as part of the conflict target, making `IncrementUsage()` safe under retry.

5. **Observable usage patterns.** The `GET /admin/usage-summary` endpoint surfaces per-tenant consumption trends. Anomalous spikes (e.g., 10,000 LLM calls in an hour) trigger operational alerts.

6. **Multi-tenant isolation.** Both `audit_events` and `usage_records` include `tenant_id` as the first filter column. RLS applies as defense-in-depth. Admin API requires elevated privileges to view cross-tenant data.

7. **Low operational overhead.** Two PostgreSQL tables, two GIN/B-tree indexes, a retention cron job. No new infrastructure, no streaming pipeline, no external audit service.

### Trade-offs / Risks

1. **Storage growth for audit_events.** Each job produces multiple events (created, queued, processing, completed/failed, HITL events). At 100K jobs/month with ~5 events each, that is 500K rows/month. Mitigation: retention policy with configurable TTL (default 90 days); archive to cold storage for long-term compliance needs before deletion.

2. **Usage counter contention under high concurrency.** Multiple workers processing jobs for the same tenant simultaneously will contend on the same `usage_records` row. Mitigation: use `INSERT ... ON CONFLICT DO UPDATE` with `RETURNING`; the upsert is atomic and fast (< 5ms). For extreme contention, shard by metric type.

3. **No real-time enforcement for concurrent requests.** `CheckPlanLimit()` and `IncrementUsage()` are not in the same transaction. A race window exists where two concurrent requests both pass the check but together exceed the limit. Mitigation: accept occasional soft overages; the `usage_records` upsert is the source of truth for billing, not the pre-check. Overages are corrected at month-end reconciliation.

4. **Plan limits are in-memory Go code, not configurable at runtime.** Changing `PlanLimits` requires a code deploy. This is intentional (plan definitions are business logic subject to code review and audit), but it means plan changes cannot be made through the admin UI. Mitigation: the `PlanLimits` map is small and changes infrequently; deploy velocity is fast enough.

5. **Audit metadata in JSONB has no schema validation.** Any component can write arbitrary JSON to the `metadata` column. Misspelled keys or inconsistent types make querying difficult. Mitigation: document the expected schema per event action in the domain types; add integration tests that verify metadata structure for each event type.

## Implementation Details

### Audit Event Schema

```sql
CREATE TABLE audit_events (
    event_id    TEXT        PRIMARY KEY,  -- deterministic: "audit-{job_id}-{seq}"
    tenant_id   TEXT        NOT NULL,
    job_id      TEXT        NOT NULL,
    actor       TEXT        NOT NULL,      -- "system", "user:{id}", "queue-worker:{id}"
    action      TEXT        NOT NULL,      -- "vendor.created", "job.queued", "hitl.approved"
    old_state   TEXT,                      -- previous JobStatus or domain state
    new_state   TEXT,                      -- new JobStatus or domain state
    metadata    JSONB       DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_events_tenant_job
    ON audit_events (tenant_id, job_id, created_at);

CREATE INDEX idx_audit_events_tenant_created
    ON audit_events (tenant_id, created_at DESC);
```

### Usage Counter Schema

```sql
CREATE TABLE usage_records (
    tenant_id    TEXT        NOT NULL,
    metric       TEXT        NOT NULL,     -- documents_uploaded, ocr_pages, llm_calls, compliance_checks
    count        BIGINT      NOT NULL DEFAULT 0,
    period_start DATE        NOT NULL,     -- first day of the month (e.g., '2026-07-01')
    period_end   DATE        NOT NULL,     -- last day of the month
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, metric, period_start)
);

CREATE INDEX idx_usage_records_tenant
    ON usage_records (tenant_id, period_start DESC);
```

### What Transitions Are Audited

| Workflow | Action | Triggered By | Metadata |
|----------|--------|-------------|----------|
| **Vendor** | `vendor.created` | HTTP handler | `{ "gst": "...", "risk_score": 0 }` |
| **Vendor** | `vendor.onboarding_started` | Queue worker | `{ "queue": "vendor-queue" }` |
| **Vendor** | `vendor.onboarding_completed` | Agent | `{ "risk_score": 75, "trust_tier": "STANDARD" }` |
| **Vendor** | `vendor.onboarding_failed` | Agent | `{ "error": "...", "retryable": true }` |
| **Vendor** | `vendor.approved` | HITL / Admin | `{ "operator": "user:42", "reason": "..." }` |
| **Vendor** | `vendor.rejected` | HITL / Admin | `{ "operator": "user:42", "reason": "..." }` |
| **Job** | `job.queued` | HTTP handler | `{ "workflow_type": "VENDOR_ONBOARDING" }` |
| **Job** | `job.processing` | Queue worker | `{ "worker_id": "worker-1" }` |
| **Job** | `job.completed` | Agent | `{ "duration_ms": 1234 }` |
| **Job** | `job.failed` | Agent | `{ "error": "...", "attempts": 3 }` |
| **Job** | `job.dead_lettered` | Queue worker | `{ "error": "...", "dlq": "vendor-queue-poison" }` |
| **Document** | `document.uploaded` | HTTP handler | `{ "filename": "...", "size": 1024, "hash": "sha256:..." }` |
| **Document** | `document.processed` | Queue worker | `{ "ocr_pages": 5, "doc_type": "invoice" }` |
| **Document** | `document.ocr_failed` | Queue worker | `{ "error": "...", "retryable": true }` |
| **HITL** | `hitl.requested` | Agent | `{ "reason": "risk_score >= 60", "channel": "slack" }` |
| **HITL** | `hitl.approved` | Slack webhook / Admin | `{ "operator": "user:42", "decision": "approve" }` |
| **HITL** | `hitl.rejected` | Slack webhook / Admin | `{ "operator": "user:42", "decision": "reject" }` |
| **Compliance** | `compliance.check_started` | Agent | `{ "documents_checked": 5 }` |
| **Compliance** | `compliance.check_completed` | Agent | `{ "gaps_found": 2, "severity": "HIGH" }` |
| **Compliance** | `compliance.gap_flagged` | Agent | `{ "gap": "...", "severity": "HIGH", "section": "73" }` |

### Usage Counter Integration

Every successful processing completion increments the relevant usage counters atomically:

```
ProcessVendor():
  ├── vendor_job = agent.ProcessVendor(ctx, vendor, job)
  ├── if vendor_job.Status == COMPLETED:
  │     ├── IncrementUsage(ctx, tenantID, MetricDocumentsUploaded, 1)
  │     ├── if ocr_performed: IncrementUsage(ctx, tenantID, MetricOCRPages, ocrPages)
  │     └── if llm_used:      IncrementUsage(ctx, tenantID, MetricLLMCalls, 1)
  ├── AppendAuditEvent(ctx, event)
  └── return result

IncrementUsage(ctx, tenantID, metric, count):
  └── INSERT INTO usage_records (tenant_id, metric, count, period_start, period_end, updated_at)
        VALUES ($1, $2, $3, $4, $5, NOW())
        ON CONFLICT (tenant_id, metric, period_start)
        DO UPDATE SET count = usage_records.count + EXCLUDED.count,
                      updated_at = NOW()
```

### Plan Enforcement Flow

```
Pre-check (before expensive operation):

  CheckPlanLimit(ctx, tenantID, metric):
    ├── plan = GetTenantPlan(ctx, tenantID)          // from tenant_settings
    ├── limit = GetLimit(plan, metric)                // from domain PlanLimits
    ├── if IsUnlimited(limit): return OK
    ├── currentUsage = SELECT count FROM usage_records
    │                   WHERE tenant_id = $1
    │                   AND metric = $2
    │                   AND period_start = $3
    ├── if currentUsage >= limit:
    │     return ErrPlanLimitExceeded{
    │         metric:  metric,
    │         current: currentUsage,
    │         limit:   limit,
    │         plan:    plan,
    │     }
    └── return OK
```

### Monthly Rolling Window

The usage period is calculated deterministically:

```go
func CurrentUsagePeriod(now time.Time) (start, end time.Time) {
    year, month, _ := now.Date()
    start = time.Date(year, month, 1, 0, 0, 0, 0, now.Location())
    end = start.AddDate(0, 1, -1)
    return
}
```

This ensures all workers compute the same period for the same timestamp, preventing edge-case misalignment across month boundaries.

### Retention Policy

```sql
-- Cron job run daily:
DELETE FROM audit_events
WHERE created_at < NOW() - INTERVAL '90 days';

DELETE FROM usage_records
WHERE period_start < DATE_TRUNC('month', NOW()) - INTERVAL '12 months';
```

- Audit events: 90-day retention for operational querying. Archive to cold storage before deletion for long-term compliance.
- Usage records: 12-month retention for annual billing reconciliation. Older records are not needed for plan enforcement.

### Audit Middleware Integration

Every agent method that performs a state transition follows this pattern:

```go
func (a *VendorAgent) ProcessVendor(ctx context.Context, vendor *domain.Vendor, job *domain.Job) (*domain.VendorJob, error) {
    // ... processing logic ...

    // Determine old and new state
    oldState := string(job.Status)
    newState := string(domain.JobStatusCompleted)

    // Append audit event
    auditEvent := domain.AuditEvent{
        ID:         fmt.Sprintf("audit-%s-%d", job.ID, seq),
        TenantID:   vendor.TenantID,
        Actor:      "system",
        Action:     "vendor.onboarding_completed",
        OldState:   oldState,
        NewState:   newState,
        Metadata:   map[string]interface{}{"risk_score": riskScore, "trust_tier": tier},
    }
    if err := a.auditProvider.Append(ctx, auditEvent); err != nil {
        // Non-fatal — log and continue
        slog.Warn("failed to append audit event", "error", err, "event_id", auditEvent.ID)
    }

    // Increment usage counters
    if err := a.usageProvider.Increment(ctx, vendor.TenantID, domain.MetricDocumentsUploaded, 1); err != nil {
        slog.Warn("failed to increment usage", "error", err)
    }

    return result, nil
}
```

Audit and usage failures are non-fatal — the system continues processing but logs the error. This prevents a downstream audit outage from blocking critical onboarding workflows.

## File Map

### Existing Files Modified

| File | Change |
|------|--------|
| `internal/domain/types.go` | `AuditEvent` struct already exists (`types.go:76-89`) — add `Metadata map[string]interface{}` field alongside existing fields |
| `internal/domain/usage.go` | Already defines `Metric`, `UsageRecord`, `PlanLimits`, `GetLimit()`, `IsUnlimited()` — add `IncrementUsage()` and `CheckPlanLimit()` domain functions |
| `internal/providers/interfaces.go` | Add `AuditProvider` interface (`Append(ctx, event) error`) and `UsageProvider` interface (`Increment(ctx, tenantID, metric, count) error`, `CheckLimit(ctx, tenantID, metric) (bool, error)`) |
| `internal/agents/vendor_agent.go` | Add audit and usage calls after state transitions in `ProcessVendor()` |
| `internal/agents/compliance_agent.go` | Add audit and usage calls after compliance check completes |
| `internal/agents/document_agent.go` | Add audit after document upload and processing; increment OCR page counter |
| `cmd/server/main.go` | Register `GET /admin/usage-summary` and `GET /jobs/{id}/audit` handlers; wire audit and usage providers |
| `internal/adapters/postgres/adapter.go` | Optionally expose `queryExecer` for audit and usage adapter implementations |

### New Files

| File | Purpose |
|------|---------|
| `internal/adapters/postgres/audit.go` | `PostgresAuditProvider` — implements `AuditProvider` with `INSERT INTO audit_events` and deterministic ID dedup |
| `internal/adapters/postgres/usage.go` | `PostgresUsageProvider` — implements `UsageProvider` with `UPSERT INTO usage_records` and `SELECT` for limit checks |
| `internal/adapters/http/admin_handler.go` | `GET /admin/usage-summary` — per-tenant monthly aggregation; `GET /jobs/{id}/audit` — ordered event list for a job |
| `internal/domain/audit.go` | Audit domain helpers: `NewAuditEventID()`, `AuditAction` constants, event type-specific metadata builders |
| `internal/domain/usage_meter.go` | Usage domain functions: `IncrementUsage()`, `CheckPlanLimit()`, `CurrentUsagePeriod()`, `ErrPlanLimitExceeded` error type |
| `tests/unit/usage_meter_test.go` | Unit tests for `CheckPlanLimit()`, `IncrementUsage()`, `CurrentUsagePeriod()`, plan enforcement edge cases |
| `tests/unit/audit_test.go` | Unit tests for `NewAuditEventID()`, event metadata builders, sequential ID generation |
| `tests/integration/postgres_audit_test.go` | Integration tests for `PostgresAuditProvider`: insert, read, dedup, tenant isolation, JSONB metadata |
| `tests/integration/postgres_usage_test.go` | Integration tests for `PostgresUsageProvider`: increment, check limit, monthly rollover, concurrent upserts |
| `tests/agentic/audit_workflow_test.go` | Agentic tests: verify audit events are created for vendor onboarding, document processing, compliance checks |
| `tests/agentic/usage_enforcement_test.go` | Agentic tests: verify plan limit enforcement, rejection on exceeded limits, idempotent increment under retry |

## Compliance with OpsCore Principles

### "Deterministic first, LLM last"

Audit and usage metering are pure deterministic systems:

- **Audit events**: Structured data with explicit fields — no LLM involvement in event generation. Every event is a deterministic record of a state transition.
- **Usage counters**: Arithmetic increments and bounded comparisons — pure Go logic with no AI component.
- **Plan enforcement**: Map lookup + integer comparison — trivially deterministic. No model, no prompt, no inference.
- **Retention policy**: Time-based truncation with configurable TTL — deterministic and auditable.

### Multi-tenant isolation

- All audit queries include `WHERE tenant_id = $1` as the first filter.
- `usage_records` uses `PRIMARY KEY (tenant_id, metric, period_start)` — tenant is the first column in every access path.
- `GET /admin/usage-summary` requires elevated privileges and returns per-tenant data; cross-tenant aggregation is admin-only.
- PostgreSQL RLS (already implemented on `audit_events` and `usage_records`) applies as defense-in-depth.
- The `AuditProvider` and `UsageProvider` interfaces accept `tenantID` as a required parameter.

### Observability

- Every audit event is recorded with trace ID and correlation ID, linking to the originating request and Langfuse trace.
- Usage counter changes are logged at `slog.Info` level with tenant ID, metric, old count, and new count.
- Plan limit enforcement produces structured error responses that are captured in metrics: `opsmeter_usage_denied_total{tenant, plan, metric}`.
- The `GET /admin/usage-summary` endpoint feeds the Streamlit admin UI for operational visibility.
- Anomalous usage patterns (spikes > 3σ from trailing 3-month average) trigger alerts in the monitoring dashboard.

## Related ADRs

- **ADR-001**: Retrieval Architecture for Compliance & Document Search — establishes the "deterministic first, LLM last" principle that this ADR extends to audit and metering.
- **ADR-0002**: Vendor Onboarding — Asynchronous Real-Time Workflow with Queue-Backed Processing — the vendor onboarding workflow is the primary consumer of audit events and usage counters.
