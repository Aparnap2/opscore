# ADR-0007: Structured Ingestion and Semantic Chunking

## Status

**Proposed** — the current chunker in `internal/domain/chunker.go` uses fixed 2000-character boundaries with word-split logic. Section-aware chunking is planned but not implemented. This ADR defines the upgrade to structure-aware ingestion.

## Date

2026-07-13

## Context

### Problem Statement

OpsCore ingests regulatory compliance documents (SEBI circulars, RBI notifications, GST notices) and business documents (invoices, contracts, purchase orders). These documents must be split into chunks for downstream processing — compliance gap analysis, search indexing, and LLM-based reasoning.

The current chunker (`internal/domain/chunker.go`) has significant limitations:

1. **Fixed 2000-character boundaries** — text is split at character count intervals with no awareness of document structure. A heading that appears at position 1995 is cleanly separated from the body that starts at position 2005, even though they belong to the same section.

2. **Word-split logic** — the chunker does not even preserve word boundaries. The last word of one chunk may be truncated mid-character, and the continuation appears in the next chunk with no prefix context.

3. **Heading hierarchy is lost** — the chunker has no concept of `#`, `##`, `SECTION`, `ANNEXURE`, or numbered headings. When a heading lands in chunk N and its body in chunk N+1, the downstream LLM receives body text without heading context, producing worse reasoning.

4. **Tables are split across chunks** — pipe-separated tables (`| header | header |`, `|---|---|`) are treated as plain text. A table that spans a chunk boundary becomes two disjoint text fragments, both of which are unparseable by any downstream system.

5. **No metadata enrichment** — the chunker does not extract regulatory metadata (circular number, regulator, effective date, applicability). The downstream compliance agent receives raw text and must re-discover this metadata in every chunk, wasting tokens and producing inconsistent results.

6. **Sequential processing of all chunks** — the compliance agent (`internal/agents/compliance_agent.go`) receives all chunks and processes them sequentially with the LLM. For a 50-page SEBI circular producing ~30 chunks, this means 30 LLM calls, each repeating the same instructions and re-discovering document context.

### Current Chunker Behavior

```go
// From internal/domain/chunker.go
func ChunkDocument(text string, docID, tenantID string, docType DocumentType, pageCount int) []ComplianceChunk {
    // ...
    for i := 0; i < textLen; i += ChunkSizeChars {  // ChunkSizeChars = 2000
        end := i + ChunkSizeChars
        // ...
        chunk := ComplianceChunk{
            ID:           fmt.Sprintf("%s_chunk_%d", docID, chunkIndex),
            Content:      text[i:end],    // raw byte slice — no structure awareness
            ChunkIndex:   chunkIndex,
            DocumentType: string(docType),
            PageNumber:   (chunkIndex * ChunkSizeChars / 2000) + 1, // approximate
        }
        // ...
    }
}
```

The `ComplianceChunk` struct (`internal/domain/types.go:117-128`) has `Content`, `ChunkIndex`, `DocumentType`, and `PageNumber` — but no `HeadingPath`, `SectionID`, or `Metadata` fields. These additions are required for structured ingestion.

### Requirements

1. **Preserve heading hierarchy** — each chunk must know its position in the document's heading tree (e.g., `"Chapter 3 > Section 3.1 > Sub-section 3.1.2"`).
2. **Keep tables atomic** — pipe-separated and grid tables must never be split across chunks. A table is atomic unit that belongs entirely to one chunk.
3. **Extract regulatory metadata** — at ingestion time, extract circular number, regulator name, effective date, applicable entities, and sections referenced.
4. **Reduce chunk count** — structure-aware chunking should produce fewer, more meaningful chunks than fixed-size splitting. Target: 40-60% reduction in chunk count for regulatory documents.
5. **Fallback when structure is undetectable** — if the document has no detectable headings, sections, or tables, fall back to fixed-size chunking with word-boundary preservation.
6. **Metric tracking** — track extraction success rate, chunk count, average chunk size, and token savings per document type.

