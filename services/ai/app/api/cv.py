"""POST /internal/v1/cv/extract"""

from __future__ import annotations

import base64
import binascii
import logging

from fastapi import APIRouter, Depends, Request
from pydantic import BaseModel, Field

from app.core.errors import ProviderUnavailable, UnreadableDocument
from app.core.security import require_service_token
from app.extraction.cv import extract_profile
from app.extraction.schemas import ProfileProposal
from app.parsers.documents import DOCX, PDF, extract_text

log = logging.getLogger("careerpilot.ai.cv")
router = APIRouter(prefix="/internal/v1", dependencies=[Depends(require_service_token)])

# 10 MiB file → ~13.4 MiB base64.
_MAX_B64 = 14 * 1024 * 1024


class ExtractRequest(BaseModel):
    filename: str = Field(max_length=255)
    mime_type: str
    content_base64: str = Field(max_length=_MAX_B64)


class UsageOut(BaseModel):
    input_tokens: int
    output_tokens: int


class Meta(BaseModel):
    provider: str
    model: str
    prompt_version: str
    usage: UsageOut
    latency_ms: int


class ExtractResponse(BaseModel):
    proposal: ProfileProposal
    meta: Meta


@router.post("/cv/extract", response_model=ExtractResponse)
def extract_cv(body: ExtractRequest, request: Request) -> ExtractResponse:
    provider = request.app.state.provider
    if provider is None:
        raise ProviderUnavailable()
    if body.mime_type not in (PDF, DOCX):
        raise UnreadableDocument("Only PDF and DOCX documents are supported.")
    try:
        data = base64.b64decode(body.content_base64, validate=True)
    except (binascii.Error, ValueError) as e:
        raise UnreadableDocument("The document payload is not valid base64.") from e

    text = extract_text(data, body.mime_type)
    proposal, usage, prompt_id = extract_profile(text, provider)
    # Log sizes and counts only: never document content or extracted PII.
    log.info(
        "cv extracted",
        extra={
            "fields": {
                "chars": len(text),
                "experiences": len(proposal.experiences),
                "skills": len(proposal.skills),
                "provider": usage.provider,
                "prompt": prompt_id,
            }
        },
    )
    return ExtractResponse(
        proposal=proposal,
        meta=Meta(
            provider=usage.provider,
            model=usage.model,
            prompt_version=prompt_id,
            usage=UsageOut(input_tokens=usage.input_tokens, output_tokens=usage.output_tokens),
            latency_ms=usage.latency_ms,
        ),
    )
