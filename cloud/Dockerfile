# syntax=docker/dockerfile:1

ARG BUILDPLATFORM
ARG TARGETOS=linux
ARG TARGETARCH

FROM --platform=${BUILDPLATFORM} golang:1.27.1-bookworm AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/ao-cloud ./cmd/ao-cloud && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/ao-cloud-migrate ./cmd/ao-cloud-migrate && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/ao-cloud-healthcheck ./cmd/ao-cloud-healthcheck && \
    for worker_arch in amd64 arm64; do \
        CGO_ENABLED=0 GOOS=linux GOARCH="${worker_arch}" \
        go build -trimpath -ldflags="-s -w" -o "/out/linux-${worker_arch}/ao-worker" ./cmd/ao-worker && \
        CGO_ENABLED=0 GOOS=linux GOARCH="${worker_arch}" \
        go build -trimpath -ldflags="-s -w" -o "/out/linux-${worker_arch}/ao" ./cmd/ao-cloud-agent || exit 1; \
    done && \
    cp "/out/linux-${TARGETARCH}/ao-worker" /out/ao-worker && \
    cp "/out/linux-${TARGETARCH}/ao" /out/ao

# Trixie has no fix for CVE-2026-77214 yet. Rebuild Debian's pinned Expat
# 2.9.0 source on Trixie instead of mixing unstable runtime dependencies.
# Remove this stage once Trixie's libexpat1 includes the upstream bounds check.
FROM debian:trixie-slim AS expat-build
WORKDIR /build
RUN apt-get update && \
    apt-get install --yes --no-install-recommends \
        build-essential debhelper docbook-to-man fakeroot && \
    rm -rf /var/lib/apt/lists/*
ADD --checksum=sha256:b5ad165baca0f2f2ac2a02b6b4b66d68ee890f582df398da9f86399679ca9c29 \
    https://deb.debian.org/debian/pool/main/e/expat/expat_2.9.0-1.dsc ./
ADD --checksum=sha256:6e8ce7b52ebbaa423c59becc7cf1e6875c42e49228c062ac8123c10e700cec27 \
    https://deb.debian.org/debian/pool/main/e/expat/expat_2.9.0.orig.tar.gz ./
ADD --checksum=sha256:d23e632b05f10e269ee5426ff1ea962b4aec17fcd179c4dec14390a6793da764 \
    https://deb.debian.org/debian/pool/main/e/expat/expat_2.9.0-1.debian.tar.xz ./
COPY test/expat-buffer-bounds.c /build/
RUN dpkg-source -x expat_2.9.0-1.dsc source && \
    cd source && \
    dpkg-buildpackage -b -us -uc -Pstage1,noudeb --jobs=2 && \
    make -C build check && \
    cd /build && \
    dpkg -i libexpat1_2.9.0-1_*.deb libexpat1-dev_2.9.0-1_*.deb && \
    mkdir /out && \
    cp libexpat1_2.9.0-1_*.deb /out/libexpat1.deb && \
    cc -Wall -Wextra -Werror expat-buffer-bounds.c -lexpat -o /out/expat-buffer-bounds && \
    /out/expat-buffer-bounds

FROM node:22-trixie-slim AS worker
ARG TARGETARCH
ARG CLAUDE_CODE_VERSION=2.1.228
ARG CODEX_VERSION=0.147.0
ARG CURSOR_AGENT_VERSION=2026.08.11-e8db854
# OpenCode v2 ships as a NEW package, @opencode/cli (v1 was opencode-ai, whose
# `latest` never crosses to v2). Pinned to the v2 major; AO's opencode adapter is
# ported to v2 and gates a too-old binary loudly. Keep in lockstep with
# coder/Sandbox.Dockerfile.
ARG OPENCODE_VERSION=2
ARG GH_VERSION=2.97.0
RUN apt-get update && \
    apt-get upgrade --yes && \
    apt-get install --yes --no-install-recommends \
        bash \
        ca-certificates \
        curl \
        git \
        jq \
        openssh-client \
        procps \
        tar && \
    case "${TARGETARCH}" in \
        amd64|arm64) gh_arch="${TARGETARCH}" ;; \
        *) echo "unsupported GitHub CLI architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    curl --fail --location --silent --show-error \
        "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${gh_arch}.tar.gz" \
        | tar --strip-components=2 -xzf - -C /usr/local/bin \
            "gh_${GH_VERSION}_linux_${gh_arch}/bin/gh" && \
    rm -rf /var/lib/apt/lists/* && \
    npm install --global \
        "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
        "@agentclientprotocol/claude-agent-acp@0.70.0" \
        "@openai/codex@${CODEX_VERSION}" \
        "@opencode/cli@${OPENCODE_VERSION}" && \
    claude --version && \
    test -x "$(command -v claude-agent-acp)" && \
    codex --version && \
    opencode --version && \
    gh --version && \
    groupadd --gid 10001 ao-worker && \
    useradd --uid 10001 --gid ao-worker --home-dir /workspace/.ao/home --shell /bin/bash ao-worker && \
    mkdir -p /workspace/repository /workspace/.ao/home && \
    chown -R ao-worker:ao-worker /workspace
RUN case "${TARGETARCH}" in \
        amd64) cursor_arch=x64 ;; \
        arm64) cursor_arch=arm64 ;; \
        *) echo "unsupported Cursor Agent architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    mkdir -p "/opt/cursor-agent/${CURSOR_AGENT_VERSION}" && \
    curl --fail --location --silent --show-error \
        "https://downloads.cursor.com/lab/${CURSOR_AGENT_VERSION}/linux/${cursor_arch}/agent-cli-package.tar.gz" \
        | tar --strip-components=1 -xzf - -C "/opt/cursor-agent/${CURSOR_AGENT_VERSION}" && \
    ln -s "/opt/cursor-agent/${CURSOR_AGENT_VERSION}/cursor-agent" /usr/local/bin/cursor-agent && \
    cursor-agent --version
RUN --mount=type=bind,from=expat-build,source=/out,target=/tmp/expat-fix \
    dpkg -i /tmp/expat-fix/libexpat1.deb && \
    /tmp/expat-fix/expat-buffer-bounds && \
    test "$(dpkg-query -W -f='${Version}' libexpat1)" = 2.9.0-1
COPY --from=build --chown=ao-worker:ao-worker /out/ao-worker /ao-worker
COPY --from=build --chown=ao-worker:ao-worker /out/ao /usr/local/bin/ao
USER ao-worker
WORKDIR /workspace/repository
ENTRYPOINT ["/ao-worker"]

FROM gcr.io/distroless/static-debian12:nonroot AS control-plane
COPY --from=build --chown=nonroot:nonroot /out/ao-cloud /ao-cloud
COPY --from=build --chown=nonroot:nonroot /out/ao-cloud-migrate /ao-cloud-migrate
COPY --from=build --chown=nonroot:nonroot /out/ao-cloud-healthcheck /ao-cloud-healthcheck
# Workers are uploaded into sandboxes, not run here. /ao-worker and /ao are
# always the linux/amd64 build (the contract every provider and bake script
# relies on); the linux/arm64 build sits beside them for Coder workspaces on
# arm64 hosts, which the control plane detects and serves per architecture.
COPY --from=build --chown=nonroot:nonroot /out/linux-amd64/ao-worker /ao-worker
COPY --from=build --chown=nonroot:nonroot /out/linux-amd64/ao /ao
COPY --from=build --chown=nonroot:nonroot /out/linux-arm64/ao-worker /ao-worker-linux-arm64
COPY --from=build --chown=nonroot:nonroot /out/linux-arm64/ao /ao-linux-arm64
EXPOSE 8080
ENTRYPOINT ["/ao-cloud"]
