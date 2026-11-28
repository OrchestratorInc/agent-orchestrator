# Local orchestrator resume fixes

Trial branch: `fix/orchestrator-resume-bugs`. Base: `95e5d2f741862d8c60948d46090b39151542ef82`.
No upstream PR is opened by this work. Several days of ordinary coding are the next validation step, not an automated test result.

## Changes

### Persistent-host slow-consumer recovery

Controller-facing host writes have a 30-second no-reader-progress deadline. A slow but progressing reader renews a blocked write's deadline; a controller that stops reading cannot indefinitely hold the host mutex and exclusive attachment. Failure follows the existing detach/journal path; the provider process is not killed. Idle time and total replay duration do not consume the next frame's budget.

Read receipts use a second connection to the existing authenticated loopback control listener, scoped by a fresh attachment capability. They are sent only after output is read (not by a heartbeat), coalesced to at most ten receipts per second, and processed independently of the host mutex so replay cannot block them. Closing or replacing the attachment retires its receipt channel. These receipts prove consumption only: the existing durable prompt-completion ACK remains the only authority for discarding the journal. The optional capability preserves attachment to older hosts that do not advertise it.

`TestControllerWritePreservesSlowReaderAndReleasesStoppedReader` exercises a write lasting longer than its initial budget, exact output, idle deadline clearing, and ownership release when reads stop. Retired/unknown progress capabilities are rejected. `TestStalledControllerReleasesOwnershipAndPreservesACPReplay` stalls a real local socket with a large synthetic ACP update burst. Its journal wait reads only appended bytes and has a separate one-minute completion budget for race-instrumented CI. The replacement receives all 12,001 updates in exact content/order and a terminal result, with the same provider PID. The fixture is synthetic; this proves the failure class, not the precise trigger of a historical production incident.

### Rebound-orchestrator identity recovery

A cold-resuming orchestrator can inherit a project conversation whose active branch still belongs to a retired predecessor. The coordinator requires:

- The live session already owns the project conversation and is its sole live orchestrator.
- The old branch has a positively identified, older, terminated orchestrator owner in the same project.
- A provider history probe positively confirms the replacement's own stored native thread exists.
- The resumed provider returns that exact handle and passes native-history admission.

Recovery appends an independent provider boundary through the existing atomic history/controller publication transaction. Old branch IDs, owners, handles, messages and provider namespaces are not rewritten. The transaction rechecks controller ownership, head, history sequence, predecessor identity/retirement and competing orchestrators after provider I/O. An ordinary same-session conflicting handle remains rejected.

Unsupported or ambiguous recovery is not treated as proof. In particular, a same-session reserved-ID mismatch is not automatically healed. Recorded predecessor history with an empty provider identity is explicitly refused.

## Regression coverage

- `TestOrdinaryOrchestratorRecoveryCoordinator`: the actual ordinary-resume coordinator path, including same/cross-harness recovery, reserved predecessor IDs, positive/missing/erroring native proof, competing owners and unchanged historic state.
- `TestReboundOrchestratorRecoveryPublication`: real Chat → Lifecycle → SQLite publication, provider/history failures, wrong returned handle, predecessor revival, competing owner, changed head/history and rollback.
- `TestReboundOrchestratorRecoveryRepeatResumeIsIdempotent`: a second ordinary resume does not append another recovery epoch.
- `TestOrdinaryResumeRejectsSameSessionHandleMismatch`: unproved identity conflicts still fail before provider I/O.
- `TestCommitChatSpawnRecoverySourceReproof`: transactional source-identity and ownership fences.
- Existing restart, replay, handoff and controller-generation tests remain part of full validation.

The coordinator regression fails against the original coordinator implementation and passes after the fix. Complete backend unit tests, full race coverage, build, vet and pinned lint are run locally before trial activation. Heavy Chat/SQLite race packages may be sharded, as upstream CI does; all discovered top-level tests must be included exactly once. Frontend typecheck/unit tests and real Electron/daemon startup are checked with a matched frontend/backend checkout.

## Trial and rollback

Run the matched desktop checkout in development mode so upstream auto-updates cannot replace the trial. Keep daemon binaries in commit-named directories rather than overwriting the installed application. A separate local desktop shortcut starts this trial with the existing project/session data only after the installed app has stopped.

Before activation, take a consistent SQLite backup and an Electron-profile backup. Keep the packaged app installed and preserve all worktrees, journals and provider-native histories. Do not delete live state or blindly restore an old database after several days of new work: preserve the post-trial state first and assess migration compatibility.

During the trial, check ordinary messages, worker reports, reconnects and desktop restarts. A full OS reboot and multiple days of coding remain real-world acceptance checks. If an error recurs, retain the exact session ID, request ID, current executable/commit, controller generation and daemon log; do not assume every generic 500 has the same cause.
