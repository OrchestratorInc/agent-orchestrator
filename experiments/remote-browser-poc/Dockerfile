# The agent's browser for a remote host: headless Chromium, the same
# agent-browser build AO ships (up.sh fetches it and checks AO's sha256 pin),
# and the browser service.
# The fonts render CJK, Arabic and Devanagari pages instead of boxes.
FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      chromium fonts-liberation fonts-noto-color-emoji fonts-noto-core fonts-noto-cjk ca-certificates procps \
    && rm -rf /var/lib/apt/lists/*
COPY dist/agent-browser /usr/local/bin/agent-browser
COPY dist/rbhost /usr/local/bin/rbhost
ENV HOME=/root
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/rbhost"]
