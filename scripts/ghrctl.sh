#!/usr/bin/env bash

set -euo pipefail

# ============================================================
# GitHub Runner Control Utility
#
# Usage:
#   bash scripts/ghrctl.sh start
#   bash scripts/ghrctl.sh stop
#   bash scripts/ghrctl.sh restart
#   bash scripts/ghrctl.sh status
#   bash scripts/ghrctl.sh logs
#   bash scripts/ghrctl.sh pull
#
# Environment overrides:
#   COMPOSE_FILE=/path/to/docker-compose.yml bash scripts/ghrctl.sh start
# ============================================================


# ------------------------------------------------------------
# Docker Compose file
#
# Can be overridden externally:
#
#   COMPOSE_FILE=compose.runner.yml bash scripts/ghrctl.sh start
# ------------------------------------------------------------

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_FILE="${COMPOSE_FILE:-${SCRIPT_DIR}/../docker/docker-compose.yaml}"


# ------------------------------------------------------------
# Resolve Docker socket group ID from the host.
#
# The GitHub runner container mounts:
#
#   /var/run/docker.sock
#
# Docker socket access is controlled by its numeric GID.
# We pass this GID into Docker Compose as DOCKER_GID so that:
#
#   group_add:
#     - "${DOCKER_GID}"
#
# always matches the host Docker installation.
# ------------------------------------------------------------

get_docker_gid() {
  if [[ ! -S /var/run/docker.sock ]]; then
    echo "[ghrctl.sh][ERROR] Docker socket not found: /var/run/docker.sock" >&2
    exit 1
  fi

  stat -c '%g' /var/run/docker.sock
}


# ------------------------------------------------------------
# Wrapper around Docker Compose.
#
# Automatically injects DOCKER_GID for every Compose command.
# This avoids storing a machine-specific Docker GID in .env.
# ------------------------------------------------------------

compose() {
  local docker_gid
  docker_gid="$(get_docker_gid)"
  DOCKER_GID="${docker_gid}" \
    docker compose \
      -f "${COMPOSE_FILE}" \
      "$@"
}


# ------------------------------------------------------------
# Print command usage.
# ------------------------------------------------------------

usage() {
  cat <<EOF
GitHub Runner Control Utility

Usage:
  $0 <command>

Commands:
  start       Start the GitHub Actions runner
  stop        Stop and remove the runner container
  restart     Restart the runner container
  status      Show runner container status
  logs        Follow runner logs
  pull        Pull the configured runner image

Examples:
  $0 start
  $0 status
  $0 logs
  $0 restart

Optional:
  COMPOSE_FILE=compose.runner.yml $0 start
EOF

  exit 1
}


# ============================================================
# Command handling
# ============================================================

main() {
  case "${1:-}" in

    # ----------------------------------------------------------
    # Start the GitHub runner in detached mode.
    # ----------------------------------------------------------

    start)
      echo "[ghrctl.sh] Starting GitHub Actions runner..."

      compose up -d

      echo "[ghrctl.sh] Runner started."
      ;;


    # ----------------------------------------------------------
    # Gracefully stop the runner and remove its container.
    #
    # The entrypoint inside the runner container should handle
    # SIGTERM and deregister the runner from GitHub.
    # ----------------------------------------------------------

    stop)
      echo "[ghrctl.sh] Stopping GitHub Actions runner..."

      compose down

      echo "[ghrctl.sh] Runner stopped."
      ;;


    # ----------------------------------------------------------
    # Restart the runner.
    #
    # This performs a complete stop + recreate rather than just
    # restarting the existing container. This ensures Compose
    # configuration changes are applied.
    # ----------------------------------------------------------

    restart)
      echo "[ghrctl.sh] Restarting GitHub Actions runner..."

      compose down
      compose up -d

      echo "[ghrctl.sh] Runner restarted."
      ;;


    # ----------------------------------------------------------
    # Display container status.
    # ----------------------------------------------------------

    status)
      compose ps
      ;;


    # ----------------------------------------------------------
    # Follow runner logs.
    #
    # Exit log streaming with:
    #
    #   Ctrl+C
    # ----------------------------------------------------------

    logs)
      compose logs \
        --follow \
        --tail=200 \
        github-runner
      ;;


    # ----------------------------------------------------------
    # Pull the configured GitHub runner image.
    #
    # This does NOT automatically recreate the running runner.
    # Follow with:
    #
    #   bash scripts/ghrctl.sh restart
    # ----------------------------------------------------------

    pull)
      echo "[ghrctl.sh] Pulling GitHub runner image..."

      compose pull github-runner

      echo "[ghrctl.sh] Image pulled successfully."
      echo "[ghrctl.sh] Run '$0 restart' to deploy it."
      ;;


    # ----------------------------------------------------------
    # Unknown or missing command.
    # ----------------------------------------------------------

    *)
      usage
      ;;

  esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
