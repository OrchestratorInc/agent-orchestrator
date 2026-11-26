# Plan: Local bots, Phase 0

> **Status:** Design spec, not yet implemented. Written 2026-11-26.
> Background and competitor research: [Bots: competitive landscape and AO implementation plan](../research/bots-competitive-landscape-2026-11.md).

## Goal

Ship **bots** on the desktop daemon. A bot is a named, long-lived agent that owns an outcome in one project, remembers what it learns, delegates code work to worker sessions, and surfaces only what needs its owner in a **Needs you** inbox.

Phase 0 is local-only: you reach a bot from AO's own chat, the CLI, mobile, and schedules. The design is hybrid from the start. Every decision here must keep a later move possible, so that one bot can live on a laptop, a self-hosted remote host, or an AO Cloud sandbox and move between them. That hybrid is the gap no competitor fills. See the research doc's "Where bots run" section.

## Non-goals for Phase 0

- Slack, Linear, GitHub-event, and webhook ingress. That is Phase 1, through an AO Cloud relay.
- Moving a bot between homes, and cloud homes. That is Phase 2. Phase 0 only reserves the fields and keeps the bot portable.
- Teammates asking a bot, sharing roles, and pooled credentials. That is Phase 3.
- Token or dollar budgets. Phase 0 enforces a worker-count cap only. Spend budgets come in Phase 2, built on `service/usage` and `pricing`.
- Templates beyond porting the three bundled automation templates.

## Concepts

| Term | Meaning |
| --- | --- |
| **Bot** | A durable entity: handle, display name, purpose, project, agent config, home, limits. It is not a session. Its sessions come and go. |
| **Bot home** | The host whose daemon owns the bot. Phase 0 always uses `local`. The renderer already treats local as "a host like any other" (`frontend/src/renderer/lib/hosts.ts`), so a bot is addressed as `Ref{host, id}`. |
| **Bot session** | The current agent session running the bot. It uses a new session kind, `bot`. It hibernates between turns and is recycled into a fresh session when its context grows stale. |
| **Bot conversation** | The durable narrative across every bot session. Its identity survives recycling, the same way the project-scoped orchestrator conversation survives orchestrator replacement. |
| **Bot folder** | `$AO_DATA_DIR/bots/<bot-id>/`, holding `SOUL.md`, `MEMORY.md`, and history. It is self-contained and portable. |
| **Crew** | Worker sessions the bot started. Each carries a durable `bot_id`. |
| **Needs you** | A derived, read-only projection of open items across a bot and its crew that only the owner can unblock. |
| **Bot ask** | A question the bot raised through `ao bot ask`. It is the one new durable fact behind the inbox. |

## Key decisions

### 1. A bot is a new entity plus a new session kind, not another orchestrator

AO allows exactly one live orchestrator per project, and several places enforce it:

- the per-project lock and `activeOrchestrators` check in `service/session/service.go`;
- `newestOrchestrator` report routing in `service/report/coordinator.go`;
- the "competing live orchestrator" guard on project conversation binding in `storage/sqlite/store/conversation_store.go`;
- a single orchestrator workspace path per project in `adapters/workspace/gitworktree/workspace.go` (`<project>/orchestrator/<prefix>-orchestrator`).

Making bots `KindOrchestrator` sessions would break all four, and it would prevent having a triage bot and a CI bot in the same project. Instead:

- Add `domain.KindBot SessionKind = "bot"`.
- Bot sessions behave like orchestrators for launch, permissions inheritance, and chat mode. They are excluded from every project-singleton check.
- The project orchestrator is unchanged and keeps working alongside bots.

**Required audit.** Every `KindOrchestrator` branch must be checked and given an explicit `KindBot` decision. At the time of writing these are:

| Area | Files |
| --- | --- |
| Session manager | `session_manager/manager.go`, `chat_spawn.go`, `chat_spawn_async.go`, `chat_handoff.go`, `tui_version_refresh.go` |
| Session service | `service/session/service.go`, `telemetry_attribution.go` |
| Chat | `service/chat/service.go`, `hibernate.go` |
| Session fork | `service/sessionfork/service.go` |
| Reports | `service/report/coordinator.go` |
| Automations | `service/automation/service.go` |
| Storage | `storage/sqlite/store/session_interaction.go`, `conversation_store.go`, `project_store.go` |
| Workspace adapters | `adapters/workspace/gitworktree/workspace.go`, `adapters/workspace/scratch/workspace.go` |
| Agent adapters | `adapters/agent/kiro/kiro.go` |
| HTTP | `httpd/controllers/sessions.go` |

