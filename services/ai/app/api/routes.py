"""Health and service-information endpoints."""

from __future__ import annotations

from typing import Annotated

from fastapi import APIRouter, Depends, Request
from pydantic import BaseModel

from app import prompts
from app.core.config import Settings, get_settings
from app.core.security import require_service_token
from app.documents.validate import version as validator_version
from app.jobs.analysis import DETERMINISTIC_VERSION
from app.taxonomy import catalogue

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
    embedding_model: str | None
    catalogue_version: str
    analyzer_versions: dict[str, str]


@public.get("/healthz", response_model=Health)
def healthz(settings: Annotated[Settings, Depends(get_settings)]) -> Health:
    return Health(status="ok", version=settings.app_version)


@internal.get("/info", response_model=Info)
def info(request: Request, settings: Annotated[Settings, Depends(get_settings)]) -> Info:
    # Capabilities list only endpoints that can actually serve requests, so
    # the platform can report availability honestly.
    embedder = request.app.state.embedder
    caps = ["info", "jobs.analyze", "skills.normalize", "documents.validate"]
    if embedder is not None:
        caps.append("embeddings")
    if settings.llm_configured:
        caps += ["cv.extract", "jobs.analyze.llm", "documents.generate"]
    return Info(
        service="careerpilot-ai",
        version=settings.app_version,
        llm_provider=settings.resolved_provider.value,
        llm_configured=settings.llm_configured,
        prompt_versions=prompts.all_ids(),
        capabilities=caps,
        embedding_model=embedder.model if embedder is not None else None,
        catalogue_version=catalogue().version,
        analyzer_versions={
            "jobs.deterministic": f"{DETERMINISTIC_VERSION}+{catalogue().version}",
            "documents.validator": validator_version(),
        },
    )
