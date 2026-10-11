# claude-code harness layer, replayed after Sandbox.base.Dockerfile by
# publish-freestyle-snapshot.sh.

RUN npm install --global @anthropic-ai/claude-code@2.1.228 @agentclientprotocol/claude-agent-acp@0.70.0 && \
    ln -sfn "$(npm root --global)/@anthropic-ai/claude-code/cli-wrapper.cjs" \
        /usr/local/bin/claude && \
    rm -rf /root/.npm && \
    claude --version && \
    test -x "$(command -v claude-agent-acp)"
