"""Deterministic factual validation of application documents (no LLM).

Every claim block (paragraph, bullet, answer) is checked against the fact
sheet. The checks are conservative: they catch the most damaging kinds of
fabrication (unsupported numbers, skills, years of experience, degrees and
certifications, unresolved citations and placeholders) and are explainable.
They cannot prove that free text is true, which is why the user still reviews
and approves every document.
"""

from __future__ import annotations

import re
from collections.abc import Iterable

from app.documents.schemas import CLAIM_TYPES, Block, DocumentContent, Fact, Issue, ValidationReport
from app.taxonomy import catalogue

VALIDATOR_VERSION = "fact-check-2"

_NUMBER = re.compile(r"(?<![A-Za-z\d.])\d[\d,]*(?:\.\d+)?")
_YEARS = re.compile(r"(?<![\d.])(\d+(?:\.\d+)?)\s*\+?\s*(?:-\s*)?(?:years?|yrs?)\b", re.IGNORECASE)
_PLACEHOLDER = re.compile(
    r"\[[^\]\n]{1,80}\]|\{\{[^}]*\}\}|<[A-Z_ ]{3,40}>|\b(?:TODO|TBD|XXX)\b|lorem ipsum", re.I
)
_DEGREES: dict[str, re.Pattern[str]] = {
    "DOCTORATE": re.compile(r"\b(?:ph\.?\s?d|doctorate|doctoral degree)\b", re.I),
    "MASTERS": re.compile(r"\b(?:master'?s|masters|m\.?sc|mba|m\.eng|meng)\b", re.I),
    "BACHELORS": re.compile(r"\b(?:bachelor'?s|bachelors|b\.?sc|b\.eng|beng)\b", re.I),
}
_CERTIFIED = re.compile(r"\bcertifi(?:ed|cation|cations)\b", re.I)
_LEVEL_RANK = {"ASSOCIATE": 1, "BACHELORS": 2, "MASTERS": 3, "DOCTORATE": 4}


def version() -> str:
    return f"{VALIDATOR_VERSION}+{catalogue().version}"


def _numbers(text: str) -> set[str]:
    out = set()
    for m in _NUMBER.finditer(text):
        n = m.group(0).replace(",", "").rstrip(".")
        if "." in n:
            n = n.rstrip("0").rstrip(".")
        out.add(n.lstrip("0") or "0")
    return out


_DATE_FIELDS = frozenset({"start", "end", "issued", "expires"})


def _field_text(fields: dict[str, str]) -> str:
    # Dates support their year only: a month ("2019-03") must not make "3"
    # look like a supported figure.
    return " ".join(v[:4] if k in _DATE_FIELDS else v for k, v in fields.items())


def _fact_text(f: Fact) -> str:
    return f.text + " " + _field_text(f.fields)


