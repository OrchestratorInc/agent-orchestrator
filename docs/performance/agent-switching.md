# Agent switching performance

Switching a chat to another agent should feel instant and lose nothing. This
page counts the time from send to the target agent running the user's message.
The model's own time to first token comes after that and is not counted.

## How a switch runs

1. Picking an agent in the model menu records the choice in the renderer,
   prewarms the target's readiness check (`POST
   /agents/{agent}/chat/prewarm`), and, once the choice has held for 400ms,
   starts the target's process (`POST /sessions/{id}/switch-agent/prewarm`).
   No provider session is created. See "Starting the target on pick" below.
2. The next send carries the switch: `POST /sessions/{id}/switch-agent` with the
   message, model, and reasoning effort. The message shows at once and reads as
   Working from the send. Messages sent in the meantime wait in the queue dock.
3. The daemon stops the source agent and starts the target fresh. It hands
   context over in the target's system prompt and delivers the message as the
   target's first turn. There is no separate activation turn.
4. The chat shows a "Switched from X to Y" divider above that message.

Every chat agent with a Chat driver can be a source or a target: Claude Code,
Codex, opencode, Droid, Cursor, Grok, fx, pi, and omp. TUI sessions still
switch only between Claude Code, Codex, and fx.

## What the target is told

The handoff carries the git, PR, and CI facts, the original task, and AO's own
transcript of the chat. The transcript holds every message plus a compact line
for each action: commands with their exit code (and a failed command's output
tail), file changes, MCP tool calls, plans, errors, approvals, and questions.
It fills a 96 KiB budget from the newest end and shrinks if the prompt would
overflow. The provider's native transcript path is added as a pointer when one
exists.

The target always starts a new native session, even when switching back to an
agent that ran earlier in the chat. Resumed Claude Code and Codex sessions
ignore a new system prompt, so they would miss whatever happened while they
were away. Cursor ignores the system prompt entirely, so its handoff goes in
as a text block ahead of the first message. fx takes standing instructions as
an embedded resource only when its URI names an existing local file, so its
driver writes them to the session's prompt directory and points a `file://`
URI there; an `ao://` URI made fx drop them, and with them the handoff. fx also
writes its own diagnostics (`[context] ...`, skill discovery warnings) into the
reply stream; its driver drops them while they lead a turn.

The chat's approval mode carries over to the target.

## Results

Measured on one macOS machine in October 2026 against a headless daemon on its
own port and data dir. Each run starts a fresh chat on the source agent and
plants context over two turns: fact A plus a real shell command, then fact B.
It then switches with a five-part quiz the target must answer from the handoff
alone: both facts, the exact command, the file it created, and the first word
of the first message.

"Ready" is send to the target running the user's message. On main a switch
first runs an activation turn and the message is sent after it, so main's
number covers both.

Main refuses every chat switch outside Claude Code and Codex with
`UNSUPPORTED_SWITCH_HARNESS`. For those agents the baseline is main's protocol
run on this branch: switch with no message, wait for the activation turn, then
send the quiz. That baseline already includes this branch's startup fixes, so
it understates main. For Codex it measured 7.44s against real main's 11.57s,
and for Claude Code 8.03s against 8.85s.

| Switch from Claude Code to | main | main's protocol | This branch | Speedup | Quiz |
| --- | --- | --- | --- | --- | --- |
| Codex | 11.57s | 7.44s | 0.36s | 32× vs main | 5/5 |
| Grok | refused | 19.91s | 0.70s | 28× | 5/5 |
| pi | refused | 7.92s | 0.76s | 10× | 5/5 |
| omp | refused | 8.39s | 1.09s | 7.7× | 5/5 |
| fx | refused | 5.97s | 1.61s | 3.7× | 5/5 |
| opencode | refused | 11.34s | 1.72s | 6.6× | 5/5 |
| Droid, picked 1s or more before send | refused | 7.33 to 7.82s (4 runs) | 1.32 to 1.68s (8 runs) | 4.4× or more | 5/5 |
| Droid, cold | refused | 7.53s (median) | 2.63s (median of 5) | 2.9× | 5/5 |
| Cursor, picked 6s before send | refused | 13.27 to 21.27s (4 runs) | 1.04 to 1.90s (4 runs) | 7× or more | 5/5 |
| Cursor, picked 3s before send | refused | 13.27 to 21.27s | 2.34 to 3.51s (3 runs) | 3.8× or more | 5/5 |
| Cursor, picked 1s before send, or cold | refused | 14.15s (median) | 4.94s (median of 8, 4.3 to 7.0s) | 2.9× | 5/5 |

Codex to Claude Code: main 8.85s, main's protocol 8.03s, this branch 2.03s
(4.4× vs main), 5/5. "or more" compares the slowest run on this branch with the
fastest baseline run; other multi-run numbers are medians. pi's first ever
start on a machine took 4.8s.

