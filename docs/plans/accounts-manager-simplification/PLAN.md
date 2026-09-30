# Accounts Manager simplification implementation plan

Date: 2026-09-30. Status: local P0/P1 candidate sealed; independent review and approved publication block cutover.

Contract: [SPEC.md](SPEC.md). Execution evidence: [IMPLEMENTATION-GATES.md](IMPLEMENTATION-GATES.md). The user authorized local implementation after the documentation review. Preserve the embedded engine until dependency cutover gates pass; remote publication still requires explicit approval. Existing compact UI changes remain separately preserved.

## Execution order

```text
P0 baseline and patch audit
  -> P1 immutable dependency candidate
  -> P2 build and packaging cutover
  -> P3 equivalence review and engine-tree removal
  -> P4 bounded integration/containment audit
  -> P5 essential workflow closure and final review
```

Keep each phase a separately reviewable change. Independent reviewers receive exact source manifests, commands, failures and omissions. Do not combine an upstream upgrade, dependency extraction and runtime repair in one patch. Routine local progress does not authorize remote publication.

## P0: Freeze the actual starting point

Owns: dependency inventory and evidence documents only.

1. Record the current base/head and dirty-file hashes. Preserve the compact-switch source, protected native/Subscriptions inventory, guest-design records, generated contracts and existing review archives. Include unrelated working changes in preservation checks without importing them into this slice.
2. Recompute the PR line/file/binary totals and split the engine implementation, tests and other content. The initial verified benchmark is 602,994 additions, of which 532,876 are engine additions, leaving 70,118 before replacement overhead.
3. Compare the embedded tree against the exact upstream commit in SPEC.md. Produce a complete patch/exclusion manifest covering callback, streaming, registration test isolation, module cleanup and every other retained difference. Investigate unexplained differences before moving files.
4. Capture production and test import closures, selected module versions, embedded files, testdata and non-Go runtime assets for all five build targets. Scan executable build/CI references separately from historical documentation references.
5. Record known failures, especially terminal wait-for-turn and the engine media race. Preserve their assertions and existing failing logs. Reproduce relevant controls on both sides of the later cutover without calling an inherited red result green.

Exit: exact baseline, scope and patch inventory reviewed. No source removed.

## P1: Prepare and verify the patched dependency

Owns: isolated engine dependency candidate and its regression/CI contract. No AO runtime changes.

1. Prepare the candidate locally from the same upstream lineage plus the complete reviewed embedded delta. Keep retained source and tests byte-for-byte initially. Preserve the license, original notices, retained catalogs, `config.example.yaml` and fixture files. Do not restore previously excluded standalone paths or upgrade versions.
2. Run callback, streaming and registration tests in the candidate module. Add extraction-boundary checks with negative controls: stock upstream without the callback extension must fail; removing the delimiter fix must fail early event delivery; an omitted embedded asset or license must fail verification. Use disposable fixture checkouts, never the working tree or shared module cache.
3. Run the complete engine ordinary/race suites, build/vet and dependency drift check. Preserve the known media race as a named failure until separately fixed. Configure exact-revision dependency CI so later account PRs do not silently stop testing the engine.
4. Resolve publication destination and ownership. An AO-controlled public fork is the selected design; repository creation or publication requires separate explicit approval. If no fetchable approved revision exists, stop before cutover and retain the local engine. Do not replace it with a temporary developer-only path.
5. After approved publication, let Go resolve the full fork commit to its canonical version. Verify a fresh-cache fetch, checksums, module archive contents and provenance. Record maintainer responsibility and the update/rollback procedure.

Exit: immutable fetchable candidate; all production deltas accounted for; required patch regressions and ordinary module suite pass; any inherited race failure remains a separate explicit release HOLD. Independent dependency review required before P2.

## P2: Wire the runner and desktop build

Owns: `accounts-manager/runner/go.mod`, runner `go.sum`, a small dependency/provenance record, `accounts-manager/UPSTREAM.md`, relevant packaging scripts/tests and the nested-module workflow. Keep `engine/` present until P3.

1. Add a failed-first packaging test that hides the local engine directory in a disposable checkout. Current license copy and local replacement must fail for the known reasons. Add wrong-revision, unavailable-module, tampered-content and missing-license controls.
2. Replace `../engine` with the exact reviewed external module revision while retaining canonical SDK imports. Keep `GOWORK=off`; do not add an ambient `go.work`, generated checkout inside the repo, submodule, source tarball or runtime downloader. Review `go mod tidy` changes and reject unrelated upgrades.
3. Update `frontend/scripts/build-accounts-manager.mjs` to resolve the pinned module using Go's structured module metadata and verify it before copying notices. Preserve existing resource filenames and `frontend/forge.config.ts` package assertions. Compare the executable's `go version -m` replacement module/version with the dependency record. If patch identity is added to runner version output, keep its existing contract and add matching tests rather than weakening the version check.
4. Change `.github/workflows/accounts-manager.yml` from a local engine module path to exact-revision dependency verification. Test an exact candidate checkout, not a module-cache tree modified in place. Include packaging script/metadata paths in triggers and cache keys. Preserve fork-contributor buildability with no private-repository token requirement.
5. Verify packaged startup and version/provenance without source checkout or dependency fetching. Check fresh-cache and warm-cache offline builds separately. Corrupt or missing dependencies must fail before packaging, with no account or engine fallback.

