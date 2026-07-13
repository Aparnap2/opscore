# OpsCore — Product Requirements Document v5.0

**Version:** 5.0 — Current  
**Date:** 2026-07-13  
**Status:** Active — Development  
**Repository:** github.com/aparna/opscore · branch: main  
**Live URL:** *(coming soon)*

---

## Architecture Decisions

OpsCore's design is governed by 18 Architecture Decision Records (ADRs) in `docs/architecture/adr/`. The following statuses are current as of v5.0:

| ADR | Title | Status |
|-----|-------|--------|
| 0002 | Vendor Onboarding — Asynchronous Real-Time Workflow | Accepted |
| 0003 | Multi-Tenancy and Tenant Isolation | Accepted |
| 0005 | Audit Trail and Usage Metering | Proposed |
| 0006 | Deterministic-First AI Policy | Proposed |
| 0007 | Structured Ingestion and Semantic Chunking | Proposed |
| 0008 | Retrieval Router — Deterministic, Vectorless, Hybrid | Proposed |
| 0009 | Bounded Agent Loops and Fallback Policy | Proposed |
| 0010 | Human-in-the-Loop Approval Boundary | Proposed |
| 0011 | Queue Semantics, Retries, Idempotency, and DLQ | Accepted |
| 0012 | Observability and LLMOps | Accepted |
| 0013 | Live-Provider Testing Strategy | Proposed |
| 0014 | Agentic AI Verification Strategy | Proposed |
| 0015 | Deployment Target and Cloud-Agnostic Runtime | Accepted |
| 0016 | Search/Indexing Backend Choice | Proposed |
| 0017 | Billing Readiness and Plan Limits | Proposed |
| 0018 | Compliance-Source Ingestion Strategy | Proposed |
| 0019 | Data Retention, PII, and Document Security | Proposed |
| 0020 | Review Queue and Operator UX Boundary | Accepted |

---

## One Paragraph Description

OpsCore is an autonomous back-office system for Indian B2B businesses. It ingests vendor documents (PDF invoices, onboarding forms, compliance notices) via REST API, validates Indian tax identifiers (GST, PAN, IFSC) using compiled Go regex, scores vendor risk using a deterministic formula, tracks vendor trust state through a five-state machine, and delivers a structured Slack approval request to the business owner. The owner clicks one button. All classification, validation, scoring, and audit-trail logic is compiled Go code — no LLM. Sarvam AI is invoked only when OCR confidence falls below threshold or when compliance gap reasoning requires natural language. The system runs on PostgreSQL 16 with Redis-backed queues and MinIO object storage, deployed via Docker Compose for development and Google Cloud Run for production — zero Azure dependencies.

---

## The Problem (Grounded in The E-Myth)

Every Indian B2B business owner started as a craftsperson: a trader, an agency founder, a service expert. They built their business around what they knew how to do. At five employees, the back-office collapses. Invoice processing, vendor ID verification, GST compliance, duplicate detection — these are not skilled tasks, but they consume skilled people. The owner does them because no one else can be trusted to do them consistently. The business does not scale because the system is the owner. As Gerber describes: *the technician who starts a business works in it, not on it.* OpsCore replaces the manual back-office not with more people, but with a system that produces identical results every time, with no human in the loop except for the final approval decision.

---

## Product Philosophy

**Default to compiled logic. Use LLM as a last resort.**

Every decision that can be encoded as a deterministic rule must be. This is not a cost optimisation — it is a reliability guarantee. A regex pattern that validates a GST number never hallucinates. A state machine that transitions a vendor from PROBATION to STANDARD never drifts. A risk formula that subtracts 40 points for a duplicate GST number never changes its mind between runs.

The LLM (Sarvam-M) is invoked only for two things:
1. Field extraction from documents where OCR confidence < 0.85 and the fields are ambiguous
2. Compliance gap reasoning, where natural language understanding of a regulatory circular is unavoidably required

Everything else is Go.

---

## System Architecture

### Runtime Stack

| Layer | Technology | Reason |
|-------|------------|--------|
| Language | Go 1.25 | Single compiled binary, no runtime, deterministic behaviour |
| API | REST (net/http, no framework) | Zero framework dependency, full control over routing and middleware |
| Architecture | Hexagonal (Ports & Adapters) | Domain has zero I/O; all external systems behind interfaces |
| Database | PostgreSQL 16 | Mature, proven, rich full-text search, RLS for tenant isolation |
| Storage | MinIO / S3-compatible | S3 API, cloud-agnostic, production-ready |
| Queue | Redis 7 | Pub/Sub, list-based queues with atomic lease semantics via BRPopLPush |
| Auth | API-key based with RBAC | 5 roles: Owner, OpsAdmin, Reviewer, Auditor, Viewer |
| Tenant isolation | tenant_id on every table + RLS middleware | Three-layer enforcement: API, application, database |
| OCR | Sarvam Document Intelligence | India-native, job-based, structured output |
| LLM | Sarvam-M (json_schema response format) | India-specific, used only as fallback when OCR confidence < 0.85 |
| OCR Fallback | OpenRouter (tencent/hy3:free) | Fires only when confidence < threshold |
| HITL | Slack Block Kit + Admin API | Approval in existing workflow; Admin API is canonical source of truth |
| LLM Obs | Langfuse Cloud | Traces, latency, token count, cost tracking with no-op fallback |
| Admin UI | Python/Streamlit (REST-only, no direct DB access) | Consumes Go API exclusively via HTTP; zero database credentials |
| Middleware | Auth, Tenant, Rate Limit, Usage Enforcement | Middleware chain enforces auth, tenant context, rate limits, plan limits |
| Logging | log/slog with JSON handler | Structured key-value pairs, machine-parseable, no bare Println |
| Metrics | In-process endpoints | `/metrics/llm-summary`, `/metrics/workflow-summary` — no Prometheus dependency |
| Deployment | Docker Compose → Google Cloud Run / GCP | Cloud-agnostic; documented path to Cloud Run, Cloud SQL, Memorystore |
| CI/CD | GitHub Actions | lint + test-unit + test-integration + build + deploy |

### Infrastructure Cost

Local development runs at zero marginal cost (Docker containers on developer machine). Production targets managed PostgreSQL (Cloud SQL), managed Redis (Memorystore/Upstash), and S3-compatible storage (GCS) — costs scale with usage.

