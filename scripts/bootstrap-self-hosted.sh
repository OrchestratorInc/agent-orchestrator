#!/usr/bin/env bash
# Fresh Linux (x64 or arm64, apt or dnf) entry point. The lower-level installer owns AO releases and service setup.
set -euo pipefail

# Braced so bash reads the whole script before running any of it: a download
# cut off mid-way by `curl ... | bash` runs nothing.
{
usage() {
	printf '%s\n' 'Usage: bootstrap-self-hosted.sh [--nightly | --source-ref BRANCH] [--lan]'
	printf '%s\n' 'Installs prerequisites, then installs released Orchestrator or builds a source branch.'
	printf '%s\n' 'Downloads stable by default; --nightly installs the newest published nightly.'
}

source_ref=""
nightly=false
tunnel=true
while (($#)); do
	case "$1" in
		--nightly) nightly=true; shift ;;
		--source-ref) source_ref="${2:?--source-ref needs a branch}"; shift 2 ;;
		--lan) tunnel=false; shift ;;
		-h|--help) usage; exit 0 ;;
		*) usage >&2; exit 2 ;;
	esac
done
if "$nightly" && [[ -n "$source_ref" ]]; then
	printf '%s\n' '--nightly cannot be combined with --source-ref.' >&2; exit 2
fi

[[ "$(id -u)" != 0 ]] || { printf '%s\n' 'Run as the host user, not root.' >&2; exit 1; }
# go_sha256 is go.dev's official checksum of go${go_version}.linux-${go_arch}.tar.gz.
go_version=1.27.4
case "$(uname -s):$(uname -m)" in
	Linux:x86_64) go_arch=amd64 node_arch=x64 go_sha256=7226736c2339719cda28fbf3a54d71aa63bcefe376e98cef00034268dab24840 ;;
	Linux:aarch64|Linux:arm64) go_arch=arm64 node_arch=arm64 go_sha256=3392582988a273a87ab5043ee1b2c9c17a2c3c00580bf9326c47a2e0aaaeac92 ;;
	*) printf '%s\n' 'This bootstrap supports Linux on x64 or arm64 only.' >&2; exit 1 ;;
esac
# The host package's binaries need glibc 2.34 or newer.
glibc="$(getconf GNU_LIBC_VERSION 2>/dev/null || true)"
if [[ ! "$glibc" =~ ^glibc\ ([0-9]+)\.([0-9]+) ]] ||
	((BASH_REMATCH[1] < 2 || (BASH_REMATCH[1] == 2 && BASH_REMATCH[2] < 34))); then
	printf 'Orchestrator needs glibc 2.34 or newer (Ubuntu 22.04+, Debian 12+, Amazon Linux 2023, RHEL 9+); found: %s\n' "${glibc:-none}" >&2
	exit 1
fi
if command -v apt-get >/dev/null; then
	pm=apt
elif command -v dnf >/dev/null; then
	pm=dnf
else
	printf '%s\n' 'This bootstrap needs apt-get (Ubuntu, Debian) or dnf (Amazon Linux 2023, Fedora, RHEL).' >&2; exit 1
fi
[[ -z "$source_ref" || "$source_ref" =~ ^[a-zA-Z0-9][a-zA-Z0-9._/-]*$ ]] || {
	printf '%s\n' 'Invalid source branch.' >&2; exit 2;
}

if [[ "$pm" == apt ]]; then
	sudo apt-get update
	sudo apt-get install -y ca-certificates curl gh git python3
else
	# Installing curl over curl-minimal conflicts, so only add it when missing.
	command -v curl >/dev/null || sudo dnf install -y curl
	# The GitHub CLI is not in these distributions' own repositories.
	curl -fsSL https://cli.github.com/packages/rpm/gh-cli.repo | sudo tee /etc/yum.repos.d/gh-cli.repo >/dev/null
	sudo dnf install -y ca-certificates gh git python3 tar
fi
sudo loginctl enable-linger "$(id -un)"
systemctl --user show-environment >/dev/null || {
	printf '%s\n' 'A systemd user session is required for an always-on Orchestrator host.' >&2; exit 1;
}

