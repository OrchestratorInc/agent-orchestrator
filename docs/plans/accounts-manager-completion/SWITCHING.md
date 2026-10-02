# Session switch and removal completion

Contract: a selected target is pending until its binding is committed, and is ready only after the replacement controller acknowledges readiness. One session's operation must not change another session, its future-session default, or native credentials. A source credential failure never prevents choosing a usable target.

Current disposition: R4 is independently CLEAR in `rpt_3b54e7fc-bd40-4e04-951f-edc73173d20f`. The next deletion slice is implemented, locally verified, and frozen in [REVIEW-82-DELETION.md](REVIEW-82-DELETION.md). Public controls/UI remain held for independent deletion CLEAR. The correction narratives below preserve historical holds; they are not the current disposition.

Implementation order:

1. Add a separate durable switch journal. Admit one operation per session with an idempotency key and expected binding revision. Capture controller ownership without credentials. Cancellation wins only before source stopping. Commit the binding and operation phase in one transaction.
2. Include durable stop fences in runner reconciliation. Reserve the existing session input gate, use supported drain or explicit interrupt, prove the source stopped, commit, then relaunch through the existing opt-in managed route. Readiness requires the intended controller generation. Preserve a recovery gate when ownership or stop/start results are ambiguous.
3. Reconcile unfinished operations before startup input admission. Never replay a prompt or silently resume a different account. Expose pending, committed, failed, and recovery-required states separately, with scoped cancellation/retry.
4. Add an in-use removal journal and an explicit affected-session confirmation. Refuse new selections of a deleting account, stop confirmed affected controllers, tombstone credentials, then clear only that account's default. Recovery finishes cleanup without choosing replacement accounts.
5. Connect initial/session controls and verify the real desktop workflows after backend boundaries pass.

The existing native account-switch modules and Subscriptions remain untouched. Reusing the shared session input fence, same-controller relaunch boundary, and supported handoff helpers does not authorize calling the native global account switch.

- [x] S1: Journal idempotency, source ownership, cancellation versus stopping, atomic binding commitment, durable request fences, and restart reads pass storage tests.
  EVIDENCE: Fish, backend, isolated environment. Focused race checks passed (store 16.143s, service 2.019s, migrations 12.874s). Expanded journal tests passed three repetitions (15.272s), including an injected journal-write failure that rolls back the binding and clock, close/reopen recovery before and after commitment, source-generation rejection, cancellation/stop contention, and false-readiness rejection. The runner's full race suite passed after adding durable stop fences (2.856s). Controller execution and real workflows remain unimplemented gates below.

- [ ] S2: Controller integration honors explicit timing, blocks input during commitment, confirms readiness, isolates other sessions, and preserves recovery obligations on uncertainty.
  EVIDENCE: Controller execution and the daemon handoff adapter are implemented locally. The correction suite passed three race repetitions: manager 37.454s, Chat 20.159s, store 15.732s, tmux 1.019s, daemon 1.023s. Coverage includes the four independent-review findings, Manager-to-Chat queue adoption, stale acknowledgement rejection, exited-pane cleanup, and actual isolated tmux shutdown. Independent confirmation, public routes, and integrated desktop verification remain open; this gate is not met by package tests.

- [ ] S3: Restart and cancellation tests prove no prompt replay, lost committed choice, stale authorization, or false ready state.
  EVIDENCE: Startup retains pre-stop requested/waiting intent as cancellable, and quarantines later interrupted phases as recovery-required without automatic launch. Retry retains the retired owner separately from the next generation, reconciles both runtime identities, rotates authorization, and binds acknowledgement to the observed generation. Close/reopen checks cover sync, route preparation, process creation, and metadata publication failures, including a surviving reserved process. Empty-conversation proof persists before stopping and across cold retry. Per-turn queue obligations survive cancellation until dispatch or explicit message cancellation. Independent confirmation and real application restart evidence remain open.

