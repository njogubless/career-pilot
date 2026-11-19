# Deployment Guide

Status:
- Development stack (Compose): `IMPLEMENTED`.
- Single-node production overlay (`deploy/compose.prod.yml`): `IMPLEMENTED`, verified locally in production mode (ADR-026). It has not run on a public host yet.
- Image publishing (`.github/workflows/release.yml`): `IMPLEMENTED`, not yet run (no GitHub remote, KI-013).

This guide only describes what exists. Operating procedures are in `OPERATIONS_RUNBOOK.md`.

## Images
| Image | Dockerfile | Runtime user | Port | Notes |
|---|---|---|---|---|
| api | `services/api/Dockerfile` (distroless static) | `nonroot` | 8080 (+9100 metrics) | Commands: `serve` (default), `worker`, `migrate up/status`, `keys status/rotate`, `users promote/demote`, `sources seed`, `healthcheck` |
| ai | `services/ai/Dockerfile` (python:3.12-slim, security updates applied) | uid 10001 | 8090 | Dependencies install from a hash-pinned lockfile; embedding model baked in |
| web | `apps/web/Dockerfile` (nginx-unprivileged 1.29.3) | 101 | 8080 | Serves the SPA, proxies `/api/`, edge rate limits, 11 MiB uploads |

Build arguments `VERSION` and `REVISION` set OCI labels and the default `APP_VERSION`. Tagged releases (`v1.2.3`) publish `ghcr.io/<owner>/<repo>/{api,ai,web}` with SBOM and provenance attestations.

## Development (Compose)
```bash
make env        # .env with generated secrets
make up         # web on http://localhost:8088
make observability   # optional: + Prometheus :59090, Alertmanager :59093, Jaeger :56686
```
All published ports bind to `127.0.0.1`. Set `OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318` in `.env` to send traces to Jaeger.

## Production: single node
The overlay keeps the same services. Postgres, Redis and object storage can stay bundled, or point at managed services (below).

### 1. Prerequisites
- A Linux host with Docker Engine and Compose v2, at least 4 vCPU and 8 GB RAM (see Capacity), and disk for Postgres, documents and backups.
- A domain name and a **TLS-terminating reverse proxy** in front of `web` (Caddy, Traefik, nginx or a cloud load balancer). It must:
  - forward to `127.0.0.1:${WEB_HOST_PORT:-8088}`;
  - set `X-Forwarded-For` (the API trusts only the last hop) and `X-Forwarded-Proto: https`;
  - send `Strict-Transport-Security: max-age=63072000; includeSubDomains`;
  - allow request bodies of at least 11 MiB.
- An SMTP provider for password-reset email (required in production, KI-010).
- Optional: an Anthropic API key (AI features), Google OAuth credentials (Gmail), and an alert receiver.

### 2. Configure
```bash
cp .env.example .env            # non-secret settings
scripts/init-secrets.sh         # deploy/secrets/* (git-ignored); fill in the empty ones
```
In `.env`, set at least:
- `PUBLIC_BASE_URL=https://your.domain` (cookies are Secure; links in emails use it);
- `SMTP_ADDR`, `SMTP_FROM` and `SMTP_USERNAME` (the password goes in `deploy/secrets/smtp_password`);
- `S3_ACCESS_KEY` and `S3_SECRET_KEY` for the bundled store, or your provider's credentials;
- `APP_VERSION` to the image tag you deploy, and `IMAGE_PREFIX` if you pull published images (`ghcr.io/<owner>/<repo>`);
- `OTEL_TRACES_SAMPLER_ARG=0.1` (or lower) if you enable tracing: the development default samples every request.

Secrets are files mounted at `/run/secrets/*` and read through `*_FILE` variables, so they never appear in `docker inspect`. With a secret manager (Vault, AWS Secrets Manager, Docker Swarm or Kubernetes secrets), mount the same files at the same paths. **Back up `data_encryption_key`**: without it, users must reconnect Gmail.

### 3. Database roles (recommended)
Run `deploy/postgres/roles.sql` once as the database administrator (see the file header). Then point `deploy/secrets/database_url` for the `migrate` service at `careerpilot_owner`, and for `api`/`worker` at `careerpilot_app`. The app role cannot change the schema or erase audit records. Verify the setup with `make check-db-roles`.