if "$tunnel" && ! command -v cloudflared >/dev/null; then
	if [[ "$pm" == apt ]]; then
		sudo install -d -m 0755 /usr/share/keyrings
		curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg | sudo tee /usr/share/keyrings/cloudflare-main.gpg >/dev/null
		printf '%s\n' 'deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared noble main' |
			sudo tee /etc/apt/sources.list.d/cloudflared.list >/dev/null
		sudo apt-get update
		sudo apt-get install -y cloudflared
	else
		curl -fsSL https://pkg.cloudflare.com/cloudflared.repo | sudo tee /etc/yum.repos.d/cloudflared.repo >/dev/null
		sudo dnf install -y cloudflared
	fi
fi

stage="$(mktemp -d)"
trap 'rm -rf -- "$stage"' EXIT
installer="$stage/setup-self-hosted.sh"
if [[ -z "$source_ref" ]]; then
	curl -fsSL https://raw.githubusercontent.com/OrchestratorInc/agent-orchestrator/main/scripts/setup-self-hosted.sh -o "$installer"
else
	if [[ "$pm" == apt ]]; then
		sudo apt-get install -y build-essential pkg-config xz-utils
	else
		sudo dnf install -y gcc gcc-c++ make pkgconf-pkg-config xz
	fi
	tools="$HOME/.local/ao-build-tools"
	mkdir -p "$tools"
	# Replace a cached Go older than the one backend/go.mod requires; keep a newer one.
	cached_go="$(GOTOOLCHAIN=local "$tools/go/bin/go" env GOVERSION 2>/dev/null || true)"
	if [[ "$(printf '%s\n' "$go_version" "${cached_go#go}" | sort -V | head -n1)" != "$go_version" ]]; then
		rm -rf "$tools/go"
		curl -fsSL "https://go.dev/dl/go${go_version}.linux-${go_arch}.tar.gz" -o "$stage/go.tar.gz"
		printf '%s  %s\n' "$go_sha256" "$stage/go.tar.gz" | sha256sum -c -
		tar -xzf "$stage/go.tar.gz" -C "$tools"
	fi
	node_dist="node-v22.23.5-linux-${node_arch}"
	if [[ ! -x "$tools/$node_dist/bin/node" ]]; then
		curl -fsSL "https://nodejs.org/dist/v22.23.5/$node_dist.tar.xz" -o "$stage/node.tar.xz"
		curl -fsSL https://nodejs.org/dist/v22.23.5/SHASUMS256.txt -o "$stage/SHASUMS256.txt"
		expected="$(awk -v name="$node_dist.tar.xz" '$2 == name { print $1 }' "$stage/SHASUMS256.txt")"
		[[ "$expected" =~ ^[0-9a-f]{64}$ ]] || { printf '%s\n' 'Node checksum missing.' >&2; exit 1; }
		printf '%s  %s\n' "$expected" "$stage/node.tar.xz" | sha256sum -c -
		tar -xJf "$stage/node.tar.xz" -C "$tools"
	fi
	export PATH="$tools/go/bin:$tools/$node_dist/bin:$PATH"
	git clone --depth 1 --single-branch --branch "$source_ref" https://github.com/OrchestratorInc/agent-orchestrator.git "$stage/source"
	(
		cd "$stage/source/frontend"
		export GOMAXPROCS=2 NODE_OPTIONS=--max-old-space-size=1536
		npm ci
		npm run build:daemon
		npm run build:tmux
		npm run build:acp-runtime
		npm run build:host
	)
	installer="$stage/source/scripts/setup-self-hosted.sh"
	bundle="$stage/source/frontend/dist-host/ao-host-linux-${node_arch}.tar.gz"
fi

args=()
"$nightly" && args+=(--nightly)
[[ -z "$source_ref" ]] || args+=(--bundle "$bundle")
if "$tunnel"; then
	args+=(--tunnel)
fi
bash "$installer" "${args[@]}"

printf '\nDay-two commands: %s\n' "$HOME/.ao/host/current/resources/daemon/ao remote-host status"
printf 'Service logs: journalctl --user -u ao-self-hosted.service -n 100 --no-pager\n'
}
