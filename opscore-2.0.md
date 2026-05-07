# OpsCore PRD and Migration Plan

Version: 1.0  
Date: 2026-05-07  
Status: Proposed

## 1. Product Overview

OpsCore is a vertical agentic AI operations platform for Indian B2B back-office workflows. It automates three high-friction operational streams: document ingestion, vendor onboarding, and compliance monitoring. The target outcome is a production-grade portfolio project that is hosted, observable, CI/CD-enabled, and cheap to run on Azure serverless infrastructure while keeping LLM usage tightly scoped. [web:205][web:244][page:1]

OpsCore is not a general chatbot. It is a workflow system with deterministic code as the default path and LLM reasoning only where ambiguity genuinely exists. This design reduces token cost, improves auditability, and makes the project more credible as an enterprise-grade system. [web:205][web:252]

## 2. Problem Statement

Indian B2B internal operations often depend on manual handling of invoices, vendor forms, GST and banking details, and changing regulatory circulars. These workflows are repetitive, document-heavy, error-prone, and require judgment only in a minority of cases. [web:244]

Most “agentic AI” demos overuse the model for routing, validation, and business logic. OpsCore instead uses static code for predictable work and reserves the LLM for document interpretation, ambiguity resolution, and compliance reasoning. This yields lower cost and better operational reliability. [web:205][web:252]

## 3. Goals

- Build a production-grade vertical AI system that feels enterprise-ready, not like a toy demo.  
- Keep monthly infra cost near zero by using Azure free services wherever possible. [page:1]  
- Use deterministic Go and serverless Azure primitives for core workflows.  
- Use Sarvam AI as the primary India-focused OCR and LLM provider, with Modal as fallback for heavy OCR or failure handling. [web:244][web:255]  
- Support human-in-the-loop approval for sensitive operations. ADK Go 1.0 supports confirmation flows and native observability that fit this requirement well. [web:205][web:252]  
- Gracefully transform the existing codebase rather than rewriting recklessly.

## 4. Non-Goals

- Building a general-purpose autonomous agent platform.  
- Replacing all Python immediately.  
- Supporting every ERP, bank, or compliance portal.  
- Building a frontend-heavy SPA with React or Next.js.  
- Running a large always-on container fleet.

## 5. Users and Roles

### Primary users

- Finance operator: uploads invoices, reviews extracted fields, approves payments.  
- Procurement operator: onboards vendors, reviews risk flags, approves vendors.  
- Compliance analyst: reviews regulatory changes and gap reports.  
- Admin: manages tenants, roles, settings, secrets, and policies.

### Access model

Use Azure Entra ID B2C or equivalent Azure-native auth so auth complexity is offloaded rather than custom-built. The free services page lists Entra ID B2C under free services, which supports the portfolio goal of minimizing operational overhead. [page:1]

## 6. Product Scope

OpsCore v1 will ship three workflows.

### Workflow A: Document ingestion

Input: PDF invoice, contract, PO, GST notice.  
Output: structured fields, validation report, optional approval request, and downstream sync action.

### Workflow B: Vendor onboarding

Input: vendor form and supporting documents.  
Output: validation, risk score, trust tier, duplicate flags, and approval status.

### Workflow C: Compliance monitoring

Input: scheduled fetch from regulatory sources such as SEBI, RBI, and GST portals.  
Output: extracted regulation text, chunk embeddings, gap analysis, citations, and issue tickets.

## 7. Product Principles

1. Deterministic-first.  
2. Human approval for risky actions.  
3. Serverless by default.  
4. India-specific AI where useful.  
5. Explicit schemas and typed outputs.  
6. Observable every step.  
7. Graceful migration from the current codebase.

## 8. Functional Requirements

### 8.1 Document ingestion

- Upload PDF to Azure Blob Storage. [page:1]  
- Create a job record in Cosmos DB. [page:1]  
- Push job onto Azure Queue Storage for asynchronous processing. [page:1]  
- Extract text and layout using Sarvam Document Intelligence or Sarvam document digitization APIs. Sarvam supports structured processing across 22 Indian languages plus English and exposes job-based document processing endpoints with JSON and Markdown-oriented outputs. [web:244][web:255][web:259]  
- If Sarvam fails, times out, or returns low confidence, call Modal fallback hosting Docling/Granite/DeepSeek OCR.  
- Perform deterministic validation in code for GST, PAN, IFSC, duplicates, and threshold rules.  
- Route low-confidence or high-value cases to HITL approval. ADK Go 1.0 has confirmation flows suitable for pausing on sensitive tool execution. [web:205][web:252]  
- Write audit trail entries for every step.

