package main

import (
	"log/slog"
	"net/http"

	"github.com/aparna/opscore/internal/adapters/postgres"
	"github.com/aparna/opscore/internal/middleware/tenant"
)

// statusWriter captures the HTTP status code so we know whether to commit or roll back.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

// txMiddleware wraps each request in a database transaction. When a tenant is
// present in the context (injected by tenantMW), it begins a transaction, sets
// the app.tenant_id configuration for RLS, and commits on 2xx status codes.
// Non-2xx responses trigger an automatic rollback.
func txMiddleware(db *postgres.Adapter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := tenant.FromContext(r.Context())
		if t == nil {
			// No tenant context — pass through without transaction.
			next.ServeHTTP(w, r)
			return
		}

		tx, err := db.Begin(r.Context())
		if err != nil {
			slog.Error("Failed to begin transaction", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Rollback is a no-op if already committed.
		defer func() {
			_ = tx.Rollback(r.Context())
		}()

		// Set RLS tenant context inside the transaction.
		if err := postgres.SetTenantContextTx(r.Context(), tx, t.ID); err != nil {
			slog.Error("Failed to set tenant context", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		// Store the transaction in context so adapter methods automatically use it.
		ctx := postgres.WithTx(r.Context(), tx)
		ww := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(ww, r.WithContext(ctx))

		// Commit on success (2xx), rollback otherwise (handled by defer).
		if ww.code >= 200 && ww.code < 300 {
			if err := tx.Commit(r.Context()); err != nil {
				slog.Error("Failed to commit transaction", "err", err)
			}
		}
	})
}