Rerun `grep -rn KindOrchestrator backend/internal` before starting; the list will drift. Each site gets a test that pins the `KindBot` behaviour.

### 2. The bot conversation reuses the project-conversation rebinding pattern

`conversations` already has `scope IN ('session','project')`. A project conversation keeps its identity while `current_session_id` is rebound to each replacement orchestrator. Bots need exactly this.

- **Recommended:** add `scope = 'bot'` with a `bot_id` column and a unique index per bot. The `CHECK` constraint lives in `0066_chat_session_mode.sql`, so this is a SQLite table rebuild in a new migration. Merged migrations must not be edited. The rebuild has to preserve every FK from `conversation_turns`, `conversation_messages`, `conversation_activities`, and `conversation_provider_events`.
- **Fallback if the rebuild is too risky:** a `bot_conversations(bot_id, conversation_id)` link table pointing at a `session`-scope conversation, with rebinding done in the bot service. This is less uniform, and handoff code would need a bot-aware branch.

The first implementation PR picks one. The rest of the spec does not depend on the choice.

### 3. Memory is files in the bot folder, written only through the daemon

Files rather than DB rows, because they are:

- editable by the user;
- readable by any harness;
- portable: moving a bot later means copying one folder.

All three competitors with persistent bots (Orca, Capy, Kiro Crew) converged on this.

```text
$AO_DATA_DIR/bots/<bot-id>/
  SOUL.md          # who the bot is: purpose, rules, boundaries. Owner-written.
  MEMORY.md        # what the bot has learned. Bot-maintained through the daemon.
  journal/
    2026-11-26.md  # append-only daily log of turns and outcomes (input to tidy)
  .history/
    MEMORY.md.<ts> # last 20 snapshots, for undo
```

- **Location.** The folder is under `$AO_DATA_DIR`, per the `~/.ao`-only hard rule. It is never in the repo or a worktree.
- **Write path.** The bot never writes these files directly. A bot session's workspace is a repo checkout, and harness sandboxes may block writes outside it. The bot uses `ao bot memory append|replace` instead. The daemon then:
  - checks size (MEMORY.md is capped at 16 KiB);
  - rejects content matching credential patterns, since the bot must never save a secret;
  - snapshots the previous version to `.history/`;
  - emits a change event.
- **SOUL.md is owner-only.** It is edited in the UI, the API, or a text editor. The bot can propose a SOUL change only as a bot ask.
- **Injection.** At every bot-session start (first start, wake, recycle), the system prompt gets SOUL.md, MEMORY.md, the bot's open asks, and a crew summary. It goes through the existing `writeSystemPromptFile` path in `session_manager/manager.go`.
- **Portability rule.** Memory must not contain absolute paths or daemon-local session IDs as load-bearing references. The tidy prompt says so, and the folder must be able to move to another home unchanged.

### 4. Bot sessions hibernate between turns and are recycled

- **Hibernate.** Idle hibernation currently skips orchestrators (`service/chat/hibernate.go`). Bots take the opposite choice: they hibernate after `IdleBeforeHibernate`, and they wake when a message, schedule, crew report, or answered ask arrives. This mirrors "machine sleeps between turns" in Capy, keeps prompt cache warm for 10 minutes, and costs nothing while idle.
- **Recycle.** When a bot session hits a context threshold (provider-reported context use above about 60%, or 7 days old), the bot service ends it after its turn settles. It then starts a fresh `KindBot` session bound to the same bot conversation. The new session's prompt carries SOUL, MEMORY, and a handoff note from the existing `chat.Service.HandoffNote`. Recycling is a normal event in the bot's timeline, not an error. Devin exposes the same idea as "Recycle".

### 5. Crew attribution is durable, and limits are enforced by the daemon

