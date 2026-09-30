#!/usr/bin/env bash
# Install AO's daemon resources from a desktop release or a locally built host bundle.
set -euo pipefail

usage() {
	printf '%s\n' 'Usage: setup-self-hosted.sh [--bundle PATH] [--tunnel] [--install-only]'
	printf '%s\n' 'Installs AO under ~/.ao/host, starts a user service, and prints pairing details.'
}

bundle=""
tunnel=false
install_only=false
while (($#)); do
	case "$1" in
		--bundle) bundle="${2:?--bundle needs a path}"; shift 2 ;;
		--tunnel) tunnel=true; shift ;;
		--install-only) install_only=true; shift ;;
		-h|--help) usage; exit 0 ;;
		*) usage >&2; exit 2 ;;
	esac
done

if [[ "$(id -u)" == 0 ]]; then
	printf '%s\n' 'Run this as the user who will own AO sessions, not as root.' >&2
	exit 1
fi

host_root="${AO_HOST_INSTALL_DIR:-$HOME/.ao/host}"
umask 077
mkdir -p "$host_root/releases"
stage="$(mktemp -d "$host_root/.setup.XXXXXX")"
lock_owned=false
cleanup() {
	if "$lock_owned"; then
		rmdir "$lock_dir" 2>/dev/null || true
	fi
	rm -rf "$stage"
}
trap cleanup EXIT

platform="$(uname -s)"
arch="$(uname -m)"
case "$platform:$arch" in
	Darwin:arm64) asset='agent-orchestrator-darwin-arm64.zip' ;;
	Darwin:x86_64) asset='agent-orchestrator-darwin-x64.zip' ;;
	Linux:x86_64) asset='agent-orchestrator-linux-x64.AppImage' ;;
	Linux:aarch64|Linux:arm64) if [[ -z "$bundle" ]]; then
		printf 'No published desktop artifact for %s/%s; pass --bundle from a native build.\n' "$platform" "$arch" >&2
		exit 1
	fi ;;
	*) printf 'Unsupported host platform: %s/%s\n' "$platform" "$arch" >&2; exit 1 ;;
esac
command -v python3 >/dev/null || { printf '%s\n' 'python3 is required for host setup.' >&2; exit 1; }
command -v git >/dev/null || { printf '%s\n' 'git is required for AO projects and worktrees.' >&2; exit 1; }

if [[ -n "$bundle" ]]; then
	[[ -f "$bundle" ]] || { printf 'Bundle not found: %s\n' "$bundle" >&2; exit 1; }
	python3 - "$bundle" <<'PY'
import posixpath, sys, tarfile
with tarfile.open(sys.argv[1], 'r:gz') as archive:
    for entry in archive:
        name = entry.name
        if (name != 'resources' and not name.startswith('resources/')) or '..' in name.split('/'):
            raise SystemExit(f'Unsafe host bundle path: {name}')
        if entry.issym():
            target = posixpath.normpath(posixpath.join(posixpath.dirname(name), entry.linkname))
            if posixpath.isabs(entry.linkname) or not target.startswith('resources/'):
                raise SystemExit(f'Unsafe host bundle link: {name}')
        elif not (entry.isfile() or entry.isdir()):
            raise SystemExit(f'Unsafe host bundle entry: {name}')
PY
	tar -xzf "$bundle" -C "$stage"
	resources="$stage/resources"
else
	command -v curl >/dev/null || { printf '%s\n' 'curl is required.' >&2; exit 1; }
	metadata="$stage/release.json"
	curl -fsSL --retry 3 'https://api.github.com/repos/Untrivial-ai/agent-orchestrator/releases/latest' -o "$metadata"
	asset_info="$(python3 - "$metadata" "$asset" <<'PY'
import json, re, sys
release = json.load(open(sys.argv[1]))
item = next((a for a in release.get('assets', []) if a['name'] == sys.argv[2]), None)
if not item or not re.fullmatch(r'sha256:[0-9a-f]{64}', item.get('digest') or ''):
    raise SystemExit(f'No verified release asset: {sys.argv[2]}')
