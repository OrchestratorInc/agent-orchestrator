# Coder Agents Adapter

AO runs [Coder Agents](https://coder.com/docs/ai-coder/agents) — the agent
built into a Coder deployment — as a structured Chat harness. Inference and the
chat loop run in your Coder deployment. Every file and command tool is a
client-executed tool that AO runs in the session's local worktree, behind AO's
own approvals. There is no Terminal UI mode.

## Requirements

- A Coder deployment at **v2.37 or newer** (the stable `/api/v2/chats` API),
  with Coder Agents enabled, an AI provider configured by an administrator, and
  at least one enabled model you can use.
- The official `coder` CLI, signed in to that deployment. Settings → Harness
  installs it (Homebrew or `install.sh` on macOS, `install.sh` into
  `~/.local/bin` on Linux, `winget install Coder.Coder` on Windows).

## Sign in

Use **Log in** on the Coder Agents row in Settings → Harness, or run:

```bash
coder login https://coder.example.com
```

AO reads the session the way the CLI does:

| Source | Used for |
| --- | --- |
| `CODER_URL` | Deployment URL; otherwise the `url` file in the CLI config directory (`CODER_CONFIG_DIR`, default `~/.config/coderv2` on Linux, `~/Library/Application Support/coderv2` on macOS, `%APPDATA%\coderv2` on Windows) |
| `CODER_SESSION_TOKEN` | Session token; otherwise AO runs `coder login token`, so a token in the OS keyring works without AO reading the keyring itself |
| `CODER_AGENTS_ORGANIZATION` | Organization ID or name, when you belong to several and none is the default |

The token stays in the AO daemon's memory. It is never written by AO, never
logged, and removed from the environment of every command the agent runs.
Requests refuse redirects to another origin, so the token is never sent
anywhere except the deployment.

Harness Settings shows **Connected** only after a round trip: the deployment
accepted the session (`GET /api/v2/users/me`) **and** your organization offers
at least one enabled Coder Agents model. A session that exists but cannot be
verified, or a deployment without an enabled model, reads as **Configured**.
A missing or rejected session reads as **Not set up**.

## Models

The model list is the deployment's enabled model configs
(`GET /api/v2/organizations/{org}/chats/models`). AO stores the model config
ID, offers each model's own reasoning efforts, and rejects a model or effort
the deployment does not offer.

## How tools run

AO creates the chat **without a Coder workspace** and declares five
client-executed tools (`unsafe_dynamic_tools`, a Coder API marked
experimental):

| Tool | Effect |
| --- | --- |
| `ao_read_file`, `ao_list_directory` | Read inside the worktree |
| `ao_write_file`, `ao_edit_file` | Change files inside the worktree |
| `ao_execute` | Run a shell command with the worktree as working directory |

Paths are confined to the worktree (and any additional workspace roots), with
symlinks resolved, so a tool cannot read or write outside them. Commands run on
your machine as your user.

AO's approval modes apply to these tools:

| Mode | Asks before |
| --- | --- |
| Ask first (default) | File changes and commands |
| Accept edits | Commands |
| Bypass permissions | Nothing |

Auto mode is not offered: AO has no sandbox to run commands unasked. A
read-only (reviewer) conversation declares only the read tools.

AO never lets the conversation use a Coder workspace. If the agent calls
`create_workspace`, `start_workspace` or `spawn_agent`, or Coder binds a
workspace to the chat, AO interrupts the run, fails the turn, and refuses
further turns in that session. Start a new session to continue.

Each tool call is journaled under the AO data directory before it runs. A call
that Coder replays after a reconnect or restart is answered from the journal
and never run twice; a call that was interrupted mid-run is reported to the
agent as having an unknown effect instead of being re-run.

## Lifecycle

- **Start:** AO creates the Coder chat with your first message, AO's system
  prompt as Coder's hidden per-chat system prompt, and the labels
  `ao.conversation` and `ao.session`.
- **Stop:** AO interrupts the run (`POST /api/v2/chats/{id}/interrupt`).
- **Kill:** interrupts any run, then detaches. Coder keeps the chat history.
- **Restore:** AO finds the chat by its `ao.conversation` label and checks the
  owner, the session label and that no workspace is bound. A missing,
  archived, foreign or workspace-bound chat is a typed resume failure, never a
  silent new chat. A run that continued in Coder while AO was away is adopted
  as a turn.

## Limitations

- Image and resource attachments are not sent.
- AO's own MCP tools (`ao mcp`) are not offered, because Coder cannot start a
  local stdio server; the agent can still run `ao` commands through
  `ao_execute`.
- Coder's built-in server-side tools that do not need a workspace (for example
  template listing or web tools an administrator enabled) still run in Coder.
- A Coder Community license allows a limited number of concurrently active
  agents; Coder's own limits apply.
