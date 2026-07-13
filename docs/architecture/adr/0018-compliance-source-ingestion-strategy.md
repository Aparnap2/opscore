# ADR-0018: Compliance-Source Ingestion Strategy

## Status

**Proposed** — OpsCore ingests regulatory content from SEBI, RBI, GST, and MCA sources. Current ingestion is ad-hoc (manual import or per-agent HTTP fetch). No defined strategy for scheduled polling, deduplication, or parsing.

## Date

2026-07-13

## Context

### Problem Statement

OpsCore monitors compliance by ingesting regulatory documents from Indian statutory bodies. Each source has a different access method, data format, and update frequency:

| Source | Access Method | Format | Update Frequency | Current State |
|--------|--------------|--------|-----------------|---------------|
| **SEBI** | RSS feed (`sebi.gov.in/rss.html`) | RSS XML | Daily (circulars, press releases) | Ad-hoc HTTP fetch in test only |
| **RBI** | RSS feed (`rbi.org.in/rss/rss.aspx`) | RSS XML | Daily (notifications, circulars) | Ad-hoc HTTP fetch in test only |
| **GST** | GST Developer Portal API | JSON | Weekly (notices, rate changes) | No integration |
| **MCA** | data.gov.in API (company master) | JSON | Monthly (company filings, director changes) | No integration |
| **SEBI** | PDF circulars (direct download) | PDF | As issued | No integration |

Without a defined strategy:
1. **No polling schedule.** Compliance documents are ingested only when manually triggered or when an agent specifically fetches them.
2. **No deduplication.** The same circular could be ingested multiple times if fetched by different agents.
3. **No parsing strategy.** RSS feeds and PDF circulars require different parsing approaches. There is no parser for SEBI or RBI RSS feeds.
4. **No rate limiting.** Unbounded fetch frequency could trigger rate limiting or IP blocking by source servers.
5. **No fallback.** If a source is unavailable (e.g., SEBI RSS returns 503), there is no defined fallback behavior.

### Requirements

1. Official-source-first: prefer official RSS feeds and structured APIs over scraping.
2. Deterministic parsing of RSS/HTML before any LLM extraction.
3. Configurable polling schedule (cron expression) per source.
4. Deduplication by `source_hash` (SHA256 of the raw document content).
5. Fallback to LLM extraction only when the source structure changes (e.g., SEBI changes their RSS XML schema).
6. Rate limiting per source to avoid IP blocking.
7. Audit trail for every ingestion (success, failure, source unavailable, structure changed).

## Decision

### Primary Decision: Official-Source-First Ingestion with RSS as Primary, Structured API as Secondary, Web Scraping as Last Resort

### Source Priority Matrix

| Priority | Source | Method | Format | Parser | Fallback |
|----------|--------|--------|--------|--------|----------|
| **1** | SEBI circulars | RSS feed | XML | Deterministic XML → domain.ComplianceDocument | LLM extraction if XML structure changes |
| **2** | RBI notifications | RSS feed | XML | Deterministic XML → domain.ComplianceDocument | LLM extraction if XML structure changes |
| **3** | GST notices | GST Developer Portal API | JSON | Deterministic JSON → domain.ComplianceDocument | Web scraping → LLM extraction |
| **4** | MCA company data | data.gov.in API | JSON | Deterministic JSON → domain.ComplianceDocument | Web scraping of MCA portal → LLM |
| **5** | SEBI PDF circulars | Direct PDF download | PDF | Sarvam OCR → deterministic field extraction | LLM extraction if OCR fails |

### RSS Polling Scheduler

A compliance scheduler agent runs on a configurable cron schedule:

```go
// internal/agents/compliance_scheduler.go
type SourceConfig struct {
    Name        string   `json:"name"`
    FeedURL     string   `json:"feed_url"`
    CronExpr    string   `json:"cron_expr"`    // e.g., "0 8 * * *" (daily at 8 AM)
    RateLimit   int      `json:"rate_limit"`   // max requests per minute
    Enabled     bool     `json:"enabled"`
}

var DefaultSources = []SourceConfig{
    {
        Name:      "sebi",
        FeedURL:   "https://www.sebi.gov.in/rss.html",
        CronExpr:  "0 8 * * *",
        RateLimit: 5,
        Enabled:   true,
    },
    {
        Name:      "rbi",
        FeedURL:   "https://rbi.org.in/rss/rss.aspx",
        CronExpr:  "0 9 * * *",
        RateLimit: 5,
        Enabled:   true,
    },
}
```

The scheduler runs in a goroutine in `cmd/server/main.go`:

```go
func startComplianceScheduler(ctx context.Context, agent *agents.ComplianceAgent) {
    for _, src := range DefaultSources {
        if !src.Enabled {
            continue
        }
        go func(src SourceConfig) {
            cron := cron.New()
            cron.AddFunc(src.CronExpr, func() {
                agent.PollSource(ctx, src)
            })
            cron.Start()
            <-ctx.Done()
            cron.Stop()
        }(src)
    }
}
```

