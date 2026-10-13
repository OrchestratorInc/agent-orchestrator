# Hermes Adapter

Hermes is integrated into Agent Orchestrator as both an interactive Terminal UI
harness and a structured Chat harness. AO launches the user's own `hermes`
binary inside the session worktree in either mode.

## Install

Install Hermes Agent using its upstream installer, then make sure the `hermes`
binary is available on `PATH`:

```bash
curl -fsSL https://hermes-agent.nousresearch.com/install.sh | bash
```

Verify the install:

```bash
hermes --version
```

AO resolves `hermes` from:

- `PATH`
- `/usr/local/bin/hermes`, `/opt/homebrew/bin/hermes`
- `~/.local/bin/hermes` and `~/.hermes/bin/hermes`
- `~/.hermes/bin/hermes.exe` and the checkout venv on Windows

A name match alone is not trusted: AO validates the candidate's help banner
identifies it as Hermes Agent before accepting it.

## Supported AO Modes

Hermes is exposed through both of AO's interfaces.

### Terminal UI

A fresh prompted session launches as:

```bash
[env HERMES_EPHEMERAL_SYSTEM_PROMPT=<system prompt>] hermes chat --source tool \
    [-m <model>] [--yolo] -q "<prompt>"
```

- `-q` seeds the interactive session with the prompt as its first submitted
  turn (terminal panes always present a TTY), so the prompt is delivered in the
  launch command.
- `--source tool` records AO's sessions under Hermes' third-party-integration
  source tag so they stay out of the user's personal session lists.
- `HERMES_EPHEMERAL_SYSTEM_PROMPT` delivers AO's standing instructions at
  inference time; Hermes has no system-prompt flag, and this variable is never
  persisted into the session transcript.
- `bypass permissions` maps to `--yolo`. Hermes has no CLI flag for its
  intermediate approval modes, so every other AO permission mode emits no flag
  and Hermes' own approvals configuration governs.

### Chat

Hermes runs through its native `hermes acp` server. Chat activity, approvals,
diffs, model switching, and resume are derived from the ACP protocol itself:

- the session response advertises Hermes' model catalog; AO renders it as the
  model picker (`configured` custom-model semantics)
- AO's permission modes map onto Hermes' ACP session modes: `accept-edits` →
  `accept_edits`, `auto` and `bypass permissions` → `dont_ask`
- in `bypass permissions`, AO additionally answers Hermes' dangerous-command
  approval requests with allow, because the ACP server has no server-side
  bypass switch

## Restore

When AO has captured a Hermes native session id in session metadata, restore
uses:

```bash
[env HERMES_EPHEMERAL_SYSTEM_PROMPT=<system prompt>] hermes chat --source tool \
    --resume <native-session-id> [-m <model>] [-q "<prompt>"]
```

AO verifies a session row for that id exists in Hermes' state database before
trusting it. If the row is missing or no native session id is available, AO
falls back to a fresh interactive launch.

## Auth

AO checks Hermes auth using local-only signals:

1. Provider API keys in the environment (`ANTHROPIC_API_KEY`,
   `OPENAI_API_KEY`, `ZAI_API_KEY`, and other provider keys Hermes reads).
2. `HERMES_HOME/auth.json` (default `~/.hermes/auth.json`): any `providers` or
   `credential_pool` entry counts as configured credential evidence.

These checks are advisory. Hermes can also run against local runtimes (e.g.
Ollama) with no stored credential at all, and the ACP handshake or a later
model call remains authoritative.

## Not Supported

- Activity hooks for Terminal UI sessions. Hermes' shell-hook system is
  configured in the user's global `~/.hermes/config.yaml`, not per-workspace,
  so AO installs nothing and derives no hook-based session identity or
  activity there; status for TUI sessions comes from runtime facts only.
  Structured Chat sessions have native activity signals.
- Reviewer harness. Hermes offers no read-only tool allow/deny enforcement,
  which the reviewer role requires.
- TUI-to-Chat or Chat-to-TUI handoff.
