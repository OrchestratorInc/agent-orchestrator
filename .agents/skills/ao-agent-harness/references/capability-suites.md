# Capability suites, matrix and failure diagnosis

After the TUI lifecycle, the runner drives every AO capability that Codex and
Claude Code declare and that can be proven through the loopback API without
human input. Each run's report gets a **capability matrix** per agent. Each
failed or blocked gate also gets a **diagnosis** that tells you where to start
fixing it. The catalog lives in
[`scripts/audit_capabilities.mjs`](../scripts/audit_capabilities.mjs), and the
suites live in [`scripts/audit_suites.mjs`](../scripts/audit_suites.mjs).

Select suites with `--suites extras,chat,interface` (the default is all three).
Use `--suites none` to run only the TUI lifecycle. The suites run only for an
agent whose strict preflight allowed the live lifecycle. Otherwise their gates
are recorded as `NOT_RUN` with the prerequisite that stopped them.

## Suites

| Suite | Session | Gates | What proves it |
| --- | --- | --- | --- |
| `extras` | The lifecycle's TUI session; no provider turn | `ao_models_refresh`, `model_efforts`, `tui_system_prompt_file`, `ao_skill_pointer`, `tui_hooks_installed`, `tui_usage_recorded` | Catalog refresh returns models with valid `efforts`/`defaultEffort`. `<data>/prompts/<id>/system.md` carries the project agentRules token. `<data>/skills/using-ao/SKILL.md` exists and `system.md` points at it. Claude's worktree `.claude/settings.local.json` wires `ao hooks claude-code`; for Codex (hooks are launch `-c` flags), the hook-delivered native id is persisted. `GET /usage/sessions/{id}` reports tokens. |
| `interface` | The restored TUI session, after `post_restore_message` passes | `iface_status`, `iface_tui_to_chat`, `iface_chat_continuation`, `iface_chat_to_tui`, `iface_tui_continuation` | `GET /sessions/{id}/interface-transition` offers Chat. A `drain` transition reaches `phase: completed` with `session.mode: chat`. The first Chat turn recalls the TUI history-only token. Switching back reaches `mode: tui`. After the native ready cue, the TUI keeps the Chat edits and answers. |
| `chat` | A new `mode: chat` session in the same project | see below | Conversation snapshot, proof file in the Chat worktree, sqlite `sessions.provider_conversation_id`, and the cancellation process witness. |

Chat gates, in order:

1. `chat_spawn` proves AO created the Chat session. It fails if AO silently created a TUI session instead.
2. `chat_controller_ready`, `chat_initial_turn` and `chat_instructions` prove that the initial prompt ran once in the worktree, using the agentRules and `AGENTS.md` tokens.
3. `chat_mcp_servers` proves the AO MCP server is listed in the snapshot.
4. `chat_turn_lifecycle`, `chat_followup` and `chat_idempotent_send` prove the turn ran (running → completed, streaming recorded), its edit landed, and a repeated `clientMessageId` returns `duplicate: true` without a new turn.
5. `chat_models` and `chat_settings` prove the model list works and a model/effort change persists. The settings read back from the snapshot and the `conversations.reasoning_effort` column.
6. `chat_skills` and `chat_usage` cover skills listing and Chat token usage.
7. `chat_steer` proves a steer lands in the running turn. Without the `steer` capability, AO must refuse the steer.
8. `chat_interrupt` and `chat_post_interrupt_continuation` use the same PID and kernel-start witness as TUI cancellation. They prove the turn ended `interrupted`/`cancelled`, no late side effect appeared, and the next turn works.
9. `chat_approval_reject` and `chat_approval_approve` run with `accept-edits`. They prove a pending approval appears, the offered reject/allow decision is delivered, the side effect matches the decision, and nothing ran before the decision. Approval mode returns to bypass afterwards.
10. `chat_kill`, `chat_restore` and `chat_restore_continuation` prove the same `provider_conversation_id` before and after restore, and that the restored Chat recalls the history-only token.
11. `chat_compact` proves `compactedAt` appears and the conversation still answers.

## Statuses

`UNSUPPORTED` counts as passing. Use it only when AO does not declare the
capability for that harness (catalog `support` is `no`, `conditional` or
`unknown`) **and** AO refused truthfully. Truthful refusal means one of three
things: the snapshot omits the capability, the steer API rejects the request,
or Chat spawn returns a typed 4xx. For a capability declared `yes`, the same
observation is a `FAIL`, because it is a regression. A capability with no gate
in the run is `NOT_RUN`, never `PASS`.

Capabilities the runner deliberately does not drive appear in the matrix as
`MANUAL`, each with its reason and where to verify it:

- in-page install
- login and Codex accounts
- reviewer
- attachments and images
- rollback, fork and edit
- hibernation
- cross-harness switch-agent
- subagents

Use the [journey map](audit-journeys.md) for these. A `MANUAL` row is not
coverage.

## Diagnosis

Every `FAIL` or `BLOCKED` gate carries `diagnosis` in the JSON report, in its
`issue-NNNN.json`, and as one line in `runner.log`:

```text
codex: chat_interrupt FAIL — [Chat interrupt of an active tool command] — expected interrupt stops the witnessed command; turn ends interrupted; no late side effect — observed witnessed command pid 4242 is still running after interrupt (process not reaped); POST /api/v1/sessions/s1/conversation/interrupt → HTTP 200 — likely interrupt not forwarded to provider — inspect backend/internal/service/chat/controller.go; evidence=…/issue-0007.json
```

| Field | Content |
| --- | --- |
| `capability`, `capabilityTitle`, `declaredSupport` | Catalog row and whether AO declares it for this harness |
| `expected` | What the gate requires |
| `observed` | The gate reason plus each API call (`METHOD path → HTTP status CODE: message`) and command outcome found in its evidence |
| `likelyCauses` | Known failure modes for this gate, most likely first |
| `inspect` | AO source files to open first, expanded for the harness (`claudecode`/`claudeacp`, `codex`/`codexappserver`) |
| `daemonLogExcerpt` | Up to 25 daemon log lines that look like errors and name the session or agent, or else the most recent error lines |

`report.md` repeats this under **Failure diagnosis**, and shows the capability
matrix first in each agent's section.

## Keeping the catalog true

When an adapter, driver or route moves, update its `inspect` paths.
`audit_capabilities.test.mjs` fails when a path no longer exists, and
`audit_suites.test.mjs` drives each suite against a fake daemon. When AO adds
a capability for Codex or Claude, take these steps:

1. Add a catalog row with its gates.
2. Add the gate hints (expected and causes).
3. Add the suite step that collects positive evidence.
4. Add a fixture for its failure path.

Do not record a capability as `PASS` from an accepted request alone.
