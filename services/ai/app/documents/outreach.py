"""Grounded recruiter emails (Phase 7).

Same contract as document generation: facts under aliases, claims cite
facts, the deterministic validator runs, and unsupported claims are removed
before the draft is returned. The platform assembles and sends the email.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Any, Literal

from pydantic import BaseModel, Field

from app import prompts
from app.documents.generate import (
    Aliases,
    _Builder,
    _neutralise,
    render_facts,
    render_job,
    strip_unsupported,
)
from app.documents.schemas import (
    Candidate,
    Claim,
    DocumentContent,
    Fact,
    JobContext,
    Section,
    ValidationReport,
)
from app.documents.validate import validate
from app.providers.base import LLMProvider, Usage
from app.providers.fake import register

EmailKind = Literal["INTRODUCTION", "FOLLOW_UP"]


class Contact(BaseModel):
    name: str = Field(default="", max_length=200)
    title: str = Field(default="", max_length=200)
    company: str = Field(default="", max_length=200)


class OutreachDraft(BaseModel):
    subject: str = Field(description="Plain subject under 80 characters; empty for FOLLOW_UP.")
    greeting: str = Field(description="'Hi <first name>,' if the name is known, else 'Hello,'.")
    paragraphs: list[Claim] = Field(description="Short paragraphs, each citing its supporting facts.")
    sign_off: str = Field(description="e.g. 'Best regards,'.")
    questions: list[str] = Field(description="Questions for the candidate about missing information.")


@dataclass
class Email:
    subject: str
    paragraphs: list[str]
    greeting: str
    sign_off: str
    validation: ValidationReport
    questions: list[str]
    warnings: list[str]
    usage: Usage
    prompt_id: str


def render_contact(c: Contact) -> str:
    body = f"Name: {c.name or 'unknown'}\nTitle: {c.title or 'unknown'}\nCompany: {c.company or 'unknown'}"
    return "<contact>\n" + _neutralise(body, "contact") + "\n</contact>"


def draft_email(
    *,
    kind: EmailKind,
    job: JobContext,
    facts: list[Fact],
    candidate: Candidate,
    contact: Contact,
    provider: LLMProvider,
) -> Email:
    system, prompt_id = prompts.load("outreach_email")
    aliases = Aliases.build(facts)
    years = "unknown" if candidate.years_experience is None else f"{candidate.years_experience:g}"
    user = "\n\n".join(
        [
            f"Email kind: {kind}",
            render_job(job),
            render_contact(contact),
            render_facts(facts, aliases),
            f"Total verified years of professional experience: {years}.",
        ]
    )
    draft, usage = provider.structured(task="outreach_email", system=system, user=user, schema=OutreachDraft)

    b = _Builder(aliases, facts)
    blocks = [b.claim(p) for p in draft.paragraphs]
    content = DocumentContent(
        kind="COVER_LETTER", sections=[Section(key="letter", title="Email", blocks=blocks)]
    )
    warnings = []
    if b.bad_refs:
        warnings.append(f"{b.bad_refs} citation(s) did not match a profile fact and were ignored.")
    removed = strip_unsupported(
        content, validate(content, facts, candidate.years_experience, candidate.degree_levels)
    )
    if removed:
        warnings.append(
            f"{removed} generated statement(s) were removed because your verified profile "
            "does not support them."
        )
    report = validate(content, facts, candidate.years_experience, candidate.degree_levels)
    paragraphs = [blk.text for blk in content.sections[0].blocks if blk.text]
    greeting = draft.greeting.strip() or "Hello,"
    # Never address someone by a name the candidate did not record.
    if contact.name.strip() == "" and greeting.lower() not in ("hello,", "hi,", "hello there,"):
        greeting = "Hello,"
    return Email(
        subject=draft.subject.strip()[:200],
        paragraphs=paragraphs,
        greeting=greeting,
        sign_off=draft.sign_off.strip() or "Best regards,",
        validation=report,
        questions=[q.strip() for q in draft.questions if q.strip()][:5],
        warnings=warnings,
        usage=usage,
        prompt_id=prompt_id,
    )


# ---- Deterministic fake (tests / offline dev only) ----


@register("outreach_email")
def _fake_outreach(_system: str, user: str) -> dict[str, Any]:
    kind = re.search(r"^Email kind: (\w+)", user, re.M)
    name = re.search(r"^Name: (.*)$", user, re.M)
    title = re.search(r"^Title: (.*)$", user, re.M)
    first = name.group(1).split()[0] if name and name.group(1) != "unknown" else ""
    facts = re.findall(r"^\s*\[([A-Z]+\d+(?:\.A\d+)?)\] (\w+): (.*?)(?: \([^()]*\))?$", user, re.M)
    greeting = f"Hi {first}," if first else "Hello,"
    role = title.group(1) if title else "the role"
    if kind and kind.group(1) == "FOLLOW_UP":
        return {
            "subject": "",
            "greeting": greeting,
            "paragraphs": [{"text": f"I wanted to follow up on my note about the {role} role.", "refs": []}],
            "sign_off": "Best regards,",
            "questions": [],
        }
    paragraphs = [{"text": f"I'm writing about the {role} role.", "refs": []}]
    ach = next(((a, t) for a, k, t in facts if k == "achievement"), None)
    if ach:
        paragraphs.append({"text": ach[1], "refs": [ach[0]]})
    paragraphs.append({"text": "Would you be open to a short conversation?", "refs": []})
    return {
        "subject": f"{role}: application interest",
        "greeting": greeting,
        "paragraphs": paragraphs,
        "sign_off": "Best regards,",
        "questions": [],
    }
