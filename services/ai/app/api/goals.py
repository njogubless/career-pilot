"""POST /internal/v1/goals/interpret — objective → template + parameters (proposal only)."""

from __future__ import annotations

import logging

from fastapi import APIRouter, Depends, Request
from pydantic import BaseModel, Field

from app.core.errors import ProviderUnavailable
from app.core.security import require_service_token
from app.goals.interpret import GoalInterpretation, interpret

log = logging.getLogger("careerpilot.ai.goals")
router = APIRouter(prefix="/internal/v1", dependencies=[Depends(require_service_token)])


class InterpretRequest(BaseModel):
    objective: str = Field(min_length=3, max_length=2000)


class InterpretResponse(BaseModel):
    interpretation: GoalInterpretation
    prompt_version: str
    provider: str
    model: str


@router.post("/goals/interpret", response_model=InterpretResponse)
def interpret_goal(body: InterpretRequest, request: Request) -> InterpretResponse:
    provider = request.app.state.provider
    if provider is None:
        raise ProviderUnavailable()
    out, usage, prompt_id = interpret(body.objective, provider)
    log.info(
        "goal interpreted", extra={"fields": {"template": out.template, "questions": len(out.questions)}}
    )
    return InterpretResponse(
        interpretation=out, prompt_version=prompt_id, provider=usage.provider, model=usage.model
    )
