# ADR-0015: Deployment Target and Cloud-Agnostic Runtime

## Status

**Accepted** — Docker Compose configuration and Cloud Run deployment model are in active use. Multi-stage Dockerfile, docker-compose.yml with PostgreSQL 16, Redis 7, and MinIO are implemented.

## Date

2026-07-13

## Context

### Problem Statement

OpsCore was originally designed with a specific Azure-bound deployment in mind. As the architecture evolved toward cloud-agnostic design (enforced by the hexagonal architecture pattern), the deployment story was left implicit:

1. **No explicit deployment target.** The `cmd/server/main.go` starts an HTTP server, but the deployment model (bare metal, VM, container, serverless) was never decided.
2. **Cloud-specific SDKs in domain.** During early development, some Azure SDK types leaked into domain types. These have since been removed, but the deployment model was never updated.
3. **Docker Compose exists but is undocumented.** The `docker-compose.yml` file starts PostgreSQL, Redis, and MinIO, but the service architecture, healthchecks, and dependency ordering are not documented as architectural decisions.
4. **No production deployment path defined.** There is no documented path from `docker-compose up` to a production deployment on any cloud provider.

### Current Infrastructure

| Component | Local Dev | Production Target |
|-----------|-----------|-------------------|
| **Application** | `go run ./cmd/server` | Containerized Go binary |
| **Database** | PostgreSQL 16 via docker-compose | Managed PostgreSQL (Cloud SQL / Supabase) |
| **Cache/Queue** | Redis 7 via docker-compose | Managed Redis (Memorystore / Upstash) |
| **Object Storage** | MinIO via docker-compose | S3-compatible (GCS / AWS S3) |
| **LLM/OCR** | Sarvam AI API, OpenRouter API | Same (no self-hosted models) |
| **Secrets** | `.env` file | Cloud Secret Manager |

### Requirements

1. Cloud-agnostic runtime — no cloud-specific SDKs in domain or agent code.
2. Docker Compose for local development, with all dependencies containerized.
3. Clear production deployment path to Google Cloud Run (with documented alternatives).
4. Multi-stage Dockerfile for minimal production image size.
5. Healthchecks for all services.
6. Configuration via environment variables, not files.

## Decision

### Primary Decision: Docker Compose for Dev + Google Cloud Run for Production

**Local development** uses Docker Compose with three backing services plus the Go application built and run natively:

```
┌─────────────────────────────────────────────────────────┐
│                    docker-compose.yml                     │
│                                                          │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐               │
│  │ PostgreSQL│  │  Redis 7 │  │  MinIO   │               │
│  │   :5432   │  │  :6379   │  │  :9000   │               │
│  └──────────┘  └──────────┘  └──────────┘               │
│                                                          │
│  Application runs natively on host:                      │
│  $ go run ./cmd/server                                   │
│                                                          │
│  (or in container for CI):                               │
│  $ docker compose run --build server                     │
└─────────────────────────────────────────────────────────┘
```

**Production** targets Google Cloud Run (or equivalent), with managed backing services:

```
┌──────────────────────────────────────────────┐
│           Google Cloud Run                    │
│  ┌────────────────────────────────────┐       │
│  │  opscore-server (container)        │       │
│  │  PORT=8080, GIN_MODE=release       │       │
│  │  Auto-scaling: 0-10 instances      │       │
│  │  CPU: 1 vCPU, Memory: 512Mi        │       │
│  └────────────────────────────────────┘       │
│                                              │
│  Backing Services (managed):                 │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  │
│  │ Cloud SQL │  │ Memory-  │  │  GCS     │  │
│  │ PG 16     │  │ store    │  │ (S3-api) │  │
│  └──────────┘  └──────────┘  └──────────┘  │
└──────────────────────────────────────────────┘
```

### Multi-Stage Dockerfile

