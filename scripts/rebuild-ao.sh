#!/usr/bin/env bash
# Rebuild the local self-build from the current checkout: ao CLI (via
# daemon-build.sh), the Electron .app (which bundles a fresh daemon), codesign,
# and a swap into /Applications. Quits a running app first — warn anyone with
# live sessions that the daemon restarts (worker sessions survive in tmux).
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
app_name="Agent Orchestrator.app"
packaged_app="${repo_root}/frontend/out/Agent Orchestrator-darwin-arm64/${app_name}"
installed_app="/Applications/${app_name}"
codesign_identity="AO-Local-CodeSign"

printf '==> building ao CLI\n'
bash "${script_dir}/daemon-build.sh"

printf '==> packaging %s (bundles the same daemon)\n' "${app_name}"
(cd "${repo_root}/frontend" && npm run package)

if [[ ! -d "${packaged_app}" ]]; then
	printf 'packaged app bundle missing at %s\n' "${packaged_app}" >&2
	exit 1
fi

printf '==> codesigning with %s\n' "${codesign_identity}"
codesign --force --deep --sign "${codesign_identity}" "${packaged_app}"

if pgrep -f "/Applications/${app_name}/Contents/MacOS/" >/dev/null 2>&1; then
	printf '==> quitting running app\n'
	osascript -e "tell application \"${app_name%.app}\" to quit"
	sleep 3
fi

printf '==> installing to %s\n' "${installed_app}"
ditto "${packaged_app}" "${installed_app}"

printf '==> launching app\n'
open "${installed_app}"

printf '==> done\n'
