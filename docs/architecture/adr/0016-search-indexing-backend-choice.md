# ADR-0016: Search/Indexing Backend Choice

## Status

**Proposed** — ADR-001 defines the retrieval funnel but defers the specific search backend choice. PostgreSQL FTS is the current default; this ADR formalizes the decision.

## Date

2026-07-13

## Context

### Problem Statement

ADR-001 defines a three-tier retrieval funnel (structured index → FTS → vector search) for compliance and business documents. The first two tiers (structured index, FTS) and the third tier (vector search) each require a search backend. The choices are:

1. **PostgreSQL Full-Text Search (FTS)** via `tsvector`/`tsquery` with GIN indexes — already available in the existing PostgreSQL 16 instance.
2. **pgvector** as a PostgreSQL extension — for vector similarity search in Phase 3.
3. **Dedicated search infrastructure** — OpenSearch, Elasticsearch, or Solr — requiring separate deployment and operations.

The decision must consider:

1. **Current scale.** Phase 1 targets < 10,000 compliance chunks per tenant. A single PostgreSQL instance with GIN indexes can handle this comfortably.
2. **Operational overhead.** OpsCore runs on Cloud Run with managed PostgreSQL (Cloud SQL). Each additional infrastructure component increases cost and complexity.
3. **Tenant isolation.** All queries must be scoped by `tenant_id`. PostgreSQL RLS works seamlessly with FTS queries. OpenSearch would require per-tenant indices or document-level security.
4. **India-specific search requirements.** Regulatory documents use Indian English terms ("circular", "notification", "section"). PostgreSQL English stemming handles these adequately for Phase 1.
5. **Future vector search.** When Phase 3 adds semantic search, pgvector provides vector similarity within the same PostgreSQL instance — no new infrastructure.

### Requirements

1. Support both exact-match and full-text search on compliance document chunks.
2. Support metadata-filtered search (severity, source, date range, document type).
3. Scale to 10,000 documents (100,000 chunks) per tenant without degrading query performance.
4. Respect tenant isolation — every query must enforce `tenant_id` filtering.
5. Zero additional infrastructure for Phase 1-2. Phase 3 may add infrastructure only if data volume proves need.
6. Support structured index tier (hierarchical TOC navigation) defined in ADR-001 Phase 2.

## Decision

### Primary Decision: PostgreSQL FTS as Primary, pgvector as Optional Extension

**Phase 1-2: PostgreSQL FTS only.**

All full-text search is handled by PostgreSQL `tsvector`/`tsquery` with GIN indexes:

```sql
-- Migration: add tsvector column and GIN index
ALTER TABLE compliance_chunks ADD COLUMN tsv_content tsvector
    GENERATED ALWAYS AS (to_tsvector('english', content)) STORED;

CREATE INDEX idx_compliance_chunks_tsv
    ON compliance_chunks
    USING GIN (tsv_content);
```

Search queries use `ts_query` with phrase search:

```sql
SELECT chunk_id, document_id, content, metadata,
       ts_rank_cd(tsv_content, query) AS rank
FROM compliance_chunks, plainto_tsquery('english', $search_term) AS query
WHERE tenant_id = $1
  AND tsv_content @@ query
  AND (metadata->>'severity' = $2 OR $2 IS NULL)
ORDER BY rank DESC
LIMIT 20;
```

**Phase 3: pgvector for vector similarity (data-volume gated).**

When FTS recall is insufficient (precision@5 < 0.7 on evaluation set), add pgvector:

```sql
CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE compliance_chunks ADD COLUMN embedding vector(1536);

CREATE INDEX ON compliance_chunks
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 200);
```

Hybrid search combines FTS and vector scores with reciprocal rank fusion (RRF).

### Why PostgreSQL FTS First

| Consideration | PostgreSQL FTS | OpenSearch |
|---------------|---------------|------------|
| **Operational cost** | $0 (existing PG instance) | $30-100/month for dedicated cluster |
| **Setup time** | 1 migration, 0 new infrastructure | 2-3 days for cluster provisioning |
| **Tenant isolation** | Native RLS `WHERE tenant_id = $1` | Per-index security or document-level security |
| **Query latency (10K chunks)** | < 50ms | < 20ms |
| **Relevance ranking** | `ts_rank_cd` (OK for compliance) | BM25 (better for general search) |
| **Indian language support** | English stemming only | ICU analysis plugin |
| **Vector search** | pgvector extension | k-NN plugin |
| **Operational expertise** | Already managed PostgreSQL | New expertise required |

