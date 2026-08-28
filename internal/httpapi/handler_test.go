package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/phelukas/go-webhook-dispatcher/internal/webhook"
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

func TestSubmitWebhookReturnsAccepted(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 8, 28, 20, 0, 0, 0, time.UTC)
	store := storeFunc(func(_ context.Context, submission webhook.Submission) (webhook.Webhook, bool, error) {
		if submission.IdempotencyKey != "order-123" {
			t.Errorf("IdempotencyKey = %q", submission.IdempotencyKey)
		}
		if submission.EventType != "order.created" {
			t.Errorf("EventType = %q", submission.EventType)
		}
		return webhook.Webhook{ID: "webhook-1", Status: "pending", CreatedAt: createdAt}, false, nil
	})
	handler := newTestHandlerWithStore(store)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/webhooks",
		strings.NewReader(`{
			"target_url":"https://example.com/hooks",
			"event_type":"order.created",
			"payload":{"order_id":"123"}
		}`),
	)
	request.Header.Set("Idempotency-Key", "order-123")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusAccepted, response.Body.String())
	}
	var payload submissionResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.ID != "webhook-1" || payload.Replayed {
		t.Errorf("response = %+v", payload)
	}
}

func TestSubmitWebhookReturnsOKForReplay(t *testing.T) {
	t.Parallel()

	store := storeFunc(func(context.Context, webhook.Submission) (webhook.Webhook, bool, error) {
		return webhook.Webhook{ID: "webhook-1", Status: "pending", CreatedAt: time.Now()}, true, nil
	})
	handler := newTestHandlerWithStore(store)
	request := validSubmissionRequest()
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestSubmitWebhookRejectsIdempotencyConflict(t *testing.T) {
	t.Parallel()

	store := storeFunc(func(context.Context, webhook.Submission) (webhook.Webhook, bool, error) {
		return webhook.Webhook{}, false, webhook.ErrIdempotencyConflict
	})
	handler := newTestHandlerWithStore(store)
	request := validSubmissionRequest()
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusConflict)
	}
}

func TestSubmitWebhookValidatesInput(t *testing.T) {
	t.Parallel()

	handler := newTestHandler()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/webhooks",
		strings.NewReader(`{"target_url":"file:///tmp/hook","event_type":"","payload":[]}`),
	)
	request.Header.Set("Idempotency-Key", "invalid")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestSubmitWebhookRejectsOversizedBody(t *testing.T) {
	t.Parallel()

	handler := newTestHandler()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/webhooks",
		strings.NewReader(`{"target_url":"https://example.com/hooks","event_type":"order.created","payload":{"data":"`+
			strings.Repeat("x", maxSubmissionBodyBytes)+`"}}`),
	)
	request.Header.Set("Idempotency-Key", "oversized")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestReadinessReturnsUnavailableWhenDatabaseFails(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(logger, prometheus.NewRegistry(), Dependencies{
		Submissions: defaultStore(),
		Readiness:   readinessFunc(func(context.Context) error { return errors.New("database offline") }),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func newTestHandler() http.Handler {
	return newTestHandlerWithStore(defaultStore())
}

func newTestHandlerWithStore(store SubmissionStore) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewHandler(logger, prometheus.NewRegistry(), Dependencies{
		Submissions: store,
		Readiness:   readinessFunc(func(context.Context) error { return nil }),
	})
}

func validSubmissionRequest() *http.Request {
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/webhooks",
		strings.NewReader(`{"target_url":"https://example.com/hooks","event_type":"order.created","payload":{"order_id":"123"}}`),
	)
	request.Header.Set("Idempotency-Key", "order-123")
	return request
}

type storeFunc func(context.Context, webhook.Submission) (webhook.Webhook, bool, error)

func (f storeFunc) Create(
	ctx context.Context,
	submission webhook.Submission,
) (webhook.Webhook, bool, error) {
	return f(ctx, submission)
}

func defaultStore() storeFunc {
	return func(context.Context, webhook.Submission) (webhook.Webhook, bool, error) {
		return webhook.Webhook{ID: "webhook-test", Status: "pending", CreatedAt: time.Now()}, false, nil
	}
}

type readinessFunc func(context.Context) error

func (f readinessFunc) Ping(ctx context.Context) error {
	return f(ctx)
}
