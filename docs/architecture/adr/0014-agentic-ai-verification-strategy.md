# ADR-0014: Agentic AI Verification Strategy — Four-Layer Verification Framework for Agentic Workflows

## Status

**Proposed** — testing patterns exist in `tests/agentic/` but no formal verification framework codifies them.

## Date

2026-07-13

## Context

### Problem Statement

OpsCore uses agentic workflows for document ingestion, vendor onboarding, and compliance gap analysis. These agents combine deterministic domain logic (validation, risk scoring, state machines) with optional LLM calls for OCR refinement and compliance reasoning. While each agent is individually tested, there is no unified verification framework that guarantees agentic correctness across four critical dimensions:

1. **Schema enforcement** — LLM outputs are parsed into `map[string]any` without validation against explicit schemas. A malformed LLM response (missing fields, wrong types) can propagate silently into downstream domain logic.

2. **Groundedness validation** — The compliance agent's `AnalyzeCompliance` method sends all chunks to the LLM and returns the raw reasoning string. There is no check that LLM claims cite specific chunk IDs, document references, or section numbers. Ungrounded claims (hallucinations) can reach the operator without warning.

3. **State machine invariants** — While individual agents test allowed transitions (see `tests/agentic/states/state_machine_test.go`), there is no invariant check that *every* agentic workflow asserts transition legality before persisting. The `JobStatus` transitions in `domain/types.go:72-76` define `IsTerminal()` and `IsRetryable()` predicates, but agents do not call these guards before mutating status.

4. **Bounded execution** — Agent loops (LLM retries, compliance chunk iteration, document reprocessing) have implicit iteration counts but no explicit ceilings. A buggy LLM provider returning infinite `RETRYABLE_FAILED` statuses could loop until resource exhaustion without routing to DLQ or HITL escalation.

### Current Test Infrastructure

The existing test infrastructure in `tests/agentic/` provides a strong foundation but lacks a codified framework:

| Layer | Current State | Gap |
|-------|---------------|-----|
| **Testcontainers** | PostgreSQL 16, Redis 7, MinIO containers per test (see `tests/agentic/helpers.go:47-205` — `StartInfra`) | No verification-specific container setup (e.g., dedicated schema validation DB) |
| **Stub providers** | `StubOCR` and `StubLLM` in `tests/agentic/stubs.go` — trigger-word-based deterministic test doubles | No validation of LLM output schema — stubs always return well-formed responses |
| **Golden tests** | `tests/agentic/golden/golden_document_test.go` — table-driven expected-trajectory vs actual-trajectory comparison for document and vendor onboarding | No golden trajectories for compliance analysis; no schema validation in golden assertions |
| **Provider contract tests** | `tests/agentic/contracts/provider_contract_test.go` — verifies stub contracts (OCR result shape, LLM method routing, DB CRUD round-trips, QueueProvider enqueue/dequeue) | Contracts verify shape but not schema conformance of LLM outputs |
| **State machine tests** | `tests/agentic/states/state_machine_test.go` — pure-domain tests for `JobStatus` allowed/disallowed transitions, `IsRetryable`, `IsTerminal`, `TrustBattery` transitions, `HITLRequest` lifecycle, and optimistic locking | No invariant checks wired into agent workflows — tests exist in isolation |
| **Error recovery tests** | `tests/agentic/error_recovery_test.go` — verifies LLM failure is non-fatal (`TestStubLLMErrorDoesNotCrashAgent`), MinIO outage recovery, concurrent agent processing (`TestMultipleConcurrentAgents`, `TestMultipleConcurrentVendorAgents`), TrustBattery state machine recovery | No test for bounded-iteration enforcement (loops that exceed max iterations) |
| **Concurrent safety** | `TestMultipleConcurrentAgents` (5 concurrent DocumentAgents), `TestConcurrentWorkerCrashOneRecovers` (worker crash with concurrent recovery) | No concurrent schema validation testing |

### Requirements

