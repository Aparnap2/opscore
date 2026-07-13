# ADR-0006: Deterministic-First AI Policy

## Status

**Proposed** — policy is defined in prose in AGENTS.md but not yet enforced as an architectural rule with automated gates.

## Date

2026-07-13

## Context

### Problem Statement

OpsCore's fundamental engineering philosophy is **"compiled logic first, LLM last."** All validation, scoring, classification, trust-tier transitions, and workflow state machines must be implemented as deterministic Go code wherever possible. LLM/OCR should only be used where deterministic logic cannot reliably solve the problem.

This philosophy exists as prose in `AGENTS.md` but has never been codified as an architectural rule set with enforceable mechanisms. As the system grows, the risk of LLM creeping into domain logic increases:

1. **No architectural boundary prevents LLM in domain code.** The `internal/domain` package has zero I/O imports today, but there is no automated check or review gate that prevents an LLM call from being added to a domain function.

2. **No config gating for LLM calls.** Today, an LLM provider is passed to agents as a constructor parameter (`llm providers.LLMProvider`). If the provider is non-nil, the LLM call executes. There is no global or per-tenant config flag to disable all LLM calls in one place.

3. **No enforced interface for LLM traceability.** While `providers.LLMProvider` exists and `providers.TracingProvider.RecordLLMCall()` is defined, there is no requirement that every LLM call records model name, latency, token count, and cost. Some calls (e.g., compliance gap analysis) do not currently record these metrics.

4. **No code review requirement for new LLM usage.** A developer can introduce a new LLM call in a PR without justifying why deterministic logic was insufficient.

5. **Existing deterministic logic is not cataloged.** There is no single document that enumerates which rules are deterministic and which are LLM-based, making it hard to review whether LLM is being used appropriately.

### Current State: What Is Deterministic vs. What Uses LLM

#### Deterministic (Pure Go, No I/O) — in `internal/domain/`

| Component | File | Logic |
|-----------|------|-------|
| `ComputeVendorRisk()` | `internal/domain/risk_scorer.go` | Regex validation + weighted arithmetic (base 50, +15 for GST, +10 for PAN, -40 for duplicate, etc.) |
| `TrustBattery` state machine | `internal/domain/trust_battery.go` | Explicit state transitions: PROBATION → STANDARD → PREFERRED → STRATEGIC + BLOCKED. Methods: `RecordSuccess()`, `RecordError()`, `FraudFlagged()`, `AllChecksPass()`, `TransactionThresholdMet()`, `DisputeFiled()`, `InactivityDecay()` |
| `IndiaValidator` | `internal/domain/india_validator.go` | Regex-based validation for GST, PAN, IFSC, Aadhaar, UPI, bank account, PIN code, state codes |
| `JobStatus` state machine | `internal/domain/types.go:41-66` | Explicit enum with transition rules: `IsRetryable()`, `IsTerminal()`, `IsFailed()` |
| `HasPermission()` | `internal/domain/auth.go` | Role-permission matrix lookup with admin wildcard |
| `GetLimit()` | `internal/domain/usage.go` | Plan-metric map lookup for usage limits |
| `ValidateGST()`, `ValidatePAN()`, `ValidateIFSC()` | `internal/domain/risk_scorer.go` | Regex pattern matching |
| `DocumentClassifier` | `internal/domain/document_classifier.go` | Keyword-based classification using first 500 characters |
| `ChunkDocument()` | `internal/domain/chunker.go` | Size-based text splitting |
| `BatchJobFromJob()` | `internal/domain/batch_job.go` | Pure data transformation |

#### LLM-Based (Via Provider Interface) — in `internal/agents/`

| Component | File | LLM Usage | Config Guard |
|-----------|------|-----------|--------------|
| Vendor document analysis | `internal/agents/vendor_agent.go:97-106` | `a.llm.Reason()` for non-standard vendor document analysis | Guarded: `if a.llm != nil && len(data.Documents) > 0` |
| Compliance gap analysis | `internal/agents/compliance_agent.go:154-178` | `a.llm.Reason()` for policy-against-updates gap analysis | Guarded: `if a.llm == nil` returns early |
| Compliance analyze job | `internal/agents/compliance_agent.go:300-312` | `a.AnalyzeCompliance()` called for "analyze" job type | Guarded: `if a.llm != nil` |

