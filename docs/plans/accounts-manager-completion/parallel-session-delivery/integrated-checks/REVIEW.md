# Integrated local verification checkpoint

Verdict: HOLD. Local implementation and verification have progressed; the full product is not complete. Preserve the strict retirement guard and all retained positive recovery expectations.

Evidence root: `/var/tmp/pr-5769-integrated-79.6t2fcN`. Runtime source matches the browser-recovery seal. The only subsequent source correction is the two-file [registration fixture package](../engine-fixture/REVIEW.md), which changes a test and its upstream-delta note. The new integrated manifest has 78 entries and SHA256 `c290321c49cf56234c6edce50815e7118a78975383bfa6d0cce3308adb016b32`.

## Completed checks

All Go test commands use uncached execution, serial package scheduling, Go 1.27.1 and stripped credential environments. JSON logs and derived summaries retain failures and skips. A package with no test files is not counted as a passing test case.

| Scope | Observed result |
| --- | --- |
| Backend build/vet/full pinned lint | Pass; lint reports zero findings |
| Complete backend ordinary suite | 10,882 passing cases, 10 failed cases, 75 declared skips |
| Complete backend race suite | 10,887 passing cases, five failed cases, 75 declared skips |
| Unmodified persistent-host package, three repeats | 345 passing cases, 15 retained retirement failures, no skips |
| Actual direct/fallback controller process matrix, race count three | 105 scenario executions pass, no skips |
| Complete tagged installed-client adapter race suite | 125 Go cases pass; five embedded process scenarios pass; two existing Windows-only skips |
| Linux tagged CLI end-to-end | 730 passing cases, one existing Mac-only skip |
| Runner build/vet/full pinned lint | Pass; zero lint findings |
| Complete runner ordinary and race, after final correction | 297 passing cases in each command, zero failed/skipped cases |
| Complete engine build/vet/ordinary, after final correction | Pass; ordinary suite has 10,043 passing cases and eight declared skips |
| Complete engine race, after final correction | 10,038 passing cases, one media-relay failure and 11 declared skips |
| Complete cloud build/vet/race | Pass; 309 cases, one explicit live-service skip |
| API specification parity, API generation and SQL generation | Pass; 33 generated entries retain their bytes |
| Formatting and tagged session-manager vet | Pass |
| Fresh-install Docker image and no-network run | Pass; original repository Dockerfile, source-only context, session-labelled containers |

The earlier browser-recovery seal contains the complete Node 24 frontend suite: 5,626 passing cases and six existing mobile skips, both typechecks, renderer/product/cloud-client suites, documentation build and Linux app packaging. Its 12 source files are unchanged. A fresh Node 22.23.2 dependency installation passes both typechecks and the complete 352-file frontend suite with the same 5,626 passes and six skips. Exact commands and exits are in `verify-node22.fish` and `node22-status.txt`.

The initial Node 22 make fails because required Debian/RPM packagers are absent. The unchanged command then succeeds with those tools supplied through session-labelled, non-root, no-network containers confined to the isolated checkout and staging directory. No host-wide package or product-source change was made. The original failure remains in `node22-make.log`; the successful command is `node22-make-tools.log` with `packaging-status.txt`. Tool image: `sha256:a276445283f24fb8e744418cbcbb8de9d1e2dc51637b637a2057623072de576c`, including dpkg 1.21.23 and RPM 4.18.0. This is not the release runner's Ubuntu baseline or a signed release.

`package-artifacts.json` records the resulting artifacts:

| Artifact | Bytes | SHA256 |
| --- | --- | --- |
| AppImage | 217,096,579 | `4d26915a7f3e6ce30ec1a0d7dfd0a00648d13de7a71204e77288ee9a94597831` |
| Debian amd64 | 168,009,416 | `a6187f5e129fd16120c1e23a9250d0e072ae5b530c9d240a9bcfdabca1a80a56` |
| RPM x86_64 | 164,517,657 | `f1f802bb7bd9a38d225d585cc8d122eeb2a8fd1c78f72d2957b568aabaca05f6` |

Metadata inspection confirms the expected version/architecture and current-user ownership. The first inspection helper lacked a writable container temporary directory; that helper-only error is preserved, and the corrected read-only-artifact check passes. Installer builds and metadata checks do not establish native installation/update behavior.

The CI-pinned secret scanner reports no leaks in 2,059 current changed files under the unchanged repository rules (`secret-preflight.Gt0LCa/secret-scan.log`). Preparation initially rejected two committed environment examples. Both are included only after proving they are tracked and byte-identical to HEAD; actual private environment files remain rejected. This is a working-source scan against the cached merge base, not a history scan. A separate final scan covers this updated documentation snapshot and is recorded in the final freeze.

## Failure classification

The five retained retirement cases across three test groups still fail under the safe guard. The ordinary backend run also hit six descriptor-publication timeouts; the same unmodified package passes those six cases on three subsequent repeats. Their timing cause remains unproven. No timeout was widened and no assertion was skipped. The inherited HTTP shutdown test passes the current broad runs; its earlier diagnosis remains documentation-only.

The engine registration fixture assumed that the second concurrent worker was Account B. The unchanged HEAD control and assertion-only diagnostic reproduced the wrong assumption. The corrected fixture passes its focused matrix and complete package races, and deliberately suppressing per-task completion makes both enqueue orders fail. The unrelated media-relay race failure reproduces three times on unchanged HEAD. Its source and deadline remain untouched. No data-race warning was reported in these runs.

The JSON summary utility initially confused flat slash-named tests with actual nested subtests. Deterministic sequential/parallel controls corrected that bookkeeping. The raw logs and exit codes never changed. Counts here use the corrected utility; original summaries and failed controls remain preserved.

## Evidence limits and next dependencies

- Controller and installed-process A/B checks use synthetic upstream identities. They establish routing, isolation and local recovery behavior, not live-provider account or quota support.
- Real isolated Electron captures demonstrate negative/manual sign-in recovery, pending-operation persistence and empty inventory across restart. They do not establish completed provider authentication or positive A/B execution. The UI files and inspected captures remain in the earlier immutable seal.
- Linux containment primitives are implemented and tested but not wired into production. Their unchanged candidate awaits independent review. Exact retirement, managed Chat and isolated profile adoption remain dependent on that review and remaining integration.
- Native Windows execution and Mac arm64/x64 containment/guest proof require unavailable native hosts. Compilation and compatibility-runtime diagnostics cannot replace them.
- Authorized distinct live accounts, positive quota, full desktop responsiveness measurements and final integrated independent review remain open.
- The fresh-install container has networking disabled. It proves safe download failure and CLI behavior, not a successful release download or a real remote 404 response.

No commit, push, PR edit, review publication or release deployment was performed. The exact final source/documentation, preservation and evidence manifests are recorded in `/var/tmp/pr-5769-integrated-79.6t2fcN/final/FREEZE.md`. Request independent review of that package and the unchanged containment candidate before dependent runtime adoption. No new verdict has been received.
