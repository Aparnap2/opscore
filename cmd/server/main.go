package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/aparna/opscore/internal/adapters/minio"
	"github.com/aparna/opscore/internal/adapters/openrouter"
	"github.com/aparna/opscore/internal/adapters/postgres"
	"github.com/aparna/opscore/internal/adapters/queue"
	"github.com/aparna/opscore/internal/adapters/sarvam"
	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/middleware/auth"
	"github.com/aparna/opscore/internal/middleware/ratelimit"
	"github.com/aparna/opscore/internal/middleware/tenant"
	"github.com/aparna/opscore/internal/middleware/usage"
	"github.com/aparna/opscore/internal/providers"
	"github.com/aparna/opscore/internal/telemetry"
)

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// ---------------------------------------------------------------------------
// Container and queue name constants
// ---------------------------------------------------------------------------

const (
	ContainerDocuments  = "documents"
	ContainerVendors    = "vendors"
	ContainerCompliance = "compliance"

	QueueDocument   = "document-queue"
	QueueVendor     = "vendor-queue"
	QueueCompliance = "compliance-queue"
	QueueSignal     = "signal-queue"
)

// ---------------------------------------------------------------------------
// Request / Response types
// ---------------------------------------------------------------------------

type UploadResponse struct {
	JobID   string `json:"job_id"`
	Status  string `json:"status"`
	BlobURL string `json:"blob_url,omitempty"`
	Message string `json:"message,omitempty"`
}

type JobStatusResponse struct {
	ID           string      `json:"id"`
	WorkflowType string      `json:"workflow_type"`
	Status       string      `json:"status"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
	Output       interface{} `json:"output,omitempty"`
	Error        string      `json:"error,omitempty"`
}

type VendorResponse struct {
	VendorID string `json:"vendor_id"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
}

