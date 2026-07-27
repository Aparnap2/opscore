package main

import (
	"net/http"
	"time"
)

// healthHandler handles GET /health.
func (s *ServerDeps) healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	health := HealthResponse{
		Status:    "healthy",
		Timestamp: time.Now().UTC(),
		Services:  make(map[string]string),
	}

	// Postgres health: try a simple ping.
	ctx := r.Context()
	if err := s.db.Ping(ctx); err != nil {
		health.Services["postgres"] = "unhealthy: " + err.Error()
		health.Status = "degraded"
	} else {
		health.Services["postgres"] = "healthy"
	}

	// MinIO health: check a known bucket or just verify client initialized.
	if s.storage != nil {
		health.Services["minio"] = "healthy"
	} else {
		health.Services["minio"] = "unhealthy: not initialized"
		health.Status = "degraded"
	}

	// Queue health (Redis or PubSub).
	if s.rq != nil {
		health.Services["queue"] = "healthy"
	} else {
		health.Services["queue"] = "unhealthy: not initialized"
		health.Status = "degraded"
	}

	status := http.StatusOK
	if health.Status != "healthy" {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, health)
}
