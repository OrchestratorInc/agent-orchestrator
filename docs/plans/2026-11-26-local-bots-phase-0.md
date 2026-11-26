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
  - rejects content matching known credential formats (best-effort; see below);
  - snapshots the previous version to `.history/`;
  - emits a change event.
- **Secrets: reduce the risk; no guarantee.** AO cannot guarantee that a secret never reaches bot memory. The credential-format check only catches text in recognizable formats, such as provider token prefixes and private-key blocks. A password written as ordinary prose passes it. Phase 0 layers these controls on top:
  - Memory content is always owner-visible. Every write shows up as a diff in the Memory tab.
  - `ao bot memory purge` is for when a secret slips through. Any session that still holds the text could write it back, so purge is fenced and runs in this order:
    1. **Fence.** The daemon puts the bot into a purge state. Every memory write (`append`, `replace`, `undo`) is refused with `BOT_PURGE_IN_PROGRESS`. No new tidy can start, and incoming messages queue rather than start a session.
    2. **Stop every session that may hold the text.**
       - Any active tidy is aborted. Its session is ended, its pending `replace` is discarded, and its watermark does not advance.
       - The main bot session, live or hibernated, is ended after its in-progress turn settles.
       - Ending a session runs the existing `cleanupSystemPromptDir`. That deletes the whole `$AO_DATA_DIR/prompts/<session-id>/` directory, where `writeSystemPromptFile` put the prompt (SOUL and MEMORY) and where some harnesses keep their own files.
    3. **Scrub.** The value is removed from:
       - `MEMORY.md`, every `.history/` snapshot, and the `journal/` files;
       - the question, option, and answer text of the bot's asks, since open asks are injected into the prompt;
       - any remaining prompt directory of the bot's earlier sessions, which normally were removed when those sessions terminated.
    4. **Invalidate stale writers.** Purge bumps the `MEMORY.md` revision. A `replace` prepared against any pre-purge revision is rejected even after the fence lifts, including a tidy's conflict retry.
    5. **Restart clean.** The fence lifts, and queued messages are delivered to a fresh session. That session starts from the purged SOUL, MEMORY, and asks with **no handoff note**, because the note is derived from pre-purge conversation turns that may contain the value. The conversation shows a purge marker, and the owner is told the new session has no summary of earlier turns.
    6. **Verify and report.** Purge succeeds only after re-reading every location in step 3 and finding no match. It reports any copy it could not remove. If any step fails, the fence stays up, and the bot remains stopped until the owner retries or cancels.
  - Purge cannot reach copies outside the bot folder and prompt directories:
    - text in the bot conversation itself (for example, a secret the owner typed in chat);
    - a harness's own transcript store;
    - what the model provider already received.

    The purge result lists these limits.
  - The bot skill and system prompt tell the bot not to store secrets, and to refer to credentials by name only. This is a model instruction, not enforcement.
  - AO never syncs memory, uploads it to AO services, or puts it in telemetry. **It does leave the home through the model provider:**
    - Every bot session sends its system prompt, including `SOUL.md` and `MEMORY.md`, to the bot's model provider when the model is remote (see Injection).
    - A tidy turn also sends the journal days it reads.
    - A secret that slips into memory is sent to the provider at every session start until it is purged. Purging does not recall copies a provider already received; their retention follows that provider's terms.
  - Some harnesses take the system prompt as an inline command-line argument (for example `cline -s`). Memory can then appear in the local process list for the lifetime of the session.
  - Moving or exporting a bot in a later phase must re-run the check and show the owner the memory before it leaves the machine.
