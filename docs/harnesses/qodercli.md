# Qoder CLI Adapter

Qoder CLI is integrated into Agent Orchestrator as an interactive Terminal UI
worker and orchestrator. AO launches the user's own `qodercli` binary inside the
session worktree; Chat and reviewer support are not part of this integration.

## Install

Install Qoder CLI and keep `qodercli` on `PATH`:

```bash
npm install -g @qoder-ai/qodercli
```

Verify the install and the login:

```bash
qodercli --version
qodercli status -o json   # {"logged_in":true,...}
```

Sign in with `qodercli login`, or export `QODER_PERSONAL_ACCESS_TOKEN`
(`QODER_SERVICE_ACCOUNT_KEY` and `QODER_JOB_TOKEN` also work).

AO resolves `qodercli` itself. The `qoder` binary is a dispatcher script that
forwards some invocations to the IDE, so AO never resolves it.

## How AO launches it

```
qodercli --session-id <uuid> [--permission-mode …] [--allowed-tools …]
         [--disallowed-tools …] [--model …]
         [--append-system-prompt-file …] --settings <path> [-i <task>]
```

- **The task rides on `-i`**, which keeps the TUI interactive. A bare positional
  prompt is rejected when it looks like a subcommand name.
- **`--settings` points at one AO-owned file** under
  `AO_DATA_DIR/agent-runtime/qodercli/<session>/settings.json`. It carries trust
  for the session worktree, AO's activity hooks, and `general.enableAutoUpdate:
  false`. Nothing is written into the checkout.
- **Resume** uses `--resume <uuid>`. A new turn is typed into the pane rather
  than passed on the command line, because Qoder CLI resolves `--resume`
  asynchronously inside the TUI and a command-line turn can land before the
  conversation finishes loading.

Permission modes map as `acceptEdits → accept_edits`, `auto → auto`,
`bypassPermissions → bypass_permissions`; AO's `default` passes no flag, leaving
whatever the user configured. Tool scoping is one `--allowed-tools` /
`--disallowed-tools` flag per rule.

## Trust

Qoder CLI treats an unfamiliar directory as untrusted. An untrusted directory
drops the permission mode AO asked for, blocks project- and system-scoped hooks,
and opens a modal trust dialog — with the initial task submitted behind it, so an
unattended session answers nobody and finishes nothing. AO therefore trusts each
session's worktree through the same `--settings` file that carries the hooks,
listing both the path AO knows and its symlink-resolved form.

AO's own hooks are flag-scoped and still run in an untrusted directory, so
activity reporting survives; it is the dialog and the permission downgrade that
make trust load-bearing.

One consequence is worth knowing: a repository's own committed
`.qoder/settings.json` is not loaded in AO sessions. Qoder CLI decides whether to
read workspace settings from the user's settings file alone, before AO's flag is
merged. Add the worktree root to `permissions.trustDirectories` in
`~/.qoder/settings.json` if you need them.

## Activity tracking

AO installs Claude-Code-shaped hooks (`SessionStart`, `UserPromptSubmit`, the
tool-use trio, `PermissionRequest`, `Stop`, `Notification`, `SessionEnd`) that
call `ao hooks qodercli <event>`. Qoder CLI's event names, payload fields,
notification types and session-end reasons match Claude Code's, so AO reuses
Claude's interpretation, plus one addition: an elicitation dialog (Qoder CLI's
`AskUserQuestion` form) reports **waiting for input** rather than looking idle.

`SubagentStop` is deliberately not installed — it carries no activity signal, and
its payload would overwrite the session summary with a subagent's last line.

Two environment variables change hook behavior and are worth avoiding in a
daemon environment:

- `CI=true` or `GITHUB_ACTIONS=true` force Qoder CLI into headless mode, which is
  incompatible with a TUI session.
- `GITHUB_SHA` or `SURFACE=Github` switch on strict environment sanitization,
  which strips AO's variables from hook processes. Activity reporting goes quiet
  until they are unset.

## Restore

The native session id is captured from hooks, with the UUID AO pinned at launch
as the fallback:

```bash
qodercli --resume <session-uuid> --settings <path>
```

Only a real UUID is resumed: a non-UUID argument would be read as an index into
Qoder CLI's recent-session list and attach the pane to an unrelated
conversation. AO also never moves a running session between binaries — a
mid-session upgrade cannot be proven through this CLI's surface, so terminal
version refresh is not claimed.

## Current limits

Qoder CLI Chat, reviewer support, TUI/Chat handoff, and first-class nested-agent
sessions are not part of this integration. The CN build (`qodercn`) is not
supported.
