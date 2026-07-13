# ADR-0008: Retrieval Router — Deterministic, Vectorless, Hybrid Query Classification and Routing

## Status

**Proposed** — ADR-001 defines the retrieval architecture direction but the router itself is not yet implemented.

## Date

2026-07-13

## Context

### Problem Statement

ADR-001 defines a three-tier retrieval funnel (metadata filter → structured index → FTS → vector search) for compliance document search. However, ADR-001 does not define the **router** that decides which tier to use for a given query. Currently, the compliance agent (`internal/agents/compliance_agent.go`) bypasses all retrieval tiers: it loads all compliance chunks from the database and sends them to the LLM for gap analysis. This approach has critical limitations:

1. **Does not scale beyond ~20 chunks.** The `ProcessCompliance` method stores chunks via `StoreComplianceChunks` and then analyzes them all at once. For a tenant with hundreds of regulatory documents, this pattern exceeds LLM context windows and incurs unnecessary token costs.

2. **No query classification.** All compliance queries are treated the same way — "scrape and analyze" via `job.JobType == "scrape"` or `"analyze"`. There is no classification of query intent: is this an exact-match question, a metadata query, or a fuzzy semantic search?

3. **No tier selection.** The compliance agent does not consult a structured index, FTS, or vector store. It loads everything and sends everything to the LLM. ADR-001's three-tier funnel exists in the architecture diagram but has no concrete router implementation.

4. **No confidence thresholds.** There is no configurable threshold that determines when a tier's results are "good enough" or when the system should escalate to the next tier or to HITL.

5. **No observability into retrieval quality.** The current code tracks `chunks_created` and `items_processed` but does not track which retrieval tier was used, the confidence of results, or the zero-result rate.

### Current Architecture

Before this decision, retrieval is a monolithic pass-through:

```
Compliance Job (scrape)
    │
    ▼
┌──────────────────────────────────────┐
│  ComplianceAgent.ProcessCompliance    │
│  (internal/agents/compliance_agent.go)│
│                                        │
│  case "scrape":                        │
│    chunkText(content, 500)             │
│    StoreComplianceChunks()             │
│    → stores all chunks as Documents    │
│                                        │
│  case "analyze":                       │
│    load all items                      │
│    AnalyzeCompliance(ctx, items)       │
│    → sends ALL items to LLM            │
└──────────────────────────────────────┘
```

There is no router, no tier selection, no confidence check, and no escalation path.

### Requirements

1. **Query classification** — every query must be classified into one of three paths: deterministic (no RAG), vectorless retrieval, or hybrid search. Classification must be deterministic (regex/rules-based), not LLM-based.

2. **Configurable confidence thresholds** — each tier must have per-tenant configurable thresholds that determine when results are sufficient and when to escalate to the next tier or to HITL.

3. **Router state machine** — the router must implement a deterministic state machine with escalation paths: low confidence in Tier 1 → escalate to Tier 2; low confidence in Tier 2 → escalate to Tier 3; all tiers low confidence → escalate to HITL.

4. **Integration with compliance agent** — the compliance agent must call the router instead of loading all chunks. The router returns the best available context for LLM gap analysis.

5. **Metrics** — the router must track tier utilization, per-tier latency, result count, zero-result rate, and escalation rate.

6. **Follow ADR-001's three-tier funnel** — the router implements the funnel defined in ADR-001: metadata filter → structured index → FTS → (future) vector search.

## Decision

Implement a **deterministic retrieval router** in `internal/agents/router_agent.go` that classifies queries into one of three paths using regex/rules-based intent classification, routes to the appropriate tier(s), and escalates based on configurable confidence thresholds.

### Architecture Overview

