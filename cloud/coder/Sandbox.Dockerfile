# syntax=docker/dockerfile:1

# The binaries come from the exact control-plane image that will manage these
# workspaces. Coder's bootstrap verifies their SHA-256 hashes before using the
# fast path, so a stale template safely falls back to the PTY upload.
ARG AO_CONTROL_PLANE_IMAGE=ao-cloud-control-plane:local
FROM ${AO_CONTROL_PLANE_IMAGE} AS ao-release

FROM node:22-bookworm-slim AS node-runtime

FROM codercom/enterprise-base:ubuntu
ARG CLAUDE_CODE_VERSION=2.1.228
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
