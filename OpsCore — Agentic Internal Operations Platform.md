Now I have enough context. Let me write the comprehensive PRD + HLD + LLD + Dev Plan + Checklist + Tests document with OSS replacements.

------

# OpsCore — Agentic Internal Operations Platform

## Complete Engineering Blueprint (OSS Stack)

------

## OSS Stack Replacement Map

Before diving into documents, here's the full substitution from Azure-native to OSS/self-hosted:

| Azure Service                       | OSS Replacement                      | Rationale                                                    |
| ----------------------------------- | ------------------------------------ | ------------------------------------------------------------ |
| Azure Document Intelligence         | **Docling** (IBM OSS)                | Table/formula/reading-order detection, batch processing, MCP-native [docling](https://www.docling.ai/) |
| Azure AI Search (hybrid BM25+dense) | **Qdrant** (with BM25 payload index) | Native hybrid sparse+dense search, no managed service needed |
| Azure Storage Queue                 | **Redis Streams** or **RabbitMQ**    | Celery-compatible, dead-letter, retry policies               |
| Azure Bicep IaC                     | **Docker Compose → Helm**            | Self-hosted first, Helm for k8s later                        |
| Azure DI for vendor docs            | **Docling + Tesseract fallback**     | Full pipeline, no API cost                                   |
| Compliance web scraper              | **Crawl4AI**                         | Async, LLM-aware, robots.txt-respecting [crawl4](https://www.crawl4.com/blog/crawl4ai-security-ethics-and-legal-web-scraping) |
| Knowledge graph / entity memory     | **Graphiti + Neo4j**                 | Real-time entity graph, hybrid search, agent-native [ainews](https://ainews.cool/article/20260328-20250415-graphiti-real-time-knowledge-graphs-ai-agents)[youtube](https://www.youtube.com/watch?v=H2Cb5wbcRzo) |

------

## 1. Product Requirements Document (PRD)

## 1.1 Problem Statement

Mid-size Indian B2B companies (50–500 employees) lose 20–40 hours/week on three manual ops nightmares: (1) manually keying PDF invoices into Tally/QuickBooks, (2) missing regulatory updates from SEBI/RBI/GST portals discovered only at audit, and (3) vendor onboarding cycles of 2–3 weeks via email chains.

## 1.2 Target User & ICP

- **Primary:** Finance Manager, Procurement Head, Compliance Officer at 50–500 employee B2B companies
- **Secondary:** IT Admin who deploys and monitors the platform
- **Geography:** India (GST-aware, INR amounts, Indian regulatory bodies)
- **Price point:** ₹15,000–₹40,000/month SaaS or self-hosted perpetual license

## 1.3 Success Metrics

| Metric                                  | Target                             |
| --------------------------------------- | ---------------------------------- |
| Document processing time                | < 45 seconds per PDF               |
| Extraction accuracy (structured fields) | ≥ 95% on invoices                  |
| Compliance gap detection recall         | ≥ 90% vs. manual audit             |
| Vendor onboarding cycle                 | ≤ 2 business days (from 14)        |
| HITL queue clearance SLA                | < 4 hours per item                 |
| System uptime                           | 99.5%                              |
| LLM cost per workflow run               | < ₹5 (invoice), < ₹15 (compliance) |

## 1.4 Non-Goals (v1)

- No mobile app
- No real-time ERP bi-directional sync (writes only in v1)
- No custom workflow builder (3 predefined workflows only)
- No multi-language UI (English only)
- No voice interface

## 1.5 Personas

**Priya (Finance Manager):** Uploads 30–50 invoices/week, wants auto-entry into QuickBooks, needs to review only flagged items. Current pain: 3 hours/day of data entry.

**Rahul (Compliance Officer):** Needs to track SEBI/RBI/GST circulars monthly, currently reads PDFs manually or misses them. Fear: surprise audit findings.

**Karan (Procurement Head):** Onboards 5–10 new vendors/month. Currently sends 12+ emails per vendor. Pain: waiting on Finance and Legal sign-off.

## 1.6 Functional Requirements

## Workflow 1 — Document Ingestion Pipeline

- FR-1.1: Accept PDF upload via drag-drop UI or API POST
- FR-1.2: Classify document type (invoice / contract / GST notice / PO) within 5 seconds
- FR-1.3: Extract structured fields per document type using Docling + LLM validation
- FR-1.4: Validate completeness, cross-reference vendor registry, flag anomalies
- FR-1.5: Route low-confidence or high-value items to HITL review queue
- FR-1.6: Auto-sync approved documents to QuickBooks (bill/PO) and PostgreSQL audit trail
- FR-1.7: Expose confidence score per extracted field in review UI

## Workflow 2 — Compliance Monitoring Pipeline

- FR-2.1: Accept regulatory PDF via manual upload or Crawl4AI scheduled scraper
- FR-2.2: Chunk document semantically and store in Qdrant with section metadata
- FR-2.3: Run gap analysis against company policy documents using hybrid search
- FR-2.4: Every gap finding must include page + paragraph citation (RAGAS faithfulness ≥ 0.85)
- FR-2.5: Map gaps to departments, assign owners from department registry
- FR-2.6: Create Linear/Jira ticket and send Slack notification per owner
- FR-2.7: Log all runs to compliance dashboard with status and deadline tracking

## Workflow 3 — Vendor Onboarding Pipeline

- FR-3.1: Accept vendor request form (name, category, contact)
- FR-3.2: Generate document checklist based on vendor category (IT / Services / Goods)
- FR-3.3: Verify uploaded documents (GST cert, PAN, bank details) using Docling extraction
- FR-3.4: Run deterministic risk scoring (blacklist, duplicate detection, fuzzy match)
- FR-3.5: Route through tiered approval via Slack interactive buttons
- FR-3.6: Register approved vendor in QuickBooks + initialize Trust Battery in PostgreSQL
- FR-3.7: Send welcome email to vendor on approval

## Platform Requirements

- FR-P.1: Multi-tenant isolation (row-level security in PostgreSQL)
- FR-P.2: Full audit trail for every workflow step
- FR-P.3: Langfuse tracing on every LLM call
- FR-P.4: Role-based access: Admin, Finance, Compliance, Procurement, Viewer
- FR-P.5: Workflow retry with exponential backoff (max 3 attempts)

------

## 2. High-Level Design (HLD)

## 2.1 System Architecture Overview

```
text┌─────────────────────────────────────────────────────────────────┐
│                        CLIENTS                                  │
│   React UI (Next.js)    Slack Bolt     REST API consumers       │
└────────────┬────────────────┬──────────────────┬───────────────┘
             │                │                  │
             ▼                ▼                  ▼
┌─────────────────────────────────────────────────────────────────┐
│                     API GATEWAY LAYER                           │
│              FastAPI (Python 3.12, async)                       │
│         Auth: JWT + tenant middleware + RBAC                    │
└────────────┬────────────────────────────────────────────────────┘
             │
    ┌────────┴──────────┐
    ▼                   ▼
TEMPORAL WORKER     WORKFLOW ROUTER
(Durable execution)   (routes by type)
    │
    ├─── Workflow 1: Document Ingestion
    │         [Docling OCR → LangGraph Agent → HITL Queue → QuickBooks MCP]
    │
    ├─── Workflow 2: Compliance Monitoring
    │         [Crawl4AI/Upload → Docling → Qdrant → LangGraph Agent → Slack/Linear]
    │
    └─── Workflow 3: Vendor Onboarding
              [Form → Docling → Risk Scorer → Slack HITL → QuickBooks MCP]

┌─────────────────────────────────────────────────────────────────┐
│                     DATA LAYER                                  │
│  PostgreSQL (primary) │ Qdrant (vectors) │ Neo4j/Graphiti       │
│  pgvector (embeddings)│ Redis Streams    │ (entity memory/graph)│
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                   OBSERVABILITY LAYER                           │
│     Langfuse (LLM traces)  │  RAGAS (faithfulness)             │
│     Prometheus + Grafana   │  Temporal UI (workflow state)      │
└─────────────────────────────────────────────────────────────────┘
```

## 2.2 OSS Component Roles

**Docling** replaces Azure Document Intelligence. It handles PDF-to-structured-data conversion with table detection, reading order, and formula parsing. Runs locally via `DocumentConverter`, supports RapidOCR for scanned pages. Zero per-page API cost.docling+2

**Crawl4AI** replaces manual regulatory web scraping. Async, LLM-aware crawler with built-in robots.txt compliance, domain allow/block lists, and audit logging. Scheduled via Temporal cron workflows to hit SEBI, RBI, and GST portals weekly.crawl4+1

**Graphiti + Neo4j** replaces Azure AI Search knowledge layer. Graphiti maintains a real-time entity graph: vendors, clauses, regulations, departments, and their relationships. Hybrid search (semantic vector + BM25 keyword) enables both "find vendors similar to X" and "find all GST regulations mentioning Input Tax Credit."[ainews](https://ainews.cool/article/20260328-20250415-graphiti-real-time-knowledge-graphs-ai-agents)[youtube](https://www.youtube.com/watch?v=H2Cb5wbcRzo)

**Qdrant** handles dense vector storage for compliance document chunks with metadata filters (source, date, regulation type, section).

**Redis Streams** replaces Azure Storage Queue. Three streams: `ops:extraction`, `ops:compliance`, `ops:onboarding`. Dead-letter stream: `ops:dlq`. Celery connects to Redis as broker + result backend.

## 2.3 Workflow Data Flow

```
textPDF Upload (multipart/form-data)
    → FastAPI /api/v1/documents/ingest
    → Validates tenant context + file type
    → Stores to MinIO (S3-compatible OSS object store)
    → Publishes job_id to Redis Stream ops:extraction
    → Temporal workflow starts with job_id
    → Returns 202 Accepted + job_id to client
    → SSE /api/v1/jobs/{job_id}/status for real-time UI updates
```

## 2.4 Multi-Tenancy Model

Row-level security via PostgreSQL `tenant_id` column on all tables. FastAPI middleware injects `tenant_id` from JWT claims. Qdrant uses collection-per-tenant or payload filter. Neo4j uses subgraph isolation via tenant label. MinIO uses bucket-per-tenant.

## 2.5 Trust Battery State Machine

```
textPROBATION ──(30 days clean + 3 successful invoices)──► STANDARD
STANDARD  ──(90 days clean + 10 transactions)─────────► CORE
CORE      ──(180 days + strategic contract)────────────► STRATEGIC

Any tier:
  ├─ Fraud detected ────────────────────────────────► PROBATION (reset)
  ├─ 3 consecutive errors ──────────────────────────► tier - 1
  └─ 90 days inactive ──────────────────────────────► tier - 1
```

------

## 3. Low-Level Design (LLD)

## 3.1 Database Schema (PostgreSQL)

```
sql-- Core tenant isolation
CREATE TABLE tenants (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    slug        TEXT UNIQUE NOT NULL,
    plan        TEXT DEFAULT 'starter',
    created_at  TIMESTAMPTZ DEFAULT now()
);

-- Document jobs
CREATE TABLE document_jobs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID REFERENCES tenants(id) NOT NULL,
    workflow_type   TEXT NOT NULL,  -- 'ingestion' | 'compliance' | 'onboarding'
    status          TEXT NOT NULL DEFAULT 'pending',
    input_path      TEXT,           -- MinIO object key
    temporal_run_id TEXT,
    created_at      TIMESTAMPTZ DEFAULT now(),
    updated_at      TIMESTAMPTZ DEFAULT now()
);

-- Extracted documents
CREATE TABLE extracted_documents (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID REFERENCES tenants(id) NOT NULL,
    job_id          UUID REFERENCES document_jobs(id),
    doc_type        TEXT NOT NULL,  -- 'invoice' | 'contract' | 'gst_notice' | 'po'
    raw_extraction  JSONB,          -- Docling output
    validated_data  JSONB,          -- Post-validation structured data
    confidence      JSONB,          -- Per-field confidence scores
    status          TEXT DEFAULT 'pending_review',
    erp_synced      BOOLEAN DEFAULT false,
    created_at      TIMESTAMPTZ DEFAULT now()
);

-- HITL queue
CREATE TABLE hitl_queue (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID REFERENCES tenants(id) NOT NULL,
    job_id          UUID REFERENCES document_jobs(id),
    queue_type      TEXT NOT NULL,  -- 'low_confidence' | 'high_value' | 'new_vendor'
    payload         JSONB NOT NULL,
    assigned_to     UUID,
    status          TEXT DEFAULT 'pending',
    reviewed_at     TIMESTAMPTZ,
    reviewer_id     UUID,
    created_at      TIMESTAMPTZ DEFAULT now()
);

-- Vendors
CREATE TABLE vendors (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID REFERENCES tenants(id) NOT NULL,
    name                TEXT NOT NULL,
    gst_number          TEXT,
    pan_number          TEXT,
    category            TEXT,   -- 'IT' | 'Services' | 'Goods'
    trust_tier          TEXT DEFAULT 'PROBATION',
    trust_score         INTEGER DEFAULT 0,
    qb_vendor_id        TEXT,
    consecutive_errors  INTEGER DEFAULT 0,
    last_active_at      TIMESTAMPTZ,
    created_at          TIMESTAMPTZ DEFAULT now()
);

-- Compliance gaps
CREATE TABLE compliance_gaps (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID REFERENCES tenants(id) NOT NULL,
    regulation_id   UUID NOT NULL,
    gap_description TEXT NOT NULL,
    severity        TEXT,           -- 'LOW' | 'MEDIUM' | 'HIGH' | 'CRITICAL'
    deadline        DATE,
    department      TEXT,
    owner_id        UUID,
    citation_page   INTEGER,
    citation_para   TEXT,
    ticket_id       TEXT,
    status          TEXT DEFAULT 'open',
    ragas_score     FLOAT,
    created_at      TIMESTAMPTZ DEFAULT now()
);

-- Audit trail (append-only)
CREATE TABLE audit_events (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   UUID NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id   UUID NOT NULL,
    action      TEXT NOT NULL,
    actor_id    UUID,
    actor_type  TEXT,   -- 'user' | 'agent' | 'system'
    diff        JSONB,
    created_at  TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX idx_audit_entity ON audit_events(entity_type, entity_id);
CREATE INDEX idx_audit_tenant ON audit_events(tenant_id, created_at DESC);
```

## 3.2 Pydantic Extraction Schemas

```
python# schemas/documents.py
from pydantic import BaseModel, Field
from typing import Optional
from decimal import Decimal

class FieldWithConfidence(BaseModel):
    value: str | None
    confidence: float = Field(ge=0.0, le=1.0)

class InvoiceSchema(BaseModel):
    invoice_number: FieldWithConfidence
    vendor_name: FieldWithConfidence
    vendor_gst: FieldWithConfidence
    invoice_date: FieldWithConfidence
    due_date: FieldWithConfidence
    line_items: list[dict]           # [{description, qty, rate, amount}]
    subtotal: FieldWithConfidence
    gst_amount: FieldWithConfidence
    total_amount: FieldWithConfidence
    bank_details: FieldWithConfidence

class GSTNoticeSchema(BaseModel):
    notice_type: FieldWithConfidence  # 'DRC-01' | 'DRC-07' | 'ASMT-10' etc.
    arn_number: FieldWithConfidence
    tax_period: FieldWithConfidence
    demand_amount: FieldWithConfidence
    response_deadline: FieldWithConfidence
    issuing_office: FieldWithConfidence

class VendorDocSchema(BaseModel):
    doc_type: str   # 'GST_CERT' | 'PAN' | 'BANK_DETAILS' | 'MSME'
    extracted_number: FieldWithConfidence
    entity_name: FieldWithConfidence
    validity_date: FieldWithConfidence | None
    is_format_valid: bool
```

## 3.3 Docling Extraction Service

```
python# services/docling_extractor.py
from docling.document_converter import DocumentConverter, InputFormat, PdfFormatOption
from docling.datamodel.pipeline_options import PdfPipelineOptions, RapidOcrOptions

class DoclingExtractor:
    def __init__(self):
        pipeline_opts = PdfPipelineOptions(do_ocr=True)
        self.converter = DocumentConverter(
            format_options={
                InputFormat.PDF: PdfFormatOption(
                    pipeline_options=pipeline_opts
                )
            }
        )

    async def extract(self, file_path: str) -> dict:
        result = await asyncio.to_thread(
            self.converter.convert, source=file_path
        )
        doc = result.document
        return {
            "markdown": doc.export_to_markdown(),
            "tables": [t.export_to_dataframe().to_dict() for t in doc.tables],
            "text_blocks": [b.text for b in doc.texts],
            "metadata": {
                "page_count": len(doc.pages),
                "has_tables": len(doc.tables) > 0,
            }
        }
```

## 3.4 LangGraph Agent — Workflow 1 Extraction Agent

```
python# agents/extraction_agent.py
from langgraph.graph import StateGraph, END
from typing import TypedDict, Annotated
import operator

class ExtractionState(TypedDict):
    job_id: str
    tenant_id: str
    raw_text: str
    doc_type: str
    extraction_attempt: int
    extracted_data: dict | None
    confidence_scores: dict
    needs_hitl: bool
    hitl_reason: str | None
    langfuse_trace_id: str

def classify_document(state: ExtractionState) -> ExtractionState:
    """Deterministic classifier — no LLM, uses regex + keyword matching."""
    text = state["raw_text"][:500].lower()
    if any(k in text for k in ["tax invoice", "gstin", "cgst", "sgst"]):
        doc_type = "invoice"
    elif any(k in text for k in ["purchase order", "po number", "delivery date"]):
        doc_type = "purchase_order"
    elif any(k in text for k in ["notice", "drc-01", "arn", "demand"]):
        doc_type = "gst_notice"
    else:
        doc_type = "contract"
    return {**state, "doc_type": doc_type}

async def run_llm_extraction(state: ExtractionState) -> ExtractionState:
    """LLM extraction with Pydantic schema enforcement."""
    schema = DOC_TYPE_SCHEMAS[state["doc_type"]]
    result = await llm_client.structured_output(
        prompt=EXTRACTION_PROMPT.format(
            doc_type=state["doc_type"],
            text=state["raw_text"]
        ),
        output_schema=schema,
        trace_id=state["langfuse_trace_id"]
    )
    return {**state, "extracted_data": result.dict(), 
            "confidence_scores": compute_confidence(result)}

def validation_gate(state: ExtractionState) -> str:
    """Router — returns next node name."""
    scores = state["confidence_scores"]
    avg_confidence = sum(scores.values()) / len(scores)
    
    # Business rules for HITL routing
    if avg_confidence < 0.80:
        return "route_to_hitl"
    if float(state["extracted_data"].get("total_amount", {}).get("value", 0)) > 100000:
        return "route_to_hitl"
    return "proceed_to_erp"

# Build graph
workflow = StateGraph(ExtractionState)
workflow.add_node("classify", classify_document)
workflow.add_node("extract", run_llm_extraction)
workflow.add_node("route_to_hitl", send_to_hitl_queue)
workflow.add_node("proceed_to_erp", sync_to_quickbooks)
workflow.set_entry_point("classify")
workflow.add_edge("classify", "extract")
workflow.add_conditional_edges("extract", validation_gate)
```

## 3.5 Graphiti Integration — Vendor Knowledge Graph

```
python# services/graphiti_service.py
# Graphiti + Neo4j for vendor entity memory and relationship tracking
from graphiti_core import Graphiti
from graphiti_core.nodes import EpisodeType

graphiti = Graphiti(neo4j_uri, neo4j_user, neo4j_password)

async def ingest_vendor_entity(vendor: dict, tenant_id: str):
    """Store vendor as episode in knowledge graph."""
    await graphiti.add_episode(
        name=f"vendor_onboard_{vendor['id']}",
        episode_body=f"""
            Vendor: {vendor['name']}
            GST: {vendor['gst_number']}
            Category: {vendor['category']}
            Bank IFSC: {vendor['ifsc_code']}
            Risk Score: {vendor['risk_score']}
            Onboarded: {vendor['created_at']}
        """,
        source=EpisodeType.text,
        source_description="vendor_onboarding",
        group_id=tenant_id
    )

async def find_similar_vendors(vendor_name: str, tenant_id: str) -> list:
    """Hybrid search for duplicate/similar vendor detection."""
    results = await graphiti.search(
        query=f"vendor named {vendor_name}",
        group_ids=[tenant_id],
        num_results=5
    )
    return results
```

## 3.6 Crawl4AI Compliance Scraper

```
python# services/compliance_scraper.py
from crawl4ai import AsyncWebCrawler, CrawlerRunConfig
from crawl4ai.content_filter_strategy import PruningContentFilter

REGULATORY_SOURCES = {
    "sebi": "https://www.sebi.gov.in/legal/circulars/",
    "rbi": "https://www.rbi.org.in/Scripts/BS_CircularIndexDisplay.aspx",
    "gst": "https://cbic-gst.gov.in/gst-goods-services-rates.html"
}

async def scrape_regulatory_updates(source: str, since_date: str) -> list[dict]:
    config = CrawlerRunConfig(
        content_filter=PruningContentFilter(threshold=0.5),
        word_count_threshold=200,
        exclude_external_links=True,
    )
    async with AsyncWebCrawler() as crawler:
        result = await crawler.arun(
            url=REGULATORY_SOURCES[source],
            config=config
        )
    # Extract PDF links from crawled content
    pdf_links = extract_pdf_links(result.markdown, since_date)
    return pdf_links
```

## 3.7 Temporal Workflow Definition

```
python# workflows/document_ingestion.py
from temporalio import workflow, activity
from datetime import timedelta

@workflow.defn
class DocumentIngestionWorkflow:
    @workflow.run
    async def run(self, job_id: str, tenant_id: str, file_path: str) -> dict:
        # Step 1 + 2: Extract with Docling + LLM
        extraction_result = await workflow.execute_activity(
            extract_document,
            args=[job_id, tenant_id, file_path],
            start_to_close_timeout=timedelta(minutes=5),
            retry_policy=RetryPolicy(
                maximum_attempts=3,
                initial_interval=timedelta(seconds=5),
                backoff_coefficient=2.0,
            ),
        )
        # Step 3: Validation gate
        if extraction_result["needs_hitl"]:
            # Step 4: HITL — wait for human signal (up to 24 hours)
            approval = await workflow.wait_condition(
                lambda: self._approval_received,
                timeout=timedelta(hours=24)
            )
        # Step 5: ERP Sync
        sync_result = await workflow.execute_activity(
            sync_to_erp,
            args=[extraction_result, tenant_id],
            start_to_close_timeout=timedelta(minutes=2),
        )
        return {"status": "completed", "erp_id": sync_result["qb_id"]}

    @workflow.signal
    def approval_signal(self, approved: bool, reviewer_id: str):
        self._approval_received = approved
        self._reviewer_id = reviewer_id
```

## 3.8 Risk Scoring Engine (Workflow 3, fully determine

Continuing directly from **Section 3.8** — the Risk Scoring Engine — completing the full LLD, then Dev Plan, Checklist, and Tests.

> **⚠️ Security Note:** Ensure you use **Graphiti ≥ 0.28.2** — versions before that had a critical Cypher injection vulnerability (CVE-2026-32247, CVSS 8.1) where `SearchFilters.node_labels` values were concatenated unsanitized into Cypher queries. The fix is confirmed in 0.28.2+.app.opencve+1

------

## 3.8 Risk Scoring Engine (Workflow 3 — Pure Python, No LLM)

```
python# services/risk_scorer.py
import re
from rapidfuzz import fuzz
from dataclasses import dataclass

@dataclass
class RiskResult:
    score: int          # 0–100
    tier: str           # 'LOW' | 'MEDIUM' | 'HIGH'
    flags: list[str]

GST_REGEX = r'^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z]{1}[1-9A-Z]{1}Z[0-9A-Z]{1}$'
PAN_REGEX  = r'^[A-Z]{5}[0-9]{4}[A-Z]{1}$'
IFSC_REGEX = r'^[A-Z]{4}0[A-Z0-9]{6}$'

async def compute_vendor_risk(
    vendor: dict,
    existing_vendors: list[dict],
    blacklist: list[str],
    tenant_id: str
) -> RiskResult:
    score = 0
    flags = []

    # 1. Format validation
    if not re.match(GST_REGEX, vendor.get("gst_number", "")):
        score += 25; flags.append("INVALID_GST_FORMAT")
    if not re.match(PAN_REGEX, vendor.get("pan_number", "")):
        score += 20; flags.append("INVALID_PAN_FORMAT")
    if not re.match(IFSC_REGEX, vendor.get("ifsc_code", "")):
        score += 15; flags.append("INVALID_IFSC_FORMAT")

    # 2. Blacklist check
    if vendor["gst_number"] in blacklist or vendor["pan_number"] in blacklist:
        score += 50; flags.append("BLACKLISTED_ENTITY")

    # 3. Duplicate bank account
    if any(v["bank_account"] == vendor["bank_account"] for v in existing_vendors):
        score += 40; flags.append("DUPLICATE_BANK_ACCOUNT")

    # 4. Fuzzy name match (potential duplicate vendor)
    for ev in existing_vendors:
        similarity = fuzz.token_sort_ratio(vendor["name"], ev["name"])
        if similarity > 85:
            score += 20
            flags.append(f"SIMILAR_VENDOR_NAME:{ev['id']}:{similarity}%")
            break

    # 5. Graphiti graph check — connected to known risky entities
    risky_connections = await graphiti_service.find_risky_connections(
        vendor["gst_number"], tenant_id
    )
    if risky_connections:
        score += 30; flags.append("CONNECTED_TO_RISKY_ENTITY")

    # Tier assignment
    tier = "LOW" if score < 30 else "MEDIUM" if score < 60 else "HIGH"
    return RiskResult(score=score, tier=tier, flags=flags)
```

## 3.9 Compliance Gap Analysis Agent (Workflow 2, LangGraph)

```
python# agents/compliance_agent.py
from langgraph.graph import StateGraph
from ragas.metrics import Faithfulness
from ragas import evaluate, EvaluationDataset

class ComplianceState(TypedDict):
    regulation_id: str
    tenant_id: str
    regulation_chunks: list[dict]   # from Qdrant
    company_policy_chunks: list[dict]
    gaps: list[dict]
    citations: list[dict]
    ragas_score: float | None
    langfuse_trace_id: str

async def hybrid_search_node(state: ComplianceState) -> ComplianceState:
    """Qdrant hybrid search: dense + BM25 sparse on regulation text."""
    results = await qdrant_client.search_hybrid(
        collection=f"compliance_{state['tenant_id']}",
        query_text=state["regulation_summary"],
        query_vector=await embed(state["regulation_summary"]),
        sparse_vector=bm25_encode(state["regulation_summary"]),
        limit=20,
        with_payload=True
    )
    return {**state, "company_policy_chunks": results}

async def gap_analysis_node(state: ComplianceState) -> ComplianceState:
    """LLM compares new regulation vs existing company policy."""
    gaps = await llm_client.chat(
        system=GAP_ANALYSIS_SYSTEM_PROMPT,
        user=f"""
New Regulation (chunked):
{format_chunks(state['regulation_chunks'])}

Company Current Policy:
{format_chunks(state['company_policy_chunks'])}

Identify gaps. For each gap output JSON:
{{
  "gap": "...",
  "severity": "LOW|MEDIUM|HIGH|CRITICAL",
  "regulation_section": "...",
  "regulation_page": N,
  "required_action": "...",
  "deadline": "..."
}}
        """,
        response_format="json_array",
        trace_id=state["langfuse_trace_id"]
    )
    return {**state, "gaps": gaps}

async def ragas_evaluation_node(state: ComplianceState) -> ComplianceState:
    """Run RAGAS faithfulness on every gap citation."""
    eval_samples = [
        {
            "user_input": gap["gap"],
            "response": gap["required_action"],
            "retrieved_contexts": [
                c["text"] for c in state["regulation_chunks"]
                if c["page"] == gap["regulation_page"]
            ]
        }
        for gap in state["gaps"]
    ]
    dataset = EvaluationDataset.from_list(eval_samples)
    result = evaluate(dataset=dataset, metrics=[Faithfulness()])
    avg_faithfulness = result["faithfulness"]

    # Flag low-confidence gaps for human review
    for gap in state["gaps"]:
        gap["needs_review"] = avg_faithfulness < 0.85

    return {**state, "ragas_score": avg_faithfulness}
```

## 3.10 Slack HITL Integration

```
python# integrations/slack_hitl.py
from slack_bolt.async_app import AsyncApp

app = AsyncApp(token=settings.SLACK_BOT_TOKEN)

async def send_approval_request(
    channel: str,
    job_id: str,
    workflow_run_id: str,
    payload: dict,
    approval_type: str  # 'document' | 'vendor_medium' | 'vendor_high'
):
    await app.client.chat_postMessage(
        channel=channel,
        text=f"⏳ Approval Required: {approval_type}",
        blocks=[
            {
                "type": "section",
                "text": {
                    "type": "mrkdwn",
                    "text": format_approval_summary(payload)
                }
            },
            {
                "type": "actions",
                "block_id": f"approval_{job_id}",
                "elements": [
                    {
                        "type": "button",
                        "text": {"type": "plain_text", "text": "✅ Approve"},
                        "style": "primary",
                        "value": f"{job_id}|{workflow_run_id}|approve"
                    },
                    {
                        "type": "button",
                        "text": {"type": "plain_text", "text": "❌ Reject"},
                        "style": "danger",
                        "value": f"{job_id}|{workflow_run_id}|reject"
                    },
                    {
                        "type": "button",
                        "text": {"type": "plain_text", "text": "✏️ Edit Fields"},
                        "value": f"{job_id}|{workflow_run_id}|edit"
                    }
                ]
            }
        ]
    )

@app.action(re.compile(r"approval_.*"))
async def handle_approval(ack, body, action):
    await ack()
    job_id, run_id, decision = action["value"].split("|")
    reviewer_id = body["user"]["id"]

    # Signal Temporal workflow
    await temporal_client.get_workflow_handle(run_id).signal(
        "approval_signal",
        args=[decision == "approve", reviewer_id]
    )

    # Update Slack message to show decision
    await app.client.chat_update(
        channel=body["channel"]["id"],
        ts=body["message"]["ts"],
        text=f"{'✅ Approved' if decision == 'approve' else '❌ Rejected'} by <@{reviewer_id}>",
        blocks=[]
    )
```

## 3.11 QuickBooks MCP Integration

```
python# integrations/quickbooks_mcp.py
# Model Context Protocol client for QuickBooks

class QuickBooksMCP:
    def __init__(self, client_id: str, client_secret: str, realm_id: str):
        self.client = MCPClient(
            server_url=settings.QB_MCP_SERVER_URL,
            auth=OAuth2Auth(client_id, client_secret)
        )
        self.realm_id = realm_id

    async def create_bill(self, invoice: dict, vendor_qb_id: str) -> str:
        result = await self.client.call_tool(
            "quickbooks_create_bill",
            {
                "realm_id": self.realm_id,
                "vendor_ref": vendor_qb_id,
                "line_items": invoice["line_items"],
                "total_amount": invoice["total_amount"]["value"],
                "due_date": invoice["due_date"]["value"],
                "doc_number": invoice["invoice_number"]["value"]
            }
        )
        return result["bill_id"]

    async def create_vendor(self, vendor: dict) -> str:
        result = await self.client.call_tool(
            "quickbooks_create_vendor",
            {
                "realm_id": self.realm_id,
                "display_name": vendor["name"],
                "tax_identifier": vendor["gst_number"],
                "email": vendor["email"],
                "phone": vendor["phone"]
            }
        )
        return result["vendor_id"]
```

## 3.12 Langfuse Observability Wrapper

```
python# observability/langfuse_client.py
from langfuse import Langfuse
from functools import wraps

langfuse = Langfuse(
    public_key=settings.LANGFUSE_PUBLIC_KEY,
    secret_key=settings.LANGFUSE_SECRET_KEY,
    host=settings.LANGFUSE_HOST  # self-hosted
)

def trace_llm_call(workflow: str, step: str):
    """Decorator: wraps any async LLM call with Langfuse tracing."""
    def decorator(func):
        @wraps(func)
        async def wrapper(*args, **kwargs):
            trace = langfuse.trace(
                name=f"{workflow}:{step}",
                metadata={"tenant_id": kwargs.get("tenant_id")}
            )
            span = trace.span(name=step)
            try:
                result = await func(*args, **kwargs, trace_id=trace.id)
                span.end(
                    output=result,
                    level="DEFAULT",
                    status_message="success"
                )
                # Cost tracking
                langfuse.generation(
                    trace_id=trace.id,
                    model=settings.LLM_MODEL,
                    usage={
                        "input": result.get("prompt_tokens"),
                        "output": result.get("completion_tokens")
                    }
                )
                return result
            except Exception as e:
                span.end(level="ERROR", status_message=str(e))
                raise
        return wrapper
    return decorator
```

------

## 4. Development Plan

## 4.1 Phase Structure Overview

| Phase          | Duration   | Focus                     | Deliverable                          |
| -------------- | ---------- | ------------------------- | ------------------------------------ |
| 0 — Foundation | Week 1–2   | Infra, auth, DB, CI       | Running skeleton + all containers    |
| 1 — Workflow 1 | Week 3–5   | Document Ingestion        | End-to-end invoice → QuickBooks      |
| 2 — Workflow 3 | Week 6–8   | Vendor Onboarding         | Vendor form → ERP + Trust Battery    |
| 3 — Workflow 2 | Week 9–11  | Compliance Pipeline       | Crawl4AI → gap report → Slack        |
| 4 — Platform   | Week 12–13 | Observability + RBAC      | Langfuse + dashboard + tests to 150+ |
| 5 — Hardening  | Week 14    | Load test, security, docs | Production-ready                     |

## 4.2 Phase 0 — Foundation (Week 1–2)

```
textTasks:
  □ docker-compose.yml: FastAPI, PostgreSQL, Redis, Qdrant, Neo4j,
                        Temporal, Langfuse (self-hosted), MinIO
  □ Alembic migration: all tables from Section 3.1
  □ FastAPI skeleton: /health, /api/v1/auth/*, tenant middleware
  □ JWT auth + RBAC (5 roles: Admin, Finance, Compliance, Procurement, Viewer)
  □ MinIO bucket provisioning per tenant
  □ Temporal server + worker scaffolding
  □ GitHub Actions CI: lint (ruff), type-check (mypy), pytest -x
  □ Pre-commit hooks: ruff + mypy + gitleaks (secret scanning)

Stack spin-up commands:
  docker compose up -d postgres redis qdrant neo4j temporal minio langfuse
```

## 4.3 Phase 1 — Workflow 1: Document Ingestion (Week 3–5)

```
textWeek 3:
  □ Docling service: PDF → markdown + tables + metadata
  □ Document classifier (deterministic, regex-based)
  □ Pydantic schemas: InvoiceSchema, GSTNoticeSchema, POSchema, ContractSchema
  □ LLM extraction agent (LangGraph, Steps 2+4)
  □ Confidence scoring per field

Week 4:
  □ Validation gate: completeness check + vendor cross-reference
  □ HITL queue: PostgreSQL table + FastAPI endpoints
  □ Slack HITL: send approval request, handle button actions
  □ Temporal workflow: DocumentIngestionWorkflow with approval signal

Week 5:
  □ QuickBooks MCP: create_bill, create_vendor
  □ PostgreSQL audit trail writer
  □ SSE endpoint for real-time job status updates
  □ React UI: upload page + HITL review queue + confidence field editor
  □ Tests: 40+ tests for Workflow 1
```

## 4.4 Phase 2 — Workflow 3: Vendor Onboarding (Week 6–8)

```
textWeek 6:
  □ Vendor onboarding form (React)
  □ Document checklist generator (category-based rules)
  □ Vendor portal link generation (time-limited JWT token)
  □ Docling extraction for GST cert, PAN, bank details

Week 7:
  □ Risk scoring engine (Section 3.8)
  □ Graphiti + Neo4j integration (≥ 0.28.2 mandatory) [web:13]
  □ Vendor entity ingestion into knowledge graph
  □ Fuzzy duplicate detection (rapidfuzz)
  □ Blacklist registry (PostgreSQL + Graphiti)

Week 8:
  □ Tiered Slack approval (LOW/MEDIUM/HIGH routing)
  □ QuickBooks vendor registration
  □ Trust Battery state machine + decay policies
  □ Welcome email via SMTP (or Resend OSS)
  □ Tests: 35+ tests for Workflow 3
```

## 4.5 Phase 3 — Workflow 2: Compliance Pipeline (Week 9–11)

```
textWeek 9:
  □ Crawl4AI scraper: SEBI + RBI + GST portal crawling [web:7]
  □ Temporal cron workflow: weekly scrape + new-doc detection
  □ Docling chunking: semantic section detection
  □ Qdrant ingestion: chunks + sparse BM25 vectors + metadata

Week 10:
  □ Hybrid search: dense + BM25 on Qdrant
  □ Gap analysis LangGraph agent (Steps 2+4)
  □ RAGAS faithfulness evaluation node [web:16][web:20]
  □ Department mapper + owner assignment from registry

Week 11:
  □ Linear/Jira ticket creation (Linear API OSS-friendly)
  □ Slack notification per department owner
  □ Compliance dashboard (React): gap list + status + deadline
  □ Company policy document upload + re-index flow
  □ Tests: 40+ tests for Workflow 2
```

## 4.6 Phase 4 — Platform Hardening (Week 12–13)

```
textWeek 12:
  □ Langfuse self-hosted: full LLM tracing across all 3 workflows
  □ Cost tracking: tokens per workflow run → PostgreSQL
  □ Prometheus metrics: queue depth, error rate, HITL SLA
  □ Grafana dashboard: 4 panels (throughput, error rate, cost, HITL queue)
  □ Alerting: Slack alert when queue > 50 or error rate > 5%

Week 13:
  □ Multi-tenancy audit: RLS verification + penetration test
  □ RBAC middleware tests
  □ Dead-letter queue handler + manual retry UI
  □ Load test: 50 concurrent document uploads (Locust)
  □ Total test count: 150+ (target)
  □ README + API docs (auto-generated from FastAPI OpenAPI)
```

------

## 5. Master Checklist

## Infrastructure

- `docker-compose.yml` covers all 9 services (FastAPI, PostgreSQL, Redis, Qdrant, Neo4j, Temporal, Langfuse, MinIO, Grafana)
- All secrets in `.env` with `.env.example` committed (never `.env` itself)
- Graphiti version pinned to **≥ 0.28.2** in `pyproject.toml`[app.opencve](https://app.opencve.io/cve/CVE-2026-32247)
- MinIO bucket per tenant with lifecycle policy (90-day raw PDF retention)
- PostgreSQL RLS enabled on all tenant-scoped tables
- Alembic migrations for every schema change (no `CREATE TABLE` in app code)
- Redis Streams: 3 queues + DLQ + TTL on processed messages
- Temporal namespace per environment (dev / staging / prod)
- Health check endpoints: `/health/live` and `/health/ready`

## Security

- JWT tokens: 15-min access + 7-day refresh
- RBAC middleware tested for all 5 roles × all endpoints
- Graphiti node_labels never accept raw user input (CVE-2026-32247)[tenable](https://www.tenable.com/cve/CVE-2026-32247)
- Vendor portal links: time-limited JWT (24-hour expiry)
- gitleaks in pre-commit (no secrets in git history)
- File upload validation: PDF-only, max 25MB, MIME type check
- Rate limiting: 100 req/min per tenant via FastAPI middleware
- Audit trail is append-only (no UPDATE/DELETE on `audit_events`)

## Workflow 1 — Document Ingestion

- Docling extracts tables correctly from sample invoices (test with 20 real PDFs)
- All 4 doc types classifiable from first-page text
- Confidence scores populated for every extracted field
- HITL routing fires for: confidence < 0.80, amount > ₹1,00,000, new vendor
- Slack approval buttons signal Temporal correctly
- QuickBooks bill created with correct line items and GST breakdown
- Audit event written at every step transition
- SSE status updates reach UI within 2 seconds of state change

## Workflow 2 — Compliance Monitoring

- Crawl4AI respects `robots.txt` on all 3 regulatory portals[crawl4](https://www.crawl4.com/blog/crawl4ai-security-ethics-and-legal-web-scraping)
- Temporal cron runs weekly without drift
- Qdrant hybrid search returns results (dense + BM25 both active)
- RAGAS faithfulness score ≥ 0.85 on test regulation set[blog.langchain](https://blog.langchain.com/evaluating-rag-pipelines-with-ragas-langsmith/)
- Every gap has page + paragraph citation
- Low-faithfulness gaps (< 0.85) flagged for human review
- Linear ticket created with full compliance brief attached
- Department owner receives Slack notification within 60 seconds

## Workflow 3 — Vendor Onboarding

- GST, PAN, IFSC regex validation covers all valid formats
- Fuzzy match detects vendors with >85% name similarity
- Blacklist check runs against both GST and PAN
- Graphiti entity graph query finds related risky entitiesmintlify+1
- LOW risk → auto-approve with notification (no human required)
- MEDIUM → single Finance approver via Slack
- HIGH → dual approval chain (Finance must approve before Legal prompt)
- Trust Battery initializes to PROBATION on registration
- Trust Battery decay: 90-day inactivity drops one tier (Temporal cron)
- Welcome email sent only after final ERP registration

## Observability

- Every LLM call has Langfuse trace with `tenant_id` + `workflow` metadata
- Token usage logged per run → cost dashboard populated
- Grafana alert fires when queue depth > 50
- Grafana alert fires when error rate > 5% (5-min rolling window)
- RAGAS scores stored in `compliance_gaps.ragas_score` column
- Temporal UI accessible on port 8233 (internal only)
- Dead-letter queue items visible in admin UI with retry button

------

## 6. Test Suite (150+ Tests)

## 6.1 Test Categories

| Category                                       | Count   | Tool                        |
| ---------------------------------------------- | ------- | --------------------------- |
| Unit — extraction schemas                      | 20      | pytest                      |
| Unit — risk scorer                             | 15      | pytest                      |
| Unit — classifier                              | 10      | pytest                      |
| Unit — Trust Battery state machine             | 10      | pytest                      |
| Integration — Docling extraction               | 15      | pytest + real PDFs          |
| Integration — Qdrant hybrid search             | 10      | pytest                      |
| Integration — Temporal workflows (test worker) | 15      | pytest + temporalio testing |
| Integration — Slack HITL                       | 10      | pytest + Slack mock         |
| Integration — QuickBooks MCP                   | 10      | pytest + VCR cassettes      |
| E2E — full workflow runs                       | 15      | pytest + docker-compose     |
| RAGAS eval suite                               | 10      | ragas evaluate              |
| Security / RBAC                                | 10      | pytest                      |
| Load tests                                     | 5       | Locust                      |
| **Total**                                      | **155** |                             |

## 6.2 Sample Test Code

```
python# tests/unit/test_risk_scorer.py
import pytest
from services.risk_scorer import compute_vendor_risk, RiskResult

@pytest.fixture
def clean_vendor():
    return {
        "name": "Acme Supplies Pvt Ltd",
        "gst_number": "27AAPCA1234A1Z5",
        "pan_number": "AAPCA1234A",
        "ifsc_code": "HDFC0001234",
        "bank_account": "1234567890",
        "email": "accounts@acme.in"
    }

@pytest.mark.asyncio
async def test_clean_vendor_scores_low(clean_vendor, mock_graphiti):
    result = await compute_vendor_risk(
        clean_vendor, existing_vendors=[], blacklist=[], tenant_id="t1"
    )
    assert result.tier == "LOW"
    assert result.score < 30
    assert result.flags == []

@pytest.mark.asyncio
async def test_invalid_gst_increases_score(clean_vendor, mock_graphiti):
    clean_vendor["gst_number"] = "INVALID123"
    result = await compute_vendor_risk(
        clean_vendor, existing_vendors=[], blacklist=[], tenant_id="t1"
    )
    assert "INVALID_GST_FORMAT" in result.flags
    assert result.score >= 25

@pytest.mark.asyncio
async def test_blacklisted_vendor_scores_high(clean_vendor, mock_graphiti):
    result = await compute_vendor_risk(
        clean_vendor,
        existing_vendors=[],
        blacklist=["27AAPCA1234A1Z5"],
        tenant_id="t1"
    )
    assert result.tier == "HIGH"
    assert "BLACKLISTED_ENTITY" in result.flags

@pytest.mark.asyncio
async def test_duplicate_bank_account_flagged(clean_vendor, mock_graphiti):
    existing = [{**clean_vendor, "id": "other-id", "bank_account": "1234567890"}]
    result = await compute_vendor_risk(
        clean_vendor, existing_vendors=existing, blacklist=[], tenant_id="t1"
    )
    assert "DUPLICATE_BANK_ACCOUNT" in result.flags

@pytest.mark.asyncio
async def test_fuzzy_name_match_detected(clean_vendor, mock_graphiti):
    existing = [{"id": "x", "name": "Acme Supply Pvt Ltd", "bank_account": "9999"}]
    result = await compute_vendor_risk(
        clean_vendor, existing_vendors=existing, blacklist=[], tenant_id="t1"
    )
    assert any("SIMILAR_VENDOR_NAME" in f for f in result.flags)
python# tests/unit/test_trust_battery.py
import pytest
from services.trust_battery import TrustBattery, TrustTier

def test_initial_tier_is_probation():
    tb = TrustBattery()
    assert tb.tier == TrustTier.PROBATION

def test_upgrade_path_requires_clean_days():
    tb = TrustBattery()
    tb.record_success(count=3)
    tb.advance_days(30)
    assert tb.tier == TrustTier.STANDARD

def test_fraud_resets_to_probation():
    tb = TrustBattery(tier=TrustTier.CORE)
    tb.flag_fraud()
    assert tb.tier == TrustTier.PROBATION

def test_three_consecutive_errors_drops_tier():
    tb = TrustBattery(tier=TrustTier.STANDARD)
    tb.record_error(); tb.record_error(); tb.record_error()
    assert tb.tier == TrustTier.PROBATION

def test_90_day_inactivity_drops_tier():
    tb = TrustBattery(tier=TrustTier.CORE)
    tb.advance_days(91)
    assert tb.tier == TrustTier.STANDARD
python# tests/integration/test_docling_extraction.py
import pytest
from pathlib import Path
from services.docling_extractor import DoclingExtractor

extractor = DoclingExtractor()
FIXTURES = Path("tests/fixtures/pdfs")

@pytest.mark.asyncio
async def test_invoice_extraction_completeness():
    result = await extractor.extract(str(FIXTURES / "sample_invoice_gst.pdf"))
    assert result["metadata"]["page_count"] >= 1
    assert len(result["text_blocks"]) > 0
    # Must contain GST-relevant keywords
    full_text = " ".join(result["text_blocks"]).lower()
    assert any(k in full_text for k in ["gstin", "cgst", "sgst", "invoice"])

@pytest.mark.asyncio
async def test_table_extraction_from_invoice():
    result = await extractor.extract(str(FIXTURES / "invoice_with_line_items.pdf"))
    assert len(result["tables"]) >= 1
    table = result["tables"][0]
    assert len(table) > 0  # at least one row

@pytest.mark.asyncio
async def test_scanned_pdf_ocr_fallback():
    """Scanned PDFs must go through RapidOCR fallback."""
    result = await extractor.extract(str(FIXTURES / "scanned_invoice.pdf"))
    assert len(result["text_blocks"]) > 5
python# tests/ragas_eval/test_compliance_faithfulness.py
import pytest
from ragas import evaluate, EvaluationDataset
from ragas.metrics import Faithfulness

COMPLIANCE_TEST_CASES = [
    {
        "user_input": "What does the new SEBI circular require for AIF disclosures?",
        "response": "AIFs must disclose fee structures to all unit holders quarterly per Section 4.2.",
        "retrieved_contexts": [
            "Section 4.2: Alternative Investment Funds shall disclose all fee "
            "structures, including management fees and performance fees, to unit "
            "holders on a quarterly basis via electronic means."
        ]
    },
    # Add 9 more cases covering RBI, GST scenarios
]

def test_compliance_faithfulness_above_threshold():
    dataset = EvaluationDataset.from_list(COMPLIANCE_TEST_CASES)
    result = evaluate(dataset=dataset, metrics=[Faithfulness()])
    assert result["faithfulness"] >= 0.85, (
        f"Faithfulness {result['faithfulness']:.2f} below required 0.85 threshold"
    )
python# tests/security/test_rbac.py
import pytest
from httpx import AsyncClient
from main import app

@pytest.mark.asyncio
async def test_viewer_cannot_approve_hitl():
    async with AsyncClient(app=app) as client:
        token = await get_token(role="viewer")
        resp = await client.post(
            "/api/v1/hitl/queue/some-job-id/approve",
            headers={"Authorization": f"Bearer {token}"}
        )
        assert resp.status_code == 403

@pytest.mark.asyncio
async def test_finance_can_approve_document_hitl():
    async with AsyncClient(app=app) as client:
        token = await get_token(role="finance")
        # pre-seed a HITL queue item
        job_id = await seed_hitl_item(queue_type="low_confidence")
        resp = await client.post(
            f"/api/v1/hitl/queue/{job_id}/approve",
            headers={"Authorization": f"Bearer {token}"},
            json={"approved": True, "notes": "Verified manually"}
        )
        assert resp.status_code == 200

@pytest.mark.asyncio
async def test_cross_tenant_data_isolation():
    """Tenant A must never see Tenant B's documents."""
    async with AsyncClient(app=app) as client:
        token_a = await get_token(role="finance", tenant="tenant_a")
        tenant_b_doc_id = await seed_document(tenant="tenant_b")
        resp = await client.get(
            f"/api/v1/documents/{tenant_b_doc_id}",
            headers={"Authorization": f"Bearer {token_a}"}
        )
        assert resp.status_code == 404  # Not 403 — don't leak existence
```

------

## 7. Docker Compose (Full OSS Stack)

```
text# docker-compose.yml
version: "3.9"
services:
  api:
    build: .
    ports: ["8000:8000"]
    depends_on: [postgres, redis, qdrant, neo4j, temporal, minio]
    env_file: .env

  postgres:
    image: pgvector/pgvector:pg16
    environment:
      POSTGRES_DB: opscore
      POSTGRES_USER: ${POSTGRES_USER}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
    volumes: [pgdata:/var/lib/postgresql/data]

  redis:
    image: redis:7-alpine
    command: redis-server --appendonly yes
    volumes: [redisdata:/data]

  qdrant:
    image: qdrant/qdrant:latest
    ports: ["6333:6333"]
    volumes: [qdrantdata:/qdrant/storage]

  neo4j:
    image: neo4j:5.20-community
    environment:
      NEO4J_AUTH: neo4j/${NEO4J_PASSWORD}
      NEO4J_PLUGINS: '["apoc"]'
    ports: ["7474:7474", "7687:7687"]
    volumes: [neo4jdata:/data]

  temporal:
    image: temporalio/auto-setup:1.24
    depends_on: [postgres]
    environment:
      DB: postgresql
      DB_PORT: 5432
      POSTGRES_USER: ${POSTGRES_USER}
      POSTGRES_PWD: ${POSTGRES_PASSWORD}
      POSTGRES_SEEDS: postgres
    ports: ["7233:7233"]

  temporal-ui:
    image: temporalio/ui:latest
    environment:
      TEMPORAL_ADDRESS: temporal:7233
    ports: ["8233:8233"]

  minio:
    image: minio/minio:latest
    command: server /data --console-address ":9001"
    environment:
      MINIO_ROOT_USER: ${MINIO_USER}
      MINIO_ROOT_PASSWORD: ${MINIO_PASSWORD}
    ports: ["9000:9000", "9001:9001"]
    volumes: [miniodata:/data]

  langfuse:
    image: langfuse/langfuse:latest
    depends_on: [postgres]
    environment:
      DATABASE_URL: postgresql://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres/langfuse
      NEXTAUTH_SECRET: ${LANGFUSE_SECRET}
    ports: ["3000:3000"]

  grafana:
    image: grafana/grafana:latest
    ports: ["3001:3000"]
    volumes: [grafanadata:/var/lib/grafana]

  prometheus:
    image: prom/prometheus:latest
    volumes: [./prometheus.yml:/etc/prometheus/prometheus.yml]
    ports: ["9090:9090"]

volumes:
  pgdata: qdrantdata: neo4jdata: redisdata: miniodata:
  grafanadata:
```

------

## 8. Key Architecture Decisions Log

| Decision | Choice | Rationale |
| -------- | ------ | --------- |
|          |        |           |

| Decision             | Choice                       | Rationale                                                    |
| -------------------- | ---------------------------- | ------------------------------------------------------------ |
| OCR engine           | Docling (IBM OSS)            | Zero API cost, table-aware, RapidOCR fallback for scanned PDFs github+1 |
| Compliance scraping  | Crawl4AI                     | Async, LLM-aware, robots.txt compliant, domain allowlists [crawl4](https://www.crawl4.com/blog/crawl4ai-security-ethics-and-legal-web-scraping) |
| Vendor entity memory | Graphiti ≥ 0.28.2 + Neo4j    | Real-time entity graph, hybrid search, temporal context ainews+1 |
| Search               | Qdrant hybrid (dense + BM25) | No managed service, both sparse and dense in one store       |
| Queue                | Redis Streams + Celery       | Celery-native, dead-letter, cheaper than any managed queue   |
| Object storage       | MinIO                        | S3-compatible, bucket-per-tenant, self-hosted                |
| LLM eval             | RAGAS Faithfulness           | Citation accuracy enforcement; threshold ≥ 0.85 blog.langchain+1 |
| Workflow engine      | Temporal                     | Durable execution, crash recovery, cron support              |
| Agent framework      | LangGraph (W1+W2 only)       | Predefined state machine, not for W3 which is pure function chains |