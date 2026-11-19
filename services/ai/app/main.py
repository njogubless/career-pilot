"""FastAPI application factory for the CareerPilot AI service."""

from __future__ import annotations

import logging
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import FastAPI, HTTPException, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse

from app.api import cv, documents, goals, jobs
from app.api.routes import internal, public
from app.core.config import AppEnv, ProviderName, Settings, get_settings
from app.core.errors import ServiceError
from app.core.logging import configure_logging
from app.core.observability import Metrics, instrument, setup_tracing, shutdown_tracing, telemetry_config
from app.embeddings import Embedder, HashEmbedder, LocalEmbedder
from app.providers.factory import build_provider

# Largest accepted request body (base64 CV + JSON overhead).
MAX_BODY_BYTES = 15 * 1024 * 1024

log = logging.getLogger("careerpilot.ai")


def _envelope(code: str, message: str, status: int, details: dict | None = None) -> JSONResponse:
    body: dict = {"error": {"code": code, "message": message}}
    if details:
        body["error"]["details"] = details
    return JSONResponse(status_code=status, content=body)


def build_embedder(settings: Settings) -> Embedder | None:
    match settings.embedding_provider:
        case "local":
            return LocalEmbedder(
                cache_dir=settings.embedding_cache_dir,
                offline=settings.embedding_offline,
                threads=settings.embedding_threads,
            )
        case "hash":
            return HashEmbedder()
        case _:
            return None


@asynccontextmanager
async def _lifespan(_: FastAPI) -> AsyncIterator[None]:
    yield
    # Flush pending spans on graceful shutdown (uvicorn handles SIGTERM).
    shutdown_tracing()


def create_app(settings: Settings | None = None) -> FastAPI:
    settings = settings or get_settings()
    configure_logging(settings.log_level)
    setup_tracing(settings.app_version, settings.app_env.value)

    app = FastAPI(
        title="CareerPilot AI Service",
        version=settings.app_version,
        # Interactive docs are useful locally but not exposed in production.
        docs_url=None if settings.app_env is AppEnv.PRODUCTION else "/docs",
        redoc_url=None,
        openapi_url=None if settings.app_env is AppEnv.PRODUCTION else "/openapi.json",
        lifespan=_lifespan,
        telemetry=telemetry_config(),
    )
    app.dependency_overrides[get_settings] = lambda: settings
    # Built once; None means no provider is configured (endpoints answer 503).
    app.state.provider = None if settings.resolved_provider is ProviderName.NONE else build_provider(settings)
    app.state.embedder = build_embedder(settings)
    app.include_router(public)
    app.include_router(internal)
    app.include_router(cv.router)
    app.include_router(jobs.router)
    app.include_router(documents.router)
    app.include_router(goals.router)

    # Metrics are served on the internal service port (never published through
    # the web proxy); they hold counts and latencies only.
    metrics = Metrics(settings.app_version) if settings.metrics_enabled else None
    app.state.metrics = metrics
    if metrics is not None:
        app.add_api_route("/metrics", metrics.render, methods=["GET"], include_in_schema=False)

    @app.middleware("http")
    async def _limit_body(request: Request, call_next):  # type: ignore[no-untyped-def]
        length = request.headers.get("content-length")
        if length is not None and (not length.isdigit() or int(length) > MAX_BODY_BYTES):
            return _envelope("PAYLOAD_TOO_LARGE", "The request body is too large.", 413)
        return await call_next(request)

    instrument(app, metrics)

    @app.exception_handler(ServiceError)
    async def _service_exc(_: Request, exc: ServiceError) -> JSONResponse:
        if exc.status >= 500:
            log.warning("service error", extra={"fields": {"code": exc.code, "status": exc.status}})
        return _envelope(exc.code, exc.message, exc.status)

    @app.exception_handler(HTTPException)
    async def _http_exc(_: Request, exc: HTTPException) -> JSONResponse:
        if isinstance(exc.detail, dict) and "code" in exc.detail:
            return _envelope(exc.detail["code"], exc.detail.get("message", ""), exc.status_code)
        return _envelope("HTTP_ERROR", str(exc.detail), exc.status_code)

    @app.exception_handler(RequestValidationError)
    async def _validation_exc(_: Request, exc: RequestValidationError) -> JSONResponse:
        details = {".".join(str(p) for p in e["loc"]): e["msg"] for e in exc.errors()}
        return _envelope("VALIDATION_FAILED", "The request is invalid.", 422, details)

    @app.exception_handler(Exception)
    async def _unhandled(_: Request, exc: Exception) -> JSONResponse:
        log.exception("unhandled error")
        return _envelope("INTERNAL", "An unexpected error occurred.", 500)

    log.info(
        "ai service configured",
        extra={
            "fields": {
                "env": settings.app_env.value,
                "llm_provider": settings.resolved_provider.value,
                "embeddings": settings.embedding_provider,
            }
        },
    )
    return app
