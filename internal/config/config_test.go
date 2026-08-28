package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadFromUsesDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := LoadFrom(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}

	if cfg.HTTPAddress != ":8080" {
		t.Errorf("HTTPAddress = %q, want %q", cfg.HTTPAddress, ":8080")
	}
	if cfg.DatabaseURL != defaultDatabaseURL {
		t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, defaultDatabaseURL)
	}
	if cfg.DatabaseTimeout != 5*time.Second {
		t.Errorf("DatabaseTimeout = %s, want %s", cfg.DatabaseTimeout, 5*time.Second)
	}
	if cfg.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("ReadHeaderTimeout = %s, want %s", cfg.ReadHeaderTimeout, 5*time.Second)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %s, want %s", cfg.ShutdownTimeout, 10*time.Second)
	}
}

func TestLoadFromReadsEnvironment(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		"HTTP_ADDR":           "127.0.0.1:9090",
		"DATABASE_URL":        "postgresql://example/test",
		"DATABASE_TIMEOUT":    "1s",
		"READ_HEADER_TIMEOUT": "2s",
		"SHUTDOWN_TIMEOUT":    "3s",
	}
	lookup := func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}

	cfg, err := LoadFrom(lookup)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}

	if cfg.HTTPAddress != "127.0.0.1:9090" {
		t.Errorf("HTTPAddress = %q", cfg.HTTPAddress)
	}
	if cfg.DatabaseURL != "postgresql://example/test" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.DatabaseTimeout != time.Second {
		t.Errorf("DatabaseTimeout = %s", cfg.DatabaseTimeout)
	}
	if cfg.ReadHeaderTimeout != 2*time.Second {
		t.Errorf("ReadHeaderTimeout = %s", cfg.ReadHeaderTimeout)
	}
	if cfg.ShutdownTimeout != 3*time.Second {
		t.Errorf("ShutdownTimeout = %s", cfg.ShutdownTimeout)
	}
}

func TestLoadFromRejectsInvalidDuration(t *testing.T) {
	t.Parallel()

	lookup := func(key string) (string, bool) {
		if key == "SHUTDOWN_TIMEOUT" {
			return "immediately", true
		}
		return "", false
	}

	_, err := LoadFrom(lookup)
	if err == nil || !strings.Contains(err.Error(), "SHUTDOWN_TIMEOUT") {
		t.Fatalf("LoadFrom() error = %v, want SHUTDOWN_TIMEOUT validation error", err)
	}
}
