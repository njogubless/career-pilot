#!/usr/bin/env bash
# Restores a backup over the running Compose stack's database and bucket.
# DESTRUCTIVE: replaces current data. Run scripts/restore-drill.sh on the same
# backup first, and take a fresh backup of the current state.
#
#   scripts/restore.sh <backup-dir> --yes-replace-current-data
#
# Steps: stop api and worker (no writes during restore), pg_restore --clean
# into the database, sync objects back into the bucket, apply this build's
# migrations (a backup may predate them), start api and worker, then wait for
# the API container to report healthy.
#
# Targets the Compose project in the environment: set COMPOSE_PROJECT_NAME and
# COMPOSE_FILE (e.g. docker-compose.yml:deploy/compose.prod.yml) for production.
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck source=scripts/lib-env.sh
source scripts/lib-env.sh
load_env .env
dir="${1:?usage: scripts/restore.sh <backup-dir> --yes-replace-current-data}"
[[ "${2:-}" == "--yes-replace-current-data" ]] || { echo "refusing without --yes-replace-current-data" >&2; exit 2; }
[[ -f "$dir/manifest.json" && -f "$dir/db.dump" ]] || { echo "not a backup directory: $dir" >&2; exit 2; }
[[ "$(sha256sum "$dir/db.dump" | cut -d' ' -f1)" == "$(python3 -c "import json; print(json.load(open('$dir/manifest.json'))['db_dump_sha256'])")" ]] \
  || { echo "dump checksum does not match manifest" >&2; exit 1; }

echo "stopping api and worker"
docker compose stop api worker
# Recreate the database empty: pg_restore --clean only drops objects present
# in the dump, so objects from newer migrations would otherwise survive.
db_user="${POSTGRES_USER:-careerpilot}" db_name="${POSTGRES_DB:-careerpilot}"
echo "recreating database $db_name"
docker compose exec -T postgres dropdb -U "$db_user" --force --if-exists "$db_name"
docker compose exec -T postgres createdb -U "$db_user" "$db_name"
echo "restoring database"
docker compose exec -T postgres pg_restore -U "$db_user" -d "$db_name" --no-owner --single-transaction --exit-on-error <"$dir/db.dump"
echo "restoring objects"
net="$(docker compose ps -q storage | xargs docker inspect --format '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}}')"
aws() {
  docker run --rm --network "$net" -e HOME=/tmp \
    -e AWS_ACCESS_KEY_ID="$S3_ACCESS_KEY" -e AWS_SECRET_ACCESS_KEY="$S3_SECRET_KEY" -e AWS_DEFAULT_REGION="${S3_REGION:-us-east-1}" \
    -v "$PWD/$dir/objects:/objects:ro" amazon/aws-cli:2.31.39 --endpoint-url http://storage:8333 "$@"
}
# A disaster-recovery target may not have the bucket yet.
aws s3api head-bucket --bucket "$S3_BUCKET" >/dev/null 2>&1 || aws s3 mb "s3://${S3_BUCKET}"
aws s3 sync /objects "s3://${S3_BUCKET}" --only-show-errors
echo "applying migrations"
docker compose run --rm --no-deps api migrate up
echo "starting api and worker"
docker compose start api worker
api="$(docker compose ps -q api)"
for _ in $(seq 1 45); do
  if [[ "$(docker inspect --format '{{.State.Health.Status}}' "$api")" == "healthy" ]]; then echo "restore complete: API healthy"; exit 0; fi
  sleep 2
done
echo "API did not become ready; check: docker compose logs api" >&2
exit 1
