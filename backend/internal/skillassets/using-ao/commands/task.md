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
| `--agent string` | Agent for the worker(s) | The project's worker agent |
| `--base string` | Base branch for the worker(s) | The project's default branch |
| `--wait` | Wait until the task has started, failed, or needs the user | Off |
| `--timeout duration` | How long `--wait` waits | `120s` |
| `--json` | Print `{status, taskId, sessionIds, reason, fix}` | Off |

On the orchestrator path, `--agent` and `--base` are passed to the
orchestrator as instructions for the workers it starts.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | The task started (running or done). Without `--wait` it may also still be `starting`. |
| `1` | The task failed, or `--timeout` ran out before it started. |
| `2` | The task is waiting on the user, such as for an approval. A usage error also exits `2`, but prints no JSON. |

`status` in the JSON is `starting`, `running`, `done`, `failed`, or
`needs_input`. A task counts as started once its turn has finished, or has
stayed running for 5 seconds. Use a `--timeout` of at least `10s`.

## Reading the result

- `fix` is set when the cause is known. When the agent is not signed in, it is
  the agent's own login command, for example ``run `claude auth login`, then
  retry``. Show it to the user; signing in is theirs to do.
- `taskId` is the worker's session id, or `<orchestrator-session>:<turn>` for a
  task sent to a Chat orchestrator. A terminal (TUI) orchestrator's task id is
  its session id alone. `taskId` is empty when the command failed before a task
  existed.
- `sessionIds` lists the session that runs the task and any workers the
  orchestrator started for it.
- A terminal session reports no turns. `ao task wait` reports `done` only if it
  saw the agent working and then back at its prompt.
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
