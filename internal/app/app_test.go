package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/phelukas/go-webhook-dispatcher/internal/config"
)

func TestRunStopsAfterContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := config.Config{
		HTTPAddress:       "127.0.0.1:0",
		ReadHeaderTimeout: time.Second,
		ShutdownTimeout:   time.Second,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, cfg, logger)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not stop after context cancellation")
	}
}
