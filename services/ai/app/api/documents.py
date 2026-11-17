"""Application documents.

POST /internal/v1/documents/generate   tailored draft (LLM) + validation
POST /internal/v1/documents/validate   deterministic factual validation only
"""

from __future__ import annotations

import logging

from fastapi import APIRouter, Depends, Request
from pydantic import BaseModel, Field, model_validator

from app.core.errors import ProviderUnavailable
from app.core.security import require_service_token
from app.documents.generate import generate
from app.documents.outreach import Contact, EmailKind, draft_email
from app.documents.schemas import Candidate, DocumentContent, Fact, JobContext, Kind, ValidationReport
from app.documents.validate import validate

log = logging.getLogger("careerpilot.ai.documents")
router = APIRouter(prefix="/internal/v1", dependencies=[Depends(require_service_token)])

MAX_FACTS = 600


class GenerateRequest(BaseModel):
    kind: Kind
    job: JobContext
    facts: list[Fact] = Field(max_length=MAX_FACTS)
    candidate: Candidate
    questions: list[str] = Field(default_factory=list, max_length=25)

    @model_validator(mode="after")
    def _questions(self) -> GenerateRequest:
        if self.kind == "APPLICATION_ANSWERS" and not self.questions:
            raise ValueError("APPLICATION_ANSWERS needs at least one question.")
        if any(len(q) > 1000 for q in self.questions):
            raise ValueError("Each question must be at most 1000 characters.")
        return self


class UsageOut(BaseModel):
    provider: str
    model: str
    prompt_version: str
    input_tokens: int
    output_tokens: int
    latency_ms: int


class GenerateResponse(BaseModel):
    content: DocumentContent
    validation: ValidationReport
    questions: list[str]
    warnings: list[str]
    usage: UsageOut


@router.post("/documents/generate", response_model=GenerateResponse)
def generate_document(body: GenerateRequest, request: Request) -> GenerateResponse:
    provider = request.app.state.provider
    if provider is None:
        raise ProviderUnavailable()
    g = generate(
        kind=body.kind,
        job=body.job,
        facts=body.facts,
        candidate=body.candidate,
        questions=body.questions,
        provider=provider,
    )
    # Counts only: never document text or profile content.
    log.info(
        "document generated",
        extra={
            "fields": {
                "kind": body.kind,
                "facts": len(body.facts),
                "status": g.validation.status,
                "issues": len(g.validation.issues),
                "warnings": len(g.warnings),
                "prompt": g.prompt_id,
            }
        },
    )
    return GenerateResponse(
        content=g.content,
        validation=g.validation,
        questions=g.questions,
        warnings=g.warnings,
        usage=UsageOut(
            provider=g.usage.provider,
            model=g.usage.model,
            prompt_version=g.prompt_id,
            input_tokens=g.usage.input_tokens,
            output_tokens=g.usage.output_tokens,
            latency_ms=g.usage.latency_ms,
        ),
    )


class ValidateRequest(BaseModel):
    content: DocumentContent
    facts: list[Fact] = Field(max_length=MAX_FACTS)
    candidate: Candidate


@router.post("/documents/validate", response_model=ValidationReport)
def validate_document(body: ValidateRequest) -> ValidationReport:
    return validate(body.content, body.facts, body.candidate.years_experience, body.candidate.degree_levels)


class OutreachRequest(BaseModel):
    kind: EmailKind
    job: JobContext
    facts: list[Fact] = Field(max_length=MAX_FACTS)
    candidate: Candidate
    contact: Contact


class OutreachResponse(BaseModel):
    subject: str
    greeting: str
    paragraphs: list[str]
    sign_off: str
    validation: ValidationReport
    questions: list[str]
    warnings: list[str]
    usage: UsageOut


@router.post("/outreach/draft", response_model=OutreachResponse)
def outreach_draft(body: OutreachRequest, request: Request) -> OutreachResponse:
    provider = request.app.state.provider
    if provider is None:
        raise ProviderUnavailable()
    e = draft_email(
        kind=body.kind,
        job=body.job,
        facts=body.facts,
        candidate=body.candidate,
        contact=body.contact,
        provider=provider,
    )
    log.info(
        "outreach drafted",
        extra={"fields": {"kind": body.kind, "status": e.validation.status, "warnings": len(e.warnings)}},
    )
    return OutreachResponse(
        subject=e.subject,
        greeting=e.greeting,
        paragraphs=e.paragraphs,
        sign_off=e.sign_off,
        validation=e.validation,
        questions=e.questions,
        warnings=e.warnings,
        usage=UsageOut(
            provider=e.usage.provider,
            model=e.usage.model,
            prompt_version=e.prompt_id,
            input_tokens=e.usage.input_tokens,
            output_tokens=e.usage.output_tokens,
            latency_ms=e.usage.latency_ms,
        ),
    )