## Decision

Upgrade ingestion to a structure-aware pipeline with three phases:

1. **Document structure detection** — Identify headings (`##`, `SECTION`, `ANNEXURE`, numbered lists), tables (pipe-separated), and metadata fields (circular number, date, regulator).
2. **Section-aware chunking** — Preserve heading hierarchy, keep tables atomic, never split across section boundaries.
3. **Metadata enrichment** — Extract structured metadata (regulator, document type, effective date, applicability, sections referenced) at ingestion time, before chunks are stored.

### Architecture Overview

```
Raw Document Text (from OCR / PDF extraction)
         │
         ▼
┌─────────────────────────────────────┐
│  Phase 1: Structure Detection       │
│                                     │
│  1. Heading Detector                │
│     ├── ##/### Markdown headings    │
│     ├── SECTION \d+ patterns        │
│     ├── ANNEXURE / SCHEDULE caps    │
│     ├── Numbered lists (1., a., i.) │
│     └── Regulatory prefixes         │
│         (Circular No., Notification)│
│                                     │
│  2. Table Boundary Detector         │
│     ├── Pipe-separated tables       │
│     ├── Grid tables (---+-)         │
│     └── Multi-line cells            │
│                                     │
│  3. Metadata Scanner                │
│     ├── Circular number regex       │
│     ├── Date patterns               │
│     ├── Regulator name keywords     │
│     └── Section references          │
└──────────────┬──────────────────────┘
               │
               ▼
┌─────────────────────────────────────┐
│  Phase 2: Section-Aware Chunking    │
│                                     │
│  1. Build heading tree              │
│  2. Assign each text line to a      │
│     heading node                    │
│  3. If table detected: keep as      │
│     atomic unit within its section  │
│  4. If section > max_chunk_size:    │
│     sub-chunk within section        │
│     boundaries, not across them     │
│  5. Fallback: word-boundary-aware   │
│     fixed-size splitting            │
└──────────────┬──────────────────────┘
               │
               ▼
┌─────────────────────────────────────┐
│  Phase 3: Metadata Enrichment       │
│                                     │
│  1. Apply per-regulator metadata    │
│     extraction rules                │
│  2. Enrich each chunk with:         │
│     ├── heading_path (TOC position) │
│     ├── section_id (if applicable)  │
│     ├── page_number (from OCR)      │
│     ├── doc_type (circular/notice)  │
│     └── regulatory_metadata          │
│  3. Compute chunk metrics           │
│  4. Store enriched chunks           │
└──────────────┬──────────────────────┘
               │
               ▼
        ComplianceChunks
        (with heading_path, metadata JSONB,
         document_type, section_id)
```

### Why Structure-Aware Instead of LLM-Based Chunking

1. **Document structure is deterministic.** Indian regulatory circulars follow predictable patterns:
   - SEBI circulars: `Circular No. SEBI/HO/.../`, followed by date, subject, and numbered sections (`1.`, `2.`, `A.`, `B.`).
   - RBI notifications: `RBI/.../Notification No. ...`, with `CHAPTER`, `SECTION`, `SCHEDULE` in all caps.
   - GST notices: `GSTIN: ...`, `Notice No. ...`, `Section 73/74 of the CGST Act`.
   
   These patterns are reliably detectable with regex — no LLM needed.

2. **LLM-based chunking is slow and expensive.** Calling an LLM to determine section boundaries for every document would add 5-30 seconds per document and cost ₹0.10-0.50 in LLM tokens. The deterministic approach completes in microseconds.

3. **LLM boundaries are non-deterministic.** The same document chunked twice by an LLM could produce different section boundaries. This breaks reproducibility of downstream compliance analysis. Deterministic chunking always produces identical chunks for identical input.

