# Review request: explicit recovered-switch retry controls

Date: 2026-09-30. Baseline: `d23bdaee42640da7d7fe70fd0b9af52843a3ca61`. This is a bounded M03 observation/control slice, not release clearance. Keep the original shutdown review request and archive intact. Neither slice has an independent verdict yet.

## Exact source

Root: `/var/tmp/pr-5769-retry-controls-79.fGWJZO/retry-final`.

| Artifact | Count | SHA256 |
| --- | --- | --- |
| source.sha256 | 16 files | `7a83c56699cd5c0024e266e18391a747aeefa3e902d193afbbd3b982ae1e7a42` |
| source.tar.gz | same 16 files | `9694114ac25b396d73e0987635382557e45345476b70a43dd57090e0c942126b` |
| generated.sha256 | 2 files, included above | `68f22515838f73329569cc60634ce2b90404ec46ea3e931a231d3051a16e74f3` |
| protected.sha256 | 91 unchanged files | `3438055a85087fe789dfb0aa5f2dc583ab52dac8a51bb683e0100647b096396f` |
| guest.sha256 | 5 unchanged files | `bcb421310148ec0976f2fb406b82de8e93e2d61003cc1c1690852a8adfca2d3a` |
| shutdown.sha256 | 3 separately reviewed files, unchanged | `911e15c0850d1c31d6680cea396a29ef5c48c28cc04b17c5d233873bc271d8c5` |

`source-files.txt` is the exact inventory. The archive was read back member by member and compared to both the manifest and live files. New source is limited to the optional manager reader, session-service delegation, safe HTTP DTO/projection, mirrored CLI output, renderer guards, tests and generated contracts. No lifecycle, vault, runtime, native switching or Subscriptions implementation changed in this slice.

## Contract and requested review

Review this immutable archive read-only. Return CLEAR or bounded findings against its exact manifest. Review the original three-file shutdown correction separately at `/var/tmp/pr-5769-production-79.Lvj7m9/shutdown-final`; this archive neither replaces nor expands its approval.

`canRetry` is an observation, not durable state or authorization. It requires the recorded operation's idle, non-cancelled local reservation, a live manager lifetime and a known recoverable phase. Startup must reconstruct the reservation first. Unsupported services report false. Every actual retry still executes the existing durable and local admission checks.

Check cancellation/shutdown interleavings, stale operation observations, optional service construction, public identity validation, old-daemon compatibility and the separation of committed account state from pending operations. A stale true observation may lead to an ordinary conflict; it must never bypass admission. Unknown or missing fields cannot authorize a renderer action. No historical terminal journal is reopened.

Midpoint self-review and the current gate states are in RECOVERY-CONTROLS-GATES.md. Live Retry execution and the wider recovery matrix are not certified by the UI observation test.

## Verification

Logs are under `/var/tmp/pr-5769-retry-controls-79.fGWJZO`. Commands ran from this worktree through `/usr/bin/fish --no-config`, with credential-stripped environments, Go 1.27.1, Node 22.23.2 for frontend suites and the existing local zip fixture tool. Go checks use `GOMAXPROCS=2`, `-p 1`, the existing session-owned temporary directory and build cache.

