#!/usr/bin/python3
"""Root-owned, forced-command SSH gateway. No caller-supplied paths or shell."""
import datetime
import json
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path("/srv/exchange-platform")
LOGS = ROOT / "backups/deploy"
INSTALLED = "/usr/local/lib/exchange-platform-cd/server.py"
LOCK = "/run/lock/exchange-platform-cd.lock"
PUBLIC = "https://backend.ccyu.dpdns.org"
ENV = {"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
       "HOME": "/root", "LANG": "C.UTF-8", "GIT_TERMINAL_PROMPT": "0"}
SHA = r"[0-9a-f]{40}"
REQUEST = re.compile(rf"(check|deploy|status) ({SHA}) ([0-9]{{1,20}}-[0-9]{{1,5}})")


class GateError(Exception):
    pass


def require(condition, reason):
    if not condition:
        raise GateError(reason)


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def command(args, timeout=120):
    # Never emit stderr: Compose/build/connector errors may contain credentials.
    result = subprocess.run(args, cwd=ROOT, env=ENV, stdin=subprocess.DEVNULL,
                            capture_output=True, text=True, timeout=timeout)
    require(result.returncode == 0, "command-failed:" + Path(args[0]).name)
    return result.stdout.strip()


def git(*args):
    return command(["git", "-C", str(ROOT), *args])


def compose(*args):
    return command(["docker", "compose", "--env-file", str(ROOT / "deploy/.env"),
                    "-f", str(ROOT / "deploy/compose.prod.yml"), *args])


def write_json(path, value):
    temporary = path.with_suffix(".tmp")
    with open(temporary, "w", encoding="utf-8") as out:
        os.chmod(temporary, 0o600)
        json.dump(value, out, sort_keys=True)
        out.write("\n")
        out.flush()
        os.fsync(out.fileno())
    os.replace(temporary, path)
    if os.name == "posix":
        directory_fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)


def state(name):
    path = ROOT / ".deploy-state" / name
    require(path.is_file() and not path.is_symlink(), "deployment-state-missing")
    value = path.read_text().strip()
    require(re.fullmatch(SHA, value), "invalid-deployment-state")
    return value


def assert_target(sha):
    require(git("branch", "--show-current") == "main", "server-branch-not-main")
    require(not git("status", "--porcelain", "--untracked-files=normal"), "server-working-tree-not-clean")
    require(git("remote", "get-url", "origin") in (
        "https://github.com/cc483858939-ops/exchange-platform.git",
        "git@github.com:cc483858939-ops/exchange-platform.git"), "unexpected-origin")
    # Read the remote, not just a potentially stale local tracking ref.
    require(git("ls-remote", "origin", "refs/heads/main").split()[0] == sha, "remote-main-advanced")
    git("fetch", "origin", "+refs/heads/main:refs/remotes/origin/main")
    require(git("rev-parse", "origin/main") == sha, "origin-main-sha-mismatch")
    git("merge-base", "--is-ancestor", "HEAD", sha)
    git("merge-base", "--is-ancestor", state("last-successful-runtime-commit"), sha)
    git("merge-base", "--is-ancestor", state("last-applied-compose-commit"), sha)


def schema_contract(sha):
    source = git("show", sha + ":Go.exchange/initialize/runtime_schema.go")
    result = {}
    for field, name in (("required", "RequiredSchemaVersion"), ("current", "PublishedSchemaCurrentVersion"),
                        ("floor", "PublishedSchemaCompatibilityFloor")):
        match = re.search(r"\b" + name + r"\s+int64\s*=\s*(\d+)", source)
        require(match is not None, "cannot-determine-target-schema")
        result[field] = int(match[1])
    require(result["floor"] <= result["required"] <= result["current"], "invalid-target-schema-contract")
    return result


def snapshot():
    sql = """BEGIN READ ONLY;
SELECT json_build_object(
'schema', (SELECT current_version FROM runtime_schema_state WHERE id=1),
'floor', (SELECT compatibility_floor FROM runtime_schema_state WHERE id=1),
'accounts', (SELECT count(*) FROM devdata_mirror_accounts),
'mirror_posts', (SELECT count(*) FROM devdata_mirror_posts WHERE state='active'),
'posts', (SELECT count(*) FROM posts),
'media', (SELECT count(*) FROM post_media),
'mirror_media', (SELECT count(*) FROM post_media m JOIN devdata_mirror_posts p
 ON p.local_post_id=m.post_id WHERE p.state='active'));
COMMIT;"""
    # Environment expansion happens inside the existing DB container. Never print it.
    value = compose("exec", "-T", "db", "sh", "-c",
                    'exec psql -X -qAt -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "$1"', "sh", sql)
    data = json.loads(value)
    require(all(type(v) is int and v >= 0 for v in data.values()), "invalid-database-snapshot")
    return data


