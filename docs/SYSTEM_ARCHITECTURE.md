# System Architecture

Status: design accepted (see ADR-001 to ADR-013). Implementation status per component is tracked in `IMPLEMENTATION_TRACKER.md`.

## 1. Component view

```text
                         ┌──────────────────────────────┐
  Browser (React SPA) ── │  Go API  (services/api serve) │ ── S3-compatible storage (private)
   cookie session +CSRF  │  /api/v1  modular monolith    │
                         └──────┬───────────────┬────────┘
                                │ SQL (pgx)     │ HTTP/JSON + service token
                         ┌──────▼──────┐   ┌────▼─────────────────────┐
                         │ PostgreSQL  │   │ Python AI service (FastAPI)│── LLM providers
                         │ + pgvector  │   │ stateless: parse, extract, │   (Anthropic / OpenAI)
                         │ + River jobs│   │ embed, generate, explain   │
                         └──────▲──────┘   └────▲─────────────────────┘
                                │               │
                         ┌──────┴───────────────┴──┐
                         │ Go worker (services/api  │── Job sources (Greenhouse/Lever/
                         │ worker): River consumer, │   Ashby/Remotive/RemoteOK)
                         │ orchestrator, agents'    │── Gmail API (OAuth, user-authorised)
                         │ tool execution           │
                         └──────────┬───────────────┘
                                    │
                                 Redis (rate-limit buckets, caches; never authoritative)
```

## 2. Responsibilities

| Component | Owns | Never does |
|---|---|---|
| **Go API** | Auth, sessions, authorisation, all authoritative writes, application state machine, permissions, approvals, audit, rate limits, file access | LLM calls with untrusted content outside the AI service |
| **Go worker** | Executes workflow tasks: discovery fetches, calls to the AI service, matching computation, outbound email sending (after permission re-check), timers and follow-ups | Bypasses services or permission checks; it uses the same domain services as the API |
| **AI service** | CV parsing, job requirement extraction, skill normalisation, embeddings, goal interpretation into a *proposed* plan, document generation, claim validation, explanations | DB access, sending email, submitting applications, deciding permissions |
| **PostgreSQL** | Source of truth for all records, including workflow state and the job queue (River) | n/a |
| **Redis** | Token-bucket rate limiting, short-lived caches | Holds any record that must survive |
| **Object storage** | CV uploads, generated documents, evidence attachments | Public access |

## 3. Go package layout (`services/api`)

```text
cmd/careerpilot/          main: `serve`, `worker`, `migrate`
internal/
  platform/               cross-cutting: config, db, httpx (errors, JSON, middleware), logging,
                          clock, ids, crypto (token hashing, AES-GCM for OAuth tokens), ratelimit
  auth/                   registration, login, sessions, CSRF, password reset
  users/                  account, settings, export/delete
  profiles/               profile, experience, education, skills, certs, projects, provenance
  preferences/            career preferences, hard constraints
  documents/              uploads, versions, storage adapter
  sources/                source adapter contract + adapters + registry of boards
  jobs/                   normalised jobs, dedupe, expiry, search
  matching/               eligibility filters, weighted scorer, versions
  applications/           state machine, evidence, answers, documents links
  recruiters/             contacts + verification state
  outreach/               drafts, sending (via integrations/gmail), replies, follow-ups
  automation/             permissions, modes, limits, kill switch
  workflows/              orchestrator, task model, River integration, agents & tool registry
  aiclient/               typed client for the AI service
  notifications/          in-app notifications
  analytics/              read models over persisted records
  audit/                  append-only audit events
migrations/               goose SQL migrations (embedded)
```

Dependency direction: `handlers → services → repositories → platform`. Domain modules may call other modules' **services**, never their repositories. Handlers are thin (decode → authorise → call service → encode).

## 4. Core flows

### 4.1 Goal → plan → execution (summary; detail in AGENT_ARCHITECTURE.md)
1. `POST /api/v1/search-goals` stores the goal.
2. The orchestrator creates a `workflow_execution`. Task 1, `interpret_goal`, calls the AI service, which returns a *proposed* structured plan.
3. The Go plan validator checks the plan against the allowed task types, the user's permissions and limits. Any missing information becomes a `NEEDS_INPUT` state with questions for the user.
4. Tasks run as River jobs. Completing a task and enqueuing its dependents happen in the same transaction.
5. Approval checkpoints create `approval_requests`; the workflow waits in `AWAITING_APPROVAL`.
6. Every external side effect runs `automation.Authorize(ctx, user, action)` immediately before execution, then is recorded in `audit_events`.

### 4.2 Application lifecycle
`DISCOVERED → SHORTLISTED → PREPARING → AWAITING_APPROVAL → APPROVED → SUBMITTED → ACKNOWLEDGED → (INTERVIEWING | ASSESSMENT) → OFFER`. Terminal states are `REJECTED`, `WITHDRAWN` and `FAILED`. Transitions are enforced in `applications.StateMachine`, and `SUBMITTED` requires an evidence record (ADR-007).

## 5. Cross-cutting concerns

- **Idempotency.** Mutating endpoints accept an `Idempotency-Key` header, stored per user with the response hash. External actions use deterministic keys, for example `send_email:{outreach_message_id}`, enforced by unique constraints.
- **Errors.** Every error uses the same envelope: `{"error":{"code","message","details","request_id"}}`.
- **Observability.** JSON logs with `request_id`, `user_id` (internal ID only), `workflow_id`. `/healthz` reports liveness and `/readyz` checks DB, Redis, storage and the AI service. Metrics (Prometheus) and tracing (OTel) come in Phase 10.
- **Configuration.** Environment variables are validated at startup. In `APP_ENV=production`, missing secrets, a fake AI provider, or insecure cookies cause a startup failure.
- **Graceful shutdown.** Context cancellation, an HTTP server drain, and River stop with a timeout.

## 6. Deployment topology
Docker Compose for development and single-node staging. Production targets one container image per service (api/worker share an image), managed Postgres with pgvector, managed Redis, and S3. See DEPLOYMENT_GUIDE.md.
