"""Goal-interpretation evaluation (master prompt §21).

Runs the labelled objectives in goals.jsonl through `interpret` with a given
provider and reports per-field accuracy. Platform-side validation is a
separate, deterministic layer tested in Go.

    python -m app.evals.goal_eval            # configured provider (needs a key)
    python -m app.evals.goal_eval --fake     # deterministic rule parser (harness check)
"""

from __future__ import annotations

import json
import sys
from dataclasses import dataclass, field
from pathlib import Path

from app.goals.interpret import interpret
from app.providers.base import LLMProvider

DATASET = Path(__file__).with_name("goals.jsonl")


@dataclass
class EvalReport:
    cases: int = 0
    checks: int = 0
    passed: int = 0
    failures: list[str] = field(default_factory=list)

    @property
    def accuracy(self) -> float:
        return self.passed / self.checks if self.checks else 0.0


def check(case: dict, out) -> list[tuple[str, bool]]:
    e, p = case["expect"], out.params
    results = [("template", out.template == e["template"])]
    for key in ("remote_only", "country", "count", "require_disclosed_salary"):
        if key in e:
            results.append((key, getattr(p, key) == e[key]))
    if "salary_currency" in e:
        got = p.min_monthly_salary.currency if p.min_monthly_salary else None
        results.append(("salary_currency", got == e["salary_currency"]))
    if "salary_monthly" in e:
        got = p.min_monthly_salary.amount if p.min_monthly_salary else None
        results.append(
            (
                "salary_monthly",
                got is not None and abs(got - e["salary_monthly"]) <= e["salary_monthly"] * 0.02,
            )
        )
    if "query_contains" in e:
        results.append(("query", e["query_contains"] in p.query.lower()))
    return results


def run(provider: LLMProvider, dataset: Path = DATASET) -> EvalReport:
    rep = EvalReport()
    for line in dataset.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        case = json.loads(line)
        out, _, _ = interpret(case["objective"], provider)
        rep.cases += 1
        for name, ok in check(case, out):
            rep.checks += 1
            rep.passed += ok
            if not ok:
                rep.failures.append(f"{case['objective'][:60]}… → {name}")
    return rep


def main() -> int:  # pragma: no cover - manual tool
    from app.core.config import get_settings
    from app.providers.factory import build_provider
    from app.providers.fake import FakeProvider

    provider = FakeProvider() if "--fake" in sys.argv else build_provider(get_settings())
    rep = run(provider)
    print(f"{rep.cases} cases, {rep.passed}/{rep.checks} checks ({rep.accuracy:.0%})")
    for f in rep.failures:
        print("  FAIL", f)
    return 0


if __name__ == "__main__":  # pragma: no cover
    raise SystemExit(main())
