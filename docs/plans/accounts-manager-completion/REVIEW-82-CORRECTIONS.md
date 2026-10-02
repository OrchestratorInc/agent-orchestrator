# Review 82 correction handoff

Local worktree: session 79, HEAD `048a59775999b60f8276a1f5d1107dbef57f5483` plus preserved unpublished changes. Checklist: `/tmp/pr-5769-read-only-review-82.txt`. No publication or PR mutation.

Current disposition: the independent review closed the reported R1 wrapper defect and holds deletion on R2. The failed-first destructive-retry correction and its new freeze are recorded in [REVIEW-82-R2.md](REVIEW-82-R2.md), pending independent confirmation. The sections below retain the earlier evidence.

## Reproductions and corrections

| Finding | Observed before correction | Correction and regression |
| --- | --- | --- |
| Last-server stop becomes unknown | Absent-server and absent-socket fixtures returned `unknown`; negative controls correctly remained unknown. | Preserve the adapter's typed positive absence evidence. Actual isolated tmux teardown and repeated recovery are covered by `TestAccountsManagerSwitchConfirmsLastTmuxStopOnRepeatedRecovery`. |
| Repeated retry loses the previous controller | Second retry returned `CONTROLLER_CHANGED` after rotation and failed launch. | Persist retired generation and handle independently from the next reservation. Probe the actual owner. Close/reopen tests inject failure at binding sync, route preparation, runtime creation, and metadata publication. |
| Cancellation loses accepted queued work | Startup and cancellation-before-cold-adoption both changed an undispatched turn to `failed`. | Startup keeps recovery-required intent. Per-turn durable obligations survive terminal switch phases and are retired by dispatch or explicit message cancellation. Native unrelated queue settlement remains unchanged. |
| Empty-conversation proof discarded | An untouched ordinary switch entered `TARGET_START_UNCONFIRMED` after commitment. | Persist positive empty-source proof and resolved history identity before source stop. Honor that decision on launch and cold retry. Missing history with prior-work evidence still fails before stopping. |

Additional reproduced defects corrected in this slice:

- A retained exited pane prevented creating its replacement. Known-dead cleanup now retires that pane; unknown probes still do not authorize destruction.
- A retry probed its reserved next generation instead of its retired owner.
- An old readiness witness could acknowledge a newer generation. The storage transaction now compares the witness's expected generation.
- The production daemon adapter did not expose account handoff methods. A capability regression failed before adding the delegations.

## Exact correction files

Runtime and coordinator:

- `backend/internal/adapters/runtime/tmux/tmux.go`
- `backend/internal/adapters/runtime/tmux/accounts_manager_recovery_test.go`
- `backend/internal/domain/accounts_manager_switch.go`
- `backend/internal/ports/accounts_manager.go`
- `backend/internal/session_manager/accounts_manager_switch.go`
- `backend/internal/session_manager/accounts_manager_recovery.go`
- `backend/internal/session_manager/accounts_manager_switch_test.go`
- `backend/internal/session_manager/accounts_manager_recovery_test.go`
- `backend/internal/session_manager/accounts_manager_chat_test.go`
- `backend/internal/service/chat/accounts_manager_handoff_test.go`
- `backend/internal/daemon/accounts_manager_wiring.go`
- `backend/internal/daemon/accounts_manager_wiring_test.go`

Durable state:

- `backend/internal/storage/sqlite/migrations/0165_accounts_manager_retry_owner.sql`
- `backend/internal/storage/sqlite/migrations/0166_accounts_manager_history_decision.sql`
- `backend/internal/storage/sqlite/migrations/0167_accounts_manager_queue_obligations.sql`
- `backend/internal/storage/sqlite/migrate_burned_versions_test.go`
- `backend/internal/storage/sqlite/queries/accounts_manager_switches.sql`
- `backend/internal/storage/sqlite/queries/conversations.sql`
- `backend/internal/storage/sqlite/store/accounts_manager_switch_store.go`
- `backend/internal/storage/sqlite/store/accounts_manager_switch_store_test.go`
- `backend/internal/storage/sqlite/gen/accounts_manager_switches.sql.go`
- `backend/internal/storage/sqlite/gen/conversations.sql.go`
- `backend/internal/storage/sqlite/gen/models.go`

