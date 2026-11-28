#!/bin/sh
# Self-test for check-no-leak.sh: it must catch every encoding of the secret
# and must never report "clean" for a scan it could not complete.
#
#   sh test/credential-vault/check-no-leak.test.sh
set -u
here=$(cd "$(dirname "$0")" && pwd)
check="$here/check-no-leak.sh"
work=$(mktemp -d)
trap 'chmod -R u+rwX "$work"; rm -rf "$work"' EXIT

# A password with characters that form encoding escapes but
# encodeURIComponent leaves alone ("!", "'", "(", ")", "*", "~").
cat > "$work/secret.env" <<'EOF'
DEMO_PASSWORD=Vlt-a b!c'(d)*e~9!
DEMO_SEED=JBSWY3DPEHPK3PXPJBSWY3DP
EOF

failures=0
expect() { # <want-exit> <description> <args...>
  want=$1 desc=$2
  shift 2
  sh "$check" "$work/secret.env" "$@" > "$work/out" 2>&1
  got=$?
  if [ "$got" -eq "$want" ]; then
    echo "ok: $desc"
  else
    echo "FAIL: $desc (exit $got, want $want)"
    sed 's/^/    /' "$work/out"
    failures=$((failures + 1))
  fi
}

mkdir "$work/clean" "$work/form" "$work/raw" "$work/unreadable" "$work/socket"
echo "nothing to see" > "$work/clean/a.txt"
node -e 'process.stdout.write("user=a&password=" + new URLSearchParams({ p: process.argv[1] }).toString().slice(2))' \
  "Vlt-a b!c'(d)*e~9!" > "$work/form/request.log"
printf '%s' "Vlt-a b!c'(d)*e~9!" > "$work/raw/page.html"
echo "hidden" > "$work/unreadable/locked.txt"
chmod 000 "$work/unreadable/locked.txt"

expect 0 "a clean directory passes" "$work/clean"
# Data dirs hold live Unix sockets (run/*.sock); they have no content to leak.
echo "nothing here" > "$work/socket/a.txt"
node -e 'const s = require("net").createServer().listen(process.argv[1], () => { s.unref(); process.exit(0); })' "$work/socket/live.sock" 2>/dev/null \
  || python3 -c 'import socket,sys; socket.socket(socket.AF_UNIX).bind(sys.argv[1])' "$work/socket/live.sock"
[ -S "$work/socket/live.sock" ] || echo "note: could not create a socket fixture"
expect 0 "a Unix socket is skipped, not an incomplete scan" "$work/socket"
expect 1 "the raw password is caught" "$work/raw"
expect 1 "a file holding only the form-encoded password is caught" "$work/form"
expect 2 "a missing path fails the scan" "$work/clean" "$work/does-not-exist"
if [ "$(id -u)" -ne 0 ]; then
  expect 2 "an unreadable file fails the scan" "$work/unreadable"
fi

[ "$failures" -eq 0 ] && echo "all checks passed"
exit "$failures"
