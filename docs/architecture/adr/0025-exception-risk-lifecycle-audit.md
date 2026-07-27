# ADR-0025: Exception Case Lifecycle, Risk, and Audit

## Status

**Proposed** — exception lifecycle + audit transition are implemented and green on branch
`feat/manufacturing-pivot-persistence`; the risk model below is the v1 decision.

## Date

2026-07-18

## Context

Exception cases are the control-tower's unit of operator attention. The pivot must define:

1. The allowed lifecycle transitions for an `ExceptionCase`.
2. How risk/severity is derived (deterministically, not via LLM).
3. The mandatory audit trail on every state change.

## Decision

### ExceptionCase state machine

Constants in `internal/domain/mismatch.go`: `ExceptionStatusOpen` (OPEN),
`ExceptionStatusAck` (ACKNOWLEDGED), `ExceptionStatusResolved` (RESOLVED),
`ExceptionStatusDismissed` (DISMISSED).

- The v1 API allows transitions to any of `ACKNOWLEDGED`, `RESOLVED`, `DISMISSED` from OPEN
  via `POST /exceptions/{id}/resolve` (`resolveExceptionHandler`, `cmd/server/main.go:1233`).
- Transition enforcement: the request body `status` must be one of the three allowed target
  states; any other value returns 400. Tenant isolation is enforced by `GetExceptionCaseByID`
  (returns not-found for other tenants → 404, and the tenant middleware returns 403 for a
  cross-tenant `X-Tenant-ID`).
- No explicit ordering constraint between the three target states in v1 (any OPEN case may be
  acknowledged, resolved, or dismissed directly). This is intentional simplicity; a stricter
  OPEN→ACKNOWLEDGED→RESOLVED ordering can be added behind a follow-up ADR if operators need it.

### Risk / severity derivation

- `ExceptionCase.Severity` (`HIGH`/`MEDIUM`/`LOW`) is assigned **deterministically** by the
  mismatch engine at detection time (e.g. payment-affecting variances and vendor conflicts are
  HIGH). No LLM scores severity.
- Risk is derived **from `exception_cases`**, not from a separate `risk_signals` table in v1.
  `GET /ops/summary` reports `open_exceptions` and `open_exceptions_by_severity` directly from
  `ListExceptionCases(status=OPEN)`. There is no independent risk entity in v1.

### Audit on transition

Every successful transition appends an `AuditEvent`:

```
{ TenantID, Actor, Action: "EXCEPTION_STATUS_CHANGED",
  TargetType: "exception_case", TargetID: id,
  OldState: <prior status>, NewState: <target status>,
  Timestamp }
```

`Actor` resolves from the authenticated principal (falls back to `"api"`). The audit write is
best-effort (logged, not failing the transition).

## Alternatives Considered

1. **LLM-assigned severity.** Rejected per ADR-0006 (deterministic-first); severity is a
   business rule, not a judgment call.
2. **Separate `risk_signals` table + scoring job.** Rejected for v1: adds a table and a
   scheduler with no new capability over querying `exception_cases`. Revisit if shortage/delay
   predictive risk is built (see PRD "Future / Planned").
3. **Strict OPEN→ACK→RESOLVED ordering.** Deferred: adds operator friction; not required for
   v1 control-tower utility.

## Consequences

- Operators get a deterministic, auditable exception lifecycle.
- `GET /ops/summary` risk view is a direct query, cheap and correct.
- Audit trail satisfies the "audit every state transition" guardrail (ADR-0005).

## Related ADRs

- **ADR-0005**: Audit Trail and Usage Metering — `AppendAuditEvent` contract.
- **ADR-0006**: Deterministic-First AI Policy — no LLM in severity/state.
- **ADR-0023**: ExceptionCase persistence (table + `UpdateExceptionCaseStatus`).
- **ADR-0026**: Transitional Bridge Retirement — exception cases are the v1 risk source of truth.
