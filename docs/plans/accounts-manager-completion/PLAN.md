# Accounts Manager completion work

Scope: complete the remaining implementation in PR 5769 against the [specification](../2026-09-26-accounts-manager-spec.md) and [implementation plan](../2026-09-27-accounts-manager-implementation.md). Earlier review gates certify only that increment. This ledger starts with the release requirements unmet.

All work stays local until explicit publication approval. Preserve the existing dirty worktree, Subscriptions, and native account-switch modules. The approved loopback callback extension is the only exception to the pinned engine boundary. Implement sequentially in this session and review after each coherent change.

| Work | State | Exit evidence |
| --- | --- | --- |
| Credential persistence and lifecycle | Serve, browser/device transport, draft recovery, reconnect identity/CAS, and manual refresh coalescing have synthetic integration evidence; remaining maintenance and release evidence open | All enabled methods use encrypted writes, late writers cannot resurrect deleted records, and cancellation/recovery tests pass. |
| Session choice and route authorization | Durable intent and revision-aware runner authorization verified synthetically; full integration gates open | Explicit native/managed records, revision-bound authorization, concurrent A/B isolation, and restore tests pass. |
| Switching and in-use removal | R4 independently CLEAR; coordinated deletion implemented and locally frozen for reviewer 82; public controls/UI held | Durable deletion, exact-owner stop/revocation, cancellation, no-fallback cleanup, and restart proof pass. Independent deletion CLEAR remains required. |
| Accounts and session controls | Waiting | Real desktop initial choice, two-action switch, account maintenance, pending/error states, and keyboard interaction pass. |
| Compatibility and release verification | Full frontend and runner checks pass; full backend verification and lint resolution remain open | Actual supported CLI modes, native coexistence, complete relevant checks, platform evidence, and latency measurements are recorded. |

The first implementation review traces each SDK persistence entry point. The runner remains the sole credential writer. The daemon owns durable user choices and session operations. Private management commands must acknowledge durable state before public success. No busy-switch timing default is inferred; offer explicit supported actions.

Per increment: reproduce missing behavior, implement, run focused checks, reread integration callers, record the result, then proceed. Finish with full regression and real desktop checks. Missing live credentials or platform access is a verification gap, never a passing gate.

## Progress

- 2026-09-27: resumed the attached PR with prior review changes intact. Confirmed multiple SDK credential-write paths; a token-store override alone does not cover imports or key configuration.
- 2026-09-27: implemented and tested the encrypted store, operation cancellation, tombstones, and concurrent completion/removal fences. Runner build, vet, and full race suite passed; Windows/macOS builds passed. The store is not wired into the running proxy. [Persistence review](storage/REVIEW.md) records executable SDK bypass evidence and the browser callback extension decision. Later milestones remain blocked by M0, not completed by these package checks.
- 2026-09-27: resumed PR ownership with a successful no-takeover claim after session 81 released it. Imported its approved SDK callback extension and credential-runtime/admission tests at the same base commit. Its local report records verification, but checks are rerun here before integration. No changes were made to the other worktree.
- 2026-09-27: wired Serve to encrypted persistence and per-request admission, replaced daemon key/import/status/removal commands, disabled legacy writers and watching, and restored SDK model registration. Runner build/vet/full race and daemon adapter race suites pass. Callback response testing now models the independent browser lifetime; 100 repetitions and the full authentication package pass. Device transport and legacy-data recovery are next; all product gates remain open.
- 2026-09-27: encrypted device subprocess handoff and draft gateway recovery are implemented. Device/browser lifecycle races passed 100 repetitions; migration/vault tests passed 20. Actual runner A/B routing, restart, removal, and another restart passed 10 repetitions. This exposed default SDK error-request logging despite request-log=false; it is now disabled through the public factory, with a regression assertion. Final runner build/vet/full race and daemon adapter/service race suites pass. No product gate is marked complete; reconnect, refresh coalescing, bindings/switching, UI, and release verification remain.

## Current integration slice

Current checkpoint: [coordinated deletion review handoff](REVIEW-82-DELETION.md). R4 is independently CLEAR in `rpt_3b54e7fc-bd40-4e04-951f-edc73173d20f`, and its four correction files remain unchanged. Deletion now has durable cancellation/stop boundaries, complete binding impact, exact-owner teardown, runner revocation, cold-host identity, queue preservation, and idempotent no-fallback cleanup. All six local deletion gates pass, including full race suites for 13 backend packages, 105 production-process executions, runner full checks and restart proof, build/vet, zero changed-scope lint findings, zero API/SQL drift, and 91 protected baseline matches. The exact 42-file delta and 1,802-file source snapshot are frozen for reviewer 82. Stop here before public controls/UI pending independent CLEAR. The earlier full frontend suite passed 337 files and 5,341 tests with six skips; it was not rerun for this backend slice. Broader verification does not close product gates.

Reconnect now preserves a provider-established identity and credential generation, with a material fingerprint CAS against concurrent refresh. Its public controls carry an explicit target and show bounded failure codes. Imports cannot claim replacement identity. Manual checks share a per-account refresh and a four-account pool. These local checks do not establish live sign-in, platform runtime, or desktop release readiness.

1. Rerun callback and credential foundation tests, then connect durable storage and request admission to the runner. Replace enabled plaintext management writers and disable file-watcher credential loading. Restore model registration through public SDK boundaries.
2. Connect the daemon to bounded private credential commands, preserving stable public account identifiers and safe snapshots. Prove add, disable, remove, restart, and actual A/B upstream identity using synthetic credentials.
3. Replace browser/device workers only after their safe transport and persistence tests pass. Unsupported methods remain unavailable with an explanation. Complete reconnect, refresh, and operation recovery before marking the lifecycle gate met.
4. Review cross-boundary behavior and run the affected full suites. Continue with explicit session bindings and switching after the required runtime boundaries pass.

M0 checks gate enabling each capability. Independent local implementation and synthetic verification may proceed while live-provider and native-platform release evidence remains unavailable; those gaps remain open release gates.
