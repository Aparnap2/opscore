# Indian Government Data Sources for Validation

OpsCore validates vendor and document data against official Indian government sources where available.

## Validation Strategy

Always validate in layers:
1. **Syntactic validation** (local Go, deterministic) — format, checksum, required fields
2. **Cross-field validation** (local Go, deterministic) — invoice arithmetic, GST breakup consistency
3. **Authoritative lookup** (external API) — confirm against official datasets
4. **Trust downgrade** — if lookup fails or is unavailable, reduce trust score and route to review

## Available Sources

### GSTIN Validation

| Source | Type | Access | Notes |
|--------|------|--------|-------|
| GST Developer Portal | Official API | Registration required | https://developer.gst.gov.in |
| e-Invoice GSTIN detail | Official API | Credentials required | NIC/IRIS taxpayer detail endpoints |

**Local validation first:** GSTIN format (15 chars: state code + PAN + entity code + check digit + Z) and checksum are validated deterministically in Go (see `internal/domain/india_validator.go`) before any external call.

### Company / Vendor Identity

| Source | Type | Access | Notes |
|--------|------|--------|-------|
| MCA Company Master Data | Open data | Free via data.gov.in | https://data.gov.in/catalog/mca-company-master-data |
| OGD India API | Open API | Free API key | https://api.data.gov.in |

### Compliance Monitoring

| Source | Type | Access | Notes |
|--------|------|--------|-------|
| SEBI RSS Feeds | Public RSS | Free, no auth | Official regulatory circulars |
| SEBI Circulars page | Public HTML | Free, no auth | https://www.sebi.gov.in |
| RBI RSS Feeds | Public RSS | Free, no auth | https://rbi.org.in/rss/rss.aspx |
| RBI DBIE Portal | Public data | Free, no auth | https://dbie.rbi.org.in |

## External Source Unavailability Policy

When an external source is unavailable:
1. Do NOT silently pass validation
2. Log the unavailability with structured slog
3. Downgrade trust score / mark as unverified
4. Preserve audit reason for every decision
5. Route to human review if confidence drops below threshold

## Rate Limiting

All external API calls are subject to:
- Token-bucket rate limiter (per-tenant, configured via `RATE_LIMIT_RPS` env)
- Max 3 real LLM/OCR calls per live test run
- Exponential backoff with jitter on retryable failures
- Hard stop after max retries — debug dump to `/tmp/opscore_live_*.json`