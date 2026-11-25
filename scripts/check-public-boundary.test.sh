#!/usr/bin/env bash
# Runs check-public-boundary.sh against throwaway repositories. The private
# names are synthetic and swapped in by hash (AO_BOUNDARY_NAME_HASHES), so no
# real one appears here: a clean repository passes, and one naming a private
# name (whole, inside a longer token, as a family prefix, or as a path inside
# another path) fails by file and line, without printing the name.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Synthetic stand-ins: a role, a settings family (a prefix) and a script path.
printf '%s\n' zz_synthetic_role ZZ_SYNTHETIC_FAMILY_ lib/zz-synthetic-helper.sh > "$tmp/names"
python3 -c 'import hashlib,sys; [print(hashlib.sha256(l.strip().encode()).hexdigest()) for l in open(sys.argv[1]) if l.strip()]' "$tmp/names" > "$tmp/hashes"
export AO_BOUNDARY_NAME_HASHES="$tmp/hashes"

repo() {
	local dir="$tmp/$1"
	mkdir -p "$dir/scripts"
	git -C "$dir" init -q
	cp "$here/check-public-boundary.sh" "$here/check_private_names.py" "$dir/scripts/"
	printf '# A public app\n' > "$dir/README.md"
	git -C "$dir" add -A
	echo "$dir"
}

clean=$(repo clean)
out=$(cd "$clean" && scripts/check-public-boundary.sh 2>&1) || { echo "FAIL: a clean repository was refused: $out" >&2; exit 1; }
[[ "$out" == "public boundary: ok" ]] || { echo "FAIL: clean output = $out" >&2; exit 1; }

n=0
for leak in 'psql -U zz_synthetic_role' 'export ZZ_SYNTHETIC_FAMILY_URL=http://x' 'source "$cp/lib/zz-synthetic-helper.sh"' 'role=zz_synthetic_role.'; do
	n=$((n + 1))
	dirty=$(repo "dirty-$n")
	mkdir -p "$dirty/test"
	printf 'line one\n%s\n' "$leak" > "$dirty/test/harness.sh"
	git -C "$dirty" add -A
	if out=$(cd "$dirty" && scripts/check-public-boundary.sh 2>&1); then
		echo "FAIL: a file naming a private name passed: $leak" >&2
		exit 1
	fi
	[[ "$out" == *"private internals"* && "$out" == *"test/harness.sh:2"* ]] || { echo "FAIL: output for leak $n = $out" >&2; exit 1; }
	while IFS= read -r name; do
		[[ "$out" != *"$name"* ]] || { echo "FAIL: the output names $name" >&2; exit 1; }
	done < "$tmp/names"
done

# A token that only shares a prefix with a name, without its boundary, passes.
near=$(repo near)
printf 'zz_synthetic_roles and ZZ_SYNTHETIC_FAMILY\n' > "$near/notes.txt"
git -C "$near" add -A
out=$(cd "$near" && scripts/check-public-boundary.sh 2>&1) || { echo "FAIL: a near miss was refused: $out" >&2; exit 1; }
echo "check-public-boundary.test.sh: ok"
