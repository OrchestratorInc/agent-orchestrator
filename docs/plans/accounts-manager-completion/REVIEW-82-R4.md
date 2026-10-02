# R4 cancelled retry admission

Disposition: R4 correction verified locally and frozen for reviewer 82. Independent confirmation is pending. Deletion remains held. No publication is authorized.

Input: `rpt_46af3a2b-e8bd-4053-8a9e-d5d432b6745f`, `/tmp/pr-5769-handoff-r3-review-82.txt`. R3 ownership and no-replay are independently closed for their reported sequences. R4 concerns a retry holding a stale waiting snapshot while cancellation wins and cleanup is still pending.

Plan:

1. Preserve the R3 freeze. Reproduce the three-party schedule using a paused journal read, cancellation at the worker's uncertainty write, and a blocked terminal-fence release. Assert conflict and no new runtime work after cancellation.
2. Reject cancelled runs under the admission mutex. Keep cleanup from publishing a retryable idle run before cancellation cleanup completes. Preserve exactly-once fence release and completion-channel closure. Review the handoff ordering before broader tests.
3. Cover both handle forms, SQLite reopen and retry, valid resume, and stop-wins controls. Exercise actual production-selected processes. Repeat affected race suites, build/vet, and protected-file comparisons.
4. Freeze the exact R4 delta and supporting source manifest, persist the handoff, request reviewer 82, and stop for independent confirmation. Do not begin deletion.

The pre-R4 source and historical manifests are preserved in `/tmp/pr-5769-pre-cancel-retry-correction-79.tar.gz`, SHA256 `f13d49b71cb93a0c0fff0031ce2e5bdd079b1613a8fad72722e0bc62403420ff`. The R3 manifests matched before work began. Production edits are limited to account-switch retry admission and completion ordering; protected native switching and subscription paths remain out of scope.

## Failed-first evidence and correction

All four deterministic cases reproduced R4 before production edits: both runtime handle forms, from initial execution and reconstructed waiting intent. Cleanup exposed `running=false, cancelled=true`; the paused retry returned success, installed a new completion channel, made one additional ownership probe, and sent one interrupt. No destruction or launch was observed in that red sequence. Log `/tmp/pr-5769-r4-red-79.log`, exit 1, 12.658s.

The correction changes only two production files:

- `backend/internal/session_manager/accounts_manager_recovery.go`: include the run's cancellation flag in admission rejection under `accountSwitchMu`, before allocating a worker context.
- `backend/internal/session_manager/accounts_manager_switch.go`: keep a cancelled run running while cleanup releases its fences and removes it. Publish idle and close the captured completion channel together under the admission mutex after cleanup. Non-cancelled completion also closes its channel before a new retry can replace it.

New tests are `accounts_manager_cancel_retry_test.go` and `accounts_manager_cancel_retry_e2e_test.go` in the same directory. The shared schedule uses real SQLite reads and CAS, an explicit barrier after the retry's waiting read, and the existing terminal-gate release boundary. It adds no production test hooks. Every case reopens SQLite and rebuilds the manager before two further rejected retries.

## Midpoint review

Reviewed cancellation both before and after the worker's final journal write. A winning cancellation either sees a settled worker and owns cleanup, or marks the still-running worker cancelled so that worker owns cleanup. Raw fence release can block without exposing a retryable idle run. The mutex is not held across Chat abort or terminal-fence release, avoiding a nested acquisition in `releaseAccountSwitch`. Completion is closed once before a valid new retry can replace it. Existing per-run release-once protection is unchanged.

The first correction check passed three race repetitions, exit 0, 27.921s: twelve R4 interleavings, six prior cancellation/error-write checks, and 27 startup/cancellation/resume controls. Log `/tmp/pr-5769-r4-green-79.log`.

The first actual-process check passed all four R4 cases under race, exit 0, 24.017s. It uses production runtime construction and a real supervised source workload in each direct/fallback slot, with both initial and reconstructed waiting intent. SQLite is reopened before two additional rejected retries. Exact source-child liveness is verified afterward. Log `/tmp/pr-5769-r4-real-green-79.log`.

The final test assertions additionally verify removal of the cancelled run, unchanged binding and unblocked authorization after database reopen, and restored session intake. These assertions are included in the repeated affected and actual-process suites below. Of the 22 R3 frozen files, only the two listed production files changed. All 91 protected files still match the baseline. Broader product and release gates remain open.

## Full affected verification

Explicit Fish, `backend` working directory, Go 1.27.1, `GOMAXPROCS=2`, serial package execution, environment sanitization, private tmux state, and short physical scratch `/var/tmp/ao79.uF9PZF` were used. No live credentials or external provider requests are involved.

The seven-package focused race suite passed three repetitions, exit 0: manager 123.248s, selection 1.014s, tmux 2.092s, native host 1.020s, Chat 23.942s, store 15.848s, daemon 1.021s. Log `/tmp/pr-5769-r4-focused-race-79.log`.

