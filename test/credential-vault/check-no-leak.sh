#!/bin/sh
# Proves a vault secret never left the edge: searches every file under the
# given directories (AO data dir, agent transcripts, logs) for the password,
# the TOTP seed and the common encodings of both. Exits 1 on any hit.
#
#   check-no-leak.sh <secret.env> <dir> [<dir>...]
#
# secret.env holds DEMO_PASSWORD=... and DEMO_SEED=... (see run-e2e.md). The
# values are read from the file and never printed.
set -eu
[ $# -ge 2 ] || { echo "usage: $0 <secret.env> <dir>..." >&2; exit 2; }
env_file=$1
shift
# shellcheck disable=SC1090
. "$env_file"
: "${DEMO_PASSWORD:?missing DEMO_PASSWORD}" "${DEMO_SEED:?missing DEMO_SEED}"

patterns=$(mktemp)
trap 'rm -f "$patterns"' EXIT
node -e '
const [pw, seed] = process.argv.slice(1);
const out = new Set();
for (const v of [pw, seed]) {
  out.add(v);
  out.add(encodeURIComponent(v));
  out.add(encodeURIComponent(v).replace(/%20/g, "+"));
  out.add(JSON.stringify(v).slice(1, -1));
  out.add(Buffer.from(v).toString("base64"));
}
process.stdout.write([...out].join("\n") + "\n");
' "$DEMO_PASSWORD" "$DEMO_SEED" > "$patterns"

hits=0
for dir in "$@"; do
  [ -e "$dir" ] || { echo "skip (missing): $dir"; continue; }
  files=$(find "$dir" -type f | wc -l | tr -d ' ')
  # -a: search binary files too (SQLite, LevelDB, crash dumps); -l: names only.
  matches=$(grep -a -r -l -F -f "$patterns" "$dir" 2>/dev/null || true)
  if [ -n "$matches" ]; then
    hits=1
    echo "LEAK in $dir:"
    echo "$matches" | sed 's/^/  /'
  else
    echo "clean: $dir ($files files searched)"
  fi
done
[ "$hits" -eq 0 ] && echo "no secret found in any searched file"
exit "$hits"
