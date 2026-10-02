# R3 source-input ownership correction

Disposition: corrected locally and frozen for reviewer 82 confirmation. Coordinated deletion remains held. Product gates S2/S3 and G1-G6 remain open.

Input: `rpt_50bbb964-7bd3-4b86-aa5f-18bc2832dddd`, artifact `/tmp/pr-5769-teardown-r2-review-82.txt`. R2 is independently closed for the reported destructive-retry sequence.

## Correction scope

1. `backend/internal/session_manager/accounts_manager_switch.go`: the account-specific terminal handoff requires fresh fenced ownership before Stop-now input. Missing identity or any unknown result stops before input; positively dead owners need no interrupt. Uncertainty before stopping retains both input fences and the cancellable waiting phase. A durable winning cancellation is also handed to worker cleanup under the operation mutex, so a racing journal write cannot strand input gates.
2. `backend/internal/session_manager/accounts_manager_recovery.go`: pre-stop retry uses the same ownership guard. Recovery after the durable stopping boundary does not replay terminal handoff from a stale activity record. The existing exact-owner teardown and binding-commit checks remain authoritative.
3. `backend/internal/domain/accounts_manager_switch.go`: waiting may retain its phase while recording an ownership error. This preserves the durable pre-stop cancellation proof across restart.
4. `backend/internal/session_manager/accounts_manager_handoff_ownership_test.go`: both handle forms, initial admission, pre-stop and post-stop SQLite reopen, uncertain-owner variants, repeated retry, retained gates, safe cancellation, exact-owner and absence controls.
5. `backend/internal/session_manager/accounts_manager_handoff_ownership_e2e_test.go`: actual production runtime construction, real replacement workloads, SQLite reopen, repeated retry, exact-child survival, and same-owner/absence controls before and after stopping.

The shared native/interface handoff is unchanged by R3. The real-process test wrapper forwards the optional exact-process inspection capability, so readiness uses the same production boundary rather than a synthetic acknowledgement.

## Failed-first evidence

- Original 24-case deterministic matrix: all 16 foreign/unknown cases sent an interrupt, including cold retries. Four confirmed-absence cases also sent input. `/tmp/pr-5769-r3-red-79.log`.
- Original real-process cases: direct and fallback foreign owners each received an interrupt; absent-source cases also attempted input. `/tmp/pr-5769-r3-real-red-79.log`. The initial positive controls additionally exposed a missing optional inspector forwarding method in the test wrapper. That test-only omission was corrected before judging target readiness.
- Post-stop same-owner controls reproduced an unnecessary second handoff on both slots before removing replay. `/tmp/pr-5769-r3-poststop-red-79.log`.
- The adversarial cancellation test made cancellation commit immediately before the worker's waiting-to-waiting error write. Both handle forms retained their raw input fence before the cleanup correction. `/tmp/pr-5769-r3-cancellation-red-79.log`.

## Verification

Commands run from `backend` through explicit Fish, the existing environment-sanitizing wrapper, Go 1.27.1, `GOMAXPROCS=2`, `-p=1`, private tmux state, and physical short scratch `/var/tmp/ao79.uF9PZF`. No live credentials are used.

- Expanded deterministic matrix: exit 0 under race, 36 cases, 28.991s. `/tmp/pr-5769-r3-expanded-green-79.log`. Uncertain cases also retry after another SQLite reopen. Pre-stop cancellation releases both gates without touching the runtime; post-stop cancellation remains refused.
- Real-process matrix: exit 0 under race, 12 cases, 54.845s. `/tmp/pr-5769-r3-real-green-79.log`. Both actual runtime forms preserve a foreign supervisor and its child through repeated cold retries with zero input, destruction, launch, or binding/generation rotation. Same-owner pre-stop controls interrupt once; post-stop and confirmed-absence controls do not interrupt.
- After cancellation cleanup: all handoff and startup tests passed three race repetitions, exit 0, 73.237s. This includes 108 ownership-matrix executions, six cancellation-write contention cases, and 27 startup/cancellation cases. `/tmp/pr-5769-r3-cancellation-green-79.log`.
- All actual-process workflows passed three race repetitions after the final cleanup correction, exit 0, 223.806s: 75 scenario executions, including all 36 R3 cases, no skips or race reports. `/tmp/pr-5769-r3-actual-process-79.log`.
- All 91 protected files match baseline `b398a95c59425c381ec2f3d36d507097c9ccc2de`. Of the prior 19 frozen files, only the account switch and recovery implementation files changed; the other 17 still match.
- Seven-package focused suite passed three race repetitions, exit 0: manager 119.097s, selection 1.014s, tmux 2.008s, native host 1.015s, Chat 23.812s, store 15.781s, daemon 1.020s. `/tmp/pr-5769-r3-focused-race-79.log`.
- Shared harness switching, recovery, reconciliation, and controller exit passed under race, exit 0, 17.845s. `/tmp/pr-5769-r3-shared-switch-race-79.log`.
- Full backend build/vet and e2e manager vet each exited 0. All five R3 Go files are formatted; `git diff --check` passes.

