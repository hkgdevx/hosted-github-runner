# Binary operations and releases

`ghrctl` supports one persistent GitHub.com organization runner on a Linux x64
host with Docker Engine, Compose v2, and `/var/run/docker.sock`. The CLI connects
to that local socket explicitly. Remote Docker contexts and rootless socket
locations are not supported. No repository checkout or Go runtime is needed on
the deployment server.

## Configuration and first use

Run `ghrctl start`. If configuration is missing or incomplete, an interactive
wizard requests the organization, PAT, unique runner name, and labels. Token
entry is hidden. Empty token input retains an existing token. A generated
hostname-based name is saved permanently. Default labels are
`self-hosted,linux,x64,development`.

The CLI validates Docker access and GitHub organization runner read/write
permissions before saving. It requests and discards a temporary registration
token; the container requests its own token when starting. Existing names are
rejected unless they identify the local managed runner. A classic PAT's
organization permissions, SSO authorization, or organization approval may also
be required; see [authentication](operations.md#authentication).

Configuration paths, in priority order:

1. Global `--config-dir /absolute/path` (relative paths are made absolute).
2. `$XDG_CONFIG_HOME/ghrctl` when the environment variable is set and absolute.
3. `~/.config/ghrctl`.

The directory contains `config.yaml`, `credentials.env`, and a `runtime/`
directory with the last applied `compose.yaml` and `runner.env`. Directories use
mode `0700`; files use `0600`. Symlink paths are refused. Mutating commands use
an advisory lock that is released even if the CLI crashes. Do not edit files
while a command is running.

Credentials are plaintext. Both `credentials.env` and the applied `runner.env`
contain the PAT; Docker stores it in the container environment. Restrict access
to the account and Docker socket. Do not share `docker inspect` or rendered
Compose output. CLI subprocess output redacts the saved and applied PATs, but
workflow logs can contain other secrets and still need careful handling.

Run the CLI consistently as the same account. Installing in `/usr/local/bin`
does not make its configuration system-wide. Another account gets separate
configuration and cannot adopt the existing container. Moving the configuration
directory changes deployment ownership; stop the old deployment first.

### Editing and automation

`ghrctl configure` edits saved settings without changing the running container.
Then run `ghrctl restart` when jobs have finished. Malformed YAML or unsupported
schema versions are reported, not overwritten. Back up configuration privately
before manual edits.

Advanced fields in `config.yaml` are `workdir` (default `_work`), `cpus` (`"3"`),
`memory` (`6G`), `reserved_cpus` (`"1"`), `reserved_memory` (`2G`), and
`shared_memory` (`1G`). Quote CPU values as strings. `image` is the selected image
reference; production setup receives the digest embedded in the release binary.
`previous_image` is maintained by upgrades. `schema` must remain `1`.

Noninteractive setup requires explicit organization, name, labels, and a token
on stdin. There is deliberately no token argument:

```bash
# /secure/path/pat contains only the token and is readable only by the operator.
ghrctl configure --non-interactive \
  --org my-org --name build-server-01 --labels development \
  --token-stdin < /secure/path/pat
ghrctl start --timeout 5m
```

Development binaries have no bundled image. Supply `--image IMAGE` to
`configure`; building the CLI alone does not build a runner image. Release
binaries need no image argument.

### Credential rotation

Run `ghrctl configure` and enter the new PAT, then `ghrctl restart`. The saved
credential can verify the old runner's busy state, while the still-running
container retains its old credential for deregistration. Keep the old PAT valid
until restart and cleanup finish, then revoke it. If it has already expired,
cleanup may leave an offline name collision: inspect GitHub and remove the
confirmed stale registration before starting again. Do not use `--force` to
silently replace a registration.

## Commands and failure handling

| Command | Behavior |
| --- | --- |
| `start` | Setup if needed; start and wait up to three minutes for GitHub online status |
| `configure` | Validate and save settings; no deployment change |
| `restart` | Pull selected image, stop and recreate with saved settings |
| `stop` | Remove the container, retain configuration and caches, check deregistration |
| `status` | Show local container/image and GitHub online/offline/busy state |
| `logs` | Follow 200 recent lines; Ctrl+C exits only the viewer |
| `pull` | Download the selected image without replacing the container |
| `doctor` | Check configuration, host, Docker/Compose, ownership, GitHub permissions and name collisions |
| `version` | Print binary version, commit and bundled image |
| `upgrade` | Pull and deploy this binary's bundled image, retaining the previous image reference |
| `upgrade --rollback` | Deploy the previous image, retaining current settings and caches |

`help` and `version` never run setup. Other commands without configuration
instruct the user to run `start` or `configure`. A noninteractive `start` without
valid configuration fails instead of hanging for input.

A repeated `start` leaves a running container alone. If saved configuration,
socket GID, or the managed template differs, it requires `restart`. Compose
project `ghrctl` and container `ghrctl-runner` have stable identities; unrelated
containers are never adopted. Docker/Compose overrides and `.env` files in the
calling directory are ignored.

`start`, `restart`, and `upgrade` accept `--timeout 5m`. Readiness requires a
running container and an online GitHub runner matching the ID read from its
`.runner` file. An online busy runner counts as ready. API failures report
unknown status, not offline. A timeout leaves the container available for
diagnosis and returns nonzero: inspect `status` and `logs`.

Disruptive commands refuse a busy runner or unverifiable busy state unless
`--force` is supplied. This check does not prevent a new job from being assigned
between the check and shutdown. The two-minute shutdown grace period is not job
draining. Restrict scheduling before planned maintenance. Deregistration is
best effort; inspect warnings and remove only confirmed stale registrations.

Replacing the binary does not upgrade the selected image. `upgrade` pulls its
target before shutdown and records the previous image. On failure, inspect the
remaining deployment and use `upgrade --rollback` explicitly. No automatic
binary download, downgrade, or cache deletion occurs. GitHub runner internal
auto-updates remain enabled, so an image digest does not freeze those updates.

## Manual migration from Bash

1. Wait for jobs to finish and record the old image and volume names. With the
   old deployment running, inspect only its mounts:
   `docker inspect github-runner --format '{{json .Mounts}}'`.
2. Stop/remove the old container using `bash scripts/ghrctl.sh stop` from its
   checkout. Verify GitHub deregistration. Do not add `-v` to Compose down.
3. Install the binary and run `ghrctl start`. Re-enter settings and choose a
   unique runner name.
4. Verify the smoke workflow. The binary uses new `ghrctl`-scoped cache volumes;
   old volumes remain untouched. There is no automatic cache import.

The old helper and Compose file remain available for source deployments. Their
checked-in image reference is legacy configuration, not a certified production
release. Choose an available image explicitly when using that path.

## Maintainer build and release process

From the repository root, with the version of Go specified by `go.mod`:

```bash
go test ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -o dist/ghrctl-linux-amd64 ./cmd/ghrctl
```

Production builds add version, commit and digest via linker flags in the release
workflow. PR checks also run the existing Bash regression tests, ShellCheck,
actionlint, and real Compose rendering with dummy credentials. Build outputs are
ignored by Git and excluded from the Docker context.

Configure the `production` environment's `DOCKER_HUB_USERNAME` and
`DOCKER_HUB_ACCESS_TOKEN` with push access to `hkgdevx/hosted-github-runner`.
`DOCKER_IMAGE_NAME` is no longer used. Both the Docker Hub image repository and
GitHub repository must be public for anonymous production downloads; workflows
do not change visibility.

Follow the [release automation guide](releases.md) to configure the bot and
bootstrap v0.1.0. Release Please accumulates changes in one release PR; merge
that PR when ready. It creates a `vMAJOR.MINOR.PATCH` tag and draft, then calls
packaging with the exact release commit. The workflow builds and
smoke-tests the exact Linux amd64 image without registration, pushes that image,
verifies anonymous pulling, captures its digest, and builds the matching CLI.
It attaches the executable, `release-manifest.json`, and `checksums.txt` to the
existing **draft**, with this version's changelog and acceptance instructions.
Existing assets and versioned images are never overwritten. If a release job
fails after image push, inspect its partial result and use a fresh version.
An empty draft with no published image can be retried through the Release
candidate workflow's tag input; see the recovery instructions in the guide.

Download draft assets as a maintainer for acceptance:

```bash
gh release download v1.0.0 --repo hkgdevx/hosted-github-runner --dir acceptance
cd acceptance
sha256sum --check checksums.txt
```

Use the actual candidate version in place of the example. Keep the release draft
until all live checks below pass. Record results in the release notes, then
publish through GitHub Releases. Finally download all three assets without GitHub
authentication and repeat checksum verification. Anonymous download URLs cannot
be tested while a release is draft. Main-branch image publishing continues to use
`commit_<short-sha>` tags and does not publish CLI releases.

### Live acceptance checklist

Use a dedicated test organization and a clean Linux x64 Docker/Compose host.
Do not use production jobs for these checks.

1. Verify the exact draft assets and run first-use setup without a checkout.
2. Confirm online status and run the README smoke workflow successfully.
3. Exercise `status`, `logs`, `doctor`, and repeated `start`.
4. Confirm the container survives CLI exit and resumes after host reboot.
5. Write a disposable cache marker; confirm it survives stop/start and restart.
6. Rotate the PAT while the old token is still valid, and verify cleanup.
7. Exercise an explicit upgrade and rollback using two available candidate
   image versions; verify readiness, selected digests, and preserved caches.
8. Stop, verify deregistration, and remove only disposable test data.

Record date, host platform, CLI version, image digest, and each observed result.
Image smoke tests and mock tests do not establish live acceptance. Public release
publication remains a maintainer step after acceptance.
