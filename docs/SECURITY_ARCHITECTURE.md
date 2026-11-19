# Security Architecture

Status: design accepted; controls implemented per phase (see tracker). No compliance claims are made without legal review.

## 1. Threat model (summary)
| Asset | Threats | Primary controls |
|---|---|---|
| User accounts | credential stuffing, session theft, CSRF | Argon2id, rate limits, HttpOnly/SameSite cookies, CSRF tokens, session revocation |
| CVs & generated documents (PII) | unauthorised access, IDOR, leakage via logs | per-user scoping in every query, private storage proxied via API, no PII in logs, metric labels or object keys; encryption at rest depends on the store (provider SSE, or host disk encryption for bundled SeaweedFS, KI-052) |
| Gmail OAuth tokens | theft, misuse | AES-256-GCM at rest with key versioning, minimal scopes, revocation, never logged |
| External actions (email/submission) | unauthorised sending, duplicate sending, agent misuse | deterministic `Authorize` before each side effect, approvals bound to payload hash, idempotency keys, limits, kill switch, audit |
| AI pipeline | prompt injection, data exfiltration via generation, fabricated claims | untrusted-content envelopes, no tools on untrusted calls, schema validation, claim validator, AI service has no DB/side-effect access |
| Job discovery fetchers | SSRF, malicious HTML | fixed host allowlist per adapter, no user-supplied URLs fetched server-side (Phase 3), HTML sanitised (bluemonday) before storage/display |
| Uploads | malware, zip bombs, parser exploits | size limit (10 MB), MIME sniffing + extension allowlist (PDF, DOCX), parsing in the AI service with timeouts/memory limits, malware-scanning hook (ClamAV planned) |

## 2. Authentication & sessions (ADR-003)
- Argon2id: `m=64MiB, t=3, p=2` by default, configurable; parameters stored in the encoded hash.
- Passwords: minimum 10 characters, checked against a small common-password list. No composition rules.
- Session cookie `cp_session`: `HttpOnly; Secure (prod); SameSite=Lax; Path=/`. Idle timeout 7 days, absolute timeout 30 days. Rotated on login.
- CSRF: the token is returned by `GET /api/v1/auth/session` and sent as an `X-CSRF-Token` header on unsafe methods. It is validated against a hash stored on the session.
- Login and reset: generic error messages, per-IP and per-email rate limits, constant-time comparisons.

## 3. Authorisation
- Resource-level: every repository method for user-owned data requires `userID`. Handlers take `userID` only from the session, never from the request body or path.
- Roles: `user`, plus `admin` (operator) for source curation. Admin endpoints are under `/api/v1/admin`, with a separate check.
- Tests: user-isolation tests per module (user B gets 404 for user A's resources, never 403, to avoid leaking existence).

## 4. Input/output handling
- JSON bodies use a size limit and strict decoding (unknown fields rejected).
- Output: the React SPA escapes by default. Job HTML is sanitised server-side and rendered only through a sanitised component. No `dangerouslySetInnerHTML` without sanitisation.
- Headers: CSP (`default-src 'self'`, no inline scripts), `X-Content-Type-Options`, `Referrer-Policy: strict-origin-when-cross-origin`, `frame-ancestors 'none'`, HSTS in production.
- SQL: parameterised queries only (pgx).

## 5. Secrets
- Supplied via environment variables, or as files through `NAME_FILE` (Docker and Kubernetes secrets, or a secret manager mounting files). Setting both forms is a startup error. The production overlay mounts every secret as a file, so none appear in container environments (verified with `docker inspect`). `.env` and `deploy/secrets/` are git-ignored, and `.env.example` contains no real values.
- In production, startup fails without strong secrets: `SESSION_SECRET`, `DATA_ENCRYPTION_KEY` (32 bytes, base64) and `AI_SERVICE_TOKEN`, plus an HTTPS origin and SMTP.
- `DATA_ENCRYPTION_KEY` is a versioned keyring. Retired keys stay readable until `careerpilot keys rotate` re-seals every token, and `keys status` confirms completion (ADR-026).
- The migration step receives only the database URL.
- Logs are redacted: the logger never receives tokens, passwords, cookies or document contents. Traces never record SQL arguments or request bodies; FastAPI's telemetry logs (which would include invalid request input) are disabled.

## 6. Privacy (design intent; legal review required)
Relevant frameworks: Kenya Data Protection Act 2019, GDPR (EU users), and others where the product operates.
- **Consent and purpose:** explicit consent screens for CV processing by AI providers, Gmail access, and autonomous actions.
- **Data minimisation:** inbound email bodies are never read or stored (`gmail.metadata` scope; replies are kept as sender and time only). Outbound emails the user writes and approves are stored as records of what was sent. Recruiter contacts are limited to professional details and their source.
- **Rights (implemented, ADR-027):** data export (`data.json` covering every user table, plus uploaded files, in a zip) and account deletion (cascade, audit pseudonymisation, Gmail revocation, storage purge with an orphan-sweep fallback), both from Settings and both requiring the password. A test fails if a new user table is missing from the export. Older backups keep a deleted account until they expire (KI-051).
- **Processors:** LLM providers, cloud hosting and email are listed in a processor register. Cross-border transfer is disclosed.
- **AI transparency:** match scores are labelled as decision support, not predictions of hiring outcomes.

## 7. Audit
`audit_events` records an entry for every authentication event, permission change, approval decision, external action attempt and outcome, data export and deletion. With the production roles (`deploy/postgres/roles.sql`), the application role can only insert into it. On account deletion, the narrow `SECURITY DEFINER` function `audit_forget_user` removes IP addresses and metadata from that user's events, and the events themselves remain.

## 8. Dependency & supply-chain
Lockfiles are committed: `go.sum`, `pnpm-lock.yaml`, and `services/ai/requirements.lock` (pip-compile with hashes; the image installs with `--require-hashes`).
- CI runs `govulncheck`, `pnpm audit`, `pip-audit`, gitleaks over full history, and Trivy on every image (fails on fixable HIGH/CRITICAL). Base images are pinned to versions, and security updates are applied at build.
- Releases publish SBOM and build-provenance attestations. Dependabot is configured (`.github/dependabot.yml`).
- CI has not yet run on GitHub (KI-013); every check was run locally on 2026-11-19 and passed.

## 9. Infrastructure and operations (ADR-026)
- **Containers** (production overlay): read-only root filesystems, `no-new-privileges`, all capabilities dropped (except where Postgres and SeaweedFS need them), non-root users, resource limits. Only the web proxy publishes a port, on loopback, behind the operator's TLS proxy, which sets HSTS.
- **Database:** least-privilege roles. The application role cannot change the schema or alter audit records. Row-level security is not enabled (KI-007); isolation is enforced and tested in the application.
- **Abuse controls:** edge rate limits (nginx) plus per-user and per-action limits in the API. The API limit fails open, with logging, when Redis is unavailable.
- **Monitoring:** metrics and traces carry no personal data (route-pattern labels; no SQL arguments). Metrics are served on internal listeners only.
