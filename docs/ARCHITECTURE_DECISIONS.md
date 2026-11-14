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

**Defence in depth:** Postgres Row-Level Security on user-owned tables is planned for Phase 10 (tracked in KNOWN_ISSUES).

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

## ADR-013 — Local ports and compose topology
**Status:** ACCEPTED · 2026-11-14

Development host ports avoid common defaults already in use: Postgres `55432`, Redis `56379`, MinIO `59000`/`59001`, API `8080`, AI `8090`, Web `5173`. All are configurable in `.env`.
