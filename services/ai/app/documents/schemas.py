"""Application documents: fact sheet, document content model (doc-v1),
validation report and the LLM draft schemas.

The platform (Go) sends a *fact sheet*: the user's VERIFIED profile facts,
each with a stable reference (`exp:<uuid>`, `exp:<uuid>#a2`, `skill:<uuid>`,
`note:1`, ...). Generated text must cite the facts it relies on; the
deterministic validator checks those citations and the claims in the text.

`*Draft` models are filled by the LLM (structured output). They cite facts by
short aliases (E1, S3, ...) that are mapped back to stable references here,
so the model never sees or invents database identifiers.
"""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, Field

DOC_SCHEMA = "doc-v1"

Kind = Literal["CV", "COVER_LETTER", "APPLICATION_ANSWERS"]
FactKind = Literal[
    "basics", "experience", "achievement", "education", "skill", "certification", "project", "note"
]


# ---- Inputs from the platform ----


class Fact(BaseModel):
    ref: str = Field(max_length=120)
    kind: FactKind
    text: str = Field(max_length=6000)
    # Structured fields (title, employer, dates, degree level ...) as strings.
    fields: dict[str, str] = Field(default_factory=dict)
    parent: str | None = Field(default=None, max_length=120)


class Candidate(BaseModel):
    name: str = Field(max_length=120)
    email: str = Field(default="", max_length=320)
    location: str = Field(default="", max_length=200)
    links: list[str] = Field(default_factory=list, max_length=20)
    years_experience: float | None = None
    degree_levels: list[str] = Field(default_factory=list)


class JobContext(BaseModel):
    title: str = Field(max_length=500)
    company: str = Field(default="", max_length=300)
    text: str = Field(default="", max_length=60_000)
    skills: list[str] = Field(default_factory=list, max_length=200)


# ---- Document content (doc-v1) ----

BlockType = Literal["contact", "heading", "entry", "paragraph", "bullet", "skills", "answer"]
CLAIM_TYPES = frozenset({"paragraph", "bullet", "answer"})


class Block(BaseModel):
    id: str = Field(max_length=40)
    type: BlockType
    text: str = Field(default="", max_length=5000)
    refs: list[str] = Field(default_factory=list, max_length=50)
    # Presentation and state hints: role (greeting/closing), parent entry,
    # status (NEEDS_INPUT), question (for answers), dates (entries).
    meta: dict[str, str] = Field(default_factory=dict)


class Section(BaseModel):
    key: str = Field(max_length=40)
    title: str = Field(max_length=120)
    blocks: list[Block] = Field(default_factory=list, max_length=200)


class DocumentContent(BaseModel):
    schema_version: Literal["doc-v1"] = DOC_SCHEMA
    kind: Kind
    sections: list[Section] = Field(max_length=30)


# ---- Validation report ----

Severity = Literal["ERROR", "WARNING"]


class Issue(BaseModel):
    block_id: str
    code: str
    severity: Severity
    message: str
    excerpt: str = ""


class ValidationReport(BaseModel):
    validator_version: str
    status: Literal["PASSED", "WARNINGS", "FAILED"]
    issues: list[Issue]
    claims_checked: int


# ---- LLM drafts ----


class Claim(BaseModel):
    text: str = Field(description="One sentence or bullet, written for the candidate.")
    refs: list[str] = Field(
        description="Aliases of the facts that support this text (e.g. ['E1.A2', 'S4']); never empty."
    )


class EntryDraft(BaseModel):
    ref: str = Field(description="Alias of the experience or project fact (e.g. 'E2', 'P1').")
    bullets: list[Claim] = Field(description="Tailored bullets, each supported by this entry's facts.")


class CVDraft(BaseModel):
    summary: list[Claim] = Field(description="Two to four sentences of professional summary.")
    experiences: list[EntryDraft] = Field(description="Experiences to include, most relevant first.")
    skills: list[str] = Field(description="Aliases of skill facts to list, most relevant first.")
    projects: list[EntryDraft] = Field(description="Relevant projects; may be empty.")
    education: list[str] = Field(description="Aliases of education facts to include.")
    certifications: list[str] = Field(description="Aliases of certification facts to include.")
    questions: list[str] = Field(
        description="Questions for the candidate about information the CV needs but the facts lack."
    )


class CoverLetterDraft(BaseModel):
    greeting: str = Field(description="Salutation, e.g. 'Dear Hiring Team,'. No invented names.")
    paragraphs: list[Claim] = Field(description="Three or four paragraphs. Each cites its supporting facts.")
    sign_off: str = Field(description="Closing line such as 'Kind regards,'.")
    questions: list[str] = Field(
        description="Questions for the candidate, e.g. their motivation for this company if no note gives it."
    )


class AnswerDraft(BaseModel):
    question_index: int = Field(description="Zero-based index of the application question.")
    status: Literal["ANSWERED", "NEEDS_INPUT"]
    text: str = Field(description="The answer, or empty when NEEDS_INPUT.")
    refs: list[str] = Field(description="Aliases of supporting facts.")
    question_for_user: str = Field(
        description="When NEEDS_INPUT: what the candidate must provide. Otherwise empty."
    )


class AnswersDraft(BaseModel):
    answers: list[AnswerDraft]
