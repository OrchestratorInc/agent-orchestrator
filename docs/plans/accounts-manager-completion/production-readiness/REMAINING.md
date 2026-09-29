# Accounts Manager: remaining work

Updated: 2026-09-30. Baseline: `d23bdaee42640da7d7fe70fd0b9af52843a3ca61` plus the separately sealed shutdown and retry-control corrections. This is the current release checklist. Historical plans and evidence remain intact.

## Implemented, with bounded verification

- Encrypted managed credentials, explicit session bindings, public account controls, removal journals and admission fences exist. Session account choice never authorizes automatic fallback.
- The latest shutdown correction preserves pre-stop switch intent. Its focused race and direct/fallback process checks passed. Independent review of that exact three-file correction is pending.
- The latest retry-control correction exposes a server-derived availability flag through HTTP, CLI and desktop controls. Focused races, affected package reruns, the complete frontend suite, typechecks and packaging passed on its sealed snapshot. Independent review is pending.
- The isolated desktop demonstrated restart persistence, recovered Retry visibility, pre-stop cancellation and explicit Chat resume. Live target Retry execution, cold runner/vault recovery and destructive live removal were not established by those checks.

Exact evidence and retained intermittent failures: [shutdown review](SHUTDOWN-REVIEW.md) and [retry-control review](RETRY-CONTROLS-REVIEW.md). These results predate the cleanup below and cannot certify a later source snapshot.

## Required before production

| Area | Missing result | Completion evidence |
| --- | --- | --- |
| Exact retirement and safe removal | Wire reviewed Linux creation-time containment into managed launches; close the five positive retirement assertions across three recovery groups; finish combined multi-session removal, revocation and cold recovery | Production-path crash, escaped-descendant, replacement-owner, cancellation and no-fallback tests; independent safety review |
| Switching and isolation | Repair eligible historical failed journals; reconcile model/effort/permission and queues through cold runner/vault restart; finish the remaining managed Chat adapter, isolated native profiles and explicit token migration/reconnect | Overlapping A/B requests with distinct credentials; preserved queues and settings; wrong-identity and stale-generation rejection |
| Credential and usage truthfulness | Complete revalidation/status recovery and final setup-token eligibility checks without inventing token balances or clearing capacity backoff blindly | Positive and negative provider responses; stale-response controls; fresh desktop observations |
| Native platforms | Real Windows execution; exact Mac guest retirement and desktop compatibility on arm64 and x64 | Native kernel and packaged application runs. Compilation and compatibility-runtime results do not close these gates |
| Integrated release candidate | Reconcile the current base, resolve repeatable test/lint failures, run complete required suites and actual account workflows, measure responsiveness, perform final independent bug review | One immutable candidate with commands, failures, skips and external gaps recorded; publication only after explicit approval |

Detailed acceptance contracts remain in [SPEC.md](SPEC.md) and [PLAN.md](PLAN.md). Cleanup must not silently drop any requirement above.

## Cleanup contract

Remove only demonstrably unused feature additions: standalone upstream examples, release/deployment tooling and packages outside the runner's production/test dependency closure on supported platforms. Preserve licenses, embedded data, API and SQL generated contracts, relevant regression tests and every protected native/Subscriptions path.

Do not delete safety guards, pending platform mechanisms or recovery tests to reduce the line count. Do not prune imported provider implementations solely because the current configuration disables them; a shared SDK may still compile or register them. Keep the two current source seals and five guest-design records unchanged during this pass.

Cleanup decisions, measurements and final checks are recorded in [CLEANUP-GATES.md](CLEANUP-GATES.md). A smaller diff alone is not production clearance.

## Current cleanup checkpoint

- Removed 222 standalone-only upstream files containing 31,612 lines. Their exact recoverable archive is `/var/tmp/pr-5769-cleanup-79.Eur8Lb/removed-source.tar.gz`.
- The runner's 110 upstream package records are unchanged across all five build targets. Runtime source, relevant regressions, local callback/streaming patches and licenses are retained. Dependency tidying removes 42 obsolete engine requirements without changing an existing directly required version.
- Both modules pass build/vet and tidy drift checks. Runner cross-builds pass for all five targets, and the normal desktop runner packaging script retains the exact license and provenance files. A Linux amd64 build with CGO disabled, trimmed paths and VCS metadata omitted is byte-identical before and after cleanup.
- Added a dedicated nested-module verification workflow. It is local and has not run on a hosted runner. Cross-build jobs do not constitute native-platform acceptance.
- The complete runner ordinary and race suites each pass 297 leaf cases. The retained engine ordinary suite passes 9,952 leaves with eight existing skips; its race suite passes 9,947 leaves, skips 11 existing cases and fails one media-channel case. The identical media case fails three times on an untouched HEAD archive whose files match the pre-cleanup manifest. That failure remains a release-verification blocker, not an established cleanup regression or a passing check.
- Backend build/vet, repository-configured full runner lint and workflow lint pass. The first lint invocation used default rules instead of the repository configuration; its 17 test-helper findings are preserved as a command-configuration diagnostic. No source suppression or test assertion was changed.
- The remote PR remains draft and reports conflicts against main, with no reported checks at the time of inspection. No commit, push or PR update has occurred during cleanup.

Current main tip inspected: `39305c3f30b730ba26664134080140a853b24ad7`. A non-mutating merge diagnostic reports conflicts in `backend/internal/storage/sqlite/migrate_burned_versions_test.go`, `backend/internal/storage/sqlite/migrate_fx_test.go` and `frontend/src/renderer/components/TaskComposer.tsx`. The earlier clean diagnostic used the branch's ancestry base, not this current main tip. No merge, rebase or conflict-resolution edit was performed during the cleanup seal.

The exact cleanup source manifest is `/var/tmp/pr-5769-cleanup-79.Eur8Lb/cleanup-final/source.sha256`, SHA256 `cc504bbb6980bfe17e677d9b3d71aa7c04c3bd9874618c91fdcd782322f6264a`. [CLEANUP-REVIEW.md](CLEANUP-REVIEW.md) records its separate deletion manifest, verification logs and bounded review request. All 91 protected paths, five guest-design files and both pending review source seals are unchanged. This is a cleanup candidate for review, not production clearance.