print(item['browser_download_url'])
print(item['digest'][7:])
PY
)"
	url="${asset_info%%$'\n'*}"
	digest="${asset_info#*$'\n'}"
	archive="$stage/$asset"
	curl -fL --retry 3 "$url" -o "$archive"
	if command -v shasum >/dev/null; then
		actual="$(shasum -a 256 "$archive" | awk '{print $1}')"
	else
		actual="$(sha256sum "$archive" | awk '{print $1}')"
	fi
	[[ "$actual" == "$digest" ]] || { printf '%s\n' 'Release SHA-256 mismatch.' >&2; exit 1; }
	if [[ "$platform" == Darwin ]]; then
		ditto -x -k "$archive" "$stage/extracted"
	else
		mkdir "$stage/extracted"
		(cd "$stage/extracted" && chmod +x "$archive" && "$archive" --appimage-extract >/dev/null)
	fi
	mapfile_path="$(find "$stage/extracted" \( -path '*/resources/daemon/ao' -o -path '*/Resources/daemon/ao' \) -type f -print -quit)"
	[[ -n "$mapfile_path" ]] || { printf '%s\n' 'AO daemon missing from release artifact.' >&2; exit 1; }
	resources="$(dirname "$(dirname "$mapfile_path")")"
fi

ao="$resources/daemon/ao"
node="$resources/acp-runtime/node/bin/node"
adapter="$resources/acp-runtime/node_modules/@agentclientprotocol/claude-agent-acp/dist/index.js"
tmux="$resources/tmux/bin/tmux"
for required in "$ao" "$node" "$adapter"; do
	[[ -f "$required" ]] || { printf 'Incomplete AO host package: %s\n' "$required" >&2; exit 1; }
done
[[ -f "$tmux" ]] || {
	printf '%s\n' 'AO host package is missing its bundled tmux.' >&2; exit 1;
}
[[ -x "$tmux" ]] || {
	printf '%s\n' 'AO host package bundled tmux is not executable.' >&2; exit 1;
}
"$ao" version >/dev/null
"$ao" remote-host --help >/dev/null || {
	printf '%s\n' 'This AO build predates self-hosted remote support; use --bundle from this PR.' >&2
	exit 1
}
"$node" --version >/dev/null

lock_dir="$host_root/.install.lock"
if ! mkdir "$lock_dir" 2>/dev/null; then
	if [[ -d "$lock_dir" ]]; then
		printf 'Install lock exists at %s; another setup may be running. If stale, remove it after confirming no setup is running.\n' "$lock_dir" >&2
	else
		printf 'Cannot create install lock at %s.\n' "$lock_dir" >&2
	fi
	exit 1
fi
lock_owned=true

# Do not attach a second service to a daemon owned by another AO installation.
status="$("$ao" status --json)"
state="$(printf '%s' "$status" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("state", ""))')"
if [[ "$state" == ready ]]; then
	current_exe="$(printf '%s' "$status" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("executablePath", ""))')"
	case "$current_exe" in
		"$host_root"/releases/*/resources/daemon/ao|"$host_root"/current/resources/daemon/ao) ;;
		*) printf 'Another AO daemon is already running: %s. Stop it before setting up this host.\n' "$current_exe" >&2; exit 1 ;;
	esac
elif [[ "$state" != stopped && "$state" != stale ]]; then
	printf 'An AO daemon is already present but not ready (%s); resolve it before host setup.\n' "$state" >&2
	exit 1
fi

if [[ "$platform" == Linux ]] && ! "$install_only"; then
	command -v systemctl >/dev/null || { printf '%s\n' 'systemd user services are required; use --install-only for another service manager.' >&2; exit 1; }
	systemctl --user show-environment >/dev/null 2>&1 || { printf '%s\n' 'systemd user services are unavailable; use --install-only for another service manager.' >&2; exit 1; }
fi
if [[ -e "$host_root/current" && ! -L "$host_root/current" ]]; then
	printf 'Refusing to replace non-symlink: %s/current\n' "$host_root" >&2
	exit 1
fi
previous_release="$(readlink "$host_root/current" 2>/dev/null || true)"
prune_allowed=true
if [[ "$previous_release" == releases/* && "${previous_release#releases/}" =~ ^[0-9]{14}-[0-9]+$ ]]; then
	previous_release="$host_root/$previous_release"
elif [[ -n "$previous_release" ]]; then
	previous_name="${previous_release#"$host_root"/releases/}"
	[[ "$previous_name" != "$previous_release" && "$previous_name" =~ ^[0-9]{14}-[0-9]+$ ]] || prune_allowed=false
fi
running_release=""
if [[ "$state" == ready ]]; then
	case "$current_exe" in
		"$host_root"/releases/*/resources/daemon/ao) running_release="${current_exe%/resources/daemon/ao}" ;;
		"$host_root"/current/resources/daemon/ao) running_release="$previous_release" ;;
	esac