```
User Query (string)
    │
    ▼
┌──────────────────────────────────────────────┐
│  Query Classifier                              │
│  (internal/domain/query_classifier.go)         │
│                                                 │
│  Regex/rules-based:                             │
│    count/HIGH/LOW/severity → metadata query     │
│    Section/section/clause/exact ref → exact     │
│    what/why/how/explain/semantic → fuzzy        │
└──────────────────┬───────────────────────────┘
                   │
                   ▼
┌──────────────────────────────────────────────┐
│  Router State Machine                         │
│  (internal/domain/retrieval_router.go)         │
│                                                 │
│  Intent → tier selection → confidence check    │
│  Low confidence → escalate to next tier         │
│  All tiers low → HITL                           │
└──────────────────┬───────────────────────────┘
    ┌──────────────┼──────────────┐
    ▼              ▼              ▼
┌─────────┐  ┌────────────┐  ┌──────────────┐
│ Path 1: │  │ Path 2:    │  │ Path 3:      │
│ Metadata │  │ Vectorless │  │ Hybrid       │
│ Only    │  │ (Structured│  │ (FTS +       │
│         │  │  + FTS)    │  │  pgvector)   │
└─────────┘  └────────────┘  └──────────────┘
    │              │              │
    ▼              ▼              ▼
┌──────────────────────────────────────────────┐
│  Confidence Check                              │
│  (per-tier threshold)                          │
│                                                 │
│  Above threshold → return results               │
│  Below threshold → escalate to next tier        │
│  All tiers exhausted → HITL                     │
└──────────────────────────────────────────────┘
```

### Path 1: Deterministic (No RAG)

For queries that can be answered from metadata alone, without any document content retrieval.

**Query patterns classified as Path 1:**

| Pattern | Example | Matches |
|---------|---------|---------|
| `count` keyword | "how many HIGH-severity gaps this quarter?" | Metadata aggregation |
| Severity filter | "show all HIGH severity items" | `severity == "HIGH"` |
| Status inquiry | "what is the status of vendor V-123?" | Vendor status lookup |
| Count by source | "how many SEBI circulars in Q3?" | Source + date aggregation |
| Tier inquiry | "which vendors are in PROBATION?" | TrustTier filter |

**Routing logic:**

```go
// internal/domain/query_classifier.go

type QueryIntent int

const (
    IntentMetadataOnly   QueryIntent = iota // Path 1: deterministic, no RAG
    IntentVectorless                        // Path 2: structured index + FTS
    IntentHybrid                            // Path 3: FTS + vector search
)

type QueryClassification struct {
    Intent       QueryIntent
    Confidence   float64           // classification confidence (0.0-1.0)
    Filters      map[string]string // extracted metadata filters
    RawQuery     string
}

// ClassifyQuery determines the intent of a search query using regex rules.
func ClassifyQuery(query string) *QueryClassification {
    lower := strings.ToLower(query)

    // Path 1 patterns: metadata-only queries
    if hasCountPrefix(lower) || hasSeverityFilter(lower) || hasStatusInquiry(lower) {
        return &QueryClassification{
            Intent:     IntentMetadataOnly,
            Confidence: 0.95,
            Filters:    extractMetadataFilters(lower),
            RawQuery:   query,
        }
    }

    // Path 2 patterns: exact section/clause references
    if hasExactSectionRef(lower) || hasClauseReference(lower) || hasCircularRef(lower) {
        return &QueryClassification{
            Intent:     IntentVectorless,
            Confidence: 0.90,
            RawQuery:   query,
        }
    }

    // Path 3 patterns: fuzzy semantic queries
    if hasSemanticIntent(lower) || hasExplanationQuery(lower) || hasComparisonQuery(lower) {
        return &QueryClassification{
            Intent:     IntentHybrid,
            Confidence: 0.70, // lower confidence — may need escalation
            RawQuery:   query,
        }
    }

    // Default: vectorless (structured + FTS) — conservative fallback
    return &QueryClassification{
        Intent:     IntentVectorless,
        Confidence: 0.60,
        RawQuery:   query,
    }
}
```

### Path 2: Vectorless Retrieval (Structured Index + FTS)

For queries that require document content but can be navigated via structured index and full-text search, without vector embeddings.

**Query patterns classified as Path 2:**

