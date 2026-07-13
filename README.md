# OpsCore

Every B2B business owner starts as a craftsperson — a trader, a service expert. They built their business around what they know how to do. But at five employees, the back-office collapses. Invoice processing, vendor ID verification, GST compliance, duplicate detection — these are not skilled tasks, yet they consume skilled people. The owner does them because no one else can be trusted to do them consistently.

OpsCore replaces the manual back-office with a system that produces identical results every time, with no human in the loop except for the final approval decision. Classification, validation, scoring, and audit-trail logic is all compiled Go code — no LLM unless absolutely necessary.

**Loom walkthrough:** *(coming soon)*

---

## Philosophy

> **"Default to compiled logic. Use LLM as a last resort."**

All validation, classification, scoring, and state-machine logic is deterministic Go code. A regex pattern that validates a GST number never hallucinates. A state machine that transitions a vendor from PROBATION to STANDARD never drifts. A risk formula that subtracts 40 points for a duplicate GST number never changes its mind between runs.

The LLM (Sarvam-M) is called only for two things:
1. **OCR confidence fallback** — field extraction when OCR confidence drops below 85%
2. **Compliance gap reasoning** — natural language understanding of regulatory circulars

Everything else is Go.

---

## How It Works

```
You upload a PDF invoice
  → Go validates GST/PAN/IFSC with regex (no LLM)
  → Deterministic risk score computed
  → If amount > ₹1L or low confidence → Slack approval request
  → Owner clicks [Approve] → done
```

**Three workflows:**

1. **Document Ingestion** — Upload a PDF invoice. Go validates tax identifiers with compiled regex, extracts structured data via Sarvam OCR, classifies document type by keyword rules, and routes high-value or low-confidence items through a Slack Block Kit approval flow.

2. **Vendor Onboarding** — Submit vendor details (GST, PAN, IFSC). Go validates every field with regex, computes a deterministic risk score (0–100), assigns a trust tier (PROBATION → STANDARD → PREFERRED → STRATEGIC), and triggers HITL approval for risky or duplicate entries.

3. **Compliance Monitoring** — A weekly timer scrapes SEBI/RBI/GST portal RSS feeds. New circulars are chunked and analyzed against tenant policy rules via Sarvam-M. Critical and high-severity gaps trigger Slack alerts. No human needed for routine monitoring.

---

## Architecture

![OpsCore Workflow](opscore_workflow.png)

| Layer | Technology |
|-------|------------|
| Language | Go 1.25 |
| API | REST (standard `net/http`, no framework) |
| Database | PostgreSQL 16 |
| Storage | MinIO / S3-compatible |
| Queue | Redis 7 |
| OCR | Sarvam Document Intelligence |
| LLM | Sarvam-M (used only when OCR confidence < 0.85) |
| HITL | Slack Block Kit |
| LLMObs | Langfuse Cloud — traces, latency, prompt/version tracking, cost analytics |
| Admin UI | Python/Streamlit (REST-only, no direct DB access) |
| Deployment | Docker → Google Cloud Run + Supabase |

### Design Pattern: Hexagonal (Ports & Adapters)

```
cmd/server/main.go          # HTTP server, worker, route registration
internal/domain/            # Pure Go business logic (zero I/O)
internal/providers/         # Interface definitions (OCR, LLM, DB, Storage, Queue, HITL, Tracing)
internal/adapters/          # Implementations (Postgres, MinIO, Redis, Sarvam)
internal/agents/            # Workflow orchestrators (document, vendor, compliance)
internal/telemetry/         # Langfuse tracing, metrics, no-op fallback
internal/middleware/        # HTTP middleware
│   ├── auth/               # API-key auth & RBAC (Owner, OpsAdmin, Reviewer)
│   ├── tenant/             # Tenant context extraction
│   ├── ratelimit/          # Rate limiter
│   └── usage/              # Plan limit enforcement
ops-ui/                     # Streamlit admin dashboard
```

The system follows a hexagonal (ports & adapters) architecture. Every external dependency — database, storage, queue, OCR service, LLM, HITL provider — is defined as a Go interface in `internal/providers/` and implemented in `internal/adapters/`. The domain layer has zero I/O imports. Adapters are swappable via constructor injection.

### Deterministic Logic (No LLM)

| Decision | Implementation |
|----------|----------------|
| Document classification | `internal/domain/document_classifier.go` (keyword rules) |
| GST/PAN/IFSC validation | `internal/domain/india_validator.go` (compiled Go regex) |
| Vendor risk scoring | `internal/domain/risk_scorer.go` (base 50 + adjustments) |
| Trust tier transitions | `internal/domain/trust_battery.go` (state machine) |

Architecture decisions are recorded in `docs/architecture/adr/` (20 ADRs covering the full system).

---

## REST API

