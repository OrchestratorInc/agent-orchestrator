# Worktree startup cues

In the Cue editor, enable **Run on worktree creation** for a command cue. Saving another selected command atomically clears the previous selection for that project. Agent cues cannot be startup cues. The shell defaults to the platform shell (`cmd.exe` on Windows, `sh` elsewhere); the editor allows an explicit shell and timeout, defaulting to ten minutes.

Existing project `postCreate` commands run first, in their configured order, and keep their current failure behavior: a failed command aborts provider startup. The daemon then snapshots the selected cue command when provisioning a new session worktree. It applies to worker and orchestrator sessions in Chat and TUI mode. Scratch sessions, project registration, existing-session reopen, restore, and handoff do not run it. Automatic execution does not change the last manually run cue.

After legacy `postCreate` succeeds, the provider can start while the selected cue runs, but AO holds the opening turn and later messages until the command exits. Chat shows the running cue directly above the composer and accepts messages into its durable queue. Terminal input is held until setup and queued delivery finish. The command runs synchronously in a daemon-owned worker in the worktree directory, with the project runtime environment; session creation remains responsive.

Success clears the loader without a toast. Failure or timeout releases queued delivery and keeps the provider usable. The session shows an error toast and a persistent failure banner with command output. Output retains its last 64 KiB; timeout and cancellation terminate the command process tree. Provider startup failures remain separate from command failures.

Each worktree records one immutable cue snapshot, and an atomic pending-to-running transition admits only one command execution. A daemon restart marks unfinished execution as interrupted, releases its hold, and never reruns the command automatically. Waiting AO messages remain in storage for normal session recovery. Editing or deleting a cue affects future worktrees only.

If the same shell operation is configured in both `postCreate` and the selected cue, it runs twice. Keep shared project setup in `postCreate` and use a startup cue only when its first-turn gating and session status are useful.
