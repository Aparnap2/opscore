# OpsCore — Agentic Internal Operations Platform
## Product Requirements Document v3.0

**Version:** 3.0  
**Date:** 2026-05-08  
**Status:** Active  
**Branch:** main (Go + Azure)  
**Previous version:** v2.0 (Python + Docker OSS stack — archived)

---

## 1. What OpsCore Is

OpsCore is a vertical agentic AI platform for Indian B2B back-office operations. It automates three high-friction workflows — document ingestion, vendor onboarding, and compliance monitoring — using deterministic Go code as the default execution path and Sarvam AI models only where genuine ambiguity exists.

OpsCore is not a general-purpose chatbot or a workflow visualiser. It is a production-grade operational system with typed inputs, typed outputs, human approval gates, a full audit trail, and zero always-on infrastructure.

---

## 2. What Changed from v2.0 (Why This Document Exists)

The v2.0 PRD described an OSS self-hosted stack. That stack was evaluated, partially built, and deliberately replaced. Every decision below is final.

| v2.0 (Archived) | v3.0 (Current) | Reason |
|---|---|---|
| Python (FastAPI, LangGraph) | **Go** | Single binary, no runtime, ADK Go 1.0 available |
| PostgreSQL + pgvector | **Azure Cosmos DB NoSQL** | Serverless, free tier, no container |
| Redis + ARQ/Celery | **Azure Queue Storage** | Always-free, no broker daemon |
| Temporal | **Azure Timer + Queue Triggers** | Zero containers, same durability |
| Qdrant | **Cosmos DB** (no vectors in v3.0) | Deferred until traversal value proven |
| Neo4j + Graphiti | **Removed** | Cosmos adjacency sufficient for v3.0 |
| Docling + RapidOCR | **Sarvam Document Intelligence** | India-native, job-based, structured output |
| OpenAI GPT-4o-mini | **Sarvam-M** | India-specific, free credits, JSON output |
| Langfuse | **Azure Application Insights** | 5 GB free, OTel native, zero hosting |
| Streamlit / React | **Slack Block Kit** (HITL only) | No frontend needed for v3.0 |
| Docker Compose (9 containers) | **docker-compose.test.yml** (3 emulators, test-only) | Local dev only, zero prod containers |
| JWT custom auth | **Azure Entra ID B2C** (deferred P3) | Not needed before demo |
| Crawl4AI | **Go net/http + RSS** | Plain HTTP, zero dependency |

---

## 3. Architecture

### 3.1 Runtime

**Language:** Go 1.22+  
**Agent runtime:** Google ADK Go v1.0  
**Deployment:** Azure Functions Consumption plan (custom handler)  
**Binary:** Single `handler` binary compiled `GOOS=linux GOARCH=amd64`  
**Local test host:** Azure Functions Core Tools v4 (`func start`)

The Go binary listens on `FUNCTIONS_CUSTOMHANDLER_PORT`. The Functions host routes HTTP triggers using `enableForwardingHttpRequest: true`, meaning routes in the Go HTTP mux map directly to `/api/*` paths as normal Go HTTP handlers. Queue and Timer triggers use the JSON envelope protocol.

### 3.2 Infrastructure (Azure Free Tier)

| Service | Purpose | SKU | Cost |
|---|---|---|---|
| Azure Functions | All triggers (HTTP, Queue, Timer) | Consumption Y1/Dynamic | Always free (1M req/month) |
| Azure Cosmos DB NoSQL | Jobs, vendors, documents, audit events, HITL requests, compliance chunks | Free tier | Always free (1000 RU/s, 25 GB) |
| Azure Blob Storage | PDF uploads, extracted artifacts | Standard LRS | Always free (5 GB, 12 months) |
| Azure Queue Storage | document-queue, vendor-queue, compliance-queue, dead-letter | Standard | Always free (100K ops/month) |
| Azure Application Insights | Traces, metrics, logs, alerts | Pay-as-you-go | Always free (5 GB/month) |
| Azure Static Web Apps | Landing page / future UI | Free tier | Always free |

**Production containers: zero.**  
**Monthly cost at demo scale: ₹0.**

### 3.3 Repository Layout