#### LLM-Optional but Useful (OCR)

| Component | File | OCR Usage | Config Guard |
|-----------|------|-----------|--------------|
| Sarvam OCR | `internal/agents/document_agent.go` | `ocrProvider.Extract()` for PDF/image text extraction | Guarded: provider configured via env vars |

### The Risk of LLM Creep

Without explicit architectural enforcement, the following scenarios become likely:

1. **A developer adds LLM to risk scoring.** "Let's use LLM to validate GST addresses" — instead of using the existing regex-based validator. The LLM call is slower, non-deterministic, and more expensive.

2. **A developer adds LLM to the state machine.** "Let's use LLM to decide trust tier transitions" — instead of the explicit state machine in `trust_battery.go`. Trust tiers become non-deterministic and un-auditable.

3. **A developer adds LLM to the auth layer.** "Let's use LLM to resolve permissions" — instead of the role-permission matrix. Auth becomes slow and unpredictable.

4. **LLM costs grow unbounded.** Without per-tenant config gating and cost tracking, LLM usage can grow silently, increasing operational costs without corresponding business value.

5. **System behavior becomes non-deterministic.** The same inputs produce different outputs on different days (model updates, prompt drift, temperature settings). This breaks auditability and reproducibility.

## Decision

Codify the deterministic-first policy as an explicit architectural rule with four enforcement mechanisms. The policy applies to all Go code in the OpsCore repository.

### Policy Statement

> **All business logic in `internal/domain/` must be pure Go with zero I/O. LLM calls must only exist in `internal/agents/` or `internal/adapters/`, must go through `providers.LLMProvider`, must be guarded by a config flag, and must be justified in code review.**

### Enforcement Mechanism 1: Domain Isolation (Architectural Boundary)

**Rule:** `internal/domain/` must not import or reference any LLM, OCR, or I/O-related type.

This is already true today — `internal/domain/` imports only `time`, `regexp`, and standard library packages. The rule formalizes this as a non-negotiable architectural boundary.

**Checklist for code review:**
- Does the new code go in `internal/domain/`? → It must be pure Go, zero I/O.
- If it needs an LLM call → it belongs in `internal/agents/` or `internal/adapters/`.
- Domain code that currently references external systems (e.g., `ComputeVendorRisk` takes a `tenantID` parameter for dedup but does not do I/O itself) must remain I/O-free.

**Verification:** `go vet` does not catch I/O in domain. Manual review or a future `custom vet` check can verify that `internal/domain/` imports no database/HTTP/queue packages.

### Enforcement Mechanism 2: LLM Gating Config (Global and Per-Tenant)

**Rule:** Every LLM call must be guarded by a config flag that can disable LLM globally or per-tenant.

**Proposed implementation:**

```go
// internal/domain/llm_config.go (new)
type LLMConfig struct {
    Enabled bool `json:"enabled"` // global on/off; defaults to true in dev, false in prod
}

// Global LLM config, loaded from environment at startup.
var GlobalLLMConfig = LLMConfig{
    Enabled: os.Getenv("LLM_ENABLED") != "false",
}

// Per-tenant override in domain.Tenant.Config:
// tenant.Config["llm_enabled"] = false disables LLM for that tenant.
func IsLLMEnabled(tenantConfig map[string]any) bool {
    if !GlobalLLMConfig.Enabled {
        return false
    }
    if v, ok := tenantConfig["llm_enabled"]; ok {
        if enabled, ok := v.(bool); ok {
            return enabled
        }
    }
    return true
}
```

**Usage in agents:**

```go
// Before an LLM call, check both global and per-tenant config.
if !domain.IsLLMEnabled(tenant.Config) {
    slog.Debug("LLM disabled for tenant", "tenant_id", tenantID)
    return // or skip LLM step
}
if a.llm == nil {
    slog.Debug("LLM provider not configured, skipping LLM step")
    return
}
```

**Current state:** The agent-level guard `if a.llm != nil` already provides a partial gate — if no provider is configured, LLM calls are skipped. The new config adds:
1. A global `LLM_ENABLED=false` env var to disable all LLM calls regardless of provider configuration.
2. A per-tenant `llm_enabled` config key in `domain.Tenant.Config` to disable LLM for specific tenants.
3. Explicit `slog.Debug` logging when LLM is disabled, for operational visibility.

