# Gates: feature-only cleanup

OWNS: .github/workflows/accounts-manager.yml, accounts-manager/UPSTREAM.md, accounts-manager/engine/examples/**, accounts-manager/engine/.github/**, accounts-manager/engine/cmd/**, accounts-manager/engine/internal/tui/**, accounts-manager/engine/internal/cmd/**, accounts-manager/engine/internal/store/**, accounts-manager/engine/internal/auth/empty/**, accounts-manager/engine/Dockerfile, accounts-manager/engine/docker-build.*, accounts-manager/engine/docker-compose*.yml, accounts-manager/engine/.dockerignore, accounts-manager/engine/AGENTS.md, accounts-manager/engine/go.mod, accounts-manager/engine/go.sum, accounts-manager/runner/go.mod, accounts-manager/runner/go.sum, docs/plans/accounts-manager-completion/production-readiness/REMAINING.md, docs/plans/accounts-manager-completion/production-readiness/CLEANUP-GATES.md, docs/plans/accounts-manager-completion/production-readiness/CLEANUP-REVIEW.md

Scope: remove unused imported scaffolding only after dependency and reference checks. The owned paths are candidates, not a deletion list. Keep all runtime dependencies, relevant tests, local upstream patches and immutable review/protection manifests. Work locally without commits or publication.

Additional owned file after midpoint inspection: `.github/workflows/accounts-manager.yml`. Existing workflows omit both nested Go modules. Add a read-only test/build workflow so future embedded-library changes receive their own checks. Do not alter existing workflows or claim cross-builds as native execution.

## Plan

1. Record the remaining release work and preserve the exact starting tree, review seals and deletion candidates outside the repository.
2. Compute the runner's production and test dependency closure on Linux, Windows and both Mac architectures. Inspect embeds, generation commands, build scripts and file-based test fixtures. Approve only explicit unused paths for deletion.
3. Remove the approved paths, update upstream import instructions and check dependency equivalence. Review the cleanup before running complete affected verification.
4. Run runner and retained-engine tests, build/vet, cross-compiles, packaging and source preservation checks. Preserve existing failures instead of weakening their assertions.
5. Freeze the cleanup delta with exact counts, exclusions, logs and a review handoff. Keep the product gate open for unmet implementation, native execution or independent review.

- [x] C1: current remaining work and the immutable starting source inventory are recorded.
  EVIDENCE: REMAINING.md records the five open release areas. /var/tmp/pr-5769-cleanup-79.Eur8Lb contains starting-head.txt, starting-status.txt, starting-dirty.sha256, starting-dirty.tar.gz and starting-engine.sha256. Both pending source seals, all 91 protected paths and five guest-design files verified unchanged before edits.

- [x] C2: every removed file is outside the supported runner dependency and fixture closure, with exact recoverable backup and reason.
  CHECK: node /var/tmp/pr-5769-cleanup-79.Eur8Lb/verify-cleanup.mjs
  EXPECT: cleanup preservation and dependency controls passed
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79; path=73b5dbbe765e/37 entries; output={"removed":222,"removedLines":31612,"retainedEngineFiles":1359,"preservedInitialFiles":27,"protectedCount":91,"guest":5,"retry":16,"shutdown":3,"dependencyTargets":5} | cleanup preservation and dependency controls passed

- [x] C3: runner dependency closure and embedded resources are preserved on all supported build targets.
  CHECK: node /var/tmp/pr-5769-cleanup-79.Eur8Lb/audit.mjs verify
  EXPECT: dependency equivalence passed
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79; path=73b5dbbe765e/37 entries; output=verify darwin-amd64: 110 upstream package records, 67511c7630166652e0593a3a88a9241869da6228784b9a58c047d157412e6dd5 | dependency equivalence passed; 222 removed files outside runner closure

- [ ] C4: complete affected tests and build/vet checks pass on the final cleanup snapshot, with skips and unrelated failures classified.
  EVIDENCE: partial. test-summary.json independently recounts runner ordinary/race at 297 leaf passes each with no skips; engine ordinary at 9,952 passes with eight existing skips; engine race at 9,947 passes, 11 existing skips and one media-channel timeout. media-head-control.log repeats the identical failure three times on untouched HEAD verified against starting-engine.sha256. The pre-cleanup ordinary engine suite passed 10,041 leaves with eight existing skips. The initial engine pass counts were each one too high; these final counts exclude every parent test. Build/vet pass for both modules and the backend; tidy drift, five cross-builds, desktop runner/license packaging and normalized Linux binary equivalence pass. Configured runner lint and workflow lint pass. runner-lint.log is retained as a wrong-configuration diagnostic; runner-lint-configured.log uses the existing backend configuration and reports zero issues. Keep the inherited race failure open.

- [x] C5: protected paths, guest design and both pending review source seals remain unchanged; the cleanup has an exact final manifest.
  CHECK: node /var/tmp/pr-5769-cleanup-79.Eur8Lb/verify-cleanup.mjs --final
  EXPECT: cleanup preservation and dependency controls passed
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79; path=73b5dbbe765e/37 entries; output={"removed":222,"removedLines":31612,"retainedEngineFiles":1359,"preservedInitialFiles":27,"protectedCount":91,"guest":5,"retry":16,"shutdown":3,"dependencyTargets":5} | cleanup preservation and dependency controls passed

- [ ] C6: independent review clears the cleanup and the remaining release gates are reported without claiming production completion.
  EVIDENCE: pending

## Dependency review

The runner's production/test package graph has 110 upstream records on each checked target: Linux amd64/arm64, Windows amd64 and Mac amd64/arm64. Complete pre-cleanup package/file/import records are in `before-*-deps.json` under the evidence root.

Approved candidates are the standalone server/catalog commands, their private command/UI/storage/empty-auth packages, examples, nested upstream automation and container tooling. `internal/homeplugins` is retained: the embedded SDK imports it. The public SDK pipeline, plugin API wrappers, translator-boundary tests and integration tests are retained even when absent from the runner's direct graph. Embedded catalogs, word lists, all testdata and `config.example.yaml` are retained; an executor regression reads the example configuration.

Original upstream reference documentation and its images are retained with an explicit embedded-only scope notice. Runtime source imported through shared SDK registration is not removed merely because a provider is currently disabled.

## Midpoint self-review

- Production source in every retained upstream package is unchanged. The 110-record graph matches before and after cleanup on all five targets. A package-prefix check initially confused a private child package with its imported parent; the removal guard was corrected to compare exact package directories before any deletion.
- Import closure alone cannot establish safe deletion of fixtures. Separate scans preserved embedded assets, the example configuration, SDK integration tests and source-scanning translator/util tests. The original complete engine suite passed before cleanup.
- The cleanup removes only standalone storage implementations. Product credential persistence, generation admission, route selection, deletion fences and lifecycle ownership are untouched. The independent-review seals and the full initial 27-file delta remain unchanged.
- Module tidying removes 42 engine requirements, adds two explicit test-only requirements already present in the upstream dependency graph, and changes no existing directly required version. The runner's go.mod is unchanged; checksum changes are retained for review.
- A read-only CI inventory exposed missing nested-module coverage. The new workflow tests both modules and separately cross-compiles five runner targets. Its parser/contract check fails before the file exists and passes afterward. Hosted execution remains unverified until publication is authorized.
- Full runner race passes. The first full retained-engine race run reproduces the previously recorded media-channel timeout. Preserve that failure and compare untouched source before attributing it to cleanup; do not delete, skip or lengthen the test.
- The untouched HEAD control reproduces the media timeout three times at the same assertion. This supports classifying it as inherited from the imported baseline; it does not establish a root cause or clear the release gate. The full ordinary engine baseline passed, so race-instrumented stability requires separate diagnosis.

## Closing self-review

- All 222 deletions match the preapproved recoverable inventory. The six retained cleanup files contain metadata, module sums and verification configuration; no retained application runtime source changed. The cleanup patch excludes the two pending behavior corrections and preserves their exact source hashes.
- The ordinary engine suite, complete runner ordinary/race suites and all cross-builds pass. Reproducible Linux builds have identical bytes. None of those results substitute for native execution, live accounts or the still-failing race-instrumented media test.
- The first runner lint command omitted the repository configuration. Its 17 reports are test-helper error checks excluded by the existing policy. The corrected command loads the unchanged configuration and passes; no suppression or source edit was introduced.
- The current main merge diagnostic has three conflicts. They are documented in REMAINING.md and intentionally excluded from this cleanup seal. No claim is made that the complete PR is ready to merge.
- C4 remains unmet on the inherited race failure; C6 remains unmet until independent cleanup review. Neither gate is abandoned. Final product review still depends on the separate implementation and platform gates.
