# PC4 F1 correction for reviewer82

Status: final verification is green and the source is frozen for independent re-review. Independent CLEAR is still required. Work is local only.

Artifact root: `/tmp/pr-5769-pc4-f1-79.sHId1v`.
Review input: `/tmp/pr-5769-pc4-review-82.txt`, SHA256 `1129ebe92eded0fdb5db4f160afd5a631b824b7b78ea56813e565a8ea569f534`.

## Scope and audit

Only these two production/test files change from the original PC4 seal:

- `frontend/src/renderer/hooks/useAccountsManagerQuery.ts`
- `frontend/src/renderer/hooks/useAccountsManagerQuery.test.ts`

This handoff and `PC4-F1-GATES.md` are separate new documentation. The prior four PC4 documents remain unchanged.

The initial audit verified every one of the 26 sealed PC4 files and the 5,560-entry live inventory. No unintended live-source edit existed to revert. The older desktop snapshot differed only in `PC4-GATES.md`, `PC4-REMOVAL-REVIEW.md`, and the subsequently added original handoff, all matching the later accepted seal. Reverting them would discard authorized work. Interrupted localization-run artifacts remain outside the repository at `/tmp/pr-5769-pc4-i18n-79.bJEHhx`; no locale file changed in F1. See `baseline-audit.json` and `baseline-source.sha256`.

## Correction and self-review

Each inventory GET records its query identity and data-update counter before issuing the request. Its response carries private weak-map provenance. Structural sharing checks that provenance at cache commit, including updates between asynchronous continuations. If any cache write has intervened, the read preserves the current snapshot. Focus and reconnect reads now use the same query options and fence as mount reads. Event cleanup rejects a late callback.

Existing event and mutation-receipt writes continue through the shared cache. Applied same-object writes also advance the checked counter. Response objects are distinct per request, so separate clients cannot consume each other's provenance. No metadata enters the DTO or diagnostics, and no backend revision format changes. A subsequent authoritative read can still replace an unsafe rounded-equal revision.

`MIDPOINT.md` records the self-review. The checks specifically reject three insufficient approaches: numeric ordering alone, comparing only the cached object, and checking only before the query function returns. The correction does not rewrite general mutation ordering or change service availability.

## Failed-first evidence

| Log under the artifact root | Observed result |
| --- | --- |
| `red-int64.log` | Exit 1. Both exact large-int64 reviewer schedules fail, five existing controls pass. |
| `red-causality-matrix.log` | Exit 1. Both exact schedules and all 12 event/mutation plus mount/focus/reconnect schedules fail, five controls pass. |
| `green-initial.log` | Exit 0. Nineteen tests pass after the production correction. |
| `midpoint-adversarial.log`, `midpoint-final.log` | Exit 0. Twenty-two, then 23 tests pass with commit-time, same-object, client-isolation and cleanup controls. |
| `final-negative-control.log` | Exit 1, expected. Final tests with the sealed pre-fix hook substituted in memory: two required failures, two controls pass, 19 intentionally unselected. |

The exact reproductions use GET `1789925704937855000` versus event `1789925704937856000`, and GET literal `1789925704937854010` versus event literal `1789925704937854011`. The second pair rounds to the same Number. Both tests assert the removed account stays absent. The expanded matrix also asserts disabled/status/routing state and accepts the next rounded-equal refresh. It uses deferred promises and a controlled reconnect timer, not timing sleeps.

The expanded fixture was tightened to wait for observer notification before checking a subsequent refresh; no product invariant was weakened. The final exact regression is re-proven against the original hook by `negative-control.mjs`, with its original hash checked in `baseline/`.

## Verification

Fish launches the recorded commands. Test subprocesses omit inherited service credentials. Node 22.22.0 and Vitest 4.1.8 are the observed local versions.

| Command or check | Result | Log |
| --- | --- | --- |
| Six-file focused matrix, three serial runs | Each exit 0, 129 tests pass | `final-focused-1.log` through `final-focused-3.log` |
| SessionView entry selection | Exit 0, three pass; 144 deliberately unselected | `final-entry.log` |
| `npm run typecheck` | Exit 0 | `final-typecheck.log` |
| `npm run typecheck:e2e` | Exit 0; typecheck only | `final-typecheck-e2e.log` |
| `npm test -- --maxWorkers=1` in isolated checkout | Exit 0; 340 files, 5,413 pass, 7 existing skips; 509.97s reported by the test runner | `final-frontend-tests.log` |
| `vite build --config vite.renderer.config.ts --outDir /tmp/pr-5769-pc4-f1-79.sHId1v/renderer-build` | Exit 0; existing large-chunk warnings retained | `final-renderer-build.log` |
| `git diff --check`; exact correction reverse-apply check | Both exit 0 | `final-diff-check.log`, `correction-patch-check.log` |

