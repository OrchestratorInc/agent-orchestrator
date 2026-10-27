# Session search: analysis

Status: analysis and plan for the session-search feature, revised twice for
approved scope changes (`session-search-addendum.md`, 21 Oct 2026, and the
Cmd+K confirmation of 22 Oct 2026), and a third time (23 Oct 2026) for ranking:
search is now BM25 end to end, measured against public retrieval benchmarks
(§7). Where §2.6 and §7 disagree with older text below, §7 is the measured one.

The original brief said not to build Cmd+K. Both scope changes reverse that, and
the reversal is final: **session search must work from Cmd+K** as well as from
the Search page, and the index must cover every word of every session across all
harnesses. This document describes the resulting design, which is what is
implemented on `ao/session-search`; where the two surfaces differ it says so
explicitly (§2.7 and §2.7.1).

Hard constraints that still hold:

- **The index is read-only toward session data.** It never writes, moves, or
  rewrites transcripts, harness files, or the session tables. Native
  transcripts are opened read-only and read forward.
- **8 GB Air.** Indexing must stream, batch, and yield. No per-session
  goroutines, no whole-file reads, no unbounded caches.
- **Do not touch** the in-progress compaction (`ao/compaction-phase1`),
  hibernation, or session-migration (`ao/session-migration`) work.
- Commit locally on `ao/session-search`. No push, no PR.

---

## 1. Where session content lives today

There are three distinct stores of "what was said". AO's own SQLite database is
authoritative for Chat sessions; native harness transcripts are the only record
for TUI sessions.

### 1.1 AO chat storage (SQLite, `~/.ao/data/ao.db`)

| Table | What it holds | Migration |
| --- | --- | --- |
| `sessions` | one row per AO session: `id`, `project_id`, `kind`, `harness`, `display_name`, `prompt`, `latest_user_prompt`, `activity_state`, `is_terminated`, `hibernated_at`, `native_transcript_path`, `session_mode`, `branch`, `workspace_path`, timestamps | `0001_init.sql`, later `ALTER`s (hibernation: `0190_session_hibernation.sql`) |
| `conversations` | one Chat conversation per session (`scope='session'`) or per project orchestrator (`scope='project'`); `provider_title`, `applied_title`, `latest_sequence`, `active_branch_id` | `0066_chat_session_mode.sql`, `0072_conversation_history_ops.sql`, `0087_conversation_branches.sql` |
| `conversation_messages` | durable user/assistant prose: `id`, `conversation_id`, `turn_id`, `sequence`, `revision`, `role`, `origin`, `text`, `streaming`, `created_at` | `0066_chat_session_mode.sql` |
| `conversation_activities` | everything that is not prose: `kind` ∈ `command`, `file_change`, `plan`, `reasoning`, `approval`, `usage`, `error`, `system`, `mcp_tool`, `auto_review`, `user_input`; `summary`, `detail_json`, **`command_output`** (0070), **`streamed_text`** (0073) | `0066`, `0070_command_output_and_diffs.sql`, `0073_conversation_provider_state.sql`, `0074_activity_kinds_mcp_and_auto_review.sql` |
| `conversation_branches` | active branch head; edits/rollbacks fork rather than delete | `0087_conversation_branches.sql` |
| `agent_native_sessions` | retained provider conversations: `harness`, `config_dir`, `native_session_id`, `transcript_path`, `last_used_at` | `0085_agent_switching.sql` |
| `pr` | `url`, `session_id`, `number`, `pr_state` — the join that makes a pasted PR URL resolve to its session | `0001_init.sql` |
| `projects` | `id`, `path`, `display_name` — project filter and result labels | `0001_init.sql` |

Measured on the author's real database (120 sessions): 2,365 messages and
**15,914 activities**, of which 7,638 are `command` rows carrying **17.7 MB** of
`command_output` (max 65,536 bytes per row, capped by AO). Tool output is the
largest single body of searchable text, which is exactly what the addendum
requires and what every single-harness competitor skips.

Branch semantics matter: `SelectConversationMessages`
(`backend/internal/storage/sqlite/queries/conversations.sql:1242`) walks the
active branch path and excludes rolled-back, promoted, and cancelled turns.
Indexing `conversation_messages` raw would surface text the session no longer
shows, and clicking such a result could not scroll anywhere.

### 1.2 Native CLI transcripts

| Harness | Location | Format | Reader in AO today |
| --- | --- | --- | --- |
| Claude Code | `$CLAUDE_CONFIG_DIR` or `~/.claude/projects/<project-key>/<native-id>.jsonl` | JSONL: `type` ∈ `user`/`assistant`/`attachment`/`system`/`file-history-snapshot`/`mode`/…; `message.content` is a string or a part array (`text`, `tool_use`, `tool_result`, `thinking`); `isSidechain`, `isMeta`, `timestamp` | usage collector (`ClaudeProjects`), `adapters/agent/claudecode/turn_interrupts.go` |
| Codex | `$CODEX_HOME` or `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`, plus `archived_sessions/` | JSONL: `session_meta`, `event_msg` (`task_started`, `item_completed` with `UserMessage`/`AgentMessage`/`CommandExecution`), `response_item` (`message` with `role` and `content[]`), `token_usage_record` | usage collector (`CodexSessions`, `CodexArchived`) |
| OpenCode v1 | `$XDG_DATA_HOME/opencode/opencode.db` | SQLite: `session(title, directory, …)`, `message(session_id, data)` on older builds, `session_message(session_id, type, seq, data)` on newer ones | `adapters/agent/opencode/` (resume/history) |
| OpenCode v2 | `$XDG_DATA_HOME/opencode-v2-home/opencode/opencode.db` | SQLite: `session`, `session_message(type ∈ user/assistant/idle, data JSON with text)`, `part` | `adapters/agent/opencodev2/` |
| Kimi | `<dataDir>/kimi/.../agents/*/wire.jsonl`, `session_index.jsonl` | JSONL wire records | usage collector (`KimiHome`) |
| Everything else (aider, amp, cline, cursor, copilot, gemini, grok, qwen, …) | provider-specific local stores; AO treats most as opaque | — | AO reads hook-reported metadata only |

Measured: 29 Claude transcripts (20 MB), 227 Codex rollouts (329 MB). The
largest single transcript is 5.6 MB. A 329 MB corpus is well within reach for a
byte-offset incremental reader, but not for anything that loads files whole.

### 1.3 Hibernated, terminated, and killed sessions

- **Hibernated** (`sessions.hibernated_at`): the provider host was stopped, but
  conversation rows and the native transcript remain. Fully searchable; opening
  the result must wake the session through the existing hibernation wake path.
- **Terminated / killed** (`is_terminated = 1`): rows and transcripts survive;
  the reaper never deletes `sessions` rows. These are the most valuable search
  targets and today have no entry point other than the archive list.
- **Workspace cleanup** deletes worktrees but not sessions, so
  `sessions.workspace_path` may no longer exist. Indexing must not depend on it.

### 1.4 What is already indexed or searchable

Nothing for prose. No FTS table existed before this change; `ao` has no search
command; the desktop CommandPalette searches commands, session titles, and
workspace file paths — never message or tool text.

SQLite FTS5 is available in the pure-Go driver AO ships (`modernc.org/sqlite`
v1.51.0 → SQLite 3.53.3). Verified in-repo: `bm25()`, `snippet()`,
external-content tables, `unicode61 remove_diacritics 2`, and `json_each`.

### 1.5 Existing APIs, routes, and UI entry points

- REST: `backend/internal/httpd/api.go` mounts one controller per resource under
  `/api/v1`. The route/spec parity test
  (`backend/internal/httpd/apispec/parity_test.go`) forces every mounted route
  to have an OpenAPI operation and vice versa, so a new route needs a `specgen`
  entry in the same change.
- Conversation reads: `GET /api/v1/sessions/{sessionId}/conversation`
  (`controllers/conversations.go`), paginated via `beforeSequence`.
- CLI: `backend/internal/cli/client.go` (`getJSON`/`postJSON`) is the thin
  daemon client; one file per noun.
- Desktop navigation: `frontend/src/renderer/lib/navigate-to-session.ts`
  resolves session + project into the right route.
- Sidebar top nav: `components/Sidebar.tsx` renders `SidebarTopNavRow`s for
  Automations and Artifacts — the established place for a top-level surface.
- **Cmd+K**: `components/CommandPalette.tsx` (889 lines) is an existing palette
  with `PaletteView` modes (`root`, `session-actions`, `new-task`,
  `file-session-picker`, `file-search`), `CommandInput`/`CommandList`, and a
  `command-palette` keybinding. It is gated by
  `useCommandPaletteEnabled()`. This is where Cmd+K session search is wired —
  no new palette.

### 1.6 Opening a session scrolled to a message

The Chat timeline already supports this, for annotation jumps:

- `ChatWorkspace` accepts `annotationNavigationRequest: { id, text, messageId,
  revision }` (`components/chat/ChatWorkspace.tsx:342`).
- On request it finds the timeline group containing `messageId`, expands the
  activity disclosure that may hide it, calls `virtualizer.scrollToIndex(index,
  { align: "center" })` when virtualized, retries up to 20× at 80 ms for the row
  to mount, then highlights with the CSS Custom Highlight API
  (`lib/chat-annotation-navigation.ts`).
- It calls `onLoadOlder()` when the message is not yet loaded, which is what
  makes deep links into long conversations work.
- Message rows carry `data-chat-message-id` (`ChatTimelineItems.tsx:632,904`).

So "open at message" needs no new scroll machinery: it needs a channel carrying
`sessionId` + `messageId` + matched text into `SessionChatSurface`. TUI sessions
have no message timeline, so for them the correct behavior is to open the
session and show the snippet, not to fake a scroll.

**Match navigation inside the session.** The addendum also requires
Enter/Shift+Enter to move between matches. `ChatWorkspace`'s annotation
navigation handles one target at a time, so this feature carries an ordered list
of matches for the open session and re-issues `annotationNavigationRequest` per
step — reusing the scroll/highlight path rather than duplicating it.

---

## 2. Plan

### 2.1 Index store

A dedicated FTS5 index in the same SQLite database (new migration), owned by a
new `sessionsearch` package:

- `session_search_docs` — one row per indexable unit: `id`, `source_key`,
  `doc_key`, `session_id`, `project_id`, `harness`, `kind`, `message_id`,
  `seq`, `title`, `body`, `paths`, `created_at`, `indexed_at`.
- `session_search_fts` — `USING fts5(title, body, paths,
  content='session_search_docs', content_rowid='id',
  tokenize='unicode61 remove_diacritics 2')`, with insert/delete/update
  triggers so the mirror cannot drift.
- `session_search_sources` — the incremental cursor per source: `kind`
  (`chat`/`native`), `path`, `cursor_value` (conversation sequence or byte
  offset), `file_size`, `file_mtime`, `fingerprint`, `doc_count`, `state`
  (`pending`/`active`/`unavailable`/`unsupported`), `last_error_code`,
  `updated_at`.

### 2.2 What gets indexed, per harness

