# First-time setup

Use this when the user asks you to set up Orchestrator, add their first project, or get them started. Run the steps yourself. Ask the user only for what you cannot find out, and offer choices instead of asking for a path.

## One command: `ao setup`

Choose the repository first, as in manual step 3. `ao project ls` needs a running daemon, so start one with `ao serve` (it reuses a running one, and `ao setup` reuses it too), then run `ao project ls`; if the user named no repository, offer a few recent ones. Then run `ao setup --path <repo> --json`. It does manual steps 1, 2 and 4 for you (daemon, agent, registration) and lists what needs a person. Without `--path` it uses `AO_PROJECT_ID`'s project when you run inside an Orchestrator session, and otherwise the git repository around the current directory, so pass `--path` whenever you mean a different repository. If `ao setup` isn't available, follow the manual steps.

```bash
ao setup --path <repo> --json
```

It runs these steps in order. Each one reports `{step, status, detail, fix}`, with `status` one of `ok`, `fixed`, `skipped`, `needs_human` or `failed`:

1. **daemon:** reuses the running daemon, or starts one in the background the way `ao serve` does.
2. **agent:** picks an agent for each role, worker and orchestrator. `--worker-agent` and `--orchestrator-agent` set one role each. `--agent` sets every role not named by a role flag. Without those flags, a registered project keeps the agents it already saves. A role that still has no agent gets the coding agent running the command (you), detected from its environment (`AI_AGENT` first, then the tags coding agents set in the shells they run), or else the first ready agent. It checks the agent is installed and signed in, asking the agent's own sign-in status command when the daemon cannot tell. It never installs an agent or signs one in.
3. **github:** checks that the GitHub CLI is signed in. It never runs `gh auth login`. For a repository with a `github.com` remote, a missing or signed-out `gh` is `needs_human`. Otherwise it is advisory: `skipped`, with its fix in `next_actions`, and setup can still be ready.
4. **project:** registers `--path` (default: `AO_PROJECT_ID`'s project inside an Orchestrator session, otherwise the git repository around the current directory; a linked worktree resolves to its main repository) with those agents as worker and orchestrator, or reuses its existing registration. On a registered project it fills in a role that has no saved agent, but replaces a saved agent only when you pass an agent flag **and** `--yes`; otherwise it reports `needs_human`. A repository with no remote gets its checked-out branch as the default branch. Setup uses `origin`, or the first remote when there is no `origin`. A branch on another remote is saved remote-qualified, such as `upstream/main`. If that remote's default branch cannot be resolved (never fetched, unreachable), setup uses its `main` or `master` tracking branch if the repository has one. Otherwise it reports `needs_human`, and `next_actions` gives the fix: `git -C <repo> fetch <remote> && git -C <repo> remote set-head <remote> --auto`. If the remote stays unreachable, set the branch on a registered project with `ao project set-config <id> --default-branch <branch>`. It never picks the checked-out branch there, which may be a feature branch.
5. **first-run:** a registered project makes the desktop app skip its first-run screens.
6. **start:** only with `--start "<task>"`. Pass the user's own request as the task. It goes to the project's orchestrator, or to one worker with `--worker`, and setup waits until it has started.
7. **verify:** `ready` is true when nothing is left to do.

Exit codes:

| Code | Meaning |
|---|---|
| `0` | Ready (and the task started, with `--start`). A `--dry-run` also exits 0 with `ready: false` when setup still has work to do; read `ready` for the verdict. |
| `10` | The user must do something first. `next_actions` lists runnable commands (the explanation is in each step's `detail`), such as the agent's login command or `gh auth login`. Show them to the user, wait until they are done, then rerun the command the `verify` step names (it keeps your `--path` and agent flags; a bare `ao setup` may pick another repository). |
| `1` | A step failed; its `detail` says why. With `--start`, if `task.taskId` is set the task may already exist: check it with `ao task status <taskId>` instead of starting it again (see [task.md](task.md)). If the start step failed with no task id, a session may still exist: check `ao session ls` before sending the task again. |
| `2` | Usage error: fix the command. |
| `3` | The task started with `--start` is waiting on the user, for example for an approval. Do not start it again. |
| `4` | The task started with `--start` was submitted, but the wait ended before it was confirmed: it is still queued behind the agent's current turn, it was not confirmed in time, or the wait was interrupted. Do not start it again; follow it with `ao task wait <taskId>`. |

With `--json` the output is `{ready, agent, project, steps, next_actions, task}`. `task` is the `ao task start --json` result once setup reaches the start step. It is `null` without `--start`, and also when an earlier step blocked setup, so the task was never sent.

| Flag | Meaning |
|---|---|
| `--path <repo>` | Repository to register. Default: `AO_PROJECT_ID`'s project inside an Orchestrator session, otherwise the git repository containing the current directory (a linked worktree resolves to its main repository). |
| `--agent <id>` | Agent to use, from `ao agent ls`. Default: a registered project's saved agents, then the calling agent, then the first ready agent. |
| `--worker-agent <id>` / `--orchestrator-agent <id>` | Use a different agent for one role. |
| `--start "<task>"` | Start this task once setup is ready. |
| `--worker` | With `--start`, give the task to one worker instead of the orchestrator. |
| `--yes` | Replace the agents already saved on a registered project with the ones you passed. Without it, setup keeps them and asks. |
| `--check` | Report what is set up and what is missing. Changes no setup state and starts nothing. It still runs read-only checks, such as the agents' own sign-in status commands and a bounded, non-interactive `git ls-remote` for the default branch, and records the usual CLI usage event. It exits 10 until setup is complete. |
| `--dry-run` | Show what setup would do. Writes nothing. |
| `--json` | Print the result as JSON. |

```bash
# Set up the repository the user chose and start the user's task
ao setup --path ~/code/my-app --start "Fix the failing login test" --json
```

On macOS the user can then open the app with `open "/Applications/Agent Orchestrator.app"`. If `ao setup` is not available (an older Orchestrator), follow the manual steps below.

## Manual steps

1. **Check the install.** `ao status` must report the daemon ready. In `ao doctor`, fix `FAIL` lines; a `WARN` for an agent the user does not use needs no action.
2. **Check the agent.** `ao agent ls --refresh` must show the agent the user wants as `installed` and `authorized`. You cannot install an agent or sign in for the user. If it is not ready, send them to **Settings → Harness** in the app, which installs and signs in each agent, then run the check again.
3. **Choose the repository.** Run `ao project ls` first: the project may already exist. If the user named no repository, find a few recent Git repositories on disk and ask which one to use. For a repository that is not on disk yet, run `git clone`, then register the clone.
4. **Register it**, unless it is already a project. `ao project add` on a registered path fails with `PATH_ALREADY_REGISTERED`: use the existing project, whose path `ao project get <id>` shows. It keeps its saved agents, so if the user wants different ones, set them with `ao project set-config <id> --worker-agent <agent> --orchestrator-agent <agent>`. An orchestrator that is already running keeps the agent it started with.

   ```bash
   ao project add --path <absolute-path> --name <name> --worker-agent <agent> --orchestrator-agent <agent>
   ```

   A repository with no remote has no default branch Orchestrator can resolve, and sessions fail to start until you set one:

   ```bash
   ao project set-config <project-id> --default-branch <branch>
   ```
5. **Make sure the project orchestrator is running.** The app starts one when a project is added there; `ao project add` does not. `ao orchestrator ls` shows each orchestrator's status. Start one when the project has none, or only `terminated` ones:

   ```bash
   ao spawn --project <project-id> --kind orchestrator --name "<name>"
   ```

   Resume one marked `[exited]` with `ao session resume-agent <id>`.
6. **Start a first task only if the user asked for one.**

   ```bash
   ao spawn --project <project-id> --name "<name>" --prompt "<task>"
   ```

   `ao session get <id>` shows `working` while it runs. If `ao spawn` reports that the session failed to start, fix the cause it names, then retry with `ao session resume-agent <id>`. A turn appears in `ao session history` only after the agent finishes it, so an empty history during the first turn is normal.

## Hand off in the app

End by telling the user what to do in the desktop app, not in a shell. A new install has no `ao` command on the user's PATH, so do not give them `ao` commands to run.

- Their project is in the sidebar. Its board has **New task**, which starts a worker, and **Orchestrator**, which opens the project's main agent.
- Link each session you started as `ao://sessions/{project-id}/{session-id}` so they can open it. See [session.md](session.md).
- Say what you changed that they did not ask for, such as a default branch you set.

## What needs the user

- Installing or signing in to a coding agent.
- `gh auth login` for GitHub pull requests and CI. `ao doctor` reports whether a GitHub token is available.
- Choosing the repository when more than one is plausible.