### Enforcement Mechanism 3: LLM Call Interface with Traceable Metadata

**Rule:** Every LLM call must go through `providers.LLMProvider` and must record metadata (model name, latency, token count, cost) via `TracingProvider.RecordLLMCall()`.

The `providers.LLMProvider` interface (already defined):

```go
// internal/providers/interfaces.go:30-34
type LLMProvider interface {
    ExtractFields(ctx context.Context, text string, schema any) (json.RawMessage, float64, error)
    Reason(ctx context.Context, prompt string) (string, error)
    Chat(ctx context.Context, messages []ChatMessage) (string, *json.RawMessage, error)
}
```

The `TracingProvider.RecordLLMCall()` method (already defined):

```go
// internal/providers/interfaces.go:117
type TracingProvider interface {
    // ...
    RecordLLMCall(ctx context.Context, model string, tokens int, costINR float64, durationMs int)
    // ...
}
```

**Current gaps:**
1. `vendor_agent.go:104` calls `a.tracer.RecordLLMCall(ctx, "sarvam-llm", 0, 0.0, 0)` with placeholder values (0 tokens, 0 cost, 0 duration). The actual token count, cost, and duration are not captured.
2. `compliance_agent.go` does not call `RecordLLMCall` at all — its LLM usage is untraced.
3. The `LLMProvider` interface does not return usage/cost information. The adapters (Sarvam, OpenRouter) receive token counts in API responses but do not surface them in the interface contract.

**Required changes:**

1. Add usage metadata to the LLMProvider interface:

```go
type LLMResult struct {
    Content          string
    ReasoningDetails *json.RawMessage
    Model            string
    InputTokens      int
    OutputTokens     int
    DurationMs       int64
    CostINR          float64
}

type LLMProvider interface {
    ExtractFields(ctx context.Context, text string, schema any) (*LLMResult, error)
    Reason(ctx context.Context, prompt string) (*LLMResult, error)
    Chat(ctx context.Context, messages []ChatMessage) (*LLMResult, error)
}
```

2. Update each adapter (`openrouter/llm.go`, `sarvam/llm.go`, `fallback/llm.go`) to populate `LLMResult` with actual token counts and latency.

3. Update every LLM call site to call `a.tracer.RecordLLMCall()` with real metadata.

4. The Langfuse provider (`telemetry/langfuse.go`) already sends `model`, `usage`, `cost`, and timestamps — but the data it receives is incomplete.

### Enforcement Mechanism 4: Code Review Gate

**Rule:** All PRs introducing new LLM calls must include justification in the PR description.

**PR description template (for LLM-introducing PRs):**

```markdown
## LLM Usage Justification

### Why is deterministic logic insufficient here?
[Explain what deterministic approach was considered and why it cannot solve the problem.]

### What is the LLM's role?
[Describe what the LLM does: extraction, reasoning, classification, etc.]

### What is the fallback if LLM is unavailable or disabled?
[e.g., "Return empty result", "Skip the analysis step", "Default to human review"]

### What traceability is in place?
- [x] LLM call goes through providers.LLMProvider
- [x] LLM gating config (global and per-tenant) is checked
- [x] TracingProvider.RecordLLMCall() is called with real metadata
- [x] LLM errors are non-fatal (system continues without LLM result)

### What metrics will track LLM usage?
[e.g., "langfuse_llm_call_count, vendor_agent_llm_duration_ms"]
```

**Gate location:** This should be a mandatory checklist in the PR template (`PULL_REQUEST_TEMPLATE.md` or a GitHub PR template). The reviewer must verify that the justification is adequate before approving.

