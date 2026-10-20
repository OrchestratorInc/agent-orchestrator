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

AO reports credentials as **configured**, without claiming provider verification,
when the `default` profile in
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
The turn uses Freebuff's native bracketed paste handler to preserve multiline
text. Large turns appear as a native pasted-text attachment.
AO confirms that Freebuff saved the complete user turn before reporting a
successful spawn. A visible draft alone does not establish delivery; bounded
Enter retries require a matching draft in the current native composer.

Freebuff has no approval prompts, so every AO permission mode launches the same
command.

## Models and reasoning effort

The model picker lists Freebuff's regular `/model` picker: MiMo v2.6 Flash Fast
(Freebuff's default), GLM 5.3 Flash, DeepSeek V4.1 Flash, DeepSeek V4.1 Flash
Fast, GPT-6 Luna, MiMo 2.6 Pro, Solar Mini 4, Solar Pro 4, Gemini 3.8 Flash,
Muse Spark 1.3, Gemini 4 Argon and GPT-6.1 Sol. Each row offers only its own
reasoning ladder.

Freebuff has no `--model` flag. AO serializes startup for sessions sharing a
Freebuff config directory, seeds the requested model, and lets Freebuff resolve
its current catalog row. It then selects that row and the requested effort
through the native picker and confirms the resulting footer before sending the
task. The lock spans preparation through first-turn acceptance, so concurrent AO
launches cannot replace each other's startup preferences. Native credentials and
chat history keep their original paths. Project `FREEBUFF_CONFIG_DIR` overrides
apply to fresh launches and exact-chat restore.

Paid-plan and region-limited models stay locked in Freebuff, as they are in its
picker. An unavailable selection or unrecognized native screen fails startup
instead of reporting that a different model was selected.

## Activity

Freebuff has no hooks. AO recognizes its complete current composer and native
model/chat footer. An anchored running status with its stop control reports
**active**; the native task placeholder reports **idle**. Transcript quotations,
draft text and partial screens do not establish activity. Styled cursor evidence
is required before AO treats the composer as empty.

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
