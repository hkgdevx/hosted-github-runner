# hosted-github-runner

A Dockerized, persistent GitHub Actions organization runner for **Linux x64** hosts, managed by the downloadable `ghrctl` CLI. It includes Docker build tools, security scanners, and Chromium system libraries for project-managed Playwright tests.

The Ubuntu 24.04 container runs as `gthb-runner`. Its Docker CLI connects to the **host Docker daemon** through `/var/run/docker.sock`; there is no Docker daemon inside the image. The entrypoint registers with GitHub.com, accepts jobs until stopped, and attempts to deregister on exit. Runner auto-updates remain enabled.

## Prerequisites

- A Linux x64 host with Docker Engine and Docker Compose v2. The operator must be able to use Docker and inspect `/var/run/docker.sock`. The legacy Bash helper additionally requires Bash and GNU `stat`.
- Capacity for the configured runner limits of 3 CPUs and 6 GB RAM, plus the host and any containers started by jobs. Compose also specifies reservations of 1 CPU / 2 GB and 1 GB shared memory.
- Outbound access to GitHub, Docker Hub, package registries, and any services used by your workflows.
- A GitHub organization and a PAT authorized to manage its runners. For a fine-grained PAT, select the organization and grant **Self-hosted runners: write**; organization approval policies may apply. See [authentication details](docs/operations.md#authentication).

Only trusted workflows should use this host: Docker socket access gives jobs control over the host daemon and its containers. Restrict runner access to trusted repositories and contributors.

## Binary quickstart

Download the executable, manifest, and checksums from a published [GitHub Release](https://github.com/hkgdevx/hosted-github-runner/releases). Merging a [release PR](docs/releases.md) prepares a tagged draft; a downloadable release is available after a maintainer validates and publishes it. Version history is recorded in [CHANGELOG.md](CHANGELOG.md).

```bash
base=https://github.com/hkgdevx/hosted-github-runner/releases/latest/download
curl -fLO "$base/ghrctl-linux-amd64"
curl -fLO "$base/release-manifest.json"
curl -fLO "$base/checksums.txt"
sha256sum --check checksums.txt
sudo install -m 0755 ghrctl-linux-amd64 /usr/local/bin/ghrctl
ghrctl start
```

First use prompts for the organization, a hidden PAT, runner name, and labels. Settings are saved under `~/.config/ghrctl/` (or `$XDG_CONFIG_HOME/ghrctl`). Run the CLI as the same Linux user each time; `sudo` is needed for the installation above, not for ordinary CLI use when your user already has Docker access.

```bash
ghrctl status
ghrctl logs
ghrctl doctor
ghrctl stop
```

The container continues after the CLI exits. Replacing the binary preserves the selected image; use `ghrctl upgrade` to deploy the new binary's bundled image, or `ghrctl upgrade --rollback` to restore the previous image. See the [binary operations and release guide](docs/binary.md) for configuration, automation, rotation, migration, and acceptance requirements.

## Source-based quickstart

Run these commands from the repository root on the Linux host:

```bash
cp .env.example .env
chmod 600 .env
${EDITOR:-vi} .env
```

Replace `ORG_NAME` with the organization slug and `ACCESS_TOKEN` with your PAT. Keep the example `development` label for the workflow below. The root `.env` is ignored by Git and excluded from Docker builds.

Review the `image:` field in `docker/docker-compose.yaml`. It currently names `weteams/github-runner:commit_b3fa2dd`; availability and correspondence to this checkout have not been verified. Select an available image built from the corrected source, or follow the [local build instructions](docs/operations.md#local-image-builds). Source edits do not change an already published image.

```bash
bash scripts/ghrctl.sh pull   # Skip for an image you built locally
bash scripts/ghrctl.sh start
bash scripts/ghrctl.sh status
bash scripts/ghrctl.sh logs
```

The helper detects the host Docker socket GID automatically. Exit log streaming with Ctrl+C; the container keeps running. Confirm the runner is **Idle/online** under the organization's **Settings → Actions → Runners**. Container status alone does not prove registration succeeded.

Add this workflow to a repository allowed to use the organization runner:

```yaml
name: Runner smoke test
on: workflow_dispatch
permissions:
  contents: read
jobs:
  smoke:
    runs-on: [self-hosted, linux, x64, development]
    steps:
      - name: Check runner and host Docker access
        run: |
          id
          uname -m
          docker version
          docker info
          docker buildx version
```

Wait for active jobs to finish before stopping or recreating the runner:

```bash
bash scripts/ghrctl.sh stop
```

## Included tools

| Area | Image contents |
| --- | --- |
| Containers | Docker CLI, Buildx, Compose plugin; host daemon required |
| Scanning and linting | Trivy, Hadolint, actionlint, ShellCheck |
| Development | Git, Git LFS, GitHub CLI, Python 3, pip, venv, GCC/G++, make |
| Utilities | Bash, curl, wget, jq, yq, SSH client, rsync, archive and network tools |
| Browser support | Chromium system libraries, fonts, shared memory configuration |

Workflows must install their own **Node.js, pnpm, project dependencies, and Playwright browsers**. The runner's internal Node runtime is not a project Node.js installation. After setting up Node and installing a project's locked dependencies, use that project's Playwright version, for example `npx playwright install chromium`, without `--with-deps` (the runner user cannot install OS packages). Browser cache, Trivy cache, and a pnpm data directory are mounted as named volumes; configure pnpm to use the mounted directory for its store if needed.

## Operations and limitations

See the [operations and maintainer runbook](docs/operations.md) for configuration defaults, cache behavior, troubleshooting, upgrades, image publishing, and validation commands.

This setup targets one persistent Linux x64 runner per Compose deployment. ARM64, ephemeral runners, autoscaling, repository-scoped registration, and GitHub Enterprise Server are outside the supported scope. Container jobs, service containers, and Docker actions have not been validated with the current workspace mounts. Linux image-build and live registration acceptance must be completed before treating a deployment as verified.
