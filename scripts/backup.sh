#!/usr/bin/env bash
# Backs up the database and the document store (ADR-026, OPERATIONS_RUNBOOK.md).
#
#   scripts/backup.sh [backup-root]        (default: ./backups)
#
# Produces <backup-root>/<UTC timestamp>/ with:
#   db.dump        pg_dump custom format (compressed; restore with pg_restore)
#   objects/       a copy of every object in the document bucket
#   manifest.json  schema version, row counts, object count and checksums,
#                  which scripts/restore-drill.sh verifies against
#
# Works against the Compose stack by default. For managed services set
#   BACKUP_DATABASE_URL  (a postgres:// URL reachable from this host)
#   BACKUP_S3_ENDPOINT   (https://... for the S3 API; empty skips objects)
# S3 credentials and bucket come from S3_ACCESS_KEY, S3_SECRET_KEY, S3_BUCKET.
#
# Backups contain personal data. Store them encrypted, off the host, with
# restricted access, and delete them on the retention schedule
# (BACKUP_KEEP, default 14 most recent).
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck source=scripts/lib-env.sh
source scripts/lib-env.sh
load_env .env

root="${1:-backups}"
ts="$(date -u +%Y%m%dT%H%M%SZ)"
dir="$root/$ts"
keep="${BACKUP_KEEP:-14}"
pg_image="pgvector/pgvector:pg16"
aws_image="amazon/aws-cli:2.31.39"
umask 077
mkdir -p "$dir/objects"

psql_q() { # query → single value
  if [[ -n "${BACKUP_DATABASE_URL:-}" ]]; then
    docker run --rm --network host "$pg_image" psql "$BACKUP_DATABASE_URL" -tAc "$1"
  else
    docker compose exec -T postgres psql -U "${POSTGRES_USER:-careerpilot}" -d "${POSTGRES_DB:-careerpilot}" -tAc "$1"
  fi
}

echo "backup → $dir"
# 1. Database: one consistent snapshot (pg_dump runs in a single transaction).
if [[ -n "${BACKUP_DATABASE_URL:-}" ]]; then
  docker run --rm --network host "$pg_image" pg_dump "$BACKUP_DATABASE_URL" -Fc >"$dir/db.dump"
else
  docker compose exec -T postgres pg_dump -U "${POSTGRES_USER:-careerpilot}" -d "${POSTGRES_DB:-careerpilot}" -Fc >"$dir/db.dump"
fi

# Row counts of the tables that matter most for a restore check. Taken after
# the dump, so a busy system may show slightly higher counts than the dump;
# the drill treats the manifest as a lower bound for append-mostly tables.
counts="$(psql_q "SELECT json_build_object(
  'users', (SELECT count(*) FROM users),
  'professional_profiles', (SELECT count(*) FROM professional_profiles),
  'profile_documents', (SELECT count(*) FROM profile_documents),
  'jobs', (SELECT count(*) FROM jobs),
  'applications', (SELECT count(*) FROM applications),
  'workflow_executions', (SELECT count(*) FROM workflow_executions),
  'audit_events', (SELECT count(*) FROM audit_events))")"
schema="$(psql_q "SELECT max(version_id) FROM goose_db_version WHERE is_applied")"

# 2. Objects: copy the bucket (Compose: SeaweedFS on the internal network).
objects=0
if [[ -n "${BACKUP_DATABASE_URL:-}" && -z "${BACKUP_S3_ENDPOINT:-}" ]]; then
  echo "BACKUP_S3_ENDPOINT not set: skipping objects (use provider versioning/replication)"
else
  endpoint="${BACKUP_S3_ENDPOINT:-http://storage:8333}"
  net=(--network host)
  [[ -z "${BACKUP_S3_ENDPOINT:-}" ]] && net=(--network "$(docker compose ps -q storage | xargs docker inspect --format '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}}')")
  docker run --rm "${net[@]}" -u "$(id -u):$(id -g)" -e HOME=/tmp \
    -e AWS_ACCESS_KEY_ID="$S3_ACCESS_KEY" -e AWS_SECRET_ACCESS_KEY="$S3_SECRET_KEY" -e AWS_DEFAULT_REGION="${S3_REGION:-us-east-1}" \
    -v "$PWD/$dir/objects:/objects" "$aws_image" --endpoint-url "$endpoint" s3 sync "s3://${S3_BUCKET}" /objects --only-show-errors
  objects="$(find "$dir/objects" -type f | wc -l | tr -d ' ')"
fi

dump_sha="$(sha256sum "$dir/db.dump" | cut -d' ' -f1)"
cat >"$dir/manifest.json" <<JSON
{
  "created_at": "$ts",
  "format": "careerpilot-backup-v1",
  "schema_version": $schema,
  "db_dump_sha256": "$dump_sha",
  "db_dump_bytes": $(stat -c %s "$dir/db.dump"),
  "row_counts": $counts,
  "objects": $objects
}
JSON
echo "done: schema $schema, $(du -h "$dir/db.dump" | cut -f1) dump, $objects objects"

# 3. Retention: keep the newest $keep backups.
ls -1d "$root"/*/ 2>/dev/null | sort | head -n -"$keep" | while read -r old; do
  echo "retention: removing $old"
  rm -rf -- "$old"
done
