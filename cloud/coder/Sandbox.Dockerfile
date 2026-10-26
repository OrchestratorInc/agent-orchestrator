# syntax=docker/dockerfile:1

# The binaries come from the exact control-plane image that will manage these
# workspaces. Coder's bootstrap verifies their SHA-256 hashes before using the
# fast path, so a stale template safely falls back to the PTY upload.
ARG AO_CONTROL_PLANE_IMAGE=ao-cloud-control-plane:local
FROM ${AO_CONTROL_PLANE_IMAGE} AS ao-release

FROM node:22-bookworm-slim AS node-runtime

FROM codercom/enterprise-base:ubuntu
ARG CLAUDE_CODE_VERSION=2.1.287
# OpenCode v2 ships as a NEW package, @opencode/cli (v1 was opencode-ai, whose
# `latest` never crosses to v2). Pinned to the v2 major. The shared adapter
# (backend/internal/adapters/agent/opencode) is ported to v2's plugin/CLI surface
# and gates a too-old binary loudly. Keep in lockstep with cloud/Dockerfile.
ARG OPENCODE_VERSION=2
# Codex (npm) and cursor-agent (native tarball) complete the four supported
# harnesses (claude + opencode are installed above). Without these, codex/cursor
# sessions hang with "harness binary unavailable". Keep versions in lockstep with
# cloud/scripts/bake-coder-azure-image.sh.
ARG CODEX_VERSION=0.147.0
ARG CURSOR_AGENT_VERSION=2026.08.11-e8db854
USER root

# Keep the normal Coder image/user/sudo contract, while baking the harnesses that
# this approved template exposes. Copying the official Node runtime avoids a
# package-manager install on every workspace start.
COPY --from=node-runtime /usr/local/ /usr/local/
RUN npm install --global \
      "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
      "@agentclientprotocol/claude-agent-acp@0.70.0" \
      "@opencode/cli@${OPENCODE_VERSION}" \
      "@openai/codex@${CODEX_VERSION}" && \
    claude --version && \
    test -x "$(command -v claude-agent-acp)" && \
    opencode --version && \
    codex --version && \
    mkdir -p /etc/skel/.local/bin && \
    ln -sfn "$(readlink -f "$(command -v claude)")" \
      /etc/skel/.local/bin/claude && \
    ln -sfn "$(readlink -f "$(command -v opencode)")" \
      /etc/skel/.local/bin/opencode && \
    ln -sfn "$(readlink -f "$(command -v codex)")" \
      /etc/skel/.local/bin/codex && \
    test -x /etc/skel/.local/bin/claude && \
    test -x /etc/skel/.local/bin/opencode && \
    test -x /etc/skel/.local/bin/codex

# The agent browser ao-worker starts on first use: headless Chromium, fonts that
# render CJK, Arabic and Devanagari pages instead of boxes, and the agent-browser
# CLI. Ubuntu's chromium package is a snap stub that cannot run in a container,
# so Chromium is Playwright's build (version kept in step with
# frontend/package-lock.json) under a root-owned path every user can read,
# linked to /usr/local/bin/chromium. Playwright 1.60 has no build for Ubuntu
# releases after 25.x (enterprise-base:ubuntu is 26.04), so those take its 24.04
# build and dependency list. agent-browser version and sha256 pins: keep in
# lockstep with frontend/scripts/prepare-agent-browser.mjs.
RUN apt-get update && \
    apt-get install --yes --no-install-recommends \
      fonts-liberation \
      fonts-noto-cjk \
      fonts-noto-color-emoji \
      fonts-noto-core && \
    . /etc/os-release && \
    case "$VERSION_ID" in \
      2[0-5].*) ;; \
      *) export PLAYWRIGHT_HOST_PLATFORM_OVERRIDE="ubuntu24.04-$(uname -m | sed 's/x86_64/x64/; s/aarch64/arm64/')" ;; \
    esac && \
    PLAYWRIGHT_BROWSERS_PATH=/opt/ao-browser \
      npx --yes playwright@1.60.0 install --with-deps --no-shell chromium && \
    rm -rf /var/lib/apt/lists/* /root/.npm && \
    set -- /opt/ao-browser/chromium-*/chrome-linux*/chrome && \
    if [ "$#" -ne 1 ] || [ ! -x "$1" ]; then echo "expected one Playwright Chromium, found: $*" >&2; exit 1; fi && \
    ln -sfn "$1" /usr/local/bin/chromium && \
    chromium --version && \
    agent_browser_version=0.38.1 && \
    case "$(dpkg --print-architecture)" in \
      amd64) agent_browser_target=linux-x64 \
        agent_browser_sha256=5100149a1903211c889de4e545bf36d90803740cea4f99aa22651649f9205ea1 ;; \
      arm64) agent_browser_target=linux-arm64 \
        agent_browser_sha256=937b315ee0761e8a62f7950ddcfef9b3d3d8e8d5eb9c9d2bf9e23e5725664511 ;; \
      *) echo "unsupported agent-browser architecture: $(dpkg --print-architecture)" >&2; exit 1 ;; \
    esac && \
    curl --fail --location --silent --show-error -o /usr/local/bin/agent-browser \
      "https://github.com/vercel-labs/agent-browser/releases/download/v${agent_browser_version}/agent-browser-${agent_browser_target}" && \
    echo "${agent_browser_sha256}  /usr/local/bin/agent-browser" | sha256sum -c - && \
    chmod 0755 /usr/local/bin/agent-browser && \
    agent-browser --version

# cursor-agent ships as a native tarball (amd64 -> x64), not an npm package.
RUN mkdir -p "/opt/cursor-agent/${CURSOR_AGENT_VERSION}" && \
    curl --fail --location --silent --show-error \
      "https://downloads.cursor.com/lab/${CURSOR_AGENT_VERSION}/linux/x64/agent-cli-package.tar.gz" \
      | tar --strip-components=1 -xzf - -C "/opt/cursor-agent/${CURSOR_AGENT_VERSION}" && \
    ln -sfn "/opt/cursor-agent/${CURSOR_AGENT_VERSION}/cursor-agent" /usr/local/bin/cursor-agent && \
    ln -sfn /usr/local/bin/cursor-agent /etc/skel/.local/bin/cursor-agent && \
    HOME=/root cursor-agent --version && \
    test -x /etc/skel/.local/bin/cursor-agent

COPY --from=ao-release /ao-worker /usr/local/bin/ao-worker
COPY --from=ao-release /ao /usr/local/bin/ao
RUN chmod 0755 /usr/local/bin/ao-worker /usr/local/bin/ao && \
    sha256sum /usr/local/bin/ao-worker /usr/local/bin/ao \
      > /usr/local/share/ao-worker.sha256

USER coder
