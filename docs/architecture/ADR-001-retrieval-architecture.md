# ADR-001: Retrieval Architecture for Compliance & Document Search

## Status

**Accepted** — Phase 1 implementation begins immediately. Phases 2–4 are gated on production data volume and observed search quality metrics.

## Context

### Problem Statement

OpsCore ingests and processes regulatory compliance documents (SEBI circulars, RBI notifications, GST notices) and business documents (invoices, contracts, purchase orders). These documents must be searchable for:

1. **Exact-match queries**: "Show me all circulars referencing section 73 of the CGST Act" — requires precise string/term matching with section-aware navigation.
2. **Metadata-driven queries**: "Find all HIGH-severity compliance gaps from SEBI in Q3 2026" — requires structured filtering on metadata fields.
3. **Fuzzy/semantic queries**: "Which circulars discuss late fee implications for GSTR-3B filing?" — requires understanding of conceptual relationships beyond keyword overlap.
4. **Compliance gap analysis**: "What compliance gaps does this new circular create for my vendor base?" — requires reasoning over retrieved context.

### Current Architecture Limitations

| Area | Current State | Limitation |
|------|--------------|------------|
| **Chunking** | `domain/chunker.go` — fixed 2000-char boundaries, word-split only for compliance agent | Splits tables, loses section boundaries, no heading awareness |
| **Storage** | `compliance_chunks` table with `content TEXT`, no text search index | Only exact-match `LIKE` queries possible; no relevance ranking |
| **Retrieval** | No search API exists; compliance agent does sequential LLM analysis on all chunks | Doesn't scale beyond ~20 chunks; no ability to answer ad-hoc queries |
| **Metadata** | `compliance_chunks` has `source_url`, `source_hash`, `severity`, `document_type` | No extraction of section numbers, regulator, applicability, effective date |
| **Classification** | `domain/document_classifier.go` — keyword-based, looks only at first 500 chars | Misses document structure; cannot extract sections or headings |
| **Tenant isolation** | RLS via `tenant_id` on all tables | Works, but search queries must be optimized for tenant-first filtering |
| **Evaluation** | No retrieval quality metrics | Cannot measure precision, recall, or faithfulness |

### Requirements

1. Support structured (regulatory circulars, contracts) and semi-structured (invoices, notices) documents
2. Enable both exact and fuzzy search across compliance chunks
3. Scale to thousands of documents per tenant without linear degradation
4. Respect tenant isolation at query time (mandatory `tenant_id` filter first)
5. Follow the "deterministic first, LLM last" philosophy — use reasoning-based retrieval over vector similarity where possible
6. Support the compliance gap analysis workflow with retrieved context
7. Be measurable — precision, recall, latency, cost per query

## Decision

### Primary Decision: Hybrid Retrieval Funnel — Vectorless-First with Optional Vector Secondary

We adopt a **three-tier retrieval funnel** that defaults to deterministic, structure-aware retrieval and escalates to vector similarity only when needed:

```
User Query
    │
    ▼
┌──────────────────────────────┐
│  Tier 1: Tenant Filter +     │  ← Mandatory: WHERE tenant_id = ?
│  Metadata Pre-Filter          │  ← Optional: severity, source, date range
└──────────┬───────────────────┘
           ▼ pass
┌──────────────────────────────┐
│  Tier 2: Structured Index    │  ← Vectorless: hierarchical TOC-based
│  (Section-Aware Navigation)  │  ← Supports: exact headings, section IDs,
│                              │     circular numbers, clause references
└──────────┬───────────────────┘
           │ no exact match
           ▼
┌──────────────────────────────┐
│  Tier 3a: PostgreSQL FTS     │  ← tsvector/tsquery on content
│  (Full-Text Search)          │  ← Ranked by ts_rank_cd
└──────────┬───────────────────┘
           │ low confidence
           ▼
┌──────────────────────────────┐
│  Tier 3b: Vector Search      │  ← pgvector (optional, Phase 3+)
│  (Semantic Similarity)       │  ← Only when hybrid signal < threshold
└──────────┬───────────────────┘
           │
           ▼
┌──────────────────────────────┐
│  Conditional Router          │  ← Deterministic: enough context?
│                              │  ← LLM reasoning: compliance gap?
│                              │  ← HITL: ambiguous/high-stakes?
└──────────┬───────────────────┘
           │
           ▼
      Final Answer / Gap Report
```

