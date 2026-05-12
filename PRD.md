# OpsCore — Product Requirements Document v4.0

**Version:** 4.0 — Final  
**Date:** 2026-05-12  
**Status:** Active — Ship  
**Repository:** github.com/aparna/opscore · branch: main  
**Live URL:** https://opscore-functions-prod.azurewebsites.net/api/health  

---

## One Paragraph Description

OpsCore is an autonomous back-office system for Indian B2B businesses. It ingests vendor documents (PDF invoices, onboarding forms, compliance notices) via API, validates Indian tax identifiers (GST, PAN, IFSC) using compiled Go regex, scores vendor risk using a deterministic formula, tracks vendor trust state through a five-state machine, and delivers a single structured Slack approval request to the business owner. The owner clicks one button. All classification, validation, scoring, and audit-trail logic is compiled Go code — no LLM. Sarvam AI is invoked only when OCR confidence falls below threshold or when compliance gap reasoning requires natural language. The system runs on Azure Functions (Consumption plan) with Cosmos DB, Blob Storage, and Queue Storage — zero containers in production, zero cost when idle.

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
|---|---|---|
| Language | Go 1.22+ | Single compiled binary, no runtime, deterministic behaviour |
| Agent runtime | Google ADK Go v1.0 | Structured tool calling, minimal prompt overhead |
| Deployment | Azure Functions Consumption Y1 | Zero containers, zero idle cost, scales to zero |
| Routing | Custom handler, enableForwardingHttpRequest: true | HTTP routes work as standard Go net/http mux |
| Queue | Azure Queue Storage | Always-free tier, no broker process |
| Database | Azure Cosmos DB NoSQL (free tier) | Serverless, 1000 RU/s, 25 GB, partition by tenant_id |
| Storage | Azure Blob Storage (Standard LRS) | PDF uploads and extracted artifacts |
| OCR / Extraction | Sarvam Document Intelligence | India-native, job-based, structured output |
| LLM | Sarvam-M (json_schema response format) | India-specific, free credits, used sparingly |
| OCR Fallback | Modal (Docling + Granite) | Fires only on confidence < 0.70 |
| HITL | Slack Block Kit | Approval in existing workflow, no new UI |
| Observability | Azure Application Insights (OTel) | 5 GB free, zero hosting |
| IaC | Azure Bicep | Provisions all resources in one command |
| Local test host | Azure Functions Core Tools v4 | Exact same runtime as production |

### Infrastructure Cost

₹0/month at demo and small-production scale. All services are within their always-free or 12-month-free tiers. Budget alert set at ₹400/month as a safety gate.

### Three Containers (Test Only)

| Service | Image | Purpose |
|---|---|---|
| cosmos | mcr.microsoft.com/cosmosdb/linux/azure-cosmos-emulator:vnext-preview | Cosmos DB NoSQL emulator |
| azurite | mcr.microsoft.com/azure-storage/azurite | Blob + Queue emulator |
| mockoon | mockoon/cli | Slack API mock |

These run only during local development and CI. Production has zero containers.

---

## Repository Layout