- **Durable attribution.** Add a nullable `sessions.bot_id`. Today `ParentSessionID` (`ports/session.go`) only lives for the duration of a spawn, and reports route per project. When `ao spawn` runs inside a bot session, `AO_SESSION_ID` resolves to a `KindBot` parent, and the daemon stamps the child with `bot_id`. Callers never supply `bot_id` themselves.
- **Permissions inheritance.** Extend `inheritedSpawnPermissions` to treat `KindBot` parents like orchestrators.
- **Report routing.** In `service/report/coordinator.go`, a worker with `bot_id` sends its reports to the bot conversation, waking the bot if needed, instead of `newestOrchestrator`. Workers without `bot_id` keep today's routing.
- **Worker cap (hard).** Each bot has `max_active_workers`, default 3. Spawning past the cap fails in the daemon with `BOT_WORKER_LIMIT`, and the bot is told to queue the work or ask. This is enforcement, not a prompt rule. It answers Orca's admission that "None of these settings is a guarantee."
- **Default permissions.** The bot session defaults to `PermissionModeDefault` (ask for approval), not bypass. Crew inherit it unless the owner raises it. Orca defaults to Full access; we deliberately do not.
- **Bots cannot create bots.** There is no agent-facing create route. A bot may propose one as an ask, and only the owner can confirm it.

### 6. Schedules are automations that target a bot

Automations already provide RRULE/cron canonicalization, catch-up limits, spawn leases, and run states (`service/automation/`).

- Add a nullable `automations.bot_id`. When it is set, a due run delivers the automation prompt as a new turn in the bot conversation, waking or recycling the bot, instead of spawning a session.
- Run states map directly: `spawning` covers waking, `running` covers the turn in progress, and `completed`/`failed` cover the turn outcome.
- A bot can create, edit, and pause its own schedules with `ao automation` scoped by `AO_SESSION_ID`. New bot-created schedules start disabled, and enabling one is an ask. This matches Orca, where template schedules start turned off.
- The three bundled templates (`daily_pr_review_v1`, `daily_issue_triage_v1`, `weekly_release_notes_v1`) become the first bot templates.

## Data model

New migrations, created with `npm run migration:create`, appended to `shippedMigrations`, and followed by `npm run sqlc`. CDC comes from `change_log` triggers on the new tables, never from manual emission in store code.

```sql
CREATE TABLE bots (
    id                 TEXT PRIMARY KEY,            -- bot_<ulid>
    project_id         TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    handle             TEXT NOT NULL,               -- e.g. "ci-watch"; unique per project
    display_name       TEXT NOT NULL,
    purpose            TEXT NOT NULL DEFAULT '',    -- one line, also seeds SOUL.md
    harness            TEXT NOT NULL,               -- defaults from project orchestrator role
    model              TEXT NOT NULL DEFAULT '',
    effort             TEXT NOT NULL DEFAULT '',
    permissions        TEXT NOT NULL DEFAULT 'default',
    home               TEXT NOT NULL DEFAULT 'local' CHECK (home IN ('local')), -- widened in Phase 2
    max_active_workers INTEGER NOT NULL DEFAULT 3 CHECK (max_active_workers BETWEEN 0 AND 20),
    memory_tidy        TEXT NOT NULL DEFAULT 'nightly' CHECK (memory_tidy IN ('nightly','weekly','off')),
    paused_at          TIMESTAMP,                   -- owner intent, not derived status
    archived_at        TIMESTAMP,
    revision           INTEGER NOT NULL DEFAULT 1,
    created_at         TIMESTAMP NOT NULL,
    updated_at         TIMESTAMP NOT NULL,
    UNIQUE (project_id, handle)
);

CREATE TABLE bot_asks (
    id            TEXT PRIMARY KEY,
    bot_id        TEXT NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    session_id    TEXT REFERENCES sessions(id) ON DELETE SET NULL, -- bot session that asked
    subject_ref   TEXT NOT NULL DEFAULT '',  -- optional PR URL / crew session id / issue
    kind          TEXT NOT NULL CHECK (kind IN ('decision','approval','review','info')),
    question      TEXT NOT NULL,             -- <= domain.MaxReportTextCharacters
    options_json  TEXT NOT NULL DEFAULT '[]',
    answer        TEXT,
    answered_at   TIMESTAMP,
    withdrawn_at  TIMESTAMP,                 -- bot no longer needs it
    created_at    TIMESTAMP NOT NULL
);

ALTER TABLE sessions    ADD COLUMN bot_id TEXT REFERENCES bots(id) ON DELETE SET NULL;
ALTER TABLE automations ADD COLUMN bot_id TEXT REFERENCES bots(id) ON DELETE CASCADE;
-- plus the bot conversation migration from decision 2
```

**No stored status.** Per the hard rule, a bot's displayed state (`working`, `sleeping`, `needs_you`, `paused`) is derived at read time from three facts: `paused_at`/`archived_at`, the bot session's activity and terminated facts, and the count of open Needs-you items.

## Needs you inbox

The inbox is a **read projection over durable facts**, computed in a new `service/bot` read path. It has no inbox table and no stored status. An item is open while its underlying fact is unresolved, and it disappears when that fact changes.