- **SOUL.md is owner-only.** It is edited in the UI, the API, or a text editor. The bot can propose a SOUL change only as a bot ask.
- **Injection.** At every bot-session start (first start, wake, recycle), the system prompt gets SOUL.md, MEMORY.md, the bot's open asks, and a crew summary. It goes through the existing `writeSystemPromptFile` path in `session_manager/manager.go`.
- **Provider policy (Phase 0).** The bot's memory goes to the model provider the owner chose for the bot. This is inherent: a bot can only use memory its model can read. It is the same trust boundary as the repository content any AO session already sends. Phase 0 adds no separate control over it:
  - The owner's control is the choice of harness and model at create time. A locally hosted model keeps memory on the machine.
  - The create dialog and the Memory tab state in one line that memory is sent to the bot's model provider.
  - A per-bot "do not send memory" switch, or redacting memory before injection, is not planned. Open question 6 tracks whether one is needed.
- **Portability rule.** Memory must not contain absolute paths or daemon-local session IDs as load-bearing references. The tidy prompt says so, and the folder must be able to move to another home unchanged.

### 4. Bot sessions hibernate between turns and are recycled

- **Hibernate.** Idle hibernation currently skips orchestrators (`service/chat/hibernate.go`). Bots take the opposite choice: they hibernate after `IdleBeforeHibernate`, and they wake when a message, schedule, crew report, or answered ask arrives. This mirrors "machine sleeps between turns" in Capy, keeps prompt cache warm for 10 minutes, and costs nothing while idle.
- **Prompt directory across hibernate and wake.** Hibernation does not terminate the session, so `prompts/<session-id>/` stays on disk while the bot sleeps. On wake, the bot service regenerates `system.md` from the current SOUL, MEMORY, open asks, and crew summary before relaunching. A woken bot therefore never runs on a stale or purged prompt. The directory is deleted when the session ends: on recycle, archive, or purge, through `cleanupSystemPromptDir`.
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

The daemon schedules a tidy turn from `memory_tidy`. A tidy runs only when the bot had new turns since its last tidy:

| Setting | Schedule |
| --- | --- |
| `nightly` | Due each night after 03:00 local time, if there were new turns since the last tidy. |
| `weekly` | Due once a week, on Monday after 03:00 local time, if there were new turns since the last tidy. |
| `off` | Never. The owner can still run a tidy from the Memory tab. |

**Catch-up.** The rule checks journal entries after the last tidy's watermark (below), not just the previous day. So turns from days when the daemon was offline, or turns that arrived during a tidy, are never skipped.

- When the daemon resumes past a due time it missed, it runs one catch-up tidy covering every journal entry since the last tidy. It runs one tidy, not one per missed night. This follows the automation scheduler's catch-up limit.
- **The cutoff is an input watermark, not the tidy's own timestamp.**
  - Every journal entry gets a per-bot sequence number that only increases.
  - A tidy fixes its input range when it starts: every entry after the previous watermark, up to the newest entry at that moment.
  - The tidy event records the newest entry it read as the new watermark.
  - Eligibility and the next tidy's input both start after that watermark. A turn journaled while a tidy is running therefore lands after the watermark and is picked up by the next tidy.
- **Tidy does not block normal turns, and it does not overwrite concurrent memory writes.**
  - Normal turns keep going to the main bot session while a tidy runs.
  - The tidy's `ao bot memory replace` carries the `MEMORY.md` revision it read.
  - If the main session changed `MEMORY.md` in the meantime, the daemon rejects the replace. The tidy then retries once with a fresh read. If that also conflicts, the tidy is abandoned, the watermark does not advance, and the tidy runs at the next due time.
  - Memory written by the bot is never lost to a tidy.

The turn runs in a fresh bot session with these instructions:

- read the journal entries in its fixed input range and the current MEMORY.md;
- write a consolidated MEMORY.md through `ao bot memory replace`;
- make no other tool calls, spawns, or sends.

Each tidy is a reviewable diff in the Memory tab, auto-accepted, with one-click undo from `.history/`.

**Known gap.** Orca runs its tidy with no tools at all. AO is harness-neutral and cannot guarantee a tool-less turn on every harness. Phase 0 enforces the parts the daemon controls:

