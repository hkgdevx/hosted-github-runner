FROM ubuntu:24.04

ARG DEBIAN_FRONTEND=noninteractive

# ============================================================
# Base OS / CI utilities
# ============================================================

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    wget \
    git \
    git-lfs \
    gnupg \
    jq \
    uuid-runtime \
    unzip \
    zip \
    tar \
    gzip \
    xz-utils \
    openssh-client \
    rsync \
    procps \
    iproute2 \
    dnsutils \
    tini \
    \
    python3 \
    python3-pip \
    python3-venv \
    python3-dev \
    \
    build-essential \
    gcc \
    g++ \
    make \
    libssl-dev \
    libffi-dev \
    \
    shellcheck \
    \
    # --------------------------------------------------------
    # Playwright / Chromium runtime dependencies
    # Browser itself is installed by the project's Playwright
    # version during CI.
    # --------------------------------------------------------
    libnss3 \
    libnspr4 \
    libatk-bridge2.0-0 \
    libatk1.0-0 \
    libatspi2.0-0 \
    libdrm2 \
    libxkbcommon0 \
    libxcomposite1 \
    libxdamage1 \
    libxfixes3 \
    libxrandr2 \
    libgbm1 \
    libgtk-3-0 \
    libasound2t64 \
    libxshmfence1 \
    libcups2 \
    libx11-xcb1 \
    libxcb1 \
    libxext6 \
    libx11-6 \
    libxrender1 \
    libxcursor1 \
    libxi6 \
    libxtst6 \
    fonts-liberation \
    xdg-utils \
    && rm -rf /var/lib/apt/lists/*


# ============================================================
# yq
# ============================================================

ARG YQ_VERSION="v4.47.2"

RUN ARCH="$(dpkg --print-architecture)" && \
    case "${ARCH}" in \
      amd64) YQ_ARCH="amd64" ;; \
      arm64) YQ_ARCH="arm64" ;; \
      *) echo "Unsupported architecture: ${ARCH}" && exit 1 ;; \
    esac && \
    curl -fsSL \
      "https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/yq_linux_${YQ_ARCH}" \
      -o /usr/local/bin/yq && \
    chmod +x /usr/local/bin/yq


# ============================================================
# Docker CLI only
#
# Docker daemon/containerd should run on the VPS host.
# Runner container talks to it through /var/run/docker.sock.
# ============================================================

RUN install -m 0755 -d /etc/apt/keyrings && \
    curl -fsSL \
      https://download.docker.com/linux/ubuntu/gpg \
      -o /etc/apt/keyrings/docker.asc && \
    chmod a+r /etc/apt/keyrings/docker.asc && \
    echo \
      "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] \
      https://download.docker.com/linux/ubuntu \
      $(. /etc/os-release && echo "${VERSION_CODENAME}") stable" \
      > /etc/apt/sources.list.d/docker.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
      docker-ce-cli \
      docker-buildx-plugin \
      docker-compose-plugin \
    && rm -rf /var/lib/apt/lists/*


# ============================================================
# Trivy
# ============================================================

RUN curl -fsSL \
      https://aquasecurity.github.io/trivy-repo/deb/public.key \
      | gpg --dearmor \
      -o /usr/share/keyrings/trivy.gpg && \
    echo \
      "deb [signed-by=/usr/share/keyrings/trivy.gpg] \
      https://aquasecurity.github.io/trivy-repo/deb generic main" \
      > /etc/apt/sources.list.d/trivy.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
      trivy \
    && rm -rf /var/lib/apt/lists/*


# ============================================================
# Hadolint
# ============================================================

ARG HADOLINT_VERSION="v2.14.0"

RUN ARCH="$(dpkg --print-architecture)" && \
    case "${ARCH}" in \
      amd64) HADOLINT_ARCH="x86_64" ;; \
      arm64) HADOLINT_ARCH="arm64" ;; \
      *) echo "Unsupported architecture: ${ARCH}" && exit 1 ;; \
    esac && \
    curl -fsSL \
      "https://github.com/hadolint/hadolint/releases/download/${HADOLINT_VERSION}/hadolint-Linux-${HADOLINT_ARCH}" \
      -o /usr/local/bin/hadolint && \
    chmod +x /usr/local/bin/hadolint


# ============================================================
# actionlint
# ============================================================

ARG ACTIONLINT_VERSION="1.7.7"

RUN ARCH="$(dpkg --print-architecture)" && \
    case "${ARCH}" in \
      amd64) ACTIONLINT_ARCH="amd64" ;; \
      arm64) ACTIONLINT_ARCH="arm64" ;; \
      *) echo "Unsupported architecture: ${ARCH}" && exit 1 ;; \
    esac && \
    curl -fsSL \
      "https://github.com/rhysd/actionlint/releases/download/v${ACTIONLINT_VERSION}/actionlint_${ACTIONLINT_VERSION}_linux_${ACTIONLINT_ARCH}.tar.gz" \
      -o /tmp/actionlint.tar.gz && \
    tar -xzf /tmp/actionlint.tar.gz \
      -C /usr/local/bin \
      actionlint && \
    chmod +x /usr/local/bin/actionlint && \
    rm -f /tmp/actionlint.tar.gz


# ============================================================
# GitHub CLI
# ============================================================

RUN mkdir -p -m 755 /etc/apt/keyrings && \
    curl -fsSL \
      https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      -o /etc/apt/keyrings/githubcli-archive-keyring.gpg && \
    chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg && \
    echo \
      "deb [arch=$(dpkg --print-architecture) \
      signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] \
      https://cli.github.com/packages stable main" \
      > /etc/apt/sources.list.d/github-cli.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends gh && \
    rm -rf /var/lib/apt/lists/*


# ============================================================
# GitHub Actions Runner user
# ============================================================

RUN useradd \
      --create-home \
      --shell /bin/bash \
      gthb-runner


# ============================================================
# Install latest GitHub Actions runner for current architecture
# ============================================================

WORKDIR /home/gthb-runner/actions-runner

RUN ARCH="$(dpkg --print-architecture)" && \
    case "${ARCH}" in \
      amd64) RUNNER_ARCH="x64" ;; \
      arm64) RUNNER_ARCH="arm64" ;; \
      armhf) RUNNER_ARCH="arm" ;; \
      *) echo "Unsupported architecture: ${ARCH}" && exit 1 ;; \
    esac && \
    \
    RUNNER_VERSION="$( \
      curl -fsSL https://api.github.com/repos/actions/runner/releases/latest \
      | jq -r '.tag_name' \
      | sed 's/^v//' \
    )" && \
    \
    echo "Installing GitHub Actions Runner ${RUNNER_VERSION} for ${RUNNER_ARCH}" && \
    \
    curl -fsSLo actions-runner.tar.gz \
      "https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-${RUNNER_ARCH}-${RUNNER_VERSION}.tar.gz" && \
    \
    tar -xzf actions-runner.tar.gz && \
    rm actions-runner.tar.gz && \
    chown -R gthb-runner:gthb-runner /home/gthb-runner/actions-runner

# ============================================================
# Runner cache directories
# ============================================================

RUN mkdir -p \
      /home/gthb-runner/.cache/trivy \
      /home/gthb-runner/.cache/ms-playwright \
      /home/gthb-runner/.cache/buildx \
      /home/gthb-runner/.local/share/pnpm && \
    chown -R gthb-runner:gthb-runner \
      /home/gthb-runner/.cache \
      /home/gthb-runner/.local


# ============================================================
# Entrypoint
# ============================================================

COPY scripts/entrypoint.sh /home/gthb-runner/actions-runner/entrypoint.sh

RUN sed -i 's/\r$//' \
      /home/gthb-runner/actions-runner/entrypoint.sh && \
    chmod 0755 \
      /home/gthb-runner/actions-runner/entrypoint.sh && \
    chown gthb-runner:gthb-runner \
      /home/gthb-runner/actions-runner/entrypoint.sh


USER gthb-runner

ENTRYPOINT [ "/usr/bin/tini", "--", "./entrypoint.sh"]