Repeated commands:

```text
go test -mod=readonly -p=1 -race -count=3 -timeout=180s
  ./internal/session_manager -run '^TestAccountsManagerSwitch(Handoff|Startup)' -v

go test -mod=readonly -p=1 -race -tags=e2e -count=3 -timeout=360s
  ./internal/session_manager
  -run '^TestAccountsManagerSwitchReal(Production|ExitedSupervisorPane|ReservedTargetSurvivesColdRecovery)' -v

go test -mod=readonly -p=1 -race -count=3 -timeout=300s
  ./internal/session_manager ./internal/adapters/runtime/runtimeselect
  ./internal/adapters/runtime/tmux ./internal/adapters/runtime/conpty
  ./internal/service/chat ./internal/storage/sqlite/store ./internal/daemon
  -run '^(TestAccountsManager(Switch|Chat)|TestAccountRecovery|TestProbeFencedRuntime|TestHybridRuntime|TestProductionRuntime)'

go test -mod=readonly -p=1 -race -count=1 -timeout=300s
  ./internal/session_manager
  -run '^Test(SwitchAgent|RecoverAgentSwitch|ReconcileAgentSwitch|ExitAgent)'

go build -mod=readonly -p=1 ./...
go vet -mod=readonly -p=1 ./...
go vet -mod=readonly -p=1 -tags=e2e ./internal/session_manager
```

## Preservation and limits

The pre-correction R2 source, manifest, and handoff are preserved in `/tmp/pr-5769-pre-handoff-correction-79.tar.gz`, SHA256 `96a5a32bc4dbe9ce60ffc506daf36c5c083673cdcb6b98cb3f384a511b3140e1`. Historical manifests must not be replaced. R3 is a separate delta.

The exact five-file R3 code/test freeze is `/tmp/pr-5769-r3-delta-79.sha256`. The accompanying `/tmp/pr-5769-handoff-freeze-79.sha256` covers 22 files: the previous 19 entries at their current hashes, plus the phase model and two new test files. Seventeen prior entries are unchanged. HEAD remains `048a59775999b60f8276a1f5d1107dbef57f5483`; all changes remain uncommitted and local. No protected native-switch or subscription file was edited. Source hashes were compared again after verification.

The cancellation flag is set only after durable cancellation succeeds. Worker cleanup reads it under the same mutex that marks the run settled; either cancellation observes a settled worker and releases the gates, or the worker observes the winning cancellation and does so. The worker captures its completion channel before unlocking, preventing a retry from replacing the channel it must close. Terminal journal state remains authoritative, and cancellation after stopping is still rejected.

This corrects the missing pre-input ownership check and replay ordering. The underlying interrupt API is handle-only; a final ownership probe is not an atomic compare-and-interrupt against arbitrary external replacement after that probe. Tests use actual Linux processes with synthetic workloads, not provider sign-in or native execution on other operating systems. This backend-only correction has no new visual behavior.

No deletion integration or publication is included. Real desktop workflows, public switching routes/CLI, session controls, coordinated removal, supported-platform/provider execution, and measured responsiveness remain separate release work.

Reviewer request: confirm both pre-stop ownership guarding and post-stop no-replay, including the cancellation-write race and retained fences after uncertainty. R2 remains closed for its reported sequence. No dependency is cleared by this worker's passing checks alone.

## Broad verification status

The prior full frontend result remains 337 files and 5,341 tests passed, six skipped. Runner full build/vet/race and frontend typechecks passed. The full backend race run was interrupted before R3 editing, not completed: its HTTP shutdown timeout reproduced five of ten times on both the current tree and untouched HEAD. Full engine testing retains the established baseline media-test failure. Lint has 66 recorded findings. Cross-platform runtime/manager builds are compile-only evidence, not execution. These limits do not change the R3 gate or imply release readiness.