### 8.2 Vendor onboarding

- Accept vendor details and documents.  
- Validate GST, PAN, IFSC, and duplicate vendors with deterministic code.  
- Compute risk score and trust battery state in code.  
- Use LLM only for ambiguous document interpretation, if needed.  
- Pause for approval on risky or incomplete onboarding cases. [web:205]

### 8.3 Compliance monitoring

- Run scheduled fetches of public regulatory sources using Azure Functions timer triggers and plain HTTP/HTML/RSS parsing.  
- Store raw files in Blob Storage and indexed content in Cosmos DB. [page:1]  
- Generate embeddings and store them in Cosmos DB vector-capable collections. Microsoft documents vector search in Azure Cosmos DB for NoSQL, supporting integrated vector search patterns in the database. [web:220][web:224]  
- Use LLM reasoning to produce concise, citation-backed gap analysis.  
- Classify severity with deterministic code.  
- Create tickets or review items.

## 9. Success Metrics

### Product metrics

- 90%+ successful workflow completion without manual intervention on clean sample data.  
- <10% of documents escalated to HITL on curated demo data.  
- Compliance scrape succeeds weekly across configured sources.  
- Mean time from upload to extraction result under 2 minutes for small documents.

### Engineering metrics

- 95%+ job state persistence integrity.  
- P95 API latency under 800 ms for non-LLM endpoints.  
- Zero critical secrets committed to repo.  
- CI green on lint, unit, integration, and E2E tests.

## 10. Architecture Summary

OpsCore will run primarily on Azure serverless services: Azure Functions, Azure Static Web Apps, Azure Cosmos DB, Azure Queue Storage, Azure Blob Storage, and Azure Monitor/Application Insights. The Azure free services page lists Functions, Static Web Apps, Cosmos DB, and other relevant services in its free offering, which supports the low-cost deployment target. [page:1]

Sarvam AI is the primary model and OCR provider. Sarvam’s document intelligence offering is designed for structured document processing in Indian languages and uses a job model with upload and completion states. [web:244][web:255] Modal is retained as an operational fallback for OCR-heavy or failed jobs.

ADK Go 1.0 is the preferred agent runtime for serverless execution because it includes native OpenTelemetry integration, human-in-the-loop confirmation flows, and YAML-configurable agents, all of which align with OpsCore’s production-grade requirements. [web:205][web:252][web:256]

## 11. Serverless Azure System Design

### 11.1 Frontend

- Azure Static Web Apps. [page:1]  
- HTMX + lightweight templates.  
- No heavy SPA requirement.

### 11.2 API layer

- Azure Functions HTTP triggers. [page:1]  
- Responsibilities: auth enforcement, upload orchestration, job polling, approval actions, admin APIs.

### 11.3 Async execution

- Azure Queue Storage queues. [page:1]  
- Azure Functions Queue triggers for document, vendor, compliance, and dead-letter processing.

### 11.4 Scheduled jobs

- Azure Functions Timer triggers for regulatory scrapes, trust battery decay, cleanup, and health checks.

### 11.5 Persistence

- Azure Cosmos DB for jobs, document metadata, audit events, compliance chunks, vectors, and workflow state. Microsoft documents integrated vector search for Azure Cosmos DB for NoSQL, which helps remove separate vector infrastructure. [web:220][web:224]  
- Azure Blob Storage for PDFs, extracted artifacts, and export bundles. Blob Storage appears on the Azure free services page as part of the introductory free tier. [page:1]

### 11.6 Observability

- Azure Monitor + Application Insights. Azure free services include Azure Monitor-related free usage allowances on the pricing page, making it a better operational fit than self-hosting Prometheus/Grafana for this project. [page:1]

### 11.7 Identity

- Prefer Azure-native auth such as Entra ID B2C or equivalent Azure identity integration rather than custom JWT infrastructure. [page:1]

## 12. LLM and OCR Strategy

### Primary path

- Sarvam Document Intelligence / document digitization for OCR and layout extraction. Sarvam supports structured output formats, language configuration, and job lifecycle management for uploaded files. [web:244][web:255][web:259]  
- Sarvam-M for extraction, summarization, and gap analysis where a model is needed.

### Fallback path

- Modal-hosted OCR stack, such as Docling + Granite or DeepSeek OCR variant, triggered only if primary OCR fails or confidence is below threshold.

### Model usage policy

Use the LLM only for:
- ambiguous extraction,  
- compliance interpretation,  
- citation-backed summaries,  
- uncertain vendor-document interpretation.

