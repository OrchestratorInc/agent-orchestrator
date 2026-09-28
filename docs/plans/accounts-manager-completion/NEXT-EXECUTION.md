# Account completion execution record

User authorization: 2026-09-28. Continue implementation through the plan with review between slices. No routine approval pauses. No release-complete claim until native and live-provider gates have evidence.

## Baselines and artifacts

- Published control: `04e12ca3dc78ba96674a63c2039ac6b0b271b52f`.
- Fetched main: `e853b39601876d4be6d51c7f99dce0adcea5fa10`.
- Current artifact directory: `/var/tmp/pr-5769-next-79.wos4rh`.
- Protected baseline: `/tmp/pr-5769-public-controls-79.PbwFW0/protected.sha256`, 91 files. Preserve its original bytes; upstream-only changes require a separate reviewed comparison.
- Prior verification: `/tmp/pr-5769-publish-79.p3jxPQ`. Do not rewrite old logs, manifests or review seals.

## Slice state

| Slice | State | Next acceptance evidence |
| --- | --- | --- |
| P0 integration | Source sealed, independent review requested | Frozen merge/migration correction; support matrix and wider release checks remain open |
| P1 selection/isolation | Pending | Atomic initial binding and production A/B identity tests |
| P2 switch readiness | Pending | Deterministic readiness/cancellation correction and independent review |
| P3 retirement/platforms | Pending | Escaped-descendant correction, durable recovery and native proof |
| P4 credentials/controls | Pending | Supported kinds, usage, Harness scope and UI contracts |
| P5 integrated evidence | Pending | Complete checks, actual desktop/provider runs and final review |

Reviewer coordination and native Windows/Mac host availability were requested through the orchestrator. Unavailable native hosts do not block independent local work and do not count as passing platform evidence.

## P0 acceptance

- [x] B1: the unchanged published control reproduces the production readiness and escaped-descendant assertions with preserved logs.
  EVIDENCE: `/var/tmp/pr-5769-next-79.wos4rh/published-blockers-red.log`, isolated Go race command exited 1 before merge. Managed/native target readiness failed with TARGET_NOT_READY. Both escaped-descendant cases failed with a live child after retirement acknowledgement. The cancellation control passed this run; its earlier intermittent failure remains under investigation.
- [x] B2: current main is reconciled without discarding either side's unrelated behavior or modifying merged migrations.
  EVIDENCE: source tree `9902a253fb1ffbae3ab1492d3068f721b6da2973`; six conflict resolutions, final full SQLite/spec tests, repeated legacy upgrades and native prompt controls pass. Main migration bytes and renamed branch migration bytes are verified independently.
- [x] B3: regeneration, affected compile checks and the protected-path comparison validate the integrated source.
  EVIDENCE: all eight commands in `integration-final-checks.log` exit 0. Frontend typecheck exits 0 under Node 24.21.0. Final protected audit records 89 original matches and two exact main matches; generated repeat has zero drift.
- [x] B4: self-review and an exact integration manifest distinguish upstream changes from feature-authored edits.
  EVIDENCE: `integration-freeze/SUMMARY.json`; 20-file correction, 318-file integration delta, 5,671-file source manifest, 91-file protection manifest and immutable archive. Independent verdict remains required and is not implied by this self-review gate.

## Integration review, 2026-09-29

- A normal no-commit merge incorporates current main. Six textual conflicts are resolved in the working files, retaining the automation and account-control dependencies on both sides. Generated API artifacts were regenerated from merged sources.
- The merged tree also exposed migration-number collisions at 161-164. Main owns these versions. Only this branch's eight account migrations move, byte-for-byte, to 165-172. The four main migration files are unchanged.
- Failed-first legacy-upgrade evidence: `migration-legacy-upgrade-red.log` in the artifact directory. All eight historical account migration cutoffs failed before correction; main controls passed. New transactional history repair checks schema evidence before remapping version entries, retains durable bindings and journals, and refuses incomplete proof.
- Final race checks passed three repetitions for eight legacy upgrades, six main controls, six corrupt/incomplete evidence cases, four rollback/reopen cases and targeted migration controls. `migration-race3-final.log` replaces the first-run checks as final source evidence.
- Self-review added failure-atomic history writes and process-reopen coverage. The helper runs before older compatibility repairs so an invalid account schema cannot partially rewrite its history.
- `protected-integration-audit.json` records 89 original matches and two exact upstream-only changes across the 91 protected paths. It also verifies all eight moved migration contents and all four main migration contents. Original manifests remain unchanged.
- Readiness diagnosis lead: the public execution runtime fixture implements fenced probes but not the optional exact supervised-process inspector required by target readiness. Production guards must remain strict. This is not yet a runtime verdict and will be verified in the readiness slice.
- Full schema verification found an incoming downgrade test that migrated to the latest version but asserted 164. The test now explicitly applies through 164 before reversing that migration. The failure is preserved in `integration-schema-full.log`; the corrected full suite passes in `schema-full-final.log`.
- Untouched main reproduced the inherited-shell runtime failure with a test-owned tmux socket. With `SHELL=/bin/sh`, unchanged main and the integrated branch both pass the full selected tmux/observer/terminal matrix three times under race. Logs: `main-inherited-shell-control.log`, `main-runtime-controls.log`, `branch-runtime-controls.log`. No user shell configuration or shared runtime service was changed. This classifies the observed environment-sensitive failures, not arbitrary runtime errors.
- The first frontend command had an invalid Fish PATH expansion and did not start the compiler. Its red log is retained. The corrected scoped Node 24 PATH starts the compiler and exits 0 in `integration-frontend-typecheck-final.log`.

Still open: final integration checks/review, readiness and cancellation validation, escaped-descendant retirement, initial account selection, credential experience, native platforms and live-provider/desktop evidence. No release-completion claim is made.