### Rationale for Vectorless-First

1. **Document structure is deterministic.** Regulatory circulars have predictable structure (circular number, date, sections, annexures). A hierarchical index of these structures can be built at ingestion time with deterministic parsers — no embeddings needed.

2. **Exact references matter in compliance.** "Section 73 of CGST Act" must match exactly, not "similar" content. Vector search loses exact-match precision. PostgreSQL FTS with phrase search (`tsquery` with `<->` operator) handles this better.

3. **Tenant-filtered first.** All queries must filter by `tenant_id`. pgvector HNSW indexes are global — filtering is applied after index scan, which degrades recall for small tenant subsets. VL-based structured indexes are naturally partitioned by tenant.

4. **LLM reasoning over structure, not chunks.** The compliance agent should reason over a hierarchical index (table of contents + section bodies), not over N chunks of ~2000 chars each. This reduces token usage and improves reasoning accuracy.

5. **Operational simplicity.** No embedding model to host, no vector dimension to choose, no HNSW parameters to tune. PostgreSQL FTS + GIN indexes are zero-config.

### When Vector Search Is Appropriate (Deferred to Phase 3)

- Fuzzy semantic matching for natural-language queries ("circulars about late payment penalties for composition dealers")
- Cross-document similarity ("find all circulars related to this one")
- When FTS recall is insufficient (< 0.7 precision on eval set)

### Specific Technology Choices

| Component | Choice | Rationale |
|-----------|--------|-----------|
| **Structured index** | Custom Go implementation in `internal/domain/` | Deterministic, no external dependency; builds TOC from parsed headings |
| **Full-text search** | PostgreSQL `tsvector` / `tsquery` with GIN index | Native PostgreSQL, zero ops, handles Indian English stemming well |
| **Vector search** | pgvector (via `pgvector-go` + pgx) | PostgreSQL-native, no separate infrastructure, production-proven |
| **Embedding model** | To be evaluated in Phase 3 — candidates: `text-embedding-3-small` via OpenRouter or local model | Cost-vs-quality tradeoff requires measurement |
| **Chunking** | Section-aware, heading-preserving, table-preserving | Replaces current fixed-size chunker |
| **Metadata store** | Extended `compliance_chunks` table with JSONB metadata column | Flexible schema for regulator-specific fields |
| **Conditional router** | Go state machine in `internal/agents/retrieval_agent.go` | Deterministic routing based on confidence thresholds |
| **Evaluation** | Stored query-result pairs in new `eval_queries` table | Enables precision/recall measurement without external framework |

## Consequences

### Benefits

1. **Deterministic by default.** The retrieval funnel prefers the most deterministic path first. Vector search is the last resort, not the default.

2. **Multi-tenant isolation preserved.** `tenant_id` filtering happens first in every path. Structured indexes are partitioned per tenant. FTS + RLS works natively.

3. **Auditable retrieval paths.** Every query records which tier(s) it used, confidence scores, and which chunks were returned. This enables debugging and evaluation.

4. **Reduced LLM dependency.** Structured index + FTS handles 70-80% of queries without any LLM invocation. Only compliance gap reasoning and low-confidence FTS results need the LLM.

5. **Leverages existing PostgreSQL infrastructure.** No new databases, no vector DB operations overhead, no embedding pipeline to maintain in Phase 1-2.

6. **Incremental adoption.** Each phase is independently valuable. Phase 1 alone improves the current compliance workflow without any new infrastructure.

### Trade-offs / Risks

1. **Structured index maintenance.** The hierarchical TOC index must be rebuilt when documents are updated. Mitigation: store index as JSONB alongside document; rebuild is O(n) over headings, not content.

2. **pgvector operational complexity (Phase 3+).** Embedding dimension choice, HNSW index tuning, and recall@k measurement require expertise. Mitigation: defer until data volume proves need; start with IVF-Flat (faster build, lower memory).

3. **FTS does not handle Indian languages well.** PostgreSQL `tsvector` supports English stemming only. For Hindi/mixed-language documents, we may need vector search sooner. Mitigation: Phase 1 is English-only for FTS; Hindi documents route to Sarvam OCR with structured extraction.

