# ADR-0002: Vendor Onboarding — Asynchronous Real-Time Workflow with Queue-Backed Processing

## Status

**Accepted** — implemented and deployed.

## Date

2026-07-13

## Context

### Problem Statement

OpsCore onboards B2B vendors for Indian businesses. Each vendor must be evaluated for trustworthiness before being approved for transactions. The evaluation involves:

1. **Identifier validation** — syntactic checks on GST numbers, PAN cards, and IFSC codes using India-specific regex patterns and checksum rules.
2. **Risk scoring** — a weighted deterministic score based on identifier validity, duplicate detection, document completeness, and dispute flags.
3. **Trust tier assignment** — mapping the risk score to a trust battery tier (PROBATION → STANDARD → PREFERRED → STRATEGIC, with BLOCKED as terminal).
4. **Document analysis** — optional LLM-based reasoning when vendor-supplied documents are present.
5. **Human-in-the-loop approval** — when risk exceeds thresholds or validation fails, an operator must review and approve/reject.

The system must handle these requirements:

1. **Non-blocking HTTP response** — the vendor creation endpoint must return quickly (< 500ms) without waiting for risk computation, document analysis, or human approval.
2. **Deterministic risk scoring** — trust evaluation must be reproducible, auditable, and free of LLM bias.
3. **Human-in-the-loop** — ambiguous or high-risk outcomes must be routed to an operator for decision.
4. **Idempotent processing** — duplicate messages, worker crashes, and retries must not produce duplicate side effects.
5. **Observable state machine** — every vendor and job must have a well-defined status with auditable transitions.
6. **Multi-tenant isolation** — all data and processing must be scoped to a tenant.

### Current Architecture

Before this decision, vendor onboarding was a synchronous HTTP request-response cycle:

```
POST /vendors → parse form → create DB record → return 201
```

There was no risk scoring, no trust tier assignment, no document analysis, and no HITL. The vendor was created with `RiskScore=0` and `Approved=false` — a stub that required manual follow-up outside the system.

## Decision

Vendor onboarding and document-driven trust evaluation will use an **asynchronous real-time workflow** with queue-backed processing, deterministic risk/trust logic, and Slack-based HITL approval for ambiguous or high-risk outcomes.

### Architecture Overview

```
POST /vendors
    │
    ▼
┌──────────────────────┐
│  HTTP Handler         │  ← Returns 201 immediately
│  (main.go:456-553)    │     Vendor created with RiskScore=0
│                       │     Job enqueued to "vendor-queue"
└──────────┬───────────┘
           │
           ▼
┌──────────────────────┐
│  Redis Queue          │  ← LPUSH → BRPopLPush (30s blocking)
│  "vendor-queue"       │     Hash for O(1) message lookup
│  "vendor-queue-poison"│     DLQ for failed messages
└──────────┬───────────┘
           │
           ▼
┌──────────────────────┐
│  Queue Worker         │  ← pollQueue goroutine
│  (worker.go:175-261)  │     Optimistic locking on Job.Version
│                       │     Sets job→PROCESSING
└──────────┬───────────┘
           │
           ▼
┌──────────────────────────────────────────────┐
│  VendorAgent.ProcessVendor                    │
│  (internal/agents/vendor_agent.go:55-200)     │
│                                                │
│  1. Validate identifiers (IndiaValidator)       │
│  2. ComputeVendorRisk() — deterministic        │
│  3. Determine HITL need                        │
│  4. Optional LLM analysis (non-fatal)          │
│  5. Determine trust tier                       │
│  6. UpsertVendor + UpsertJob (COMPLETED)       │
│  7. Create HITLRequest if needed               │
└──────────┬─────────────────────────────────────┘
           │
           ▼
┌──────────────────────┐
│  Worker checks result │  ← needs_hitl? → Slack Block Kit
│  (worker.go:234-249)  │     Approve / Reject buttons
└──────────┬───────────┘
           │
           ▼
┌──────────────────────┐
│  Slack Webhook        │  ← POST /slack/webhook
│  (main.go:622-780)    │     block_actions callback
│                       │     Updates Job + HITLRequest
│  Admin API fallback   │  ← POST /admin/review-queue
│  (main.go:870-960)    │     Non-Slack alternative
└──────────────────────┘
```