```
opscore/
├── cmd/
│   ├── functions/main.go          # Entry: registers all HTTP + queue + timer handlers
│   └── migrate/main.go            # One-time: creates Cosmos containers + indexes
│
├── internal/
│   ├── domain/                    # Pure Go, zero I/O — source of truth for all business rules
│   │   ├── trust_battery.go       # State machine: PROBATION→STANDARD→PREFERRED→STRATEGIC→BLOCKED
│   │   ├── risk_scorer.go         # Deterministic risk score 0-100
│   │   ├── document_classifier.go # Keyword classification: INVOICE/CONTRACT/GST_NOTICE/PO/OTHER
│   │   ├── india_validator.go     # GST / PAN / IFSC regex validation
│   │   └── schemas.go             # All typed Go structs with json tags
│   │
│   ├── agents/                    # ADK Go agents (LLM via tools only)
│   │   ├── document_agent.go
│   │   ├── vendor_agent.go
│   │   └── compliance_agent.go
│   │
│   ├── providers/                 # Interfaces + implementations
│   │   ├── interfaces.go          # OCRProvider, LLMProvider, StorageProvider, QueueProvider, DBProvider, HITLProvider
│   │   ├── sarvam/ocr.go          # Sarvam Document Intelligence (job-poll loop)
│   │   ├── sarvam/llm.go          # Sarvam-M (always json_schema)
│   │   ├── modal/fallback.go      # Docling fallback (confidence < 0.70)
│   │   ├── azure/blob.go
│   │   ├── azure/queue.go
│   │   ├── azure/cosmos.go
│   │   └── slack/hitl.go          # Block Kit approval messages
│   │
│   └── functions/                 # Azure Function trigger handlers
│       ├── http_health.go         # GET /api/health
│       ├── http_upload.go         # POST /api/upload
│       ├── http_job_status.go     # GET /api/jobs/{jobId}
│       ├── http_slack_webhook.go  # POST /api/slack/webhook
│       ├── queue_document.go      # Queue: document-queue
│       ├── queue_vendor.go        # Queue: vendor-queue
│       ├── queue_compliance.go    # Queue: compliance-queue
│       ├── timer_scraper.go       # Timer: Mon 9am IST
│       └── timer_trust_decay.go   # Timer: daily 2am IST
│
├── tests/
│   ├── domain/                    # Unit tests — no build tag, always runs
│   ├── adapters/                  # Integration — //go:build integration
│   ├── agents/                    # Integration — //go:build integration
│   ├── e2e/                       # E2E — //go:build integration
│   └── fixtures/
│       ├── pdfs/                  # invoice_english.pdf, invoice_hindi.pdf, gst_notice.pdf
│       └── expected/              # Golden output JSON files
│
├── infra/
│   ├── main.bicep                 # Provisions all 5 Azure resources
│   └── modules/
│
├── .github/workflows/
│   ├── ci.yml                     # lint + unit + integration + bicep build + secret scan
│   ├── cd-staging.yml             # deploy on main push
│   └── cd-prod.yml                # deploy on tag v* with manual approval gate
│
├── HttpHealth/function.json
├── HttpUpload/function.json
├── HttpJobStatus/function.json
├── HttpSlackWebhook/function.json
├── QueueDocument/function.json
├── QueueVendor/function.json
├── QueueCompliance/function.json
├── TimerScraper/function.json
├── TimerTrustDecay/function.json
│
├── host.json                      # enableForwardingHttpRequest: true
├── local.settings.json            # gitignored: Azurite + emulator settings
├── docker-compose.test.yml        # 3 containers: cosmos + azurite + mockoon
├── Makefile
├── go.mod
├── .env.example
└── README.md
```

---

## Trigger Map (Complete)

| Function Name | Trigger Type | Route / Queue / Schedule | File |
|---|---|---|---|
| HttpHealth | HTTP GET | /api/health | internal/functions/http_health.go |
| HttpUpload | HTTP POST | /api/upload | internal/functions/http_upload.go |
| HttpJobStatus | HTTP GET | /api/jobs/{jobId} | internal/functions/http_job_status.go |
| HttpSlackWebhook | HTTP POST | /api/slack/webhook | internal/functions/http_slack_webhook.go |
| QueueDocument | Queue | document-queue | internal/functions/queue_document.go |
| QueueVendor | Queue | vendor-queue | internal/functions/queue_vendor.go |
| QueueCompliance | Queue | compliance-queue | internal/functions/queue_compliance.go |
| TimerScraper | Timer | 0 0 9 * * 1 (Mon 9am IST) | internal/functions/timer_scraper.go |
| TimerTrustDecay | Timer | 0 0 2 * * * (daily 2am IST) | internal/functions/timer_trust_decay.go |

---

## Workflow 1: Document Ingestion

### Entry
```
POST /api/upload
Headers: X-Tenant-ID: string
Body: multipart/form-data, field "file" (PDF ≤ 10MB)
```

