#!/bin/bash
# Build the browser service and (re)start its container, then wait until ready.
#   NAME=rbpoc PORT=8080 NETNS=<the AO daemon's container> ./up.sh
# The daemon then gets AO_AGENT_BROWSER_URL=http://127.0.0.1:8080 and
# AO_AGENT_BROWSER_TOKEN=$(cat /tmp/rbpoc-token); agents get ./browser with RB_TOKEN.
set -euo pipefail
cd "$(dirname "$0")"
NAME=${NAME:-rbpoc}; PORT=${PORT:-8080}
[ -s /tmp/rbpoc-token ] || (umask 077; openssl rand -hex 24 > /tmp/rbpoc-token)
free_mb=$(df -k / | tail -1 | awk '{print int($4/1024)}')
if [ "$free_mb" -lt 500 ]; then echo "refusing to build: only ${free_mb} MB free on /" >&2; exit 3; fi
# Builds, downloads and lookups run before the old container is removed, so a failure
# leaves it running. Docker names its architecture as Go does (arm64, amd64);
# agent-browser names it as Node does (x64).
arch=$(docker version -f '{{.Server.Arch}}')
target=linux-${arch/amd64/x64}
# The service lives in the cloud module. GOWORK=off: outside the workspace the
# build needs only the modules rbhost imports, not the backend's.
(cd ../../cloud && GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o ../experiments/remote-browser-poc/dist/rbhost ./cmd/rbhost)
# agent-browser is the build AO ships: the version, release URL and sha256 pins come
# from the desktop's fetch script. A file that already has the pinned hash is kept.
# ponytail: sed reads that script's layout and fails loudly if it changes; move the
# pins to a JSON file both scripts read if that happens often.
pins=../../frontend/scripts/prepare-agent-browser.mjs
pin=$(sed -n "/\"$target\": {/,/}/p" "$pins")
sha=$(sed -n 's/.*sha256: "\([0-9a-f]*\)".*/\1/p' <<<"$pin")
[ -n "$sha" ] || { echo "no agent-browser build for $target in $pins" >&2; exit 1; }
sha256() { openssl dgst -sha256 -r "$1" 2>/dev/null | cut -d' ' -f1; }
if [ "$(sha256 dist/agent-browser)" != "$sha" ]; then
  version=$(sed -n 's/^const VERSION = "\([^"]*\)".*/\1/p' "$pins")
  base=$(sed -n "s/^const RELEASE_BASE = \`\([^\`]*\)\`.*/\1/p" "$pins")
  url=${base/\$\{VERSION\}/$version}/$(sed -n 's/.*asset: "\([^"]*\)".*/\1/p' <<<"$pin")
  echo "fetching $url"
  curl -fsSL -o dist/agent-browser "$url"
  got=$(sha256 dist/agent-browser)
  [ "$got" = "$sha" ] || { rm -f dist/agent-browser; echo "agent-browser checksum mismatch: expected $sha, got $got" >&2; exit 1; }
fi
chmod +x dist/agent-browser
docker build -q -t rbpoc:latest . >/dev/null
# The browser takes this machine's time zone, language and light/dark mode, as the
# person's own Chrome would. Time zone: /etc/localtime, else /etc/timezone, $TZ, UTC.
# Language: macOS's AppleLocale, else $LANG, en-US.
tz=$(readlink /etc/localtime 2>/dev/null | sed -n 's#.*/zoneinfo/##p' || true)
[ -n "$tz" ] || tz=$(cat /etc/timezone 2>/dev/null || echo "${TZ:-UTC}")
lang=$({ defaults read -g AppleLocale 2>/dev/null || echo "${LANG:-}"; } | sed 's/[.@].*//; s/_/-/')
case $lang in ''|C|POSIX) lang=en-US ;; esac
scheme=$(defaults read -g AppleInterfaceStyle 2>/dev/null | grep -qi dark && echo dark || echo light)
docker rm -f "$NAME" >/dev/null 2>&1 || true
extra=(-p "127.0.0.1:${PORT}:8080")
# NETNS joins the AO daemon's container network (it publishes 8080), so the agent's
# Chromium and the session's dev servers share localhost, as on one machine.
[ -n "${NETNS:-}" ] && extra=(--network "container:${NETNS}")
# The profile (logins, open tabs) lives in the $NAME-profile volume, so redeploys keep
# it. --init reaps Chromium's exited helper processes.
docker run -d --init --name "$NAME" --shm-size=1g -v "$NAME-profile:/tmp/rb-profile" ${extra[@]+"${extra[@]}"} \
  -e RB_TZ="${RB_TZ:-$tz}" -e RB_LANG="${RB_LANG:-$lang}" -e RB_SCHEME="${RB_SCHEME:-$scheme}" \
  -e RB_CHROME_LOG="${RB_CHROME_LOG:-}" -e RB_TOKEN="$(cat /tmp/rbpoc-token)" rbpoc:latest >/dev/null
for _ in $(seq 1 120); do curl -sf "http://127.0.0.1:${PORT}/stats?token=$(cat /tmp/rbpoc-token)" >/dev/null && { echo "up $NAME on :$PORT"; exit 0; }; sleep 0.5; done
echo "$NAME did not answer on :$PORT within 60 s; its last log lines:" >&2
docker logs --tail 20 "$NAME" >&2
exit 1