1. All LLM outputs must conform to explicit Go struct schemas before they enter domain logic — malformed outputs must be rejected, not silently coerced.
2. LLM compliance analysis claims must cite specific chunk IDs, document references, or section numbers — unsupported claims must trigger HITL escalation, not silent acceptance.
3. Every agentic state transition must be guarded by an invariant check that verifies transition legality and idempotency.
4. All agent loops must have explicit maximum iteration ceilings — exceeded ceilings route to DLQ or HITL, not silent retry.
5. The framework must be easy to adopt incrementally — existing tests should require minimal restructuring.
6. The framework must be measurable — verification pass/fail rates, schema rejection rate, and groundedness score must be observable in CI.

## Decision

Implement a **four-layer agentic AI verification strategy** codified in a new `internal/domain/verification/` package with corresponding test patterns in `tests/agentic/verification/`.

### Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│  Agent Workflow (e.g., ComplianceAgent.AnalyzeCompliance)  │
│                                                             │
│  1. Call LLM with prompt                                     │
│  2. Receive raw string response                              │
│  3. Parse into typed struct (Step 1: Schema enforcement)     │
│  4. Validate groundedness (Step 2: Citation check)           │
│  5. Execute state transitions (Step 3: Invariant check)      │
│  6. Return result or escalate                                │
└──────────┬──────────────────────────────────────────────────┘
           │
           ▼
┌─────────────────────────────────────────────────────────────┐
│  Layer 1: Schema Enforcement                                 │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  LLM output → typed struct via json.Unmarshal        │   │
│  │  Missing fields → RejectOutput error                 │   │
│  │  Wrong types → RejectOutput error                    │   │
│  │  Unknown fields → warning (non-fatal)                │   │
│  └─────────────────────────────────────────────────────┘   │
└──────────┬──────────────────────────────────────────────────┘
           │ pass
           ▼
┌─────────────────────────────────────────────────────────────┐
│  Layer 2: Groundedness Validation                           │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  Extract chunk IDs / document refs from LLM output   │   │
│  │  Verify each claim references a known chunk ID       │   │
│  │  Unsupported claims → LowConfidence → HITL           │   │
│  │  No citations at all → HITL escalation               │   │
│  └─────────────────────────────────────────────────────┘   │
└──────────┬──────────────────────────────────────────────────┘
           │ pass
           ▼
┌─────────────────────────────────────────────────────────────┐
│  Layer 3: State Machine Invariants                          │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  Before state transition:                            │   │
│  │    Is allowed transition? → guard check              │   │
│  │    Is terminal state? → block writes                 │   │
│  │    Is idempotent? → dedup check                      │   │
│  │  After transition: verify persisted state            │   │
│  └─────────────────────────────────────────────────────┘   │
└──────────┬──────────────────────────────────────────────────┘
           │ pass
           ▼
┌─────────────────────────────────────────────────────────────┐
│  Layer 4: Bounded Execution                                  │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  Every agent loop has max iterations:                │   │
│  │    LLM retries: max 3 → DLQ on exhaustion            │   │
│  │    Chunk iteration: max 100 → HITL on overflow       │   │
│  │    State retries: max 3 → DEAD_LETTERED              │   │
│  │  Ceiling exceeded → route to DLQ / HITL              │   │
│  └─────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

### Layer 1: Schema Enforcement

All LLM outputs that enter domain logic must be validated against explicit Go struct schemas. This applies to:

- `ComplianceAgent.AnalyzeCompliance` — returns a `GapAnalysisResult` struct instead of a raw string
- `VendorAgent.ProcessVendor` — LLM analysis output validated against `LLMAnalysis` struct
- `DocumentAgent.ProcessDocument` — OCR `KeyValues` validated against field-specific schemas

**Schema validation flow:**

```go
// internal/domain/verification/schema.go

// LLMOutputSchema defines the expected structure of an LLM response.
type LLMOutputSchema struct {
    RequiredFields []string      // fields that must be present
    FieldTypes     map[string]reflect.Type  // expected types per field
    AllowedValues  map[string][]string      // enumerated values if constrained
}

// ValidateOutput checks that a parsed LLM output conforms to the schema.
// Returns RejectOutputError if required fields are missing or wrong types.
func ValidateOutput(schema LLMOutputSchema, output map[string]any) error {
    for _, field := range schema.RequiredFields {
        val, ok := output[field]
        if !ok {
            return NewRejectOutputError("missing required field: %s", field)
        }
        expectedType := schema.FieldTypes[field]
        if expectedType != nil && reflect.TypeOf(val) != expectedType {
            return NewRejectOutputError("field %s: expected %s, got %T",
                field, expectedType, val)
        }
    }
    return nil
}
```