fi

release="$host_root/releases/$(date +%Y%m%d%H%M%S)-$$"
mkdir -p "$release/resources"
cp -R "$resources/daemon" "$resources/acp-runtime" "$resources/tmux" "$release/resources/"
ln -sfn "$release" "$host_root/current"
prune_old_releases() {
	[[ "$prune_allowed" == true ]] || return 0
	for old in "$host_root"/releases/*; do
		[[ -d "$old" && ! -L "$old" && "${old##*/}" =~ ^[0-9]{14}-[0-9]+$ ]] || continue
		[[ "$old" == "$release" || "$old" == "$previous_release" || "$old" == "$running_release" ]] && continue
		rm -rf -- "$old"
	done
}
printf 'Installed AO host at %s\n' "$release"

if "$install_only"; then
	prune_old_releases
	printf 'Start with: %s/resources/daemon/ao daemon\n' "$host_root/current"
	exit 0
fi

# A daemon launched by a service must see the same harness tools as this shell.
runner="$host_root/run-daemon.sh"
service_path="$HOME/.local/bin:$HOME/.ao/bin:$PATH"
printf '#!/usr/bin/env bash\nexport PATH=%q\n' "$service_path" > "$runner"
for name in AO_DATA_DIR AO_RUN_FILE AO_PORT; do
	if [[ -n "${!name-}" ]]; then
		printf 'export %s=%q\n' "$name" "${!name}" >> "$runner"
	fi
done
printf 'exec %q daemon\n' "$host_root/current/resources/daemon/ao" >> "$runner"
chmod 700 "$runner"

if [[ "$platform" == Linux ]]; then
	unit_dir="$HOME/.config/systemd/user"
	mkdir -p "$unit_dir"
	escaped_runner="${runner//%/%%}"
	escaped_runner="${escaped_runner//\\/\\\\}"
	escaped_runner="${escaped_runner//\"/\\\"}"
	printf '[Unit]\nDescription=AO self-hosted daemon\nAfter=network-online.target\n\n[Service]\nType=simple\nExecStart="%s"\nRestart=on-failure\nRestartSec=3\n\n[Install]\nWantedBy=default.target\n' \
		"$escaped_runner" > "$unit_dir/ao-self-hosted.service"
	systemctl --user daemon-reload
	systemctl --user enable ao-self-hosted.service
	systemctl --user restart ao-self-hosted.service
	if command -v loginctl >/dev/null && [[ "$(loginctl show-user "$(id -un)" -p Linger --value 2>/dev/null || true)" != yes ]]; then
		printf 'To keep AO running after logout: sudo loginctl enable-linger %s\n' "$(id -un)" >&2
	fi
else
	plist="$HOME/Library/LaunchAgents/dev.aoagents.self-hosted.plist"
	mkdir -p "$(dirname "$plist")" "$host_root/logs"
	AO_HOST_PLIST="$plist" AO_HOST_RUNNER="$runner" AO_HOST_LOG_DIR="$host_root/logs" python3 - <<'PY'
import os, plistlib
data = {
    'Label': 'dev.aoagents.self-hosted',
    'ProgramArguments': [os.environ['AO_HOST_RUNNER']],
    'RunAtLoad': True,
    'KeepAlive': True,
    'StandardOutPath': os.path.join(os.environ['AO_HOST_LOG_DIR'], 'daemon.log'),
    'StandardErrorPath': os.path.join(os.environ['AO_HOST_LOG_DIR'], 'daemon.err'),
}
with open(os.environ['AO_HOST_PLIST'], 'wb') as f:
    plistlib.dump(data, f)
PY
	launchctl bootout "gui/$(id -u)" "$plist" >/dev/null 2>&1 || true
	launchctl bootstrap "gui/$(id -u)" "$plist"
fi

for attempt in {1..20}; do
	status="$("$host_root/current/resources/daemon/ao" status --json)"
	state="$(printf '%s' "$status" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("state", ""))')"
	[[ "$state" == ready ]] && break
	sleep 1
done
[[ "$state" == ready ]] || {
	printf '%s\n' 'AO service did not become ready; inspect the service logs.' >&2
	exit 1
}

args=(remote-host enable)
"$tunnel" && args+=(--tunnel)
"$host_root/current/resources/daemon/ao" "${args[@]}"
prune_old_releases
printf '\nOn your laptop: Settings → Remote hosts → Add host. Enter the address and password above.\n'
