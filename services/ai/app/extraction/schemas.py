"""Profile proposal schema produced from a CV.

`*Draft` models are what the LLM fills (structured output). The response
models add `evidence_found`, computed deterministically by checking that each
item's quoted evidence actually occurs in the document text. The platform
shows unsupported items to the user as unconfirmed.
"""

from __future__ import annotations

from pydantic import BaseModel, Field


class Link(BaseModel):
    label: str
    url: str


class PersonalDraft(BaseModel):
    full_name: str | None = Field(description="Name exactly as written, or null.")
    headline: str | None = Field(
        description="Professional title as written in the CV (e.g. 'Senior Backend Engineer'), or null."
    )
    summary: str | None = Field(
        description="The CV's own summary/profile paragraph, copied verbatim, or null."
    )
    location_city: str | None = Field(description="City of residence if explicitly stated, else null.")
    location_country: str | None = Field(
        description="ISO 3166-1 alpha-2 code ONLY if the country is explicitly stated, else null."
    )
    links: list[Link] = Field(description="Profile/portfolio URLs present in the CV.")


class ExperienceDraft(BaseModel):
    employer: str
    title: str
    location: str | None
    start_date: str | None = Field(description="YYYY-MM or YYYY as written; null if absent.")
    end_date: str | None = Field(description="YYYY-MM or YYYY; null if current or absent.")
    is_current: bool
    description: str | None
    achievements: list[str] = Field(description="Bullet points copied from the CV, not rewritten.")
    evidence: str = Field(
        description="A short verbatim quote from the CV supporting this entry (e.g. the title/employer line)."
    )


class EducationDraft(BaseModel):
    institution: str
    degree: str | None
    field: str | None
    start_date: str | None
    end_date: str | None
    evidence: str


class SkillDraft(BaseModel):
    name: str
    category: str | None = Field(description="e.g. Language, Framework, Cloud, Database, Tool, Soft skill.")
    evidence: str = Field(description="Verbatim text from the CV where the skill appears.")


class CertificationDraft(BaseModel):
    name: str
    issuer: str | None
    issued_date: str | None
    evidence: str


class ProjectDraft(BaseModel):
    name: str
    description: str | None
    url: str | None
    evidence: str


class ProposalDraft(BaseModel):
    personal: PersonalDraft
    experiences: list[ExperienceDraft]
    education: list[EducationDraft]
    skills: list[SkillDraft]
    certifications: list[CertificationDraft]
    projects: list[ProjectDraft]
    warnings: list[str] = Field(description="Ambiguities the user should review (e.g. unclear dates).")


# ---- Response models (draft + deterministic grounding check) ----


class Experience(ExperienceDraft):
    evidence_found: bool


class Education(EducationDraft):
    evidence_found: bool


class Skill(SkillDraft):
    evidence_found: bool


class Certification(CertificationDraft):
    evidence_found: bool


class Project(ProjectDraft):
    evidence_found: bool


class Personal(PersonalDraft):
    # Which personal fields appear verbatim in the document.
    found: dict[str, bool]


class ProfileProposal(BaseModel):
    personal: Personal
    experiences: list[Experience]
    education: list[Education]
    skills: list[Skill]
    certifications: list[Certification]
    projects: list[Project]
    warnings: list[str]
