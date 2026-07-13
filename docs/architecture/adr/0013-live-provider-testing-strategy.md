# ADR-0013: Live-Provider Testing Strategy

## Status

**Proposed** — live tests exist for Sarvam OCR/LLM, Slack, and compliance feeds. The pattern is established but not yet documented as an architectural decision.

## Date

2026-07-13

## Context

### Problem Statement

OpsCore integrates with multiple external providers: Sarvam AI (OCR + LLM), OpenRouter (LLM), Slack (HITL notifications), SEBI RSS (compliance feeds), RBI RSS (compliance feeds), and GST/MCA APIs (vendor verification).

Agentic and integration tests use stubs and mocks for all providers. This is intentional — tests must be fast, deterministic, and runnable without network access or API keys. However, stubs have fundamental limitations:

1. **Stubs cannot reproduce real provider payload shapes.** A Sarvam OCR stub returns a fixed response. The real Sarvam API may return different field names, nesting, or error structures. If the real response changes, the stub still passes.

2. **Stubs cannot reproduce real error modes.** Provider APIs return rate-limit errors (429), server errors (500), timeout errors, and malformed responses. Stubs typically return success or a single error type.

3. **Stubs cannot measure real latency.** The production system must handle provider latency variance (500ms to 30s for Sarvam OCR). Stubs respond in microseconds.

4. **Stubs mask provider API drift.** If Sarvam changes their OCR response schema, the stub is unaffected. The first production request may fail with a deserialization error.

5. **Stubs cannot validate API key or authentication configuration.** A misconfigured `SARVAM_API_KEY` environment variable is only caught on the first real API call.

### Current State