```text
go test -mod=readonly -p=1 -race -count=3 -timeout=300s
  ./internal/session_manager ./internal/adapters/runtime/runtimeselect
  ./internal/adapters/runtime/tmux ./internal/adapters/runtime/conpty
  ./internal/service/chat ./internal/storage/sqlite/store ./internal/daemon
  -run '^(TestAccountsManager(Switch|Chat)|TestAccountRecovery|TestProbeFencedRuntime|TestHybridRuntime|TestProductionRuntime)'
```

The complete actual-process suite passed three repetitions, exit 0, 245.526s: 87 scenario executions, including twelve R4 interleavings, with no skips, failing tests, or race reports. The log has 102 passing entries including fifteen parent-suite entries. Log `/tmp/pr-5769-r4-actual-process-79.log`.

```text
go test -mod=readonly -p=1 -race -tags=e2e -count=3 -timeout=360s
  ./internal/session_manager
  -run '^TestAccountsManagerSwitchReal(Production|ExitedSupervisorPane|ReservedTargetSurvivesColdRecovery)'
```

Shared switching, recovery, reconciliation, and exit regressions passed, exit 0, 17.709s. Log `/tmp/pr-5769-r4-shared-switch-race-79.log`.

```text
go test -mod=readonly -p=1 -race -count=1 -timeout=300s
  ./internal/session_manager
  -run '^Test(SwitchAgent|RecoverAgentSwitch|ReconcileAgentSwitch|ExitAgent)'
```

Final static checks each exited 0:

- `go build -mod=readonly -p=1 ./...`, `/tmp/pr-5769-r4-build-79.log`.
- `go vet -mod=readonly -p=1 ./...`, `/tmp/pr-5769-r4-vet-79.log`.
- `go vet -mod=readonly -p=1 -tags=e2e ./internal/session_manager`, `/tmp/pr-5769-r4-e2e-vet-79.log`.
- `git diff --check` and `gofmt -l` on the four R4 files produced no findings.

The successful build/vet logs are empty because the commands produced no diagnostics; exit status was observed. An initial build-launch attempt used an unavailable optional `/usr/bin/time` executable and exited 127 before Go ran. The direct rerun above succeeded.

## Exact freeze and review request

Frozen at HEAD `048a59775999b60f8276a1f5d1107dbef57f5483`, branch `ao/agent-orchestrator-79/accounts-manager`, with the existing uncommitted implementation preserved. All paths in the manifests are relative to this session's worktree. No commit, push, PR mutation, or publication was performed.

- Four-file R4 delta: `/tmp/pr-5769-r4-delta-79.sha256`, manifest SHA256 `57e158ce52f9d536dd5362e21292fb9163d850a62392567b5f0e8c9cfa5ff9c5`.
- Complete 24-file supporting freeze: `/tmp/pr-5769-cancel-retry-freeze-79.sha256`, manifest SHA256 `f7b0247ba4439cfffacaddcbf31bd83218d0aee86f2d1eeb8bd3aecb673b5db1`.
- Protected-path and production-diff audit: `/tmp/pr-5769-r4-integrity-79.txt`, SHA256 `f2eaac5a3d0644a2fabe61f632db4f37012bcc23ed92da78e862d2ccca699e3d`.

Both manifests passed `sha256sum -c`. The 24-file source snapshot is byte-for-byte identical to the snapshot taken before the final affected and actual-process runs. The protected inventory from `/tmp/accounts-manager-81/final-audit.json` contains 91 paths; all match baseline `b398a95c59425c381ec2f3d36d507097c9ccc2de`. The audit records the exact two production diffs against the preserved pre-R4 archive. The archive hash still matches, and all twenty other R3 manifest entries are unchanged.

Final adversarial reread confirmed that stale retry admission cannot install a context for a cancelled run, cleanup cannot expose that run as idle while raw-fence release blocks, and completion publication cannot race a new retry's channel replacement. Existing ownership proof, durable stopping/no-replay, per-run release-once, and post-stop cancellation rejection are unchanged. The deterministic and real-process schedules assert zero post-cancellation ownership probes, interrupts, input, destruction, launch, or revision rotation. Both initial and reconstructed waiting intent are covered, followed by SQLite reopen and two more rejected retries. Resume and stop-wins controls are included in the focused race suite.

Reviewer 82: confirm the R4 interleaving is closed on these exact hashes, inspect admission and cleanup ordering plus the negative controls, and return CLEAR or a bounded finding. Implementation stops at this freeze pending that disposition. Coordinated deletion must not resume before CLEAR.

## Remaining release gates

This is a local backend correction, not a whole-PR completion or release claim. The actual-process evidence is Linux production runtime construction with synthetic workloads and no live credentials. It does not establish native execution on other supported platforms, live-provider behavior, or real desktop coexistence. No visual surface changed in R4, so screenshots are not applicable to this correction; the product's desktop evidence gate is still open.

S2/S3, coordinated deletion S4, session controls/desktop S5, and product gates G1-G6 remain open. Full backend tests/race and lint are not claimed passing: the prior interrupted full race run, reproduced baseline HTTP shutdown timeout, 66 recorded lint findings, and baseline engine media-test failure remain recorded. Public switch routes/CLI, deletion coordination, session UI, complete supported-platform/provider checks, generated-contract completion, and performance evidence remain required after independent clearance. Historical frontend and runner results are unchanged and were not rerun for this narrow correction.
