.PHONY: local-up local-down run-api run-ui test-unit test-integration test-all build lint fmt run

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
