package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/phelukas/go-webhook-dispatcher/internal/webhook"
)

type Queue interface {
	ClaimNext(context.Context, string, time.Duration) (webhook.Claim, bool, error)
	Complete(context.Context, string, string) error
	Fail(context.Context, string, string, string) error
}

type Processor interface {
	Process(context.Context, webhook.Webhook) error
}

type ProcessorFunc func(context.Context, webhook.Webhook) error

func (f ProcessorFunc) Process(ctx context.Context, item webhook.Webhook) error {
	return f(ctx, item)
}

type Config struct {
	Concurrency   int
	PollInterval  time.Duration
	LeaseDuration time.Duration
	InstanceID    string
}

// Pool claims durable work and limits the number of concurrent processors.
type Pool struct {
	queue     Queue
	processor Processor
	logger    *slog.Logger
	config    Config
}

func New(queue Queue, processor Processor, logger *slog.Logger, config Config) (*Pool, error) {
	if queue == nil {
		return nil, errors.New("worker queue is required")
	}
	if processor == nil {
		return nil, errors.New("worker processor is required")
	}
	if logger == nil {
		return nil, errors.New("worker logger is required")
	}
	if config.Concurrency <= 0 {
		return nil, errors.New("worker concurrency must be positive")
	}
	if config.PollInterval <= 0 {
		return nil, errors.New("worker poll interval must be positive")
	}
	if config.LeaseDuration <= 0 {
		return nil, errors.New("worker lease duration must be positive")
	}
	config.InstanceID = strings.TrimSpace(config.InstanceID)
	if config.InstanceID == "" || len(config.InstanceID) > 100 {
		return nil, errors.New("worker instance ID is required and must contain at most 100 characters")
	}

	return &Pool{queue: queue, processor: processor, logger: logger, config: config}, nil
}

// Run starts the configured number of workers and stops claiming on cancellation.
func (p *Pool) Run(ctx context.Context) error {
	group, groupCtx := errgroup.WithContext(ctx)
	for index := range p.config.Concurrency {
		workerID := fmt.Sprintf("%s-%d", p.config.InstanceID, index+1)
		group.Go(func() error {
			return p.runWorker(groupCtx, workerID)
		})
	}
	return group.Wait()
}

func (p *Pool) runWorker(ctx context.Context, workerID string) error {
	for {
		claim, found, err := p.queue.ClaimNext(ctx, workerID, p.config.LeaseDuration)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("worker %s claim: %w", workerID, err)
		}
		if !found {
			if err := wait(ctx, p.config.PollInterval); err != nil {
				return nil
			}
			continue
		}

		if err := p.processor.Process(ctx, claim.Webhook); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if failErr := p.queue.Fail(ctx, claim.ID, claim.LeaseToken, err.Error()); failErr != nil {
				if errors.Is(failErr, webhook.ErrLeaseLost) {
					p.logger.Warn("webhook failure ignored after lease loss", "worker_id", workerID, "webhook_id", claim.ID)
					continue
				}
				return fmt.Errorf("worker %s fail webhook %s: %w", workerID, claim.ID, failErr)
			}
			p.logger.Warn("webhook processing failed", "worker_id", workerID, "webhook_id", claim.ID, "error", err)
			continue
		}

		if err := p.queue.Complete(ctx, claim.ID, claim.LeaseToken); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, webhook.ErrLeaseLost) {
				p.logger.Warn("webhook completion ignored after lease loss", "worker_id", workerID, "webhook_id", claim.ID)
				continue
			}
			return fmt.Errorf("worker %s complete webhook %s: %w", workerID, claim.ID, err)
		}
		p.logger.Info("webhook processing completed", "worker_id", workerID, "webhook_id", claim.ID)
	}
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
