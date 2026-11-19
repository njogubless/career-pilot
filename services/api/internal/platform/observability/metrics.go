// Package observability provides Prometheus metrics, OpenTelemetry tracing
// and the internal listener that exposes them (ADR-026).
//
// Labels never carry user data: HTTP metrics use chi route patterns
// ("/api/v1/jobs/{id}"), not raw paths, and no metric is keyed by user.
package observability

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Metrics owns a dedicated registry (never the global one) so tests and
// processes register exactly what they expose.
type Metrics struct {
	Registry *prometheus.Registry

	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec
	httpInFlight prometheus.Gauge

	jobsFinished *prometheus.CounterVec
	jobRun       *prometheus.HistogramVec
	jobWait      *prometheus.HistogramVec
}

// NewMetrics registers process, Go runtime, HTTP and job metrics.
func NewMetrics(service, version string) *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	build := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "careerpilot_build_info", Help: "Build information (always 1).",
		ConstLabels: prometheus.Labels{"service": service, "version": version},
	})
	build.Set(1)
	m := &Metrics{
		Registry: reg,
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "careerpilot_http_requests_total", Help: "HTTP requests by method, route pattern and status code.",
		}, []string{"method", "route", "status"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "careerpilot_http_request_duration_seconds", Help: "HTTP request latency by method and route pattern.",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
		}, []string{"method", "route"}),
		httpInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "careerpilot_http_requests_in_flight", Help: "HTTP requests currently being served.",
		}),
		jobsFinished: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "careerpilot_jobs_finished_total", Help: "Background job executions by kind and outcome (completed, failed, cancelled, snoozed, interrupted).",
		}, []string{"kind", "outcome"}),
		jobRun: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "careerpilot_job_run_duration_seconds", Help: "Background job run time by kind.",
			Buckets: []float64{.01, .05, .1, .5, 1, 5, 15, 30, 60, 120, 300, 600},
		}, []string{"kind"}),
		jobWait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "careerpilot_job_queue_wait_seconds", Help: "Time jobs waited in the queue before running, by kind.",
			Buckets: []float64{.01, .1, .5, 1, 5, 15, 60, 300, 900, 3600},
		}, []string{"kind"}),
	}
	reg.MustRegister(build, m.httpRequests, m.httpDuration, m.httpInFlight, m.jobsFinished, m.jobRun, m.jobWait)
	return m
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// HTTP records request metrics after routing, so the chi route pattern is
// known. It also names the active trace span after the route. Health probes
// are excluded to keep the series meaningful.
func (m *Metrics) HTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		m.httpInFlight.Inc()
		defer m.httpInFlight.Dec()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		route := "unmatched"
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			route = rc.RoutePattern()
		}
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		m.httpRequests.WithLabelValues(r.Method, route, strconv.Itoa(sw.status)).Inc()
		m.httpDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
		if span := trace.SpanFromContext(r.Context()); span.IsRecording() {
			span.SetName(r.Method + " " + route)
			span.SetAttributes(attribute.String("http.route", route))
		}
	})
}

// JobFinished records one finished job execution.
func (m *Metrics) JobFinished(kind, outcome string, run, wait time.Duration) {
	m.jobsFinished.WithLabelValues(kind, outcome).Inc()
	if run > 0 {
		m.jobRun.WithLabelValues(kind).Observe(run.Seconds())
	}
	if wait > 0 {
		m.jobWait.WithLabelValues(kind).Observe(wait.Seconds())
	}
}

// Serve exposes /metrics on addr (an internal listener, never the public
// router) until ctx is cancelled. An empty addr disables it.
func (m *Metrics) Serve(ctx context.Context, addr string, log *slog.Logger) {
	if addr == "" {
		log.Info("metrics listener disabled (METRICS_ADDR unset)")
		return
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{Registry: m.Registry, Timeout: 10 * time.Second}))
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Info("metrics listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics listener failed", "err", err)
		}
	}()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
}
