# CareerPilot AI

An autonomous career-management platform. It discovers relevant jobs from approved sources, explains suitability with evidence, prepares truthful applications and runs outreach, with human approval and auditable control over every external action.

> **Status: Phase 1 (Foundation) of 11.** The platform skeleton runs end-to-end (API, AI service, web app, Postgres, Redis), and product features arrive phase by phase. See [docs/DEVELOPMENT_ROADMAP.md](docs/DEVELOPMENT_ROADMAP.md) and [docs/IMPLEMENTATION_TRACKER.md](docs/IMPLEMENTATION_TRACKER.md) for what actually works today.

## Architecture
| Component | Path | Stack |
|---|---|---|
| Core API (authoritative data, permissions, workflows) | `services/api` | Go 1.26, chi, pgx, goose |
| AI service (stateless parsing, extraction, generation) | `services/ai` | Python 3.12, FastAPI, Pydantic |
| Web app | `apps/web` | React 19, Vite, Tailwind v4, TanStack Query |
| Data | compose | PostgreSQL 16 + pgvector, Redis 7 |
| Design source of truth | `stitch_careerpilot_ai_platform_design/` | Google Stitch "Editorial Intelligence" |

Details: [SYSTEM_ARCHITECTURE](docs/SYSTEM_ARCHITECTURE.md) · [ADRs](docs/ARCHITECTURE_DECISIONS.md) · [Security](docs/SECURITY_ARCHITECTURE.md)

## Quick start
Prerequisites: Docker + Compose, Go ≥1.24 (auto-fetches 1.26), Python 3.12, Node 22 + pnpm.

```bash
make env      # create .env with generated secrets (never committed)
make up       # build + start everything → http://localhost:8088
make ps       # status; API health: curl localhost:8080/readyz
```

Host-based development with hot reload:
```bash
make setup    # install Go/Python/Node dependencies
make infra    # Postgres + Redis in Docker
make dev-api  # Go API on :8080 (applies migrations)
make dev-ai   # AI service on :8090
make dev-web  # Vite on :5173, proxies /api → :8080
```

Quality gates: `make lint` and `make test`.

## Configuration
All settings are documented in [.env.example](.env.example). With `APP_ENV=production`, the services refuse to start without strong secrets, and the AI service rejects the fake provider.

Live AI features need `ANTHROPIC_API_KEY` (or `OPENAI_API_KEY`). Without one, the platform reports the LLM as **not configured** and never fabricates AI output.

## Principles
- Never fabricates CV content, contacts or results. Unknown stays unknown.
- No email is sent and no application submitted without authorisation, re-checked immediately before the action.
- Never bypasses CAPTCHA, MFA or a provider's terms. See [INTEGRATION_STRATEGY](docs/INTEGRATION_STRATEGY.md).