4. **LLM-based chunking violates OpsCore principles.** The "deterministic first, LLM last" rule reserves LLM for reasoning tasks, not mechanical text processing. Chunking is a mechanical text processing task.

### Why Not Regex-Only Extraction

Standalone regex has known failure modes for document parsing:

1. **Nested headings.** A `SECTION` inside an `ANNEXURE` produces ambiguous matches. Regex alone cannot build a hierarchical tree.
2. **Table boundary detection.** A table row starting with `|` in a code example or an indented block quote should not trigger table detection. Context-aware parsing is required.
3. **False positives.** "Section 73 of the CGST Act" in running prose should not create a section boundary. Only top-level `SECTION \d+` in heading position should create a boundary.

The solution combines regex for pattern detection with a lightweight state machine that tracks heading level, nesting depth, and table context.

### Specific Technology Choices

| Component | Choice | Rationale |
|-----------|--------|-----------|
| **Heading detection patterns** | Regex patterns in `internal/domain/heading_detector.go` | Deterministic; maintainable patterns file per regulator type; tested against 50+ real documents |
| **Table boundary detection** | Line-based state machine tracking `\|` density and `-\+-` separator rows | Lightweight; no external dependency; handles pipe, grid, and simple markdown tables |
| **Heading tree** | Flat list sorted by line position with parent-pointer to tree (built in post-processing) | Simpler than a tree during parsing; tree is constructed after all headings are found |
| **Section-aware chunking** | Lines assigned to heading nodes; sections exceeding max_chunk_size sub-chunked at paragraph boundaries | Preserves heading context in every chunk; never splits tables or headings from their body |
| **Metadata extraction** | Regulator-specific extractors in `internal/domain/compliance_metadata.go` | Each regulator has distinct metadata patterns; per-regulator extractors are tested independently |
| **Fallback chunking** | Word-boundary-aware fixed-size splitting (same 2000-char target but split at nearest word boundary) | Graceful degradation when document has no structure; preserves readability |
| **Chunk storage** | Extended `ComplianceChunk` with `heading_path TEXT[]`, `metadata JSONB`, `section_id TEXT` | Flexible schema for regulatory and business documents alike |
| **Metrics** | Prometheus counters and histograms in `internal/telemetry/metrics.go` | Extraction success rate, chunk count reduction, token savings, structure detection latency |

### Heading Detection Patterns for Indian Regulatory Documents

```go
// Heading patterns by document origin
var headingPatterns = map[string][]HeadingPattern{
    "sebi": {
        {Pattern: `^Circular No\.\s+\S+`, Level: 1, Type: "circular_header"},
        {Pattern: `^(?:I|II|III|IV|V)\.\s+[A-Z]`, Level: 2, Type: "section"},
        {Pattern: `^[A-Z]\.\s+`, Level: 3, Type: "subsection"},
        {Pattern: `^(?:ANNEXURE|SCHEDULE)\s+\d+`, Level: 1, Type: "annexure"},
    },
    "rbi": {
        {Pattern: `^(?:CHAPTER|CHAPTER\s+\w+)`, Level: 1, Type: "chapter"},
        {Pattern: `^SECTION\s+\d+`, Level: 2, Type: "section"},
        {Pattern: `^(?:SCHEDULE|SCHEDULE\s+\w+)`, Level: 1, Type: "schedule"},
    },
    "gst": {
        {Pattern: `^Notice No\.\s+\S+`, Level: 1, Type: "notice_header"},
        {Pattern: `^Section\s+\d+[A-Z]?`, Level: 2, Type: "legal_section"},
    },
}
```

### Chunk Metadata Schema

The enriched `ComplianceChunk` carries structured metadata for downstream consumption:

