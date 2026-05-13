package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/aparna/opscore/cmd/functions/handlers"
	"github.com/aparna/opscore/internal/adapters/azure"
	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/middleware/ratelimit"
	"github.com/aparna/opscore/internal/providers"
)

// Config holds all Azure configuration
type Config struct {
	// Azure Storage
	StorageAccountName string
	StorageAccountKey  string

	// Azure Cosmos DB
	CosmosEndpoint string
	CosmosKey      string
	CosmosDatabase string

	// Azure Functions
	QueueName string

	// Slack
	SlackSigningSecret string
	SlackBotToken      string

	// App Insights
	AppInsightsKey string
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

// parseStorageConnectionString extracts account name and key from a storage connection string
func parseStorageConnectionString(connStr string) (accountName, accountKey string) {
	if connStr == "" {
		return "", ""
	}
	// Parse connection string like: DefaultEndpointsProtocol=https;AccountName=foo;AccountKey=bar;...
	parts := strings.Split(connStr, ";")
	for _, part := range parts {
		if strings.HasPrefix(part, "AccountName=") {
			accountName = strings.TrimPrefix(part, "AccountName=")
		}
		if strings.HasPrefix(part, "AccountKey=") {
			accountKey = strings.TrimPrefix(part, "AccountKey=")
		}
	}
	return accountName, accountKey
}

func main() {
	log.Println("Starting OpsCore Azure Functions...")

	// Parse storage connection string to get account name and key
	storageConnStr := getEnv("AzureWebJobsStorage", getEnv("STORAGE_CONNECTION", ""))
	storageAccountName, storageAccountKey := parseStorageConnectionString(storageConnStr)

	// Load configuration
	config := Config{
		StorageAccountName: storageAccountName,
		StorageAccountKey:  storageAccountKey,
		CosmosEndpoint:     getEnv("COSMOS_ENDPOINT", ""),
		CosmosKey:          getEnv("COSMOS_KEY", ""),
		CosmosDatabase:     getEnv("COSMOS_DATABASE", "opscore"),
		QueueName:          getEnv("QUEUE_NAME", "opscore-jobs"),
		SlackSigningSecret: getEnv("SLACK_SIGNING_SECRET", ""),
		SlackBotToken:      getEnv("SLACK_BOT_TOKEN", ""),
		AppInsightsKey:     getEnv("APPINSIGHTS_INSTRUMENTATIONKEY", ""),
	}

	log.Printf("Storage account: %s", config.StorageAccountName)
	log.Printf("Cosmos endpoint: %s", config.CosmosEndpoint)

	ctx := context.Background()

	// Initialize adapters - continue even if they fail to keep handler running
	var blobAdapter *azure.BlobAdapter
	var queueAdapter *azure.QueueAdapter
	var cosmosAdapter *azure.CosmosAdapter

	blobAdapter, _ = azure.NewBlobAdapter(azure.BlobConfig{
		AccountName: config.StorageAccountName,
		AccountKey:  config.StorageAccountKey,
	})

	queueAdapter, _ = azure.NewQueueAdapter(azure.QueueConfig{
		AccountName: config.StorageAccountName,
		AccountKey:  config.StorageAccountKey,
	})

	cosmosAdapter, _ = azure.NewCosmosAdapter(azure.CosmosConfig{
		Endpoint:     config.CosmosEndpoint,
		Key:          config.CosmosKey,
		DatabaseName: config.CosmosDatabase,
	})

	log.Printf("Blob adapter: %v", blobAdapter != nil)
	log.Printf("Queue adapter: %v", queueAdapter != nil)
	log.Printf("Cosmos adapter: %v", cosmosAdapter != nil)

	// Initialize agents
	validator := domain.NewIndiaValidator()

	// Document Agent
	var docAgent *agents.DocumentAgent
	if blobAdapter != nil && queueAdapter != nil && cosmosAdapter != nil {
		// Create OCR provider stub
		docAgent = agents.NewDocumentAgent(
			blobAdapter,
			queueAdapter,
			cosmosAdapter,
			nil, // OCR provider
			validator,
		)
	}

	// Vendor Agent
	var vendorAgent *agents.VendorAgent
	if cosmosAdapter != nil {
		vendorAgent = agents.NewVendorAgent(
			cosmosAdapter,
			validator,
			nil, // LLM provider
		)
	}

	// Compliance Agent
	var complianceAgent *agents.ComplianceAgent
	if cosmosAdapter != nil {
		complianceAgent = agents.NewComplianceAgent(
			cosmosAdapter,
			validator,
		)
	}

	// HITL Provider
	var hitlProvider providers.HITLProvider
	if config.SlackBotToken != "" {
		hitlProvider = providers.NewSlackHITLProvider(config.SlackBotToken)
	}

	// Store adapters in context for handlers
	ctx = context.WithValue(ctx, "blobAdapter", blobAdapter)
	ctx = context.WithValue(ctx, "queueAdapter", queueAdapter)
	ctx = context.WithValue(ctx, "cosmosAdapter", cosmosAdapter)
	ctx = context.WithValue(ctx, "docAgent", docAgent)
	ctx = context.WithValue(ctx, "vendorAgent", vendorAgent)
	ctx = context.WithValue(ctx, "complianceAgent", complianceAgent)
	ctx = context.WithValue(ctx, "hitlProvider", hitlProvider)
	ctx = context.WithValue(ctx, "config", config)

	// Register handlers - for Azure Functions this is done via function.json
	// This main function is primarily for local development testing
	_ = []interface{}{
		handlers.HTTPUploadHandler,
		handlers.HTTPJobStatusHandler,
		handlers.HTTPSlackWebhookHandler,
		handlers.HTTPVendorHandler,
		handlers.QueueDocumentHandler,
		handlers.QueueVendorHandler,
		handlers.QueueComplianceHandler,
		handlers.TimerScraperHandler,
		handlers.TimerTrustDecayHandler,
	}

	// For custom handler pattern, we'll run a simple HTTP server
	// that routes to the appropriate handler based on path
	serveHTTP(ctx)
}

// serveHTTP starts an HTTP server for the custom handler pattern
func serveHTTP(ctx context.Context) {
	// Read from FUNCTIONS_CUSTOMHANDLER_PORT (standard for custom handlers)
	// Fall back to FUNCTIONS_HTTPWORKER_PORT for backward compatibility
	port := getEnv("FUNCTIONS_CUSTOMHANDLER_PORT", getEnv("FUNCTIONS_HTTPWORKER_PORT", "8080"))
	log.Printf("Starting HTTP server on port %s", port)

	mux := http.NewServeMux()

	// Create context with all adapters for handlers
	baseCtx := ctx

	// Register HTTP trigger routes - folder names for Functions host (enableForwardingHttpRequest: false)
	mux.HandleFunc("/HttpHealth", func(w http.ResponseWriter, r *http.Request) {
		handlerCtx := context.WithValue(baseCtx, handlers.CtxKeyBlobAdapter, getCtxValue(baseCtx, "blobAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyQueueAdapter, getCtxValue(baseCtx, "queueAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyCosmosAdapter, getCtxValue(baseCtx, "cosmosAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyDocAgent, getCtxValue(baseCtx, "docAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyVendorAgent, getCtxValue(baseCtx, "vendorAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyComplianceAgent, getCtxValue(baseCtx, "complianceAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyHITLProvider, getCtxValue(baseCtx, "hitlProvider"))
		handlers.HTTPHealthHandler(handlerCtx, w, r)
	})

	mux.HandleFunc("/HttpUpload", func(w http.ResponseWriter, r *http.Request) {
		handlerCtx := context.WithValue(baseCtx, handlers.CtxKeyBlobAdapter, getCtxValue(baseCtx, "blobAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyQueueAdapter, getCtxValue(baseCtx, "queueAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyCosmosAdapter, getCtxValue(baseCtx, "cosmosAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyDocAgent, getCtxValue(baseCtx, "docAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyVendorAgent, getCtxValue(baseCtx, "vendorAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyComplianceAgent, getCtxValue(baseCtx, "complianceAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyHITLProvider, getCtxValue(baseCtx, "hitlProvider"))
		handlers.HTTPUploadHandler(handlerCtx, w, r)
	})

	mux.HandleFunc("/HttpJobStatus", func(w http.ResponseWriter, r *http.Request) {
		handlerCtx := context.WithValue(baseCtx, handlers.CtxKeyBlobAdapter, getCtxValue(baseCtx, "blobAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyQueueAdapter, getCtxValue(baseCtx, "queueAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyCosmosAdapter, getCtxValue(baseCtx, "cosmosAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyDocAgent, getCtxValue(baseCtx, "docAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyVendorAgent, getCtxValue(baseCtx, "vendorAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyComplianceAgent, getCtxValue(baseCtx, "complianceAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyHITLProvider, getCtxValue(baseCtx, "hitlProvider"))
		handlers.HTTPJobStatusHandler(handlerCtx, w, r)
	})

	mux.HandleFunc("/HttpSlackWebhook", func(w http.ResponseWriter, r *http.Request) {
		handlerCtx := context.WithValue(baseCtx, handlers.CtxKeyBlobAdapter, getCtxValue(baseCtx, "blobAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyQueueAdapter, getCtxValue(baseCtx, "queueAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyCosmosAdapter, getCtxValue(baseCtx, "cosmosAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyDocAgent, getCtxValue(baseCtx, "docAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyVendorAgent, getCtxValue(baseCtx, "vendorAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyComplianceAgent, getCtxValue(baseCtx, "complianceAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyHITLProvider, getCtxValue(baseCtx, "hitlProvider"))
		handlers.HTTPSlackWebhookHandler(handlerCtx, w, r)
	})

	// API paths for enableForwardingHttpRequest: true
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		handlerCtx := context.WithValue(baseCtx, handlers.CtxKeyBlobAdapter, getCtxValue(baseCtx, "blobAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyQueueAdapter, getCtxValue(baseCtx, "queueAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyCosmosAdapter, getCtxValue(baseCtx, "cosmosAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyDocAgent, getCtxValue(baseCtx, "docAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyVendorAgent, getCtxValue(baseCtx, "vendorAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyComplianceAgent, getCtxValue(baseCtx, "complianceAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyHITLProvider, getCtxValue(baseCtx, "hitlProvider"))
		handlers.HTTPHealthHandler(handlerCtx, w, r)
	})

	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		handlerCtx := context.WithValue(baseCtx, handlers.CtxKeyBlobAdapter, getCtxValue(baseCtx, "blobAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyQueueAdapter, getCtxValue(baseCtx, "queueAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyCosmosAdapter, getCtxValue(baseCtx, "cosmosAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyDocAgent, getCtxValue(baseCtx, "docAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyVendorAgent, getCtxValue(baseCtx, "vendorAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyComplianceAgent, getCtxValue(baseCtx, "complianceAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyHITLProvider, getCtxValue(baseCtx, "hitlProvider"))
		handlers.HTTPHealthHandler(handlerCtx, w, r)
	})

	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		handlerCtx := context.WithValue(baseCtx, handlers.CtxKeyBlobAdapter, getCtxValue(baseCtx, "blobAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyQueueAdapter, getCtxValue(baseCtx, "queueAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyCosmosAdapter, getCtxValue(baseCtx, "cosmosAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyDocAgent, getCtxValue(baseCtx, "docAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyVendorAgent, getCtxValue(baseCtx, "vendorAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyComplianceAgent, getCtxValue(baseCtx, "complianceAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyHITLProvider, getCtxValue(baseCtx, "hitlProvider"))
		handlers.HTTPUploadHandler(handlerCtx, w, r)
	})

	mux.HandleFunc("/api/upload", func(w http.ResponseWriter, r *http.Request) {
		handlerCtx := context.WithValue(baseCtx, handlers.CtxKeyBlobAdapter, getCtxValue(baseCtx, "blobAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyQueueAdapter, getCtxValue(baseCtx, "queueAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyCosmosAdapter, getCtxValue(baseCtx, "cosmosAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyDocAgent, getCtxValue(baseCtx, "docAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyVendorAgent, getCtxValue(baseCtx, "vendorAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyComplianceAgent, getCtxValue(baseCtx, "complianceAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyHITLProvider, getCtxValue(baseCtx, "hitlProvider"))
		handlers.HTTPUploadHandler(handlerCtx, w, r)
	})

	mux.HandleFunc("/jobs/", func(w http.ResponseWriter, r *http.Request) {
		handlerCtx := context.WithValue(baseCtx, handlers.CtxKeyBlobAdapter, getCtxValue(baseCtx, "blobAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyQueueAdapter, getCtxValue(baseCtx, "queueAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyCosmosAdapter, getCtxValue(baseCtx, "cosmosAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyDocAgent, getCtxValue(baseCtx, "docAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyVendorAgent, getCtxValue(baseCtx, "vendorAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyComplianceAgent, getCtxValue(baseCtx, "complianceAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyHITLProvider, getCtxValue(baseCtx, "hitlProvider"))
		handlers.HTTPJobStatusHandler(handlerCtx, w, r)
	})

	mux.HandleFunc("/api/jobs/", func(w http.ResponseWriter, r *http.Request) {
		handlerCtx := context.WithValue(baseCtx, handlers.CtxKeyBlobAdapter, getCtxValue(baseCtx, "blobAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyQueueAdapter, getCtxValue(baseCtx, "queueAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyCosmosAdapter, getCtxValue(baseCtx, "cosmosAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyDocAgent, getCtxValue(baseCtx, "docAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyVendorAgent, getCtxValue(baseCtx, "vendorAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyComplianceAgent, getCtxValue(baseCtx, "complianceAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyHITLProvider, getCtxValue(baseCtx, "hitlProvider"))
		handlers.HTTPJobStatusHandler(handlerCtx, w, r)
	})

	mux.HandleFunc("/slack/webhook", func(w http.ResponseWriter, r *http.Request) {
		handlerCtx := context.WithValue(baseCtx, handlers.CtxKeyBlobAdapter, getCtxValue(baseCtx, "blobAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyQueueAdapter, getCtxValue(baseCtx, "queueAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyCosmosAdapter, getCtxValue(baseCtx, "cosmosAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyDocAgent, getCtxValue(baseCtx, "docAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyVendorAgent, getCtxValue(baseCtx, "vendorAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyComplianceAgent, getCtxValue(baseCtx, "complianceAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyHITLProvider, getCtxValue(baseCtx, "hitlProvider"))
		handlers.HTTPSlackWebhookHandler(handlerCtx, w, r)
	})

	mux.HandleFunc("/api/slack/webhook", func(w http.ResponseWriter, r *http.Request) {
		handlerCtx := context.WithValue(baseCtx, handlers.CtxKeyBlobAdapter, getCtxValue(baseCtx, "blobAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyQueueAdapter, getCtxValue(baseCtx, "queueAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyCosmosAdapter, getCtxValue(baseCtx, "cosmosAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyDocAgent, getCtxValue(baseCtx, "docAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyVendorAgent, getCtxValue(baseCtx, "vendorAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyComplianceAgent, getCtxValue(baseCtx, "complianceAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyHITLProvider, getCtxValue(baseCtx, "hitlProvider"))
		handlers.HTTPSlackWebhookHandler(handlerCtx, w, r)
	})

	// Vendor HTTP endpoints
	mux.HandleFunc("/HttpVendor", func(w http.ResponseWriter, r *http.Request) {
		handlerCtx := context.WithValue(baseCtx, handlers.CtxKeyBlobAdapter, getCtxValue(baseCtx, "blobAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyQueueAdapter, getCtxValue(baseCtx, "queueAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyCosmosAdapter, getCtxValue(baseCtx, "cosmosAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyDocAgent, getCtxValue(baseCtx, "docAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyVendorAgent, getCtxValue(baseCtx, "vendorAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyComplianceAgent, getCtxValue(baseCtx, "complianceAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyHITLProvider, getCtxValue(baseCtx, "hitlProvider"))
		handlers.HTTPVendorHandler(handlerCtx, w, r)
	})

	mux.HandleFunc("/vendors", func(w http.ResponseWriter, r *http.Request) {
		handlerCtx := context.WithValue(baseCtx, handlers.CtxKeyBlobAdapter, getCtxValue(baseCtx, "blobAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyQueueAdapter, getCtxValue(baseCtx, "queueAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyCosmosAdapter, getCtxValue(baseCtx, "cosmosAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyDocAgent, getCtxValue(baseCtx, "docAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyVendorAgent, getCtxValue(baseCtx, "vendorAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyComplianceAgent, getCtxValue(baseCtx, "complianceAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyHITLProvider, getCtxValue(baseCtx, "hitlProvider"))
		handlers.HTTPVendorHandler(handlerCtx, w, r)
	})

	mux.HandleFunc("/api/vendors", func(w http.ResponseWriter, r *http.Request) {
		handlerCtx := context.WithValue(baseCtx, handlers.CtxKeyBlobAdapter, getCtxValue(baseCtx, "blobAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyQueueAdapter, getCtxValue(baseCtx, "queueAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyCosmosAdapter, getCtxValue(baseCtx, "cosmosAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyDocAgent, getCtxValue(baseCtx, "docAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyVendorAgent, getCtxValue(baseCtx, "vendorAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyComplianceAgent, getCtxValue(baseCtx, "complianceAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyHITLProvider, getCtxValue(baseCtx, "hitlProvider"))
		handlers.HTTPVendorHandler(handlerCtx, w, r)
	})

	// Vendor ID lookup routes
	mux.HandleFunc("/vendors/", func(w http.ResponseWriter, r *http.Request) {
		handlerCtx := context.WithValue(baseCtx, handlers.CtxKeyBlobAdapter, getCtxValue(baseCtx, "blobAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyQueueAdapter, getCtxValue(baseCtx, "queueAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyCosmosAdapter, getCtxValue(baseCtx, "cosmosAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyDocAgent, getCtxValue(baseCtx, "docAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyVendorAgent, getCtxValue(baseCtx, "vendorAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyComplianceAgent, getCtxValue(baseCtx, "complianceAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyHITLProvider, getCtxValue(baseCtx, "hitlProvider"))
		handlers.HTTPVendorHandler(handlerCtx, w, r)
	})

	mux.HandleFunc("/api/vendors/", func(w http.ResponseWriter, r *http.Request) {
		handlerCtx := context.WithValue(baseCtx, handlers.CtxKeyBlobAdapter, getCtxValue(baseCtx, "blobAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyQueueAdapter, getCtxValue(baseCtx, "queueAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyCosmosAdapter, getCtxValue(baseCtx, "cosmosAdapter"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyDocAgent, getCtxValue(baseCtx, "docAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyVendorAgent, getCtxValue(baseCtx, "vendorAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyComplianceAgent, getCtxValue(baseCtx, "complianceAgent"))
		handlerCtx = context.WithValue(handlerCtx, handlers.CtxKeyHITLProvider, getCtxValue(baseCtx, "hitlProvider"))
		handlers.HTTPVendorHandler(handlerCtx, w, r)
	})

	// Queue triggers - use envelope pattern (placeholder)
	mux.HandleFunc("/QueueDocument", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"Outputs":{},"Logs":["QueueDocument trigger"]}`))
	})
	mux.HandleFunc("/QueueVendor", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"Outputs":{},"Logs":["QueueVendor trigger"]}`))
	})
	mux.HandleFunc("/QueueCompliance", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"Outputs":{},"Logs":["QueueCompliance trigger"]}`))
	})

	// Timer triggers - use envelope pattern
	mux.HandleFunc("/TimerScraper", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"Outputs":{},"Logs":["TimerScraper trigger"]}`))
	})
	mux.HandleFunc("/TimerTrustDecay", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"Outputs":{},"Logs":["TimerTrustDecay trigger"]}`))
	})

	addr := fmt.Sprintf(":%s", port)
	log.Printf("OpsCore Functions listening on %s", addr)

	// Initialize rate limiter from environment
	rl := ratelimit.NewFromEnv()
	log.Printf("Rate limiter: RPS=%v, Burst=%v", getEnv("RATE_LIMIT_RPS", "10"), getEnv("RATE_LIMIT_BURST", "20"))

	// Wrap mux with rate limiting middleware
	handler := rl.Middleware(mux)

	// Catch-all for debugging
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Caught request: %s %s", r.Method, r.URL.Path)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		w.Write([]byte("OpsCore handler running. Path: " + r.URL.Path))
	})

	if err := http.ListenAndServe(addr, handler); err != nil && err != http.ErrServerClosed {
		log.Printf("HTTP server error: %v", err)
	}
}

// getCtxValue safely retrieves values from the context
func getCtxValue(ctx context.Context, key string) interface{} {
	switch key {
	case "blobAdapter":
		return ctx.Value("blobAdapter")
	case "queueAdapter":
		return ctx.Value("queueAdapter")
	case "cosmosAdapter":
		return ctx.Value("cosmosAdapter")
	case "docAgent":
		return ctx.Value("docAgent")
	case "vendorAgent":
		return ctx.Value("vendorAgent")
	case "complianceAgent":
		return ctx.Value("complianceAgent")
	case "hitlProvider":
		return ctx.Value("hitlProvider")
	case "config":
		return ctx.Value("config")
	default:
		return nil
	}
}