Do not use the LLM for:
- routing,  
- validation,  
- thresholding,  
- duplicate checks,  
- risk scoring,  
- state transitions.

## 13. Workflow Definitions

### 13.1 Document ingestion workflow

1. User uploads document.  
2. API stores file in Blob Storage.  
3. API writes job record to Cosmos DB.  
4. API enqueues job to document queue.  
5. Worker pulls job.  
6. OCR via Sarvam. [web:244][web:255]  
7. Structured extraction via Sarvam-M if needed.  
8. Deterministic validations run.  
9. If confidence low or business rule triggered, create HITL event. ADK Go supports confirmation-oriented pauses for sensitive steps. [web:205]  
10. Resume after approval.  
11. Persist final result, audit events, and any downstream sync status.

### 13.2 Vendor onboarding workflow

1. User submits vendor form and docs.  
2. API stores docs and job state.  
3. Queue trigger validates identifiers and checks duplicates.  
4. Risk score is computed in deterministic code.  
5. Optional model step interprets messy supporting docs.  
6. HITL if required.  
7. Write vendor record and trust state.

### 13.3 Compliance workflow

1. Timer trigger fetches updates from configured public sources.  
2. New documents are stored in Blob Storage.  
3. Content is extracted and chunked.  
4. Embeddings stored in Cosmos DB vector collection. [web:220][web:224]  
5. Model compares policy context and extracted changes.  
6. Deterministic severity rules applied.  
7. Gap report and citations persisted.  
8. Ticket or task created.

## 14. Data Model

### Core collections

- jobs  
- documents  
- vendors  
- compliance_chunks  
- audit_events  
- hitl_requests  
- settings

### Required properties

- tenant_id  
- workflow_type  
- status  
- created_at  
- updated_at  
- trace_id  
- correlation_id

### Audit events

Every mutation must create append-only audit events:
- actor  
- action  
- target_type  
- target_id  
- old_state summary  
- new_state summary  
- timestamp

## 15. Existing Codebase Transformation Strategy

The current codebase already has passing E2E flows and should be treated as a working baseline, not as throwaway work. The migration must be incremental and reversible.

### Guiding approach

- Keep domain logic that already works.  
- Replace infrastructure before replacing business logic.  
- Introduce adapters rather than rewriting core modules blindly.  
- Maintain passing E2E coverage during every major change.

### Preserve if currently working

- trust battery state machine  
- vendor risk scoring logic  
- validation utilities  
- schemas and tests  
- workflow fixtures and E2E dataset

### Replace gradually

- Docker-local infra with Azure adapters  
- Python queue/runtime orchestration with Azure Queue + Functions or hybrid adapter layer  
- heavy OCR/extraction pipeline with Sarvam-first adapter  
- scraping infra with plain scheduled fetchers  
- monitoring stack with App Insights

## 16. Migration Phases

### Phase 0: Baseline freeze

- Tag current branch.  
- Capture current E2E outputs.  
- Export architecture inventory.  
- Mark stable modules and volatile modules.

### Phase 1: Introduce provider interfaces

Create interfaces or abstractions around:
- OCR provider  
- LLM provider  
- queue provider  
- object storage  
- workflow checkpoint store  
- tracing provider

At this stage, legacy implementations continue to work.

### Phase 2: Add Azure adapters

Implement:
- BlobStorageAdapter  
- QueueStorageAdapter  
- CosmosStoreAdapter  
- AppInsightsTracer  
- StaticWebApp frontend contract

### Phase 3: Add Sarvam adapters

Implement:
- SarvamOCRAdapter  
- SarvamLLMAdapter  
- ModalFallbackAdapter  
- confidence-based router

### Phase 4: Introduce serverless entrypoints

Add Azure Functions wrappers for:
- upload API  
- job status API  
- approval API  
- document queue trigger  
- vendor queue trigger  
- compliance timer + queue trigger

### Phase 5: Port or wrap workflow engine

Preferred direction: move runtime orchestration to Go + ADK Go while preserving proven Python logic behind service boundaries where necessary. ADK Go 1.0 explicitly supports HITL confirmation and OTel tracing, making it a suitable orchestration layer. [web:205][web:252]

### Phase 6: Retire replaced components

Remove obsolete Docker services, local infra assumptions, and dead code only after parity is proven.

## 17. Development Plan

### Sprint 1: Assessment and adapters

- Inventory current modules.  
- Map every module to preserve, wrap, or replace.  
- Introduce provider interfaces.  
- Add contract tests for provider boundaries.

