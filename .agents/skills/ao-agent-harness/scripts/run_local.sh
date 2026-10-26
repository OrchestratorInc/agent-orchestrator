#!/usr/bin/env bash
# Launcher for the harness audit. Picks Node 22+, finds the AO checkout and
# writes each run to a fresh folder outside the checkout.
#
#   scripts/run_local.sh doctor [agent...]   # Node, Go, AO checkout, and each harness's CLI and login
#   scripts/run_local.sh audit [agent...]    # live API audit (real provider calls); no agent = every harness in the contract file
#   scripts/run_local.sh ui <agent> [flags]  # UI audit with screenshots (the app must run with --remote-debugging-port=9222)
#
# Environment: AO_REPO (AO checkout), AO_AUDIT_REPORTS (report root),
# AO_AUDIT_CONTRACTS (contract file), NODE (node binary).
set -euo pipefail

SKILL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPORTS="${AO_AUDIT_REPORTS:-$HOME/Desktop/agents/ao-audit-reports}"
CONTRACTS="${AO_AUDIT_CONTRACTS:-$SKILL_DIR/assets/local-contracts.json}"

die() { echo "error: $*" >&2; exit 2; }

pick_node() {
  local candidate
  for candidate in "${NODE:-}" "$(command -v node || true)" /opt/homebrew/bin/node /usr/local/bin/node; do
    [[ -n "$candidate" && -x "$candidate" ]] || continue
    (( $("$candidate" -p 'process.versions.node.split(".")[0]' 2>/dev/null || echo 0) >= 22 )) && { echo "$candidate"; return; }
  done
  die "Node 22 or newer is required; set NODE=/path/to/node"
}

find_repo() {
  local dir="${AO_REPO:-$SKILL_DIR}"
  while [[ "$dir" != "/" ]]; do
    [[ -d "$dir/backend/cmd/ao" ]] && { echo "$dir"; return; }
    dir="$(dirname "$dir")"
  done
  die "AO checkout not found; set AO_REPO=/path/to/agent-orchestrator"
}

NODE_BIN="$(pick_node)"
mode="${1:-}"; shift || true
case "$mode" in
  doctor)
    REPO="$(find_repo)"
    echo "Node: $("$NODE_BIN" --version) · Go: $(go version 2>/dev/null | awk '{print $3}' || echo MISSING) · sqlite3: $(command -v sqlite3 >/dev/null && echo ok || echo MISSING)"
    echo "AO checkout: $REPO ($(git -C "$REPO" log -1 --format='%h %cs' 2>/dev/null))"
    [[ -f "$REPO/frontend/resources/acp-runtime/package.json" ]] && echo "Chat bridge: built" || echo "Chat bridge: not built (ACP Chat needs it: npm run build:acp-runtime in frontend/)"
    "$NODE_BIN" -e '
      const { spawnSync } = require("child_process");
      const agents = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")).agents;
      const run = (bin, args) => { const out = spawnSync(bin, args, { encoding: "utf8", timeout: 15000 }); return out.error ? null : `${out.stdout}${out.stderr}`; };
      for (const id of process.argv.slice(2).length ? process.argv.slice(2) : Object.keys(agents)) {
        const contract = agents[id];
        if (!contract) { console.log(`  ${id}: not in the contract file; copy assets/contract-template.json`); continue; }
        const version = run(contract.binary, contract.versionArgs ?? ["--version"]);
        const login = contract.integration ? run(contract.binary, contract.integration.args) : null;
        const state = !contract.integration ? "no login check" : login?.includes(contract.integration.expect) ? "logged in" : "NOT logged in";
        console.log(`  ${id}: ${version === null ? `MISSING (${contract.binary})` : version.trim().split("\n")[0]} · ${state}`);
      }
    ' "$CONTRACTS" "$@"
    ;;
  audit)
    REPO="$(find_repo)"
    out="$REPORTS/$(date +%Y%m%d-%H%M%S)-audit"
    agents=()
    for agent in "$@"; do agents+=(--agent "$agent"); done
    echo "Live audit: this makes real provider calls with the CLIs' existing logins."
    exec "$NODE_BIN" "$SKILL_DIR/scripts/audit.mjs" --repo "$REPO" --contracts "$CONTRACTS" --out "$out" ${agents[@]+"${agents[@]}"}
    ;;
  ui)
    [[ $# -ge 1 ]] || die "usage: run_local.sh ui <agent> [--install] [--login] [--interfaces tui,chat] [--no-spawn]"
    REPO="$(find_repo)"
    agent="$1"; shift
    out="$REPORTS/$(date +%Y%m%d-%H%M%S)-ui-$agent"
    AO_REPO="$REPO" exec "$NODE_BIN" "$SKILL_DIR/scripts/ui_audit.mjs" --repo "$REPO" --harness "$agent" --out "$out" "$@"
    ;;
  *)
    sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'
    [[ -z "$mode" || "$mode" == "-h" || "$mode" == "--help" ]] || exit 2
    ;;
esac