**Where validation is injected:**

| Agent | Method | Current return type | New return type | Schema |
|-------|--------|-------------------|-----------------|--------|
| ComplianceAgent | `AnalyzeCompliance` | `(string, error)` | `(*GapAnalysisResult, error)` | `GapAnalysisSchema` |
| VendorAgent | `ProcessVendor` (LLM path) | `result["llm_analysis"] any` | `(*LLMAnalysis, error)` | `LLMAnalysisSchema` |
| DocumentAgent | `ProcessDocument` | `map[string]any` | `(*DocumentResult, error)` | `DocumentResultSchema` |

### Layer 2: Groundedness Validation

LLM compliance analysis claims must cite specific chunk IDs, document references, or section numbers. The groundedness validator extracts citation patterns from LLM output and verifies they reference known chunks.

```go
// internal/domain/verification/groundedness.go

type Citation struct {
    ChunkID    string `json:"chunk_id"`
    DocumentID string `json:"document_id,omitempty"`
    SectionRef string `json:"section_ref,omitempty"`
}

type GroundednessResult struct {
    AllClaimsSupported  bool        `json:"all_claims_supported"`
    SupportedClaims     []Citation  `json:"supported_claims"`
    UnsupportedClaims   []string    `json:"unsupported_claims"`
    Confidence          float64     `json:"confidence"`
}

// ValidateGroundedness extracts and validates citations from LLM output.
// Returns LowConfidenceError if unsupported claims exceed threshold.
func ValidateGroundedness(
    ctx context.Context,
    llmOutput string,
    knownChunks []domain.ComplianceChunk,
    threshold float64,
) (*GroundednessResult, error) {
    citations := extractCitations(llmOutput)  // regex: chunk-*, doc-*, §, Section
    supported := filterKnownChunks(citations, knownChunks)
    ratio := float64(len(supported)) / float64(len(citations))
    if ratio < threshold {
        return nil, NewLowConfidenceError("groundedness: %.2f < %.2f", ratio, threshold)
    }
    return &GroundednessResult{
        AllClaimsSupported: len(citations) == len(supported),
        SupportedClaims:    supported,
        UnsupportedClaims:  findUnsupported(citations, knownChunks),
        Confidence:         ratio,
    }, nil
}
```

**Citation patterns detected:**

| Pattern | Example | Source |
|---------|---------|--------|
| Chunk ID reference | `"as noted in chunk-47"` | `ComplianceChunk.ID` |
| Section reference | `"Section 73 of CGST Act"` | `ComplianceMetadata.SectionsReferenced` |
| Document reference | `"per Circular No. SEBI/HO/2026/45"` | `ComplianceMetadata.CircularNumber` |
| Page reference | `"page 12, paragraph 3"` | `ComplianceChunk.PageNumber` |

**Escalation rules:**

- **No citations at all** → immediately route to HITL (operator reviews the LLM output for hallucination risk)
- **Supported ratio < 0.7** → route to HITL with a warning that most claims are ungrounded
- **Supported ratio 0.7-0.9** → accept but flag in audit trail for operator awareness
- **Supported ratio >= 0.9** → accept without escalation

### Layer 3: State Machine Invariants

Every agentic state transition must be guarded by an invariant check. The invariant checker wraps the existing `JobStatus` and `TrustBattery` state machines and is called before any state mutation.

