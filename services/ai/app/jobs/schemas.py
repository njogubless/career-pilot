"""Job requirement analysis schema.

Every populated field carries verbatim evidence from the posting. Fields the
posting does not state stay UNKNOWN/None: the matching engine treats them as
unknown rather than guessing (master prompt §7).
"""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, Field

Importance = Literal["REQUIRED", "PREFERRED", "MENTIONED"]
Seniority = Literal[
    "INTERN",
    "JUNIOR",
    "MID",
    "SENIOR",
    "STAFF",
    "PRINCIPAL",
    "LEAD",
    "MANAGER",
    "DIRECTOR",
    "EXECUTIVE",
    "UNKNOWN",
]
Sponsorship = Literal["AVAILABLE", "NOT_AVAILABLE", "UNKNOWN"]
Degree = Literal["NONE_STATED", "BACHELORS", "MASTERS", "DOCTORATE"]


class SkillRequirement(BaseModel):
    slug: str | None = Field(description="Canonical catalogue slug; null when the skill is not catalogued.")
    name: str
    importance: Importance
    evidence: str = Field(description="Verbatim text from the posting.")


class JobAnalysis(BaseModel):
    method: Literal["DETERMINISTIC", "LLM"]
    analyzer_version: str
    skills: list[SkillRequirement]
    min_years: int | None = None
    years_evidence: str | None = None
    seniority: Seniority = "UNKNOWN"
    seniority_evidence: str | None = None
    sponsorship: Sponsorship = "UNKNOWN"
    sponsorship_evidence: str | None = None
    work_authorization: list[str] = Field(
        default_factory=list, description="ISO country codes the posting requires authorisation for."
    )
    work_authorization_evidence: str | None = None
    degree: Degree = "NONE_STATED"
    degree_required: bool | None = None
    degree_evidence: str | None = None
    warnings: list[str] = Field(default_factory=list)


# ---- LLM draft (structured output). Grounded and normalised afterwards. ----


class SkillDraft(BaseModel):
    name: str = Field(description="Skill, tool, technology or competency as named in the posting.")
    importance: Importance = Field(
        description="REQUIRED if listed as a requirement/qualification, PREFERRED if 'nice to have', "
        "'preferred', 'bonus' or 'a plus', MENTIONED if only referenced in the role description."
    )
    evidence: str = Field(description="Short verbatim quote from the posting containing the skill.")


class JobAnalysisDraft(BaseModel):
    skills: list[SkillDraft]
    min_years: int | None = Field(
        description="Minimum years of professional experience REQUIRED, only if explicitly stated; else null."
    )
    years_evidence: str | None = Field(description="Verbatim quote stating the years, or null.")
    seniority: Seniority = Field(description="Level stated in the title or text; UNKNOWN if not stated.")
    seniority_evidence: str | None
    sponsorship: Sponsorship = Field(
        description="AVAILABLE or NOT_AVAILABLE only if the posting explicitly says so; else UNKNOWN."
    )
    sponsorship_evidence: str | None
    work_authorization: list[str] = Field(
        description="ISO 3166-1 alpha-2 codes of countries where the posting explicitly REQUIRES existing "
        "work authorisation. Empty if not stated."
    )
    work_authorization_evidence: str | None
    degree: Degree
    degree_required: bool | None = Field(
        description="true if a degree is mandatory, false if optional/'or equivalent experience', "
        "null if no degree is mentioned."
    )
    degree_evidence: str | None
