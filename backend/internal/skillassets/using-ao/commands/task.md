# ao task

Hand a task to Orchestrator and learn from the exit code whether the agent
really picked it up. Use it from scripts or from a coding agent outside
Orchestrator, for example when a user says "send this task to Orchestrator".

By default the project's orchestrator receives the task, plans it, and starts
workers. With `--worker`, a single worker session takes it directly; use that
for a small, one-off change.

## Syntax

```
ao task start "<brief>" [flags]
ao task status <task-id> [--json]
ao task wait <task-id> [--timeout <duration>] [--json]
```

## Flags for `ao task start`

| Flag | Meaning | Default / Required |
|---|---|---|
| `--project string` | Project id | `AO_PROJECT_ID`, then the registered project that contains the current directory |
| `--worker` | Give the task to one worker instead of the orchestrator | Off |
| `--agent string` | Agent for the worker (only with `--worker`) | The project's worker agent |
| `--base string` | Base branch for the worker (only with `--worker`) | The project's default branch |
| `--wait` | Wait until the task has started, failed, or needs the user | Off |
| `--timeout duration` | How long `--wait` waits | `120s` |
| `--json` | Print `{status, taskId, sessionIds, reason, fix}` | Off |

Without `--worker`, `--agent` and `--base` are a usage error (exit `2`): the
orchestrator chooses agents and branches for the workers it starts. Set the
project's defaults with `ao project set-config <id> --worker-agent <agent>
--default-branch <branch>` instead.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | With `--wait` or `ao task wait`: the task started (`running` or `done`). A one-shot read (`ao task status`, or `start` without `--wait`) also exits `0` while the task is `starting` or `queued`, so read `status` before treating it as started. |
| `1` | The task failed, `--timeout` ran out before it started, or a status read failed. A failed status read (for example a 503 on the final check) does not mean the task failed or never started: if `taskId` is set, check that id with `ao task status <task-id>` before submitting again. |
| `2` | Usage error: fix the command (for example pass `--project`). Prints no JSON. |
| `3` | The task is waiting on the user, such as for an approval. Ask the user; do not start the task again. |
| `4` | `--wait` ran out while the task was still `queued` behind the agent's current turn. It is accepted and will run: do not start it again. Wait longer with `ao task wait <task-id>`. |

`status` in the JSON is `starting`, `queued` (accepted; the agent runs it after
its current turn), `running`, `done`, `failed`, or `needs_input`. A task counts
as started once its turn has finished, or has stayed running for 5 seconds. Use
a `--timeout` of at least `10s`. A status read that fails for a passing reason
(the daemon briefly unreachable, a 5xx) does not end a wait; it keeps polling
within `--timeout`. A task that was just created keeps its `taskId` even if its
first status read fails.

`--wait` and `ao task wait` confirm that the task **started**, not that it
finished: they return once its turn has run for 5 seconds. An approval the
agent asks for after that is not seen by that call. Keep calling
`ao task status <task-id>` (or `ao task wait`) until `status` is `done`,
`failed` or `needs_input`, and act on `needs_input` when it appears.

That polling works for Chat sessions, which report turns. It does not work for
a terminal (TUI) task: its one-shot status stays `starting` once the agent is
back at its prompt, so a task that finishes between polls never reads `done`.
For a terminal task, check the session itself with `ao session get <id>`.

On the orchestrator path, the status covers the workers the orchestrator
started for the task, not just its own turn:

- `needs_input` if any of those workers needs the user;
- `running` while the orchestrator or any worker is still working (the reason
  names the busy workers);
- `failed` if a worker failed after the orchestrator finished its turn;
- `done` only when the orchestrator's turn and every worker have finished.

Each worker is read at the turn it is running now (otherwise its latest), so
follow-up work the orchestrator sends it (such as review findings) keeps the
task `running`, and an approval pending on the running turn reads
`needs_input` even when a follow-up is queued behind it. A worker that cannot
be read keeps the task `running` rather than letting it read `done`. If the
orchestrator's turn was retried, only the current attempt's workers can fail
the task. Workers that already existed before the task are not tracked: nothing
records which of them the orchestrator messaged for it.

A worker in a terminal (TUI) session never reads `done`, so a task with one
stays `running`; check that worker with `ao session get <id>`.

## Reading the result

- `fix` is set when the cause is known. For `needs_input` it names the session
  that owns the pending request: the orchestrator when its own turn asks, or
  the worker when a tracked worker asks. Answer in the session `fix` names;
  don't assume it is a worker. When the agent is not signed in, it is
  the agent's own login command, for example ``run `claude auth login`, then
  retry``. Show it to the user; signing in is theirs to do.
- `taskId` is the worker's session id, or `<orchestrator-session>:<turn>` for a
  task sent to a Chat orchestrator. A terminal (TUI) orchestrator's task id is
  its session id alone. `taskId` is empty when no task id could be reported. That
  is not proof that no task exists: if the reason says the daemon accepted the
  task without a turn id, or that delivery could not be confirmed, the task may
  still run, so check the session (`ao session history <id>`) before starting it
  again.
- `sessionIds` lists the session that runs the task and any workers the
  orchestrator started for it; their state is part of `status`.
- A terminal session reports no turns and is never `done`. If `ao task wait`
  sees the agent working and then back at its prompt, it reports `running`
  (started), because that work may predate the task; check the session itself
  with `ao session get <id>` to see what it did.
- If the reason says delivery could not be confirmed, check `ao session ls` or
  `ao session history <id>` before starting the task again, so it does not run
  twice.

## Examples

```bash
# Send a task to the project's orchestrator and wait until it has started
ao task start "Fix the failing login test" --wait --json
```

```bash
# A small one-off change for a single worker on a specific agent
ao task start "Bump the lodash version" --worker --agent codex --wait --json
```

```bash
# Check on a task later
ao task status my-app-orchestrator:7a1f0c2e --json
ao task wait my-app-3 --timeout 5m
```