```go
// internal/domain/verification/invariants.go

// StateTransition defines a legal state transition.
type StateTransition struct {
    From        domain.JobStatus
    To          domain.JobStatus
    IsIdempotent bool
}

// AllowedTransitions defines the legal state machine.
var AllowedTransitions = []StateTransition{
    {From: domain.JobStatusPending,       To: domain.JobStatusQueued,          IsIdempotent: false},
    {From: domain.JobStatusQueued,        To: domain.JobStatusProcessing,     IsIdempotent: false},
    {From: domain.JobStatusProcessing,    To: domain.JobStatusCompleted,      IsIdempotent: true},
    {From: domain.JobStatusProcessing,    To: domain.JobStatusRetryableFailed, IsIdempotent: true},
    {From: domain.JobStatusProcessing,    To: domain.JobStatusAwaitingHITL,   IsIdempotent: true},
    {From: domain.JobStatusRetryableFailed, To: domain.JobStatusProcessing,   IsIdempotent: false},
    {From: domain.JobStatusAwaitingHITL,  To: domain.JobStatusCompleted,      IsIdempotent: true},
    {From: domain.JobStatusAwaitingHITL,  To: domain.JobStatusFailed,         IsIdempotent: true},
    {From: domain.JobStatusProcessing,    To: domain.JobStatusDeadLettered,   IsIdempotent: true},
}

// AssertTransition checks that a transition is legal and idempotent.
func AssertTransition(from, to domain.JobStatus) error {
    for _, t := range AllowedTransitions {
        if t.From == from && t.To == to {
            return nil
        }
    }
    return NewInvariantViolationError("illegal transition: %s → %s", from, to)
}
```

**Invariant checks per agent:**

| Agent | Transition Guarded | Enforced By |
|-------|-------------------|-------------|
| `DocumentAgent.ProcessDocument` | PROCESSING → COMPLETED / AWAITING_HITL | `AssertTransition` before `UpsertJob` |
| `VendorAgent.ProcessVendor` | PROCESSING → COMPLETED / AWAITING_HITL | `AssertTransition` before `UpsertJob` |
| Worker poll loop | QUEUED → PROCESSING | `AssertTransition` before optimistic lock |
| Worker error path | PROCESSING → RETRYABLE_FAILED / DEAD_LETTERED | `AssertTransition` before DLQ routing |
| HITL callback | AWAITING_HITL → COMPLETED / FAILED | `AssertTransition` in Slack webhook handler |

### Layer 4: Bounded Execution

All agent loops must have explicit maximum iteration ceilings. Exceeding a ceiling routes the job to DLQ (for retryable failures) or HITL (for ambiguous outcomes) — never silent infinite retry.

```go
// internal/domain/verification/bounds.go

// ExecutionBounds defines iteration ceilings for agent loops.
type ExecutionBounds struct {
    MaxLLMRetries        int  // max LLM call retries (default: 3)
    MaxChunkIterations   int  // max compliance chunks per analysis (default: 100)
    MaxStateRetries      int  // max optimistic lock retries (default: 3)
    MaxPipelineDepth     int  // max chained agent invocations (default: 5)
    MaxJobProcessingTime time.Duration // max wall-clock time per job (default: 5m)
}

// DefaultExecutionBounds returns the default bounds.
func DefaultExecutionBounds() ExecutionBounds {
    return ExecutionBounds{
        MaxLLMRetries:        3,
        MaxChunkIterations:   100,
        MaxStateRetries:      3,
        MaxPipelineDepth:     5,
        MaxJobProcessingTime: 5 * time.Minute,
    }
}

// CheckCeiling returns an error if the current count exceeds the maximum.
func (b ExecutionBounds) CheckCeiling(current, max int, label string) error {
    if current >= max {
        return NewCeilingExceededError("%s: %d >= %d, routing to DLQ", label, current, max)
    }
    return nil
}
```

**Where bounds are enforced:**

| Loop | Ceiling | Exceeded Action | Current Code |
|------|---------|-----------------|-------------|
| LLM retry in compliance chat | 3 | `DEAD_LETTERED`, audit reason logged | No ceiling — retries indefinitely |
| Chunk iteration in `chunkText` | 100 | `AWAITING_HITL`, flag `too_many_chunks` | No ceiling — can iterate arbitrary chunks |
| Optimistic lock retry in `UpsertJob` | 3 | `DEAD_LETTERED`, preserve last error | Retries once (see `document_agent.go:173-185`) |
| Worker poll loop dequeue | 5 consecutive failures | Worker pauses 30s, alerts operator | No failure tracking — loops through errors |

