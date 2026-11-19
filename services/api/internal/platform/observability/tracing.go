package observability

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TracingEnabled reports whether an OTLP endpoint is configured. Tracing is
// off by default: nothing is exported unless an operator points it somewhere.
func TracingEnabled() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != ""
}

// SetupTracing installs a global tracer provider exporting over OTLP/HTTP.
// The exporter, sampler (OTEL_TRACES_SAMPLER, OTEL_TRACES_SAMPLER_ARG) and
// headers are configured with the standard OTEL_* variables. Only W3C trace
// context is propagated (not baggage, which could carry data across
// services). The returned function flushes and stops the provider.
func SetupTracing(ctx context.Context, service, version, env string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	if !TracingEnabled() {
		return func(context.Context) error { return nil }, nil
	}
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		attribute.String("service.name", service),
		attribute.String("service.version", version),
		attribute.String("deployment.environment.name", env),
	))
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// HTTPHandler wraps the public router in a server span. Spans are renamed to
// the route pattern by Metrics.HTTP; health probes are not traced.
func HTTPHandler(h http.Handler) http.Handler {
	return otelhttp.NewHandler(h, "http.request",
		otelhttp.WithFilter(func(r *http.Request) bool { return r.URL.Path != "/healthz" && r.URL.Path != "/readyz" }),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return r.Method }),
	)
}

// Transport instruments outbound calls and injects the traceparent header,
// so a request is one trace across the API and the AI service.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return otelhttp.NewTransport(base)
}

// TraceID returns the active trace id, or "" when there is no sampled span.
func TraceID(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}
