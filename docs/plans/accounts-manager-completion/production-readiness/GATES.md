# Gates: production-readiness specification

Scope: author and review SPEC.md and PLAN.md for the five requested release-readiness areas, plus the implementation checklist. This ledger measures the documents, not implementation or release approval. Product acceptance remains open in SPEC.md and IMPLEMENTATION-GATES.md.

- [x] D1: all five requested areas have bounded requirements, observable acceptance cases and a release decision.
  EVIDENCE: self-review of SPEC.md R1-R5 confirmed all five requested outcomes, implementation boundaries, failure controls and exit evidence. Read-only Node structural validation counted 28 unique acceptance cases across five sections (6/6/6/5/5), exit 0 on 2026-09-29. This verifies document coverage, not product behaviour.

- [x] D2: lifecycle, account isolation and recovery requirements preserve user choice, exact ownership, protected paths and the existing public service boundaries.
  EVIDENCE: compared domain/accounts_manager_removal.go and accounts_manager_switch.go, the original product specification and parallel-session-delivery/PLAN.md. Self-review clarified local in-flight cancellation versus remote uncertainty, retained missing-proof recovery, and added repair acceptance for already-stuck shutdown operations. Protected native behaviour and all 91 paths remain requirements; no runtime implementation was changed or independently re-reviewed here.

- [x] D3: the specification separates recorded evidence, new implementation requirements and unavailable native/live verification without declaring product completion.
  EVIDENCE: reconciled ACCOUNT-PICKER-REPAIR.md, parallel-session-delivery/user-flow/LIVE-RESULTS.md and testing-handoff/EVIDENCE.md with baseline d23bdaee42640da7d7fe70fd0b9af52843a3ca61. Preserved five failing recovery cases, bounded A/B observations, daemon reattachment limits, skipped tests, missing native execution and final-review gaps. All five product gates remain OPEN. No application tests were rerun for this prose-only task.

- [x] D4: the final document has valid local references, is opened for the user, and changes no production source or historical evidence.
  EVIDENCE: read-only structural check from the repository root under /usr/bin/fish exited 0 with SPEC_STRUCTURE_AND_SCOPE_PASS: 14 local links resolve, a deliberately missing-link control is rejected, whitespace checks pass, HEAD is unchanged and the only additions are SPEC.md and this ledger. git diff --check exited 0. ao preview opened SPEC.md; browser title/URL and the R5 heading were verified. No source edits, staging, commits, pushes or PR publication occurred.

- [x] D5: PLAN.md maps the specification to concrete implementation boundaries, dependencies, failed-first tests and independent review checkpoints.
  EVIDENCE: self-reviewed all 12 milestones against inspected launch/permit/ownership, switch finish/retry, managed-mode rejection, refresh, migration and renderer configuration boundaries. Corrected a potential native-verification/managed-interface dependency cycle by separating ownership subgates from final workflow acceptance; retained already-green controls and clarified durable cancellation linearization. Future independent reviews remain explicit milestone requirements.

- [x] D6: the implementation ledger covers every specification acceptance case without claiming implementation or weakening existing safety assertions.
  EVIDENCE: final read-only Node check exited 0 with PLAN_FINAL_STRUCTURE_PASS: all 28 specification acceptance IDs are mapped, the 12 plan milestones match the 12 unchecked implementation gates, and a deliberately omitted acceptance ID is detected. Ledger status parsing reports 0 met and 12 unmet, as expected for future implementation. Existing five positive retirement cases and protected invariants remain required.

- [x] D7: plan references and scope checks pass, SPEC.md remains unchanged, and the plan is opened for the user without source changes or publication.
  EVIDENCE: final structural validation from the repository root under /usr/bin/fish exited 0: 18 local references resolve, seven explicit existing source references exist, one new adapter path is labelled proposed, and a deliberately missing link is rejected. SPEC.md retains SHA256 d67788b40f9f1a4e7fa533bd76b4b294da96e48b18ba0b273ed22d86c2909878. Tracked source, index and HEAD are unchanged; only the four documentation files in this directory are untracked. Whitespace checks pass. The plan was opened with ao preview and its final dependency text was verified in the Browser panel. No application tests, implementation, commits, pushes or PR publication occurred.
