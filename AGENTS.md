# OpsCore — Engineering Rules (Coding Agent Prompt)

You are the coding agent for OpsCore.

Build and modify this codebase like a senior backend/product engineer working on a production-grade autonomous back-office system for Indian B2B businesses.

## System Identity

OpsCore is an autonomous back-office platform for Indian B2B businesses. It handles:
1. Document ingestion and extraction
2. Vendor onboarding and trust/risk assessment
3. Compliance monitoring using official and near-official Indian regulatory sources

## Primary Philosophy

> **Default to compiled logic. Use LLM as a last resort.**

This rule is non-negotiable.
- All validation, scoring, classification, trust-tier transitions, and workflow state machines must be deterministic Go whenever possible.
- OCR/LLM should only be used where deterministic logic cannot reliably solve the problem.
- LLM usage must be observable, measurable, and easy to disable or bypass.
- Never introduce AI where a rules engine, parser, validator, checksum, or explicit decision table would do better.

## Tech Stack

- Go 1.25
- `net/http` only, no external HTTP router unless explicitly requested
- PostgreSQL 16 via pgx/v5
- MinIO for object storage (S3-compatible)
- Redis for queueing
- Sarvam AI for OCR + LLM fallback
- OpenRouter for alternate LLM provider (tencent/hy3:free)
- Slack Block Kit for human-in-the-loop approvals
- Docker Compose for local dev
- Google Cloud Run + Supabase/Postgres-style managed infra as production target
- GitHub Actions for CI/CD
- Python/Streamlit for ops/admin UI
- Langfuse for LLM observability

## Architecture

Use Hexagonal Architecture / Ports and Adapters.

```
cmd/server/main.go              -> HTTP server + worker + scheduler bootstrap
cmd/server/worker.go            -> queue worker and DLQ processing
cmd/migrate/main.go             -> migrations entry point

internal/domain/                -> pure business logic, zero I/O
internal/providers/             -> interface definitions only
internal/adapters/              -> concrete implementations for all external systems
internal/agents/                -> workflow orchestrators
tests/integration/              -> infra-backed tests
tests/agentic/                  -> workflow tests with controlled providers
tests/e2e/                      -> full-stack tests
tests/live/                     -> real provider tests (opt-in, low volume)
```

### Architectural Rules

1. `internal/domain` must have zero I/O.
2. Domain code must not import database, HTTP, Redis, MinIO, or SDK code.
3. All external systems must be hidden behind interfaces in `internal/providers`.
4. `internal/adapters` implements provider interfaces and translates between external payloads and domain types.
5. `internal/agents` orchestrates workflows but should not contain raw infrastructure code.
6. `cmd/*` wires dependencies only; it should not contain business rules.
7. Prefer small interfaces, explicit structs, constructor injection.
8. Avoid framework-heavy abstractions.
9. Keep functions small and explicit.
10. Optimize for maintainability, auditability, and testability over cleverness.

## Domain Design Principles

The domain is the product.

Implement these as deterministic domain modules first:
- GSTIN validation: deterministic format/checksum logic before any external lookup
- Invoice arithmetic: validated deterministically
- Risk scoring: explicit weighted rules, not LLM judgments
- Trust tier changes: explicit state machine
- Compliance matching: rule-based checks before LLM reasoning

## Data Validation Strategy

Always validate in layers:
1. Syntactic validation (format, checksum, required fields)
2. Cross-field/business-rule validation (totals, consistency)
3. External/authoritative verification if available
4. Risk/trust downgrade if verification is unavailable or inconsistent

### India-Specific Data Sources

Prefer official or near-official sources:
- GST Developer Portal for GST validation where access is available
- MCA company master data via OGD/data.gov.in
- SEBI official RSS/circular feeds for compliance ingestion
- RBI official RSS/data feeds/DBIE portal for compliance ingestion

If an external source is unavailable: do not silently pass, downgrade trust, preserve audit reason.

## LLM/OCR Policy

LLM and OCR are fallback tools, not the core engine.

**Allowed:**
- OCR when parsing PDFs/images
- LLM only when OCR confidence is below threshold
- LLM only for compliance-gap reasoning requiring contextual interpretation
- LLM-assisted normalization only if deterministic parsing fails

**Not allowed:**
- LLM for basic field validation
- LLM for trust scoring
- LLM for state transitions
- LLM where regex, parsers, decision trees, or explicit rules are sufficient

Every LLM call must:
- Go through a provider interface
- Include traceable metadata
- Record latency, error rate, model name, and token/cost data
- Be easy to stub in tests
- Be easy to disable with config

## Observability

**Logging:** Use slog, structured key-value pairs, never log secrets.

**Metrics:** Track queue depth, retries, DLQ count, OCR fallback rate, LLM invocation rate, provider latency, job completion/failure counts.

**Tracing:** Integrate Langfuse for LLM calls and important agentic steps.

## API Design

- Use net/http, keep handlers thin
- Decode request → call agent/service → encode response
- Use explicit JSON request/response models
- Return clear machine-readable errors
- Support health/status/metrics endpoints
- Use context.Context correctly with reasonable timeouts

## Queue / Worker Design

- Redis-backed queueing
- Retries with capped attempts
- DLQ support
- Jobs must be idempotent
- Workers must tolerate duplicate delivery
- Persist state transitions explicitly
- Recovery behavior must be deliberate and testable

## Human-in-the-Loop

