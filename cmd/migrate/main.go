package main

import (
	"context"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aparna/opscore/internal/adapters/postgres"
)

func main() {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		log.Fatalf("connecting to postgres: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("pinging postgres: %v", err)
	}
	log.Println("Connected to PostgreSQL")

	if err := postgres.RunMigrations(ctx, pool); err != nil {
		log.Fatalf("running migrations: %v", err)
	}

	log.Println("Migration completed successfully")
}
