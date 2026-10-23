# Orchestration: spawn, list, send, report, kill

All five commands talk to the AO control plane with this session's rotating
worker credential. `spawn`, `list`, `send`, and `kill` require the
`worker:orchestrate` scope, which only orchestrator sessions hold; `report`
requires a parent orchestrator. A command run without the required scope fails
with `SCOPE_REQUIRED`.

## ao spawn — create a worker session

```
ao spawn --name "<label>" --prompt "<clear worker task>" [--agent <harness>] [--mode standard|trusted] [--branch <branch>]
```

- `--name` (required): the label the human sees in the sidebar. Keep it short
  and specific. Twenty characters or fewer is ideal; 100 characters is the
  hard cap. Count it before running the command; an over-long label wastes a
  turn on a rejection.
- `--prompt` (required): the worker's complete task. Write it like a brief for
  a competent engineer with no other context: goal, constraints, and the
  expected outcome (usually a pull request). Before you name a specific file,
  class, or path, confirm it exists in the checked-out repository (`ls`,
  `find`, `grep`). A prompt pointing at a file that does not exist wastes the
  whole worker.
- `--agent` / `--harness`: `claude-code`, `codex`, `cursor`, or `opencode`. If
  omitted, the project-configured worker harness is used; when the project has
  no worker harness, the control plane falls back to `claude-code`.
- `--mode`: `trusted` (default) or `standard`.
- `--branch`: the git branch the worker works on. Use it only when the human
  asks for a specific branch, or to resume work a stopped worker already
  pushed (`ao list --all` shows each worker's branch). A branch that already
  exists on the remote is checked out with its commits; a new name starts from
  the default branch. It cannot be the project's default branch, the session
  prefix, a base branch of one of the project's repositories, or a name that
  reads as another ref (`refs/...`, `origin/...`, `FETCH_HEAD`); a refused
  name fails with `invalid_branch` and says why. Omit it for ordinary tasks.
  A branch belongs to one live worker at a time. To resume another worker's
  branch, first ask that worker to commit and push its work (unpushed work is
  lost when it is killed), then `ao kill` it; spawning on a branch a live
  worker still holds fails with `branch_in_use` naming that worker. If it
  already opened a pull request, tell the new worker to run
  `ao claim-pr <number>` instead of opening another one.

Prints `spawned <session-id> (<status>)`. The worker provisions its own
sandbox, checks out the project repository on a fresh branch (or the
`--branch` you named), and starts on the prompt — typically live within ~10 seconds. You do not need to wait: a `send`
issued before it is ready is queued and delivered on startup.

Errors: `agent_provider_required` means the org has no validated credential for
that harness — pick a different `--agent` or report the blocker to the human.
`SANDBOX_QUOTA_EXCEEDED` means the org hit its sandbox cap — kill finished
workers first.

## ao list — inspect your workers

```
ao list [--json] [--all]
```

Shows the workers this orchestrator spawned: id, name, harness, branch,
status, whether the worker runtime is connected, its pull request (number
and CI state) once one exists, and the files in its artifact directory once it
has written any. Terminated workers are hidden unless `--all`.

`--json` includes per-PR detail (`prs`: url, number, state, ci, review,
mergeability, source/target branch), the worker's artifact files
(`artifactCount`, `artifactsUpdatedAt`, and `artifactFiles` with path, kind,
size, and updatedAt for the most recently changed: at most 10, fewer when
their paths are very long, with `artifactFilesTruncated` set when there are
more), plus `activityState` and
`isTerminated`.

Status vocabulary: `working`, `idle`, `needs_input`, `pr_open`, `draft`,
`ci_failed`, `review_pending`, `changes_requested`, `commented`, `approved`,
`mergeable`, `merged`, `exited`, `terminated`, and `no_signal`. These are
derived session and pull-request states, so a worker can move between them as
activity and repository facts change.

## ao send — message a worker

```
ao send <session-id> <message...>
ao send --session <session-id> --message "<message>"
```

Delivers into the worker's conversation, exactly as if the human had typed it.
Queued durably: a worker that is still provisioning receives it when its agent
starts. Use it to route CI failures, review feedback, clarifications, and
course corrections.

## ao report — message your orchestrator (workers only)

```
ao report <message...>
```

Sends a message to the orchestrator session that spawned this worker. Use it
when the task is done (say what was delivered and the PR number), or when
blocked on a decision only the orchestrator or human can make. Keep it to one
or two sentences: the outcome and PR number, or the single blocking reason.
Never paste diffs, logs, or file contents — a long report reads like an
injected prompt in the orchestrator's conversation, where it appears prefixed
with this session's id. A session that was not spawned by an orchestrator gets
`SCOPE_REQUIRED`.

## ao kill — terminate a worker

```
ao kill <session-id>
```

Terminates the worker session and destroys its sandbox. Kill workers whose
task is complete (PR merged or handed off) or that are permanently stuck after
a redirect attempt. Killing is not reversible; the branch and any open PR
survive on GitHub.