### Sprint 2: Storage and queue migration

- Blob Storage integration.  
- Queue Storage integration.  
- Cosmos DB integration.  
- App Insights tracing.  
- Smoke tests on Azure free resources.

### Sprint 3: Sarvam-first AI layer

- Sarvam OCR integration. [web:244][web:255]  
- Sarvam extraction integration.  
- Modal fallback path.  
- Confidence scoring and fallback routing tests.

### Sprint 4: Workflow migration

- Document workflow end-to-end on serverless stack.  
- Vendor workflow end-to-end on serverless stack.  
- Compliance workflow end-to-end on serverless stack.

### Sprint 5: Frontend and auth

- Static Web Apps frontend. [page:1]  
- Azure-native auth integration.  
- HITL approval pages.  
- tenant-aware UI states.

### Sprint 6: Hardening

- retry policies  
- dead-letter handling  
- alerting  
- chaos tests  
- load tests  
- deploy gates

## 18. Delivery Checklist

### Product checklist

- [ ] PRD reviewed  
- [ ] workflows finalized  
- [ ] tenant model finalized  
- [ ] role model finalized  
- [ ] HITL rules finalized  
- [ ] sample data curated

### Architecture checklist

- [ ] Azure resources provisioned via IaC  
- [ ] all queues defined  
- [ ] Cosmos partition strategy documented  
- [ ] Blob containers defined  
- [ ] App Insights wired  
- [ ] auth strategy decided

### AI checklist

- [ ] Sarvam OCR integrated  
- [ ] Sarvam extraction integrated  
- [ ] fallback to Modal implemented  
- [ ] confidence thresholds configured  
- [ ] prompt/schema versioning documented

### Engineering checklist

- [ ] provider interfaces merged  
- [ ] adapter tests passing  
- [ ] Azure deployment scripts working  
- [ ] branch protections enabled  
- [ ] secrets stored outside repo  
- [ ] rollback procedure documented

### Demo checklist

- [ ] sample invoice demo  
- [ ] sample vendor onboarding demo  
- [ ] sample compliance gap demo  
- [ ] live metrics dashboard  
- [ ] recorded walkthrough

## 19. Test Strategy

### 19.1 Unit tests

Test:
- GST/PAN/IFSC validators  
- trust battery transitions  
- risk scoring  
- duplicate detection  
- confidence gating  
- adapter request/response mappers

### 19.2 Integration tests

Test:
- Blob upload/download  
- Queue enqueue/dequeue  
- Cosmos persistence and partitioning  
- Sarvam adapter happy path and failure path  
- Modal fallback invocation  
- App Insights trace emission

### 19.3 Contract tests

Test:
- Sarvam API response parsing against fixtures based on documented job states and payload shape. Sarvam exposes job states such as Accepted, Pending, Running, Completed, PartiallyCompleted, and Failed, which should be reflected in contract tests. [web:244][web:255]  
- Azure adapter interfaces  
- approval event schema  
- audit event schema

### 19.4 End-to-end tests

Must cover:
- document upload → OCR → validation → HITL → final state  
- vendor onboarding → validation → risk score → approval  
- compliance fetch → chunk → vector store → gap report

### 19.5 Resilience tests

- OCR provider timeout triggers fallback  
- queue poison message routes to DLQ  
- duplicate approval signal is idempotent  
- partial persistence resumes correctly

### 19.6 Performance tests

- 20 parallel upload jobs  
- 100 compliance chunks vector insert  
- polling endpoint under concurrent reads  
- queue drain under burst load

## 20. CI/CD Plan

### CI

On every PR:
- lint  
- format check  
- unit tests  
- integration tests with mocks  
- build artifact check  
- IaC validation  
- secret scan

### CD

On merge to main:
- deploy to staging  
- run smoke tests  
- verify dashboards and health endpoints  
- manual approval gate  
- deploy production  
- run post-deploy E2E smoke suite

## 21. Operational Concerns

### Reliability

- queue-based async boundaries  
- dead-letter queue  
- retry with capped exponential backoff  
- idempotency keys on uploads and approvals

### Security

- managed identities where possible  
- no secrets in code  
- approval needed for sensitive operations  
- role-based access control  
- append-only audit trail

### Cost control

- deterministic-first logic  
- LLM only for ambiguity  
- bounded prompt sizes  
- provider fallback only on need  
- serverless services within free tier targets. The Azure free services page provides the baseline quotas that support this target architecture. [page:1]

## 22. Risks and Mitigations