```go
type EnrichedComplianceChunk struct {
    ID              string              `json:"id"`
    TenantID        string              `json:"tenant_id"`
    Content         string              `json:"content"`
    ChunkIndex      int                 `json:"chunk_index"`
    PageNumber      int                 `json:"page_number"`
    DocumentType    string              `json:"document_type"`
    HeadingPath     []string            `json:"heading_path"`     // e.g., ["Circular No. SEBI/HO/...", "3. Applicability", "3.1"]
    SectionID       string              `json:"section_id"`       // e.g., "sebi-circular-2026-003-section-3.1"
    Metadata        RegulatoryMetadata  `json:"metadata"`
    DetectionMethod string              `json:"detection_method"` // "structure" or "fallback"
}

type RegulatoryMetadata struct {
    CircularNumber  string   `json:"circular_number,omitempty"`
    Regulator       string   `json:"regulator,omitempty"`       // SEBI, RBI, GST, MCA
    EffectiveDate   string   `json:"effective_date,omitempty"`
    DateOfIssue     string   `json:"date_of_issue,omitempty"`
    Applicability   []string `json:"applicability,omitempty"`   // e.g., ["all_vendors", "gst_registered"]
    SectionsRef     []string `json:"sections_referenced,omitempty"`
    DocumentSubType string   `json:"document_sub_type,omitempty"` // circular, notification, notice, guideline
    Language        string   `json:"language,omitempty"`
}
```

## Alternatives Considered

### 1. LLM-Based Chunking

Rejected because:
- Non-deterministic — same document can produce different chunks on different calls.
- Expensive — ₹0.10-0.50 per document at scale; at 10K documents/month this is ₹1,000-5,000.
- Slow — 5-30 seconds per document vs. microseconds for deterministic parsing.
- Violates the "deterministic first, LLM last" principle.
- The LLM has no advantage over regex+state-machine for the structured patterns found in Indian regulatory documents.

### 2. External Parsing Service (Adobe PDF Extract, Amazon Textract)

Rejected because:
- Adds per-document cost ($0.015-0.05 per page for Textract).
- Introduces a new external dependency with its own API surface and reliability characteristics.
- These services return their own structure (PDF element trees) that would need translation to OpsCore's domain types.
- For the document types OpsCore processes (typed text, not handwritten), Sarvam OCR already provides reliable text extraction. Structure detection can be done on the extracted text.
- OpsCore philosophy prefers deterministic domain code over external services for core pipeline logic.

### 3. Regex-Only Extraction

Rejected as insufficient (not rejected entirely — regex is a component, not the whole solution):
- Regex cannot build a hierarchical heading tree. It can match patterns but cannot determine that heading "3.1" is a child of "3." without positional and level tracking.
- Regex cannot distinguish table rows from lines that happen to start with `|` (e.g., code examples, quoted text).
- Regex cannot track whether a `|` row is inside a table that started N lines ago — a state machine is needed for table boundaries.
- The solution uses regex for pattern matching and a lightweight state machine for structure — the two are complementary, not alternatives.

### 4. Unstructured.io or LangChain Document Loaders

Rejected because:
- Adds Python dependency for what is fundamentally string processing.
- These tools are designed for general-purpose document processing and carry significant overhead.
- OpsCore's document types (Indian regulatory circulars) are poorly represented in their training data — custom patterns would be needed anyway.
- External libraries for chunking would bypass OpsCore's domain layer, violating the hexagonal architecture.

## Consequences

### Benefits

1. **Higher ingestion quality.** Section-aware chunks preserve document context. The compliance agent receives chunks with heading paths and metadata, reducing the need to re-discover document structure in every LLM call.

2. **Lower LLM token usage.** Fewer, richer chunks mean fewer LLM calls and smaller prompts. Target: 40-60% reduction in chunk count for regulatory documents, translating to 30-50% reduction in LLM token consumption for compliance analysis.

3. **Table-preserving chunks.** Tables are kept atomic, making them usable by downstream systems for exact-value lookups (e.g., "what is the late fee percentage in this circular's fee schedule table?").

4. **Deterministic and reproducible.** Same document → same chunks every time. No LLM drift, no model versioning, no prompt engineering for chunking.