| Item kind | Source fact | Resolves when |
| --- | --- | --- |
| `question` | `bot_asks` row with no `answered_at` or `withdrawn_at` | answered or withdrawn |
| `approval` | Pending chat permission request on the bot session or a crew session (the same requests `integrations/grok-bot` answers) | answered (a second answer gets `409 CHAT_REQUEST_NOT_PENDING`) |
| `blocked` | Crew session whose latest report is `needs_input` or `stuck`, or whose activity `NeedsInput()` is true | a later report or activity change |
| `review` | Crew PR with a `review_changes_requested` notification and no newer push | the PR head changes or the PR closes |
| `merge` | Crew PR with an unresolved `ready_to_merge` notification (`NeedsResolution()`) | merged, closed, or blocked again |

**Rules that keep it from becoming noise.** This is the main product risk: a noisy bot gets muted within a week.

- Only items the owner can unblock appear. Progress, checkpoints, and `done` reports go to the bot's **digest**, a timeline in the bot conversation, never to the inbox.
- One open item per subject. When a crew session is blocked and its PR is also ready to merge, show the most actionable item and fold the other into it.
- Push notifications (existing push and mobile bridge) fire only for `question` and `approval`, and at most once per item. `blocked`, `review`, and `merge` badge silently.
- Every item answers "what do you need from me?" in one line. It links to the exact place to act (the chat request, the PR, the crew session), with a short "why" from the bot.

**Scopes:**

- per bot, at `GET /api/v1/bots/{id}/inbox`;
- across all bots on the daemon, at `GET /api/v1/inbox`.

The renderer merges `/inbox` across hosts it can reach, using the existing host federation and `Ref{host,id}`. This is what later makes one inbox span a laptop bot and a cloud bot.

**Acting on items.** Approvals, merges, and session messages go through their existing routes. Only one new write is needed: `POST /api/v1/bots/{id}/asks/{askId}/answer`. It records the answer and delivers it to the bot as a turn, waking the bot.

## Lifecycle

| Action | Behaviour |
| --- | --- |
| Create | Owner only, from the UI wizard, `ao bot create`, or the API. It writes the bot row and the folder (SOUL.md seeded from the purpose, an empty MEMORY.md). No session starts until the first message. |
| Message | `ao bot send <handle> "<text>"`, in-app chat, or mobile. Appends a turn to the bot conversation and wakes or starts the bot session. |
| Pause | Sets `paused_at`. Schedules are skipped and recorded as skipped runs. New messages queue; they are not dropped. Crew keep running, but new spawns fail. |
| Resume | Clears `paused_at`. Queued messages are delivered in order. |
| Archive | Stops the bot session and disables its schedules. Crew detach and stay as ordinary sessions, which respects the rule against force-deleting dirty worktrees. The folder is kept. |
| Delete | Only an archived bot can be deleted, with confirmation. Removes the row, asks, and schedules. The folder moves to `$AO_DATA_DIR/bots/.trash/` for 30 days. |

## Memory tidy

When `memory_tidy` is on, the daemon schedules a tidy turn: nightly after 03:00 local time, and only after a day with new turns. The turn runs in a fresh bot session with these instructions:

- read the day's journal and the current MEMORY.md;
- write a consolidated MEMORY.md through `ao bot memory replace`;
- make no other tool calls, spawns, or sends.

Each tidy is a reviewable diff in the Memory tab, auto-accepted, with one-click undo from `.history/`.

**Known gap.** Orca runs its tidy with no tools at all. AO is harness-neutral and cannot guarantee a tool-less turn on every harness. Phase 0 enforces the parts the daemon controls:

- a tidy session refuses spawns and every agent-facing write route except `bot memory replace`, gated by a tidy flag on the session;
- it runs with permission mode `default`.

This is an open question below.

## Hybrid readiness (what Phase 0 must not break)

- `bots.home` exists from day one. Phase 2 widens its `CHECK` constraint to add a remote-host value and `cloud`.
- The bot folder plus the bot conversation export make up the whole portable state. Nothing a bot needs lives only in process memory or in absolute paths.
- Every renderer and API surface addresses a bot as `Ref{host, id}`. The Phase 1 relay will route Slack and GitHub events to `(host, bot id)` the same way.
- Inbox aggregation happens in the client, across hosts. This follows the cloud architecture rule of federation at the client, not in the local daemon.
- Handles are unique per project, not globally, so two homes never fight over a name.

