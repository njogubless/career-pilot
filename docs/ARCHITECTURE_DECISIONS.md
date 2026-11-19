# Architecture Decision Records

Format: Context → Options → Decision → Consequences. Status is one of `PROPOSED`, `ACCEPTED`, `SUPERSEDED`.

---

## ADR-001 — Modular monolith (Go) + stateless AI service (Python) + SPA (React)
**Status:** ACCEPTED · 2026-11-14

**Context.** The master prompt (§22) mandates a modular monolith core, a separate Python AI service, and no unnecessary microservices.

**Decision.**
- `services/api`: one Go binary with two run modes, `serve` (HTTP API) and `worker` (background jobs and workflows). Both share one codebase and one database.
- `services/ai`: a FastAPI service that is **stateless with respect to business data**. It receives the inputs it needs, returns structured results, and never writes authoritative records or performs external side effects (email, submission).
- `apps/web`: React SPA.

**Consequences.** Business rules live in one place, and transactions span modules freely. The AI service can scale and be redeployed independently, and holds no user secrets beyond the LLM provider keys.

---

## ADR-002 — Durable workflows: Postgres-persisted workflow state + River job queue (Temporal deferred)
**Status:** ACCEPTED · 2026-11-14

**Context.** We need workflows that survive restarts, durable timers (follow-ups in N days, approvals that wait for days), retries with backoff, cancellation, and auditable history (§12, §28). The spec also requires explicit `workflow_executions` / `workflow_tasks` records regardless of engine.

**Options.**

| | Temporal | River (Postgres-backed Go queue) + our workflow tables | Redis queue (asynq) |
|---|---|---|---|
| Durability | Excellent | Excellent (Postgres) | Redis is not authoritative (spec forbids it as sole record) |
| Durable timers | Native | `scheduled_at` jobs + periodic jobs | Limited |
| Transactional enqueue with business writes | No (dual write → needs outbox) | **Yes**: same Postgres transaction | No |
| Ops cost | Temporal server + its DB + UI + version pinning | None beyond Postgres | Redis |
| Visibility | Temporal UI | Our own tables (already required) + River UI optional | asynqmon |
| Learning curve / determinism constraints | High (deterministic workflow code) | Low | Low |

**Decision.** Use **River** for job execution and model workflows explicitly as **persisted state machines** in `workflow_executions` / `workflow_tasks`. The orchestrator advances a workflow by completing a task and enqueuing its dependents in the **same transaction**. Approval waits are workflow states, not sleeping processes. Timers are scheduled jobs.

**Consequences.**
- No extra infrastructure, and exactly-once enqueue relative to DB state.
- We own the step-advancement logic, which is kept small and well-tested.
- **Revisit triggers:** more than ~20 workflow types with complex branching, cross-service sagas, or throughput beyond what Postgres can handle. The `workflows` package hides River behind an interface to keep migration feasible.

---

## ADR-003 — Authentication: server-side sessions in HttpOnly cookies, Argon2id passwords
**Status:** ACCEPTED · 2026-11-14

**Context.** A first-party SPA on the same site as the API.

**Options.** JWT access/refresh tokens in JS-accessible storage (XSS exfiltration risk, hard revocation) vs. opaque server-side sessions.

**Decision.**
- Session tokens are 256-bit random values, stored only as SHA-256 hashes in `user_sessions`.
- Sent as an `HttpOnly; Secure; SameSite=Lax` cookie, with idle and absolute expiry.
- CSRF protection uses a per-session token, which state-changing requests must send in an `X-CSRF-Token` header.
- Passwords are hashed with Argon2id (memory-hard; parameters in config).
- Password reset uses single-use hashed tokens with a 30-minute expiry. Responses are identical whether or not the account exists.
- Rate limiting is applied per IP and per account.

**Consequences.** Instant revocation (logout everywhere, kill switch). OAuth login (Google) can be added later as another credential type.

---

## ADR-004 — Data access: pgx v5 with hand-written SQL in per-module repositories; goose migrations
**Status:** ACCEPTED · 2026-11-14

**Decision.**
- `jackc/pgx/v5` with explicit SQL. No ORM.
- Every query on user-owned data takes `userID` as a required parameter.
- Migrations are versioned SQL files run by `pressly/goose`, embedded into the binary.

**Consequences.** Full control over queries and locking (`FOR UPDATE` in state transitions). The cost is more boilerplate than sqlc; revisit sqlc if repository code grows large.

**Defence in depth:** Postgres Row-Level Security was evaluated in Phase 10 and deferred; least-privilege database roles were adopted instead (ADR-026, KI-007).

---

## ADR-005 — HTTP: chi router, `log/slog` structured logging, OpenTelemetry
**Status:** ACCEPTED · 2026-11-14

`go-chi/chi/v5` is idiomatic and plays well with `net/http` middleware. Logging uses the stdlib `slog` with JSON output, with request IDs propagated. OpenTelemetry tracing comes in Phase 10, and the hooks (request IDs, context propagation) exist from Phase 1.

---

## ADR-006 — Go ↔ Python contract: internal HTTP/JSON, service token, versioned schemas
**Status:** ACCEPTED · 2026-11-14

**Decision.**
- The Go API calls the AI service over HTTP on a private network.
- Calls authenticate with a shared service token (`AI_SERVICE_TOKEN`, constant-time compare). This upgrades to mTLS in production if the network isn't private.
- Request and response schemas are Pydantic models, exported as OpenAPI (`services/ai/openapi.json`) and mirrored by Go structs.
- Every response carries `model`, `prompt_version` and `usage` for audit and cost tracking.

**Consequences.** Contracts are explicit and testable. The AI service has no direct DB access, which keeps the security boundary simple.

---

## ADR-007 — Application submission channels (v1)
**Status:** ACCEPTED · 2026-11-14 (confirmed by user)

**Context.** See STAGE0_INVESTIGATION §4.2: candidate-side ATS submission APIs are generally unavailable, and hosted forms use bot protection.

