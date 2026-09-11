# ghrctl implementation validation

Validation date: 2026-09-11. No production release was published by this local
implementation session.

## Passed locally

- Go 1.26.0 formatting, unit tests and `go vet` on the Windows development host.
- Standalone Linux amd64 build with CGO disabled; the executable runs under
  Ubuntu 22.04/WSL and reports development build metadata.
- Linux test binary covering private configuration, invalid input, symlinks,
  atomic replacement, locks, API permissions/collisions, Docker argument and
  environment isolation, setup, readiness, ownership, busy guards, explicit
  upgrades, failed pulls, failed readiness, rollback, and credential rotation.
- A real pseudo-terminal test verifies hidden password input and terminal echo
  restoration after cancellation.
- Real Docker Compose v2.24.6 renders the embedded template and preserves the
  intended environment values and socket GID. This check needs no Docker daemon.
- All 15 existing offline Python/Bash regression tests.
- ShellCheck 0.10.0, Bash syntax validation, and actionlint 1.7.7.

The Go race detector is configured in Linux CI; it was not run locally because
the local Linux environment does not have the Go/C compiler toolchain required
for that check. The test binary was cross-compiled on Windows.

## Pending external acceptance

The local Docker daemon was unavailable. Container integration, image build and
image smoke tests could not run here. Tests using fake Docker and a local mock
GitHub server do not establish production image behavior.

The repository is public. Docker Hub repository visibility and credentials,
GitHub Actions execution, draft asset creation, a dedicated test organization,
live job execution, reboot persistence, cleanup, upgrade/rollback across real
images, and anonymous release downloads remain to be verified using the
[release acceptance procedure](binary.md#live-acceptance-checklist).

The generated `dist/ghrctl-linux-amd64` is a development executable without a
bundled production image. Production assets will be created by the version-tag
workflow after changes are committed and pushed. Keep the resulting release
draft until live acceptance is recorded.
