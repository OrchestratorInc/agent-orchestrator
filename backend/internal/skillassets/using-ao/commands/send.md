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
