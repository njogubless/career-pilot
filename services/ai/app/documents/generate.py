"""Tailored document generation, grounded in the fact sheet.

Flow: facts get short aliases -> the LLM writes a draft that cites aliases ->
aliases map back to stable refs -> the draft becomes doc-v1 blocks -> the
deterministic validator runs -> generated claims with error-level issues are
removed (never shown as content) -> the final document is re-validated.

Structural text (names, titles, employers, dates, degrees, certification
names) is NOT written by the model: entry blocks carry only a reference and
the platform renders them from the profile.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import Any

from app import prompts
from app.documents.schemas import (
    AnswersDraft,
    Block,
    Candidate,
    Claim,
    CoverLetterDraft,
    CVDraft,
    DocumentContent,
    Fact,
    JobContext,
    Kind,
    Section,
    ValidationReport,
)
from app.documents.validate import validate
from app.providers.base import LLMProvider, Usage
from app.providers.fake import register

MAX_POSTING_CHARS = 12_000
_UNSUPPORTED_ANSWER = "The generated answer could not be supported by your profile. Please write it."
# Codes that mean a generated claim is not supported by the profile.
_REMOVE = {
    "UNSUPPORTED_NUMBER",
    "UNSUPPORTED_SKILL",
    "UNSUPPORTED_YEARS",
    "UNSUPPORTED_CREDENTIAL",
    "PLACEHOLDER",
}
_PREFIX = {
    "basics": "B",
    "experience": "E",
    "education": "ED",
    "skill": "S",
    "certification": "C",
    "project": "P",
    "note": "N",
}
_TASK = {"CV": "cv_tailor", "COVER_LETTER": "cover_letter", "APPLICATION_ANSWERS": "app_answers"}


@dataclass
class Generated:
    content: DocumentContent
    validation: ValidationReport
    questions: list[str]
    warnings: list[str]
    usage: Usage
    prompt_id: str


@dataclass
class Aliases:
    to_ref: dict[str, str] = field(default_factory=dict)
    to_alias: dict[str, str] = field(default_factory=dict)

    @classmethod
    def build(cls, facts: list[Fact]) -> Aliases:
        a = cls()
        counters: dict[str, int] = {}
        child_counters: dict[str, int] = {}
        for f in facts:
            if f.kind == "achievement":
                continue
            p = _PREFIX[f.kind]
            counters[p] = counters.get(p, 0) + 1
            a._add(f"{p}{counters[p]}", f.ref)
        for f in facts:
            if f.kind == "achievement" and f.parent in a.to_alias:
                parent = a.to_alias[f.parent]
                child_counters[parent] = child_counters.get(parent, 0) + 1
                a._add(f"{parent}.A{child_counters[parent]}", f.ref)
        return a

    def _add(self, alias: str, ref: str) -> None:
        self.to_ref[alias] = ref
        self.to_alias[ref] = alias

    def resolve(self, aliases: list[str]) -> tuple[list[str], int]:
        refs, bad = [], 0
        for raw in aliases:
            ref = self.to_ref.get(raw.strip().strip("[]").upper())
            if ref is None:
                bad += 1
            elif ref not in refs:
                refs.append(ref)
        return refs, bad


def _neutralise(text: str, tag: str) -> str:
    return re.sub(rf"</?\s*{tag}\s*>", "[tag removed]", text, flags=re.IGNORECASE)


def render_facts(facts: list[Fact], aliases: Aliases) -> str:
    lines = []
    for f in facts:
        alias = aliases.to_alias.get(f.ref)
        if alias is None:
            continue
        detail = "; ".join(f"{k}: {v}" for k, v in f.fields.items() if v)
        indent = "  " if f.kind == "achievement" else ""
        line = f"{indent}[{alias}] {f.kind}: {f.text}"
        if detail:
            line += f" ({detail})"
        lines.append(line)
    return "<candidate_facts>\n" + _neutralise("\n".join(lines), "candidate_facts") + "\n</candidate_facts>"


def render_job(job: JobContext) -> str:
    text = _neutralise(job.text[:MAX_POSTING_CHARS], "job_posting")
    head = f"Title: {job.title}\nCompany: {job.company or 'not stated'}"
    if job.skills:
        head += "\nSkills the posting asks for: " + ", ".join(job.skills[:40])
    return f"<job_posting>\n{_neutralise(head, 'job_posting')}\n\n{text}\n</job_posting>"


def render_questions(questions: list[str]) -> str:
    body = "\n".join(f"Q{i}: {q}" for i, q in enumerate(questions))
    return (
        "<application_questions>\n"
        + _neutralise(body, "application_questions")
        + "\n</application_questions>"
    )


class _Builder:
    def __init__(self, aliases: Aliases, facts: list[Fact]) -> None:
        self.aliases = aliases
        self.facts = {f.ref: f for f in facts}
        self.n = 0
        self.bad_refs = 0

    def block(self, type_: str, text: str = "", refs: list[str] | None = None, **meta: str) -> Block:
        self.n += 1
        return Block(id=f"b{self.n}", type=type_, text=text.strip(), refs=refs or [], meta=meta)  # type: ignore[arg-type]

    def claim(self, c: Claim, type_: str = "paragraph", **meta: str) -> Block:
        refs, bad = self.aliases.resolve(c.refs)
        self.bad_refs += bad
        return self.block(type_, c.text, refs, **meta)

    def refs_of_kind(self, aliases: list[str], kind: str) -> list[str]:
        refs, bad = self.aliases.resolve(aliases)
        self.bad_refs += bad
        return [r for r in refs if self.facts[r].kind == kind]


def _by_start_desc(refs: list[str], facts: dict[str, Fact]) -> list[str]:
    def key(r: str) -> tuple[int, str]:
        f = facts[r].fields
        return (1 if f.get("current") == "true" else 0, f.get("start", ""))

    return sorted(refs, key=key, reverse=True)


def build_cv(draft: CVDraft, aliases: Aliases, facts: list[Fact]) -> tuple[DocumentContent, int]:
    b = _Builder(aliases, facts)
    fmap = b.facts
    sections = [Section(key="contact", title="Contact", blocks=[b.block("contact")])]
    sections.append(Section(key="summary", title="Summary", blocks=[b.claim(c) for c in draft.summary]))

    bullets: dict[str, list[Claim]] = {}
    chosen: list[str] = []
    for e in draft.experiences:
        refs = b.refs_of_kind([e.ref], "experience")
        if refs and refs[0] not in bullets:
            chosen.append(refs[0])
            bullets[refs[0]] = e.bullets
    # Every verified role appears (no hidden gaps), newest first.
    all_exp = [f.ref for f in facts if f.kind == "experience"]
    exp_blocks: list[Block] = []
    for ref in _by_start_desc(all_exp, fmap):
        entry = b.block("entry", refs=[ref])
        exp_blocks.append(entry)
        for c in bullets.get(ref, []):
            blk = b.claim(c, "bullet", parent=entry.id)
            if ref not in blk.refs and not any(fmap[r].parent == ref for r in blk.refs if r in fmap):
                blk.refs.insert(0, ref)
            exp_blocks.append(blk)
    sections.append(Section(key="experience", title="Experience", blocks=exp_blocks))

    proj_blocks: list[Block] = []
    for p in draft.projects:
        refs = b.refs_of_kind([p.ref], "project")
        if not refs:
            continue
        entry = b.block("entry", refs=refs[:1])
        proj_blocks.append(entry)
        for c in p.bullets:
            blk = b.claim(c, "bullet", parent=entry.id)
            if refs[0] not in blk.refs:
                blk.refs.insert(0, refs[0])
            proj_blocks.append(blk)
    if proj_blocks:
        sections.append(Section(key="projects", title="Projects", blocks=proj_blocks))

    skills = b.refs_of_kind(draft.skills, "skill")
    if skills:
        sections.append(Section(key="skills", title="Skills", blocks=[b.block("skills", refs=skills)]))

    for key, title, kind, picked in (
        ("education", "Education", "education", draft.education),
        ("certifications", "Certifications", "certification", draft.certifications),
    ):
        refs = b.refs_of_kind(picked, kind)
        refs += [f.ref for f in facts if f.kind == kind and f.ref not in refs]
        if refs:
            sections.append(Section(key=key, title=title, blocks=[b.block("entry", refs=[r]) for r in refs]))
    return DocumentContent(kind="CV", sections=sections), b.bad_refs


def build_cover_letter(
    draft: CoverLetterDraft, aliases: Aliases, facts: list[Fact]
) -> tuple[DocumentContent, int]:
    b = _Builder(aliases, facts)
    blocks = [b.block("paragraph", draft.greeting, role="greeting")]
    blocks += [b.claim(c) for c in draft.paragraphs]
    blocks.append(b.block("paragraph", draft.sign_off, role="closing"))
    sections = [
        Section(key="contact", title="Contact", blocks=[b.block("contact")]),
        Section(key="letter", title="Letter", blocks=blocks),
    ]
    return DocumentContent(kind="COVER_LETTER", sections=sections), b.bad_refs


def build_answers(
    draft: AnswersDraft, questions: list[str], aliases: Aliases, facts: list[Fact]
) -> tuple[DocumentContent, int]:
    b = _Builder(aliases, facts)
    by_index = {a.question_index: a for a in draft.answers}
    sections = []
    for i, q in enumerate(questions):
        a = by_index.get(i)
        if a is None or a.status == "NEEDS_INPUT" or not a.text.strip():
            ask = (a.question_for_user if a else "") or "Your profile does not answer this. Please write it."
            blk = b.block("answer", "", [], status="NEEDS_INPUT", question_for_user=ask)
        else:
            blk = b.claim(Claim(text=a.text, refs=a.refs), "answer")
        sections.append(Section(key=f"q{i + 1}", title=q, blocks=[blk]))
    return DocumentContent(kind="APPLICATION_ANSWERS", sections=sections), b.bad_refs


def strip_unsupported(content: DocumentContent, report: ValidationReport) -> int:
    """Remove generated claim blocks with error-level support issues."""
    bad = {i.block_id for i in report.issues if i.code in _REMOVE}
    removed = 0
    for s in content.sections:
        keep = []
        for blk in s.blocks:
            if blk.id in bad:
                removed += 1
                if blk.type == "answer":  # keep the question, ask the user instead
                    keep.append(
                        blk.model_copy(
                            update={
                                "text": "",
                                "refs": [],
                                "meta": {
                                    "status": "NEEDS_INPUT",
                                    "question_for_user": _UNSUPPORTED_ANSWER,
                                },
                            }
                        )
                    )
                continue
            keep.append(blk)
        s.blocks = keep
    return removed


def generate(
    *,
    kind: Kind,
    job: JobContext,
    facts: list[Fact],
    candidate: Candidate,
    questions: list[str],
    provider: LLMProvider,
) -> Generated:
    task = _TASK[kind]
    system, prompt_id = prompts.load(task)
    aliases = Aliases.build(facts)
    parts = [render_job(job), render_facts(facts, aliases)]
    if kind == "APPLICATION_ANSWERS":
        parts.append(render_questions(questions))
    years = "unknown" if candidate.years_experience is None else f"{candidate.years_experience:g}"
    parts.append(f"Total verified years of professional experience: {years}.")
    user = "\n\n".join(parts)

    warnings: list[str] = []
    if kind == "CV":
        draft, usage = provider.structured(task=task, system=system, user=user, schema=CVDraft)
        content, bad = build_cv(draft, aliases, facts)
        asked = draft.questions
    elif kind == "COVER_LETTER":
        cl, usage = provider.structured(task=task, system=system, user=user, schema=CoverLetterDraft)
        content, bad = build_cover_letter(cl, aliases, facts)
        asked = cl.questions
    else:
        ans, usage = provider.structured(task=task, system=system, user=user, schema=AnswersDraft)
        content, bad = build_answers(ans, questions, aliases, facts)
        asked = [
            a.question_for_user for a in ans.answers if a.status == "NEEDS_INPUT" and a.question_for_user
        ]

    if bad:
        warnings.append(f"{bad} citation(s) did not match a profile fact and were ignored.")
    first = validate(content, facts, candidate.years_experience, candidate.degree_levels)
    removed = strip_unsupported(content, first)
    if removed:
        warnings.append(
            f"{removed} generated statement(s) were removed because your verified profile "
            "does not support them."
        )
    report = validate(content, facts, candidate.years_experience, candidate.degree_levels)
    return Generated(
        content=content,
        validation=report,
        questions=[q.strip() for q in asked if q.strip()][:10],
        warnings=warnings,
        usage=usage,
        prompt_id=prompt_id,
    )


# ---- Deterministic fakes (tests / offline dev only) ----

_FACT_LINE = re.compile(r"^\s*\[(?P<alias>[A-Z]+\d+(?:\.A\d+)?)\] (?P<kind>\w+): (?P<text>.*)$", re.M)


def _parse_facts(user: str) -> list[tuple[str, str, str]]:
    m = re.search(r"<candidate_facts>\n(.*?)\n</candidate_facts>", user, re.S)
    body = m.group(1) if m else ""
    out = []
    for fm in _FACT_LINE.finditer(body):
        text = re.sub(r" \([^()]*\)$", "", fm.group("text"))
        out.append((fm.group("alias"), fm.group("kind"), text))
    return out


def _first_sentence(text: str) -> str:
    return re.split(r"(?<=[.!?])\s", text.strip(), maxsplit=1)[0]


def _job_field(user: str, name: str) -> str:
    m = re.search(rf"^{name}: (.*)$", user, re.M)
    return m.group(1).strip() if m else ""


@register("cv_tailor")
def _fake_cv(_system: str, user: str) -> dict[str, Any]:
    facts = _parse_facts(user)
    summary = [
        {"text": _first_sentence(t), "refs": [a]} for a, k, t in facts if k == "basics" and a == "B1" and t
    ]
    exps = []
    for a, k, _t in facts:
        if k == "experience":
            bullets = [
                {"text": t, "refs": [ca]}
                for ca, ck, t in facts
                if ck == "achievement" and ca.startswith(a + ".")
            ]
            exps.append({"ref": a, "bullets": bullets[:4]})
    projects = [
        {"ref": a, "bullets": [{"text": _first_sentence(t), "refs": [a]}] if t else []}
        for a, k, t in facts
        if k == "project"
    ]
    return {
        "summary": summary,
        "experiences": exps,
        "skills": [a for a, k, _ in facts if k == "skill"],
        "projects": projects,
        "education": [a for a, k, _ in facts if k == "education"],
        "certifications": [a for a, k, _ in facts if k == "certification"],
        "questions": []
        if exps
        else ["Which roles should this CV include? Your profile has no verified experience."],
    }


@register("cover_letter")
def _fake_cover_letter(_system: str, user: str) -> dict[str, Any]:
    facts = _parse_facts(user)
    title, company = _job_field(user, "Title"), _job_field(user, "Company")
    paragraphs = []
    headline = next(((a, t) for a, k, t in facts if k == "basics"), None)
    if headline:
        paragraphs.append(
            {
                "text": f"I am applying for the {title} role. {_first_sentence(headline[1])}",
                "refs": [headline[0]],
            }
        )
    ach = next(((a, t) for a, k, t in facts if k == "achievement"), None)
    if ach:
        paragraphs.append({"text": f"In a recent role: {ach[1]}", "refs": [ach[0]]})
    note = next(((a, t) for a, k, t in facts if k == "note"), None)
    questions = []
    if note:
        paragraphs.append({"text": note[1], "refs": [note[0]]})
    else:
        questions.append(f"What draws you to {company or 'this company'}?")
    return {
        "greeting": "Dear Hiring Team,",
        "paragraphs": paragraphs,
        "sign_off": "Kind regards,",
        "questions": questions,
    }


@register("app_answers")
def _fake_answers(_system: str, user: str) -> dict[str, Any]:
    facts = _parse_facts(user)
    m = re.search(r"<application_questions>\n(.*?)\n</application_questions>", user, re.S)
    qs = re.findall(r"^Q(\d+): (.*)$", m.group(1), re.M) if m else []
    ach = next(((a, t) for a, k, t in facts if k == "achievement"), None)
    answers = []
    for idx, q in qs:
        personal = re.search(r"salary|sponsor|visa|why|notice|start date", q, re.I)
        if personal or ach is None:
            answers.append(
                {
                    "question_index": int(idx),
                    "status": "NEEDS_INPUT",
                    "text": "",
                    "refs": [],
                    "question_for_user": f"Please answer: {q}",
                }
            )
        else:
            answers.append(
                {
                    "question_index": int(idx),
                    "status": "ANSWERED",
                    "text": ach[1],
                    "refs": [ach[0]],
                    "question_for_user": "",
                }
            )
    return {"answers": answers}
