# syntax=docker/dockerfile:1

# AO Dev-kit workspace image: the approved AO workspace image (harnesses + the
# release-matched ao-worker/ao binaries) plus a small, plain set of common
# developer tools. The tooling is layered ON TOP of the approved base so the
# baked /usr/local/bin/ao-worker and /ao are inherited byte-for-byte — their
# SHA-256 hashes (recorded in /usr/local/share/ao-worker.sha256) stay identical,
# so the reconciler's preinstalled fast path still matches and no PTY upload is
# needed. Both dev-kit templates (medium + large) use this one image; they differ
# only by the per-workspace memory/CPU limits set at template-push time.
ARG AO_WORKSPACE_IMAGE=ao-coder-workspace:local
FROM ${AO_WORKSPACE_IMAGE}

USER root
# Plain developer tooling. Kept deliberately small ("nothing fancy"): a search
# tool, JSON/archive helpers, an editor, and a Python + build toolchain for
# repos that need to compile native deps. Add to this list to grow the kit.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        build-essential \
        jq \
        less \
        python3 \
        python3-pip \
        python3-venv \
        ripgrep \
        tree \
        unzip \
        vim && \
    rm -rf /var/lib/apt/lists/*

# Restore the standard Coder user; the base image's ao-worker/ao and their
# recorded hashes are untouched by the layer above.
USER coder