5. **Metadata enrichment at ingestion.** Circular number, regulator, effective date, and section references are extracted once and stored, rather than being re-discovered by every downstream component.

6. **Fallback safety.** Documents without detectable structure (scanned handwritten notes, irregular formats) fall back to word-boundary-aware fixed-size chunking — never worse than the current behavior.

7. **Measurable quality.** Metrics track extraction success rate (target: ≥90% for regulatory documents), chunk count reduction, and token savings. These metrics drive continuous improvement of heading detection patterns.

### Trade-offs / Risks

1. **More complex chunker.** The current chunker is 57 lines of straightforward Go. The structure-aware chunker is estimated at 400-600 lines across `chunker.go`, `heading_detector.go`, `table_detector.go`, and `compliance_metadata.go`. More code means more maintenance and more edge cases.

2. **Heading detection patterns need maintenance.** Each regulator's document format evolves over time. New heading patterns must be added as formats change. Mitigation: patterns are in a single configurable file (`heading_detector.go`) with per-regulator sections. Adding a new pattern is a one-line change.

3. **False positives in structure detection.** A line that looks like a heading but is actually body text (e.g., "Section 73 of the CGST Act states...") could create a spurious section boundary. Mitigation: heading detection requires explicit formatting signals — standalone capitalization, numbered prefix, or known regulatory patterns. Running text is not matched.

4. **Table detection may fail for complex tables.** Merged cells, multi-line rows, and irregular column alignments are common in Indian regulatory documents. Mitigation: the table detector uses conservative heuristics (minimum 3 `|`-separated columns, consistent separator row). Unparseable tables fall through to section-aware chunking (treated as regular text) rather than failing ingestion.

5. **Needs real-document corpus for test coverage.** Section-aware chunking must be tested against 50+ real regulatory documents to validate pattern coverage. Without this corpus, edge cases will surface in production. Mitigation: build a test corpus from publicly available SEBI/RBI/GST circulars during Phase 1. The `tests/agentic` directory is the natural home.

6. **Metadata extraction depends on document quality.** If OCR quality is poor, metadata patterns may not match. A circular number that reads as "Circu1ar No." (OCR error) will fail regex extraction. Mitigation: metadata extraction is best-effort — the `RegulatoryMetadata` struct uses `omitempty` and missing fields do not block ingestion. The `extraction_success` metric tracks per-field success rates.

## Implementation Plan

### Phase 1: Structure Detection + Section-Aware Chunking (Now)

**Goal:** Replace fixed-size chunking with structure-aware chunking. Headings preserved, tables atomic, no section boundary splits.

| Task | Files Changed / Created | Effort |
|------|------------------------|--------|
| **1a. Heading detector** | `internal/domain/heading_detector.go` (new) — regex patterns for Indian regulatory headings (`##`, `SECTION`, `Circular No.`, `ANNEXURE`, numbered lists `1.`, `A.`, `(i)`) | 2 days |
| **1b. Table boundary detector** | `internal/domain/table_detector.go` (new) — line-based state machine detecting `|`+`-+-` table patterns, tracking start/end boundaries | 1 day |
| **1c. Section-aware chunker** | `internal/domain/chunker.go` — rewrite `ChunkDocument` to accept heading tree and table boundaries; never split tables; sub-chunk oversized sections at paragraph boundaries | 2 days |
| **1d. Heading tree builder** | `internal/domain/heading_tree.go` (new) — convert sorted heading list to parent-pointer tree; assign each text line to a heading node | 1 day |
| **1e. Fallback word-boundary splitter** | `internal/domain/chunker.go` — when structure detection yields no headings, split at word boundaries instead of character boundaries (target 2000 chars, snap to nearest space) | 0.5 day |
| **1f. Enriched ComplianceChunk type** | `internal/domain/types.go` — add `EnrichedComplianceChunk` struct with `HeadingPath []string`, `SectionID string`, `Metadata JSONB`; keep backward compatibility with existing `ComplianceChunk` | 0.5 day |
| **1g. ShouldChunk update** | `internal/domain/chunker.go` — update `ShouldChunk` to handle regulatory document types; ensure compliance circulars are always chunked | 0.25 day |
| **1h. Metrics** | `internal/telemetry/metrics.go` — add `chunk_count_reduction` histogram, `structure_detection_success` counter, `fallback_reason` counter | 0.5 day |
| **1i. Tests** | `tests/unit/heading_detector_test.go`, `tests/unit/table_detector_test.go`, `tests/unit/chunker_test.go` — update for section-aware tests with real regulatory text samples | 2 days |

