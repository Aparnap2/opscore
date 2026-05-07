package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/aparna/opscore/cmd/functions/handlers"
	"github.com/aparna/opscore/internal/adapters/azure"
	"github.com/aparna/opscore/internal/agents"
	"github.com/aparna/opscore/internal/domain"
	"github.com/aparna/opscore/internal/providers"
)

// Config holds all Azure configuration
type Config struct {
	// Azure Storage
	StorageAccountName string
	StorageAccountKey  string

	// Azure Cosmos DB
	CosmosEndpoint  string
	CosmosKey       string
	CosmosDatabase  string

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

func main() {
	log.Println("Starting OpsCore Azure Functions...")

	// Load configuration
	config := Config{
		StorageAccountName: getEnv("AzureWebJobsStorage__AccountName", ""),
		StorageAccountKey:  getEnv("AzureWebJobsStorage__AccountKey", ""),
		CosmosEndpoint:     getEnv("COSMOS_ENDPOINT", ""),
		CosmosKey:          getEnv("COSMOS_KEY", ""),
		CosmosDatabase:     getEnv("COSMOS_DATABASE", "opscore"),
		QueueName:          getEnv("QUEUE_NAME", "opscore-jobs"),
		SlackSigningSecret: getEnv("SLACK_SIGNING_SECRET", ""),
		SlackBotToken:      getEnv("SLACK_BOT_TOKEN", ""),
		AppInsightsKey:     getEnv("APPINSIGHTS_INSTRUMENTATIONKEY", ""),
	}

	ctx := context.Background()

	// Initialize adapters
	blobAdapter, err := azure.NewBlobAdapter(azure.BlobConfig{
		AccountName: config.StorageAccountName,
		AccountKey:  config.StorageAccountKey,
	})
	if err != nil {
		log.Printf("Warning: Could not initialize blob adapter: %v", err)
	}

	queueAdapter, err := azure.NewQueueAdapter(azure.QueueConfig{
		AccountName: config.StorageAccountName,
		AccountKey: config.StorageAccountKey,
	})
	if err != nil {
		log.Printf("Warning: Could not initialize queue adapter: %v", err)
	}

	cosmosAdapter, err := azure.NewCosmosAdapter(azure.CosmosConfig{
		Endpoint:     config.CosmosEndpoint,
		Key:          config.CosmosKey,
		DatabaseName: config.CosmosDatabase,
	})
	if err != nil {
		log.Printf("Warning: Could not initialize cosmos adapter: %v", err)
	}

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
	port := getEnv("FUNCTIONS_HTTPWORKER_PORT", "8080")
	log.Printf("Starting HTTP server on port %s", port)

	// In production, Azure Functions runtime handles this
	// This is for local development only
	addr := fmt.Sprintf(":%s", port)
	log.Printf("OpsCore Functions listening on %s", addr)

	// Block indefinitely
	select {}
}