# Common base for the per-harness sandbox templates: OS tooling, Node (the npm
# harnesses need it), the GitHub CLI, the agent browser, and the worker
# user/workspace layout.
# publish-nodeops-template.sh appends exactly one nodeops/harness/*.Dockerfile
# snippet plus the worker bake layer; Sandbox.Dockerfile remains the all-in-one
# variant. Keeping one agent per template roughly halves the image, which
# shrinks the provider's cold-host pull - the dominant worst-case cost of
# creating a sandbox. The agent browser adds about 800 MB unpacked back
# (Chromium and its libraries about 650 MB, fonts about 150 MB).
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
    groupadd --gid 10001 ao-worker && \
    useradd --uid 10001 --gid ao-worker --home-dir /workspace/.ao/home \
        --shell /bin/bash ao-worker && \
    mkdir -p /workspace/repository /workspace/.ao/home /workspace/.ao/worker && \
    chown -R ao-worker:ao-worker /workspace && \
    rm -rf /var/lib/apt/lists/* && \
    gh --version && \
    node --version

# The agent browser ao-worker starts on first use: headless Chromium, fonts that
# render CJK, Arabic and Devanagari pages instead of boxes, and the agent-browser
# CLI. Freestyle replays these steps on Ubuntu, whose chromium package is a snap
# stub, so there Chromium is Playwright's build (version kept in step with
# frontend/package-lock.json) linked to /usr/local/bin/chromium; Ubuntu releases
# after 25.x, which Playwright 1.60 has no build for, take its 24.04 build.
# agent-browser version and sha256 pins: keep in lockstep with
# frontend/scripts/prepare-agent-browser.mjs.
RUN apt-get update && \
    apt-get install --yes --no-install-recommends \
        fonts-liberation \
        fonts-noto-cjk \
        fonts-noto-color-emoji \
        fonts-noto-core && \
    . /etc/os-release && \
    if [ "$ID" = ubuntu ]; then \
        case "$VERSION_ID" in \
            2[0-5].*) ;; \
            *) export PLAYWRIGHT_HOST_PLATFORM_OVERRIDE="ubuntu24.04-$(uname -m | sed 's/x86_64/x64/; s/aarch64/arm64/')" ;; \
        esac && \
        PLAYWRIGHT_BROWSERS_PATH=/opt/ao-browser \
            npx --yes playwright@1.60.0 install --with-deps --no-shell chromium && \
        set -- /opt/ao-browser/chromium-*/chrome-linux*/chrome && \
        if [ "$#" -ne 1 ] || [ ! -x "$1" ]; then echo "expected one Playwright Chromium, found: $*" >&2; exit 1; fi && \
        ln -sfn "$1" /usr/local/bin/chromium && \
        rm -rf /root/.npm; \
    else \
        apt-get install --yes --no-install-recommends chromium; \
    fi && \
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