All endpoints except `/health` require the `X-Tenant-ID` header.

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/health` | Health check with per-service status |
| GET | `/status/summary` | Job counts by status |
| POST | `/upload` | Upload PDF/IMG (multipart/form-data) |
| GET | `/jobs/recent` | Recent jobs (query: `?limit=N`) |
| GET | `/jobs/{id}` | Job detail |
| GET | `/jobs/{id}/audit` | Audit trail for a job |
| POST | `/vendors` | Create vendor |
| GET | `/vendors` | List all vendors |
| GET | `/vendors/{id}` | Vendor detail |
| GET | `/vendors/risky` | High-risk vendors |
| GET | `/compliance/recent` | Recent compliance records |
| POST | `/compliance/run` | Trigger compliance check |
| GET | `/metrics/llm-summary` | LLM call metrics (count, latency, cost) |
| GET | `/metrics/workflow-summary` | Workflow processing metrics |
| POST | `/slack/webhook` | Slack interactive callbacks |
| GET | `/admin/review-queue` | List pending HITL requests (query: `?status=&tenant_id=&limit=&offset=`) |
| GET | `/admin/review-queue/{id}` | Get single HITL request with audit events |
| POST | `/admin/review-queue` | Approve/reject a HITL request |

---

## Quick Start

```bash
# 1. Clone the repository
git clone https://github.com/Aparnap2/opscore.git
cd opscore

# 2. Configure environment
cp .env.example .env
# Edit .env as needed (defaults work for local dev)

# 3. Start infrastructure (Postgres + MinIO + Redis)
make local-up
# Launches: Postgres 16 on :5432, MinIO on :9000/:9001, Redis 7 on :6380

# 4. Run the API server (terminal 1)
make run-api
# Starts Go HTTP server on :8080

# 5. Run the admin UI (terminal 2)
make run-ui
# Starts Streamlit dashboard on :8501

# 6. Verify everything is running
curl http://localhost:8080/health
curl http://localhost:8080/status/summary
```

### Local Validation Checklist

```bash
# Backend smoke tests
curl http://localhost:8080/health
curl http://localhost:8080/status/summary
curl http://localhost:8080/jobs/recent

# Upload a sample document
curl -X POST http://localhost:8080/upload \
  -H "X-Tenant-ID: default" \
  -F "file=@test_data/test_invoice.txt"

# Check job appeared
curl http://localhost:8080/jobs/recent

# Run unit tests
make test-unit

# Run integration tests (requires Docker)
make test-integration
```

---

## Project Structure

```
opscore/
├── cmd/
│   ├── server/main.go          # HTTP server entry point
│   ├── server/worker.go        # Queue consumer (Redis Pub/Sub)
│   └── migrate/                # Database migration tool
│
├── internal/
│   ├── domain/                 # Pure Go business logic (zero I/O)
│   │   ├── trust_battery.go    # State machine: PROBATION→STANDARD→PREFERRED→STRATEGIC→BLOCKED
│   │   ├── risk_scorer.go      # Deterministic risk score (0–100)
│   │   ├── document_classifier.go  # Keyword classification
│   │   ├── india_validator.go  # GST / PAN / IFSC regex validation
│   │   ├── chunker.go          # Text chunking for compliance analysis
│   │   ├── upload.go           # Upload validation & storage logic
│   │   ├── batch_job.go        # Batch processing support
│   │   └── types.go            # Shared domain types
│   │
│   ├── agents/                 # Workflow orchestrators
│   │   ├── document_agent.go   # Document ingestion workflow
│   │   ├── vendor_agent.go     # Vendor onboarding workflow
│   │   └── compliance_agent.go # Compliance monitoring workflow
│   │
│   ├── providers/              # Interface definitions
│   │   ├── interfaces.go       # OCR, LLM, Storage, Queue, DB, HITL providers
│   │   └── slack_hitl.go       # Slack HITL types
│   │
│   ├── adapters/               # Interface implementations
│   │   ├── postgres/           # PostgreSQL adapter
│   │   ├── minio/              # MinIO/S3 storage adapter
│   │   ├── queue/              # Redis Pub/Sub queue adapter
│   │   ├── sarvam/             # Sarvam OCR + LLM adapter
│   │   └── slack/              # Slack API adapter
│   │
│   ├── telemetry/              # Observability
│   │   ├── langfuse.go         # Langfuse Cloud tracing
│   │   ├── metrics.go          # LLM & workflow metrics
│   │   └── noop.go             # No-op tracer fallback
│   │
│   ├── logging/                # Structured JSON logging
│   │   └── logger.go           # log/slog JSON handler configuration
│   │
│   └── middleware/             # HTTP middleware
│       ├── auth/               # API-key auth & RBAC (Owner, OpsAdmin, Reviewer)
│       ├── tenant/             # Tenant context extraction
│       ├── ratelimit/          # Rate limiter
│       └── usage/              # Plan limit enforcement
│
├── ops-ui/                     # Streamlit admin dashboard
│   ├── app.py                  # Dashboard entry point (REST-only)
│   ├── config.py               # UI configuration
│   └── requirements.txt        # Python dependencies
│
├── tests/                      # Test suites
│   ├── unit/                   # Pure unit tests (no infra)
│   ├── domain/                 # Domain-layer tests
│   ├── integration/            # Docker-backed integration tests
│   │   ├── handler_test.go     # HTTP handler tests with mocks
│   │   ├── handler_review_queue_test.go  # Review queue endpoint tests
│   │   ├── e2e_test.go         # Full pipeline E2E tests
│   │   └── hitl_e2e_test.go    # Slack HITL flow tests
│   ├── agentic/                # Agent workflow tests
│   │   ├── vendor_workflow_test.go  # Vendor onboarding tests
│   │   ├── document_workflow_test.go # Document ingestion tests
│   │   ├── error_recovery_test.go    # Failure/recovery tests
│   │   ├── state_recovery_test.go    # State machine recovery tests
│   │   ├── pipeline_test.go          # Full pipeline tests
│   │   ├── review_queue_admin_test.go # Admin queue tests
│   │   ├── golden/                   # Golden trajectory tests
│   │   ├── contracts/               # Provider contract tests
│   │   ├── decisions/               # Decision logic tests
│   │   ├── states/                  # State machine tests
│   │   └── latency/                 # Latency tests
│   ├── live/                   # Live provider tests (opt-in, //go:build live)
│   ├── load/                   # Load tests (k6)
│   ├── golden/                 # Golden file / snapshot tests
│   ├── security/               # Security-focused tests
│   ├── ragas_eval/             # LLM output evaluation (RAGAS)
│   ├── mockoon/                # Slack API mock configurations
│   └── fixtures/               # Test PDFs and expected outputs

