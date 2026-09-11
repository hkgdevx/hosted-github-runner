# Operations and maintainer runbook

Commands below assume a Linux x64 Docker host and, unless specified otherwise, the repository root. See the [README](../README.md) for initial setup.

## Configuration

Copy `.env.example` to `.env` at the repository root. The Compose file loads `../.env`, relative to its own `docker/` directory, as described in [Docker's environment-file documentation](https://docs.docker.com/compose/how-tos/environment-variables/set-environment-variables/). This is the container environment file; Compose interpolation of `${DOCKER_GID}` is supplied separately by the helper's process environment.

| Variable | Required / script default | Example or behavior |
| --- | --- | --- |
| `ORG_NAME` | Required, nonempty | Organization slug only, such as `my-org`, not a URL |
| `ACCESS_TOKEN` | Required, nonempty | PAT used to request registration and removal tokens; never commit or log it |
| `RUNNER_NAME` | Optional, generated if unset or empty | An exact name overrides prefix generation; choose a unique name |
| `RUNNER_NAME_PREFIX` | `wbg-worker` | Example file uses `xxx-worker-xxx`; generated names append `-` and eight lowercase UUID characters |
| `RUNNER_LABELS` | `self-hosted,linux,x64` | Example adds `development`; workflow labels must match |
| `RUNNER_WORKDIR` | `_work` | Relative to `/home/gthb-runner/actions-runner`; no workspace volume is configured |
| `DOCKER_GID` | Computed by helper | Numeric group of host `/var/run/docker.sock`; used for Compose `group_add`, not an entrypoint setting |
| `COMPOSE_FILE` | Helper resolves `docker/docker-compose.yaml` relative to its script directory | Host-side override; a relative override resolves from the caller's working directory. Export it or prefix the helper command, rather than putting it in the container `.env` |

Optional entrypoint variables use their defaults when empty as well as when unset. For direct Compose commands, supply `DOCKER_GID` explicitly. The helper recomputes it and does not use a manually supplied value.

### Authentication

Use a PAT authorized for the target organization. A fine-grained PAT requires the organization **Self-hosted runners: write** permission. A classic PAT requires `admin:org` (and `repo` when applicable for private repositories), with an account allowed to administer organization runners. Organization approval and SSO authorization requirements may also apply. Check [GitHub's registration and removal API requirements](https://docs.github.com/en/rest/actions/self-hosted-runners#create-a-registration-token-for-an-organization).

The entrypoint exchanges `ACCESS_TOKEN` for short-lived registration/removal tokens through `api.github.com`; do not put a temporary registration token in `ACCESS_TOKEN`. Keep the PAT valid for shutdown cleanup. Rotate it by editing `.env` and recreating the container after jobs finish.

Use `chmod 600 .env`. Avoid sharing `docker inspect` or rendered `docker compose config` output: they can expose container environment secrets. `docker compose config --quiet` validates without rendering them.

## Routine operations

| Command | Effect |
| --- | --- |
| `bash scripts/ghrctl.sh start` | `compose up -d`; starts the runner |
| `bash scripts/ghrctl.sh stop` | `compose down`; stops and removes the container, retaining named volumes |
| `bash scripts/ghrctl.sh restart` | `compose down` then `up -d`; recreates the container and applies configuration changes |
| `bash scripts/ghrctl.sh status` | Shows Compose container status, not GitHub readiness |
| `bash scripts/ghrctl.sh logs` | Follows the latest 200 log lines; Ctrl+C exits the viewer |
| `bash scripts/ghrctl.sh pull` | Pulls the configured image; does not recreate the running container |

The helper works from other directories when invoked by absolute path:

```bash
bash /opt/hosted-github-runner/scripts/ghrctl.sh status
COMPOSE_FILE=/opt/custom/docker-compose.yaml bash /opt/hosted-github-runner/scripts/ghrctl.sh status
```

An alternate Compose file must define the `github-runner` service for service-specific helper commands. Its environment-file paths are relative to that alternate file. The helper requires the Docker socket even for status, logs, and pull.

### Update and rollback

1. Wait for active jobs to finish. Record the currently deployed image tag.
2. Edit `image:` in the Compose file to a published `hkgdevx/hosted-github-runner:commit_<short-sha>` tag built from the desired source.
3. Run `bash scripts/ghrctl.sh pull`, then `bash scripts/ghrctl.sh restart`.
4. Inspect logs, verify the runner is online in GitHub, and run the README smoke workflow.

To roll back, restore the previous image tag and repeat pull/restart/verification. The checked-in `weteams/github-runner:commit_b3fa2dd` is a configured reference, not evidence of a verified release. Do not assume it contains local fixes. Registry tags can be overwritten; retain the image digest when exact rollback identity matters.

## Lifecycle, storage, and trust

The entrypoint creates a unique name unless `RUNNER_NAME` is set, registers unattended at organization scope, and runs continuously. It does not set `--ephemeral` or `--disableupdate`. The image fetches the latest runner at build time, and the runner can update itself while deployed; rebuilding the same source later need not produce identical software.

On SIGINT/SIGTERM, the entrypoint forwards SIGTERM to its runner child, waits for it, and attempts deregistration. A normal runner exit also triggers removal and preserves the child exit code. Removal failures are warnings. Compose allows two minutes before forced termination, so this is not a guarantee that active jobs finish or cleanup succeeds. The `unless-stopped` restart policy restarts an exited container unless explicitly stopped.

On startup, an existing `.runner` file triggers a best-effort removal attempt followed by deletion of local registration credentials and new registration. Network failures, crashes, forced termination, or expired PATs can leave stale offline runners in GitHub. Remove stale entries from organization settings after confirming they are no longer active; a new unique name does not clean up an older remote registration.

| Storage | Persistence |
| --- | --- |
| `trivy-cache` → `/home/gthb-runner/.cache/trivy` | Named volume retained across recreation |
| `playwright-cache` → `/home/gthb-runner/.cache/ms-playwright` | Named volume retained across recreation |
| `pnpm-cache` → `/home/gthb-runner/.local/share/pnpm` | Named volume; set pnpm's store directory under this path if its version uses another default |
| Runner `_work`, credentials, auto-updated binaries, `/home/gthb-runner/.cache/buildx` | Container writable layer; survives a container restart but is discarded by helper `restart`/`stop` recreation |
| Images, containers, Buildx builder state managed by host Docker | Stored by the host daemon; not removed with the runner container |

Named volumes are scoped to the Compose project; changing the project name can select different volumes. `compose down -v` deletes the caches and is not used by the helper. Workspace contents are not isolated between successive jobs in the same container.

The non-root container user receives the host socket's group ID. Access to that socket allows host-level Docker operations; the container's CPU/memory limits do not constrain separate containers launched on the host daemon. Run trusted jobs only and restrict organization runner repository access. Shared caches and the persistent workspace are not security boundaries between untrusted jobs. Protect the PAT as a secret available in the runner's environment.

Container jobs, service containers, Docker actions, and arbitrary Docker bind mounts are unvalidated here: paths passed to the daemon resolve on the host, while the runner workspace exists inside its container. Do not assume this layout provides matching host workspace paths. Playwright support is for project-installed Chromium; other browser engines may need additional OS packages.

## Troubleshooting

| Symptom | Checks and action |
| --- | --- |
| Registration fails / HTTP 401 or 403 | Check organization slug, PAT expiration, organization runner permission, approval/SSO, and outbound access. Use logs without exposing the token. |
| Runner stays offline or restarts repeatedly | Inspect logs for failed configuration, name collisions, or a runner process exit; container status alone is insufficient. |
| Docker socket missing | Confirm Docker Engine is running on the Linux host with `/var/run/docker.sock`; the helper expects this exact Unix socket. |
| Docker permission denied | Check `stat -c '%g' /var/run/docker.sock`, operator Docker access, and container supplementary groups via `docker exec github-runner id`. Recreate after a socket GID change; do not make the socket world-writable. |
| Job remains queued | Compare every `runs-on` label with the online runner and confirm repository access to its organization runner group. |
| Node or pnpm missing | Install the workflow's chosen versions before package installation. These are not installed by the image. |
| Playwright browser missing | Install locked project dependencies, then the matching browser with `npx playwright install chromium`. Check cache ownership and any `PLAYWRIGHT_BROWSERS_PATH` override. |
| Image pull fails | Verify the configured tag exists and registry credentials permit access; source changes require a new image. |
| Build download fails | Check the failing URL, upstream asset name/architecture, GitHub API rate limits, and package repository/network access. actionlint uses `linux_amd64`, whereas Hadolint uses `Linux-x86_64`. |
| Offline duplicate / removal warning | Restore API access and PAT permissions, and remove confirmed stale entries in organization settings. Cleanup is best effort. |
| Cache unexpectedly empty | Check Compose project identity and volume mounts; confirm the tool uses the mounted location. |

## Local image builds

Build from the repository root; the Dockerfile copies `scripts/entrypoint.sh` from this context:

```bash
docker build --platform linux/amd64 -f docker/Dockerfile -t hosted-github-runner:local .
```

For local deployment, set Compose's `image:` to `hosted-github-runner:local`, skip `pull`, and run `bash scripts/ghrctl.sh restart`. This does not publish an image.

The build arguments `YQ_VERSION=v4.47.2`, `HADOLINT_VERSION=v2.14.0`, and `ACTIONLINT_VERSION=1.7.7` pin those downloads and can be overridden with `--build-arg`. Ubuntu packages, the base-image tag, and the latest runner download are not fully pinned. `DEBIAN_FRONTEND=noninteractive` controls package installation. Architecture branches exist in the Dockerfile, but only Linux x64 is the supported validation target.

## Publishing from GitHub Actions

Main-branch builds publish `hkgdevx/hosted-github-runner:commit_<short-sha>` after
reusable checks pass. Configure `DOCKER_HUB_USERNAME` and
`DOCKER_HUB_ACCESS_TOKEN` in the `production` environment with push access to
that repository. The old `DOCKER_IMAGE_NAME` secret is no longer used.

Merging the rolling release PR creates a version tag (`vMAJOR.MINOR.PATCH`) and
draft GitHub Release, then calls packaging to smoke-test and push the image and
attach the matching Linux x64 CLI and checksums. Tag pushes alone do not start
packaging. See the [release automation guide](releases.md) and
[binary release guide](binary.md#maintainer-build-and-release-process)
for configuration, live acceptance, and publication. Workflows use pinned action
commit SHAs. The source Compose image field is not automatically updated or
certified by these workflows.

## Validation

Validation for this update (2026-09-11): 15 offline tests passed under Ubuntu/WSL, along with Bash syntax checks, ShellCheck 0.10.0, actionlint 1.7.7, local Markdown link checks, and Compose v2.24.6 configuration validation using example credentials. The Linux x64 image build was attempted but blocked because the local Docker daemon was unavailable. Image tool smoke tests and live GitHub acceptance remain unverified.

### Static checks and mocked behavior

With Bash, Python 3, ShellCheck, and actionlint installed:

```bash
bash -n scripts/entrypoint.sh scripts/ghrctl.sh
shellcheck scripts/entrypoint.sh scripts/ghrctl.sh
actionlint .github/workflows/build.yaml
python3 tests/test_scripts.py
```

Tests use fake API responses and runner commands; they do not contact GitHub, register a runner, or start Docker. They cover required configuration, registration failures, naming, runner exit codes, shutdown, cleanup failures, and control-script path resolution/dispatch. They also check documentation links and script line endings.

To validate Compose with dummy credentials without modifying a real `.env`, create an isolated temporary project:

```bash
validation_dir=$(mktemp -d)
mkdir "$validation_dir/docker"
cp docker/docker-compose.yaml "$validation_dir/docker/"
cp .env.example "$validation_dir/.env"
DOCKER_GID=123 docker compose -f "$validation_dir/docker/docker-compose.yaml" config --quiet
```

The temporary directory contains example values only. Successful rendering checks configuration structure, not Docker connectivity or registration.

### Image smoke test

After the local build, override the entrypoint to avoid registration. The image's default non-root user is retained:

```bash
docker run --rm --entrypoint bash hosted-github-runner:local -lc '
  set -e
  test "$(id -un)" = gthb-runner
  docker --version
  docker buildx version
  docker compose version
  trivy --version
  hadolint --version
  actionlint --version
  shellcheck --version
  yq --version
  jq --version
  gh --version
  git --version
  git lfs version
  python3 --version
  python3 -m pip --version
  gcc --version
  make --version
  ./bin/Runner.Listener --version
'
```

This checks image tools, not host socket access, browser launch, or GitHub registration.

### Live acceptance — not yet verified

Use a dedicated test organization and an image built from this source:

1. Configure the root `.env`, start the runner, and confirm its name and labels appear online in GitHub.
2. Run the README smoke workflow and verify successful host Docker access. For browser workloads, separately set up the project's Node/pnpm versions, install locked dependencies and Chromium, and run a browser test.
3. Write a disposable cache marker: `docker exec github-runner sh -c 'echo acceptance > /home/gthb-runner/.cache/trivy/runner-acceptance-marker'`.
4. Wait for jobs to finish, run `bash scripts/ghrctl.sh stop`, and verify that registration disappears from GitHub.
5. Run `bash scripts/ghrctl.sh start`, confirm online registration, and verify persistence with `docker exec github-runner cat /home/gthb-runner/.cache/trivy/runner-acceptance-marker`.
6. Remove the marker with `docker exec github-runner rm /home/gthb-runner/.cache/trivy/runner-acceptance-marker`, stop the test runner, and confirm deregistration again.

Record the image tag/digest, host platform, date, and observed results. Image-build and live acceptance results must be recorded separately from mocked/static checks; this document does not certify a published image.
