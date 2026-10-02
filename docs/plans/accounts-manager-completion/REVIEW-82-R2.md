# R2 correction for independent confirmation

Disposition: corrected locally, pending reviewer 82 confirmation. Coordinated deletion remains held. This handoff does not close S2/S3 or G1-G6.

Input: `rpt_fb981ee8-c50f-4074-ab7a-75346f5007be`, local artifact `/tmp/pr-5769-production-runtime-r1-review-82.txt`. R1 is independently closed for its reported sequences.

## Scope and preservation

The production change is four lines in `backend/internal/session_manager/agent_switching.go`, inside `stopSourceRuntime`. After a failed destroy, only `FencedAlive` for the original reference permits the bounded second attempt. `FencedDead` still completes idempotently. All other results immediately retain `ErrSwitchSourceStopUnconfirmed`, the original command error, and the probe reason. Neither account bindings nor launch policy change.

This is the explicitly requested shared-helper correction. Its existing harness-switch callers are covered by their own race regression run. The earlier session-route lookup change elsewhere in that file predates R2 and is preserved.

New tests:

- `backend/internal/session_manager/accounts_manager_teardown_test.go`: post-error ownership changes, idempotent outcomes, source and target recovery, and repeated retries after SQLite reopen for both handle forms.
- `backend/internal/session_manager/accounts_manager_teardown_e2e_test.go`: actual replacement supervisors in both production-selected slots, before and after restart; simultaneous direct and fallback owners retired through cold recovery.

The prior 16-file freeze remains unchanged. Its manifest and the original shared helper and handoff documents were archived before editing:

```text
/tmp/pr-5769-pre-teardown-correction-79.tar.gz
SHA256 bfe87e92b52499ed134043ec2b633220bf3ea3204744db746a5fd48918d68f04
```

## Failed-first evidence

Before editing production code:

- All six exact-helper cases made two destructive calls instead of one after generation mismatch, missing identity, or probe failure, across both handle forms.
- All eight source/committed-target cases permitted replacement destruction and a new launch. They cover foreign and unknown ownership on both slots.
- Both actual fallback cases reported `replacement=true destructive calls=2 launches=1`.
- Both actual direct cases reported the same result in `/tmp/pr-5769-r2-direct-red-79.log`. Their first harness attempt exceeded its four-second settling limit. Repeating with a bounded fifteen-second wait reproduced the destructive defect before the fix.
- All twelve positive controls passed before the fix: first success, first command committed despite error, same-owner retry success, second command committed despite error, persistent same-owner failure, and uncertainty after the second error.

After correction, all of these passed together under `-race`, including SQLite reopen and repeated retry. Observed exit 0, 29.709s. Log: `/tmp/pr-5769-r2-green-79.log`.

## Repeated verification

Commands run from `backend`, using Go 1.27.1, `GOMAXPROCS=2`, `-mod=readonly`, a private tmux socket root, and physical short scratch `/var/tmp/ao79.uF9PZF`. The existing isolation wrapper removes inherited application/provider credentials before tests.

```text
go test -mod=readonly -p=1 -race -count=3 -timeout=180s
  ./internal/session_manager ./internal/adapters/runtime/runtimeselect
  ./internal/adapters/runtime/tmux ./internal/adapters/runtime/conpty
  ./internal/service/chat ./internal/storage/sqlite/store ./internal/daemon
  -run '^(TestAccountsManager(Switch|Chat)|TestAccountRecovery|TestProbeFencedRuntime|TestHybridRuntime|TestProductionRuntime)'
```

Exit 0. Package times: manager 94.576s, selection 1.023s, tmux 2.292s, native host 1.024s, Chat 25.342s, store 17.124s, daemon 1.026s. Log: `/tmp/pr-5769-r2-focused-race-79.log`.

```text
go test -mod=readonly -p=1 -race -tags=e2e -count=3 -timeout=240s
  ./internal/session_manager
  -run '^TestAccountsManagerSwitchReal(Production|ExitedSupervisorPane|ReservedTargetSurvivesColdRecovery)' -v
```