**Phase 1 success criteria:**
- Heading detection succeeds for ≥90% of regulatory documents in test corpus.
- Tables are never split across chunks (verified by test assertions with pipe-separated tables).
- Chunk count reduced by ≥40% compared to fixed-size chunking for regulatory documents ≥10 pages.
- Fallback to word-boundary-aware splitting occurs only when no headings are detected.
- Zero chunk boundaries fall mid-word.

### Phase 2: Metadata Enrichment + TOC Index (Next 2-3 Weeks)

**Goal:** Extract structured metadata at ingestion time. Build a table of contents index for hierarchical navigation.

| Task | Files Changed / Created | Effort |
|------|------------------------|--------|
| **2a. Regulatory metadata extractor** | `internal/domain/compliance_metadata.go` (new) — per-regulator extractors for circular number, date, regulator name, applicability, sections referenced | 2 days |
| **2b. SEBI metadata extractor** | `internal/domain/compliance_metadata.go` — patterns for `Circular No. SEBI/HO/...`, date formats, subject line parsing | 0.5 day |
| **2c. RBI metadata extractor** | `internal/domain/compliance_metadata.go` — patterns for `RBI/.../Notification No. ...`, chapter/schedule references | 0.5 day |
| **2d. GST metadata extractor** | `internal/domain/compliance_metadata.go` — patterns for `Notice No. ...`, `Section 73/74`, `GSTIN` references | 0.5 day |
| **2e. Metadata integration in ingestion** | `internal/agents/ingestion_agent.go` — call metadata extraction after chunking; store metadata JSONB alongside chunks | 1 day |
| **2f. TOC index builder** | `internal/domain/toc_index.go` (new) — build hierarchical TOC from heading tree; store as JSONB at document level | 1 day |
| **2g. TOC storage migration** | `internal/adapters/postgres/migrations.go` — add `toc_index JSONB` column to `documents` table | 0.25 day |
| **2h. Extraction success metrics** | `internal/telemetry/metrics.go` — per-field extraction success rate, metadata completeness score | 0.25 day |
| **2i. Tests** | `tests/unit/compliance_metadata_test.go`, `tests/unit/toc_index_test.go`, `tests/agentic/ingestion_pipeline_test.go` | 2 days |

**Phase 2 success criteria:**
- Metadata extracted for ≥90% of regulatory document types in test corpus.
- Circular number, regulator, and effective date extracted with ≥95% precision.
- TOC index built at ingestion time for all documents with section-aware chunks.
- Extraction failures are non-fatal — document is still ingested with available metadata.
- Per-field extraction success rates visible in metrics dashboard.

## File Map

### Existing Files Modified

| File | Phase | Change |
|------|-------|--------|
| `internal/domain/chunker.go` | 1 | Rewrite `ChunkDocument` to section-aware algorithm; preserve heading hierarchy, tables, add word-boundary-aware fallback |
| `internal/domain/types.go` | 1 | Add `EnrichedComplianceChunk` struct with `HeadingPath []string`, `SectionID string`, `Metadata RegulatoryMetadata` |
| `internal/domain/document_classifier.go` | 1 | Optionally use detected headings for classification boost |
| `internal/agents/compliance_agent.go` | 1, 2 | Consume `EnrichedComplianceChunk`; use heading path and metadata in LLM prompts |
| `internal/telemetry/metrics.go` | 1, 2 | Add chunk count reduction, structure detection success, extraction success metrics |
| `internal/adapters/postgres/migrations.go` | 1 | Add `heading_path TEXT[]`, `metadata JSONB`, `section_id TEXT` columns to `compliance_chunks` |
| `internal/adapters/postgres/migrations.go` | 2 | Add `toc_index JSONB` column to `documents` table |