Generated files were regenerated with the pinned sqlc command. The removal foundation was not advanced during this correction slice.

## Observed verification

Fish, backend directory, isolated inherited credential/runtime variables, `GOMAXPROCS=2`, serial package execution, workspace-owned `TMPDIR` and `GOTMPDIR`.

```text
go test -mod=readonly -p=1 -race -count=3 -timeout=180s \
  ./internal/session_manager ./internal/service/chat ./internal/storage/sqlite/store \
  ./internal/adapters/runtime/tmux ./internal/daemon \
  -run '^(TestAccountsManager(Switch|Chat)|TestAccountRecovery)'
```

Exit 0: manager 37.454s, Chat 20.159s, store 15.732s, tmux 1.019s, daemon 1.023s. This includes synthetic Manager-to-Chat drain and queue adoption with native history, and rejects task replay.

The subsequent stronger queue check closes/reopens SQLite before startup reconciliation and cold adoption. It passed three race repetitions, Chat 15.478s. The explicit verbose real-runtime check reports `PASS` for `TestAccountsManagerSwitchConfirmsLastTmuxStopOnRepeatedRecovery` (0.26s), not a skip. Backend-wide `go build -mod=readonly -p=1 ./...` and `go vet -mod=readonly -p=1 ./...` passed under explicit Fish. The full backend race suite is running; no result is claimed yet.

The exact 91-path protected inventory from the continuation audit has zero changed paths against baseline `b398a95c59425c381ec2f3d36d507097c9ccc2de`. `git diff --check` passes. Root has about 25 GB free; temporary compilation no longer uses shared `/tmp`.

## Held gates

S2/S3 and coordinated deletion remain held for independent confirmation. These corrections do not establish public route/CLI behavior, completed in-use deletion, session controls, actual desktop coexistence, live sign-in, supported-platform runtime behavior, full-branch verification, or responsiveness. Main completion gates G1-G6 remain open. Provider conversations in controller tests are synthetic; only the isolated tmux shutdown uses a real runtime process in this slice.

Independent re-review `rpt_d0fcad72-e691-4a4d-98ca-9b0bdf86b3ca` closed the four original sequences. Its full artifact is `/tmp/pr-5769-read-only-rereview-82.txt`. It retains three blockers, with deletion still held:

1. Reproduce a reserved target surviving failed metadata publication and failed cleanup. Reconcile both reserved and retired exact runtime owners, reject unrelated generations, then verify cold retry.
2. Reproduce the actual retained `cat` pane through the runtime adapter. Require generation-bound origin plus positive residue evidence before teardown; retain unknown results for manual workloads and foreign generations.
3. Reproduce cancellation after startup from requested/waiting, including queued work and an unavailable target. Retain durable pre-stop proof, release the restored fence, and verify exactly-once queue adoption. Cancellation after stopping remains forbidden.

Each reproduction must fail before its correction. Run affected race suites, audit protected paths, then request reviewer 82 confirmation before deletion integration. Full verification was interrupted to address these findings; its partial output also reported three workspace integration failures that still require attribution and rerun. No full-suite pass is claimed.

## Re-review correction slice

