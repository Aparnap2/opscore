#!/bin/bash
# OpsCore PostgreSQL init script — seeds tenants for development.
#
# This runs once on first container startup (Postgres runs scripts in
# /docker-entrypoint-initdb.d/ lexicographic order).
#
# Idempotent: uses ON CONFLICT DO NOTHING so it is safe to re-run.
#
# The app's migration system (RunMigrations) creates the tables and enables
# Row-Level Security. This script only inserts seed data that the migrations
# depend on (the tenants table is created in migration v3).
#
# If the tenants table doesn't exist yet (migrations haven't run), this
# script is a no-op — the app will create the tables via migrations first.

set -e

echo "init-db.sh: seeding development tenants..."

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
  -- The tenants table may not exist yet if migrations haven't run.
  -- Use a DO block to guard against that race:
  -- (the init script runs before the app, migrations run when the app starts)
  DO \$\$
  BEGIN
    IF EXISTS (SELECT FROM pg_tables WHERE tablename = 'tenants') THEN
      INSERT INTO tenants (id, name, slug, plan, status, config)
      VALUES ('default', 'Default', 'default', 'pro', 'active', '{}')
      ON CONFLICT (id) DO NOTHING;

      INSERT INTO tenants (id, name, slug, plan, status, config)
      VALUES ('tenant-alpha', 'Alpha Corp', 'alpha', 'business', 'active', '{}')
      ON CONFLICT (id) DO NOTHING;

      INSERT INTO tenants (id, name, slug, plan, status, config)
      VALUES ('tenant-beta', 'Beta Pvt Ltd', 'beta', 'pro', 'active', '{}')
      ON CONFLICT (id) DO NOTHING;

      RAISE NOTICE 'Seed tenants inserted successfully';
    ELSE
      RAISE NOTICE 'tenants table does not exist yet — migrations must run first; skipping seed';
    END IF;
  END
  \$\$;
EOSQL

echo "init-db.sh: done."
