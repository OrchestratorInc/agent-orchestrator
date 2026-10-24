# ZCode

AO supports ZCode (Z.AI's coding CLI) in both session modes:

- **Chat mode** (the default for a non-standalone spawn) runs a persistent
  `zcode app-server --stdio` behind AO's detached chat host, driven by the
  `zcodeappserver` Chat driver. Follow-up turns, approvals, user questions,
  streaming text, tool activity, interrupts, usage, model selection, history
  import after a restart, and hibernate/resume all go through the app-server
  protocol.
- **TUI mode** runs a one-shot `zcode -p` turn per spawn: the reply streams in
  the session pane and the process exits, leaving AO's keep-alive shell for
  inspection. This path is unchanged.

## Install and launch

- ZCode ships bundled with the ZCode desktop app
  (`/Applications/ZCode.app/Contents/Resources/glm/zcode.cjs`, run by Node).
  There is no standalone installer and **the app installs no CLI launcher**:
  nothing named `zcode` lands on `PATH`. AO's install page therefore shows an
  explicit not-installable entry, and the launcher is user-provided.
- AO resolves a `zcode` executable from `PATH`, `/usr/local/bin`,
  `/opt/homebrew/bin` and `~/.local/bin`, and never a bare `zcode.cjs`: the
  bundle needs the provider-config environment the desktop app would give it,
  or it cannot reach its provider. Provide that as a wrapper script, for
  example `~/.local/bin/zcode` (mode `0755`):

  ```sh
  #!/bin/sh
  # zcode: run the CLI bundled inside ZCode.app with the provider environment
  # the desktop app would supply. AO launches this script, never zcode.cjs.
  BUILTIN="$(ls -t "$HOME"/.zcode/v2/runtime/provider/*/*/endpoint-*/zcode-builtin.json 2>/dev/null | head -n1)"
  [ -n "$BUILTIN" ] || BUILTIN="/Applications/ZCode.app/Contents/Resources/config/provider/zcode-builtin.json"
  export ZCODE_BUILTIN_PROVIDER_CONFIG_FILE="$BUILTIN"
  [ -n "$ZCODE_BASE_URL" ] || export ZCODE_BASE_URL="https://zcode.z.ai"
  # AO presets the next two for every session it launches. The guards are
  # load-bearing: an unconditional export would replace AO's provider config
  # (the API key written by `ao zcode-login`) and AO's isolated credential
  # store with the wrapper's own, silently.
  [ -n "$ZCODE_PERSONAL_PROVIDER_CONFIG_FILE" ] || export ZCODE_PERSONAL_PROVIDER_CONFIG_FILE="$HOME/.zcode/v2/provider_config.json"
  # Standalone CLI use keeps its credential store apart from the desktop app's:
  # both write the same slot of <base>/.zcode/v2/credentials.json but store
  # different token types there, and sharing it breaks whichever logged in first.
  [ -n "$ZCODE_DATA_BASE_DIR" ] || export ZCODE_DATA_BASE_DIR="$HOME/.zcode-cli"
  exec node /Applications/ZCode.app/Contents/Resources/glm/zcode.cjs "$@"
  ```

  The first line picks the provider config the desktop app last downloaded
  and falls back to the one in the bundle. `node` must be a Node 20+ on
  `PATH` (or spell out its absolute path). `zcode --version` from a shell and
  `ao agent ls --refresh` showing `zcode ZCode installed` confirm the
  launcher; readiness then depends on the credentials below.
- Chat mode spawns `<wrapper> app-server --stdio --cwd <worktree>` and talks
  NDJSON over stdin/stdout. The provider's stderr is discarded: the CLI's
  credential logger writes there.
- `zcode -p` (TUI mode) has no `--model`, `--session-id`, or
  `--append-system-prompt` flags: the model comes from
  `defaultModelSelection` in the provider config and standing instructions are
  prepended to the prompt. Chat mode prepends the standing instructions to the
  first turn in the same way (zcode has no system-prompt channel), so a `-c`
  TUI restore of a chat session sees the same first turn.

## Credentials

Two credential routes exist; the API key is the one chat mode is built for.

- **Coding-plan API key (recommended).** `Log in to ZCode` in settings offers
  an API-key option that runs the hidden `ao zcode-login` command and writes an
  AO-owned personal provider config to
  `<AO data dir>/harnesses/zcode/provider_config.json` (mode `0600`, directory
  `0700`). The file names the `zai-api` template provider `zai-api-ao` with the
  models `GLM-5.3` and `GLM-5.3-Flash`. AO injects it into every launch (TUI
  and chat) as `ZCODE_PERSONAL_PROVIDER_CONFIG_FILE`; the auth probe reports
  `authorized` while the file holds a key. The key has no hourly expiry and
  needs no browser flow, and the app server resolves the model with no OAuth
  session at all. AO never logs or prints the key; the value is read once with
  a no-echo prompt and written to the file.
- **OAuth login (legacy).** `zcode login zai` against AO's isolated credential
  store at `<AO data dir>/harnesses/zcode` (injected as `ZCODE_DATA_BASE_DIR`).
  The raw z.ai token lives **one hour** and the CLI has no refresh flow, so the
  auth probe reports `needs auth` once it expires. In chat mode the app server
  additionally needs a desktop-style host to entitle the account provider, which
  AO does not play: **chat mode requires the API key route.** Do not point
  `ZCODE_DATA_BASE_DIR` at the desktop app's shared `~/.zcode/v2` store.

An operator-set `ZCODE_DATA_BASE_DIR` or `ZCODE_PERSONAL_PROVIDER_CONFIG_FILE`
in the daemon environment wins over AO's own values on every path (probe,
login, launch).

