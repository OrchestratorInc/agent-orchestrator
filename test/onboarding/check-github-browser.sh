#!/usr/bin/env bash
set -euo pipefail
repo_root=$(cd "$(dirname "$0")/../.." && pwd)
: "${AO_GITHUB_BROWSER_BINARY:?Set the path to the released gh binary}"
fixture_dir=$(mktemp -d)
fixture_pid=
cleanup() {
  if [[ -s "$fixture_dir/url" ]]; then
    curl --silent --show-error --max-time 5 -X POST "$(cat "$fixture_dir/url")/shutdown" || true
  fi
  if [[ -n "$fixture_pid" ]]; then
    for ((attempt=0; attempt<10; attempt++)); do
      kill -0 "$fixture_pid" 2>/dev/null || break
      sleep 1
    done
    kill "$fixture_pid" 2>/dev/null || true
    wait "$fixture_pid" 2>/dev/null || true
  fi
  cat "$fixture_dir/daemon.log"
  rm -rf "$fixture_dir"
}
trap cleanup EXIT
AO_GITHUB_FIXTURE_FILE="$fixture_dir/url" go -C "$repo_root/backend" test ./e2e -run '^TestGitHubDeviceBrowserFixture$' -count=1 -v -timeout=12m > "$fixture_dir/daemon.log" 2>&1 &
fixture_pid=$!
for ((attempt=0; attempt<120; attempt++)); do
  [[ -s "$fixture_dir/url" ]] && break
  kill -0 "$fixture_pid" 2>/dev/null || exit 1
  sleep 1
done
[[ -s "$fixture_dir/url" ]] || { echo "GitHub fixture did not start" >&2; exit 1; }
cd "$repo_root/frontend"
AO_GITHUB_FIXTURE_URL=$(cat "$fixture_dir/url") ./node_modules/.bin/playwright test e2e/github-onboarding-connected.spec.ts --workers=1
