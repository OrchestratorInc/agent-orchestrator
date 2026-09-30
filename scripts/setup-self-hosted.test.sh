#!/usr/bin/env bash
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/setup-self-hosted.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/pkg/resources/daemon" "$tmp/pkg/resources/acp-runtime/node/bin" \
	"$tmp/pkg/resources/acp-runtime/node_modules/@agentclientprotocol/claude-agent-acp/dist" \
	"$tmp/pkg/resources/tmux/bin"

# Keep systemctl absent even when the test runner has it installed.
for tool in chmod cp date git gzip ln mkdir mktemp python3 readlink rm rmdir tar; do
	ln -s "$(command -v "$tool")" "$tmp/bin/$tool"
done
printf '%s\n' '#!/bin/sh' 'case "$1" in' \
	'  -u) echo 1000 ;;' '  -un) echo ao ;;' 'esac' > "$tmp/bin/id"
printf '%s\n' '#!/bin/sh' 'case "$1" in' \
	'  -s) echo Linux ;;' '  -m) echo x86_64 ;;' 'esac' > "$tmp/bin/uname"
printf '%s\n' '#!/bin/sh' 'case "$1" in' \
	'  status)' \
	'    if [ -n "${TEST_AO_BLOCK_FILE:-}" ]; then' \
	'      : > "${TEST_AO_BLOCK_FILE}.ready"' \
	'      while [ ! -e "${TEST_AO_BLOCK_FILE}.go" ]; do /bin/sleep 0.02; done' \
	'    fi' \
	'    printf '\''{"state":"%s","executablePath":"%s"}\n'\'' "${TEST_AO_STATE:-stopped}" "${TEST_AO_EXE:-}" ;;' \
	'  version|remote-host) exit 0 ;;' 'esac' > "$tmp/pkg/resources/daemon/ao"
printf '%s\n' '#!/bin/sh' 'echo v22.0.0' > "$tmp/pkg/resources/acp-runtime/node/bin/node"
printf '%s\n' '#!/bin/sh' 'echo tmux' > "$tmp/pkg/resources/tmux/bin/tmux"
: > "$tmp/pkg/resources/acp-runtime/node_modules/@agentclientprotocol/claude-agent-acp/dist/index.js"
chmod +x "$tmp/bin/id" "$tmp/bin/uname" "$tmp/pkg/resources/daemon/ao" \
	"$tmp/pkg/resources/acp-runtime/node/bin/node" "$tmp/pkg/resources/tmux/bin/tmux"