| Finding | Failing reproduction observed | Local correction |
| --- | --- | --- |
| F1 reserved target survives publication | `TestAccountsManagerSwitchColdRetryRetiresSurvivingReservedTarget` returned `TARGET_STOP_UNCONFIRMED` after failed publication and cleanup, then storage reopen. Its foreign-generation control passed. | Optional runtime launch-handle contract resolves the exact slot before creation, consistently across restarts. Recovery first probes the journaled reservation, then accepts only an exact retired-owner witness when the reservation reports a generation mismatch. Probe failures and unrelated generations cannot authorize teardown or rotation. |
| F2 actual retained pane | `TestAccountRecoveryRealRetainedPaneIsGenerationBound` observed a real `cat` pane but received `unknown/identity_missing`. | Confirm a sole, childless stdin sink, its exact generated launch origin and generation, and a stable PID/process tree. Parse quoted content without evaluation or logging. Manual workloads, altered origins, multiple panes, foreign generations, and probe errors remain unknown. |
| F3 cancellation after startup | Both requested and waiting cases in `TestAccountsManagerChatCancellationAfterStartupAdoptsQueue` rejected cancellation after SQLite reopen and `ReconcileStartupSafety`. | Keep the durable requested/waiting phases as pre-stop proof. Explicit retry can resume those phases. Cold cancellation releases restored terminal and operation fences and preserves per-turn queue obligations. Stopping and uncertain-stop recovery remain non-cancellable. No schema backfill guesses at older ambiguous recovery rows. |

Additional changed files for this slice:

- `backend/internal/ports/outbound.go`
- `backend/internal/adapters/runtime/tmux/launch_handle.go`
- `backend/internal/adapters/runtime/conpty/launch_handle.go`
- `backend/internal/adapters/runtime/tmux/retained_pane.go`
- `backend/internal/adapters/runtime/tmux/retained_pane_test.go`
- `backend/internal/session_manager/accounts_manager_reserved_test.go`
- `backend/internal/session_manager/accounts_manager_startup_test.go`
- `backend/internal/session_manager/accounts_manager_tmux_e2e_test.go`

Previously listed files further changed: `accounts_manager_recovery.go`, `accounts_manager_switch.go`, `accounts_manager_switch_test.go` in the manager package; `accounts_manager_handoff_test.go` in Chat; `tmux.go` and `accounts_manager_recovery_test.go` in the tmux adapter. No deletion, protected switching, subscription, or SDK files changed in this slice.

The initial three corrected reproductions passed under `-race`: manager 13.828s, runtime 1.347s, Chat 13.682s. The expanded one-pass suite passed: manager 23.422s, runtime 1.342s, Chat 16.308s, store 13.333s, daemon 1.018s. The crash-cut case closes/reopens storage with `starting` intent and a live reserved target, without publication or cleanup. The unknown-generation control verifies no destruction, launch, or revision rotation.

Real-runtime checks:

```text
go test -mod=readonly -p=1 -race -tags=e2e -count=3 -timeout=240s \
  ./internal/session_manager \
  -run '^TestAccountsManagerSwitchReal(ExitedSupervisorPane|ReservedTargetSurvivesColdRecovery)$' -v
```

Exit 0, 27.262s, all six tests explicitly passed without skips. These build and run the actual local supervisor executable in isolated tmux. One replaces its retained pane and reaches exact-generation readiness. The other leaves a real unpublished target alive, demonstrates the retired generation mismatch, reopens storage, constructs a fresh runtime adapter, and completes retry. Workloads and credentials remain synthetic; this is not live-provider or desktop evidence. An initial end-to-end fixture rejected normal terminal whitespace; trimming only the synthetic inspector corrected the fixture, with no production readiness relaxation.

Final focused race repetitions passed on the corrected tree:

```text
go test -mod=readonly -p=1 -race -count=3 -timeout=180s \
  ./internal/session_manager ./internal/adapters/runtime/tmux ./internal/service/chat \
  ./internal/storage/sqlite/store ./internal/daemon \
  -run '^(TestAccountsManager(Switch|Chat)|TestAccountRecovery|TestProbeFencedRuntime)'
```

Exit 0: manager 45.460s, runtime 2.018s, Chat 23.773s, store 15.704s, daemon 1.020s. Repeated the actual-supervisor command above on that same final tree: exit 0, 26.960s, six explicit passes and no skips.

