package config

import (
	"fmt"
	"os"
	"time"
)

const (
	defaultHTTPAddress       = ":8080"
	defaultReadHeaderTimeout = 5 * time.Second
	defaultShutdownTimeout   = 10 * time.Second
)

// Config contains the process-level settings required by the HTTP service.
type Config struct {
	HTTPAddress       string
	ReadHeaderTimeout time.Duration
	ShutdownTimeout   time.Duration
}

// Load reads configuration from the environment and applies safe defaults.
func Load() (Config, error) {
	return LoadFrom(os.LookupEnv)
}

// LoadFrom keeps configuration parsing deterministic and easy to test.
func LoadFrom(lookup func(string) (string, bool)) (Config, error) {
	readHeaderTimeout, err := duration(lookup, "READ_HEADER_TIMEOUT", defaultReadHeaderTimeout)
	if err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := duration(lookup, "SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return Config{}, err
	}

	return Config{
		HTTPAddress:       stringValue(lookup, "HTTP_ADDR", defaultHTTPAddress),
		ReadHeaderTimeout: readHeaderTimeout,
		ShutdownTimeout:   shutdownTimeout,
	}, nil
}

func stringValue(lookup func(string) (string, bool), key, fallback string) string {
	if value, ok := lookup(key); ok && value != "" {
		return value
	}
	return fallback
}

func duration(
	lookup func(string) (string, bool),
	key string,
	fallback time.Duration,
) (time.Duration, error) {
	value, ok := lookup(key)
	if !ok || value == "" {
		return fallback, nil
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", key)
	}
	return parsed, nil
}
