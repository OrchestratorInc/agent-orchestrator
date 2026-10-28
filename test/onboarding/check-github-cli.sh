#!/usr/bin/env bash
set -euo pipefail

# Legacy Go-built gh releases use the platform trust store on macOS. Linux lets
# the fixture trust its temporary CA without changing the machine's trust store.
[[ $(uname -s) == Linux ]] || { echo "Run this matrix on Linux (or in a Linux container)." >&2; exit 1; }
case $(uname -m) in
  aarch64|arm64) arch=arm64 ;;
  x86_64) arch=amd64 ;;
  *) echo "Unsupported test architecture" >&2; exit 1 ;;
esac
repo_root=$(cd "$(dirname "$0")/../.." && pwd)
fixture_dir=${AO_GITHUB_CLI_FIXTURE_DIR:-$(mktemp -d)}
mkdir -p "$fixture_dir"
fixture_dir=$(cd "$fixture_dir" && pwd)
binaries=()
for version in 2.4.0 2.20.2 2.50.0 2.100.0; do
  version_dir="$fixture_dir/$version"
  mkdir -p "$version_dir"
  archive="gh_${version}_linux_${arch}.tar.gz"
  checksums="gh_${version}_checksums.txt"
  release="https://github.com/cli/cli/releases/download/v${version}"
  curl --fail --silent --show-error --location --retry 3 "$release/$archive" -o "$version_dir/$archive"
  curl --fail --silent --show-error --location --retry 3 "$release/$checksums" -o "$version_dir/$checksums"
  (cd "$version_dir" && sha256sum --check --ignore-missing "$checksums")
  tar -xzf "$version_dir/$archive" -C "$version_dir"
  binaries+=("$version_dir/gh_${version}_linux_${arch}/bin/gh")
done
printf -v AO_GITHUB_E2E_BINARIES '%s:' "${binaries[@]}"
export AO_GITHUB_E2E_BINARIES=${AO_GITHUB_E2E_BINARIES%:}
go -C "$repo_root/backend" test ./e2e -run '^TestGitHubDeviceLoginCLICompatibility$' -count=1 -v -timeout=10m
