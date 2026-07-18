# ADR-0024: Signal Ingestion API Boundary

## Status

**Proposed** — companion to ADR-0023; the route and consumer are implemented and green on
branch `feat/manufacturing-pivot-persistence`.

## Date

2026-07-18

## Context

The manufacturing pivot needs a single, explicit ingestion boundary for operational
documents (PO/GRN/Invoice) distinct from the legacy generic `/upload` document-ingestion
endpoint. Two requirements collide:

1. Reuse the hardened `/upload` middleware chain (auth → tenant → rate-limit → usage →
   permission) so the new surface inherits security posture without re-implementation.
2. Route the payload into the **signal pipeline** (`QueueSignal` → `processSignalJob` →
   `agents.PersistSignalResult`) rather than the generic document pipeline, so the
   manufacturing tables are populated deterministically.

## Decision

`POST /signals/upload` is the sole HTTP ingestion boundary for manufacturing signals.

- It mirrors `/upload`'s middleware chain exactly, reusing `domain.PermissionDocumentUpload`
  and `domain.MetricDocumentsUploaded` (no new permission/metric symbols introduced).
- The handler (`signalsUploadHandler`, `cmd/server/main.go:389`) performs only: file I/O,
  SHA-256 dup detection (409 on duplicate), MinIO upload to the documents container, a
  `domain.Job` (WorkflowSignalIngestion) + `domain.Document` record, and enqueue of an
  `agents.SignalJob` to `QueueSignal`.
- It does **not** perform extraction, classification, mismatch detection, or persistence.
  Those run asynchronously in `processSignalJob` (worker) and `PersistSignalResult` (agents),
  keeping the handler thin per the hexagonal boundary rule.
- The legacy `/upload` endpoint remains the generic document-ingestion path and is not
  deprecated by this ADR.

## Alternatives Considered

1. **Reuse `/upload` for everything, tag signals via body field.** Rejected: conflates two
   pipelines, complicates middleware/usage accounting, and obscures the audit trail of a
   signal ingest vs a generic document ingest.
2. **Separate micro-service for signals.** Rejected: violates the single-binary `net/http`
   discipline and adds deployment/operational surface for no functional gain.

## Consequences

- Clear, observable ingestion boundary: every manufacturing doc enters via `/signals/upload`.
- Security posture inherited from `/upload` (no drift).
- Async decoupling: handler latency is bounded; heavy work happens in the worker.

## Related ADRs

- **ADR-0023**: Manufacturing Entity Persistence Model — where the signal lands.
- **ADR-0011**: Queue Semantics, Retries, Idempotency, DLQ — idempotent signal enqueue.
- **ADR-0006**: Deterministic-First AI Policy — OCR/LLM only inside the worker, never the handler.
