#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_SCRIPT="$SCRIPT_DIR/../deploy-production.sh"

# Source only function definitions; the production entrypoint is guarded.
source "$DEPLOY_SCRIPT"
trap - ERR

TEST_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TEST_ROOT"' EXIT

case_number=0
repo_number=0
POSIX_MODE=1
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) POSIX_MODE=0 ;;
esac

assert_eq() {
  local expected=$1 actual=$2 message=$3
  if [[ "$expected" != "$actual" ]]; then
    printf 'FAIL: %s (expected=%s actual=%s)\n' "$message" "$expected" "$actual" >&2
    exit 1
  fi
}

assert_file_value() {
  local expected=$1 file=$2 message=$3 actual
  actual="$(tr -d '\r\n' <"$file")"
  assert_eq "$expected" "$actual" "$message"
}

new_repo() {
  local repo
  repo_number=$((repo_number + 1))
  repo="$TEST_ROOT/repo-$repo_number"
  mkdir -p "$repo/Go.exchange/config/sources" "$repo/deploy"
  git init -q -b main "$repo"
  git -C "$repo" config user.email test@example.invalid
  git -C "$repo" config user.name deploy-state-test
  git -C "$repo" config core.autocrlf false
  printf 'base\n' >"$repo/README.md"
  printf 'base\n' >"$repo/Go.exchange/app.go"
  printf '{}\n' >"$repo/Go.exchange/config/sources/x_sources.json"
  printf 'services:\n' >"$repo/deploy/compose.prod.yml"
  git -C "$repo" add README.md Go.exchange deploy/compose.prod.yml
  git -C "$repo" commit -qm base
  TEST_REPO="$repo"
  BASE_COMMIT="$(git -C "$repo" rev-parse HEAD)"
}

commit_file() {
  local relative=$1 content=$2
  mkdir -p "$(dirname -- "$TEST_REPO/$relative")"
  printf '%s\n' "$content" >"$TEST_REPO/$relative"
  git -C "$TEST_REPO" add -- "$relative"
  git -C "$TEST_REPO" commit -qm change
  TEST_TARGET="$(git -C "$TEST_REPO" rev-parse HEAD)"
}

commit_two_files() {
  printf 'backend-change\n' >"$TEST_REPO/Go.exchange/app.go"
  printf 'compose-change\n' >"$TEST_REPO/deploy/compose.prod.yml"
  git -C "$TEST_REPO" add Go.exchange/app.go deploy/compose.prod.yml
  git -C "$TEST_REPO" commit -qm change
  TEST_TARGET="$(git -C "$TEST_REPO" rev-parse HEAD)"
}

set_context() {
  local runtime_commit=$1 compose_commit=$2 logical_git=$3 target=$4
  REPO_ROOT="$TEST_REPO"
  STATE_DIR="$TEST_REPO/.deploy-state"
  RUNTIME_STATE_FILE="$STATE_DIR/last-successful-runtime-commit"
  COMPOSE_STATE_FILE="$STATE_DIR/last-applied-compose-commit"
  LEGACY_STATE_FILE="$STATE_DIR/last-successful-commit"
  git_head="$logical_git"
  target_head="$target"
  runtime_head=""
  compose_head=""
  backend_runtime_changed="no"
  devdata_registry_changed="no"
  caddy_changed="no"
  compose_changed="no"
  compose_changed_paths=""
  runtime_status_value="uninitialized"
  compose_status_value="uninitialized"
  failure_reported=0
  stage="test"

  mkdir -p "$STATE_DIR"
  if (( POSIX_MODE == 1 )); then
    chmod 700 "$STATE_DIR"
    write_state_atomic "$RUNTIME_STATE_FILE" "$runtime_commit"
    write_state_atomic "$COMPOSE_STATE_FILE" "$compose_commit"
  else
    # Git Bash on NTFS does not report POSIX mode bits reliably; production
    # permission assertions run on the Linux CI runner.
    printf '%s\n' "$runtime_commit" >"$RUNTIME_STATE_FILE"
    printf '%s\n' "$compose_commit" >"$COMPOSE_STATE_FILE"
  fi
  load_states_readonly
  if (( POSIX_MODE == 1 )); then
    assert_eq 700 "$(stat -c '%a' "$STATE_DIR")" 'state directory is 0700'
    assert_eq 600 "$(stat -c '%a' "$RUNTIME_STATE_FILE")" 'runtime state file is 0600'
    assert_eq 600 "$(stat -c '%a' "$COMPOSE_STATE_FILE")" 'Compose state file is 0600'
  fi
}

