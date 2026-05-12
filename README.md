# OpsCore — Autonomous Back-Office for Indian B2B

OpsCore is an autonomous back-office system for Indian B2B businesses. It ingests vendor documents (PDF invoices, onboarding forms, compliance notices) via API, validates Indian tax identifiers (GST, PAN, IFSC) using compiled Go regex, scores vendor risk using a deterministic formula, tracks vendor trust state through a five-state machine, and delivers a single structured Slack approval request. The owner clicks one button.

**Live demo:** https://opscore-functions-linux.azurewebsites.net/api/health

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
1. **Document Ingestion** — PDF → OCR → validation → Slack approval
2. **Vendor Onboarding** — form → risk scoring → Trust Battery → approval
3. **Compliance Monitoring** — RSS scraper → gap analysis → Slack alert

---

## Architecture

![OpsCore Workflow](opscore_workflow.png)

| Layer | Technology |
|-------|------------|
| Language | Go 1.22+ |
| Deployment | Azure Functions (Consumption Y1, zero idle cost) |
| Database | Azure Cosmos DB NoSQL (free tier) |
| Storage | Azure Blob Storage (Standard LRS) |
| Queue | Azure Queue Storage (always-free) |
| OCR | Sarvam Document Intelligence |
| LLM | Sarvam-M (used only when OCR confidence < 0.85) |
| HITL | Slack Block Kit |
| Observability | Azure Application Insights |

**₹0/month** at demo scale. All services within always-free tiers.

---

## Quick Start

```bash
# 1. Clone
git clone https://github.com/Aparnap2/opscore.git
cd opscore

# 2. Configure
cp .env.example local.settings.json
# Fill in: AzureWebJobsStorage, COSMOS_ENDPOINT, COSMOS_KEY, SARVAM_API_KEY

# 3. Start emulators (for local dev)
docker compose -f docker-compose.test.yml up -d

# 4. Build & run
go build -o handler ./cmd/functions
go run ./cmd/migrate   # create Cosmos containers
func start

# 5. Test
curl http://localhost:7071/api/health
```

---

## API Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /api/health` | Health check (no auth) |
| `POST /api/upload` | Upload PDF (multipart/form-data) |
| `GET /api/jobs/{id}` | Get job status |
| `POST /api/vendors` | Create vendor request |
| `GET /api/vendors/{id}` | Get vendor status |
| `POST /api/slack/webhook` | Slack callbacks |

All endpoints except `/health` require `x-functions-key` header.

---

## Deterministic Logic (No LLM)

| Decision | Implementation |
|----------|----------------|
| Document classification | `internal/domain/document_classifier.go` (keyword rules) |
| GST/PAN/IFSC validation | `internal/domain/india_validator.go` (regex) |
| Vendor risk scoring | `internal/domain/risk_scorer.go` (base 50 + adjustments) |
| Trust tier transitions | `internal/domain/trust_battery.go` (state machine) |

LLM is called only for: OCR confidence < 0.85 AND ambiguous fields.

---

## License

MIT