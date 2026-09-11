# Release automation

Release Please maintains one rolling release PR against `main`. Review its
version and changelog, then squash-merge it when ready to prepare a candidate.
Automation creates the `vMAJOR.MINOR.PATCH` tag and a **draft** GitHub Release,
then packages the exact tagged commit. Publishing remains a maintainer action
after the [live acceptance checklist](binary.md#live-acceptance-checklist).

The version applies to both `ghrctl` and its bundled runner image. Development
binaries still report `dev`. The release manifest in the repository tracks the
version; `CHANGELOG.md` tracks the changes. Main-branch Docker builds continue
to use `commit_<short-sha>` tags.

## Commit and version policy

Squash-merge PRs with a Conventional Commit title. The PR title becomes the
squash commit title; preserve meaningful PR-body footers in the commit body.

| PR title | Effect during 0.x |
| --- | --- |
| `fix: handle runner restart` | Patch, such as 0.1.0 to 0.1.1 |
| `fix(deps): update the shipped scanner` | Patch |
| `perf: reduce startup time` | Patch |
| `feat: add a command` | Minor, such as 0.1.0 to 0.2.0 |
| `feat!: replace the configuration format` | Minor, with a breaking-change notice |
| `docs: explain setup`, `ci: tune checks`, `chore(deps): update a test tool` | No release alone |

Any type with `!` denotes a breaking change, including `refactor!`. Add a
`BREAKING CHANGE: ...` footer explaining migration steps. After reaching 1.0.0,
breaking changes bump major; features still bump minor and fixes bump patch.
Use an explicit one-time `Release-As: 1.0.0` footer when committing to stability.

Use `fix(deps)` for changes to dependencies or tools shipped in the image or
CLI. Use `chore(deps)` for development-only dependencies. Use `fix` for a
user-visible corrective revert; a nonbreaking `revert` or maintenance commit
alone does not trigger a release. Multiple changes select the highest bump and
accumulate on the same release PR. Hidden maintenance sections do not hide
breaking-change notes.

## One-time repository setup

1. Create a fine-grained PAT owned by an account with write access, restricted
   to `hkgdevx/hosted-github-runner`. Grant repository **Contents**, **Pull
   requests**, and **Issues** read/write, plus the automatic Metadata read
   permission. Store it as the repository Actions secret `RELEASE_PLEASE_TOKEN`.
   Do not paste the token in an issue, PR, commit, or workflow log. Choose an
   expiry and rotate the secret before expiration; the bot has no fallback to
   a personal login or the built-in token. Organization approval may apply.
2. Keep `DOCKER_HUB_USERNAME` and `DOCKER_HUB_ACCESS_TOKEN` in the `production`
   environment. The image repository and GitHub repository must be public.
   The production environment may require approval before packaging starts.
3. Enable squash merges; disable merge commits and rebase merges. Set squash
   commit title to PR title and squash body to PR body. Do not enable auto-merge.
4. Protect `main` with required PRs and up-to-date branches, requiring the
   `checks`, `Conventional PR title`, and `PR description complete` status checks. These are the direct
   PR job names, not the nested packaging job names. Keep existing protection
   rules if installing into a repository that already has them.
5. Restrict version-tag pushes to release maintainers/bot and enable immutable
   version tags in Docker Hub if available. Workflows serialize candidates and
   refuse existing tags, but an unrelated external writer can race a registry
   preflight check; registry permissions/immutability enforce that boundary.

Release Please uses the PAT so its PR events run CI normally. Packaging uses
`GITHUB_TOKEN` and production environment secrets. The reusable workflow is
called directly from Release Please outputs; tag pushes do not start packaging.
The PAT does not need Actions write access for this connection.

## Bootstrap v0.1.0

The initial `.release-please-manifest.json` is deliberately empty. The
`initial-version: 0.1.0` setting applies only when there is no previous release;
it does not override subsequent calculated bumps. When merging the
implementation, retain the explicit one-time footer with this squash commit
title and body:

```text
feat: automate versions and changelogs

Prepare release PRs and draft assets for the CLI and runner image.

Release-As: 0.1.0
```

Check the final squash commit body before merging: GitHub must retain the
footer. After the workflow runs, verify the bot's first PR proposes **0.1.0**
in the manifest and changelog. Do not merge a different proposed version.
The first-release-only default also prevents an accidental 1.0.0 proposal if
the footer is omitted. Do not set a permanent `release-as` config value
or pretend 0.1.0 was already released by pre-populating the manifest.

After the first release PR merges, the manifest and tag establish the next
version boundary, including while the release remains a draft. The one-time
footer is outside subsequent release ranges.

## Preparing and publishing a candidate

1. Let normal PRs accumulate. Review the bot PR's proposed version and notes;
   ensure breaking changes have useful migration instructions. Keep the bot's
   branch, release metadata, and labels intact. Correct source commit metadata
   when needed; subsequent bot updates may regenerate notes. Complete both
   Release review checkboxes before merging; updates may reset them. See
   [contributor guidance](../CONTRIBUTING.md#reviewing-a-release-pr).
2. Squash-merge the release PR. The Release PR workflow creates a draft and
   immediately creates its tag, then calls Release candidate once with the tag
   and full commit SHA. It does not publish the release.
3. Packaging resolves the tag and verifies its ancestry, manifest version,
   changelog entry, remote tag SHA, and empty draft. Checks and builds use that
   commit even if `main` advances. The image is smoke-tested before push and
   pulled anonymously by digest before building the CLI.
4. Inspect the workflow summary and draft. It must contain exactly
   `ghrctl-linux-amd64`, `release-manifest.json`, and `checksums.txt`. Notes come
   from this version's tagged changelog entry plus the installation and live
   acceptance instructions, excluding other versions.
5. Download and verify these exact assets; complete the
   [live acceptance checklist](binary.md#live-acceptance-checklist). Replace the
   pending acceptance paragraph in the draft notes with the recorded results,
   then publish through GitHub Releases. Verify anonymous asset downloads and
   checksums after publication.

## Failure recovery

Failed jobs report the candidate tag and attempt to show draft assets and
registry state. API errors, unknown registry state, published releases, tag/SHA
mismatches, and existing assets stop packaging. Only an explicit registry
`MANIFEST_UNKNOWN` permits a new versioned image push; authentication, rate-limit,
and network errors never count as proof of absence.

If a matching empty draft exists and neither its image nor assets were
published, retry the failed packaging jobs or dispatch **Release candidate**
on `main` with the existing `tag`. Manual dispatch resolves the tag rather than
using the current `main` commit. Re-running Release PR may not call packaging
again because the draft has already been created; use Release candidate for
that recovery.

If an image or any assets exist, do not delete, replace, or retag them to make
a retry pass. Inspect the partial candidate and keep it unpublished. Merge a
corrective PR with a one-time higher `Release-As` footer (for example, 0.1.1
after a failed 0.1.0), then merge the resulting fresh release PR. Give the
corrective commit a user-facing `fix:` description so the recovery is recorded.
Do not publish an incomplete draft or reuse its version. If tag/draft creation
itself fails partially, inspect both GitHub objects before rerunning the bot;
never move an existing tag to a different commit.

## Local and rollout validation

Run `actionlint`, `python3 -m unittest discover -s tests -p 'test_release*.py'`,
and the existing Go, Bash, and Compose checks. The checks workflow installs
Release Please 17.6.0 (the engine bundled in the pinned v5.0.0 action) and runs
`tests/release-policy.cjs` to validate the config schema, bootstrap, version
matrix, hidden sections, breaking notes, and stable release branch. Update the
engine test version and action pin together.

For the first real release, confirm bot PR checks run, only one packaging run
starts, and the tag, binary output, and manifest agree on version and commit.
Verify image digests and checksums. Local/offline tests cannot establish GitHub
event delivery, production secret access, image registration, or live runner
acceptance; record those results from the actual candidate before publishing.