```
opscore/
├── cmd/
│   ├── functions/
│   │   └── main.go              # Single binary — all triggers registered here
│   └── migrate/
│       └── main.go              # One-off Cosmos container + index setup
│
├── internal/
│   ├── domain/                  # Pure Go, zero I/O — the system's source of truth
│   │   ├── trust_battery.go     # State machine: PROBATION→STANDARD→PREFERRED→STRATEGIC
│   │   ├── risk_scorer.go       # Vendor risk: GST/PAN/IFSC + flags + score 0-100
│   │   ├── document_classifier.go  # Keyword-based: INVOICE/CONTRACT/GST_NOTICE/PO/OTHER
│   │   ├── india_validator.go   # ValidateGST / ValidatePAN / ValidateIFSC (regex)
│   │   └── schemas.go           # Typed Go structs, json tags matching API field names
│   │
│   ├── agents/                  # ADK Go agents — LLM only for ambiguity
│   │   ├── document_agent.go
│   │   ├── vendor_agent.go
│   │   └── compliance_agent.go
│   │
│   ├── providers/               # Interfaces + adapters
│   │   ├── interfaces.go        # OCRProvider, LLMProvider, StorageProvider, QueueProvider, DBProvider, HITLProvider, TracingProvider
│   │   ├── sarvam/
│   │   │   ├── ocr.go           # Sarvam Document Intelligence — job-poll loop
│   │   │   └── llm.go           # Sarvam-M — always json_schema response format
│   │   ├── modal/
│   │   │   └── fallback.go      # Docling/Granite fallback — fires on confidence < 0.70
│   │   ├── azure/
│   │   │   ├── blob.go
│   │   │   ├── queue.go
│   │   │   └── cosmos.go
│   │   └── slack/
│   │       └── hitl.go          # Block Kit approval messages
│   │
│   ├── telemetry/
│   │   └── appinsights.go       # OTel → App Insights
│   │
│   └── functions/               # Azure Function handlers
│       ├── http_upload.go
│       ├── http_job_status.go
│       ├── http_health.go
│       ├── http_slack_webhook.go
│       ├── queue_document.go
│       ├── queue_vendor.go
│       ├── queue_compliance.go
│       ├── timer_scraper.go
│       └── timer_trust_decay.go
│
├── tests/
│   ├── domain/                  # Unit — no build tag, always runs
│   ├── adapters/                # Integration — build tag: integration
│   ├── agents/                  # Integration — build tag: integration
│   ├── e2e/                     # E2E — build tag: integration
│   └── fixtures/
│       ├── pdfs/                # invoice_english.pdf, invoice_hindi.pdf, etc.
│       └── expected/            # JSON expected outputs (parity gate)
│
├── infra/
│   ├── main.bicep               # Provisions all 5 Azure resources in one command
│   └── modules/
│
├── .github/workflows/
│   ├── ci.yml                   # lint + unit + integration (emulators as GH services)
│   ├── cd-staging.yml           # deploy on main push
│   └── cd-prod.yml              # deploy on tag v* with manual gate
│
├── host.json                    # enableForwardingHttpRequest: true
├── local.settings.json          # gitignored — points to Azurite + Cosmos emulator
├── docker-compose.test.yml      # Cosmos emulator + Azurite + Mockoon (test only)
├── Makefile
├── go.mod
└── .env.example
```

### 3.4 Trigger Map

| Trigger | Type | Route / Queue / Schedule | Handler |
|---|---|---|---|
| Upload document | HTTP POST | `/api/upload` | `http_upload.go` |
| Get job status | HTTP GET | `/api/jobs/{jobId}` | `http_job_status.go` |
| Health check | HTTP GET | `/api/health` | `http_health.go` |
| Slack HITL webhook | HTTP POST | `/api/slack/webhook` | `http_slack_webhook.go` |
| Process document | Queue | `document-queue` | `queue_document.go` |
| Process vendor | Queue | `vendor-queue` | `queue_vendor.go` |
| Process compliance | Queue | `compliance-queue` | `queue_compliance.go` |
| Scrape regulations | Timer | `0 0 9 * * 1` (Mon 9am) | `timer_scraper.go` |
| Trust battery decay | Timer | `0 0 2 * * *` (daily 2am) | `timer_trust_decay.go` |

---

## 4. LLM and OCR Strategy

### Primary path: Sarvam AI

- **Sarvam Document Intelligence** for OCR and layout extraction. Accepts PDF uploads via presigned URL, returns job with states: `Accepted → Pending → Running → Completed / PartiallyCompleted / Failed`. Structured output includes text, tables, key-value pairs, confidence score, and detected language.
- **Sarvam-M** for field extraction and compliance reasoning. Always called with `response_format: json_schema`. Input truncated to 3000 characters.
- **Free tier:** ₹1000 credits on signup, Sarvam-M currently free per token.