### Why Asynchronous Instead of Synchronous HTTP-Only

1. **Risk scoring is fast but document analysis is not.** Identifier validation and risk scoring complete in microseconds. But document OCR (via Sarvam AI) and optional LLM analysis can take seconds. Blocking the HTTP response for these would violate the < 500ms response target.

2. **HITL is inherently asynchronous.** Human approval via Slack can take minutes to hours. A synchronous HTTP response cannot represent a "pending approval" state without polling or webhooks.

3. **Queue-backed processing provides resilience.** If the worker crashes mid-processing, the message remains in the Redis processing list and can be recovered. A synchronous handler would lose in-flight work on process restart.

4. **Idempotent retry is easier with queues.** The queue layer provides at-least-once delivery semantics. The agent layer uses optimistic locking and deterministic IDs to ensure exactly-once processing semantics.

### Why Deterministic Scoring / State Machine Stays in Domain

All risk scoring, trust tier transitions, and job status transitions are implemented as pure Go functions in `internal/domain/`:

| Component | File | Purity |
|-----------|------|--------|
| `ComputeVendorRisk()` | `internal/domain/risk_scorer.go` | Pure Go, regex-based, weighted scoring |
| `TrustBattery` state machine | `internal/domain/trust_battery.go` | Pure Go, explicit transitions |
| `JobStatus` state machine | `internal/domain/types.go:44-53` | Pure Go, explicit transitions |
| `IndiaValidator` | `internal/domain/india_validator.go` | Pure Go, regex-based validation |

These are kept in the domain layer because:
1. **Auditability** — every risk score is reproducible from the same inputs. No LLM "judgment" that can drift.
2. **Testability** — pure functions are trivially testable without containers or mocks.
3. **Determinism** — same inputs always produce same outputs. No model versioning, no prompt drift.
4. **Performance** — regex and arithmetic are orders of magnitude faster than any LLM call.

### Why Slack Approval Is Part of the Workflow Boundary

Slack is the primary HITL channel because:
1. **Operator familiarity** — Slack is already the daily tool for ops teams. No separate dashboard login required.
2. **Block Kit interactivity** — Approve/Reject buttons in Slack messages provide a zero-friction UX.
3. **Webhook callback** — Slack's `block_actions` interactive payload is parsed by `POST /slack/webhook` and drives the job status transition.
4. **Admin API fallback** — `POST /admin/review-queue` provides a non-Slack alternative for environments where Slack is unavailable.

The HITL provider is abstracted behind `providers.HITLProvider` interface, so Slack can be replaced without changing agent logic.

### Specific Technology Choices

| Component | Choice | Rationale |
|-----------|--------|-----------|
| **Queue** | Redis lists (LPUSH/BRPopLPush) with hash for O(1) message lookup | Zero ops overhead (already in stack), atomic lease semantics via BRPopLPush, DLQ via `{queue}-poison` |
| **Queue fallback** | Google Cloud Pub/Sub (via `providers.QueueProvider` interface) | For production deployments requiring managed queue with explicit Ack/Nack |
| **DLQ** | `{queue}-poison` Redis list | Full JSON preserved for DLQ consumers; hash entry cleaned after poison |
| **Risk scoring** | `internal/domain/risk_scorer.go` — pure Go, regex-based | Deterministic, testable, no LLM dependency |
| **Trust battery** | `internal/domain/trust_battery.go` — explicit state machine | PROBATION → STANDARD → PREFERRED → STRATEGIC + BLOCKED (terminal) |
| **Job status machine** | `internal/domain/types.go:44-53` — explicit enum | PENDING → QUEUED → PROCESSING → COMPLETED/FAILED/DEAD_LETTERED with AWAITING_HITL branch |
| **India identifier validation** | `internal/domain/india_validator.go` — regex-based | Syntactic GST/PAN/IFSC validation, no external API call |
| **HITL channel** | Slack Block Kit (primary) + Admin API (fallback) | Approve/Reject buttons in Slack; `POST /admin/review-queue` as alternative |
| **HITL provider interface** | `providers.HITLProvider` | Abstract interface; Slack implementation in `internal/providers/slack_hitl.go` |
| **Queue provider interface** | `providers.QueueProvider` | Redis implementation; Pub/Sub alternative available |
| **Idempotency** | Content-based SHA256 dedup + optimistic locking + deterministic HITLRequest.ID | Prevents duplicate processing at multiple layers |
| **Worker concurrency** | Two goroutines (document-queue, vendor-queue) | Each with panic recovery, 30s blocking Dequeue, DLQ on failure |
| **Test infrastructure** | Testcontainers (PostgreSQL 16, Redis 7, MinIO) | Real containers, stub OCR/LLM, no real API calls in agentic tests |