Exit 0, 137.388s. All 39 leaf executions passed, with no skips or race reports. This includes twelve mid-destroy takeover cases and three simultaneous-live-slot recovery cases, in addition to the prior 24 executions. Log: `/tmp/pr-5769-r2-actual-process-79.log`.

```text
go test -mod=readonly -p=1 -race -count=1 -timeout=300s
  ./internal/session_manager
  -run '^Test(SwitchAgent|RecoverAgentSwitch|ReconcileAgentSwitch)'
```

Exit 0, 30.199s. Log: `/tmp/pr-5769-r2-shared-switch-race-79.log`.

Backend `go build -mod=readonly -p=1 ./...`, `go vet -mod=readonly -p=1 ./...`, and e2e manager vet all exited 0. Formatting and `git diff --check` pass. All 91 protected files match baseline `b398a95c59425c381ec2f3d36d507097c9ccc2de` byte for byte.

The correction is frozen in `/tmp/pr-5769-teardown-freeze-79.sha256`: the original 16 unchanged files plus the shared helper and two new test files. Do not replace `/tmp/pr-5769-runtime-freeze-79.sha256`; it remains historical R1 evidence. Review may proceed against the new 19-file manifest while broad verification continues without source changes.

## Adversarial review and limits

The post-error probe receives the unchanged session, handle, and generation. Unknown results return before the second destructive call, so account recovery cannot reach revision rotation or target creation. The source and committed-target tests assert unchanged generation/revision, retained input fencing, and zero further destructive calls after the injected failure, including repeated cold retries. The actual-process tests use the production factory, real supervisor executable, and actual detached host or tmux. Replacement ownership is observed before returning the injected command error.

Positive controls retain idempotence when teardown completed despite a lost response. Repeated same-owner errors remain bounded. The two-live-slot case verifies that the correction does not strand valid owners when both slots genuinely belong to the reserved generation.

This fixes the reported retry ordering. It does not turn the handle-only destroy API into an atomic external compare-and-destroy operation. No claim is made about arbitrary external replacement between an otherwise valid final probe and the following command.

This slice changes no UI; new desktop screenshots do not apply to this backend-only correction. Complete desktop workflow, live-provider sign-in/requests, actual macOS/Windows execution, public switching routes/CLI, session controls, coordinated removal, and performance acceptance remain release gates. Real-process tests use synthetic workloads and no live credentials.

## Broad verification, separate from R2

- Runner full build/vet/race passed, test package 4.947s.
- Frontend typecheck and e2e typecheck passed.
- First Node 24 frontend full run: 334 files and 5,239 tests passed; 102 failures in three release-script files were caused by missing `zip`. A private official-archive package was verified against the local package database SHA256, without system installation. All 102 then passed, 11.77s. The complete rerun exited 0: 337 files passed, 5,341 tests passed, 6 skipped, 256.55s. Log: `/tmp/pr-5769-frontend-full-79.log`.
- Full engine race run completed with the previously established untouched-HEAD media-test failure (`TestPionMediaRelayBridgesAudioAndDataChannel`). This is not an engine-suite pass.
- Full lint reports 66 issues. Artifact: `/tmp/pr-5769-local-lint-79.json`. No lint correction is included in this freeze.
- Full backend race suite was restarted on the corrected tree with CI's `-timeout=20m`. It remains in progress, not passed.
- The backend run found `TestServerShutdownEndpoint` timing out after five seconds. The focused test failed five of ten repeats both on this tree (25.057s) and on an untouched `048a59775999b60f8276a1f5d1107dbef57f5483` archive (25.053s). Server and test files are unchanged. Logs: `/tmp/pr-5769-shutdown-recheck-79.log` and `/tmp/pr-5769-shutdown-baseline-79.log`. The clean snapshot is `/var/tmp/ao79-httpd-baseline.DNtctm`. This establishes a baseline failure, not a passing HTTP suite; no shutdown code or test tolerance was changed.

Nothing has been committed, pushed, or published by this correction.
