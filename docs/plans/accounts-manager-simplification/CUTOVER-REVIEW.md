# Published dependency cutover

This checkpoint supersedes the extraction documents' publication prerequisite.
The consumer now uses the published fork at
`f8e08347b8f7667bfaf26de2e59081dc346fc644`, without changing SDK imports or
runner behavior. It does not close the product release gates.

## Scope and preservation

- Replace `../engine` with the immutable public Go module and its two checksums.
- Resolve the license from verified module metadata. Verify the selected module,
  cached contents and built binary before packaging the notices and identity record.
- Keep dependency tests in CI through an exact external checkout, alongside runner
  tests, packaging boundaries and five cross-builds.
- Remove only the 1,362-file engine copy. Its 532,876 PR additions now live in the
  dependency, not in this PR. No patch, upstream test or runtime behavior was dropped.
- Preserve the compact UI as a separate commit, including the original native
  agent/model controls, explicit account/timing choices and pending/recovery state.

Evidence root: `/var/tmp/pr-5769-cutover-79.OVswfT`. Each command has a matching
`.command.json`, `.log` and `.exit.json`. Tests inherit no provider credential
environment. No new live provider request, account switch or deletion was performed.

The source freeze has ten files: workflow, upstream record, dependency identity,
runner module/checksums, Forge config/test, builder, dependency helper and Node tests.

- `code-seal/source.sha256`: `bcf42c5a40d5317025ecd459de9f3425bf45b6c663fcd7929ab40a3f8c375ea8`.
- `code-seal/source.tar.gz`: `136b03c475796888f91142f12f51f43b5af2ddf9f21978b6b814487689846287`.
- `code-seal/removed.sha256`: `6f8f38c63de6c13167b7d4d18b6fe67fb4f928cb587a50074020f1b2a5512d7d`.

The removed directory is recoverable at `embedded-engine` under the evidence root,
from Git history, or from the earlier `engine-source.tar` archive with SHA256
`d9a6ad163fe0bbcca5bd5971d54124ea3f41f4919b7ada5c90fa3572f42b0a83`.
Preservation checks verify 5,582 unchanged file/link entries, including the relocated
engine, plus all seven prior manifests: compact source/docs, 91 protected paths,
guest design, generated contracts, shutdown and browser-import files. Prior archives
and the standalone fork checkout remain unchanged.

## Verification

| Evidence label | Result |
| --- | --- |
| `packaging-red` | Failed first: missing sibling license prevents the original builder from packaging without an engine checkout |
| `packaging-final`, `forge-green` | Pass: 13 dependency boundary cases and 29 Forge cases, including missing notices |
| `public-fetch` | Pass: fresh-cache public fetch with exact module/revision/checksums, no repository credentials |
| `package-offline`, `binary-provenance`, `binary-no-toolchain` | Pass: network-disabled warm-cache build without the engine tree; minimum Go 1.26 build identity and no-Go execution |
| `dependencies-summary.json`, `cross-*` | Pass: all five source closures match, 110 engine packages each; five minimum-toolchain builds |
| `runner-tidy`, `runner-build`, `runner-vet`, `runner-test`, `runner-race` | Pass: full runner checks on its Go 1.26 minimum |
| `ci-runner-tidy`, `ci-runner-build`, `ci-runner-vet`, `ci-runner-test`, `ci-runner-race`, `ci-cross-*` | Pass: complete runner repeats and all five cross-builds on CI's Go 1.27.1 |
| `ci-fork-build`, `ci-fork-vet`, `ci-fork-service-control` | Pass: CI-toolchain build/vet and the full SDK service package under race, 30.558s |
| `fork-patches-race` | Pass: callback, streaming delimiter and registration-order regressions, three repeats under race |
| `backend-build`, `backend-vet`, `backend-focused-race`, `backend-lint` | Pass: six focused service/controller packages under race and zero lint issues |
| `frontend-focused`, `frontend-types-final`, `frontend-e2e-types`, `renderer-build` | Pass: 242 focused tests, both typechecks and renderer build |
| `api-generate`, `sql-generate`, `generated-drift`, `workflow-final` | Pass: regenerated API/SQL unchanged and workflow lint clean |
| `desktop-package-ci`, `packaged-binary-provenance`, `packaged-binary-smoke` | Pass: complete Linux Electron package on Node 24.21.0/Go 1.27.1; post-package checks and no-Go binary smoke |
| `desktop` | Pass: real isolated Electron, twelve matching UI/catalog files, three identities, twelve unchanged bindings, zero mutations/errors |
| `frontend-final` | Not passed: 5,663 passed, seven skipped, four unfinished; unchanged native browser-import cleanup aborts the worker |
| `fork-race` | Not passed: inherited media relay test cannot create its data channel; no assertion suppressed |
| `ci-fork-race` | Diagnostic only: media failure plus a stalled upstream SDK test; the owned test process was interrupted to preserve its stack |
| `backend-full-race` | Not passed: five retirement cases across three groups, two native-switch deadlines and the locally selected ten-minute SQLite timeout |
| `native-timeout-control` | Pass: both native-switch deadline tests, three focused repeats under race; this does not erase the full-suite failures |
| `backend-sqlite-ci-race` | Pass: full SQLite package with CI's twenty-minute deadline, Go 1.27.1 and four scheduler threads; command completed in 690.548s |
| `secret-scan` | Pass: pinned, offline repository-rule scan of the 39 present changed files, zero findings |
| `consumer-path-audit` | Pass: only the ten frozen integration files, archived engine removal and extraction documents are staged; no active build reference to the removed engine remains |