### Local Development Stack (Three Containers)

| Service | Image | Purpose |
|---------|-------|---------|
| postgres | postgres:16-alpine | Primary database on :5432 |
| minio | minio/minio:latest | S3-compatible storage on :9000/:9001 |
| redis | redis:7-alpine | Queue backend on :6379 |

These run via `docker compose -f docker-compose.local.yml up -d`. The Go API and Streamlit UI run natively on the host.

---

## Repository Layout

```
opscore/
├── cmd/
│   ├── server/main.go          # HTTP server entry point + route registration
│   ├── server/worker.go        # Queue consumer (Redis Pub/Sub, BRPopLPush)
│   └── migrate/                # Database migration tool
│
├── internal/
│   ├── domain/                 # Pure Go, zero I/O — source of truth for all business rules
│   │   ├── trust_battery.go    # State machine: PROBATION→STANDARD→PREFERRED→STRATEGIC→BLOCKED
│   │   ├── risk_scorer.go      # Deterministic risk score 0-100
│   │   ├── document_classifier.go # Keyword classification: INVOICE/CONTRACT/GST_NOTICE/PO/OTHER
│   │   ├── india_validator.go  # GST / PAN / IFSC regex validation
│   │   ├── chunker.go          # Text chunking for compliance analysis
│   │   ├── upload.go           # Upload validation & storage logic
│   │   ├── batch_job.go        # Batch processing support
│   │   ├── auth.go             # RBAC roles, permissions, User type
│   │   ├── tenant.go           # Tenant model and constructor
│   │   ├── usage.go            # Plans, metrics, limit checking
│   │   ├── audit.go            # Audit event definitions
│   │   ├── job_status.go       # Job status state machine
│   │   └── types.go            # All shared domain types with json tags
│   │
│   ├── agents/                 # Workflow orchestrators
│   │   ├── document_agent.go   # Document ingestion workflow
│   │   ├── vendor_agent.go     # Vendor onboarding workflow
│   │   └── compliance_agent.go # Compliance monitoring workflow
│   │
│   ├── providers/              # Interface definitions only
│   │   ├── interfaces.go       # OCR, LLM, Storage, Queue, DB, HITL, Tracing, Auth, Tenant, Usage
│   │   └── slack_hitl.go       # Slack HITL types
│   │
│   ├── adapters/               # Interface implementations
│   │   ├── postgres/           # PostgreSQL adapter (jobs, vendors, audit, usage, RLS)
│   │   ├── minio/              # MinIO/S3 storage adapter
│   │   ├── queue/              # Redis Pub/Sub + list-based queue adapter
│   │   ├── sarvam/             # Sarvam OCR + LLM adapter
│   │   ├── openrouter/         # OpenRouter LLM adapter (fallback provider)
│   │   ├── fallback/           # Provider fallback chain adapter
│   │   └── slack/              # Slack API adapter (Block Kit)
│   │
│   ├── telemetry/              # Observability
│   │   ├── langfuse.go         # Langfuse Cloud tracing
│   │   ├── metrics.go          # LLM & workflow metrics endpoints
│   │   └── noop.go             # No-op tracer fallback
│   │
│   ├── logging/                # Structured JSON logging
│   │   └── logger.go           # log/slog JSON handler configuration
│   │
│   └── middleware/             # HTTP middleware
│       ├── auth/auth.go        # API-key auth & RBAC enforcement
│       ├── tenant/tenant.go    # Tenant context extraction + RLS setup
│       ├── ratelimit/          # Rate limiter
│       └── usage/              # Plan limit enforcement
│
├── ops-ui/                     # Streamlit admin dashboard
│   ├── app.py                  # Dashboard entry point (REST-only)
│   ├── config.py               # UI configuration
│   └── requirements.txt        # Python dependencies
│
├── tests/
│   ├── unit/                   # Pure unit tests (no infra)
│   ├── domain/                 # Domain-layer tests
│   ├── integration/            # Integration tests (requires Docker)
│   ├── agentic/                # Agent workflow tests (requires Docker)
│   ├── e2e/                    # End-to-end tests (full stack in Docker)
│   ├── golden/                 # Golden file / snapshot tests
│   ├── security/               # Security-focused tests
│   ├── live/                   # Opt-in live provider tests (//go:build live)
│   │   ├── llm/                # LLM provider contract tests
│   │   ├── ocr/                # OCR provider contract tests
│   │   ├── slack/              # Slack API contract tests
│   │   └── compliance_feeds/   # SEBI/RBI RSS feed tests
│   ├── load/                   # k6 load test scripts
│   ├── ragas_eval/             # LLM output evaluation (RAGAS)
│   ├── mockoon/                # Slack API mock configurations
│   └── fixtures/               # Test PDFs and expected outputs
│
├── config/                     # Application configuration
├── scripts/                    # Utility scripts
├── mocks/                      # Generated test mocks
├── libs/                       # Shared libraries
│
├── docs/architecture/adr/      # 18 Architecture Decision Records
│
├── infra/                      # Infrastructure as Code
│   └── main.bicep              # Legacy Azure Bicep (migration reference, not in use)
│
├── docker-compose.yml          # Full local stack (app + infra)
├── docker-compose.local.yml    # Lightweight infra only (Postgres + MinIO + Redis)
├── docker-compose.e2e.yml      # E2E test stack (full + stubs)
├── Dockerfile                  # Multi-stage Go production build
├── Makefile                    # Dev workflows (local-up, run-api, test-*, etc.)
└── .github/workflows/          # CI/CD pipelines
```

---

## Endpoint Map