├── docs/
│   ├── architecture/
│   │   ├── ADR-001-retrieval-architecture.md
│   │   └── adr/
│   │       ├── 0002-vendor-onboarding-realtime-nw.md
│   │       ├── 0003-multi-tenancy-tenant-isolation.md
│   │       └── ... (19 ADR files total)

├── infra/                      # Infrastructure as Code
│   ├── main.bicep              # Azure Bicep (legacy) / GCP config
│   └── init-db.sh              # Tenant seed data
│
├── config/                     # Application configuration
├── scripts/                    # Utility scripts
├── mocks/                      # Generated test mocks
├── libs/                       # Shared libraries
│
├── docker-compose.yml          # Full local stack (incl. app)
├── docker-compose.local.yml    # Lightweight infra only (Postgres + MinIO + Redis)
├── Dockerfile                  # Go production build
├── Makefile                    # Dev workflows
└── .github/workflows/          # CI/CD pipelines
```

---

## Testing

| Layer | Command | Dependencies |
|-------|---------|-------------|
| Unit | `make test-unit` | None (pure Go, <5s) |
| Integration | `make test-integration` | Docker (Postgres, MinIO, Redis) |
| All | `make test-all` | Docker |

**Test strategy:**
- **Unit tests** (`make test-unit`) — cover `internal/domain/` with no external dependencies. Always runs in CI. No build tags needed.
- **Integration tests** (`make test-integration`) — require Docker containers for Postgres, MinIO, and Redis. Tagged with `//go:build integration`. Test adapters against real (containerized) infrastructure — no mocks.
- **Golden file tests** — expected output JSON in `tests/golden/` for deterministic regression checks.
- **E2E workflows** — full pipeline tests in `tests/agentic/` exercise document ingestion, vendor onboarding, and compliance monitoring end-to-end against containerized infra.

---

## Observability

OpsCore ships with three observability layers, designed to be informative without requiring a dedicated observability stack.

### 1. Structured JSON Logging

All services emit structured key-value pairs via Go's `log/slog` with the JSON handler:

```json
{"time":"2026-07-08T10:30:00Z","level":"INFO","msg":"document processed",
 "job_id":"abc123","tenant_id":"tenant-1","workflow":"ingestion","duration_ms":234}
```

No bare `fmt.Println` or `log.Printf` — every log line is machine-parseable and searchable.

### 2. Langfuse Cloud Traces

Every OCR and LLM call is traced through Langfuse Cloud with:
- **Latency** — per-call duration
- **Token count** — input/output token usage
- **Cost** — estimated cost per call
- **Prompt version** — which prompt template was used
- **Trace ID** — correlated across the full workflow for end-to-end debugging

The telemetry layer includes a no-op fallback: when Langfuse is not configured, tracing silently downgrades to no-ops — no crashes, no panics.

### 3. Metrics Endpoints

| Endpoint | What It Exposes |
|----------|----------------|
| `/health` | Per-service health (Postgres, MinIO, Redis, Sarvam) |
| `/status/summary` | Operational overview — job counts by status |
| `/metrics/llm-summary` | LLM call volume, average latency, token usage, estimated cost |
| `/metrics/workflow-summary` | Workflow processing counts, success/failure rates, throughput |

This lightweight approach avoids the operational overhead of Prometheus/Grafana while providing sufficient insight for a single-tenant or small-scale deployment.

---

## License

MIT