### Integration Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                     PR Review Gate                           │
│  "Why not deterministic? Explain LLM justification"         │
└─────────────────────────┬───────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│                 Agent Layer (internal/agents/)               │
│                                                             │
│  ┌─────────────────────┐  ┌──────────────────────────────┐  │
│  │  VendorAgent        │  │  ComplianceAgent              │  │
│  │  .ProcessVendor()   │  │  .AnalyzeCompliance()        │  │
│  │                     │  │                              │  │
│  │  1. Deterministic   │  │  1. Deterministic severity   │  │
│  │     risk scoring    │  │     classification           │  │
│  │  2. LLM docs        │  │  2. LLM gap analysis         │  │
│  │     (optional)      │  │     (optional)               │  │
│  └────────┬────────────┘  └────────┬─────────────────────┘  │
│           │                        │                        │
│           ▼                        ▼                        │
│  ┌──────────────────────────────────────────────┐           │
│  │  Config Gate                                 │           │
│  │  if !IsLLMEnabled(tenant.Config) → skip LLM  │           │
│  │  if a.llm == nil → skip LLM                  │           │
│  └─────────────────────┬────────────────────────┘           │
│                        │                                    │
│                        ▼                                    │
│  ┌──────────────────────────────────────────────┐           │
│  │  LLM Provider Interface                      │           │
│  │  providers.LLMProvider                       │           │
│  │  .ExtractFields() → LLMResult                │           │
│  │  .Reason() → LLMResult                       │           │
│  │  .Chat() → LLMResult                         │           │
│  └─────────────────────┬────────────────────────┘           │
│                        │                                    │
└────────────────────────┼────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│              Adapter Layer (internal/adapters/)              │
│                                                             │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐  │
│  │  OpenRouter  │  │  Sarvam      │  │  Fallback Chain  │  │
│  │  /llm.go     │  │  /llm.go     │  │  /llm.go         │  │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────────┘  │
│         │                 │                 │              │
│         └─────────────────┴─────────────────┘              │
│                           │                                 │
│                           ▼                                 │
│              RecordLLMCall(model, tokens, cost, ms)         │
│                           │                                 │
│                           ▼                                 │
│              Langfuse API (telemetry/langfuse.go)           │
└─────────────────────────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│                    Domain Layer (internal/domain/)           │
│                                                             │
│  No I/O. Pure Go. No LLM references.                        │
│  Risk scorer, trust battery, india validator, state machine │
└─────────────────────────────────────────────────────────────┘
```

### Specific Technology Choices

| Component | Choice | Rationale |
|-----------|--------|-----------|
| **Domain isolation rule** | Zero I/O imports in `internal/domain/` | Already true today; formalizes with code review gate and optional `go vet` check |
| **Global LLM config** | `LLM_ENABLED` env var (defaults to true in dev, false in prod) | Single environment variable controls all LLM calls; easy to disable in emergencies |
| **Per-tenant LLM config** | `llm_enabled` key in `domain.Tenant.Config` map | Tenants opt in to LLM features; starter plan sets `llm_calls=0` already in PlanLimits |
| **LLM interface** | `providers.LLMProvider` with `LLMResult` return type | Structured metadata (model, tokens, cost, duration) returned from every call |
| **Tracing** | `TracingProvider.RecordLLMCall()` via Langfuse | Already implemented; needs real metadata instead of placeholder values |
| **PR gate** | Mandatory justification in PR description for new LLM calls | Low-friction, human-enforceable; no tooling required to start |
| **Future automation** | Custom `go vet` analyzer or `golangci-lint` rule | Would block domain I/O at compile time; deferred until LLM usage grows |

## Alternatives Considered

### 1. No Formal Policy (Status Quo)

Rejected because:

- **LLM creep is inevitable.** Without enforcement, the path of least resistance is to "just use LLM" for problems that have deterministic solutions.
- **No audit trail for LLM decisions.** If a developer adds LLM to a domain function, there is no record of the justification or the alternatives considered.
- **Inconsistent LLM usage.** Some agents call `RecordLLMCall`, some don't. Some check `a.llm != nil`, some don't. The policy exists only in AGENTS.md prose, which is not referenced during code review.

### 2. Full LLM-First Approach

Rejected because:

- **Violates OpsCore's core philosophy.** The system explicitly chose deterministic-first for auditability, reliability, and cost.
- **Non-deterministic business logic.** Risk scores, trust tiers, and compliance classifications would vary between LLM calls. Auditors cannot reproduce results.
- **Cost scales with volume.** At 10,000 vendors/month, LLM-based risk scoring would cost ₹1,000-5,000/month vs. near-zero for regex-based scoring.
- **Latency impact.** Deterministic scoring completes in microseconds. LLM calls take 1-5 seconds.

### 3. Hybrid Scoring (LLM + Deterministic Combined)

Rejected because:

- **Hard to audit.** If deterministic and LLM scores disagree, which takes precedence? The logic becomes a complex heuristic rather than a simple rule.
- **Worse for both cases.** Deterministic scoring is already sufficient for the current rule set. Adding LLM would increase cost and latency without improving accuracy.
- **Not needed at current scale.** The current risk scoring rules (PRD v3.0 spec, Section 8) are explicit and complete. There is no gap that LLM fills.

### 4. Automated Blocking (Custom Linter)

Partially deferred because:

- **A custom `go vet` analyzer for domain I/O** would be valuable but is significant work to implement correctly (tracking transitive imports, interface implementations, etc.).
- **Can be added later** when LLM usage grows and automated enforcement becomes cost-effective.
- **For now, human review is sufficient.** The team is small (< 5 engineers). Code review catches domain I/O violations trivially.
- **Deferred action:** This ADR proposes adding a `golangci-lint` rule or custom `vet` check as a follow-up when the team grows or when a violation is caught in review.

## Consequences

### Benefits

1. **Clear architectural boundary.** Developers know exactly where LLM calls belong (agents/adapters) and where they are forbidden (domain). Code review can enforce this with a single check: "Does this file import any I/O package?"

2. **Config gating for emergencies.** If an LLM provider is degraded or returning poor results, operators can set `LLM_ENABLED=false` (globally) or `llm_enabled=false` (per-tenant) to disable all LLM calls. No code deploy needed.

3. **Observable, traceable LLM usage.** Every LLM call records model name, input/output tokens, latency, and cost. The Langfuse dashboard shows LLM usage trends, cost per tenant, and error rates. The placeholder values in `vendor_agent.go:104` are replaced with real data.

4. **Reduced LLM dependency.** The system's core logic (risk scoring, trust tiers, validation, state machines) remains deterministic and LLM-free. LLM is used only where it adds unique value: unstructured document analysis and compliance gap reasoning.

5. **Auditable decisions.** Every LLM usage has a documented justification in the PR description. Reviewers can verify that deterministic alternatives were considered and rejected for good reason.

6. **PR template drives consistency.** The mandatory justification template ensures that every new LLM call has a clear reason, a fallback plan, traceability hooks, and metrics — before the code is merged.

7. **Per-tenant control.** Tenants on the starter plan (which sets `llm_calls: 0` in `PlanLimits`) automatically have LLM disabled by usage limits. The new `llm_enabled` config provides an additional, explicit control.

### Trade-offs / Risks

1. **LLM justification is subjective.** A developer might argue that LLM is needed for a task that could be solved with a regex or a decision tree. Mitigation: code review provides a second opinion. The PR template asks explicit questions ("Why is deterministic logic insufficient?") that the reviewer can challenge.

2. **LLMResult interface change is breaking.** Changing `LLMProvider` methods from returning `(json.RawMessage, float64, error)` to `(*LLMResult, error)` requires updating all implementations (Sarvam, OpenRouter, fallback chain, test mocks). Mitigation: this is a one-time refactor. The interface is used in ~5 files. The change is mechanical and testable.

3. **Placeholder values in existing LLM calls.** `vendor_agent.go` currently records `RecordLLMCall` with zero tokens and zero cost. The Sarvam adapter does not expose token counts. Mitigation: the ADR requires updating adapters to return real metadata. Until then, the placeholder values are documented as a known gap.

4. **No automated enforcement for domain isolation.** The rule "domain must not use LLM" relies on code review rather than a compiler check. A motivated developer could bypass review. Mitigation: add a custom `go vet` check as a follow-up (deferred action). For now, the team is small and code review is thorough.

5. **PR template is not enforced by CI.** A PR could omit the LLM justification and still merge. Mitigation: use a GitHub PR template that the author must fill out. While not technically enforced, the template serves as a visible reminder. Add a CI check that requires the "LLM Usage Justification" section in the PR body for PRs that touch LLM provider files.

6. **Per-tenant LLM config adds complexity.** Operators must set `llm_enabled` in the tenant's `Config` JSONB field. This is not yet exposed in the Streamlit admin UI. Mitigation: add a toggle in the admin UI as a follow-up. For now, it can be set via direct DB update or tenant creation code.

## Implementation Plan

### Phase 1: PR Review Gate (Immediate)

**Goal:** Ensure new LLM usage is justified from day one.

| Task | Owner | Effort |
|------|-------|--------|
| Add PR template with LLM justification section | Team | 0.5 day |
| Document the deterministic-first policy in `CONTRIBUTING.md` with a link to this ADR | Team | 0.5 day |

### Phase 2: Config Gating (This Sprint)

**Goal:** Add global and per-tenant LLM config that can disable all LLM calls.

| Task | Files Changed / Created | Effort |
|------|------------------------|--------|
| Create `internal/domain/llm_config.go` with `IsLLMEnabled()` | New file | 0.5 day |
| Add `LLM_ENABLED` env var parsing in `cmd/server/main.go` | `cmd/server/main.go` | 0.25 day |
| Update `VendorAgent.ProcessVendor()` to check `IsLLMEnabled()` before LLM call | `internal/agents/vendor_agent.go` | 0.25 day |
| Update `ComplianceAgent.AnalyzeCompliance()` to check `IsLLMEnabled()` before LLM call | `internal/agents/compliance_agent.go` | 0.25 day |
| Update `DocumentAgent` to check config before OCR (optional, OCR is not LLM but follows same pattern) | `internal/agents/document_agent.go` | 0.25 day |
| Add tests for `IsLLMEnabled()` with global off, per-tenant off, and both on | `tests/unit/llm_config_test.go` | 0.5 day |

### Phase 3: LLM Metadata (This Sprint)

**Goal:** Every LLM call returns and records real metadata.

| Task | Files Changed / Created | Effort |
|------|------------------------|--------|
| Add `LLMResult` struct to `internal/providers/interfaces.go` | `internal/providers/interfaces.go` | 0.25 day |
| Update `LLMProvider` interface methods to return `*LLMResult` | `internal/providers/interfaces.go` | 0.25 day |
| Update `openrouter/llm.go` to populate `LLMResult` from API response | `internal/adapters/openrouter/llm.go` | 1 day |
| Update `sarvam/llm.go` to populate `LLMResult` from API response | `internal/adapters/sarvam/llm.go` | 1 day |
| Update `fallback/llm.go` to propagate `LLMResult` from successful provider | `internal/adapters/fallback/llm.go` | 0.5 day |
| Update `VendorAgent.ProcessVendor()` to record real metadata | `internal/agents/vendor_agent.go` | 0.25 day |
| Update `ComplianceAgent.AnalyzeCompliance()` to record real metadata | `internal/agents/compliance_agent.go` | 0.25 day |
| Update test mocks (`LLMAdapterMock`, `agent_test.go`) | `internal/adapters/sarvam/llm.go`, `internal/agents/agent_test.go` | 0.5 day |
| Add integration test verifying `RecordLLMCall` is called with non-zero values | `tests/agentic/vendor_workflow_test.go` | 0.5 day |

### Phase 4: Langfuse Dashboard + Alerts (Next Sprint)

**Goal:** Visualize LLM usage, cost, and error rates.

| Task | Files Changed / Created | Effort |
|------|------------------------|--------|
| Add LLM-specific metrics to `internal/telemetry/metrics.go` (call count, error rate, latency p50/p99, cost per tenant) | `internal/telemetry/metrics.go` | 1 day |
| Verify Langfuse dashboard shows LLM call breakdown by model, tenant, and agent | Configuration | 0.5 day |
| Add alert for LLM error rate > 5% or cost spike > 2x baseline | `internal/domain/alert_config.go` | 0.5 day |

### Phase 5: Automated Enforcement (Future / Data-Volume Gated)

**Goal:** Catch domain I/O violations at compile time.

| Task | Files Changed / Created | Effort |
|------|------------------------|--------|
| Evaluate if a `golangci-lint` rule or custom `go vet` analyzer can detect I/O imports in `internal/domain/` | Research | 1 day |
| Implement custom analyzer (if viable) | New file in `tools/` | 2-3 days |
| Add to CI pipeline | `.github/workflows/ci.yml` | 0.5 day |

## File Map

### Existing Files

| File | Role |
|------|------|
| `internal/domain/risk_scorer.go` | Deterministic risk scoring — pure Go, regex + arithmetic |
| `internal/domain/trust_battery.go` | Deterministic trust state machine — pure Go, explicit transitions |
| `internal/domain/india_validator.go` | Deterministic India-specific validation — pure Go, regex |
| `internal/domain/types.go` | Job status state machine — pure Go, explicit enum |
| `internal/domain/auth.go` | Permission matrix — pure Go, map lookup |
| `internal/domain/usage.go` | Plan limits — pure Go, map lookup |
| `internal/domain/chunker.go` | Document chunking — pure Go, string splitting |
| `internal/providers/interfaces.go` | `LLMProvider`, `TracingProvider`, `UsageProvider` interfaces |
| `internal/agents/vendor_agent.go` | Vendor onboarding — deterministic scoring + optional LLM |
| `internal/agents/compliance_agent.go` | Compliance monitoring — deterministic classification + optional LLM gap analysis |
| `internal/adapters/openrouter/llm.go` | OpenRouter LLM adapter |
| `internal/adapters/sarvam/llm.go` | Sarvam LLM adapter |
| `internal/adapters/fallback/llm.go` | Fallback chain for multiple LLM providers |
| `internal/telemetry/langfuse.go` | Langfuse tracing provider (sends LLM call data to Langfuse) |
| `cmd/server/main.go` | Dependency wiring — creates LLM provider, tracer, agents |

### New Files (Proposed)

| File | Phase | Purpose |
|------|-------|---------|
| `internal/domain/llm_config.go` | 2 | `IsLLMEnabled()` function with global and per-tenant config |
| `tests/unit/llm_config_test.go` | 2 | Tests for LLM config gating |
| `PULL_REQUEST_TEMPLATE.md` | 1 | PR template with LLM justification section |

## Compliance with OpsCore Principles

### "Deterministic first, LLM last"

This ADR is specifically about enforcing this principle architecturally:

- **Domain isolation** ensures deterministic logic stays deterministic.
- **Config gating** ensures LLM can be turned off globally or per-tenant.
- **LLMResult metadata** ensures LLM usage is observable and measurable.
- **PR review gate** ensures every new LLM call is justified.

### Multi-tenant isolation

- Per-tenant LLM config (`llm_enabled` in `Tenant.Config`) allows tenants to opt out of LLM features.
- LLM costs are tracked per tenant via `RecordLLMCall()` with `tenantID` context.
- The starter plan already sets `llm_calls: 0` in usage limits, which effectively disables LLM for free-tier tenants.

### Observability

- Every LLM call records model name, input/output tokens, latency, and cost to Langfuse.
- LLM metrics (call count, error rate, latency p50/p99, cost per tenant) feed into dashboards and alerts.
- Slog debug logging when LLM is disabled provides operational visibility.
- PR descriptions with LLM justification provide an audit trail for every LLM addition.

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Developers bypass LLM justification in PR | New LLM calls without audit trail | PR template is visible in the PR form; CI can check for LLM justification section when LLM files are modified |
| LLMResult interface change breaks existing deployments | Compilation errors until adapters are updated | Change is mechanical and affects ~5 files; all adapters have test coverage |
| Per-tenant LLM config is not discoverable | Operators don't know they can disable LLM per tenant | Document in the Streamlit admin UI; add a toggle on the tenant settings page |
| LLM costs grow despite config gating | Operational cost overruns | Langfuse dashboard shows cost per tenant; alert on cost spike > 2x baseline |
| Custom go vet analyzer is never built | Domain isolation relies only on human review | Acceptable for team size < 5; re-evaluate when team grows or when a violation is caught in review |

## Related ADRs

- **ADR-0001**: Retrieval Architecture for Compliance & Document Search — explicitly encodes "deterministic first, LLM last" via the three-tier retrieval funnel (structured index → PostgreSQL FTS → vector → LLM).
- **ADR-0002**: Vendor Onboarding — Asynchronous Real-Time Workflow — deterministic risk scoring and trust tier assignment in domain layer; LLM is optional and non-fatal for document analysis.
- **ADR-0003**: Multi-Tenancy and Tenant Isolation — per-tenant config in `Tenant.Config` enables per-tenant LLM gating.