Exit: clean checkout builds and packages against the reviewed module with the local engine hidden. Source/module graph differences are limited to approved extraction metadata. Midpoint independent review before any removal.

## P3: Remove only the externalized copy

Owns: `accounts-manager/engine/**` removal and directly obsolete build/CI references. No runtime policy or schema changes.

1. Produce a recoverable archive, per-file hashes and the exact removal list. Verify it matches the reviewed external candidate, including the required regression tests and assets. Archive verification happens before deletion.
2. Remove the resolved engine path only after P1/P2 acceptance. Do not delete any directory through a broad workspace glob. Preserve the current runner, backend and frontend integration tests and the dependency record/license packaging.
3. Run the full runner ordinary/race suites, build/vet, configured lint, exact dependency tests and all five target builds. Check clean clone and normal desktop packaging. Compare normalized dependency/source/asset closures to P0; expected module build metadata is not a runtime regression.
4. Verify existing vault/bindings/journals reopen without migration using synthetic data in an isolated copy. Confirm loopback management, callback cancellation, secret redaction, overlap delivery and per-request admission still work through the public production construction.
5. Recompute final net additions/deletions and binary counts. Do not claim 70,118 as a fixed target or rewrite branch history to hide the old source. Publish only after the user approves the exact action and repository PR/evidence requirements are met.

Exit: no required build/runtime/test asset depends on the removed sibling tree; required new-snapshot checks pass or explicitly preserve an established inherited release blocker; independent extraction review CLEAR. No production-ready claim yet.

## P4: Audit remaining integration and containment scope

Owns: one bounded candidate at a time, selected by reference and runtime evidence. This phase may legitimately remove nothing.

1. Classify every proposed cleanup as required, duplicated, unreachable or deferred. Record callers, reflective/registered use, platform build tags, persisted-state compatibility and test coverage. Line count alone is not evidence.
2. Retain the existing service/port boundaries. A helper may be consolidated only if ownership, errors, cancellation, locking and redaction are equivalent in its real callers. Do not build a new abstraction layer just to reduce files.
3. Freeze guest design/history and declare advanced containment implementation deferred where not independently verified. Move only proven unused experiments to recoverable follow-up scope; never remove primitives already required for exact-owner switching or safe revocation. Preserve tests defining the unresolved descendant-retirement contract.
4. Prove unsupported deletion refusal end to end: capabilities and UI/CLI explain the limitation, a preflight refusal has no irreversible mutation, and an in-progress uncertain removal retains its tombstone, encrypted credential and recovery identity. No process label, missing descriptor, failed probe or ordinary group disappearance may turn into a success acknowledgement.
5. Review each deletion/consolidation independently, rerun affected full packages and all protected hashes. Keep account safety/lifecycle changes in separately bounded implementation commits, not a broad cleanup diff.

Exit: each cleanup has evidence and a rollback; no weakened guarantee or deleted regression; unsupported capabilities are explicit. Native paths and Subscriptions remain byte-for-byte preserved unless a separately reviewed product requirement authorizes a minimal change.

## P5: Close essential workflows and prepare final review

Owns: separately scoped behavior fixes, integrated verification and current testing/release documentation. Follow existing implementation plans for unfinished account features; do not redesign them as part of extraction.

1. Repair the live terminal wait-for-turn failure separately. Begin with a deterministic failing regression that reaches source completion and then rejects quiescence. Identify the exact idle/surface condition before changing a guard. Preserve unknown-owner, pending-permission, nonempty-draft and foreign-generation negative controls.
2. Complete the existing essential gaps: remaining managed Chat support, isolated native profiles, explicit setup-token migration/reconnect, stale authorization rejection and restart recovery. Keep platform-unavailable modes blocked instead of silently using device-global credentials.
3. Repeat actual A/B overlap and both switch timing choices in Chat and terminal. Verify request routing at the controlled transport in synthetic tests and binding/controller state with real responses in live runs. Test queue adoption/cancel/retry, account B continuity while A is revoked, restart persistence, unavailable usage and positive reported quota. Do not use assistant self-identification as billing proof.
4. Run the complete integrated suites and actual isolated desktop flows on the final snapshot, with real provider catalogs and only approved safe local credentials. Compare stream/switch timings and responsiveness to the same-workload baseline. Record exact failures/skips and native Windows/Mac gaps. Linux success or cross-compilation cannot clear native execution or containment gates.
5. Reconcile the current base conflicts in a separate reviewed change, refresh generated drift and measured PR counts, and request final independent bug review. Follow the repository's PR description and real-app evidence requirements before any approved push. No publication merely to trigger a missing native runner.

