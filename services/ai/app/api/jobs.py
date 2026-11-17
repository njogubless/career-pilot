"""Matching inputs: job analysis, skill normalisation and embeddings.

POST /internal/v1/jobs/analyze     deterministic (batch) or LLM (single job)
POST /internal/v1/skills/normalize terms → canonical skills; texts → mentions
POST /internal/v1/embeddings       texts → vectors
"""

from __future__ import annotations

import logging
from typing import Literal

from fastapi import APIRouter, Depends, Request
from pydantic import BaseModel, Field

from app.core.errors import ProviderUnavailable, ServiceError
from app.core.security import require_service_token
from app.embeddings import Embedder, EmbeddingsUnavailable
from app.jobs.analysis import analyze_deterministic, analyze_llm, embedding_text
from app.jobs.schemas import JobAnalysis
from app.taxonomy import catalogue

log = logging.getLogger("careerpilot.ai.jobs")
router = APIRouter(prefix="/internal/v1", dependencies=[Depends(require_service_token)])

MAX_TEXT = 100_000


class JobIn(BaseModel):
    id: str = Field(max_length=64)
    title: str = Field(max_length=500)
    company: str = Field(default="", max_length=300)
    text: str = Field(max_length=MAX_TEXT)


class AnalyzeRequest(BaseModel):
    mode: Literal["deterministic", "llm"] = "deterministic"
    embed: bool = True
    jobs: list[JobIn] = Field(min_length=1, max_length=64)


class UsageOut(BaseModel):
    provider: str
    model: str
    prompt_version: str
    input_tokens: int
    output_tokens: int
    latency_ms: int


class AnalyzeResult(BaseModel):
    id: str
    analysis: JobAnalysis
    embedding: list[float] | None = None


class AnalyzeResponse(BaseModel):
    results: list[AnalyzeResult]
    embedding_model: str | None
    usage: UsageOut | None = None


def _embedder(request: Request) -> Embedder:
    emb = request.app.state.embedder
    if emb is None:
        raise EmbeddingsUnavailable()
    return emb


@router.post("/jobs/analyze", response_model=AnalyzeResponse)
def analyze(body: AnalyzeRequest, request: Request) -> AnalyzeResponse:
    usage_out: UsageOut | None = None
    if body.mode == "llm":
        if len(body.jobs) != 1:
            raise ServiceError("VALIDATION_FAILED", "LLM analysis takes exactly one job.", 422)
        provider = request.app.state.provider
        if provider is None:
            raise ProviderUnavailable()
        job = body.jobs[0]
        analysis, usage, prompt_id = analyze_llm(job.title, job.text, provider)
        analyses = [analysis]
        usage_out = UsageOut(
            provider=usage.provider,
            model=usage.model,
            prompt_version=prompt_id,
            input_tokens=usage.input_tokens,
            output_tokens=usage.output_tokens,
            latency_ms=usage.latency_ms,
        )
    else:
        analyses = [analyze_deterministic(j.title, j.text) for j in body.jobs]

    vectors: list[list[float]] | None = None
    model: str | None = None
    if body.embed:
        emb = _embedder(request)
        vectors = emb.embed([embedding_text(j.title, j.company, j.text) for j in body.jobs])
        model = emb.model

    log.info(
        "jobs analyzed",
        extra={"fields": {"mode": body.mode, "count": len(body.jobs), "embedded": vectors is not None}},
    )
    return AnalyzeResponse(
        results=[
            AnalyzeResult(id=j.id, analysis=a, embedding=vectors[i] if vectors else None)
            for i, (j, a) in enumerate(zip(body.jobs, analyses, strict=True))
        ],
        embedding_model=model,
        usage=usage_out,
    )


class NormalizeRequest(BaseModel):
    terms: list[str] = Field(default_factory=list, max_length=500)
    texts: list[str] = Field(default_factory=list, max_length=200)


class CanonicalSkill(BaseModel):
    slug: str
    name: str
    category: str


class TermResult(BaseModel):
    term: str
    skill: CanonicalSkill | None


class TextMention(BaseModel):
    slug: str
    name: str
    category: str
    evidence: str


class NormalizeResponse(BaseModel):
    catalogue_version: str
    terms: list[TermResult]
    texts: list[list[TextMention]]


@router.post("/skills/normalize", response_model=NormalizeResponse)
def normalize(body: NormalizeRequest) -> NormalizeResponse:
    cat = catalogue()
    terms = []
    for t in body.terms:
        s = cat.normalize(t[:200])
        skill = CanonicalSkill(slug=s.slug, name=s.name, category=s.category) if s else None
        terms.append(TermResult(term=t, skill=skill))
    texts: list[list[TextMention]] = []
    for text in body.texts:
        text = text[:20_000]
        seen: dict[str, TextMention] = {}
        for m in cat.find(text):
            if m.skill.slug not in seen:
                left = max(0, m.start - 60)
                seen[m.skill.slug] = TextMention(
                    slug=m.skill.slug,
                    name=m.skill.name,
                    category=m.skill.category,
                    evidence=text[left : m.end + 60].strip(),
                )
        texts.append(list(seen.values()))
    return NormalizeResponse(catalogue_version=cat.version, terms=terms, texts=texts)


class EmbedRequest(BaseModel):
    texts: list[str] = Field(min_length=1, max_length=64)


class EmbedResponse(BaseModel):
    model: str
    dim: int
    vectors: list[list[float]]


@router.post("/embeddings", response_model=EmbedResponse)
def embeddings(body: EmbedRequest, request: Request) -> EmbedResponse:
    emb = _embedder(request)
    return EmbedResponse(model=emb.model, dim=emb.dim, vectors=emb.embed([t[:MAX_TEXT] for t in body.texts]))