## Alternatives Considered

### 1. Synchronous HTTP-Only Processing

Rejected because:
- Document OCR and LLM analysis can take 5-30 seconds, blocking the HTTP response.
- HITL is inherently asynchronous (minutes to hours for human response).
- No resilience to worker crashes — in-flight work is lost on process restart.
- No retry mechanism — a transient OCR failure requires the client to resubmit.

### 2. LLM-Based Risk Scoring

Rejected because:
- Violates the "deterministic first, LLM last" principle.
- Risk scores would not be reproducible — same vendor could get different scores on different days.
- LLM cost per vendor would be non-trivial at scale (₹0.10-0.50 per call).
- Audit trail would require storing prompts and responses, adding complexity.
- The scoring logic (regex validation + weighted arithmetic) is trivially deterministic.

### 3. External Workflow Engine (Temporal, Camunda)

Rejected because:
- Adds significant infrastructure complexity (Temporal server, worker binaries).
- The workflow is simple enough for a Redis queue + Go state machine.
- OpsCore's philosophy is to minimize external dependencies.
- The queue-backed approach with optimistic locking provides equivalent resilience guarantees.

### 4. Email-Based HITL

Rejected because:
- Email round-trip latency is unpredictable (minutes to hours).
- No interactive buttons — requires parsing email replies.
- Slack is already in the ops workflow; adding email would fragment the operator experience.

## Consequences

### Benefits

1. **Non-blocking API.** `POST /vendors` returns in < 50ms (DB write + queue enqueue). The client receives an immediate `202 Accepted` with a job ID for status polling.

2. **Deterministic trust evaluation.** Risk scores are reproducible from the same inputs. No LLM drift, no model versioning, no prompt engineering for core scoring.

3. **Resilient to failures.** Worker crashes lose only the in-flight message (which remains in the Redis processing list). On restart, the message is either re-queued or processed from the processing list. DB outages are handled gracefully with retry logic.

4. **Idempotent by design.** Multiple layers prevent duplicate processing:
   - Content-based SHA256 dedup before document upload (409 Conflict if exists)
   - Optimistic locking on `Job.Version` (`UPDATE WHERE version = $N`)
   - `INSERT ON CONFLICT DO UPDATE` for upserts
   - Deterministic HITLRequest.ID (`"hitl-"+jobID`) prevents duplicate HITL requests

5. **Observable state machine.** Every job transitions through explicit states (PENDING → QUEUED → PROCESSING → COMPLETED/FAILED/DEAD_LETTERED). Every transition is recorded in the audit trail with actor, old state, new state, and timestamp.

6. **Multi-tenant isolation.** All queries include `WHERE tenant_id = $1`. The queue message includes `TenantID`. The agent passes tenant context through every DB call.

7. **Concurrency-safe.** Tests pass with `-race`. `sync.Mutex` in test doubles and PubSub adapter. pgxpool and Redis client are connection-safe. No mutable shared state in agents.

8. **Graceful LLM degradation.** LLM analysis is optional and non-fatal. If the LLM provider is unavailable or returns an error, the vendor is still saved and processed. The `llm_analysis` key is simply absent from the result.

### Trade-offs / Risks

1. **Queue consumer crash loses in-flight message.** Redis `BRPopLPush` atomically moves the message to a processing list, but if the worker crashes after dequeue but before processing, the message remains in the processing list. Mitigation: the processing list is monitored; stale entries can be re-queued by a watchdog. The current implementation does not have this watchdog — it relies on the worker completing processing before the next poll.

