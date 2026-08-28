package httpapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type observer struct {
	logger   *slog.Logger
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// NewHandler builds the public HTTP surface with isolated Prometheus metrics.
func NewHandler(logger *slog.Logger, registry *prometheus.Registry) http.Handler {
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
	mux.Handle("GET /readyz", obs.instrument("/readyz", http.HandlerFunc(readiness)))
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

func readiness(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Default().Error("encode JSON response", "error", err)
	}
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
