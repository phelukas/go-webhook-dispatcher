package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

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
		RETURNING id, idempotency_key, target_url, event_type, payload, status,
			attempt_count, created_at`,
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
		`SELECT id, idempotency_key, target_url, event_type, payload, status,
			attempt_count, created_at
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

// ClaimNext reserves one pending or expired webhook without blocking other workers.
func (r *Repository) ClaimNext(
	ctx context.Context,
	workerID string,
	leaseDuration time.Duration,
) (webhook.Claim, bool, error) {
	if workerID == "" {
		return webhook.Claim{}, false, errors.New("worker ID is required")
	}
	if leaseDuration <= 0 {
		return webhook.Claim{}, false, errors.New("lease duration must be positive")
	}

	leaseMilliseconds := max(leaseDuration.Milliseconds(), 1)
	claimed, err := scanClaim(r.pool.QueryRow(
		ctx,
		`WITH candidate AS (
			SELECT id
			FROM webhook_submissions
			WHERE status = 'pending'
			   OR (
				status = 'processing'
				AND locked_at < NOW() - ($1 * INTERVAL '1 millisecond')
			)
			ORDER BY COALESCE(locked_at, created_at), created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE webhook_submissions AS submission
		SET status = 'processing',
			attempt_count = attempt_count + 1,
			locked_by = $2,
			locked_at = NOW(),
			lease_token = gen_random_uuid(),
			last_error = NULL,
			completed_at = NULL,
			updated_at = NOW()
		FROM candidate
		WHERE submission.id = candidate.id
		RETURNING submission.id, submission.idempotency_key,
			submission.target_url, submission.event_type, submission.payload,
			submission.status, submission.attempt_count, submission.created_at,
			submission.lease_token`,
		leaseMilliseconds,
		workerID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return webhook.Claim{}, false, nil
	}
	if err != nil {
		return webhook.Claim{}, false, fmt.Errorf("claim webhook submission: %w", err)
	}
	return claimed, true, nil
}

// Complete marks a webhook delivered only while the caller still owns its lease.
func (r *Repository) Complete(ctx context.Context, id, leaseToken string) error {
	return r.finish(ctx, id, leaseToken, "delivered", "")
}

// Fail marks a webhook failed only while the caller still owns its lease.
func (r *Repository) Fail(ctx context.Context, id, leaseToken, message string) error {
	return r.finish(ctx, id, leaseToken, "failed", message)
}

func (r *Repository) finish(
	ctx context.Context,
	id, leaseToken, status, message string,
) error {
	result, err := r.pool.Exec(
		ctx,
		`UPDATE webhook_submissions
		SET status = $3,
			locked_by = NULL,
			locked_at = NULL,
			lease_token = NULL,
			last_error = NULLIF($4, ''),
			completed_at = NOW(),
			updated_at = NOW()
		WHERE id = $1
		  AND status = 'processing'
		  AND lease_token = $2::uuid`,
		id,
		leaseToken,
		status,
		message,
	)
	if err != nil {
		return fmt.Errorf("mark webhook %s: %w", status, err)
	}
	if result.RowsAffected() != 1 {
		return webhook.ErrLeaseLost
	}
	return nil
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
		&result.AttemptCount,
		&result.CreatedAt,
	)
	return result, err
}

func scanClaim(row rowScanner) (webhook.Claim, error) {
	var result webhook.Claim
	err := row.Scan(
		&result.ID,
		&result.IdempotencyKey,
		&result.TargetURL,
		&result.EventType,
		&result.Payload,
		&result.Status,
		&result.AttemptCount,
		&result.CreatedAt,
		&result.LeaseToken,
	)
	return result, err
}
