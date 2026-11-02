# Plan: Desktop performance overhaul

Status: proposed (2026-10-13). Not implemented yet.

## Goal

Make AO feel instant everywhere a user waits: launch, the first page,
onboarding, switching sessions, streaming chat, terminals, and long all-day
use. "Instant" here means the screen shows useful content from local data
immediately and the network or daemon only ever refreshes it, the same model
that makes Linear feel fast.

This plan comes from three inputs:

1. A reverse engineering of the Linear desktop app (static analysis of its
   shell and cached web client, plus REA runtime coverage while scrolling).
2. Recorded benchmarks of the installed AO nightly against installed Linear.
3. Seven code audits of this repo: renderer startup and data path, renderer
   hot paths, session switching, onboarding and cold start, the Go daemon,
   terminal and browser panel, idle and memory, and build and packaging.

Every item below cites the code it changes. Item IDs (`SU-`, `DL-`, `SW-`,
`CH-`, `TB-`, `BG-`, `BE-`, `BU-`, `FX-`) are stable so commits and PRs can
reference them.

## What Linear does, and what we take from it

Linear is a thin Electron shell (stock Electron 41, no custom renderer, no
Chromium flags, no V8 snapshot). The UI is plain React 19 DOM. Its speed
comes from architecture:

| Linear technique | Evidence | AO equivalent today |
| --- | --- | --- |
| Renders from a local IndexedDB copy before any network; sync starts after first render | `RefreshManager` bootstrap types `full`/`partial`/`local`, `onFirstRender.subscribeOnce` | Nothing persisted; full-screen splash until the daemon answers |
| Optimistic edits applied to in-memory models, batched to the server | `_transactions` store, 40 per batch | Partly (chat local echo, queue reorder, kill) |
| Socket carries sync ids; client fetches deltas | `wss://sync.linear.app`, `/sync/delta?lastSyncId=` | Every CDC event triggers a full list refetch |
| Staged startup: first render, +500ms, +2s, idle, background | `LoadingDoneHelper` | Everything starts at mount |
| Lazy chunks preloaded one at a time in idle callbacks | `LoadApplicationLayout` | None |
| Window hidden until `ready-to-show`, background colour from cached theme | Linear `main/index.js` window factory | Window shown at construction, empty, then splash |
| Fine-grained observers: one change re-renders one row | ~3,000 MobX `observer` components | Clean Zustand selectors, but chat re-flows one big snapshot per tick |
| Virtualizer with cached heights, adaptive overscan, transitions for offscreen rows | `GroupedList`, `Board` (Fenwick tree) | TanStack Virtual in chat with a fixed 600px estimate, overscan 2 |
| Heavy work in worker pools, 10ms time slices with `scheduler.yield()` | `DiffParser.worker`, `SearchScore.worker` | Diff parser and big-code highlight workers only |
| Startup hints in localStorage (`startupModelCounts`, `splashScreenConfig`, `mediaDimensionsCache`) | REA page inspection | Theme only |