### 4. Start
```bash
docker compose -f docker-compose.yml -f deploy/compose.prod.yml up -d --build   # or pull published images
docker compose -f docker-compose.yml -f deploy/compose.prod.yml ps
```
Order: Postgres becomes healthy, then `migrate` runs and exits 0, then the API (which refuses to start on an outdated schema), worker and web start. In production mode the API and AI service refuse to start with missing or weak secrets, a non-HTTPS origin, or no SMTP.

### 5. Verify
- `curl -fsS https://your.domain/api/v1/meta` shows `"environment":"production"` and honest integration status.
- Register, sign in, and upload a CV larger than 1 MB.
- `docker compose ... logs api | grep '"level":"ERROR"'` is empty.
- Prometheus targets are up, and `make backup && make restore-drill` passes.

### 6. Upgrade
1. `make backup`.
2. Set the new `APP_VERSION`, pull or build, then run `up -d`. The `migrate` job applies new migrations before the new API starts.
3. The API drains for 5 s (`API_HTTP_SHUTDOWN_DRAIN`); the worker lets jobs finish for up to 30 s.

Rollback: redeploy the previous tag. Migrations are additive by convention. If one is not, restore the pre-upgrade backup (`scripts/restore.sh`).

### Hardening applied by the overlay
- Read-only root filesystems, `no-new-privileges`, all Linux capabilities dropped (Postgres and SeaweedFS keep the defaults they need to manage their data directories), and `/tmp` on tmpfs.
- Memory and CPU limits per service, `restart: unless-stopped`, and JSON log rotation (5 × 20 MB).
- Only `web` publishes a port, on loopback. Metrics, the AI service, Postgres, Redis and storage are reachable only on the Compose network.

## Managed services
| Bundled | Managed replacement | How |
|---|---|---|
| Postgres (pgvector) | Any Postgres 16 with `vector` and `citext` (RDS, Cloud SQL, Neon, Supabase) | Put its URL in `deploy/secrets/database_url` (use `sslmode=require`); scale `postgres` to 0. Use provider point-in-time recovery in addition to `make backup` |
| Redis | Any Redis 7 | `REDIS_URL`; holds only rate-limit counters (non-authoritative) |
| SeaweedFS | S3, R2, GCS (S3 API), MinIO | `S3_ENDPOINT`, `S3_USE_SSL=true`, credentials, and `S3_CREATE_BUCKET=false`. Create the bucket private, with server-side encryption and versioning |

## Capacity considerations
Measured on the development host (4 cores, idle stack, `docker stats`, 2026-11-19); treat these as starting points, not benchmarks.

| Component | Measured | Driver and guidance |
|---|---|---|
| API | about 15 MB RSS idle | Stateless. Each Argon2id sign-in attempt uses 64 MiB briefly (m=64 MiB, t=3, p=2), so concurrent sign-ins drive memory; the 1 GB limit allows about 10 at once with headroom. Add replicas behind the proxy (Redis keeps rate limits shared). Pool: `DATABASE_MAX_CONNS` (10) per replica |
| Worker | about 85 MB RSS | River runs 4 jobs concurrently. Discovery is network-bound. Jobs are idempotent and River coordinates several replicas |
| AI service | about 320 MB RSS with the local embedding model | Embedding is CPU-bound at about 1–2 postings/s/core (KI-026); LLM calls are I/O-bound. Give it 2+ cores; a large first backfill competes with requests |
| Postgres | 213 MB on disk for about 4,700 postings (19 MB compressed dump) | Postings plus embeddings dominate. Watch `careerpilot_db_pool_*` and disk growth with discovery volume |
| Object storage | CVs up to 10 MB each | Size for users × documents. The orphan sweep reclaims failed deletes daily |
| LLM cost | Token use is stored with each CV extraction, job analysis and document generation | No per-user budget enforcement yet (Phase 11 review item) |

Connection budget: `(api replicas + worker replicas) × DATABASE_MAX_CONNS`, plus migrations and backups, must stay below Postgres `max_connections` (100 by default).
