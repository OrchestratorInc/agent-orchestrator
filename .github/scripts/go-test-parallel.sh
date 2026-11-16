#!/usr/bin/env bash
# Runs Go packages' top-level tests split across parallel processes.
#
# go test runs one package's tests serially unless they call t.Parallel, and
# the SQLite-backed packages would serialize anyway on the process-wide goose
# lock (sqlite.gooseMu), so their test binaries kept one core of the runner
# busy while the rest sat idle. Separate processes each get their own lock.
#
# Usage, from the module root:
#   go-test-parallel.sh PROCS PACKAGE...
# Each package's top-level tests are dealt round-robin by name into PROCS
# groups, and all the groups share a pool of PROCS processes, so the next
# package starts on cores the last one has finished with.
set -euo pipefail

procs=$1
shift

bindir=$(mktemp -d)
# One go invocation compiles every package's test binary in parallel.
go test -race -c -o "$bindir/" "$@"

# One line per process: label, package directory, test binary, -run pattern.
groups=()
for pkg in "$@"; do
	name=$(basename "$pkg")
	bin="$bindir/$name.test"
	if [ ! -x "$bin" ]; then
		echo "$pkg: no test binary (no test files?)" >&2
		exit 1
	fi
	dir=$(go list -f '{{.Dir}}' "$pkg")
	mapfile -t tests < <("$bin" -test.list '.*' | grep -E '^(Test|Example|Fuzz)')
	if [ "${#tests[@]}" -eq 0 ]; then
		echo "$pkg: no tests listed" >&2
		exit 1
	fi
	for ((p = 0; p < procs; p++)); do
		group=()
		for ((i = p; i < ${#tests[@]}; i += procs)); do
			group+=("${tests[i]}")
		done
		[ "${#group[@]}" -gt 0 ] || continue
		groups+=("$name.$p $dir $bin ^($(IFS='|'; echo "${group[*]}"))\$")
	done
done

# go test runs a test binary from its package directory; testdata paths
# depend on that. xargs exits non-zero if any group fails.
# shellcheck disable=SC2016 # expanded by the inner bash, per group
printf '%s\n' "${groups[@]}" | xargs -P "$procs" -L 1 bash -c '
	set -o pipefail
	label=$1 dir=$2 bin=$3 run=$4
	cd "$dir" && "$bin" -test.timeout=10m -test.run "$run" 2>&1 | sed -u "s/^/[$label] /" && exit
	echo "FAIL [$label]"
	exit 1
' _
