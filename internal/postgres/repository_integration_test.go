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

func TestRepositoryClaimsAndFencesLeases(t *testing.T) {
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
	if _, err := pool.Exec(ctx, "TRUNCATE webhook_submissions"); err != nil {
		t.Fatalf("truncate submissions: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), "TRUNCATE webhook_submissions")
	}()

	repository := NewRepository(pool)
	first := createIntegrationSubmission(t, ctx, repository, "claim-first")
	second := createIntegrationSubmission(t, ctx, repository, "claim-second")

	start := make(chan struct{})
	results := make(chan claimResult, 2)
	for _, workerID := range []string{"worker-a", "worker-b"} {
		go func() {
			<-start
			claim, found, err := repository.ClaimNext(ctx, workerID, time.Minute)
			results <- claimResult{claim: claim, found: found, err: err}
		}()
	}
	close(start)

	claimsByID := make(map[string]webhook.Claim, 2)
	for range 2 {
		result := <-results
		if result.err != nil || !result.found {
			t.Fatalf("concurrent ClaimNext() = found %t, error %v", result.found, result.err)
		}
		claimsByID[result.claim.ID] = result.claim
	}
	if len(claimsByID) != 2 {
		t.Fatalf("concurrent claims returned %d distinct IDs, want 2", len(claimsByID))
	}
	firstClaim, firstFound := claimsByID[first.ID]
	secondClaim, secondFound := claimsByID[second.ID]
	if !firstFound || !secondFound {
		t.Fatalf("claimed IDs = %v; want %q and %q", claimsByID, first.ID, second.ID)
	}
	if firstClaim.AttemptCount != 1 || secondClaim.AttemptCount != 1 {
		t.Fatalf("attempt counts = %d, %d; want 1, 1", firstClaim.AttemptCount, secondClaim.AttemptCount)
	}

	if err := repository.Complete(ctx, firstClaim.ID, secondClaim.LeaseToken); !errors.Is(err, webhook.ErrLeaseLost) {
		t.Fatalf("Complete() with wrong lease = %v, want ErrLeaseLost", err)
	}
	if err := repository.Complete(ctx, firstClaim.ID, firstClaim.LeaseToken); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if err := repository.Fail(ctx, secondClaim.ID, secondClaim.LeaseToken, "delivery rejected"); err != nil {
		t.Fatalf("Fail() error = %v", err)
	}

	third := createIntegrationSubmission(t, ctx, repository, "claim-expired")
	expiredClaim, found, err := repository.ClaimNext(ctx, "worker-old", time.Minute)
	if err != nil || !found || expiredClaim.ID != third.ID {
		t.Fatalf("expired ClaimNext() = id %q, found %t, error %v", expiredClaim.ID, found, err)
	}
	if _, err := pool.Exec(
		ctx,
		"UPDATE webhook_submissions SET locked_at = NOW() - INTERVAL '5 minutes' WHERE id = $1",
		third.ID,
	); err != nil {
		t.Fatalf("expire lease: %v", err)
	}

	reclaimed, found, err := repository.ClaimNext(ctx, "worker-new", time.Minute)
	if err != nil || !found {
		t.Fatalf("reclaimed ClaimNext() = found %t, error %v", found, err)
	}
	if reclaimed.ID != third.ID || reclaimed.AttemptCount != 2 || reclaimed.LeaseToken == expiredClaim.LeaseToken {
		t.Fatalf("reclaimed = %+v, original lease = %q", reclaimed, expiredClaim.LeaseToken)
	}
	if err := repository.Complete(ctx, third.ID, expiredClaim.LeaseToken); !errors.Is(err, webhook.ErrLeaseLost) {
		t.Fatalf("stale Complete() = %v, want ErrLeaseLost", err)
	}
	if err := repository.Complete(ctx, third.ID, reclaimed.LeaseToken); err != nil {
		t.Fatalf("reclaimed Complete() error = %v", err)
	}
}

type claimResult struct {
	claim webhook.Claim
	found bool
	err   error
}

func createIntegrationSubmission(
	t *testing.T,
	ctx context.Context,
	repository *Repository,
	key string,
) webhook.Webhook {
	t.Helper()

	created, replayed, err := repository.Create(ctx, webhook.Submission{
		IdempotencyKey: fmt.Sprintf("%s-%d", key, time.Now().UnixNano()),
		TargetURL:      "https://example.com/hooks",
		EventType:      "order.created",
		Payload:        json.RawMessage(`{"order_id":"123"}`),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if replayed {
		t.Fatal("Create() replayed = true, want false")
	}
	return created
}