A chain through every agent, Claude Code to Codex, opencode, Droid, Grok, fx,
omp, pi, Cursor, and back to Claude Code, telling each one a new fact, recalled
all 9 facts at the end in each of four runs.

Rerun on the final build with the target picked 6s before send, from Codex:
pi 0.43s, Claude Code 1.04s, opencode 1.18s, Droid 1.56 to 1.70s, all 5/5.
Cursor took 1.11 to 4.92s, because its own startup ranged from 3.6 to 9.5s
that day; a raw cold Cursor start measured about 6s, with or without the
preload's later changes.

## Where the time goes

Each switch logs `agent switch timing` with one duration per step. Each ACP
start logs `chat: ACP start stage` for `process_open`, `initialize`, and
`session_new`.

AO's own steps (database writes, the transcript excerpt, publishing the handoff)
take 10 to 30ms in total. The rest is stopping the source and starting the
target.

### Stopping the source

AO asks the provider to close its session and waits up to 2s for the answer.
The host then closes the provider's stdin, waits 1s, sends SIGTERM to the
provider's process group, and kills it at 3s. When the provider answered the
close, its session is saved and the 1s becomes 300ms.

Two agents gain nothing from those waits, so their drivers set `QuickStop`: a
500ms close wait and the 300ms grace.

- Cursor has no `session/close` and ignores stdin closing. It exits 50ms
  after SIGTERM.
- Grok saves its session after every turn. A Grok process killed with SIGTERM
  and no close, then resumed in a new process, still knew the last turn's
  fact (two of two runs). Its close answer takes 2.5 to 4s.

Measured stops: Claude Code, Codex, opencode, fx, omp, and pi under 0.2s,
Cursor 0.32s, Droid 1.1s (it exits on stdin closing after about a second),
Grok 1.4 to 1.7s. Earlier versions cut every agent off after 300ms and a 500ms
close; that was fast but could stop an agent mid-save, and a failed switch
resumes the source from that save.

### Starting the target

| Agent | Start through AO |
| --- | --- |
| Codex | ~0.3s |
| pi, Grok | 0.5 to 0.8s |
| omp, fx, opencode | 1.0 to 1.7s |
| Claude Code | ~1.8s |
| Droid | 2 to 3s |
| Cursor | 4 to 6.5s |

## Starting the target on pick

Most of a switch is the target's own startup: Cursor about 4s loading account,
settings, and model services before its first session, Droid 2 to 3s in
`initialize` and its first `session/new`, the rest 0.5 to 2s. Picking an agent
in the model menu now starts it while the user finishes typing. Claude Code,
Cursor, Droid, Grok, fx, opencode, omp, pi, Command Code, Kimi, Kimchi, and
Qwen take part. Codex starts in about 0.3s and does not.

- The daemon prepares the target's workspace files, builds its standing
  instructions and environment as the switch would, and starts the provider in
  a persistent host under a key of its own (`<session>.standby-<random>`),
  because the current agent still owns the session's host. Each standby gets a
  new key, so a late one can never collide with the next. It runs
  `initialize`, and for Cursor `cursor/list_available_models`, which loads
  those services without creating a session.
- No provider session exists until the send. The process holds a browser
  bearer whose verifier is stored only when the switch adopts it, after the
  source agent stops, the same point where a cold switch rotates it.
- The switch adopts the standby when the target, model, effort, approval mode,
  environment, and standing instructions match. It asks the host to rekey onto
  the session (`persistenthost.Rekey`), sets the provider scope, and opens the
  session. Agents that read the standing instructions at launch (Droid,
  opencode, omp, pi, Kimi, Kimchi, Qwen) get the handoff ahead of their first message,
  as Cursor already does.
- Anything else drops the standby: a different pick, picking the current agent
  again, other settings at send, or two minutes unused. A send that arrives
  while the standby is still starting waits up to 10s for it, then starts the
  agent itself. A standby that finishes after that shuts itself down without
  touching the workspace files, which belong to the switch by then.

The gain depends on how long before the send the user picks. Cursor's standby
takes about 4s to be ready (plus a one-time readiness check of about 1.7s per
daemon), so a pick 1s before sending saves almost nothing, 3s saves about half,
and 6s brings Cursor to 1 to 2s. Droid's standby is ready in about 1.1s.

## Starting a new chat on pick

The new chat page does the same before the first message. On open it reserves
the session id, branch, and worktree (`POST /task-preparations`, or a scratch
directory for a chat with no project). Once the agent, model, and effort have
held for 400ms, it starts that agent there (`POST
/task-preparations/{token}/agent`). The send passes the preparation's token,
and the launch adopts the standby when everything matches. Terminal launches
and cloud projects skip this.

| Agent | Cold | Picked 6s before send |
| --- | --- | --- |
| Cursor | 5.67s (7.7 to 8.8s on other days) | 1.0 to 2.8s |
| Droid | 2.2 to 3.7s | 1.3 to 1.6s |
| Claude Code | 1.8 to 2.0s | 1.0 to 1.4s |

