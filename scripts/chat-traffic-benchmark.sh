#!/usr/bin/env bash
# Local chat traffic benchmark: baseline revision vs this working tree.
#
#   scripts/chat-traffic-benchmark.sh [--baseline-ref REF] [--out-dir DIR]
#       [--limits FILE] [--allow-client-change] [--keep-work]
#
# The baseline is REF's committed tree (default: origin/main, else main),
# exported with `git archive` into a private temporary directory. Nothing is
# fetched and no worktree is registered. Only the benchmark harness files are
# copied into it, add-only, and the exported tree is verified blob by blob
# against REF. The candidate is this working tree, including uncommitted
# changes, which are recorded as a digest. Both sides run the same harness,
# fixtures, toolchain and Go settings; the comparator then checks both runs
# against the reviewed limits file.
#
# Exit codes: 0 pass, 1 limit or semantic failure, 2 invalid, incomparable,
# failed replay or setup failure. See docs/performance/chat-traffic-benchmark.md.
set -Eeuo pipefail
# Exit 1 is reserved for a comparison that found a failure. Any setup step
# that fails (a build, the export, an overlay or tree check) exits 2.
trap 'exit 2' ERR

usage() {
	sed -n '2,6p' "$0" >&2
	exit 2
}

# value returns an option's argument, or stops with usage when it is missing.
value() {
	[[ $# -ge 2 && -n "$2" && "$2" != --* ]] || usage
	printf '%s' "$2"
}

baseline_ref=""
out_dir=""
limits=""
allow_client_change=""
keep_work=""
while [[ $# -gt 0 ]]; do
	case "$1" in
	--baseline-ref) baseline_ref="$(value "$@")"; shift 2 ;;
	--out-dir) out_dir="$(value "$@")"; shift 2 ;;
	--limits) limits="$(value "$@")"; shift 2 ;;
	--allow-client-change) allow_client_change="--allow-client-change"; shift ;;
	--keep-work) keep_work=1; shift ;;
	*) usage ;;
	esac
done