### Processing Pipeline
```
1. Validate: file type must be application/pdf, size ≤ 10MB
2. Write PDF to Azure Blob Storage at tenants/{tenant_id}/docs/{uuid}.pdf
3. Create job record in Cosmos DB:
   {id, tenant_id, status: "queued", blob_url, created_at, trace_id}
4. Enqueue message to document-queue:
   {job_id, tenant_id, blob_url, trace_id}
5. Return HTTP 200: {job_id, status: "queued"}

[Queue trigger fires]
6. Download PDF from Blob Storage
7. Submit to Sarvam Document Intelligence (presigned URL upload)
8. Poll Sarvam job until status = Completed or PartiallyCompleted or Failed
   - If Failed or confidence < 0.70: invoke Modal fallback (Docling)
9. Classify document type using keyword matching (no LLM):
   INVOICE / CONTRACT / GST_NOTICE / PO / OTHER
10. Validate Indian identifiers using regex:
    GST: ^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z]{1}[1-9A-Z]{1}Z[0-9A-Z]{1}$
    PAN: ^[A-Z]{5}[0-9]{4}[A-Z]{1}$
    IFSC: ^[A-Z]{4}0[A-Z0-9]{6}$
11. Check for duplicate GST in Cosmos DB
12. Evaluate HITL conditions:
    - Invoice amount > ₹1,00,000
    - OCR confidence < 0.85
    - GST/PAN mismatch vs vendor record
    - Document type is CONTRACT or GST_NOTICE (always)
13. If HITL required:
    a. Create hitl_request record in Cosmos DB
    b. Send Slack Block Kit with Approve / Flag / Edit buttons
    c. Update job status: "pending_hitl"
    d. Wait for POST /api/slack/webhook callback
14. On approval:
    a. Write extracted_data to Cosmos DB
    b. Update job status: "completed"
    c. Append audit_event: {actor, action, old_state, new_state, timestamp}
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
Headers: X-Tenant-ID: string
Body: JSON {name, gst, pan, ifsc, bank_name, contact_email, documents: [blob_urls]}
```

### Processing Pipeline
```
1. Write vendor draft + job record to Cosmos DB
2. Enqueue to vendor-queue

[Queue trigger fires]
3. ValidateGST(gst) → bool, error_detail
4. ValidatePAN(pan) → bool, error_detail
5. ValidateIFSC(ifsc) → bool, error_detail
6. Duplicate check: query Cosmos for existing vendor with same GST in tenant
7. Compute risk score (deterministic formula, no LLM):
   Base: 50
   +15 if GST valid
   +10 if PAN valid
   +10 if IFSC valid
   -40 if duplicate GST found
   -20 if active dispute flag exists
   -15 if incomplete documents
8. Set initial trust tier: PROBATION
9. If score < 40 OR duplicate GST: require HITL approval
10. On approval: write vendor record, emit audit_event
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

Timer `TimerTrustDecay` runs daily at 2am IST. It queries all vendors with `last_transaction_at` older than 180 days and applies InactivityDecay transitions. Every transition emits an audit_event.

---

## Workflow 3: Compliance Monitoring

### Entry
Timer `TimerScraper` fires Monday 9am IST.

### Processing Pipeline
```
1. Fetch SEBI / RBI / GST portal RSS feeds using Go net/http
2. For each item: compute SHA256 hash of content URL
3. Compare against last-seen hashes in Cosmos DB
4. New items only: enqueue to compliance-queue

[Queue trigger fires]
5. Download full document
6. Write raw file to Blob Storage
7. Extract text:
   - PDF: Sarvam Document Intelligence
   - HTML: Go html.NewTokenizer (no OCR)
8. Chunk text into ~500 token segments
9. Write chunks to Cosmos DB compliance_chunks container
10. Invoke Sarvam-M with tenant policy context:
    "Given this regulatory text and these policy rules, identify gaps."
    Response format: json_schema {gaps: [{clause, severity, description, citation}]}
11. Classify severity per gap using keyword rules:
    CRITICAL: ["penalty", "prosecution", "suo moto", "cancellation"]
    HIGH:     ["non-compliance", "show cause", "demand notice"]
    MEDIUM:   ["advisory", "clarification", "amendment"]
    LOW:      ["guidance", "circular", "update"]
