"""Job posting → structured requirements.

Two tiers (ADR-019):
  * DETERMINISTIC: catalogue matching plus documented heuristics. It is cheap,
    reproducible and runs for every job.
  * LLM: structured extraction on demand. Its evidence is grounded against the
    posting and its skills are normalised through the same catalogue.
"""

from __future__ import annotations

import re
from typing import Any

from app import prompts
from app.extraction.cv import Grounder
from app.jobs.schemas import (
    Degree,
    Importance,
    JobAnalysis,
    JobAnalysisDraft,
    Seniority,
    SkillRequirement,
    Sponsorship,
)
from app.providers.base import LLMProvider, Usage
from app.providers.fake import register
from app.taxonomy import catalogue

DETERMINISTIC_VERSION = "jd-det-2"
MAX_LLM_CHARS = 30_000
_EVIDENCE_MAX = 240

_I = re.IGNORECASE

# ---- Section detection ----
_PREFERRED = re.compile(
    r"\b(nice[- ]to[- ]haves?|preferred|bonus(?:\s+points)?|pluses|(?:is|are|would be)?\s*a plus|desirable|"
    r"good[- ]to[- ]haves?|extra credit|would be (?:great|nice)|ideally|not required|"
    r"even better|stand out)\b",
    _I,
)
_IGNORED_HEAD = re.compile(
    r"\b(benefits|perks|about us|about the company|compensation|pay range|salary range|our values|"
    r"our mission|equal (?:employment )?opportunity|why join|what we offer|life at|accommodations?|"
    r"privacy|how we work)\b",
    _I,
)
_REQUIRED_HEAD = re.compile(
    r"\b(requirements?|qualifications?|what you(?:'|\u2019)?ll need|what you need|"
    r"what you(?:'|\u2019)?ll bring|"
    r"what we(?:'|\u2019)?re looking for|who you are|about you|you have|you will have|you bring|"
    r"must[- ]haves?|skills|minimum|you might be a fit)\b",
    _I,
)
_ROLE_HEAD = re.compile(
    r"\b(responsibilities|what you(?:'|\u2019)?ll do|what you will do|the role|about the role|role overview|"
    r"day[- ]to[- ]day|in this role|your impact|the difference you will make|the opportunity|"
    r"about the team|the team|what you(?:'|\u2019)?ll work on)\b",
    _I,
)
_ABOUT_COMPANY = re.compile(r"^\s*about\s+(?!the role|the team|you\b)[\w.&' -]{1,40}:?\s*$", _I)
# Legal and recruiting boilerplate mentions terms ("talent acquisition",
# "accommodation") that are not requirements of the role.
_BOILERPLATE = re.compile(
    r"\b(equal (?:employment )?opportunit|talent acquisition team|reasonable accommodation|e-verify|"
    r"privacy (?:policy|notice)|recruitment (?:privacy|fraud)|do not discriminate|without regard to|"
    r"recruiter will|applicable law|background check|protected (?:veteran|characteristic))",
    _I,
)
_HAS_DIGIT = re.compile(r"\d")


def _is_heading(line: str) -> bool:
    stripped = line.strip()
    if not stripped or len(stripped) > 90:
        return False
    if stripped.endswith(":"):
        return True
    words = stripped.split()
    return (
        len(words) <= 6
        and stripped[0].isupper()
        and not _HAS_DIGIT.search(stripped)
        and stripped[-1] not in ".!?"
    )


def _heading_context(line: str) -> str | None:
    """Context a heading switches to, or None if the line is not a known
    heading (short skill-list items look like headings but are content)."""
    if _PREFERRED.search(line):
        return "PREFERRED"
    if _ABOUT_COMPANY.match(line) or _IGNORED_HEAD.search(line):
        return "IGNORED"
    if _ROLE_HEAD.search(line):
        return "MENTIONED"
    if _REQUIRED_HEAD.search(line):
        return "REQUIRED"
    return None


def _sections(text: str) -> list[tuple[str, int, str]]:
    """Return (line, offset, context) where context is REQUIRED, PREFERRED,
    MENTIONED or IGNORED, derived from the nearest preceding heading and from
    inline markers such as 'a plus'."""
    out: list[tuple[str, int, str]] = []
    context = "MENTIONED"
    offset = 0
    for line in text.split("\n"):
        start = offset
        offset += len(line) + 1
        if not line.strip():
            continue
        heading = _heading_context(line) if _is_heading(line) else None
        if heading is not None:
            context = heading
            continue
        line_ctx = context
        if _BOILERPLATE.search(line):
            line_ctx = "IGNORED"
        elif line_ctx != "IGNORED" and _PREFERRED.search(line):
            line_ctx = "PREFERRED"
        out.append((line, start, line_ctx))
    return out


