package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phelukas/go-webhook-dispatcher/internal/webhook"
)

func TestPoolLimitsConcurrentProcessing(t *testing.T) {
	t.Parallel()

	queue := newFakeQueue(4)
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	processor := ProcessorFunc(func(context.Context, webhook.Webhook) error {
		current := active.Add(1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return nil
	})

	pool := newTestPool(t, queue, processor, 2)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- pool.Run(ctx) }()

	waitForSignal(t, started, "first processor")
	waitForSignal(t, started, "second processor")
	select {
	case <-started:
		t.Fatal("third processor started while both worker slots were occupied")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	for range 4 {
		waitForSignal(t, queue.completed, "completed webhook")
	}
	cancel()
	if err := waitForResult(t, done, "worker pool"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if maximum.Load() != 2 {
		t.Fatalf("maximum concurrency = %d, want 2", maximum.Load())
	}
}

func TestPoolMarksProcessorFailure(t *testing.T) {
	t.Parallel()

	queue := newFakeQueue(1)
	processor := ProcessorFunc(func(context.Context, webhook.Webhook) error {
		return errors.New("destination rejected request")
	})
	pool := newTestPool(t, queue, processor, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- pool.Run(ctx) }()

	waitForSignal(t, queue.failed, "failed webhook")
	cancel()
	if err := waitForResult(t, done, "worker pool"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if queue.failureMessage != "destination rejected request" {
		t.Fatalf("failure message = %q", queue.failureMessage)
	}
}

func TestPoolContinuesAfterLeaseLoss(t *testing.T) {
	t.Parallel()

	queue := &leaseLostQueue{fakeQueue: newFakeQueue(2)}
	processor := ProcessorFunc(func(context.Context, webhook.Webhook) error { return nil })
	pool := newTestPool(t, queue, processor, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- pool.Run(ctx) }()

	waitForSignal(t, queue.completed, "webhook completed after lease loss")
	cancel()
	if err := waitForResult(t, done, "worker pool"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func newTestPool(t *testing.T, queue Queue, processor Processor, concurrency int) *Pool {
	t.Helper()
	pool, err := New(queue, processor, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{
		Concurrency:   concurrency,
		PollInterval:  time.Millisecond,
		LeaseDuration: time.Minute,
		InstanceID:    "test-instance",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return pool
}

func waitForSignal(t *testing.T, channel <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitForResult(t *testing.T, channel <-chan error, description string) error {
	t.Helper()
	select {
	case result := <-channel:
		return result
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
		return nil
	}
}

type fakeQueue struct {
	mu             sync.Mutex
	claims         []webhook.Claim
	completed      chan struct{}
	failed         chan struct{}
	failureMessage string
}

func newFakeQueue(count int) *fakeQueue {
	claims := make([]webhook.Claim, count)
	for index := range count {
		claims[index] = webhook.Claim{
			Webhook:    webhook.Webhook{ID: string(rune('a' + index))},
			LeaseToken: string(rune('A' + index)),
		}
	}
	return &fakeQueue{
		claims:    claims,
		completed: make(chan struct{}, count),
		failed:    make(chan struct{}, count),
	}
}

func (q *fakeQueue) ClaimNext(context.Context, string, time.Duration) (webhook.Claim, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.claims) == 0 {
		return webhook.Claim{}, false, nil
	}
	claim := q.claims[0]
	q.claims = q.claims[1:]
	return claim, true, nil
}

func (q *fakeQueue) Complete(context.Context, string, string) error {
	q.completed <- struct{}{}
	return nil
}

func (q *fakeQueue) Fail(_ context.Context, _, _, message string) error {
	q.mu.Lock()
	q.failureMessage = message
	q.mu.Unlock()
	q.failed <- struct{}{}
	return nil
}

type leaseLostQueue struct {
	*fakeQueue
	once sync.Once
}

func (q *leaseLostQueue) Complete(ctx context.Context, id, leaseToken string) error {
	leaseLost := false
	q.once.Do(func() { leaseLost = true })
	if leaseLost {
		return webhook.ErrLeaseLost
	}
	return q.fakeQueue.Complete(ctx, id, leaseToken)
}