| Pattern | Example | Matches |
|---------|---------|---------|
| Section reference | "what does Section 5.2 say about late fees?" | Exact section lookup |
| Clause reference | "Clause 14 of the vendor agreement" | Clause navigator |
| Circular reference | "find Circular No. SEBI/HO/2026/45" | Circular number lookup |
| Heading match | "show me the 'Penalties' section" | Heading-based navigation |
| Phrase search | "garnishment of bank accounts per GST Act" | FTS phrase query |

**Tier 2a: Structured Index Navigation:**

The structured index (built per ADR-001, Phase 2) is a hierarchical TOC stored as JSONB. The router navigates this index first:

1. Extract section/clause/heading reference from query via regex
2. Look up section in TOC index (`WHERE tenant_id = ? AND document_id IN (SELECT id FROM documents WHERE toc_index @> ?)`)
3. Return section content if exact match found
4. If no exact match, fall through to FTS

**Tier 2b: PostgreSQL FTS:**

When structured index navigation fails or the query is not a section reference, the router falls through to PostgreSQL full-text search:

1. Parse query into `tsquery` with phrase search (`<->` operator)
2. Search `compliance_chunks` with `tsv_content @@ tsquery`
3. Rank by `ts_rank_cd`
4. Apply confidence check against threshold

```go
// internal/domain/retrieval_router.go

type RouterState int

const (
    RouterStateStart           RouterState = iota
    RouterStateMetadataOnly
    RouterStateStructuredIndex
    RouterStateFTS
    RouterStateVectorSearch
    RouterStateLLM
    RouterStateHITL
    RouterStateComplete
)

// RouteResult represents the outcome of a routing decision.
type RouteResult struct {
    Tier           string            // "metadata" | "structured" | "fts" | "vector" | "llm" | "hitl"
    Confidence     float64           // confidence in the results (0.0-1.0)
    Results        []domain.ComplianceChunk
    LatencyMs      int64
    EscalationPath []string          // which tiers were tried before this one
}

// Route determines the retrieval path for a classified query.
func (r *RetrievalRouter) Route(ctx context.Context, tenantID string, classification *QueryClassification) (*RouteResult, error) {
    switch classification.Intent {
    case IntentMetadataOnly:
        return r.routeMetadataOnly(ctx, tenantID, classification)
    case IntentVectorless:
        return r.routeVectorless(ctx, tenantID, classification)
    case IntentHybrid:
        return r.routeHybrid(ctx, tenantID, classification)
    default:
        return r.routeVectorless(ctx, tenantID, classification)
    }
}
```

### Path 3: Hybrid Search (FTS + pgvector)

For fuzzy semantic queries where FTS recall is insufficient. Deferred to Phase 3 per ADR-001 but the router must define the path now to enable clean escalation.

**Query patterns classified as Path 3:**

| Pattern | Example | Matches |
|---------|---------|---------|
| Fuzzy concept | "find circulars about price manipulation" | Semantic concept, not exact term |
| Cross-document | "find all circulars related to SEBI's insider trading guidelines" | Cross-document similarity |
| Explanation | "explain the compliance implications of the new GST rules" | Requires reasoning over multiple docs |
| Comparison | "how does this differ from the previous circular?" | Comparative semantic search |

**Hybrid search combines:**

1. FTS score (`ts_rank_cd`) — weight α (configurable, default 0.6)
2. Vector cosine similarity — weight β (configurable, default 0.4)
3. Reciprocal rank fusion for merging result sets

### Router State Machine

The router implements a deterministic state machine with escalation:

