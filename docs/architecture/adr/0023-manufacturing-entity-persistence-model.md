# ADR-0023: Manufacturing Entity Persistence Model

## Status

**Accepted** — Finalized on branch `feat/manufacturing-pivot-persistence`. The decision is
no longer provisional: the migration v6 tables, the Postgres adapter methods, and the
integration test suite (`internal/adapters/postgres/manufacturing_test.go`, 7 integration
tests covering RLS isolation, optimistic-locking conflict detection, and PO/GRN/Invoice/
ExceptionCase round-trips) are **landed and green**. The `jobs.extracted_data` bridge remains
retained as a read/write compatibility layer until the Phase 4 read path is verified (see
ADR-0026 for retirement criteria).

## Date

2026-07-18 (opened) · 2026-07-18 (accepted)

## Context

### Problem Statement

Phase 1.1–1.3 delivered deterministic domain entities (`PurchaseOrder`, `GoodsReceipt`,
`Invoice`, `ExceptionCase`) plus the signal agent/worker wiring. At the `pivot-phase-1.3-complete`
checkpoint these normalized entities are persisted only in `jobs.extracted_data` — a JSONB
column on the `jobs` table used as a **transitional bridge**.

This bridge is insufficient for the control-tower goals of v6.0:

1. **No queryable structure.** Mismatch detection, read models (Phase 1.5), and ops
   endpoints (`GET /exceptions`, `GET /ops/summary`) need to filter, join, and aggregate
   PO/GRN/invoice lines by `tenant_id`, `document_no`, `vendor_gstin`, `status`, and
   `mismatch_type`. JSONB extraction in SQL is brittle and unindexed.
2. **No row-level tenant isolation.** RLS policies are defined per table; a JSONB blob
   inside `jobs` cannot be policy-scoped at the line-item grain.
3. **No optimistic concurrency.** The domain entities carry a `Version` field, but the
   bridge has no mechanism to detect concurrent writes — a requirement for safe
   multi-writer reconciliation.
4. **No first-class audit linkage.** Exception cases and entity updates need append-only
   `AuditEvent` rows tied to specific entity IDs, not buried in a job blob.

### Requirements

1. Persist PO/GRN/Invoice (header + lines) and `ExceptionCase` as first-class tables.
2. Every table is `tenant_id`-scoped and covered by PostgreSQL RLS.
3. Every table carries a `version` column used for optimistic locking on writes.
4. Storage goes through explicit, non-generic `DBProvider` methods (no generic
   `SaveBlob(table, json)` escape hatch).
5. The `jobs.extracted_data` bridge is retained until table-backed reads are verified green.

## Decision

### Primary Decision: First-Class Tables Over the `jobs.extracted_data` JSONB Bridge

Manufacturing entities are persisted in dedicated, explicitly-modeled tables rather than as
JSONB payloads inside `jobs`:

- `purchase_orders` + `purchase_order_lines`
- `goods_receipts` + `goods_receipt_lines`
- `invoices` + `invoice_lines`
- `exception_cases`

Each header table carries `id`, `tenant_id`, `version`, and timestamps; each line table
references its header and is likewise `tenant_id`-scoped. Writes flow exclusively through
explicit `DBProvider` methods already stubbed in `internal/providers/interfaces.go`
(`UpsertPurchaseOrder`, `GetPurchaseOrderByID`, `ListPurchaseOrders`, `UpsertGoodsReceipt`,
`UpsertInvoice`, `UpsertExceptionCase`, `UpdateExceptionCaseStatus`, etc.). These methods
perform optimistic-locking-aware upserts using the entity `Version` field and require
`tenant_id` for RLS correctness.

The transitional `jobs.extracted_data` bridge is **retained** as a read/write compatibility
layer until the table-backed read path (Phase 1.4A integration tests + Phase 1.5 read model)
is verified green, after which it may be deprecated behind a follow-up ADR.

## Alternatives Considered

### 1. Keep JSONB bridge, query via `jsonb_path_query`

Rejected because it cannot be RLS-scoped at line grain, cannot enforce optimistic locking,
and forces unindexed scans for the read-model and ops endpoints.

### 2. Generic `SaveEntity(table string, json []byte)` DBProvider method

Rejected because it bypasses compile-time safety, hides the schema contract behind strings,
and violates the explicit-interface discipline already established in the provider layer.
The pivot requires explicit, typed methods per entity.

### 3. EAV (entity-attribute-value) model for lines

Rejected because line attributes (qty, rate, tax) are fixed and typed; EAV adds join
explosion and loses column typing that the deterministic domain relies on.

## Consequences

### Benefits

1. **Queryable, indexable state.** Ops endpoints and the Phase 1.5 read model can filter
   and aggregate deterministically with proper indexes.
2. **Row-level tenant isolation.** RLS policies apply at the entity and line grain.
3. **Safe concurrency.** `version` optimistic locking prevents lost updates during
   concurrent reconciliation.
4. **Explicit, auditable contract.** Typed `DBProvider` methods keep the schema contract
   in code and make the persistence surface reviewable and testable.

### Trade-offs / Risks

1. **Migration + adapter surface area.** Four header tables, three line tables, and
   `exception_cases` must be migrated, adapted, and tested — the bulk of Phase 1.4A effort.
2. **Bridge dual-write window.** While the bridge is retained, writers must keep both paths
   consistent. Mitigation: the bridge is read-only fallback until table reads are green; no
   new logic should depend on it long-term.
3. **ADR finalization gated on green tests.** This record stays Proposed/In-Progress until
   the integration tests (RLS + optimistic locking + round-trip) pass; findings during
   implementation may refine the table/column design.

## Related ADRs

- **ADR-0003**: Multi-Tenancy and Tenant Isolation — RLS policy foundation for the new tables.
- **ADR-0005**: Audit Trail and Usage Metering — `AppendAuditEvent` linkage for entity changes.
- **ADR-0011**: Queue Semantics, Retries, Idempotency, and DLQ — idempotent signal writes.
- **ADR-0018 / 0019**: Data Retention, PII, and Document Security — retention posture for
  persisted manufacturing entities.
- **Phase 1.5 (read model)** and **Phase 1.6 (PRD/naming convergence)**: depend on this
  persistence model landing.