| Provider | Test Coverage | Live Tests |
|----------|---------------|------------|
| Sarvam OCR | Stub in `tests/agentic/` | `tests/live/sarvam_ocr_test.go` (//go:build live) |
| Sarvam LLM | Stub in `tests/agentic/` | `tests/live/sarvam_llm_test.go` (//go:build live) |
| OpenRouter | Stub in `tests/agentic/` | None yet |
| Slack | Stub in `tests/agentic/` | `tests/live/slack/slack_hitl_test.go` (//go:build live) |
| SEBI RSS | No stub (HTTP fetch) | `tests/live/compliance_feeds/sebi_test.go` (//go:build live) |
| RBI RSS | No stub (HTTP fetch) | `tests/live/compliance_feeds/rbi_test.go` (//go:build live) |
| GST developer portal | No stub | None yet |
| MCA via data.gov.in | No stub | None yet |

### Requirements

1. Live tests must be opt-in — they never run in default CI or `go test ./...`.
2. Live tests must be rate-limited to prevent accidental API budget exhaustion.
3. Live tests must assert schema invariants, not brittle LLM wording.
4. Live tests must fail meaningfully: schema validation failure vs API auth failure vs rate limit vs network error.
5. Live tests must be low-volume — at most a handful of real API calls per test run.
6. Live tests must support debug dumping of raw responses for manual inspection.

## Decision

### Primary Decision: Opt-In Live Tests with `//go:build live` Build Tag

All live provider tests use Go build tags to ensure they are excluded from normal builds and test runs:

```go
//go:build live

package live_sarvam

import "testing"

func TestSarvamOCRLive(t *testing.T) {
    // ...
}
```

Running live tests requires an explicit tag:

```bash
go test -tags=live ./tests/live/...
```

### Rate-Limited Budget

A shared budget mechanism in `tests/live/budget.go` limits real API calls per test invocation:

```go
// tests/live/budget.go
package live

import (
    "context"
    "fmt"
    "sync/atomic"
)

var callBudget int32 = 3 // max 3 real API calls per test run

// ConsumeBudget decrements the budget and returns an error if exhausted.
func ConsumeBudget() error {
    remaining := atomic.AddInt32(&callBudget, -1)
    if remaining < 0 {
        return fmt.Errorf("budget exhausted: max %d real API calls per run", capBudget)
    }
    return nil
}

type BudgetExhaustedError struct{}

func (e BudgetExhaustedError) Error() string {
    return "live test budget exhausted"
}
```

Test functions call `ConsumeBudget()` at the start. If budget is exhausted, the test skips:

```go
func TestSarvamOCRLive(t *testing.T) {
    if err := live.ConsumeBudget(); err != nil {
        t.Skip(err)
    }
    // ... real API call
}
```

### Assertion Philosophy: Schema Over Wording

Live tests assert structural invariants, not exact LLM output:

**Do assert:**
- Response HTTP status is 200
- Response body is valid JSON
- Required fields are present and non-empty
- Field types match expectations (string, number, array)
- Enum values are within expected bounds
- Latency is within acceptable range (logged, not failed)

**Do not assert:**
- Exact LLM response wording
- Order of returned fields
- Specific numeric values (unless deterministic, e.g., token count > 0)

Example:

```go
func TestSarvamOCRLive(t *testing.T) {
    if err := live.ConsumeBudget(); err != nil {
        t.Skip(err)
    }

    result, err := sarvamOCR.Extract(ctx, "testdata/invoice.pdf")
    if err != nil {
        t.Fatalf("Sarvam OCR extract failed: %v", err)
    }

    // Assert schema invariants
    if result.Text == "" {
        t.Error("extracted text must not be empty")
    }
    if result.Confidence <= 0 || result.Confidence > 1.0 {
        t.Errorf("confidence out of range [0,1]: %f", result.Confidence)
    }
    if result.Pages <= 0 {
        t.Errorf("pages must be positive: %d", result.Pages)
    }

    // Assert required fields are present (but not their wording)
    if result.Metadata == nil {
        t.Error("metadata must not be nil")
    }
}
```

### Debug Dump to `/tmp/opscore_live_*.json`

All live tests dump raw request and response payloads for offline debugging:

```go
// tests/live/debug.go
package live

import (
    "encoding/json"
    "os"
    "path/filepath"
    "time"
)

func DebugDump(name string, data any) {
    dump := map[string]any{
        "test":      name,
        "timestamp": time.Now().UTC(),
        "data":      data,
    }
    path := filepath.Join("/tmp", fmt.Sprintf("opscore_live_%s_%d.json",
        name, time.Now().UnixNano()))
    f, _ := os.Create(path)
    defer f.Close()
    json.NewEncoder(f).Encode(dump)
}
```

### Live Test Layout

```
tests/live/
├── budget.go              # Budget counter, ConsumeBudget(), BudgetExhaustedError
├── debug.go               # DebugDump() helper
├── testdata/              # Test fixtures (sample PDF, invoice image)
│   ├── invoice.pdf
│   ├── pan_card.png
│   └── gst_certificate.pdf
├── sarvam_ocr_test.go     # Sarvam OCR live tests
├── sarvam_llm_test.go     # Sarvam LLM live tests
├── openrouter_llm_test.go # OpenRouter LLM live tests
├── slack/
│   └── slack_hitl_test.go # Slack HITL live tests
└── compliance_feeds/
    ├── sebi_test.go       # SEBI RSS feed live tests
    └── rbi_test.go        # RBI RSS feed live tests
```

### What Tests Exist (or Should Exist)

| Test Function | Budget Cost | What It Validates |
|---------------|-------------|-------------------|
| `TestSarvamOCRLive` | 1 | PDF text extraction returns non-empty text, confidence > 0, valid metadata |
| `TestSarvamOCREdgeCases` | 1 | Empty PDF, corrupt file, unsupported format return appropriate errors |
| `TestSarvamLLMExtractFields` | 1 | Structured field extraction returns valid JSON matching requested schema |
| `TestSarvamLLMChat` | 1 | Chat completion returns non-empty response with expected structure |
| `TestOpenRouterLLMReason` | 1 | Reasoning call returns non-empty text with valid metadata (model, tokens) |
| `TestSlackSendApproval` | 1 | Slack Block Kit message sends successfully; returns channel and timestamp |
| `TestSEBIRSSFeed` | 1 | SEBI RSS endpoint returns valid XML; required fields present |
| `TestRBIRSSFeed` | 1 | RBI RSS endpoint returns valid XML; required fields present |
| `TestGSTVerification` | 1 | GST developer portal returns valid response or appropriate auth error |

Budget of 3 means at most 3 of the 9 total test functions run per invocation; the rest skip with `BudgetExhaustedError`.

### Running Live Tests

```bash
# Single test (recommended for development)
go test -tags=live -run TestSarvamOCRLive ./tests/live/

# All live tests (only 3 will actually run due to budget)
go test -tags=live ./tests/live/...

# Nightly run (full budget, no skip)
BUDGET=9 go test -tags=live ./tests/live/...
```

## Alternatives Considered

### 1. All Tests Against Real Providers (No Stubs)

Rejected because:
- Tests would be slow (seconds per API call), non-deterministic (network flakiness), and expensive (API costs).
- CI would fail on network issues, not code issues.
- Developers would need API keys for every provider, creating onboarding friction.
- Rate limits would be hit quickly in CI.

### 2. Record/Replay (e.g., go-vcr)

Rejected because:
- Recorded cassettes are brittle — if the provider response changes, the cassette must be re-recorded.
- Cassettes cannot reproduce error modes or latency variance.
- Cassette size grows with each provider interaction, bloating the repository.
- Schema drift is only detected when cassettes are re-recorded, which is a manual process.

### 3. Contract Tests Only (Pact)

Rejected because:
- Pact requires the provider to host a Pact broker or publish contracts.
- Most providers (Sarvam, OpenRouter, SEBI) do not participate in contract testing.
- Pact adds significant infrastructure for what is essentially a schema validation test.

### 4. No Live Tests (Rely on Production Monitoring)

Rejected because:
- Production monitoring detects failures after they impact users.
- Schema drift is only caught on first production request — too late.
- Misconfigured API keys are only caught at deploy time, not at release time.

## Consequences

### Benefits

1. **Real provider contract verification.** Live tests catch API schema changes, authentication failures, and structural mismatches before production.

2. **Opt-in by default, opt-out for nightly runs.** Developers are not blocked by live test requirements. Nightly runs provide the safety net.

3. **Rate-limited budget prevents accidental cost.** Even running all live tests cannot exceed 3 API calls per invocation (by default). This prevents CI pipeline misconfiguration from generating large bills.

4. **Schema-invariant assertions prevent brittle tests.** Tests fail only when the API contract is violated, not when LLM wording changes. This reduces false positives and maintenance burden.

5. **Debug dumps aid manual investigation.** When a live test fails, the raw request and response are written to `/tmp/opscore_live_*.json` for inspection. No need to add temporary logging or reproduce the issue.

6. **Build tags ensure cleanliness.** Live provider imports never compile into production binaries. No risk of accidentally calling a real API from production code.

### Trade-offs / Risks

1. **Budget of 3 may be too low.** With 9 test functions, only 3 run per invocation. Developers must run multiple invocations to test all providers. Mitigation: developers typically test one provider at a time during development. Nightly runs use a higher budget.

2. **Live tests require API keys in CI.** For nightly runs, the CI pipeline must have access to provider API keys. Mitigation: store keys in GitHub Actions secrets. Run live tests only on `main` branch or nightly schedule, not on PRs from forks.

3. **Provider API changes can break live tests outside business hours.** If Sarvam changes their API at 2 AM, the nightly live test run fails. Operators receive an alert and can investigate. Mitigation: this is by design — better to catch the change at night than during business hours.

4. **Debug dumps may contain PII.** OCR extraction results may contain vendor names, GST numbers, or other PII in the extracted text. Mitigation: debug dumps are local to `/tmp/` and are never committed or shared. Tests should note in comments that dumps contain sensitive data.

5. **`testdata/` files must be realistic but not from real vendors.** Test invoices and PAN cards must resemble real documents without containing real PII. Mitigation: generate synthetic documents with fake names, GST numbers (valid format but not real), and addresses.

## Related ADRs

- **ADR-0009**: Bounded Agent Loops and Fallback Policy — live tests validate that provider retry and fallback behaviors match real provider error modes.
- **ADR-0014**: Agentic AI Verification Strategy — defines the verification gates that live tests feed into for pre-release validation.
- **ADR-0018**: Compliance-Source Ingestion Strategy — compliance feed live tests validate RSS endpoint structure and document parsing.
