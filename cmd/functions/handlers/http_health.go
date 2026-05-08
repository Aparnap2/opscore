package handlers

import (
	"context"
	"log"
	"net/http"
	"time"
)

// HTTPHealthHandler handles GET /health
// Returns the health status of the function app
func HTTPHealthHandler(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	log.Println("Health check requested")

	// Validate request method
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Check adapter health
	health := HealthResponse{
		Status:    "healthy",
		Timestamp: time.Now().UTC(),
		Services:  make(map[string]string),
	}

	// Check blob adapter
	if _, err := getBlobAdapter(ctx); err != nil {
		health.Services["blob"] = "unhealthy: " + err.Error()
		health.Status = "degraded"
	} else {
		health.Services["blob"] = "healthy"
	}

	// Check queue adapter
	if _, err := getQueueAdapter(ctx); err != nil {
		health.Services["queue"] = "unhealthy: " + err.Error()
		health.Status = "degraded"
	} else {
		health.Services["queue"] = "healthy"
	}

	// Check cosmos adapter
	if _, err := getCosmosAdapter(ctx); err != nil {
		health.Services["cosmos"] = "unhealthy: " + err.Error()
		health.Status = "degraded"
	} else {
		health.Services["cosmos"] = "healthy"
	}

	// Return health response
	status := http.StatusOK
	if health.Status != "healthy" {
		status = http.StatusServiceUnavailable
	}

	writeJSON(w, status, health)
}

// HealthResponse represents the health check response
type HealthResponse struct {
	Status    string            `json:"status"`
	Timestamp time.Time         `json:"timestamp"`
	Services  map[string]string `json:"services"`
}