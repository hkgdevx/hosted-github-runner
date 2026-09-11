#!/usr/bin/env bash

set -euo pipefail

# ============================================================
# Required environment variables
# ============================================================

: "${ORG_NAME:?Error: ORG_NAME env var must be set}"
: "${ACCESS_TOKEN:?Error: ACCESS_TOKEN env var must be set}"


# ============================================================
# Optional environment variables
# ============================================================

RUNNER_LABELS="${RUNNER_LABELS:-self-hosted,linux,x64}"
RUNNER_WORKDIR="${RUNNER_WORKDIR:-_work}"
RUNNER_NAME_PREFIX="${RUNNER_NAME_PREFIX:-wbg-worker}"

RUNNER_PID=""


# ============================================================
# Generate unique runner name
# ============================================================

if [[ -z "${RUNNER_NAME:-}" ]]; then
  RUNNER_NAME_SUFFIX="$(
    uuidgen \
      | tr '[:upper:]' '[:lower:]' \
      | cut -c 1-8
  )"

  RUNNER_NAME="${RUNNER_NAME_PREFIX}-${RUNNER_NAME_SUFFIX}"
fi


# ============================================================
# Logging helper
# ============================================================

log() {
  echo "[entrypoint] $*"
}

warn() {
  echo "[entrypoint][WARN] $*" >&2
}

error() {
  echo "[entrypoint][ERROR] $*" >&2
}


# ============================================================
# GitHub API helper
# ============================================================

github_api() {
  local endpoint="$1"

  curl \
    --fail \
    --silent \
    --show-error \
    --location \
    --request POST \
    --retry 3 \
    --retry-delay 2 \
    --retry-all-errors \
    -H "Authorization: Bearer ${ACCESS_TOKEN}" \
    -H "Accept: application/vnd.github+json" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    "https://api.github.com${endpoint}"
}


# ============================================================
# GitHub runner token helpers
# ============================================================

get_registration_token() {
  github_api \
    "/orgs/${ORG_NAME}/actions/runners/registration-token" \
    | jq -er '.token'
}


get_remove_token() {
  github_api \
    "/orgs/${ORG_NAME}/actions/runners/remove-token" \
    | jq -er '.token'
}


# ============================================================
# Runner cleanup
# ============================================================

remove_runner() {
  if [[ ! -f ".runner" ]]; then
    log "Runner is not configured; nothing to remove."
    return 0
  fi

  log "Fetching runner removal token..."

  local remove_token

  if ! remove_token="$(get_remove_token)"; then
    warn "Unable to obtain runner removal token."
    warn "Runner may remain registered in GitHub."
    return 0
  fi

  log "Removing runner '${RUNNER_NAME}' from organization '${ORG_NAME}'..."

  if ! ./config.sh remove \
      --unattended \
      --token "${remove_token}"; then
    warn "Runner removal failed."
  else
    log "Runner removed successfully."
  fi
}


# ============================================================
# Graceful shutdown
# ============================================================

# Invoked by the signal trap; covered by the offline shutdown regression test.
# shellcheck disable=SC2317
shutdown() {
  log "Shutdown signal received."

  trap - SIGINT SIGTERM

  if [[ -n "${RUNNER_PID}" ]] && kill -0 "${RUNNER_PID}" 2>/dev/null; then
    log "Stopping runner process PID=${RUNNER_PID}..."

    kill -TERM "${RUNNER_PID}" 2>/dev/null || true

    wait "${RUNNER_PID}" 2>/dev/null || true
  fi

  remove_runner

  log "Shutdown complete."

  exit 0
}


trap 'shutdown' SIGINT SIGTERM


# ============================================================
# Safety check
# ============================================================

if [[ -f ".runner" ]]; then
  warn "Existing .runner configuration detected."
  warn "Attempting to remove stale registration before continuing."

  remove_runner || true

  rm -f \
    .runner \
    .credentials \
    .credentials_rsaparams
fi


# ============================================================
# Fetch registration token
# ============================================================

log "Fetching GitHub runner registration token..."

if ! RUNNER_TOKEN="$(get_registration_token)"; then
  error "Failed to obtain runner registration token."
  exit 1
fi

if [[ -z "${RUNNER_TOKEN}" ]]; then
  error "GitHub returned an empty runner registration token."
  exit 1
fi


# ============================================================
# Configure runner
#
# NOTE:
# --disableupdate is intentionally NOT used.
# GitHub runner auto-update therefore remains enabled.
# ============================================================

log "Configuring GitHub Actions runner..."
log "Organization : ${ORG_NAME}"
log "Runner name  : ${RUNNER_NAME}"
log "Labels       : ${RUNNER_LABELS}"
log "Workdir      : ${RUNNER_WORKDIR}"

./config.sh \
  --url "https://github.com/${ORG_NAME}" \
  --token "${RUNNER_TOKEN}" \
  --name "${RUNNER_NAME}" \
  --labels "${RUNNER_LABELS}" \
  --work "${RUNNER_WORKDIR}" \
  --unattended


# ============================================================
# Start runner
# ============================================================

log "Starting GitHub Actions runner '${RUNNER_NAME}'..."

./run.sh &

RUNNER_PID=$!

log "Runner started with PID=${RUNNER_PID}"


# ============================================================
# Wait for runner process
# ============================================================

set +e

wait "${RUNNER_PID}"

RUNNER_EXIT_CODE=$?

set -e

RUNNER_PID=""

log "Runner process exited with code ${RUNNER_EXIT_CODE}."


# ============================================================
# Remove runner registration
# ============================================================

remove_runner


# ============================================================
# Exit with runner's exit code
# ============================================================

exit "${RUNNER_EXIT_CODE}"
