# ADR-0011: Queue Semantics, Retries, Idempotency, and DLQ

## Status

**Accepted** — implemented and deployed.

## Date

2026-07-13

## Context

### Problem Statement

OpsCore processes vendor onboarding and document ingestion asynchronously using queue-backed workers. These workloads have distinct reliability requirements:

1. **Vendor onboarding** — each vendor must be evaluated deterministically (identifier validation, risk scoring, trust tier assignment) with optional LLM document analysis and potential human-in-the-loop approval. The workflow involves multiple DB writes and external calls.

2. **Document ingestion** — uploaded documents must be OCR-extracted (via Sarvam AI), optionally refined via LLM fallback, and persisted with extracted fields. Document payloads can be large and processing can take seconds.

3. **Crash resilience** — the worker process may crash mid-processing. In-flight work must not be lost. On restart, messages must be recoverable without duplicate side effects.

4. **Observability of failures** — operators must be able to inspect failed messages, understand why they failed, and replay them after remediation.

The system must satisfy these requirements:

1. **At-least-once delivery** — every enqueued message must be processed at least once. Duplicate deliveries are tolerated because handlers are idempotent.

2. **Idempotent processing** — processing the same message twice must produce the same result. No duplicate vendors, no duplicate documents, no duplicate HITL requests.

3. **Lease semantics** — when a worker dequeues a message, it must hold an exclusive lease. If the worker crashes, the lease must expire and the message must be recoverable.

4. **Dead-letter queue** — messages that consistently fail processing must be isolated for inspection. The full original payload must be preserved.

5. **Two backends** — development and CI use Redis (zero ops overhead). Production may use Google Cloud Pub/Sub (managed, auto-scaling). Both must be interchangeable behind a common interface.

6. **Concurrent processing** — document and vendor queues must be processed concurrently without shared mutable state.

### Current Architecture