Chat mode checks the credential twice: at the capability probe (cached per
daemon lifetime once it succeeds) and again at every `Start`/`Resume`. A
credential that lapses after a successful probe fails the spawn with
`CHAT_AUTH_REQUIRED`, drops the cached probe, and the next spawn is refused up
front by the preflight check instead, before a worktree exists. `ao agent ls
--refresh` re-reads readiness after a login.

## Permissions

zcode's `--mode` accepts build, edit, plan, yolo, or guarded, with a
documented default of yolo for `--prompt` runs (all approvals skipped).
Because of that, AO's default/unset mode is pinned to `edit` in both session
modes; accept-edits maps to edit, auto to build, bypass-permissions to yolo.
Read-only (reviewer) sessions are refused: `plan` is a planning mode, not a
provider-enforced sandbox.

- **Chat mode:** under `edit` (proven; `build`, AO's `auto` mapping, is
  untested) the app server raises `interaction/requestPermission` for
  side-effecting tools (a safe `date` runs on its own; `touch …` asks). The
  request appears as an approval card with the provider's own options (allow
  once, always allow in this project, deny); AO echoes the chosen option back
  verbatim. The server re-announces the same unanswered request under fresh
  frame ids, 1 s apart and doubling to every 10 s, and AO answers every frame
  id with the same reply, which keeps the detached host's replay clean across
  daemon restarts. An approval nobody answers is refused
  after 30 minutes rather than decided. `AskUserQuestion` arrives as a form
  (`input.requested`) and is answered the same way; the server never
  auto-answers it. A per-turn permission mode that differs from the session's
  launch mode is refused.
- **TUI mode:** in headless `-p` runs, approval-class tool calls fail fast
  ("No permission client configured") rather than hanging; workers that need
  side-effecting shell commands must run bypass-permissions (yolo).

## Sending follow-ups (`ao send`)

- A **chat-mode** session is conversational: `ao send` relays the message as
  the next turn, queued behind a running one (the app server refuses a send
  during a prompt with `-32010`, so AO's controller holds it; there is no
  steer). An interrupt stops the running turn (`session/stop`) and it settles
  as interrupted with whatever text had streamed.
- A **TUI-mode** zcode session is a one-shot process. Typing into its pane is
  undefined mid-run and, after exit (until the reaper's next 5 s tick flips
  the session to exited), would be executed as a shell command at the
  keep-alive prompt, so the session manager's one-shot gate refuses `ao send`
  for it with 409 `HARNESS_ONE_SHOT`; the message names the harness and
  points at `ao spawn --agent zcode`. The refusal is per harness, so a
  restored session (`ao restore` / `ao resume-agent`, which relaunches as the
  interactive `zcode -c` TUI) is refused too; type into its pane directly. A
  terminated session keeps the usual 409 `SESSION_TERMINATED`. Use chat mode
  for anything that needs follow-ups.

## Context cost

Every app-server session loads the CLI's configured MCP servers and plugins
into the model context. On an operator machine with a large global MCP
server registered in `~/.zcode/cli/config.json`, that was ~270k input tokens
per turn (mostly cache reads) in the Phase 1 probes. Chat mode therefore
passes AO's own tool servers as `session/create.mcpServers`, which
**replaces** the operator's global `config.mcp.servers` for the session
(plugin-provided servers are still merged in). The CLI's own session title
generation is disabled on create to save a model call. Follow-ups still open:
a per-worktree `.zcode/config.json` with `plugins.enabled:false` to drop the
plugin servers and the skills index as well, and the same trim for the
one-shot path, which has no per-session parameters.

## Restart, hibernate, resume

The app server survives daemon restarts behind the chat host; a reattached
daemon resumes the same process. When the host is gone, AO resumes the stored
session id with `session/resume` in a replacement process, which keeps the
model's memory (turn numbers continue), and refuses to start a fresh session
in its place unless the server reports `Session not found`. An idle session
(no running turn, no active tool calls, background jobs or pending requests)
can be hibernated: the app server is stopped and the native session resumed
on demand. After a resume or a TUI handoff, AO imports the native transcript
through `session/read` (visible user prompts, assistant text and reasoning;
runtime system reminders are skipped).

## Known limits

- Model and reasoning level are chosen per turn (`modelSelection` with the
  level from the model's advertised list); there is no steer, compaction,
  fork or rollback through the app server yet.
- Tool output is not streamed by the app server; the activity row settles
  with the tool's result (output, exit code, duration).
- Tool parts in the native transcript are not imported yet (no capture
  covered one); text and reasoning are.
- The provider's own error prose is surfaced on a failed turn (for example
  Z.AI `1113` "Insufficient balance or no resource package") with its code
  and attribution; AO does not map it to a re-login prompt.
- TUI mode has no workspace hooks in v1 (zcode hooks live in per-plugin
  `hooks.json` manifests), so a TUI session's active work reads as idle to
  the dashboard; the reaper flips the session to exited within one tick of
  `zcode -p` returning. Chat mode reports activity from the protocol.
- TUI restore is cwd-scoped `zcode -c` (latest session for the worktree);
  chat mode stores the native session id.