| Method | Endpoint | Description | Auth Required | Tenant Required |
|--------|----------|-------------|---------------|-----------------|
| GET | `/health` | Health check with per-service status | No | No |
| GET | `/status/summary` | Job counts by status | Yes | Yes |
| POST | `/upload` | Upload PDF/IMG (multipart/form-data) | Yes | Yes |
| GET | `/jobs/recent` | Recent jobs (query: `?limit=N`) | Yes | Yes |
| GET | `/jobs/{id}` | Job detail | Yes | Yes |
| GET | `/jobs/{id}/audit` | Audit trail for a job | Yes | Yes |
| POST | `/vendors` | Create vendor | Yes | Yes |
| GET | `/vendors` | List all vendors | Yes | Yes |
| GET | `/vendors/{id}` | Vendor detail | Yes | Yes |
| GET | `/vendors/risky` | High-risk vendors | Yes | Yes |
| GET | `/compliance/recent` | Recent compliance records | Yes | Yes |
| POST | `/compliance/run` | Trigger compliance check | Yes | Yes |
| GET | `/admin/review-queue` | List pending HITL requests | Yes (OpsAdmin+) | Yes |
| POST | `/admin/review-queue` | Approve/reject a HITL request | Yes (OpsAdmin+) | Yes |
| GET | `/admin/usage-summary` | Per-tenant monthly usage | Yes (Owner/Auditor) | No (cross-tenant) |
| GET | `/metrics/llm-summary` | LLM call metrics (count, latency, cost) | Yes | No |
| GET | `/metrics/workflow-summary` | Workflow processing metrics | Yes | No |
| POST | `/slack/webhook` | Slack interactive callbacks | Slack signing secret | Internal |

---

## Workflow 1: Document Ingestion

### Entry
```
POST /api/upload
Headers: X-Tenant-ID: string (required)
         Authorization: Bearer <token> or X-API-Key: <key>
Body: multipart/form-data, field "file" (PDF ≤ 10MB, or IMG)
```

### Processing Pipeline
```
1. Auth middleware: validate API key, resolve user + tenant
2. Tenant middleware: resolve tenant, SET app.tenant_id for RLS
3. Validate: file type must be PDF or IMG, size ≤ 10MB
4. Usage check: CheckLimit(tenant_id, "documents_uploaded")
   → 403 Plan Limit Exceeded if at limit
5. Write file to MinIO at tenants/{tenant_id}/docs/{uuid}.{ext}
6. Compute SHA256 hash of file content for dedup
7. Create job record in PostgreSQL:
   {id, tenant_id, status: "queued", blob_url, content_hash, created_at, trace_id}
8. Enqueue message to document-queue (Redis LPUSH):
   {job_id, tenant_id, blob_url, trace_id}
9. Return HTTP 200: {job_id, status: "queued"}

[Worker polls document-queue via BRPopLPush]
10. Download file from MinIO
11. Submit to Sarvam Document Intelligence for OCR
12. Poll Sarvam job until status = Completed or PartiallyCompleted or Failed
    - If Failed or confidence < 0.85: invoke LLM fallback (Sarvam-M) for field extraction
    - If all providers fail: route to HITL (mandatory tier)
13. Classify document type using keyword matching (no LLM):
    INVOICE / CONTRACT / GST_NOTICE / PO / OTHER
14. Validate Indian identifiers using regex:
    GST: ^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z]{1}[1-9A-Z]{1}Z[0-9A-Z]{1}$
    PAN: ^[A-Z]{5}[0-9]{4}[A-Z]{1}$
    IFSC: ^[A-Z]{4}0[A-Z0-9]{6}$
15. Check for duplicate GST in PostgreSQL
16. Evaluate HITL conditions:
    - Invoice amount > ₹1,00,000
    - OCR confidence < 0.85
    - GST/PAN mismatch vs vendor record
    - Document type is CONTRACT or GST_NOTICE (always)
    - Duplicate content (SHA256 match)
17. If HITL required:
    a. Create hitl_request record in PostgreSQL
    b. Determine tier (mandatory / conditional / bypassed)
    c. Send Slack Block Kit with Approve / Flag / Edit buttons
    d. Update job status: "pending_hitl"
    e. Wait for POST /api/slack/webhook callback or Admin API decision
    f. SLA timer starts; on expiry: auto-approve (conditional) or auto-reject (mandatory)
18. On approval:
    a. Write extracted_data to PostgreSQL
    b. Update job status: "completed"
    c. Increment usage counters (documents_uploaded++, ocr_pages+=N)
    d. Append audit_event: {actor, action, old_state, new_state, timestamp}
    e. Ack queue message (Redis Delete)
```

### HITL Slack Message Format
```
[OpsCore] Invoice from Acme Trading Co.
Amount: ₹1,23,456 | GST: 27AAPFU0939F1ZV ✓ | PAN: AAPFU0939F ✓
Status: PROBATION tier (1 prior transaction)
Confidence: 91%

[Approve to Ledger]  [Flag for Audit]  [Edit Fields]
```

---

## Workflow 2: Vendor Onboarding

### Entry
```
POST /api/vendors
Headers: X-Tenant-ID: string (required)
         Authorization: Bearer <token> or X-API-Key: <key>
Body: JSON {name, gst, pan, ifsc, bank_name, contact_email, documents: [file_refs]}
```

### Processing Pipeline
```
1. Auth + tenant middleware: validate, resolve, set tenant context
2. Usage check: CheckLimit(tenant_id, "documents_uploaded")
3. Write vendor draft + job record to PostgreSQL
4. Enqueue to vendor-queue (Redis LPUSH)

[Worker polls vendor-queue via BRPopLPush]
5. ValidateGST(gst) → bool, error_detail
6. ValidatePAN(pan) → bool, error_detail
7. ValidateIFSC(ifsc) → bool, error_detail
8. Duplicate check: query PostgreSQL for existing vendor with same GST in tenant
9. Compute risk score (deterministic formula, no LLM):
   Base: 50
   +15 if GST valid
   +10 if PAN valid
   +10 if IFSC valid
   -40 if duplicate GST found
   -20 if active dispute flag exists
   -15 if incomplete documents
10. Set initial trust tier: PROBATION
11. Evaluate HITL tier:
    - Risk score >= 80: mandatory
    - Risk score 60-79: conditional
    - Risk score < 60, all IDs valid: bypassed
    - Duplicate GST: mandatory
12. If bypassed: complete vendor creation, emit audit_event
13. If conditional: start SLA timer (2h default), notify Slack
    - On SLA expiry: auto-approve
14. If mandatory: block until operator response
    - On SLA expiry, auto-reject, route to DLQ
15. On approval: write vendor record, increment usage, emit audit_event
16. On rejection: mark job as failed, emit audit_event with reason
```