| Source | Units indexed | When |
| --- | --- | --- |
| AO Chat | session display name; every visible user/assistant message (active-branch rule); every activity's `summary`, `detail_json` paths, `command_output`, and `streamed_text`; touched file paths | on write via a bounded sweep over `latest_sequence`, plus backfill |
| Claude Code | `user`/`assistant` text parts **and** `tool_use`/`tool_result` text; prompts queued while busy (`queued_command`); edited files (`edited_text_file`); sidechain and meta records skipped | byte-offset incremental, plus backfill |
| Codex | One stream per rollout (§8.6): the `event_msg.item_completed` items (`UserMessage`, `AgentMessage`, `CommandExecution` command + output, `FileChange` paths + changed lines, `McpToolCall`, web-search `Extension`, `Reasoning` summaries) when the file has them, else `response_item` (messages, tool calls and outputs, reasoning summaries) | same |
| OpenCode v1/v2 | `session.title`; `session_message` user/assistant rows: text, reasoning and tool parts (shell command + output, edit/write path + content, fetch/search) | read-only SQLite attach + backfill |
| Other harnesses (Kimi included) | title + prompt only | backfill; coverage says transcripts are not parsed |

Native transcripts are opened `O_RDONLY`, read forward from the stored offset,
and refused if the resolved path escapes the provider root (including through a
symlinked parent).

### 2.3 Incremental updates

- **Chat**: a cheap fingerprint over the visible message ids + revisions + the
  conversation's activity head. A rollback or an edit changes it even when
  `latest_sequence` does not move, so the skip stays honest. A changed source is
  rewritten in one pass and pruned by `indexed_at < this pass`, which removes
  anything the current view no longer shows.
- **Native**: fsnotify-style size/mtime check per sweep; a file whose size went
  backwards or whose path changed resets its cursor and re-indexes from zero.
  Partial trailing lines are left for the next pass.
- **Sweep cadence**: 30 s in the daemon; a backfill starts 5 s after boot in
  50-session pages with a 10 ms yield between pages.

### 2.4 Memory limits (8 GB Air)

- 256 KiB read buffer, one line decoded at a time, 64 documents per transaction.
- Per-document body capped at 8 KiB (a snippet needs far less); path lists
  capped at 200 entries; titles at 512 bytes.
- One worker goroutine; no fan-out. Files over 64 MiB read from the current
  offset only.
- Command output is the big one: 17.7 MB total today, capped per row by AO at
  64 KiB. Indexing it tail-truncated (last 8 KiB) keeps the useful error text
  without duplicating AO's whole buffer.

### 2.5 Failure behavior

- Index failures never fail a session write or a chat turn.
- Every error is recorded on the source row (`last_error_code`) and logged at
  warn; the rest of the index keeps serving.
- A malformed transcript line is skipped; the source stays active.
- A missing transcript marks the source `unavailable` and keeps already-indexed
  documents — a deleted file must not erase history.
- Search degrades explicitly: the API returns `coverage`
  (`ready|backfilling|unavailable`, counts, `unindexedHarnesses`) so an empty
  result over an incomplete index is never read as "this never happened".

### 2.6 Query language and ranking

**Operators** (parsed in one box, so the same syntax works in Cmd+K, the Search
page, and `ao search`):

| Operator | Meaning |
| --- | --- |
| `harness:codex` | restrict to one harness |
| `project:api` | restrict to one project (id or name prefix) |
| `machine:<host>` | restrict to one connected machine |
| `before:2026-08-01`, `after:2026-07-01` | date bounds |
| `created:2026-01-01..2026-06-01` | date range in one operator; `*` for an open end |
| `.go$` | suffix anchor (fzf); matched as a token, with the suffix recorded for post-filtering |
| `foo \| bar`, `foo OR bar` | either term (fzf/Atuin style) |
| `status:killed` | `killed`/`terminated`, `live`, `hibernated` |
| `path:src/auth` | restrict to touched paths |
| `"exact phrase"` | phrase match |
| `'exact` | exact token (fzf style) |
| `^prefix` | prefix anchor |
| `!term`, `-term` | exclude (the whole session, not one document) |
| a PR URL | resolves to its session |

