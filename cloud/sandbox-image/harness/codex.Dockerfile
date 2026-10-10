# codex harness layer, replayed after Sandbox.base.Dockerfile by
# publish-freestyle-snapshot.sh.

RUN npm install --global @openai/codex@0.147.0 && \
    rm -rf /root/.npm && \
    codex --version