### Trust Battery State Machine
```
States: PROBATION → STANDARD → PREFERRED → STRATEGIC
Blocked state: BLOCKED (terminal)

Transitions:
  AllChecksPass:          PROBATION  → STANDARD
  ThreeTxns:              STANDARD   → PREFERRED
  TenTxns:                PREFERRED  → STRATEGIC
  DisputeFiled:           PREFERRED  → STANDARD
  DisputeFiled:           STRATEGIC  → PREFERRED
  InactivityDecay 180d:   STRATEGIC  → PREFERRED
  InactivityDecay 180d:   PREFERRED  → STANDARD
  FraudFlagged:           any        → BLOCKED

Score ranges:
  PROBATION:  0-30
  STANDARD:   31-60
  PREFERRED:  61-85
  STRATEGIC:  86-100
```

Timer runs daily at 2am IST. It queries all vendors with `last_transaction_at` older than 180 days and applies InactivityDecay transitions. Every transition emits an audit_event.

---

## Workflow 3: Compliance Monitoring

### Entry
Timer fires Monday 9am IST (configurable cron per source).

### Processing Pipeline
```
1. Fetch SEBI / RBI / GST portal RSS feeds using Go net/http
2. For each item: compute SHA256 hash of content URL
3. Compare against last-seen hashes in PostgreSQL
4. New items only: enqueue to compliance-queue

[Worker polls compliance-queue]
5. Download full document (PDF or HTML)
6. Write raw file to MinIO
7. Extract text:
   - PDF: Sarvam Document Intelligence
   - HTML: Go html.NewTokenizer (no OCR)
8. Chunk text into ~500 token segments (structure-aware chunking per ADR-0007)
9. Write chunks to PostgreSQL compliance_chunks table with heading_path metadata
10. Invoke Sarvam-M with tenant policy context:
    "Given this regulatory text and these policy rules, identify gaps."
    Response format: json_schema {gaps: [{clause, severity, description, citation}]}
11. Classify severity per gap using keyword rules:
    CRITICAL: ["penalty", "prosecution", "suo moto", "cancellation"]
    HIGH:     ["non-compliance", "show cause", "demand notice"]
    MEDIUM:   ["advisory", "clarification", "amendment"]
    LOW:      ["guidance", "circular", "update"]
12. Write gap report to PostgreSQL
13. If any CRITICAL or HIGH gap: send Slack notification
14. Append audit_event with compliance check summary
15. Ack queue message
```

---

## Data Model (PostgreSQL 16, tenant_id on every table + RLS)

### tenants
```
id              TEXT PRIMARY KEY
name            TEXT NOT NULL
slug            TEXT UNIQUE NOT NULL
plan            TEXT NOT NULL DEFAULT 'starter'   (starter | pro | business)
status          TEXT NOT NULL DEFAULT 'active'    (active | suspended)
config          JSONB DEFAULT '{}'
created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
```

### users (auth)
```
id              TEXT PRIMARY KEY
tenant_id       TEXT NOT NULL REFERENCES tenants(id)
email           TEXT NOT NULL
role            TEXT NOT NULL                    (owner | ops_admin | reviewer | auditor | viewer)
name            TEXT NOT NULL
api_key_hash    TEXT NOT NULL
created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
RLS: tenant_id = app.current_tenant_id()
```

### jobs
```
id              TEXT PRIMARY KEY
tenant_id       TEXT NOT NULL
workflow_type   TEXT NOT NULL                    (document_ingestion | vendor_onboarding | compliance)
status          TEXT NOT NULL                    (queued | processing | pending_hitl | completed | failed | retryable_failed | terminal_failed | dead_lettered)
retry_count     INT NOT NULL DEFAULT 0
max_retries     INT NOT NULL DEFAULT 3
blob_url        TEXT
document_type   TEXT                             (INVOICE | CONTRACT | GST_NOTICE | PO | OTHER)
confidence      FLOAT
extracted_data  JSONB
risk_flags      TEXT[]
hitl_reason     TEXT
version         INT NOT NULL DEFAULT 1
trace_id        TEXT
error           TEXT
created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
RLS: tenant_id = app.current_tenant_id()
```

### vendors
```
id                   TEXT PRIMARY KEY
tenant_id            TEXT NOT NULL
name                 TEXT NOT NULL
gst                  TEXT
pan                  TEXT
ifsc                 TEXT
trust_tier           TEXT NOT NULL DEFAULT 'PROBATION'   (PROBATION | STANDARD | PREFERRED | STRATEGIC | BLOCKED)
risk_score           INT NOT NULL DEFAULT 50
risk_flags           TEXT[]
status               TEXT NOT NULL DEFAULT 'pending'     (pending | active | blocked | rejected)
last_transaction_at  TIMESTAMPTZ
created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
RLS: tenant_id = app.current_tenant_id()
```

### documents
```
id              TEXT PRIMARY KEY
tenant_id       TEXT NOT NULL
filename        TEXT NOT NULL
content_hash    TEXT NOT NULL
size_bytes      INT NOT NULL
mime_type       TEXT NOT NULL
storage_key     TEXT NOT NULL
job_id          TEXT
created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
RLS: tenant_id = app.current_tenant_id()
```

### audit_events (append-only, never update or delete)
```
id              TEXT PRIMARY KEY                   (deterministic: "audit-{job_id}-{seq}")
tenant_id       TEXT NOT NULL
job_id          TEXT NOT NULL
actor           TEXT NOT NULL                      (system | user:{id} | queue-worker:{id} | slack:{id})
action          TEXT NOT NULL                      (job_created | job_approved | vendor_created | trust_tier_changed | ...)
old_state       TEXT
new_state       TEXT
metadata        JSONB DEFAULT '{}'
created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
RLS: tenant_id = app.current_tenant_id()
Index: (tenant_id, job_id, created_at)
Index: (tenant_id, created_at DESC)
```

### hitl_requests
```
id              TEXT PRIMARY KEY                   (deterministic: "hitl-{job_id}")
tenant_id       TEXT NOT NULL
job_id          TEXT NOT NULL
workflow        TEXT NOT NULL
tier            INT NOT NULL                       (1=mandatory, 2=conditional, 3=bypassed)
status          TEXT NOT NULL DEFAULT 'PENDING'    (PENDING | APPROVED | REJECTED | EXPIRED | AUTO_APPROVED | AUTO_REJECTED)
context         JSONB
decision        TEXT
decision_reason TEXT
decided_by      TEXT
sla_expires_at  TIMESTAMPTZ
created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
RLS: tenant_id = app.current_tenant_id()
```