# Relative --out-dir and --limits name paths from the caller's directory.
caller="$(pwd -P)"
absolute() {
	case "$1" in
	/*) printf '%s' "$1" ;;
	*) printf '%s/%s' "$caller" "$1" ;;
	esac
}
[[ -z "$out_dir" ]] || out_dir="$(absolute "$out_dir")"
[[ -z "$limits" ]] || limits="$(absolute "$limits")"

# A reused --out-dir must never hand an earlier run's results to this one, even
# when this run stops during setup.
clear_results() { rm -f "$1"/{baseline,candidate}.{json,log} "$1"/report.{json,md}; }
[[ -z "$out_dir" || ! -d "$out_dir" ]] || clear_results "$out_dir"

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$repo"

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum | cut -d' ' -f1; else shasum -a 256 | cut -d' ' -f1; fi
}

fail() {
	echo "chat-traffic-benchmark: $*" >&2
	exit 2
}

tmp_root="${TMPDIR:-/tmp}"
tmp_root="${tmp_root%/}"

if [[ -z "$baseline_ref" ]]; then
	if git rev-parse --verify --quiet origin/main^{commit} >/dev/null; then baseline_ref=origin/main; else baseline_ref=main; fi
fi
base_sha="$(git rev-parse --verify --quiet "${baseline_ref}^{commit}")" || fail "cannot resolve baseline ref $baseline_ref"
head_sha="$(git rev-parse --verify HEAD^{commit})"
limits="${limits:-$repo/backend/internal/chattraffic/limits.json}"
[[ -f "$limits" && ! -L "$limits" ]] || fail "limits file $limits is missing or not a regular file"
limits_sha="$(sha256 <"$limits")"

# Paths the benchmark compiles or reads. Dirty state elsewhere cannot change a
# measurement and is not part of the candidate identity.
measured=(backend frontend/src/renderer/hooks/useConversation.ts frontend/src/renderer/lib/event-transport.ts)
# The candidate's identity is its commit plus a digest of uncommitted changes to
# the measured paths.
candidate_dirty() {
	if [[ -n "$(git status --porcelain --untracked-files=all -- "${measured[@]}")" ]]; then
		{
			git diff --binary HEAD -- "${measured[@]}"
			git ls-files --others --exclude-standard -z -- "${measured[@]}" | while IFS= read -r -d '' f; do
				printf 'untracked %s %s\n' "$f" "$(sha256 <"$f")"
			done
		} | sha256
	fi
}
dirty_sha="$(candidate_dirty)"

work="$(mktemp -d "$tmp_root/ao-chattraffic.XXXXXX")"
baseline_lock=""
cleanup() {
	[[ -n "$keep_work" ]] || rm -rf "$work"
	[[ -z "$baseline_lock" ]] || rm -rf "$baseline_lock"
}
trap cleanup EXIT
out_dir="${out_dir:-$tmp_root/ao-chattraffic-results/$(date -u +%Y%m%dT%H%M%SZ)}"
# The baseline tree lives at a stable path per commit so Go's build cache,
# whose keys include each package directory, is reused across runs. It is
# still deleted and exported afresh every run, then verified.
baseline_root="$tmp_root/ao-chattraffic-baseline/$base_sha"
# Runs of the same baseline share that tree, so one run at a time owns it. A
# lock left by a run that is no longer alive is taken over.
mkdir -p "$(dirname "$baseline_root")"
if ! mkdir "$baseline_root.lock" 2>/dev/null; then
	owner="$(cat "$baseline_root.lock/pid" 2>/dev/null || true)"
	[[ -n "$owner" ]] || fail "baseline $base_sha is locked by a run that recorded no pid; remove $baseline_root.lock if none is active"
	! kill -0 "$owner" 2>/dev/null || fail "another run (pid $owner) is using baseline $base_sha; try again when it finishes"
	rm -rf "$baseline_root.lock"
	mkdir "$baseline_root.lock" 2>/dev/null || fail "cannot lock baseline $base_sha"
fi
baseline_lock="$baseline_root.lock"
echo $$ >"$baseline_lock/pid"
rm -rf "$baseline_root"
mkdir -p "$out_dir" "$baseline_root" "$work/bin" "$work/gotmp"
clear_results "$out_dir"

# One Go configuration for every step: no workspace file, no toolchain or
# module downloads, read-only module graph, private temporary build output.
go_env=(env GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOPROXY=off GOMAXPROCS=2 GOTMPDIR="$work/gotmp")

echo "baseline  $baseline_ref = $base_sha"
echo "candidate working tree on $head_sha${dirty_sha:+ (uncommitted changes $dirty_sha)}"
echo "limits    $limits ($limits_sha)"

(cd backend && "${go_env[@]}" go build -o "$work/bin/chattraffic" ./internal/chattraffic/cmd/chattraffic)
bench="$work/bin/chattraffic"

git archive --format=tar "$base_sha" -- "${measured[@]}" | tar -x -C "$baseline_root"
git ls-tree -r -z --full-tree "$base_sha" -- "${measured[@]}" >"$work/ls-tree.bin"
"$bench" overlay --candidate-root "$repo" --baseline-root "$baseline_root" --manifest "$work/overlay.json"
"$bench" verify-export --export-root "$baseline_root" --ls-tree "$work/ls-tree.bin" --manifest "$work/overlay.json"

run_side() {
	local side="$1" root="$2" ref="$3" dirty="$4"
	local module_dir
	module_dir="$(cd "$root/backend" && "${go_env[@]}" go list -m -f '{{.Dir}}')"
	# Compare physical paths: macOS reaches $TMPDIR through the /var symlink.
	[[ -d "$module_dir" && "$(cd "$module_dir" && pwd -P)" == "$(cd "$root/backend" && pwd -P)" ]] ||
		fail "$side compiles module $module_dir, not $root/backend"
	echo "running $side harness in $root/backend"
	local artifact="$out_dir/$side.json" status=0
	rm -f "$artifact"
	(cd "$root/backend" && "${go_env[@]}" \
		AO_CHATTRAFFIC_OUT="$artifact" AO_CHATTRAFFIC_SIDE="$side" \
		AO_CHATTRAFFIC_SOURCE_REF="$ref" AO_CHATTRAFFIC_DIRTY_DIFF_SHA256="$dirty" \
		AO_CHATTRAFFIC_LIMITS_SHA256="$limits_sha" \
		go test -count=1 -p 1 -timeout 15m -run '^TestChatTrafficBenchmark$' ./internal/httpd/ >"$out_dir/$side.log" 2>&1) || status=$?
	# The harness carries semantic differences inside the artifact and passes;
	# it fails, and writes no artifact, only when the replay itself broke an
	# invariant or could not run. Either way there is nothing valid to compare.
	if [[ $status -ne 0 || ! -s "$artifact" ]]; then
		tail -n 40 "$out_dir/$side.log" >&2
		fail "$side harness failed (exit $status, see $out_dir/$side.log); no comparison"
	fi
}

run_side baseline "$baseline_root" "$base_sha" ""
run_side candidate "$repo" "$head_sha" "$dirty_sha"
# An edit made while the runs compiled would be measured under the identity
# recorded before them.
[[ "$(git rev-parse --verify HEAD^{commit})" == "$head_sha" && "$(candidate_dirty)" == "$dirty_sha" ]] ||
	fail "the candidate's measured sources changed during the run; no comparison"

status=0
"$bench" compare --baseline "$out_dir/baseline.json" --candidate "$out_dir/candidate.json" --limits "$limits" \
	--expect-baseline-ref "$base_sha" --expect-candidate-ref "$head_sha" $allow_client_change \
	--out-json "$out_dir/report.json" --out-md "$out_dir/report.md" || status=$?
echo
echo "report: $out_dir/report.md and $out_dir/report.json (exit $status)"
exit "$status"
