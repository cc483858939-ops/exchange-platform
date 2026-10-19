#!/usr/bin/env python3
"""Linux/root-only local regression. No SSH connections, listener or user creation."""
import os
from pathlib import Path
import pwd
import stat
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
FORCED = '/usr/bin/sudo -n /usr/local/sbin/exchange-production-cd "$SSH_ORIGINAL_COMMAND"'


class RestrictedSSHAccessTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if os.name != "posix" or os.geteuid() != 0:
            raise RuntimeError("Run SSH access regression in a disposable Linux root test environment")
        cls.reader = pwd.getpwnam("nobody")
        cls.sshd = os.environ.get("CD_TEST_SSHD", "/usr/sbin/sshd")
        cls.private = tempfile.TemporaryDirectory(prefix="cd-ssh-hostkey-")
        cls.hostkey = Path(cls.private.name) / "hostkey"
        subprocess.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(cls.hostkey)], check=True)
        cls.passwd = Path(cls.private.name) / "passwd"
        # Extracted sshd needs a privilege-separation identity even for -T.
        # The NSS fixture applies only to the parser subprocess, never /etc/passwd.
        cls.passwd.write_text(Path("/etc/passwd").read_text() +
                              "sshd:x:65530:65534:offline fixture:/run/sshd:/usr/sbin/nologin\n")

    @classmethod
    def tearDownClass(cls):
        cls.private.cleanup()

    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="cd-ssh-permissions-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        os.chmod(self.root, 0o755)
        self.home = self.root / "home"
        self.home.mkdir(mode=0o755)
        self.ssh = self.home / ".ssh"
        self.ssh.mkdir(mode=0o750)
        os.chown(self.ssh, 0, self.reader.pw_gid)
        os.chmod(self.ssh, 0o750)
        self.authorized = self.ssh / "authorized_keys"
        command = FORCED.replace('"', '\\"')
        self.keyline = f'restrict,command="{command}" ' + self.hostkey.with_suffix(".pub").read_text()
        self.authorized.write_text(self.keyline)
        os.chown(self.authorized, 0, self.reader.pw_gid)
        os.chmod(self.authorized, 0o640)

    def as_reader(self, *args):
        return subprocess.run(["runuser", "-u", self.reader.pw_name, "--", *args], capture_output=True, text=True)

    def effective(self, before="", after="", user="exchange-deploy", address="192.0.2.10", strict="yes"):
        config = self.root / "sshd_config"
        config.write_text(
            f"HostKey {self.hostkey}\nStrictModes {strict}\nUsePAM no\n"
            "AllowUsers admin exchange-deploy@192.0.2.0/24\n" + before +
            f"Include {ROOT / 'deploy/ssh/exchange-deploy.conf'}\n" + after)
        env = os.environ.copy()
        if env.get("CD_TEST_SSHD_LIBRARY_PATH"):
            env["LD_LIBRARY_PATH"] = env["CD_TEST_SSHD_LIBRARY_PATH"]
        if env.get("CD_TEST_NSS_WRAPPER"):
            env.update(LD_PRELOAD=env["CD_TEST_NSS_WRAPPER"], NSS_WRAPPER_PASSWD=str(self.passwd),
                       NSS_WRAPPER_GROUP="/etc/group")
        # -T/-C parse effective Include/Match policy only; sshd never listens.
        result = subprocess.run([self.sshd, "-T", "-f", str(config), "-C",
                                 f"user={user},host=runner.example,addr={address},laddr=127.0.0.1,lport=2222"],
                                capture_output=True, text=True, env=env)
        self.assertEqual(result.returncode, 0, result.stderr)
        settings = {}
        for line in result.stdout.splitlines():
            if " " not in line:
                continue
            name, value = line.split(" ", 1)
            if name in {"allowusers", "denyusers", "allowgroups", "denygroups"} and name in settings:
                settings[name] += " " + value
            else:
                settings[name] = value
        return settings

    def assert_restricted(self, settings):
        required = {"strictmodes": "yes", "authorizedkeysfile": ".ssh/authorized_keys",
                    "authorizedkeyscommand": "none", "trustedusercakeys": "none",
                    "authenticationmethods": "publickey", "pubkeyauthentication": "yes",
                    "passwordauthentication": "no", "kbdinteractiveauthentication": "no",
                    "disableforwarding": "yes", "permittty": "no", "permituserrc": "no",
                    "forcecommand": FORCED}
        for name, value in required.items():
            self.assertEqual(settings.get(name), value, name)
        self.assertIn("exchange-deploy@192.0.2.0/24", settings.get("allowusers", "").split())

    def test_group_readable_root_managed_authorization(self):
        for directory in (self.root, self.home, self.ssh):
            self.assertEqual(self.as_reader("test", "-x", str(directory)).returncode, 0, directory)
            self.assertEqual(directory.stat().st_uid, 0)
            self.assertEqual(stat.S_IMODE(directory.stat().st_mode) & 0o022, 0)
        read = self.as_reader("cat", str(self.authorized))
        self.assertEqual(read.returncode, 0, read.stderr)
        self.assertEqual(read.stdout, self.keyline)
        self.assertEqual(self.authorized.stat().st_uid, 0)
        self.assertEqual(self.authorized.stat().st_gid, self.reader.pw_gid)
        self.assertEqual(stat.S_IMODE(self.authorized.stat().st_mode), 0o640)

    def test_old_root_0700_and_0600_are_unreadable(self):
        os.chown(self.ssh, 0, 0)
        os.chown(self.authorized, 0, 0)
        os.chmod(self.ssh, 0o700)
        os.chmod(self.authorized, 0o600)
        self.assertNotEqual(self.as_reader("cat", str(self.authorized)).returncode, 0)
        os.chown(self.ssh, 0, self.reader.pw_gid)
        os.chmod(self.ssh, 0o750)
        self.assertNotEqual(self.as_reader("cat", str(self.authorized)).returncode, 0)
        os.chown(self.authorized, 0, self.reader.pw_gid)
        os.chmod(self.authorized, 0o640)
        self.assertEqual(self.as_reader("cat", str(self.authorized)).returncode, 0)

    def test_reader_cannot_write_replace_or_add_keys(self):
        commands = [
            ["python3", "-c", "import sys; open(sys.argv[1], 'a').write('forbidden')", str(self.authorized)],
            ["mv", str(self.authorized), str(self.ssh / "replaced")],
            ["touch", str(self.ssh / "authorized_keys2")],
            ["mv", str(self.ssh), str(self.home / "replaced-ssh")],
        ]
        for args in commands:
            with self.subTest(args=args[0]):
                self.assertNotEqual(self.as_reader(*args).returncode, 0)
        self.assertEqual(self.authorized.read_text(), self.keyline)
        self.assertTrue(self.ssh.is_dir())

    def test_effective_authorized_keys_and_strict_modes(self):
        self.assert_restricted(self.effective())
        with self.assertRaises(AssertionError):
            self.assert_restricted(self.effective(strict="no"))

    def test_existing_allowusers_must_include_deployment_identity(self):
        settings = self.effective()
        self.assertEqual(settings["allowusers"], "admin exchange-deploy@192.0.2.0/24")
        # -T exposes allow/deny lists; an operator must evaluate source patterns,
        # not infer login eligibility merely from sshd syntax being valid.
        denied = self.effective(after="DenyUsers exchange-deploy\n")
        self.assertEqual(denied["denyusers"], "exchange-deploy")

    def test_earlier_match_can_override_authorized_keys_file(self):
        settings = self.effective(before="Match User exchange-deploy\n AuthorizedKeysFile .ssh/other_keys\nMatch all\n")
        self.assertEqual(settings["authorizedkeysfile"], ".ssh/other_keys")
        with self.assertRaises(AssertionError):
            self.assert_restricted(settings)

    def test_source_address_match_is_evaluated_for_actual_client(self):
        before = "Match Address 198.51.100.0/24\n PasswordAuthentication yes\nMatch all\n"
        self.assert_restricted(self.effective(before=before, address="192.0.2.10"))
        with self.assertRaises(AssertionError):
            self.assert_restricted(self.effective(before=before, address="198.51.100.10"))

    def test_match_does_not_replace_administrator_policy(self):
        admin = self.effective(user="admin")
        self.assertEqual(admin["forcecommand"], "none")
        self.assertEqual(admin["permittty"], "yes")
        self.assertIn("admin", admin["allowusers"].split())

    def test_minimal_sudo_and_gateway_reject_arbitrary_commands(self):
        policy = self.root / "sudoers"
        policy.write_text("exchange-deploy ALL=(root) NOPASSWD: /usr/local/sbin/exchange-production-cd *\n")
        os.chmod(policy, 0o440)
        subprocess.run(["visudo", "-cf", str(policy)], capture_output=True, check=True)
        # Malformed requests are rejected before the gateway touches LOGS or ROOT.
        for request in ("", "id", "sh", "sudo id", "docker ps", "--worker", "bash -s",
                        "check " + "a" * 40 + " 123-1; id"):
            result = subprocess.run(["python3", str(ROOT / "scripts/cd/server.py"), request],
                                    capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0, request)
            self.assertEqual(result.stderr, "", "must not execute arbitrary shell content")
        for args in ([], ["first", "second"]):
            result = subprocess.run(["sh", str(ROOT / "scripts/cd/gateway.sh"), *args], capture_output=True)
            self.assertEqual(result.returncode, 2)

    def test_installation_document_matches_read_only_group_permissions(self):
        doc = (ROOT / "deploy/production-cd.md").read_text()
        self.assertIn("useradd --create-home --user-group --shell /bin/sh exchange-deploy", doc)
        self.assertIn("install -d -o root -g exchange-deploy -m 0750 /home/exchange-deploy/.ssh", doc)
        self.assertIn("install -o root -g exchange-deploy -m 0640", doc)
        self.assertIn('restrict,command="', doc)
        self.assertIn("StrictHostKeyChecking=yes", doc)
        self.assertIn("sshd -T -C", doc)
        self.assertIn("exchange-deploy ALL=(root) NOPASSWD: /usr/local/sbin/exchange-production-cd *", doc)
        self.assertNotIn("NOPASSWD: ALL", doc)


if __name__ == "__main__":
    unittest.main()