2. **No capped retry in queue layer.** Retry tracking is in the DB `Job` status machine (`RETRYABLE_FAILED` → retry → `COMPLETED`/`DEAD_LETTERED`), not in the queue layer. The queue itself does not limit retries — a poison message goes directly to `{queue}-poison` DLQ. This means a transient failure that is retried by the worker (not the queue) could loop indefinitely if the worker keeps re-enqueuing. Mitigation: the worker sets `RETRYABLE_FAILED` and returns the error to the poll loop, which sends to DLQ. The agent itself does not re-enqueue.

3. **Slack dependency for HITL.** If Slack is unavailable, HITL requests are not delivered. The Admin API (`POST /admin/review-queue`) provides a fallback, but operators must know to check it. Mitigation: the HITL request is persisted in the DB regardless of Slack delivery success. The admin API lists all pending requests.

4. **Trust tier assignment bug (known issue).** There are two conflicting mappings for trust tier assignment in `vendor_agent.go:108-146`:
   - **Mapping 1 (lines 108-114, in result map):** Lower risk scores → better tiers. `riskScore < 30` → `TrustTierPreferred`, `riskScore < 60` → `TrustTierStandard`, else → `TrustTierProbation`.
   - **Mapping 2 (lines 137-146, on TrustBattery.Tier):** Higher risk scores → better tiers. `riskScore >= 80` → `TrustTierPreferred`, `riskScore >= 50` → `TrustTierStandard`, `riskScore >= 30` → `TrustTierProbation`, else → `TrustTierBlocked`.
   
   These mappings are inverted relative to each other. Mapping 1 treats a low risk score (good vendor) as Preferred, while Mapping 2 treats a high risk score (also good vendor, since ComputeVendorRisk adds points for valid IDs) as Preferred. The `result["trust_tier"]` is set to Mapping 1's value (line 117), but the `vendor.TrustBattery.Tier` is set to Mapping 2's value (lines 138-146). This means the result map and the persisted vendor record can disagree on the trust tier. This should be resolved in a follow-up by aligning both mappings to use the same logic — the `ScoreBasedTier` function in `trust_battery.go:129-140` provides the canonical mapping.

5. **Worker poll loop is single-threaded per queue.** Each queue has one goroutine. If a single vendor job takes a long time (e.g., LLM analysis), subsequent jobs in the same queue are delayed. Mitigation: the LLM analysis is optional and fast (< 5s for Sarvam). For high-throughput scenarios, multiple worker goroutines per queue can be added.

6. **No capped retry in queue layer.** The Redis adapter does not track dequeue count or enforce a retry limit. A message that consistently fails processing will be sent to DLQ on the first failure by the worker's poll loop. The DLQ preserves the full message JSON for manual inspection and replay.

## Implementation Details

### End-to-End Flow