```
Query → Classify Intent
    │
    ▼
┌─────────────────────────────────────────────────────────┐
│  Path 1: Metadata Only                                   │
│  ┌──────────────────────────────────────────────────┐    │
│  │  Apply metadata filters                            │    │
│  │  Return aggregated result                          │    │
│  │  No document content retrieval needed              │    │
│  └──────────────────────────────────────────────────┘    │
│  Confidence >= 0.95 → return result                      │
│  Confidence < 0.95 → escalate to Path 2                  │
└─────────────────────────────────────────────────────────┘
    │ escalate
    ▼
┌─────────────────────────────────────────────────────────┐
│  Path 2a: Structured Index                               │
│  ┌──────────────────────────────────────────────────┐    │
│  │  Navigate TOC by section/clause/heading           │    │
│  │  Return section content if exact match            │    │
│  └──────────────────────────────────────────────────┘    │
│  Exact match found → return with confidence 0.95         │
│  No exact match → fall through to FTS                    │
└─────────────────────────────────────────────────────────┘
    │ fall through
    ▼
┌─────────────────────────────────────────────────────────┐
│  Path 2b: PostgreSQL FTS                                 │
│  ┌──────────────────────────────────────────────────┐    │
│  │  tsquery parse → GIN index scan                  │    │
│  │  ts_rank_cd ranking                               │    │
│  │  Return top-K chunks                              │    │
│  └──────────────────────────────────────────────────┘    │
│  Confidence >= threshold (default 0.7) → return          │
│  Confidence < threshold → escalate to Path 3             │
└─────────────────────────────────────────────────────────┘
    │ escalate
    ▼
┌─────────────────────────────────────────────────────────┐
│  Path 3: Hybrid Search (FTS + Vector)                    │
│  ┌──────────────────────────────────────────────────┐    │
│  │  FTS score * α + vector cosine * β               │    │
│  │  Reciprocal rank fusion                           │    │
│  │  Return fused top-K chunks                        │    │
│  └──────────────────────────────────────────────────┘    │
│  Confidence >= threshold (default 0.6) → return          │
│  (Phase 3 — returns empty if vector not deployed)        │
└─────────────────────────────────────────────────────────┘
    │ escalate
    ▼
┌─────────────────────────────────────────────────────────┐
│  All tiers exhausted → HITL                               │
│  ┌──────────────────────────────────────────────────┐    │
│  │  Create HITLRequest with reason:                   │    │
│  │  "retrieval_router_low_confidence"                 │    │
│  │  Include tried tiers + confidence values           │    │
│  │  Operator reviews and provides context             │    │
│  └──────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────┘
```

### Why Deterministic Classification Instead of LLM-Based Routing

1. **Query classification is a simple regex problem.** "How many", "Section", "explain", "find" — these are lexical patterns, not semantic understanding. Regex handles them with 100% determinism.

2. **No LLM cost for routing.** LLM-based routing would add $0.001-0.005 per query in token costs. At 10,000 queries/tenant/month, that's $10-50/month per tenant for routing — more than the actual retrieval cost.

3. **Deterministic routing is auditable.** Every routing decision maps to a specific regex pattern. There is no "model drift" in routing behavior.

4. **Faster than LLM routing.** Regex classification completes in < 1µs. LLM routing takes 500-2000ms.

### Why Confidence Thresholds Are Configurable Per-Tenant

Different tenants have different tolerance for retrieval quality vs. cost:

| Tenant Profile | Metadata Threshold | FTS Threshold | Hybrid Threshold | Expected Behavior |
|----------------|-------------------|---------------|------------------|-------------------|
| **Strict compliance** (bank, NBFC) | 0.90 | 0.80 | 0.70 | Escalate aggressively; prefer HITL over low-confidence results |
| **Standard** (mid-market) | 0.85 | 0.70 | 0.60 | Balance cost and quality |
| **High-volume** (e-commerce) | 0.80 | 0.60 | 0.50 | Prefer speed over precision; accept lower-confidence results |

### Specific Technology Choices

| Component | Choice | Rationale |
|-----------|--------|-----------|
| **Query classifier** | `internal/domain/query_classifier.go` — regex/rules-based | Deterministic, < 1µs, no LLM dependency |
| **Router state machine** | `internal/domain/retrieval_router.go` — explicit state enum with escalation | Follows existing `TrustBattery` / `JobStatus` state machine pattern |
| **Confidence thresholds** | `internal/domain/retrieval_config.go` — per-tenant config struct | Extensible from hardcoded constants; stored in tenant config table |
| **Structured index** | TOC index stored as JSONB (ADR-001 Phase 2) | Reuses ADR-001 structured index; no new storage |
| **Full-text search** | PostgreSQL `tsvector` / `tsquery` with GIN index (ADR-001 Phase 1) | Already implemented; router uses the `SearchProvider` interface |
| **Vector search** | pgvector (ADR-001 Phase 3, deferred) | Router defines the path but no implementation until Phase 3 gating |
| **HITL escalation** | Existing `providers.HITLProvider` + Slack Block Kit | Reuses ADR-0002's HITL infrastructure |
| **Metrics** | `internal/telemetry/metrics.go` — new counter keys per tier | Existing telemetry; new keys for tier utilization, latency, escalation |

