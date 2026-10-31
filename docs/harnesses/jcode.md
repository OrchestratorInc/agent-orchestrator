# jcode Adapter

[jcode](https://github.com/1jehuang/jcode) is integrated as a Terminal UI
harness. AO launches `jcode` (the copy AO installed, else yours) in the session worktree. AO
does not register jcode for Chat or code review yet (see Limits).

The behaviour below was verified against jcode v0.99.1.

## Install and sign in

The Harness page's Install button runs jcode's official installer, which
downloads the release archive and refuses it unless its SHA-256 matches the
release's `SHA256SUMS`. On macOS and Linux that script always adds `PATH`
lines to your shell profiles, runs `jcode setup-hotkey`, and reloads a running
jcode server, so AO runs it into a release folder of its own (under AO's data
dir) with a private `HOME`, `JCODE_SKIP_SERVER_RELOAD=1` and
`JCODE_NO_TELEMETRY=1`: your profiles and your jcode server are not touched,
and AO sessions use that copy. On Windows AO runs `install.ps1` with
`-SkipHotkeySetup -SkipAlacrittySetup` and the same two variables; that script
still adds its folder to your user `PATH`.

```bash
curl -fsSL https://jcode.sh/install | bash        # macOS, Linux (manual install)
irm https://jcode.sh/install.ps1 | iex             # Windows
jcode --version                                    # jcode v0.99.1 (…)
```

AO resolves `jcode` from its own installed copy first, then `PATH`, `~/.local/bin/jcode`, or
`%LOCALAPPDATA%\jcode\bin\jcode.exe`, and only accepts a binary whose
`--version` output starts with `jcode v<semver>`.

**Log in** opens `jcode login`, jcode's own provider chooser (Claude or ChatGPT
OAuth, GitHub Copilot, Gemini, an API key, or importing an existing login).

## Connected state

`jcode auth status` only checks that credentials exist, so AO never shows
Connected from it alone. AO reads which provider jcode will use for new
sessions (`jcode provider current --json`), then runs `jcode usage --json`,
which asks that provider's usage API with the stored credential and spends no
model tokens:

| Result | Harness page |
| --- | --- |
| No provider configured | Not signed in |
| Configured, and the active provider's usage report has no error | Connected |
| Configured, but the usage check failed (quota, rejected, timeout) or the active provider has no usage API (most API-key providers) | Credentials configured, not verified |

AO does not use `jcode auth-test`: its refresh step rotates OAuth refresh
tokens and its smoke steps send prompts.

## Sessions

| Area | How AO drives jcode |
| --- | --- |
| Launch | `jcode --no-update [--model <id>]`, with `JCODE_NO_AUTO_UPDATE=1` so jcode never updates itself mid-session |
| First prompt | jcode's TUI takes no prompt argument. AO waits for the empty `1>` composer, then types the task. Multi-line and long (over 512 characters) tasks are sent as one bracketed paste, because a typed newline is jcode's Ctrl+J shortcut and would be dropped |
| Instructions | jcode has no system-prompt flag. AO writes its instructions to `.jcode/prompt-overlay.md` in the worktree (git-ignored), which jcode appends to its assembled system prompt. A project `.jcode/system-prompt.md` replaces only jcode's base prompt, so AO's instructions still apply. AO refuses to replace an overlay file it did not create, or to write through a symlinked `.jcode` folder |
| Daemon | jcode's TUI is a client of a background `jcode serve`. Each AO session gets its own daemon (`JCODE_RUNTIME_DIR` and `JCODE_SOCKET` under AO's data dir), so its hooks run with that session's AO environment. Kill stops that daemon with `jcode server stop --force`; the saved conversation stays resumable |
| Status | AO sets jcode's lifecycle hook variables: `session_start` → idle (and captures the session id), `turn_start` → working, `turn_end` → idle, `session_end` → exited. Hooks you configured for the same events in `config.toml` or the environment run first |
| Token usage | jcode saves `token_usage` on every assistant message of `~/.jcode/sessions/<id>.json`, a file it rewrites on each save. On every jcode hook and when the session ends, AO appends the messages it has not exported yet, keyed by their message id, to its own ledger under the AO data dir, and reads usage from there. `prompt_tokens` is the whole prompt, so cached tokens count once, and a resumed session does not add earlier messages again |
| Restore | `jcode --resume <id>` with the captured id. AO resumes only an exact `session_…` id whose saved `working_dir` is this worktree; a missing, foreign or ambiguous id starts a fresh session instead, because jcode would otherwise reopen a session in its own saved folder |
| Models | `jcode model list --json` for the active provider, with the provider's selected model as the default. A model ID can also be typed in |

## Permissions

jcode runs tools without asking for approval and has no approval modes, so AO
passes no permission flag in any mode (as for Pi and Amp). AO tool allow/deny
lists and reasoning effort are rejected rather than silently ignored.

## Limits

- **First run.** A new jcode install shows an onboarding page before its input
  box. For a signed-in user it is a one-time "How would you like to begin?"
  choice with no timeout; AO answers it with jcode's own "New session in
  <worktree>" option (Right, Enter, which does nothing on an ordinary input
  box) and types the task only once the empty input box is on screen. When no
  provider is signed in, jcode shows its login onboarding instead; AO does not
  answer that, and the start fails with "native startup command was not
  confirmed" rather than typing the task into the login page. Sign in with
  **Log in** first.
- **Restore after Kill continues the interrupted turn.** Kill stops the
  session's daemon and the command it was running. When the session is
  restored, jcode itself sees the unfinished turn in the saved history and
  continues it ("Reload complete - continuing."), which can run the killed
  command again. This happens however the daemon stopped (`server stop`,
  SIGKILL, or after `jcode debug cancel`), so AO cannot prevent it.
- **Token usage per model switch.** jcode stores token usage on each assistant
  message but not that message's model; AO attributes each message to the
  session's model when it exports it (on every hook), so a `/model` switch
  inside a single turn is attributed to the new model.
- **Chat.** `jcode acp` exists, but AO does not register it: it ignores a
  system prompt (`_meta.systemPrompt`), answers `session/load` for an unknown
  id with success (a silent new conversation), sends no permission requests,
  and its prompt, resume and cancel have not been proven end to end.
- **Code review.** Not registered: AO cannot enforce a read-only tool set.
- **Effort.** jcode sets reasoning effort per provider in its own config, not
  per launch.
- **Hooks are asynchronous.** jcode runs hooks detached; on a very short turn
  the working and idle callbacks can arrive close together.