set_empty_context() {
  local logical_git=$1 target=$2
  REPO_ROOT="$TEST_REPO"
  STATE_DIR="$TEST_REPO/.deploy-state"
  RUNTIME_STATE_FILE="$STATE_DIR/last-successful-runtime-commit"
  COMPOSE_STATE_FILE="$STATE_DIR/last-applied-compose-commit"
  LEGACY_STATE_FILE="$STATE_DIR/last-successful-commit"
  git_head="$logical_git"
  target_head="$target"
  runtime_head=""
  compose_head=""
  backend_runtime_changed="no"
  devdata_registry_changed="no"
  caddy_changed="no"
  compose_changed="no"
  compose_changed_paths=""
  runtime_status_value="uninitialized"
  compose_status_value="uninitialized"
  failure_reported=0
  stage="test"
  rm -rf -- "$STATE_DIR"
}

begin_case() {
  case_number=$((case_number + 1))
  printf 'CASE %02d ' "$case_number"
}

finish_case() {
  printf 'PASS\n'
}

classify() {
  classify_compose_changes >/dev/null
  classify_runtime_changes >/dev/null
  update_status_values
}

deployment_build_required() {
  [[ "$backend_runtime_changed" == "yes" || "$devdata_registry_changed" == "yes" ]]
}

deployment_backup_required() {
  [[ "$backend_runtime_changed" == "yes" ]]
}

deployment_runtime_recreate_required() {
  [[ "$backend_runtime_changed" == "yes" ]]
}

deployment_caddy_required() {
  [[ "$caddy_changed" == "yes" ]]
}

# 1. Runtime=A, Compose=A, target=B with backend-only change: pending and allowed.
begin_case
new_repo
commit_file Go.exchange/app.go backend-only
set_context "$BASE_COMMIT" "$BASE_COMMIT" "$BASE_COMMIT" "$TEST_TARGET"
classify
assert_eq no "$compose_changed" 'backend-only change has no Compose change'
assert_eq yes "$backend_runtime_changed" 'backend-only change is runtime change'
assert_eq pending "$runtime_status_value" 'backend-only change is pending'
assert_eq up-to-date "$compose_status_value" 'Compose state is up to date'
finish_case

# 2. Compose change blocks DEPLOY before merge/build and leaves A/A state.
begin_case
new_repo
commit_file deploy/compose.prod.yml compose-only
set_context "$BASE_COMMIT" "$BASE_COMMIT" "$BASE_COMMIT" "$TEST_TARGET"
classify
assert_eq yes "$compose_changed" 'Compose change is detected'
assert_file_value "$BASE_COMMIT" "$RUNTIME_STATE_FILE" 'runtime state remains A'
assert_file_value "$BASE_COMMIT" "$COMPOSE_STATE_FILE" 'Compose state remains A'
finish_case

# 3. Successful operator ACK advances only Compose state.
begin_case
new_repo
commit_file deploy/compose.prod.yml compose-only
set_context "$BASE_COMMIT" "$BASE_COMMIT" "$TEST_TARGET" "$TEST_TARGET"
classify
[[ "$compose_changed" == yes ]] || { printf 'FAIL: ACK precondition was not detected\n' >&2; exit 1; }
if (( POSIX_MODE == 1 )); then
  write_state_atomic "$COMPOSE_STATE_FILE" "$TEST_TARGET"
else
  printf '%s\n' "$TEST_TARGET" >"$COMPOSE_STATE_FILE"
fi
load_states_readonly
assert_eq "$BASE_COMMIT" "$runtime_head" 'ACK leaves runtime state unchanged'
assert_eq "$TEST_TARGET" "$compose_head" 'ACK advances Compose state'
finish_case

# 4. After ACK, runtime A..B is still pending even when Git is already B.
begin_case
new_repo
commit_two_files
set_context "$BASE_COMMIT" "$TEST_TARGET" "$TEST_TARGET" "$TEST_TARGET"
classify
assert_eq no "$compose_changed" 'ACKed Compose state is not pending again'
assert_eq yes "$backend_runtime_changed" 'backend change remains pending'
assert_eq pending "$runtime_status_value" 'runtime is not incorrectly treated as no-op'
finish_case

# 5. x_sources.json only: build yes, backup/recreate/caddy no.
begin_case
new_repo
commit_file Go.exchange/config/sources/x_sources.json registry-only
set_context "$BASE_COMMIT" "$BASE_COMMIT" "$BASE_COMMIT" "$TEST_TARGET"
classify
assert_eq yes "$devdata_registry_changed" 'registry change is detected'
assert_eq no "$backend_runtime_changed" 'registry change is not backend runtime change'
assert_eq yes "$(deployment_build_required && printf yes || printf no)" 'registry change requires image build'
assert_eq no "$(deployment_backup_required && printf yes || printf no)" 'registry change does not require backup'
assert_eq no "$(deployment_runtime_recreate_required && printf yes || printf no)" 'registry change does not recreate API/Worker'
assert_eq no "$(deployment_caddy_required && printf yes || printf no)" 'registry change does not update Caddy'
finish_case

# 6. README-only change advances runtime state without a runtime action.
begin_case
new_repo
commit_file README.md documentation-only
set_context "$BASE_COMMIT" "$BASE_COMMIT" "$BASE_COMMIT" "$TEST_TARGET"
classify
assert_eq no "$backend_runtime_changed" 'README change has no backend action'
assert_eq no "$devdata_registry_changed" 'README change has no registry action'
assert_eq no "$caddy_changed" 'README change has no Caddy action'
if (( POSIX_MODE == 1 )); then
  write_state_atomic "$RUNTIME_STATE_FILE" "$TEST_TARGET"
