"""Offline release safety tests; no GitHub or Docker writes."""

import importlib.util
import io
import json
import os
from pathlib import Path
import tempfile
import textwrap
import unittest
from unittest.mock import patch
import urllib.error


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("release", ROOT / "scripts/release.py")
release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release)
SHA = "a" * 40


class ReleaseTests(unittest.TestCase):
    def test_pr_title_workflow_checks_metadata_as_data(self):
        workflow = (ROOT / ".github/workflows/pr-title.yaml").read_text(encoding="utf-8")
        code = textwrap.dedent(workflow.split("python3 - <<'PY'\n", 1)[1].rsplit("          PY", 1)[0])
        with tempfile.TemporaryDirectory() as directory:
            event = Path(directory) / "event.json"
            for title, valid in (("feat: add a command", True), ("chore(main): release 0.1.0", True),
                                 ("fix(deps)!: update runtime", True), ("fix: $(exit 99)", True),
                                 ("Update code", False), ("feat: ", False),
                                 ("fix: first\nfeat: second", False)):
                event.write_text(json.dumps({"pull_request": {"title": title}}), encoding="utf-8")
                with self.subTest(title=title), patch.dict(os.environ, GITHUB_EVENT_PATH=str(event)):
                    if valid:
                        exec(compile(code, "pr-title-workflow", "exec"), {})
                    else:
                        with self.assertRaises(SystemExit):
                            exec(compile(code, "pr-title-workflow", "exec"), {})

    def test_versions_reject_options_prereleases_and_leading_zeroes(self):
        for version in ("v0.1.0", "v1.2.3"):
            release.validate_version(version)
        for version in ("--help", "v01.2.3", "v1.2", "v1.2.3-rc.1", "v1.2.3\n"):
            with self.subTest(version=version), self.assertRaises(ValueError):
                release.validate_version(version)

    def test_extract_only_requested_version_with_breaking_notes(self):
        content = "# Changelog\n\n## [0.2.0](https://example.org/compare) (2026-09-11)\n\n### ⚠ BREAKING CHANGES\n\n* migrate config\n\n### Features\n\n* new command\n\n## 0.1.0 (2026-09-10)\n\n* initial release\n"
        entry = release.changelog_entry(content, "v0.2.0")
        self.assertIn("migrate config", entry)
        self.assertNotIn("initial release", entry)
        self.assertIn("initial release", release.changelog_entry(content, "v0.1.0"))
        for bad in (content, "## 0.3.0\n", "## 0.3.0\n* a\n## 0.3.0\n* b\n"):
            with self.assertRaises(ValueError):
                release.changelog_entry(bad, "v0.3.0")

    def test_prepare_uses_checked_out_manifest_and_commit(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "docs").mkdir()
            (root / "docs/release-notes.md").write_text("Acceptance pending", encoding="utf-8")
            (root / "CHANGELOG.md").write_text("## 0.1.0\n\n* initial\n", encoding="utf-8")
            (root / ".release-please-manifest.json").write_text('{".":"0.1.0"}', encoding="utf-8")
            with patch.object(release.subprocess, "check_output", return_value=SHA):
                release.prepare("v0.1.0", SHA, root)
                self.assertIn("Acceptance pending", (root / "dist/release-notes.md").read_text())
                with self.assertRaises(ValueError):
                    release.prepare("v0.1.1", SHA, root)
                with self.assertRaises(ValueError):
                    release.prepare("v0.1.0", "b" * 40, root)

    def test_draft_requires_matching_tag_commit_and_empty_assets(self):
        draft = {"tagName": "v0.1.0", "isDraft": True, "assets": []}
        release.validate_draft(draft, "v0.1.0", SHA, SHA)
        for modified in ({"isDraft": False}, {"assets": [{"name": "checksums.txt"}]},
                         {"assets": None}, {"tagName": "v0.2.0"}):
            with self.subTest(modified=modified), self.assertRaises(ValueError):
                release.validate_draft(draft | modified, "v0.1.0", SHA, SHA)
        with self.assertRaises(ValueError):
            release.validate_draft(draft, "v0.1.0", "b" * 40, SHA)

    def registry(self, status, code="MANIFEST_UNKNOWN"):
        def opener(request, timeout):
            if isinstance(request, str):
                return io.BytesIO(b'{"token":"dummy"}')
            if status == 200:
                return io.BytesIO(b'{}')
            raise urllib.error.HTTPError(request.full_url, status, "test", {},
                                         io.BytesIO(json.dumps({"errors": [{"code": code}]}).encode()))
        return opener

    def test_registry_allows_only_explicit_missing_manifest(self):
        release.check_image("v0.1.0", self.registry(404))
        for status, code in ((200, ""), (401, "UNAUTHORIZED"), (429, "TOOMANYREQUESTS"),
                             (500, "UNKNOWN"), (404, "NAME_UNKNOWN")):
            with self.subTest(status=status, code=code), self.assertRaises(ValueError):
                release.check_image("v0.1.0", self.registry(status, code))

    def test_registry_network_failure_is_not_absence(self):
        def unavailable(*args, **kwargs):
            raise urllib.error.URLError("offline")
        with self.assertRaises(urllib.error.URLError):
            release.check_image("v0.1.0", unavailable)


if __name__ == "__main__":
    unittest.main()