type VendorGetResponse struct {
	VendorID  string    `json:"vendor_id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	GSTNumber string    `json:"gst_number,omitempty"`
	PANNumber string    `json:"pan_number,omitempty"`
	IFSCCode  string    `json:"ifsc_code,omitempty"`
	RiskScore int       `json:"risk_score"`
	RiskTier  string    `json:"risk_tier"`
	Approved  bool      `json:"approved"`
	TrustTier string    `json:"trust_tier"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type SlackWebhookRequest struct {
	Event     map[string]any `json:"event,omitempty"`
	Type      string         `json:"type"`
	Challenge string         `json:"challenge,omitempty"`
}

type SlackWebhookResponse struct {
	Challenge string `json:"challenge,omitempty"`
}

type HealthResponse struct {
	Status    string            `json:"status"`
	Timestamp time.Time         `json:"timestamp"`
	Services  map[string]string `json:"services"`
}

// ---------------------------------------------------------------------------
// Server dependencies
// ---------------------------------------------------------------------------

type ServerDeps struct {
	db        *postgres.Adapter
	storage   *minio.Adapter
	rq        providers.QueueProvider
	docAgent  *agents.DocumentAgent
	vendAgent *agents.VendorAgent
	compAgent *agents.ComplianceAgent
	slack     *providers.SlackHITLProvider
	tracer    providers.TracingProvider
	worker    *Worker
	usageMW   *usage.Middleware
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func parseInt(s string) int {
	var v int
	fmt.Sscanf(s, "%d", &v)
	return v
}

func extractJobIDFromPath(path, prefix, suffix string) string {
	idx := len(prefix)
	end := -1
	for i := 0; i < len(path)-idx; i++ {
		if path[idx+i] == '/' || path[idx+i] == '?' {
			end = idx + i
			break
		}
	}
	if end == -1 {
		end = len(path)
	}
	return path[idx:end]
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	slog.Info("Starting OpsCore server...")

	ctx := context.Background()

	// Read environment variables.
	databaseURL := os.Getenv("DATABASE_URL")
	s3Endpoint := os.Getenv("S3_ENDPOINT")
	s3AccessKey := os.Getenv("S3_ACCESS_KEY")
	s3SecretKey := os.Getenv("S3_SECRET_KEY")
	redisAddr := os.Getenv("REDIS_ADDR")
	redisPassword := os.Getenv("REDIS_PASSWORD")
	sarvamAPIKey := os.Getenv("SARVAM_API_KEY")
	slackBotToken := os.Getenv("SLACK_BOT_TOKEN")
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Initialize PostgreSQL adapter.
	if databaseURL == "" {
		slog.Error("DATABASE_URL environment variable is required")
		os.Exit(1)
	}
	dbAdapter, err := postgres.NewAdapter(ctx, databaseURL)
	if err != nil {
		slog.Error("Failed to initialize PostgreSQL adapter", "err", err)
		os.Exit(1)
	}
	defer dbAdapter.Close()
	slog.Info("PostgreSQL adapter initialized")

	// Initialize MinIO storage adapter.
	if s3Endpoint == "" {
		slog.Error("S3_ENDPOINT environment variable is required")
		os.Exit(1)
	}
	storageAdapter, err := minio.NewAdapter(s3Endpoint, s3AccessKey, s3SecretKey, false)
	if err != nil {
		slog.Error("Failed to initialize MinIO adapter", "err", err)
		os.Exit(1)
	}
	slog.Info("MinIO adapter initialized")

	// Initialize queue adapter (PubSub if emulator configured, else Redis).
	var queueAdapter providers.QueueProvider
	pubsubHost := os.Getenv("PUBSUB_EMULATOR_HOST")
	if pubsubHost != "" {
		projectID := os.Getenv("PUBSUB_PROJECT_ID")
		if projectID == "" {
			projectID = "opscore-local"
		}
		qa, err := queue.NewPubSubAdapter(ctx, projectID)
		if err != nil {
			slog.Error("Failed to initialize PubSub adapter", "err", err)
			os.Exit(1)
		}
		queueAdapter = qa
		slog.Info("PubSub adapter initialized", "emulator", pubsubHost)
	} else {
		if redisAddr == "" {
			redisAddr = "localhost:6379"
		}
		qa, err := queue.NewRedisAdapter(redisAddr, redisPassword, 0)
		if err != nil {
			slog.Error("Failed to initialize Redis adapter", "err", err)
			os.Exit(1)
		}
		queueAdapter = qa
		slog.Info("Redis adapter initialized")
	}

	// Close queue adapter if it supports Close() (RedisAdapter does, PubSubAdapter does not).
	if closer, ok := queueAdapter.(interface{ Close() error }); ok {
		defer closer.Close()
	}

	// Initialize LLM adapters (Sarvam preferred, OpenRouter fallback).
	var ocrProvider providers.OCRProvider
	var llmProvider providers.LLMProvider
	if sarvamAPIKey != "" {
		ocrProvider = sarvam.NewOCRAdapter(sarvam.OCRConfig{APIKey: sarvamAPIKey})
		llmProvider = sarvam.NewLLMAdapter(sarvam.LLMConfig{APIKey: sarvamAPIKey})
	} else if openRouterAPIKey := os.Getenv("OPENROUTER_API_KEY"); openRouterAPIKey != "" {
		slog.Info("Using OpenRouter LLM provider", "model", "tencent/hy3:free")
		llmProvider = openrouter.NewAdapter(openrouter.Config{
			APIKey: openRouterAPIKey,
		})
	}

	// Initialize tracing provider.
	var tracer providers.TracingProvider
	langfuseBaseURL := os.Getenv("LANGFUSE_BASE_URL")
	langfuseSecretKey := os.Getenv("LANGFUSE_SECRET_KEY")
	langfusePublicKey := os.Getenv("LANGFUSE_PUBLIC_KEY")
	if langfuseBaseURL != "" && langfuseSecretKey != "" && langfusePublicKey != "" {
		tracer = telemetry.NewLangfuseProvider(langfuseBaseURL, langfuseSecretKey, langfusePublicKey)
		slog.Info("Langfuse tracing provider initialized")
	} else {
		tracer = &telemetry.NoopTracer{}
		slog.Info("Langfuse credentials not set — using no-op tracer")
	}

	// Initialize agents.
	validator := domain.NewIndiaValidator()
	docAgent := agents.NewDocumentAgent(storageAdapter, queueAdapter, dbAdapter, ocrProvider, validator, tracer)
	vendAgent := agents.NewVendorAgent(dbAdapter, validator, llmProvider, tracer)
	compAgent := agents.NewComplianceAgent(dbAdapter, validator, llmProvider)

	// Initialize Slack HITL provider.
	slackChannel := os.Getenv("SLACK_HITL_CHANNEL")
	var slackHITL *providers.SlackHITLProvider
	if slackBotToken != "" {
		slackHITL = providers.NewSlackHITLProvider(slackBotToken)
		if slackChannel != "" {
			slackHITL.WithChannel(slackChannel)
		}
		slog.Info("Slack HITL provider initialized")
	} else {
		slog.Info("SLACK_BOT_TOKEN not set — Slack HITL disabled")
	}

	// Initialize usage middleware for tracking and enforcing tenant usage limits.
	usageMW := usage.NewMiddleware(dbAdapter)
	slog.Info("Usage middleware initialized")

	// Create worker.
	worker := &Worker{
		db:          dbAdapter,
		queue:       queueAdapter,
		docAgent:    docAgent,
		signalAgent: agents.NewSignalAgent(docAgent),
		vendAgent:   vendAgent,
		slack:       slackHITL,
		tracer:      tracer,
	}

	deps := &ServerDeps{
		db:        dbAdapter,
		storage:   storageAdapter,
		rq:        queueAdapter,
		docAgent:  docAgent,
		vendAgent: vendAgent,
		compAgent: compAgent,
		slack:     slackHITL,
		tracer:    tracer,
		worker:    worker,
		usageMW:   usageMW,
	}

	// Initialize rate limiter (reads RATE_LIMIT_RPS / RATE_LIMIT_BURST env vars; defaults: 10 rps, burst 20).
	rl := ratelimit.NewFromEnv()
	slog.Info("Rate limiter initialized")

	// Initialize tenant middleware with a static provider.
	// The provider only knows about the "default" tenant; a DB-backed
	// implementation should replace this once tenant management exists.
	tenantMW := tenant.New(staticTenantProvider{})
	slog.Info("Tenant middleware initialized")

	// Initialize auth middleware with static API keys for dev.
	// In production, replace with a database-backed AuthProvider.
	ownerAPIKey := os.Getenv("OWNER_API_KEY")
	if ownerAPIKey == "" {
		ownerAPIKey = "owner-dev-key"
	}
	opsAdminAPIKey := os.Getenv("OPS_ADMIN_API_KEY")
	if opsAdminAPIKey == "" {
		opsAdminAPIKey = "ops-admin-dev-key"
	}
	reviewerAPIKey := os.Getenv("REVIEWER_API_KEY")
	if reviewerAPIKey == "" {
		reviewerAPIKey = "reviewer-dev-key"
	}
	authKeys := map[string]*domain.User{
		ownerAPIKey: {
			ID:       "dev-owner",
			TenantID: "default",
			Email:    "owner@opscore.dev",
			Role:     domain.RoleOwner,
			Name:     "Dev Owner",
		},
		opsAdminAPIKey: {
			ID:       "dev-opsadmin",
			TenantID: "default",
			Email:    "opsadmin@opscore.dev",
			Role:     domain.RoleOpsAdmin,
			Name:     "Dev Ops Admin",
		},
		reviewerAPIKey: {
			ID:       "dev-reviewer",
			TenantID: "default",
			Email:    "reviewer@opscore.dev",
			Role:     domain.RoleReviewer,
			Name:     "Dev Reviewer",
		},
	}
	authProvider := auth.NewStaticAPIKeyProvider(authKeys)
	authMW := auth.New(authProvider)
	slog.Info("Auth middleware initialized with static API keys")

	// Register routes.
	// Public routes (no auth): health check and Slack webhook.
	// All other routes require authentication.
	mux := http.NewServeMux()
	mux.Handle("/health", rl.Middleware(http.HandlerFunc(deps.healthHandler)))
	mux.HandleFunc("/slack/webhook", deps.slackWebhookHandler)

	// Protected routes: all wrapped with auth middleware and
	// permission-based access control where appropriate.
	uploadHandler := usageMW.CheckLimit(domain.MetricDocumentsUploaded)(
		usageMW.IncrementOnResponse(domain.MetricDocumentsUploaded)(
			http.HandlerFunc(deps.uploadHandler),
		),
	)
	mux.Handle("/upload", authMW.Wrap(
		rl.Middleware(
			auth.RequirePermission(domain.PermissionDocumentUpload)(uploadHandler),
		),
	))
	signalUploadHandler := usageMW.CheckLimit(domain.MetricDocumentsUploaded)(
		usageMW.IncrementOnResponse(domain.MetricDocumentsUploaded)(
			http.HandlerFunc(deps.signalsUploadHandler),
		),
	)
	mux.Handle("/signals/upload", authMW.Wrap(
		rl.Middleware(
			auth.RequirePermission(domain.PermissionDocumentUpload)(signalUploadHandler),
		),
	))
	mux.Handle("/status/summary", authMW.Wrap(rl.Middleware(http.HandlerFunc(deps.statusSummaryHandler))))
	mux.Handle("/jobs/recent", authMW.Wrap(rl.Middleware(http.HandlerFunc(deps.recentJobsHandler))))
	mux.Handle("/jobs/", authMW.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/audit") {
			deps.jobAuditHandler(w, r)
			return
		}
		deps.jobStatusHandler(w, r)
	})))
	mux.Handle("/vendors/risky", authMW.Wrap(http.HandlerFunc(deps.riskyVendorsHandler)))
	mux.Handle("/vendors", authMW.Wrap(http.HandlerFunc(deps.vendorHandler)))
	mux.Handle("/vendors/", authMW.Wrap(http.HandlerFunc(deps.vendorHandler)))
	mux.Handle("/compliance/recent", authMW.Wrap(http.HandlerFunc(deps.recentComplianceHandler)))
	mux.Handle("/metrics/llm-summary", authMW.Wrap(http.HandlerFunc(deps.llmMetricsSummaryHandler)))
	mux.Handle("/metrics/workflow-summary", authMW.Wrap(http.HandlerFunc(deps.workflowMetricsSummaryHandler)))

	// Admin endpoints — review queue and dashboard.
	mux.Handle("/admin/review-queue", authMW.Wrap(rl.Middleware(auth.RequirePermission(domain.PermissionReviewQueue)(http.HandlerFunc(deps.reviewQueueHandler)))))
	mux.Handle("/admin/review-queue/", authMW.Wrap(rl.Middleware(auth.RequirePermission(domain.PermissionReviewQueue)(http.HandlerFunc(deps.reviewQueueByIDHandler)))))
	mux.Handle("/admin/dashboard", authMW.Wrap(rl.Middleware(auth.RequirePermission(domain.PermissionMetricsView)(http.HandlerFunc(deps.adminDashboardHandler)))))
	mux.Handle("/admin/usage", authMW.Wrap(rl.Middleware(auth.RequirePermission(domain.PermissionAdmin)(http.HandlerFunc(deps.adminUsageHandler)))))

	// Exception case read + lifecycle.
	// GET /exceptions            -> list (read permission).
	// GET /exceptions/{id}       -> single (read permission).
	// POST /exceptions/{id}/resolve -> lifecycle transition (review-queue permission).
	mux.Handle("/exceptions", authMW.Wrap(rl.Middleware(http.HandlerFunc(deps.exceptionsHandler))))
	mux.Handle("/exceptions/", authMW.Wrap(rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			auth.RequirePermission(domain.PermissionReviewQueue)(http.HandlerFunc(deps.resolveExceptionHandler)).ServeHTTP(w, r)
			return
		}
		http.HandlerFunc(deps.exceptionByIDHandler).ServeHTTP(w, r)
	}))))
	mux.Handle("/ops/summary", authMW.Wrap(rl.Middleware(http.HandlerFunc(deps.opsSummaryHandler))))

	// Manufacturing signal read: GET /signals/{id} probes PO -> GRN -> Invoice.
	mux.Handle("/signals/", authMW.Wrap(rl.Middleware(http.HandlerFunc(deps.signalsHandler))))

	// Start worker goroutines.
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	worker.Start(workerCtx)
	slog.Info("Queue worker started")

	// Start compliance scheduler (hourly).
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				slog.Info("Compliance scheduler: starting periodic check")
				compJob := &agents.ComplianceJob{
					TenantID: "default",
					JobID:    uuid.New().String(),
					JobType:  "scrape",
					Items:    []agents.ComplianceItem{},
				}
				result, err := compAgent.ProcessCompliance(workerCtx, compJob)
				if err != nil {
					slog.Error("Compliance check failed", "err", err)
				} else {
					slog.Info("Compliance check completed", "result", result)
				}
			}
		}
	}()
	slog.Info("Compliance scheduler started (hourly)")

	// Wrap the entire mux with tenant middleware to inject tenant info
	// into every request context. All handlers can then use
	// tenant.FromContext(r.Context()) to access the resolved tenant.
	var handler http.Handler = mux
	handler = tenantMW.Wrap(handler)

	// Wrap with transaction middleware that creates a database transaction
	// per request, sets the RLS tenant context inside the transaction, and
	// commits on success (2xx) or rolls back on failure. This ensures that
	// PostgreSQL Row-Level Security is active for every tenant-scoped query.
	handler = txMiddleware(dbAdapter, handler)
	slog.Info("Transaction middleware initialized")

	// Start HTTP server with graceful shutdown.
	server := &http.Server{Addr: fmt.Sprintf(":%s", port), Handler: handler}
	go func() {
		slog.Info("OpsCore server listening", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server error", "err", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal.
	shutdownCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-shutdownCtx.Done()

	slog.Info("Shutting down server...")

	// Stop worker first.
	workerCancel()
	slog.Info("Worker stopped")

	// Shutdown HTTP server with timeout.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server shutdown error", "err", err)
	}

	slog.Info("Server stopped gracefully")
}
