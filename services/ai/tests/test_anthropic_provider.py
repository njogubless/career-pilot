"""Anthropic provider behaviour with a stubbed SDK client (no network)."""

from types import SimpleNamespace

import anthropic
import httpx2
import pytest

from app.core.errors import (
    OutputInvalid,
    ProviderRefused,
    ProviderRejected,
    ProviderTransient,
    ProviderUnavailable,
)
from app.extraction.schemas import ProposalDraft
from app.providers.anthropic_provider import AnthropicProvider


def provider_with(parse) -> AnthropicProvider:
    p = AnthropicProvider(api_key="test-key", model="claude-opus-5-5", effort="medium", timeout_s=5)
    p._client = SimpleNamespace(beta=SimpleNamespace(messages=SimpleNamespace(parse=parse)))
    return p


def api_error(cls, status: int):
    req = httpx2.Request("POST", "https://api.anthropic.com/v1/messages")
    return cls("boom", response=httpx2.Response(status, request=req), body=None)


def _draft() -> ProposalDraft:
    return ProposalDraft.model_validate(
        {
            "personal": {
                "full_name": None,
                "headline": None,
                "summary": None,
                "location_city": None,
                "location_country": None,
                "links": [],
            },
            "experiences": [],
            "education": [],
            "skills": [],
            "certifications": [],
            "projects": [],
            "warnings": [],
        }
    )


def response(stop_reason="end_turn", parsed=None):
    return SimpleNamespace(
        model="claude-opus-5-5",
        stop_reason=stop_reason,
        parsed_output=parsed,
        usage=SimpleNamespace(input_tokens=100, output_tokens=50),
        _request_id="req_1",
    )


def test_success_passes_schema_effort_and_fallback():
    seen = {}

    def parse(**kwargs):
        seen.update(kwargs)
        return response(parsed=_draft())

    out, usage = provider_with(parse).structured(
        task="cv_extract", system="s", user="u", schema=ProposalDraft
    )
    assert isinstance(out, ProposalDraft)
    assert usage.input_tokens == 100 and usage.provider == "anthropic"
    assert seen["output_format"] is ProposalDraft
    assert seen["output_config"] == {"effort": "medium"}
    assert seen["fallbacks"] == "default"
    assert seen["model"] == "claude-opus-5-5"


@pytest.mark.parametrize(
    ("stop", "parsed", "exc"),
    [
        ("refusal", None, ProviderRefused),
        ("max_tokens", None, OutputInvalid),
        ("end_turn", None, OutputInvalid),
    ],
)
def test_stop_reasons(stop, parsed, exc):
    with pytest.raises(exc):
        provider_with(lambda **_: response(stop, parsed)).structured(
            task="t", system="s", user="u", schema=ProposalDraft
        )


@pytest.mark.parametrize(
    ("err", "exc"),
    [
        (api_error(anthropic.AuthenticationError, 401), ProviderUnavailable),
        (api_error(anthropic.PermissionDeniedError, 403), ProviderUnavailable),
        (api_error(anthropic.BadRequestError, 400), ProviderRejected),
        (api_error(anthropic.RateLimitError, 429), ProviderTransient),
        (api_error(anthropic.InternalServerError, 500), ProviderTransient),
        (anthropic.APIConnectionError(request=httpx2.Request("POST", "https://x")), ProviderTransient),
    ],
)
def test_error_mapping(err, exc):
    def parse(**_):
        raise err

    with pytest.raises(exc):
        provider_with(parse).structured(task="t", system="s", user="u", schema=ProposalDraft)
