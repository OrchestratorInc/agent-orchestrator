# P2 switching correction review request

Review the immutable source package, not the live tree. Initial-selection implementation may continue separately. This request does not clear deletion, containment, native platform, live-provider, or release gates.

Base commit: `1e243310fa29c3a4a71920df463180947cc8954c`.
Frozen source tree: `cfb2e45f0bc94d2afa0e6931cdccc01030599ef7`.
Freeze directory: `/var/tmp/pr-5769-next-79.wos4rh/switching-freeze`.

| Artifact | Count | SHA256 |
| --- | --- | --- |
| `switching-correction.sha256` | 4 files | `a570517996238ffb2a9c38cd6616bdd24d06394f746bfb09fadd57129e1cc0c8` |
| `integrated-source.sha256` | 5,678 files | `66acf2b4ca776d39a2264dee56513236f1841f7b3fa760e1256a272c0f95019a` |
| `protected.sha256` | 91 files | `3438055a85087fe789dfb0aa5f2dc583ab52dac8a51bb683e0100647b096396f` |
| `evidence.sha256` | 17 logs | `6279feb92ecf343ed8f8fa8d9739ddb9eb199c23dfcc086b86a9c53a4c967416` |
| `source.tar` | full tracked source | `86dbbd60ca6c69ba479f462a6324fc11f04441a6277d09a4ba7c00ff0e3797be` |
| `correction.tar` | four-file correction | `334b0691edb1faefa05e4ba8414f0fdccd2023d9d8e08270e494af120e2e87c8` |

The optional private submodule pointer is recorded in SUMMARY.json, not included or claimed tested. Updated gate/review documents are outside the archived code snapshot.

## Exact correction

- `backend/internal/daemon/accounts_manager_execution_test.go`: the public execution fixture now exposes the exact supervised-process proof required by the existing production guard; it also supports deliberately capability-limited wrappers.
- `backend/internal/daemon/accounts_manager_readiness_test.go`: public-service negative controls for absent capability, probe failure, replacement generation and absent target despite visible ready output. All retain the input fence without extra teardown, launch or revision changes.
- `backend/internal/session_manager/accounts_manager_switch.go`: cancellation retries only requested-to-waiting CAS conflict, reads the durable outcome after ambiguous errors, and performs matching-run cleanup when observing an already-committed cancellation. Stopping remains irreversible.
- `backend/internal/session_manager/accounts_manager_cancel_phase_test.go`: deterministic barrier tests for both handle forms, stop-wins and already-cancelled controls, plus committed-response and first-read loss. No signal, stop, launch or binding rotation is allowed when cancellation wins.

## Failed-first and self-review evidence

Logs are under `/var/tmp/pr-5769-next-79.wos4rh`. `published-blockers-red.log` preserves the original readiness failure. `cancel-phase-assertion-red.log` records the requested/waiting race and repeated-cancellation failures while stop-wins controls pass. `cancel-response-assertion-red.log` records ambiguous committed-response/read failures and retained fences. The earlier `cancel-phase-red.log` was a fixture compile error and is not counted as defect reproduction.

Midpoint self-review moved store decoration before startup reconciliation. Final lint found two test-only if/else chains; they were converted to tagged switches without changing assertions. Both affected full packages, repeated focused matrix, real-process suite and build/vet were rerun afterward. The dependent service/store/runtime-selection and HTTP/CLI package source and tests were unchanged, so their passing full/focused results remain applicable.

## Observed verification

Exact command arrays, exit codes and wall-clock durations are in `switch-final-checks.log` and `switch-post-lint-checks.log`. Use only the post-lint daemon/manager/process/build/vet/lint results for this source snapshot.

- Repeated focused race matrix: daemon 57.518s, manager 34.004s, all selected cases execute three times.
- Real production runtime, cold recovery, cancelled retry and handoff ownership: 72.695s under race, direct/fallback and replacement-preservation cases, no skips. Named scenarios are recorded in SUMMARY.json.
- Full daemon race: exit 0, 34.665s command wall time.
- Full manager race: exit 0, 199.864s package time.
- Full account/Chat/session/store/runtime-selection race packages: all exit 0; package times 15.741s, 238.110s, 17.770s, 166.267s and 1.010s respectively.
- Focused public HTTP/CLI account controls under race: both exit 0.
- Backend build, full vet and tagged manager vet: all exit 0 after the final test edit.
- Changed-scope lint, pinned version 2.13.2: zero issues after correction. This is not a complete branch lint pass.
- All 91 integrated protected hashes pass. Original protected manifest and P0 archive remain unchanged. API/SQL generated files have zero diff from the integration baseline; this slice changes no contract or generated source.

Tests use Go 1.27.1, stripped credential environments, test-owned runtime sockets and deterministic shell settings. Actual process tests use synthetic provider frames, not live credentials. No new desktop evidence is required for this backend/test-only slice, and no desktop workflow is claimed verified here.

## Requested independent decision

Check the bounded cancellation retry, ambiguous-result cleanup, cancellation versus durable stopping, stale retry admission and matching-run fence release. Confirm the readiness correction adds an accurate fixture capability without loosening production proof. Review the negative controls against the production construction, not only concrete runtime mocks.

Return CLEAR or bounded findings against the exact manifest. Coordinated deletion remains held on the separate escaped-descendant defect and containment proof. Initial selection, credential-kind/usage/Harness work, managed app-server integration, full lint, native Windows/Mac verification, real A/B provider sessions and final desktop evidence remain open. No push or PR metadata change accompanies this handoff.
