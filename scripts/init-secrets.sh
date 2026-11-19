#!/usr/bin/env bash
# Creates the secret files used by deploy/compose.prod.yml (Docker secrets).
#
#   scripts/init-secrets.sh [secrets-dir]   (default: deploy/secrets)
#
# Generates random values for internal secrets and creates empty files for
# credentials you obtain elsewhere (Anthropic API key, SMTP password, Google
# OAuth client secret): fill those in. Existing files are never overwritten,
# so re-running is safe. The directory is 0700 and git-ignored; files are
# 0644 because Compose bind-mounts them with host permissions and the
# containers run as unprivileged users with other uids. Only the owner can
# reach them on the host (the directory permission).
# In a managed environment, put the same values in your secret manager and
# mount them at the same paths instead.
set -euo pipefail
dir="${1:-$(dirname "$0")/../deploy/secrets}"
mkdir -p "$dir"
chmod 700 "$dir"
umask 022

rand() { head -c "$1" /dev/urandom | base64 | tr -d '\n=+/' | head -c "$2"; }

write() { # name value
  if [[ -e "$dir/$1" ]]; then
    echo "kept     $1"
  else
    printf '%s' "$2" >"$dir/$1"
    echo "created  $1"
  fi
}

pg_password="$(rand 48 40)"
if [[ -e "$dir/postgres_password" ]]; then pg_password="$(cat "$dir/postgres_password")"; fi
write postgres_password "$pg_password"
write database_url "postgres://${POSTGRES_USER:-careerpilot}:${pg_password}@postgres:5432/${POSTGRES_DB:-careerpilot}?sslmode=disable"
write session_secret "$(rand 64 48)"
write data_encryption_key "$(head -c 32 /dev/urandom | base64 | tr -d '\n')"
write ai_service_token "$(rand 64 48)"
write anthropic_api_key ""
write smtp_password ""
write google_oauth_client_secret ""
chmod 644 "$dir"/*
echo "secrets in $dir: fill in anthropic_api_key, smtp_password and google_oauth_client_secret as needed."
echo "Back up data_encryption_key securely: losing it means users must reconnect Gmail."
