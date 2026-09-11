"""Offline regression checks. Run with Python 3 on Linux (including WSL)."""

import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import tempfile
import time
import unittest


ROOT = Path(__file__).resolve().parents[1]
BASH = shutil.which("bash")


class RepositoryTests(unittest.TestCase):
    def test_shell_files_are_plain_lf_scripts(self):
        for path in (ROOT / "scripts").glob("*.sh"):
            data = path.read_bytes()
            self.assertTrue(data.startswith(b"#!/usr/bin/env bash\n"), path)
            self.assertNotIn(b"\r", data, path)
            self.assertNotIn(b"```", data, path)

    def test_local_documentation_links(self):
        for path in [ROOT / "README.md", *sorted((ROOT / "docs").glob("*.md"))]:
            content = path.read_text()
            for target in re.findall(r"\[[^\]]+\]\(([^)]+)\)", content):
                if "://" in target:
                    continue
                filename, _, anchor = target.partition("#")
                linked = path.parent / filename if filename else path
                self.assertTrue(linked.is_file(), (path, target))
                if anchor:
                    headings = re.findall(r"^#+ (.+)$", linked.read_text(), re.M)
                    slugs = [re.sub(r"[^\w\- ]", "", h.lower()).replace(" ", "-")
                             for h in headings]
                    self.assertIn(anchor, slugs, (path, target))