PostgreSQL FTS is "good enough" for the Phase 1-2 workload. OpenSearch's advantages (better ranking, language analysis) do not justify the operational cost at current scale.

### When to Add pgvector (Phase 3, Data-Volume Gated)

pgvector is added when both conditions are met:

1. **Volume threshold:** More than 100,000 chunks per tenant (Phase 1-2 scale limit).
2. **Recall threshold:** FTS precision@5 < 0.7 on the evaluation query set, measured over 4 weeks.

If only condition 1 is met but condition 2 is not, the structured index + FTS tiers are sufficient. Vector search would add complexity without measurable improvement.

### When to Consider OpenSearch

OpenSearch is considered when all three conditions are met:

1. **Scale threshold:** More than 100,000 documents per tenant.
2. **Recall requirement:** Cross-document full-text search requiring BM25 ranking, fuzzy matching, or n-gram tokenization.
3. **Operational capacity:** Dedicated team member with OpenSearch operational experience.

At the current projected scale (< 1,000 tenants × < 500 documents/tenant), OpenSearch is not needed.

### Tenant Isolation with FTS

PostgreSQL RLS (Row-Level Security) is already configured on all tables. The FTS adapter enforces `tenant_id` filtering in the WHERE clause as a defense-in-depth measure:

```go
// internal/adapters/postgres/search.go
func (p *PostgresSearch) Search(ctx context.Context, tenantID string, query string, filters domain.SearchFilters) (*domain.SearchResult, error) {
    // Primary: explicit tenant_id filter in query
    // Secondary: RLS on the compliance_chunks table
    rows, err := p.pool.Query(ctx, `
        SELECT chunk_id, document_id, content, metadata,
               ts_rank_cd(tsv_content, plainto_tsquery('english', $2)) AS rank
        FROM compliance_chunks
        WHERE tenant_id = $1
          AND tsv_content @@ plainto_tsquery('english', $2)
          AND ($3::text IS NULL OR metadata->>'severity' = $3)
        ORDER BY rank DESC
        LIMIT 20
    `, tenantID, query, filters.Severity)
    // ...
}
```

For pgvector (Phase 3), the tenant_id filter must be applied before the vector index search. pgvector HNSW indexes are global — filtering is applied after index scan. For small tenant subsets, this may degrade recall. Mitigation: use IVFFlat index (which supports filtering better) or maintain per-tenant embedding tables.

### GIN Index Configuration

```sql
-- Primary GIN index for FTS
CREATE INDEX idx_compliance_chunks_tsv
    ON compliance_chunks
    USING GIN (tsv_content);

-- Composite index for tenant-filtered FTS queries
CREATE INDEX idx_compliance_chunks_tenant_tsv
    ON compliance_chunks
    USING GIN (tenant_id, tsv_content);

-- B-tree index for metadata filtering
CREATE INDEX idx_compliance_chunks_metadata
    ON compliance_chunks
    USING GIN (metadata jsonb_path_ops);
```

### Structured Index (Phase 2) Storage

The hierarchical TOC index is stored as JSONB in the `documents` table, not in a separate search engine:

```go
type DocumentTOC struct {
    DocumentID string          `json:"document_id"`
    TenantID   string          `json:"tenant_id"`
    Sections   []TOCSection    `json:"sections"`
    BuiltAt    time.Time       `json:"built_at"`
}

type TOCSection struct {
    ID        string        `json:"id"`
    Level     int           `json:"level"`
    Heading   string        `json:"heading"`
    Offset    int           `json:"offset"`     // character offset in document
    Length    int           `json:"length"`     // content length
    Children  []TOCSection  `json:"children,omitempty"`
}
```

This is loaded into memory at query time for section navigation. No external search index is needed.

## Alternatives Considered

### 1. OpenSearch as Primary