### compliance_chunks
```
id              TEXT PRIMARY KEY
tenant_id       TEXT NOT NULL
source_url      TEXT NOT NULL
source_hash     TEXT NOT NULL
content         TEXT NOT NULL
chunk_index     INT NOT NULL
heading_path    TEXT[]
section_id      TEXT
metadata        JSONB DEFAULT '{}'
severity        TEXT                               (CRITICAL | HIGH | MEDIUM | LOW)
tsv_content     TSVECTOR                           (generated, for full-text search)
created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
RLS: tenant_id = app.current_tenant_id()
GIN index: tsv_content
```

### usage_records
```
tenant_id       TEXT NOT NULL
metric          TEXT NOT NULL                      (documents_uploaded | ocr_pages | llm_calls | compliance_checks)
count           BIGINT NOT NULL DEFAULT 0
period_start    DATE NOT NULL
period_end      DATE NOT NULL
updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
PRIMARY KEY (tenant_id, metric, period_start)
RLS: tenant_id = app.current_tenant_id()
```

---

## API Reference

### GET /health
```
Response 200:
{
  "status": "healthy",
  "timestamp": "2026-07-13T10:30:00Z",
  "services": {
    "postgres": "healthy",
    "minio": "healthy",
    "redis": "healthy"
  }
}
```

### GET /status/summary
```
Headers: X-Tenant-ID: string (required)

Response 200:
{
  "queued": 3,
  "processing": 1,
  "completed": 134,
  "failed": 2,
  "pending_hitl": 1
}
```

### POST /upload
```
Headers: X-Tenant-ID: string (required)
         Authorization: Bearer <token> or X-API-Key: <key>
Body: multipart/form-data, field "file" (PDF or IMG, max 10MB)

Response 200: {"job_id": "uuid", "status": "queued"}
Response 400: {"error": "invalid file type"} | {"error": "missing X-Tenant-ID"}
Response 403: {"error": "plan limit exceeded", "metric": "documents_uploaded", "current": 100, "limit": 100}
Response 409: {"error": "duplicate document", "content_hash": "sha256:..."}
Response 413: {"error": "file too large, max 10MB"}
Response 500: {"error": "internal error", "trace_id": "..."}
```

### GET /jobs/recent
```
Headers: X-Tenant-ID: string (required)
Query: ?limit=10 (default)

Response 200:
{
  "jobs": [
    {"id": "uuid", "workflow_type": "document_ingestion", "status": "completed", "created_at": "..."},
    ...
  ]
}
```

### GET /jobs/{id}
```
Headers: X-Tenant-ID: string (required)

Response 200:
{
  "job_id": "uuid",
  "status": "completed",
  "workflow_type": "document_ingestion",
  "document_type": "INVOICE",
  "confidence": 0.92,
  "extracted_data": {...},
  "risk_flags": [],
  "hitl_reason": "",
  "retry_count": 0,
  "created_at": "...",
  "updated_at": "..."
}

Response 404: {"error": "job not found"}
```

### GET /jobs/{id}/audit
```
Headers: X-Tenant-ID: string (required)

Response 200:
{
  "events": [
    {"actor": "system", "action": "job.created", "old_state": "", "new_state": "PENDING", "created_at": "..."},
    {"actor": "system", "action": "job.queued", "old_state": "PENDING", "new_state": "QUEUED", "created_at": "..."},
    {"actor": "system", "action": "job.completed", "old_state": "PROCESSING", "new_state": "COMPLETED", "created_at": "..."}
  ]
}
```

### POST /vendors
```
Headers: X-Tenant-ID: string (required)
Body: {name, gst, pan, ifsc, ...}

Response 200: {"vendor_id": "uuid", "risk_score": 65, "trust_tier": "PROBATION", "status": "pending"}
Response 403: {"error": "plan limit exceeded"}
Response 422: {"error": "validation failed", "fields": {"gst": "invalid format"}}
```

### GET /vendors/{id}
```
Response 200: {vendor detail object}
```

### GET /vendors
```
Headers: X-Tenant-ID: string (required)
Query: ?status=active&trust_tier=PROBATION&limit=20

Response 200: {"vendors": [...], "total": 5}
```

### GET /vendors/risky
```
Response 200: {"vendors": [...], "total": 2, "threshold": 60}
```

### GET /compliance/recent
```
Headers: X-Tenant-ID: string (required)
Query: ?limit=10

Response 200:
{
  "items": [
    {"id": "uuid", "source_name": "SEBI", "severity": "HIGH", "created_at": "..."},
    ...
  ]
}
```

### POST /compliance/run
```
Headers: X-Tenant-ID: string (required)

Response 200: {"job_id": "uuid", "status": "queued"}
Response 403: {"error": "plan limit exceeded", "metric": "compliance_checks"}
```

### GET /admin/review-queue
```
Headers: Authorization: Bearer <token> (requires OpsAdmin+ role)

Response 200:
{
  "pending": [
    {
      "id": "hitl-uuid",
      "workflow": "vendor_onboarding",
      "tier": 1,
      "status": "PENDING",
      "context": {"vendor_name": "Acme Trading", "risk_score": 82, "reason": "high_risk_score"},
      "created_at": "...",
      "sla_expires_at": "..."
    }
  ],
  "total": 1,
  "summary": {"mandatory": 1, "conditional": 0, "expired": 0}
}
```

### POST /admin/review-queue
```
Headers: Authorization: Bearer <token> (requires OpsAdmin+ role)
Body: {id: "hitl-uuid", action: "approve" | "reject", reason: "optional text"}

Response 200: {"status": "approved", "job_id": "...", "audit_event_id": "..."}
Response 404: {"error": "HITL request not found"}
Response 409: {"error": "HITL request already resolved"}
```

### GET /admin/usage-summary
```
Headers: Authorization: Bearer <token> (requires Owner or Auditor role)

Response 200:
{
  "tenant_id": "tenant-alpha",
  "plan": "pro",
  "period": {"start": "2026-07-01", "end": "2026-07-31"},
  "usage": {
    "documents_uploaded": {"current": 45, "limit": 1000},
    "ocr_pages": {"current": 89, "limit": 1000},
    "llm_calls": {"current": 12, "limit": 100},
    "compliance_checks": {"current": 3, "limit": 500}
  }
}
```