@unittest.skipUnless(os.name == "posix" and BASH, "Linux Bash is required; use WSL on Windows")
class ScriptTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="runner-script-tests-")
        self.addCleanup(self.temp.cleanup)
        self.work = Path(self.temp.name)
        self.bin = self.work / "bin"
        self.bin.mkdir()
        self.env = os.environ.copy()
        for name in ("ORG_NAME", "ACCESS_TOKEN", "RUNNER_NAME", "RUNNER_NAME_PREFIX",
                     "RUNNER_LABELS", "RUNNER_WORKDIR", "COMPOSE_FILE", "BASH_ENV"):
            self.env.pop(name, None)
        self.env.update(ORG_NAME="test-org", ACCESS_TOKEN="dummy-secret",
                        PATH=f"{self.bin}:{self.env['PATH']}", TEST_LOG=str(self.work / "calls"))
        self.write(self.bin / "uuidgen", "echo ABCDEF12-3456-7890-ABCD-123456789012\n")
        self.write(self.bin / "curl", '''
case "${*: -1}" in
  */registration-token)
    [[ "${FAIL_REGISTER:-0}" == 1 ]] && exit 22
    [[ "${EMPTY_TOKEN:-0}" == 1 ]] && { echo '{"token":null}'; exit 0; }
    echo '{"token":"registration-test-token"}' ;;
  */remove-token)
    [[ "${FAIL_REMOVE_TOKEN:-0}" == 1 ]] && exit 22
    echo '{"token":"removal-test-token"}' ;;
  *) exit 99 ;;
esac
''')
        self.write(self.bin / "jq", '''
read -r value
case "$value" in
  '{"token":"registration-test-token"}') echo registration-test-token ;;
  '{"token":"removal-test-token"}') echo removal-test-token ;;
  *) exit 1 ;;
esac
''')
        self.write(self.work / "config.sh", '''
printf '%s\\n' "$*" >> "$TEST_LOG"
if [[ "${1:-}" == remove ]]; then
  [[ "${FAIL_REMOVE_CONFIG:-0}" == 1 ]] && exit 1
  rm -f .runner
else
  touch .runner
fi
''')
        self.write(self.work / "run.sh", '''
echo run >> "$TEST_LOG"
if [[ "${WAIT_FOR_SIGNAL:-0}" == 1 ]]; then
  trap 'echo terminated >> "$TEST_LOG"; exit 0' TERM
  touch ready
  while :; do sleep 0.1; done
fi
exit "${CHILD_EXIT:-0}"
''')
        self.write(self.bin / "docker", '''
printf '%s|' "${DOCKER_GID:-unset}" "$@" >> "$TEST_LOG"
printf '\\n' >> "$TEST_LOG"
''')

    def write(self, path, body):
        path.write_text("#!/usr/bin/env bash\nset -eu\n" + body)
        path.chmod(0o755)

    def entrypoint(self, **overrides):
        return subprocess.run([BASH, str(ROOT / "scripts/entrypoint.sh")],
                              cwd=self.work, env={**self.env, **overrides},
                              capture_output=True, text=True, timeout=10)

    def calls(self):
        path = self.work / "calls"
        return path.read_text() if path.exists() else ""

    def test_required_configuration(self):
        for missing in ("ORG_NAME", "ACCESS_TOKEN"):
            with self.subTest(missing=missing):
                result = self.entrypoint(**{missing: ""})
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(missing, result.stderr)
                self.assertEqual(self.calls(), "")

    def test_failed_registration_and_empty_token(self):
        for setting in ("FAIL_REGISTER", "EMPTY_TOKEN"):
            with self.subTest(setting=setting):
                result = self.entrypoint(**{setting: "1"})
                self.assertEqual(result.returncode, 1)
                self.assertIn("Failed to obtain runner registration token", result.stderr)
                self.assertEqual(self.calls(), "")

    def test_default_name_and_normal_exit(self):
        result = self.entrypoint()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("--name wbg-worker-abcdef12", self.calls())
        self.assertIn("--labels self-hosted,linux,x64 --work _work", self.calls())
        self.assertIn("remove --unattended --token removal-test-token", self.calls())
        self.assertFalse((self.work / ".runner").exists())
        self.assertNotIn("dummy-secret", result.stdout + result.stderr)

    def test_overrides_and_exit_code(self):
        result = self.entrypoint(RUNNER_NAME="exact-name", RUNNER_NAME_PREFIX="ignored",
                                 RUNNER_LABELS="custom", RUNNER_WORKDIR="jobs", CHILD_EXIT="7")
        self.assertEqual(result.returncode, 7)
        self.assertIn("--name exact-name --labels custom --work jobs", self.calls())
        self.assertIn("remove --unattended", self.calls())

    def test_prefix_override(self):
        result = self.entrypoint(RUNNER_NAME_PREFIX="team")
        self.assertEqual(result.returncode, 0)
        self.assertIn("--name team-abcdef12", self.calls())

    def test_removal_failure_preserves_child_exit(self):
        for failure in ("FAIL_REMOVE_TOKEN", "FAIL_REMOVE_CONFIG"):
            with self.subTest(failure=failure):
                result = self.entrypoint(**{failure: "1", "CHILD_EXIT": "7"})
                self.assertEqual(result.returncode, 7)
                self.assertIn("[WARN]", result.stderr)
                self.assertTrue((self.work / ".runner").exists())

    def test_stale_registration_is_removed_before_configuring(self):
        (self.work / ".runner").touch()
        result = self.entrypoint()
        self.assertEqual(result.returncode, 0)
        self.assertTrue(self.calls().startswith("remove --unattended"))
        self.assertEqual(self.calls().count("remove --unattended"), 2)

    def test_signal_shutdown(self):
        process = subprocess.Popen([BASH, str(ROOT / "scripts/entrypoint.sh")],
                                   cwd=self.work, env={**self.env, "WAIT_FOR_SIGNAL": "1"},
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
                                   start_new_session=True)
        try:
            deadline = time.monotonic() + 5
            while not (self.work / "ready").exists() and time.monotonic() < deadline:
                if process.poll() is not None:
                    break
                time.sleep(0.02)
            self.assertTrue((self.work / "ready").exists(), "runner did not start")
            process.send_signal(signal.SIGTERM)
            stdout, stderr = process.communicate(timeout=5)
            self.assertEqual(process.returncode, 0, stderr)
            self.assertIn("Shutdown complete", stdout)
            self.assertIn("terminated", self.calls())
            self.assertEqual(self.calls().count("remove --unattended"), 1)
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
                process.communicate()

    def control(self, command, cwd=None, fail_gid=False, **env):
        # Source the production functions; replace only host socket discovery.
        code = 'source "$1"; get_docker_gid() { echo 123; }; main "$2"'
        if fail_gid:
            code = 'source "$1"; get_docker_gid() { return 1; }; main "$2"'
        return subprocess.run([BASH, "-c", code, "test", str(ROOT / "scripts/ghrctl.sh"), command],
                              cwd=cwd or self.work, env={**self.env, **env},
                              capture_output=True, text=True, timeout=5)

    def test_control_resolves_paths_from_root_and_elsewhere(self):
        for cwd in (ROOT, self.work):
            with self.subTest(cwd=cwd):
                self.assertEqual(self.control("status", cwd=cwd).returncode, 0)
        for call in self.calls().splitlines():
            parts = call.split("|")
            self.assertEqual(parts[:3], ["123", "compose", "-f"])
            self.assertEqual(Path(parts[3]).resolve(), ROOT / "docker/docker-compose.yaml")
            self.assertEqual(parts[4], "ps")

    def test_compose_override(self):
        result = self.control("status", COMPOSE_FILE="alternate.yaml")
        self.assertEqual(result.returncode, 0)
        self.assertIn("|-f|alternate.yaml|ps|", self.calls())

    def test_control_command_dispatch(self):
        expected = {"start": ["up|-d|"], "stop": ["down|"],
                    "restart": ["down|", "up|-d|"], "status": ["ps|"],
                    "logs": ["logs|--follow|--tail=200|github-runner|"],
                    "pull": ["pull|github-runner|"]}
        for command, suffixes in expected.items():
            with self.subTest(command=command):
                (self.work / "calls").write_text("")
                result = self.control(command)
                self.assertEqual(result.returncode, 0, result.stderr)
                lines = self.calls().splitlines()
                self.assertEqual(len(lines), len(suffixes))
                for line, suffix in zip(lines, suffixes):
                    self.assertTrue(line.endswith(suffix), line)

    def test_failed_socket_discovery_does_not_invoke_docker(self):
        result = self.control("start", fail_gid=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.calls(), "")

    def test_direct_invocation_dispatches_and_rejects_unknown_command(self):
        result = subprocess.run([BASH, str(ROOT / "scripts/ghrctl.sh"), "invalid"],
                                cwd=self.work, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 1)
        self.assertIn("Usage:", result.stdout)
        self.assertEqual(self.calls(), "")


if __name__ == "__main__":
    unittest.main(verbosity=2)
