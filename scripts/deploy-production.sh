#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
COMPOSE_FILE="${COMPOSE_FILE:-$REPO_ROOT/deploy/compose.prod.yml}"
ENV_FILE="${ENV_FILE:-$REPO_ROOT/deploy/.env}"
LOCK_FILE="/tmp/exchange-platform-production-deploy.lock"

MODE="${1:-}"
if [[ $# -ne 1 || ( "$MODE" != "CHECK" && "$MODE" != "DEPLOY-PRODUCTION" ) ]]; then
  printf 'Usage: bash scripts/deploy-production.sh CHECK|DEPLOY-PRODUCTION\n' >&2
  exit 2
fi

compose=(docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE")

stage="startup"
old_head="unknown"
target_head="unknown"
database_backup="none"
failure_reported=0
backend_changed="no"
caddy_changed="no"
compose_changed="no"
api_ready="no"
worker_ready="no"
public_health="skipped"

on_unexpected_error() {
  local exit_code=$?
  if [[ "$failure_reported" != "1" ]]; then
    printf 'Deployment failed\n'
    printf 'stage=%s\n' "$stage"
    printf 'from_commit=%s\n' "$old_head"
    printf 'to_commit=%s\n' "$target_head"
    printf 'database_backup=%s\n' "$database_backup"
    printf 'inspect_logs=docker compose --env-file deploy/.env -f deploy/compose.prod.yml logs --tail=100 api worker caddy\n' >&2
  fi
  exit "$exit_code"
}
trap on_unexpected_error ERR

fail_stage() {
  local message=$1
  failure_reported=1
  printf 'Deployment failed\n' >&2
  printf 'stage=%s\n' "$stage" >&2
  printf 'message=%s\n' "$message" >&2
  printf 'from_commit=%s\n' "$old_head" >&2
  printf 'to_commit=%s\n' "$target_head" >&2
  printf 'database_backup=%s\n' "$database_backup" >&2
  printf 'inspect_logs=docker compose --env-file deploy/.env -f deploy/compose.prod.yml logs --tail=100 api worker caddy\n' >&2
  exit 1
}

acquire_lock() {
  stage="deployment lock"
  exec {lock_fd}>"$LOCK_FILE" || fail_stage "cannot open deployment lock"
  flock -n "$lock_fd" || fail_stage "another deployment is already running"
}

check_preconditions() {
  stage="preconditions"

  [[ -f "$COMPOSE_FILE" ]] || fail_stage "Compose file not found"
  [[ -f "$ENV_FILE" ]] || fail_stage "production env file not found"

  local env_mode
  env_mode="$(stat -c '%a' "$ENV_FILE" 2>/dev/null)" || fail_stage "cannot inspect production env permissions"
  local env_mode_value=$((0${env_mode}))
  (( (env_mode_value & 077) == 0 )) || fail_stage "production env permissions are more permissive than 0600"

  git -C "$REPO_ROOT" rev-parse --show-toplevel >/dev/null 2>&1 || \
    fail_stage "repository check failed"

  local branch
  branch="$(git -C "$REPO_ROOT" branch --show-current)" || fail_stage "cannot read current branch"
  [[ "$branch" == "main" ]] || fail_stage "current branch is not main"

  local tracked_dirty
  tracked_dirty="$(git -C "$REPO_ROOT" status --porcelain --untracked-files=no)" || \
    fail_stage "cannot inspect Git working tree"
  if [[ -n "$tracked_dirty" ]]; then
    printf 'Tracked dirty files:\n' >&2
    git -C "$REPO_ROOT" status --porcelain --untracked-files=no \
      | sed -E 's/^.. //' >&2
    fail_stage "tracked working tree is not clean"
  fi

  stage="Compose static validation"
  "${compose[@]}" config --quiet >/dev/null 2>&1 || \
    fail_stage "Compose config validation failed"
}

fetch_origin_main() {
  stage="git fetch origin main"
  git -C "$REPO_ROOT" fetch origin main >/dev/null 2>&1 || \
    fail_stage "git fetch origin main failed"
}

read_current_head() {
  stage="reading current Git head"
  old_head="$(git -C "$REPO_ROOT" rev-parse HEAD)" || fail_stage "cannot read current HEAD"
}

read_target_head() {
  stage="reading origin/main"
  target_head="$(git -C "$REPO_ROOT" rev-parse origin/main)" || \
    fail_stage "cannot read origin/main"
}

check_fast_forward() {
  stage="fast-forward ancestry check"
  git -C "$REPO_ROOT" merge-base --is-ancestor "$old_head" "$target_head" || \
    fail_stage "current HEAD is not an ancestor of origin/main"
}

classify_changes() {
  local changed_paths_text path
  stage="classifying Git changes"
  changed_paths_text="$(git -C "$REPO_ROOT" diff --name-only "$old_head..$target_head")" || \
    fail_stage "cannot list changes between commits"

  backend_changed="no"
  caddy_changed="no"
  compose_changed="no"
  while IFS= read -r path; do
    [[ -z "$path" ]] && continue
    case "$path" in
      Go.exchange/*) backend_changed="yes" ;;
      deploy/Caddyfile) caddy_changed="yes" ;;
      deploy/compose.prod.yml) compose_changed="yes" ;;
    esac
  done <<< "$changed_paths_text"

  printf 'changed_paths:\n'
  if [[ -n "$changed_paths_text" ]]; then
    printf '%s\n' "$changed_paths_text"
  else
    printf '(none)\n'
  fi
  printf 'backend_changed=%s\n' "$backend_changed"
  printf 'caddy_changed=%s\n' "$caddy_changed"
  printf 'compose_changed=%s\n' "$compose_changed"
}

read_env_value() {
  local key=$1 line value
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ "$line" == "$key="* ]] || continue
    value="${line#"$key="}"
    value="${value%$'\r'}"
    if [[ "$value" == '"'*'"' && ${#value} -ge 2 ]]; then
      value="${value:1:${#value}-2}"
    elif [[ ${#value} -ge 2 && "${value:0:1}" == "'" && "${value: -1}" == "'" ]]; then
      value="${value:1:${#value}-2}"
    fi
    printf '%s' "$value"
    return 0
  done < "$ENV_FILE"
  return 1
}

wait_internal_readiness() {
  local attempt api_ok worker_ok
  stage="internal readiness"
  for ((attempt = 1; attempt <= 60; attempt++)); do
    api_ok=0
    worker_ok=0
    if "${compose[@]}" exec -T api curl -fsS -o /dev/null \
      http://127.0.0.1:3000/readyz >/dev/null 2>&1; then
      api_ok=1
    fi
    if "${compose[@]}" exec -T worker curl -fsS -o /dev/null \
      http://127.0.0.1:8081/readyz >/dev/null 2>&1; then
      worker_ok=1
    fi
    if (( api_ok == 1 && worker_ok == 1 )); then
      api_ready="yes"
      worker_ready="yes"
      return 0
    fi
    sleep 2
  done
  fail_stage "API and Worker readiness did not succeed within 120 seconds"
}

check_public_health() {
  local api_domain=""
  if ! api_domain="$(read_env_value API_DOMAIN 2>/dev/null)"; then
    public_health="skipped"
    return 0
  fi
  if [[ -z "$api_domain" ]]; then
    public_health="skipped"
    return 0
  fi

  stage="public health"
  curl -fsS --max-time 10 -o /dev/null "https://${api_domain}/healthz" >/dev/null 2>&1 || \
    fail_stage "public health check failed"
  curl -fsS --max-time 10 -o /dev/null "https://${api_domain}/readyz" >/dev/null 2>&1 || \
    fail_stage "public readiness check failed"
  public_health="yes"
}

create_database_backup() {
  local backup_log
  stage="PostgreSQL backup"
  backup_log="$(mktemp)" || fail_stage "cannot create temporary backup log"
  if ! bash "$REPO_ROOT/scripts/backup-postgres.sh" >"$backup_log" 2>&1; then
    rm -f -- "$backup_log"
    fail_stage "PostgreSQL backup failed"
  fi
  database_backup="$(awk -F': ' '/^PostgreSQL backup: / {print substr($0, index($0, ": ") + 2); exit}' "$backup_log")"
  rm -f -- "$backup_log"
  [[ -n "$database_backup" ]] || fail_stage "backup completed without a reported path"
}

run_initialization() {
  local service=$1
  stage="initialization: $service"
  "${compose[@]}" run --rm "$service" || fail_stage "$service failed"
}

print_success() {
  printf 'Deployment succeeded\n'
  printf 'from_commit=%s\n' "${old_head:0:8}"
  printf 'to_commit=%s\n' "${target_head:0:8}"
  printf 'backend_changed=%s\n' "$backend_changed"
  printf 'caddy_changed=%s\n' "$caddy_changed"
  printf 'compose_changed=%s\n' "$compose_changed"
  printf 'database_backup=%s\n' "$database_backup"
  printf 'api_ready=%s\n' "$api_ready"
  printf 'worker_ready=%s\n' "$worker_ready"
  printf 'public_health=%s\n' "$public_health"
  if [[ "$compose_changed" == "yes" ]]; then
    printf 'Infrastructure Compose changes require separate manual apply.\n'
  fi
}

acquire_lock
check_preconditions

if [[ "$MODE" == "CHECK" ]]; then
  read_current_head
  fetch_origin_main
  read_target_head
  check_fast_forward
  classify_changes
  printf 'current_commit=%s\n' "$old_head"
  printf 'target_commit=%s\n' "$target_head"
  printf 'ahead_commits=%s\n' "$(git -C "$REPO_ROOT" rev-list --count "$old_head..$target_head")"
  printf 'CHECK succeeded\n'
  exit 0
fi

read_current_head
fetch_origin_main
read_target_head
check_fast_forward

if [[ "$old_head" == "$target_head" ]]; then
  wait_internal_readiness
  check_public_health
  print_success
  printf 'No-op: production is already at origin/main.\n'
  exit 0
fi

classify_changes

stage="git fast-forward"
git -C "$REPO_ROOT" merge --ff-only origin/main >/dev/null 2>&1 || \
  fail_stage "git fast-forward failed"
[[ "$(git -C "$REPO_ROOT" rev-parse HEAD)" == "$target_head" ]] || \
  fail_stage "HEAD after fast-forward does not match origin/main"

stage="post-merge Compose static validation"
"${compose[@]}" config --quiet >/dev/null 2>&1 || \
  fail_stage "post-merge Compose config validation failed"

if [[ "$compose_changed" == "yes" && "${ALLOW_PRODUCTION_COMPOSE_CHANGE:-0}" != "1" ]]; then
  failure_reported=1
  printf 'Production Compose changed; runtime deployment requires explicit ALLOW_PRODUCTION_COMPOSE_CHANGE=1\n' >&2
  printf 'Infrastructure Compose changes require separate manual apply.\n' >&2
  printf 'from_commit=%s\n' "${old_head:0:8}" >&2
  printf 'to_commit=%s\n' "${target_head:0:8}" >&2
  exit 2
fi

if [[ "$backend_changed" == "no" && "$caddy_changed" == "no" && "$compose_changed" == "no" ]]; then
  printf 'No VPS runtime deployment required.\n'
  printf 'from_commit=%s\n' "${old_head:0:8}"
  printf 'to_commit=%s\n' "${target_head:0:8}"
  exit 0
fi

if [[ "$backend_changed" == "yes" || "$compose_changed" == "yes" ]]; then
  stage="backend image build"
  "${compose[@]}" build api || fail_stage "backend image build failed"

  create_database_backup
  run_initialization outbox-cutover
  run_initialization migrate
  run_initialization kafka-init
  run_initialization cdc-init

  stage="API and Worker update"
  "${compose[@]}" up -d --no-deps --force-recreate api worker || \
    fail_stage "API and Worker update failed"
fi

if [[ "$caddy_changed" == "yes" || "$compose_changed" == "yes" ]]; then
  stage="Caddy update"
  "${compose[@]}" up -d --no-deps --force-recreate caddy || \
    fail_stage "Caddy update failed"
fi

wait_internal_readiness
check_public_health
print_success
