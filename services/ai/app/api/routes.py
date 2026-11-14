"""Health and service-information endpoints."""

from __future__ import annotations

from typing import Annotated

from fastapi import APIRouter, Depends
from pydantic import BaseModel

from app.core.config import Settings, get_settings
from app.core.security import require_service_token

public = APIRouter()
internal = APIRouter(prefix="/internal/v1", dependencies=[Depends(require_service_token)])


class Health(BaseModel):
    status: str
    version: str


class Info(BaseModel):
    service: str
    version: str
    llm_provider: str
    llm_configured: bool
    prompt_versions: list[str]
    capabilities: list[str]


@public.get("/healthz", response_model=Health)
def healthz(settings: Annotated[Settings, Depends(get_settings)]) -> Health:
    return Health(status="ok", version=settings.app_version)


@internal.get("/info", response_model=Info)
def info(settings: Annotated[Settings, Depends(get_settings)]) -> Info:
    # Capabilities list only endpoints that are actually implemented, so the
    # platform can report availability honestly.
    return Info(
        service="careerpilot-ai",
        version=settings.app_version,
        llm_provider=settings.resolved_provider.value,
        llm_configured=settings.llm_configured,
        prompt_versions=[],
        capabilities=["info"],
    )