## When something goes wrong

- A message sent while a switch runs waits in the queue dock and is stored
  with the switch it waits for, so it survives a reload. It goes to the new
  agent only when that switch completes. A switch that is refused, fails, or
  was never recorded puts the text back in the composer, after anything typed
  since.
- A failed switch clears the picked agent, so the next send does not quietly
  try the switch again.
- If the handoff cannot be built, the target gets the source agent's own
  transcript tail instead. An agent resumed after a restart is told it is
  resuming, without a stale handoff.
- The Cursor preload never blocks a launch. It steps aside when a proxy is
  configured or its file is missing, and stale copies are removed.
- A discarded preparation removes only its own scratch directory, and falls
  back to the normal workspace teardown when that check fails.

## opencode: the per-worktree install (fixed)

opencode npm-installs `@opencode-ai/plugin` into every `.opencode` directory it
loads (`packages/opencode/src/config/config.ts`). AO writes its activity plugin
and the using-ao skill into each worktree's `.opencode/`. So the first opencode
start in every new worktree ran that install, which took 4 to 7s inside
`initialize`. An agent switch to opencode nearly always lands in such a
worktree.

opencode skips the install when `node_modules` exists and `package-lock.json`
names every declared package (`packages/core/src/npm.ts`; it checks names, not
versions). AO's plugin imports the package only as a type, which Bun erases, so
nothing needs installing. `GetAgentHooks` therefore seeds an empty
`.opencode/node_modules` and a stub `package-lock.json`, but only when
`.opencode` holds nothing except AO's files. A repository with its own opencode
plugins keeps the real install, and a stub left from before such plugins
appeared is removed. The stubs sit under an AO-managed `.gitignore`, so the
worktree can still be removed. With the stubs, a switch to opencode in a fresh
worktree takes 1.7s instead of 8.4s. The plugin still fires.

opencode v2 also copied its downloaded tool cache (`bin/`) into every new data
home. That copy is now skipped.

## Cursor: slow session creation

Cursor's first `session/new` on a process makes about a dozen calls to
`api2.cursor.sh`, mostly one after another. From this machine the round trip is
about 225ms, and each call took 0.7 to 1.3s, because every call opened a new
TCP and TLS connection.

The CLI bootstraps `global-agent` 3.0.0, which replaces Node's HTTPS agent with
a wrapper that declares no `keepAlive` or `maxSockets`. Node then sends
`Connection: close` on every request. The Cursor driver now loads a small
preload (`cursoracp/netpreload.cjs`) through `NODE_OPTIONS`:

- When no proxy is configured, it marks the wrapper keep-alive, so requests
  pool on Node's own keep-alive agent. Reused calls take 0.25 to 0.5s.
- It opens three TLS connections to the API host at start, which the agent uses
  for the calls that run in parallel.
- It removes itself from `NODE_OPTIONS`, so commands Cursor runs for the user
  never load it, and it does nothing outside `cursor-agent acp`.

Start plus first `session/new`, raw, medians of 6 interleaved runs: 6.90s
without the preload, 5.81s with keep-alive only, 5.02s with both. A second
session on the same process went from 2.0s to about 1s. Through AO, a switch to
Cursor went from a ~6s median to 4.94s.

What remains is Cursor's own server time: about six rounds of settings and
model calls, several of which take 0.5 to 1.7s on the server. None of these
changed it:

- CLI flags, a clean `cli-config.json`, or removing MCP servers and skills.
  AO's MCP server adds 0.3 to 0.5s.
- `session/load`, which is no faster. A session with no turns cannot be loaded
  at all ("Session not found").
- An idle, already initialized process. Only the second `session/new` on a
  process is fast.
- `session/set_config_option` for the model takes 2 to 3s and rewrites the
  CLI's global default model in `~/.cursor/cli-config.json`, which changes
  every other Cursor session. Do not change the model at switch time.

Droid is similar: its `initialize` takes 0.7 to 1s and its first `session/new`
0.9s raw, plus about 0.4s inside Droid for AO's MCP server (AO's own MCP
handshake answers in 12ms). It is a compiled binary, so there is no preload
equivalent.

Without a pick ahead of the send, the cold path above is the floor. Creating
the Cursor session itself ahead of the send would cut it further but costs a
Cursor session for every pick that is not sent, so the standby stops at a
started, warmed process.

## Reproducing

The scripts for these numbers are kept out of the repo.

- Switch bench: start a standalone chat on the source, plant the two facts,
  then `POST /sessions/{id}/switch-agent` with the quiz as the message. Poll
  `/sessions/{id}/agent-switches` and `/sessions/{id}/conversation` until the
  quiz turn runs and completes.
- For main, switch without a message, wait for the switch to complete, then
  send the quiz.
- Raw ACP timing: spawn the agent, send `initialize`, then two `session/new`
  calls with `mcpServers: []`, timing each.
- Read `agent switch timing` and `chat: ACP start stage` from the daemon log.
