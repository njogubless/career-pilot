"""FastAPI application factory for the CareerPilot AI service."""

from __future__ import annotations

import logging

from fastapi import FastAPI, HTTPException, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse

from app.api.routes import internal, public
from app.core.config import AppEnv, Settings, get_settings
from app.core.logging import configure_logging

log = logging.getLogger("careerpilot.ai")


def _envelope(code: str, message: str, status: int, details: dict | None = None) -> JSONResponse:
    body: dict = {"error": {"code": code, "message": message}}
    if details:
        body["error"]["details"] = details
    return JSONResponse(status_code=status, content=body)


def create_app(settings: Settings | None = None) -> FastAPI:
    settings = settings or get_settings()
    configure_logging(settings.log_level)

    app = FastAPI(
        title="CareerPilot AI Service",
        version=settings.app_version,
        # Interactive docs are useful locally but not exposed in production.
        docs_url=None if settings.app_env is AppEnv.PRODUCTION else "/docs",
        redoc_url=None,
        openapi_url=None if settings.app_env is AppEnv.PRODUCTION else "/openapi.json",
    )
    app.dependency_overrides[get_settings] = lambda: settings
    app.include_router(public)
    app.include_router(internal)

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
        extra={"fields": {"env": settings.app_env.value, "llm_provider": settings.resolved_provider.value}},
    )
    return app
