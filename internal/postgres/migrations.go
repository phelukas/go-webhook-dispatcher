package postgres

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/001_initial.sql
var initialMigration string

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

	var applied bool
	if err := tx.QueryRow(
		ctx,
		"SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = 1)",
	).Scan(&applied); err != nil {
		return fmt.Errorf("check migration version: %w", err)
	}
	if applied {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration check: %w", err)
		}
		return nil
	}

	if _, err := tx.Exec(ctx, initialMigration); err != nil {
		return fmt.Errorf("apply migration 1: %w", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES (1)"); err != nil {
		return fmt.Errorf("record migration 1: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration 1: %w", err)
	}
	return nil
}
