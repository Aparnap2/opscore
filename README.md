# OpsCore - Agentic Internal Operations Platform

A production-grade internal operations platform for mid-size B2B companies in India. Automates document ingestion, compliance monitoring, and vendor onboarding with AI agents.

## Features

### Workflow 1: Document Ingestion
- PDF upload via drag-drop UI or API
- Automatic document classification (Invoice, Contract, GST Notice, Purchase Order)
- Structured field extraction using Docling + LLM
- Confidence scoring per field
- HITL (Human-In-The-Loop) review for low-confidence or high-value items
- Auto-sync to QuickBooks

### Workflow 2: Compliance Monitoring
- Scheduled regulatory scraper (SEBI, RBI, GST)
- Semantic chunking and vector storage (pgvector)
- Gap analysis with hybrid search
- RAGAS faithfulness scoring (≥0.85)
- Department owner assignment
- Slack notifications

### Workflow 3: Vendor Onboarding
- Vendor request form
- Document verification (GST, PAN, Bank details)
- Deterministic risk scoring
- Tiered approval workflow (Slack)
- Trust Battery state machine
- Auto-sync to QuickBooks

## Tech Stack

| Component | Technology |
|-----------|------------|
| API | FastAPI + Python 3.12 |
| Database | PostgreSQL + pgvector |
| Task Queue | ARQ + Redis |
| Knowledge Graph | Neo4j + Graphiti |
| LLM | Ollama / OpenAI / Claude |
| Agents | LangGraph |
| OCR | Docling |

## Architecture

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   React UI  │────│  FastAPI   │────│   PostgreSQL │
│   Slack     │     │   (Async)  │     │  + pgvector │
└─────────────┘     └──────┬──────┘     └─────────────┘
                          │
                   ┌────▼────┐
                   │   ARQ   │
                   │  Worker │
                   └────┬────┘
                        │
         ┌──────────────┼──────────────┐
         ▼              ▼              ▼
   ┌──────────┐  ┌──────────┐  ┌──────────┐
   │ Document │  │Compliance│  │ Vendor  │
   │ Ingestion│  │ Monitoring│  │Onboarding│
   └──────────┘  └──────────┘  └──────────┘
```

## Quick Start

### Prerequisites
- Docker + Docker Compose
- Python 3.12+
- uv (recommended)

### 1. Clone and Setup
```bash
git clone https://github.com/Aparnap2/opscore.git
cd opscore
cp .env.example .env
```

### 2. Start Infrastructure
```bash
docker compose up -d postgres redis neo4j
```

### 3. Install Dependencies
```bash
uv pip install -r requirements.txt
```

### 4. Run Migrations
```bash
docker exec opscore-postgres psql -U admin -d opscore -f /path/to/migrations/001_add_pgvector.sql
```

### 5. Start API
```bash
uvicorn apps.api.main:app --port 8000
```

### 6. Start Worker (separate terminal)
```bash
arq apps.api.worker.WorkerSettings
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection | postgresql://admin:password@localhost:5434/opscore |
| `REDIS_URL` | Redis connection | redis://localhost:6380/0 |
| `NEO4J_URI` | Neo4j bolt URI | bolt://localhost:7688 |
| `JWT_SECRET` | JWT signing secret | change-me-in-production |
| `LLM_API_KEY` | OpenAI/Claude API key | - |
| `OLLAMA_BASE_URL` | Ollama endpoint | https://ollama.com |

## API Endpoints

### Health
- `GET /health` - Basic health check
- `GET /health/live` - Liveness probe
- `GET /health/ready` - Readiness probe

### Auth
- `POST /api/v1/auth/register` - Register tenant + admin
- `POST /api/v1/auth/login` - Get access token
- `GET /api/v1/auth/me` - Current user info

### Documents
- `POST /api/v1/documents/ingest` - Upload PDF
- `GET /api/v1/documents/jobs/{id}/status` - Job status
- `GET /api/v1/documents/extracted/{id}` - Extracted data

### Vendors
- `POST /api/v1/vendors/onboard` - Create vendor request
- `GET /api/v1/vendors` - List vendors

### Compliance
- `GET /api/v1/compliance/regulations` - List regulations
- `POST /api/v1/compliance/scrape` - Trigger scrape
- `GET /api/v1/compliance/gaps` - List gaps

### HITL
- `GET /api/v1/hitl/queue` - Pending items
- `POST /api/v1/hitl/queue/{id}/approve` - Approve
- `POST /api/v1/hitl/queue/{id}/reject` - Reject

## Testing

```bash
# Run all tests
pytest tests/

# Run specific test
pytest tests/e2e/test_full_integration.py -v
```

## Project Structure

```
opscore/
├── apps/
│   └── api/
│       ├── api/v1/routes/    # API endpoints
│       ├── agents/           # LangGraph agents
│       ├── services/        # Business logic
│       ├── workflows/       # ARQ tasks
│       ├── integrations/     # Slack, QuickBooks
│       ├── db/             # SQLAlchemy models
│       └── observability/   # Langfuse
├── tests/
│   ├── e2e/               # End-to-end tests
│   └── unit/               # Unit tests
├── config/                 # Configuration
└── docker-compose.yml      # Infrastructure
```

## License

MIT License
