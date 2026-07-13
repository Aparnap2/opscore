package agentic

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aparna/opscore/internal/adapters/minio"
	"github.com/aparna/opscore/internal/adapters/postgres"
	"github.com/aparna/opscore/internal/adapters/queue"
	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
	"github.com/aparna/opscore/internal/telemetry"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// ---------------------------------------------------------------------------
// TestInfra — real Docker containers for agentic trajectory tests
// ---------------------------------------------------------------------------

// TestInfra holds running Docker containers for a single test case.
// Each test gets its own isolated infrastructure.
type TestInfra struct {
	DB      *StubDB
	Storage providers.StorageProvider
	Queue   providers.QueueProvider

	postgresC testcontainers.Container
	minioC    testcontainers.Container
	redisC    testcontainers.Container
	cleanup   func()
}

// StartInfra starts PostgreSQL, MinIO, and Redis containers via Testcontainers.
// It blocks until all containers are healthy and returns a ready-to-use TestInfra.
//
// The caller MUST call ti.Close() in a t.Cleanup to avoid orphan containers.
func StartInfra(t *testing.T) (*TestInfra, error) {
	t.Helper()
	ctx := context.Background()

	// -----------------------------------------------------------------------
	// 1. PostgreSQL 16
	// -----------------------------------------------------------------------
	t.Log("Starting PostgreSQL container...")
	postgresC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "postgres:16-alpine",
			Env: map[string]string{
				"POSTGRES_DB":       "opscore",
				"POSTGRES_USER":     "opscore",
				"POSTGRES_PASSWORD": "opscore",
			},
			ExposedPorts: []string{"5432/tcp"},
			WaitingFor: wait.ForAll(
				wait.ForLog("database system is ready to accept connections"),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		return nil, fmt.Errorf("starting postgres: %w", err)
	}

	pgPort, err := postgresC.MappedPort(ctx, "5432")
	if err != nil {
		postgresC.Terminate(ctx)
		return nil, fmt.Errorf("getting postgres port: %w", err)
	}
	connStr := fmt.Sprintf("postgres://opscore:opscore@localhost:%s/opscore?sslmode=disable", pgPort.Port())

	dbAdapter, err := postgres.NewAdapter(ctx, connStr)
	if err != nil {
		postgresC.Terminate(ctx)
		return nil, fmt.Errorf("creating postgres adapter: %w", err)
	}

	// Wrap in StubDB for tracking
	stubDB := NewStubDB(dbAdapter)
	t.Logf("PostgreSQL ready on port %s", pgPort.Port())

	// -----------------------------------------------------------------------
	// 2. MinIO (S3-compatible object storage)
	// -----------------------------------------------------------------------
	t.Log("Starting MinIO container...")
	minioC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "minio/minio:latest",
			Env: map[string]string{
				"MINIO_ROOT_USER":     "minioadmin",
				"MINIO_ROOT_PASSWORD": "minioadmin",
			},
			ExposedPorts: []string{"9000/tcp"},
			Cmd:          []string{"server", "/data"},
			WaitingFor: wait.ForAll(
				wait.ForLog("API:"),
				wait.ForListeningPort("9000/tcp"),
			).WithDeadline(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		postgresC.Terminate(ctx)
		dbAdapter.Close()
		return nil, fmt.Errorf("starting minio: %w", err)
	}

	minioPort, err := minioC.MappedPort(ctx, "9000")
	if err != nil {
		postgresC.Terminate(ctx)
		minioC.Terminate(ctx)
		dbAdapter.Close()
		return nil, fmt.Errorf("getting minio port: %w", err)
	}

	storageAdapter, err := minio.NewAdapter(
		fmt.Sprintf("localhost:%s", minioPort.Port()),
		"minioadmin",
		"minioadmin",
		false, // useSSL=false for local tests
	)
	if err != nil {
		postgresC.Terminate(ctx)
		minioC.Terminate(ctx)
		dbAdapter.Close()
		return nil, fmt.Errorf("creating minio adapter: %w", err)
	}
	t.Logf("MinIO ready on port %s", minioPort.Port())

	// -----------------------------------------------------------------------
	// 3. Redis 7 (queue backend)
	// -----------------------------------------------------------------------
	t.Log("Starting Redis container...")
	redisC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "redis:7-alpine",
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor: wait.ForAll(
				wait.ForLog("Ready to accept connections"),
				wait.ForListeningPort("6379/tcp"),
			).WithDeadline(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		postgresC.Terminate(ctx)
		minioC.Terminate(ctx)
		dbAdapter.Close()
		return nil, fmt.Errorf("starting redis: %w", err)
	}

	redisPort, err := redisC.MappedPort(ctx, "6379")
	if err != nil {
		postgresC.Terminate(ctx)
		minioC.Terminate(ctx)
		redisC.Terminate(ctx)
		dbAdapter.Close()
		return nil, fmt.Errorf("getting redis port: %w", err)
	}

	queueAdapter, err := queue.NewRedisAdapter(
		fmt.Sprintf("localhost:%s", redisPort.Port()),
		"", // no password
		0,  // default DB
	)
	if err != nil {
		postgresC.Terminate(ctx)
		minioC.Terminate(ctx)
		redisC.Terminate(ctx)
		dbAdapter.Close()
		return nil, fmt.Errorf("creating redis adapter: %w", err)
	}
	t.Logf("Redis ready on port %s", redisPort.Port())

	// -----------------------------------------------------------------------
	// Cleanup — terminates all containers and closes adapters
	// -----------------------------------------------------------------------
	cleanup := func() {
		dbAdapter.Close()
		queueAdapter.Close()
		postgresC.Terminate(context.Background())
		minioC.Terminate(context.Background())
		redisC.Terminate(context.Background())
	}

	return &TestInfra{
		DB:        stubDB,
		Storage:   storageAdapter,
		Queue:     queueAdapter,
		postgresC: postgresC,
		minioC:    minioC,
		redisC:    redisC,
		cleanup:   cleanup,
	}, nil
}

