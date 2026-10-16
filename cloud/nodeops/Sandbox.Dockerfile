FROM nodeops/sandbox:debian

RUN apt-get update && \
    apt-get install --yes --no-install-recommends \
        bash \
        ca-certificates \
        curl \
        git \
        gnupg \
        jq \
        openssh-client \
        procps \
        tar \
        util-linux && \
    mkdir -p /etc/apt/keyrings && \
    curl --fail --location --silent --show-error \
        https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key \
        | gpg --dearmor -o /etc/apt/keyrings/nodesource.gpg && \
    echo "deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_22.x nodistro main" \
        > /etc/apt/sources.list.d/nodesource.list && \
    apt-get update && \
    apt-get install --yes --no-install-recommends nodejs && \
    architecture="$(dpkg --print-architecture)" && \
    gh_version=2.97.0 && \
    curl --fail --location --silent --show-error \
        "https://github.com/cli/cli/releases/download/v${gh_version}/gh_${gh_version}_linux_${architecture}.tar.gz" \
        | tar --strip-components=2 -xzf - -C /usr/bin \
            "gh_${gh_version}_linux_${architecture}/bin/gh" && \
    npm install --global \
        @anthropic-ai/claude-code@2.1.287 \
        @agentclientprotocol/claude-agent-acp@0.70.0 \
        @openai/codex@0.147.0 && \
    ln -sfn "$(npm root --global)/@anthropic-ai/claude-code/cli-wrapper.cjs" \
        /usr/local/bin/claude && \
    groupadd --gid 10001 ao-worker && \
    useradd --uid 10001 --gid ao-worker --home-dir /workspace/.ao/home \
        --shell /bin/bash ao-worker && \
    mkdir -p /workspace/repository /workspace/.ao/home /workspace/.ao/worker && \
    chown -R ao-worker:ao-worker /workspace && \
    rm -rf /var/lib/apt/lists/* /root/.npm && \
    claude --version && \
    test -x "$(command -v claude-agent-acp)" && \
    codex --version

RUN gh --version

RUN architecture="$(dpkg --print-architecture)" && \
    case "$architecture" in \
        amd64) cursor_arch=x64 ;; \
        arm64) cursor_arch=arm64 ;; \
        *) echo "unsupported Cursor Agent architecture: $architecture" >&2; exit 1 ;; \
    esac && \
    cursor_version=2026.08.11-e8db854 && \
    mkdir -p "/opt/cursor-agent/$cursor_version" && \
    curl --fail --location --silent --show-error \
        "https://downloads.cursor.com/lab/$cursor_version/linux/$cursor_arch/agent-cli-package.tar.gz" \
        | tar --strip-components=1 -xzf - -C "/opt/cursor-agent/$cursor_version" && \
    ln -s "/opt/cursor-agent/$cursor_version/cursor-agent" /usr/local/bin/cursor-agent && \
    cursor-agent --version

# The agent browser ao-worker starts on first use: headless Chromium, fonts that
# render CJK, Arabic and Devanagari pages instead of boxes, and the agent-browser
# CLI. agent-browser version and sha256 pins: keep in lockstep with
# frontend/scripts/prepare-agent-browser.mjs.
RUN apt-get update && \
    apt-get install --yes --no-install-recommends \
        chromium \
        fonts-liberation \
        fonts-noto-cjk \
        fonts-noto-color-emoji \
        fonts-noto-core && \
    rm -rf /var/lib/apt/lists/* && \
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