**Matching** (revised in §7 after measurement): plain words are ranked, not
required. They are joined with OR, minus stopwords (NLTK's English list; a
query of only stopwords keeps them), and each matches as a prefix of its Porter
stem (`failing` → `"fail"*`, `signatures` → `"signatur"*`), with the last word
always a prefix because it is the one being typed (Algolia's `prefixLast`).
Explicit operators stay requirements: a `"phrase"`, `'exact`, `^prefix` and
`suffix$` must match. The first implementation ANDed every word and returned
nothing for 100% of the natural-language questions in LongMemEval and LoCoMo.

**Ranking**: BM25 throughout. FTS5's `bm25()` (k1 = 1.2, b = 0.75) with column
weights title 8 > paths 4 > body 1, multiplied by a per-kind weight (user 1.25,
title/assistant/path 1.0, tool/command 0.8), a BM25F-style field boost rather
than a hard tier: a strong agent-reply match can beat a weak user mention.
Results are grouped one row per session (its best document, scored as best +
0.25 × the rest of its matches) with a count of the session's other matches;
`group=message` lists every matching document. Recency breaks ties. Browsing
(no query, or `sort=recent`) orders by recency instead.

**Tokenization**: identifiers and paths are split before indexing
(`handleSubmit` → `handle`, `submit`; `src/auth/login_test.ts` → `src`,
`auth`, `login`, `test`, `ts`) and also kept whole, so both `handleSubmit` and
`submit` find the same document. The expansion is stored after a separator, so
snippets show only the original text. Common words are handled by BM25's IDF
(FTS5 clamps a word in more than half the documents to a negligible weight)
and, for queries, by the stoplist above.

**Benchmarks**: two layers. A fixed rule corpus with named cases (§5) pins the
product rules; the retrieval benchmark (§7) measures recall, nDCG and MRR on
LongMemEval-S, LoCoMo and an AO-specific agentic suite, against a committed
baseline that fails `go test` on regression.

### 2.7 Exposure

- REST: `GET /api/v1/search/sessions` (`q`, `projectId`, `harness`, `from`,
  `to`, `status`, `path`, `sort`, `group`, `limit`, `offset`) and
  `GET /api/v1/search/index-status`. Both declared in `specgen` so the parity
  test passes.
- CLI: `ao search <query>` with `--json --harness --project --since --until
  --path --sort --per-message --index-status`, a thin daemon client like every
  other command. An agent can ask in its own words (`ao search "how did we fix
  the flaky login test"`).
- UI: **both** surfaces search the same index through the same endpoint and the
  same query language:
  - **Cmd+K palette** (the existing palette, extended — no second palette). See
    §2.7.1.
  - **`/search` sidebar page** ("Past sessions" in the sidebar top nav, beside
    Automations and Artifacts) with project, agent, and date filters.

  Results on both are one row per session (its best match) with the session
  name, project, harness, state, a snippet with the match marked, and how many
  more matches the session holds (`group=message` lists every match). Selecting
  a result opens the session at the
  matching message with the match highlighted; Enter/Shift+Enter then step
  through the session's other matches. A modifier action copies the resume
  command.

#### 2.7.1 Cmd+K palette behavior (as shipped)

The palette is the existing `components/CommandPalette.tsx`, which already
handles commands, session actions, and workspace file search. Session-text
results are a fourth source inside it, not a new dialog.

| Aspect | Behavior |
| --- | --- |
| Trigger | The existing `command-palette` keybinding (⌘K / Ctrl+K). No new shortcut. |
| Input | The same box. Typing ≥2 characters searches session text; the floor exists because one character matches nearly the whole archive. |
| Ranking in the list | Session-text rows render in their own group **above** command rows: typing into the palette is a search, so transcript text must not be buried under a command whose label happens to match. |
| Row content | Session name, a match-kind label (`session` / `you` / `agent` / `tool` / `command` / `file`), project, harness, and the snippet with matched terms wrapped in `<mark>`. The kind label is what tells a person *why* a row matched. |
| Select (Enter / click) | Navigates to the session **and** hands `{ messageId, text, query }` to `lib/session-search-reveal.ts`. The session surface consumes that target once, feeds it to the Chat timeline's existing annotation navigation, and the timeline scrolls to the message and highlights the match. A result with no message id (command output, a path, a native transcript record) opens the session without a fabricated anchor. |
| Modifier (⌘/Ctrl+Enter, ⌘/Ctrl+click) | Copies `ao session resume-agent <id>` instead of opening, mirroring the palette's existing "secondary action on a modifier" pattern. cmdk's `onSelect` drops the pointer event, so the modifier is read from the row's own capture handlers. |
| Empty result | Says "No past sessions matched", and when the index is incomplete adds what it cannot see (backfill progress, harnesses whose transcripts are not parsed). An empty result over an incomplete index must never read as "this never happened". |
| Failure | A search error renders as an inline alert in the list; an unavailable index-status resolves to a note, never an error beside a working search. |

Why both surfaces rather than one: the palette is for a person who already knows
what they are looking for and wants to be there in one keystroke; the page is
for browsing and filtering an archive. They share the endpoint, the parser, and
the result model, so a query typed in one behaves identically in the other and
in `ao search`.

### 2.8 Tests

- Indexer: chat backfill; incremental append; fingerprint change on rollback;
  native JSONL backfill + byte-offset append; truncation reset; partial trailing
  line deferred; read-only guarantee (source mtime/size unchanged); path escape
  refused.
- Store: ranking (title > body), path match, project/harness/date filters,
  trigger-maintained delete.
- Query language: every operator, plus malformed input.
- Benchmark: fixed corpus, fixed expectations.
- API: validation, filters, coverage, 501 without a service.
- CLI: flags, JSON, error envelope, index status.
- UI: snippet highlighting (offsets, overlaps, out-of-range), result rendering,
  click → navigation + reveal, Enter/Shift+Enter stepping.
- Cmd+K specifically (`CommandPalette.test.tsx`, "session-text search"): the
  marked snippet renders, selecting a row navigates **and** hands the matched
  message to the session (asserted on the reveal store, not just on navigation),
  a command-output hit opens without inventing an anchor, the modifier copies the
  resume command instead of opening, the two-character floor issues no request,
  and an incomplete index explains itself. The reveal assertion was verified to
  fail when the hand-off is removed, so it cannot pass vacuously.
- The receiving end (`SessionChatSurface.test.tsx`, "search reveal"): the
  session surface consumes the handed-over match and forwards
  `{ messageId, text }` to the timeline, then clears it so a remount does not
  jump again. This closes the chain the palette test starts — palette → reveal
  store → session surface → timeline scroll/highlight — so "selecting a result
  opens the session at the matching message" is covered at both ends rather
  than only asserted as navigation.

---

## 3. Competitor and prior-art review

Read from primary sources on 22 Oct 2026, not from summaries. Every claim below
is attributable to the linked page; where a product's behaviour is not documented
I say so rather than guessing.

### 3.1 Orca — the closest competitor

Sources: [Agent session history](https://www.onorca.dev/docs/agents/session-history),
[CLI reference](https://www.onorca.dev/docs/cli/reference), and the
[BM25 announcement](https://x.com/orca_build/status/2107855216125305305).

**What it does well**

- **Scope as a first-class toggle.** `Workspace` / `Project` / `All`, plus a
  per-agent enable list and grouping by Project, Folder, or Agent. A header count
  reads like `12 shown · 47 recent`, and the count is of *sessions*, not documents.
- **Two sort modes, remembered separately.** Browsing sorts by Last updated or
  Created; searching switches the control to Most relevant or Newest, with the
  newer session winning ties. This is the cleanest articulation of "relevance
  while searching, recency while browsing" I found.
- **Explicit scan limits.** "Show more" past the scan limit, and a
  "Refresh Session History" button. It is honest about not having searched
  everything yet.
- **Rich row actions.** Resume, Resume in New Native Chat, Copy resume command,
  Copy session ID, Copy log path, Open log, Reveal log, Open cwd. The resume
  command differs per agent (`claude --resume <id>`, `codex resume <id>`,
  `pi --session <file>`) and it re-exports `CODEX_HOME` when the original set it.
- **CLI parity.** `orca search --query --scope conversation|all --agent --path
  --since --sort --limit --cursor --json --index-status --fresh`, searching the
  same index as the panel, including other paired machines.
- **`--index-status`** answers "is the index ready" without running a query.

**What I am copying**

- The two sort modes and their separation between searching and browsing.
- A count that is sessions-or-documents matched, not the page length.
- `--index-status` (already shipped in `ao search`).
- Per-agent resume commands and a copy action on a modifier.
- Per-agent enable/disable and a scan-limit disclosure.

**What we do better**

- **Orca searches preview text and metadata.** Its docs describe filtering "by
  session title, working directory, branch, model, or preview text"; tool calls
  are not documented as indexed. AO indexes every tool call, its arguments, its
  output, every command's output, and diff text.
- **Orca resumes; it does not land on the match.** Its result opens the session.
  AO opens at the exact message with the match highlighted, then steps through
  the rest with Enter/Shift-Enter.
- **Orca's cross-machine search needs per-machine opt-in** and says so when a
  host is offline. AO is local-only and honest about it, with no half-enabled
  remote state to explain.
- **Orca's index can lag behind a Refresh button.** AO sweeps incrementally and
  reports backfill progress.

### 3.2 Zed — thread history

Sources: [Parallel Agents / Thread History](https://zed.dev/docs/ai/parallel-agents).

**What it does well**: Thread History (`cmd-g`) holds every thread including
archived ones, grouped by project; restoring a thread whose worktree was deleted
**restores the worktree automatically**. Quick switching is `ctrl-tab` through
recent threads. External agent threads can be imported.

**What I am copying**: grouping by project in the result list, and the principle
that opening a result should restore whatever the session needs, not just show a
transcript.

**What we do better**: Zed's search is **title-only fuzzy matching** ("search will
fuzzy match on thread titles"). AO searches the full transcript. This is the
single largest gap in the field and the reason AO's search is worth building.

### 3.3 Claude Code `/resume`

Sources: [Manage sessions](https://code.claude.com/docs/en/sessions).

**What it does well**

- **Scope cycling in the picker**: `Ctrl+W` widens to all worktrees of the repo,
  `Ctrl+A` to every project on the machine, `Ctrl+B` filters to the current
  branch. Each toggles back.
- **`Space` previews** a session without leaving the picker; `1`–`9` resume by
  position; `Ctrl+R` renames inline.
- **Paste a PR URL** to find the session that created it.
- **Grouped forks**: `/branch` and `--fork-session` produce separate sessions that
  the picker groups under one row, expandable with `→`.
- **Honest about what a resume restores**: model, agent, permission mode,
  interrupted tool calls, scheduled tasks — with a table of exactly which mode
  you come back in.

**What I am copying**: the PR-URL-as-identifier behaviour (shipped), grouped
same-session rows, and preview-before-commit.

**What we do better**: the picker matches **name, generated title, summary, or
first prompt** — not transcript text. It also cannot land on a message. AO does
both, and AO's grouping is by session rather than only by fork.

### 3.4 Codex `resume` picker

Source: [`resume_picker.rs`](https://github.com/openai/codex/blob/main/codex-rs/tui/src/resume_picker.rs).

**What it does well** — the most carefully engineered picker in the field:

- **Two-layer filtering, stated explicitly in the source**: provider / source /
  cwd filtering pushed to the backend, then typed search over the loaded rows in
  the picker. That is the right split and it is what AO's server-side filter plus
  client-side presentation already mirrors.
- **Cursor pagination with dedupe across pages** (`SeenRowKey`), 25 per page,
  loading when within 5 of the end — so new sessions arriving mid-scroll cannot
  duplicate or shift rows.
- **A toolbar** (Filter / Status / Sort) reached with `Tab` and changed with
  arrows, so the keyboard never has to leave the list.
- **Density toggle** (`Ctrl+O`) and lazy transcript previews (`Ctrl+E` expands,
  `Ctrl+T` opens the full transcript), loaded off the UI thread with cancellation.
- **Sort keys are real**: Created vs Updated, with a labelled column header.

**What I am copying**: pagination that dedupes, loading near the end rather than
only on click, and a preview that loads lazily with cancellation.

**What we do better**: the picker searches title, first message, ID, branch, and
cwd — metadata, not transcript text. Its `matches_query` is a substring scan over
those fields.

### 3.5 OpenCode session list

Source: [TUI docs](https://opencode.ai/docs/tui/) and the
[`dialog-session-list` component](https://github.com/sst/opencode/blob/bbd1c071/packages/opencode/src/cli/cmd/tui/component/dialog-session-list.tsx).

**What it does well**: `/sessions` (alias `/resume`, `Ctrl+X L`) filters as you
type and caps the list at 30 results.

**What I am copying**: the hard result cap. A palette that renders hundreds of
rows is slower and no more useful than one showing the best 30.

**What we do better**: whether its filter is full-text is not documented and the
component filters a session list in memory. AO indexes OpenCode's own database
(read-only) so its history is searchable from AO's search, not only inside
OpenCode.

### 3.6 Cursor and Windsurf

**Cursor**: Cmd+K searches full transcripts from a local index with snippets,
scoped to This Computer / Cloud / a specific machine
([forum thread](https://forum.cursor.com/t/supports-searching-session-content-instead-of-just-titles/170411),
[side-chat changelog](https://cursor.com/changelog/side-chat)).

**Windsurf**: I could not verify a documented global chat-history search. The
pages that would describe it returned 404 during this review, so I make no claim
either way.

**What I am copying from Cursor**: transcript-level search with a snippet per
result, and a machine scope selector.

**What we do better**: Cursor stops at the conversation; landing on the matched
message still needs a second Cmd+F. AO lands on the message and steps through
matches in one flow.

### 3.7 Warp, Raycast, VS Code — palette mechanics

Sources: [Warp command search](https://docs.warp.dev/terminal/entry/command-search/),
[Warp palette](https://docs.warp.dev/terminal/command-palette/),
[Raycast manual](https://manual.raycast.com/search-bar).

**What they do well**

- **Typed prefixes in one box**: Warp uses `history:` / `ai_history:` /
  `sessions:`; VS Code uses `>` for commands, `@` for symbols, `#` for search,
  `:` for line numbers. One field, many namespaces.
- **Raycast's ranking order**: exact alias, then fuzzy title, then frecency, with
  `⌘K` opening an action panel and `⌘Enter` running the secondary action.
- **Recents before a query.** Palettes that are useful on an empty box show what
  you were last doing.

**What I am copying**: one box with typed filters (shipped), the modifier for a
secondary action (shipped: ⌘Enter copies the resume command), and **recents
before a query** (shipped this pass).

**What we do better**: none of these search session *content*; their namespaces
are commands, files, and history lines. AO adds a transcript namespace to a
palette that already had commands and files.

### 3.8 Slack and GitHub — search syntax

Sources: [Slack search](https://slack.com/help/articles/202528808-Search-in-Slack),
[GitHub search syntax](https://docs.github.com/en/search-github/getting-started-with-searching-on-github/understanding-the-search-syntax).

**What they do well**

- Slack: `"phrase"`, `-word`, `in:`, `from:`, `before:` / `after:` / `on:` /
  `during:`, `has:`, and modifiers combined freely. It also documents `rep*` for
  a prefix on a partial word of at least three characters.
- GitHub: `QUALIFIER:value` with `>`, `>=`, `<`, `<=`, and `N..N` ranges for dates
  and numbers; `-QUALIFIER` to exclude; `NOT` for keywords; quotes for phrases
  with whitespace.

**What I am copying**: the qualifier vocabulary (`project:`, `harness:`,
`before:`/`after:`), `-`/`!` exclusion, quoted phrases, and **range syntax**, which
AO did not have.

**What we do better**: Slack requires `rep*` and a three-character minimum for
prefix matching; AO prefix-matches every term by default, which is what a person
typing mid-word expects.

### 3.9 fzf — token syntax and keyboard flow

Source: [fzf README](https://github.com/junegunn/fzf).

**What it does well**: `'exact`, `'exact'` (word-boundary), `^prefix`, `.suffix$`,
`!inverse`, `!^inverse-prefix`, `!.inverse-suffix`, and `|` for OR. Plus
`--preview` with a key to toggle it, and `CTRL-R` toggling relevance vs
chronological order.

**What I am copying**: `'exact`, `^prefix`, `!exclude` (shipped), and **suffix and
OR support**, which AO lacked. Also the "toggle sort order from the list" idea,
which the Search page's relevance/newest control implements.

**What we do better**: fzf matches one line of text with no notion of session,
author, or time; its ranking cannot say "your own words outrank the agent's
reply".

### 3.10 Atuin — filter mode versus search mode

Source: [Atuin advanced usage](https://docs.atuin.sh/main/guide/advanced-usage/).

**What it does well** — the clearest conceptual split in any of these tools:

- **Filter mode** (which history): global / host / session / directory /
  workspace / session-preload, cycled with `ctrl-r`.
- **Search mode** (how to match): fuzzy / prefix / fulltext / daemon-fuzzy,
  cycled with `ctrl-s`.
- Context switching: `ctrl-a c` jumps to the selected command's session context.

**What I am copying**: the explicit separation of *scope* from *matching*. AO's
operators do this implicitly (`harness:` changes scope, quotes change matching),
but the two are different questions and the docs now say so.

**What we do better**: Atuin indexes shell command lines, not agent conversations,
so it has no notion of an agent reply or a tool result.

### 3.11 SQLite FTS5 — the index itself

Source: [FTS5 documentation](https://sqlite.org/fts5.html).

Facts that shaped the implementation:

- **External-content tables** (`content=`) store the index without a second copy
  of the text — which is why AO's index is 7.6% of the source rather than ~100%.
- **`bm25(t, w1, w2, w3)`** takes per-column weights, so the title/body/paths
  weighting is native rather than emulated.
- **Prefix indexes** (`prefix='2 3'`) make prefix queries a lookup instead of a
  merge. Measured on a 22,000-document snapshot (§7.5): two- and three-letter
  prefixes got 2–4× faster natively (`"te"*` 32 → 7.6 ms), and the postings grew
  2.35× (16.5 → 38.9 MB), with the matching write cost on every insert. Not
  adopted: after the changes in §7.5 the prefix merge is a minority of a broad
  query's cost, and the remaining cost is scoring, which a prefix index does
  not reduce.
- **The porter tokenizer stems prefix queries too**, so it breaks typeahead: on
  a porter index `"authenti"*` matches nothing, because "authentication" is
  indexed as "authent" (checked with the sqlite3 shell). AO therefore keeps an
  unstemmed index and stems on the query side, matching each word as a prefix of
  its stem (§7.3), which keeps half-typed words working.
- **`snippet()` re-runs the match.** For a prefix term FTS5 rebuilds the merged
  posting list of every token sharing the prefix on each call; on the snapshot
  that was 600 ms of a 900 ms keystroke. Result snippets are cut in Go from the
  page's own text instead (`domain.SessionSearchSnippet`).
- **External content can live in its own table.** Ranking reads metadata for
  every candidate; with an 8 KiB body in the same row, each read walked the
  body's overflow pages. Text now lives in `session_search_text`, which is the
  FTS content table, and `session_search_docs` is narrow.
- **The trigram tokenizer** enables substring matching, but has three costs: no
  sub-3-character matches, a much larger index, and no prefix/phrase semantics
  alongside it. AO's identifier splitting achieves substring-like reach for code
  identifiers at a fraction of the size, so trigram is deliberately not used.
- **`detail=column`/`detail=none`** shrink the index at the cost of phrase and
  NEAR queries. AO needs phrases, so it stays at `detail=full`.
- **Default searches are case-independent**, and `unicode61 remove_diacritics 2`
  folds accents — so "café" matches "cafe" and case never matters.
- **A `*` inside a quoted string is not a prefix operator**; it is passed to the
  tokenizer. AO therefore appends `*` outside the quotes.

### 3.12 Meilisearch and Tantivy — ranking ideas

Sources: [Meilisearch ranking score](https://www.meilisearch.com/docs/learn/relevancy/ranking_score),
[Tantivy](https://github.com/quickwit-oss/tantivy).

**What they do well**

- Meilisearch exposes a **`_rankingScoreDetails` breakdown** so a person can see
  which rule decided a result, and normalises the score to 0–1.
- Its rule order is words → typo → proximity → attribute → exactness, where
  earlier rules dominate. AO's first ranking had the same shape (a hard kind
  tier, then bm25); measured, the hard tier cost 22 points of turn-level
  recall@10 on LoCoMo, so AO folds kind into the score instead (§7.3).
- `matchingStrategy` (default `last`) returns documents matching only some
  words after those matching all; AO's OR matching follows the same idea, and
  the measured ablation of an explicit all-words-first tier is in §7.3.
- `maxTotalHits` (default 1,000) bounds the candidate set for speed; AO's
  ranking stage does the same with a 2,000-candidate cap (§7.5).
- Tantivy's BM25 is field-weighted and its snippets come from matched-term
  positions.

**What I am copying**: the *idea* of an explainable ranking (AO's `kind` is
returned per result, so a row says why it matched) and normalized-feeling scores.

**What we do better / deliberately not copied**: typo tolerance and proximity
ranking. Typo tolerance on code identifiers is actively harmful — `user_id` and
`used_id` are different symbols — and proximity matters less when results are
whole messages rather than fragments. `detail=full` already gives FTS5 phrase
support, which is the part of proximity that earns its keep.

### 3.13 What this review changed in the implementation

The review was not decoration; four shipped changes came directly from it:

| Finding | Change |
| --- | --- |
| Orca's `12 shown · 47 recent` needs a real total | The daemon now counts matches instead of reporting the page length; the page shows "N of M matches" and "Show N more". |
| Raycast/Atuin show recents before a query | The palette lists recent sessions on an empty box, capped at five and placed *below* commands so running something is not buried. |
| Orca/Linear's two sort modes | The Search page gained a relevance/newest control, and a filter-only browse now queries instead of showing nothing. |
| Slack/GitHub/fzf syntax breadth | Range syntax, suffix, and OR were added to the query language. |

### 3.14 Deliberate non-goals, with reasons

- **Semantic/embedding search.** Every reference implementation here is keyword
  search. Embeddings would add a model download and resident memory that the 8 GB
  target cannot spare, for a case (half-remembered wording) that prefix and
  phrase matching already cover.
- **Typo tolerance.** See §3.12: on code identifiers it produces confidently
  wrong results.
- **A dedicated preview pane.** Claude Code's `Space` and Codex's `Ctrl+E` exist
  because their lists show only metadata. AO's rows already show the matched
  snippet, and Enter lands on the match, so a second render of conversation
  content inside the palette buys little. This is the one item from the brief I
  am consciously declining.
- **Cross-machine indexing.** `machine:` parses and filters; each daemon indexes
  its own machine. Orca's model requires per-host opt-in and an offline-host
  notice, and AO's remote sessions are reached through their own daemon.

## 4. Implementation map

| Area | Files |
| --- | --- |
| Migration | `backend/internal/storage/sqlite/migrations/*_session_search.sql` |
| Store | `backend/internal/storage/sqlite/store/session_search_store.go`, `session_search_names.go` |
| Domain vocabulary | `backend/internal/domain/session_search.go` |
| Query parser | `backend/internal/domain/session_search.go` (`ParseSessionSearchQuery`) |
| Indexer | `backend/internal/service/sessionsearch/service.go`, `native.go`, `opencode.go` |
| API | `backend/internal/httpd/controllers/session_search.go`, `specgen/build.go` |
| Daemon wiring | `backend/internal/daemon/session_search_wiring.go` |
| CLI | `backend/internal/cli/search.go` |
| UI page | `frontend/src/renderer/components/SessionSearchView.tsx`, `hooks/useSessionSearch.ts` |
| Cmd+K | `frontend/src/renderer/components/CommandPalette.tsx` (new palette mode) |
| Reveal channel | `frontend/src/renderer/lib/session-search-reveal.ts`, `components/chat/SessionChatSurface.tsx` |
| Stemming, snippets | `backend/internal/domain/session_search_stem.go`, `session_search_snippet.go` |
| Retrieval benchmark | `backend/internal/service/sessionsearch/search_benchmark_test.go`, `testdata/search_benchmark_baseline.json`, `scripts/search-benchmark.sh` |
| Latency benchmark | `backend/internal/service/sessionsearch/search_latency_test.go` |
| Docs | `docs/session-search/ANALYSIS.md` |

## 5. Benchmark results

`backend/internal/service/sessionsearch/benchmark_test.go` is a fixed corpus (5
sessions across 3 projects and 3 harnesses) with fixed queries and expected top
results. It is a correctness benchmark, not a throughput one: every case names
the product rule it proves, so a regression reports the rule rather than a
number. All cases pass (`go test ./internal/service/sessionsearch/ -run
TestRankingBenchmark`).

| Case | Query | Expected top | Rule proved |
| --- | --- | --- | --- |
| title beats body | `flaky login` | `auth-1` / title | a name match outranks a body mention |
| user message beats agent reply | `forever` | `auth-2` / assistant | an agent reply is findable; no title is invented for it |
| human words outrank the reply | `give up` | `auth-2` / user | the human's words outrank the agent's reply |
| rare term beats common term | `constant-time` | `billing-1` / assistant | a one-document term is not diluted |
| command output is searchable | `TestLogin` | `auth-1` / command | tool and command output is indexed |
| path is searchable | `login_test.ts` | `auth-1` / title + path present | a touched path is indexed |
| phrase must be contiguous | `"resets its backoff"` | `auth-2` / assistant | phrase matching is contiguous |
| exclusion removes a session | `backoff !upload` | `legacy-1` / user, `auth-2` absent | `!term` removes the whole session |
| harness operator filters | `backoff harness:codex` | `auth-2` / title, `auth-1` absent | `harness:` restricts to one agent |
| project operator filters | `webhook project:billing` | `billing-1` / title | `project:` restricts to one project |
| status operator filters | `backoff status:killed` | `legacy-1` / title | `status:killed` is terminated only |
| path operator filters | `backoff path:src/upload` | `auth-2` / title, `auth-1` absent | `path:` filters sessions by touched path |
| identifier splitting | `login` | `auth-1` / title | an identifier is findable by its parts |

Plus two independent assertions:

- `TestRankingBenchmarkKindWeights` pins the kind weighting with identical text
  in every kind: title, then user, then assistant, then tool and command.
- `TestRankingBenchmarkWeightsAreNotTiers` pins the other half: an agent reply
  about the query outranks a long user message that mentions it once. (This
  replaced the earlier `KindOrder` test, which pinned a hard tier; see §7.3.)
- `TestRankingBenchmarkRecencyTieBreak` pins deterministic ordering for
  equal-relevance documents, which is what keeps a result list stable while a
  person types.

### What the benchmark caught during implementation

Writing the benchmark first was not ceremony: it found four real defects that
unit tests built around single documents would have missed.

1. **Title documents ignored the session's own name.** They used only the
   provider's thread title, so a session the user had named was unfindable by
   that name. `domain.SessionSearchTitle` now prefers `display_name`.
2. **Chat documents carried no harness**, so `harness:codex` matched nothing for
   AO Chat sessions. The conversation query now joins `sessions`.
3. **`path:` matched only documents whose own `paths` column matched**, which
   meant "the diff row", not "the session that touched this file". It now filters
   sessions.
4. **`!term` excluded documents, not sessions**, so a session's other rows stayed
   visible and the exclusion looked ignored. It now excludes whole sessions.

### Measured on the author's real database

| Figure | Value |
| --- | --- |
| AO sessions | 120 |
| Chat messages | 2,365 |
| Chat activities | 15,914 (7,638 `command`, 7,245 `reasoning`, 424 `file_change`, 150 `mcp_tool`) |
| Command output stored | 17.7 MB total, 64 KiB max per row |
| Claude Code transcripts | 29 files, 20 MB (largest 5.6 MB) |
| Codex rollouts | 227 files, 329 MB |

The corpus is dominated by command output, which is exactly why skipping tool
output (as several competitors do) makes their search feel empty by comparison.
---

## 6. Verification: test results and measured footprint

This section records what was actually run, not what was intended. Every figure
below comes from a command in this session on this machine (8 GB Air).

### 6.1 Backend suites

`cd backend && go test <packages>` with the ambient AO session environment
cleared (`AO_DATA_DIR`, `AO_RUN_FILE`, `AO_BROWSER_CAPABILITY`, `AO_SESSION_ID`,
`AO_PROJECT_ID`), because those variables otherwise point the CLI tests at the
running daemon on this machine and produce failures unrelated to the code.

| Package | Result | Covers |
| --- | --- | --- |
| `internal/service/sessionsearch` | ok | indexer, parsers, ranking, benchmark, footprint, restart |
| `internal/domain` | ok | query language, tokenization, tiers |
| `internal/storage/sqlite/store` | ok | search query, filters, source cursors |
| `internal/httpd/controllers` | ok | search, matches, index-status wire contract |
| `internal/cli` | ok | `ao search` flags, JSON, error envelope |
| `internal/telemetrymeta` | ok | `ao search` classified as a user command |
| `internal/daemon` | ok | wiring, startup |

### 6.2 Frontend suites

| Suite | Tests |
| --- | --- |
| `CommandPalette.test.tsx` | 62 (incl. 8 session-text cases, recents, modifier action) |
| `SessionSearchView.test.tsx` | 9 (incl. counts, Show more, sort, date filters) |
| `SessionChatSurface.test.tsx` | 61 (incl. the reveal hand-off) |
| `Sidebar.test.tsx` | 152 |
| search hooks and command model | 16 |
| i18n instance + renderer coverage gates | 21 |
| **Total across the search surfaces** | **321 in the search-focused run; 182 without the Sidebar suite** |

`tsc --noEmit` reports no errors.

One caveat worth recording: running all ten heavy suites in a single vitest
invocation produced one timing-sensitive failure in an unrelated
`CommandPalette` case (an orchestrator-spawn error assertion). The same suite
passes alone, in pairs with every other suite, and in the nine-suite run above.
That is resource contention on an 8 GB machine, not a logic failure, but it is
why the numbers are stated per grouping rather than as one grand total.

### 6.3 Index footprint

Measured with `TestIndexFootprintStaysSmallRelativeToSourceText` and
`TestOneHugeCommandIsBounded`, which are permanent tests rather than one-off
scripts:

| Figure | Value |
| --- | --- |
| Indexed text (`docs.body + paths + title`) | 972 KB |
| Source command output it describes | 12,864 KB |
| **Ratio** | **7.6%** |
| Largest single document | ≤ 8 KiB, enforced |
| Database growth while indexing 2,800 docs | 8.5 MB |

The index stores a bounded excerpt of each source, not a second copy: the
per-document budget is 8 KiB, and command output keeps its **tail** because a
failing build is diagnosed from the end. The ratio test fails at 35%, so a
design change that starts mirroring whole outputs is caught.

### 6.4 Incremental cost

Measured on a synthetic archive shaped like the real one (60 conversations,
2,400 messages, 3,600 activities):

| Operation | Before the stamp | After the stamp |
| --- | --- | --- |
| Backfill (6,000 docs) | 547 ms | 546 ms |
| No-op sweep | 56 ms | **9.6 ms** |

The sweep compares one grouped aggregate query for the whole archive against a
recorded stamp per conversation and skips without reading message or activity
text. The backfill records the stamp as it goes, so the first sweep after a
restart is also cheap — an earlier version recorded a locally-derived stamp that
could never match the aggregate, which made the skip dead code and left every
sweep at 56 ms.

A no-op sweep at 9.6 ms every 30 seconds is about 0.03% of one core, which is the
budget this feature should occupy when nothing has changed.

### 6.5 What the research changed

The competitor review (§3) was written first and then implemented, which is the
only way a review is worth doing. Four user-visible changes came from it:

| Reference | Change |
| --- | --- |
| Orca's `12 shown · 47 recent` | Real match totals (`CountSessionSearchDocsMatching`), "N of M matches", "Show N more" |
| Raycast / Atuin recents | Cmd+K lists recent sessions before a query, capped at five and below the commands |
| Orca / Linear sort modes | Search page relevance/newest control; filter-only browse queries instead of showing nothing |
| Slack / GitHub / fzf syntax | `created:A..B` ranges, `.go$` suffix anchors, `|` / `OR` |

### 6.6 What the tests caught during this pass

Writing the tests and measurements found six real defects, which is the reason
they are listed here rather than only in commit messages:

1. **Diff text was not indexed.** Only a file change's paths were searchable, so
   a symbol added or removed in a patch could not be found. Added and removed
   lines are now indexed; context lines and `+++`/`---` headers are not.
2. **Acronym identifiers did not split.** `parseHTTPResponse` indexed only as
   `parsehttpresponse`, so `http` and `response` were invisible. The camel-case
   rule only broke on lower-to-upper and missed the acronym boundary.
3. **The stamp skip never fired.** The backfill recorded a stamp derived from
   the rows in hand instead of the aggregate it is compared against, so every
   sweep re-read the whole archive.
4. **Chat sessions re-globbed the provider tree every sweep.** A session with no
   native transcript now records that fact, so an idle sweep stops searching for
   a file that will never appear. Recorded as its own `no_transcript` state,
   because reporting it as `unsupported` would claim the whole harness is
   unindexed and make the coverage note lie.
5. **The test fixture never wired the namer**, so session names and the
   hibernated/live state were untested even though the daemon wires them. The
   hibernation test failed on the fixture and passes on the real wiring.
6. **The Search page's date filters were untested**, so a regression that dropped
   them from the request would have passed.
7. **The result total was the page length.** A search for 40 matches reported
   `total: 20`, which makes "N of M" and any next-page affordance impossible.
   The daemon now counts matches with the same WHERE clause the search uses.
8. **A recency browse was rejected.** Opening the palette with an empty box had
   nothing to ask for, so recents needed the daemon to accept a sort-only
   browse. It does now, and the palette uses it.

### 6.7 Known gaps

- **`golangci-lint`** could not run on the original 8 GB machine (Go 1.27.2
  export data). It runs cleanly under Go 1.27.1; see §8 for the result.
- **`go test ./...` has pre-existing failures** in `runtime/tmux`,
  `agent/opencode`, `agent/opencodev2`, `service/systemcheck`, and
  `session_manager`. They reproduce on `main` (this machine is missing AO's
  bundled tmux), and none of those packages are touched by this feature.
- **Desktop visual verification was blocked.** `npm run dev` fails in prelaunch
  on an unrelated native wake-helper Swift compile error, and the plain Vite dev
  server is CORS-blocked against the daemon. The palette and page were verified
  through component tests instead; a real-app pass is still worth doing once that
  build is fixed.
- **Only the local machine is indexed.** `machine:` parses and filters, but a
  remote host's sessions are searched by that host's daemon.
- **OpenCode sessions are matched to AO sessions by workspace directory.** A
  directory holding several provider sessions indexes the most recently updated
  one.
- **Ranking limits** (vocabulary gaps, very broad single words) are measured
  and listed in §7.6.

---

## 7. BM25 retrieval benchmark (revision 3, 23 Oct 2026)

The ranking was rebuilt around measurement. Everything in this section was run
on this machine (8 GB Air, shared with other sessions, so absolute latencies are
pessimistic) through the shipped code path.

### 7.1 What is measured

`backend/internal/service/sessionsearch/search_benchmark_test.go` runs three
suites through the real indexer (`Service.IndexChat` into a migrated SQLite
store), the real parser and the real `Service.Search`. The only test double is
the timeline reader, which serves a dataset's turns as Chat messages.

| Suite | Source | Size | Graded by |
| --- | --- | --- | --- |
| LongMemEval-S | Wu et al., ICLR 2025; [`xiaowu0162/longmemeval-cleaned`](https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned) | 470 answerable questions (30 abstention questions excluded, as every published retrieval number does), each over its own ~48-session haystack | answer sessions, and answer turns (`has_answer`) |
| LoCoMo | Maharana et al., ACL 2024; [`snap-research/locomo`](https://github.com/snap-research/locomo) | 10 conversations, 1,536 questions (category 5, adversarial, has no evidence and is excluded) | evidence turns (`D1:3`) and their sessions |
| agentic | written for AO | 21 sessions with commands, error output, identifiers, paths and diffs; 43 queries in 11 categories, distractor sessions sharing vocabulary on purpose | the session(s) that answer |

The public suites are retrieval over past chat sessions, which is exactly what
session search is, and they have published BM25 numbers to compare against.
Neither contains tool output or code, which is what most of an AO session is,
so the agentic suite covers error strings, symbols, identifier parts, paths,
half-typed words, word forms, near-duplicate sessions, multi-answer queries and
a deliberate vocabulary-gap category (synonyms with no shared word) that
lexical search is expected to fail, so that limit is measured rather than
assumed.

Metrics are the standard ones: recall@k as LongMemEval reports it (a hit if any
relevant item is in the top k), nDCG@10 with binary relevance, and MRR, at
session level (the grouped list, one row per session, as the product shows it)
and at turn level (the per-message list). Each corpus is indexed once and every
ranking variant queries it, so an ablation costs about as much as a single run.

**Regression gate.** The shipped numbers are recorded in
`testdata/search_benchmark_baseline.json`. A metric may not fall more than 0.5
points (MRR: 0.005) below it; the agentic suite needs no download and runs in
every `go test`, so a ranking regression fails CI.

```sh
scripts/search-benchmark.sh              # quick: agentic + LoCoMo + 100 LongMemEval questions
scripts/search-benchmark.sh full         # all 470 LongMemEval questions
scripts/search-benchmark.sh ablate       # every ranking variant side by side
scripts/search-benchmark.sh --update     # record the current numbers as the baseline
```

The script downloads the datasets once into
`~/.cache/aoagents/agent-orchestrator/search-benchmark` and verifies them by
SHA-256, so an upstream change cannot silently move the numbers.

### 7.2 Results: before and after

Full profile (`scripts/search-benchmark.sh full ablate`). v1 is the ranking
this branch had before the benchmark existed (every word required, hard kind
tier); v2 is the first OR ranking (no stemming, all-words-first, sessions
ranked by their best message); shipped is the current default.

| Suite | Metric | v1 | v2 | **shipped** |
| --- | --- | --- | --- | --- |
| LongMemEval-S (470) | session R@1 / R@5 / R@10 | 0.9 / 0.9 / 0.9 | 86.0 / 95.3 / 97.2 | **87.7 / 96.8 / 98.1** |
| | session nDCG@10 / MRR | 0.5 / 0.009 | 88.4 / 0.901 | **90.7 / 0.915** |
| | turn R@5 / R@10 / MRR | 0 / 0 / 0 | 81.5 / 87.2 / 0.672 | **83.4 / 88.7 / 0.698** |
| | queries with no result | 99.1% | 0.6% | 0.6% |
| LoCoMo (1,536) | session R@1 / R@5 / R@10 | 0.1 / 0.1 / 0.1 | 59.2 / 85.2 / 92.0 | **61.7 / 86.8 / 94.0** |
| | session nDCG@10 / MRR | 0.1 / 0.001 | 72.6 / 0.709 | **74.6 / 0.729** |
| | turn R@5 / R@10 / MRR | 0 / 0 / 0 | 51.6 / 58.4 / 0.390 | **53.4 / 62.0 / 0.409** |
| | queries with no result | 99.9% | 0.5% | 0.3% |
| agentic (43) | session R@1 / R@5 | 65.1 / 65.1 | 88.4 / 93.0 | **93.0 / 95.3** |
| | nDCG@10 / MRR | 65.1 / 0.651 | 91.3 / 0.907 | **94.5 / 0.942** |
| | queries with no result | 34.9% | 7.0% | 2.3% |

Weakest categories, shipped: LongMemEval *single-session-preference* (R@1
46.7, R@5 93.3; the question rarely repeats the answer's words) and
*temporal-reasoning* (R@1 79.5); LoCoMo *open-domain* (R@1 37.0, R@5 67.4) and
*multi-hop* (R@1 51.8); agentic *vocabulary-gap* (0, by design) and
*disambiguation* (R@1 85.7). Every other agentic category (error strings,
identifiers, identifier parts, paths, typeahead, word forms, multi-answer,
natural-language questions) is at 100%.

For scale, published BM25 numbers on the same data (self-reported, different
tokenizers and session handling, so compare loosely): agentmemory's BM25-only
LongMemEval-S run reports R@5 86.2%, R@10 94.6%, nDCG@10 73.0%, MRR 0.715
([LONGMEMEVAL.md](https://cdn.jsdelivr.net/gh/rohitg00/agentmemory@main/benchmark/LONGMEMEVAL.md));
open-memex's lexical pipeline reports R@5 97.0%, MRR 0.909 on 470 questions
([ablation study](https://unpkg.com/open-memex@0.8.1/docs/retrieval-ablation-study.md)).
On LoCoMo, Wyrm reports a no-LLM full-text floor of 59.9% recall@10 at turn
level ([BENCHMARKS.md](https://cdn.jsdelivr.net/npm/wyrm-mcp@8.5.8/BENCHMARKS.md)),
and memini a Porter BM25 turn-level R@10 of 67.1%
([memini bench](https://pkg.go.dev/github.com/eleboucher/memini/bench)).
AO's session index is per message, not per session, which is the harder
setting for session-level recall.

### 7.3 Ablations: what each choice is worth

Each row changes one thing from the shipped configuration (full profile).

| Variant (one change from shipped) | LME R@1 | LME MRR | LME turn MRR | LoCoMo R@1 | LoCoMo MRR | LoCoMo turn R@10 | agentic R@1 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| **shipped** | **87.7** | **0.915** | **0.698** | **61.7** | **0.729** | **62.0** | **93.0** |
| every word required (strict AND) | 0.9 | 0.009 | 0.000 | 0.1 | 0.001 | 0.0 | 65.1 |
| keep stopwords | 85.3 | 0.901 | 0.692 | 47.5 | 0.619 | 56.6 | 93.0 |
| no stemming | 87.9 | 0.915 | 0.680 | 60.1 | 0.714 | 58.4 | 86.0 |
| hard kind tier before bm25 | 87.0 | 0.909 | 0.656 | 56.2 | 0.686 | 39.5 | 83.7 |
| flat kind weights (all 1.0) | 86.8 | 0.911 | 0.628 | 61.1 | 0.726 | 62.2 | 93.0 |
| user weight 1.5 | 87.9 | 0.916 | 0.711 | 59.8 | 0.718 | 60.9 | 93.0 |
| all-words-first tier | 87.7 | 0.915 | 0.691 | 61.5 | 0.727 | 62.0 | 93.0 |
| session = best message (CombMAX) | 87.2 | 0.911 | 0.698 | 60.8 | 0.726 | 62.0 | 93.0 |
| session = sum of messages (CombSUM) | 84.0 | 0.895 | 0.698 | 48.9 | 0.638 | 62.0 | 93.0 |
| prefix every word | 87.0 | 0.911 | 0.698 | 61.5 | 0.732 | 62.4 | 93.0 |
| prefix no word | 87.9 | 0.916 | 0.698 | 61.6 | 0.727 | 62.1 | 86.0 |

Differences under about one point on LongMemEval (5 questions) or half a point
on LoCoMo (8 questions) are noise; the decisions below rest on the larger ones.

What the ablations decided:

1. **OR, not AND.** Requiring every word returned nothing for 100% of the
   public questions and for a third of the agentic queries. open-memex found
   the same ("OR semantics is the load-bearing wall", MRR 0.909 → 0.257).
   Plain words are OR-ed and BM25 ranks; explicit operators still require.
2. **Stopwords dropped from ranked words.** Keeping them cost LoCoMo 14 points
   of R@1 and LongMemEval 2; NLTK's list is applied to the query only, never to
   the index.
3. **Kind is a weight, not a tier.** A hard title > user > reply > tool tier
   lost 22 points of LoCoMo turn-level R@10 and 9 of agentic R@1, because a
   strong match in a reply could never beat a weak one in a user message. It is
   now a multiplier on bm25 (BM25F-style; Robertson, Zaragoza & Taylor, CIKM
   2004). The weights earn their place: flat weights cost LongMemEval 0.07 of
   turn MRR. A user weight above 1.25 helped LongMemEval (whose answers sit in
   user turns) and hurt LoCoMo (whose "user" is an arbitrary speaker), so it
   stays at 1.25.
4. **Stems as prefixes.** Porter's algorithm, implemented in Go and verified
   against Porter's own 23,531-word vocabulary with zero mismatches, turns
   each ranked word into a prefix of its stem. Without it: agentic −7 points of
   R@1, LoCoMo −3.6 of turn R@10, LongMemEval −0.018 of turn MRR. The index
   stays unstemmed because FTS5's porter tokenizer breaks half-typed prefixes
   (§3.11).
5. **Sessions score as best + a quarter of the rest.** Ranking a session by its
   single best message (CombMAX) misses evidence spread over several messages;
   by the sum (CombSUM; Fox & Shaw, TREC-2 1994) a long session wins on volume
   and lost 4 (LongMemEval) and 13 (LoCoMo) points of R@1. A quarter of the
   tail beat both.
6. **No all-words-first tier.** Ranking documents with every word ahead of
   all others changed nothing measurable (bm25 already does it) and cost a
   second full-text pass per query, so it was removed.
7. **Prefix only the last word.** Expanding every word was a wash on quality
   and multiplies the prefix merges that dominate latency (§7.5); expanding
   none cost agentic 7 points (half-typed words stop matching).

### 7.4 What the benchmark caught

Beyond choosing between alternatives, building the benchmark found defects
that the hand-written rule corpus (§5) could not:

1. **Natural-language queries returned nothing** (strict AND). This also meant
   `ao search "how did we fix the flaky login"` from an agent found nothing.
2. **Tool and command rows copied the session's touched paths** into their own
   `paths` column, which carries bm25 weight 4, so every tool row in a session
   matched any word in any touched file and could outrank the agent's reply.
   `path:` filters whole sessions, so the copy never helped a filter. Removed;
   an index format version (`indexFormatVersion`, part of both the stamp and
   the fingerprint) makes the next sweep re-index existing conversations once.
3. **Highlight offsets were bytes, the renderer slices UTF-16.** Every match
   after an accent, CJK text or an emoji was highlighted on the wrong letters.
   Offsets are now UTF-16 code units.
4. **One session could fill the page.** Results were per message; the palette
   shows eight rows, and a long session's eight matches hid every other
   session. Results are now one row per session with a match count.
5. **The benchmark itself was too slow to run after every change**, which is how
   the latency problems in §7.5 were found.

### 7.5 Latency on a real archive

`search_latency_test.go` snapshots a real AO database read-only (`VACUUM
INTO`, so the daemon's file is never written), migrates and indexes the copy
through the shipped code, and times 21 queries: palette typing (`lo`, `log`,
`test`), error strings and identifiers, agent questions, and operators. The
snapshot is this machine's own archive: 22,113 documents from 3,184 Chat messages and 18,798 activities, 47 MB of indexed text and 16 MB of FTS5 postings.

Measured with 25 results per page, one warm-up pass per query, under a load
average of 7–11 from other sessions:

| | v1 (before revision 3) | first OR build | **shipped**, per session | **shipped**, per message |
| --- | --- | --- | --- | --- |
| p50 | 105 ms | 152 ms | **131 ms** | **139 ms** |
| p95 | 998 ms | 1,504 ms | **318 ms** | **264 ms** |
| max | 1,211 ms | 1,976 ms | **416 ms** | **408 ms** |
| backfill of the whole archive | 38 s | 47 s | **33 s** | |
| indexed text | 51.3 MB | 46.9 MB | **47.4 MB** | |

Specific queries (an error string, an identifier, a phrase, `harness:` or
`status:` filters) answer in 8–50 ms; the slow tail is a single very common word
or a two-letter prefix. v1's lower median is not speed: strict AND matched
few documents, and it answered the four agent questions with 4–9 results where
the current ranking returns a full page. The index is 8% smaller than v1's
because tool rows no longer copy the session's paths.

Where the time went, found by profiling rather than guessed:

| Cause | Evidence | Fix |
| --- | --- | --- |
| `snippet()` re-runs the match; for a prefix term that rebuilds the merged posting list | `"test"*`: 904 ms with snippets, 288 ms without | Snippets cut in Go from the page's text (`domain.SessionSearchSnippet`) |
| Every request ran the full-text match up to four times (ranking, all-words tier, count, snippets) | `fts5MergePrefixLists` 30% of CPU | Total from `COUNT(*) OVER ()`; all-words tier removed; Go snippets |
| Ranking read 8 KiB rows (with `created_at` after `body`) for every candidate | page reads and overflow walks per candidate | Text moved to `session_search_text`; metadata rows are narrow |
| A broad word scores every match (6,700 for `test`) | bm25 per candidate is the floor | Ranking stage keeps the best 2,000 by raw bm25 (Meilisearch's `maxTotalHits` does the same); off whenever a filter or recency sort could discard candidates; exact totals recounted |
| Every pass rewrote unchanged text, re-indexing it | upsert's `DO UPDATE` fired the FTS triggers | Text upsert is guarded (`WHERE body IS NOT excluded.body …`) |

Considered and not adopted, with numbers: FTS5's `rank` column ordering was
slower than explicit `bm25()` (232 vs 122 ms for `"test"*`); a larger page
cache and `mmap_size` made no difference measurable above the machine's noise;
a prefix index (§3.11) costs 2.35× postings for a gain the cap mostly removed.

### 7.6 Limits that remain

- **Vocabulary gaps.** "out of memory" does not find a session that only says
  `OOMKilled`; the agentic suite's vocabulary-gap category scores 0, on purpose.
  Closing it needs synonyms or embeddings, which §3.14 declines for memory
  reasons; the category is there so a future change can prove it helps.
- **Very broad single words** ("the", a two-letter prefix) still cost a full
  scoring pass over the capped candidates.
- **LoCoMo's speakers are two people**, mapped to user and assistant
  arbitrarily, which is why it penalizes any strong user weight; LongMemEval's
  roles are real. Both are conversation datasets; the agentic suite is the only
  one with tool output, and it is small (43 queries).
- **Quick profile** samples the first 100 LongMemEval questions, which cover two
  of its six question types; `full` covers all six.

## 8. Rebase onto main (26 Oct 2026)

### 8.1 Rebases

Rebased the 22 commits onto `origin/main` 8dd86f02a8 (it had moved 116
commits), then again onto 841b5bb1fa (4 more: streaming-markdown reuse, `cn()`
text sizes, typechecks via `tsgo`, an FX warning fix). The second rebase was
clean; none of the four touch search, migrations or the API, and regenerating
sqlc, the OpenAPI spec and `schema.ts` produced no drift.

- **Migration.** At these two rebases `20261021161647852_session_search.sql`
  kept its number. At the third (28 Oct, onto `289ea750e3`, 60 commits) main
  had shipped `20261023055441558` and `20261024194546592` after it, so the
  migration was renumbered `20261027202618080_session_search.sql`, as
  AGENTS.md asks of a new migration. At the fourth (28 Oct, onto `d869fb3119`,
  16 commits) main had shipped `20261028124744441` after that, so it is now
  `20261028144134972_session_search.sql`. It never shipped. A development
  database that applied an earlier number must drop the `session_search_*`
  tables first, or the renumbered migration stops at "table already exists".
- **Cmd+K redesign (#7244), third rebase.** The palette now draws every row
  through `PaletteRow`. Session-search rows use its `snippet` line (matched
  words in the foreground, as for main's message rows) instead of `<mark>`
  segments. "Copy resume command" is the row's `secondaryAction`, run by
  main's ⌘↵ with a footer hint that names it, not a bespoke click handler.
  The session-search error joins main's single notice.
- **Titles.** Main's `UpdateSession` no longer writes `display_name` (renames
  have narrow writers), so the benchmark and OpenCode fixtures name sessions
  through `RenameSession`. The index itself was unaffected.
- **Lint.** golangci-lint v2.13.2 (Go 1.27.1) on all touched backend packages:
  0 issues.

### 8.2 Coexistence with main's palette chat search (#7185)

Main's Cmd+K also searches AO chat messages, as a "Messages" group backed by its
own `conversation_messages_fts` (porter tokenizer, kept in sync by triggers on
`conversation_messages`). The two searches overlap only on AO Chat message text:

| | Session search (this branch) | Main's chat search |
| --- | --- | --- |
| Covers | every transcript of a session: its own chat, its reviewer chats, Claude Code/Codex/OpenCode terminal transcripts, tool calls, command output, diffs, paths, titles | finished AO Chat messages of Chat-mode sessions and live reviewer chats |
| Killed / hibernated | yes | messages yes; archived reviewer chats no |
| Surfaces | Cmd+K "Past sessions", Search page, `ao search` | Cmd+K "Messages" |
| Ranking | weighted bm25, one row per session (§7) | FTS5 rank, exact phrase first, 8 messages |

**Gap found and closed: reviewer chats.** Session search indexed only a
session's own conversation, so words said in a reviewer chat (this machine
has one, 9 messages) were findable from Cmd+K through main's group but not from
the Search page or `ao search`. Reviewer chats are now their own sources
(`review:<id>`), attributed to the session they review. They stay searchable
after their review is archived or switched to the terminal, since ending a
session archives its reviews and the scope includes killed sessions; the app
only shows a reviewer chat while its session is active and the review is live
in Chat, so only then does a result carry `reviewId` and open that chat at the
message. Otherwise it opens the session itself. A session also lists exactly
one own conversation (project scope first, then newest, as main's chat search
picks it), so two conversations of one session can no longer share a source and
rewrite each other on every sweep (none on this machine have two; the guard is
cheap).

**Dedupe** (revised by §8.5): the palette keeps one row per session. A
"Messages" row whose session "Past sessions" already lists is dropped, reviewer
chats included, because that row's count includes their matches.

**Two hand-offs, one entry point.** Both surfaces open a result through
`revealSessionSearchMatch`. A match in the session's own chat uses
`session-search-reveal`, which also carries the query so Enter/Shift+Enter can
step through the session's other matches; a reviewer-chat match uses main's
`requestChatMessageFocus` (consumed by `useChatMessageFocus`), which is what
brings the reviewer chat forward. They end in the same annotation navigation;
`selectSessionTerminal` (behind `onSelectChat`) also closes an active file tab,
so a reveal never lands behind an invisible chat. Folding the query into
main's focus request would leave one channel; that is a small follow-up.

Tests added: the reviewer-chat lifecycle through the real store (found,
attributed, opened in the reviewer chat, excluded from stepping, no sweep churn,
still found after terminal switch and archive); `reviewId` on the wire; the
shared reveal helper's two channels; the conversation-level dedupe; a rendered
palette with both groups (the twin row hidden, the reviewer row kept); opening
reviewer matches from the palette and the Search page.

### 8.3 Verification (this machine, 8 GB, targeted)

| Check | Result |
| --- | --- |
| `go test -p 1` domain, sqlite/store, sessionsearch (incl. agentic gate), httpd/controllers, httpd/apispec (+ spec drift), cli | ok |
| `scripts/search-benchmark.sh` quick profile | LongMemEval R@5 100.0, LoCoMo R@10 94.0, agentic R@1 93.0, identical to the baseline |
| golangci-lint v2.13.2, touched packages | 0 issues |
| sqlc, OpenAPI, `schema.ts` regenerated | only the new `reviewId` field |
| frontend `tsgo --noEmit` (main's new typecheck) | clean, 32 s |
| Vitest `--maxWorkers=1`: CommandPalette, SessionSearchView, command-palette-search, session-search-reveal | 100 tests pass |

The MacBook Pro additionally ran, before the second rebase: `go build ./...`,
`go vet`, daemon and telemetrymeta tests, and the full frontend Vitest suite
(542 files, 9,298 tests). Not run anywhere: the complete `go test ./...`,
Playwright e2e, and a real desktop visual pass.

### 8.4 Open decisions

- **Terminal sessions of other harnesses** (37 harnesses besides Claude Code,
  Codex and OpenCode) are indexed by title only; Chat sessions of every harness
  are fully indexed. Closing it means one transcript reader per harness. Every
  session on this machine is a Chat session, so none are affected here.
- **Two FTS indexes over chat text.** Main's `conversation_messages_fts` and this
  branch's index both hold AO chat messages. Keeping both is cheap at this size;
  serving the "Messages" group from the session index would remove one, at the
  cost of changing main's ranking (exact-phrase-first) for that group.
- **The Search page forgets its query when left** (noted in the mockup review);
  a small frontend follow-up. (The missing cue for stepping is resolved by the
  find bar, §8.5.)
- **Terminal sessions** open without the find bar: the terminal has its own
  (Cmd+F, xterm search), and pre-filling it from a search result means
  plumbing a query into `XtermTerminal`. Every session on this machine is a
  Chat session.

### 8.5 Cmd+K: one row per session, opened at the best message (decision, 26 Oct 2026)

Dhruv's decision for Cmd+K, applied to the Search page too:

| Decision | Implementation |
| --- | --- |
| One row per session; main's duplicate chat row stays hidden | `withoutChatHitsCoveredBySessionSearch` drops every "Messages" row whose session is listed, reviewer chats included |
| One blended score, title weighted higher | A session scores as its best document (title documents included; the title column weighs 8× in bm25) plus 0.25 × the rest. The title document now also carries branch and project, at body weight, so a branch match counts but under a name match |
| Show the best-matching message, query marked | The row is the session's best document *that opens somewhere*: messages first, tool and command output via the message of their turn (`AnchorMessageID`), the title last. Its snippet is cut in Go with the matched words marked |
| A match count ("· 7 matches") | `sessionMatches` is distinct matched messages, without the title; a message and the tool output shown with it count once. Palette: "you · 7 matches · project · agent"; Search page: "7 matches"; CLI: "(7 matches)" |
| Enter opens at the best message, find bar prefilled, Enter / Shift+Enter step | `SessionFindBar` over the Chat transcript: the query typed in, "1/7", previous/next, Esc to close; editing the query searches again from the first match. It steps through the same set the count counts (`MatchesForSession`, one stop per message). The opened message highlights the matched word, not the whole snippet |

**A bug this found.** The search hooks called `clientForHost(hostId)` with the
local host, and `clientForHost` throws for the local host (it is for remote
machines; `clientForSessionHost` maps local to the local client). So every
search request on this machine failed in the app: the Search page, Cmd+K
"Past sessions", index status, and the in-session match list. The tests mocked
`host-clients` and could not see it; the find-bar test, which fetches matches
through the real hook, did. The hooks now use `clientForSessionHost`, and the
hook and page tests mock the local API client instead, so host resolution runs
for real (reverting the fix fails 4 of the hook's 8 tests).

**Benchmark effect.** LongMemEval and the agentic suite are unchanged. LoCoMo
moved within the gate's tolerance (session R@1 61.7 → 61.4, MRR 0.729 →
0.727): its sessions have no names, so each now gains a title document
holding its project id, which shifts bm25's collection statistics slightly.
The baseline was not re-recorded.

Tests added: the row shows the best message, not the title, with both query
words marked, and the session ranks first by its name; the count is distinct
messages (a command anchored to a message counts once); branch metadata ranks
under a name match and a metadata-only row opens with no count; the stepping
list equals the counted messages; a command result opens at its turn's message;
the find bar is prefilled and focused, counts "1/2", steps with Enter /
Shift+Enter (wrapping), marks the matched word, re-searches on edit, closes on
Esc, and is absent for a result without a query; "N matches" in the palette
row, the Search page (singular, plural, none) and the CLI; one row per session
in the rendered palette; the matched word handed to the opened session.

Verification (this machine): `go test -p 1` domain, sqlite/store,
sessionsearch, httpd/controllers, httpd/apispec (+ spec drift), cli all ok;
golangci-lint 0 issues; quick benchmark passes the gate; no sqlc/OpenAPI/
`schema.ts` drift; `tsgo --noEmit` clean; Vitest `--maxWorkers=1` on the
touched suites (CommandPalette 72, SessionChatSurface 68, SessionSearchView,
the palette model, the reveal helper, both search hooks, i18n gates) all pass.

### 8.6 The three native readers, checked against real transcripts (26 Oct 2026)

Scope: Claude Code, Codex and OpenCode only, for AO's own sessions. Other
agents' transcripts and anything outside AO are not indexed (Dhruv's decision;
readers for more agents are deferred).

Method: a read-only census of this machine's transcripts (92 Claude files,
207 Codex rollouts, 906 OpenCode v2 `session_message` rows), compared against
what each reader extracted. `TestReadersOnRealTranscripts` keeps the check: it
is skipped unless `AO_SEARCH_REAL_TRANSCRIPTS=1`, opens every file read-only,
indexes nothing, and fails on a read error or on the same user/assistant
message emitted twice within two seconds.

| Reader | Gap found | Fix |
| --- | --- | --- |
| Codex | Real `CommandExecution` items carry `command` as an argv array (`["/bin/zsh","-lc","…"]`) and output in `aggregated_output`; the reader expected a string and `output` (the shape of its own test fixture), so all 11,584 commands and their output were dropped | Read both shapes; keep only the script after `-c`/`-lc`. Command units: 4 → 11,611 (67 MB of output) |
| Codex | 205 of 205 rollouts with content write both the item stream and `response_item` for the same turns; reading both doubled every match count | Probe the first 256 KB for `item_completed` and read one stream per file |
| Codex | `FileChange` (450 items), `McpToolCall` (126), web-search `Extension` results, and `response_item` reasoning summaries (`summary_text` parts were filtered out) not indexed | Indexed: diffs as paths + added/removed lines, MCP server/tool/arguments/result, query + URLs + result titles, summaries. Reasoning is otherwise encrypted, so only 1 summary exists here |
| OpenCode v2 | Only text parts were read: 47 KB of the 4.4 MB of message data. 973 tool parts and 237 reasoning parts were skipped | Tool parts index as command (shell + output or error), path (edit/write: path, content, old/new) or tool (fetch/search); reasoning as assistant |
| Claude | Prompts typed while the agent was busy (`queued_command` attachments) and edited files (`edited_text_file`) were skipped | Indexed as user and path units. `api-request-blob`, `last-prompt` and other attachments duplicate indexed text or are bookkeeping, and stay skipped |

The census also double-counted Claude: an agent account's `projects` is a
symlink to `~/.claude/projects`, so 184 files were really 92.

**Upgrade.** Native sources now record the reader format (`native-v2`, plus
`+items` for a Codex rollout read from its item stream) in their fingerprint. A source written by an older reader is cleared and read again from
the start once, so text the old reader skipped becomes findable without the
transcript changing. Reverting that check fails the upgrade test.

**Memory.** Each unit is trimmed to the document limit (8 KB: the end of
command output, the start of anything else) and copied as it is read, so a
transcript's tool output is not held whole. Reading the two largest
transcripts here (167 MB and 101 MB) peaks at 29 MB RSS.

After the fixes, per reader (units extracted, zero duplicates):

| Reader | Sources | user | assistant | tool | command | path |
| --- | --- | --- | --- | --- | --- | --- |
| Claude Code | 92 | 253 | 1,409 | 3,493 | 3,488 | 99 |
| Codex | 207 | 1,961 | 4,639 | 316 | 11,611 | 451 |
| OpenCode v2 | 7 | 16 | 513 | 16 | 879 | 78 |

Tests added: each Codex stream is ignored in the other mode, and a rollout
with both indexes each message once (one match); real-shaped `CommandExecution`
(argv + `aggregated_output`) and the older string shape; FileChange paths and
changed lines without context or `+++` headers, findable by an added symbol and
by `path:`; MCP, web search with results, reasoning summaries, empty reasoning
and empty Extension items; item-stream detection; Claude queued prompts and
edited files, with sidechain and bookkeeping records skipped; every OpenCode
part kind, including a failed shell call; units bounded at read time (tail
kept for command output, start for prose); a source from the older reader is
read again with no duplicates.

Verification (this machine): `go test -p 1 ./internal/service/sessionsearch/`
ok; `TestReadersOnRealTranscripts` passes on the real data; golangci-lint 0
issues.

**A rollout that gains its item stream.** A Codex rollout read before its first
`item_completed` line exists is read from `response_item`. The stream is part
of the source's format (`native-v2+items`), so when the item stream appears the
source resets and is read once from it, instead of indexing its first turn from
both streams. Without that, the first turn counts twice (the test fails with
two matches).

### 8.7 End-to-end QA on real sessions, and the bugs it found (27 Oct 2026)

**Method.** 226 searches a person or an agent would type, run against this
machine's real archive (168 sessions, about 37,100 documents). The case list,
ground truth and grading live outside the repo, in the session's artifacts
(`search-e2e/search_e2e.py`). Ground truth comes from the raw tables, never from
search output. A case passes when the intended session is in the top 3; it is
soft in 4–8, which is still visible in Cmd+K.

Two ways to serve it, both on a read-only copy (`VACUUM INTO` from a `mode=ro`
URI):

- `TestServeSessionSearchOverSnapshot`: env-gated, serves the daemon router
  with only search wired.
- A real `ao daemon` from this branch on a copy fenced off from the live
  install:
  - its own data dir, run file and port;
  - every project, workspace, worktree and artifact path rewritten to scratch;
  - automations disabled;
  - scratch `HOME`, and a `PATH` without any agent, `gh` or `node`;
  - a private tmux socket and telemetry off.

  Its startup tried to recover reviewer chats and stopped at "binary not
  found"; the live sessions, worktrees and daemon were unchanged afterwards.

| Run | Pass | Soft | Fail | Find by topic | Remembered phrase | Warm Cmd+K p50 / p95 |
| --- | --- | --- | --- | --- | --- | --- |
| Before (fixture) | 166 / 218 | 27 | 25 | 18 / 30 | 14 / 26 | 223 / 570 ms |
| After (fixture) | 216 / 226 | 6 | 4 | 30 / 30 | 26 / 26 | 57 / 268 ms |
| After (real daemon) | 217 / 226 | 4 | 5 | 30 / 30 | 26 / 26 | 60 / 314 ms |

What still fails is by design:
- typos (`compction`): there is no fuzzy matching;
- a rare word whose stem matches a common one (`Regress` also matches
  "regression");
- a word past the long-text caps (§6.7).

**Bugs and fixes.** Each has a test in `bugs_found_e2e_test.go` (or the
package named). The four in the middle were also re-broken to watch their test
fail.

| Bug | Cause | Fix |
| --- | --- | --- |
| Long sessions outranked the session named for the query: 29 of 33 topic misses had the orchestrator (which relays every worker's messages, 4,362 documents) or a 611-message review at #1 | A session scored its best document plus 0.25 × the sum of **all** its other matches, so hundreds of weak matches on common words won | The tail counts the session's next 5 best matches (`DefaultSessionSearchTailDepth`; benchmark variants `tail-depth-*`) |
| Cmd+K recents were empty, and the Search page's newest-first sort was ignored | The parser always set `Sort: relevance`, and the service copied it over the request's sort; the controller tests stubbed the service | `Sort` is set only by a typed `sort:`; relevance is the service's default |
| `sort:recent` put old sessions first; `after:` let old sessions through by name | Chat title documents were dated by the indexing pass | Titles are dated by their session (index format 4) |
| Date filters on a browse, and browse rows' times, were zero (1970) | `strftime` cannot parse Go's stored time text (`… +0000 UTC`) and returns NULL; the stamp's `UpdatedAt` was always 0 too | Parse the first 19 characters |
| A pasted PR URL listed every session | It resolved to its session, then fell through to a browse whose SQL ignored the session filter (and path, machine, `!term`) | Browse and its count share one filter clause covering all filters. A PR URL lists its session first, then sessions that mention `pull/N`; a PR no AO session owns still finds those. `…/pull/N/files` is recognized |
| "· N matches" differed from the find bar's stops on 74 of 113 opens | The find bar stepped through the title; the row counted reviewer-chat matches; the row only counted inside the 2,000-candidate cap | One exact count per page session, over the session's own transcript without the title (`SessionSearchMatchCounts`). The row prefers the session's own transcript over its reviewer chat |
| `C++`, `a`, `100% \n` took about 2 s; a pasted 60-line log about 1.5 s | A one-character last word became a prefix (`c*`) over most of the index; every pasted token was OR-ed | One-character words never become a prefix and are dropped beside real words; at most 24 distinct ranked words |
| `path:` took 2.4–6.3 s and matched sessions that only printed a path | `LIKE '%…%'` over every document body | An FTS phrase on the paths column (`paths : "src routes" *`): sessions that changed the file, as the CLI help says |
| Snippets showed `<` and literal `\n` | Tool details were indexed as raw JSON | Their text values are indexed (index format 4) |
| `ao search`'s table had no session id | — | An `ID` column first |
| A fresh daemon said `ready` with 0 documents for its first half minute | Coverage only reported `backfilling` while sources were pending, and none are recorded before the first pass | `backfilling` until this process's first full pass completes, when the index is empty or the pass is running |

**Tail depth, measured.** Bounding the tail helped LoCoMo and left
LongMemEval and the agentic suite unchanged.

| Tail depth | LoCoMo session R@1 | LoCoMo MRR | LongMemEval R@1 | Agentic R@1 | Real archive: found at #1 / MRR |
| --- | --- | --- | --- | --- | --- |
| unbounded (before) | 61.3 | 0.727 | 92.0 | 93.0 | — |
| 10 | 62.1 | 0.732 | 92.0 | 93.0 | — |
| 5 (shipped) | 62.8 | 0.738 | 92.0 | 93.0 | 127 of 172 / 0.848 |
| 3 | 63.5 | 0.743 | 92.0 | 93.0 | 126 of 172 / 0.843 |
| 1 | 62.4 | 0.737 | 93.0 | 93.0 | — |

3 and 5 are within noise of each other. 5 is shipped because the real archive
is the target. The baseline was re-recorded.

Verification:
- `go test -p 1`: domain, sqlite/store (session search), service/sessionsearch,
  httpd/controllers, cli all pass.
- golangci-lint: 0 issues.
- Benchmark gate passes.
- No API shape change, so no OpenAPI, `schema.ts` or sqlc drift.

### 8.8 Bugs found on a second Mac's archive (28 Oct 2026)

The MacBook Pro's archive was searched with this branch, on a fenced copy:
361 sessions, including standalone sessions and Amp sessions. It found three
bugs that this machine's archive could not show.

| Bug | Effect | Fix |
| --- | --- | --- |
| A standalone session (no project, so `project_id` is NULL) was scanned into a string | The backfill's first query failed outright: **0 of 361 sessions indexed** after 10 minutes | Project reads in the targets, conversations and browse queries are coalesced |
| The daemon ran the backfill once, and a failed one left the state at "backfilling" | Search said "still indexing" for the life of the daemon | A failed backfill reports `unavailable` (the CLI says the index could not be built) and the daemon retries it on every sweep tick until one completes |
| Cmd+K treated only `__standalone__` as standalone | A standalone hit (empty project id) opened `/projects//sessions/<id>` | An empty project id opens `/sessions/<id>`, as the Search page already did |
| A session named for the query lost to busy sessions | `amp smoke test` ranked "Amp ChatGPT AO smoke test" 8th, and its exact name ranked it 3rd: Amp transcripts are not indexed, so its name was all it had, and the bounded tail still let sessions mentioning the words hundreds of times win | See the name tier below |

**The name tier.** Results grouped by session and ranked by relevance now put
sessions whose name holds every typed word first, ahead of any bm25 score.
- **The rule.** "Holds every typed word" is decided by
  `SessionSearchTextHasAllTerms`, the same matching that marks words in a
  snippet.
- **Real names only.** A session whose name is its own id is not "named", or
  an id such as `agent-orchestrator-7` would put every unnamed session first
  for "orchestrator".
- **Cost.** Names are matched in Go, one short string per session: 0.1 ms warm
  and 19 ms cold over 166 named sessions. There is no second full-text pass.
- **Benchmarks.** LongMemEval (quick), LoCoMo and the agentic set are
  unchanged from the baseline to the decimal. `Ranking.NameTierOff` keeps the
  old order for ablation.

Each fix has a test that fails without it: the standalone backfill, the
retry, the palette route, and the name tier. The name-tier test also fails if
ids count as names.
