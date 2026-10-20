# Freebuff Adapter

[Freebuff](https://freebuff.com) is a free, ad-supported terminal coding agent
built on Codebuff. AO runs the user's own `freebuff` binary as an interactive
Terminal UI harness inside the session worktree.

## Install

```bash
npm install -g freebuff
freebuff --version
```

AO's one-click installer uses the same npm package. AO resolves the binary from
`PATH`, `/usr/local/bin`, `/opt/homebrew/bin`, Node-managed global bin
directories, and `freebuff.cmd`/`freebuff.exe` under `%APPDATA%\npm` on Windows.

## Sign in

Settings → Harnesses → Freebuff → **Log in** runs `freebuff login` in an AO
terminal. It prints a login URL (and opens the browser), then waits until the
account is linked.

AO reports Freebuff as signed in when the `default` profile in
`credentials.json` holds a token or a keychain pointer, or when
`CODEBUFF_API_KEY` is set. Credentials live in Freebuff's config directory:
`$FREEBUFF_CONFIG_DIR` when set (it must be absolute), otherwise
`~/.config/manicode`. Freebuff has no logout subcommand; use `/logout` inside a
Freebuff session.

## Launch

```bash
freebuff --cwd <worktree>
```

Freebuff has no prompt argument, so AO types the first turn once the composer
shows `Enter a coding task`. A model queue can delay that, so AO waits up to 60
seconds.

Freebuff has no system-prompt input. AO sends its standing instructions with
the task as one first turn, under `## AO Standing Instructions` and
`## AO Task` headings. A restored conversation keeps them in its history.

Freebuff has no approval prompts, so every AO permission mode launches the same
command.

## Models and reasoning effort

The model picker lists Freebuff's regular `/model` picker: MiMo v2.6 Flash Fast
(Freebuff's default), GLM 5.3 Flash, DeepSeek V4.1 Flash, DeepSeek V4.1 Flash
Fast, GPT-6 Luna, MiMo 2.6 Pro, Solar Mini 4, Solar Pro 4, Gemini 3.8 Flash,
Muse Spark 1.3, Gemini 4 Argon and GPT-6.1 Sol. Each row offers only its own
reasoning ladder.

Freebuff has no `--model` flag. Before launch, AO writes the choice to
`settings.json` in Freebuff's config directory, the same `freebuffModel` and
`freebuffReasoningEfforts` keys the `/model` and `/reasoning` pickers save. It
also clears `freebuffModelKey` so the explicit choice wins. Freebuff's own
picker remembers the last selection the same way.

Paid-plan and region-limited models stay locked in Freebuff, as they are in its
picker. Freebuff drops unknown models or efforts when it loads settings and
falls back to its default model.

## Activity

Freebuff has no hooks. AO reads the bottom of the terminal:

- **active:** `Add to the current task` in the composer, or `thinking...`,
  `working...` or `■ Esc` in the status bar;
- **idle:** `Enter a coding task` in the composer.

AO's process supervisor detects when the TUI exits.

## Restore

Freebuff stores each conversation under
`<config>/projects/<worktree-name>/chats/<chat-id>/`, and records the project
root in `run-state.json`. On restore AO picks the newest chat whose project
root is the session worktree, or the chat id it already recorded, and runs:

```bash
freebuff --cwd <worktree> --continue <chat-id>
```

Without a matching chat AO starts a fresh session instead of letting
`--continue` pick an unrelated recent chat. Freebuff does not support
unattended terminal refresh on a new release.

## Not supported

- Chat (ACP) mode and reviewer sessions.
- Per-session tool allow/deny lists: Freebuff has no such input.