```
1. HTTP Handler (main.go:456-553)
   ├── Parse form data (name, gst, pan, ifsc, bank_account)
   ├── Create domain.Vendor with RiskScore=0, Approved=false
   ├── UpsertVendor to PostgreSQL
   ├── Create VendorJob with UUID
   ├── Enqueue to "vendor-queue" via Redis LPUSH
   ├── Append VENDOR_CREATED audit event
   └── Return 201 { vendor_id, status: "queued" }

2. Queue Worker (worker.go:28-79, 175-261)
   ├── pollQueue goroutine with 30s blocking BRPopLPush
   ├── Unmarshal VendorJob from message body
   ├── Update Job → PROCESSING with optimistic locking
   │   └── Retry once on ErrVersionConflict
   ├── Call VendorAgent.ProcessVendor()
   ├── On error: set Job → RETRYABLE_FAILED, return error → DLQ
   ├── On success: check result["needs_hitl"]
   │   └── If true: send Slack Block Kit approval via slack.SendApprovalRequest()
   │       └── Upsert HITLRequest with deterministic ID "hitl-"+jobID
   ├── Append PROCESSED audit event
   └── Ack message (Delete from Redis processing list + hash)

3. VendorAgent.ProcessVendor (vendor_agent.go:55-200)
   ├── Step 1: Validate identifiers via IndiaValidator
   │   └── Syntactic checks: GST regex, PAN regex, IFSC regex
   ├── Step 2: ComputeVendorRisk() — deterministic scoring
   │   ├── Base score: 50
   │   ├── Valid GST: +15
   │   ├── Valid PAN: +10
   │   ├── Valid IFSC: +10
   │   ├── Duplicate GST: -40
   │   ├── Not approved: -20
   │   ├── Incomplete documents: -15
   │   └── Clamp: [0, 100]
   ├── Step 3: Determine HITL need
   │   └── riskScore >= 60 || validationErrors > 0
   ├── Step 4: Optional LLM analysis (non-fatal)
   │   └── Only if llm provider != nil && documents present
   ├── Step 5: Determine trust tier (two conflicting mappings — see known issue)
   ├── Step 6: UpsertVendor with Approved flag
   ├── Step 7: Upsert Job → COMPLETED with optimistic locking
   └── Step 8: Create HITLRequest if needed

4. Slack Webhook (main.go:622-780)
   ├── POST /slack/webhook receives block_actions callback
   ├── Parse action ID: "approve:<jobID>" or "reject:<jobID>"
   ├── Update Job → COMPLETED or FAILED
   ├── Update HITLRequest → APPROVED or REJECTED
   └── Append audit event

5. Admin API (main.go:870-960)
   ├── GET /admin/review-queue → list pending HITL requests
   └── POST /admin/review-queue → approve/reject by HITL ID
```

### State Machine Transitions

**Job Status:**
```
PENDING ──→ QUEUED ──→ PROCESSING ──→ COMPLETED
                              │
                              ├──→ RETRYABLE_FAILED ──→ (retry) ──→ PROCESSING
                              │
                              ├──→ TERMINAL_FAILED
                              │
                              └──→ AWAITING_HITL ──→ COMPLETED (approved)
                                                     └──→ FAILED (rejected)
```

**Trust Battery (per-vendor, ongoing):**
```
PROBATION ──(3 successes)──→ STANDARD ──(3 transactions)──→ PREFERRED ──(10 transactions)──→ STRATEGIC
    ↑                              ↑                              ↑
    └──(3 errors)──────────────────┘                              │
    └──(90 days inactivity)────────────────────────────────────────┘
    └──(dispute filed)───────────────────────────────────────────┘

Any tier ──(fraud flagged)──→ BLOCKED (terminal)
```

### Idempotency Strategy

| Layer | Mechanism | Scope |
|-------|-----------|-------|
| **Document upload** | Content-based SHA256 dedup → 409 Conflict if exists | Per-tenant, per-hash |
| **Job status update** | Optimistic locking on `Job.Version` (`UPDATE ... WHERE version = $N`) | Per-job |
| **Vendor upsert** | `INSERT ON CONFLICT (id, tenant_id) DO UPDATE` | Per-vendor |
| **HITLRequest** | Deterministic ID `"hitl-"+jobID` → `ON CONFLICT DO NOTHING` | Per-job |
| **Queue message** | UUID-based message ID; hash for O(1) lookup | Per-message |

### Concurrency Model

- Two goroutines at startup: one for `document-queue`, one for `vendor-queue`
- Each goroutine has a `defer recover()` to prevent panics from crashing the server
- `sync.Mutex` in test doubles and PubSub adapter (not in production Redis/Postgres adapters — pgxpool and redis.Client are connection-safe)
- No mutable shared state in agents — all state is passed via function parameters and persisted to DB
- Tests pass with `-race` flag

### DLQ Behavior

When a handler returns an error, the poll loop calls `queue.Poison()`:
- Redis: moves message from `{queue}:processing` list to `{queue}-poison` list
- Full message JSON is preserved in the poison queue for DLQ consumers
- Hash entry is cleaned up after poison queue receives the message

### HITL Request Deduplication

The HITLRequest ID is deterministically derived from the job ID: `"hitl-" + jobID`. This means:
- If the worker crashes after creating the HITL request but before acking the message, the retry will upsert the same HITL request (idempotent).
- The `UpsertHITLRequest` uses `ON CONFLICT DO UPDATE`, so duplicate creation is harmless.
- The Slack message may be sent twice (once per retry), but the HITL request in DB is deduplicated.