bundle="$tmp/host.tar.gz"
case "${1:-}" in
	bad-tmux)
		chmod -x "$tmp/pkg/resources/tmux/bin/tmux"
		COPYFILE_DISABLE=1 tar -czf "$bundle" -C "$tmp/pkg" resources
		if env PATH="$tmp/bin" AO_HOST_INSTALL_DIR="$tmp/host" /bin/bash "$script" --bundle "$bundle" --install-only > "$tmp/out" 2>&1; then
			printf '%s\n' 'non-executable tmux was accepted' >&2; exit 1
		fi
		grep -q 'bundled tmux is not executable' "$tmp/out" || { cat "$tmp/out" >&2; exit 1; }
		;;
	no-systemd)
		COPYFILE_DISABLE=1 tar -czf "$bundle" -C "$tmp/pkg" resources
		if env PATH="$tmp/bin" AO_HOST_INSTALL_DIR="$tmp/host" /bin/bash "$script" --bundle "$bundle" > "$tmp/out" 2>&1; then
			printf '%s\n' 'default install without systemd succeeded' >&2; exit 1
		fi
		grep -q 'systemd user services are required' "$tmp/out" || { cat "$tmp/out" >&2; exit 1; }
		[[ ! -L "$tmp/host/current" ]]
		releases=("$tmp/host/releases"/*)
		[[ ! -e "${releases[0]}" ]]
		;;
	inactive-systemd)
		printf '%s\n' '#!/bin/sh' 'exit 1' > "$tmp/bin/systemctl"
		chmod +x "$tmp/bin/systemctl"
		COPYFILE_DISABLE=1 tar -czf "$bundle" -C "$tmp/pkg" resources
		if env PATH="$tmp/bin" AO_HOST_INSTALL_DIR="$tmp/host" /bin/bash "$script" --bundle "$bundle" > "$tmp/out" 2>&1; then
			printf '%s\n' 'default install without an active user service manager succeeded' >&2; exit 1
		fi
		grep -q 'systemd user services are unavailable' "$tmp/out" || { cat "$tmp/out" >&2; exit 1; }
		[[ ! -L "$tmp/host/current" ]]
		releases=("$tmp/host/releases"/*)
		[[ ! -e "${releases[0]}" ]]
		;;
	prune)
		COPYFILE_DISABLE=1 tar -czf "$bundle" -C "$tmp/pkg" resources
		install() {
			env PATH="$tmp/bin" AO_HOST_INSTALL_DIR="$tmp/host" TEST_AO_STATE="$1" TEST_AO_EXE="${2:-}" \
				/bin/bash "$script" --bundle "$bundle" --install-only > "$tmp/out" 2>&1
		}
		install stopped
		first="$(readlink "$tmp/host/current")"
		install stopped
		second="$(readlink "$tmp/host/current")"
		install ready "$first/resources/daemon/ao"
		third="$(readlink "$tmp/host/current")"
		[[ -d "$first" && -d "$second" && -d "$third" ]]
		install stopped
		fourth="$(readlink "$tmp/host/current")"
		[[ ! -e "$first" && ! -e "$second" && -d "$third" && -d "$fourth" ]]
		releases=("$tmp/host/releases"/*)
		[[ ${#releases[@]} -eq 2 ]]
		;;
	failed-restarts)
		printf '%s\n' '#!/bin/sh' 'case "$2" in restart) exit 1 ;; *) exit 0 ;; esac' > "$tmp/bin/systemctl"
		chmod +x "$tmp/bin/systemctl"
		COPYFILE_DISABLE=1 tar -czf "$bundle" -C "$tmp/pkg" resources
		env PATH="$tmp/bin" AO_HOST_INSTALL_DIR="$tmp/host" /bin/bash "$script" --bundle "$bundle" --install-only > "$tmp/out" 2>&1
		good="$(readlink "$tmp/host/current")"
		for attempt in 1 2; do
			if env HOME="$tmp/home" PATH="$tmp/bin" AO_HOST_INSTALL_DIR="$tmp/host" /bin/bash "$script" --bundle "$bundle" > "$tmp/out" 2>&1; then
				printf '%s\n' 'simulated service restart unexpectedly succeeded' >&2; exit 1
			fi
		done
		[[ -d "$good" ]] || { printf '%s\n' 'last working release was deleted after failed restarts' >&2; exit 1; }
		;;
	relative-current)
		COPYFILE_DISABLE=1 tar -czf "$bundle" -C "$tmp/pkg" resources
		for attempt in 1 2; do
			env PATH="$tmp/bin" AO_HOST_INSTALL_DIR="$tmp/host" /bin/bash "$script" --bundle "$bundle" --install-only > "$tmp/out" 2>&1
			if [[ "$attempt" == 1 ]]; then
				good="$(readlink "$tmp/host/current")"
				ln -sfn "releases/${good##*/}" "$tmp/host/current"
			fi
		done
		[[ -d "$good" ]] || { printf '%s\n' 'relative current target was deleted' >&2; exit 1; }
		second="$(readlink "$tmp/host/current")"
		ln -sfn "./releases/${second##*/}" "$tmp/host/current"
		env PATH="$tmp/bin" AO_HOST_INSTALL_DIR="$tmp/host" /bin/bash "$script" --bundle "$bundle" --install-only > "$tmp/out" 2>&1
		[[ -d "$second" ]] || { printf '%s\n' 'unknown current target was deleted' >&2; exit 1; }
		;;
	concurrent)
		COPYFILE_DISABLE=1 tar -czf "$bundle" -C "$tmp/pkg" resources
		block="$tmp/block"
		env PATH="$tmp/bin" AO_HOST_INSTALL_DIR="$tmp/host" TEST_AO_BLOCK_FILE="$block" \
			/bin/bash "$script" --bundle "$bundle" --install-only > "$tmp/first.out" 2>&1 &
		first_pid=$!
		for attempt in {1..100}; do
			[[ -e "$block.ready" ]] && break
			/bin/sleep 0.05
		done
		if [[ ! -e "$block.ready" ]]; then
			: > "$block.go"
			wait "$first_pid" || true
			cat "$tmp/first.out" >&2
			printf '%s\n' 'first installer did not reach status' >&2; exit 1
		fi
		second_status=0
		env PATH="$tmp/bin" AO_HOST_INSTALL_DIR="$tmp/host" \
			/bin/bash "$script" --bundle "$bundle" --install-only > "$tmp/second.out" 2>&1 || second_status=$?
		lock_held=false
		[[ -d "$tmp/host/.install.lock" ]] && lock_held=true
		: > "$block.go"
		wait "$first_pid"
		"$lock_held" || { printf '%s\n' 'second installer removed the first installer lock' >&2; exit 1; }
		[[ ! -e "$tmp/host/.install.lock" ]] || { printf '%s\n' 'install lock was not cleaned up' >&2; exit 1; }
		[[ "$second_status" -ne 0 ]] || { printf '%s\n' 'concurrent installer was accepted' >&2; exit 1; }
		grep -q 'Install lock exists' "$tmp/second.out" || { cat "$tmp/second.out" >&2; exit 1; }
		[[ -d "$(readlink "$tmp/host/current")" ]]
		releases=("$tmp/host/releases"/*)
		[[ ${#releases[@]} -eq 1 ]]
		;;
	*) printf 'Usage: %s {bad-tmux|no-systemd|inactive-systemd|prune|failed-restarts|relative-current|concurrent}\n' "$0" >&2; exit 2 ;;
esac
printf 'PASS %s\n' "$1"