### New Files

| File | Phase | Purpose |
|------|-------|---------|
| `internal/domain/heading_detector.go` | 1 | Regex patterns and detection logic for Indian regulatory headings |
| `internal/domain/heading_tree.go` | 1 | Build hierarchical heading tree from detected headings |
| `internal/domain/table_detector.go` | 1 | State-machine-based table boundary detection for pipe and grid tables |
| `internal/domain/compliance_metadata.go` | 2 | Per-regulator metadata extractors (SEBI, RBI, GST, MCA) |
| `internal/domain/toc_index.go` | 2 | Build hierarchical TOC from heading tree |
| `tests/unit/heading_detector_test.go` | 1 | Heading detection unit tests with real regulatory text samples |
| `tests/unit/table_detector_test.go` | 1 | Table boundary detection unit tests |
| `tests/unit/compliance_metadata_test.go` | 2 | Metadata extraction unit tests per regulator |
| `tests/unit/toc_index_test.go` | 2 | TOC index builder unit tests |
| `tests/agentic/ingestion_pipeline_test.go` | 1, 2 | End-to-end ingestion pipeline tests with controlled document inputs |
| `tests/live/ocr/compliance_ingestion_test.go` | 2 | Live provider test for end-to-end OCR → structure detection → metadata extraction (//go:build live) |

## Compliance with OpsCore Principles

### "Deterministic first, LLM last"

The structured ingestion pipeline explicitly encodes this:

- **Heading detection**: Pure Go regex — no LLM, no external API.
- **Table boundary detection**: State machine over text lines — no LLM, no ML.
- **Section-aware chunking**: Line-to-heading assignment with paragraph-boundary sub-chunking — pure arithmetic and string operations.
- **Metadata extraction**: Per-regulator regex extractors — no LLM.
- **TOC index building**: Tree construction from heading levels — pure data structure manipulation.
- **LLM**: Not used for chunking at all. LLM is reserved for downstream compliance gap analysis, which benefits from the richer, structured chunks produced by this pipeline.

### Multi-tenant isolation

- All chunk storage includes `tenant_id` as a required field.
- The heading tree and metadata are document-level attributes, already scoped by `tenant_id` through the document ownership chain.
- Metrics are labeled by `tenant_id` for per-tenant tracking of extraction quality.
- No tenant can see another tenant's document structure or metadata — PostgreSQL RLS applies.

### Observability

- Every ingestion run records: structure detection method (structure/fallback), chunk count, detection latency, number of tables found.
- Extraction success rate is tracked per field (`circular_number_extracted`, `regulator_extracted`, `effective_date_extracted`).
- Chunk count reduction vs. fixed-size baseline is computed and exposed as a Prometheus histogram.
- Fallback events are logged with reason (`no_headings`, `low_confidence`, `non_text_content`) for continuous improvement of heading patterns.
- All metrics are labeled by `document_type` and `regulator` for targeted analysis.

## Related ADRs

- **ADR-001**: Retrieval Architecture for Compliance & Document Search — establishes section-aware chunking as a prerequisite for the structured index (Phase 1a-1c). This ADR provides the chunking implementation that ADR-001 depends on.
- **ADR-0002**: Vendor Onboarding — Asynchronous Real-Time Workflow with Queue-Backed Processing — vendor onboarding consumes ingested documents; structured chunking improves the document analysis step in the vendor workflow.