### Parsing Strategy Per Source

**RSS Feeds (SEBI, RBI):**

```go
// internal/adapters/compliance/rss.go
type RSSParser struct{}

func (p *RSSParser) Parse(ctx context.Context, raw []byte) ([]domain.ComplianceDocument, error) {
    var feed struct {
        Channel struct {
            Title       string `xml:"title"`
            Description string `xml:"description"`
            Items       []struct {
                Title       string `xml:"title"`
                Link        string `xml:"link"`
                Description string `xml:"description"`
                PubDate     string `xml:"pubDate"`
                Category    string `xml:"category"`
            } `xml:"item"`
        } `xml:"channel"`
    }
    if err := xml.Unmarshal(raw, &feed); err != nil {
        return nil, fmt.Errorf("rss parse failed: %w", err)
    }

    var docs []domain.ComplianceDocument
    for _, item := range feed.Channel.Items {
        docs = append(docs, domain.ComplianceDocument{
            Source:      "sebi", // or "rbi"
            ExternalID:  item.Link,
            Title:       item.Title,
            Summary:     item.Description,
            PublishedAt: parseRSSDate(item.PubDate),
            SourceHash:  sha256Hex([]byte(item.Link + item.Title)),
            URL:         item.Link,
            Raw:         raw,
        })
    }
    return docs, nil
}
```

**Structured API (GST Developer Portal, MCA via data.gov.in):**

```go
// internal/adapters/compliance/gst.go
type GSTParser struct{}

func (p *GSTParser) Parse(ctx context.Context, raw []byte) ([]domain.ComplianceDocument, error) {
    var response struct {
        Status string          `json:"status"`
        Data   json.RawMessage `json:"data"`
    }
    if err := json.Unmarshal(raw, &response); err != nil {
        return nil, fmt.Errorf("gst api parse failed: %w", err)
    }
    // Deterministic JSON to domain type mapping
    // ...
}
```

**LLM Fallback (when structure changes):**

If the RSS XML schema changes (e.g., SEBI adds a namespace or changes field names), the deterministic parser fails. The fallback extracts the same fields using LLM:

```go
// internal/agents/compliance_agent.go
func (a *ComplianceAgent) parseWithFallback(ctx context.Context, src SourceConfig, raw []byte) ([]domain.ComplianceDocument, error) {
    docs, err := a.rssParser.Parse(ctx, raw)
    if err == nil {
        return docs, nil
    }
    // Deterministic parser failed — likely a structure change
    a.tracer.RecordParserFallback(ctx, src.Name, err)

    // LLM-assisted extraction
    prompt := fmt.Sprintf(`Extract compliance documents from this RSS feed XML.
For each item, extract: title, link, description, publication date, and category.
Return as JSON array of objects.

XML: %s`, string(raw))

    result, llmErr := a.llm.ExtractFields(ctx, prompt, &[]domain.ComplianceDocument{})
    if llmErr != nil {
        return nil, fmt.Errorf("llm fallback failed after parser error %w: %w", err, llmErr)
    }
    return result, nil
}
```

### Deduplication

Deduplication is by `source_hash` — SHA256 of the document's unique content (or link + title for RSS items):

```go
// internal/agents/compliance_agent.go
func (a *ComplianceAgent) ingestDocuments(ctx context.Context, tenantID string, docs []domain.ComplianceDocument) (int, error) {
    ingested := 0
    for _, doc := range docs {
        // Check for existing document by source_hash
        exists, err := a.db.CheckDocumentExists(ctx, tenantID, doc.SourceHash)
        if err != nil {
            return ingested, err
        }
        if exists {
            continue // skip duplicate
        }
        if err := a.db.InsertDocument(ctx, tenantID, doc); err != nil {
            return ingested, err
        }
        ingested++
    }
    return ingested, nil
}
```

### Rate Limiting Per Source

Each source has a rate limit (requests per minute). The rate limiter is implemented as a token bucket:

```go
// internal/adapters/compliance/rate_limiter.go
type RateLimiter struct {
    tokens    chan struct{}
    ticker    *time.Ticker
}

func NewRateLimiter(rpm int) *RateLimiter {
    rl := &RateLimiter{
        tokens: make(chan struct{}, rpm),
        ticker: time.NewTicker(time.Minute / time.Duration(rpm)),
    }
    go func() {
        for range rl.ticker.C {
            select {
            case rl.tokens <- struct{}{}:
            default:
            }
        }
    }()
    return rl
}

func (rl *RateLimiter) Wait(ctx context.Context) error {
    select {
    case <-rl.tokens:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}
```

### Ingestion Workflow