12. Write gap report to Cosmos DB
13. If any CRITICAL or HIGH gap: send Slack notification
```

---

## Data Model (Cosmos DB, partition key: tenant_id)

### jobs
```
id              string   (UUID)
tenant_id       string   (partition key)
workflow_type   string   (document_ingestion | vendor_onboarding | compliance)
status          string   (queued | processing | pending_hitl | completed | failed)
blob_url        string
document_type   string   (INVOICE | CONTRACT | GST_NOTICE | PO | OTHER)
confidence      float64
extracted_data  object   (dynamic JSON from OCR + LLM)
risk_flags      []string
hitl_reason     string
trace_id        string
created_at      time.Time
updated_at      time.Time
```

### vendors
```
id                   string
tenant_id            string   (partition key)
name                 string
gst                  string
pan                  string
ifsc                 string
trust_tier           string   (PROBATION | STANDARD | PREFERRED | STRATEGIC | BLOCKED)
risk_score           int
risk_flags           []string
status               string   (pending | active | blocked)
last_transaction_at  time.Time
created_at           time.Time
updated_at           time.Time
```

### audit_events (append-only, never update or delete)
```
id           string
tenant_id    string   (partition key)
actor        string   (system | user_id | slack_user_id)
action       string   (job_created | job_approved | vendor_created | trust_tier_changed | ...)
target_type  string
target_id    string
old_state    string
new_state    string
trace_id     string
timestamp    time.Time
```

### hitl_requests
```
id            string
tenant_id     string   (partition key)
job_id        string
reason        string
status        string   (pending | approved | flagged | edited)
sent_at       time.Time
responded_at  time.Time
responder     string
decision      string
slack_ts      string   (Slack message timestamp, used for updating the message)
```

### compliance_chunks
```
id           string
tenant_id    string   (partition key)
source_url   string
source_hash  string
content      string
chunk_index  int
severity     string
created_at   time.Time
```

---

## API Reference

### GET /api/health
```
Response 200:
{
  "status": "healthy",
  "timestamp": "2026-05-12T...",
  "services": {
    "blob": "healthy",
    "cosmos": "healthy",
    "queue": "healthy"
  }
}
```

### POST /api/upload
```
Headers: X-Tenant-ID: string (required)
Body: multipart/form-data, field "file" (PDF, max 10MB)

Response 200: {"job_id": "uuid", "status": "queued"}
Response 400: {"error": "invalid file type"} | {"error": "missing X-Tenant-ID"}
Response 413: {"error": "file too large, max 10MB"}
Response 500: {"error": "internal error", "trace_id": "..."}
```

### GET /api/jobs/{jobId}
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
  "created_at": "...",
  "updated_at": "..."
}

Response 404: {"error": "job not found"}
```

### POST /api/slack/webhook
```
Headers: X-Slack-Signature: string (verified in production)
         X-Slack-Request-Timestamp: string

Body: Slack interactive payload (application/x-www-form-urlencoded)

Response 200: (empty body — Slack requires 200 within 3 seconds)

Side effects:
  - Updates hitl_request.status in Cosmos DB
  - Updates job.status to "completed" or "flagged"
  - Emits audit_event
  - If approved: re-enqueues job for ledger write
```

---

## Provider Interfaces

```go
type OCRProvider interface {
    Extract(ctx context.Context, blobURL string) (*OCRResult, error)
}

type LLMProvider interface {
    ExtractFields(ctx context.Context, text string, schema any) (json.RawMessage, float64, error)
    Reason(ctx context.Context, prompt string) (string, error)
}

type StorageProvider interface {
    Upload(ctx context.Context, key string, r io.Reader) (string, error)
    Download(ctx context.Context, key string) (io.ReadCloser, error)
}

type QueueProvider interface {
    Enqueue(ctx context.Context, queue string, msg any) (string, error)
}

type DBProvider interface {
    UpsertJob(ctx context.Context, job Job) error
    GetJob(ctx context.Context, id, tenantID string) (*Job, error)
    UpsertVendor(ctx context.Context, v Vendor) error
    AppendAuditEvent(ctx context.Context, e AuditEvent) error
    GetComplianceChunks(ctx context.Context, tenantID string) ([]ComplianceChunk, error)
}

type HITLProvider interface {
    SendApprovalRequest(ctx context.Context, req HITLRequest) error
}
```

