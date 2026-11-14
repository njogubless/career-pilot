# Integration Strategy

Status legend: `IMPLEMENTED` · `MOCKED` · `AWAITING_CREDENTIALS` · `AWAITING_PROVIDER_APPROVAL` · `BLOCKED_BY_PROVIDER` · `NOT_SUPPORTED` · `PLANNED`

> Provider details below reflect public documentation as understood on 2026-11-14 and **must be re-verified against current docs/terms before production enablement** (KNOWN_ISSUES KI-002).

## 1. Job sources (read-only discovery)

| Source | Endpoint (public) | Auth | Rate / terms notes | Data quality | Status |
|---|---|---|---|---|---|
| Greenhouse Job Board | `GET https://boards-api.greenhouse.io/v1/boards/{board_token}/jobs?content=true` | none | Public job board data; be polite (≤1 req/s per board), cache, identify the client in User-Agent | High: title, location, HTML content, updated_at, absolute_url; salary rarely structured | PLANNED (Phase 3) |
| Lever | `GET https://api.lever.co/v0/postings/{company}?mode=json` | none | Public postings; polite rate | High: categories (location, commitment, team), workplaceType, salaryRange sometimes | PLANNED |
| Ashby | `GET https://api.ashbyhq.com/posting-api/job-board/{name}?includeCompensation=true` | none | Public job board API | High: compensation often present, isRemote, location | PLANNED |
| Remotive | `GET https://remotive.com/api/remote-jobs` | none | Terms require attribution/link back and discourage frequent polling (a few times per day) | Medium | PLANNED |
| Remote OK | `GET https://remoteok.com/api` | none | Terms require attribution and link back to the original | Medium | PLANNED |
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

## 3. Gmail (Phase 7)
- **Purpose:** send approved outreach and application emails, create drafts, detect replies.
- **Auth:** OAuth 2.0 authorization-code flow with PKCE, offline access. Tokens are encrypted at rest (AES-GCM) and refresh-token rotation is handled.
- **Scopes, minimum needed:**
  - `gmail.send` to send.
  - `gmail.readonly`, or `gmail.metadata` plus thread fetch, to detect replies. We will try thread-scoped reads of threads we created.
  - `gmail.compose` for draft-only mode.
- **Provider constraint:** these are sensitive/restricted scopes. Production use beyond test users requires Google OAuth app verification, and restricted scopes require a third-party security assessment. Status: `AWAITING_PROVIDER_APPROVAL` for production, workable in testing mode with up to 100 test users.
- **Limits:** Gmail per-user sending limits. Our own limits are stricter: default 10 outreach emails per day per user, 3-day cooldown per recipient, no repeat sends without a reply, and follow-ups capped at 2.
- **Reply detection:** poll threads by stored `thread_id` (watch/Pub/Sub later). A reply cancels pending follow-ups.
- **Testing:** fake Gmail client interface. Automated tests never send real email.

## 4. Application submission (ADR-007)
| Channel | Status |
|---|---|
| Assisted submission (user submits, attests) | PLANNED (Phase 6) |
| Email application via Gmail | PLANNED (Phase 7, after Gmail) |
| Greenhouse/Lever/Ashby candidate-side API submission | BLOCKED_BY_PROVIDER (requires employer API keys) |
| Browser automation (Playwright) | Deferred, per-domain ToS review required |

## 5. Recruiter contacts
| Source | Status |
|---|---|
| User-provided contacts | PLANNED |
| Contacts named in job postings (e.g. "contact jane@company.com") | PLANNED: extracted with source = posting, verification = `UNVERIFIED` |
| Enrichment providers (Hunter, Apollo, etc.) | Not selected: costs money and needs a privacy/processor review. **Requires user approval** |
| LinkedIn profile scraping | NOT_SUPPORTED |

Email addresses are never guessed and presented as verified.

## 6. Object storage
MinIO (dev) / S3 (prod). Status: PLANNED (Phase 2).

## 7. Notifications
In-app notifications first. Transactional email (password reset, digests) needs an SMTP/ESP provider. In dev, emails are written to the log or Mailpit. Status: `AWAITING_CREDENTIALS` for production ESP.
