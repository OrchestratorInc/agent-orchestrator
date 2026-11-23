# ao send

Send a message to a running agent session without killing and respawning it.

Plain `ao send` delivers a separate message. If a Chat session is busy with a
running turn, the message is queued and delivered as its own turn after the
current turn ends; it does not reach the running turn. To correct or redirect
an agent mid-turn, use `--steer`.

## Syntax

```
ao send [flags]
```

## Flags

| Flag | Meaning | Default / Required |
|---|---|---|
| `--message string` | Message body | Required unless `--recover-only` |
| `--session string` | Session id | Required |
| `--steer` | Add guidance to the active Chat turn, or start a normal turn when idle | Optional |
| `--client-message-id string` | Stable delivery handle for steering and safe retries | Generated automatically with `--steer` |
| `--recover-only` | Recover the outcome for an existing delivery handle without resending | Requires `--steer` and `--client-message-id` |
| `--wait` | After sending, block until the session next finishes a turn or asks for an answer | Optional; not with `--recover-only` |
| `--timeout duration` | With `--wait`, give up after this long and exit 124 | `10m` |

## Waiting for the reply

`--wait` makes one call out of "message a session and wait for its answer". It
notes the session's last activity before it sends, and accepts a settled state
only when it is newer than that. A plain `ao session wait` started after
`ao send` can return at once on the idle state from before the message,
because a message can reach the agent after `ao send` returns.

```bash
ao send --session mer-3 --message "Run the tests and report the result." --wait --timeout 20m
```

Exit codes are those of `ao session wait`: 0 the turn finished, 5 the session
asks for an answer or an approval, 124 timed out. Read the reply with
`ao session get mer-3 --transcript 5`.

Send to a session that is idle. If it is in the middle of a turn, a plain
message would be queued behind that turn and the wait would end with the wrong
turn, so `--wait` refuses with exit 1 and sends nothing: run
`ao session wait <id>` first, or add `--steer`.

Exit 2 means nothing was sent. If the wait itself fails after delivery, the
error starts with "the message was sent": do not send it again; check the
session with `ao session wait` or `ao session get`.

## Steering and recovery

`--steer` uses one atomic daemon operation. If the session has an active Chat
turn, the message is added to that turn. If the session is idle, the message
starts a normal Chat turn. The state cannot change between those decisions and
leave the message queued as a separate correction.

Every steering request has a client message id. Supply one explicitly when a
caller must retain the handle across retries:

```bash
ao send --session mer-3 --steer --client-message-id correction-42 --message "Focus only on the backend."
```

If the command reports an uncertain outcome or a transport failure, preserve
the delivery handle printed in the error. Recover the existing result without
contacting the provider again:

```bash
ao send --session mer-3 --steer --recover-only --client-message-id correction-42
```

Do not invent a new client message id after an uncertain result. Reusing the
original id lets the daemon return the durable steering receipt or normal turn
without delivering the message twice. Recovery confirms the recorded delivery
outcome, not that the agent acted on the guidance.

## Examples

```bash
# Correct a running agent mid-turn (starts a normal turn if it is idle)
ao send --session mer-3 --steer --message "Focus only on the backend; ignore frontend files."
```

```bash
# Give the agent new instructions mid-task
ao send --session mer-3 --steer --message "The issue is in session_manager.go line 142, not in the CLI. Investigate there."
```

```bash
# Send a separate message (queued behind a running Chat turn)
ao send --session mer-3 --message "When you finish, also update the changelog."
```