```
┌──────────────┐     ┌────────────────┐     ┌───────────────────┐
│  Cron Trigger │────▶│ PollSource()    │────▶│  HTTP Fetch       │
│  (0 8 * * *)  │     │ (compliance     │     │  (with rate       │
│               │     │  scheduler)     │     │   limiter)        │
└──────────────┘     └────────────────┘     └────────┬──────────┘
                                                     │
                                                     ▼
                                            ┌───────────────────┐
                                            │  Parse Response    │
                                            │  ┌─────────────┐   │
                                            │  │ XML/JSON     │   │
                                            │  │ Deterministic│   │
                                            │  │ Parser       │   │
                                            │  └──────┬──────┘   │
                                            │         │ fail     │
                                            │         ▼          │
                                            │  ┌─────────────┐   │
                                            │  │ LLM Fallback │   │
                                            │  │ Parser       │   │
                                            │  └──────┬──────┘   │
                                            └─────────┼─────────┘
                                                      │
                                                      ▼
                                            ┌───────────────────┐
                                            │  Deduplication     │
                                            │  (source_hash      │
                                            │   check)           │
                                            └─────────┬─────────┘
                                                      │
                                                      ▼
                                            ┌───────────────────┐
                                            │  Insert + Index    │
                                            │  (document store   │
                                            │   + search index)  │
                                            └─────────────────────┘
```

## Alternatives Considered

### 1. Manual Import Only

Rejected because:
- Regulatory compliance requires timely monitoring. Manual import is not fast enough for daily circulars.
- OpsCore's value proposition is autonomous back-office — manual import contradicts this.

### 2. Web Scraping for All Sources

Rejected because:
- RSS feeds are available for SEBI and RBI. Scraping would be brittle and violate the source's terms of service.
- Scraping requires maintaining HTML parsers that break when the website layout changes.
- RSS feeds are the canonical, machine-readable source.

### 3. LLM-First Parsing

Rejected because:
- RSS XML is structured and deterministic. LLM parsing adds cost and latency without benefit.
- LLM parsing is non-deterministic — same RSS feed may produce different documents on different runs.
- LLM should only be used when the structure changes (as a fallback).

### 4. Polling via Third-Party Feed Reader (e.g., Feedly API)

Rejected because:
- Adds third-party dependency for a simple HTTP fetch + XML parse.
- Feed reader APIs may not handle Indian regulatory feeds correctly.
- Data would flow through a third party, complicating security and privacy.

## Consequences

### Benefits

1. **Timely compliance updates.** The cron scheduler polls sources daily. New circulars and notifications are ingested within hours of publication.

2. **Deterministic parsing for stable sources.** RSS XML and structured JSON APIs are parsed deterministically. No LLM cost or non-determinism for routine ingestion.

3. **Graceful structure-change handling.** If a source changes its format, the deterministic parser fails, the fallback LLM parser activates, and an alert is sent to operators. The system does not silently break.

4. **Deduplication prevents duplicate processing.** The same circular is not ingested twice, even if polled multiple times or fetched by different agents.

5. **Rate limiting prevents IP blocking.** Each source has a conservative rate limit (5 RPM). OpsCore is a good citizen of the source's infrastructure.

6. **Auditable ingestion.** Every poll, parse success/failure, deduplication, and LLM fallback is recorded in the audit trail.

### Trade-offs / Risks

1. **RSS feed availability.** If SEBI or RBI discontinues their RSS feed, the deterministic parser breaks permanently. Mitigation: the LLM fallback takes over immediately. The operator is alerted to investigate alternative sources (e.g., web scraping the circulars page).

2. **GST Developer Portal API access.** The GST Developer Portal requires registration and API key provisioning. If access is delayed or denied, GST notices cannot be ingested via API. Mitigation: the fallback chain uses web scraping of the GST portal → LLM extraction. This is less reliable but functional.

3. **Cron scheduler single point of failure.** If the application crashes, the scheduler stops running. Mitigation: the scheduler runs in the main application process. On restart, it determines the next scheduled run from the cron expression. Missed runs are not caught up — the next scheduled poll catches new documents.

4. **Source_hash collision risk.** Two different documents with the same link and title would be treated as duplicates. Mitigation: `source_hash` includes the raw content SHA256 as well as the link, making collisions astronomically unlikely. If a source re-publishes an updated version of the same circular, the content hash differs and it is ingested as a new version.

5. **Rate limiting may miss time-sensitive documents.** If SEBI publishes multiple circulars in quick succession, the rate limiter may delay ingestion of the second one by 12 seconds (5 RPM = one request per 12 seconds). Mitigation: this is acceptable — compliance documents are not millisecond-sensitive. The scheduler polls daily, not continuously.

## Related ADRs

- **ADR-001**: Retrieval Architecture for Compliance & Document Search — ingested documents are chunked, indexed, and made searchable through the retrieval funnel.
- **ADR-0008**: Retrieval Router — Deterministic and Vectorless/Hybrid — the conditional router selects the retrieval path for ingested compliance documents.
- **ADR-0019**: Data Retention, PII, and Document Security — ingested compliance documents are subject to the 7-year retention policy and bucket lifecycle rules.