The focused command selects `useAccountsManagerQuery.test.ts`, `AccountsManagerSection.test.tsx`, `SessionAccountControl.test.tsx`, `AccountRemovalControl.test.tsx`, `accounts-manager-controls.test.ts`, and `api-client.test.ts`, with `--maxWorkers=1`. The entry selection uses `opens managed account controls explicitly|does not expose local account controls`.

`verify-frontend.mjs` records exact command arrays, working directories, start/end times, exit statuses and log hashes in `verification-results.json`, and verifies the frozen source before and after each check. The isolated checkout has its own installed dependencies. The task-owned zip binary and Node-compatible SQLite dependency are used for the full suite. No failing test file is excluded.

The final full-suite log SHA256 is `b382a979e89b1c17e8bde7f76d88fcb2671ac9ab085d466ab3a59a0838ebc8d9`. `final-verification.json` independently validates the archived source against the live files and command receipts; its SHA256 is `47e1384942d5526f88b8e78ee284c4edf964005ad868b2dbd606101d60d5d5fb`. No production/test source edit followed the start of final verification.

No frontend lint command exists. No backend source or contract changed, so backend build/vet/race and API regeneration are not rerun for this bounded correction. Generated OpenAPI and TypeScript bytes are checked against their reviewed hashes. The build here is the production renderer build, not another full Electron packaging or desktop run.

## Exact source seals

| Artifact under the root | Files | SHA256 |
| --- | --- | --- |
| `pc4-f1-source.sha256` | 22 integrated PC4 source files | `1f4e645292c8d8bf0fc0d3b4169d33a9abcd83ae6c7004902050f32fc28adf5a` |
| `pc4-f1-source.tar.gz` | 22 | `368f7be3f9028540e8b14a66d5fb4d7a5af198e1a60ac459d178b1a810eb1d2b` |
| `pc4-f1-correction.sha256` | 2 F1 files | `ba4a9f442787b385d90abb6447ba34174a1cb11c3622a94e2d083304a7e25fbf` |
| `pc4-f1-correction.tar.gz` | 2 | `b4da02477d567cb952e55c725910a8e9fc62dab0544bb7ecf896a75f87a2d29b` |
| `pc4-f1-only.patch` | 2 | `3f7c0479dd1d3758c91bfb604f7458ddd24e415bc8c50c0cd6100182080fb7a1` |

The hook hash is `359b8708a8f252ed6d642889a4f4733d4ff1522e5d53f193b51197ea1ee878a2`; the test hash is `d595105e9ed9626f4bb32b67ad218e5c05074ce5a273b275a02469be71efe80e`. `source-seal.json` enumerates all 22 files. The original PC4 archives are preserved, not overwritten.

## Preservation and remaining holds

Final preservation checks pass. `preservation-final.json` verifies corrected PC3 source/correction archives, PC2's 13 files, lifecycle/design's 113 files, the 91 protected paths and the five-file guest design. Its SHA256 is `edc440a05863d14d391978a0ffbf4835688599c6500a77a3cebcf80747da84ed`. `preserved-pc3-slice.log` and `preserved-pc3-evidence.log` also verify that prior handoff/evidence seal.

`scope-final.json`, SHA256 `be6542b6ff83e4c2ecbc62d2140ac47c1c2bd545ec8d9d6f975cbd43caa6d2c3`, verifies the original PC4 archives and all 177 original evidence files. Twenty-four of the original 26 live source/doc files remain unchanged; the two expected F1 files are sealed separately. The isolated source audit checks 5,558 regular source files against the recorded snapshot plus exactly those two F1 files. OpenAPI and generated TypeScript remain byte-identical to their reviewed hashes. No API generation was needed or claimed.

`review-freeze.json` records the final four-file correction-plus-documentation slice and evidence archive paths/hashes. Its manifests are additional seals; nothing under the original PC4 artifact root is overwritten. `PC4-F1-GATES.md` has five met gates, zero unmet and zero abandoned for this correction and review preparation. The independent verdict is a separate, still-open gate.

No new authenticated provider, successful coordinated switch/removal, desktop restart, native Windows or macOS guest result is claimed. The optional production control service still returns 501. The unresolved root HTTP shutdown race, native lifecycle/guest, pinned Node 24 CI, performance and release gates remain open. Existing desktop screenshots/recording are preserved as prior evidence, not presented as new F1 execution.

Independent review request: reviewer82 should confirm both failed-first schedules, the later rounded-equal acceptance control, all shared-cache writer interleavings, commit-time and cleanup negative controls, and the exact two-file delta. Do not mark bounded PC4 CLEAR or proceed to another implementation slice until that review returns CLEAR. No commit, push, PR edit or publication.
