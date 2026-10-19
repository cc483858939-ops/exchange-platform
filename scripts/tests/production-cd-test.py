#!/usr/bin/env python3
"""Offline security and contract tests. No SSH, Docker or production access."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import unittest
from unittest.mock import patch, Mock

ROOT = Path(__file__).resolve().parents[2]


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / "scripts/cd" / filename)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


server = module("server", "server.py")
ci = module("ci", "verify-ci.py")
client = module("client", "ssh-client.py")
SHA = "a" * 40
OLD = "b" * 40


class CITest(unittest.TestCase):
    def setUp(self):
        self.run = dict(head_sha=SHA, head_branch="main", workflow_id=123,
                        event="push", status="completed", conclusion="success")
        self.jobs = [dict(name=name, status="completed", conclusion="success",
                          steps=[dict(name=step, conclusion="success") for step in steps])
                     for name, steps in ci.REQUIRED.items()]

    def test_success(self):
        ci.validate(self.run, self.jobs, SHA, 123)

    def test_fail_closed(self):
        for key, value in (("head_sha", OLD), ("head_branch", "feature"), ("workflow_id", 456),
                           ("event", "pull_request"), ("status", "in_progress"), ("conclusion", "failure")):
            with self.subTest(key=key), self.assertRaises(AssertionError):
                ci.validate(dict(self.run, **{key: value}), self.jobs, SHA, 123)

    def test_missing_skipped_and_duplicate_jobs(self):
        for jobs in (self.jobs[1:], self.jobs + [self.jobs[0]]):
            with self.assertRaises(AssertionError):
                ci.validate(self.run, jobs, SHA, 123)
        for conclusion in ("skipped", "failure", "cancelled"):
            jobs = copy.deepcopy(self.jobs)
            jobs[0]["conclusion"] = conclusion
            with self.assertRaises(AssertionError):
                ci.validate(self.run, jobs, SHA, 123)

    def test_skipped_required_step(self):
        self.jobs[1]["steps"][0]["conclusion"] = "skipped"
        with self.assertRaises(AssertionError):
            ci.validate(self.run, self.jobs, SHA, 123)

    def test_required_names_exist_in_existing_ci(self):
        source = (ROOT / ".github/workflows/ci.yml").read_text()
        for name, steps in ci.REQUIRED.items():
            for text in [name, *steps]:
                self.assertIn("name: " + text, source)


class ServerTest(unittest.TestCase):
    def test_parser_rejects_shell_and_paths(self):
        self.assertTrue(server.REQUEST.fullmatch(f"deploy {SHA} 123-1"))
        for text in (f"deploy {SHA} 123-1; id", f"deploy {SHA} 123-1\n", "bash -s",
                     f"ACK-COMPOSE-APPLIED {SHA} 123-1", f"deploy main 123-1",
                     f"status {SHA} ../../x", f"deploy {SHA} 123-1 extra"):
            self.assertFalse(server.REQUEST.fullmatch(text), text)

    def test_check_is_readonly_and_reports_upgrade(self):
        record = {}
        calls = []
        with patch.object(server, "assert_target"), patch.object(server, "official", return_value={
                "target_commit": SHA, "compose_status": "up-to-date"}) as official, \
                patch.object(server, "git", side_effect=lambda *args: calls.append(args) or (OLD if args == ("rev-parse", "HEAD") else "")), \
                patch.object(server, "state", return_value=OLD), patch.object(server, "schema_contract", return_value={"current": 20, "floor": 20}), \
                patch.object(server, "snapshot", return_value={"schema": 19}), patch.object(server, "health", return_value={"api": "healthy"}):
            server.preflight(SHA, record)
        official.assert_called_once_with("CHECK", SHA, record)
        self.assertTrue(record["migration_required"])
        self.assertFalse(any(args[0] == "merge" for args in calls))

    def test_compose_pending_stops_before_deploy_and_shows_diff(self):
        record = {}
        with patch.object(server, "assert_target"), patch.object(server, "official", return_value={
                "target_commit": SHA, "compose_status": "pending"}) as official, \
                patch.object(server, "git", return_value=OLD) as git, patch.object(server, "state", return_value=OLD), \
                patch.object(server, "schema_contract", return_value={"current": 20, "floor": 20}), patch.object(server, "snapshot", return_value={"schema": 19}), \
                patch.object(server, "health", return_value={}), patch.object(server, "compose_diff", return_value="review this diff"):
            with self.assertRaisesRegex(server.GateError, "compose-pending"):
                server.deploy(SHA, record)
        self.assertEqual([c.args[0] for c in official.call_args_list], ["CHECK"])
        self.assertFalse(any(c.args[0] == "merge" for c in git.call_args_list))
        self.assertEqual(record["compose_diff"], "review this diff")

    def test_final_counts_cannot_decrease(self):
        counts = dict(schema=20, floor=20, accounts=20, mirror_posts=655, posts=655, media=685, mirror_media=685)
        for decreased in (None, "accounts", "mirror_posts", "posts", "media", "mirror_media", "schema", "floor"):
            record = {"before": counts.copy(), "target_schema": 20, "target_floor": 20, "request_id": "123-1"}
            after = counts.copy()
            if decreased:
                after[decreased] -= 1
            def git(*args):
                if args[0] == "show":
                    return "DEPLOY_EXPECTED_SHA"
                if args[0] == "rev-parse":
                    return SHA
                return ""
            with self.subTest(decreased=decreased), patch.object(server, "preflight"), \
                    patch.object(server, "assert_target"), patch.object(server, "git", side_effect=git), \
                    patch.object(server, "official"), patch.object(server, "write_json"), patch.object(server, "state", return_value=SHA), \
                    patch.object(server, "snapshot", return_value=after), patch.object(server, "health", return_value={}):
                if decreased:
                    with self.assertRaises(server.GateError):
                        server.deploy(SHA, record)
                else:
                    server.deploy(SHA, record)

    def test_failed_deploy_retains_unresolved_marker(self):
        def failure(sha, record):
            record["mutation_started"] = True
            raise server.GateError("official-script-failed:DEPLOY-PRODUCTION")
        with tempfile.TemporaryDirectory() as directory, patch.object(server, "LOGS", Path(directory)), \
                patch.object(server, "deploy", side_effect=failure):
            active = Path(directory) / "active.json"
            active.write_text("{}")
            result = server.execute("deploy", SHA, "123-1")
            self.assertEqual(result["status"], "failed")
            self.assertEqual(result["exit_code"], 1)
            self.assertTrue(active.exists())
            self.assertEqual(json.loads((Path(directory) / "123-1.json").read_text())["target_sha"], SHA)
            if os.name == "posix":
                self.assertEqual((Path(directory) / "123-1.json").stat().st_mode & 0o777, 0o600)

    def test_official_failure_and_secret_output_are_not_success(self):
        process = Mock(stdout=iter(["PASSWORD=private\n", "DATABASE_DSN=private\n", "token leaked here\n",
                                  "compose_status=pending\n", "message=private\n"]), wait=Mock(return_value=2))
        record = {"request_id": "123-1"}
        with patch.object(server.subprocess, "Popen", return_value=process), patch.object(server, "write_json"):
            with self.assertRaises(server.GateError):
                server.official("DEPLOY-PRODUCTION", SHA, record)
        self.assertNotIn("private", json.dumps(record))
        self.assertEqual(record["official_exit_code"], 2)
        self.assertEqual(record["official"], [{"compose_status": "pending"}])

    def test_remote_advance_blocks_before_fetch(self):
        def git(*args):
            return {"branch": "main", "status": "", "remote": "https://github.com/cc483858939-ops/exchange-platform.git",
                    "ls-remote": OLD + "\trefs/heads/main"}[args[0]]
        with patch.object(server, "git", side_effect=git) as mock:
            with self.assertRaisesRegex(server.GateError, "remote-main-advanced"):
                server.assert_target(SHA)
        self.assertFalse(any(c.args[0] == "fetch" for c in mock.call_args_list))

    def test_diff_redacts_credentials(self):
        with patch.object(server, "git", return_value="+ ACCESS_KEY: private\n+ DATABASE_DSN: private\n+ replicas: 2"):
            text = server.compose_diff(OLD, SHA)
        self.assertNotIn("private", text)
        self.assertIn("replicas: 2", text)

    def test_preflight_failure_does_not_leave_deploy_blocked(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(server, "LOGS", Path(directory)), \
                patch.object(server, "deploy", side_effect=server.GateError("compose-pending")):
            active = Path(directory) / "active.json"
            active.write_text("{}")
            result = server.execute("deploy", SHA, "123-1")
            self.assertEqual(result["status"], "failed")
            self.assertFalse(active.exists())

    def test_schema_contract_includes_published_floor(self):
        with patch.object(server, "git", return_value=(ROOT / "Go.exchange/initialize/runtime_schema.go").read_text()):
            self.assertEqual(server.schema_contract(SHA), {"required": 20, "current": 20, "floor": 20})


@unittest.skipUnless(os.name == "posix" and getattr(os, "geteuid", lambda: -1)() == 0,
                     "Linux root-only forced-command lifecycle test; run with sudo on a disposable local environment")
class GatewayLifecycleTest(unittest.TestCase):
    def test_detached_worker_lock_status_and_replay(self):
        # A copy of the real gateway uses temporary paths and a harmless deploy
        # fixture. No production Git/Docker/SSH commands can be reached here.
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            gateway = root / "server.py"
            source = (ROOT / "scripts/cd/server.py").read_text()
            source = source.replace('ROOT = Path("/srv/exchange-platform")', 'ROOT = Path(' + repr(directory) + ')')
            source = source.replace('INSTALLED = "/usr/local/lib/exchange-platform-cd/server.py"', 'INSTALLED = ' + repr(str(gateway)))
            source = source.replace('LOCK = "/run/lock/exchange-platform-cd.lock"', 'LOCK = ' + repr(str(root / "lock")))
            fixture = '''
def deploy(sha, record):
    import time
    record["mutation_started"] = True
    write_json(LOGS / (record["request_id"] + ".json"), record)
    time.sleep(0.5)
    record["official_exit_code"] = 0
'''
            source = source.replace('if __name__ == "__main__":', fixture + '\nif __name__ == "__main__":')
            gateway.write_text(source)
            def request(mode, request_id="123-1"):
                return subprocess.run(["/usr/bin/python3", str(gateway), f"{mode} {SHA} {request_id}"],
                                      capture_output=True, text=True, timeout=5)
            started = request("deploy")
            self.assertEqual(started.returncode, 0, started.stderr)
            self.assertEqual(json.loads(started.stdout)["status"], "running")
            # SSH-equivalent parent process has exited; detached worker persists.
            blocked = request("deploy", "124-1")
            self.assertNotEqual(blocked.returncode, 0)
            deadline = time.monotonic() + 5
            while True:
                status = json.loads(request("status").stdout)
                if status["status"] != "running" or time.monotonic() > deadline:
                    break
                time.sleep(0.05)
            self.assertEqual(status["status"], "success")
            self.assertEqual(status["exit_code"], 0)
            self.assertEqual((root / "backups/deploy/123-1.json").stat().st_mode & 0o777, 0o600)
            self.assertNotEqual(request("deploy").returncode, 0, "must not replay a request")
            self.assertFalse((root / "backups/deploy/active.json").exists())


class ClientTest(unittest.TestCase):
    def test_completed_check_failure_stays_failed_and_success_requires_exit_zero(self):
        for status, exit_code, ssh_rc, expected in (("failed", 1, 1, 1), ("success", 0, 0, 0), ("success", 1, 0, 1)):
            with self.subTest(status=status, exit_code=exit_code), tempfile.TemporaryDirectory() as directory:
                original = Path.cwd()
                os.chdir(directory)
                try:
                    env = {"GITHUB_RUN_ID": "123", "GITHUB_RUN_ATTEMPT": "1", "TARGET_SHA": SHA,
                           "GITHUB_REF": "refs/heads/main", "GITHUB_SHA": SHA, "DEPLOY_MODE": "check",
                           "DEPLOY_HOST": "example.org", "DEPLOY_PORT": "22", "DEPLOY_USER": "exchange-deploy",
                           "DEPLOY_SSH_PRIVATE_KEY": "MOCK-PRIVATE", "DEPLOY_SSH_KNOWN_HOSTS": "MOCK-HOST", "RUNNER_TEMP": directory}
                    record = {"status": status, "target_sha": SHA, "request_id": "123-1", "exit_code": exit_code}
                    response = Mock(returncode=ssh_rc, stdout=json.dumps(record))
                    with patch.dict(os.environ, env), patch.object(client.subprocess, "run", return_value=response) as ssh:
                        self.assertEqual(client.run(), expected)
                    self.assertEqual(ssh.call_count, 1)
                    expected_status = "unknown" if status == "success" and exit_code else status
                    self.assertEqual(json.loads(Path("cd-report/result.json").read_text())["status"], expected_status)
                    command = ssh.call_args.args[0]
                    self.assertIn("StrictHostKeyChecking=yes", command)
                    identity = Path(command[command.index("-i") + 1])
                    self.assertFalse(identity.exists(), "temporary private key must be removed")
                finally:
                    os.chdir(original)

    def test_lost_ssh_never_resubmits_deploy(self):
        with tempfile.TemporaryDirectory() as directory:
            original = Path.cwd()
            os.chdir(directory)
            try:
                env = {"GITHUB_RUN_ID": "123", "GITHUB_RUN_ATTEMPT": "1", "TARGET_SHA": SHA,
                       "GITHUB_REF": "refs/heads/main", "GITHUB_SHA": SHA, "DEPLOY_MODE": "deploy",
                       "DEPLOY_HOST": "example.org", "DEPLOY_PORT": "22", "DEPLOY_USER": "exchange-deploy",
                       "DEPLOY_SSH_PRIVATE_KEY": "MOCK-PRIVATE", "DEPLOY_SSH_KNOWN_HOSTS": "MOCK-HOST", "RUNNER_TEMP": directory}
                with patch.dict(os.environ, env), patch.object(client.subprocess, "run", side_effect=subprocess.TimeoutExpired("ssh", 240)) as ssh:
                    self.assertEqual(client.run(), 1)
                actions = [call.args[0][-1].split()[0] for call in ssh.call_args_list]
                self.assertEqual(actions, ["deploy", "status"])
                self.assertEqual(json.loads(Path("cd-report/result.json").read_text())["status"], "unknown")
                self.assertNotIn("MOCK-PRIVATE", Path("cd-report/execution.jsonl").read_text())
            finally:
                os.chdir(original)


if __name__ == "__main__":
    unittest.main()
