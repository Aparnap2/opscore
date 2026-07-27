# OpsCore v3.0 - Cloud-Agnostic Go HTTP Server
# Multi-stage Docker build for minimal production image

# Stage 1: Build the Go binary
FROM golang:1.25-alpine AS builder

WORKDIR /build

RUN apk add --no-cache gcc musl-dev

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-w -s" -o opscore-server ./cmd/server

# Stage 2: Minimal runtime image
FROM alpine:3.20

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata curl

COPY --from=builder /build/opscore-server .

ENV PORT=8080
ENV APP_ENV=production

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD curl -sf http://localhost:8080/health || exit 1

CMD ["./opscore-server"]
