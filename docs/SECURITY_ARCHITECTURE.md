# Security Architecture

Status: design accepted; controls implemented per phase (see tracker). No compliance claims are made without legal review.

## 1. Threat model (summary)
| Asset | Threats | Primary controls |
|---|---|---|
| User accounts | credential stuffing, session theft, CSRF | Argon2id, rate limits, HttpOnly/SameSite cookies, CSRF tokens, session revocation |
| CVs & generated documents (PII) | unauthorised access, IDOR, leakage via logs | per-user scoping in every query, private storage proxied via API, no PII in logs or object keys, encryption at rest |
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
- Supplied via environment or a secret manager. `.env` is git-ignored, and `.env.example` contains no real values.
- In production, startup fails without strong secrets: `SESSION_SECRET`, `DATA_ENCRYPTION_KEY` (32 bytes, base64) and `AI_SERVICE_TOKEN`.
- Logs are redacted: the logger never receives tokens, passwords, cookies or document contents.

## 6. Privacy (design intent; legal review required)
Relevant frameworks: Kenya Data Protection Act 2019, GDPR (EU users), and others where the product operates.
- **Consent and purpose:** explicit consent screens for CV processing by AI providers, Gmail access, and autonomous actions.
- **Data minimisation:** email bodies are not stored; only metadata is kept. Recruiter contacts are limited to professional details.
- **Rights:** data export (JSON + documents ZIP) and account deletion (cascade + storage purge), available from Settings.
- **Processors:** LLM providers, cloud hosting and email are listed in a processor register. Cross-border transfer is disclosed.
- **AI transparency:** match scores are labelled as decision support, not predictions of hiring outcomes.

## 7. Audit
`audit_events` records an entry for every authentication event, permission change, approval decision, external action attempt and outcome, data export and deletion. The table is append-only.

## 8. Dependency & supply-chain
Lockfiles are committed: `go.sum`, `pnpm-lock.yaml`, and `requirements.lock` (pip-compile). CI runs `govulncheck`, `pnpm audit`, `pip-audit`, Dependabot or Renovate, and pinned base images.