Use human review when:
- OCR confidence is too low
- External verification is unavailable or conflicting
- Risk threshold exceeds approval limits
- Compliance interpretation is uncertain
- Workflow enters ambiguous or high-impact states

## Testing Standard

Testing is a first-class requirement.

**Layers:**
1. Unit tests for `internal/domain`
2. Integration tests for adapters with real infra + stubbed providers
3. Agentic tests for orchestrated workflows
4. Full-stack e2e tests
5. Live-provider tests using real OCR/LLM/third-party APIs (opt-in, low volume)
6. Load/resilience tests

**What tests must prove:**
- Correctness of deterministic rules
- Provider contract correctness
- Workflow orchestration correctness
- Idempotency, retries, DLQ behavior
- Restart safety
- Duplicate-event handling
- UI graceful degradation
- Real provider payload validity and parsing

### Live Provider Test Policy

- Do not make default CI dependent on expensive/flaky live providers
- Keep live tests opt-in (`make test-live`), nightly, or pre-release
- Never assert brittle exact LLM wording
- Assert schema, invariants, required fields, bounded classifications, allowed state transitions
- Run against a staging tenant with low-volume curated datasets

## Security / Reliability Rules

- Validate all inputs
- Sanitize filenames and object keys
- Use context timeouts and bounded retries
- Never trust provider output blindly
- Guard against malformed JSON and partial payloads
- Avoid panic-prone codepaths
- Return explicit errors
- Keep audit trails for decisions and state transitions
- Be careful with PII, tax IDs, and uploaded documents

## Coding Style

- Write idiomatic Go
- Favor composition over inheritance-like patterns
- Be explicit over magical abstractions
- Prefer clear names over clever names
- Keep domain types central and understandable
- Avoid unnecessary dependencies
- Preserve current structure unless there is a strong reason to improve it

## When Implementing Features

Always think in this order:
1. What is the domain rule?
2. Can this be done deterministically?
3. What provider interfaces are needed?
4. What adapters implement them?
5. What workflow agent coordinates the feature?
6. What tests prove correctness?
7. What metrics/logs/traces prove operability?

## TDD Execution Policy

- Before coding, inspect existing files and summarize the current implementation
- Reuse existing types/interfaces where possible
- Do not invent parallel abstractions if one already exists
- For new external integration, add: provider interface → adapter → config/env → tests → metrics/logging hooks
- For new workflow, add: domain invariants → orchestration logic → failure paths → idempotency strategy → observability

## Success Criteria

A good solution for OpsCore is:
- Deterministic first
- Easy to test
- Easy to reason about
- Observable in production
- Resilient to bad external dependencies
- India-specific where it matters
- Scalable by replication and queue-based decoupling
- Honest about where LLMs help and where they should stay out

## Accumulated Learnings

| Date | Task | Learning |
|------|------|----------|
| 2026-07-09 | Live provider contract tests for Sarvam OCR/LLM | Use `//go:build live` build tag for opt-in live tests; they never compile in normal builds. Rate-limit budget in `tests/live/budget.go` (no build tag) enforces max 3 real API calls per test run. The `live` package is always compiled but only consumed by `-tags=live` tests. Sarvam OCR adapter accepts file paths directly via `Extract(ctx, path)`. Assert schema invariants (required fields, structure) — never brittle LLM wording in live tests. Debug dump to `/tmp/opscore_live_*.json` with `live.DebugDump()`. `TestSarvamOCRLive` consumes 1 budget call, `TestSarvamOCREdgeCases` consumes 1, `TestSarvamLLMExtractFields` consumes 1, `TestSarvamLLMChat` consumes 1 = max 4 test functions, budget of 3 means at most 3 will run per invocation, last one skips with BudgetExhaustedError.
| 2026-07-09 | TDD verification pass on `internal/domain/` | All domain source files now have test coverage: added `batch_job_test.go` (BatchJobFromJob), `chunker_test.go` (ShouldChunk, ChunkDocument). Expanded `trust_battery_test.go` (AllChecksPass, TransactionThresholdMet, DisputeFiled, InactivityDecay, ScoreBasedTier, ToMap, TrustBatteryFromMap). Expanded `india_validator_test.go` (IsValidGSTStateCode, IndiaValidator.ValidateVendor, IndiaValidator.ValidateAll, ValidationResult.HasErrors/HasWarnings). Total: 35 top-level test functions, ~180 subtests, all pass with -race. Use `TestChunkDocument_EmptyText` pattern to verify nil vs empty-slice behavior for document processing. All `t.Errorf` format strings must have matching arg counts — extra args cause build failures caught only at compile time.
| 2026-07-09 | Live contract tests for Slack + compliance feeds | Added `live.PostJSON`, `live.GetJSON`, `live.MeasureLatency` helpers in `tests/live/budget.go` (imports: `context`, `bytes`, `io`, `net/http`). Slack test uses `live.PostJSON` to call `https://slack.com/api/chat.postMessage`. Compliance feeds test uses raw `net/http` to verify SEBI (`https://www.sebi.gov.in/rss.html`) and RBI (`https://rbi.org.in/rss/rss.aspx`) RSS endpoints. Budget of 3 means at most 3 of the 4 total live test functions across Slack + compliance_feeds will run per invocation; the rest skip with BudgetExhaustedError. New packages `tests/live/slack` and `tests/live/compliance_feeds` with `//go:build live`. Helper functions must import `context` in budget.go to compile — the `context.Context` type is needed by PostJSON/GetJSON signatures.
