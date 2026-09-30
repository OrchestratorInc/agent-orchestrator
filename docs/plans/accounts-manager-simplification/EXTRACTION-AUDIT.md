# Local engine extraction audit

Date: 2026-09-30. This is the P0/P1 candidate, not a completed dependency cutover.

## Exact source

- Consumer baseline: `43578e5cba1565bf08f1332656b2e0873bc1e125`.
- Upstream `v7.3.8`: `c93978c4ea2e908255a2a06c37599fda3651554a`, verified through the public tag and a fresh checkout.
- Local candidate: `f8e08347b8f7667bfaf26de2e59081dc346fc644`, branch `ao/agent-orchestrator-79/engine`.
- Candidate location and evidence root: `/var/tmp/pr-5769-extraction-79.4UyUrH`.
- Original retained-tree archive: `engine-source.tar`, SHA256 `d9a6ad163fe0bbcca5bd5971d54124ea3f41f4919b7ada5c90fa3572f42b0a83`.

The complete upstream comparison found 1,351 unchanged files, eight modified files, three added files and 222 omitted files. The modifications are contributor guidance, both module files, the callback interface/two entry points, the stream translator and the registration regression. Additions are the callback receiver/tests and delimiter regression. All 222 omissions match the already documented standalone distribution exclusions; no new exclusion was introduced in extraction.

The external candidate contains 1,364 tracked files. Its only differences from the 1,362 retained files are corrected contributor-document references, `DISTRIBUTION.md` and `.github/workflows/embedded-verification.yml`. Every other retained file hash matches, including runtime code, tests, fixtures, license and catalogs. The candidate commit contains only upstream lineage and the reviewed engine delta, not consumer repository history or local account data.

## Dependency and asset baseline

The runner's production/test package listing was recomputed with `GOWORK=off`, `CGO_ENABLED=0`, `GOMAXPROCS=2` and `-mod=readonly`. Each target has 110 engine package records; all selected source, assembly, test and embedded-file hashes are recorded in the target JSON manifest.

| Target | Total package records | Engine package records |
| --- | ---: | ---: |
| Linux amd64 | 575 | 110 |
| Linux arm64 | 573 | 110 |
| Windows amd64 | 577 | 110 |
| Mac amd64 | 575 | 110 |
| Mac arm64 | 573 | 110 |

These are dependency listings, not native execution results. All retained assets also have whole-tree hashes. The ambient workspace omits this nested module; explicitly disabling workspace mode is necessary for reproducible runner commands.

## Failed-first boundaries

| Control | Observed outcome |
| --- | --- |
| Exact candidate callback, delimiter and registration checks, race count 3 | Passed in all three packages |
| Restore upstream's original callback interface in a disposable fixture | Expected compile failure for the missing listener/authorization delivery fields |
| Restore upstream's original translator in a disposable fixture | Expected assertion failures for both named/data-only delimiters and per-response state |
| Remove the license from a disposable candidate | Expected integrity-check failure |
| Remove the embedded model catalog from a disposable candidate | Expected compiler failure for the missing embedded asset |
| Run the current desktop builder with a fake Go tool and a module-cache license, without the embedded sibling tree | Expected assertion failure: license copy still reads the missing embedded path |

The last control isolates the packaging path assumption; it is not proof of a real module fetch or runner build. No product guard was changed to satisfy it.

## Final candidate checks

All commands run with `GOWORK=off` and bounded parallelism. Local toolchain: Go 1.27.1 on Linux amd64. The declared minimum/CI toolchain is not independently certified by these local checks.

| Check | Result |
| --- | --- |
| Complete ordinary engine suite | Pass: 9,952 leaf tests passed, eight skipped |
| Complete engine race suite | Fail: 9,947 leaf tests passed, 11 skipped, one inherited media-channel failure |
| Focused callback, delimiter and registration races, count 3 | Pass |
| Build, vet, module tidy drift | Pass |
| Linux amd64/arm64, Windows amd64, Mac amd64/arm64 cross-builds | All five pass; compile-only |
| Dependency verification workflow lint | Pass |
| Candidate archive inventory, every source hash and retained file modes | Pass |
| Original consumer source/inventory and prior preservation manifests | Pass: 5,590 entries, including all 91 protected paths |

The race failure is `TestPionMediaRelayBridgesAudioAndDataChannel`: `media_test.go:268: upstream DataChannel was not created`. It matches the retained untouched-baseline failure in `/var/tmp/pr-5769-cleanup-79.Eur8Lb/media-head-control.log`. It is not a pass or an extraction fix. The dependency workflow retains the full race command and no test was skipped to hide this failure. Exact commands, exit statuses and logs are indexed in [REVIEW-EXTRACTION-P1.md](REVIEW-EXTRACTION-P1.md).

## Preservation and review

No consumer production source, runner pin, credentials or native/platform behavior has changed. The embedded directory remains present. The pre-existing compact UI changes and all original source snapshots are preserved separately. The candidate archive was extracted into a new disposable directory and every member was compared to the clean local commit.

A read-only midpoint review was requested through the project orchestrator. Publishing the candidate requires explicit user approval for the destination and exact commit; no fork was created and no push occurred. The implementation ledger remains open until the external dependency is reviewed, fetchable and used by a verified cutover.

Local diagnostic corrections: the first retained-manifest check ran from the archive's parent directory and could not resolve any entries; rerunning from the candidate directory verified all 1,362 files. The initial whole-repository hashing helper in the preceding planning task encountered a gitlink directory and was corrected to record its type without traversing it. The first final-preservation helper incorrectly joined an absolute evidence path to the workspace; using absolute-path resolution fixed that helper, and the complete inventory plus seven prior manifests passed. These diagnostic failures remain recorded. They did not change product source or mask a test failure.
