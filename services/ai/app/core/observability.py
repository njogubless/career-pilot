"""Prometheus metrics and OpenTelemetry tracing for the AI service (ADR-026).

Labels never carry request data: HTTP metrics use the route template
("/internal/v1/jobs/analyze"), not the raw path. Tracing is off unless an
OTLP endpoint is configured; only W3C trace context is propagated.
"""

from __future__ import annotations

import os
import time
from typing import Any

from fastapi import FastAPI, Request, Response
from opentelemetry import trace
from opentelemetry.propagate import set_global_textmap
from opentelemetry.trace.propagation.tracecontext import TraceContextTextMapPropagator
from prometheus_client import (
    CONTENT_TYPE_LATEST,
    CollectorRegistry,
    Counter,
    Gauge,
    GCCollector,
    Histogram,
    PlatformCollector,
    ProcessCollector,
    generate_latest,
)

_UNTRACED = {"/healthz", "/metrics"}
_propagator = TraceContextTextMapPropagator()


def tracing_enabled() -> bool:
    return bool(os.getenv("OTEL_EXPORTER_OTLP_ENDPOINT") or os.getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"))


def setup_tracing(version: str, env: str) -> None:
    """Install an OTLP/HTTP exporting tracer provider when configured.

    Exporter endpoint, headers and sampler use the standard OTEL_* variables.
    """
    set_global_textmap(_propagator)
    if not tracing_enabled():
        return
    from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
    from opentelemetry.sdk.resources import Resource
    from opentelemetry.sdk.trace import TracerProvider
    from opentelemetry.sdk.trace.export import BatchSpanProcessor

    resource = Resource.create(
        {"service.name": "careerpilot-ai", "service.version": version, "deployment.environment.name": env}
    )
    provider = TracerProvider(resource=resource)
    provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter()))
    trace.set_tracer_provider(provider)


def shutdown_tracing() -> None:
    provider = trace.get_tracer_provider()
    shutdown = getattr(provider, "shutdown", None)
    if callable(shutdown):
        shutdown()


class Metrics:
    """A dedicated registry so tests and processes expose exactly this set."""

    def __init__(self, version: str) -> None:
        self.registry = CollectorRegistry()
        ProcessCollector(registry=self.registry)
        PlatformCollector(registry=self.registry)
        GCCollector(registry=self.registry)
        Gauge(
            "careerpilot_build_info",
            "Build information (always 1).",
            ["service", "version"],
            registry=self.registry,
        ).labels("ai", version).set(1)
        self.requests = Counter(
            "careerpilot_http_requests",
            "HTTP requests by method, route template and status code.",
            ["method", "route", "status"],
            registry=self.registry,
        )
        self.duration = Histogram(
            "careerpilot_http_request_duration_seconds",
            "HTTP request latency by method and route template.",
            ["method", "route"],
            buckets=(0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300),
            registry=self.registry,
        )
        self.in_flight = Gauge(
            "careerpilot_http_requests_in_flight", "Requests in flight.", registry=self.registry
        )

    def render(self) -> Response:
        return Response(generate_latest(self.registry), media_type=CONTENT_TYPE_LATEST)


def _route(request: Request) -> str:
    route = request.scope.get("route")
    path = getattr(route, "path", None)
    return path if isinstance(path, str) else "unmatched"


def telemetry_config() -> dict[str, Any]:
    """FastAPI's native OpenTelemetry settings (server spans that continue the
    caller's traceparent). Telemetry logs are off: they would record
    validation failures with their original input values (CV text).
    Operation spans and OTel metrics are off; Prometheus covers metrics."""
    return {
        "tracing": True,
        "metrics": False,
        "logs": False,
        "operation_spans": False,
        "exclude": lambda scope: scope.get("path") in _UNTRACED,
    }


def instrument(app: FastAPI, metrics: Metrics | None) -> None:
    """Record Prometheus request metrics by route template."""
    if metrics is None:
        return

    @app.middleware("http")
    async def _observe(request: Request, call_next):  # type: ignore[no-untyped-def]
        if request.url.path in _UNTRACED:
            return await call_next(request)
        start = time.perf_counter()
        status = 500
        metrics.in_flight.inc()
        try:
            response = await call_next(request)
            status = response.status_code
            return response
        finally:
            route = _route(request)
            metrics.in_flight.dec()
            metrics.requests.labels(request.method, route, str(status)).inc()
            metrics.duration.labels(request.method, route).observe(time.perf_counter() - start)


def current_trace_id() -> str | None:
    ctx = trace.get_current_span().get_span_context()
    return format(ctx.trace_id, "032x") if ctx.is_valid else None