The last adversarial extension exposed a conservative false rejection for quoted dollars/backticks and newlines in the real pane origin. A separate tmux display decoder now precedes strict shell-structure validation. The installed tmux 3.6a display format uses C-style `vis` escapes, verified against its [argument renderer](https://github.com/tmux/tmux/blob/3.6a/arguments.c#L559). The stronger real-runtime fixture and forged-token controls passed after correction. No launch text or credentials enter production diagnostics.

Tests now use a short, physical, session-owned root-volume directory, `/var/tmp/ao79.uF9PZF`, for both temporary variables. This avoids shared temporary quota, Unix socket path limits, and symlink-sensitive workspace fixtures. Current backend-wide `go build -mod=readonly -p=1 ./...` and `go vet -mod=readonly -p=1 ./...` passed. Full affected-package race suites passed: tmux 7.125s, ConPTY 8.536s, workspace 7.375s, supervisor 1.338s. The earlier three workspace failures were path-alias fixture failures: they passed on the short physical path with no changes to that package. Full backend race verification remains pending. Protected inventory still reports 91 paths, zero changes. All three blockers require reviewer 82 confirmation before deletion resumes.

## Production runtime correction R1

Independent report `rpt_b8462ce0-cd99-422a-9f66-e3982339904f`, artifact `/tmp/pr-5769-frozen-corrections-review-82.txt`, closes F1-F3 for their reported sequences. It holds deletion on R1: Linux/macOS production construction returns a wrapper without the new optional launch-identity capability. The prior real-runtime tests constructed a concrete adapter and missed this integration boundary.

### Failed-first evidence

- `TestAccountsManagerSwitchProductionRuntimeAdmission` constructs `runtimeselect.New`, exactly as daemon wiring does. Before correction it failed with `session: interface handoff unsupported`. After correction it passed; its cancelled background context deliberately prevents process creation in this unit test.
- `TestAccountRecoveryUsesCanonicalLaunchHandle` exposed a related identity mismatch for long session IDs. The fallback adapter creates a shortened, hashed name, while its fenced probe compared the handle to the original session ID. The expected canonical handle returned `unknown/identity_missing` before correction. Canonical identity now passes; raw noncanonical and unrelated handles remain unknown.
- `TestAccountsManagerSwitchReadinessRequiresUnambiguousRuntime` initially acknowledged ready despite a second slot containing another live runtime, a foreign generation, or an unknown probe. All three negative cases failed before correction; the absent-second-slot positive control passed. The account-only acknowledgement now requires every other possible slot to be proven dead. Retry retires both exact journaled owners when proven, but cannot tear down or rotate through a foreign or unknown owner.

### Durable identity contract

`RuntimeLaunchHandleResolver.LaunchHandles(sessionID)` returns the complete, nonempty, unique set of slots that creation can affect, including partial native startup and fallback. It is pure and independent of current backend availability. The durable journal's session ID reconstructs the same set after restart; concrete backend availability is never guessed or inferred from stale session metadata.

- Direct native slot: `ptyhost-v1:` plus the concrete native handle. The prefix remains part of durable opaque session metadata and is stripped only when forwarding to that backend.
- Fallback slot: the existing unprefixed canonical tmux handle, including its existing long-ID sanitization. Its current creation and legacy routing policy is unchanged.
- Single-backend platforms expose their concrete slot. Empty, duplicate, unsupported, nested/version-ambiguous, malformed, and foreign-session identities cannot become ownership evidence.
- Recovery validates every possible slot before any teardown, then re-probes during exact-generation teardown. Only explicit generation mismatch in the recorded owner's slot permits trying that recorded generation. Unknown evidence never authorizes retry rotation. A surviving unpublished target on a different backend remains discoverable after metadata publication failure and SQLite reopen.
- Future routing changes must retain recoverability of these handle versions. Dropping or reinterpreting a slot requires a durable migration, not a change based on current availability.

No new migration is needed for this deterministic complete-set contract. No native account selection, native runtime create/fallback policy, subscription code, or credential files changed. The shared fenced probe gained canonical handle validation; all 91 protected paths still match the agreed baseline byte-for-byte.

### R1 files

Production:

- `backend/internal/ports/outbound.go`
- `backend/internal/adapters/runtime/runtimeselect/hybrid.go`
- `backend/internal/adapters/runtime/runtimeselect/launch_handles.go`
- `backend/internal/adapters/runtime/conpty/launch_handle.go`
- `backend/internal/adapters/runtime/tmux/launch_handle.go`
- `backend/internal/adapters/runtime/tmux/tmux.go`
- `backend/internal/session_manager/accounts_manager_switch.go`
- `backend/internal/session_manager/accounts_manager_recovery.go`

Tests:

- `backend/internal/adapters/runtime/runtimeselect/launch_handles_test.go`
- `backend/internal/adapters/runtime/tmux/accounts_manager_recovery_test.go`
- `backend/internal/session_manager/accounts_manager_runtime_test.go`
- `backend/internal/session_manager/accounts_manager_slots_test.go`
- `backend/internal/session_manager/accounts_manager_production_e2e_test.go`
- `backend/internal/session_manager/accounts_manager_startup_test.go`
- `backend/internal/session_manager/accounts_manager_switch_test.go`
- `backend/internal/session_manager/accounts_manager_reserved_test.go`

The last two only adapt existing fixtures to the complete-set contract. Documentation changes are this note and `SWITCHING.md`.

### R1 verification

Explicit Fish, backend directory, the same sanitized environment and short physical temporary directory as above. Expanded focused race command:

```text
go test -mod=readonly -p=1 -race -count=3 -timeout=180s \
  ./internal/session_manager ./internal/adapters/runtime/runtimeselect \
  ./internal/adapters/runtime/tmux ./internal/adapters/runtime/conpty \
  ./internal/service/chat ./internal/storage/sqlite/store ./internal/daemon \
  -run '^(TestAccountsManager(Switch|Chat)|TestAccountRecovery|TestProbeFencedRuntime|TestHybridRuntime|TestProductionRuntime)'
```

Exit 0: manager 60.964s, selection wrapper 1.014s, tmux 2.049s, native runtime 1.014s, Chat 23.987s, store 19.369s, daemon 1.020s. This includes requested/waiting cold retry and both outcomes of cancellation racing the durable stopping transaction. SQLite is closed and reopened before each new retry scenario.

First production-selected actual-process run passed, 31.456s: native and fallback switching, plus cold recovery of an unpublished target across both backend changes. It uses the normal factory and detached-host spawner; test-binary re-execution enters the actual native host. The workload supervisor is a locally built executable. An intentional host-entrypoint failure exercises the existing fallback policy. Target authentication remains synthetic.

Expanded actual-process command on the final correction:

```text
go test -mod=readonly -p=1 -race -tags=e2e -count=3 -timeout=240s \
  ./internal/session_manager \
  -run '^TestAccountsManagerSwitchReal(Production|ExitedSupervisorPane|ReservedTargetSurvivesColdRecovery)' -v
```

Exit 0, 80.865s. All 24 leaf scenarios explicitly passed, no skips: 18 production-selected cases and six concrete-runtime regression cases. Both foreign-owner cases preserve the real unrelated process, binding revision, generation, and recovery fence after SQLite reopen. Normal cancellation of fixture background work emitted bounded context-cancelled diagnostics; it did not produce a test failure or race report.

Backend-wide `go build -mod=readonly -p=1 ./...` and `go vet -mod=readonly -p=1 ./...` passed again. `go vet -mod=readonly -p=1 -tags=e2e ./internal/session_manager` passed. Protected byte comparison: 91 paths, zero changes. `git diff --check` passed. Root free space remained about 22 GB; no caches, source, or other sessions were removed.

R1 source and tests are frozen for reviewer 82. Their 16-file SHA-256 manifest is `/tmp/pr-5769-runtime-freeze-79.sha256`, checked from this worktree with `sha256sum -c`. Independent acceptance remains required before deletion resumes. The new complete-set contract and account-only readiness rule require explicit review, not just confirmation that the wrapper now satisfies an interface.

The prior full backend race run was stopped before these source edits. Its observed passing packages are not a full-suite result. All release gates remain open, and coordinated deletion stays held until reviewer 82 confirms this correction.