All adapters are swappable via constructor injection. Tests use real implementations against emulators — no mocks except for Slack (Mockoon).

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
- Duplicate vendor detection → Cosmos DB query
- Severity classification of compliance gaps → keyword rules
- Any threshold decision that can be encoded as a constant

This policy is enforced in code review. Any PR that adds an LLM call outside these two categories will be rejected.

---

## Test Strategy

### Build Tags
- No tag: domain unit tests. No emulators. Always runs in CI.
- `//go:build integration`: adapter, agent, and E2E tests. Requires docker-compose.test.yml.

### Test Pyramid
```
make test-unit          → internal/domain/... (fast, zero infra, <5 seconds)
make test-integration   → tests/adapters/... (Cosmos emulator + Azurite + real Sarvam API)
make test-agents        → tests/agents/... (ADK Go + tools + emulators)
make test-e2e           → tests/e2e/... (full workflow, all infra, real Sarvam)
```

### E2E Coverage (already passing on e2e-tests branch)
1. Document classification (keyword-based, fixture PDFs)
2. LLM structured extraction (Sarvam-M JSON output)
3. Vendor risk scoring (valid GST/PAN/IFSC inputs)
4. Trust Battery: PROBATION → STANDARD transition
5. Cosmos DB persistence (read-back after write)
6. Compliance gaps table (schema validation)

### Local Dev Loop
```
make dev          → docker compose -f docker-compose.test.yml up -d + go build + func start
make test-local   → curl all HTTP endpoints against localhost:7071
make test-unit    → domain unit tests only

# Gate: never deploy without passing test-local
make deploy       → depends on test-local in Makefile
```

---

## Deployment

### First-Time Setup
```bash
az login
az account set --subscription <subscription-id>
az group create --name opscore-prod --location centralindia
az deployment group create   --resource-group opscore-prod   --template-file infra/main.bicep   --parameters environment=prod
go run ./cmd/migrate   # creates Cosmos containers and indexes
```

### Standard Deploy
```bash
GOOS=linux GOARCH=amd64 go build -o handler ./cmd/functions
func azure functionapp publish opscore-functions-prod
```

### CI/CD Pipeline
```
PR opened     → ci.yml: golangci-lint + test-unit + go build + bicep build + gitleaks secret scan
Merge to main → cd-staging.yml: build + zip deploy + smoke test
Tag v*        → cd-prod.yml: manual approval gate + deploy + smoke test
```

### Required GitHub Secrets
```
AZURE_CREDENTIALS           # az ad sp create-for-rbac output
SARVAM_API_KEY
MODAL_TOKEN
SLACK_BOT_TOKEN             # production only
SLACK_SIGNING_SECRET        # production only
SLACK_HITL_CHANNEL          # e.g. C08XXXXXXXX
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
- Queue handlers: always return HTTP 200, even on processing error (log, do not retry poison messages automatically)
- HITL: idempotent — duplicate Slack webhook callbacks must not double-process
- Audit events: append-only. No UPDATE or DELETE ever
- All mutations: write audit_event before returning success to caller
- Slack signing secret: verified only when APP_ENV=production
- local.settings.json: in .gitignore, never committed

---

## Deferred (Not in v4.0)

| Feature | Milestone |
|---|---|
| Email ingestion (Azure Communication Services inbound → Blob → queue) | v4.1 |
| Cosmos DB vector search for compliance chunk similarity | v4.1 |
| Azure Entra ID B2C multi-tenant auth | v4.1 |
| QuickBooks MCP integration (approved invoice → bill) | v4.2 |
| Linear ticket creation on HITL flag | v4.2 |
| HTMX web UI (Streamlit admin panel) | v4.2 |
| Neo4j vendor relationship graph | Evaluate after v4.1 data volume |

---

## README Structure (For the Repository)

The README must answer four questions in order:

1. **What problem does this solve?** (2 sentences, non-technical)
2. **What does it do?** (the workflow, told as a story — email → Slack approval)
3. **How does it work?** (architecture diagram + stack table)
4. **How do I run it?** (5 commands: clone → cp .env → docker compose up → func start → curl health)

Then: live demo URL, Loom video link.

Nothing else. No lengthy explanations. The code is the documentation.
