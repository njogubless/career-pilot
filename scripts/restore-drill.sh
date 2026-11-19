#!/usr/bin/env bash
# Restore drill: proves a backup can be restored, without touching live data.
#
#   scripts/restore-drill.sh <backup-dir>
#
# Starts a throwaway Postgres (pgvector) on an isolated network, restores
# db.dump into it, and checks:
#   1. the dump checksum matches the manifest;
#   2. pg_restore completes without errors;
#   3. the schema version equals the manifest's, and this build's
#      `careerpilot migrate status` accepts it (the app can run on it);
#   4. row counts match the manifest (lower bound: taken after the dump);
#   5. foreign keys hold (no user-owned rows without their user);
#   6. every profile_documents storage key has its object in objects/.
# Exits non-zero on any failure. The scratch container is always removed.
set -euo pipefail
cd "$(dirname "$0")/.."
dir="${1:?usage: scripts/restore-drill.sh <backup-dir>}"
[[ -f "$dir/manifest.json" && -f "$dir/db.dump" ]] || { echo "not a backup directory: $dir" >&2; exit 2; }
pg_image="pgvector/pgvector:pg16"
api_image="${API_IMAGE:-careerpilot-api}"
name="careerpilot-restore-drill-$$"
net="$name-net"
pw="$(head -c 24 /dev/urandom | base64 | tr -d '=+/')"
fail=0
check() { if eval "$2"; then echo "PASS  $1"; else echo "FAIL  $1"; fail=1; fi; }
manifest() { python3 -c "import json,sys; d=json.load(open('$dir/manifest.json')); print(eval(sys.argv[1], {}, {'d': d}))" "$1"; }

cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; docker network rm "$net" >/dev/null 2>&1 || true; }
trap cleanup EXIT

check "dump checksum matches manifest" '[[ "$(sha256sum "$dir/db.dump" | cut -d" " -f1)" == "$(manifest "d[\"db_dump_sha256\"]")" ]]'

docker network create --internal "$net" >/dev/null
docker run -d --name "$name" --network "$net" -e POSTGRES_PASSWORD="$pw" -e POSTGRES_USER=careerpilot -e POSTGRES_DB=careerpilot "$pg_image" >/dev/null
for _ in $(seq 1 60); do docker exec "$name" pg_isready -U careerpilot -d careerpilot >/dev/null 2>&1 && break; sleep 1; done
sleep 2 # the image restarts once after initialisation
for _ in $(seq 1 30); do docker exec "$name" pg_isready -U careerpilot -d careerpilot >/dev/null 2>&1 && break; sleep 1; done

q() { docker exec "$name" psql -U careerpilot -d careerpilot -tAc "$1"; }
start=$(date +%s)
if docker exec -i "$name" pg_restore -U careerpilot -d careerpilot --no-owner --exit-on-error <"$dir/db.dump"; then
  echo "PASS  pg_restore completed in $(( $(date +%s) - start ))s"
else
  echo "FAIL  pg_restore"; exit 1
fi

schema="$(q "SELECT max(version_id) FROM goose_db_version WHERE is_applied")"
check "schema version $schema equals manifest" '[[ "$schema" == "$(manifest "d[\"schema_version\"]")" ]]'
if docker image inspect "$api_image" >/dev/null 2>&1; then
  status="$(docker run --rm --network "$net" -e DATABASE_URL="postgres://careerpilot:$pw@$name:5432/careerpilot?sslmode=disable" "$api_image" migrate status 2>&1 || true)"
  check "this build accepts the restored schema ($status)" '[[ "$status" =~ current=([0-9]+)\ latest=([0-9]+) && ${BASH_REMATCH[1]} -le ${BASH_REMATCH[2]} && ${BASH_REMATCH[1]} -gt 0 ]]'
else
  echo "SKIP  app schema check (image $api_image not built; set API_IMAGE)"
fi

for t in $(manifest "' '.join(d['row_counts'])"); do
  want="$(manifest "d['row_counts']['$t']")"
  got="$(q "SELECT count(*) FROM $t")"
  check "$t rows: $got (manifest $want)" '[[ "$got" -ge 0 && ( "$got" -eq "$want" || ( "$t" =~ ^(audit_events|jobs)$ && "$got" -le "$want" ) ) ]]'
done

# shellcheck disable=SC2034 # used inside the check expression
orphans="$(q "SELECT (SELECT count(*) FROM professional_profiles p LEFT JOIN users u ON u.id = p.user_id WHERE u.id IS NULL)
  + (SELECT count(*) FROM applications a LEFT JOIN users u ON u.id = a.user_id WHERE u.id IS NULL)
  + (SELECT count(*) FROM profile_documents d LEFT JOIN users u ON u.id = d.user_id WHERE u.id IS NULL)")"
check "no user-owned rows without their user" '[[ "$orphans" == "0" ]]'

missing=0
while read -r key; do
  [[ -z "$key" ]] && continue
  [[ -f "$dir/objects/$key" ]] || { missing=$((missing + 1)); echo "      missing object: $key"; }
done < <(q "SELECT storage_key FROM profile_documents")
docs="$(q "SELECT count(*) FROM profile_documents")"
check "all $docs document objects present in backup" '[[ "$missing" == "0" ]]'

if [[ "$fail" == "0" ]]; then echo "RESTORE DRILL PASSED"; else echo "RESTORE DRILL FAILED"; fi
exit "$fail"
