# OpsCore Load Tests

Uses [k6](https://k6.io) for API load testing with pass/fail thresholds.

## Prerequisites

```bash
# Install k6
# macOS: brew install k6
# Linux: https://k6.io/docs/get-started/installation/
```

## Usage

```bash
# Smoke test (1 VU, 30s)
k6 run tests/load/script.js -e API_BASE=http://localhost:8081 --scenario smoke

# Full load test (ramping to 10 VUs)
make test-load

# Stress test
k6 run tests/load/script.js -e API_BASE=http://localhost:8081 --vus 50 --duration 2m
```

## Thresholds

| Metric | Threshold | Fail if |
|--------|-----------|---------|
| Failed requests | < 1% | rate >= 0.01 |
| Request duration p95 | < 2s | p(95) >= 2000ms |
