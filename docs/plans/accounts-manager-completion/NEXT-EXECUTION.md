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
| P1 selection/isolation | Initial-choice slice sealed, review requested | Managed app-server and actual A/B provider execution remain open |
| P2 switch readiness | Source sealed, independent review requested | P2 cancellation/readiness correction passes; independent verdict remains unconfirmed |
| P3 retirement/platforms | Pending | Escaped-descendant correction, durable recovery and native proof |
| P4 credentials/controls | Bounded input/display correction sealed | Native profiles, explicit migration and independent acceptance remain open |
| P5 integrated evidence | Pending | Complete checks, actual desktop/provider runs and final review |

The complete lint correction is separately sealed at tree `b730765f4fcb099a7522c97f1fe8b37df3ff87b4`. REVIEW-82-NEXT-LINT.md records the 56-file correction, full pinned backend/runner lint with zero findings, 13 affected backend race packages, full runner race, repeated boundary checks, actual-process switching checks, build/vet, generated drift and protected hashes. The broader P5 gate remains open; known containment and native/live-provider gaps are unchanged.

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

## P1 local checkpoint

The initial-selection source is frozen at tree `47243d155de8668ae63018879b547978f9cce64d`, with a 44-file implementation manifest and 5,695-file full source manifest. REVIEW-82-NEXT-INITIAL-SELECTION.md names the exact archives, hashes, red logs and verification. Atomic creation, prepared promotion, explicit CLI/desktop choice and native background admission are implemented. The six affected backend packages pass full race and repeated focused checks. The final renderer passes 5,583 tests with seven skips, both typechecks and Linux packaging.

Real isolated Electron exposed and helped correct a managed harness menu and unbound model-discovery gap. The final capture uses an empty credential inventory and proves unavailable-state behavior only. No live managed credentials were copied or used, and no provider task ran. Public positive-account execution, managed app-server support and real A/B identity proof remain open.

P0/P2 review requests and the request for a live reviewer were accepted by AO without confirmed provider delivery. Independent verdicts are not assumed. Deletion remains held. Source archives, protected manifests and native platform gaps are preserved; all work remains local.

## P4 local checkpoint

Credential correction tree `ba2d1198a6b4e5c331491d7c996bc91f1fe9f4f4` is sealed with 30 source/test/generated files. REVIEW-82-NEXT-CREDENTIALS.md names the exact manifests, 108 evidence files and archive hashes. Known token input is rejected before API-key mutation; legacy projection preserves storage/admission and cannot invent quota permission. Usage and Harness distinguish credential capabilities from device login. Real Electron exposed the missing renderer error allowlist, reproduced and corrected with transport-level tests.

Final local checks pass: focused races count 3, four complete affected backend packages, full runner race, API parity, both builds/vet and complete pinned lint; 177 focused frontend tests, 5,606 full-suite passes with seven existing skips, both typechecks and Linux packaging. API/SQL repeat has zero drift; 91 protected files and earlier archives remain unchanged. Three actual desktop screenshots and recording frames were inspected using isolated data and a real provider catalog. They prove negative/unavailable paths only. C6's local freeze/handoff obligation is recorded here; independent acceptance remains pending.

Still open: independent integration/switching/initial-selection/credential review, managed app-server execution, escaped-descendant retirement, isolated native profiles and explicit migration, native platforms, full branch checks, responsiveness and live-provider/desktop evidence. No release-completion claim is made.

## P1 managed route configuration checkpoint

The four-file launch/restore correction is sealed at tree `b5196dd5e49df2254df41e5dd1760a1957d6b57a`. REVIEW-82-NEXT-CODEX-ROUTE.md records the exact 5,712-file source, correction, plan and 29-file evidence manifests. Non-nil invalid managed intent no longer falls back to native argv; valid routes explicitly request process-memory credentials. Native commands and protected files remain unchanged.

All 14 final checks pass: repeated focused and full adapter races, 12 installed-binary cases, matched-shell parent/current complete session-manager races, production execution/cancellation race x3, build/vet/lint and three platform cross-compiles. The installed experiment uses synthetic state and sends no provider request. One earlier ownership-test timeout remains preserved and unclassified. No native or live-provider pass is inferred.

The local freeze/request obligation is complete; independent acceptance is pending. Managed Chat remains held on exact transport ownership and containment, not enabled by a configuration-only result. Responsiveness measurement is the next independent local slice.

## P5 bounded runner timing checkpoint

Test-only tree `2d4e69bbd00199f87e6491420e27d90e857a170a` is sealed with two test files, one plan and 26 evidence files. REVIEW-82-NEXT-RESPONSIVENESS.md records exact hashes and all 12 passing final commands. The oracle's two midpoint false-positive cases were reproduced before correction. Fifty accounts are verified, twenty bindings exercised, and the full non-race run records 100 paired warm samples and 30 real restarts with exactly 302 streaming upstream requests and no failures.

Additional first-delta p95 is 1.288 ms on this host. Capability preparation p95 is 0.253 ms; reopened runner through an authorized response p95 is 127.130 ms. These are bounded components with explicit exclusions, not complete route/controller/desktop measurements. Raw distributions, a 29.677 ms full-stream maximum and machine/process limits are preserved. G15 stays open. All six prior seals, 91 protected files and 33 generated files pass integrity checks. No production behavior changed.
