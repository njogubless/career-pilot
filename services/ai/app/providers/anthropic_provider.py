"""Anthropic (Claude) provider using the official SDK's structured outputs."""

from __future__ import annotations

import logging
import time

import anthropic

from app.core.errors import (
    OutputInvalid,
    ProviderRefused,
    ProviderRejected,
    ProviderTransient,
    ProviderUnavailable,
)
from app.providers.base import T, Usage

log = logging.getLogger("careerpilot.ai.anthropic")

# Server-side refusal fallback (opt-in beta): on a policy decline the API
# re-runs the request on a fallback model chosen by refusal category.
_FALLBACK_BETA = "server-side-fallback-2026-07-01"


class AnthropicProvider:
    name = "anthropic"

    def __init__(self, api_key: str, model: str, effort: str, timeout_s: float, max_retries: int = 2) -> None:
        self._client = anthropic.Anthropic(api_key=api_key, timeout=timeout_s, max_retries=max_retries)
        self._model = model
        self._effort = effort

    def structured(
        self, *, task: str, system: str, user: str, schema: type[T], max_tokens: int = 16000
    ) -> tuple[T, Usage]:
        started = time.monotonic()
        try:
            response = self._client.beta.messages.parse(
                model=self._model,
                max_tokens=max_tokens,
                system=system,
                messages=[{"role": "user", "content": user}],
                output_format=schema,
                output_config={"effort": self._effort},
                betas=[_FALLBACK_BETA],
                fallbacks="default",
            )
        except anthropic.AuthenticationError as e:
            raise ProviderUnavailable("The configured Anthropic API key was rejected.") from e
        except anthropic.PermissionDeniedError as e:
            raise ProviderUnavailable("The Anthropic API key lacks permission for this model.") from e
        except anthropic.NotFoundError as e:
            raise ProviderRejected(f"Model {self._model!r} is not available to this API key.") from e
        except anthropic.BadRequestError as e:
            raise ProviderRejected("The AI provider rejected the request.") from e
        except anthropic.RateLimitError as e:
            raise ProviderTransient("The AI provider is rate limiting requests.") from e
        except (anthropic.APIConnectionError, anthropic.APITimeoutError) as e:
            raise ProviderTransient("Could not reach the AI provider.") from e
        except anthropic.APIStatusError as e:
            if e.status_code >= 500:
                raise ProviderTransient(f"The AI provider returned {e.status_code}.") from e
            raise ProviderRejected(f"The AI provider returned {e.status_code}.") from e

        latency_ms = int((time.monotonic() - started) * 1000)
        log.info(
            "llm call",
            extra={
                "fields": {
                    "task": task,
                    "model": response.model,
                    "stop_reason": response.stop_reason,
                    "request_id": getattr(response, "_request_id", None),
                    "latency_ms": latency_ms,
                }
            },
        )
        if response.stop_reason == "refusal":
            raise ProviderRefused()
        if response.stop_reason == "max_tokens":
            raise OutputInvalid("The AI output was cut off before completion.")
        parsed = response.parsed_output
        if parsed is None:
            raise OutputInvalid()
        usage = Usage(
            provider=self.name,
            model=response.model,
            input_tokens=response.usage.input_tokens,
            output_tokens=response.usage.output_tokens,
            latency_ms=latency_ms,
        )
        return parsed, usage
