# ADR-0009: Bounded Agent Loops and Fallback Policy

## Status

**Proposed** — no retry ceilings are currently enforced in agent workflows. Implementation requires changes to worker loop, job status machine, and agent orchestration.

## Date

2026-07-13

## Context

### Problem Statement

OpsCore agent workflows (vendor onboarding, document ingestion, compliance analysis) interact with external systems — OCR providers, LLM APIs, third-party verification services, and databases. These interactions can fail transiently or persistently. Without explicit bounds on retries and iteration depth, workflows risk entering infinite or degenerate loops:

1. **Unbounded LLM retries.** An LLM call in the compliance agent that consistently times out (e.g., Sarvam latency spike) retries indefinitely via the worker loop, consuming budget and delaying other jobs.

2. **Unbounded OCR retries.** A corrupt PDF fed to Sarvam OCR fails extraction. The document agent retries the same file, producing the same failure, repeatedly.

3. **Unbounded pipeline depth.** A compliance analysis step triggers re-chunking, which triggers re-analysis, which triggers a new compliance job — creating an infinite processing chain.

4. **Unbounded wall-clock time.** A vendor onboarding job that waits on a slow LLM response holds the worker goroutine for minutes, blocking subsequent jobs in the same queue.

5. **No fallback chain.** When the primary provider fails, there is no defined escalation path (retry → alternative provider → degraded mode → human review). The job either retries forever or fails silently.

### Current State

| Component | Current Behavior | Problem |
|-----------|-----------------|---------|
| **Worker poll loop** (`worker.go`) | Calls `queue.Dequeue()` and processes; on error, sends to DLQ | No per-message retry count; first failure goes to DLQ |
| **Job status machine** (`types.go`) | `RETRYABLE_FAILED` → (retry) → `PROCESSING` | No max retry ceiling; agent can loop indefinitely |
| **LLM provider** | No retry logic in adapter; errors propagate to agent | Agent either retries (unbounded) or fails immediately |
| **OCR provider** | Same as LLM — no adapter-level retry | Same problem |
| **Pipeline depth** | No ceiling on agent sub-workflow depth | An agent can spawn sub-jobs recursively without limit |
| **Wall-clock timeout** | No context timeout per job processing step | A single LLM call can block the worker for 30+ seconds |

### Requirements

1. Every agent loop must have an explicit maximum iteration count.
2. Exceeded ceilings must route to a defined fallback path, not continue retrying.
3. Fallback chain must be explicit: primary → fallback → degraded → HITL.
4. Wall-clock timeout per job processing step must prevent a single job from blocking the worker indefinitely.
5. Integration with job status machine: retries track count, terminal failure goes to DEAD_LETTERED.
6. All ceilings must be configurable (env vars or domain constants), not hardcoded magic numbers.

## Decision

### Primary Decision: Explicit Retry Ceilings with Fallback Chain

All agent workflows adopt a unified bounded-loop policy with three dimensions of control:

```
Dimension 1: Retry Count (per-step)
  LLM call → max 3 retries → fallback provider → degraded mode
  OCR call → max 3 retries → LLM-assisted extraction → HITL
  DB write → max 2 retries → DLQ

Dimension 2: Pipeline Depth (per-job)
  max 5 nested sub-workflows → HITL review

Dimension 3: Wall-Clock Timeout (per-processing-step)
  max 5 minutes per job → job marked TIMEOUT → HITL or DLQ
```

### Fallback Chain

Every agent step follows the same escalation pattern:

```
Primary Provider
    ↓ (failure / timeout)
Fallback Provider (if available)
    ↓ (failure / timeout)
Degraded Mode (skip step, proceed with partial data)
    ↓ (skip not acceptable)
HITL (human reviews and decides)
    ↓ (no response within SLA)
DLQ (preserve for offline recovery)
```

### Specific Ceilings

| Ceiling | Value | Config Key | Enforced At |
|---------|-------|-----------|-------------|
| LLM call retries | 3 | `MAX_LLM_RETRIES` | Provider adapter |
| OCR call retries | 3 | `MAX_OCR_RETRIES` | Provider adapter |
| DB operation retries | 2 | `MAX_DB_RETRIES` | Adapter layer |
| Pipeline depth | 5 | `MAX_PIPELINE_DEPTH` | Agent orchestrator |
| Wall-clock timeout | 5m | `JOB_PROCESSING_TIMEOUT` | Worker context |
| Queue message retry (Redis) | 0 (DLQ on first failure) | — | Worker poll loop |

### Integration with Job Status Machine