- [ ] S4: In-use deletion, concurrent refresh/switch/add, partial cleanup, and retry/restart preserve revocation and only clear the removed account's default.
  EVIDENCE: Durable coordinator, queue/input fences, exact-owner teardown, runner revocation, pre-stop cancellation, cold recovery, inactive/dormant impact, and no-fallback cleanup are implemented. All six local deletion gates pass; 105 final production-process executions and 13 affected full race packages pass on the frozen 42-file delta. All 91 protected paths remain unchanged. Independent deletion CLEAR and public application integration remain required; see REVIEW-82-DELETION.md.

## Deletion implementation contract

### Cancelled retry admission correction, review R4

Reviewer 82 closed R3's reported input-ownership and no-replay sequences in `rpt_46af3a2b-e8bd-4053-8a9e-d5d432b6745f`. R4 holds deletion: a retry can retain an old waiting snapshot while cancellation wins, then restart through the interval between publishing idle and removing the cancelled run.

The four-case deterministic regression reproduced an additional probe and interrupt after successful cancellation. The correction rejects cancelled runs under the admission mutex and keeps cleanup from exposing an idle run before fence release and removal. Completion-channel closure and publishing idle now share the mutex. Final affected checks pass three race repetitions across seven packages; all 87 actual-process scenarios pass, including twelve R4 cases. Shared switching/exit regressions, full backend build/vet, tagged test vet, and all 91 protected paths pass. The exact four-file delta and 24-file supporting freeze are recorded in [the R4 handoff](REVIEW-82-R4.md). Work stops at this freeze; deletion remains held until reviewer 82 returns CLEAR.

### Terminal handoff ownership correction, review R3

Reviewer 82 closed R2's reported retry ordering and identified an earlier input boundary: cold pre-commit recovery can send Stop-now through a reusable handle before probing its owner. Deletion remains held. The full R2 freeze is preserved in `/tmp/pr-5769-pre-handoff-correction-79.tar.gz` with SHA256 `96a5a32bc4dbe9ce60ffc506daf36c5c083673cdcb6b98cb3f384a511b3140e1`.

1. Reproduce non-exited source recovery with foreign and uncertain owners on both slots after SQLite reopen. Assert zero interrupt/input, destruction, launch, and revision rotation. Cover initial execution as well as cold retries.
2. Gate the account-specific terminal handoff with fresh exact-owner proof. Confirmed absence skips input and can continue recovery; exact alive permits the existing Stop-now behavior. All unknown outcomes stop before input. Keep shared native/interface handoff semantics unchanged.
3. Verify valid-owner controls and actual replacement processes, repeat focused race and actual-process checks, then freeze the correction for reviewer 82. Do not resume deletion without confirmation.

The ownership guard and no-replay rule pass 36 deterministic cases and 12 real-process cases. The cancellation-write race reproduced on both handle forms and was corrected without making post-stop operations cancellable. Final three-repeat focused race checks and all 75 actual-process executions pass, as do shared switching/exit regressions and full backend build/vet. The exact 22-file freeze and unchanged 91-file protected inventory are recorded in [the R3 handoff](REVIEW-82-R3.md). Deletion remains held for independent confirmation.

### Destructive retry correction, review R2

Report `rpt_fb981ee8-c50f-4074-ab7a-75346f5007be` closes R1 but identifies an ownership takeover between a failed destroy and its retry. Deletion remains held. The prior freeze and its manifest are preserved separately in `/tmp/pr-5769-pre-teardown-correction-79.tar.gz`.

1. Reproduce a replacement owner arriving after the first destroy error, for both runtime slots. Require no further destruction, launch, or revision rotation, including retry after database reopen.
2. Require fresh exact ownership proof before the shared helper retries destruction. Unknown ownership stops immediately. Preserve completion when a failed command already removed the same owner, and retry only when that owner is still proven alive.
3. Verify real replacement processes survive, rerun the focused race suites and shared native-switch regressions, then freeze the correction in a new manifest for reviewer 82. Continue broad verification separately. Do not resume deletion without independent confirmation.

R2 is frozen for confirmation. Failed-first helper, source/target, and actual-process regressions now pass. Three-repeat focused race checks and all 39 actual-process executions pass, including simultaneous owners on both runtime slots. Shared harness-switch race checks, build/vet, formatting, and all 91 protected-file comparisons pass. Exact files, results, limitations, and the new 19-file manifest are in [the R2 handoff](REVIEW-82-R2.md). No deletion dependency gate is cleared by this local result.