### Fallback path: Modal

- Fires when Sarvam OCR returns confidence < 0.70 or job status `Failed`.
- Modal hosts Docling + Granite or DeepSeek-VL2 3B.
- Returns same `OCRResult` struct as Sarvam adapter.
- `OCRResult.Provider` records which path was used.
- Modal free tier: $30/month compute.

### LLM use policy (non-negotiable)

**Use Sarvam-M only for:**
- Ambiguous document field extraction
- Compliance gap reasoning and citation generation
- Uncertain vendor document interpretation

**Never use LLM for:**
- Document type routing → `document_classifier.go` (keyword)
- GST/PAN/IFSC validation → `india_validator.go` (regex)
- Risk scoring → `risk_scorer.go` (deterministic formula)
- Trust tier transitions → `trust_battery.go` (state machine)
- Duplicate detection → Cosmos DB query
- Threshold decisions → constants in code

---

## 5. Workflows

### 5.1 Document Ingestion

```
User uploads PDF
  → HTTP POST /api/upload
  → Validate file type and size (Go, deterministic)
  → Write to Azure Blob Storage
  → Create job record in Cosmos DB (status: queued)
  → Enqueue to document-queue
  → Return {job_id, status: queued}

Queue trigger fires
  → OCR via Sarvam Document Intelligence (job-poll loop)
  → If confidence < 0.70 → Modal fallback
  → Classify document type (keyword-based, no LLM)
  → Validate Indian IDs if present (regex)
  → If high-value / low-confidence / ID mismatch → create HITL event
      → Send Slack Block Kit approval message
      → Update job status: pending_hitl
      → Wait for webhook callback
  → On approval → update status: completed, write extracted_data to Cosmos
  → Append audit events throughout
```

**HITL trigger conditions:**
- Invoice amount > ₹1,00,000
- OCR confidence < 0.85
- GST/PAN mismatch between document and vendor record
- Document type: CONTRACT or GST_NOTICE (always manual review)

### 5.2 Vendor Onboarding

```
User submits vendor form + documents
  → HTTP POST (structured form data)
  → Write job + documents to Cosmos DB
  → Enqueue to vendor-queue

Queue trigger fires
  → ValidateGST, ValidatePAN, ValidateIFSC (regex, no LLM)
  → Duplicate check: query Cosmos for existing GST number
  → Compute risk score (deterministic formula, 0-100)
  → Set initial trust tier: PROBATION
  → If score < 40 or duplicate GST → HITL approval required
  → On approval → write vendor record, set trust tier
  → Append audit events
```

### 5.3 Compliance Monitoring

```
Timer fires Monday 9am IST
  → Fetch SEBI / RBI / GST portal RSS feeds (Go net/http)
  → Compare against last-seen document hashes in Cosmos DB
  → New documents → enqueue to compliance-queue

Queue trigger fires
  → Download full document
  → Store raw file in Blob Storage
  → Extract text (Sarvam OCR if PDF, plain text if HTML)
  → Chunk text into ~500 token segments
  → Store chunks in Cosmos DB compliance_chunks container
  → Sarvam-M: compare chunks against tenant policy context
  → Generate gap report with citations (exact chunk references)
  → Classify severity: CRITICAL / HIGH / MEDIUM / LOW (keyword rules)
  → Write gap report to Cosmos DB
  → If CRITICAL or HIGH → send Slack notification
```

---

## 6. Data Model

### Cosmos DB Containers

All containers use `tenant_id` as partition key.

**jobs**
```
id, tenant_id, workflow_type, status, created_at, updated_at,
blob_url, document_type, confidence, extracted_data,
risk_flags, hitl_reason, trace_id, correlation_id
```

**vendors**
```
id, tenant_id, name, gst, pan, ifsc, trust_tier, risk_score,
risk_flags, status, created_at, updated_at, last_transaction_at
```

**documents**
```
id, tenant_id, job_id, blob_url, document_type,
extracted_data, confidence, provider, created_at
```

**compliance_chunks**
```
id, tenant_id, source_url, source_hash, content,
chunk_index, severity, created_at
```

**audit_events** (append-only, no updates ever)
```
id, tenant_id, actor, action, target_type, target_id,
old_state, new_state, timestamp, trace_id
```

**hitl_requests**
```
id, tenant_id, job_id, reason, status, sent_at,
responded_at, responder, decision, slack_ts
```

---

## 7. API Reference

### HTTP Triggers