// Close terminates all containers and releases resources.
func (ti *TestInfra) Close() {
	if ti.cleanup != nil {
		ti.cleanup()
	}
}

// StopPostgres stops the PostgreSQL container to simulate a database outage.
func (ti *TestInfra) StopPostgres(ctx context.Context) error {
	return ti.postgresC.Stop(ctx, nil)
}

// StartPostgres starts the PostgreSQL container after a stop.
func (ti *TestInfra) StartPostgres(ctx context.Context) error {
	return ti.postgresC.Start(ctx)
}

// StopMinio stops the MinIO container to simulate a storage outage.
func (ti *TestInfra) StopMinio(ctx context.Context) error {
	return ti.minioC.Stop(ctx, nil)
}

// StartMinio starts the MinIO container after a stop.
func (ti *TestInfra) StartMinio(ctx context.Context) error {
	return ti.minioC.Start(ctx)
}

// StopRedis stops the Redis container to simulate a queue outage.
func (ti *TestInfra) StopRedis(ctx context.Context) error {
	return ti.redisC.Stop(ctx, nil)
}

// StartRedis starts the Redis container after a stop.
func (ti *TestInfra) StartRedis(ctx context.Context) error {
	return ti.redisC.Start(ctx)
}

// PostgresPort returns the mapped port of the PostgreSQL container.
func (ti *TestInfra) PostgresPort(ctx context.Context) (string, error) {
	p, err := ti.postgresC.MappedPort(ctx, "5432")
	if err != nil {
		return "", err
	}
	return p.Port(), nil
}

// MinioPort returns the mapped port of the MinIO container.
func (ti *TestInfra) MinioPort(ctx context.Context) (string, error) {
	p, err := ti.minioC.MappedPort(ctx, "9000")
	if err != nil {
		return "", err
	}
	return p.Port(), nil
}

// RedisAddr returns the address of the Redis container.
func (ti *TestInfra) RedisAddr(ctx context.Context) (string, error) {
	p, err := ti.redisC.MappedPort(ctx, "6379")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("localhost:%s", p.Port()), nil
}

// UploadTestFile uploads a dummy file to MinIO and returns the blob URL.
// The blobKey can contain trigger words (e.g. "confidence_0.95") that the
// StubOCR will use to determine its response.
func (ti *TestInfra) UploadTestFile(ctx context.Context, bucket, blobKey string) (string, error) {
	content := fmt.Sprintf("test document content for %s", blobKey)
	return ti.Storage.Upload(ctx, bucket, blobKey, bytes.NewReader([]byte(content)), "application/pdf")
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// DefaultTestContext returns a context with timeout for test operations.
func DefaultTestContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// ShortContext returns a context with a short timeout, useful for testing
// operations that should timeout or fail quickly (e.g. empty queue dequeue).
func ShortContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 3*time.Second)
}

// CreateTestDocumentAgent creates a DocumentAgent wired with stubs and real infra.
func CreateTestDocumentAgent(
	stubOCR providers.OCRProvider,
	stubLLM providers.LLMProvider,
	ti *TestInfra,
) *agents.DocumentAgent {
	validator := domain.NewIndiaValidator()
	tracer := &telemetry.NoopTracer{}
	return agents.NewDocumentAgent(
		ti.Storage,
		ti.Queue,
		ti.DB,
		stubOCR,
		validator,
		tracer,
	)
}

// CreateTestVendorAgent creates a VendorAgent wired with stubs and real infra.
func CreateTestVendorAgent(
	stubLLM providers.LLMProvider,
	ti *TestInfra,
) *agents.VendorAgent {
	validator := domain.NewIndiaValidator()
	tracer := &telemetry.NoopTracer{}
	return agents.NewVendorAgent(
		ti.DB,
		validator,
		stubLLM,
		tracer,
	)
}

// CreateInitialJob creates a PENDING job in the database and returns it.
// This simulates what the upload handler does before enqueueing.
func CreateInitialJob(ctx context.Context, ti *TestInfra, jobID, tenantID string) *domain.Job {
	now := time.Now()
	job := &domain.Job{
		ID:           jobID,
		TenantID:     tenantID,
		WorkflowType: domain.WorkflowDocumentIngestion,
		Status:       domain.JobStatusPending,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	// Use the underlying real DB for the initial upsert so StubDB tracking
	// only captures agent-level operations.
	ti.DB.UpsertJob(ctx, job)
	return job
}

// MustString is a test helper that extracts a string value from a map.
func MustString(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("key %q not found in result map", key)
		return ""
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("key %q is not a string (got %T)", key, v)
	}
	return s
}

// MustBool is a test helper that extracts a bool value from a map.
func MustBool(t *testing.T, m map[string]any, key string) bool {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("key %q not found in result map", key)
		return false
	}
	b, ok := v.(bool)
	if !ok {
		t.Fatalf("key %q is not a bool (got %T)", key, v)
	}
	return b
}

// MustFloat is a test helper that extracts a float64 value from a map.
func MustFloat(t *testing.T, m map[string]any, key string) float64 {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("key %q not found in result map", key)
		return 0
	}
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("key %q is not a float64 (got %T)", key, v)
	}
	return f
}

// GenerateJobID returns a unique job ID for testing.
func GenerateJobID() string {
	return "test-" + uuid.New().String()
}

// ReadAll reads all bytes from a reader and returns the string content.
func ReadAll(r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ContainsAny checks if a string contains any of the given substrings.
func ContainsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