What AO already does better: much less JS (2.6 MB before first paint against
Linear's 13.7 MB), a smaller heap (24 MB against 87 MB), and a faster command
palette. The gap is the loading experience and behaviour under load.

## Baseline measurements

All numbers are from this machine (Apple Silicon, macOS 27.2). The installed
AO build is `0.13.4-nightly.202610051230` on Electron 33.4.11. Linear is
1.32.4 on Electron 41.3.0. Scripts and raw JSON are listed in
[Measurement](#measurement).

### AO against Linear (same protocol, medians)

| Metric | AO | Linear |
| --- | --- | --- |
| Cold launch to first paint | 1,460 ms (745 to 2,184) | 4,429 ms (4,396 to 6,178) |
| Cold launch to DOM settled | 3,002 ms | 9,116 ms |
| Reload to first paint | 214 ms | 1,019 ms |
| JS loaded on reload | 34 files, 4.2 MB | 1,326 files, 24 MB |
| JS heap after settle | 24 MB | 87 MB |
| Electron RSS | 245 MB (4 processes) | 161 MB (6 processes) |
| Idle CPU, 30s | 4.0% (2.2 app + 1.8 daemon) | 2.4% |
| Scroll, 6s wheel | 118.9 fps, p95 9.3 ms | 118.2 fps, p95 9.2 ms |
| Cmd+K to palette visible | 34 ms | 54 ms |

Caveat: these were measured in a background window from page milestones. The
user-perceived launch is worse than the table suggests because AO shows an
empty window, then a splash, then the app, while Linear shows nothing until
its first frame already contains data. Phase 0 adds a screen-recorded launch
benchmark to measure what the user sees.

### Session switching (AO, hash navigation, 4 working sessions)

| | First frame after click | Settled (no DOM change for 750ms) | Long tasks per switch |
| --- | --- | --- | --- |
| First visit | median ~62 ms (6 to 148) | median ~545 ms (339 to 707) | 1 to 2, 54 to 132 ms |
| Revisit | median ~88 ms (11 to 177) | median ~503 ms (246 to 961) | 1 to 2, 73 to 160 ms |

Revisiting is not faster than a first visit: nothing is kept between switches.

### Daemon API (installed daemon, 15 sequential requests)

| Endpoint | Median | Bytes | gzip | ETag |
| --- | --- | --- | --- | --- |
| `GET /api/v1/projects` | 0.5 ms | 379 | no | no |
| `GET /api/v1/sessions` | 2.2 ms (20 to 222 ms during boot) | 32,360 | no | no |
| `GET /api/v1/settings` | 0.2 ms | 334 | no | no |
| `GET /api/v1/sessions/:id` | 0.6 ms | 933 | no | no |

Handlers are fast at steady state. The cost is in how often the UI calls
them and what happens around the calls (process spawns, git, CDC volume).

### Bundle and package

| Item | Size |
| --- | --- |
| Renderer total | 489 chunks, 22.6 MB |
| Before first paint | 2.6 MB raw, 742 KB gzip (8 locale catalogs ~1.5 MB rendered, posthog full build 650 KB) |
| Every route also loads | xterm + webgl, `OrchestratorStartingChat` (lexical, micromark), settings, QR libraries |
| Opening a session adds | 5.7 MB (`@primer/octicons` 1.1 MB, `@react-symbols/icons` 754 KB, CodeMirror, diff/tree viewers) |
| `main.js` | 2.26 MB, of which 1.32 MB is all 8 renderer locale catalogs |
| App bundle | 489 MB; `acp-runtime` 157 MB and 6,093 of 6,220 files |

### Processes and memory observed

- Hovering an exited chat in the sidebar for 300 ms, or opening it, starts a
  persistent `ao chat-host` plus an ACP `node` runtime (about 13 MB each pair)
  that never exits because hibernation is off by default. Viewing 8 sessions
  left 8 hosts and 14 node processes running after the window and the daemon
  had quit.
- One `ao pty-host` running a full-screen agent reached about 700 MB RSS and
  36% CPU after 18 hours (unbounded partial-line buffer, TB-1).
- About 430 MB of stale cache in `~/.ao/electron`: a 210 MB root code cache
  and 198 MB HTTP cache from old dev servers and sites, none of it from the
  app itself.

## Targets

| Experience | Today | Target |
| --- | --- | --- |
| Launch (daemon running) to usable sidebar and last chat | about 3 s, splash shown | under 1 s, no splash after the first ever launch |
| Launch on a slow or offline network | up to +10 s (version-floor fetch) | same as normal launch |
| Window on launch | empty, then splash, then app | hidden until the first frame, which already shows cached data |
| Switch to a recently viewed session | about 500 ms settled, 1 to 2 long tasks | under 150 ms settled, no long task |
| Viewing or hovering a session | spawns a host and a node runtime | spawns nothing |
| Streaming chat cost per chunk | grows with conversation length | grows with new text only |
| JS before first paint | 2.6 MB | about 1.0 MB |
| `main.js` parsed before the window | 2.26 MB | under 0.9 MB |
| Idle CPU (app + daemon) | 4.0% | under 1.5%; near zero requests while hidden |
| Long-running memory | unbounded terminals, diff cache, pty ring | every cache bounded |
| App size | 489 MB | about 380 MB without the Electron upgrade, about 270 MB with it |

## Workstreams

Priority: P0 is the largest user-visible win or a correctness fix, P1 is
large, P2 is worthwhile cleanup. LOC estimates include tests. Risk is
breaking risk after the listed mitigation.

### 1. Startup and first paint (`SU`)

| ID | Pri | Change | Evidence | LOC | Risk and mitigation |
| --- | --- | --- | --- | --- | --- |
| SU-1 | P0 | Add `codeCache: true` to the `app://` scheme privileges so V8 caches compiled renderer code. Today 0 of 7,157 code cache entries belong to `app://renderer`. | `frontend/src/main.ts:454-459` | 1 | None. Filenames are content hashed. |
| SU-2 | P0 | Start the daemon first in `whenReady`, in parallel with everything else. Make the desktop version-floor check fire and forget (it awaits a GitHub fetch with a 10s timeout). Move `agentBrowserRuntime.prepare()`, `createBrowserProfileStore`, `profileImporter.initialize()` and the download-history read after `loadURL`, or start them on first browser use. | `main.ts:3019-3022`, `:3045-3168`, `:697-711`; `main/desktop-version-floor.ts:7,36` | 60 to 100 | Low to medium. Telemetry policy must still precede the daemon spawn. A blocked version shows its dialog a moment later. The profile importer must tolerate a browser opened before setup ends. |
| SU-3 | P0 | Check for an existing daemon before the login-shell probe, and only probe when spawning. | `main.ts:1644`, `:1664`, `:1193` | 10 to 20 | Low. Probe again if the daemon later restarts. |
| SU-4 | P0 | Keep the window hidden until the renderer reports its first painted frame (`BaseWindow` has no `ready-to-show`), with a 2 to 3 s fallback. Keep the theme background colour. | `main.ts:711-741` | 40 to 60 | Low. The fallback prevents an invisible window if the renderer fails. |
| SU-5 | P0 | Inline a sidebar and topbar skeleton in `index.html`, sized from localStorage hints (sidebar width, project and session counts, locale, theme), so the first frame already has the app's shape. Follow `DESIGN.md`. | `frontend/index.html`, `renderer/lib/apply-initial-theme.ts` | 60 to 120 | Low. Visual parity checked in the running app. |
| SU-6 | P0 | Replace the full-screen `DaemonStartupLoader` gate with the persisted snapshot (DL-1) plus a small "connecting" indicator. | `renderer/routes/_shell.tsx:472-475`, `:1227` | 40 to 80 | Medium. Actions on cached rows stay disabled until the daemon confirms. |
| SU-7 | P1 | Add a small staged scheduler: first paint, +500 ms high priority, idle, background. Move telemetry init, PostHog, update checks, agent-model prefetch, keybinding load and GitHub owner refresh into the later tiers. Preload route chunks (session view, settings) one at a time in idle callbacks. | `renderer/main.tsx:84-99,116`; `__root.tsx:24`; `_shell.tsx:417-432`; `router.tsx` | 80 to 150 | Low. Early telemetry events are queued, not dropped. |
| SU-8 | P1 | Persist agent readiness snapshots to SQLite and serve them first; make the model-catalog prefetch presence-only; start auth probes after the first `/sessions` response plus about 5 s, or when the composer opens. Today 32 probes run before the listener and auth probes spawn CLIs for 2 to 4.7 s each. | `agent/readiness_coordinator.go:148-166,632-645`; `daemon.go:572`; `agent/service.go:220` | ~150 | Low. A stale "authorized" is re-verified at launch time. |
| SU-9 | P1 | Daemon boot: skip migration repair probes when the schema version is current, share one session snapshot during boot (`ListAllSessions` runs 4+ times), replace the readiness full load with `GROUP BY harness`, and log per-phase timings. | `httpd/server.go:104-113`; `daemon.go:183-1035`; `agent/readiness.go:121-140`; `storage/sqlite/db.go:292-365` | ~65 | None. |
| SU-10 | P1 | Boot status: report `ready` immediately for terminated and non-candidate sessions, mark only affected rows `checking`, move reaping and message delivery after the done signal, and push readiness over CDC. Today the UI polls `/projects` + `/sessions` every 150 to 300 ms for the first ~7 s of every launch (about 25 full fetches). | `session_manager/status_readiness.go:44-53`; `manager.go:3831-3955`; `hooks/useWorkspaceQuery.ts:391-395` | 40 to 80 | Medium. Verify mobile reads `statusReadiness` the same way. Keep a 2 s fallback poll. |
| SU-11 | P1 | A quick relaunch replaces the running daemon (shutdown polled every 200 ms for up to 8 s, then a full reconcile). Hand browser-runtime ownership to the new app run in place and attach instead. | `shared/daemon-takeover.ts:55-56`; `main.ts:1549-1559` | 150 to 250 | Medium. Reuse the existing stdin token path for the handover. |
| SU-12 | P2 | Cache `gh auth status` and the owner list under `~/.ao` and render from the cached value; refresh the owner list when the clone form opens instead of at boot. | `systemcheck.go:262-297`; `main.ts:3024`, `:2511-2535` | ~60 | Low. Refresh on focus. |
| SU-13 | P2 | Onboarding "Check again" re-checks installation only (under 0.5 s) and runs auth probes in the background (today 2 to 5 s). | `InstallDependencyDialog.tsx:59-63`; `readiness.go:155-166` | ~20 | None. |
| SU-14 | P2 | Ship the startup mascot at 192 px instead of a 1254 px RGBA PNG (576 KB, decoded to 6.3 MB on every launch). | `assets/ao-mascot.png`; `TitlebarNav.tsx:166`; `DaemonStartupLoader.tsx:79` | asset | None. |

### 2. Data layer and sync (`DL`)

| ID | Pri | Change | Evidence | LOC | Risk and mitigation |
| --- | --- | --- | --- | --- | --- |
| DL-1 | P0 | Persist the TanStack cache for the workspace list, settings, agent catalogs, and the first page of recently viewed conversations (IndexedDB; localStorage is capped at 5 MB). Hydrate before first render. Bust on app version, schema version, cloud account switch, and per remote host. Data stays under `~/.ao/electron` (pinned userData). | `renderer/lib/query-client.ts`; no persister exists | 300 to 600 | Medium. Show a connecting state; never act on cached rows until confirmed; versioned keys. |
| DL-2 | P0 | Patch the cache from CDC instead of refetching. Add `revision` and the sidebar fields to the `sessions_cdc_update` payload and apply with `setQueryData` when `revision == cached + 1`; otherwise refetch. Scope usage events (`"scope":"usage"`) so they only invalidate usage queries. 14,024 of 25,410 `session_updated` rows are id-only usage touches that each refetch everything. | `backend` migrations (`usage_bindings_cdc_update`, `sessions_cdc_update`); `usage_store.go:488,605,746`; `renderer/lib/event-transport.ts:216-221,352` | 40 SQL + 80 to 150 FE | Low. Payload change is additive; keep the event type (CHECK constraint on `change_log`). |
| DL-3 | P0 | Connect the event stream with `?after=latest` (mobile already does). Today desktop replays from seq 0, up to 100k events on every connect. Server side: marshal once per broadcast, flush once per replay batch. | `event-transport.ts:431`; `httpd/events.go:69-72,172-201` | 1 FE + ~30 BE | None. Reconnect keeps `Last-Event-ID`. |
| DL-4 | P1 | One owner for the workspace poll. About 18 components subscribe with their own `refetchInterval`; only `_shell` should. | `_shell.tsx:227`, `HomePage.tsx:156`, `NotificationCenter.tsx:145`, `RestartToUpdateDialog.tsx:48`, others | 10 to 20 | Low. |
| DL-5 | P1 | Rely on the event stream when connected; keep polling only as a fallback when it drops. Applies to workspace (15 s and 300 ms), settings (15 s), config options (5 s), skills, remote conversations (2 s `setInterval`), agent switches (500 ms). | `useWorkspaceQuery.ts:395`; `useSettings.ts:44`; `useConversation.ts:287,384,1283,1378`; `CenterPane.tsx:448` | 100 to 300 FE, plus BE for any state without an event | Medium. Some state may have no daemon event yet; add events or keep a slow poll. |
| DL-6 | P1 | `_shell` loader uses `ensureQueryData` (or a 10 s staleTime) instead of `fetchQuery({staleTime: 0})`, which today refetches the whole workspace on every navigation and hover preload. | `_shell.tsx:81-85`; `router.tsx:21,30` | ~10 | None. |
| DL-7 | P2 | Weak ETag on `/sessions` and `/projects` from `MAX(change_log.seq)`, recovery revision and a 30 s bucket; return 304. | `internal/httpd` (no ETag support anywhere) | ~60 | None. Additive. |

### 3. Session switching (`SW`)

| ID | Pri | Change | Evidence | LOC | Risk and mitigation |
| --- | --- | --- | --- | --- | --- |
| SW-1 | P0 | Viewing spawns nothing. Remove the 300 ms hover warm-up; stop resuming the provider on open; warm on composer focus or first keystroke, or queue the first send and wake in the background. History is a pure SQLite read. | `Sidebar.tsx:162,2368-2376,2463-2466`; `SessionView.tsx:472-506`; `useCanResumeAgent.ts:17-24`; `controllers/sessions.go:240,1722`; `persistenthost/host.go:293`; `chat/service.go:1191-1214,1876` | ~60 FE + ~40 BE | Medium. Model and config pickers need a cached per-harness catalog while cold; first send pays startup. |
| SW-2 | P0 | Turn chat hibernation on by default. It already honours a 30 s view lease and grace period. Render hibernated chats read-only and wake on composer focus or send. | `service/settings/service.go:144-147`; `daemon.go:1074`; `chat/hibernate.go:14-15,61-71` | 10 to 50 | Medium. First send after hibernation is slower; prewarm on focus. |
| SW-3 | P0 | Stop refetching on switch. Conversation staleTime Infinity with CDC invalidation (60 s safety net), gcTime 30 min; invalidate after `POST /chat-view` only when the server woke the provider; refresh only page 0 on switch-back (reuse `refreshRemoteConversation`). | `SessionView.tsx:1394-1398`; `lib/query-client.ts:6`; `event-transport.ts:211-213,233-236`; `useConversation.ts:322-345` | ~30 | Low. Reconnect already invalidates everything. |
| SW-4 | P1 | Keep the last 3 chat surfaces mounted under React 19 `<Activity mode="hidden">` instead of `key={uiSessionId}` remounts. Hidden activity also pauses their effects and polls. | `SessionView.tsx:1934`; `chat/SessionChatSurface.tsx:581` | ~60 | Low to medium. Bounded memory; check focus and scroll restore. |
| SW-5 | P1 | Load @-mention paths lazily on the first `@` through a path-only endpoint (or the cached manifest). Today every open runs `/workspace/files` (6 to 8 git processes, up to 5,000 file open+reads) twice and sets up a 4,096-entry watcher, torn down on unmount. Keep workspace streams alive about 60 s after unmount. | `SessionChatSurface.tsx:390`; `useConversation.ts:1406-1440`; `workspace_files.go:30,278-345,1884-1935,2367-2380`; `workspacewatch/watcher.go:187-200`; `workspace-file-events.ts:83-91,197-203` | ~90 | Low. Brief loading state on the first `@`. |
| SW-6 | P1 | Extend hover prefetch (already loads conversation page 1 at 100 ms) to models and skills from a per-harness cache, so the composer is complete on arrival. | `Sidebar.tsx:2362-2366` | ~40 | Low. |
| SW-7 | P2 | Slim activity previews on the first conversation page (about 4 KB each) and fetch full `command_output` / `detail_json` on expand. One conversation's 37 activities total 248 KB. | `queries/conversations.sql:1374-1402`; `gen/models.go:253-271` | ~80 | Medium. API shape change; ship as an opt-in query parameter. |

### 4. Chat streaming and rendering (`CH`)

| ID | Pri | Change | Evidence | LOC | Risk and mitigation |
| --- | --- | --- | --- | --- | --- |
| CH-1 | P0 | Daemon: let per-delta lookups use the partial unique indexes by adding `AND provider_item_id <> ''`. Today each token scans the conversation (confirmed with EXPLAIN on a real DB). | `queries/conversations.sql:1059,1064,1068,1173,1328,1332` | ~10 | None. |
| CH-2 | P0 | Daemon: buffer provider deltas in memory per item and flush every ~150 ms or on completion in one transaction. Today each delta is its own transaction with a `GetSession`, an event row, an O(n^2) `text || ?` rewrite and a trigger-inserted `change_log` row (about 70 revisions per message, up to 563). | `chat/controller.go:2973`; `conversation_store.go:2403,2793-2833`; migration 0149 | ~80 | Low. A crash loses at most ~150 ms of deltas; settle writes the full text. |
| CH-3 | P0 | Renderer: patch the conversation cache per item id and revision instead of invalidating every loaded page, and compare rows by `id` + `revision` instead of up to four deep walks per tick. Add `maxPages` to the infinite query. | `event-transport.ts:27,124,233-235`; `useConversation.ts:285,351-357`; `ChatWorkspace.tsx:3107-3108,3170-3179,3217-3224` | 150 to 300 | Medium. Periodic page-0 reconcile catches drift. |
| CH-4 | P0 | Markdown: split into top-level blocks, memoize settled blocks and re-render only the last. Limit `rehype-stream-fade` to the last ~200 characters (today up to 4,000 per-character spans, each animated). Apply reveal updates in `startTransition`. | `ChatTimelineItems.tsx:137,224`; `ChatMarkdown.tsx:199-206`; `lib/rehype-stream-fade.ts:18,60-66`; `styles.css:1897` | 100 to 200 | Low to medium. Golden tests on real transcripts for lists and fences across blocks. |
| CH-5 | P1 | Move the chat scrollbar and minimap into their own component and drive the thumb with `style.transform`; set state only when marker visibility changes. Today every scroll event re-renders the whole Timeline. | `ChatWorkspace.tsx:2672,3374-3441,3577-3596,3795-3812` | ~80 | Low. |
| CH-6 | P1 | Virtualizer: persist measured heights per session (`initialMeasurementsCache`), add `contain: layout style` to rows, disable pointer events while scrolling, and consider per-item rows instead of whole-turn rows. | `ChatWorkspace.tsx:2335,2342,3245,3814-3821` | ~60 | Low. |
| CH-7 | P1 | Memoize `TimelineItem` and `ActivityRun`, compute activities once in `runsOf`, pass each turn only its own disclosure boolean, use stable callbacks. | `ChatWorkspace.tsx:3828,4172-4174,4205-4209,4345`; `ActivityRun.tsx:26` | ~50 | Low. |
| CH-8 | P1 | Motion: `initial={false}` unless the item is new, so virtualized rows do not replay fades; animate transform or grid rows instead of `height: "auto"`. | `ActivityRun.tsx:85-88,142,259,337`; `ChatTimelineItems.tsx:979,1081-1094,1229,1796` | ~40 | Low. |
| CH-9 | P2 | Enable the React Compiler in annotation mode, one area at a time (chat first). | `vite.renderer.config.ts:240` | ~10 + fixes | Medium. Opt-in per file. |

### 5. Terminal and browser panel (`TB`)

| ID | Pri | Change | Evidence | LOC | Risk and mitigation |
| --- | --- | --- | --- | --- | --- |
| TB-1 | P0 | Cap the pty ring by bytes (2 to 4 MB including the partial line) and stop re-copying the partial line on every read. Observed one pty-host at ~700 MB and 36% CPU. Replay on attach becomes bounded. | `backend/internal/adapters/runtime/conpty/ring.go:9,28-42,72-77`; `host.go:475-476` | ~40 | Low. Cut at a newline, ESC or clear-screen boundary; mode restore re-sends state. |
| TB-2 | P0 | Create the browser `WebContentsView` only when the Browser slot is first shown, a preview URL arrives, or an agent runs a browser command. Close idle hidden views beyond about 3, keeping the URL. Today every opened session creates a renderer process and partition. | `useBrowserView.ts:484,1092`; `SessionView.tsx:1298`; `browser-view-host.ts:958,1171,3243` | ~80 | Low. The on-demand path at `:3243` covers agent use. |
| TB-3 | P1 | Ignore window scroll events that do not involve the browser slot, skip `browser:setBounds` when nothing changed, and skip native calls in main when bounds are unchanged. Today every scroll anywhere costs layout reads and two IPC round trips per frame. | `useBrowserView.ts:324,726`; `browser-view-host.ts:857-863,2883-2886` | ~30 | Low. Resize observer and settle timer still reposition. |
| TB-4 | P1 | LRU-evict parked terminals beyond 6 to 8 (never the visible one or one with pending input). Each keeps 5,000 lines of scrollback, parses output, and holds a daemon attach (a tmux client). | `TerminalPane.tsx:310-640,545`; `XtermTerminal.tsx:727`; `tmux/tmux.go:959-1018` | ~50 | Low. Eviction costs a replay on return. |
| TB-5 | P1 | Only copy the WebGL canvas for synchronized output during resize, using `preserveDrawingBuffer`. Today every frame runs a `querySelector` and a full-canvas `drawImage` into a second ~25 MB canvas. | `XtermTerminal.tsx:1287-1310` | ~25 | Low. Verify drag-resize with Claude Code. |
| TB-6 | P1 | Terminal open: send `open` with the measured slot size in parallel with the first fit and skip quiet windows when nothing resized. Timers alone add ~550 ms today. | `XtermTerminal.tsx:1193`; `TerminalPane.tsx:1151,1232`; `useTerminalSession.ts:155-162` | ~40 | Low. Fall back when the cell size is unknown. |
| TB-7 | P1 | One-time versioned purge of the stale root caches (`clearCache`, `clearCodeCaches`), cap the disk cache, and delete unused `Partitions/ao-browser-temporary-*` folders at startup. Reclaims about 430 MB. | `browser-view-host.ts:684`; `main.ts:673` | ~30 | Low. |
| TB-8 | P2 | Register the global `webRequest` failure listeners only while an agent wants browser signals. | `browser-view-host.ts:1332` | ~30 | Low. |
| TB-9 | P2 | Send terminal output as binary WebSocket frames (negotiated, JSON fallback) and detect terminal queries with xterm parser hooks instead of decoding every chunk. | `manager.go:596`; `terminal-mux.ts:36-43`; `XtermTerminal.tsx:1678` | ~120 | Medium. Protocol negotiation. |
| TB-10 | P2 | Lower the WebGL pool to the visible terminal plus 2. Detach the browser debugger domains when a capture ends. | `terminal-webgl-pool.ts:12`; `browser-view-host.ts:5003-5004` | ~10 | Low. ~20 to 40 ms to recreate on switch-back. |

### 6. Idle work and memory (`BG`)

| ID | Pri | Change | Evidence | LOC | Risk and mitigation |
| --- | --- | --- | --- | --- | --- |
| BG-1 | P0 | Stop the main-process side-chat state poll (every 500 ms for the app lifetime, about 7,200 requests an hour). Read on demand and on `before-quit`, or have the daemon push it. | `main.ts:604` | 5 to 30 | Low. Final read on quit and daemon exit. |
| BG-2 | P0 | Poll side chats only while the side-chat panel is visible; skip state updates when nothing changed; drop the 3 s timer when the SSE stream is open. Today every open local chat polls every 2 s and re-renders the chat surface. | `IndependentSideChats.tsx:453,521`; `SessionChatSurface.tsx:393-400` | 20 to 40 | Low. 30 s list poll for the "has work" badge while hidden. |
| BG-3 | P0 | One `useVisibleInterval` helper (pause when hidden, 4x slower when blurred, refetch on return) for every hand-rolled poll; send `setViewActive(false)` while hidden so hibernation can work overnight. | `CenterPane.tsx:448`; `event-transport.ts:149,180`; `useConversation.ts:384`; `workspace-file-events.ts:177`; `SessionView.tsx:1417` | ~40 + ~10 per site | Low. Immediate refetch on focus. |
| BG-4 | P1 | Skip `setStatus` in `useDaemonStatus` when nothing changed. Today the 10 s poll re-renders the whole shell context. | `useDaemonStatus.ts:85`; `_shell.tsx:1200` | 6 | None. |
| BG-5 | P1 | Update status: compare before notifying; poll every 3 s only while downloading or preparing. Today the Sidebar re-renders every 3 s whenever an update is pending. | `useUpdateStatus.ts:30,37-39`; `Sidebar.tsx:633` | 8 | Low. |
| BG-6 | P1 | Cap `diffsByCacheKey` (LRU about 256, or drop older workspace versions). It grows with every agent edit while the review pane is open. | `components/diffs/trailingContext.ts:65` | 8 | Low. Evict only keys not on screen. |
| BG-7 | P1 | Review-diff prefetch only on the latest workspace version, debounced 2 to 5 s, gcTime about 2 min. | `useSessionWorkspaceFiles.ts:505-511,540-612` | 15 | Low. Prefetch on Files tab hover. |
| BG-8 | P2 | Narrow the document-wide MutationObserver in `useBrowserView` to portal roots; replace the sidebar scroller's mutation-driven `scrollHeight` reads with ResizeObserver. | `useBrowserView.ts:682-701`; `Sidebar.tsx:423-433` | ~20 | Low. |
| BG-9 | P2 | Pause infinite CSS animations (status pulse, working shimmer) when the window is blurred; make the shimmer a transform. Replace animated `padding`, `width`, `height`, `max-width`, `gridTemplateRows` transitions with transforms. | `Sidebar.tsx:586,1852-1858`; `SessionsBoardAdapters.tsx:54`; `styles.css:485,649,1868-1915,2838-2852`; `ShellTopbar.tsx:97-104`; `ui/sidebar.tsx:505`; `ChatWorkspace.tsx:1723` | ~60 | Low. Visual check in the app. |
| BG-10 | P2 | Turn the ~30 `body:has(...)` / `#root:has(...)` native-composition rules into a class toggled from JS; remove dead keyframes. | `styles.css:4969-5026,5859-5897,1987-1994` | ~30 | Medium. Check browser-overlay layering. |
| BG-11 | P2 | Virtualize the notification center above ~50 rows, use stable handlers, pre-parse timestamps. Memoize sidebar rows and open one context menu per list on demand. | `NotificationCenter.tsx:666,778,826-828,912`; `Sidebar.tsx:2315,2431-2435` | ~80 | Low. |

### 7. Daemon background work (`BE`)

| ID | Pri | Change | Evidence | LOC | Risk and mitigation |
| --- | --- | --- | --- | --- | --- |
| BE-1 | P1 | `GET /sessions`: batch the per-session cleanup-facts query (N+1), cache artifact listings (today a `WalkDir` per session per call), and add a `?view=summary` slim list for desktop and mobile. | `service/session/service.go:1078,1221,1230-1239,1261`; `sessionartifacts.go:43` | ~150 | Low. Opt-in view keeps the contract. |
| BE-2 | P1 | Preview poller: narrow `ListActiveSessions` (no blob columns, partial index) driven by fsnotify with a 2 s fallback. Today it scans and decodes every session every 250 ms (about 240 scans a minute at idle); the reaper and SCM, activity, artifacts and autoreview loops each do their own full list. | `preview/poller.go:14,104`; `daemon.go:980`; `reaper.go:21` | ~100 | Low. Internal. |
| BE-3 | P1 | Gate pollers on attached clients: when no subscriber is connected, slow SCM to 2 to 5 min and pause preview and artifacts; cache git remotes per project; batch PR reads. | `observe/scm/observer.go:806,2149` | ~100 | Low. Keep a 5 min floor for auto-terminate on merge. |
| BE-4 | P2 | Prune `conversation_provider_events` (17 MB in a week, never deleted) with a retention janitor or by compacting deltas when a message settles. | no DELETE anywhere | ~40 | Low. Shorter debug journal. |
| BE-5 | P2 | Set `revision = revision + 1` inline and drop the extra trigger update. | migration 0144 | ~60 | Low. Many queries to touch. |
| BE-6 | P2 | Gzip responses over 32 KB on the LAN and tunnel listeners only; add an incremental conversation page (`?sinceSequence=`). | `internal/httpd` | ~120 | Low. Additive. |

### 8. Bundle and packaging (`BU`)

| ID | Pri | Change | Evidence | Saving | LOC | Risk |
| --- | --- | --- | --- | --- | --- | --- |
| BU-1 | P0 | Load only the active locale; `import()` the rest. Read the saved locale from localStorage before first render so non-English users never see English. | `renderer/i18n/messages.ts:1-8`; `renderer/i18n/instance.ts` | ~1.3 MB from startup | 40 to 70 | Low |
| BU-2 | P0 | Tray uses a generated tray-only catalog instead of importing all renderer catalogs into `main.js`. | `src/main/tray.ts:4` | −1.32 MB of `main.js` (58%) | ~30 | Low; key parity test |
| BU-3 | P0 | Lazy-load PostHog after first paint, queueing early events. | `renderer/lib/telemetry.ts:1` | 650 KB rendered from startup | 20 to 40 | Low |
| BU-4 | P0 | Split the shell closure: `TerminalCacheProvider` without xterm, `lazy()` `OrchestratorStartingChat`, the settings pane body, `CreateProjectFlow`, and the `GitHubOnboardingNotice` terminal. | `_shell.tsx:1,18,32`; `TerminalPane.tsx:43`; `Sidebar.tsx:138`; `CommandPalette.tsx:47`; `HomePage.tsx:22`; `GitHubOnboardingNotice.tsx:10` | ~2.5 MB rendered from every route | 40 to 60 | Low to medium; idle prefetch |
| BU-5 | P1 | Replace `rehype-github-alerts` (imports all of `@primer/octicons` plus parse5 for 5 icons) with a local plugin. | `MarkdownFileView.tsx:32,55` | −1.06 MB from the session route | ~40 | Low; snapshot test |
| BU-6 | P1 | Curated file-icon map (about 40 extensions, lucide fallback) or lazy-load the full map. | `WorkspaceEntryIcon.tsx:1` | ~500 KB from the session route | ~60 | Low to medium |
| BU-7 | P1 | `lazy()` the CodeMirror `MarkdownEditor` (only shown while editing) and the Files/Review inspector panes (`@pierre/diffs`, `@pierre/trees`, two preact copies). | `FileContentPane.tsx:39,479`; `CloudFileContentPane.tsx:14,103`; `SessionView.tsx:2086-2188` | ~860 KB from the session route | ~25 | Low to medium |
| BU-8 | P1 | `main.js`: `import()` WorkOS, electron-updater and browser-profile-import on first use; fix the ineffective dynamic import at `main.ts:3106`. | `main.ts:3106`; `auto-updater.ts` | ~350 KB of main parse | ~40 | Medium; keep IPC registration order |
| BU-9 | P1 | Daemon build with `-trimpath -ldflags="-s -w"`. | `frontend/scripts/build-daemon.mjs:54` | −12 to −14 MB | 1 | Low |
| BU-10 | P1 | Prune `.map`, `.d.ts`, `.md`, `.ts` from `acp-runtime/node_modules`, or bundle the adapter into one file. | `scripts/build-acp-runtime.mjs` | −19 to −45 MB, ~6,000 fewer files to sign and verify | 15 to 60 | Low (prune) |
| BU-11 | P1 | Exclude `better-sqlite3/deps` and `src` from the asar. | `forge.config.ts` | −10 MB | ~5 | Low |
| BU-12 | P2 | Prune Chromium `.lproj` locales the app does not support, before signing. | packager `afterExtract` | −33 MB | ~20 | Low |
| BU-13 | P2 | Alias `shiki` to `shiki/core` with ~40 languages, 2 themes and the JS engine; drop the unused oniguruma WASM. | 242 language + 65 theme chunks | −8.5 MB asar | ~60 | Medium; plain text for unsupported languages |
| BU-14 | P2 | `annotate-preload`: native `element.animate` instead of motion, load fonts with `FontFace` instead of base64. | `annotate-preload.ts:2-4` | −130 KB per browser navigation | ~30 | Low |
| BU-15 | P2 | Move feature-specific CSS next to its components (index CSS is 355 KB, 166 KB of it hand-written `styles.css`). | `renderer/styles.css` | ~100 KB from startup | varies | Low |
| BU-16 | Separate | Electron 33 to 41. Electron 33 is end of life. Unlocks running the ACP adapter on Electron's Node (`ELECTRON_RUN_AS_NODE`) and dropping the bundled 107 MB Node 22. Likely memory and scheduling gains. | `package.json` | −107 MB app | ~50 + QA | High; own PR, full QA, fuse and entitlement review |

Measured and ruled out: `build.target: 'chrome130'` (byte-identical output),
narrowing Tailwind scanning (identical CSS), production sourcemaps (none
shipped), duplicate libraries in the startup path (none), a V8 snapshot for
main (not worth it after BU-2 and BU-8).

### 9. Correctness fixes found along the way (`FX`)

| ID | Pri | Change | Evidence | LOC |
| --- | --- | --- | --- | --- |
| FX-1 | P0 | The session route crashed with `Cannot read properties of null (reading 'includes')` on any session with an open PR in nightlies before `v0.13.4-nightly.202610060624` (`pr.mergeability.reasons` serialized as `null`). Fixed by #6263 and #6287. Add a test that encodes zero-value response DTOs and fails on `null` arrays. | `controllers/dto.go:1313`; `lib/pr-display.ts` | ~30 |
| FX-2 | P0 | Wrap the inspector and the PR card in error boundaries so one bad field cannot take down the whole session route. | `SessionView.tsx`, `SessionInspector.tsx` | ~20 |

## Implementation order

The work can land in one pass on one branch, committed per workstream so each
commit is reviewable and revertible. Recommended order, because later items
depend on earlier ones:

1. **Phase 0, instrumentation (do first).** `performance.mark` at window
   created, first frame, shell interactive, session switch start and end;
   daemon per-phase boot timings; commit the benchmark scripts under
   `frontend/e2e/performance/desktop/` with a README; a screen-recorded
   foreground launch benchmark. Record the baseline on this checkout.
2. **Phase 1, quick wins (low risk, high value).** SU-1, SU-3, BG-1, BG-4,
   BG-5, DL-3, DL-4, DL-6, CH-1, TB-1, BU-2, BU-9, BU-11, SU-14, FX-1, FX-2.
3. **Phase 2, startup.** SU-2, SU-4, SU-5, BU-1, BU-3, BU-4, SU-7, SU-8,
   SU-9, SU-10.
4. **Phase 3, cached data and no splash.** DL-1, SU-6, DL-2, DL-5.
5. **Phase 4, session switching.** SW-1, SW-2, SW-3, SW-4, SW-5, SW-6,
   BU-5, BU-6, BU-7.
6. **Phase 5, streaming chat.** CH-2, CH-3, CH-4, CH-5, CH-6, CH-7, CH-8.
7. **Phase 6, terminal, browser, idle and memory.** TB-2 to TB-7, BG-2,
   BG-3, BG-6, BG-7, BG-8, BG-9.
8. **Phase 7, daemon background and packaging.** BE-1 to BE-3, BU-8, BU-10,
   BU-12, SU-11, SU-12, SU-13.
9. **Phase 8, P2 cleanup.** Remaining P2 items, CH-9.
10. **Separate PR.** BU-16 (Electron upgrade).

Rough size: phases 1 to 8 are about 4,000 to 6,500 lines including tests,
mostly in the renderer and `main.ts`, with roughly 1,000 to 1,500 in the Go
daemon.

## What could break

Nothing is removed and no API is broken: all daemon payload changes are
additive or opt-in. The real risks, and how each is contained:

- **Showing cached data (DL-1, SU-6).** A session can look "working" for a
  moment when it has finished. Mitigation: a connecting indicator, actions
  disabled on cached rows until the daemon confirms, versioned cache keys.
- **Fewer refetches (DL-2, DL-5, SW-3, CH-3).** A missed event leaves stale
  UI. Mitigation: refetch when a revision gap is seen, full refresh on stream
  reconnect, slow safety-net polls.
- **No spawn on view, hibernation on (SW-1, SW-2).** The first message after
  idle is slower. Mitigation: warm on composer focus; cached per-harness
  model and config catalogs so pickers are never empty.
- **Lazy loading (BU-1, BU-3, BU-4, BU-5 to BU-7).** A short loading state
  the first time a terminal, settings or editor opens. Mitigation: idle
  prefetch in SU-7.
- **Hidden window until first paint (SU-4).** A renderer failure could hide
  the window. Mitigation: 2 to 3 s fallback show.
- **Daemon startup reordering (SU-2, SU-9, SU-10, SU-11).** Ordering
  dependencies. Mitigation: per-phase timings and e2e launch tests.
- **Mobile.** Any change to `statusReadiness`, list shape or events is
  checked against the mobile client before merging.
- **Electron upgrade (BU-16).** Highest risk; kept out of the main pass.

Tests that assert the splash or the immediate window show will change with
SU-4 and SU-6.

## Measurement

Every phase reports before and after on the same machine, three runs minimum,
medians with ranges, raw JSON committed next to the README, in the style of
[chat responsiveness](../performance/chat-responsiveness/README.md).

Scripts used for the baseline (to be moved into the repo in Phase 0):

- `bench.mjs`: launches an installed app with a loopback DevTools port
  (`--disable-backgrounding-occluded-windows`), records cold launch (CDP up,
  FCP, LCP, settled), steady state (heap, DOM, RSS), idle CPU over 30 s,
  reloads (FCP, script bytes), scroll frame stats over the tallest scroller,
  and Cmd+K palette latency.
- `switch-bench.mjs`: session switch latency through hash navigation (first
  mutation, first frame, settled, long tasks), first visit and revisit.
- `api-bench.mjs`: daemon endpoint latency, size, compression and ETag.
- `bundle-report.mjs` / `viz-report.mjs`: startup closure, per-route closures,
  and per-package chunk contents from a manifest and visualizer build.

Additions in Phase 0: a foreground, screen-recorded launch benchmark (click
to first frame with real content); streaming benchmark with a scripted agent
(renderer commit time per chunk, daemon writes per second); a 2-hour soak
(renderer heap, process count, pty-host RSS, idle CPU).

## Appendix: side effects of the baseline run

Opening sessions during benchmarking resumed their agents (SW-1): sessions
`brick-19` to `brick-24` moved from `exited` to `idle`, and their `chat-host`
and ACP runtime processes were still running afterwards. This is the
behaviour SW-1 and SW-2 remove.