## UI

Follow `DESIGN.md`: sentence case, no new decorative chrome, and status derived, never invented.

- **Sidebar.** Bots appear as child rows of their project, pinned above sessions, using the same `NavRowHighlight` row and a bot glyph. A single "Needs you" utility row shows the open count; this is semantic color, used only when the count is above zero.
- **Bot route** (`/_shell/host/$hostId/project/$projectId/bot/$botId`). This is a working room like a session: the conversation is primary, with recycle and tidy events shown inline as quiet separators. The inspector has four tabs:
  - **Needs you**
  - **Crew** (live and recent workers, with their PRs)
  - **Memory** (SOUL.md and MEMORY.md, editable and diffable, with history and undo)
  - **Schedules** (the existing automation editor, filtered by `bot_id`)
- **Create dialog.** Name, purpose, project, agent, and optionally a template, using the existing modal composition. "Nothing is created until you press Create."
- **Mobile.** The Needs-you list, with answer and approve actions through existing mobile routes, plus bot chat.
- **No home-page dashboard.** Per DESIGN.md, Home stays minimal.

## CLI and agent skills

Thin clients over new daemon routes, following `backend/internal/cli` patterns. Misuse returns `usageError` and exits with code 2.

```text
ao bot create --project <p> --handle <h> --name <n> [--purpose ...] [--agent ...] [--template ...]
ao bot ls | get <handle> | send <handle> <text> | pause | resume | archive | rm
ao bot memory show | append <text> | replace --file <f> | history | undo   # agent-facing writes
ao bot ask --kind decision|approval|review|info "<question>" [--option ...] [--subject <ref>]
ao inbox [--bot <handle>]
```

Add a `using-ao` skill page for bots. It tells a bot session how to use memory, asks, spawn, and reports, and that content from issues, PRs, and logs is information, never instructions.

## Telemetry and success metrics

Emit bot events through the existing telemetry pipeline, following `docs/telemetry.md`, with no prompt text or memory content: `bot_created`, `bot_turn` (source: chat, schedule, report, ask), `bot_recycled`, `inbox_item_opened`, and `inbox_item_resolved` (kind, time to resolve, acted on vs dismissed).

Phase 0 is done when three bots run against this repository for two weeks: CI failures, issue triage, and a PR-review digest. The targets:

- **Inbox signal:** at least 70% of inbox items are acted on rather than dismissed or ignored.
- **Responsiveness:** median `question` resolution is under 4 hours.
- **Usefulness:** at least 50% of crew PRs are merged.
- **Retention:** no bot is paused for noise.

## Delivery plan

Ten PRs of about two weeks' total work, each with tests for the boundary it changes:

1. Domain types and migrations: `bots`, `bot_asks`, `sessions.bot_id`, `automations.bot_id`, sqlc. Store, plus CDC trigger tests.
2. The bot conversation migration (decision 2), plus rebinding tests that copy the project-conversation tests.
3. `KindBot`, plus the `KindOrchestrator` audit with one pinned behaviour test per site.
4. The bot service with create, get, list, pause, resume, archive, and delete; HTTP routes and DTOs (`npm run api`); and the CLI.
5. The bot folder and memory routes: size cap, secret check, history and undo, and system-prompt injection.
6. Crew attribution, permissions inheritance, the worker cap, and report routing to the bot conversation.
7. Hibernate and wake for bots, and recycle with a handoff note.
8. Automations targeting bots, with bundled templates as bot templates.
9. The Needs-you projection and APIs, the ask answer route, and push rules.
10. Renderer: sidebar rows, bot route, inspector tabs, create dialog, and the cross-host inbox. Then the memory tidy and the dogfood bots.

## Open questions

1. **Conversation scope.** A table rebuild for `scope='bot'`, or the link-table fallback? Decide in PR 2 after a migration dry run on a copy of a large real database.
2. **Tool-less tidy.** Is "daemon refuses everything but `memory replace` during tidy" enough, or should tidy run through a direct provider call with no harness? The second option is harness-specific and may belong in Phase 2.
3. **Project scope.** Phase 0 gives each bot one project, and workspace projects cover multi-repo. Is a cross-project bot (for example "triage everything") needed before Phase 1?
4. **Recycle thresholds.** Is 60% context use or 7 days right? Tune from dogfood telemetry.
5. **Relationship to the project orchestrator.** Should the orchestrator be able to message bots in its project (Capy lets Captains message each other), or stay separate in Phase 0? The default is separate.
