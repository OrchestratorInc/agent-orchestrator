# Stakpak Adapter

[Stakpak Agent](https://github.com/stakpak/agent) is integrated into Agent
Orchestrator as an interactive Terminal UI harness. AO launches the user's own
`stakpak` binary inside the session worktree. Qualified against Stakpak 0.3.88.

## Install

AO's installer offers, on macOS and Linux:

- Homebrew: `brew install stakpak/stakpak/stakpak`
- the official installer: `curl -sSL https://stakpak.dev/install.sh | bash`

Windows has no installer: download `stakpak-windows-x86_64.zip` from the
[GitHub release](https://github.com/stakpak/agent/releases) and put
`stakpak.exe` on `PATH`. AO resolves `stakpak` from `PATH`, then
`/opt/homebrew/bin`, `/usr/local/bin`, `/home/linuxbrew/.linuxbrew/bin`,
`/usr/bin` and `~/.local/bin`, and accepts a candidate only when
`stakpak --version` prints `stakpak <version>`.

Update uses `stakpak update`. Stakpak also updates itself in the background
when its TUI starts; its only switch for that (`STAKPAK_SKIP_WARDEN`) also turns
off its Docker sandbox, so AO leaves it alone.

## Log in

Settings → Harness → Log in runs `stakpak auth login`: create a Stakpak API key
in the browser, or paste an Anthropic, OpenAI or Gemini key. Other
OpenAI-compatible providers go in `~/.stakpak/config.toml`, for example:

```toml
[profiles.default]
provider = "local"
model = "zai/glm-5.1"

[profiles.default.providers.zai]
type = "custom"
api_endpoint = "https://api.z.ai/api/coding/paas/v4"
api_key = "..."
```

AO verifies the login with a provider round-trip and never reports a key as
connected just because it exists:

- With a Stakpak API key, `stakpak auth status` checks it against the Stakpak
  API.
- Otherwise AO reads the active profile's providers (`STAKPAK_PROFILE`, else
  `default`, over `all`) and asks each whether it accepts its key, without
  spending tokens: `GET <endpoint>/models` for OpenAI-compatible and custom
  providers, and an empty Messages request for Anthropic-compatible ones
  (the provider rejects it as invalid only after accepting the key). One
  accepting provider is enough. All rejecting reads as logged out; anything
  else as configured but unverified.

## Terminal UI

```bash
stakpak --system-prompt-file <file> [--model <model>]
stakpak [--system-prompt-file <file>] [--model <model>] --session <id>
```

- The interactive TUI ignores a positional prompt, so AO types the first task
  into the composer once the TUI is idle.
- `--system-prompt-file` carries AO's standing instructions. Stakpak stores
  them in the conversation and inserts them again on every resume, so a restore
  passes them only when they changed.
- `--model` takes Stakpak's names, for example `anthropic/claude-sonnet-4-5` or
  `<custom-provider>/<model>`. The model field is free text.
- Reasoning effort and per-launch tool restrictions have no Stakpak flag and
  are rejected.

### Status and session id

Stakpak has no external hooks, so AO reads status from the rendered screen:
the footer under the composer box shows `esc cancel` while a turn runs, and
`$ shell | / commands` when idle. An `Approval Required` box means the session
is waiting for a decision.

Stakpak creates a session id on the first message. AO records the worktree and
launch time, then takes the newest session `stakpak sessions list --json`
reports for that worktree since the launch. Restore checks the id with
`stakpak sessions show` first, and launches fresh when Stakpak no longer has it.

### Permissions

The TUI has no permission flag; it reads `.stakpak/session/auto_approve.json`
in its working directory. AO writes that file (and a `.gitignore` beside it):

| AO permission mode | Tools approved without asking |
| --- | --- |
| default | Stakpak's own defaults (AO removes a file it wrote earlier) |
| accept-edits | `create`, `str_replace`, `remove` |
| auto, bypass-permissions | every tool by default |

Stakpak always asks before shell commands (`run_command` and friends), in every
mode. A policy file the user wrote is left untouched.

## Not supported

- **Chat.** Stakpak 0.3.88's ACP server (`stakpak acp`) answers
  `session/load` by recording the id only; it neither reloads nor replays the
  stored messages, so a restored Chat would continue without its history. Its
  ACP model list also omits custom providers, and a profile model without an
  `anthropic/`, `openai/` or `google/` prefix is routed to the Stakpak API.
- **Unattended version refresh** of idle terminals: the session id is inferred
  from Stakpak's session list rather than reported by a hook, so AO restores
  only when asked.
- **Native `.gitignore` edits.** With its default `auto_append_gitignore`,
  Stakpak appends `.stakpak/session*` to the worktree's `.gitignore`. Set
  `auto_append_gitignore = false` under `[settings]` to keep worktrees clean.
