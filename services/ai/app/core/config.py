"""Service configuration loaded from the environment.

Production validation is strict: a missing service token or an explicitly
configured fake provider aborts startup (ADR-009: never silently substitute
mocks for production integrations).
"""

from __future__ import annotations

import os
from enum import StrEnum
from functools import lru_cache
from pathlib import Path
from typing import Any, Literal

from pydantic import Field, SecretStr, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


class AppEnv(StrEnum):
    DEVELOPMENT = "development"
    TEST = "test"
    PRODUCTION = "production"


class ProviderName(StrEnum):
    AUTO = "auto"  # pick the first provider with credentials
    ANTHROPIC = "anthropic"
    OPENAI = "openai"
    FAKE = "fake"  # deterministic, tests/offline dev only
    NONE = "none"


# Secrets that may be supplied as files (Docker and Kubernetes secrets):
# NAME_FILE=/run/secrets/name.
_FILE_SECRETS = ("ai_service_token", "anthropic_api_key", "openai_api_key")


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=None, extra="ignore", case_sensitive=False)

    app_env: AppEnv = AppEnv.DEVELOPMENT
    app_version: str = "dev"
    log_level: str = "info"

    ai_service_token: SecretStr = Field(default=SecretStr(""))
    ai_provider: ProviderName = ProviderName.AUTO
    anthropic_api_key: SecretStr = Field(default=SecretStr(""))
    openai_api_key: SecretStr = Field(default=SecretStr(""))

    # Default model per project guidance; override with AI_MODEL.
    ai_model: str = "claude-opus-5-5"
    # Claude Opus 5.5 defaults to "medium"; set explicitly so behaviour is pinned.
    ai_effort: Literal["low", "medium", "high", "xhigh", "max"] = "medium"
    ai_timeout_s: float = Field(default=180.0, gt=0, le=600)

    # Embeddings (ADR-020). "local" = bge-small via fastembed in-process;
    # "hash" = deterministic test embedder (rejected in production).
    embedding_provider: Literal["local", "hash", "none"] = "local"
    embedding_cache_dir: str | None = None
    # Refuse to download the model at runtime (the image bakes it in).
    embedding_offline: bool = False
    embedding_threads: int | None = Field(default=None, ge=1, le=32)

    # Prometheus /metrics on the internal service port.
    metrics_enabled: bool = True

    @model_validator(mode="before")
    @classmethod
    def _secret_files(cls, data: Any) -> Any:
        if not isinstance(data, dict):
            return data
        for name in _FILE_SECRETS:
            path = os.environ.get(f"{name.upper()}_FILE", "").strip()
            if not path:
                continue
            if data.get(name) or os.environ.get(name.upper()):
                raise ValueError(f"{name.upper()} and {name.upper()}_FILE: set only one")
            try:
                data[name] = Path(path).read_text(encoding="utf-8").strip()
            except OSError as exc:
                # The path is not secret; the message never includes contents.
                raise ValueError(f"{name.upper()}_FILE: cannot read {path}") from exc
        return data

    @model_validator(mode="after")
    def _validate(self) -> Settings:
        if self.app_env is AppEnv.PRODUCTION:
            if len(self.ai_service_token.get_secret_value()) < 32:
                raise ValueError("AI_SERVICE_TOKEN is required in production (>= 32 chars)")
            if self.ai_provider is ProviderName.FAKE:
                raise ValueError("AI_PROVIDER=fake is not allowed in production")
            if self.embedding_provider == "hash":
                raise ValueError("EMBEDDING_PROVIDER=hash is not allowed in production")
        if self.ai_provider is ProviderName.ANTHROPIC and not self.anthropic_api_key.get_secret_value():
            raise ValueError("AI_PROVIDER=anthropic requires ANTHROPIC_API_KEY")
        if self.ai_provider is ProviderName.OPENAI:
            # Planned (ADR-009) but not implemented; refuse rather than pretend.
            raise ValueError("AI_PROVIDER=openai is not implemented yet; use anthropic")
        return self

    @property
    def resolved_provider(self) -> ProviderName:
        """The provider that will actually serve requests."""
        if self.ai_provider is not ProviderName.AUTO:
            return self.ai_provider
        if self.anthropic_api_key.get_secret_value():
            return ProviderName.ANTHROPIC
        return ProviderName.NONE

    @property
    def llm_configured(self) -> bool:
        return self.resolved_provider is not ProviderName.NONE


@lru_cache
def get_settings() -> Settings:
    return Settings()
