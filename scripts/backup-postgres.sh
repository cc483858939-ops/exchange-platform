#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
COMPOSE_FILE="${COMPOSE_FILE:-$REPO_ROOT/deploy/compose.prod.yml}"
ENV_FILE="${ENV_FILE:-$REPO_ROOT/deploy/.env}"
BACKUP_DIR="${BACKUP_DIR:-$REPO_ROOT/backups/postgres}"

if [[ ! -f "$COMPOSE_FILE" ]]; then
  printf 'Compose file not found: %s\n' "$COMPOSE_FILE" >&2
  exit 1
fi
if [[ ! -f "$ENV_FILE" ]]; then
  printf 'Production env file not found: %s\nCopy deploy/.env.example to deploy/.env first.\n' "$ENV_FILE" >&2
  exit 1
fi

mkdir -p -- "$BACKUP_DIR"

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup_path="$BACKUP_DIR/goexchange_${timestamp}.dump"
partial_path="${backup_path}.partial"
cleanup() {
  rm -f -- "$partial_path"
}
trap cleanup EXIT

compose=(docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE")

"${compose[@]}" exec -T db sh -c \
  'PGPASSWORD="$POSTGRES_PASSWORD" pg_dump --format=custom --no-owner --no-privileges -h 127.0.0.1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' \
  >"$partial_path"

if [[ ! -s "$partial_path" ]]; then
  printf 'PostgreSQL backup was empty: %s\n' "$partial_path" >&2
  exit 1
fi

mv -- "$partial_path" "$backup_path"
trap - EXIT
printf 'PostgreSQL backup: %s\n' "$backup_path"