**Decision.** v1 supports two channels:
1. **Assisted submission:** the platform prepares the package, the user submits on the official site, then confirms. Evidence is `USER_ATTESTED`, optionally with an attachment, and is later upgradeable to `EMAIL_CONFIRMATION` via Gmail detection.
2. **Email application:** used only when the posting provides an application email. The approved package is sent from the user's Gmail. Evidence is the Gmail message ID.

Browser automation (Playwright) is **deferred**. A `Submitter` interface keeps the door open for real integrations.

**Consequences.**
- UI copy replaces "One-click dispatch" with the actual channel.
- An application only becomes `SUBMITTED` with an evidence record.
- Mode C autonomy covers discovery, matching, preparation, outreach and follow-up within limits. It cannot click "submit" on third-party sites.

---

## ADR-008 — Object storage: S3-compatible, private, access proxied through the API
**Status:** ACCEPTED · 2026-11-14

An S3-compatible store in development (product choice deferred to Phase 2, see KI-011) and any S3-compatible store in production. Buckets are private, and object keys are random UUIDs (no PII in keys). Downloads go through an authorised API endpoint that streams the object, so every access is checked and auditable; short-lived presigned URLs are a later optimisation. Server-side encryption is enabled in production.

---

## ADR-009 — LLM provider abstraction; Anthropic default; deterministic fake for tests
**Status:** ACCEPTED · 2026-11-14

**Decision.**
- The AI service defines an `LLMProvider` protocol with structured-output calls.
- Anthropic (Claude) is the default implementation. OpenAI is optional when configured. A deterministic `FakeProvider` is used **only** in tests and local dev when `AI_PROVIDER=fake` is explicitly set.
- If no provider is configured in production, AI endpoints return `503 AI_PROVIDER_UNAVAILABLE`. Mocks are never silently substituted.

---

## ADR-010 — Matching: deterministic eligibility + weighted scoring computed in Go; AI for extraction/explanation
**Status:** ACCEPTED · 2026-11-14

**Context.** Scores must be reproducible and versioned (§7, Phase 4 acceptance criteria).

**Decision.**
- Hard eligibility filters and the weighted score run as deterministic code in the Go `matching` package, over structured job and profile data.
- The Python service provides inputs to that code: job requirement extraction, skill normalisation, embeddings (semantic similarity as one sub-signal), and natural-language explanations grounded in the computed breakdown.
- Each result stores `algorithm_version`, the weights snapshot, and the input hashes.

**Consequences.** Scores can be recomputed exactly, and an LLM can never change a score.

---

## ADR-011 — Frontend stack
**Status:** ACCEPTED · 2026-11-14

React + TypeScript + Vite, with the following:
- Tailwind CSS, with design tokens taken from Stitch `DESIGN.md`.
- shadcn/ui-style primitives (Radix) restyled to the design system.
- TanStack Query for server state and React Router.
- React Hook Form + Zod for forms, Recharts for charts.
- Vitest + Testing Library for unit tests, Playwright for E2E.
- Fonts (Newsreader, Manrope, JetBrains Mono) and Material Symbols are **self-hosted via npm packages**. This removes third-party font requests (privacy, CSP).

---

## ADR-012 — Design system extension for undesigned screens
**Status:** ACCEPTED · 2026-11-14 (confirmed by user)

**Decision.** Screens without a Stitch design are built from DESIGN.md tokens and the components extracted from the four Stitch screens: app shell, dossier card, metric tile, mono status tag, filter chip, hairline table, pipeline terminal. No new visual language is introduced, and Stitch designs supplied later replace these screens.

Where the Stitch Tailwind config and the DESIGN.md prose disagree, the prose wins. The main case is corner radius: 4 px is the default for containers and buttons, 2 px for chips.

---

## ADR-014 — River adopted in Phase 2 (brought forward from Phase 3)
**Status:** ACCEPTED · 2026-11-14

**Context.** CV extraction calls an LLM and can take 30–60 s or more. ADR-002 forbids running such work inside an HTTP request or as a fire-and-forget goroutine.

**Decision.**
- River is introduced now. `careerpilot worker` runs a River client.
- The API holds an insert-only client and enqueues `extract_cv` jobs in the same transaction as the `profile_extractions` row.
- Job payloads carry IDs only, never personal data.
- Permanent failures cancel the job and mark the record `FAILED`. Examples: unreadable document, provider not configured, refusal.
- Transient failures retry with River's backoff, up to 4 attempts.

**Consequences.** Extraction is durable across restarts. The same queue will host discovery and workflow jobs in later phases.

---

## ADR-015 — CSRF tokens derived by HMAC; JSON content-type required
**Status:** ACCEPTED · 2026-11-14 (refines ADR-003)

**Decision.**
- The CSRF token is `HMAC-SHA256(SESSION_SECRET, "csrf:" || session_id)`. It is stable per session and can be re-issued by `GET /auth/session` without storing it, so no `csrf_token_hash` column is needed.
- All JSON endpoints require `Content-Type: application/json`. Cross-site HTML forms cannot send that type, which protects the unauthenticated login and registration endpoints against login CSRF.
- Password-reset links carry the token in the URL **fragment** (`#token=`), which browsers never send to servers, proxies or `Referer` headers.

---

## ADR-016 — Development object storage: SeaweedFS
**Status:** ACCEPTED · 2026-11-14 (resolves KI-011)

`chrislusf/seaweedfs:4.51` (released 2026-10-26) provides the S3 API in Compose. Credentials are generated into `.env` by `make env`. The Go `documents.Storage` interface uses the generic S3 client (minio-go), so any S3-compatible store works in production.

---

## ADR-017 — LLM defaults and grounding
**Status:** ACCEPTED · 2026-11-14