The job status machine is extended to support retry counting:

```
PENDING → QUEUED → PROCESSING ──→ COMPLETED
                         │
                         ├──→ RETRYABLE_FAILED (retry_count < max) → QUEUED
                         │
                         ├──→ TERMINAL_FAILED (retry_count >= max)
                         │
                         ├──→ DEAD_LETTERED (explicit DLQ routing)
                         │
                         └──→ TIMEOUT (wall-clock exceeded)
```

The `Job` struct gains a `RetryCount` field:

```go
type Job struct {
    ID          string    `json:"id"`
    Workflow    string    `json:"workflow"`
    Status      JobStatus `json:"status"`
    RetryCount  int       `json:"retry_count"`   // NEW
    MaxRetries  int       `json:"max_retries"`   // NEW — per-workflow config
    Input       json.RawMessage `json:"input"`
    Output      json.RawMessage `json:"output,omitempty"`
    Error       string    `json:"error,omitempty"`
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
}
```

Retry logic in the worker loop:

```go
func (w *Worker) processJob(ctx context.Context, job *domain.Job) error {
    ctx, cancel := context.WithTimeout(ctx, w.cfg.JobProcessingTimeout)
    defer cancel()

    err := w.agent.Process(ctx, job)
    if err == nil {
        return w.db.UpdateJobStatus(ctx, job.ID, domain.JobStatusCompleted, nil)
    }

    // Increment retry count
    job.RetryCount++
    if job.RetryCount >= job.MaxRetries {
        return w.db.UpdateJobStatus(ctx, job.ID, domain.JobStatusTerminalFailed, err)
    }

    return w.db.UpdateJobStatus(ctx, job.ID, domain.JobStatusRetryableFailed, err)
}
```

### Provider-Level Retry (Adapter Layer)

Provider adapters implement their own bounded retry before returning an error to the agent:

```go
// internal/adapters/openrouter/llm.go
func (o *OpenRouterLLM) Reason(ctx context.Context, prompt string) (*domain.LLMResult, error) {
    var lastErr error
    for i := 0; i < o.maxRetries; i++ {
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        default:
        }
        result, err := o.callAPI(ctx, prompt)
        if err == nil {
            return result, nil
        }
        lastErr = err
        // Exponential backoff: 1s, 2s, 4s
        time.Sleep(time.Duration(1<<i) * time.Second)
    }
    return nil, fmt.Errorf("llm call failed after %d retries: %w", o.maxRetries, lastErr)
}
```

### Fallback Provider Chain

A fallback provider adapter wraps multiple providers and implements the escalation chain:

```go
// internal/adapters/fallback/llm.go
type FallbackLLM struct {
    primary   providers.LLMProvider  // e.g., OpenRouter
    fallback  providers.LLMProvider  // e.g., Sarvam
    tracer    providers.TracingProvider
}

func (f *FallbackLLM) Reason(ctx context.Context, prompt string) (*domain.LLMResult, error) {
    result, err := f.primary.Reason(ctx, prompt)
    if err == nil {
        return result, nil
    }
    f.tracer.RecordProviderFallback(ctx, "llm", "primary→fallback", err)

    result, err = f.fallback.Reason(ctx, prompt)
    if err == nil {
        return result, nil
    }
    f.tracer.RecordProviderFallback(ctx, "llm", "fallback→degraded", err)

    return nil, fmt.Errorf("all LLM providers failed: %w", err)
    // Agent receives this error and routes to HITL or DLQ
}
```

### How Unbounded Loops Are Prevented in Each Agent

| Agent | Loop Risk | Prevention |
|-------|-----------|------------|
| **VendorAgent** | LLM doc analysis retries on timeout | Provider adapter max 3 retries; agent checks `IsLLMEnabled` and skips if disabled |
| **VendorAgent** | DB upsert retry loop | Adapter max 2 retries; worker sets `TERMINAL_FAILED` after ceiling |
| **DocumentAgent** | OCR retry on corrupt file | Provider adapter max 3 retries; fallback to LLM-assisted extraction; then HITL |
| **DocumentAgent** | Re-chunking loop | Pipeline depth counter; max 5 nested workflows; exceeded routes to HITL |
| **ComplianceAgent** | LLM gap analysis retry | Provider adapter max 3 retries; agent records non-fatal error and skips gap analysis |
| **ComplianceAgent** | Compliance job spawning sub-workflows | Pipeline depth counter in job metadata; agent checks before spawning |
| **RetrievalAgent** | FTS → vector → LLM routing loop | Single-pass routing; no recursive calls in the conditional router |