```dockerfile
# Stage 1: Build
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/server ./cmd/server

# Stage 2: Runtime
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /app/server /server
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1
ENTRYPOINT ["/server"]
```

### Docker Compose Service Layout

```yaml
version: "3.9"
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: opscore
      POSTGRES_USER: opscore
      POSTGRES_PASSWORD: opscore-dev
    ports:
      - "5432:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U opscore"]
      interval: 5s
      timeout: 5s
      retries: 5
    volumes:
      - pgdata:/var/lib/postgresql/data

  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 5

  minio:
    image: minio/minio:latest
    ports:
      - "9000:9000"
      - "9001:9001"
    environment:
      MINIO_ROOT_USER: opscore
      MINIO_ROOT_PASSWORD: opscore-dev
    command: server /data --console-address ":9001"
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:9000/minio/health/live"]
      interval: 10s
      timeout: 5s
      retries: 5
    volumes:
      - minio_data:/data

volumes:
  pgdata:
  minio_data:
```

### Environment Variable Configuration

All configuration is via environment variables, loaded at startup in `cmd/server/main.go`:

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP server port |
| `DATABASE_URL` | `postgres://opscore:opscore-dev@localhost:5432/opscore` | PostgreSQL connection string |
| `REDIS_URL` | `redis://localhost:6379/0` | Redis connection URL |
| `S3_ENDPOINT` | `http://localhost:9000` | S3-compatible endpoint (MinIO in dev, GCS in prod) |
| `S3_ACCESS_KEY` | `opscore` | S3 access key |
| `S3_SECRET_KEY` | `opscore-dev` | S3 secret key |
| `S3_BUCKET` | `opscore-docs` | Document storage bucket |
| `LLM_ENABLED` | `true` (dev) / `false` (prod) | Global LLM toggle |
| `OPENROUTER_API_KEY` | — | OpenRouter API key |
| `SARVAM_API_KEY` | — | Sarvam AI API key |
| `SLACK_BOT_TOKEN` | — | Slack bot token for HITL |
| `SLACK_CHANNEL_ID` | — | Default Slack channel for HITL |

### Cloud Run Migration Path

1. Build and push container to Artifact Registry:
   ```bash
   docker build -t gcr.io/opscore-prod/server:latest .
   docker push gcr.io/opscore-prod/server:latest
   ```

2. Deploy to Cloud Run:
   ```bash
   gcloud run deploy opscore-server \
     --image gcr.io/opscore-prod/server:latest \
     --platform managed \
     --region asia-south1 \
     --min-instances 0 \
     --max-instances 10 \
     --concurrency 80 \
     --cpu 1 \
     --memory 512Mi \
     --timeout 300 \
     --set-env-vars "LLM_ENABLED=false,DATABASE_URL=..." \
     --set-secrets "OPENROUTER_API_KEY=openrouter-key:1"
   ```

3. Alternative targets (documented but not primary):
   - **AWS ECS Fargate** — same container, different env vars
   - **Azure Container Apps** — same container, different env vars
   - **Self-hosted Docker Swarm / Nomad** — same container, no cloud dependency

### Cloud-Agnostic Principle

No cloud-specific SDK is imported in `internal/domain/`, `internal/agents/`, or `internal/providers/`. Cloud-specific implementations exist only in `internal/adapters/` and are behind provider interfaces:

| Interface | Cloud Adapter | Cloud-Free Alternative |
|-----------|---------------|----------------------|
| `ObjectStorage` | GCS adapter (`internal/adapters/gcs/`) | MinIO adapter (`internal/adapters/minio/`) |
| `QueueProvider` | GCP PubSub adapter (`internal/adapters/pubsub/`) | Redis adapter (`internal/adapters/queue/redis.go`) |
| `SecretStore` | GCP Secret Manager adapter | `.env` file loader |

The application is compiled once and deployed anywhere. The adapter implementation is chosen at startup based on environment variable configuration.

## Alternatives Considered