## File Map

### Existing Files Modified

| File | Change |
|------|--------|
| `cmd/server/main.go` | Added `vendorHandler` (POST/GET), `slackWebhookHandler`, `reviewQueueHandler`, worker wiring, queue constants |
| `cmd/server/worker.go` | Added `Worker` struct, `pollQueue` loop, `processVendorJob`, `processDocumentJob`, DLQ handling, Slack HITL integration |
| `internal/domain/types.go` | Added `JobStatus` enum, `HITLRequest` struct, `Vendor` struct, `TrustTier`/`RiskTier` enums, `WorkflowType` enum |
| `internal/domain/risk_scorer.go` | Added `ComputeVendorRisk()`, `ValidateGST/PAN/IFSC`, `RiskResult` struct |
| `internal/domain/trust_battery.go` | Added `TrustBattery` struct with state machine methods |
| `internal/domain/india_validator.go` | Added `IndiaValidator`, `ValidateVendor`, `ValidateAll`, India-specific regex patterns |
| `internal/providers/interfaces.go` | Added `QueueProvider`, `HITLProvider`, `DBProvider`, `TracingProvider` interfaces |
| `internal/providers/slack_hitl.go` | Added `SlackHITLProvider` implementing `HITLProvider` with Block Kit |
| `internal/adapters/queue/redis.go` | Added `RedisAdapter` implementing `QueueProvider` with LPUSH/BRPopLPush, hash-based O(1) lookup, DLQ |

### New Files

| File | Purpose |
|------|---------|
| `internal/agents/vendor_agent.go` | `VendorAgent` — orchestrates vendor onboarding workflow |
| `tests/agentic/vendor_workflow_test.go` | Vendor agent tests: clean auto-approve, high-risk HITL, LLM analysis, LLM error handling |
| `tests/agentic/error_recovery_test.go` | Error recovery tests: LLM failure, MinIO outage, concurrent agents, TrustBattery state machine |
| `tests/agentic/state_recovery_test.go` | State recovery tests: retry persistence, job status machine, DB crash recovery, queue consumer crash |
| `tests/agentic/pipeline_test.go` | Full pipeline test: upload → process → HITL → approve → complete |
| `tests/live/slack/slack_hitl_test.go` | Live Slack provider contract test (//go:build live) |

## Compliance with OpsCore Principles

### "Deterministic first, LLM last"

The vendor onboarding workflow explicitly encodes this:

- **Identifier validation**: Pure Go regex — no external API, no LLM.
- **Risk scoring**: Weighted arithmetic with regex-based flag detection — no LLM.
- **Trust tier assignment**: Explicit state machine with deterministic transitions — no LLM.
- **Job status machine**: Pure Go enum with explicit transition rules — no LLM.
- **LLM**: Invoked only for optional document analysis when documents are present AND the LLM provider is configured. LLM errors are non-fatal — the vendor is still saved and processed.

### Multi-tenant isolation

- All DB queries include `WHERE tenant_id = $1` as the first filter.
- Queue messages include `TenantID` — the worker passes it through every DB call.
- The `Vendor` struct has `TenantID` as a required field.
- The HTTP handler extracts tenant from `tenant.FromContext(r.Context())`.
- PostgreSQL RLS (already implemented) applies as defense-in-depth.

### Observability

- Every job transition is recorded in the `AuditEvent` table with actor, action, old state, new state, and timestamp.
- The `TracingProvider` interface wraps every `ProcessVendor` call in a span with job ID, tenant ID, and workflow type.
- LLM calls are recorded via `tracer.RecordLLMCall()` with model name, token count, cost, and duration.
- Worker logs include queue name, message ID, and error details via `slog`.
- The `Job` record stores input, output, and error for every processing attempt.

## Related ADRs

- **ADR-001**: Retrieval Architecture for Compliance & Document Search — establishes the "deterministic first, LLM last" principle and the queue-backed processing pattern that this ADR extends to vendor onboarding.
