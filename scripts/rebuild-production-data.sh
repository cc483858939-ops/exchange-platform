#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" != "RESET-PRODUCTION" ]]; then
  printf 'Refusing destructive rebuild. Re-run with the exact confirmation argument: RESET-PRODUCTION\n' >&2
  exit 2
fi

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
COMPOSE_FILE="${COMPOSE_FILE:-$REPO_ROOT/deploy/compose.prod.yml}"
ENV_FILE="${ENV_FILE:-$REPO_ROOT/deploy/.env}"
SOURCE="${DEVDATA_SOURCE:-rsshub}"

if [[ ! -f "$COMPOSE_FILE" ]]; then
  printf 'Compose file not found: %s\n' "$COMPOSE_FILE" >&2
  exit 1
fi
if [[ ! -f "$ENV_FILE" ]]; then
  printf 'Production env file not found: %s\nCopy deploy/.env.example to deploy/.env first.\n' "$ENV_FILE" >&2
  exit 1
fi

compose=(docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE")

printf 'Stopping API and worker before rebuilding derived DevData state...\n'
"${compose[@]}" stop api worker

printf 'Clearing only the persistent DevData snapshot/checkpoint volume...\n'
"${compose[@]}" run --rm --no-deps --entrypoint /bin/sh devdata -c \
  'find /app/.devdata -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +'

printf 'Re-running the guarded database and eventing initialization steps...\n'
"${compose[@]}" run --rm outbox-cutover
"${compose[@]}" run --rm migrate
"${compose[@]}" run --rm kafka-init
"${compose[@]}" run --rm cdc-init

printf 'Rebuilding DevData from source=%s...\n' "$SOURCE"
"${compose[@]}" run --rm devdata refresh \
  --source="$SOURCE" \
  --allow-destructive \
  --reset-checkpoint

printf 'Starting API and worker...\n'
"${compose[@]}" up -d api worker

printf 'Waiting for API readiness...\n'
for ((attempt = 1; attempt <= 60; attempt++)); do
  if "${compose[@]}" exec -T api curl -fsS http://127.0.0.1:3000/readyz >/dev/null; then
    printf 'API is ready. Production derived-data rebuild completed.\n'
    exit 0
  fi
  sleep 2
done

printf 'API did not become ready within 120 seconds. Inspect: docker compose logs api worker\n' >&2
exit 1
