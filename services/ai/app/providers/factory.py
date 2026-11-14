"""Builds the configured provider. Never substitutes a fake for a real one."""

from __future__ import annotations

from app.core.config import ProviderName, Settings
from app.core.errors import ProviderUnavailable
from app.providers.base import LLMProvider


def build_provider(settings: Settings) -> LLMProvider:
    match settings.resolved_provider:
        case ProviderName.ANTHROPIC:
            from app.providers.anthropic_provider import AnthropicProvider

            return AnthropicProvider(
                api_key=settings.anthropic_api_key.get_secret_value(),
                model=settings.ai_model,
                effort=settings.ai_effort,
                timeout_s=settings.ai_timeout_s,
            )
        case ProviderName.FAKE:
            from app.providers.fake import FakeProvider

            return FakeProvider()
        case _:
            raise ProviderUnavailable()