## Alternatives Considered

### 1. LLM-Based Query Routing

Rejected because:
- Violates the "deterministic first, LLM last" principle — query classification is a lexical pattern problem, not a reasoning problem.
- Adds $10-50/month per tenant in routing costs.
- Routing behavior could drift with model updates or prompt changes.
- Regex handles all query patterns in the compliance domain with 100% determinism.

### 2. Always-Hybrid (Always Use All Tiers)

Rejected because:
- Path 1 (metadata-only) queries incur unnecessary FTS/vector cost for questions like "how many HIGH-severity items?"
- Path 2 queries (section references) get no benefit from vector search — exact section lookups are deterministic.
- Vector search (Phase 3) has embedding cost per document — unnecessary for the 60-70% of queries that Path 1-2 handle.
- ADR-001 explicitly defines a "vectorless-first" philosophy that always-hybrid violates.

### 3. Always-Vectorless (Never Use Vector Search)

Rejected because:
- FTS handles phrase search well but semantic similarity poorly — "circulars about price manipulation" may miss relevant documents that use different terminology (e.g., "market rigging", "price fixing").
- ADR-001 Phase 3 explicitly gates vector search on data volume proving FTS recall insufficient.
- The router defines the hybrid path for future use but does not implement it until Phase 3.

### 4. Rule-Based Router Without State Machine

Rejected because:
- Without a state machine, escalation paths are implicit and hard to audit.
- The state machine provides a clear contract for which tiers are tried, in what order, and what happens at each confidence level.
- The state machine pattern is already established in OpsCore (`TrustBattery`, `JobStatus`) — reusing the pattern reduces cognitive overhead.

## Consequences

### Benefits

1. **Optimized cost/latency per query type.** Metadata-only queries (Path 1) complete in < 10ms with zero LLM or FTS cost. Vectorless queries (Path 2) use structured index + FTS but no embeddings. Hybrid queries (Path 3) only run when FTS confidence is insufficient. Expected distribution: ~20% Path 1, ~60% Path 2, ~20% Path 3 (for production tenants with vector search enabled).

2. **Deterministic routing is auditable and testable.** Every routing decision maps to a regex pattern and a confidence comparison. Router behavior is reproducible across test runs, environments, and model versions.

3. **Configurable thresholds enable tenant-specific tuning.** A strict compliance tenant (bank, NBFC) can set higher thresholds to prefer HITL over low-confidence results. A high-volume tenant can set lower thresholds to optimize throughput.

4. **Reuses existing ADR-001 infrastructure.** The structured index, FTS, and (future) pgvector are defined in ADR-001. The router simply orchestrates access to them.

5. **HITL escalation leverages ADR-0002 patterns.** The existing `HITLProvider` interface, Slack Block Kit integration, and `HITLRequest` persistence are reused without modification.

6. **Metrics enable continuous improvement.** Tier utilization data reveals whether the query classifier patterns are accurate (e.g., if Path 1 queries frequently escalate to Path 2, the classification rules need tuning).

### Trade-offs / Risks

1. **More complex routing logic.** The router adds a new component (classifier + state machine + threshold config) that must be maintained alongside the compliance agent. Mitigation: the router is a thin orchestrator that delegates to existing domain types (TOC index, SearchProvider, HITLProvider). Estimated ~300 lines of core logic.

