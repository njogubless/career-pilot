"""Versioned prompt templates. A prompt's id is `<name>@<version>` and is
recorded with every AI result so outputs can be traced and evaluated."""

from __future__ import annotations

from functools import cache
from pathlib import Path

_DIR = Path(__file__).parent

REGISTRY: dict[str, tuple[str, str]] = {
    # name -> (file, version)
    "cv_extract": ("cv_extract_v1.md", "1"),
    "jd_analyze": ("jd_analyze_v1.md", "1"),
    "cv_tailor": ("cv_tailor_v1.md", "1"),
    "cover_letter": ("cover_letter_v1.md", "1"),
    "app_answers": ("app_answers_v1.md", "1"),
    "outreach_email": ("outreach_email_v1.md", "1"),
}


@cache
def load(name: str) -> tuple[str, str]:
    """Return (prompt_text, prompt_id)."""
    filename, version = REGISTRY[name]
    return (_DIR / filename).read_text(encoding="utf-8"), f"{name}@{version}"


def all_ids() -> list[str]:
    return [f"{n}@{v}" for n, (_, v) in REGISTRY.items()]
