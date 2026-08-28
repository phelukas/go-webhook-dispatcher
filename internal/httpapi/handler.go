package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/phelukas/go-webhook-dispatcher/internal/webhook"
)

const maxSubmissionBodyBytes = 256 * 1024

type SubmissionStore interface {
	Create(context.Context, webhook.Submission) (webhook.Webhook, bool, error)
}

type ReadinessChecker interface {
	Ping(context.Context) error
}

type Dependencies struct {
	Submissions SubmissionStore
	Readiness   ReadinessChecker
}

type observer struct {
	logger   *slog.Logger
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// NewHandler builds the public HTTP surface with isolated Prometheus metrics.
func NewHandler(
	logger *slog.Logger,
	registry *prometheus.Registry,
	dependencies Dependencies,
) http.Handler {
	requests := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "webhook_dispatcher",
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Total HTTP requests processed by route and status.",
		},
		[]string{"method", "route", "status"},
	)
	duration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "webhook_dispatcher",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP request duration in seconds by route.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"method", "route"},
	)
	buildInfo := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "webhook_dispatcher",
		Name:      "build_info",
		Help:      "Static service build information.",
		ConstLabels: prometheus.Labels{
			"version": "dev",
		},
	})
	buildInfo.Set(1)
	registry.MustRegister(requests, duration, buildInfo)

	obs := observer{logger: logger, requests: requests, duration: duration}
	mux := http.NewServeMux()
	mux.Handle("GET /", obs.instrument("/", http.HandlerFunc(serviceInfo)))
	mux.Handle("GET /healthz", obs.instrument("/healthz", http.HandlerFunc(health)))
	mux.Handle("GET /readyz", obs.instrument("/readyz", readiness(dependencies.Readiness)))
	mux.Handle(
		"POST /v1/webhooks",
		obs.instrument(
			"/v1/webhooks",
			submitWebhook(logger, dependencies.Submissions),
		),
	)
	mux.Handle(
		"GET /metrics",
		obs.instrument("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{})),
	)

	return recoverPanic(logger, mux)
}

func serviceInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"service": "go-webhook-dispatcher",
		"status":  "running",
	})
}

func health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func readiness(checker ReadinessChecker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()

		if checker == nil || checker.Ping(ctx) != nil {
			writeError(w, http.StatusServiceUnavailable, "not_ready", "database is unavailable")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
}

type submissionRequest struct {
	TargetURL string          `json:"target_url"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
}

type submissionResponse struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Replayed  bool      `json:"replayed"`
	CreatedAt time.Time `json:"created_at"`
}

func submitWebhook(logger *slog.Logger, store SubmissionStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if idempotencyKey == "" || len(idempotencyKey) > 200 {
			writeError(
				w,
				http.StatusBadRequest,
				"invalid_idempotency_key",
				"Idempotency-Key is required and must contain at most 200 characters",
			)
			return
		}

		var request submissionRequest
		r.Body = http.MaxBytesReader(w, r.Body, maxSubmissionBodyBytes)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body must contain at most 256 KiB")
				return
			}
			writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON")
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid_request", "request body must contain one JSON object")
			return
		}

		request.TargetURL = strings.TrimSpace(request.TargetURL)
		request.EventType = strings.TrimSpace(request.EventType)
		if !validTargetURL(request.TargetURL) {
			writeError(w, http.StatusBadRequest, "invalid_target_url", "target_url must be an absolute HTTP or HTTPS URL")
			return
		}
		if request.EventType == "" || len(request.EventType) > 100 {
			writeError(w, http.StatusBadRequest, "invalid_event_type", "event_type is required and must contain at most 100 characters")
			return
		}
		if !validPayload(request.Payload) {
			writeError(w, http.StatusBadRequest, "invalid_payload", "payload must be a JSON object")
			return
		}

		created, replayed, err := store.Create(r.Context(), webhook.Submission{
			IdempotencyKey: idempotencyKey,
			TargetURL:      request.TargetURL,
			EventType:      request.EventType,
			Payload:        request.Payload,
		})
		if errors.Is(err, webhook.ErrIdempotencyConflict) {
			writeError(w, http.StatusConflict, "idempotency_conflict", err.Error())
			return
		}
		if err != nil {
			logger.Error("persist webhook submission", "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "could not persist webhook submission")
			return
		}

		status := http.StatusAccepted
		if replayed {
			status = http.StatusOK
		}
		writeJSON(w, status, submissionResponse{
			ID:        created.ID,
			Status:    created.Status,
			Replayed:  replayed,
			CreatedAt: created.CreatedAt,
		})
	})
}

func validTargetURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func validPayload(payload json.RawMessage) bool {
	var object map[string]any
	return json.Unmarshal(payload, &object) == nil && object != nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Default().Error("encode JSON response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func (o observer) instrument(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(recorder, r)

		elapsed := time.Since(startedAt)
		status := strconv.Itoa(recorder.status)
		o.requests.WithLabelValues(r.Method, route, status).Inc()
		o.duration.WithLabelValues(r.Method, route).Observe(elapsed.Seconds())
		o.logger.Info(
			"http request",
			"method", r.Method,
			"route", route,
			"status", recorder.status,
			"duration_ms", elapsed.Milliseconds(),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func recoverPanic(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("panic recovered", "error", fmt.Sprint(recovered))
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
