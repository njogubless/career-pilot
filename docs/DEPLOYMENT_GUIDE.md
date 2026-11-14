# Deployment Guide

Status: development/single-node deployment via Docker Compose is `IMPLEMENTED`. Production deployment (managed services, TLS, backups, monitoring) is Phase 10 and `NOT_STARTED`. This guide only describes what exists.

## Images
| Image | Dockerfile | Runtime user | Port |
|---|---|---|---|
| api | `services/api/Dockerfile` (distroless static) | `nonroot` | 8080 |
| ai | `services/ai/Dockerfile` (python:3.12-slim) | uid 10001 | 8090 |
| web | `apps/web/Dockerfile` (nginx-unprivileged) | nginx (unprivileged) | 8080 |

The `api` image entrypoint is `/careerpilot`, with commands `serve` (default), `migrate up`, `migrate status` and `healthcheck`. `serve` applies pending migrations at startup. For multi-replica production, run `migrate up` as a one-off release step instead.

## Single-node (Compose)
```bash
make env                 # or provide .env from your secret store
make up
curl -s localhost:8080/readyz
```
All published ports bind to `127.0.0.1`. Put a TLS-terminating reverse proxy in front of `web` (which proxies `/api/` to `api`) for any non-local use.

## Required configuration in production
`APP_ENV=production` enforces the following:
- `DATABASE_URL`
- `REDIS_URL`
- `SESSION_SECRET` (≥32 chars)
- `DATA_ENCRYPTION_KEY` (32 bytes, base64)
- `AI_SERVICE_URL` and `AI_SERVICE_TOKEN` (≥32 chars, same value on both services)
- `S3_*`

The AI service additionally rejects `AI_PROVIDER=fake`.

## Not yet covered (Phase 10)
Managed Postgres with pgvector, backups and restore drills, TLS, HSTS at the edge, metrics and tracing, alerting, CD pipeline, secret manager integration.
