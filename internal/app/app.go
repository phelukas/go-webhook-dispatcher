package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/phelukas/go-webhook-dispatcher/internal/config"
	"github.com/phelukas/go-webhook-dispatcher/internal/httpapi"
)

// Run starts the HTTP service and drains in-flight requests when ctx is cancelled.
func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	listener, err := net.Listen("tcp", cfg.HTTPAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.HTTPAddress, err)
	}

	server := &http.Server{
		Handler:           httpapi.NewHandler(logger, prometheus.NewRegistry()),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
	}
	serverErrors := make(chan error, 1)
	go func() {
		serveErr := server.Serve(listener)
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		serverErrors <- serveErr
	}()

	logger.Info("http server started", "address", listener.Addr().String())

	select {
	case serveErr := <-serverErrors:
		if serveErr != nil {
			return fmt.Errorf("serve HTTP: %w", serveErr)
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if serveErr := <-serverErrors; serveErr != nil {
		return fmt.Errorf("serve HTTP during shutdown: %w", serveErr)
	}

	logger.Info("http server stopped")
	return nil
}