### GET /metrics/llm-summary
```
Response 200:
{
  "total_calls": 142,
  "total_tokens": 142500,
  "total_cost_inr": 28.50,
  "avg_latency_ms": 2340,
  "calls_by_model": {"sarvam-m": 120, "tencent/hy3:free": 22},
  "calls_by_agent": {"vendor": 80, "compliance": 52, "document_fallback": 10}
}
```

### GET /metrics/workflow-summary
```
Response 200:
{
  "total_jobs": 523,
  "completed": 501,
  "failed": 15,
  "pending_hitl": 7,
  "avg_duration_ms": 3450,
  "queue_depth": {"document-queue": 2, "vendor-queue": 0, "compliance-queue": 0},
  "dlq_depth": {"document-queue-poison": 1, "vendor-queue-poison": 0}
}
```

### POST /slack/webhook
```
Headers: X-Slack-Signature: string (verified in production)
         X-Slack-Request-Timestamp: string

Body: Slack interactive payload (application/x-www-form-urlencoded)

Response 200: (empty body — Slack requires 200 within 3 seconds)

Side effects:
  - Updates hitl_request.status in PostgreSQL
  - Updates job.status to "completed" or "flagged"
  - Emits audit_event with actor = "slack:{user_id}"
  - If approved: re-enqueues job for ledger write
```

---

## Provider Interfaces

```go
type OCRProvider interface {
    Extract(ctx context.Context, filePath string) (*OCRResult, error)
}

type LLMProvider interface {
    ExtractFields(ctx context.Context, text string, schema any) (*LLMResult, error)
    Reason(ctx context.Context, prompt string) (*LLMResult, error)
    Chat(ctx context.Context, messages []ChatMessage) (*LLMResult, error)
}

type LLMResult struct {
    Content          string
    ReasoningDetails *json.RawMessage
    Model            string
    InputTokens      int
    OutputTokens     int
    DurationMs       int64
    CostINR          float64
}

type StorageProvider interface {
    Upload(ctx context.Context, key string, r io.Reader) (string, error)
    Download(ctx context.Context, key string) (io.ReadCloser, error)
    Delete(ctx context.Context, key string) error
}

type QueueProvider interface {
    Enqueue(ctx context.Context, queueName string, message any) (string, error)
    Dequeue(ctx context.Context, queueName string) (*QueueMessage, error)
    Delete(ctx context.Context, queueName, messageID string) error
    Poison(ctx context.Context, queueName, messageID string) error
}

type DBProvider interface {
    UpsertJob(ctx context.Context, job Job) error
    GetJob(ctx context.Context, id, tenantID string) (*Job, error)
    UpsertVendor(ctx context.Context, v Vendor) error
    GetVendor(ctx context.Context, id, tenantID string) (*Vendor, error)
    AppendAuditEvent(ctx context.Context, e AuditEvent) error
    GetComplianceChunks(ctx context.Context, tenantID string, opts ...QueryOption) ([]ComplianceChunk, error)
    UpsertHITLRequest(ctx context.Context, req HITLRequest) error
    GetHITLRequests(ctx context.Context, tenantID string, status string) ([]HITLRequest, error)
}

type HITLProvider interface {
    SendApprovalRequest(ctx context.Context, req HITLRequest) error
    UpdateMessage(ctx context.Context, req HITLRequest) error
}

type TracingProvider interface {
    RecordLLMCall(ctx context.Context, model string, tokens int, costINR float64, durationMs int64)
    RecordProviderFallback(ctx context.Context, provider string, step string, err error)
    Shutdown(ctx context.Context) error
}

type AuthProvider interface {
    Authenticate(ctx context.Context, token string) (*User, error)
}

type TenantProvider interface {
    GetTenant(ctx context.Context, id string) (*Tenant, error)
    GetTenantBySlug(ctx context.Context, slug string) (*Tenant, error)
}

type UsageProvider interface {
    IncrementUsage(ctx context.Context, tenantID string, metric Metric, count int64) error
    GetUsage(ctx context.Context, tenantID string, metric Metric) (int64, error)
    GetCurrentPeriodUsage(ctx context.Context, tenantID string) (map[Metric]int64, error)
    CheckLimit(ctx context.Context, tenantID string, metric Metric) (bool, error)
}
```

All adapters are swappable via constructor injection. Tests use real implementations against Docker containers (PostgreSQL, MinIO, Redis) — no mocks except for Slack (Mockoon) and provider stubs.

---

## Multi-Tenant Authentication & RBAC Model

### Authentication
API-key based using `Authorization: Bearer <token>` or `X-API-Key: <key>` header.
- Dev: `StaticAPIKeyProvider` — hardcoded keys for development
- Production: DB-backed `AuthProvider` — keys stored in `users` table, hashed

### Roles & Permissions
Five fixed roles with explicit permission sets:

| Role | Permissions |
|------|-------------|
| **Owner** | `admin:*` (wildcard — all permissions) |
| **OpsAdmin** | document:upload, document:view, vendor:create, vendor:view, vendor:approve, compliance:view, compliance:run, admin:review, audit:view, metrics:view |
| **Reviewer** | document:view, vendor:view, vendor:approve, compliance:view, audit:view |
| **Auditor** | document:view, vendor:view, compliance:view, audit:view, metrics:view |
| **Viewer** | document:view, vendor:view, compliance:view |

### Tenant Isolation (Three Layers)
1. **API layer** — `X-Tenant-ID` header required on all tenant-scoped endpoints; auth middleware verifies token scoping
2. **Application layer** — every DB query includes `WHERE tenant_id = $1`
3. **Database layer** — PostgreSQL Row-Level Security (RLS) on every tenant-scoped table

---

## RBAC Middleware Chain

```
Request → Auth Middleware → Tenant Middleware → Rate Limit Middleware → Usage Middleware → Handler
```

1. **Auth middleware** (`middleware/auth/auth.go`): Extracts API key → resolves to `domain.User` with tenant and role → injects into context. Optionally verifies `X-Tenant-ID` matches user's tenant (returns 403 on mismatch). `RequirePermission(perm)` wrapper returns 401/403.
2. **Tenant middleware** (`middleware/tenant/tenant.go`): Reads `X-Tenant-ID` header → resolves to `domain.Tenant` → sets `app.tenant_id` PostgreSQL session variable (enables RLS) → injects into context. Falls back to `default` tenant if no header.
3. **Rate limit middleware**: Token-bucket rate limiter per tenant (configurable).
4. **Usage middleware**: Before any billable write, calls `UsageProvider.CheckLimit()` → returns 403 Plan Limit Exceeded if at limit.

