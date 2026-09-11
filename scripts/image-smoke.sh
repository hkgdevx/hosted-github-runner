#!/usr/bin/env bash
set -euo pipefail

# Runs inside the built image without registration or a mounted Docker socket.
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
