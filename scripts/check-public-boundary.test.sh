#!/usr/bin/env bash
# Runs check-public-boundary.sh against throwaway repositories: a clean one
# passes, and one that names the control plane's private internals (as a
# harness that stands it up would) fails and names the file.
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/check-public-boundary.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

repo() {
	local dir="$tmp/$1"
	mkdir -p "$dir/scripts"
	git -C "$dir" init -q
	cp "$script" "$dir/scripts/check-public-boundary.sh"
	printf '# A public app\n' > "$dir/README.md"
	git -C "$dir" add -A
	echo "$dir"
}

clean=$(repo clean)
out=$(cd "$clean" && scripts/check-public-boundary.sh 2>&1) || { echo "FAIL: a clean repository was refused: $out" >&2; exit 1; }
[[ "$out" == "public boundary: ok" ]] || { echo "FAIL: clean output = $out" >&2; exit 1; }

for leak in 'AO_CLOUD_DEVELOPMENT_GITHUB_URL: http://gitserver:8080' 'pg_isready -U ao_cloud_owner' 'source "$cp/scripts/lib/docker-local.sh"' 'cp_root=${AO_CONTROL_PLANE:?}'; do
	dirty=$(repo "dirty-$RANDOM")
	mkdir -p "$dirty/test/session-move"
	printf '%s\n' "$leak" > "$dirty/test/session-move/round-trip.sh"
	git -C "$dirty" add -A
	if out=$(cd "$dirty" && scripts/check-public-boundary.sh 2>&1); then
		echo "FAIL: a file naming the control plane's internals passed: $leak" >&2
		exit 1
	fi
	[[ "$out" == *"private internals"* && "$out" == *"test/session-move/round-trip.sh"* ]] || { echo "FAIL: output for $leak = $out" >&2; exit 1; }
done
echo "check-public-boundary.test.sh: ok"