- a tidy session refuses spawns and every agent-facing write route except `bot memory replace`, gated by a tidy flag on the session;
- it runs with permission mode `default`.

This is an open question below.

## Hybrid readiness (what Phase 0 must not break)

- `bots.home` exists from day one. Phase 2 widens its `CHECK` constraint to add a remote-host value and `cloud`.
- **The portable state is the bot folder plus a bot export bundle.** Nothing a bot needs lives only in process memory or in absolute paths. The bundle holds everything the bot keeps in the database:

  | Bundle part | Contents | On import |
  | --- | --- | --- |
  | `bot.json` | The bot row: handle, display name, purpose, harness, model, effort, permissions, `max_active_workers`, `memory_tidy`, `paused_at` | The bot ID reserved by the target in move step 0, or a new ID for a plain copy. The project is matched by repository identity, not the source project ID. `home` is set to the target. The agent config is validated against the target's harnesses; an unknown model falls back to the target default and is reported to the owner. |
  | `asks.json` | Open bot asks (no `answered_at` or `withdrawn_at`), with question, kind, options, subject, and created time | Recreated as open asks, so the moved bot's system prompt and Needs-you inbox have them from its first session. Answered asks are history and live in the conversation. |
  | `schedules.json` | Every automation with this `bot_id`: RRULE, timezone, prompt, template, enabled flag | Recreated with the same RRULE and timezone, but **disabled**. The owner re-enables them in one confirmation. |
  | `conversation.jsonl` | The bot conversation's turns and messages | Imported as the new bot conversation. |
  | `queue.jsonl` | Messages accepted but not yet delivered, for example while the bot was paused | Delivered to the target bot in their original order once it starts. Each one shows in the conversation as carried over from the source. |

- **Export is a copy; a move is export plus a source-side stop.** Exporting alone, for example as a backup, does not change the source bot. Its schedules keep running. Importing that copy and enabling its schedules would run the same RRULE on both homes. So the import screen warns that the source may still be live, and AO makes no single-home guarantee for a plain copy.
- **The move flow (Phase 2) guarantees one live home.** It runs in this order:
  0. **Reserve the target ID.** The target home allocates the new bot ID and holds it as a pending reservation before anything changes on the source. If the target cannot reserve one, the move does not start.
  1. Pause the source bot and disable all its schedules.
  2. Record `moved_to` on the source row: the target home and the reserved bot ID. From this point every `BOT_MOVED` refusal names a concrete target `Ref{host, id}`.
  3. Export. The bundle's `queue.jsonl` carries every message the source accepted but had not delivered, including ones queued during the pause.
  4. Import on the target. The import must claim the reservation from step 0 and create the bot under exactly that ID, with schedules disabled. An import that does not present a valid reservation is refused.
  5. Only after the source confirms step 1 can the owner enable schedules on the target.

  If the source home is unreachable, the move does not proceed. The owner must either archive the source bot or explicitly accept a copy.

  **If the move aborts after step 2** (export or import fails, or the owner cancels):
  - The source clears `moved_to` and stays paused until the owner resumes it.
  - The target keeps the reservation as a tombstone. A message resent to the reserved ID gets `BOT_MOVE_ABORTED`, which names the source `Ref{host, id}`, so the sender is pointed back rather than told the bot does not exist.

- **Messages that arrive during or after a move are refused, not queued.**
  - From step 2 on, the source accepts no new messages or asks for the bot. It refuses them with `BOT_MOVED`, and the error names the target `Ref{host, id}`.
  - Nothing is queued on the source after step 2, so the queue exported in step 3 is complete and no message is stranded on a home the bot has left.
  - The sender is told directly. The CLI and renderer show "moved to *target*; resend there" and offer one-click resend. The Phase 1 relay redirects to the target automatically.
  - Crew detach at step 2, as on Archive. Their `bot_id` is cleared, so their later reports follow normal project routing on the source instead of being refused or lost.
  - Resuming the source after a completed move is refused. Undoing a move means moving the bot back.