class Validator:
    def __init__(
        self, facts: list[Fact], years_experience: float | None, degree_levels: Iterable[str]
    ) -> None:
        self.facts = {f.ref: f for f in facts}
        self.children: dict[str, list[Fact]] = {}
        for f in facts:
            if f.parent:
                self.children.setdefault(f.parent, []).append(f)
        self.years = years_experience
        self.degree_rank = max((_LEVEL_RANK.get(d, 0) for d in degree_levels), default=0)
        self.has_certs = any(f.kind == "certification" for f in facts)
        cat = catalogue()
        self.all_text = "\n".join(_fact_text(f) for f in facts)
        self.supported_skills = {m.skill.slug for m in cat.find(self.all_text)}
        for f in facts:
            if f.kind == "skill":
                s = cat.normalize(f.text)
                if s:
                    self.supported_skills.add(s.slug)
        # Skill/certification names that contain digits ("OAuth 2.0", "AWS S3"):
        # occurrences of these exact names are removed before checking figures,
        # so "2" is allowed in "OAuth 2.0" but not in "2M events".
        names = {f.text.strip() for f in facts if f.kind in ("skill", "certification") and _numbers(f.text)}
        self.numbered_names = [
            re.compile(re.escape(n), re.IGNORECASE) for n in sorted(names, key=len, reverse=True) if n
        ]

    def support_text(self, refs: list[str]) -> str:
        if not refs:
            return self.all_text
        parts = []
        for r in refs:
            f = self.facts.get(r)
            if f is None:
                continue
            parts.append(_fact_text(f))
            parts.extend(_fact_text(c) for c in self.children.get(r, []))
            if f.parent and f.parent in self.facts:
                parts.append(_field_text(self.facts[f.parent].fields))
        return "\n".join(parts)

    def check(self, content: DocumentContent) -> ValidationReport:
        issues: list[Issue] = []
        claims = 0
        for section in content.sections:
            for b in section.blocks:
                for r in b.refs:
                    if r not in self.facts:
                        issues.append(
                            Issue(
                                block_id=b.id,
                                code="UNKNOWN_REF",
                                severity="ERROR",
                                message="Cites a profile fact that no longer exists or is not verified.",
                                excerpt=r,
                            )
                        )
                if b.type in CLAIM_TYPES:
                    claims += 1
                    issues.extend(self._claim(b, content.kind))
        errors = any(i.severity == "ERROR" for i in issues)
        status = "FAILED" if errors else ("WARNINGS" if issues else "PASSED")
        return ValidationReport(
            validator_version=version(), status=status, issues=issues, claims_checked=claims
        )

    def _claim(self, b: Block, kind: str) -> list[Issue]:
        out: list[Issue] = []

        def add(code: str, severity: str, message: str, excerpt: str = "") -> None:
            out.append(Issue(block_id=b.id, code=code, severity=severity, message=message, excerpt=excerpt))  # type: ignore[arg-type]

        text = b.text.strip()
        if b.meta.get("status") == "NEEDS_INPUT" and not text:
            add("NEEDS_INPUT", "ERROR", b.meta.get("question_for_user") or "This answer needs your input.")
            return out
        if not text:
            add("EMPTY", "ERROR", "This block is empty. Write it or remove it.")
            return out
        structural = b.meta.get("role") in ("greeting", "closing")
        if not b.refs and not structural:
            add(
                "NO_SUPPORT",
                "WARNING",
                "No profile fact is cited for this text, so it was checked against your whole profile.",
            )
        for m in _PLACEHOLDER.finditer(text):
            add("PLACEHOLDER", "ERROR", "Replace the placeholder with real content.", m.group(0))
        if structural:
            return out

        # Years of experience: bounded by the profile's total.
        stripped = text
        for m in _YEARS.finditer(text):
            n = float(m.group(1))
            if self.years is None:
                add(
                    "UNSUPPORTED_YEARS",
                    "ERROR",
                    "States years of experience, but your profile has no dated experience to support it.",
                    m.group(0),
                )
            elif n > self.years + 0.5:
                add(
                    "UNSUPPORTED_YEARS",
                    "ERROR",
                    f"States {m.group(0)}, but your verified experience totals about {self.years:g} years.",
                    m.group(0),
                )
            stripped = stripped.replace(m.group(0), " ")

        for pat in self.numbered_names:
            stripped = pat.sub(" ", stripped)
        support = self.support_text([r for r in b.refs if r in self.facts])
        allowed = _numbers(support)
        for n in sorted(_numbers(stripped) - allowed):
            add(
                "UNSUPPORTED_NUMBER",
                "ERROR",
                f"The figure {n} does not appear in the cited profile facts.",
                n,
            )

        seen: set[str] = set()
        for m in catalogue().find(text):
            if m.skill.slug not in self.supported_skills and m.skill.slug not in seen:
                seen.add(m.skill.slug)
                mention = text[m.start : m.end]
                if kind == "CV":
                    add(
                        "UNSUPPORTED_SKILL",
                        "ERROR",
                        f"{m.skill.name} is not in your verified profile.",
                        mention,
                    )
                else:
                    add(
                        "UNVERIFIED_SKILL_MENTION",
                        "WARNING",
                        f"Mentions {m.skill.name}, which is not in your verified profile. "
                        "Make sure the text does not imply experience with it.",
                        mention,
                    )

        for level, pat in _DEGREES.items():
            hit = pat.search(text)
            if hit and _LEVEL_RANK[level] > self.degree_rank:
                add(
                    "UNSUPPORTED_CREDENTIAL",
                    "ERROR",
                    "Mentions a degree that your verified education does not include.",
                    hit.group(0),
                )
        cert = _CERTIFIED.search(text)
        if cert and not self.has_certs:
            add(
                "UNSUPPORTED_CREDENTIAL",
                "ERROR",
                "Mentions a certification, but your profile has no verified certifications.",
                cert.group(0),
            )
        return out


def validate(
    content: DocumentContent, facts: list[Fact], years_experience: float | None, degree_levels: list[str]
) -> ValidationReport:
    return Validator(facts, years_experience, degree_levels).check(content)
