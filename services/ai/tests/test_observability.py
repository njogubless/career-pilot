"""Metrics, trace propagation and file-based secrets (ADR-026)."""

from __future__ import annotations

import pytest
from fastapi.testclient import TestClient
from opentelemetry import trace
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter

from app.core.config import AppEnv, Settings
from app.main import create_app

TOKEN = "t" * 40
_exporter = InMemorySpanExporter()
_provider = TracerProvider()
_provider.add_span_processor(SimpleSpanProcessor(_exporter))
trace.set_tracer_provider(_provider)


def _client(**overrides: object) -> TestClient:
    return TestClient(create_app(Settings(app_env=AppEnv.TEST, ai_service_token=TOKEN, **overrides)))


def test_metrics_use_route_templates_not_raw_paths() -> None:
    c = _client()
    assert c.get("/internal/v1/info", headers={"Authorization": f"Bearer {TOKEN}"}).status_code == 200
    assert c.get("/internal/v1/info").status_code == 401
    assert c.get("/no/such/path").status_code == 404
    body = c.get("/metrics").text
    assert 'careerpilot_http_requests_total{method="GET",route="/internal/v1/info",status="200"} 1.0' in body
    assert 'route="/internal/v1/info",status="401"' in body
    assert 'route="unmatched"' in body
    assert "/no/such/path" not in body
    assert 'careerpilot_build_info{service="ai"' in body


def test_metrics_can_be_disabled() -> None:
    assert _client(metrics_enabled=False).get("/metrics").status_code == 404


def test_incoming_traceparent_is_continued() -> None:
    _exporter.clear()
    trace_id = "4bf92f3577b34da6a3ce929d0e0e4736"
    c = _client()
    r = c.get(
        "/internal/v1/info",
        headers={"Authorization": f"Bearer {TOKEN}", "traceparent": f"00-{trace_id}-00f067aa0ba902b7-01"},
    )
    assert r.status_code == 200
    spans = [s for s in _exporter.get_finished_spans() if s.kind == trace.SpanKind.SERVER]
    assert spans, "no server span recorded"
    span = spans[-1]
    assert format(span.context.trace_id, "032x") == trace_id
    assert span.name == "GET /internal/v1/info"
    assert span.attributes["http.route"] == "/internal/v1/info"
    # One server span per request (no duplicate), no operation spans, and
    # health probes are not traced.
    assert len(spans) == 1
    assert all(s.kind == trace.SpanKind.SERVER for s in _exporter.get_finished_spans())
    _exporter.clear()
    c.get("/healthz")
    assert not _exporter.get_finished_spans()


def test_secrets_can_come_from_files(tmp_path, monkeypatch: pytest.MonkeyPatch) -> None:
    secret = tmp_path / "token"
    secret.write_text("  " + "s" * 40 + "\n", encoding="utf-8")
    monkeypatch.setenv("AI_SERVICE_TOKEN_FILE", str(secret))
    monkeypatch.delenv("AI_SERVICE_TOKEN", raising=False)
    assert Settings(app_env=AppEnv.PRODUCTION).ai_service_token.get_secret_value() == "s" * 40

    monkeypatch.setenv("AI_SERVICE_TOKEN", "x" * 40)
    with pytest.raises(ValueError, match="set only one"):
        Settings(app_env=AppEnv.TEST)

    monkeypatch.delenv("AI_SERVICE_TOKEN")
    monkeypatch.setenv("AI_SERVICE_TOKEN_FILE", str(tmp_path / "missing"))
    with pytest.raises(ValueError, match="cannot read"):
        Settings(app_env=AppEnv.TEST)
