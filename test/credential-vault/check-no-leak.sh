#!/bin/sh
# Proves a vault secret never left the edge: searches every file under the
# given paths (AO data dir, agent transcripts, logs) for the password, the TOTP
# seed and the common encodings of both. Prints only file names.
#
#   check-no-leak.sh <secret.env> <path> [<path>...]
#
# secret.env holds DEMO_PASSWORD=... and DEMO_SEED=... (see run-e2e.md). It is
# parsed, never executed, so any characters are safe in the values.
#
# Exit status: 0 every path was searched and nothing was found; 1 a secret was
# found; 2 the scan was incomplete (missing path, unreadable file, bad input).
# Self-test: check-no-leak.test.sh.
set -u
[ $# -ge 2 ] || { echo "usage: $0 <secret.env> <path>..." >&2; exit 2; }
env_file=$1
shift

patterns=$(mktemp)
trap 'rm -f "$patterns"' EXIT
node -e '
const fs = require("fs");
const env = {};
for (const line of fs.readFileSync(process.argv[1], "utf8").split("\n")) {
  const at = line.indexOf("=");
  if (at > 0) env[line.slice(0, at).trim()] = line.slice(at + 1);
}
const values = [env.DEMO_PASSWORD, env.DEMO_SEED];
if (values.some((v) => !v)) { console.error("secret.env needs DEMO_PASSWORD and DEMO_SEED"); process.exit(2); }
const out = new Set();
// The same encodings the redactor in browser-vault.ts masks.
const lower = (e) => e.replace(/%[0-9A-F]{2}/g, (x) => x.toLowerCase());
const html = (v, apos) => v.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/\x27/g, apos);
for (const v of values) {
  const uri = encodeURIComponent(v);
  // application/x-www-form-urlencoded, as a browser posts a login form.
  const form = new URLSearchParams({ v }).toString().slice(2);
  for (const e of [v, uri, lower(uri), form, lower(form), uri.replace(/%20/g, "+"), JSON.stringify(v).slice(1, -1),
    html(v, "&#39;"), html(v, "&#x27;"), html(v, "&apos;"),
    Buffer.from(v).toString("base64"), Buffer.from(v).toString("base64url")]) out.add(e);
}
process.stdout.write([...out].join("\n") + "\n");
' "$env_file" > "$patterns" || exit 2

leaked=0
incomplete=0
for target in "$@"; do
  if [ ! -e "$target" ]; then
    echo "INCOMPLETE: $target does not exist"
    incomplete=1
    continue
  fi
  files=$(find "$target" -type f 2>/dev/null | wc -l | tr -d ' ')
  # -a: binary files too (SQLite, LevelDB, crash dumps); -l: names only.
  # -D skip: sockets, FIFOs and devices (a data dir's run/*.sock) hold no
  # stored content; reading them is not possible, so they are not a gap.
  # grep exits 0 on a match, 1 on none, 2 when it could not read a file.
  matches=$(grep -a -r -l -D skip -F -f "$patterns" "$target")
  status=$?
  if [ -n "$matches" ]; then
    leaked=1
    echo "LEAK in $target:"
    echo "$matches" | sed 's/^/  /'
  fi
  if [ "$status" -ge 2 ]; then
    echo "INCOMPLETE: could not read everything under $target"
    incomplete=1
  elif [ -z "$matches" ]; then
    echo "clean: $target ($files files searched)"
  fi
done

if [ "$leaked" -ne 0 ]; then exit 1; fi
if [ "$incomplete" -ne 0 ]; then echo "scan incomplete: not every requested file was searched"; exit 2; fi
echo "no secret found in any searched file"
exit 0
