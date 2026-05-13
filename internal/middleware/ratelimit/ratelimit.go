package ratelimit

import (
	"net/http"
	"os"
	"strconv"
	"sync"

	"golang.org/x/time/rate"
)

// RateLimiter provides token bucket rate limiting per tenant
type RateLimiter struct {
	limiters map[string]*rate.Limiter
	mu       sync.RWMutex
	rate     rate.Limit
	burst    int
}

// New creates a new RateLimiter with the specified requests per second and burst
func New(rps float64, burst int) *RateLimiter {
	return &RateLimiter{
		limiters: make(map[string]*rate.Limiter),
		rate:     rate.Limit(rps),
		burst:    burst,
	}
}

// Allow checks if the tenant is allowed to proceed based on rate limits
func (rl *RateLimiter) Allow(tenantID string) bool {
	rl.mu.RLock()
	limiter, exists := rl.limiters[tenantID]
	rl.mu.RUnlock()

	if !exists {
		rl.mu.Lock()
		// Double-check after acquiring write lock
		if limiter, exists = rl.limiters[tenantID]; !exists {
			limiter = rate.NewLimiter(rl.rate, rl.burst)
			rl.limiters[tenantID] = limiter
		}
		rl.mu.Unlock()
	}

	return limiter.Allow()
}

// Middleware returns an HTTP middleware that enforces rate limiting
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.Header.Get("X-Tenant-ID")
		if tenantID == "" {
			tenantID = "default"
		}
		if !rl.Allow(tenantID) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// MiddlewareFunc is a convenience type for http.HandlerFunc middleware
type MiddlewareFunc func(http.Handler) http.Handler

// NewFromEnv creates a RateLimiter from environment variables
// Falls back to defaults if env vars are not set
func NewFromEnv() *RateLimiter {
	rps := 10.0 // default
	burst := 20 // default

	if rpsStr := getEnv("RATE_LIMIT_RPS", ""); rpsStr != "" {
		if parsed, err := strconv.ParseFloat(rpsStr, 64); err == nil && parsed > 0 {
			rps = parsed
		}
	}

	if burstStr := getEnv("RATE_LIMIT_BURST", ""); burstStr != "" {
		if parsed, err := strconv.Atoi(burstStr); err == nil && parsed > 0 {
			burst = parsed
		}
	}

	return New(rps, burst)
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}