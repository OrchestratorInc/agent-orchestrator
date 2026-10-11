# Stakpak recovery: native ownership patch proposal

PR6550 remains draft and unregistered. This document specifies a native source
patch and regression acceptance criteria; it is not an applied native patch,
compiled replacement binary, passing runtime result or production integration.
No upstream issue/PR has been posted. The stock release remains unchanged.

## Source and live evidence

The latest release was rechecked through GitHub on the recovery VPS:
[v0.3.88](https://github.com/stakpak/agent/releases/tag/v0.3.88), published
2026-06-10. Release source is `d3eba6f502a79b993b826ef3ab4065eec9e38dfa`.
[Comparison with current main](https://github.com/stakpak/agent/compare/v0.3.88...760cd2b5984d29c2d513bb15ca33e995fae45f17)
contains no foreground executor, cancellation, ACP or interactive restore fix.
The pinned executable SHA-256 remains
`5329d4d44afbc9df57d4c99fd9f066750bce81c73d0e459171e6610640321663`.

The [previous stock-native receipt](stakpak-tui-hold-2026-10-11.md) remains FAIL:
a child-start witness preceded Escape; PID932367/start78005410 survived and
wrote the delayed file. Restore persisted two system messages. Recovery did
not repeat that known failing Escape experiment with identical source/config.

Source trace: Escape broadcasts cancellation; `run_tool_call` sends an MCP
cancel notification and immediately returns a cancelled result. The foreground
executor creates a separate process group but calls `child.kill().await` only
on its immediate shell in both cancellation branches and the timeout branch.
This trace supports the ownership defect; it does not prove exactly which
forwarding stage caused every historical surviving PID.

## Proposed native patch A: foreground command ownership

Patch these release-pinned locations:

- [libs/mcp/server/src/local_tools.rs:1328–1520](https://github.com/stakpak/agent/blob/d3eba6f502a79b993b826ef3ab4065eec9e38dfa/libs/mcp/server/src/local_tools.rs#L1328):
  replace raw foreground `Child` ownership with a request-owned command job.
- [libs/shared/src/task_manager.rs:20–75](https://github.com/stakpak/agent/blob/d3eba6f502a79b993b826ef3ab4065eec9e38dfa/libs/shared/src/task_manager.rs#L20):
  consolidate foreground/background containment in a shared job implementation;
  do not copy the current external `kill -0`/numeric-PID helper unchanged.
- [cli/src/commands/agent/run/tooling.rs:110–152](https://github.com/stakpak/agent/blob/d3eba6f502a79b993b826ef3ab4065eec9e38dfa/cli/src/commands/agent/run/tooling.rs#L110):
  keep cancellation pending until owned cleanup is acknowledged.
- [libs/mcp/proxy/src/server/mod.rs:350–385,822–878](https://github.com/stakpak/agent/blob/d3eba6f502a79b993b826ef3ab4065eec9e38dfa/libs/mcp/proxy/src/server/mod.rs#L350):
  preserve mapped request ownership through cancellation and acknowledgement.

Concrete implementation contract:

1. Create `OwnedCommandJob` in the shared native library. Capture the spawned
   leader identity before any `try_wait`/`wait` can reap it; retain the Unix
   PGID from `process_group(0)`. On Linux also record kernel start time and
   ownership snapshots for descendants that change group. Use direct OS APIs,
   not shell `kill` commands or command-text process searches. On Windows
   assign the suspended child to a kill-on-close Job Object before resuming.
   Other platforms need a separately tested ownership implementation.
2. A request/job registry owns the job independently of the stream future.
   The job owns the child and output readers. A cancellation token requests
   shutdown; dropping/aborting the tool future transfers cleanup to the owner
   task rather than dropping an unguarded child. Job registry lifetime must
   outlive proxy request cancellation. On server shutdown drain this registry
   before declaring shutdown complete.
3. Implement `cancel_and_reap(deadline)` as an idempotent operation: serialize
   against normal completion; stop accepting new work; snapshot owned members;
   signal TERM to the owned group/job; allow a bounded grace; escalate KILL;
   reap the immediate child and observe disappearance of captured identities.
   Rediscover still-provable descendants before escalation. Check process start
   identities immediately before signalling; refuse recycled identities. Do
   not forget survivors merely because the shell leader exited or was reaped.
4. Replace all three immediate-child `kill().await` sites at local_tools.rs
   1482,1493,1512 with this operation. Route stream read errors, transport
   disconnect, tool future drop and MCP-server shutdown through the same owner.
   Normal completion must finalize command-owned jobs under an explicit native
   policy; background jobs must be explicitly transferred to the native task
   registry, never left as accidental foreground orphans.
5. Cleanup failure is an explicit `COMMAND_CLEANUP_FAILED` outcome with bounded
   diagnostics and ownership IDs. Do not return `COMMAND_CANCELLED`, settle
   the native turn as safe, or accept the next command before cleanup completes.
   Keep native permissions/default agent/tools unchanged.
6. The CLI/proxy must receive a cleanup completion acknowledgement for the
   mapped request. An MCP cancelled notification alone has no such guarantee;
   do not assume `await_response` will return after a cancelled request. Add a
   native client/server negotiated request-scoped cleanup acknowledgement (or
   retain an independently awaitable job completion channel) and update both
   proxy hops. Fence it by native session + original request ID + job identity.
   On acknowledgement timeout expose failure; do not forge success. The TUI
   may show “cancelling” immediately but must not admit another execution yet.
7. Do not implement group cleanup as an unguarded `Drop` using a stored numeric
   PGID after `wait`: PID/PGID reuse can target unrelated work. A supervisor
   retains ownership until cleanup finishes; drop is a request to that owner.
   Descendants that daemonize into new sessions require verified containment
   or an explicit unsupported outcome, not an untested universal-tree claim.

### Required native regression cases

Use the actual foreground executor with request cancellation, not a mock kill
helper. Run on upstream CI; these cases were specified, not executed here.

| Trigger | Fixture and acceptance |
| --- | --- |
| Cancel with no timeout | Nested shell records PID/start, child-start, sleeps, then writes a late marker. Cancel after witness; every owned identity is gone and marker remains absent past deadline. |
| Cancel with timeout set | Same fixture reaches the other `select!` branch; identical ownership assertions. |
| Timeout | Timeout fires before delayed write; assert bounded cleanup and timeout result. |
| Future abort / disconnect | Abort pending stream or disconnect MCP client after child-start; independent job owner completes cleanup. |
| Leader exits first | Parent exits while grandchild retains pipes; retain proven ownership and stop grandchild without signalling a recycled leader. |
| New group / TERM ignored | Child changes PGID or ignores TERM; bounded KILL and identity checks; unrelated control remains alive. |
| Deliberate new SID | Either tested containment works or report cleanup unsupported/failed; never silently pass. |
| Concurrent cancel/exit | Repeated cancel and natural exit race; one reap, one terminal outcome, no recycled PID signal. |
| Same native session | After successful cleanup, submit another real edit; same native conversation/composer continues. |
| Background task | Explicit task transfer remains tracked; cancelled/shutdown task cleanup has its own acknowledgement. |

For every executing case record child-authored witness, kernel identity and late
write deadline. A cancelled label, returned error or transport write is not the
assertion. Include an unrelated live control before, immediately after and past
the delayed-effect deadline. Windows/macOS require their own runner proof.

## Proposed native patch B: additive launch instruction renewal

[mode_interactive.rs:469–526](https://github.com/stakpak/agent/blob/d3eba6f502a79b993b826ef3ab4065eec9e38dfa/cli/src/commands/agent/run/mode_interactive.rs#L469)
loads messages then unconditionally inserts supplied system text.

Introduce a native launch-context layer separate from durable conversation
history: compose native defaults + existing project/user instructions + private
launch context at model-request construction. Do not persist ephemeral launch
context into checkpoints, replace default prompts, or rewrite user/assistant/
tool messages. Persist only metadata needed to identify the native context
layer; never identify owned text by substring matching arbitrary user messages.
For historical untagged launch messages provide an explicit native migration or
refuse ambiguous renewal; do not delete system messages heuristically.

Regressions: fresh context consumed independently of visible task; restore the
same real conversation with new context; old token absent/new token present;
three restore cycles retain a single launch layer; AGENTS.md and native default
instructions unchanged. Missing/unreadable context fails before submission.
Fresh-only AO scope can hard-refuse Restore instead, after patch A is released
and qualified; patch B is not a prerequisite for that narrower scope.

## Alternatives checked and why AO cannot repair the stock coding surface

- Fresh/non-restorable TUI removes instruction renewal claims but still permits
  the broken foreground executor. Ctrl+C is AttemptQuit; Escape is the normal
  turn cancel. Quitting the CLI is not same-session recovery.
- `up --foreground` runs Autopilot infrastructure, not interactive TUI with a
  different command lifecycle. Shell-mode retry can duplicate the still-live
  original command; it is not cleanup.
- `--tool` filters model-advertised definitions; native dispatch receives the
  full tool list. Empty selection means no filtering. External MCP is additive;
  reserved native server names cannot be replaced. Therefore an AO command
  broker cannot enforce exclusion of the broken native executor.
- ACP runs the same tool executor. Denying every permission can exclude normal
  command/write/subagent calls but cannot deliver the requested workspace-edit
  coding surface. Auto-approved tools still need separate qualification.
- [ACP load_session:1779–1799](https://github.com/stakpak/agent/blob/d3eba6f502a79b993b826ef3ab4065eec9e38dfa/cli/src/commands/acp/server.rs#L1779)
  assigns a UUID without existence/workspace validation or history replay.
  A future ACP patch must load authoritative checkpoints, reject unknown or
  foreign history, replace in-memory context and replay transcript before
  reporting success. Capability advertisement is insufficient.
- AO Linux PTY Close already tears down session-owned processes. That is Kill:
  it ends the native process and cannot replace non-destructive turn cancel.
  The agent adapter receives no native per-tool job ownership/acknowledgement.
  Killing all non-leader SID members could kill persistent native MCP workers
  and unrelated tool jobs in the same session. Process-tree guessing at this
  boundary cannot identify the cancelled request safely.

## Recovery outcome and next prerequisite

No production source registration, migration or public capability was added.
Existing hard launch/hooks/restore errors remain. No credentials/native history
were edited, no patched vendor binary was installed and no upstream publication
was attempted. No provider calls or additional native Escape runs were needed
once the source-grounded alternatives were falsified.

Actual AO UI screenshots, same-session cancellation and independent AO Kill
controls remain **NOT_RUN/unavailable for Stakpak**. Launching the guarded
candidate would require weakening the admission gate. There is no synthetic
image or native-terminal substitute. An independently successful Linux Kill
would not discharge the surviving-session cancellation prerequisite.

Remaining external prerequisite: a stock released native ownership and cleanup
acknowledgement repair, or an enforceable supported executor replacement
boundary. Then qualify fresh-only TUI first: real workspace edit, private
instructions, permissions, same-session cancellation/second turn, independent
AO Kill/control and actual Electron screenshots. Resume/ACP remain excluded
until separately repaired and proven. Routine validation runs only in remote
AO PR CI; the proposed upstream implementation/regressions are uncompiled.