- Failed first: red.log has 26 manager/HTTP assertion failures; ui-red.log has four expected renderer failures. cli-red-final-fixture.log has exactly four missing-output assertions against the original CLI implementation. Earlier CLI logs rejected routine telemetry and are diagnostic only.
- Focused race, repeated three times: `go -C backend test -p 1 -race -v -count=3 -timeout=6m ./internal/session_manager ./internal/service/session ./internal/httpd/controllers ./internal/cli -run '^(TestAccountsManagerSwitch(RetryCapability.*|Startup.*|CancelRetry.*)|TestAccountsManagerControlRetryCapability|TestAccountControlRetryCapability.*|TestSessionAccountRetryCapability)$'`. final-focused-corrected.log: 50 distinct leaf cases, 150 passes.
- Complete affected races: full-race.log records service/session, controllers and CLI passes. Its two manager failures are preserved. handoff-isolated.log has four cases repeated three times, all passing. `go -C backend test -p 1 -race -json -count=1 -timeout=15m ./internal/session_manager` then passes 1,109 leaves with two existing platform skips in manager-full-final.log, 212.751s. No source edits separate these results.
- Fifty focused renderer/client tests pass in final-ui-focused.log. `npm --prefix frontend test -- --maxWorkers=1` passes all 352 files, 5,647 tests and seven existing skips in frontend-full-final.log, 581.91s. Retain the preceding timeout and missing-fixture-tool logs. The unchanged diff-view file also passes its isolated 11-test run.
- `go -C backend build -p 1 ./...`, `go -C backend vet -p 1 ./...` and changed-scope golangci-lint v2.13.2 pass. Scope: session manager, session service, HTTP controllers, CLI and ports. API spec/parity tests pass in api-parity.log.
- Tagged session-manager vet also passes in tagged-vet.log. The acceptance-ledger recheck reruns its manager and service/controller oracles successfully in gate-reverify.log. Its overall exit 1 correctly reports the one unmet independent-review gate, not a test failure.
- `npm --prefix frontend run typecheck`, `typecheck:e2e`, `npm run api` and the generated hash comparison pass. `npm --prefix frontend run build` completes the Linux x64 desktop package. This is not Windows/Mac execution, signing or installer acceptance.
- `go -C backend test -p 1 -tags e2e -race -v -count=3 -timeout=6m ./internal/session_manager -run '^TestAccountsManagerSwitchShutdownRealProductionRecovery$'` passes all four direct/fallback retry/cancel cases three times in final-process.log, 92.805s. The fallback cases intentionally exercise the production wrapper's alternate runtime. These use synthetic test commands, not real provider authentication.

Closing audit: retry-final/final-audit.json records an exact 27-file dirty inventory, comprising this 16-file source slice, the three preserved shutdown files and eight readiness documents. No other source or generated changes appeared during packaging. All source, generated, protected, guest and shutdown manifest entries were rechecked successfully.

- retry-final/evidence.sha256: 112 retained artifacts, SHA256 `41a271bb96f8bd166bb1c4730d2ae685dcea09cbde23466df63e804050b61e7d`.
- retry-final/final-preservation.log: SHA256 `c05fb0d1cdbe055b2ae7da804f33a9f6e5b8c9c052475e7ca7214bdb57535941`.

The source archive is immutable. The two review/gate documents are sealed separately after this closing audit. Five of six bounded gates are met; independent review remains unmet. There are no abandoned gates.

## Actual desktop boundary

The isolated checkout is `/var/tmp/pr-5769-desktop-final-79.yrWkBr/checkout`, data under its lab `ao-home/desktop`, daemon on `127.0.0.1:43429`. The rebuilt daemon uses this worktree's source. The three changed renderer/contract files match the worktree; other lab files were preserved rather than overwritten. Capture/automation uses that real Electron window, not a browser reconstruction.

desktop-restart.json proves six idle bindings survive restart. desktop-recovery-cancel.json proves the active Chat waiting operation survives restart, Retry becomes visible after reopening the dialog, and cancellation preserves the source account, revision and five unrelated bindings. desktop-resume-verified.json additionally proves explicit Resume reaches a ready controller and the bounded expected reply appears with all turns completed. These do not prove exactly-once external execution or live target retry.

Inspected captures: desktop-recovered-retry.png, desktop-retry-cancelled.png. Recording: desktop-recovered-cancel.mp4, four seconds, 16 actual renderer frames, H.264, 1908 x 1038. The target selector is masked during capture. No capture has been uploaded or attached to the PR.

Terminal input attempts remained idle and created no switch operation. The first Chat harness attempt expected the dialog to remain open across daemon restart and timed out; the corrected continuation reopens it. Both diagnostic logs are retained. The provider controller requires explicit Resume after restart; the successful check is recorded without asserting automatic recovery.

## Remaining release gates

Independent review of both this slice and the shutdown correction is pending. The Linux creation-time containment contract and deletion recovery, historical failed journals, complete queue/settings and cold runner recovery, remaining managed-interface/profile/migration work, native Windows, both native Mac architectures and final integrated release verification remain open. Seven frontend and existing backend platform skips are not substituted for native acceptance. Retained intermittent suite failures still require release stability review.

This 16-file slice is excluded from the earlier nine-file publication proposal. No commit, push, PR edit or media publication has occurred. A publish needs approval of its exact announced scope.