EMBED_CHARS = 1500


def embedding_text(title: str, company: str, text: str) -> str:
    """What a job is embedded from: title, company, then role-relevant lines.
    Company boilerplate, benefits and legal sections are skipped; they say
    little about the role and dilute similarity."""
    head = title if not company else f"{title} at {company}"
    body: list[str] = []
    used = 0
    for line, _off, ctx in _sections(text):
        if ctx == "IGNORED":
            continue
        line = line.strip()
        if used + len(line) > EMBED_CHARS:
            break
        body.append(line)
        used += len(line) + 1
    return head + "\n\n" + "\n".join(body)


def _evidence(line: str, start: int = 0, end: int | None = None) -> str:
    """A verbatim window of `line` around [start, end)."""
    line = line.strip() if end is None else line
    if len(line) <= _EVIDENCE_MAX:
        return line.strip()
    end = end if end is not None else start
    left = max(0, start - _EVIDENCE_MAX // 2)
    return line[left : left + _EVIDENCE_MAX].strip()


# ---- Field heuristics ----
_YEARS = re.compile(
    r"(?:(?:minimum|at least|min\.?)\s+(?:of\s+)?)?(\d{1,2})\s*(?:\+|plus)?\s*"
    # "years" may run straight into the next sentence in sources that drop
    # separators ("7+ yearsIMPORTANT"): only a lowercase letter ends the match.
    r"(?:(?:-|\u2013|\u2014|to)\s*\d{1,2}\s*\+?\s*)?years?(?-i:(?![a-z]))",
    _I,
)
_EXPERIENCE = re.compile(r"experience", _I)  # no leading boundary: "ONLYExperience"

_SENIORITY: list[tuple[Seniority, re.Pattern[str]]] = [
    ("INTERN", re.compile(r"\bintern(ship)?s?\b", _I)),
    ("EXECUTIVE", re.compile(r"\b(chief|cto|ceo|cfo|coo|ciso|vp|vice president|svp|evp)\b", _I)),
    ("DIRECTOR", re.compile(r"\b(director|head of)\b", _I)),
    ("PRINCIPAL", re.compile(r"\b(principal|distinguished|fellow)\b", _I)),
    ("STAFF", re.compile(r"\bstaff\b", _I)),
    (
        "MANAGER",
        re.compile(
            r"\b(engineering|software|development|data science|design|people)\s+manager\b|"
            r"\bmanager,\s*(engineering|software|data|design)\b",
            _I,
        ),
    ),
    ("LEAD", re.compile(r"\b(tech(nical)? lead|team lead|lead)\b", _I)),
    ("SENIOR", re.compile(r"\b(senior|sr\.?)(?=\s|,|$)", _I)),
    ("JUNIOR", re.compile(r"\b(junior|jr\.?|entry[- ]level|graduate|new grad)(?=\s|,|$)", _I)),
    ("MID", re.compile(r"\b(mid[- ]level|intermediate)\b", _I)),
    ("SENIOR", re.compile(r"\s(III)\b")),
    ("MID", re.compile(r"\s(II)\b")),
    ("JUNIOR", re.compile(r"\s(I)\s*$")),
]

_NO_SPONSOR = [
    re.compile(
        r"\b(?:unable|not able|cannot|can't|can not|will not|won't|do not|don't|does not|doesn't|"
        r"is not able|are not able|not in a position)\s+(?:to\s+)?(?:currently\s+)?"
        r"(?:offer|provide|support|sponsor)\w*\b[^.\n]{0,60}?\b(?:visa|sponsorship|work authori[sz]ation|"
        r"immigration)",
        _I,
    ),
    re.compile(r"\bno\s+(?:visa\s+)?sponsorship\b", _I),
    re.compile(r"\bsponsorship\s+(?:is\s+)?not\s+(?:available|offered|provided|possible)\b", _I),
    re.compile(
        r"\bwithout\s+(?:the\s+need\s+for\s+)?(?:current\s+or\s+future\s+)?(?:visa\s+)?sponsorship\b", _I
    ),
]
_YES_SPONSOR = [
    re.compile(r"\b(?:visa\s+)?sponsorship\s+(?:is\s+)?(?:available|offered|provided)\b", _I),
    re.compile(
        r"\b(?:we|will|can)\s+(?:can\s+|will\s+)?"
        r"(?:sponsor|offer visa sponsorship|provide visa sponsorship)\b",
        _I,
    ),
    re.compile(
        r"\bvisa\s+(?:support|sponsorship)\s+(?:and|&)\s+relocation\b|"
        r"\brelocation\s+(?:and|&)\s+visa\s+(?:support|sponsorship)\b",
        _I,
    ),
]

_COUNTRY_NAMES = {
    "united states": "US",
    "united states of america": "US",
    "us": "US",
    "u.s.": "US",
    "u.s": "US",
    "usa": "US",
    "u.s.a.": "US",
    "america": "US",
    "united kingdom": "GB",
    "uk": "GB",
    "u.k.": "GB",
    "great britain": "GB",
    "england": "GB",
    "canada": "CA",
    "australia": "AU",
    "new zealand": "NZ",
    "ireland": "IE",
    "germany": "DE",
    "france": "FR",
    "netherlands": "NL",
    "the netherlands": "NL",
    "spain": "ES",
    "portugal": "PT",
    "italy": "IT",
    "poland": "PL",
    "sweden": "SE",
    "denmark": "DK",
    "norway": "NO",
    "finland": "FI",
    "switzerland": "CH",
    "austria": "AT",
    "belgium": "BE",
    "india": "IN",
    "singapore": "SG",
    "japan": "JP",
    "south korea": "KR",
    "korea": "KR",
    "brazil": "BR",
    "mexico": "MX",
    "argentina": "AR",
    "colombia": "CO",
    "israel": "IL",
    "united arab emirates": "AE",
    "uae": "AE",
    "kenya": "KE",
    "nigeria": "NG",
    "south africa": "ZA",
    "philippines": "PH",
}
_WORK_AUTH = re.compile(
    r"\b(?:must|need to|needs to|required to|should|will need to)\s+(?:already\s+)?(?:be\s+|have\s+)?"
    r"(?:legally\s+)?(?:authori[sz]ed|eligible|permitted|the right|authori[sz]ation)\s+to\s+work\s+in\s+"
    r"(?:the\s+)?([A-Za-z][A-Za-z. ]{1,40}?)(?=[,.;:()\n]|\s+(?:without|on|for|at|and|or|by)\b|$)",
    _I,
)
_AUTH_REQUIRED = re.compile(
    r"\b(US|U\.S\.|UK|Canadian|Australian)\s+work\s+authori[sz]ation\s+(?:is\s+)?required\b"
)

_DEGREE_LINE = re.compile(r"\b(degree|bachelor|master'?s|masters|ph\.?d|doctorate|diploma)\b", _I)
_DEGREE_LEVELS: list[tuple[Degree, re.Pattern[str]]] = [
    (
        "BACHELORS",
        re.compile(r"(?i:\b(?:bachelor'?s?|undergraduate)\b)|\b(?:B\.S\.?|BS|B\.A\.?|BA|BSc|BEng)\b"),
    ),
    ("MASTERS", re.compile(r"(?i:\b(?:master'?s|masters|mba)\b)|\b(?:M\.S\.?|MS|MSc|MEng)\b")),
    ("DOCTORATE", re.compile(r"(?i:\b(?:ph\.?d|doctorate|doctoral)\b)")),
]
_DEGREE_OPTIONAL = re.compile(
    r"\bor\s+(?:equivalent|comparable|related)\b|\bequivalent\s+(?:practical\s+)?experience\b|"
    r"\bnot\s+required\b|\bpreferred\b|\bor\s+relevant\b",
    _I,
)


def analyze_deterministic(title: str, text: str) -> JobAnalysis:
    cat = catalogue()
    rank = {"REQUIRED": 3, "MENTIONED": 2, "PREFERRED": 1}
    found: dict[str, SkillRequirement] = {}
    min_years: int | None = None
    years_ev: str | None = None
    degree: Degree = "NONE_STATED"
    degree_required: bool | None = None
    degree_ev: str | None = None

    for line, _off, ctx in _sections(text):
        if ctx == "IGNORED":
            continue
        importance: Importance = ctx  # type: ignore[assignment]
        for m in cat.find(line):
            prev = found.get(m.skill.slug)
            if prev is None or rank[importance] > rank[prev.importance]:
                found[m.skill.slug] = SkillRequirement(
                    slug=m.skill.slug,
                    name=m.skill.name,
                    importance=importance,
                    evidence=_evidence(line, m.start, m.end),
                )
        if ctx != "PREFERRED" and _EXPERIENCE.search(line):
            for ym in _YEARS.finditer(line):
                n = int(ym.group(1))
                if 1 <= n <= 20 and (min_years is None or n > min_years):
                    min_years, years_ev = n, _evidence(line, ym.start(), ym.end())
        if _DEGREE_LINE.search(line):
            level: Degree = "NONE_STATED"
            for lv, pat in _DEGREE_LEVELS:
                if pat.search(line):
                    level = lv
                    break  # lowest stated level wins ("BS/MS" → bachelor's)
            if level != "NONE_STATED" and (degree == "NONE_STATED" or degree_required is False):
                required = ctx != "PREFERRED" and not _DEGREE_OPTIONAL.search(line)
                if degree == "NONE_STATED" or required:
                    degree, degree_required, degree_ev = level, required, _evidence(line)

    seniority: Seniority = "UNKNOWN"
    seniority_ev: str | None = None
    for level_name, pat in _SENIORITY:
        if pat.search(title):
            seniority, seniority_ev = level_name, title.strip()
            break

    sponsorship, sponsorship_ev, warnings = _sponsorship(text)
    auth, auth_ev = _work_authorization(text)

    return JobAnalysis(
        method="DETERMINISTIC",
        analyzer_version=f"{DETERMINISTIC_VERSION}+{cat.version}",
        skills=sorted(found.values(), key=lambda s: (-rank[s.importance], s.name.lower())),
        min_years=min_years,
        years_evidence=years_ev,
        seniority=seniority,
        seniority_evidence=seniority_ev,
        sponsorship=sponsorship,
        sponsorship_evidence=sponsorship_ev,
        work_authorization=auth,
        work_authorization_evidence=auth_ev,
        degree=degree,
        degree_required=degree_required,
        degree_evidence=degree_ev,
        warnings=warnings,
    )


def _line_at(text: str, pos: int) -> str:
    start = text.rfind("\n", 0, pos) + 1
    end = text.find("\n", pos)
    return text[start : end if end != -1 else len(text)]


def _sponsorship(text: str) -> tuple[Sponsorship, str | None, list[str]]:
    no = next((m for p in _NO_SPONSOR if (m := p.search(text))), None)
    yes = next((m for p in _YES_SPONSOR if (m := p.search(text))), None)
    if no and yes:
        return "UNKNOWN", None, ["The posting makes conflicting statements about visa sponsorship."]
    if no:
        return "NOT_AVAILABLE", _evidence(_line_at(text, no.start())), []
    if yes:
        return "AVAILABLE", _evidence(_line_at(text, yes.start())), []
    return "UNKNOWN", None, []


def _work_authorization(text: str) -> tuple[list[str], str | None]:
    codes: list[str] = []
    evidence: str | None = None
    for m in _WORK_AUTH.finditer(text):
        code = _COUNTRY_NAMES.get(m.group(1).strip().lower().rstrip("."))
        if code is None:
            code = _COUNTRY_NAMES.get(m.group(1).strip().lower())
        if code and code not in codes:
            codes.append(code)
            evidence = evidence or _evidence(_line_at(text, m.start()))
    for m in _AUTH_REQUIRED.finditer(text):
        code = {"US": "US", "U.S.": "US", "UK": "GB", "Canadian": "CA", "Australian": "AU"}[m.group(1)]
        if code not in codes:
            codes.append(code)
            evidence = evidence or _evidence(_line_at(text, m.start()))
    return codes, evidence


# ---- LLM tier ----


def wrap_posting(title: str, text: str) -> str:
    safe = re.sub(r"</?\s*job_posting\s*>", "[tag removed]", text, flags=_I)
    safe_title = re.sub(r"[<>]", "", title)[:300]
    return f"<job_posting>\nTitle: {safe_title}\n\n{safe[:MAX_LLM_CHARS]}\n</job_posting>"


def analyze_llm(title: str, text: str, provider: LLMProvider) -> tuple[JobAnalysis, Usage, str]:
    system, prompt_id = prompts.load("jd_analyze")
    user = "Extract the requirements from this job posting.\n\n" + wrap_posting(title, text)
    draft, usage = provider.structured(task="jd_analyze", system=system, user=user, schema=JobAnalysisDraft)
    return ground_llm(draft, title, text, prompt_id), usage, prompt_id


def ground_llm(draft: JobAnalysisDraft, title: str, text: str, prompt_id: str) -> JobAnalysis:
    """Keep only claims whose quoted evidence occurs in the posting, and map
    skills onto the catalogue."""
    g = Grounder(title + "\n" + text)
    cat = catalogue()
    warnings: list[str] = []
    skills: dict[str, SkillRequirement] = {}
    dropped = 0
    rank = {"REQUIRED": 3, "MENTIONED": 2, "PREFERRED": 1}
    for s in draft.skills:
        if not (g.found(s.evidence) or g.found(s.name)):
            dropped += 1
            continue
        canon = cat.normalize(s.name)
        key = canon.slug if canon else "x:" + s.name.strip().lower()
        item = SkillRequirement(
            slug=canon.slug if canon else None,
            name=canon.name if canon else s.name.strip()[:100],
            importance=s.importance,
            evidence=s.evidence.strip()[:_EVIDENCE_MAX] if g.found(s.evidence) else s.name.strip(),
        )
        prev = skills.get(key)
        if prev is None or rank[item.importance] > rank[prev.importance]:
            skills[key] = item
    if dropped:
        warnings.append(
            f"{dropped} extracted skill(s) were discarded because they were not found in the posting."
        )

    def keep(value: Any, evidence: str | None, empty: Any) -> tuple[Any, str | None]:
        if value in (None, "UNKNOWN", "NONE_STATED", []) or not g.found(evidence):
            return empty, None
        return value, (evidence or "").strip()[:_EVIDENCE_MAX]

    min_years, years_ev = keep(draft.min_years, draft.years_evidence, None)
    if min_years is not None and not 0 < min_years <= 40:
        min_years, years_ev = None, None
    seniority, seniority_ev = keep(draft.seniority, draft.seniority_evidence, "UNKNOWN")
    sponsorship, sponsorship_ev = keep(draft.sponsorship, draft.sponsorship_evidence, "UNKNOWN")
    auth_codes = [c.upper() for c in draft.work_authorization if re.fullmatch(r"[A-Za-z]{2}", c)]
    auth, auth_ev = keep(auth_codes, draft.work_authorization_evidence, [])
    degree, degree_ev = keep(draft.degree, draft.degree_evidence, "NONE_STATED")
    return JobAnalysis(
        method="LLM",
        analyzer_version=f"{prompt_id}+{cat.version}",
        skills=sorted(skills.values(), key=lambda s: (-rank[s.importance], s.name.lower())),
        min_years=min_years,
        years_evidence=years_ev,
        seniority=seniority,
        seniority_evidence=seniority_ev,
        sponsorship=sponsorship,
        sponsorship_evidence=sponsorship_ev,
        work_authorization=auth,
        work_authorization_evidence=auth_ev,
        degree=degree,
        degree_required=draft.degree_required if degree != "NONE_STATED" else None,
        degree_evidence=degree_ev,
        warnings=warnings,
    )


# ---- Deterministic fake (tests / offline dev only) ----


@register("jd_analyze")
def _fake_jd_analyze(_system: str, user: str) -> dict[str, Any]:
    m = re.search(r"<job_posting>\nTitle: (.*?)\n\n(.*)\n</job_posting>", user, re.DOTALL)
    title, text = (m.group(1), m.group(2)) if m else ("", user)
    a = analyze_deterministic(title, text)
    return {
        "skills": [{"name": s.name, "importance": s.importance, "evidence": s.evidence} for s in a.skills],
        "min_years": a.min_years,
        "years_evidence": a.years_evidence,
        "seniority": a.seniority,
        "seniority_evidence": a.seniority_evidence,
        "sponsorship": a.sponsorship,
        "sponsorship_evidence": a.sponsorship_evidence,
        "work_authorization": a.work_authorization,
        "work_authorization_evidence": a.work_authorization_evidence,
        "degree": a.degree,
        "degree_required": a.degree_required,
        "degree_evidence": a.degree_evidence,
    }