Rejected because:
- Requires provisioning and operating a separate OpenSearch cluster ($30-100/month for minimal setup).
- Tenant isolation requires per-tenant indices or complex document-level security configuration.
- No operational expertise in the current team.
- The performance advantage (BM25 ranking, fuzzy matching) is not needed at current scale.

### 2. Elasticsearch (Self-Hosted)

Rejected for the same reasons as OpenSearch, plus:
- Elasticsearch license changes (SSL → Elastic License) introduce licensing complexity.
- OpenSearch is the preferred open-source fork.

### 3. SQLite FTS5

Rejected because:
- Not available in PostgreSQL. Would require a separate SQLite database per tenant, adding complexity.
- Cannot participate in PostgreSQL transactions or RLS.
- No vector extension for Phase 3.

### 4. Algolia / Meilisearch (Managed Search)

Rejected because:
- Third-party dependency for core search functionality.
- Data must be exported to the managed service, complicating tenant isolation.
- Cost scales with document count and query volume.
- Cannot customize ranking for Indian regulatory terminology.

### 5. Vector-First (pgvector Only, No FTS)

Rejected because:
- Vector search cannot do exact phrase matching ("Section 73 of CGST Act" must match exactly).
- Embedding generation adds latency and cost to ingestion.
- Tenant isolation with global HNSW indexes is problematic for small tenant subsets.
- Violates the "deterministic first, LLM last" principle — vector search is less interpretable than FTS.

## Consequences

### Benefits

1. **Zero additional infrastructure.** PostgreSQL FTS uses the existing PostgreSQL instance. No new databases, clusters, or managed services.

2. **Native tenant isolation.** RLS + WHERE tenant_id filtering works for FTS queries. No per-index security configuration needed.

3. **Incremental scalability.** Phase 1 (FTS) works now. Phase 2 (structured index) uses JSONB in existing tables. Phase 3 (pgvector) is optional and gated on data volume.

4. **Single operational surface.** All search infrastructure is managed as PostgreSQL. Backup, restore, replication, and failover cover search data automatically.

5. **Deterministic ranking.** `ts_rank_cd` is deterministic — same query returns same results. This is important for auditability and reproducibility in compliance workflows.

6. **GIN indexes are fast for read-heavy workloads.** Compliance documents are ingested once and queried many times. GIN indexes optimize for this pattern.

### Trade-offs / Risks

1. **FTS recall may be insufficient for Indian English.** PostgreSQL English stemmer does not handle Hindi or mixed-language documents. Mitigation: Phase 1 English-only; Hindi documents route to Sarvam OCR with structured extraction. Vector search (Phase 3) for multilingual queries.

2. **No fuzzy matching or typo tolerance.** PostgreSQL FTS does not support edit-distance matching. "circular" and "circuler" are not matched. Mitigation: use `pg_trgm` extension for trigram similarity as a stopgap before vector search in Phase 3.

3. **GIN index size.** tsvector GIN indexes can be 2-3x the size of the indexed text. For 100,000 chunks (200MB text), the index is ~400-600MB. This is acceptable for a PostgreSQL instance with adequate memory.

4. **pgvector HNSW index build time.** For 100,000+ vectors, HNSW index build can take 30-60 minutes. Mitigation: this is a one-time cost per index build. Use IVFFlat (faster build, slightly lower recall) for initial deployment.

5. **No cross-document full-text search.** PostgreSQL FTS searches within chunks, not across documents. For queries like "find all documents mentioning GST Section 73", a document-level aggregation is needed. Mitigation: the structured index (Phase 2) provides document-level section navigation. The search API can aggregate chunk results by document.

## Related ADRs

- **ADR-001**: Retrieval Architecture for Compliance & Document Search — defines the three-tier retrieval funnel that this ADR implements with PostgreSQL FTS and pgvector.
- **ADR-0008**: Retrieval Router — Deterministic and Vectorless/Hybrid — defines the conditional router that selects between FTS, vector, and LLM tiers.
- **ADR-0015**: Deployment Target and Cloud-Agnostic Runtime — PostgreSQL is the primary data store; this ADR confirms FTS does not require additional infrastructure.
