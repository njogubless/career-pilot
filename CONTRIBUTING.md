# Contributing

1. Read `CLAUDE.md` and the relevant sections of `docs/MASTER_BUILD_PROMPT.md`.
2. Work in vertical slices: migration → service → API → UI → tests → docs.
3. Before opening a PR, run `make lint test`.

## Conventions
- **Go:** handlers stay thin. Business logic lives in module services. Every query on user-owned data takes `userID` from the session. Return `*httpx.Error` for client-facing failures.
- **Python:** Pydantic models for every request and response. No DB access and no external side effects in the AI service.
- **Web:** use TanStack Query for server state; do not copy API data into global stores. Use only the design tokens in `src/styles/index.css`, and keep corners at 4 px or less. Never render sample data as if it were real. Unbuilt features get an honest state, not a dead button.
- **Migrations:** add a new numbered goose file, and never edit an applied migration.
- **Docs:** update `docs/IMPLEMENTATION_TRACKER.md` with status and evidence. `VERIFIED` requires evidence.
- **Secrets:** never in code, tests, logs or docs. Tests must never call real providers, send email or submit applications.
