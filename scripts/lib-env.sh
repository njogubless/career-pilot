# shellcheck shell=bash
# load_env FILE: exports KEY=VALUE pairs from a .env file without overriding
# variables already set in the environment (the same precedence as Docker
# Compose). Inline "# comments" and surrounding quotes are stripped.
load_env() {
  local file="${1:-.env}" line key val
  [[ -f "$file" ]] || return 0
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ "$line" =~ ^[[:space:]]*([A-Za-z_][A-Za-z0-9_]*)=(.*)$ ]] || continue
    key="${BASH_REMATCH[1]}"
    val="${BASH_REMATCH[2]}"
    val="${val%%[[:space:]]#*}"
    val="${val%"${val##*[![:space:]]}"}"
    [[ "$val" =~ ^\"(.*)\"$ || "$val" =~ ^\'(.*)\'$ ]] && val="${BASH_REMATCH[1]}"
    [[ -n "${!key+x}" ]] || export "$key=$val"
  done <"$file"
}
