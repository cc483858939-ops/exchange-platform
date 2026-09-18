#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
COMPOSE_FILE="${COMPOSE_FILE:-$REPO_ROOT/deploy/compose.prod.yml}"
ENV_FILE="${ENV_FILE:-$REPO_ROOT/deploy/.env}"
STATE_DIR="${STATE_DIR:-$REPO_ROOT/.deploy-state}"
RUNTIME_STATE_FILE="$STATE_DIR/last-successful-runtime-commit"
COMPOSE_STATE_FILE="$STATE_DIR/last-applied-compose-commit"
LEGACY_STATE_FILE="$STATE_DIR/last-successful-commit"
LOCK_FILE="/tmp/exchange-platform-production-deploy.lock"

compose=(docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE")

MODE=""
ACK_TARGET=""
stage="startup"
git_head="unknown"
runtime_head=""
compose_head=""
target_head="unknown"
runtime_from_head=""
database_backup="none"
failure_reported=0
backend_runtime_changed="no"
devdata_registry_changed="no"
caddy_changed="no"
compose_changed="no"
compose_changed_paths=""
runtime_status_value="uninitialized"
compose_status_value="uninitialized"
api_ready="skipped"
worker_ready="skipped"
public_health="skipped"

on_unexpected_error() {
  local exit_code=$?
  if [[ "$failure_reported" != "1" ]]; then
    printf 'Deployment failed\n' >&2
    printf 'stage=%s\n' "$stage" >&2
    printf 'git_commit=%s\n' "$git_head" >&2
    printf 'runtime_commit=%s\n' "${runtime_head:-uninitialized}" >&2
    printf 'compose_commit=%s\n' "${compose_head:-uninitialized}" >&2
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
  printf 'runtime_commit=%s\n' "${runtime_head:-uninitialized}" >&2
  printf 'compose_commit=%s\n' "${compose_head:-uninitialized}" >&2
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
  if [[ -L "$STATE_DIR" ]]; then
    fail_stage "deployment state directory must not be a symbolic link"
  fi
  mkdir -p -- "$STATE_DIR" || fail_stage "cannot create deployment state directory"
  [[ -d "$STATE_DIR" ]] || fail_stage "deployment state path is not a directory"
  chmod 700 -- "$STATE_DIR" || fail_stage "cannot secure deployment state directory"
  local state_dir_mode
  state_dir_mode="$(stat -c '%a' "$STATE_DIR" 2>/dev/null)" || \
    fail_stage "cannot inspect deployment state directory permissions"
  [[ "$state_dir_mode" == "700" ]] || fail_stage "deployment state directory is not 0700"
}

write_state_atomic() {
  local state_file=$1
  local commit=$2
  local tmp_path resolved_commit
  [[ "$commit" =~ ^[0-9a-fA-F]{40}$ ]] || fail_stage "refusing invalid deployment state commit"
  resolved_commit="$(git -C "$REPO_ROOT" rev-parse --verify "${commit}^{commit}" 2>/dev/null)" || \
    fail_stage "refusing deployment state commit that is not in the repository"
  ensure_state_dir

  tmp_path="$(mktemp "$STATE_DIR/.state.XXXXXX")" || \
    fail_stage "cannot create temporary deployment state file"
  if ! chmod 600 -- "$tmp_path"; then
    rm -f -- "$tmp_path"
    fail_stage "cannot secure temporary deployment state file"
  fi
  if ! printf '%s\n' "$resolved_commit" >"$tmp_path"; then
    rm -f -- "$tmp_path"
    fail_stage "cannot write deployment state file"
  fi
  if ! python3 - "$tmp_path" "$STATE_DIR" <<'PY'
import os
import sys

file_fd = os.open(sys.argv[1], os.O_RDONLY)
try:
    os.fsync(file_fd)
finally:
    os.close(file_fd)

dir_fd = os.open(sys.argv[2], os.O_RDONLY | os.O_DIRECTORY)
try:
    os.fsync(dir_fd)
finally:
    os.close(dir_fd)
PY
  then
    rm -f -- "$tmp_path"
    fail_stage "cannot fsync temporary deployment state file"
  fi
  if ! mv -f -- "$tmp_path" "$state_file"; then
    rm -f -- "$tmp_path"
    fail_stage "cannot atomically replace deployment state file"
  fi
  if ! python3 - "$STATE_DIR" <<'PY'
import os
import sys

dir_fd = os.open(sys.argv[1], os.O_RDONLY | os.O_DIRECTORY)
try:
    os.fsync(dir_fd)
finally:
    os.close(dir_fd)
PY
  then
    fail_stage "cannot fsync deployment state directory"
  fi
}

read_state_file() {
  local state_file=$1
  local state_value last_byte
  local -a state_lines=()

  [[ ! -L "$state_file" ]] || fail_stage "deployment state file must not be a symbolic link"
  [[ -f "$state_file" ]] || fail_stage "deployment state path is not a regular file"
  mapfile -t state_lines <"$state_file" || fail_stage "cannot read deployment state file"
  ((${#state_lines[@]} == 1)) || fail_stage "deployment state file must contain one commit"
  last_byte="$(tail -c 1 "$state_file" | od -An -t x1 | tr -d '[:space:]')"
  [[ "$last_byte" == "0a" ]] || fail_stage "deployment state file must end with a newline"

  state_value="${state_lines[0]}"
  [[ "$state_value" =~ ^[0-9a-fA-F]{40}$ ]] || \
    fail_stage "deployment state file contains an invalid commit"
  if ! STATE_READ_VALUE="$(git -C "$REPO_ROOT" rev-parse --verify "${state_value}^{commit}" 2>/dev/null)"; then
    fail_stage "deployment state commit is not present in this repository"
  fi
}

check_state_permissions_readonly() {
  local state_dir_mode state_file_mode state_file
  stage="deployment state permissions"
  [[ ! -L "$STATE_DIR" ]] || fail_stage "deployment state directory must not be a symbolic link"
  if [[ -e "$STATE_DIR" ]]; then
    [[ -d "$STATE_DIR" ]] || fail_stage "deployment state directory path is not a directory"
    state_dir_mode="$(stat -c '%a' "$STATE_DIR" 2>/dev/null)" || \
      fail_stage "cannot inspect deployment state directory permissions"
    [[ "$state_dir_mode" == "700" ]] || fail_stage "deployment state directory is not 0700"
  fi
  for state_file in "$RUNTIME_STATE_FILE" "$COMPOSE_STATE_FILE"; do
    [[ ! -L "$state_file" ]] || fail_stage "deployment state file must not be a symbolic link"
    if [[ -e "$state_file" ]]; then
      [[ -f "$state_file" ]] || fail_stage "deployment state path is not a regular file"
      state_file_mode="$(stat -c '%a' "$state_file" 2>/dev/null)" || \
        fail_stage "cannot inspect deployment state file permissions"
      [[ "$state_file_mode" == "600" ]] || fail_stage "deployment state file is not 0600"
    fi
  done
}

secure_state_file() {
  local state_file=$1 state_file_mode
  [[ ! -L "$state_file" ]] || fail_stage "deployment state file must not be a symbolic link"
  [[ -f "$state_file" ]] || fail_stage "deployment state path is not a regular file"
  chmod 600 -- "$state_file" || fail_stage "cannot secure deployment state file"
  state_file_mode="$(stat -c '%a' "$state_file" 2>/dev/null)" || \
    fail_stage "cannot inspect deployment state file permissions"
  [[ "$state_file_mode" == "600" ]] || fail_stage "deployment state file is not 0600"
}

load_states_readonly() {
  runtime_head=""
  compose_head=""
  if [[ -e "$RUNTIME_STATE_FILE" ]]; then
    read_state_file "$RUNTIME_STATE_FILE"
    runtime_head="$STATE_READ_VALUE"
  fi
  if [[ -e "$COMPOSE_STATE_FILE" ]]; then
    read_state_file "$COMPOSE_STATE_FILE"
    compose_head="$STATE_READ_VALUE"
  fi
}

prepare_deploy_states() {
  local has_runtime=0 has_compose=0 has_legacy=0 legacy_head=""
  ensure_state_dir

  [[ ! -L "$RUNTIME_STATE_FILE" ]] || fail_stage "deployment state file must not be a symbolic link"
  [[ ! -L "$COMPOSE_STATE_FILE" ]] || fail_stage "deployment state file must not be a symbolic link"
  [[ ! -L "$LEGACY_STATE_FILE" ]] || fail_stage "legacy deployment state file must not be a symbolic link"

  [[ -e "$RUNTIME_STATE_FILE" ]] && has_runtime=1
  [[ -e "$COMPOSE_STATE_FILE" ]] && has_compose=1
  [[ -e "$LEGACY_STATE_FILE" ]] && has_legacy=1

  if (( has_runtime == 0 && has_compose == 0 && has_legacy == 0 )); then
    write_state_atomic "$RUNTIME_STATE_FILE" "$git_head"
    write_state_atomic "$COMPOSE_STATE_FILE" "$git_head"
  elif (( has_legacy == 1 )); then
    read_state_file "$LEGACY_STATE_FILE"
    legacy_head="$STATE_READ_VALUE"
    (( has_runtime == 1 )) || write_state_atomic "$RUNTIME_STATE_FILE" "$legacy_head"
    (( has_compose == 1 )) || write_state_atomic "$COMPOSE_STATE_FILE" "$legacy_head"
  elif (( has_runtime == 0 || has_compose == 0 )); then
    fail_stage "deployment state is partially initialized without a legacy state"
  fi

  secure_state_file "$RUNTIME_STATE_FILE"
  secure_state_file "$COMPOSE_STATE_FILE"

  load_states_readonly
  [[ -n "$runtime_head" ]] || fail_stage "runtime deployment state is unavailable"
  [[ -n "$compose_head" ]] || fail_stage "Compose application state is unavailable"
}

check_fast_forward() {
  stage="fast-forward ancestry check"
  git -C "$REPO_ROOT" merge-base --is-ancestor "$git_head" "$target_head" || \
    fail_stage "current HEAD is not an ancestor of origin/main"
}

check_deployed_ancestry() {
  stage="deployment state ancestry check"
  if [[ -n "$runtime_head" ]]; then
    git -C "$REPO_ROOT" merge-base --is-ancestor "$runtime_head" "$target_head" || \
      fail_stage "runtime deployment state is not an ancestor of origin/main"
  fi
  if [[ -n "$compose_head" ]]; then
    git -C "$REPO_ROOT" merge-base --is-ancestor "$compose_head" "$target_head" || \
      fail_stage "Compose application state is not an ancestor of origin/main"
  fi
}

classify_runtime_changes() {
  local changed_paths_text path classification_base
  classification_base="$runtime_head"
  [[ -n "$classification_base" ]] || classification_base="$git_head"
  stage="classifying runtime Git changes"
  changed_paths_text="$(git -C "$REPO_ROOT" diff --name-only "$classification_base..$target_head")" || \
    fail_stage "cannot list changes between runtime state and target"

  backend_runtime_changed="no"
  devdata_registry_changed="no"
  caddy_changed="no"
  while IFS= read -r path; do
    [[ -z "$path" ]] && continue
    case "$path" in
      Go.exchange/config/sources/x_sources.json) devdata_registry_changed="yes" ;;
      Go.exchange/*) backend_runtime_changed="yes" ;;
      deploy/Caddyfile) caddy_changed="yes" ;;
    esac
  done <<< "$changed_paths_text"

  printf 'runtime_changed_paths:\n'
  if [[ -n "$changed_paths_text" ]]; then
    printf '%s\n' "$changed_paths_text"
  else
    printf '(none)\n'
  fi
  printf 'backend_runtime_changed=%s\n' "$backend_runtime_changed"
  printf 'devdata_registry_changed=%s\n' "$devdata_registry_changed"
  printf 'caddy_changed=%s\n' "$caddy_changed"
}

classify_compose_changes() {
  stage="classifying Compose Git changes"
  compose_changed="no"
  compose_changed_paths=""
  if [[ -n "$compose_head" ]]; then
    compose_changed_paths="$(git -C "$REPO_ROOT" diff --name-only \
      "$compose_head..$target_head" -- deploy/compose.prod.yml)" || \
      fail_stage "cannot list Compose changes between state and target"
    [[ -z "$compose_changed_paths" ]] || compose_changed="yes"
  fi
}

update_status_values() {
  if [[ -z "$runtime_head" ]]; then
    runtime_status_value="uninitialized"
  elif [[ "$runtime_head" == "$target_head" ]]; then
    runtime_status_value="up-to-date"
  else
    runtime_status_value="pending"
  fi

  if [[ -z "$compose_head" ]]; then
    compose_status_value="uninitialized"
  elif [[ "$compose_changed" == "yes" ]]; then
    compose_status_value="pending"
  else
    compose_status_value="up-to-date"
  fi
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
  printf 'runtime_from=%s\n' "${runtime_from_head:0:8}"
  printf 'runtime_to=%s\n' "${target_head:0:8}"
  printf 'compose_commit=%s\n' "${compose_head:0:8}"
  printf 'backend_runtime_changed=%s\n' "$backend_runtime_changed"
  printf 'devdata_registry_changed=%s\n' "$devdata_registry_changed"
  printf 'caddy_changed=%s\n' "$caddy_changed"
  printf 'database_backup=%s\n' "$database_backup"
  printf 'api_ready=%s\n' "$api_ready"
  printf 'worker_ready=%s\n' "$worker_ready"
  printf 'public_health=%s\n' "$public_health"
}

print_compose_gate() {
  failure_reported=1
  printf 'Production Compose changes are pending manual application.\n' >&2
  printf 'compose_from=%s\n' "${compose_head:0:8}" >&2
  printf 'compose_target=%s\n' "${target_head:0:8}" >&2
  printf '%s\n' "$compose_changed_paths" >&2
  printf 'Review the production Compose diff.\n' >&2
  printf 'Fast-forward Git manually if approved.\n' >&2
  printf 'Manually apply the infrastructure and service changes.\n' >&2
  printf 'Verify production health.\n' >&2
  printf 'Run: bash scripts/deploy-production.sh ACK-COMPOSE-APPLIED %s\n' "$target_head" >&2
  printf 'Rerun: bash scripts/deploy-production.sh DEPLOY-PRODUCTION\n' >&2
}

run_check() {
  read_current_head
  check_state_permissions_readonly
  load_states_readonly
  fetch_origin_main
  read_target_head
  check_fast_forward
  check_deployed_ancestry
  classify_compose_changes
  classify_runtime_changes
  update_status_values

  printf 'git_commit=%s\n' "$git_head"
  printf 'target_commit=%s\n' "$target_head"
  printf 'runtime_commit=%s\n' "${runtime_head:-uninitialized}"
  printf 'compose_commit=%s\n' "${compose_head:-uninitialized}"
  printf 'runtime_status=%s\n' "$runtime_status_value"
  printf 'compose_status=%s\n' "$compose_status_value"
  printf 'CHECK succeeded\n'
}

run_ack() {
  local resolved_ack compose_from_head
  read_current_head
  check_state_permissions_readonly
  load_states_readonly
  fetch_origin_main
  read_target_head
  check_fast_forward
  check_deployed_ancestry

  stage="ACK target validation"
  [[ "$ACK_TARGET" =~ ^[0-9a-fA-F]{40}$ ]] || fail_stage "ACK target must be a full 40-character Git SHA"
  resolved_ack="$(git -C "$REPO_ROOT" rev-parse --verify "${ACK_TARGET}^{commit}" 2>/dev/null)" || \
    fail_stage "ACK target is not a valid Git commit"
  [[ "$resolved_ack" == "$target_head" ]] || fail_stage "ACK target does not equal origin/main"
  [[ "$git_head" == "$target_head" ]] || fail_stage "Git HEAD must equal origin/main before ACK"
  [[ -n "$compose_head" ]] || fail_stage "Compose state is uninitialized; cannot acknowledge it"

  classify_compose_changes
  [[ "$compose_changed" == "yes" ]] || fail_stage "ACK target contains no pending Compose change"
  compose_from_head="$compose_head"

  wait_internal_readiness
  check_public_health
  write_state_atomic "$COMPOSE_STATE_FILE" "$target_head"
  compose_head="$target_head"
  compose_changed="no"
  compose_changed_paths=""
  update_status_values

  printf 'Compose application acknowledged\n'
  printf 'compose_from=%s\n' "${compose_from_head:0:8}"
  printf 'compose_to=%s\n' "${target_head:0:8}"
  printf 'runtime_commit=%s\n' "${runtime_head:-uninitialized}"
  printf 'runtime_status=%s\n' "$runtime_status_value"
  if [[ "$runtime_status_value" == "pending" ]]; then
    printf 'Run DEPLOY-PRODUCTION to finish application deployment.\n'
  fi
}

run_deploy() {
  read_current_head
  runtime_from_head=""
  prepare_deploy_states
  runtime_from_head="$runtime_head"
  fetch_origin_main
  read_target_head
  check_fast_forward
  check_deployed_ancestry
  classify_compose_changes

  if [[ "$compose_changed" == "yes" ]]; then
    print_compose_gate
    return 2
  fi

  if [[ "$runtime_head" == "$target_head" ]]; then
    wait_internal_readiness
    check_public_health
    printf 'Deployment already complete\n'
    printf 'runtime_commit=%s\n' "${runtime_head:0:8}"
    printf 'compose_commit=%s\n' "${compose_head:0:8}"
    printf 'api_ready=%s\n' "$api_ready"
    printf 'worker_ready=%s\n' "$worker_ready"
    printf 'public_health=%s\n' "$public_health"
    return 0
  fi

  if [[ "$git_head" != "$target_head" ]]; then
    stage="git fast-forward"
    git -C "$REPO_ROOT" merge --ff-only origin/main >/dev/null 2>&1 || \
      fail_stage "git fast-forward failed"
    [[ "$(git -C "$REPO_ROOT" rev-parse HEAD)" == "$target_head" ]] || \
      fail_stage "HEAD after fast-forward does not match origin/main"
    git_head="$target_head"
  fi

  stage="post-merge Compose static validation"
  "${compose[@]}" config --quiet >/dev/null 2>&1 || \
    fail_stage "post-merge Compose config validation failed"

  classify_runtime_changes

  if [[ "$backend_runtime_changed" == "no" && "$devdata_registry_changed" == "no" && \
    "$caddy_changed" == "no" ]]; then
    write_state_atomic "$RUNTIME_STATE_FILE" "$target_head"
    runtime_head="$target_head"
    printf 'No VPS runtime deployment required.\n'
    printf 'runtime_commit=%s\n' "${target_head:0:8}"
    return 0
  fi

  if [[ "$backend_runtime_changed" == "yes" || "$devdata_registry_changed" == "yes" ]]; then
    stage="backend image build"
    "${compose[@]}" build api || fail_stage "backend image build failed"
  fi

  if [[ "$backend_runtime_changed" == "yes" ]]; then
    create_database_backup
    run_initialization outbox-cutover
    run_initialization migrate
    run_initialization kafka-init
    run_initialization cdc-init

    stage="API and Worker update"
    "${compose[@]}" up -d --no-deps --force-recreate api worker || \
      fail_stage "API and Worker update failed"
  fi

  if [[ "$caddy_changed" == "yes" ]]; then
    stage="Caddy update"
    "${compose[@]}" up -d --no-deps --force-recreate caddy || \
      fail_stage "Caddy update failed"
  fi

  if [[ "$devdata_registry_changed" == "yes" && "$backend_runtime_changed" == "no" && \
    "$caddy_changed" == "no" ]]; then
    write_state_atomic "$RUNTIME_STATE_FILE" "$target_head"
    runtime_head="$target_head"
    printf 'Registry-only deployment succeeded.\n'
    printf 'Backend image rebuilt; runtime containers unchanged.\n'
    printf 'runtime_commit=%s\n' "${target_head:0:8}"
    return 0
  fi

  wait_internal_readiness
  check_public_health
  write_state_atomic "$RUNTIME_STATE_FILE" "$target_head"
  runtime_head="$target_head"
  print_success
}

main() {
  if [[ $# -eq 1 && ( "$1" == "CHECK" || "$1" == "DEPLOY-PRODUCTION" ) ]]; then
    MODE="$1"
  elif [[ $# -eq 2 && "$1" == "ACK-COMPOSE-APPLIED" ]]; then
    MODE="$1"
    ACK_TARGET="$2"
    [[ "$ACK_TARGET" =~ ^[0-9a-fA-F]{40}$ ]] || {
      printf 'Usage: bash scripts/deploy-production.sh ACK-COMPOSE-APPLIED <full-40-character-sha>\n' >&2
      return 2
    }
  else
    printf 'Usage: bash scripts/deploy-production.sh CHECK|DEPLOY-PRODUCTION\n' >&2
    printf '   or: bash scripts/deploy-production.sh ACK-COMPOSE-APPLIED <full-40-character-sha>\n' >&2
    return 2
  fi

  acquire_lock
  check_preconditions

  case "$MODE" in
    CHECK) run_check ;;
    DEPLOY-PRODUCTION) run_deploy ;;
    ACK-COMPOSE-APPLIED) run_ack ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
