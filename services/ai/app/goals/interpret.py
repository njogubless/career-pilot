"""Objective → workflow template + parameters (Phase 8).

The model only chooses one of two fixed templates and fills parameters; the
Go platform validates them (Params.Normalize) and shows the plan to the user
before anything runs. Nothing here executes actions.
"""

from __future__ import annotations

import re
from typing import Any, Literal

from pydantic import BaseModel, Field

from app import prompts
from app.providers.base import LLMProvider, Usage
from app.providers.fake import register

Template = Literal["DISCOVER_AND_RANK", "DISCOVER_PREPARE_APPROVE"]


class MoneyDraft(BaseModel):
    amount: float
    currency: str = Field(description="ISO 4217 code.")


class GoalParams(BaseModel):
    query: str = Field(description="Role or keywords; empty if none.")
    remote_only: bool
    country: str | None = Field(description="ISO 3166-1 alpha-2, or null.")
    min_monthly_salary: MoneyDraft | None
    require_disclosed_salary: bool
    count: int | None = Field(description="1 to 10, or null.")
    use_ai: bool


class GoalInterpretation(BaseModel):
    template: Template
    params: GoalParams
    questions: list[str]
    summary: str


def wrap(objective: str) -> str:
    safe = re.sub(r"</?\s*objective\s*>", "[tag removed]", objective, flags=re.I)
    return f"<objective>\n{safe}\n</objective>"


def interpret(objective: str, provider: LLMProvider) -> tuple[GoalInterpretation, Usage, str]:
    system, prompt_id = prompts.load("goal_interpret")
    out, usage = provider.structured(
        task="goal_interpret", system=system, user=wrap(objective), schema=GoalInterpretation
    )
    p = out.params
    # Light normalisation; the platform performs the authoritative validation.
    p.query = " ".join(p.query.split())[:200]
    if p.country:
        p.country = p.country.strip().upper()
    if p.min_monthly_salary:
        p.min_monthly_salary.currency = p.min_monthly_salary.currency.strip().upper()
    return out, usage, prompt_id


# ---- Deterministic fake (tests / offline dev only): a small rule-based parser ----

_NUMBERS = {
    "one": 1,
    "two": 2,
    "three": 3,
    "four": 4,
    "five": 5,
    "six": 6,
    "seven": 7,
    "eight": 8,
    "nine": 9,
    "ten": 10,
}
_COUNTRIES = {
    "kenya": "KE",
    "kenyan": "KE",
    "nigeria": "NG",
    "nigerian": "NG",
    "germany": "DE",
    "german": "DE",
    "uk": "GB",
    "united kingdom": "GB",
    "british": "GB",
    "us": "US",
    "usa": "US",
    "united states": "US",
    "american": "US",
    "canada": "CA",
    "canadian": "CA",
    "india": "IN",
    "indian": "IN",
}
_CUR = {"$": "USD", "€": "EUR", "£": "GBP"}


@register("goal_interpret")
def _fake(_system: str, user: str) -> dict[str, Any]:
    m = re.search(r"<objective>\n(.*)\n</objective>", user, re.S)
    text = (m.group(1) if m else user).strip()
    low = text.lower()
    count = None
    cm = re.search(
        r"\b(\d{1,2}|one|two|three|four|five|six|seven|eight|nine|ten)\b(?=[^.]{0,40}\bjobs?\b)", low
    )
    if cm:
        count = int(cm.group(1)) if cm.group(1).isdigit() else _NUMBERS[cm.group(1)]
    country = None
    for name, code in _COUNTRIES.items():
        if re.search(rf"\b{re.escape(name)}\b", low):
            country = code
            break
    salary = None
    period_re = r"(month|monthly|year|yearly|annually|hour|hourly)?"
    sm = re.search(r"([$€£])\s?([\d,]+(?:\.\d+)?)\s*(k)?\s*(?:per |a |/)?\s*" + period_re, low)
    if sm:
        amount = float(sm.group(2).replace(",", "")) * (1000 if sm.group(3) else 1)
        period = sm.group(4) or "month"
        if period.startswith(("year", "annual")):
            amount /= 12
        elif period.startswith("hour"):
            amount *= 160
        salary = {"amount": round(amount, 2), "currency": _CUR[sm.group(1)]}
    qm = re.search(
        r"(?:remote\s+)?([a-z][a-z +/-]{2,40}?)\s+(?:engineering\s+)?(?:jobs?|roles?|positions?)", low
    )
    query = ""
    if qm:
        words = [
            w
            for w in qm.group(1).split()
            if w not in ("remote", "find", "me", "some", "the", "good")
            and not w.isdigit()
            and w not in _NUMBERS
        ]
        query = " ".join(words)
        if "engineering" in low and query and "engineer" not in query:
            query += " engineer"
    prepare = bool(re.search(r"\bprepare\b|\bapprov|\bcv\b|\bapplications?\b", low))
    return {
        "template": "DISCOVER_PREPARE_APPROVE" if prepare else "DISCOVER_AND_RANK",
        "params": {
            "query": query,
            "remote_only": "remote" in low,
            "country": country,
            "min_monthly_salary": salary,
            "require_disclosed_salary": bool(re.search(r"must (publish|disclose)|only .*disclosed", low)),
            "count": count,
            "use_ai": bool(re.search(r"\b(ai|llm)[- ](written|generated|drafted)\b", low)),
        },
        "questions": [] if query else ["Which role or keywords should the search use?"],
        "summary": text[:200],
    }
