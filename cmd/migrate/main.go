/*
Migrate is a one-off migration tool for setting up Cosmos DB containers.

Usage:
	COSMOS_ENDPOINT=https://<account>.documents.azure.com:443 \
	COSMOS_KEY=<primary-key> \
	go run cmd/migrate/main.go

This will create the following containers if they don't already exist:
	- jobs (partition key: /tenant_id)
	- vendors (partition key: /tenant_id)
	- documents (partition key: /tenant_id)
	- compliance_chunks (partition key: /tenant_id)
	- audit_events (partition key: /tenant_id)
	- hitl_requests (partition key: /tenant_id)

Required environment variables:
	- COSMOS_ENDPOINT: The Cosmos DB account endpoint URL
	- COSMOS_KEY: The Cosmos DB account primary key
*/
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

const (
	databaseName = "opscore"
)

// containerSpec defines a Cosmos DB container with its properties
type containerSpec struct {
	name          string
	partitionKey string
}

var (
	// containers defines all containers to create
	containers = []containerSpec{
		{name: "jobs", partitionKey: "/tenant_id"},
		{name: "vendors", partitionKey: "/tenant_id"},
		{name: "documents", partitionKey: "/tenant_id"},
		{name: "compliance_chunks", partitionKey: "/tenant_id"},
		{name: "audit_events", partitionKey: "/tenant_id"},
		{name: "hitl_requests", partitionKey: "/tenant_id"},
	}
)

func main() {
	ctx := context.Background()

	// Validate required environment variables
	endpoint := os.Getenv("COSMOS_ENDPOINT")
	key := os.Getenv("COSMOS_KEY")

	if endpoint == "" {
		log.Fatal("COSMOS_ENDPOINT environment variable is required")
	}
	if key == "" {
		log.Fatal("COSMOS_KEY environment variable is required")
	}

	log.Printf("Connecting to Cosmos DB at: %s", endpoint)

	// Create Cosmos DB client
	cred, err := azcosmos.NewKeyCredential(key)
	if err != nil {
		log.Fatalf("Failed to create credential: %v", err)
	}

	client, err := azcosmos.NewClientWithKey(endpoint, cred, nil)
	if err != nil {
		log.Fatalf("Failed to create Cosmos DB client: %v", err)
	}

	// Try to create database (may already exist)
	log.Printf("Ensuring database '%s' exists...", databaseName)
	_, err = client.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: databaseName}, nil)
	if err != nil {
		if isAlreadyExistsError(err) {
			log.Printf("Database '%s' already exists", databaseName)
		} else {
			log.Fatalf("Failed to create database: %v", err)
		}
	} else {
		log.Printf("Database '%s' created successfully", databaseName)
	}

	// Get database client
	dbClient, err := client.NewDatabase(databaseName)
	if err != nil {
		log.Fatalf("Failed to get database client: %v", err)
	}

	// Create containers
	log.Printf("Setting up %d containers...", len(containers))
	for _, spec := range containers {
		if err := createContainer(ctx, dbClient, spec); err != nil {
			log.Fatalf("Failed to create container '%s': %v", spec.name, err)
		}
	}

	log.Println("Migration completed successfully!")
}

// createContainer creates a container if it doesn't exist
func createContainer(ctx context.Context, dbClient *azcosmos.DatabaseClient, spec containerSpec) error {
	// Try to read the container to check if it exists
	_, err := dbClient.NewContainer(spec.name)
	if err == nil {
		log.Printf("  Container '%s' already exists, skipping", spec.name)
		return nil
	}

	// Check if it's a not-found error or something else
	if !isNotFoundError(err) {
		return fmt.Errorf("error checking container: %w", err)
	}

	// Container doesn't exist, create it
	log.Printf("  Creating container '%s' with partition key: %s", spec.name, spec.partitionKey)

	_, err = dbClient.CreateContainer(ctx,
		azcosmos.ContainerProperties{
			ID: spec.name,
			PartitionKeyDefinition: azcosmos.PartitionKeyDefinition{
				Paths: []string{spec.partitionKey},
			},
		}, nil)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	log.Printf("  Created container '%s'", spec.name)
	return nil
}

// isNotFoundError checks if the error is a not-found error from Cosmos DB
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "ResourceNotFound") ||
		strings.Contains(err.Error(), "404")
}

// isAlreadyExistsError checks if the error is an already-exists error from Cosmos DB
func isAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "ResourceAlreadyExists") ||
		strings.Contains(err.Error(), "409")
}