- **Not in the bundle:**
  - Crew sessions and their worktrees stay on the source home, detached as ordinary sessions, like on Archive.
  - Credentials and sign-ins are never exported. The target home uses its own agent sign-ins.
  - Session IDs, project IDs, and absolute paths are never carried over.
- **Phase 0 builds the format, not the move.** It implements bundle export and import behind the daemon API, plus a round-trip test: export, import into a fresh data dir, then assert that the bot row, open asks, disabled schedules, memory files, conversation, and undelivered queue (in order) match. The move UX and non-local homes are Phase 2.
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
ao bot ls [--project <p>]
ao bot get|send|pause|resume|archive|rm <bot> [--project <p>] [<text>]
ao bot memory show | append <text> | replace --file <f> | history | undo   # agent-facing writes
ao bot memory purge <bot> <text> [--project <p>]                           # owner only; fenced: stops sessions and tidies, scrubs files, asks and prompt copies
ao bot ask --kind decision|approval|review|info "<question>" [--option ...] [--subject <ref>]
ao inbox [--bot <bot>] [--project <p>]
```

**Addressing a bot.** Handles are unique per project only, so two projects can each have a `ci-watch`. A `<bot>` argument resolves in this order:

1. A qualified `<project>/<handle>`, such as `agent-orchestrator/ci-watch`, or a bot ID (`bot_...`). Either is always unambiguous.
2. A bare handle plus `--project <p>`.
3. A bare handle with no flag inside an AO session: the project comes from `AO_SESSION_ID`, the same way `ao spawn` resolves it.
4. A bare handle with no flag outside a session: it resolves only when exactly one project has that handle. Otherwise it fails with a `usageError` (exit 2) that lists the matching `<project>/<handle>` forms.

Agent-facing commands (`ao bot memory show|append|replace|history|undo` and `ao bot ask`) take no `<bot>` argument. They act on the calling bot, found from `AO_SESSION_ID`, and are refused outside a bot session. The HTTP API always addresses bots by ID.

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
5. The bot folder and memory routes: size cap, best-effort credential check, history and undo, purge, and system-prompt injection.
6. Crew attribution, permissions inheritance, the worker cap, and report routing to the bot conversation.
7. Hibernate and wake for bots (regenerating `system.md` on wake), recycle with a handoff note, and the fenced purge. Tests cover the prompt directory after hibernate, wake, recycle, and purge, plus a purge racing an active tidy and a stale `replace`; neither may restore the purged value.
8. Automations targeting bots, with bundled templates as bot templates.
9. The Needs-you projection and APIs, the ask answer route, and push rules.
10. Renderer: sidebar rows, bot route, inspector tabs, create dialog, and the cross-host inbox. Then the memory tidy, the export bundle with its round-trip test, and the dogfood bots.

## Open questions

1. **Conversation scope.** A table rebuild for `scope='bot'`, or the link-table fallback? Decide in PR 2 after a migration dry run on a copy of a large real database.
2. **Tool-less tidy.** Is "daemon refuses everything but `memory replace` during tidy" enough, or should tidy run through a direct provider call with no harness? The second option is harness-specific and may belong in Phase 2.
3. **Project scope.** Phase 0 gives each bot one project, and workspace projects cover multi-repo. Is a cross-project bot (for example "triage everything") needed before Phase 1?
4. **Recycle thresholds.** Is 60% context use or 7 days right? Tune from dogfood telemetry.
5. **Relationship to the project orchestrator.** Should the orchestrator be able to message bots in its project (Capy lets Captains message each other), or stay separate in Phase 0? The default is separate.
6. **Provider exposure of memory.** Is the owner's harness and model choice enough, or do bots need a per-bot switch or pre-injection redaction? A related question: should AO prefer file-based system-prompt injection over inline command-line arguments for bot sessions, so memory never appears in the process list?
