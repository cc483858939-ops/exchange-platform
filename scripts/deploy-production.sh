#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
COMPOSE_FILE="${COMPOSE_FILE:-$REPO_ROOT/deploy/compose.prod.yml}"
ENV_FILE="${ENV_FILE:-$REPO_ROOT/deploy/.env}"
STATE_DIR="${STATE_DIR:-$REPO_ROOT/.deploy-state}"
STATE_FILE="$STATE_DIR/last-successful-commit"
LOCK_FILE="/tmp/exchange-platform-production-deploy.lock"

MODE="${1:-}"
if [[ $# -ne 1 || ( "$MODE" != "CHECK" && "$MODE" != "DEPLOY-PRODUCTION" ) ]]; then
  printf 'Usage: bash scripts/deploy-production.sh CHECK|DEPLOY-PRODUCTION\n' >&2
  exit 2
fi

compose=(docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE")

stage="startup"
git_head="unknown"
deployed_head=""
deployment_from_head=""
target_head="unknown"
database_backup="none"
failure_reported=0
backend_runtime_changed="no"
devdata_registry_changed="no"
caddy_changed="no"
compose_changed="no"
api_ready="skipped"
worker_ready="skipped"
public_health="skipped"

on_unexpected_error() {
  local exit_code=$?
  if [[ "$failure_reported" != "1" ]]; then
    printf 'Deployment failed\n' >&2
    printf 'stage=%s\n' "$stage" >&2
    printf 'git_commit=%s\n' "$git_head" >&2
    printf 'deployed_commit=%s\n' "${deployed_head:-uninitialized}" >&2
    printf 'target_commit=%s\n' "$target_head" >&2
    printf 'database_backup=%s\n' "$database_backup" >&2
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
  printf 'git_commit=%s\n' "$git_head" >&2
  printf 'deployed_commit=%s\n' "${deployed_head:-uninitialized}" >&2
  printf 'target_commit=%s\n' "$target_head" >&2
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
  (( (env_mode_value & 0177) == 0 )) || fail_stage "production env permissions are more permissive than 0600"

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
  git_head="$(git -C "$REPO_ROOT" rev-parse HEAD)" || fail_stage "cannot read current HEAD"
}

read_target_head() {
  stage="reading origin/main"
  target_head="$(git -C "$REPO_ROOT" rev-parse origin/main)" || \
    fail_stage "cannot read origin/main"
}

ensure_state_dir() {
  stage="deployment state directory"
  mkdir -p -- "$STATE_DIR" || fail_stage "cannot create deployment state directory"
  chmod 700 -- "$STATE_DIR" || fail_stage "cannot secure deployment state directory"
  local state_dir_mode
  state_dir_mode="$(stat -c '%a' "$STATE_DIR" 2>/dev/null)" || \
    fail_stage "cannot inspect deployment state directory permissions"
  [[ "$state_dir_mode" == "700" ]] || fail_stage "deployment state directory is not 0700"
}

write_state_atomic() {
  local commit=$1 tmp_path
  [[ "$commit" =~ ^[0-9a-fA-F]{40}$ ]] || fail_stage "refusing invalid deployment state commit"
  ensure_state_dir

  tmp_path="$(mktemp "$STATE_DIR/.last-successful-commit.XXXXXX")" || \
    fail_stage "cannot create temporary deployment state file"
  if ! chmod 600 -- "$tmp_path"; then
    rm -f -- "$tmp_path"
    fail_stage "cannot secure temporary deployment state file"
  fi
  if ! printf '%s\n' "$commit" >"$tmp_path"; then
    rm -f -- "$tmp_path"
    fail_stage "cannot write deployment state file"
  fi
  if ! mv -f -- "$tmp_path" "$STATE_FILE"; then
    rm -f -- "$tmp_path"
    fail_stage "cannot atomically replace deployment state file"
  fi
}

read_deployed_state() {
  deployed_head=""
  if [[ ! -e "$STATE_FILE" ]]; then
    return 0
  fi
  [[ -f "$STATE_FILE" ]] || fail_stage "deployment state path is not a regular file"

  local -a state_lines=()
  mapfile -t state_lines <"$STATE_FILE" || fail_stage "cannot read deployment state file"
  ((${#state_lines[@]} == 1)) || fail_stage "deployment state file must contain one commit"

  local state_value="${state_lines[0]}"
  [[ "$state_value" =~ ^[0-9a-fA-F]{40}$ ]] || \
    fail_stage "deployment state file contains an invalid commit"
  if ! deployed_head="$(git -C "$REPO_ROOT" rev-parse --verify "${state_value}^{commit}" 2>/dev/null)"; then
    fail_stage "deployment state commit is not present in this repository"
  fi
}

check_state_permissions_readonly() {
  local state_dir_mode state_file_mode
  stage="deployment state permissions"
  if [[ -e "$STATE_DIR" ]]; then
    [[ -d "$STATE_DIR" ]] || fail_stage "deployment state directory path is not a directory"
    state_dir_mode="$(stat -c '%a' "$STATE_DIR" 2>/dev/null)" || \
      fail_stage "cannot inspect deployment state directory permissions"
    [[ "$state_dir_mode" == "700" ]] || fail_stage "deployment state directory is not 0700"
  fi
  if [[ -e "$STATE_FILE" ]]; then
    [[ -f "$STATE_FILE" ]] || fail_stage "deployment state path is not a regular file"
    state_file_mode="$(stat -c '%a' "$STATE_FILE" 2>/dev/null)" || \
      fail_stage "cannot inspect deployment state file permissions"
    [[ "$state_file_mode" == "600" ]] || fail_stage "deployment state file is not 0600"
  fi
}

prepare_deploy_state() {
  ensure_state_dir
  if [[ -e "$STATE_FILE" ]]; then
    [[ -f "$STATE_FILE" ]] || fail_stage "deployment state path is not a regular file"
    chmod 600 -- "$STATE_FILE" || fail_stage "cannot secure deployment state file"
  else
    write_state_atomic "$git_head"
  fi
  read_deployed_state
}

check_fast_forward() {
  stage="fast-forward ancestry check"
  git -C "$REPO_ROOT" merge-base --is-ancestor "$git_head" "$target_head" || \
    fail_stage "current HEAD is not an ancestor of origin/main"
}

check_deployed_ancestry() {
  stage="deployment state ancestry check"
  if [[ -n "$deployed_head" ]]; then
    git -C "$REPO_ROOT" merge-base --is-ancestor "$deployed_head" "$git_head" || \
      fail_stage "deployed state is not an ancestor of current HEAD"
    git -C "$REPO_ROOT" merge-base --is-ancestor "$deployed_head" "$target_head" || \
      fail_stage "deployed state is not an ancestor of origin/main"
  fi
}

classify_changes() {
  local changed_paths_text path classification_base
  stage="classifying Git changes"
  classification_base="${deployed_head:-$git_head}"
  changed_paths_text="$(git -C "$REPO_ROOT" diff --name-only "$classification_base..$target_head")" || \
    fail_stage "cannot list changes between deployment state and target"

  backend_runtime_changed="no"
  devdata_registry_changed="no"
  caddy_changed="no"
  compose_changed="no"
  while IFS= read -r path; do
    [[ -z "$path" ]] && continue
    case "$path" in
      Go.exchange/config/sources/x_sources.json) devdata_registry_changed="yes" ;;
      Go.exchange/*) backend_runtime_changed="yes" ;;
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
  printf 'backend_runtime_changed=%s\n' "$backend_runtime_changed"
  printf 'devdata_registry_changed=%s\n' "$devdata_registry_changed"
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
  printf 'from_commit=%s\n' "${deployment_from_head:0:8}"
  printf 'to_commit=%s\n' "${target_head:0:8}"
  printf 'backend_runtime_changed=%s\n' "$backend_runtime_changed"
  printf 'devdata_registry_changed=%s\n' "$devdata_registry_changed"
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
  check_state_permissions_readonly
  read_deployed_state
  fetch_origin_main
  read_target_head
  check_fast_forward
  check_deployed_ancestry
  classify_changes
  printf 'git_commit=%s\n' "$git_head"
  printf 'target_commit=%s\n' "$target_head"
  printf 'deployed_commit=%s\n' "${deployed_head:-uninitialized}"
  printf 'ahead_commits=%s\n' "$(git -C "$REPO_ROOT" rev-list --count "$git_head..$target_head")"
  if [[ -z "$deployed_head" ]]; then
    printf 'deployment_status=uninitialized\n'
  elif [[ "$deployed_head" != "$target_head" ]]; then
    printf 'deployment_status=pending deployment\n'
  else
    printf 'deployment_status=up to date\n'
  fi
  printf 'CHECK succeeded\n'
  exit 0
fi

read_current_head
prepare_deploy_state
deployment_from_head="$deployed_head"
fetch_origin_main
read_target_head
check_fast_forward
check_deployed_ancestry

if [[ "$deployed_head" == "$target_head" ]]; then
  wait_internal_readiness
  check_public_health
  print_success
  printf 'No-op: production runtime already matches deployed state.\n'
  exit 0
fi

classify_changes

if [[ "$git_head" != "$target_head" ]]; then
  stage="git fast-forward"
  git -C "$REPO_ROOT" merge --ff-only origin/main >/dev/null 2>&1 || \
    fail_stage "git fast-forward failed"
  [[ "$(git -C "$REPO_ROOT" rev-parse HEAD)" == "$target_head" ]] || \
    fail_stage "HEAD after fast-forward does not match origin/main"
  git_head="$target_head"
else
  printf 'Pending production deployment: Git HEAD already equals target.\n'
fi

stage="post-merge Compose static validation"
"${compose[@]}" config --quiet >/dev/null 2>&1 || \
  fail_stage "post-merge Compose config validation failed"

if [[ "$compose_changed" == "yes" && "${ALLOW_PRODUCTION_COMPOSE_CHANGE:-0}" != "1" ]]; then
  failure_reported=1
  printf 'Production Compose changed; runtime deployment requires explicit ALLOW_PRODUCTION_COMPOSE_CHANGE=1\n' >&2
  printf 'Infrastructure Compose changes require separate manual apply.\n' >&2
  printf 'deployed_commit=%s\n' "${deployed_head:0:8}" >&2
  printf 'git_commit=%s\n' "${git_head:0:8}" >&2
  printf 'target_commit=%s\n' "${target_head:0:8}" >&2
  exit 2
fi

if [[ "$backend_runtime_changed" == "no" && "$devdata_registry_changed" == "no" && \
  "$caddy_changed" == "no" && "$compose_changed" == "no" ]]; then
  write_state_atomic "$target_head"
  printf 'No VPS runtime deployment required.\n'
  printf 'deployed_commit=%s\n' "${target_head:0:8}"
  exit 0
fi

if [[ "$backend_runtime_changed" == "yes" || "$devdata_registry_changed" == "yes" || \
  "$compose_changed" == "yes" ]]; then
  stage="backend image build"
  "${compose[@]}" build api || fail_stage "backend image build failed"
fi

if [[ "$backend_runtime_changed" == "yes" || "$compose_changed" == "yes" ]]; then
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

if [[ "$devdata_registry_changed" == "yes" && "$backend_runtime_changed" == "no" && \
  "$caddy_changed" == "no" && "$compose_changed" == "no" ]]; then
  write_state_atomic "$target_head"
  deployed_head="$target_head"
  print_success
  printf 'Registry-only deployment succeeded: backend image built; runtime containers unchanged.\n'
  exit 0
fi

wait_internal_readiness
check_public_health
write_state_atomic "$target_head"
deployed_head="$target_head"
print_success