### Enforcing the Ceilings

Ceilings are defined as domain constants in `internal/domain/agent_config.go`:

```go
// internal/domain/agent_config.go
const (
    DefaultMaxLLMRetries      = 3
    DefaultMaxOCRRetries      = 3
    DefaultMaxDBRetries       = 2
    DefaultMaxPipelineDepth   = 5
    DefaultJobTimeout         = 5 * time.Minute
)
```

These can be overridden by environment variables. The worker reads them at startup and passes them to agents and adapters.

## Alternatives Considered

### 1. No Ceilings (Status Quo)

Rejected because:
- Unbounded retries can consume API budgets (Sarvam charges per call, OpenRouter has rate limits).
- A single stuck job can block the worker goroutine for minutes, delaying all subsequent jobs.
- Pipeline recursion can create infinite job chains, filling the queue with garbage.
- No audit trail for retry decisions — operators cannot tell how many times a job was retried before failing.

### 2. Hardcoded Ceilings in Agent Code

Rejected because:
- Each agent would define its own retry constants, leading to inconsistency.
- Changing ceilings requires touching multiple files and re-deploying.
- No centralized visibility into retry behavior across all agents.

### 3. Queue-Level Retry (Redis List Re-Enqueue)

Rejected because:
- Queue-level retry loses context about which step failed and why.
- The worker would need to inspect the error and decide whether to re-enqueue, duplicating agent logic.
- Redis list retries are unbounded unless the queue adapter tracks count, adding complexity.

### 4. External Retry Library (e.g., cenkalti/backoff)

Partially accepted — the provider adapter uses a simple exponential backoff loop. A full retry library is not needed because:
- The retry logic is simple (linear retries with exponential backoff).
- The worker loop provides the outer retry envelope (job-level retry count).
- Adding a library dependency for a 10-line loop is unnecessary.

## Consequences

### Benefits

1. **Bounded resource consumption.** API calls, worker time, and queue depth are bounded per job. No single job can monopolize system resources.

2. **Explicit fallback paths.** Every provider failure has a defined next step. Operators know the escalation chain for each provider type.

3. **Auditable retry history.** Every retry is recorded in the job's error field and retry count. Operators can see how many times a job was retried and what errors occurred.

4. **Predictable worker behavior.** The wall-clock timeout ensures a worker goroutine is never blocked for more than 5 minutes. This bounds queue latency for other jobs.

5. **Configurable ceilings.** All ceilings are domain constants with env var overrides. Operators can adjust retry counts without code changes.

6. **Graceful degradation.** If the primary LLM provider is down, the fallback provider takes over. If all providers fail, the job routes to HITL or DLQ with full error context.

### Trade-offs / Risks

1. **Retry count increment is a DB write.** Every retry increments `RetryCount` in the job record, which adds latency to the retry path. Mitigation: `RetryCount` is a small integer update; the DB write is negligible compared to the LLM call latency (seconds).

2. **Wall-clock timeout may abort slow-but-legitimate operations.** A 5-minute timeout might be too short for complex compliance analysis involving multiple LLM calls. Mitigation: timeout is configurable per workflow via `Job.MaxRetries` and `JOB_PROCESSING_TIMEOUT`. Compliance jobs can have a longer timeout.

3. **Fallback provider may not exist for all integrations.** Not every provider has a fallback (e.g., there is no alternative to the GST developer portal for GST validation). In these cases, the fallback step is skipped, and the chain goes directly to HITL.

4. **Pipeline depth counter must be propagated correctly.** If an agent spawns a sub-workflow that creates a new job, the depth counter must be included in the new job's metadata. Mitigation: agents pass `PipelineDepth` as part of the job input; the worker reads it and enforces the ceiling.

5. **Exponential backoff delays retry processing.** The provider adapter's backoff (1s, 2s, 4s) means a failing LLM call takes 7 seconds to exhaust retries. For latency-sensitive workflows, this may be noticeable. Mitigation: backoff duration is configurable.

## Related ADRs

- **ADR-0002**: Vendor Onboarding — Asynchronous Real-Time Workflow — establishes the queue-backed worker pattern that this ADR extends with bounded retry ceilings.
- **ADR-0006**: Deterministic-First AI Policy — defines the LLM gating config that prevents unbounded LLM calls at the config level.
- **ADR-0011**: Queue Semantics, Retries, Idempotency, and DLQ — defines the Redis queue semantics that this ADR's retry ceilings integrate with.
- **ADR-0014**: Agentic AI Verification Strategy — defines the verification gates that bounded loops must respect before retrying.
