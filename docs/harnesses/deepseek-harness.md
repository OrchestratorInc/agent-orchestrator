# DeepSeek Harness

AO supports [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) (`dsh`)
through two of the profiles it ships: the automation-only ACP server that carries
Chat, and the one-shot headless profile that carries a Terminal UI task.

Install it with npm from Settings → Agents, or manually:

```bash
npm install -g @deepseek-ai/dsh
```

Choose `deepseek-harness` as a project's agent or pass `ao spawn --harness deepseek`.
The harness id is `deepseek-harness`; the binary AO resolves is `dsh`.

## Chat

Chat is the complete path. AO launches `dsh --profile acp` and reads the session's
own configuration catalog, so the model picker offers exactly the models that
session accepts, and the reasoning-effort selector offers the levels that model
advertises (`off`, `low`, `high`, `max`).

Model values are opaque: DeepSeek Harness encodes each choice as a JSON array
string, for example `["deepseek-official","deepseek-v4-flash"]`. AO passes the
value through unchanged, and the project model field accepts the same string.

AO's own effort vocabulary is mapped onto the four Harness levels before it is
sent — `none`/`minimal`/`off` become `off`, `low` stays `low`, `medium`/`high`
become `high`, and `xhigh`/`max` become `max` — so an effort configured for
another harness does not fail a Harness session.

AO standing instructions ride the first turn as a text block, because
`session/new` reads only the working directory. The block is delivered when a
fresh conversation opens and is not repeated when a resumed session already has
prior turns, so it is not duplicated mid-conversation.

## Terminal UI

AO runs one headless task per terminal session:

```bash
dsh --profile headless "<task>"
```

The profile answers one task, prints the answer, and exits, so a terminal session
is a one-shot run rather than an interactive TUI. The headless profile has no
model or reasoning-effort flag: it uses the model route configured in the
Harness profile, and the AO model field applies to Chat only. The field
description says so.

`dsh --profile headless -` reads the task from stdin, which a terminal launch
never supplies, so AO rejects that exact task text instead of blocking.

## Credentials

DeepSeek Harness owns its credentials, which live in its own store
(`~/.dsh/.credentials.yaml`, or `$DSH_HOME`). In Settings → Agent Harnesses,
**Setup** / **Open setup** opens `dsh --profile web` using the same executable
AO launches for sessions. Open **Settings → Models** to configure a DeepSeek API
key. AO's default ACP and headless sessions use `deepseek-official`; browser
sign-in does not provide credentials for that route.

Browser sign-in under **Settings → Account** belongs to the separate
`deepseek-account` route. AO does not infer session routing from a stored grant,
so a browser grant alone leaves readiness unknown. Account-aware readiness is
not supported until AO can verify that the session uses that route.

**Log out** opens the native web UI: choose **Sign out** from the sidebar account
menu and confirm. It does not remove the API key. Leave the terminal running
until you finish, then close it so AO can check authentication again.

The ACP server advertises no auth methods and its `authenticate` call always
succeeds. AO checks only the API key in the native store or environment for
default-session readiness; values are never copied, logged, or forwarded.
Missing credentials stay unknown because private profiles may configure other
providers; AO does not assume sign-out succeeded.

## Permissions

Harness has no ACP session modes. Approvals arrive as `session/request_permission`
with one-shot choices, and AO answers them: `auto` answers every request, and
`accept-edits` answers edit, delete, and move tools. Every other mode leaves the
decision to Harness. Nothing is granted up front, so policy Harness itself
enforces stays authoritative.

## Activity and resume

Session activity reaches AO through the ACP stream. DeepSeek Harness has no
workspace hook file for AO to merge into, so AO installs no hooks and reports no
terminal-session activity signals for this harness.

Chat sessions resume through the ACP `session/resume` call. A terminal session
can only resume when AO already holds a native session id, which without a hook
surface is normally not the case, so terminal resume falls back to a fresh run.

## Current limits

- Workspace projects that expose several repositories are not supported over ACP:
  Harness rejects additional directories, and AO reports that rather than
  launching a session with the wrong working set.
- The model catalog is read from a session, and a session needs a working
  directory. Project-free discovery uses a private AO-owned directory under the
  configured data root so the catalog remains available before a project exists.
- AO standing instructions reach the headless profile by prepending them to the
  positional task with an `<ao_standing_instructions>` delimiter: the profile
  takes no system-prompt flag and has no hook file, so the task is the only
  channel that reaches the model. Chat delivers the same instructions as a text
  block on the first turn.
- On Windows, npm installs `dsh` as a `.cmd` batch shim that cmd.exe caps near
  the 8,191-character command-line limit, and a task carrying standing
  instructions exceeds that. When AO resolves the binary to a batch shim whose
  npm shape it understands, it launches the shim's Node entry point directly
  instead, which raises the limit to CreateProcess's 32,767 characters. A shim
  AO cannot parse keeps the original launch and its own error.
