"""CV → profile proposal, grounded against the source text."""

from __future__ import annotations

import re
from typing import Any

from app import prompts
from app.extraction.schemas import (
    Certification,
    Education,
    Experience,
    Personal,
    ProfileProposal,
    Project,
    ProposalDraft,
    Skill,
)
from app.providers.base import LLMProvider, Usage
from app.providers.fake import register

_SPACE = re.compile(r"\s+")


def _norm(s: str) -> str:
    return _SPACE.sub(" ", s).strip().lower()


def wrap_untrusted(text: str) -> str:
    """Delimit untrusted document text. Closing tags inside the document are
    neutralised so the content cannot escape its envelope."""
    safe = re.sub(r"</?\s*cv_document\s*>", "[tag removed]", text, flags=re.IGNORECASE)
    return f"<cv_document>\n{safe}\n</cv_document>"


class Grounder:
    """Checks that quoted evidence actually appears in the document."""

    def __init__(self, text: str) -> None:
        self._haystack = _norm(text)

    def found(self, quote: str | None) -> bool:
        if not quote:
            return False
        q = _norm(quote)
        return len(q) >= 2 and q in self._haystack


def ground(draft: ProposalDraft, text: str) -> ProfileProposal:
    g = Grounder(text)
    p = draft.personal
    personal = Personal(
        **p.model_dump(),
        found={
            "full_name": g.found(p.full_name),
            "headline": g.found(p.headline),
            "summary": g.found(p.summary),
            "location_city": g.found(p.location_city),
            "links": all(g.found(link.url) for link in p.links) if p.links else False,
        },
    )
    warnings = list(draft.warnings)
    proposal = ProfileProposal(
        personal=personal,
        experiences=[
            Experience(**e.model_dump(), evidence_found=g.found(e.evidence)) for e in draft.experiences
        ],
        education=[Education(**e.model_dump(), evidence_found=g.found(e.evidence)) for e in draft.education],
        skills=[
            Skill(**s.model_dump(), evidence_found=g.found(s.evidence) or g.found(s.name))
            for s in draft.skills
        ],
        certifications=[
            Certification(**c.model_dump(), evidence_found=g.found(c.evidence)) for c in draft.certifications
        ],
        projects=[Project(**pr.model_dump(), evidence_found=g.found(pr.evidence)) for pr in draft.projects],
        warnings=warnings,
    )
    unsupported = sum(
        not item.evidence_found
        for group in (
            proposal.experiences,
            proposal.education,
            proposal.skills,
            proposal.certifications,
            proposal.projects,
        )
        for item in group
    )
    if unsupported:
        proposal.warnings.append(
            f"{unsupported} item(s) could not be matched to text in your CV. "
            "Check them carefully before saving."
        )
    return proposal


def extract_profile(text: str, provider: LLMProvider) -> tuple[ProfileProposal, Usage, str]:
    system, prompt_id = prompts.load("cv_extract")
    user = "Extract the professional profile from this CV.\n\n" + wrap_untrusted(text)
    draft, usage = provider.structured(task="cv_extract", system=system, user=user, schema=ProposalDraft)
    return ground(draft, text), usage, prompt_id


# ---- Deterministic fake (tests / offline dev only) ----

_KNOWN_SKILLS = [
    "Python",
    "Go",
    "Golang",
    "Java",
    "TypeScript",
    "JavaScript",
    "React",
    "Node.js",
    "PostgreSQL",
    "MySQL",
    "Redis",
    "Docker",
    "Kubernetes",
    "AWS",
    "GCP",
    "Azure",
    "Terraform",
    "FastAPI",
    "Django",
    "Flask",
    "GraphQL",
    "Kafka",
    "Rust",
    "C++",
    "SQL",
    "Git",
    "Linux",
    "Figma",
    "Excel",
    "Product Management",
    "Machine Learning",
]
_URL = re.compile(r"https?://[^\s)>\]]+")
_ROLE = re.compile(
    r"^(?P<title>[^,|]+?)\s*(?:,|\bat\b|\|)\s*(?P<employer>[^,(|]+?)\s*[,(|]?\s*"
    r"(?P<start>\d{4})\s*[-\u2013]\s*(?P<end>\d{4}|present|current|now)\)?\s*$",
    re.IGNORECASE,
)


@register("cv_extract")
def _fake_cv_extract(_system: str, user: str) -> dict[str, Any]:
    m = re.search(r"<cv_document>\n(.*)\n</cv_document>", user, re.DOTALL)
    text = m.group(1) if m else user
    lines = [ln.strip() for ln in text.splitlines() if ln.strip()]
    lower = text.lower()

    skills = []
    for name in _KNOWN_SKILLS:
        if re.search(r"(?<![\w.+])" + re.escape(name.lower()) + r"(?![\w+])", lower):
            skills.append({"name": name, "category": None, "evidence": name})

    experiences = []
    for ln in lines:
        rm = _ROLE.match(ln)
        if rm:
            end = rm.group("end").lower()
            current = end in {"present", "current", "now"}
            experiences.append(
                {
                    "employer": rm.group("employer").strip(),
                    "title": rm.group("title").strip(),
                    "location": None,
                    "start_date": rm.group("start"),
                    "end_date": None if current else end,
                    "is_current": current,
                    "description": None,
                    "achievements": [],
                    "evidence": ln,
                }
            )

    warnings = []
    if "ignore" in lower and "instruction" in lower:
        warnings.append("The document contains instruction-like text, which was ignored.")
    return {
        "personal": {
            "full_name": lines[0] if lines else None,
            "headline": lines[1] if len(lines) > 1 and len(lines[1]) <= 80 else None,
            "summary": None,
            "location_city": None,
            "location_country": None,
            "links": [{"label": "Link", "url": u.rstrip(".,")} for u in _URL.findall(text)][:10],
        },
        "experiences": experiences,
        "education": [],
        "skills": skills,
        "certifications": [],
        "projects": [],
        "warnings": warnings,
    }
