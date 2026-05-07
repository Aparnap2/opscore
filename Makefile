.PHONY: test-unit test-integration test-all build lint fmt

test-unit:
	go test ./internal/domain/... -v -race

test-integration:
	docker compose -f docker-compose.test.yml up -d --wait
	go test ./tests/... -v -tags integration -timeout 300s
	docker compose -f docker-compose.test.yml down

test-all: test-unit test-integration

build:
	go build ./...

lint:
	golangci-lint run ./... || go vet ./...

fmt:
	go fmt ./...

run:
	go run ./cmd/functions/main.go

test-e2e:
	go test ./tests/e2e/... -v -tags e2e -timeout 600s