# Pax Adapter

[Pax](https://pypi.org/project/pydantic-ai-pax/) (`pydantic-ai-pax`), the
Pydantic AI terminal coding agent, is integrated into Agent Orchestrator as an
interactive Terminal UI harness. AO launches the user's own Pax inside the
session worktree. Source: `src/pydantic_ai_pax/` in
[pydantic/pydantic-ai](https://github.com/pydantic/pydantic-ai).

## Install

AO's installer runs Pax's documented install on every platform:

```bash
uv tool install pydantic-ai-pax
```

It needs [uv](https://docs.astral.sh/uv/) on `PATH` and Python 3.11 or newer.
AO resolves `pydantic-ai-pax` before `pax` (both are installed), on `PATH` and
then in `~/.local/bin`, and accepts a candidate only when `--version` prints
`pax <version>`. macOS ships an unrelated `/bin/pax` archiver, which that check
rejects. Updates run `uv tool upgrade pydantic-ai-pax`.

## Log in

Pax signs in from inside its own TUI. **Log in to Pax** opens Pax and, once you
select **Open login**, types `/login`:

- `/login openai-codex` (Pax's default model) or `/login github-copilot`;
- `/keys` saves other providers' API keys.

Provider keys AO saves (Anthropic, OpenAI, Google, OpenRouter, Groq, Mistral)
reach Pax through the environment as well.

Pax has no command that checks a sign-in with its provider, so AO reports Pax
as **configured** when it finds a provider key in the environment or saved
credentials (`credentials-*.enc` or `credentials-*.json`) in Pax's folder,
`$XDG_CONFIG_HOME/pydantic-ai-pax/` (default `~/.config/pydantic-ai-pax/`). AO
never reads those files. A live turn is what proves the account works.

## Terminal UI

```bash
pydantic-ai-pax --session-id <uuid> [-m <provider:model>]   # fresh
pydantic-ai-pax --resume <uuid> [-m <provider:model>]       # restore
```

- **Model**: the project's model override is passed to `-m` and takes any
  provider-qualified name, for example `anthropic:claude-opus-5-5`. Empty uses
  your saved Pax model. Reasoning effort is set with `/effort` inside Pax; AO
  rejects an effort override instead of ignoring it.
- **First prompt**: Pax's shell reads no positional prompt, so AO types the task
  once the editor box (a rule directly above the `❯` row) is drawn.
- **Folder trust**: the first time Pax starts in a repository it asks
  **Do you trust the files in this folder?**. The answer covers the repository
  and every linked worktree, so it is asked once per project. AO holds the
  first prompt while the question is on screen; answer it in the terminal.

### Standing instructions and permissions

Pax has no system-prompt flag. AO writes its standing instructions, and a
permission mode stricter than your own, to `.clai/settings.json` in the session
worktree, beside a self-ignoring `.clai/.gitignore`, so the file never shows in
`git status`. Pax sends unapproved project `instructions` to the model framed as
the repository's text rather than your own, and names them under its startup
banner; you do not need to `/project approve` them.

| AO mode | Pax `permissions` |
| --- | --- |
| default, bypass-permissions | not set: your own `run.permissions` (default `full`) |
| auto | `auto`: risky commands (delete, push, install, network) ask first |
| accept-edits | `read-only`: every write and command asks first. Pax has no edits-only mode, and `auto` would run commands without asking |

A project file can make Pax stricter than your setting, never wider.

If the repository already has its own `.clai/settings.json` or
`.clai/.gitignore`, AO leaves them alone and refuses to launch a session that
needs instructions, instead of starting one that silently runs without them.

### Status and session id

Pax refuses to install plugins or hooks from a shell, because the agent can
run `pax` too, so AO installs none. Status comes from the screen:

| Screen | Status |
| --- | --- |
| `─ Working … Esc to interrupt` above the editor | working |
| empty `❯` editor under a plain rule | idle |
| a question menu (`… Esc decline`) | waiting for input |
| a tool approval (`Approve for this session`) | needs a decision |

AO chooses the conversation id with `--session-id`, derived from the AO session
id, and records it under the AO data dir. Exit is detected by AO's process
supervisor.

**Optional hook-reported turns.** Run this once in any Pax session. It is
saved in your user settings and applies to every later session:

```text
/plugins add ao pydantic_ai_pax.builtin_plugins.hooks '{"turn_start":[{"command":["ao","hooks","pax","turn-start"]}],"turn_end":[{"command":["ao","hooks","pax","turn-end"]}]}'
```

`ao hooks pax` does nothing outside an AO session. Pax reports a hook command
that fails to start, so keep `ao` on `PATH`. `/plugins remove ao` removes it.

### Restore

Pax saves a conversation only after its first turn. On restore AO reads Pax's
`sessions.db` read-only and resumes with `--resume <uuid>` only when the
conversation exists and was saved in this worktree. Otherwise it starts a fresh
conversation in the same worktree. A fresh relaunch never reuses an id Pax
already holds.

## Not supported

- **Chat**: Pax has no ACP server.
- **Per-launch tool restrictions** and **reasoning effort** overrides: Pax's
  shell has no flags for them.
- **Unattended version refresh**: AO cannot tell from the screen alone that an
  idle Pax holds no unsaved draft.
- **Exact AO-issued permission widening**: a project file cannot make Pax less
  strict than your own `run.permissions`.
