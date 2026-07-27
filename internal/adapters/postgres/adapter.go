package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aparna/opscore/internal/providers"
)

// ErrVersionConflict is returned when an optimistic lock version check fails.
var ErrVersionConflict = errors.New("version conflict: job was modified concurrently")

// ErrNotFound is returned when a requested entity does not exist for the given
// tenant. It wraps pgx.ErrNoRows so callers can use errors.Is to detect it.
var ErrNotFound = errors.New("entity not found")

// Context key for storing a transaction in the context.
type txContextKey string

const txKey txContextKey = "pgx_tx"

// WithTx returns a context with the given transaction attached.
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey, tx)
}

// getTx returns the transaction from context, or nil if not set.
func getTx(ctx context.Context) pgx.Tx {
	tx, _ := ctx.Value(txKey).(pgx.Tx)
	return tx
}

// queryExecer is satisfied by both pgx.Tx and pgxpool.Pool, allowing
// transparent transaction-aware query execution.
type queryExecer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// getExec returns the transaction from context if available, else the pool.
func (a *Adapter) getExec(ctx context.Context) queryExecer {
	if tx := getTx(ctx); tx != nil {
		return tx
	}
	return a.pool
}

// Adapter implements providers.DBProvider for PostgreSQL.
type Adapter struct {
	pool *pgxpool.Pool
}

// NewAdapter creates a new PostgreSQL adapter with connection pool and runs migrations.
func NewAdapter(ctx context.Context, connStr string) (*Adapter, error) {
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		return nil, fmt.Errorf("creating postgres pool: %w", err)
	}

	adapter := &Adapter{pool: pool}

	if err := RunMigrations(ctx, pool); err != nil {
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return adapter, nil
}

// Ping verifies database connectivity.
func (a *Adapter) Ping(ctx context.Context) error {
	return a.pool.Ping(ctx)
}

// Close shuts down the connection pool.
func (a *Adapter) Close() {
	a.pool.Close()
}

// ---------------------------------------------------------------------------
// Transaction support for RLS
// ---------------------------------------------------------------------------

// Begin starts a new transaction that can be used via WithTx in context.
func (a *Adapter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	return tx, nil
}

// SetTenantContextTx sets app.tenant_id inside an explicit transaction.
// This is the correct way to enable RLS — SET LOCAL only works inside a transaction.
func SetTenantContextTx(ctx context.Context, tx pgx.Tx, tenantID string) error {
	_, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, TRUE)", tenantID)
	return err
}

// IsVersionConflict returns true if the error is an optimistic locking version conflict.
func (a *Adapter) IsVersionConflict(err error) bool {
	return errors.Is(err, ErrVersionConflict)
}

// WithTx creates a new transaction, optionally sets the RLS tenant context,
// runs fn inside it, and commits on success (rollback on error).
// If a transaction already exists in the context, it is reused (no nesting).
func (a *Adapter) WithTx(ctx context.Context, tenantID string, fn func(context.Context) error) error {
	// If there's already a tx in context, reuse it (avoid nesting).
	if tx := getTx(ctx); tx != nil {
		return fn(ctx)
	}

	tx, err := a.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if tenantID != "" {
		if err := SetTenantContextTx(ctx, tx, tenantID); err != nil {
			return fmt.Errorf("set tenant context: %w", err)
		}
	}

	txCtx := WithTx(ctx, tx)
	if err := fn(txCtx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---------------------------------------------------------------------------
// RLS (Row-Level Security) support
// ---------------------------------------------------------------------------

// SetTenantContext sets the app.tenant_id session variable for the current
// connection. This enables PostgreSQL Row-Level Security (RLS) policies to
// filter queries to the current tenant.
//
// NOTE: Setting the session variable outside an explicit transaction is
// effectively a no-op in PostgreSQL — it only lasts for the duration of
// the implicit transaction of the single Exec call. For RLS to actually
// work, use SetTenantContextTx inside an explicit transaction, or wrap
// requests with the txMiddleware in main.go.
func (a *Adapter) SetTenantContext(ctx context.Context, tenantID string) error {
	_, err := a.getExec(ctx).Exec(ctx, "SELECT set_config('app.tenant_id', $1, TRUE)", tenantID)
	return err
}

// Compile-time interface checks.
var _ providers.DBProvider = (*Adapter)(nil)
var _ providers.UsageProvider = (*Adapter)(nil)
