# Operations Runbook

Status: covers the Phase 1 foundation. It expands as workflows, integrations and monitoring are added.

## Health
| Endpoint | Meaning |
|---|---|
| `GET /healthz` (api, ai) | Process alive; no dependency checks |
| `GET /readyz` (api) | `200` when required dependencies (database; plus Redis in production) pass. Per-check status in JSON: `ok`, `fail` or `not_configured` |
| `GET /api/v1/meta` | Integration availability as shown to users |

## Common procedures
**Service won't start.** Read the first log line. Configuration errors list every invalid variable (`invalid configuration: ...`).

**Readiness failing on `database`.**
1. Run `docker compose ps postgres`.
2. Check `DATABASE_URL`.
3. Run `careerpilot migrate status`.

**Migrations.**
- Apply: `docker compose run --rm api migrate up`.
- Inspect: `docker compose run --rm api migrate status`.
- Never edit an applied migration. Add a new one.

**Logs.** JSON on stdout: `docker compose logs api --no-log-prefix | jq`. Correlate using `request_id`, which is returned in the `X-Request-ID` header and in error envelopes.

**Secret rotation.**
- `AI_SERVICE_TOKEN`: update both services together, then restart.
- `SESSION_SECRET`: rotation invalidates sessions (Phase 2+).
- `DATA_ENCRYPTION_KEY`: requires key-versioned re-encryption (procedure to be added with Phase 7 OAuth token storage). Do not rotate it until then.

**Reset local data (destructive).** `docker compose down -v` deletes the Postgres volume. Only use this in development.

## To be added
Workflow stuck/retry procedures (Phase 8), Gmail token revocation (Phase 7), source circuit-breaker reset (Phase 3), backup and restore (Phase 10).
