#!/usr/bin/env bash
# Local launcher for the AO harness audit. Picks Node 24+, finds the AO
# checkout, uses the bundled Codex/Claude contract and gives every attempt a
# fresh report basename outside any Git checkout.
#
#   scripts/run_local.sh fixtures                 # offline fixture tests, no provider calls
#   scripts/run_local.sh check [agent...]         # build AO + safe probes, no provider turns (--no-live)
#   scripts/run_local.sh audit [agent...]         # full live audit (makes provider calls)
#   scripts/run_local.sh only GATE[,GATE] [agent] # rerun one gate/capability against the last report
#   scripts/run_local.sh next                     # show the fix loop's next step from the last report
#
# Environment: AO_REPO (AO checkout), AO_AUDIT_REPORTS (report root),
# AO_AUDIT_CONTRACT (local CLI contract), NODE (node binary), AO_AUDIT_ARGS
# (extra runner flags, e.g. "--keep-daemon --timeout-seconds 300").
set -euo pipefail

SKILL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPORTS="${AO_AUDIT_REPORTS:-$HOME/Desktop/agents/ao-audit-reports}"
CONTRACT="${AO_AUDIT_CONTRACT:-$SKILL_DIR/assets/local-contract.codex-claude.json}"
DEFAULT_AGENTS=(codex claude-code)

die() { echo "error: $*" >&2; exit 2; }

pick_node() {
  local candidate major
  for candidate in "${NODE:-}" "$(command -v node || true)" "$HOME"/.nvm/versions/node/v2[4-9]*/bin/node /opt/homebrew/bin/node /usr/local/bin/node; do
    [[ -n "$candidate" && -x "$candidate" ]] || continue
    major="$("$candidate" -p 'process.versions.node.split(".")[0]' 2>/dev/null || echo 0)"
    if (( major >= 24 )); then echo "$candidate"; return; fi
  done
  die "Node 24 or newer is required (found $(node --version 2>/dev/null || echo none)). Install it with 'nvm install 24' or set NODE=/path/to/node24."
}

find_repo() {
  local dir
  if [[ -n "${AO_REPO:-}" ]]; then echo "$AO_REPO"; return; fi
  # Inside a checkout (.agents/skills/ao-agent-harness), walk up to its root.
  dir="$SKILL_DIR"
  while [[ "$dir" != "/" ]]; do
    [[ -f "$dir/backend/internal/domain/harness.go" ]] && { echo "$dir"; return; }
    dir="$(dirname "$dir")"
  done
  [[ -f "$HOME/Desktop/agent-orchestrator/backend/internal/domain/harness.go" ]] && { echo "$HOME/Desktop/agent-orchestrator"; return; }
  die "AO checkout not found; set AO_REPO=/path/to/agent-orchestrator"
}

latest_report() {
  ls -t "$REPORTS"/*.json 2>/dev/null | grep -v -e '\.next-actions\.json$' | head -1 || true
}

agent_flags() {
  local agent
  (( $# )) || set -- "${DEFAULT_AGENTS[@]}"
  for agent in "$@"; do printf -- '--agent %s ' "$agent"; done
}

NODE_BIN="$(pick_node)"
mode="${1:-}"; shift || true
case "$mode" in
  fixtures)
    REPO="$(find_repo)"
    echo "Node $("$NODE_BIN" --version); AO_REPO=$REPO"
    cd "$SKILL_DIR"
    AO_REPO="$REPO" exec "$NODE_BIN" --test --test-concurrency=1 scripts/*.test.mjs
    ;;
  check|audit|only)
    REPO="$(find_repo)"
    mkdir -p "$REPORTS"
    stamp="$(date +%Y%m%d-%H%M%S)"
    previous=()
    if [[ "$mode" == "only" ]]; then
      [[ $# -ge 1 ]] || die "usage: run_local.sh only GATE[,GATE] [agent...]"
      scope=(--only "$1"); shift
      last="$(latest_report)"
      [[ -n "$last" ]] && previous=(--previous "$last")
      base="$REPORTS/$stamp-only"
    else
      scope=()
      base="$REPORTS/$stamp-$mode"
    fi
    live=()
    [[ "$mode" == "check" ]] && live=(--no-live)
    echo "Node $("$NODE_BIN" --version) · AO checkout $REPO · contract $CONTRACT"
    echo "Report: $base.md (JSON, artifacts and next-actions beside it)"
    [[ "$mode" == "check" ]] || echo "Live mode: this makes real provider calls with the CLIs' existing logins."
    # shellcheck disable=SC2046,SC2086
    "$NODE_BIN" "$SKILL_DIR/scripts/audit_ao.mjs" --repo "$REPO" --local-contract "$CONTRACT" \
      $(agent_flags "$@") ${scope[@]+"${scope[@]}"} ${previous[@]+"${previous[@]}"} ${live[@]+"${live[@]}"} \
      --report "$base" ${AO_AUDIT_ARGS:-}
    ;;
  next)
    last="$(latest_report)"
    [[ -n "$last" ]] || die "no report in $REPORTS yet; run 'run_local.sh check' or 'audit' first"
    "$NODE_BIN" -e '
      const plan = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8"));
      console.log(`Report: ${plan.report}\nLoop status: ${plan.status}`);
      for (const action of plan.userActions ?? []) console.log(`NEEDS USER (${action.agent}): ${action.message}`);
      for (const item of (plan.items ?? []).filter((entry) => entry.failureClass !== "prerequisite")) {
        console.log(`- [${item.failureClass}${item.exhausted ? ", stopped" : ""}] ${item.agent ?? "run"} ${item.gate}: ${item.reason}`);
      }
      if (plan.next) console.log(`\nNext: ${plan.next.action}\nFiles: ${(plan.next.fixTargets ?? []).join(", ") || "-"}\nRerun: ${plan.next.rerun ?? "-"}`);
    ' "${last%.json}.next-actions.json"
    ;;
  *)
    sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'
    [[ -z "$mode" || "$mode" == "-h" || "$mode" == "--help" ]] && exit 0 || exit 2
    ;;
esac
