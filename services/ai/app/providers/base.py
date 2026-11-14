"""LLM provider abstraction (ADR-009).

Business logic depends only on `LLMProvider`; concrete providers are chosen
by configuration. Every call returns usage metadata for audit and cost tracking.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Protocol, TypeVar

from pydantic import BaseModel

T = TypeVar("T", bound=BaseModel)


@dataclass(frozen=True)
class Usage:
    provider: str
    model: str
    input_tokens: int
    output_tokens: int
    latency_ms: int


class LLMProvider(Protocol):
    name: str

    def structured(
        self, *, task: str, system: str, user: str, schema: type[T], max_tokens: int = 16000
    ) -> tuple[T, Usage]:
        """Return output validated against `schema`.

        `task` identifies the use case (e.g. "cv_extract") for logging and for
        the deterministic fake provider. `user` may contain untrusted content,
        which callers must wrap in delimiters; providers never grant tools.
        """
        ...