### Specific Technology Choices

| Component | Choice | Rationale |
|-----------|--------|-----------|
| **Schema validation** | `encoding/json` + `reflect` in `internal/domain/verification/schema.go` | Zero external dependency; mirrors existing JSON deserialization patterns |
| **Groundedness validation** | Custom Go regex + chunk ID lookup in `internal/domain/verification/groundedness.go` | Deterministic; no LLM needed to validate LLM claims |
| **State invariant engine** | Explicit transition tables in `internal/domain/verification/invariants.go` | Single source of truth for all allowed transitions; easy to audit |
| **Execution bounds** | Configurable struct in `internal/domain/verification/bounds.go` | Per-tenant overrides via `retrieval_config.go`-style pattern |
| **Verification test harness** | `tests/agentic/verification/` — new package with `//go:build agentic` | Isolated from unit and live tests; runs in CI agentic test suite |
| **Schema test doubles** | `SchemaValidatingStubLLM` — wraps `StubLLM` and validates output schema | Catches schema drift in agentic tests without real LLM calls |
| **Groundedness test fixtures** | `tests/agentic/verification/testdata/` — known chunk IDs + citations | Deterministic groundedness validation without real documents |
| **Metrics** | `internal/telemetry/metrics.go` — counters for schema rejection, groundedness score, invariant violations, ceiling exceeded | Existing telemetry infrastructure; new counter keys added |

### Why Schema Enforcement Instead of Type Assertions

The current code uses `map[string]any` type assertions (`result["risk_score"].(int)`) which panic on wrong types and silently miss missing keys. Schema enforcement provides:

1. **Explicit contracts** — every LLM output has a documented schema visible in the codebase
2. **Fail-fast rejection** — malformed outputs are caught at the boundary, not propagated into domain logic
3. **Auditability** — schema rejections are recorded in metrics and audit trail
4. **Testability** — schemas can be unit-tested independently of LLM providers

### Why Groundedness Validation Instead of Blind Trust

The compliance agent's current `AnalyzeCompliance` sends chunks to the LLM and returns the raw reasoning string. There is no verification that the LLM actually used the provided chunks. Groundedness validation:

1. **Prevents hallucination propagation** — LLM claims not supported by provided context are flagged
2. **Provides operator signal** — HITL reviewers see which claims are grounded vs. unsupported
3. **Enables quality metrics** — groundedness score per LLM call is recorded in telemetry
4. **Follows the "deterministic first" principle** — citation extraction uses regex, not a second LLM call

### Why State Machine Invariants Instead of Ad-Hoc Checks

State transitions are currently checked ad-hoc in worker logic (`worker.go:234-249` checks `needs_hitl` before Slack dispatch). The invariant layer centralizes all transition rules:

1. **Single source of truth** — all legal transitions in one table; agents call `AssertTransition` not ad-hoc checks
2. **Prevents forbidden transitions** — e.g., `COMPLETED → PROCESSING` (tested in `state_machine_test.go:82-95`) cannot happen
3. **Idempotency enforcement** — transitions marked as idempotent can be applied multiple times safely
4. **Audit trail integration** — every `AssertTransition` pass/fail is recorded

### Why Bounded Execution Instead of Implicit Iteration

Agent loops without explicit ceilings can cause resource exhaustion, unbounded costs, or silent infinite retry:

1. **LLM cost control** — max 3 retries prevents runaway spend on a stuck LLM call
2. **Worker availability** — max chunk iterations prevents a single large document from starving other jobs
3. **DLQ guarantees** — exceeded ceilings always route to DLQ, never silent abort
4. **Predictable resource usage** — each job has a known upper bound on processing time and iterations

## Alternatives Considered

### 1. No Verification (Current State)

Rejected because:
- LLM outputs flow unvalidated into domain logic — malformed responses can silently corrupt vendor risk scores or compliance gap analyses.
- No groundedness check means hallucinated regulatory interpretations can reach operators without warning.
- State transitions are checked ad-hoc or not at all — a buggy agent could persist `COMPLETED → PROCESSING` (see `state_machine_test.go:82-95`).
- Agent loops have no ceilings — a stuck LLM provider causes unbounded retries and resource exhaustion.

