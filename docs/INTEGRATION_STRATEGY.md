# Integration Strategy

Status legend: `IMPLEMENTED` · `MOCKED` · `AWAITING_CREDENTIALS` · `AWAITING_PROVIDER_APPROVAL` · `BLOCKED_BY_PROVIDER` · `NOT_SUPPORTED` · `PLANNED`

> Provider details below reflect public documentation as understood on 2026-11-14 and **must be re-verified against current docs/terms before production enablement** (KNOWN_ISSUES KI-002).

## 1. Job sources (read-only discovery)

| Source | Endpoint (public) | Auth | Rate / terms notes | Data quality | Status |
|---|---|---|---|---|---|
| Greenhouse Job Board | `GET https://boards-api.greenhouse.io/v1/boards/{board_token}/jobs?content=true` | none | Public job board data; polite rate (≤1 req/s per host), identifying User-Agent | High: title, location, HTML-escaped content, first_published, offices; no structured salary or workplace type in this endpoint | **IMPLEMENTED** (verified live 2026-11-14) |
| Lever | `GET https://api.lever.co/v0/postings/{company}?mode=json` | none | Public postings; polite rate | High: categories (location, commitment, team), workplaceType, country, optional salaryRange | **IMPLEMENTED** (verified live) |
| Ashby | `GET https://api.ashbyhq.com/posting-api/job-board/{name}?includeCompensation=true` | none | Public job board API | High: workplaceType, isRemote, secondary locations, structured compensation summary components | **IMPLEMENTED** (verified live) |
| Remotive | `GET https://remotive.com/api/remote-jobs` | none | The legal notice embedded in the API response (read 2026-11-14) says displaying their jobs "in order to collect signups/email addresses" breaches their terms, and asks for at most 4 requests/day. CareerPilot requires an account to view listings | Medium | **BLOCKED_BY_PROVIDER (terms)**: not implemented. Only their paid private API could be used |
| Remote OK | `GET https://remoteok.com/api` | none | Embedded terms: link back to the Remote OK URL **with a followed link** and name Remote OK as the source; do not use their logo. The UI shows "Remote OK (original listing)" with a followed link. Polled at most every 4 h | Medium: salary_min/max in USD/yr (0 = undisclosed; values under 1,000 treated as undisclosed) | **IMPLEMENTED** (verified live) |
| Company career pages (HTML) | n/a | n/a | Only via robots-respecting, per-domain allowlist; SSRF protections | Variable | Deferred |
| LinkedIn, Indeed, Glassdoor | n/a | n/a | ToS prohibit scraping; no candidate job-search API | n/a | NOT_SUPPORTED |

**Common adapter contract** (`internal/sources`):
```go
type Adapter interface {
    Kind() Kind
    List(ctx context.Context, src Source) ([]RawJob, error)   // full board snapshot
}
type Normalizer func(RawJob) (jobs.NormalizedJob, error)
```
Plus shared behaviour:
- token-bucket rate limiter per source
- retry with backoff on 429/5xx (respecting `Retry-After`)
- circuit breaker after N consecutive failures (source marked `DEGRADED`)
- expiry detection: a job missing from a full snapshot is marked `expired_at`
- dedupe across sources via `dedupe_key`

Board token curation: an operator-managed `job_sources` table seeded from `infrastructure/seeds/job_sources.yaml`. Users can request additional companies; board tokens are validated by a test fetch.

**Testing:** recorded fixture payloads per adapter (`testdata/`), `httptest` servers, and no live calls in CI.

## 2. LLM providers
| Provider | Purpose | Auth | Status |
|---|---|---|---|
| Anthropic | default LLM | API key `ANTHROPIC_API_KEY` | AWAITING_CREDENTIALS |
| OpenAI | optional LLM / embeddings | `OPENAI_API_KEY` | AWAITING_CREDENTIALS (optional) |
| Fake provider | tests / offline dev | n/a | PLANNED (test use only) |

Failure modes: timeouts, 429, 5xx, schema-invalid output. Retry policy: 3 attempts with exponential backoff; schema repair once.

## 3. Gmail (Phase 7, implemented: ADR-023)
- **Status:** `IMPLEMENTED`, `AWAITING_CREDENTIALS`. All paths are tested against a fake Google server; no live Google call has been made, because no OAuth client is configured.
- **Purpose:** send approved emails from the user's own address and detect replies. Drafts live in CareerPilot, not in Gmail.
- **Auth:** OAuth 2.0 authorization code with PKCE (S256), offline access, consent prompt, and a single-use state bound to the user. Tokens are AES-256-GCM encrypted (bound to the user id); refresh-token rotation and revocation on disconnect are handled.
- **Scopes:** `gmail.send` and `gmail.metadata` (headers only; bodies are never read). `gmail.readonly` and `gmail.compose` are not requested.
- **Provider constraint:** these are restricted scopes. In "Testing" mode, up to 100 listed test users can connect; production requires Google's OAuth verification and a third-party security assessment (`AWAITING_PROVIDER_APPROVAL`).
- **Setup:** create an OAuth client of type "Web application" in Google Cloud, enable the Gmail API, add the redirect URI `<APP_BASE_URL>/api/v1/integrations/gmail/callback`, and set `GOOGLE_OAUTH_CLIENT_ID` and `GOOGLE_OAUTH_CLIENT_SECRET`.
- **Limits:** our defaults are stricter than Gmail's: 10 emails per day, a 3-day cooldown per recipient, no repeated introduction without a reply, and at most 2 follow-ups, which are drafts only. All are user-configurable within bounds.
- **Reply detection:** threads of sent messages from the last 45 days are polled every 15 minutes, metadata only. Replies and bounces cancel follow-ups.

## 4. Application submission (ADR-007)
| Channel | Status |
|---|---|
| Assisted submission (user submits, attests) | IMPLEMENTED (Phase 6, ADR-022) |
| Email application via Gmail | NOT_STARTED: postings almost never publish an application address (KI-036) |
| Greenhouse/Lever/Ashby candidate-side API submission | BLOCKED_BY_PROVIDER (requires employer API keys) |
| Browser automation (Playwright) | Deferred, per-domain ToS review required |

## 5. Recruiter contacts
| Source | Status |
|---|---|
| User-provided contacts | IMPLEMENTED |
| Addresses published in job postings | IMPLEMENTED, with source evidence. Addresses for other purposes (accommodation, privacy, fraud reports and similar) are excluded. **Measured 2026-11-17: 761 of 762 published addresses in live postings were of that kind**, so this source rarely yields a contact |
| Contact verification | IMPLEMENTED: syntax and DNS (MX, implicit MX, null MX). Bounces mark the address. No SMTP probing |
| Enrichment providers (Hunter, Apollo, etc.) | Not selected: costs money and needs a privacy/processor review. **Requires user approval** |
| LinkedIn profile scraping | NOT_SUPPORTED |

Email addresses are never guessed or presented as verified mailboxes.

## 6. Object storage
MinIO (dev) / S3 (prod). Status: PLANNED (Phase 2).

## 7. Notifications
In-app notifications first. Transactional email (password reset, digests) needs an SMTP/ESP provider. In dev, emails are written to the log or Mailpit. Status: `AWAITING_CREDENTIALS` for production ESP.