Exit: every advertised core flow passes on its supported platforms, unsupported features are clearly gated, independent review is CLEAR, and remaining platform capabilities have explicit release decisions. A smaller PR alone does not satisfy this phase.

## Test ownership and commands

All commands below describe future implementation verification, not tests executed for these documents. Use the repository/CI-pinned toolchains, `GOWORK=off`, isolated data and serial package execution where required by shared fixtures. Record command, directory, exit, elapsed time, skips and source manifest.

| Boundary | Required checks |
| --- | --- |
| Exact engine candidate checkout | `go mod tidy -diff`, `go build ./...`, `go vet ./...`, `go test -count=1 ./...`, `go test -race -count=1 ./...`; focused callback/delimiter/registration tests with count 3 |
| AO runner module | Same ordinary/race/build/vet/tidy checks, `go mod verify` after exact fetch, repository-configured lint, vault admission and overlap integration regressions |
| Five build targets | Production and test dependency-closure comparison; `CGO_ENABLED=0` builds for linux/amd64, linux/arm64, windows/amd64, darwin/amd64, darwin/arm64; native behavior tested separately |
| Desktop packaging | Normal runner builder and packaged resource/version checks; missing local tree, wrong pin, license loss and module corruption negative controls; isolated real Electron startup |
| Integrated repository | Backend full ordinary/race/build/vet and pinned lint, frontend full tests/typecheck/build, `npm run api` and `npm run sqlc` with no unexplained generated diff, workflow validation, protected-path hashes |

Keep the existing source-scanning SDK persistence diagnostics and runner behavioral tests. They document unsafe upstream entry points the wrapper must keep unreachable; moving the engine must not delete their boundary assertions. Preserve the inherited engine media race result until its own correction is reviewed. Never replace a failing command with a narrower pass under the same gate name.

## Acceptance ledger for implementation

All entries start unverified. Update each only against the exact implementation snapshot; the separate planning [GATES.md](GATES.md) cannot clear them.

| ID | Outcome | Required negative control or review | Status |
| --- | --- | --- | --- |
| E1 | Complete patch/exclusion/assets manifest reproduces the embedded candidate | Unpatched callback and delimiter variants fail the corresponding contracts | Unverified |
| E2 | Exact public dependency resolves in a fresh cache with matching provenance | Wrong/missing pin or corrupted content fails; no mutable/local replacement accepted | Unverified |
| E3 | Clean build/package needs no local engine tree | Missing license/embedded asset fails; packaged notices retain exact bytes | Unverified |
| E4 | Test ownership survives the move | Engine tests run at the exact pin, not inferred from runner tests; known race failure stays visible | Unverified |
| E5 | Parallel account/credential isolation survives extraction | Cross-session credential, stale route and fallback attempts rejected while unrelated B continues | Unverified |
| E6 | Both switch timings preserve ownership, history and queues | Terminal post-turn failure fixed; stale readiness, cancellation races and foreign owners remain rejected | Unverified |
| E7 | Revocation and durable recovery survive restart | Late refresh, stale lease, interrupted journal and unauthorized relaunch attempts rejected | Unverified |
| E8 | Unsupported deletion cannot falsely complete | Missing/ambiguous evidence and escaped descendants retain the fence and recovery state | Unverified |
| E9 | Cleanup removes only proved duplication/unreachable scope | Independent safety review; native/Subscriptions/protected hashes unchanged | Unverified |
| E10 | Final net diff, complete checks and platform claims are accurate | Independent final review; no compile-only native claim, no hidden failure/skip | Unverified |

## Rollback and stop conditions

- Before P3, return only the extraction-owned files to their recorded baseline if cutover fails. Preserve all unrelated work. Do not use a destructive reset or overwrite current credentials/data.
- After P3, revert the focused extraction commit through normal history or restore the exact archived tree and prior pin in a reviewed follow-up. Packaging-only rollback must not alter the data schema or invent an account binding.
- If an upstream API assumption changes, patch fidelity fails, or the fork is unavailable, retain the existing engine and stop cutover. Do not silently choose stock upstream.
- If deletion safety cannot be proven, retain the operation/fence and refuse completion. If switching safety is unclear, refuse that switch too. Rollback must not revive revoked credentials or remove tombstones.
- Native runner availability and approved dependency publication are external prerequisites. Their absence blocks those gates, not documentation or unrelated safe verification.

## Planning self-review checklist

Check that the plan moves tests rather than discards coverage, updates the actual license/CI coupling, distinguishes diff size from total code, accounts for expected binary build-info changes, retains all three known patches, keeps the live drain failure open, and makes containment deferral a visible capability limit. Confirm no new dependency repository or product file was changed during planning.

Self-review correction: require the packaged binary's actual module replacement identity, not just its unchanged upstream version label. Clarified that the dependency remains embedded in the runner and adds no runtime service or download. Complete patch inventory, fresh-cache verification, rollout/rollback and safety failure gates remain prerequisites rather than claims of work already performed.