### 1. All-in Cloud (Google Cloud Run + All Managed Services)

Accepted as the target, but with the caveat that:
- The application must not depend on managed service features that have no cloud-agnostic equivalent (e.g., Cloud Run's request-response scaling model is compatible with any HTTP server).
- All adapters have a local Docker Compose equivalent for development.

### 2. Kubernetes (GKE / EKS)

Rejected because:
- Overkill for the current scale (single-digit microservices).
- OpsCore does not need pod autoscaling, service mesh, or persistent volume management beyond what Cloud Run provides.
- Kubernetes complexity (ingress, RBAC, monitoring) adds operational overhead without benefit.
- Cloud Run's 0-to-N autoscaling is sufficient for the expected workload pattern (bursty batch processing).

### 3. Bare Metal / VM

Rejected because:
- Manual deployment, scaling, and recovery require significant operational investment.
- No built-in healthcheck-based auto-recovery.
- SSL termination, load balancing, and secrets management must be handled manually.

### 4. Serverless (Cloud Run Functions / Lambda)

Partially accepted — the application runs on Cloud Run, which is serverless. Pure FaaS (Cloud Functions) was rejected because:
- Long-running worker goroutines (Redis queue polling) do not fit the request-response model of FaaS.
- HITL webhook callbacks (Slack) need a persistent HTTP server.
- The application has multiple concerns (HTTP API, queue worker, healthchecks) that fit a single binary better than multiple functions.

## Consequences

### Benefits

1. **Cloud-agnostic core.** The application can run anywhere — Cloud Run, ECS, bare metal — with different adapter configurations. No cloud lock-in.

2. **Single deployment artifact.** The multi-stage Dockerfile produces a minimal (~15MB) Alpine-based image. Same artifact for dev, staging, and production.

3. **Zero-config local development.** `docker compose up` starts all dependencies. The application runs natively with hot-reload via `go run`.

4. **Managed services for production.** Cloud SQL handles backups, replication, and failover. Memorystore handles Redis clustering. GCS handles object durability and lifecycle policies.

5. **Cost-efficient scaling.** Cloud Run scales to zero when idle. For a batch-processing workload that runs primarily during business hours, this is significantly cheaper than a always-on VM or Kubernetes cluster.

6. **Healthchecks in every environment.** The Dockerfile HEALTHCHECK ensures the container is responsive. Docker Compose healthchecks ensure dependencies are ready before the application starts.

### Trade-offs / Risks

1. **Cloud Run cold starts.** If the service scales to zero, the first request after idle time may take 2-5 seconds for container startup. Mitigation: set `min-instances=1` for production, or use Cloud Run's CPU boost during cold start. The queue worker is not latency-sensitive.

2. **No native PubSub in dev.** GCP PubSub is the production queue target, but Redis is used in dev. The adapter interface abstracts the difference, but the PubSub adapter has fewer test cycles. Mitigation: the Redis adapter is primary for both dev and initial production. PubSub is a future migration path.

3. **Environment variable sprawl.** With 15+ environment variables, configuration management becomes complex. Mitigation: use `.env.example` for dev. Use Cloud Run secrets and Secret Manager for production. Document all variables in the README.

4. **Multi-architecture builds.** CI must build for `linux/amd64` (Cloud Run) and `linux/arm64` (Apple Silicon dev). Mitigation: use Docker Buildx with `--platform` flag. The Dockerfile is architecture-independent.

## Related ADRs

- **ADR-0011**: Queue Semantics, Retries, Idempotency, and DLQ — defines the Redis queue adapter used in dev, with PubSub as the production alternative.
- **ADR-0019**: Data Retention, PII, and Document Security — defines the MinIO/GCS bucket policies and encryption that the deployment target must support.
- **ADR-0016**: Search/Indexing Backend Choice — evaluates PostgreSQL FTS vs OpenSearch, affecting the database deployment configuration.