2. **Eval dataset needed for threshold tuning.** Confidence thresholds are initially set to sensible defaults (0.95 metadata, 0.7 FTS, 0.6 hybrid) but optimal thresholds depend on real query distributions. Mitigation: build an eval query set (ADR-001 Phase 4) in parallel with the router; start with conservative thresholds and tune based on observed escalation rates.

3. **Hybrid path is a stub until Phase 3.** The router defines the Path 3 escalation but cannot execute it until pgvector is deployed. If Path 2 confidence is below threshold and Path 3 is a no-op, the router escalates to HITL. Mitigation: alert on hybrid escalation rate — if it exceeds 5% of queries without vector search deployed, prioritize Phase 3.

4. **Query classifier regex patterns may miss domain-specific queries.** Indian regulatory queries may use non-standard phrasing (e.g., "circular pertaining to late fee for delayed GSTR-3B"). Mitigation: the classifier has a fallback to Path 2 (vectorless) for unclassified queries; patterns are stored in a configurable rules list that can be extended without code changes.

5. **Latency varies by path.** Path 1: < 10ms. Path 2a (structured index): < 50ms. Path 2b (FTS): < 200ms. Path 3 (hybrid): < 500ms. Path escalation compounds latency. Mitigation: the router starts from the cheapest path and escalates only when necessary; total query latency is bounded by the HITL escalation timeout (30s).

## Implementation Details

### Router State Machine Transitions

```
START ──→ CLASSIFY
             │
             ├── IntentMetadataOnly ──→ METADATA_ONLY ──→ (confidence >= threshold?) ──→ COMPLETE
             │                                                 │ no
             │                                                 ▼
             │                                            STRUCTURED_INDEX
             │
             ├── IntentVectorless ──→ STRUCTURED_INDEX ──→ (exact match?) ──→ COMPLETE
             │                              │ no
             │                              ▼
             │                           FTS_SEARCH ──→ (confidence >= threshold?) ──→ COMPLETE
             │                                                 │ no
             │                                                 ▼
             │                                            VECTOR_SEARCH (if available)
             │                                                 │ no
             │                                                 ▼
             │                                            HITL
             │
             └── IntentHybrid ──→ FTS_SEARCH ──→ VECTOR_SEARCH ──→ FUSE ──→ (confidence >= threshold?) ──→ COMPLETE
                                                                                       │ no
                                                                                       ▼
                                                                                  HITL
```

### Integration with Compliance Agent

The compliance agent's `ProcessCompliance` method is updated to use the router:

```go
// Updated ProcessCompliance flow
func (a *ComplianceAgent) ProcessCompliance(ctx context.Context, job *ComplianceJob) (map[string]any, error) {
    result := map[string]any{"source_name": job.SourceName}

    switch job.JobType {
    case "scrape":
        // Existing chunk storage logic (unchanged)
        result["items_processed"] = len(job.Items)

    case "analyze":
        // NEW: Use retrieval router instead of loading all chunks
        classification := ClassifyQuery(job.SourceName + " " + strings.Join(itemTitles(job.Items), " "))
        routeResult, err := a.router.Route(ctx, job.TenantID, classification)
        if err != nil {
            return nil, fmt.Errorf("retrieval routing failed: %w", err)
        }

        // Pass retrieved context (not all chunks) to LLM gap analysis
        if routeResult.Tier == "hitl" {
            // Create HITL request with router metadata
            a.createHITLForRouter(ctx, job, routeResult)
            result["status"] = "awaiting_hitl"
            result["route_result"] = routeResult
        } else {
            context := buildLLMContext(routeResult.Results)
            gapAnalysis, err := a.AnalyzeCompliance(ctx, "default policy", context)
            if err == nil {
                result["gap_analysis"] = gapAnalysis
            }
            result["tier_used"] = routeResult.Tier
            result["confidence"] = routeResult.Confidence
        }

    default:
        result["status"] = "unknown job type"
    }

    return result, nil
}
```

### File Map

**Existing Files Modified:**