---

## Plan & Usage Enforcement

### Plans

| Plan | Documents/Month | OCR Pages | LLM Calls | Compliance Checks | Price Tier |
|------|----------------|-----------|-----------|-------------------|------------|
| Starter | 100 | 100 | 0 (LLM disabled) | 50 | Free |
| Pro | 1,000 | 1,000 | 100 | 500 | Paid |
| Business | 10,000 | 10,000 | Unlimited (-1) | 5,000 | Paid |

### Enforcement Points
- **Upload**: `CheckLimit("documents_uploaded")` before accepting file
- **OCR processing**: `CheckLimit("ocr_pages")` before submitting to Sarvam
- **LLM fallback**: `CheckLimit("llm_calls")` before invoking Sarvam-M
- **Compliance run**: `CheckLimit("compliance_checks")` before scheduling analysis

---

## HITL Classification (Three-Tier)

| Tier | Classification | Behavior | Examples |
|------|---------------|----------|----------|
| **1** | Mandatory | Blocks workflow until human decides. SLA expiry → auto-reject → DLQ. | Risk score ≥ 80, duplicate GST, fraud flag, OCR confidence < 0.5, conflicting verification |
| **2** | Conditional | Blocks for SLA duration, then auto-approves. | Risk score 60-79, first-time vendor, OCR confidence 0.5-0.7, unclassified document type |
| **3** | Bypassed | Fully automated. No HITL required. | Risk score < 60, all checks pass, OCR confidence > 0.7, routine compliance match |

Channels: Slack Block Kit (instant actions), Admin API (programmatic), Streamlit admin UI (dashboard). The Admin API is the canonical source of truth.

---

## LLM Use Policy (Non-Negotiable)

**Call Sarvam-M only for:**
- Ambiguous document field extraction when OCR confidence < 0.85
- Compliance gap reasoning against regulatory text

**Never call any LLM for:**
- Document type classification → domain/document_classifier.go (keyword rules)
- GST / PAN / IFSC validation → domain/india_validator.go (regex)
- Risk score computation → domain/risk_scorer.go (formula)
- Trust Battery transitions → domain/trust_battery.go (state machine)
- Duplicate vendor detection → PostgreSQL query
- Severity classification of compliance gaps → keyword rules
- Queue routing or error classification → deterministic rules
- Any threshold decision that can be encoded as a constant

**Config gating:**
- Global: `LLM_ENABLED=false` env var disables all LLM calls
- Per-tenant: `llm_enabled` key in `Tenant.Config` JSONB
- Agent-level: `if a.llm == nil` provider guard

Every LLM call must go through `providers.LLMProvider`, record metadata (model, tokens, cost, latency) via `TracingProvider.RecordLLMCall()`, and be justifiable in code review.

---

## Test Strategy

### Build Tags

| Tag | Scope | Dependencies | CI |
|-----|-------|-------------|----|
| No tag | Domain unit tests (`internal/domain/...`) | None | Always |
| `//go:build integration` | Adapter + agent + E2E tests | Docker (Postgres, MinIO, Redis) | PR + main |
| `//go:build agentic` | Agent workflow orchestration tests | Docker | PR + main |
| `//go:build live` | Live provider contract tests | Real API keys, rate-limited | Opt-in only |

### Test Pyramid

```
make test-unit            → internal/domain/... (fast, zero infra, <5 seconds)
make test-integration     → tests/integration/... (Docker containers + real adapters)
make test-agentic          → tests/agentic/... (orchestrated workflows with controlled providers)
make test-e2e              → tests/e2e/... (full stack in Docker Compose)
make test-live             → tests/live/... (real API calls, //go:build live, rate-limited)
make test-load-smoke       → tests/load/... (k6 smoke test)
```

### Test Layers

1. **Unit tests** (`make test-unit`) — cover `internal/domain/` with no external dependencies. Pure Go, always runs in CI. No build tags needed. 35+ top-level test functions, ~180 subtests, all pass with `-race`.

2. **Integration tests** (`make test-integration`) — require Docker containers for PostgreSQL, MinIO, and Redis. Tagged with `//go:build integration`. Test adapters against real (containerized) infrastructure — no mocks for data plane dependencies.

3. **Agentic tests** (`make test-agentic`) — full workflow orchestration tests with controlled provider stubs. Verify document ingestion, vendor onboarding, and compliance monitoring trajectories.

4. **Golden file tests** — expected output JSON in `tests/golden/` for deterministic regression checks.

5. **E2E tests** (`make test-e2e`) — full stack in Docker Compose: API + worker + Postgres + MinIO + Redis + stub OCR.

6. **Live provider tests** (`make test-live`, `//go:build live`) — opt-in tests that call real Sarvam OCR, Sarvam LLM, Slack API, SEBI RSS, RBI RSS. Rate-limited to max 3 real API calls per test run via `tests/live/budget.go`. Assert schema invariants, never brittle LLM wording.

7. **Load tests** (`make test-load`) — k6 scripts in `tests/load/`.

8. **Security tests** (`tests/security/`) — SQL injection, XSS, CSRF, auth bypass scenarios.

### Live Provider Test Policy

- Never run in default CI. Opt-in via `make test-live`.
- Rate-limited: `tests/live/budget.go` enforces max 3 real API calls per invocation.
- Assert schema invariants (required fields, structure) — never brittle LLM wording.
- Debug dump to `/tmp/opscore_live_*.json` for manual inspection.
- Run against staging tenant with low-volume curated datasets.

### What Tests Must Prove

- Correctness of deterministic rules (domain unit tests)
- Provider contract correctness (integration tests with real containers)
- Workflow orchestration correctness (agentic tests)
- Idempotency, retries, DLQ behavior (agentic error recovery tests)
- Restart safety (concurrent crash recovery tests)
- Duplicate-event handling (idempotency tests)
- Tenant isolation (RLS integration tests)
- RBAC enforcement (auth middleware tests)

---

## Queue / Worker Design

### Backend
Redis 7 with two data structures per queue:
- **List (main queue)** — LPUSH (enqueue), BRPopLPush (dequeue with atomic lease)
- **Hash** — full message body by message ID for O(1) Delete/Poison
- **Processing list** — in-flight messages (maintained by BRPopLPush)
- **DLQ** — `{queueName}-poison` list with full JSON preserved

