# AI / ML Architecture

Status: design accepted; implementation Phases 2 (CV parsing), 4 (matching inputs), 5 (generation), 8 (goal interpretation).

## 1. Service layout (`services/ai`)
```text
app/
  api/          FastAPI routers (versioned /internal/v1), auth dependency (service token)
  core/         settings (pydantic-settings), logging, errors
  providers/    LLMProvider protocol; anthropic.py, openai.py, fake.py; EmbeddingProvider protocol
  parsers/      pdf/docx text extraction, CV section heuristics
  extraction/   CV → profile proposal; JD → requirements (structured outputs)
  taxonomy/     skill catalogue + alias normalisation
  generation/   tailored CV, cover letter, answers, outreach drafts
  validation/   claim validator (generated text vs verified facts)
  planning/     goal → template + params
  prompts/      versioned prompt templates (files with semantic version ids)
  evaluation/   offline eval harness + datasets
```

## 2. Provider abstraction
- `LLMProvider.structured(prompt, schema: type[BaseModel], *, timeout, max_tokens) -> (BaseModel, Usage)`. Retries on transient errors with backoff, and validates against the Pydantic schema. On validation failure it re-asks once with the error, then fails.
- Implementations: Anthropic (default; current Claude model IDs, configurable via `AI_MODEL_*`), OpenAI (optional), and Fake (deterministic, rule-based, used in tests and offline dev only when explicitly configured).
- Every call returns `Usage {provider, model, input_tokens, output_tokens, latency_ms, prompt_version}`. Go records it in `usage_records`.
- Per-user daily token budget is enforced by **Go** before calling the AI service.

## 3. Matching (ADR-010)
| Stage | Where | Method |
|---|---|---|
| JD requirement extraction | AI | structured output: required/preferred skills, years, seniority, location constraints, eligible countries, sponsorship, salary (only if stated) |
| Skill normalisation | AI + catalogue | alias table first (deterministic), LLM fallback for unknown terms → reviewed into catalogue |
| Hard eligibility | Go | remote requirement, country eligibility, sponsorship, mandatory qualification, salary floor (only when salary known) → `ELIGIBLE`/`INELIGIBLE`/`UNKNOWN` per rule |
| Weighted score | Go | skills 35, experience/seniority 20, project/domain 15, location 15, compensation/preferences 15 (configurable, versioned) |
| Semantic similarity | AI embeddings + pgvector | profile summary vs JD embedding → contributes to project/domain dimension and candidate retrieval |
| Explanation | AI | generated **from the computed breakdown** only; cannot alter scores |

Unknown inputs reduce **confidence**, not the score, and are listed explicitly.

## 4. Embeddings
Anthropic does not provide an embeddings endpoint, so `EmbeddingProvider` is pluggable. Options, decided in Phase 4:
1. Local `fastembed` (ONNX, `bge-small-en-v1.5`, 384-d): no per-call cost and no data leaves the host, at the cost of a ~130 MB model download.
2. OpenAI `text-embedding-3-small`.
3. Voyage AI.

Vector dimension is fixed per model. Changing models means re-embedding, tracked by `model_versions`.

## 5. Document generation and factuality
- Input: only `VERIFIED` profile facts + job requirements. Unverified or inferred facts are excluded.
- Output: a structured document (sections → bullets), each bullet with `source_fact_ids`.
- The validator rejects bullets without sources and flags numeric claims not present in the source facts. Missing information becomes questions for the user, never invented answers.

## 6. ML roadmap
No custom model training until there are enough labelled outcomes: roughly several hundred applications with outcomes across several users. Then, with time-split validation:
- logistic regression or gradient boosting on match features to predict user shortlist/apply decisions (feedback signal)
- learning-to-rank (LightGBM LambdaMART)

Metrics are Precision@K, NDCG@K and calibration, broken down per profession segment. Model versions are recorded alongside the algorithm version.

## 7. Cost controls
- Cache JD extractions by `content_hash`, since jobs are global and extraction is shared across users.
- Extract only for jobs that pass cheap pre-filters (keyword/location).
- Per-user daily budgets.
- Prompt sizes kept small via truncation of very long JDs, with sections prioritised.