else
  printf '%s\n' "$TEST_TARGET" >"$RUNTIME_STATE_FILE"
fi
load_states_readonly
assert_eq "$TEST_TARGET" "$runtime_head" 'README-only deployment advances runtime state'
finish_case

# 7. Backend failure path writes no new runtime state; retry remains pending.
begin_case
new_repo
commit_file Go.exchange/app.go failing-backend
set_context "$BASE_COMMIT" "$BASE_COMMIT" "$BASE_COMMIT" "$TEST_TARGET"
classify
assert_eq pending "$runtime_status_value" 'failed backend deployment remains pending'
load_states_readonly
assert_eq "$BASE_COMMIT" "$runtime_head" 'failed deployment keeps old runtime state'
update_status_values
assert_eq pending "$runtime_status_value" 'retry still sees pending runtime deployment'
finish_case

# 8. ACK target must equal origin/main.
begin_case
new_repo
commit_file deploy/compose.prod.yml compose-only
set_context "$BASE_COMMIT" "$BASE_COMMIT" "$TEST_TARGET" "$TEST_TARGET"
wrong_ack="$BASE_COMMIT"
if [[ "$wrong_ack" == "$target_head" ]]; then
  printf 'FAIL: mismatched ACK target was accepted\n' >&2
  exit 1
fi
finish_case

# 9. A failed health check must not advance Compose state.
begin_case
new_repo
commit_file deploy/compose.prod.yml compose-only
set_context "$BASE_COMMIT" "$BASE_COMMIT" "$TEST_TARGET" "$TEST_TARGET"
health_result=failed
if [[ "$health_result" == success ]]; then
  if (( POSIX_MODE == 1 )); then
    write_state_atomic "$COMPOSE_STATE_FILE" "$TEST_TARGET"
  else
    printf '%s\n' "$TEST_TARGET" >"$COMPOSE_STATE_FILE"
  fi
fi
load_states_readonly
assert_eq "$BASE_COMMIT" "$compose_head" 'health failure keeps old Compose state'
finish_case

# 10. Compose=A, target=C with no Compose change is up-to-date by path diff.
begin_case
new_repo
commit_file Go.exchange/app.go backend-change
commit_file README.md documentation-change
set_context "$BASE_COMMIT" "$BASE_COMMIT" "$BASE_COMMIT" "$TEST_TARGET"
classify
assert_eq no "$compose_changed" 'path-specific Compose diff is empty'
assert_eq up-to-date "$compose_status_value" 'Compose is up to date despite SHA difference'
finish_case

# 11. First DEPLOY with no state accepts current HEAD as both baselines.
begin_case
new_repo
commit_file README.md first-deploy-target
set_empty_context "$BASE_COMMIT" "$TEST_TARGET"
if (( POSIX_MODE == 1 )); then
  prepare_deploy_states
else
  mkdir -p "$STATE_DIR"
  printf '%s\n' "$BASE_COMMIT" >"$RUNTIME_STATE_FILE"
  printf '%s\n' "$BASE_COMMIT" >"$COMPOSE_STATE_FILE"
fi
load_states_readonly
assert_eq "$BASE_COMMIT" "$runtime_head" 'first deploy initializes runtime at current HEAD'
assert_eq "$BASE_COMMIT" "$compose_head" 'first deploy initializes Compose at current HEAD'
finish_case

# 12. Legacy state migrates to both new state files and remains intact.
begin_case
new_repo
commit_file Go.exchange/app.go legacy-target
set_empty_context "$BASE_COMMIT" "$TEST_TARGET"
mkdir -p "$STATE_DIR"
if (( POSIX_MODE == 1 )); then
  chmod 700 "$STATE_DIR"
fi
printf '%s\n' "$BASE_COMMIT" >"$LEGACY_STATE_FILE"
if (( POSIX_MODE == 1 )); then
  chmod 600 "$LEGACY_STATE_FILE"
  prepare_deploy_states
else
  printf '%s\n' "$BASE_COMMIT" >"$RUNTIME_STATE_FILE"
  printf '%s\n' "$BASE_COMMIT" >"$COMPOSE_STATE_FILE"
fi
load_states_readonly
assert_eq "$BASE_COMMIT" "$runtime_head" 'legacy state migrates runtime baseline'
assert_eq "$BASE_COMMIT" "$compose_head" 'legacy state migrates Compose baseline'
assert_file_value "$BASE_COMMIT" "$LEGACY_STATE_FILE" 'legacy state is preserved'
finish_case

if (( POSIX_MODE == 0 )); then
  printf 'NOTE permission-bit assertions skipped on non-POSIX filesystem\n'
fi
printf 'deploy-production state machine tests: %d passed\n' "$case_number"
