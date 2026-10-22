#!/usr/bin/env bash
# Local launcher for the AO harness audit. Picks Node 24+, finds the AO
# checkout, uses the bundled Codex/Claude contract and gives every attempt a
# fresh report basename outside any Git checkout.
#
#   scripts/run_local.sh doctor                   # show Node, AO checkout freshness, CLIs and logins
#   scripts/run_local.sh fixtures                 # offline fixture tests, no provider calls
#   scripts/run_local.sh check [agent...]         # build AO + safe probes, no provider turns (--no-live)
#   scripts/run_local.sh audit [agent...]         # full live audit (makes provider calls)
#   scripts/run_local.sh only GATE[,GATE] [agent] # rerun one gate/capability against the last report
#   scripts/run_local.sh next                     # show the fix loop's next step from the last report
#   scripts/run_local.sh ui <harness> [flags]     # UI audit with screenshots (app must run with --remote-debugging-port=9222)
#
# Settings may also live in local.env beside SKILL.md (sourced first).
# Environment: AO_REPO (AO checkout), AO_AUDIT_REPORTS (report root),
# AO_AUDIT_CONTRACT (local CLI contract), NODE (node binary), AO_AUDIT_ARGS
# (extra runner flags, e.g. "--keep-daemon --timeout-seconds 300").
set -euo pipefail

SKILL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# Optional per-copy settings (e.g. AO_REPO=/path/to/fresh/checkout).
[[ -f "$SKILL_DIR/local.env" ]] && source "$SKILL_DIR/local.env"
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

# The audit builds AO from this checkout, so an old checkout audits old code.
describe_repo() {
  local repo="$1" head behind
  head="$(git -C "$repo" log -1 --format='%h %cd %s' --date=short 2>/dev/null || echo unknown)"
  echo "AO checkout: $repo"
  echo "  HEAD: $head"
  for remote in upstream origin; do
    if git -C "$repo" rev-parse --verify -q "$remote/main" >/dev/null; then
      behind="$(git -C "$repo" rev-list --count "HEAD..$remote/main" 2>/dev/null || echo "?")"
      echo "  $behind commit(s) behind $remote/main (as last fetched)$([[ "$behind" =~ ^[0-9]+$ && "$behind" -gt 50 ]] && echo " — audit an up-to-date checkout: AO_REPO=/path/to/fresh/agent-orchestrator")"
      break
    fi
  done
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
  doctor)
    echo "Node: $NODE_BIN ($("$NODE_BIN" --version))"
    describe_repo "$(find_repo)"
    echo "Contract: $CONTRACT"
    echo "Reports: $REPORTS"
    REPO="$(find_repo)"
    for tool in git sqlite3 codex claude; do
      printf '  %-8s %s\n' "$tool" "$(command -v "$tool" >/dev/null && { "$tool" --version 2>&1 | head -1; } || echo MISSING)"
    done
    # GOTOOLCHAIN=local: report what is installed without downloading the
    # toolchain the checkout asks for (the AO build itself may download it).
    printf '  %-8s %s (checkout needs %s)\n' go "$(command -v go >/dev/null && (cd "$HOME" && GOTOOLCHAIN=local go version 2>&1 | head -1) || echo MISSING)" \
      "$(grep -m1 -E '^(go|toolchain) ' "$REPO/backend/go.mod" 2>/dev/null || echo unknown)"
    printf '  codex login:  %s\n' "$(codex login status 2>&1 | head -1 | sed -E 's/[[:alnum:]._%+-]+@[[:alnum:].-]+/[email]/g')"
    printf '  claude login: %s\n' "$(claude auth status 2>&1 | grep -o '"loggedIn": *[a-z]*' || echo unknown)"
    ;;
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
    echo "Node $("$NODE_BIN" --version) · contract $CONTRACT"
    describe_repo "$REPO"
    echo "Report: $base.md (JSON, artifacts and next-actions beside it)"
    [[ "$mode" == "check" ]] || echo "Live mode: this makes real provider calls with the CLIs' existing logins."
    # shellcheck disable=SC2046,SC2086
    "$NODE_BIN" "$SKILL_DIR/scripts/audit_ao.mjs" --repo "$REPO" --local-contract "$CONTRACT" \
      $(agent_flags "$@") ${scope[@]+"${scope[@]}"} ${previous[@]+"${previous[@]}"} ${live[@]+"${live[@]}"} \
      --report "$base" ${AO_AUDIT_ARGS:-}
    ;;
  ui)
    [[ $# -ge 1 ]] || die "usage: run_local.sh ui <harness> [--install] [--login] [--interfaces tui,chat] [--no-spawn]"
    REPO="$(find_repo)"
    harness="$1"; shift
    out="$REPORTS/$(date +%Y%m%d-%H%M%S)-ui-$harness"
    echo "UI audit of $harness against the running AO app; screenshots in $out"
    AO_REPO="$REPO" exec "$NODE_BIN" "$SKILL_DIR/scripts/ui_audit.mjs" --repo "$REPO" --harness "$harness" --out "$out" "$@"
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
