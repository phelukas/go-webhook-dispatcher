package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/phelukas/go-webhook-dispatcher/internal/config"
	"github.com/phelukas/go-webhook-dispatcher/internal/httpapi"
	"github.com/phelukas/go-webhook-dispatcher/internal/postgres"
)

// Run connects dependencies, applies migrations and starts the HTTP service.
func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	databaseCtx, cancel := context.WithTimeout(ctx, cfg.DatabaseTimeout)
	defer cancel()

	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("parse database configuration: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(databaseCtx, poolConfig)
	if err != nil {
		return fmt.Errorf("create database pool: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(databaseCtx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	if err := postgres.Migrate(databaseCtx, pool); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}

	repository := postgres.NewRepository(pool)
	handler := httpapi.NewHandler(logger, prometheus.NewRegistry(), httpapi.Dependencies{
		Submissions: repository,
		Readiness:   pool,
	})
	return runHTTP(ctx, cfg, logger, handler)
}

// runHTTP drains in-flight requests when ctx is cancelled.
func runHTTP(ctx context.Context, cfg config.Config, logger *slog.Logger, handler http.Handler) error {
	listener, err := net.Listen("tcp", cfg.HTTPAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.HTTPAddress, err)
	}

	server := &http.Server{
		Handler:           handler,
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
