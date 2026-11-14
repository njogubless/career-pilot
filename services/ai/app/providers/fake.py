"""Deterministic fake provider for tests and offline development ONLY.

Selected exclusively by AI_PROVIDER=fake and rejected in production by
Settings validation. It never pretends to be a real model: usage reports
provider "fake", and outputs come from simple, documented heuristics.
"""

from __future__ import annotations

from collections.abc import Callable
from typing import Any

from pydantic import BaseModel

from app.core.errors import ProviderRejected
from app.providers.base import T, Usage

Handler = Callable[[str, str], dict[str, Any]]

_handlers: dict[str, Handler] = {}


def register(task: str) -> Callable[[Handler], Handler]:
    def deco(fn: Handler) -> Handler:
        _handlers[task] = fn
        return fn

    return deco


class FakeProvider:
    name = "fake"

    def structured(
        self, *, task: str, system: str, user: str, schema: type[T], max_tokens: int = 16000
    ) -> tuple[T, Usage]:
        handler = _handlers.get(task)
        if handler is None:
            raise ProviderRejected(f"Fake provider has no handler for task {task!r}.")
        raw = handler(system, user)
        parsed = schema.model_validate(raw) if not isinstance(raw, BaseModel) else raw
        usage = Usage(
            provider=self.name,
            model="fake-deterministic",
            input_tokens=len(user) // 4,
            output_tokens=0,
            latency_ms=0,
        )
        return parsed, usage  # type: ignore[return-value]
