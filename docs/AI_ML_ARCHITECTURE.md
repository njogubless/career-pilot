# AI / ML Architecture

Status: Phase 2 (CV parsing) and Phase 4 (matching inputs: taxonomy, job analysis, embeddings) implemented. Generation (5) and goal interpretation (8) are planned.

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
| JD requirement extraction | AI | **two tiers (ADR-019)**: deterministic catalogue + section heuristics for every job; LLM structured output on demand, grounded (quotes must occur in the posting) and normalised through the catalogue |
| Skill normalisation | AI + catalogue | versioned alias catalogue (`taxonomy/skills.json`, `tax-N`); terms outside it stay un-normalised (exact-name matching only) |
| Hard eligibility | Go | remote requirement, country eligibility, sponsorship, mandatory qualification, salary floor (only when salary known) → `ELIGIBLE`/`INELIGIBLE`/`UNKNOWN` per rule |
| Weighted score | Go | skills 35, experience/seniority 20, project/domain 15, location 15, compensation/preferences 15 (configurable, versioned) |
| Semantic similarity | AI embeddings + pgvector | profile summary vs JD embedding → contributes to project/domain dimension and candidate retrieval |
| Explanation | Go (v1) | deterministic sentences from the computed breakdown, citing evidence; an optional LLM narrative is not yet implemented (KI-025) |

Unknown inputs reduce **confidence**, not the score, and are listed explicitly.

## 4. Embeddings (decided: ADR-020)
- **Model.** Local `BAAI/bge-small-en-v1.5` (384-d) through fastembed/ONNX in the AI service. The model is baked into the image, runs offline, and ONNX telemetry is disabled.
- **Model id.** Includes the input recipe (`…@r2`), so a recipe change re-embeds rather than mixing vectors. Vectors are stored in pgvector with an HNSW cosine index.
- **Inputs.** Jobs: title, company and role-relevant lines (boilerplate skipped). Profiles: headline, target roles, summary, experience, projects and skills.
- **Tests and offline development.** A deterministic hashing embedder (`EMBEDDING_PROVIDER=hash`), which production rejects.

## 5. Document generation and factuality (implemented: ADR-021)
- **Input.** The platform's fact sheet: only `VERIFIED` facts, plus the user's notes for the document, each with a stable ref. The job posting (≤ 12,000 chars) and application questions are delimited untrusted input.
- **Prompts.** `cv_tailor@1`, `cover_letter@1` and `app_answers@1`. Structured output cites facts by short aliases, which map back to refs; unknown aliases are dropped and counted as a warning.
- **Output.** `doc-v1` blocks. Claims carry refs. Structural blocks (entries, contact, skills) carry refs only, and the platform renders their text from the profile.
- **Validator `fact-check-2`.** Deterministic (`app/documents/validate.py`) and also used for user edits. It checks unknown refs, numbers absent from the cited facts, uncatalogued-profile skills, years beyond the verified total, unsupported degrees and certifications, placeholders, and empty or unanswered blocks. It warns on uncited claims. Version 2 (Phase 7) no longer lets digits in skill names whitelist other figures: "OAuth 2.0" is allowed, but the "2" in "2M users" is checked.
- **On failure.** Generated claims with error-level issues are removed (answers become `NEEDS_INPUT`), and the removal is reported in `warnings`.
- **Missing information.** Returned as `questions` for the user. Motivation, salary, notice period, visa and legal questions are never answered by the model.
- **Fake provider.** Has deterministic handlers for all three tasks (tests and offline development only).

- **Outreach emails** use the same contract with prompt `outreach_email@1` (`app/documents/outreach.py`): short, cited paragraphs. Unsupported claims are removed, and a greeting name is used only if the user recorded it.

## 5a. Goal interpretation (Phase 8)
- **Prompt `goal_interpret@1`.** It maps an objective to one of two templates plus parameters, or returns questions. The objective is delimited; instructions inside it to send email, submit, or change settings are ignored and raised as questions.
- **Validation.** The platform re-validates everything (`Params.Normalize`, strict fields). Null means "use the default".
- **Evaluation.** The labelled set `app/evals/goals.jsonl` (12 objectives, including injection-style ones) runs through `python -m app.evals.goal_eval`, which reports per-field accuracy. The deterministic fake parser scores 59/60 checks; it chose "prepare" for an objective that also asked for auto-submission. **Real-model accuracy is unmeasured (no API key).**

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