def health():
    result = {}
    for service in ("api", "worker", "db", "redis", "kafka", "kafka-connect"):
        ids = compose("ps", "-q", service).splitlines()
        require(len(ids) == 1, "missing-or-duplicate-service:" + service)
        status = command(["docker", "inspect", "--format", "{{.State.Status}} {{.State.Health.Status}}", ids[0]])
        require(status == "running healthy", "service-unhealthy:" + service)
        result[service] = "healthy"
    connector = json.loads(compose("exec", "-T", "kafka-connect", "curl", "-fsS", "--max-time", "10",
                                   "http://127.0.0.1:8083/connectors/goexchange-outbox/status"))
    require(connector["connector"]["state"] == "RUNNING" and connector["tasks"] and
            all(t["state"] == "RUNNING" for t in connector["tasks"]), "cdc-not-running")
    result["cdc"] = "RUNNING"
    # No redirects: each public endpoint itself must return exactly HTTP 200.
    for path in ("/healthz", "/readyz", "/api/public/recommendations/posts?limit=1"):
        response = compose("exec", "-T", "api", "curl", "-sS", "--max-time", "20",
                           "-w", "\n%{http_code}", PUBLIC + path)
        body, code = response.rsplit("\n", 1)
        require(code == "200", "public-http-failed:" + path.split("?")[0])
        if "recommendations" in path:
            items = json.loads(body).get("items")
            require(isinstance(items, list) and len(items) > 0 and
                    all(isinstance(item.get("post"), dict) for item in items), "recommendations-invalid-or-empty")
        result[path.split("?")[0]] = 200
    return result


def official(mode, sha, record):
    env = dict(ENV, DEPLOY_EXPECTED_SHA=sha)
    process = subprocess.Popen(["bash", "scripts/deploy-production.sh", mode], cwd=ROOT, env=env,
                               stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                               text=True)
    # Allowlist only known structured fields. Build output, SQL, env and arbitrary
    # errors never enter persistent logs or Actions. No raw temporary log exists.
    values = {}
    allowed = {"git_commit", "target_commit", "runtime_commit", "compose_commit",
               "runtime_status", "compose_status", "backend_runtime_changed",
               "devdata_registry_changed", "caddy_changed", "api_ready", "worker_ready", "public_health"}
    for line in process.stdout:
        key, sep, value = line.strip().partition("=")
        if sep and key in allowed and re.fullmatch(r"[a-z0-9-]{1,64}", value):
            values[key] = value
            record.setdefault("official", []).append({key: value})
            write_json(LOGS / (record["request_id"] + ".json"), record)
    rc = process.wait()
    record["official_exit_code"] = rc
    require(rc == 0, "official-script-failed:" + mode)
    return values


def compose_diff(base, sha):
    diff = git("diff", "--no-ext-diff", "--no-color", base, sha, "--", "deploy/compose.prod.yml")
    # Show structure and non-secret values; redact all credential-bearing lines.
    return "\n".join("[credential-bearing diff line redacted]" if re.search(
        r"(?i)(pass|secret|token|private|credential|dsn|auth|key|://[^ /]+:[^ /]+@)", line)
        else line for line in diff.splitlines())


def preflight(sha, record):
    assert_target(sha)
    values = official("CHECK", sha, record)
    require(values.get("target_commit") == sha, "official-check-target-mismatch")
    contract = schema_contract(sha)
    record.update(git_head=git("rev-parse", "HEAD"),
                  runtime_commit=state("last-successful-runtime-commit"),
                  compose_commit=state("last-applied-compose-commit"),
                  target_schema=contract["current"], target_floor=contract["floor"], before=snapshot())
    record["migration_required"] = record["before"]["schema"] < record["target_schema"]
    require(record["before"]["schema"] <= record["target_schema"], "schema-downgrade-forbidden")
    record["risk"] = ("Migration raises schema compatibility floor; no automatic rollback or retry."
                      if record["migration_required"] else "No schema version increase expected.")
    if values.get("compose_status") == "pending":
        record["compose_diff"] = compose_diff(record["compose_commit"], sha)
        record["health"] = health()
        raise GateError("compose-pending:manual-review-apply-verify-and-official-ACK-required")
    require(values.get("compose_status") == "up-to-date", "compose-state-not-initialized")
    require(not git("diff", record["compose_commit"], sha, "--", "deploy/compose.prod.yml"), "compose-diff-without-ACK")
    record["health"] = health()