### Production wrapper correction, review R1

Reviewer 82 closed the reported F1-F3 sequences in `rpt_b8462ce0-cd99-422a-9f66-e3982339904f`. Deletion remains held on R1: the production-selected runtime omits the launch identity capability. Its create policy can use either the versioned direct host or the unprefixed fallback. A single guessed handle cannot support cold recovery.

1. Reproduce admission failure using the actual production runtime factory before changing implementation.
2. Extend the new optional resolver contract to return the complete, deterministic set of possible launch slots. Forward both backend forms through the wrapper, reject unsupported or ambiguous identities, and reconcile every slot with exact generation evidence before retry rotation. Do not change native creation/fallback policy.
3. Test direct and fallback handles, unknown/foreign generations, failed publication and cold recovery through the production factory, with real supervisor processes. Rerun focused race checks and freeze for independent confirmation before deletion proceeds.

The full backend race run was interrupted to make this correction on a stable tree. It is not a passing full-suite result. Protected native-switch and subscription files remain outside this correction.

R1 was frozen and subsequently confirmed by the independent review recorded above. The production-factory admission regression failed before correction and passes afterward. Expanded three-repeat focused race checks passed across seven packages, and 24 actual-process scenarios passed with no skips. This includes native/fallback switches, unpublished targets across both backend changes, real foreign-owner preservation, and prior concrete-runtime recovery checks. Build, vet, and all 91 protected-file comparisons passed. [The R1 handoff](REVIEW-82-CORRECTIONS.md#production-runtime-correction-r1) retains the contract, changed files, commands, results, and historical freeze manifest. S2/S3 and deletion remain held on R2 and the broader acceptance requirements.

Capture the affected bindings and controller owners durably before stopping anything. Require explicit in-use confirmation against a current binding revision. Once removal begins, refuse new selections and pending switches into that account. Runner reconciliation must revoke affected routes even while credential cleanup is unavailable. Stop only captured controller generations, retain uncertainty for retry, and never select a replacement. A durable credential tombstone precedes clearing that account's default and acknowledging completion. Repeated requests and daemon restart finish the same removal obligation.

The controller path now uses a distinct account handoff gate in Chat. Queued turns remain paused across source stop and target construction; they resume only after acknowledgement or pre-stop cancellation. Starting a new conversation with queued work is refused before stopping the source. Shared native/interface handoff behavior is unchanged.

## Midpoint recovery review

Reviewer 82's four findings reproduced before correction:

- `TestAccountsManagerSwitchCanRetryAgainAfterRotatedLaunchFails`: second retry remained `recovery_required`, code `CONTROLLER_CHANGED`.
- `TestAccountsManagerChatStartupPreservesUndispatchedQueue`: cold recovery changed an undispatched queued turn to `failed`.
- `TestAccountRecoveryDistinguishesAbsentServerFromUnknownProbe`: absent server and absent socket returned `unknown`; permission and missing-client negative controls remained unknown as required.
- `TestAccountsManagerSwitchUntouchedSessionUsesProvenFreshLaunch`: an ordinary untouched session committed the target and then failed with `TARGET_START_UNCONFIRMED`.

Corrections and exact files are recorded in [the review handoff](REVIEW-82-CORRECTIONS.md). Additional adversarial regressions reproduced cancellation followed by cold queue loss, use of the wrong generation during retry, stale readiness acknowledgement, and an exited source pane blocking replacement. The typed absent-server signal remains distinct from unknown ownership or transient probe errors. S2/S3 and coordinated deletion remain held pending independent confirmation.

The re-review closed the original four reported sequences but retained F1-F3 for surviving reserved runtimes, actual retained-pane ownership, and cancellation after startup. All three reproduced before correction. The latest local checks cover actual supervisor/tmux replacement and cold recovery, plus synthetic crash cuts and queue adoption. Final focused repetition evidence and the next independent disposition belong in the handoff; deletion remains held.

- [ ] S5: The real desktop verifies initial choice, ordinary two-action switch, pending cancellation, failure recovery, and explicit in-use deletion with no native-path changes.
  EVIDENCE: pending
