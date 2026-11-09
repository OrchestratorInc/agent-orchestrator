# ZCode Adapter

ZCode (the open-source [Z.ai coding agent](https://github.com/zai-org/ZCode),
CLI at `apps/zcode-cli`) is integrated into Agent Orchestrator as a
Terminal-UI-only harness. AO launches the user's own `zcode` binary inside the
session worktree and delivers prompts into the interactive TUI.

## Install

Install the official CLI and make sure `zcode` is on `PATH`:

```bash
npm install -g @zcode/cli
```

AO resolves `zcode` from:

- `PATH`
- `/usr/local/bin/zcode`, `/opt/homebrew/bin/zcode`
- Node version-manager global bins (nvm, Volta, fnm)
- `%APPDATA%\npm\zcode.exe` / `zcode.cmd` on Windows

## Setup

AO probes ZCode's documented personal provider config
(`~/.zcode/v2/provider_config.json`, overridable with
`ZCODE_PERSONAL_PROVIDER_CONFIG_FILE` or `ZCODE_DATA_BASE_DIR`): when a
provider rule carries an access entry, AO reports ZCode as authenticated.
Account logins created with `zcode login` live in ZCode's own credential
store, which has no documented status file, so those sessions report an
unknown auth state rather than a guess.

## Supported AO Mode

ZCode is exposed through AO's Terminal UI mode only. A fresh prompted session
launches as:

```bash
zcode [--cwd <worktree>] [--mode <mode>] [--disallowed-tools <names>]
```

The prompt is delivered into the TUI after startup; no `--prompt` is passed,
so the terminal stays interactive. Only flags documented in the CLI's own
reference are sent. Launches that request a model or effort override, or a
tool allowlist (no documented `--model`/`--allowed-tools` flags exist), fail
instead of running with the override silently dropped.

A restored session resumes the exact native session id when one is known:

```bash
zcode [--cwd <worktree>] [--mode <mode>] --resume <session-id>
```

This adapter installs no hooks, so no native id is captured today; restore
then falls back to `--continue`, which resumes the latest session for the
same working directory. Every AO worktree belongs to exactly one session, so
inside the session's own worktree that is its own latest native session.

AO permission modes map onto ZCode's documented modes:

| AO mode | ZCode |
| --- | --- |
| default | no flag: the TUI resolves its own starting mode |
| accept-edits | `--mode edit` |
| auto | `--mode build` |
| bypass | `--mode yolo` |

A project's agent config may set `mode` directly to `plan`, `build`, `edit`,
`yolo`, or `guarded`; a per-spawn permissions override wins over the stored
config.

## Instructions

ZCode's TUI has no system-prompt flag in the documented CLI reference, and
this adapter installs no workspace hooks, so AO's standing instructions are
not re-applied to ZCode sessions today.

## Activity Tracking

No hooks are installed: AO treats ZCode sessions with its conservative
terminal-idle fallback and process-supervisor exit detection.

## Chat Mode

Not supported. ZCode has no Chat driver registered, so chat-mode spawns fail
with AO's standard unsupported-chat error by design.
