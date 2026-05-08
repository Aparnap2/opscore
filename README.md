# OpsCore v2.0 - Agentic Internal Operations Platform

A production-grade internal operations platform for mid-size B2B companies in India. Built with Go and Azure serverless, it automates document ingestion, compliance monitoring, and vendor onboarding with AI agents.

## Features

### Workflow 1: Document Ingestion
- PDF/Image upload via API
- Automatic document classification (Invoice, Contract, GST Notice, Purchase Order)
- Structured field extraction using Sarvam AI OCR + LLM
- Confidence scoring per field
- HITL (Human-In-The-Loop) review via Slack for low-confidence or high-value items

### Workflow 2: Compliance Monitoring
- Scheduled regulatory scraper (SEBI, RBI, GST)
- Semantic chunking and vector storage (Cosmos DB)
- Gap analysis with hybrid search
- Department owner assignment
- Slack notifications

### Workflow 3: Vendor Onboarding
- Vendor request form via API or Slack
- Document verification (GST, PAN, Bank details)
- Deterministic risk scoring
- Tiered approval workflow (Slack)
- Trust Battery state machine

## Tech Stack

| Component | Technology |
|-----------|------------|
| Functions | Go + Azure Functions Custom Handler |
| Database | Azure Cosmos DB (SQL API) |
| Storage | Azure Blob Storage |
| Queue | Azure Queue Storage |
| OCR/LLM | Sarvam AI |
| HITL UI | Slack Block Kit |
| Observability | Azure App Insights |
| IaC | Bicep |

## Architecture

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Slack     │────│  Azure      │────│   Cosmos DB │
│  (HITL UI)  │     │  Functions  │     │  (SQL API)  │
└─────────────┘     └──────┬──────┘     └─────────────┘
                           │
                    ┌────▼────┐
                    │  Queue  │
                    │ Storage │
                    └────┬────┘
                         │
          ┌──────────────┼──────────────┐
          ▼              ▼              ▼
    ┌──────────┐  ┌──────────┐  ┌──────────┐
    │ Document │  │Compliance│  │ Vendor  │
    │ Ingestion│  │ Monitoring│  │Onboarding│
    └──────────┘  └──────────┘  └──────────┘
```

## Azure Deployment

### Prerequisites
- Azure subscription
- Azure CLI installed
- Go 1.21+

### Infrastructure Setup
```bash
cd infra
az group create --location centralindia --name opscore-prod
az deployment group create --resource-group opscore-prod --template-file main.bicep
```

### Deploy Functions
```bash
# Build Linux binary
GOOS=linux GOARCH=amd64 go build -o handler ./cmd/functions

# Deploy (creates or updates function app)
func azure functionapp publish opscore-functions-linux --resource-group opscore-prod
```

## Environment Variables

| Variable | Description | Required |
|----------|-------------|----------|
| `AzureWebJobsStorage` | Azure Storage connection string | Yes |
| `COSMOS_ENDPOINT` | Cosmos DB endpoint URL | Yes |
| `COSMOS_KEY` | Cosmos DB master key | Yes |
| `COSMOS_DATABASE` | Database name (default: opscore) | No |
| `SLACK_SIGNING_SECRET` | Slack signing secret | No |
| `SLACK_BOT_TOKEN` | Slack bot token for HITL | No |
| `APPINSIGHTS_INSTRUMENTATIONKEY` | App Insights key | No |

## API Endpoints

All endpoints (except /health) require `x-functions-key` header.

### Health
- `GET /api/health` - Basic health check (anonymous)

### Documents
- `POST /api/upload` - Upload PDF/Image
- `GET /api/jobs/{id}` - Job status

### Vendors
- `POST /api/vendors` - Create vendor request
- `GET /api/vendors/{id}` - Vendor status

### Slack
- `POST /api/slack/webhook` - Slack events & interactive callbacks

## Project Structure

```
opscore/
├── cmd/functions/           # Azure Functions entrypoint
│   ├── main.go             # Custom handler server
│   └── handlers/           # HTTP, Queue, Timer handlers
├── internal/
│   ├── domain/             # Business entities & validation
│   ├── adapters/           # Azure, Sarvam, Slack integrations
│   ├── agents/             # Document, Vendor, Compliance agents
│   └── providers/          # Interface abstractions
├── tests/                   # Unit & E2E tests
├── infra/                   # Bicep infrastructure
├── HttpHealth/             # Function definition
├── HttpUpload/             # Function definition
├── HttpJobStatus/         # Function definition
├── HttpSlackWebhook/      # Function definition
├── QueueDocument/         # Function definition
├── QueueVendor/           # Function definition
├── QueueCompliance/       # Function definition
├── TimerScraper/          # Function definition
└── TimerTrustDecay/       # Function definition
```

## Testing

```bash
# Run unit tests
go test ./...

# Run E2E tests (requires Azure credentials)
go test -tags=e2e ./tests/...

# Local function emulation
func start
```

## License

MIT License