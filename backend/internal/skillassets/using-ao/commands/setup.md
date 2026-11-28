# First-time setup

Use this when the user asks you to set up Orchestrator, add their first project, or get them started. Run the steps yourself. Ask the user only for what you cannot find out, and offer choices instead of asking for a path.

## Steps

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
