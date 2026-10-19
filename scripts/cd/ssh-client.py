#!/usr/bin/env python3
"""One SSH submission, then status reads. Never automatically resubmit a deploy."""
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import time


def run():
    report = Path("cd-report")
    report.mkdir(exist_ok=True)
    result_file = report / "result.json"
    request_id = os.environ["GITHUB_RUN_ID"] + "-" + os.environ["GITHUB_RUN_ATTEMPT"]
    sha = os.environ["TARGET_SHA"]
    mode = os.environ["DEPLOY_MODE"]
    result = {"status": "unknown", "target_sha": sha, "request_id": request_id,
              "reason": "Inspect VPS status before any retry; SSH submission is not proof of deployment."}

    def save(value):
        result_file.write_text(json.dumps(value, indent=2) + "\n")
        with (report / "execution.jsonl").open("a") as out:
            out.write(json.dumps(value) + "\n")

    save(result)  # Survives cancellation after submission.
    assert re.fullmatch(r"[0-9a-f]{40}", sha) and mode in ("check", "deploy")
    assert os.environ["GITHUB_REF"] == "refs/heads/main" and os.environ["GITHUB_SHA"] == sha
    assert re.fullmatch(r"[0-9]{1,20}-[0-9]{1,5}", request_id)
    host, port, user = (os.environ[x] for x in ("DEPLOY_HOST", "DEPLOY_PORT", "DEPLOY_USER"))
    assert re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9.-]{0,252}", host), "Invalid SSH host"
    assert port.isdigit() and 0 < int(port) < 65536, "Invalid SSH port"
    assert re.fullmatch(r"[a-z_][a-z0-9_-]{0,31}", user) and user != "root", "Dedicated SSH user required"
    private_key = os.environ.pop("DEPLOY_SSH_PRIVATE_KEY")
    known_hosts = os.environ.pop("DEPLOY_SSH_KNOWN_HOSTS")
    assert private_key.strip() and known_hosts.strip(), "Environment SSH secrets are missing"
    with tempfile.TemporaryDirectory(prefix="exchange-cd-", dir=os.environ["RUNNER_TEMP"]) as directory:
        os.chmod(directory, 0o700)
        identity = Path(directory) / "identity"
        hosts = Path(directory) / "known_hosts"
        for path, value in ((identity, private_key), (hosts, known_hosts)):
            path.write_text(value.rstrip() + "\n")
            os.chmod(path, 0o600)
        del private_key, known_hosts
        ssh = ["ssh", "-F", "/dev/null", "-T", "-p", port, "-i", str(identity),
               "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes",
               "-o", "UserKnownHostsFile=" + str(hosts), "-o", "GlobalKnownHostsFile=/dev/null",
               "-o", "ConnectTimeout=15", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3",
               "-o", "ForwardAgent=no", "-o", "ClearAllForwardings=yes", "-o", "LogLevel=ERROR",
               user + "@" + host]

        def request(action):
            completed = subprocess.run(ssh + [f"{action} {sha} {request_id}"],
                                       capture_output=True, text=True, timeout=240)
            # Deliberately do not echo SSH stderr, key paths or raw non-JSON output.
            data = json.loads(completed.stdout)
            assert data.get("target_sha") == sha and data.get("request_id") == request_id
            save(data)
            assert completed.returncode == 0 or data.get("status") == "failed", "SSH request failed; inspect saved report"
            return data

        try:
            result = request(mode)
            deadline = time.monotonic() + 45 * 60
            while result["status"] == "running" and time.monotonic() < deadline:
                time.sleep(10)
                result = request("status")
            if result["status"] == "running":
                raise TimeoutError()
        except Exception:
            # Read once for diagnostics; never send deploy again, even if the
            # original command was accepted but its response was lost.
            try:
                request("status")
            except Exception:
                pass
            result = {"status": "unknown", "target_sha": sha, "request_id": request_id,
                      "reason": "SSH lost/failed or timeout; inspect backups/deploy and actual server state before retry."}
            save(result)
            print("Production status UNKNOWN. Inspect server record; do not repeat deployment.")
            return 1
    success = result["status"] == "success" and result.get("exit_code") == 0 and \
        (mode == "check" or result.get("official_exit_code") == 0)
    if result["status"] == "success" and not success:
        result.update(status="unknown", reason="inconsistent-final-exit-code:inspect-server-before-retry")
        save(result)
    print("Production " + mode + ": " + result["status"])
    return 0 if success else 1


if __name__ == "__main__":
    raise SystemExit(run())