**POST /api/upload**
```
Headers: X-Tenant-ID: string (required)
Body: multipart/form-data, field "file" (PDF, max 10MB)
Response 200: {"job_id": "string", "status": "queued"}
Response 400: {"error": "invalid file type / missing tenant"}
Response 413: {"error": "file too large"}
```

**GET /api/jobs/{jobId}**
```
Headers: X-Tenant-ID: string (required)
Response 200: {"job_id", "status", "workflow_type", "document_type",
               "confidence", "extracted_data", "risk_flags",
               "hitl_reason", "created_at", "updated_at"}
Response 404: {"error": "job not found"}
```

**GET /api/health**
```
Response 200: {"status": "healthy", "timestamp": "...",
               "services": {"blob": "healthy", "cosmos": "healthy", "queue": "healthy"}}
```

**POST /api/slack/webhook**
```
Headers: X-Slack-Signature, X-Slack-Request-Timestamp (verified in production)
Body: Slack interactive payload
Response 200: (empty — Slack requires 200 within 3 seconds)
Side effect: Updates job status in Cosmos DB, re-enqueues if approved
```

---

## 8. Domain Logic Specification

These modules are the authoritative source of truth. They have no I/O, no external calls, and are fully covered by table-driven tests. Any future change to business rules must start here.

### TrustBattery (internal/domain/trust_battery.go)

States: `PROBATION → STANDARD → PREFERRED → STRATEGIC`  
Blocked state: `BLOCKED` (terminal, no transitions out)

| Event | From | To |
|---|---|---|
| AllChecksPass | PROBATION | STANDARD |
| TransactionThresholdMet (3 txns) | STANDARD | PREFERRED |
| TransactionThresholdMet (10 txns) | PREFERRED | STRATEGIC |
| DisputeFiled | PREFERRED | STANDARD |
| DisputeFiled | STRATEGIC | PREFERRED |
| InactivityDecay (180 days) | STRATEGIC | PREFERRED |
| InactivityDecay (180 days) | PREFERRED | STANDARD |
| FraudFlagged | any | BLOCKED |

Score ranges: PROBATION 0-30, STANDARD 31-60, PREFERRED 61-85, STRATEGIC 86-100.

### RiskScorer (internal/domain/risk_scorer.go)

Base score: 50. Adjustments:
- Valid GST: +15
- Valid PAN: +10
- Valid IFSC: +10
- Duplicate GST in system: -40
- Active dispute flag: -20
- Incomplete documents: -15

Returns: `score int, flags []RiskFlag`.

### DocumentClassifier (internal/domain/document_classifier.go)

Keyword-based, case-insensitive. No LLM.
- INVOICE: ["invoice", "bill", "tax invoice", "gst invoice", "proforma"]
- CONTRACT: ["agreement", "contract", "mou", "memorandum", "terms and conditions"]
- GST_NOTICE: ["notice", "gst department", "demand", "scrutiny", "show cause"]
- PO: ["purchase order", "p.o.", "order no", "procurement order"]
- OTHER: fallback

### IndiaValidator (internal/domain/india_validator.go)

- GST: `^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z]{1}[1-9A-Z]{1}Z[0-9A-Z]{1}$`
- PAN: `^[A-Z]{5}[0-9]{4}[A-Z]{1}$`
- IFSC: `^[A-Z]{4}0[A-Z0-9]{6}$`

---

## 9. Provider Interfaces

All external dependencies implement these interfaces. Adapters are swappable. Tests use real implementations against emulators.

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

type TracingProvider interface {
    StartSpan(ctx context.Context, name string) (context.Context, Span)
    RecordLLMCall(ctx context.Context, model string, tokens int, costINR float64)
}
```

---

## 10. Test Strategy

### Build tags

- No tag: domain unit tests. Always run. No emulators needed.
- `integration`: adapter, agent, and E2E tests. Require `docker-compose.test.yml` up.

### Test pyramid

```
make test-unit          → internal/domain/... (fast, zero infra)
make test-integration   → tests/adapters/... (Cosmos emulator + Azurite + real Sarvam)
make test-agents        → tests/agents/... (ADK Go + real tools + emulators)
make test-e2e           → tests/e2e/... (full workflow, all infra, real Sarvam API)
```

### Local test infrastructure (docker-compose.test.yml)

| Service | Image | Purpose |
|---|---|---|
| cosmos | mcr.microsoft.com/cosmosdb/linux/azure-cosmos-emulator:vnext-preview | Cosmos DB NoSQL emulator |
| azurite | mcr.microsoft.com/azure-storage/azurite:latest | Blob + Queue emulator |
| mockoon | mockoon/cli:latest | Slack API mock (tests/mockoon/slack_mock.json) |

### func start (local.settings.json)

Azure Functions Core Tools v4 runs the exact same Functions host locally. All HTTP and trigger routing behaviour is identical to production. **No deploy should happen before `func start` + `make test-local` both pass.**

```
make dev        → docker compose up + go build + func start
make test-local → curl all HTTP endpoints against localhost:7071
make deploy     → only runs after test-local passes (Makefile dependency)
```

### Parity gate

After domain logic port: run Python E2E fixture inputs through Go domain modules and assert identical outputs. This gate must pass before Python codebase is archived.

---

## 11. Local Development Setup

```bash
# Prerequisites
go install                          # Go 1.22+
brew install azure-functions-core-tools@4   # func CLI
docker compose version              # Docker Compose v2