| File | Change |
|------|--------|
| `internal/agents/compliance_agent.go` | Integrate `RetrievalRouter` — call `Route()` instead of loading all chunks; pass retrieved context to `AnalyzeCompliance` |
| `internal/providers/interfaces.go` | Add `RetrievalProvider` interface (or extend `SearchProvider` to include classification metadata) |
| `internal/telemetry/metrics.go` | Add counter keys: `tier_metadata_count`, `tier_structured_count`, `tier_fts_count`, `tier_hybrid_count`, `tier_hitl_count`, `tier_escalation_rate`, `tier_latency_ms`, `tier_zero_result_count` |

**New Files:**

| File | Purpose |
|------|---------|
| `internal/domain/query_classifier.go` | Regex/rules-based query classification; `ClassifyQuery()`, `QueryIntent` enum, `QueryClassification` struct |
| `internal/domain/retrieval_router.go` | Router state machine; `Route()`, `RouterState` enum, `RouteResult` struct, tier selection and escalation |
| `internal/domain/retrieval_config.go` | Configurable confidence thresholds; `RetrievalConfig` struct, per-tenant overrides, `DefaultRetrievalConfig()` |
| `internal/agents/router_agent.go` | Orchestration wrapper for compliance agent integration; calls classifier → router → returns context for LLM |
| `tests/unit/query_classifier_test.go` | Unit tests for `ClassifyQuery`: metadata patterns, exact refs, semantic queries, fallback behavior |
| `tests/unit/retrieval_router_test.go` | Unit tests for `Route`: tier selection by intent, confidence escalation, HITL fallback, all-tiers-low |
| `tests/unit/retrieval_config_test.go` | Unit tests for config: default values, per-tenant overrides, threshold validation |
| `tests/agentic/retrieval_agent_test.go` | Agentic tests: compliance agent with router, tier utilization metrics, HITL on low confidence |

## Compliance with OpsCore Principles

### "Deterministic first, LLM last"

The retrieval router explicitly encodes this:

- **Query classification**: Pure Go regex — no LLM involved.
- **Tier selection**: Explicit state machine with deterministic escalation — no LLM involved.
- **Confidence checking**: Numeric comparison against configurable thresholds — no LLM involved.
- **Structured index navigation**: TOC lookup by section/heading ID — no LLM involved.
- **FTS ranking**: `ts_rank_cd` — deterministic PostgreSQL ranking — no LLM involved.
- **Vector search (Phase 3)**: Cosine similarity — deterministic math — no LLM involved.
- **LLM**: Invoked only after the router returns context for gap analysis. The router itself never calls an LLM.

### Multi-tenant isolation

- All tier queries include `WHERE tenant_id = $1` as the first filter (inherited from ADR-001).
- Confidence thresholds are configurable per-tenant via `RetrievalConfig` stored in tenant settings.
- The router passes `TenantID` through every tier invocation.
- HITL requests created by the router include `TenantID` and are scoped accordingly.
- Metrics are tagged with `tenant_id` for per-tenant observability.

### Observability

- Every routing decision records: classification intent, tier used, confidence, latency, result count.
- Tier utilization counters: `tier_metadata_count`, `tier_structured_count`, `tier_fts_count`, `tier_hybrid_count`, `tier_hitl_count` — tracked per tenant.
- Escalation rate: ratio of queries that escalate to the next tier or to HITL.
- Zero-result rate: queries where a tier returned zero results before escalation.
- HITL requests created by the router include `reason: "retrieval_router_low_confidence"` and the list of tried tiers with confidence values.
- All metrics are exposed via the existing `internal/telemetry/metrics.go` infrastructure.

## Related ADRs

- **ADR-001**: Retrieval Architecture for Compliance & Document Search — defines the three-tier funnel (metadata filter → structured index → FTS → vector) that this ADR implements as a concrete router.
- **ADR-0002**: Vendor Onboarding — establishes the HITL provider interface and Slack Block Kit integration that this ADR reuses for router escalation.
- **ADR-0014**: Agentic AI Verification Strategy — the router's tier selection and confidence checks will be verified by the groundedness validation layer (Layer 2) to ensure LLM gap analysis claims are supported by retrieved context.
