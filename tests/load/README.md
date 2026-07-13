# OpsCore Load Tests

Uses [k6](https://k6.io) for API load testing with pass/fail thresholds.

## Prerequisites

```bash
# Install k6
# macOS: brew install k6
# Linux: https://k6.io/docs/get-started/installation/
```

## Scripts

| Script | Description |
|--------|-------------|
| `script.js` | Original smoke + ramping load test with a single tenant |
| `k6.js` | Multi-tenant simulation with weighted tenant selection (alpha/beta/gamma) |

## Usage

### Multi-tenant load test (`k6.js`)

```bash
# Default load test (stages: ramp 10 → sustain 20 → ramp down)
make load-test

# Custom staged run
make load-test-staged

# Quick smoke test (2 VUs for 30s)
make load-test-smoke

# Direct k6 invocation
k6 run k6.js
k6 run --vus 2 --duration 30s k6.js
```

### Single-tenant load test (`script.js`)

```bash
# Smoke test (1 VU, 30s)
k6 run tests/load/script.js -e API_BASE=http://localhost:8081 --scenario smoke

# Full load test
k6 run tests/load/script.js -e API_BASE=http://localhost:8081

# Stress test
k6 run tests/load/script.js -e API_BASE=http://localhost:8081 --vus 50 --duration 2m
```

## Thresholds

| Metric | Threshold | Fail if |
|--------|-----------|---------|
| Failed requests | < 1% (script.js) / < 10% (k6.js) | rate >= threshold |
| Request duration p95 | < 2s | p(95) >= 2000ms |

## Multi-Tenant Design (`k6.js`)

Three tenants with weighted load distribution:

| Tenant | API Key | Weight | Profile |
|--------|---------|--------|---------|
| `tenant-alpha` | `owner-dev-key` | 5 | Heavy user |
| `tenant-beta` | `ops-admin-dev-key` | 3 | Medium user |
| `tenant-gamma` | `reviewer-dev-key` | 1 | Light user |

Operations per VU iteration:
1. Upload simulation (POST /upload)
2. Vendor creation (POST /vendors)
3. Status check (GET /status/summary)
4. Health check (GET /health) — 30% chance, no auth required

## Notes

- All scripts assume the API server is running at `http://localhost:8080`.
- The `script.js` uses `API_BASE` env var to override the target URL.
- The `k6.js` uses hardcoded `localhost:8080`; edit the URL strings if needed.
- Dev API keys used in `k6.js` match the static keys in `cmd/server/main.go`.