4. **Evaluation requires ground truth.** Measuring precision/recall requires a labeled query-document pair dataset. Mitigation: start with implicit feedback (click-through, HITL escalation rate) before building explicit eval sets.

## Implementation Plan

### Phase 1: Foundation — Section-Aware Chunking + Metadata + FTS (Now)

**Goal:** Replace fixed-size chunking with structure-aware chunking, enrich metadata, add PostgreSQL FTS, and expose search API. Delivers immediate value to the compliance workflow.

| Task | Files Changed / Created | Effort |
|------|------------------------|--------|
| **1a. Section-aware chunker** | `internal/domain/chunker.go` — rewrite `ChunkDocument` to detect headings (`#`, `##`, `SECTION`, `ANNEXURE` in caps), preserve heading hierarchy, never split tables | 2-3 days |
| **1b. Heading detection rules** | `internal/domain/heading_detector.go` (new) — Indian regulatory heading patterns: `SECTION \d+`, `Circular No.`, `ANNEXURE`, numbered lists | 1 day |
| **1c. Table boundary preservation** | `internal/domain/chunker.go` — detect `|` + `-+-` table patterns, keep tables atomic | 0.5 day |
| **1d. Metadata enrichment at ingestion** | `internal/domain/compliance_metadata.go` (new) — extract circular number, regulator, effective date, applicability, sections referenced | 2 days |
| **1e. Extended compliance_chunks schema** | `internal/adapters/postgres/migrations.go` — add `metadata JSONB` column to `compliance_chunks`, add `tsv_content tsvector` column | 0.5 day |
| **1f. GIN index on tsvector** | `internal/adapters/postgres/migrations.go` — `CREATE INDEX idx_compliance_tsv ON compliance_chunks USING GIN(tsv_content)` | 0.25 day |
| **1g. Search provider interface** | `internal/providers/interfaces.go` — add `SearchProvider` interface: `Search(ctx, tenantID, query, filters) (*SearchResult, error)` | 0.5 day |
| **1h. PostgreSQL FTS adapter** | `internal/adapters/postgres/search.go` (new) — implement `SearchProvider` using `ts_query` + `ts_rank_cd`, enforce `tenant_id` filter, return ranked chunks | 1 day |
| **1i. Search API endpoint** | `cmd/server/main.go` + `internal/adapters/http/search_handler.go` (new) — `GET /api/search?q=...&source=SEBI&severity=HIGH`, tenant-filtered | 1 day |
| **1j. Metrics for search quality** | `internal/telemetry/metrics.go` — track search latency, result count, zero-result rate, tier utilization | 0.5 day |
| **1k. Update compliance agent** | `internal/agents/compliance_agent.go` — use `SearchProvider` instead of loading all chunks; pass retrieved context to LLM gap analysis | 1 day |
| **1l. Tests** | `tests/unit/chunker_test.go` — update for section-aware tests; `tests/integration/postgres_search_test.go` — FTS adapter tests | 2 days |

**Phase 1 success criteria:**
- Compliance chunks are section-aware (headings preserved, tables atomic)
- Metadata extracted for ≥90% of regulatory documents
- FTS query returns ranked results with <200ms latency at 10K chunks/tenant
- Compliance agent uses retrieved context instead of full-scan
- Search API returns results with tenant isolation verified

### Phase 2: Hierarchical Structured Index + Conditional Router (Next 2-4 weeks)

**Goal:** Add vectorless retrieval via hierarchical TOC index. Enable section-aware navigation for compliance gap analysis. Add conditional routing.

| Task | Files Changed / Created | Effort |
|------|------------------------|--------|
| **2a. Document TOC index builder** | `internal/domain/toc_index.go` (new) — builds tree from headings extracted by section-aware chunker; stores as JSONB | 2 days |
| **2b. Structured index storage** | `internal/adapters/postgres/migrations.go` — add `toc_index JSONB` to `documents` table; index by `(tenant_id, document_id)` | 0.5 day |
| **2c. Section navigator** | `internal/domain/section_navigator.go` (new) — given a TOC index + section reference (e.g., "Section 5.2"), return the section content | 1 day |
| **2d. Retrieval agent** | `internal/agents/retrieval_agent.go` (new) — orchestrates the funnel: structured index → FTS → (future: vector). Contains the conditional router logic | 2 days |
| **2e. Conditional router state machine** | `internal/domain/retrieval_router.go` (new) — deterministic routing tiers: exact match → FTS high-confidence → FTS low-confidence → vector (Phase 3) → LLM → HITL | 1 day |
| **2f. Confidence threshold config** | `internal/domain/retrieval_config.go` (new) — configurable thresholds per tier (FTS confidence > 0.7 = pass, etc.) | 0.5 day |
| **2g. Compliance agent update v2** | `internal/agents/compliance_agent.go` — integrate RetrievalAgent for gap analysis context gathering | 1 day |
| **2h. Search API update** | `internal/adapters/http/search_handler.go` — add tier-aware response metadata (which tier was used, confidence) | 0.5 day |
| **2i. Tests** | `tests/unit/toc_index_test.go`, `tests/unit/section_navigator_test.go`, `tests/agentic/retrieval_agent_test.go` | 2 days |