def deploy(sha, record):
    preflight(sha, record)
    # Revalidate after preflight, then fast-forward only the immutable commit.
    # Advancing Git is allowed only in deploy mode, after the Compose gate.
    assert_target(sha)
    pinned_script = git("show", sha + ":scripts/deploy-production.sh")
    require('DEPLOY_EXPECTED_SHA' in pinned_script, "target-lacks-official-sha-guard")
    record["mutation_started"] = True
    write_json(LOGS / (record["request_id"] + ".json"), record)
    git("merge", "--ff-only", "--no-overwrite-ignore", sha)
    require(git("rev-parse", "HEAD") == sha, "post-ff-sha-mismatch")
    assert_target(sha)
    official("DEPLOY-PRODUCTION", sha, record)
    require(git("rev-parse", "HEAD") == sha, "final-head-mismatch")
    require(state("last-successful-runtime-commit") == sha, "final-runtime-state-mismatch")
    compose_commit = state("last-applied-compose-commit")
    git("merge-base", "--is-ancestor", compose_commit, sha)
    require(not git("diff", compose_commit, sha, "--", "deploy/compose.prod.yml"), "final-compose-state-mismatch")
    after = snapshot()
    record["after"] = after
    require(after["schema"] == record["target_schema"], "final-schema-mismatch")
    require(after["floor"] == record["target_floor"], "final-schema-compatibility-floor-mismatch")
    for key in ("accounts", "mirror_posts", "posts", "media", "mirror_media"):
        require(after[key] >= record["before"][key], "data-count-decreased:" + key)
    record["health"] = health()
    record["git_head"] = sha
    record["runtime_commit"] = sha
    record["compose_commit"] = compose_commit


def execute(mode, sha, request_id):
    record = {"status": "running", "mode": mode, "target_sha": sha,
              "request_id": request_id, "started_at": now(), "exit_code": None, "mutation_started": False}
    write_json(LOGS / (request_id + ".json"), record)
    try:
        if mode == "deploy":
            deploy(sha, record)
        else:
            preflight(sha, record)
        record.update(status="success", exit_code=0)
    except GateError as error:
        record.update(status="failed", reason=str(error), exit_code=1)
    except Exception:
        # A lost worker or an unexpected exception must never mean success.
        record.update(status="unknown", reason="unexpected-error:inspect-server-before-any-retry", exit_code=1)
    record["finished_at"] = now()
    write_json(LOGS / (request_id + ".json"), record)
    # Failed/unknown deployments retain the marker until an administrator has
    # investigated. Even a new workflow run cannot automatically retry them.
    if mode == "deploy" and (record["status"] == "success" or
                             (record["status"] == "failed" and not record["mutation_started"])):
        (LOGS / "active.json").unlink()
    return record


def main():
    import fcntl  # Linux only; isolated for offline parser/logic tests on Windows.
    os.umask(0o077)
    require(os.geteuid() == 0, "root-owned-gateway-required")
    require(len(sys.argv) == 2, "one-restricted-request-required")
    match = REQUEST.fullmatch(sys.argv[1])
    require(match is not None, "invalid-request")
    mode, sha, request_id = match.groups()
    LOGS.mkdir(parents=True, exist_ok=True, mode=0o700)
    require(not LOGS.is_symlink() and LOGS.stat().st_uid == 0, "unsafe-log-directory")
    os.chmod(LOGS, 0o700)
    path = LOGS / (request_id + ".json")
    if mode == "status":
        require(path.is_file(), "request-status-missing:unknown-do-not-retry")
        record = json.loads(path.read_text())
        require(record["target_sha"] == sha, "status-sha-mismatch")
        print(json.dumps(record))
        return
    with open(LOCK, "a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        active = LOGS / "active.json"
        require(not active.exists(), "previous-deploy-unresolved:inspect-server-before-retry")
        require(not path.exists(), "request-already-exists:use-status-never-replay")
        if mode == "check":
            record = execute(mode, sha, request_id)
            print(json.dumps(record))
            sys.exit(record["exit_code"])
        write_json(active, {"request_id": request_id, "target_sha": sha, "started_at": now()})
        write_json(path, {"request_id": request_id, "target_sha": sha, "status": "running", "started_at": now()})
        # Detached worker inherits the server lock, independent of SSH/Actions.
        # The marker also survives reboot, SIGKILL, launch failure or lock loss.
        fd = lock.fileno()
        subprocess.Popen(["/usr/bin/python3", INSTALLED, "--worker", sha, request_id, str(fd)],
                         env=ENV, cwd=ROOT, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                         stderr=subprocess.DEVNULL, start_new_session=True, pass_fds=(fd,))
        print(json.dumps({"status": "running", "request_id": request_id, "target_sha": sha}))


if __name__ == "__main__":
    # This form is reachable only through the root-owned executable, not the SSH
    # parser above. sudo permits only the separate one-argument gateway wrapper.
    if len(sys.argv) == 5 and sys.argv[1] == "--worker":
        os.umask(0o077)
        require(os.geteuid() == 0 and re.fullmatch(SHA, sys.argv[2]) and
                re.fullmatch(r"[0-9]{1,20}-[0-9]{1,5}", sys.argv[3]), "invalid-worker")
        lock_fd = int(sys.argv[4])
        os.fstat(lock_fd)
        execute("deploy", sys.argv[2], sys.argv[3])
    else:
        try:
            main()
        except Exception:
            # Deliberately omit exception text, environment and raw subprocess output.
            print(json.dumps({"status": "unknown", "reason": "gateway-refused:inspect-server-before-retry"}))
            sys.exit(1)