Before this decision, there was no queue infrastructure. Vendor onboarding was synchronous (`POST /vendors` → create DB record → return 201`). Document ingestion was handled inline during the HTTP request. There was no retry, no DLQ, no idempotency.

## Decision

Queue-backed asynchronous processing with Redis lists as the primary backend, Google Cloud Pub/Sub as the production alternative, a common `providers.QueueProvider` interface, three-tier idempotency, and DB-level retry state management with a DLQ for unrecoverable failures.

### Architecture Overview

```
HTTP Handler (POST /upload, POST /vendors)
    │
    ▼
┌──────────────────────────────┐
│  providers.QueueProvider      │  ← Interface: Enqueue / Dequeue / Delete / Poison
│  Enqueue(ctx, queue, msg)     │     Returns message ID immediately
└──────────┬───────────────────┘
           │
           ▼
┌──────────────────────────────┐
│  Queue Layer                  │
│  ┌────────────────────┐      │
│  │ RedisAdapter        │      │  ← LPUSH → BRPopLPush (30s blocking)
│  │ (default)           │      │     Hash {queue}:messages for O(1) lookup
│  │                     │      │     Processing list {queue}:processing
│  │                     │      │     DLQ {queue}-poison
│  └────────────────────┘      │
│  ┌────────────────────┐      │
│  │ PubSubAdapter       │      │  ← Google Cloud Pub/Sub
│  │ (production opt.)   │      │     Auto-created topics/subscriptions
│  │                     │      │     Explicit Ack/Nack semantics
│  │                     │      │     Dead-letter topic via Nack
│  └────────────────────┘      │
└──────────┬───────────────────┘
           │
           ▼
┌──────────────────────────────┐
│  Worker (pollQueue goroutine) │  ← Two goroutines: document-queue, vendor-queue
│                               │     30s blocking Dequeue via BRPopLPush
│                               │     Panic recovery (defer recover())
│                               │     On error → Poison (DLQ)
│                               │     On success → Delete (Ack)
└──────────┬───────────────────┘
           │
           ▼
┌──────────────────────────────┐
│  Agent Layer                  │
│  VendorAgent.ProcessVendor()  │  ← Deterministic validation + scoring
│  DocumentAgent.ProcessDoc()   │     Optional LLM (non-fatal)
│                               │     DB writes with optimistic locking
│                               │     Audit trail on every transition
└──────────────────────────────┘
```

### Queue Provider Interface

The `providers.QueueProvider` interface defines four operations:

```go
type QueueProvider interface {
    Enqueue(ctx context.Context, queueName string, message any) (string, error)
    Dequeue(ctx context.Context, queueName string) (*QueueMessage, error)
    Delete(ctx context.Context, queueName, messageID string) error
    Poison(ctx context.Context, queueName, messageID string) error
}
```

- `Enqueue` serializes the message, returns a unique message ID.
- `Dequeue` blocks up to 30 seconds, returns `nil` on timeout. The returned message carries an ID and a `DequeueCount`.
- `Delete` acknowledges successful processing — removes the message from the queue.
- `Poison` marks the message as failed — moves it to the DLQ.

`QueueMessage` carries the ID, body (raw JSON string), and dequeue count for observability:

```go
type QueueMessage struct {
    ID           string `json:"id"`
    Body         string `json:"body"`
    DequeueCount int    `json:"dequeue_count"`
}
```

This interface is the **sole abstraction** between the worker/agent layer and the queue backend. No worker code references Redis or Pub/Sub directly.

### Redis Queue Adapter

The `RedisAdapter` (in `internal/adapters/queue/redis.go`) uses three Redis data structures per queue:

| Structure | Key Pattern | Purpose |
|-----------|-------------|---------|
| **List (main queue)** | `{queueName}` | LPUSH (enqueue), BRPopLPush (dequeue) |
| **Hash** | `{queueName}:messages` | Full message JSON stored by message ID for O(1) Delete/Poison |
| **List (processing)** | `{queueName}:processing` | In-flight messages moved atomically by BRPopLPush |
| **List (DLQ)** | `{queueName}-poison` | Poison messages with full JSON preserved |

**Enqueue flow:**
1. Generate UUID message ID.
2. Marshal message body into a wrapper `{"id": "<uuid>", "body": <raw JSON>}`.
3. Store wrapper in `{queue}:messages` hash keyed by message ID.
4. **Push only the message ID** to the main queue list via `LPUSH`.

Storing the body in a hash and pushing only the ID to the list means `Delete` and `Poison` are O(1) hash lookups rather than O(n) list scans.

**Dequeue flow (atomic lease):**
1. `BRPopLPush` with 30-second timeout atomically pops from the main queue and pushes to `{queue}:processing`.
2. Look up the full message from the `{queue}:messages` hash by the popped ID.
3. If the hash entry is missing (e.g., stale data), return a minimal message with just the ID — the worker can handle it.

`BRPopLPush` provides **atomic lease semantics**: the message is removed from the main queue and placed in the processing list in a single atomic operation. If the worker crashes, the message remains in the processing list — it is not lost, and it is not re-queued by another worker until a watchdog explicitly moves it.

**Delete (Ack) flow:**
1. Remove message ID from `{queue}:processing` via `LRem` (the processing list is typically small — only in-flight messages).
2. Fall back to `{queue}` main list if not found in processing list (message may not have been dequeued yet).
3. Remove hash entry regardless.

**Poison flow:**
1. Look up full message JSON from `{queue}:messages` hash.
2. Remove message ID from `{queue}:processing` via `LRem`.
3. Push full message JSON to `{queue}-poison` list — DLQ consumers see the complete payload.
4. Remove hash entry last — the poison queue entry is independent.

**Migration support:** The `Dequeue` method handles both old-format messages (full JSON wrappers in the list, pre-hash migration) and new-format messages (ID-only in the list). Old-format messages are transparently upgraded by storing them in the hash on first dequeue.

### Google Cloud Pub/Sub Adapter

The `PubSubAdapter` (in `internal/adapters/queue/pubsub.go`) wraps the Google Cloud Pub/Sub SDK behind the same `providers.QueueProvider` interface.

| Operation | Pub/Sub Mapping |
|-----------|-----------------|
| **Enqueue** | `topic.Publish()` — topic is auto-created if it does not exist |
| **Dequeue** | `subscription.Receive()` with `maxMessages=1` — subscription auto-created if it does not exist, attached to the topic |
| **Delete** | `m.Ack()` — explicit acknowledgement |
| **Poison** | `m.Nack()` — negative acknowledgement triggers redelivery or dead-letter |

**Topic/subscription naming:** The queue name maps to a Pub/Sub topic. Subscriptions are named `{queueName}-sub` and are auto-created on first `Dequeue`.

**Message handles:** The adapter stores ack/nack closures in a `sync.Mutex`-protected map (`msgRefs`). When the worker calls `Delete` or `Poison`, the adapter looks up the handle by message ID, invokes the ack or nack, and removes the entry. Orphaned handles (from cancelled contexts) are nacked and drained at the end of every `Receive` call.

**Concurrency safety:** The `msgRefs` map is protected by `sync.Mutex`. The `Receive` callback acquires the mutex before appending to the message list and storing the handle. Tests pass with `-race`.

### DLQ Behavior

When a handler returns an error, the worker's `pollQueue` loop calls `queue.Poison()`:

- **Redis:** moves the message from `{queue}:processing` to `{queue}-poison`. The full JSON wrapper (including original body) is preserved. The hash entry is cleaned up.
- **Pub/Sub:** calls `Nack()`, which tells Pub/Sub that the message was not processed. Pub/Sub's subscription dead-letter policy determines how many redeliveries occur before the message is moved to a dead-letter topic.

After poisoning, the worker continues polling — poisoning does not block the queue.

### Idempotency Strategy

Three layers protect against duplicate processing:

| Layer | Mechanism | Scope | File |
|-------|-----------|-------|------|
| **Content-based dedup** | SHA256 hash of document content → `409 Conflict` on duplicate | Per-tenant, per-document | `findBySHA256` in `DBProvider` |
| **Optimistic locking** | `UPDATE ... WHERE version = $N` on `Job.Version` | Per-job | `postgres.ErrVersionConflict` handled in `worker.go:89-104` |
| **Deterministic HITL ID** | HITLRequest ID = `"hitl-"+jobID` → `ON CONFLICT DO UPDATE` | Per-job HITL request | `worker.go:155, 235-237` |
| **Vendor upsert** | `INSERT ON CONFLICT (id, tenant_id) DO UPDATE` | Per-vendor | `DBProvider.UpsertVendor` |
| **Queue message** | UUID-based message ID per `Enqueue` call; hash for O(1) lookup | Per-message | `RedisAdapter.Enqueue` |

**Content-based SHA256 dedup (documents):** Before uploading a document, the handler computes the SHA256 hash of the file content. If a document with the same hash already exists for the tenant, a `409 Conflict` is returned. This prevents duplicate document ingestion at the HTTP boundary.

**Optimistic locking on Job.Version:** Every job status update uses an optimistic lock:
```go
dbJob := &domain.Job{
    ID:       job.JobID,
    TenantID: job.TenantID,
    Status:   domain.JobStatusProcessing,
    Version:  existingJob.Version,  // from GetJob
}
if err := w.db.UpsertJob(ctx, dbJob); err != nil {
    if errors.Is(err, postgres.ErrVersionConflict) {
        // Retry once: re-fetch version and try again
        existing, _ := w.db.GetJob(ctx, job.JobID, job.TenantID)
        dbJob.Version = existing.Version
        return w.db.UpsertJob(ctx, dbJob)
    }
}
```

The `UpsertJob` implementation uses `UPDATE jobs SET status = $1, version = version + 1, ... WHERE id = $2 AND tenant_id = $3 AND version = $4`. If two workers attempt to update the same job concurrently, exactly one succeeds — the other gets `ErrVersionConflict`.

**Deterministic HITLRequest ID:** The HITL request ID is derived from the job ID: `"hitl-" + jobID`. If the worker crashes after creating the HITL request but before acking the message, the retry will upsert the same HITL request (idempotent). The Slack message may be sent twice, but the DB state is safe.

**Upsert semantics everywhere:** All DB write operations use `UPSERT` (`INSERT ON CONFLICT DO UPDATE`). Replaying a completed job is safe because the upsert will overwrite with the same values.

### Job Status Machine and Retry

Retries are managed at the **DB level**, not the queue layer. The queue layer does not cap retries.

**Job status enum:**
```
PENDING ──→ QUEUED ──→ PROCESSING ──→ COMPLETED
                              │
                              ├──→ RETRYABLE_FAILED ──→ (retry) ──→ PROCESSING
                              │
                              ├──→ TERMINAL_FAILED
                              │
                              ├──→ DEAD_LETTERED
                              │
                              └──→ AWAITING_HITL ──→ COMPLETED (approved)
                                                     └──→ FAILED (rejected)
```

```go
type JobStatus string

const (
    JobStatusPending         JobStatus = "PENDING"
    JobStatusQueued          JobStatus = "QUEUED"
    JobStatusProcessing      JobStatus = "PROCESSING"
    JobStatusCompleted       JobStatus = "COMPLETED"
    JobStatusFailed          JobStatus = "FAILED"           // legacy - treat as terminal
    JobStatusRetryableFailed JobStatus = "RETRYABLE_FAILED" // can retry
    JobStatusTerminalFailed  JobStatus = "TERMINAL_FAILED"  // won't retry
    JobStatusDeadLettered    JobStatus = "DEAD_LETTERED"    // exceeded max retries
    JobStatusAwaitingHITL    JobStatus = "AWAITING_HITL"
)
```

**Helper methods:**
- `IsRetryable()` — returns `true` for `RETRYABLE_FAILED`
- `IsTerminal()` — returns `true` for `COMPLETED`, `FAILED`, `TERMINAL_FAILED`, `DEAD_LETTERED`
- `IsFailed()` — returns `true` for `FAILED`, `RETRYABLE_FAILED`, `TERMINAL_FAILED`, `DEAD_LETTERED`

**When a handler succeeds:**
- Worker calls `queue.Delete()` (Ack)
- Agent sets `JobStatus.COMPLETED` via `UpsertJob`
- Audit event appended: `PROCESSED`

**When a handler returns an error:**
- Worker calls `queue.Poison()` (moves to DLQ)
- Agent sets `JobStatus.RETRYABLE_FAILED` with error message
- Audit event appended: `PROCESSING_FAILED` with old state, new state, and error

**Retry flow (external to queue layer):**
- A scheduler or operator action polls for jobs with `RETRYABLE_FAILED` status
- On retry, the job status is set back to `QUEUED` and a new message is enqueued
- The queue layer sees this as a fresh message
- If retries are exhausted, an operator (or automation) sets the job to `DEAD_LETTERED`

### Worker Architecture

Two goroutines run at startup, one per queue:

```go
func (w *Worker) Start(ctx context.Context) {
    go w.pollQueue(ctx, QueueDocument, w.processDocumentJob)
    go w.pollQueue(ctx, QueueVendor, w.processVendorJob)
    // QueueCompliance can be added later
}
```

**pollQueue loop:**
```
for {
    select {
    case <-ctx.Done(): return
    default:
    }

    msg, err := w.queue.Dequeue(ctx, queueName)  // 30s blocking
    if msg == nil { time.Sleep(1s); continue }

    func() {
        defer func() {
            if r := recover(); r != nil {
                err = fmt.Errorf("PANIC in %s handler: %v", queueName, r)
            }
        }()
        err = handler(ctx, msg.Body)
    }()

    if err != nil {
        queue.Poison(ctx, queueName, msg.ID)
    } else {
        queue.Delete(ctx, queueName, msg.ID)
    }
}
```

Characteristics:
- **30-second blocking dequeue** (BRPopLPush timeout) — no busy-polling
- **Panic recovery** — a `defer recover()` prevents a single handler panic from crashing the entire server
- **Single-threaded per queue** — each queue has one goroutine. If a vendor job takes a long time (LLM analysis), subsequent jobs in the vendor queue are delayed. Mitigation: LLM analysis is optional and fast (< 5s for Sarvam)
- **No shared mutable state** — agents receive all dependencies via constructor injection. Tests pass with `-race`

### Concurrency Model

| Component | Concurrency Guarantee | Mechanism |
|-----------|----------------------|-----------|
| Worker (Redis) | One goroutine per queue, no shared state | No locks needed |
| Worker (PubSub) | One Receive call per queue, mutex for msgRefs | `sync.Mutex` |
| Agents | No mutable state; all state via params and DB | Immutable by design |
| DB | pgxpool is connection-safe | No additional locks |
| Redis | redis.Client is connection-safe | No additional locks |

### HITL Request Deduplication

The HITLRequest ID is deterministically derived: `"hitl-" + jobID`. This ensures:

- If the worker crashes after creating the HITL request but before acking the message, the retry will upsert the same HITL request (idempotent).
- The `UpsertHITLRequest` uses `ON CONFLICT DO UPDATE`, so duplicate creation is harmless.
- The Slack message may be sent twice (once per retry), but the HITL request in DB is deduplicated.

### Specific Technology Choices

| Component | Choice | Rationale |
|-----------|--------|-----------|
| **Primary queue** | Redis lists (LPUSH/BRPopLPush) + hash for O(1) lookup | Already in stack, zero ops overhead, atomic lease semantics via BRPopLPush |
| **Production queue** | Google Cloud Pub/Sub (via `pubsubClient` interface) | Managed, auto-scaling, explicit Ack/Nack, dead-letter support |
| **DLQ (Redis)** | `{queue}-poison` list with full JSON preserved | Full payload available for DLQ consumers; hash entry cleaned after poison |
| **DLQ (Pub/Sub)** | Nack with subscription dead-letter policy | Pub/Sub manages redelivery and dead-lettering automatically |
| **Idempotency** | SHA256 dedup + optimistic locking + deterministic IDs + upsert | Three layers prevent duplicate side effects |
| **Retry tracking** | DB-level `JobStatus.RETRYABLE_FAILED` | Queue layer does not cap retries; retry is controlled by scheduler or operator |
| **Worker concurrency** | Two goroutines with panic recovery | Simple, testable, no lock contention |
| **Provider interface** | `providers.QueueProvider` | Both backends interchangeable; worker/agents never reference Redis or Pub/Sub directly |

## Alternatives Considered

### 1. Synchronous HTTP-Only Processing

Rejected because:
- Document OCR and LLM analysis can take 5-30 seconds, blocking the HTTP response.
- No resilience to worker crashes — in-flight work is lost on process restart.
- No retry mechanism — a transient OCR failure requires the client to resubmit.
- No DLQ — failed operations are invisible to operators.

### 2. Temporal / Cadence Workflow Engine

Rejected because:
- Adds significant infrastructure complexity (Temporal server, worker binaries, visibility store).
- The workflow (enqueue → process → ack/poison) is simple enough for a Redis queue + Go state machine.
- OpsCore's philosophy is to minimize external dependencies.
- The queue-backed approach with BRPopLPush + optimistic locking provides equivalent resilience guarantees for the current workload.

### 3. Apache Kafka

Rejected because:
- Kafka is designed for high-throughput event streaming, not job queues.
- Requires a separate Kafka cluster (ZooKeeper or KRaft).
- No native lease/dead-letter semantics — consumer offset management is more complex.
- Overkill for a system where messages are processed one-at-a-time per queue.

### 4. RabbitMQ

Rejected because:
- Not already in the stack (Redis is).
- AMQP semantics are more complex than Redis lists for the simple queue patterns needed.
- Would add operational overhead (Erlang runtime, management plugin).

### 5. LLM-Based Message Routing / Error Classification

Rejected because:
- Queue semantics are deterministic by nature. An LLM should not decide whether a message is poison or not.
- Error classification is rule-based (HTTP 5xx = retryable, 4xx = terminal, panic = terminal).
- LLM invocation for every failure would add latency and cost with no benefit.

## Consequences

### Benefits

1. **Resilient to worker crashes.** BRPopLPush atomically moves the message to a processing list. If the worker crashes mid-processing, the message remains in `{queue}:processing` — it is not lost and not silently re-queued. A watchdog can monitor stale processing entries.

2. **Idempotent by design.** Three layers prevent duplicate side effects:
   - Content-based SHA256 dedup before document upload (409 Conflict if exists).
   - Optimistic locking on `Job.Version` (`UPDATE ... WHERE version = $N`).
   - `INSERT ON CONFLICT DO UPDATE` for all upserts.
   - Deterministic HITLRequest.ID (`"hitl-"+jobID`) prevents duplicate HITL requests.

3. **Observable failures.** Every failed message goes to a named DLQ (`{queue}-poison`) with full JSON preserved. The job record stores the error message. Audit events record every transition.

4. **Backend-agnostic worker.** `providers.QueueProvider` abstracts both Redis and Pub/Sub. Workers never reference queue infrastructure directly. Switching backends requires only wiring change in `cmd/server/main.go`.

5. **Two-queue concurrency.** Document and vendor queues are processed concurrently by separate goroutines. A slow vendor job does not block document ingestion.

6. **Panic-safe.** The `defer recover()` in `pollQueue` prevents any single handler panic from crashing the server. The panic is logged and the message is poisoned.

7. **Migration-safe.** The Redis adapter handles both old-format (full JSON in list) and new-format (ID-only in list) messages transparently.

### Trade-offs / Risks

1. **BRPopLPush crash window.** If the worker crashes after `BRPopLPush` but before the handler starts, the message remains in the `{queue}:processing` list. No other worker can dequeue it because `BRPopLPush` removed it from the main queue. Mitigation: the processing list is monitored; stale entries can be re-queued by a watchdog. The current implementation does not have this watchdog — it relies on the worker completing processing before the next poll.

2. **No capped retry in queue layer.** Retry tracking is in the DB `Job` state machine (`RETRYABLE_FAILED` → retry → `COMPLETED`/`DEAD_LETTERED`), not in the queue layer. The queue itself does not limit retries — a poison message goes directly to `{queue}-poison` DLQ on the first failure from the worker's poll loop. This means a transient failure that is retried by the worker (not the queue) could loop indefinitely if the worker keeps re-enqueuing. Mitigation: the worker sets `RETRYABLE_FAILED` and returns the error to the poll loop, which sends to DLQ. The agent itself does not re-enqueue.

3. **Single-threaded per queue.** Each queue has one goroutine. If a single vendor job takes a long time (e.g., LLM analysis), subsequent jobs in the same queue are delayed. Mitigation: the LLM analysis is optional and fast (< 5s for Sarvam). For high-throughput scenarios, multiple worker goroutines per queue can be added.

4. **Redis DLQ is an unbounded list.** The `{queue}-poison` list grows without bound if failed messages are not consumed. Mitigation: DLQ monitoring should alert when poison queue depth exceeds a threshold. The DLQ should be consumed by an operator or automated replay job.

5. **Pub/Sub adapter stores message handles in memory.** The `msgRefs` map in `realPubSubClient` grows with each dequeue. If `Delete`/`Poison` is never called (worker crashes), handles leak until the next `Receive` call drains orphans. Mitigation: the `Subscribe` method drains orphaned handles after every `Receive` call.

## Implementation Details

### Queue Constants

```go
const (
    QueueDocument   = "document-queue"
    QueueVendor     = "vendor-queue"
    QueueCompliance = "compliance-queue"
)
```

### Redis Key Patterns

| Purpose | Key Pattern | Example |
|---------|-------------|---------|
| Main queue (list) | `{queueName}` | `document-queue` |
| Message hash | `{queueName}:messages` | `document-queue:messages` |
| Processing list | `{queueName}:processing` | `document-queue:processing` |
| Poison queue | `{queueName}-poison` | `document-queue-poison` |

### Provider Interface Check

```go
// Compile-time interface checks
var _ providers.QueueProvider = (*RedisAdapter)(nil)
var _ providers.QueueProvider = (*PubSubAdapter)(nil)
```

### Test Patterns

The PubSub adapter is tested with a mock `pubsubClient` interface:

```go
type mockPubSub struct {
    publishFunc   func(ctx context.Context, topic string, data []byte) (string, error)
    subscribeFunc func(ctx context.Context, subscription, topic string, maxMessages int) ([]*pubsubMessage, error)
    ackFunc       func(ctx context.Context, ackID string) error
    nackFunc      func(ctx context.Context, ackID string) error
}
```

Tests cover:
- `Enqueue` — verifies topic name and returned message ID
- `Dequeue` — verifies subscription name, topic name, message body, and dequeue count
- `Dequeue (empty)` — verifies nil returned for empty queue
- `Delete` — verifies ack ID is forwarded correctly
- `Poison` — verifies nack ID is forwarded correctly
- `Enqueue (error)` — verifies error propagation

### Optimistic Locking in Worker

```go
upsertWithVersion := func(j *domain.Job) error {
    existingJob, getErr := w.db.GetJob(ctx, job.JobID, job.TenantID)
    if getErr == nil {
        j.Version = existingJob.Version
    }
    if err := w.db.UpsertJob(ctx, j); err != nil {
        if errors.Is(err, postgres.ErrVersionConflict) {
            existingJob, getErr := w.db.GetJob(ctx, job.JobID, job.TenantID)
            if getErr == nil {
                j.Version = existingJob.Version
            }
            return w.db.UpsertJob(ctx, j)
        }
        return err
    }
    return nil
}
```

## File Map

### Existing Files Modified

| File | Change |
|------|--------|
| `cmd/server/main.go` | Added queue constants (QueueDocument, QueueVendor, QueueCompliance), queue wiring in `ServerDeps`, `Enqueue` calls in upload/vendor handlers |
| `cmd/server/worker.go` | Added `Worker` struct, `pollQueue` loop, `processVendorJob`, `processDocumentJob`, DLQ handling, panic recovery, optimistic locking helper |
| `internal/providers/interfaces.go` | Added `QueueProvider` interface (Enqueue/Dequeue/Delete/Poison), `QueueMessage` struct |
| `internal/domain/types.go` | Added `JobStatus` enum with PENDING/QUEUED/PROCESSING/COMPLETED/RETRYABLE_FAILED/TERMINAL_FAILED/DEAD_LETTERED/AWAITING_HITL, `IsRetryable()`/`IsTerminal()`/`IsFailed()` helpers, `Job.Version` field |

### New Files

| File | Purpose |
|------|---------|
| `internal/adapters/queue/redis.go` | `RedisAdapter` — LPUSH/BRPopLPush, hash-based O(1) lookup, `{queue}:processing` lease, `{queue}-poison` DLQ, migration support for old-format messages |
| `internal/adapters/queue/pubsub.go` | `PubSubAdapter` — auto-created topics/subscriptions, explicit Ack/Nack, mutex-protected `msgRefs`, orphan drain |
| `internal/adapters/queue/pubsub_test.go` | PubSub adapter tests with `mockPubSub` — Enqueue, Dequeue, Dequeue (empty), Delete, Poison, Enqueue error |

## Compliance with OpsCore Principles

### "Deterministic first, LLM last"

- Queue semantics are entirely deterministic — LPUSH/BRPopLPush/ack/nack are pure Redis or Pub/Sub operations.
- Poisoning is rule-based: any error returned by the handler triggers Poison. No LLM is involved in deciding message fate.
- Retry decisions are DB-level state machine transitions — `RETRYABLE_FAILED` vs `TERMINAL_FAILED` is determined by the handler, not by an LLM.

### Multi-tenant isolation

- Queue messages include `TenantID` — the worker passes it through every DB call.
- All DB queries include `WHERE tenant_id = $1` as the first filter.
- The `Job` struct has `TenantID` as a required field.
- PostgreSQL RLS (already implemented) applies as defense-in-depth.

### Observability

- Every job transition is recorded in the `AuditEvent` table with actor, action, old state, new state, and timestamp.
- Worker logs include queue name, message ID, and error details via `slog`.
- The `Job` record stores input, output, error, and version for every processing attempt.
- Poison events are logged at error level with full message context.
- DLQ depth can be monitored via `LLEN {queue}-poison` for Redis, or subscription dead-letter metrics for Pub/Sub.

## Related ADRs

- **ADR-0002**: Vendor Onboarding — establishes the queue-backed processing pattern, `providers.QueueProvider` interface, worker architecture, and idempotency strategy that this ADR codifies and extends.
- **ADR-001**: Retrieval Architecture for Compliance & Document Search — references the queue-backed processing pattern for compliance document ingestion.
- **ADR-0012**: Observability and LLMOps — establishes the tracing and audit infrastructure that records every job transition described in this ADR.