**Phase 2 success criteria:**
- Hierarchical TOC index built at ingestion time for all regulatory documents
- Section-aware navigation resolves ≥95% of exact section references
- Retrieval agent routes correctly per confidence thresholds
- Zero LLM invocations for exact-match and high-confidence FTS queries
- Retrieval path recorded in audit trail

### Phase 3: Optional pgvector + Hybrid Search (Data-Volume Gated)

**Goal:** Add vector embeddings for fuzzy semantic search. Enable hybrid search combining FTS + vector with reciprocal rank fusion. Only if Phase 1-2 recall is insufficient.

| Task | Files Changed / Created | Effort |
|------|------------------------|--------|
| **3a. pgvector extension migration** | `internal/adapters/postgres/migrations.go` — `CREATE EXTENSION IF NOT EXISTS vector`; add `embedding vector(1536)` column | 0.5 day |
| **3b. Embedding provider interface** | `internal/providers/interfaces.go` — add `EmbeddingProvider` interface: `Embed(ctx, texts []string) ([][]float32, error)` | 0.25 day |
| **3c. Embedding adapter** | `internal/adapters/embeddings/openrouter.go` (new) — call OpenRouter `/embeddings` endpoint for `text-embedding-3-small` | 1 day |
| **3d. Embedding generation at ingestion** | `internal/agents/retrieval_agent.go` — after chunking, generate embedding async and store in `embedding` column | 0.5 day |
| **3e. HNSW index** | `internal/adapters/postgres/migrations.go` — `CREATE INDEX ON compliance_chunks USING hnsw (embedding vector_cosine_ops)` | 0.25 day |
| **3f. Hybrid search query builder** | `internal/adapters/postgres/search_hybrid.go` (new) — `FTS_score * α + vector_score * β`, configurable weights | 1 day |
| **3g. Reciprocal rank fusion** | `internal/domain/rrf.go` (new) — merge FTS + vector results with RRF scoring | 0.5 day |
| **3h. Embedding cost/performance tracking** | `internal/telemetry/metrics.go` — track embedding latency, tokens, cost per document | 0.25 day |
| **3i. Tests** | `tests/integration/pgvector_test.go` (//go:build integration), live embedding tests (//go:build live) | 2 days |

**Phase 3 success criteria:**
- Hybrid search improves recall by ≥15% over FTS-only on evaluation set
- Embedding cost < ₹0.10 per document (text-embedding-3-small at $0.13/1M tokens)
- HNSW index query latency < 50ms at 100K vectors
- Embedding generation is async, non-blocking for ingestion

### Phase 4: Evaluation Framework + Production Hardening (Continuous)

**Goal:** Measure retrieval quality, build ground-truth dataset, optimize thresholds, add monitoring.

| Task | Files Changed / Created | Effort |
|------|------------------------|--------|
| **4a. Evaluation query store** | `internal/domain/eval_types.go` (new) + migration for `eval_queries` table | 0.5 day |
| **4b. Precision/recall computation** | `internal/domain/eval_metrics.go` (new) — compute P@k, R@k, MRR, NDCG | 1 day |
| **4c. Faithfulness evaluation** | `internal/agents/eval_agent.go` (new) — for LLM-grounded answers, check citation faithfulness | 1 day |
| **4d. Dashboard integration** | `ops-ui/app.py` — add retrieval metrics page (precision, recall, latency, cost, tier distribution) | 1 day |
| **4e. Alert thresholds** | `internal/domain/retrieval_config.go` — alert when zero-result rate > 5%, avg confidence < 0.6 | 0.5 day |
| **4f. A/B comparison framework** | Support for comparing two retrieval configurations on same query set | 1 day |

**Phase 4 success criteria:**
- Evaluation dashboard accessible via Streamlit admin UI
- Precision@5 ≥ 0.85 for Tier 2 (structured) and Tier 3a (FTS)
- Faithfulness score ≥ 0.9 for LLM-grounded answers
- Zero-result rate < 3% across all tenants

## File Map

### Existing Files Modified

| File | Phase | Change |
|------|-------|--------|
| `internal/domain/chunker.go` | 1 | Section-aware, heading-preserving, table-preserving |
| `internal/domain/types.go` | 1 | Add `DocumentSection`, `HeadingInfo`, `TableInfo` types |
| `internal/domain/document_classifier.go` | 1 | Optionally use headings for classification boost |
| `internal/agents/compliance_agent.go` | 1, 2 | Use RetrievalAgent instead of full-table scan |
| `internal/providers/interfaces.go` | 1 | Add `SearchProvider`; Phase 3: add `EmbeddingProvider` |
| `internal/adapters/postgres/adapter.go` | 1 | Optionally expose `queryExecer` for search adapter |
| `internal/adapters/postgres/migrations.go` | 1, 2, 3 | Add metadata column, tsv column, GIN index, TOC column, pgvector |
| `internal/telemetry/metrics.go` | 1, 2, 3 | Search latency, tier utilization, embedding metrics |
| `cmd/server/main.go` | 1 | Register search handler |

### New Files

| File | Phase | Purpose |
|------|-------|---------|
| `internal/domain/heading_detector.go` | 1 | Detect Indian regulatory heading patterns |
| `internal/domain/compliance_metadata.go` | 1 | Extract circular number, regulator, effective date etc. |
| `internal/domain/retrieval_router.go` | 2 | Deterministic routing state machine |
| `internal/domain/retrieval_config.go` | 2 | Configurable threshold constants |
| `internal/domain/toc_index.go` | 2 | Build hierarchical TOC from section headings |
| `internal/domain/section_navigator.go` | 2 | Navigate TOC to find section content |
| `internal/domain/rrf.go` | 3 | Reciprocal rank fusion for hybrid search |
| `internal/domain/eval_types.go` | 4 | Evaluation query/result types |
| `internal/domain/eval_metrics.go` | 4 | Precision, recall, MRR, NDCG computation |
| `internal/adapters/postgres/search.go` | 1 | PostgreSQL FTS adapter (SearchProvider impl) |
| `internal/adapters/postgres/search_hybrid.go` | 3 | Hybrid FTS + vector search adapter |
| `internal/adapters/http/search_handler.go` | 1 | `GET /api/search` HTTP handler |
| `internal/adapters/embeddings/openrouter.go` | 3 | Embedding generation via OpenRouter |
| `internal/agents/retrieval_agent.go` | 2 | Orchestrate retrieval funnel, conditional routing |
| `internal/agents/eval_agent.go` | 4 | Faithfulness evaluation for LLM-grounded answers |
| `tests/integration/postgres_search_test.go` | 1 | FTS adapter integration tests |
| `tests/integration/pgvector_test.go` | 3 | pgvector integration tests (//go:build integration) |
| `tests/unit/toc_index_test.go` | 2 | TOC index unit tests |
| `tests/unit/section_navigator_test.go` | 2 | Section navigation unit tests |
| `tests/unit/retrieval_router_test.go` | 2 | Conditional router unit tests |
| `tests/unit/eval_metrics_test.go` | 4 | Evaluation metric unit tests |
| `tests/agentic/retrieval_agent_test.go` | 2 | Retrieval agent workflow tests |

## Domain Types: Key Additions

```go
// Phase 1 — Section-aware chunking
type DocumentSection struct {
    ID          string            `json:"id"`
    Level       int               `json:"level"`       // 1=title, 2=chapter, 3=section, 4=subsection
    Heading     string            `json:"heading"`
    Content     string            `json:"content"`
    PageNumber  int               `json:"page_number,omitempty"`
    SubSections []DocumentSection `json:"sub_sections,omitempty"`
}

// Phase 1 — Metadata enrichment
type ComplianceMetadata struct {
    CircularNumber  string   `json:"circular_number,omitempty"`
    Regulator       string   `json:"regulator,omitempty"`  // SEBI, RBI, GST, MCA
    EffectiveDate   string   `json:"effective_date,omitempty"`
    Applicability   []string `json:"applicability,omitempty"`  // e.g., ["all_vendors", "gst_registered"]
    SectionsReferenced []string `json:"sections_referenced,omitempty"`
    DocumentType    string   `json:"document_type,omitempty"`
    Language        string   `json:"language,omitempty"`
}

// Phase 2 — Retrieval results
type SearchResult struct {
    TotalHits    int                `json:"total_hits"`
    Tier         string             `json:"tier"`          // "exact" | "fts" | "vector" | "llm"
    Confidence   float64            `json:"confidence"`
    Chunks       []RetrievedChunk   `json:"chunks"`
    QueryLatencyMs int64            `json:"query_latency_ms"`
}

type RetrievedChunk struct {
    ChunkID      string             `json:"chunk_id"`
    DocumentID   string             `json:"document_id"`
    Content      string             `json:"content"`
    Score        float64            `json:"score"`
    SectionPath  []string           `json:"section_path,omitempty"`
    Metadata     ComplianceMetadata `json:"metadata,omitempty"`
}

// Phase 4 — Evaluation
type EvalQuery struct {
    ID                string    `json:"id"`
    Query             string    `json:"query"`
    RelevantChunkIDs  []string  `json:"relevant_chunk_ids"`
    ExpectedTier      string    `json:"expected_tier"`
    CreatedAt         time.Time `json:"created_at"`
}
```

## Compliance with OpsCore Principles

### "Deterministic first, LLM last"

The retrieval funnel explicitly encodes this:
- **Tier 1-2**: Pure deterministic Go (tenancy filter, metadata filter, structured index navigation)
- **Tier 3a**: PostgreSQL FTS — deterministic ranking by `ts_rank_cd`
- **Tier 3b**: pgvector — deterministic cosine similarity (no LLM involved)
- **LLM**: Invoked only when Tier 1-3b confidence is below threshold AND the query requires compliance gap reasoning

### Multi-tenant isolation

- All queries include `WHERE tenant_id = $1` as the first filter
- PostgreSQL RLS (already implemented in migration 5) applies as defense-in-depth
- Structured TOC indexes are stored per-document, which are already tenant-filtered
- FTS GIN indexes are global but RLS ensures cross-tenant leakage is impossible
- Search API requires `X-Tenant-ID` header (existing middleware pattern)

### Observability

- Every search query records: tier used, confidence, latency, result count
- Zero-result rate alerts on retrieval_router thresholds
- LLM invocation is traced through Langfuse (existing infrastructure)
- Embedding generation (Phase 3) tracked for cost and latency

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| FTS doesn't handle Indian regulatory terminology well | Low recall for Hindi/mixed docs | Phase 1 is English-only; track language detection in metadata; vector search in Phase 3 for multilingual |
| Hierarchical TOC index is brittle for varied document formats | Wrong section boundaries | Design heading_detector with configurable patterns; test against 50+ real regulatory PDFs; fall back to flat chunking |
| Phase 1-2 recall is insufficient without vector search | Users frustrated | Build evaluation set early (Phase 1); if precision@5 < 0.7, deprioritize Phase 3 gating |
| Embedding cost (Phase 3) grows with document volume | Operational cost | Use OpenRouter with caching; choose `text-embedding-3-small` ($0.13/1M tokens); benchmark before enabling for all tenants |
| Search latency degrades with tenant data growth | Poor UX | GIN indexes on tsvector, HNSW indexes on vectors, connection pooling already in place; add query timeout middleware |

## References

1. Kimi K2.6 Thinking Architecture Recommendations — structure-aware ingestion, semantic chunking, metadata enrichment, retrieval funnel, conditional router
2. Vectorless RAG — PageIndex framework (VectifyAI): hierarchical tree index + reasoning-based retrieval for structured documents
3. pgvector documentation — HNSW vs IVFFlat, hybrid search with FTS + RRF, Go client via `pgvector-go`
4. BookRAG (arXiv 2512.03413) — hierarchical structure-aware indexing for complex documents
5. OpsCore PRD v4.0 — existing chunking strategy, compliance workflow, tenant isolation
