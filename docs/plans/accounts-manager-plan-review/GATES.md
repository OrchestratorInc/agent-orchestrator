# Gates: Accounts manager implementation plan

OWNS: docs/plans/2026-09-27-accounts-manager-implementation.md, docs/plans/accounts-manager-plan-review/GATES.md

Scope: Write and critically review an incremental implementation plan extending PR #5769 without changing application code or the existing specification.

- [x] G1: The plan distinguishes the reviewed PR baseline from proposed work and preserves the protected native account features.
  EVIDENCE: Reviewed plan sections 1-3 against PR head 6f064d626d55dd5aa1c7996c06a1349a73198460 and local a47db0e06. Native controller enumeration and admission were inspected in codex_account_switch.go and codex_operation_gate.go. The two snapshots are explicitly distinguished; protected modules and native behavior cannot be modified. M0 requires a legitimate coexistence boundary before proceeding.

- [x] G2: The plan specifies implementable account lifecycle, per-session routing, switching, recovery, and performance milestones with explicit dependencies and acceptance evidence.
  EVIDENCE: Reviewed sections 4-9 as proposed contracts, not runtime proof. M0-M5 specify dependencies and exit evidence. The plan assigns credential/binding ownership, journals cross-process operations, defines revision checks and deletion fences, separates switch commitment from readiness, and gives mode-specific and performance checks. Unresolved integration feasibility is an explicit prerequisite rather than an asserted implementation guarantee.

- [x] G3: A correctness and regression review has corrected discovered plan defects and clearly identifies feasibility checks that must pass before implementation advances.
  EVIDENCE: Source review plus separate transaction and regression passes corrected missing live route revocation, unknown-caller fallback, refresh/delete races, pre-commit admission acknowledgement, end-of-turn versus stream completion, and premature revocation during removal. Section 10 records findings and corrections. A final consistency pass checked cancellation, stopped sessions, unsupported modes, feature-off behavior, and rollback. Native coexistence, encrypted-store extension coverage, and exact adapter support remain future executable gates; no application correctness claim is made.

- [x] G4: The plan and review ledger are readable Markdown files and their relative document links resolve.
  CHECK: node -e 'const fs=require("node:fs"); const path=require("node:path"); for (const file of ["docs/plans/2026-09-27-accounts-manager-implementation.md","docs/plans/accounts-manager-plan-review/GATES.md"]) { const text=fs.readFileSync(file,"utf8"); if (!text.startsWith("# ")) throw new Error(file); for (const match of text.matchAll(/\]\(([^#():]+\.md)(?:#[^)]*)?\)/g)) fs.accessSync(path.resolve(path.dirname(file),match[1])); } console.log("plan artifacts and local document links verified");'
  EXPECT: plan artifacts and local document links verified
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79; path=1eef3047a157/35 entries; output=plan artifacts and local document links verified

- [x] G5: This planning task has made no application, dependency, generated artifact, or PR changes.
  EVIDENCE: Final tracked and staged diffs are empty. Status lists only the pre-existing specification and the two new planning documents. Edits were confined to the plan and this ledger; no commits, account mutations, application tests, or publication commands were run. The existing specification was preserved.

## Verification notes

- G4 checks document readability and relative links only. Semantic and source-scope review are the separately evidenced manual gates.
- The initial validator attempt did not produce its success marker because nested process execution was restricted; a control reproduced the process error. It was rerun successfully with reviewed execution approval and an isolated fish configuration directory under `/tmp`. No shell configuration was edited.
- Opening the plan through the desktop preview and persisting the progress report could not complete because the AO daemon is not running. The Markdown artifact remains available directly in the workspace; no daemon or application was started for this planning task.
