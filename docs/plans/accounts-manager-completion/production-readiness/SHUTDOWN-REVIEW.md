# Review: pre-stop switch shutdown correction

Status: source and verification frozen for bounded independent review. No publication. This is part of M03, not completion of M03 or the production-readiness plan. Five of six slice gates are met; independent review remains open.

## Exact source

Base: d23bdaee42640da7d7fe70fd0b9af52843a3ca61.

Root: /var/tmp/pr-5769-production-79.Lvj7m9.

Manifest: `switch-shutdown-source-candidate.sha256`, three files, SHA256 `911e15c0850d1c31d6680cea396a29ef5c48c28cc04b17c5d233873bc271d8c5`.

Archive: `switch-shutdown-source-candidate.tar.gz`, SHA256 `f20b65a692dcdf8db06c2e864cd39f54b182da4e3dc32ef512dfea892f081c56`.

- `backend/internal/session_manager/accounts_manager_switch.go`
- `backend/internal/session_manager/accounts_manager_shutdown_test.go`
- `backend/internal/session_manager/accounts_manager_shutdown_e2e_test.go`

The only production change preserves requested/waiting journals when the manager lifetime is cancelled. It leaves the durable phase, operation ID, account binding, generation and intake fences unchanged. Existing stopping/recovery and terminal cancellation paths remain in charge of their own outcomes.

## Evidence and boundaries

`switch-shutdown-red.log` preserves four failed assertions on the original implementation: both handle forms, each followed by a requested retry or cancellation, become failed/ADMISSION_CHANGED at manager shutdown. `switch-shutdown-first-green.log` records the first corrected pass. `switch-shutdown-matrix.log` records the expanded matrix repeated three times before final raw-fence assertion additions. `switch-shutdown-production.log` records four actual-process cases through the production direct/fallback wrapper and fresh database/manager/runtime reconstruction.

Self-review is in SWITCH-SHUTDOWN-GATES.md. The initial broader command in `switch-shutdown-full-race.log` intentionally stripped ambient credentials but also omitted HOME. Two existing session-manager test groups failed with a missing-home error; their temporary provider profiles were already isolated. No source change was made for that environment failure. The account service, Chat service and SQLite store race packages passed in that command. A final session-manager rerun retains the original HOME value without replacing it, while keeping other ambient configuration stripped.

## Final verification

All commands used fish from the repository root, go1.27.1, GOMAXPROCS=2, serial package execution and a session-owned TMPDIR under the evidence root. Final checks used an otherwise stripped environment while retaining the original HOME and PATH, and explicit existing GOCACHE/GOPATH. The HOME value was not repurposed. Synthetic fixture profiles remain temporary. No provider login or live billing was exercised.

| Check | Command after the environment prefix | Log and result |
| --- | --- | --- |
| Environment control | `go -C backend test -p 1 -race -count=1 -timeout=3m ./internal/session_manager -run '^TestInterfaceTransition(UnpromptedChatRoundTrip|NativeHistoryOwnership)$'` | `shutdown-environment-control.log`, exit 0, 21.317s; no source correction |
| Full manager race | `go -C backend test -p 1 -race -v -count=1 -timeout=15m ./internal/session_manager` | `shutdown-final-session-race.log`, exit 0, 208.011s; 1087 leaf passes, two existing platform skips |
| Repeated recovery controls | `go -C backend test -p 1 -race -v -count=3 -timeout=10m ./internal/session_manager -run '^TestAccountsManagerSwitch(Startup|Handoff|Cancel|Destroy|Shutdown|Readiness)'` | `shutdown-final-focused.log`, exit 0, 124.171s; 105 distinct cases x3, including 15 shutdown cases x3; no skips |
| Real production runtimes | `go -C backend test -p 1 -tags=e2e -race -v -count=1 -timeout=10m ./internal/session_manager -run '^TestAccountsManagerSwitch(ShutdownRealProductionRecovery|RealProduction)'` | `shutdown-final-production-rerun.log`, exit 0, 99.195s; 29 cases, including four new shutdown cases; no skips |
| Backend build | `go -C backend build -p 1 ./...` | `shutdown-final-build.log` and `.exit`, exit 0 |
| Backend vet | `go -C backend vet -p 1 ./...` | `shutdown-final-vet.log` and `.exit`, exit 0 |
| Tagged manager vet | `go -C backend vet -p 1 -tags=e2e ./internal/session_manager` | `shutdown-final-tagged-vet.log` and `.exit`, exit 0 |
| Tagged package lint | `go -C backend run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2 run --build-tags=e2e ./internal/session_manager` | `shutdown-final-lint.log`, exit 0, 0 issues |

The three other affected packages completed in `switch-shutdown-full-race.log`: account service 15.850s, Chat service 222.820s and SQLite store 127.119s, all passing. Their source and test dependencies were unchanged by the final manager-only test assertion expansion. The aggregate command itself failed due to the manager's omitted-HOME environment, and is not described as an overall pass.

The full manager skips are `TestSpawn_RejectsMissingTmuxBeforeSessionRow` and `TestValidateRuntimePrerequisites_AllowsConfiguredBundledTmux`, both explicitly platform-specific. No required shutdown or takeover case skipped.

The first wider process run had one failure at `accounts_manager_handoff_ownership_e2e_test.go:88`, in `direct/waiting/foreign`: the existing fixture's initial source teardown reported its PTY host still alive. This happens before the corrected finish method executes. `shutdown-final-production.log` preserves the failure. The exact case then passed three times in `shutdown-setup-teardown-control.log` (21.891s) with no source edits, using the same command shape and `-run '^TestAccountsManagerSwitchRealProductionHandoffOwnership$/^direct$/^waiting$/^foreign$' -count=3 -timeout=3m`. The whole 29-case matrix subsequently passed. This is an observed intermittent teardown failure, not a proved root cause or a fixed product defect; carry it into final release stability review.

`shutdown-verification-summary.json` records measured leaf outcomes. Source manifest re-verification passed after every final command. All 91 protected paths and five guest-design records still match the integrated manifests, with results in `shutdown-final-protected.log` and `shutdown-final-guest.log`. `git diff --check` and generated-file preservation checks exited 0. No API/SQL contracts changed; generation was not rerun or claimed as independently validated. No frontend or live-account test is claimed by this slice. Existing Linux retirement failures remain unresolved; native Windows and both Mac architecture checks remain open.

## Requested independent review

Review the exact manifest against the base commit. Check that shutdown cannot discard pre-stop intent, a cancellation winner stays terminal, a committed stop cannot become cancellable, only the recorded generation's fences are retained/released, and cold retry keeps exact-owner and no-replay guards. Review both direct/fallback process evidence and the genuine-target-error control. Report findings against this bounded correction without clearing unrelated release gates.

Historical failed-record repair and a public Retry action for recovered requested/waiting operations remain separate open work. The current UI can cancel these phases but exposes Retry only for recovery_required. No old terminal operation is reopened by this patch.