### 2. Full End-to-End Verification Only

Rejected because:
- End-to-end tests with real LLM providers are slow (5-30s per call), expensive (token costs), and non-deterministic (model output drift).
- Schema violations, groundedness failures, and invariant violations are harder to debug in e2e context than in isolation.
- The four-layer approach allows unit-testing each verification concern independently before wiring into agent workflows.
- End-to-end tests remain valuable as a final integration check (see `tests/agentic/pipeline_test.go`) but cannot replace layered verification.

### 3. Property-Based Testing Only (QuickCheck / Rapid)

Rejected because:
- Property-based testing excels at finding edge cases in pure functions (domain logic, state machines) but cannot verify LLM output schema conformance or groundedness.
- Agentic workflows involve I/O (DB, queue, LLM) — property-based testing is less effective for I/O-bound systems.
- The four-layer approach includes property-based testing for state machine invariants (see `InvariantCheck` property), but this is one layer, not the whole strategy.
- Property-based testing adds a new dependency (Rapid or gopter) for a problem that explicit transition tables solve more readably.

### 4. LLM-as-Judge for Verification

Rejected because:
- Using an LLM to verify another LLM's output violates the "deterministic first, LLM last" principle.
- LLM-as-judge introduces a second source of hallucination and cost.
- Schema validation, citation regex extraction, and transition tables are all deterministic — no LLM needed.
- LLM-as-judge would be appropriate only for subjective quality assessment (e.g., "does this analysis read well?"), which is out of scope for this ADR.

## Consequences

### Benefits

1. **Schema violations caught at the boundary.** LLM outputs that miss required fields or have wrong types are rejected before they enter domain logic. Malformed outputs are recorded in telemetry for provider quality monitoring.

2. **Hallucinations are detectable and escalatable.** The groundedness validator flags unsupported LLM claims with a confidence ratio. Operators see which claims are grounded vs. unsupported, enabling informed HITL decisions.

3. **State machine integrity is enforced centrally.** Allowed transitions are defined in one table, not scattered across agent implementations. A single `AssertTransition` call protects every state mutation.

4. **Bounded execution prevents resource exhaustion.** Every agent loop has an explicit maximum iteration count. Exceeded ceilings route to DLQ or HITL — never silent retry.

5. **Incremental adoption.** The verification framework is additive — existing tests continue to work. Each layer can be adopted independently, starting with schema enforcement (highest impact, lowest migration cost).

6. **CI quality gates.** The verification test suite (`tests/agentic/verification/`) runs as a CI gate. Schema rejection rate, groundedness score, and invariant violation count are tracked as release-blocking metrics.

7. **Audit trail enrichment.** Every verification check pass/fail is recorded in the audit trail with actor (verification layer), action (schema_validation, groundedness, invariant, bounded_execution), and outcome.

### Trade-offs / Risks

1. **Higher test maintenance cost.** Each schema change requires updating the corresponding `LLMOutputSchema` definition, `GroundednessResult` expectations, and golden test trajectories. Mitigation: schema definitions are colocated with the LLM prompt they validate; golden test fixtures are generated from schema, reducing manual updates.

2. **False positives in groundedness validation.** LLM outputs may contain valid claims that reference implicit knowledge (e.g., "the CGST Act requires quarterly filing") without citing a specific chunk. Mitigation: the groundedness threshold is configurable per-tenant; a known-claims allowlist can suppress false positives for well-known regulatory facts.

3. **Performance overhead of four validation layers.** Each LLM call now passes through schema validation, groundedness extraction, invariant guard, and bounds check. Mitigation: all four layers are pure Go with no I/O (except chunk ID lookup for groundedness, which is a map lookup). Expected overhead is < 1ms per call.

4. **Coordination with provider contract tests.** Schema changes may break provider contract tests (`tests/agentic/contracts/provider_contract_test.go`) that assert stub behavior. Mitigation: contract tests are updated alongside schema changes as part of the same PR.

