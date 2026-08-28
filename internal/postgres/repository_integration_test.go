package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/phelukas/go-webhook-dispatcher/internal/webhook"
)

func TestRepositoryCreatesAndReplaysIdempotently(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	defer pool.Close()

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}

	key := fmt.Sprintf("integration-%d", time.Now().UnixNano())
	defer func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM webhook_submissions WHERE idempotency_key = $1", key)
	}()

	submission := webhook.Submission{
		IdempotencyKey: key,
		TargetURL:      "https://example.com/hooks",
		EventType:      "order.created",
		Payload:        json.RawMessage(`{"order_id":"123","amount":42}`),
	}
	repository := NewRepository(pool)

	created, replayed, err := repository.Create(ctx, submission)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if replayed {
		t.Fatal("first Create() replayed = true, want false")
	}

	reordered := submission
	reordered.Payload = json.RawMessage(`{"amount":42,"order_id":"123"}`)
	existing, replayed, err := repository.Create(ctx, reordered)
	if err != nil {
		t.Fatalf("replayed Create() error = %v", err)
	}
	if !replayed {
		t.Fatal("second Create() replayed = false, want true")
	}
	if existing.ID != created.ID {
		t.Errorf("replayed ID = %q, want %q", existing.ID, created.ID)
	}

	conflicting := submission
	conflicting.Payload = json.RawMessage(`{"order_id":"different"}`)
	_, _, err = repository.Create(ctx, conflicting)
	if !errors.Is(err, webhook.ErrIdempotencyConflict) {
		t.Fatalf("conflicting Create() error = %v, want ErrIdempotencyConflict", err)
	}
}