### Risk: Hybrid complexity

Mitigation: keep only one orchestration runtime long term; use adapters during transition.

### Risk: Sarvam API drift

Mitigation: contract tests, pinned request mappers, fixture-based regression tests. Sarvam documents stable job-oriented request/response patterns that can be codified in tests. [web:244][web:255]

### Risk: Existing code regression

Mitigation: freeze current E2E suite and enforce no-regression gates.

### Risk: Free tier misunderstanding

Mitigation: document exact quotas from Azure free services and monitor usage weekly. [page:1]

### Risk: Over-engineering graph storage

Mitigation: use Cosmos adjacency or document references first; only add Neo4j if traversal value is demonstrated.

## 23. Repository Plan

Suggested top-level layout:

```text
opscore/
├── docs/
│   ├── PRD.md
│   ├── WORKFLOWS.md
│   ├── SYSTEM_DESIGN.md
│   ├── DEV_PLAN.md
│   ├── TEST_PLAN.md
│   └── MIGRATION_PLAN.md
├── infra/
│   ├── bicep/
│   └── scripts/
├── adapters/
│   ├── azure/
│   ├── sarvam/
│   └── modal/
├── services/
├── workflows/
├── web/
├── tests/
└── .github/workflows/
```

## 24. Coding Agent Prompt

Use the following prompt for a coding agent working on the existing repository.

```md
You are transforming an existing working codebase into OpsCore vNext. Do not rewrite blindly. Preserve working domain logic and E2E behavior wherever possible.

Mission:
Migrate the current codebase toward a serverless Azure architecture with deterministic-first workflow logic, Sarvam as primary LLM/OCR provider, and Modal as fallback.

Constraints:
- Keep the existing passing E2E tests green unless explicitly replacing them with equivalent or better tests.
- Prefer adapters and incremental refactors over big-bang rewrites.
- Preserve business logic modules that already work: trust battery, deterministic scoring, validators, schemas, fixture datasets, and any stable workflow rules.
- Remove infrastructure complexity by replacing local Docker-dependent services with Azure-native abstractions where practical.
- Use LLMs only for ambiguous interpretation tasks, never for routing, validation, thresholds, or state transitions.
- Keep UI lightweight. Prefer Azure Static Web Apps + HTMX/templates. Do not introduce a frontend framework unless absolutely necessary.
- Introduce or maintain strong typing and explicit schemas at all provider boundaries.
- Every external integration must have a thin adapter and contract tests.
- Every state-changing operation must emit audit events.
- Every asynchronous workflow must be idempotent and resumable.
- Keep code changes small, reviewable, and reversible.

Target architecture:
- Azure Functions for HTTP, Queue, and Timer triggers.
- Azure Queue Storage for async jobs.
- Azure Blob Storage for file artifacts.
- Azure Cosmos DB for workflow state, metadata, audit events, and vector-backed compliance chunks.
- Azure Monitor / Application Insights for tracing, metrics, and alerts.
- Sarvam Document Intelligence / OCR as primary extraction provider.
- Sarvam-M as primary model for extraction and compliance reasoning.
- Modal as fallback OCR/Doc AI provider.
- Optional Go ADK orchestration layer where it simplifies HITL and observability; if introducing it, wrap existing logic rather than discarding it.

Execution rules:
1. First inspect the repository structure and summarize what should be preserved, wrapped, replaced, or deleted.
2. Create a migration plan with phases and explicit file-by-file changes.
3. Implement provider interfaces before swapping implementations.
4. Add tests before or alongside each adapter.
5. Keep legacy paths operational until parity is proven.
6. After each major change, run the relevant tests and report exactly what passed, failed, and what remains.
7. Do not silently change business behavior.
8. Prefer configuration flags to switch providers during transition.
9. Write docs as you go in docs/*.md.
10. When uncertain, choose the least disruptive path.

Immediate tasks:
- Inventory the current codebase.
- Identify existing workflow boundaries.
- Introduce interfaces for OCR, LLM, queue, storage, and tracing.
- Add Sarvam adapter stubs and Modal fallback stubs.
- Add Azure storage and queue adapter stubs.
- Propose the smallest viable first migration PR.

Definition of done for each PR:
- Code builds.
- Tests pass or failures are explicitly justified and queued.
- Docs updated.
- No unnecessary deletions.
- Rollback path clear.
```

## 25. First Question for You

Which migration constraint is highest priority for you: preserving the current Python business logic for as long as possible, or converging faster toward the Azure serverless target even if that means earlier workflow runtime changes?
