#!/usr/bin/env python3
"""Offline security and contract tests. No SSH, Docker or production access."""
import copy
import contextlib
import io
import importlib.util
import json
import os
from pathlib import Path
import shutil
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


class GitWorkspaceTest(unittest.TestCase):
    """Real Git fixtures; all commits/resets are confined to a disposable repo."""
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.git_executable = shutil.which("git")
        self.git("init", "-q", "-b", "main")
        self.git("config", "user.name", "offline-test")
        self.git("config", "user.email", "test@example.invalid")
        self.git("config", "core.autocrlf", "false")
        (self.root / ".gitignore").write_text("ignored/\nbackups/\n*.bin\n")
        (self.root / "stable.txt").write_text("original\n")
        (self.root / "replace").mkdir()
        (self.root / "replace/old.txt").write_text("old\n")
        self.git("add", ".")
        self.git("commit", "-qm", "base")
        self.base = self.git("rev-parse", "HEAD")
        self.git("rm", "-qr", "replace")
        (self.root / "replace").write_text("replacement\n")
        (self.root / "incoming.txt").write_text("new\n")
        (self.root / " leading.txt").write_text("new\n")
        (self.root / "newdir/sub").mkdir(parents=True)
        (self.root / "newdir/sub/new.txt").write_text("new\n")
        (self.root / "ignored").mkdir()
        (self.root / "ignored/release.txt").write_text("new\n")
        self.git("add", ".")
        self.git("add", "-f", "ignored/release.txt")
        self.git("commit", "-qm", "target")
        self.target = self.git("rev-parse", "HEAD")
        self.git("reset", "--hard", self.base)
        self.logs = self.root / "backups/deploy"
        self.logs.mkdir(parents=True, mode=0o700)
        for patcher in (patch.object(server, "ROOT", self.root), patch.object(server, "LOGS", self.logs),
                        patch.object(server, "git", side_effect=self.gateway_git),
                        patch.object(server, "state", return_value=self.base)):
            patcher.start()
            self.addCleanup(patcher.stop)

    def git(self, *args):
        result = subprocess.run([self.git_executable, "-C", str(self.root), *args],
                                capture_output=True, text=True, encoding="utf-8", check=True)
        return result.stdout.rstrip("\n")

    def gateway_git(self, *args):
        # Stub remote transport only: status, diff, ls-files and ancestry all
        # execute real Git. No offline test can contact a production remote.
        if args == ("remote", "get-url", "origin"):
            return "https://github.com/cc483858939-ops/exchange-platform.git"
        if args[0] == "ls-remote":
            return self.target + "\trefs/heads/main"
        if args[0] == "fetch":
            self.git("update-ref", "refs/remotes/origin/main", self.target)
            return ""
        return self.git(*args)

    def test_check_preserves_safe_untracked_and_ignored_files(self):
        files = {"notes/operator.txt": "local notes", "ignored/local.bin": "local artifact",
                 " leading local.txt": "leading space", "newdir/local.txt": "unrelated neighbor"}
        for name, value in files.items():
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(value)
        with patch.object(server, "official", return_value={"target_commit": self.target, "compose_status": "up-to-date"}) as official, \
                patch.object(server, "schema_contract", return_value={"current": 20, "floor": 20}), \
                patch.object(server, "snapshot", return_value={"schema": 20}), patch.object(server, "health", return_value={}):
            result = server.execute("check", self.target, "321-1")
        self.assertEqual(result["status"], "success", result)
        official.assert_called_once_with("CHECK", self.target, result)
        self.assertEqual(self.git("rev-parse", "HEAD"), self.base)
        for name, value in files.items():
            self.assertEqual((self.root / name).read_text(), value)
        self.assertEqual(list(self.logs.glob("*.log")), [], "check does not create raw diagnostics")

    def test_unstaged_tracked_change_blocks_before_official_deploy(self):
        path = self.root / "stable.txt"
        path.write_text("local tracked change")
        with patch.object(server, "official") as official, self.assertRaisesRegex(server.GateError, "tracked-working-tree"):
            server.deploy(self.target, {})
        official.assert_not_called()
        self.assertEqual(path.read_text(), "local tracked change")
        self.assertEqual(self.git("rev-parse", "HEAD"), self.base)

    def test_staged_tracked_change_is_rejected(self):
        (self.root / "stable.txt").write_text("staged")
        self.git("add", "stable.txt")
        with self.assertRaisesRegex(server.GateError, "tracked-working-tree"):
            server.assert_target(self.target)
        self.assertIn("stable.txt", self.git("diff", "--cached", "--name-only"))

    def test_conflicting_untracked_files_are_rejected_without_cleanup(self):
        for name in ("incoming.txt", " leading.txt", "newdir", "replace/local.bin", "ignored/release.txt"):
            with self.subTest(name=name):
                path = self.root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("keep me")
                try:
                    with self.assertRaisesRegex(server.GateError, "untracked-path-conflicts"):
                        server.assert_target(self.target)
                    self.assertEqual(path.read_text(), "keep me")
                    self.assertEqual(self.git("rev-parse", "HEAD"), self.base)
                finally:
                    # This is fixture cleanup only; the gateway never removes it.
                    path.unlink()

    @unittest.skipUnless(os.name == "posix", "POSIX symlink fixture")
    def test_untracked_symlink_ancestor_is_rejected(self):
        with tempfile.TemporaryDirectory() as outside:
            (Path(outside) / "keep.txt").write_text("outside untouched")
            (self.root / "newdir").symlink_to(outside, target_is_directory=True)
            with self.assertRaisesRegex(server.GateError, "untracked-path-conflicts"):
                server.assert_target(self.target)
            self.assertEqual((Path(outside) / "keep.txt").read_text(), "outside untouched")
            self.assertTrue((self.root / "newdir").is_symlink())

    def test_git_merge_rejects_conflicts_created_after_preflight(self):
        for name in ("incoming.txt", "ignored/release.txt"):
            with self.subTest(name=name):
                server.assert_target(self.target)
                path = self.root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("created after preflight")
                try:
                    with self.assertRaises(subprocess.CalledProcessError):
                        self.git("merge", "--ff-only", "--no-overwrite-ignore", self.target)
                    self.assertEqual(path.read_text(), "created after preflight")
                    self.assertEqual(self.git("rev-parse", "HEAD"), self.base)
                finally:
                    path.unlink()


