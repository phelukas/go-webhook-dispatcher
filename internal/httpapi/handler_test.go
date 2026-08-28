package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestHealthEndpoint(t *testing.T) {
	t.Parallel()

	handler := newTestHandler()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}

	var payload map[string]string
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["status"] != "ok" {
		t.Errorf("status payload = %q, want ok", payload["status"])
	}
}

func TestMetricsExposeObservedRequest(t *testing.T) {
	t.Parallel()

	handler := newTestHandler()
	healthResponse := httptest.NewRecorder()
	handler.ServeHTTP(
		healthResponse,
		httptest.NewRequest(http.MethodGet, "/healthz", nil),
	)

	metricsResponse := httptest.NewRecorder()
	handler.ServeHTTP(
		metricsResponse,
		httptest.NewRequest(http.MethodGet, "/metrics", nil),
	)

	if metricsResponse.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", metricsResponse.Code, http.StatusOK)
	}
	if !strings.Contains(
		metricsResponse.Body.String(),
		`webhook_dispatcher_http_requests_total{method="GET",route="/healthz",status="200"} 1`,
	) {
		t.Fatalf("metrics do not contain the observed health request")
	}
}

func TestUnsupportedMethodReturnsMethodNotAllowed(t *testing.T) {
	t.Parallel()

	handler := newTestHandler()
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodPost, "/healthz", nil),
	)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}

func newTestHandler() http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewHandler(logger, prometheus.NewRegistry())
}
