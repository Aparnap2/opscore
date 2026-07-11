.PHONY: local-up local-down run-api run-ui test-unit test-integration test-all test-live-llm test-live-ocr test-live test-all-with-live e2e-up e2e-run e2e-down test-e2e test-load-smoke test-load build lint fmt run

# --- Local Development ---

local-up:
	docker compose -f docker-compose.local.yml up -d
	@echo "Postgres: localhost:5432 | MinIO: localhost:9000 | Redis: localhost:6379"

local-down:
	docker compose -f docker-compose.local.yml down -v

run-api:
	go run ./cmd/server/main.go

run-ui:
	cd ops-ui && streamlit run app.py

# --- Testing ---

test-unit:
	go test ./internal/domain/... -v -race

test-integration:
	docker compose -f docker-compose.test.yml up -d --wait
	go test ./tests/... -v -tags integration -timeout 300s
	docker compose -f docker-compose.test.yml down

test-all: test-unit test-integration

# --- Build & Tooling ---

build:
	go build ./...

lint:
	golangci-lint run ./... || go vet ./...

fmt:
	go fmt ./...

run:
	go run ./cmd/server/main.go

# --- Live Provider Tests (opt-in, real API calls) ---

test-live-llm:
	@echo "Running live LLM contract tests..."
	@echo "  Requires: OPENROUTER_API_KEY or SARVAM_API_KEY"
	-go test ./tests/live/llm/... -tags=live -v -count=1 -timeout 300s

test-live-ocr:
	@echo "Running live OCR contract tests..."
	@echo "  Requires: SARVAM_API_KEY + tests/fixtures/pdfs/"
	-go test ./tests/live/ocr/... -tags=live -v -count=1 -timeout 300s

test-live: test-live-llm test-live-ocr
	@echo "Live provider tests completed."

test-all-with-live: test-all test-live
	@echo "All tests including live providers completed."

# --- End-to-End Tests (Docker-based, full stack) ---

e2e-up:
	docker compose -f docker-compose.e2e.yml up -d --build postgres minio redis stub-ocr api

e2e-run:
	docker compose -f docker-compose.e2e.yml run --rm e2e

e2e-down:
	docker compose -f docker-compose.e2e.yml down -v

test-e2e: e2e-up
	@echo "Waiting for API to be ready..."
	@sleep 5
	$(MAKE) e2e-run
	$(MAKE) e2e-down

# --- Load Tests (requires k6) ---

test-load-smoke:
	k6 run tests/load/script.js -e API_BASE=http://localhost:8081 --scenario smoke

test-load:
	k6 run tests/load/script.js -e API_BASE=http://localhost:8081