class DiagnosticTest(unittest.TestCase):
    def test_full_official_failure_is_local_only_and_reports_stage(self):
        secret = "RAW_DIAGNOSTIC_SECRET_SENTINEL"
        def deploy(sha, record):
            record["mutation_started"] = True
            server.stage(record, "official-deploy")
            server.official("DEPLOY-PRODUCTION", sha, record)
        process = Mock(stdout=iter(["build details\n", "stderr secret=" + secret + "\n",
                                  "stage=initialization: migrate\n", "message=" + secret + "\n"]),
                       wait=Mock(return_value=7))
        with tempfile.TemporaryDirectory() as directory, patch.object(server, "LOGS", Path(directory)), \
                patch.object(server, "deploy", side_effect=deploy), patch.object(server.subprocess, "Popen", return_value=process), \
                contextlib.redirect_stdout(io.StringIO()) as stdout, contextlib.redirect_stderr(io.StringIO()) as stderr:
            (Path(directory) / "active.json").write_text("{}")
            result = server.execute("deploy", SHA, "321-1")
            raw = Path(result["diagnostic_log"])
            self.assertIn(secret, raw.read_text())
            self.assertIn("build details", raw.read_text())
            self.assertIn("official_exit_code=7", raw.read_text())
            self.assertIn("target_sha=" + SHA, raw.read_text())
            self.assertEqual(result["failure_stage"], "official:initialization: migrate")
            self.assertEqual(result["official_exit_code"], 7)
            self.assertNotIn(secret, json.dumps(result))
            self.assertNotIn(secret, (Path(directory) / "321-1.json").read_text())
            self.assertNotIn(secret, stdout.getvalue() + stderr.getvalue())
            self.assertTrue((Path(directory) / "active.json").exists())
            if os.name == "posix":
                self.assertEqual(raw.stat().st_mode & 0o777, 0o600)
                self.assertEqual(Path(directory).stat().st_mode & 0o777, 0o700)
                if os.geteuid() == 0:
                    self.assertEqual(raw.stat().st_uid, 0)

    def test_command_stdout_stderr_and_unexpected_traceback_stay_local(self):
        secret = "PRIVATE_STDERR_AND_TRACEBACK"
        def deploy(sha, record):
            server.stage(record, "git-fast-forward")
            server.command(["offline-command"])
            raise RuntimeError(secret)
        response = Mock(returncode=0, stdout="PRIVATE_STDOUT\n", stderr=secret)
        with tempfile.TemporaryDirectory() as directory, patch.object(server, "LOGS", Path(directory)), \
                patch.object(server, "deploy", side_effect=deploy), patch.object(server.subprocess, "run", return_value=response), \
                contextlib.redirect_stderr(io.StringIO()) as stderr:
            (Path(directory) / "active.json").write_text("{}")
            result = server.execute("deploy", SHA, "321-1")
            raw = Path(result["diagnostic_log"]).read_text()
            self.assertIn("PRIVATE_STDOUT", raw)
            self.assertIn(secret, raw)
            self.assertIn("Traceback", raw)
            self.assertEqual(result["failure_stage"], "git-fast-forward")
            self.assertEqual(result["status"], "unknown")
            self.assertNotIn(secret, json.dumps(result) + stderr.getvalue())

    def test_existing_diagnostic_is_never_overwritten(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(server, "LOGS", Path(directory)):
            path = Path(directory) / "321-1.diagnostic.log"
            path.write_text("previous evidence")
            with self.assertRaises(FileExistsError):
                with server.diagnostics("deploy", {"request_id": "321-1"}):
                    self.fail("existing log must stop the request")
            self.assertEqual(path.read_text(), "previous evidence")

    @unittest.skipUnless(os.name == "posix", "POSIX symlink fixture")
    def test_diagnostic_does_not_follow_symlinks(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(server, "LOGS", Path(directory)):
            victim = Path(directory) / "original"
            victim.write_text("do not overwrite")
            (Path(directory) / "321-1.diagnostic.log").symlink_to(victim)
            with self.assertRaises(FileExistsError):
                with server.diagnostics("deploy", {"request_id": "321-1"}):
                    self.fail("must not follow an existing log symlink")
            self.assertEqual(victim.read_text(), "do not overwrite")

    @unittest.skipUnless(os.name == "posix" and getattr(os, "geteuid", lambda: -1)() == 0,
                         "Linux root-only permission test")
    def test_non_root_cannot_read_complete_diagnostic(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(server, "LOGS", Path(directory)), \
                patch.object(server, "deploy"):
            active = Path(directory) / "active.json"
            active.write_text("{}")
            record = server.execute("deploy", SHA, "321-1")
            raw = Path(record["diagnostic_log"])
            self.assertEqual(raw.stat().st_uid, 0)
            denied = subprocess.run(["runuser", "-u", "nobody", "--", "cat", str(raw)], capture_output=True)
            self.assertNotEqual(denied.returncode, 0)
            self.assertEqual(denied.stdout, b"")


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
            (root / "scripts").mkdir()
            (root / "scripts/deploy-production.sh").write_text(
                "#!/bin/sh\nprintf 'RAW_LOCAL_ONLY_OUTPUT\\n'\n"
                "printf 'RAW_LOCAL_ONLY_STDERR\\n' >&2\nsleep 0.5\n")
            fixture = '''
def deploy(sha, record):
    record["mutation_started"] = True
    write_json(LOGS / (record["request_id"] + ".json"), record)
    stage(record, "official-deploy")
    official("DEPLOY-PRODUCTION", sha, record)
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
            raw = Path(status["diagnostic_log"])
            self.assertEqual(raw.stat().st_mode & 0o777, 0o600)
            self.assertIn("RAW_LOCAL_ONLY_OUTPUT", raw.read_text())
            self.assertIn("RAW_LOCAL_ONLY_STDERR", raw.read_text())
            self.assertNotIn("RAW_LOCAL_ONLY", json.dumps(status) + started.stdout)
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
                    record.update(raw_log="RAW_LOG_NOT_FOR_ACTIONS", stdout="RAW_LOG_NOT_FOR_ACTIONS",
                                  stderr="RAW_LOG_NOT_FOR_ACTIONS", traceback="RAW_LOG_NOT_FOR_ACTIONS",
                                  diagnostic_log="/srv/exchange-platform/backups/deploy/123-1.diagnostic.log",
                                  failure_stage="official:backend image build")
                    response = Mock(returncode=ssh_rc, stdout=json.dumps(record))
                    with patch.dict(os.environ, env), patch.object(client.subprocess, "run", return_value=response) as ssh:
                        self.assertEqual(client.run(), expected)
                    self.assertEqual(ssh.call_count, 1)
                    expected_status = "unknown" if status == "success" and exit_code else status
                    self.assertEqual(json.loads(Path("cd-report/result.json").read_text())["status"], expected_status)
                    for artifact in Path("cd-report").iterdir():
                        self.assertNotIn("RAW_LOG_NOT_FOR_ACTIONS", artifact.read_text())
                    self.assertIn(record["diagnostic_log"], Path("cd-report/result.json").read_text())
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