# First time
cp .env.example local.settings.json
# Fill in SARVAM_AI_API_KEY

# Daily loop
make dev          # starts emulators + builds + func start
make test-local   # smoke tests all HTTP endpoints
make test-unit    # domain logic tests

# Before every deploy
make test-local && make deploy
```

---

## 12. Deployment

### First deploy (one-time)

```bash
az login
az account set --subscription <subscription-id>
az group create --name opscore-prod --location centralindia
az deployment group create   --resource-group opscore-prod   --template-file infra/main.bicep   --parameters environment=prod
go run ./cmd/migrate   # creates Cosmos containers
```

### Subsequent deploys

```bash
GOOS=linux GOARCH=amd64 go build -o handler ./cmd/functions
func azure functionapp publish opscore-functions-prod
```

### CI/CD

- **CI** (every PR): `golangci-lint` → `go test ./internal/domain/...` → `go build ./...` → `az bicep build infra/main.bicep` → secret scan
- **CD staging** (merge to main): build → zip deploy → smoke test
- **CD prod** (tag `v*`): manual gate → deploy → smoke test

### Required GitHub Secrets

```
AZURE_CREDENTIALS          # az ad sp create-for-rbac output
SARVAM_API_KEY
MODAL_TOKEN
SLACK_BOT_TOKEN            # production only
SLACK_SIGNING_SECRET       # production only
SLACK_HITL_CHANNEL
```

---

## 13. Operational Rules

### Security

- All secrets in Azure Functions App Settings, never in code or committed files
- `local.settings.json` in `.gitignore`
- Slack signing secret verified in production (`APP_ENV=production`)
- Storage account key rotated if exposed in any log or terminal output

### Reliability

- Queue triggers: return HTTP 200 even on processing error (log error, do not retry poison messages automatically)
- HITL flow: idempotent — duplicate Slack webhook callbacks must not double-process
- Audit events: append-only, never update or delete
- All mutations: write audit event before returning success

### Cost control

- LLM called only for ambiguity (see Section 4 policy)
- Cosmos DB free tier: `enableFreeTier: true` in Bicep, never exceed 1000 RU/s
- Storage: Standard LRS only
- Functions: Consumption Y1/Dynamic only — never Premium or Dedicated

### Budget alert

```bash
az consumption budget create   --budget-name opscore-budget   --amount 5   --time-grain Monthly   --resource-group opscore-prod
```

---

## 14. Deferred (Not in v3.0)

These are explicitly out of scope until v3.0 is stable and deployed.

| Feature | Deferred To |
|---|---|
| Cosmos DB vector search for compliance chunks | v3.1 |
| Azure Entra ID B2C auth | v3.1 |
| Multi-tenant isolation beyond partition key | v3.1 |
| QuickBooks / Linear integrations | v3.2 |
| HTMX web UI | v3.2 |
| Neo4j / Graphiti vendor graph | Evaluate after v3.1 |

---

## 15. Coding Standards

### Go

- `golangci-lint` must pass with zero warnings — no exceptions
- No `interface{}` where a concrete type is available
- Every error must be handled — no `_` on errors
- All external calls must have context with timeout
- No hardcoded secrets, ports, or connection strings
- Build tags on all integration tests: `//go:build integration`

### Commits

```
feat: short description of what was added
fix: short description of what was fixed
chore: dependency update, config change, cleanup
test: new or updated tests only
docs: documentation only
refactor: no behaviour change
```

### Branch → deploy gate

```
feature branch → PR → CI green → merge to main → auto-deploy staging
                                                → manual approve → deploy prod
```

No deploy to prod without CI green and staging smoke test passing.