### Queues
| Queue | Worker | Description |
|-------|--------|-------------|
| `document-queue` | Worker goroutine 1 | Document OCR + extraction |
| `vendor-queue` | Worker goroutine 2 | Vendor validation + scoring |
| `compliance-queue` | Worker goroutine 3 | Compliance analysis |

### Semantics
- **At-least-once delivery** via BRPopLPush atomic lease
- **Idempotent handlers**: content-based SHA256 dedup + optimistic locking on Job.Version + deterministic HITLRequest IDs + UPSERT everywhere
- **DLQ on failure**: `Poison()` moves message to `{queue}-poison` list
- **Panic recovery**: `defer recover()` in every poll loop
- **Two goroutines, one per queue**: no shared mutable state, testable with `-race`

### Retry Strategy
- Queue layer: no capped retry — poison on first handler error
- DB layer: `JobStatus.RETRYABLE_FAILED` → external scheduler or operator re-queues
- Provider layer: adapter-level retry with exponential backoff (3 attempts)
- Wall-clock timeout: 5 minutes per job processing step

---

## Observability

### Three Pillars

1. **Structured JSON logging** via `log/slog` — every log line includes `time`, `level`, `msg`, `job_id`, `tenant_id`, `duration_ms`. No bare `fmt.Println` or `log.Printf`.

2. **Langfuse Cloud tracing** — every OCR and LLM call traced with latency, token count, estimated cost, prompt version. No-op fallback when Langfuse is not configured.

3. **Metrics endpoints** — `/metrics/llm-summary` and `/metrics/workflow-summary` provide operational insight without Prometheus/Grafana infrastructure.

### Audit Trail
Append-only `audit_events` table. Every state transition recorded with actor, action, old state, new state, and metadata. Never updated or deleted. Configurable TTL (default 90 days).

---

## Deployment

### Local Development
```bash
# 1. Clone
git clone https://github.com/Aparnap2/opscore.git
cd opscore

# 2. Configure environment
cp .env.example .env
# Edit .env as needed (defaults work for local dev)

# 3. Start infrastructure (Postgres + MinIO + Redis)
make local-up
# Launches: Postgres 16 on :5432, MinIO on :9000/:9001, Redis 7 on :6379

# 4. Run the API server (terminal 1)
make run-api
# Starts Go HTTP server on :8080

# 5. Run the admin UI (terminal 2)
make run-ui
# Starts Streamlit dashboard on :8501

# 6. Verify
curl http://localhost:8080/health
curl http://localhost:8080/status/summary
```

### Production Target: Google Cloud Run
```
Dockerfile builds minimal Go binary → Cloud Run (serverless container)
Cloud SQL (managed PostgreSQL 16) → primary database
Memorystore / Upstash (managed Redis 7) → queue backend
GCS / S3-compatible → document storage
Cloud Secret Manager → secrets (API keys, tokens)
```

### CI/CD Pipeline
```
PR opened         → ci.yml: golangci-lint + test-unit + test-integration + go build + secret scan
Merge to main     → cd-staging.yml: build + deploy to staging + smoke test
Tag v*            → cd-prod.yml: manual approval gate + deploy + smoke test
```

### Required Environment Variables
```
DATABASE_URL                  # PostgreSQL connection string
MINIO_ENDPOINT                # S3-compatible endpoint
MINIO_ACCESS_KEY
MINIO_SECRET_KEY
REDIS_URL                     # Redis connection string
SARVAM_API_KEY
OPENROUTER_API_KEY            # LLM fallback provider
SLACK_BOT_TOKEN               # HITL notifications
SLACK_SIGNING_SECRET          # Webhook verification
SLACK_HITL_CHANNEL            # e.g. C08XXXXXXXX
LANGFUSE_SECRET_KEY           # LLM observability
LANGFUSE_PUBLIC_KEY
LANGFUSE_HOST
LLM_ENABLED                   # Global LLM toggle (default: true in dev, false in prod)
```

---

## Coding Standards

### Go
- `golangci-lint` must pass with zero warnings on every PR
- No `interface{}` where a concrete type is available
- All errors must be handled — no blank identifier on error returns
- All external calls must accept `context.Context` with timeout
- No hardcoded secrets, ports, or connection strings
- Integration tests require `//go:build integration` build tag
- Live provider tests require `//go:build live` build tag
- All logging via `log/slog` with JSON handler — no bare Println

### Architecture Rules
- `internal/domain/` must have zero I/O imports
- Domain code must not import database, HTTP, Redis, MinIO, or SDK code
- All external systems must be hidden behind interfaces in `internal/providers`
- `internal/adapters` implements provider interfaces only
- `internal/agents` orchestrates workflows but contains no raw infrastructure code
- `cmd/*` wires dependencies only; contains no business rules

### Commits
```
feat:     adds new capability
fix:      corrects a defect
chore:    dependency, config, or build change
test:     new or updated tests only
docs:     documentation only
refactor: no behaviour change
```

### Operational Rules
- Queue handlers: ack on success, poison on error (move to DLQ)
- HITL: idempotent — duplicate Slack webhook callbacks must not double-process
- Audit events: append-only. No UPDATE or DELETE ever
- All mutations: write audit_event before returning success to caller
- Slack signing secret: verified only when APP_ENV=production
- LLM calls: must go through provider interface, record metadata, be config-gated
- Retries: bounded ceilings on all provider calls (max 3), agent loops (max 5 depth), wall-clock timeouts (5 min)

---

## Deferred (Not in v5.0)

| Feature | Milestone |
|---------|-----------|
| Vector search (pgvector) for compliance chunk similarity | v5.1 (gated on data volume) |
| Email ingestion (inbound email → file → queue) | v5.1 |
| Self-served tenant onboarding UI (Stripe/Razorpay integration) | v5.2 |
| QuickBooks MCP integration (approved invoice → bill) | v5.2 |
| Linear ticket creation on HITL flag | v5.2 |
| RBAC UI in Streamlit (tenant admin manages users) | v5.2 |
| Custom linter for domain I/O isolation | v5.3 |
| Neo4j vendor relationship graph | Evaluate after v5.1 data volume |
| Real-time compliance alerts via webhook | v5.3 |