## Review findings and corrections

Go proxy responses can omit VCS metadata. Checksums, selected version and binary
provenance remain mandatory; an available full VCS hash must also match. The
regression covers both metadata forms and rejects corrupted or wrong identities.

`vitest-discovery-red` reproduced the Node/Vitest filename collision. The test now
uses the existing `.node-test.mjs` convention and runs explicitly in dependency CI.
The first full frontend run also exposed the old Forge fixture's missing identity
record. The fixture and three absent-notice checks now match the package contract.
That mixed-snapshot diagnostic is superseded by `frontend-final`, which has no
assertion failures but retains the native worker error. An unrelated chat rename
timeout in the diagnostic did not recur in the final run; no chat code was changed.

The first complete desktop build selected system Go 1.26 outside the backend
module and failed its existing minimum-version check. Selecting CI's Go 1.27.1
fixed that environment mismatch without changing the protected builder.

The full backend run used a ten-minute package deadline, shorter than the existing
CI workflow's twenty minutes. SQLite was still executing migrations at timeout;
the remaining backend packages completed. The complete SQLite rerun passes within
CI's existing budget without a source change. Its result is recorded separately
above; it does not turn the earlier full run into a pass. The two
native-switch deadline failures pass three focused race repeats and do not use
the fork dependency. Neither the native-switch source nor those tests changed.

The CI-toolchain dependency run stalled in
`TestAntigravityAsyncProbe_NormalRequestsDoNotDiscardProbe` at an unconditional
`<-probeStarted` wait (`sdk/cliproxy/antigravity_models_timeout_test.go:498`). The
exact owned test process received a diagnostic interrupt, not any shared service.
The log retains its goroutine dump and subsequent package results; it is not a
completed full-suite certification. `upstream-probe-source` confirms the test and
its model implementation are unchanged against the upstream parent. The complete
SDK service package passes independently with a bounded two-minute deadline.

Inspected [desktop captures](../../screenshots/pr-5769-compact/README.md) show the
real compact UI. They do not certify a live switch or cold restart with the new
binary. Account identity fields are masked; the prior failed operation is retained.

Independent read-only review was requested through the orchestrator with the exact
source seal. No returned verdict is claimed. Publication requires the separately
announced normal-push/PR-update approval; nothing in this checkpoint is a merge or
production sign-off.

Remaining release work includes the existing retirement/recovery failures,
terminal wait-for-turn behavior, isolation/cold-recovery gaps, native Windows and
both Mac architectures, integrated review and current-main conflicts. The fork's
remote timing-test failure also remains open. Safety guards and protected paths
were not weakened to pass those gates.