**Decision.**
- The default model is `claude-opus-5-5`, with `output_config.effort` set explicitly (default `medium`). Both are configurable with `AI_MODEL` and `AI_EFFORT`.
- Calls use SDK structured outputs (`messages.parse` with Pydantic schemas) and the server-side refusal fallback (`fallbacks: "default"`, beta `server-side-fallback-2026-07-01`). A final `refusal` stop reason is mapped to the permanent `AI_REFUSED` error.
- Every extracted item must quote verbatim evidence. The service checks each quote deterministically against the document text (`evidence_found`). Items without evidence are flagged, and they start unticked in the review UI.
- Selecting OpenAI is rejected until it is implemented.

---

## ADR-018 — Job discovery: snapshots, conservative normalisation, cross-source dedupe
**Status:** ACCEPTED · 2026-11-14

**Decision.**
- **Snapshot semantics.** Each adapter returns a board's full current listing. A *successful* snapshot is authoritative, so postings missing from it are expired. A failed fetch never expires anything. An empty snapshot for a board that had 10 or more active jobs is treated as a failure (`ErrSuspicious`).
- **Change detection.** Content is hashed (sha256 over visible fields plus `NormalizerVersion`). Unchanged postings only bump `last_seen_at`. Bumping the normaliser version reprocesses postings on their next sync.
- **Reposts.** A new external ID whose dedupe key matches an expired job sets `repost_of`. The same ID reappearing clears `expired_at`.
- **Duplicates.** `dedupe_key = norm(company)|norm(title)|norm(first location)`. Only **cross-source** matches are collapsed (an employer's ATS beats an aggregator). Same-board look-alikes stay, because companies post identical-looking requisitions.
- **Normalisation is conservative.** Remote status, remote scope (WORLDWIDE / COUNTRIES / REGION / UNKNOWN), countries, employment type and salary are filled only from explicit, structured or literal data:
  - Ambiguous two-letter codes that collide with US states (CA, DE, IN, IL, …) are ignored.
  - A named country beats a "worldwide/anywhere" keyword.
  - Salary is never converted between currencies, only annualised.
- **Scheduling and politeness.**
  - River runs a periodic `discover_all` every `DISCOVERY_INTERVAL` (default 6 h), fanning out `discover_source` jobs.
  - Uniqueness windows (5 and 10 min) collapse overlapping triggers.
  - Each source has a minimum polling interval. A circuit breaker backs off after 5 consecutive failures (1 h doubling, up to 24 h).
  - The fetcher allows only four hosts (also on redirects), limits each host to 1 request/s, retries 429/5xx with backoff while honouring `Retry-After`, and caps responses at 64 MB.
- **Sanitisation.** Third-party HTML is sanitised server-side (bluemonday UGC policy, nofollow links). The SPA renders only that sanitised HTML, under a CSP with no inline scripts.

**Consequences.** Job data is traceable (`source_id`, `external_id`, `discovery_runs`). Source failures are isolated, and quality is measurable (see IMPLEMENTATION_TRACKER). Eligibility in Phase 4 builds on these honest fields plus LLM extraction.

---

## ADR-019 — Matching pipeline: two-tier job analysis, retrieval, versioned deterministic scoring
**Status:** ACCEPTED · 2026-11-14 (implements ADR-010)

**Decision.**
- **Job analysis has two tiers.** Both are stored in `job_analyses`, keyed by job, method and the job's content hash. A content change or analyser-version change triggers reanalysis.
  - *Deterministic* (`jd-det-1+tax-N`, every active job). Matching uses the skill catalogue (`services/ai/app/taxonomy/skills.json`, about 150 skills with aliases), section detection (requirements / nice-to-have / role / ignored company and legal boilerplate) and documented heuristics for years, seniority (title only), sponsorship, work authorisation and degree. Every populated field carries verbatim evidence.
  - *LLM* ("deep analysis", on demand). A user requests it for one posting. It is rate-limited (20 per user per day), audited and shared across users. A quote that is not found in the posting is discarded, and skills are normalised through the same catalogue. Where current, it is preferred over the deterministic tier.
- **Candidate retrieval** unions three sets: the 400 semantic nearest neighbours (pgvector HNSW, cosine, `ef_search` raised per query); 400 jobs ranked by skill-slug overlap; and jobs the user gave feedback on. Only active, canonical jobs qualify.
- **Scoring** is a pure Go function, `matching.Evaluate(profile, job, weights)`, versioned `match-v1`. Its constants are in code.
  - Hard checks: remote requirement, location eligibility (including region membership with "maybe" members), work authorisation, sponsorship, salary floor (same currency only) and required degree. Degree checks never fail in v1.
  - Missing information yields UNKNOWN, never FAIL. Overall eligibility is INELIGIBLE on any FAIL, and UNKNOWN when a critical check is unknown.
  - Weighted dimensions (35/20/15/15/15) are averaged over the **known** dimensions only. Unknowns lower confidence (HIGH needs ≥85% of the weight to be known *and* an LLM analysis), not the score.
  - Skill credit depends on provenance: verified profile skill 1.0, unverified 0.6, mentioned in a verified role 0.5, mentioned in an unverified role 0.3.
- **Explanations** are deterministic sentences built from the computed breakdown, citing the evidence source of each skill. No LLM can change a score.
- **Persistence.** `job_match_results` stores, per (user, job, version), the full result, the weights, and the `profile_hash` and `job_hash` of the inputs. A rerun with unchanged inputs produces byte-identical results (integration-tested).
- **Recompute triggers:**
  - any successful profile or preferences write (middleware; debounced 20 s);
  - completed job analysis (`match_all` fan-out);
  - user-requested deep analysis;
  - a manual recompute (rate-limited);
  - a 12-hourly safety run.
  A profile changed during a run causes a rerun (snooze).
- **Feedback** (`match_feedback`) records the score and version it reacted to. It can dismiss a job from the default list. It does not alter scores; it is training and evaluation data for the ML roadmap.

**Consequences.** Scores are reproducible and auditable, and they work without an LLM key. Deterministic analysis cannot reliably separate required from optional skills, so its confidence is capped at MEDIUM. Deep analysis needs an Anthropic key.

---

## ADR-020 — Embeddings: local bge-small (fastembed/ONNX) in the AI service
**Status:** ACCEPTED · 2026-11-14 (resolves the open choice in AI_ML_ARCHITECTURE §4)

**Decision.** Use `BAAI/bge-small-en-v1.5` (384-d, MIT licence) via fastembed/ONNX Runtime, in-process in the AI service.
- The model is baked into the image at build time. The service runs with `EMBEDDING_OFFLINE=true`, so it never downloads at runtime, and ONNX Runtime telemetry is disabled.
- The reported model id includes the input recipe (`BAAI/bge-small-en-v1.5@r2`). Changing what text is embedded therefore re-embeds everything rather than mixing incomparable vectors.
- Jobs are embedded from the title, the company and role-relevant lines (boilerplate skipped, about 1,500 characters).
- Profiles are embedded from the headline, target roles, summary, experiences, projects and skills (about 2,000 characters).
- `EMBEDDING_PROVIDER=hash` is a deterministic feature-hashing embedder for tests and offline development. Production rejects it.

**Why.** No per-call cost and no data leaves the host. Anthropic has no embeddings endpoint, and a third-party embeddings API would add a new data processor.

**Consequences.**
- About 480 MB image, about 750 MB RAM while embedding.
- CPU-bound throughput of roughly 1–2 postings per second on 4 cores. The initial backfill of about 4,400 jobs takes 30–60 minutes; afterwards only new and changed jobs are embedded.
- A GPU or a hosted provider can replace it behind the same `Embedder` protocol.

---

## ADR-021 — Application preparation: grounded generation, immutable versions, explicit approval
**Status:** ACCEPTED · 2026-11-17

**Decision.**
- **Scope.** A tailored document belongs to (user, job, kind), with kind `CV`, `COVER_LETTER` or `APPLICATION_ANSWERS`. This is a deliberate change from the earlier plan (`/applications/{id}/prepare`): applications only exist from Phase 6. Phase 6 links an application to a document's *approved version*.
- **Fact sheet.** The Go platform builds the fact sheet from **verified** profile facts only, plus the user's notes for that document. Each fact has a stable reference (`exp:<uuid>`, `exp:<uuid>#a2`, `skill:<uuid>`, `note:1`, …). Unverified and rejected facts never reach the generator.
- **Content model (`doc-v1`).** Sections of typed blocks. Claim blocks (`paragraph`, `bullet`, `answer`) carry the refs that support them. Structural blocks (`contact`, `entry`, `skills`) carry refs only, and the platform renders their text (names, titles, employers, dates, degrees) from the profile on every save. Neither the model nor a user edit can change an employer or a date.
- **Generation (LLM).** Versioned prompts `cv_tailor@1`, `cover_letter@1` and `app_answers@1`. Facts appear under short aliases (`E1.A2`), so the model never sees database ids. The job posting and application questions are delimited untrusted input. Generation runs asynchronously as a River job, with at most one in flight per document.
- **Generation (no LLM).** `source: PROFILE` builds the document deterministically from verified facts, copied verbatim. Letters and answers start empty for the user to write. This keeps preparation usable without an AI provider.
- **Factual validation.** A deterministic validator (`fact-check-1`, in the AI service, no LLM) checks every claim against the cited facts:
  - Figures must appear in the cited facts (date fields support their year only).
  - Skills must be in the catalogue and in the profile. This is an error in a CV and a warning in letters and answers.
  - Stated years of experience must not exceed the verified total.
  - Degrees and certifications must be in the profile.
  - Refs must resolve; placeholders and empty or unanswered blocks are flagged.
- **What happens to failing claims.** Generated claims with error-level issues are **removed** before the user sees the draft; an unsupported answer becomes "needs your input". User edits are saved even when they fail, but they are flagged.
- **Versions.** Every generation, template, edit and restore creates a new version, which is immutable (a database trigger rejects updates). Each version stores the validation, a hash of the fact sheet (to report when the profile has changed since) and the provider, model, prompt and token usage. Edits use optimistic concurrency (`base_version_id`), and saving identical content is idempotent.
- **Approval.** Only the current version can be approved, and approval re-runs validation against the *current* profile.
  - Incomplete content (needs input, empty, placeholder) can never be approved.
  - Other errors require an explicit acknowledgement, which is recorded with the approval.
  - Approvals are audited and revocable. Editing after approval leaves the old version approved and marks the document `OUTDATED`.
- **Export.** DOCX is built with the Go standard library: single column, real text, no tables or images, so applicant tracking systems can parse it. TXT and Markdown are also available. PDF comes from the browser's print view, which handles every script and font correctly; server-side PDF libraries' core fonts do not cover non-Latin names. Exports are audited, and nothing is sent anywhere.

**Why.** The master prompt requires generated documents to use only verified facts, to never invent qualifications, to preserve versions and to require user approval. Making structure platform-rendered and claims citation-bound turns "do not fabricate" into checks that can be verified, rather than a prompt instruction.

**Consequences.**
- The validator is conservative and explainable. It cannot prove that free text is true, so the user's review remains essential and the UI says so.
- Validation needs the AI service. When it is unreachable, edits are still saved as `NOT_VALIDATED`, but approval is refused.
- No Stitch design exists for the preparation screens (editor, print view, job-page panel). They extend the Editorial Intelligence system under ADR-012 (KI-005).
- Achievement refs use the achievement's position within the role. Reordering achievements in the profile can therefore shift citations; validation re-resolves them on every check (KI-029).

---

## ADR-022 — Application pipeline: explicit state machine, evidence-backed submission, automation guard
**Status:** ACCEPTED · 2026-11-17

**Decision.**
- **One application per (user, job).** Creating one for a job that the cross-source dedupe has collapsed into another counts as a duplicate and is refused with a pointer to the existing application.
- **State machine.** The transition table lives in Go (`applications.CanTransition`). Edges into `APPROVED`, `SUBMITTED` and `FAILED`, and the revoke edge, are **guarded**: only dedicated actions can take them, never a plain status change.
  - `APPROVED` is reached only through explicit approval.
  - `SUBMITTED` is reached only by recording evidence.
  - `FAILED` is reached only by recording a failed attempt.
  - Every change is written to an append-only history (a trigger rejects updates) and the audit log.
- **Approval.** Approval needs `confirm: true` and an available channel. It **pins** the *approved* version of each tailored document (Phase 5); a CV is required, and unapproved documents are left out with a warning. Pinned versions cannot be deleted (`ON DELETE RESTRICT`, surfaced as 409). Before every submission start, the pins are re-checked against the documents' current approvals.
- **Submission channels.** These implement a `Submitter` interface:
  - `ASSISTED` is available. You submit on the employer's site with the downloaded package; CareerPilot never fills or submits third-party forms.
  - `EMAIL` reports itself unavailable until Gmail (Phase 7).
  - Candidate-side ATS APIs remain `BLOCKED_BY_PROVIDER`, because they need employer keys. Browser automation remains deferred (ADR-007).
- **Attempts and idempotency.** A submission attempt requires an `Idempotency-Key`, which is unique per user; replaying the key returns the same attempt.
  - Partial unique indexes allow one `PENDING` attempt and at most one `SUCCEEDED` attempt per application.
  - Failures are recorded with a reason (`VERIFICATION_REQUIRED`, `SITE_ERROR`, `POSTING_CLOSED`, `INELIGIBLE`, `OTHER`) and move the application to `FAILED`. Retrying opens a new attempt.
- **Evidence.** Users may only record `USER_ATTESTED` evidence. This requires an explicit attestation, can include a reference and an uploaded attachment, and its time cannot be in the future. `EMAIL_*` and `ATS_CONFIRMATION` evidence is reserved for integrations. Evidence is append-only.
- **Automation guard (`automation.Guard.Authorize`).** It runs inside the action's transaction, immediately before the action, and refusals are audited as `DENIED`.
  - The user's settings are the mode (`DISCOVERY_ONLY`, `HUMAN_APPROVED` (default) or `CONTROLLED`), a global `paused` kill switch and `daily_submission_limit`.
  - Agents and the system are blocked while paused and in discovery-only mode, and may only submit in `CONTROLLED` mode.
  - Assisted submissions always require the user, regardless of mode.
  - The user acting themselves is never blocked by pause or mode, but the daily limit applies to everyone.
  - Pausing cancels automated attempts in progress. Revoking an approval or withdrawing cancels any pending attempt.

**Why.** The acceptance criteria require valid transitions, no duplicate submissions, recorded failures, no success without evidence, and the ability to stop or revoke automation. Each of these is enforced by the database or the state machine, not only by the UI.

**Consequences.**
- "Submitted" is only as reliable as the evidence: `USER_ATTESTED` is the user's word. Email or ATS confirmation detection (Phase 7) can strengthen it later.
- Phase 8 agents must go through `Guard.Authorize` and the same service actions, so they inherit all of these guarantees.
- No Stitch design exists for the application detail page or the automation settings; both extend the system (ADR-012). The pipeline follows the Stitch Kanban design, but its copy and metrics show real data only: no "ATS sync", "autonomous dispatch" or invented response velocity.

---

## ADR-023 — Recruiter outreach: user-owned Gmail, content-bound approval, metadata-only reply detection
**Status:** ACCEPTED · 2026-11-17

**Decision.**
- **Gmail.** Users connect their own Gmail through OAuth 2.0 (authorization code + PKCE S256, `access_type=offline`, a single-use state that expires after 10 minutes and is bound to the signed-in user).
  - Scopes are **`gmail.send`** and **`gmail.metadata`** only: headers, no message bodies. Both must be granted.
  - Tokens are sealed with AES-256-GCM, bound to the user id as associated data and versioned for key rotation.
  - Refresh-token rotation is handled. A rejected grant marks the account `NEEDS_RECONNECT`.
  - Disconnecting revokes the grant at Google and deletes the tokens.
  - Without `GOOGLE_OAUTH_CLIENT_ID`/`SECRET` and `DATA_ENCRYPTION_KEY`, Gmail is reported as not configured.
- **Contacts.** Addresses come from the user, or from a job posting that publishes them, with the surrounding text kept as evidence. They are never guessed, bought or scraped.
  - Posting discovery skips addresses whose role or introducing sentence shows another purpose (accommodation, privacy, fraud reports and similar).
  - On 762 live postings, 761 published addresses were of this kind; most real contacts will therefore be user-provided.
  - Verification is syntax plus DNS (MX, an implicit MX via A/AAAA, RFC 7505 null MX): `DOMAIN_OK`, `INVALID` or `UNVERIFIED` on DNS errors. A bounce sets `BOUNCED`. Mailboxes are never probed over SMTP.
  - A `do_not_contact` flag blocks sending and cancels follow-ups.
- **Drafts.**
  - `AI` mode uses prompt `outreach_email@1` with the same fact-sheet grounding as Phase 5. Unsupported claims are removed, and a name the user did not record is never used.
  - `TEMPLATE` mode is deterministic and uses only the user's own headline and notes.
  - Edited bodies are re-validated against the whole profile (`fact-check-2`).
- **Approval** requires explicit consent (plus acknowledgement of fact-check errors) and stores a SHA-256 of recipient, subject, body and attachment. Any edit withdraws it, and sending verifies the hash.
- **Sending.** All checks run immediately before sending, with an `Idempotency-Key`:
  - Gmail connected (checked before any state change).
  - Approval hash matches.
  - Contact not do-not-contact, invalid or bounced.
  - Automation guard (`outreach.send`: agents only in CONTROLLED mode, never while paused).
  - Daily limit (default 10), recipient cooldown (default 3 days), and no second introduction without a reply.
  The row is marked `SENDING` before the Gmail call:
  - A 4xx from Gmail sets `FAILED`.
  - A 5xx or network error sets `UNKNOWN`, which is never retried automatically, so there is never a silent duplicate.
  - A crash mid-send is swept to `UNKNOWN`.
  - Follow-ups are sent in the same thread (`threadId`, `In-Reply-To`, `References`).
  - Attachments must be a currently approved document version.
- **Replies.** Thread metadata is polled every 15 minutes, or on demand. A message in a thread from someone else, received after our send, is recorded as a reply: address and time only, with the application association. Mailer-daemon/postmaster senders are bounces. A reply or bounce cancels scheduled follow-ups and unsent follow-up drafts.
- **Follow-ups.** User preferences control them: enabled, days after, maximum 0–2. Due follow-ups become **drafts** for approval and are never sent automatically. They wait while automation is paused, and are cancelled on a reply, when turned off, or for do-not-contact or bounced contacts.

**Consequences.**
- **Not live-verified (`AWAITING_CREDENTIALS`).** Live Gmail use needs a Google Cloud OAuth client, and beyond 100 test users needs Google's verification for restricted scopes. All OAuth, send and reply paths are tested against a fake Google server.
- **Data stored.** Outbound email content is stored (the user's own drafts and records); inbound content is not.
- **Email application channel.** Still unavailable: Phase 6 `EMAIL` remains off, because postings almost never publish an application address (see above) (KI-036).
- **Design.** No Stitch design exists for outreach screens; they extend the system (ADR-012).

---

## ADR-024 — Agent orchestration: template plans, granted tools, persisted tasks, approval checkpoints
**Status:** ACCEPTED · 2026-11-17 (implements ADR-002 and AGENT_ARCHITECTURE.md)

**Decision.**
- **Plans are templates** (`plan-v1`): `DISCOVER_AND_RANK` (6 steps) and `DISCOVER_PREPARE_APPROVE` (10 steps).
  - An objective becomes `{template, params}` either from the structured form, or from AI interpretation (`goal_interpret@1`). AI interpretation is only a *proposal*: it is shown to the user and runs only when they start it.
  - Every plan passes `Params.Normalize` (bounds, unknown fields rejected) and `validatePlan` (every agent holds its tool; dependencies point to earlier steps).
  - An LLM can never add steps, tools or parameters.
- **Agents** are logical: Profile Intelligence, Discovery, Matching, Application Preparation, Compliance & Safety, Execution and Performance.
  - Each agent has a static tool allowlist in code, so agents cannot grant themselves anything.
  - Ten tools are registered, each with a contract: description, strict input type (unknown fields rejected), permission action, side-effect class, timeout, maximum attempts, idempotency note and expected output.
  - No tool has external side effects. No agent can send email or submit applications.
- **Execution.** Each task is one River job (`workflow_task`).
  - Phase 1 (transaction): claim the task; check the workflow is not cancelled or paused and that the global pause is off (it stops reads too).
  - The tool then runs outside any transaction, after the grant check, strict input decode, `automation.Guard.Authorize` (for tools with a permission) and its timeout.
  - Phase 2 (transaction): record the result and the tool call (`agent_tool_calls` + `audit_events`, actor `AGENT`); make dependents `READY` and enqueue them in the same transaction (or `SKIPPED` after a failure); finalise as `SUCCEEDED`/`PARTIALLY_SUCCEEDED`/`FAILED`.
  - The report step is `run_always`, so partial runs still produce a report.
  - Tools are idempotent (one application per job, the existing CV is reused, approvals that already exist are kept), so a re-run after a crash is safe.
- **Errors** are classed `RETRYABLE` (River backoff up to the tool's attempt limit), `NON_RETRYABLE`, `DENIED` (permission refusal; the step fails and dependents are skipped) or `NEEDS_INPUT` (the workflow asks questions and resumes on request).
- **Approval checkpoint.** `request_approval` creates an `approval_requests` row: the items, their fact-check status and compliance flags, a payload hash and a 7-day expiry.
  - The user's decision must quote the payload hash, and only items that passed the checks can be approved.
  - On approval, the Execution agent approves the CV version and the application (assisted channel) through the existing services. Submission stays with the user (ADR-007).
- **Control.** Cancel, pause, resume and retry (failed and skipped steps re-run; successful results are kept). There are at most 3 active workflows per user, and 10 starts per hour.
- **Recovery.** A maintenance job runs every minute. It:
  - expires approvals;
  - resumes workflows held by the global pause once it is lifted;
  - re-enqueues `READY` tasks whose job was lost;
  - fails `RUNNING` tasks whose job is gone (`INTERRUPTED`, retryable by the user).
- **Eligibility** is decided once, by the Matching agent against the user's verified work-eligible countries. The search does not hard-filter by country: on live data a country filter removed all 144 remote backend jobs, including 17 whose eligibility was merely unknown.

**Why.** The Phase 8 acceptance criteria are: objective → structured plan → delegated agents, state that survives restarts, blocked unauthorised actions, recoverable partial failures, auditable actions, and pause/revoke. Templates plus deterministic validation make "the platform validates the plan" literal, and River plus Postgres give durability without new infrastructure (ADR-002).

**Consequences.**
- Two templates only. An outreach template is deferred because postings rarely publish recruiter addresses (KI-035); a scheduled weekly report is Phase 9.
- AI goal interpretation is evaluated on a 12-case labelled set. Only the deterministic fake parser has been measured (98%); real-model accuracy is unmeasured without an API key (KI-015, KI-040).
- No Stitch design exists for the Intelligence or Automation Centres; both extend the system (ADR-012).

---

## ADR-025 — Automation and analytics: schedules on River, notifications and analytics derived from records
**Status:** ACCEPTED · 2026-11-18 (extends ADR-002 and ADR-024)

**Decision.**
- **Schedules** (`workflow_schedules`) store a validated Phase 8 plan (`template` + `params`, run through `Propose`) with a cadence: `DAILY` or `WEEKLY` (weekday), an hour and an IANA time zone. `NextRun` is computed in the user's zone, so 08:00 stays 08:00 across DST.
  - A River periodic job (`workflow_scheduler`, every minute) claims due rows and starts a workflow with a **slot**: `workflow_executions(schedule_id, scheduled_for)` is unique, so a double fire, a retried job or two workers can start at most one run per slot.
  - A due schedule is **skipped**, not queued, when automation is paused (`SKIPPED_PAUSED`), when its previous run is still open, e.g. waiting for approval (`SKIPPED_OVERLAP`), or when the active-workflow limit is reached (`SKIPPED_LIMIT`). Missed slots are not back-filled: `next_run_at` is computed from `max(now, slot)`.
  - Scheduled runs are audited with actor `SYSTEM` and keep every approval checkpoint. At most 5 schedules per user.
- **Temporal stays deferred** (re-evaluated). Schedules need durable once-per-slot starts, retries and visibility; Postgres uniqueness plus River periodic jobs provide that without new infrastructure. Revisit if workflows need long-running timers per user or cross-service sagas.
- **Notifications are derived, not emitted.** A sweep (every 2 minutes, 48-hour window) reads persisted records: pending approvals, workflows needing input or finished, outreach replies and bounces, follow-up drafts, new strong matches and scheduled reports. Each becomes a notification with a deterministic `dedupe_key`, and `UNIQUE(user_id, dedupe_key)` with `ON CONFLICT DO NOTHING` makes re-runs harmless. Strong matches are capped at 5 per user per day; the overflow updates one daily digest. Preferences are per category. In-app only: no email or push delivery.
- **Analytics are computed on read** from status history, applications, matches, outreach and sources. Funnel stages use the furthest stage each application reached (by rank over `application_status_history`); reaching `SUBMITTED` or later requires `submitted_at` (evidence). Every rate carries its numerator and denominator and is flagged `low_sample` below 5. No vanity metrics (counts of automated actions) are shown.
- **Career reports** are deterministic summaries (counts, highlights and rule-based recommendations) stored in `career_reports`. A weekly job builds last week's report (Monday–Sunday, UTC) for users who opted in and had activity in the last 28 days; a unique index on scheduled reports prevents duplicates. Users can also generate a report for the last 7 days (10 per hour).
- **Dead letters** are River's discarded jobs (after the attempt limit); the runbook shows how to inspect them.

**Why.** The Phase 9 acceptance criteria are: scheduled workflows run reliably, duplicate notifications are controlled, follow-ups are cancelled when appropriate (Phase 7: a reply cancels pending follow-ups), and analytics come from persisted records. Database constraints give exactly-once semantics that survive crashes and retries, and deriving notifications from records means a notification can never claim something the database does not show.

**Consequences.**
- Notification latency is up to 2 minutes; schedules can start up to a minute late.
- Weeks are UTC weeks; a user far from UTC sees week boundaries shifted (KI-044).
- Report recommendations are rules, not LLM output, so they are limited but never invented (KI-045).
- No Stitch design exists for Career Analytics, notifications or schedules; they extend the system (ADR-012). Charts use one validated hue (#556b2f) and small multiples instead of the brand palette, which failed colour-vision checks.

---

## ADR-026 — Production readiness: observability, resilience, secrets, deployment and backups
**Status:** ACCEPTED · 2026-11-19 (implements ADR-005's deferred tracing; extends ADR-002, ADR-013)

**Decision.**
- **Metrics: Prometheus**, pulled from an internal listener (`METRICS_ADDR`, `:9100` in Compose) on the API and the worker, and from `/metrics` on the AI service's internal port. Nothing is exposed through the public web proxy.
  - HTTP metrics are labelled with the **route pattern** (`/api/v1/jobs/{id}`), never the raw path, and no metric is keyed by user, so labels hold no personal data and cardinality stays bounded (an integration test fails if any label contains an id).
  - The worker records job outcomes and run/queue-wait times from River's event stream, and exports queue state from Postgres at scrape time: unfinished jobs by state and kind, the oldest waiting job, jobs discarded in the last hour (dead letters), and overdue schedules. Database pool statistics come from pgx.
- **Tracing: OpenTelemetry over OTLP/HTTP, off unless `OTEL_EXPORTER_OTLP_ENDPOINT` is set.** Only W3C trace context is propagated (not baggage, which could carry data across services).
  - The API names server spans after the route, instruments its AI-service client (so one request is one trace across Go and Python), records SQL spans with statement text only (arguments are never recorded), and carries trace context into River jobs (the work span links to the request that enqueued it).
  - The AI service uses FastAPI's native telemetry for server spans, with its telemetry logs **disabled**, because they would record validation failures together with their input (CV text). Operation spans are off.
  - Logs carry `trace_id` alongside `request_id`.
- **Alerting: Prometheus rules → Alertmanager.** Nine rules (service down, API 5xx rate, API p95 latency, pool saturation, queue backlog, discarded jobs, overdue schedules, queue metrics failing, AI error rate), each linked to a runbook section and covered by `promtool` unit tests. Delivery needs a receiver chosen by the operator (email, Slack, PagerDuty): the shipped config has a placeholder, and the docs say so.
- **Rate limiting in two layers.** nginx limits per client address at the edge (20 r/s, burst 60; 2 r/s, burst 10 under `/api/v1/auth/`). The API limits per signed-in user, or per IP when signed out (`API_RATE_LIMIT_PER_MINUTE`, default 300), on top of the existing per-action limits. The API limit fails open when Redis errors (logged), so a cache outage degrades limiting rather than the product.
- **Graceful shutdown.** On SIGTERM the API fails readiness (`draining`) for `API_HTTP_SHUTDOWN_DRAIN` so load balancers move traffic, then stops accepting connections and lets in-flight requests finish. The worker gives running jobs `WORKER_SHUTDOWN_TIMEOUT` (30 s) and then cancels them; River re-runs interrupted jobs and every job is idempotent. River is started on a context that the signal does not cancel (previously SIGTERM cancelled running jobs at once). uvicorn has a 20 s graceful timeout.
- **Secrets.** Every setting can be read from a file (`NAME_FILE`, for Docker and Kubernetes secrets); setting both forms is an error. The production overlay mounts secrets as files, so they never appear in container environments. Encryption keys are a **versioned keyring**: `DATA_ENCRYPTION_KEY` with `DATA_ENCRYPTION_KEY_VERSION`, plus retired keys that stay readable; `careerpilot keys rotate` re-seals stored Gmail tokens in place (row-locked, idempotent) and `keys status` confirms completion.
- **Production deployment: a Compose overlay** (`deploy/compose.prod.yml`), not Kubernetes. It sets `APP_ENV=production` (strict configuration), runs migrations once as a release step before the API and worker start (`MIGRATE_ON_START=false`), publishes only the web port on loopback for a TLS-terminating proxy, and runs every container read-only, without capabilities or privilege escalation, with memory and CPU limits, restart policies and log rotation. The migrator receives only the database URL.
- **Least-privilege database roles instead of row-level security.** `deploy/postgres/roles.sql` creates `careerpilot_owner` (schema owner, used only by migrations) and `careerpilot_app` (data access only, no DDL, and `audit_events` is append-only; account deletion pseudonymises through a narrow `SECURITY DEFINER` function). Migration 11 keeps the audit table append-only even when the roles exist before the schema. `scripts/check-db-roles.sh` proves it: the real API and worker run as the app role in a throwaway database.
  - **RLS stays deferred (KI-007).** It would need every query to run in a per-user transaction with `SET LOCAL`, a rewrite of the data layer across all modules plus a bypass role for the worker's cross-user jobs. Isolation remains enforced in the application, with cross-user tests in every module. The privilege split limits what a compromised application credential can do (no schema changes, no audit erasure); it does not stop cross-user reads through the application role.
- **Backups.** `scripts/backup.sh` takes a consistent `pg_dump` (custom format) and copies the document bucket, with a manifest of row counts, schema version and checksums. `scripts/restore-drill.sh` restores into a throwaway Postgres and checks the checksum, schema compatibility with the current build, row counts, foreign keys and every document object, without touching live data. `scripts/restore.sh` recreates the database empty before restoring (so objects from newer migrations cannot survive), applies migrations, and waits for the API to report healthy.
- **CI/CD.** CI adds image builds with Trivy (fails on fixable HIGH/CRITICAL), gitleaks over full history, ShellCheck, alert-rule tests, nginx and Compose validation, and the database-roles check. Python dependencies install from a hash-pinned lockfile. A release workflow publishes images to GHCR on version tags with SBOM and provenance attestations; deployment stays environment specific (DEPLOYMENT_GUIDE.md). Dependabot raises weekly update PRs.

**Why.** Phase 10 requires production Docker configuration, CI/CD, backups with tested restores, monitoring, metrics, tracing, alerting, secret management, rate limiting, graceful shutdown, capacity guidance, security checks and runbooks. Every choice reuses the existing stack (Compose, Postgres, River, Redis, nginx) and standard open formats (Prometheus exposition, OTLP), so operators can point them at managed services without code changes.

**Consequences.**
- Verified live: a production-mode stack came up from the overlay with hardening applied; a real backup restored both into a throwaway database and end to end into that stack (migrated to the new schema, sign-in and data served); one request appeared as one trace across both services; the worker's job span linked to its request; Prometheus scraped all services with rules loaded.
- Running it found and fixed real defects: nginx's 1 MiB default rejected CV uploads over 1 MB (since Phase 2), SIGTERM cancelled running jobs, readiness reported missing buckets as healthy, the bundled store could not grow a new bucket, and a base image carried fixable vulnerabilities.
- Not included: a TLS proxy (operators bring one; HSTS belongs there), dashboards (Prometheus and Jaeger UIs only), alert delivery, automatic backup encryption and off-site copy (KI-047 to KI-050). CI has still not run on GitHub (KI-013).

## ADR-027 — Data rights: export and account deletion
**Status:** ACCEPTED · 2026-11-19 (engineering defaults; legal review pending, KI-008)

**Decision.**
- **Export** (`POST /users/me/export`, password re-entry, CSRF, 5 per hour) returns a zip: `README.txt`, `data.json` with every table the user owns, and the uploaded files. Columns come from the catalogue at export time, minus credentials (password hash, OAuth tokens, session and reset tokens), internal storage keys and vector embeddings. A test fails if any table referencing `users` is neither exported nor explicitly excluded, so new tables cannot be silently missed. Exports are audited.
- **Deletion** (`POST /users/me/delete`, password plus typing `DELETE`) revokes Gmail access at Google (best effort), then in one transaction deletes the user (every owned table cascades), pseudonymises the user's audit events (IP and metadata removed; the id no longer maps to anyone) and records an `account.deleted` event without personal data. Stored files are deleted afterwards; any that fail are removed by the daily orphan sweep. The session cookie is cleared, and pending jobs for deleted rows finish as no-ops.
- **Orphan sweep** (daily) removes stored objects that no document row references and that are older than 24 hours (uploads in flight are written before their row).

**Why.** Users must be able to obtain and erase their data (master prompt §34; Settings promised it for Phase 10). Keeping a pseudonymous security record of the deletion preserves auditability without retaining personal data.

**Consequences.** Retention periods for audit records, backups and logs are not legally reviewed (KI-008). Backups taken before a deletion still contain the account until they expire under the backup retention schedule (KI-051). Application logs are retained by the operator's logging platform, outside this mechanism.

---

## ADR-013 — Local ports and compose topology
**Status:** ACCEPTED · 2026-11-14

Development host ports avoid common defaults already in use: Postgres `55432`, Redis `56379`, MinIO `59000`/`59001`, API `8080`, AI `8090`, Web `5173`. All are configurable in `.env`.
