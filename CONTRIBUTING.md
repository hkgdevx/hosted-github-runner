# Contributing

## Opening an issue

Choose Bug report, Feature request, or Documentation issue in GitHub's issue
chooser. Required fields collect the information needed to investigate; logs,
screenshots, workarounds, and proposed wording are optional. Use `unknown` for
the version or host when that information is unavailable. Search existing
issues before submitting, and remove credentials and private data from reports.
Blank issues are disabled in the chooser. Issue categories and labels do not
control version bumps.

## Opening a pull request

Use a Conventional Commit title such as `fix: handle restart`,
`feat: add a command`, or `docs: explain setup`. Titles determine release impact;
see the [version policy](docs/releases.md#commit-and-version-policy).

The default template has three required sections:

- **Problem and change:** explain the concrete problem and resulting behavior.
- **Validation performed:** describe checks and results. If testing was not
  possible, explain why and what remains unverified.
- **Breaking changes and migration:** write `None` for compatible changes, or
  explain the impact and migration steps. A title containing `!` cannot use
  `None`; retain a `BREAKING CHANGE:` footer in the squash commit body as well.

The Related issues section is optional; use `Closes #123` when appropriate.
Keep the required headings. HTML comments, empty sections, and standalone
placeholders such as `TODO`, `TBD`, and `N/A` fail the **PR description complete**
check. That check runs again when the description is edited. It checks basic
completeness; reviewers still assess correctness and evidence.

GitHub uses Markdown PR templates, so these are editable sections rather than
web form fields. Required branch checks enforce completeness before merging.
Do not put PATs, tokens, or private logs in descriptions.

## Reviewing a release PR

Release Please PRs use their generated version and changelog instead of the
normal template. On the same-repository `release-please--branches--main` branch,
the check recognizes the generated title/header and validates the release
version and the two Release review checkboxes:

- Version and changelog reviewed.
- Breaking changes and migration guidance reviewed.

Tick both after reviewing the candidate; bot updates may reset them, requiring
another review. These boxes do not claim live acceptance: packaging and live
testing follow the merge, as described in the [release guide](docs/releases.md).
Other PRs, including dependency-bot PRs and similarly named fork branches, must
fill out the normal template. Merely adding a release label does not exempt a PR.

## Local validation

Run `actionlint` and `python3 -m unittest discover -s tests -p 'test_release*.py'`
for PR metadata checks, plus the existing checks relevant to your change.
CI also exercises the configured release footer with the pinned Release Please
engine. Issue forms become available when their files reach the default branch;
the new PR description workflow runs on PR events once the workflow is present
in the proposed branch.
