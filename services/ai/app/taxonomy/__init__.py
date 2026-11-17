"""Canonical skill catalogue and deterministic term matching.

The catalogue (`skills.json`) is versioned. Matching is pure and
deterministic so the platform can reproduce results: the same text and
catalogue version always yield the same skills and evidence.
"""

from __future__ import annotations

import json
import re
from dataclasses import dataclass
from functools import lru_cache
from pathlib import Path

_PATH = Path(__file__).with_name("skills.json")

# Token boundaries that treat symbols used inside skill names (c++, c#, .net,
# node.js) as part of the token.
_BEFORE = r"(?<![A-Za-z0-9_+#.])"
_AFTER = r"(?![A-Za-z0-9_+#]|\.[A-Za-z0-9])"


@dataclass(frozen=True)
class Skill:
    slug: str
    name: str
    category: str


@dataclass(frozen=True)
class Mention:
    skill: Skill
    start: int
    end: int


class Catalogue:
    def __init__(self, data: dict) -> None:
        self.version: str = data["version"]
        self.skills: dict[str, Skill] = {}
        self._alias: dict[str, str] = {}
        patterns: list[tuple[re.Pattern[str], str]] = []
        for raw in data["skills"]:
            skill = Skill(slug=raw["slug"], name=raw["name"], category=raw["category"])
            self.skills[skill.slug] = skill
            names = [raw["name"], raw["slug"], *raw.get("aliases", [])]
            for alias in names:
                self._alias.setdefault(normalize_term(alias), skill.slug)
            ci = sorted({a.lower() for a in raw.get("aliases", [])}, key=len, reverse=True)
            if ci:
                body = "|".join(re.escape(a) for a in ci)
                patterns.append((re.compile(f"{_BEFORE}(?:{body}){_AFTER}", re.IGNORECASE), skill.slug))
            for alias in raw.get("cs_aliases", []):
                self._alias.setdefault(normalize_term(alias), skill.slug)
                patterns.append((re.compile(f"{_BEFORE}{re.escape(alias)}{_AFTER}"), skill.slug))
            for pat in raw.get("patterns", []):
                patterns.append((re.compile(pat), skill.slug))
        self._patterns = patterns

    def normalize(self, term: str) -> Skill | None:
        """Map a free-text skill name to a canonical skill, or None."""
        slug = self._alias.get(normalize_term(term))
        return self.skills[slug] if slug else None

    def find(self, text: str) -> list[Mention]:
        """All catalogue mentions in `text`, ordered by position."""
        out: list[Mention] = []
        for pattern, slug in self._patterns:
            for m in pattern.finditer(text):
                out.append(Mention(self.skills[slug], m.start(), m.end()))
        out.sort(key=lambda m: (m.start, m.skill.slug))
        return out


def normalize_term(term: str) -> str:
    return re.sub(r"\s+", " ", term.strip().lower())


@lru_cache
def catalogue() -> Catalogue:
    return Catalogue(json.loads(_PATH.read_text(encoding="utf-8")))
