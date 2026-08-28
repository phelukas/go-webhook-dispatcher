package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/phelukas/go-webhook-dispatcher/internal/webhook"
)

// Repository persists webhook submissions in PostgreSQL.
type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Create stores a submission once and detects incompatible idempotent replays.
func (r *Repository) Create(
	ctx context.Context,
	submission webhook.Submission,
) (webhook.Webhook, bool, error) {
	created, err := scanWebhook(r.pool.QueryRow(
		ctx,
		`INSERT INTO webhook_submissions (
			idempotency_key, target_url, event_type, payload
		) VALUES ($1, $2, $3, $4)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id, idempotency_key, target_url, event_type, payload, status, created_at`,
		submission.IdempotencyKey,
		submission.TargetURL,
		submission.EventType,
		submission.Payload,
	))
	if err == nil {
		return created, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return webhook.Webhook{}, false, fmt.Errorf("insert webhook submission: %w", err)
	}

	existing, err := scanWebhook(r.pool.QueryRow(
		ctx,
		`SELECT id, idempotency_key, target_url, event_type, payload, status, created_at
		FROM webhook_submissions
		WHERE idempotency_key = $1
		  AND target_url = $2
		  AND event_type = $3
		  AND payload = $4::jsonb`,
		submission.IdempotencyKey,
		submission.TargetURL,
		submission.EventType,
		submission.Payload,
	))
	if err == nil {
		return existing, true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return webhook.Webhook{}, false, webhook.ErrIdempotencyConflict
	}
	return webhook.Webhook{}, false, fmt.Errorf("read idempotent submission: %w", err)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanWebhook(row rowScanner) (webhook.Webhook, error) {
	var result webhook.Webhook
	err := row.Scan(
		&result.ID,
		&result.IdempotencyKey,
		&result.TargetURL,
		&result.EventType,
		&result.Payload,
		&result.Status,
		&result.CreatedAt,
	)
	return result, err
}
