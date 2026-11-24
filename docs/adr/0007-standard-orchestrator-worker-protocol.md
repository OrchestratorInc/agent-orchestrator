# ADR 0007: standard orchestrator and worker protocol

Status: proposed.

## Context

Orchestrator and workers talk through two AO-specific commands today:

- Workers run `ao report --checkpoint|--needs-input|--stuck|--done --note ...`
  (`backend/internal/cli/report.go`). The worker prompt
  (`backend/internal/session_manager/prompt.go`, "Worker Reports") and the
  `using-ao` skill teach each harness when and how to call it.
- The orchestrator runs `ao send --session <id> --message ...`, which queues
  free text prefixed `[from <id>]` on the worker.

Both ride loopback HTTP into SQLite. The report coordinator
(`backend/internal/service/report/coordinator.go`) batches reports by urgency
and delivers them into the orchestrator's conversation as a turn.

The daemon already receives, from every Chat worker, the standard signals that
`ao report` asks the agent to restate by hand:

| `ao report` flag | Standard signal the daemon already has |
| --- | --- |
| `--checkpoint` | ACP plan updates (`turn.plan`) and tool calls |
| `--needs-input` | ACP permission requests and form/URL elicitation (`approval.requested`, `input.requested`) |
| `--stuck` | provider errors, `blocked` and `exited` activity |
| `--done --note` | the turn settles idle (`turn.completed`) and its final assistant message (`message.completed`) |
| `--pr-created` / `--pr-reviewed` | PR facts from the SCM observer |
| `--artifact` | files registered with `ao artifact add` |

Asking agents to repeat what the protocol already says costs prompt space,
fails when the agent forgets, and is AO-specific behavior that no model is
trained on.

## Decision

Follow the standards the harnesses already speak, and the subagent pattern
that coding agents are trained on: a worker is given a prompt, works, and its
final message is its result.

1. **The daemon writes reports, not the agent.** A daemon-side worker reporter
   turns the signals above into the existing report rows. The report table,
   coordinator, batching, and the Worker Updates UI stay; only the producer
   changes.
2. **`ao report` is removed** with its prompt section, skill page, CLI code,
   and `POST /api/v1/reports`.
3. **Blocking questions use elicitation.** A worker's pending ACP permission or
   elicitation is relayed to its orchestrator, which answers it or passes it
   to the user. The answer resolves the original request, so the worker leaves
   its waiting state through the standard path. Answers record whether the
   user or the orchestrator gave them, so an orchestrator answer is never
   presented as user approval.
4. **Orchestrator tools follow the subagent shape.** `ao mcp` exposes
   `spawn_worker` (description, prompt, agent, background), `send_message`,
   and `stop_worker`. A finished worker arrives as a notification turn carrying
   its final message. The `ao spawn` / `ao send` CLI stays as the fallback for
   harnesses without MCP.
5. **Only what no standard covers stays custom:** durable delivery into an
   idle or busy orchestrator (the coordinator), answer provenance, and sender
   identity. Provenance and correlation travel as `_meta` on ACP and MCP
   messages instead of a new envelope.

## Alternatives considered

- **MCP as the transport.** Request/response from agent to tool; harnesses do
  not reliably accept server-initiated messages, so it cannot wake an idle
  orchestrator. Used as a binding only (decision 4).
- **A2A.** Its task states fit, but no coding harness speaks it and it assumes
  network-hosted agents. A possible later edge gateway, not the internal path.
- **HTTP/webhooks between agents, or a message broker.** Agents cannot host
  endpoints, and SQLite already provides ordering, acknowledgement and replay
  for a local-first app.

## Consequences

- Workers need no AO reporting instructions; the "Worker Reports" prompt
  section is deleted.
- TUI workers have no ACP stream. The reporter uses their hook-derived
  activity (`idle`, `waiting_input`, `blocked`, `exited`) and points the
  orchestrator at `ao session history` instead of quoting a final message.
- A daemon-written done report means "the worker's turn ended", not "the
  assigned work is complete". The worker is usually waiting, but it may have
  exited since. The daemon cannot tell a
  finished task from a turn that ends on a plain-text question, and it never
  claims completion. The note carries the worker's final message verbatim (a
  message over the report limit keeps its start and points at
  `ao session history`), and
  the orchestrator judges from it whether the work is finished or needs an
  answer, as a parent agent does with a subagent's result. Questions raised
  through ACP permission or elicitation requests are reported as needs_input.
  Step 2 renames the state shown to the orchestrator and in Worker Updates to
  match, so the label does not overstate completion.
- A done report waits until the worker has stayed idle for 30 seconds, and
  covers everything since the work started. Work that resumes sooner, such as
  a harness that reports idle between steps or a queued message, joins the
  same report. Work that resumes after a done has been written starts a new
  stretch, which gets its own done.

## Rollout

Each step is one PR and leaves `main` working.

1. Add the daemon-side worker reporter alongside `ao report`.
2. Remove `ao report`, its prompt section, skill page, route and DTOs.
3. Relay worker elicitation and permission requests to the orchestrator, with
   provenance.
4. Add the subagent-shaped orchestrator tools to `ao mcp` (after the MCP
   server moves to the official Go SDK, tracked separately), then trim the
   orchestrator prompt's `ao send` instructions.
