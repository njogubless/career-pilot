#!/usr/bin/env bash
# Verifies deploy/postgres/roles.sql in a throwaway Postgres (ADR-026):
#   roles.sql → migrate as careerpilot_owner → API and worker as careerpilot_app
# and checks what the application role can and cannot do.
#
#   scripts/check-db-roles.sh            (builds the API image first)
set -euo pipefail
cd "$(dirname "$0")/.."
pg_image="pgvector/pgvector:pg16"
api_image="careerpilot-rolecheck:local"
name="careerpilot-rolecheck-$$"
net="$name-net"
fail=0
check() { if eval "$2"; then echo "PASS  $1"; else echo "FAIL  $1"; fail=1; fi; }
cleanup() { docker rm -f "$name" "$name-api" "$name-worker" >/dev/null 2>&1 || true; docker network rm "$net" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker build -q -t "$api_image" services/api >/dev/null
docker network create "$net" >/dev/null
docker run -d --name "$name" --network "$net" -e POSTGRES_PASSWORD=admin -e POSTGRES_DB=careerpilot "$pg_image" >/dev/null
for _ in $(seq 1 60); do docker exec "$name" pg_isready -U postgres -d careerpilot >/dev/null 2>&1 && break; sleep 1; done
sleep 2
for _ in $(seq 1 30); do docker exec "$name" pg_isready -U postgres -d careerpilot >/dev/null 2>&1 && break; sleep 1; done
docker exec "$name" psql -q -U postgres -d careerpilot -c "CREATE EXTENSION IF NOT EXISTS vector; CREATE EXTENSION IF NOT EXISTS citext;" >/dev/null

admin() { docker exec -i "$name" psql -q -U postgres -d careerpilot "$@"; }
as_app() { docker exec -e PGPASSWORD=app-pw "$name" psql -q -h 127.0.0.1 -U careerpilot_app -d careerpilot -tAc "$1" 2>&1; }

admin -v owner_password="'owner-pw'" -v app_password="'app-pw'" <deploy/postgres/roles.sql
echo "PASS  roles.sql applied to an empty database"
# shellcheck disable=SC2034 # used inside check expressions
owner_url="postgres://careerpilot_owner:owner-pw@$name:5432/careerpilot?sslmode=disable"
app_url="postgres://careerpilot_app:app-pw@$name:5432/careerpilot?sslmode=disable"
check "migrations run as careerpilot_owner" 'docker run --rm --network "$net" -e DATABASE_URL="$owner_url" "$api_image" migrate up >/dev/null 2>&1'

check "app can read" '[[ "$(as_app "SELECT count(*) FROM users")" == "0" ]]'
insert_audit="INSERT INTO audit_events (id, actor, action, outcome, metadata, ip) VALUES (gen_random_uuid(), 'SYSTEM', 'roles.check', 'SUCCESS', '{\"k\": 1}', '192.0.2.1') RETURNING 1"
# shellcheck disable=SC2034 # used inside check expressions
update_audit="UPDATE audit_events SET action = 'tampered'"
# shellcheck disable=SC2034 # used inside check expressions
appended="$(as_app "$insert_audit")"
check "app can append audit events" '[[ "$appended" == "1" ]]'
check "app cannot update audit events" '[[ "$(as_app "$update_audit")" == *"permission denied"* ]]'
check "app cannot delete audit events" '[[ "$(as_app "DELETE FROM audit_events")" == *"permission denied"* ]]'
check "app can pseudonymise via audit_forget_user only" '[[ "$(as_app "SELECT audit_forget_user(gen_random_uuid())")" == "0" ]]'
check "app cannot create tables" '[[ "$(as_app "CREATE TABLE evil (x int)")" == *"permission denied"* ]]'
check "app cannot drop tables" '[[ "$(as_app "DROP TABLE users")" == *"must be owner"* ]]'

# The real API and worker run as the application role.
docker run -d --name "$name-api" --network "$net" -e DATABASE_URL="$app_url" -e MIGRATE_ON_START=false \
  -e APP_BASE_URL=http://localhost "$api_image" >/dev/null
docker run -d --name "$name-worker" --network "$net" -e DATABASE_URL="$app_url" -e MIGRATE_ON_START=false \
  -e APP_BASE_URL=http://localhost -e DISCOVERY_INTERVAL=0 "$api_image" worker >/dev/null
sleep 8
reg="$(docker run --rm --network "$net" curlimages/curl:8.11.0 -s -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
  -d '{"email":"roles@example.com","password":"a-strong-passphrase","display_name":"Roles"}' "http://$name-api:8080/api/v1/auth/register")"
check "API registers a user as careerpilot_app (HTTP $reg)" '[[ "$reg" == "201" ]]'
check "worker runs as careerpilot_app (River leader, periodic jobs)" '[[ "$(docker inspect -f "{{.State.Running}}" "$name-worker")" == "true" ]] && ! docker logs "$name-worker" 2>&1 | grep -qi "permission denied"'
check "no permission errors in API logs" '! docker logs "$name-api" 2>&1 | grep -qi "permission denied"'

if [[ "$fail" == "0" ]]; then echo "DATABASE ROLES CHECK PASSED"; else echo "DATABASE ROLES CHECK FAILED"; docker logs --tail 20 "$name-worker" 2>&1 | tail -5; fi
exit "$fail"