5. **Bounded execution ceiling tuning.** Default ceilings (3 LLM retries, 100 chunks, 3 state retries) may be too conservative or too permissive for specific tenants. Mitigation: ceilings are configurable per-tenant via `ExecutionBounds` struct, following the `retrieval_config.go` pattern.

## Implementation Details

### Verification Package Structure

```
internal/domain/verification/
├── schema.go           — Schema definition and validation (ValidateOutput)
├── schema_test.go      — Unit tests for schema validation
├── groundedness.go     — Citation extraction and validation (ValidateGroundedness)
├── groundedness_test.go — Unit tests for groundedness with test fixtures
├── invariants.go       — State transition tables and AssertTransition
├── invariants_test.go  — Property-based tests for transition legality
├── bounds.go           — ExecutionBounds, CheckCeiling, DefaultExecutionBounds
├── bounds_test.go      — Ceiling enforcement tests
└── errors.go           — RejectOutputError, LowConfidenceError, InvariantViolationError, CeilingExceededError
```

### Verification Test Suite

```
tests/agentic/verification/
├── schema_test.go               — Test ValidateOutput with valid/invalid/missing fields
├── groundedness_test.go          — Test ValidateGroundedness with citations, no citations, partial citations
├── invariants_test.go            — Test AssertTransition for all allowed + disallowed transitions
├── bounds_test.go                — Test CheckCeiling for under/at/over limits
├── integration_test.go           — Wire verification layers into agent workflow end-to-end
└── testdata/
    ├── citations_valid.json      — Known chunk IDs and section references
    ├── citations_empty.json      — No citations (tests HITL escalation)
    └── citations_partial.json    — Mix of supported and unsupported claims
```

### Integration Points

| Integration Point | Change Required | Layer |
|-------------------|----------------|-------|
| `ComplianceAgent.AnalyzeCompliance` | Return `GapAnalysisResult` instead of string; call `ValidateOutput` | 1 |
| `ComplianceAgent.AnalyzeCompliance` | Call `ValidateGroundedness` on LLM output before returning | 2 |
| `DocumentAgent.ProcessDocument` | Call `AssertTransition` before `UpsertJob` | 3 |
| `VendorAgent.ProcessVendor` | Call `AssertTransition` before `UpsertJob` | 3 |
| Worker poll loop (`worker.go`) | Check `ExecutionBounds.CheckCeiling` before retry | 4 |
| Worker error path (`worker.go`) | Route to DLQ if ceiling exceeded | 4 |
| Compliance chunk loop | Check `MaxChunkIterations` before processing | 4 |

## Compliance with OpsCore Principles

### "Deterministic first, LLM last"

The verification strategy explicitly encodes this:

- **Schema validation**: Pure Go `reflect` + `encoding/json` — no LLM involved.
- **Groundedness validation**: Regex citation extraction + map lookup — no LLM involved.
- **State machine invariants**: Explicit transition tables in pure Go — no LLM involved.
- **Bounded execution**: Integer comparison + config — no LLM involved.
- **LLM as judge**: Explicitly rejected as an alternative (see §Alternatives Considered, item 4).

### Multi-tenant isolation

- Schema definitions are global but groundedness chunk lookups are scoped to `tenant_id`.
- Execution bounds are configurable per-tenant via `ExecutionBounds` struct.
- State invariants are tenant-agnostic (transition legality is universal).
- All verification test fixtures are scoped to a test tenant ID.

### Observability

- Every verification layer records pass/fail counts to `internal/telemetry/metrics.go`.
- Schema rejection rate is tracked per LLM provider (Sarvam, OpenRouter).
- Groundedness confidence is recorded per compliance analysis call.
- Invariant violations are logged at `slog.Warn` level with job ID and transition details.
- Ceiling-exceeded events include the loop label, current count, and maximum.
- All verification outcomes are recorded in the audit trail.

## Related ADRs

- **ADR-0002**: Vendor Onboarding — establishes the queue-backed processing pattern and state machine conventions that this ADR verifies.
- **ADR-001**: Retrieval Architecture for Compliance & Document Search — establishes the "deterministic first, LLM last" principle that this ADR extends to LLM output verification.
