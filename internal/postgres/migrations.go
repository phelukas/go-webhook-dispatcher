package postgres

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/001_initial.sql
var initialMigration string

//go:embed migrations/002_delivery_leases.sql
var deliveryLeasesMigration string

var migrations = []struct {
	version int
	sql     string
}{
	{version: 1, sql: initialMigration},
	{version: 2, sql: deliveryLeasesMigration},
}

// Migrate applies versioned schema changes under a transaction-scoped lock.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(846273)); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}

	for _, migration := range migrations {
		var applied bool
		if err := tx.QueryRow(
			ctx,
			"SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)",
			migration.version,
		).Scan(&applied); err != nil {
			return fmt.Errorf("check migration version %d: %w", migration.version, err)
		}
		if applied {
			continue
		}

		if _, err := tx.Exec(ctx, migration.sql); err != nil {
			return fmt.Errorf("apply migration %d: %w", migration.version, err)
		}
		if _, err := tx.Exec(
			ctx,
			"INSERT INTO schema_migrations (version) VALUES ($1)",
			migration.version,
		); err != nil {
			return fmt.Errorf("record migration %d: %w", migration.version, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}
