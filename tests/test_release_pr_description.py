"""Execute the metadata-only PR description check against representative events."""

import contextlib
import io
import json
import os
from pathlib import Path
import tempfile
import textwrap
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = (ROOT / '.github/workflows/pr-description.yaml').read_text(encoding='utf-8')
CODE = textwrap.dedent(WORKFLOW.split("python3 - <<'PY'\n", 1)[1].rsplit('          PY', 1)[0])
NORMAL = '## Problem and change\nFix startup failure after a restart.\n\n## Validation performed\nRan lifecycle tests; all passed.\n\n## Breaking changes and migration\nNone\n'


class DescriptionTests(unittest.TestCase):
    def check(self, body, title='fix: handle restart', head='fix-restart', fork=False):
        repository = {'full_name': 'hkgdevx/hosted-github-runner'}
        event = {'pull_request': {'title': title, 'body': body,
                 'base': {'repo': repository},
                 'head': {'ref': head, 'repo': {'full_name': 'other/fork'} if fork else repository}}}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'event.json'
            path.write_text(json.dumps(event), encoding='utf-8')
            with patch.dict(os.environ, GITHUB_EVENT_PATH=str(path)), contextlib.redirect_stdout(io.StringIO()):
                exec(compile(CODE, 'pr-description-workflow', 'exec'), {})

    def test_normal_and_explicit_untested_explanation(self):
        self.check(NORMAL)
        self.check(NORMAL.replace('Ran lifecycle tests; all passed.', 'Not tested: Docker is unavailable; image smoke testing remains pending.'))
        self.check(NORMAL.replace('Fix startup failure after a restart.', 'Treat $(exit 99) as literal user input.'))

    def test_missing_empty_and_placeholders_fail(self):
        for body in (None, '', (ROOT / '.github/pull_request_template.md').read_text(),
                     NORMAL.replace('## Validation performed', '## Other'),
                     NORMAL.replace('Ran lifecycle tests; all passed.', '<!-- tested -->'),
                     NORMAL.replace('Fix startup failure after a restart.', '- [ ] TODO'),
                     NORMAL.replace('Ran lifecycle tests; all passed.', '**TBD**'),
                     NORMAL.replace('Ran lifecycle tests; all passed.', 'N/A'),
                     NORMAL.replace('Ran lifecycle tests; all passed.', 'Not tested'),
                     NORMAL + '\n## Problem and change\nDuplicate\n'):
            with self.subTest(body=body), self.assertRaises(SystemExit):
                self.check(body)

    def test_breaking_change_requires_migration(self):
        with self.assertRaises(SystemExit):
            self.check(NORMAL, title='feat!: change config')
        self.check(NORMAL.replace('None', 'Rename runner_name to name in existing configuration.'), title='feat!: change config')
        with self.assertRaises(SystemExit):
            self.check(NORMAL + '\nBREAKING CHANGE: config changed\n')

    def test_headings_in_code_or_comments_do_not_supply_sections(self):
        for body in ('```markdown\n' + NORMAL + '```\n', '<!--\n' + NORMAL + '-->'):
            with self.assertRaises(SystemExit):
                self.check(body)

    def release_body(self):
        footer = json.loads((ROOT / 'release-please-config.json').read_text())['packages']['.']['pull-request-footer']
        return ':robot: I have created a release *beep* *boop*\n---\n\n## 0.1.0\n\n### Features\n\n* automate releases\n\n---\n' + footer

    def test_release_requires_review_and_matching_version(self):
        args = {'title': 'chore(main): release 0.1.0', 'head': 'release-please--branches--main'}
        body = self.release_body()
        with self.assertRaises(SystemExit):
            self.check(body, **args)
        checked = body.replace('- [ ]', '- [x]')
        self.check(checked, **args)
        for invalid in (checked.replace('## 0.1.0', '## 0.2.0'), checked.replace('* automate releases', ''), checked.replace('Breaking changes and migration guidance reviewed.', 'TODO')):
            with self.assertRaises(SystemExit):
                self.check(invalid, **args)

    def test_release_format_does_not_exempt_forks_or_other_branches(self):
        body = self.release_body().replace('- [ ]', '- [x]')
        with self.assertRaises(SystemExit):
            self.check(body, title='chore(main): release 0.1.0', head='release-please--branches--main', fork=True)
        with self.assertRaises(SystemExit):
            self.check(body, title='chore(main): release 0.1.0', head='other')


if __name__ == '__main__':
    unittest.main()
