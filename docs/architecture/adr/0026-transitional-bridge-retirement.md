# ADR-0026: Transitional Bridge Retirement (`jobs.extracted_data`)

## Status

**Proposed** — retirement is pending verification of the table-backed read path (PHASE 4).

## Date

2026-07-18

## Context

During Phases 1.1–1.3, normalized manufacturing entities (PO/GRN/Invoice/ExceptionCase) were
persisted only inside `jobs.extracted_data` (a JSONB column on `jobs`) as a **transitional
bridge**. ADR-0023 established first-class tables and retained the bridge as a
read/write compatibility layer until table-backed reads are verified green.

The bridge has known limitations (no line-grain RLS, no optimistic locking, unindexed query,
no entity-scoped audit). Continuing to depend on it undermines the control-tower goals.

## Decision

Retire `jobs.extracted_data` as the primary signal store once the criteria below are met.

### Retirement criteria (all must hold)

1. **Table-backed writes green.** `agents.PersistSignalResult` writes PO/GRN/Invoice/
   ExceptionCase to the dedicated tables on every signal path — implemented and covered by
   `tests/agentic/signal_persistence_test.go`.
2. **Table-backed reads green.** `GET /signals/{id}`, `GET /exceptions`, `GET /exceptions/{id}`,
   `GET /ops/summary` serve exclusively from the manufacturing tables — implemented and covered
   by `tests/integration/signals_read_test.go`.
3. **Read parity check.** For a representative seed set, the table-backed read model returns
   equivalent data to what the bridge previously exposed (regression test added in PHASE 7).

### Retirement plan

- **Step 1 (this ADR accepted):** Add a regression test asserting reads come from tables, not
  the bridge.
- **Step 2:** Stop writing `extracted_data` for new signals in `processSignalJob` (keep the job
  record's lightweight traceability summary only if needed for the worker's own bookkeeping —
  explicitly separate from entity state).
- **Step 3:** Remove reader code paths that fall back to `extracted_data`; fail loudly if a
  read cannot be satisfied by the tables.
- **Step 4:** Drop the `extracted_data` column in a follow-up migration after a soak period,
  with a backup retained per ADR-0018/0019 retention posture.

### What is NOT retired

The `jobs` table and `AuditEvent` tables remain. Only the `extracted_data` column's role as a
signal store is retired; it may persist (emptied) for backward compatibility during soak.

## Alternatives Considered

1. **Keep the bridge indefinitely as a cache.** Rejected: dual-write drift risk and the
   limitations above; the tables are the source of truth.
2. **Big-bang drop.** Rejected: no parity window; a soak period de-risks the cutover.

## Consequences

- Single source of truth for manufacturing state (the tables).
- Line-grain RLS, optimistic locking, and entity-scoped audit fully realized.
- Removal of dual-write maintenance burden.

## Related ADRs

- **ADR-0023**: Manufacturing Entity Persistence Model — the tables that replace the bridge.
- **ADR-0011**: Queue Semantics — idempotent signal writes make re-migration safe.
- **ADR-0018 / 0019**: Data Retention, PII — backup posture during the drop migration.
