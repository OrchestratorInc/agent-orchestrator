# Live Grid

Live Grid is a project presentation route, `/projects/:projectId/grid` locally and `/host/:hostId/project/:projectId/grid` for paired remote daemons. Open it with **Open Grid**, beside **Open Kanban** in an orchestrator session, or from the project board. The board's existing task behavior is unchanged.

## Sessions and lifecycle

- Cell `[0, 0]` belongs to the project's orchestrator. A missing orchestrator leaves a reconnect placeholder; opening Grid never spawns one.
- Workers come from the same workspace query and CDC event transport as Kanban. Creation appends workers in stable creation order; streamed status updates do not sort panels.
- Archived, merged and terminated workers are hidden by default. **Show completed** reveals them. An unknown activity or connection failure does not count as completion.
- Chat uses the existing `SessionChatSurface` / `ChatWorkspace`, including reasoning, activities, history, approvals and its composer. TUI uses the existing `TerminalPane`, xterm and terminal mux. Grid never mounts `SessionView`'s automatic resume-on-open flow.
- Retained portals move the same Chat and terminal DOM between Grid and the session view. Parking preserves buffers, scroll and composer state. A hidden pane is inert and cannot consume input or steal focus. Authoritative session deletion, interface changes and existing terminal generation changes dispose the appropriate resources.
- Restoration is an explicit action through AO's existing restore controls. Viewing, resizing, ordering or changing routes does not call spawn, resume, kill or interface-transition APIs.

## Layout and controls

The layout enumerates readable column counts for the measured workspace, then minimizes a score combining cell aspect ratio (target 1.35), unused cells and required scrolling. Cells have minimum dimensions of **360 × 280 CSS pixels**, with a **360 pixel minimum height when Chat is present** to leave room for its transcript and composer. When all rows cannot fit, the workspace scrolls instead of compressing terminals. A narrow workspace may also scroll horizontally.

The orchestrator occupies the first cell in row order. Workers have stable keys and remain sibling components when the shape changes. Column and row dividers resize adjacent tracks while preserving their combined size and minimum dimensions. Window resizing redistributes saved proportions with a water-filling algorithm, clamping each track to its minimum.

Drag a worker's grip to another worker, or use its previous/next buttons. Double-click a header or click its expand button to maximize; restore returns to the saved grid. Divider arrow keys resize by 10 pixels, or 50 with Shift, when the divider itself has focus. Terminal keyboard shortcuts continue through xterm, including font zoom and its existing fullscreen action.

Order, the selected orchestrator, completed visibility and split proportions are saved per host and project in Electron local storage (`ao.live-grid.v1.*`). Splits are remembered separately for each grid shape. No database migration is required.

All local terminals share AO's existing mux socket. Remote terminals share one mux socket per connected host through AO's existing authenticated loopback proxy. Remote snapshots, conversations, restore controls and asset URLs keep the owning host; equal session IDs on different hosts have separate caches. Failed/offline snapshots preserve panels and buffers. A replacement proxy reconnects the retained terminals. Conversation fallback polling is shared between observers of each remote session. The WebGL pool remains capped at eight accelerated terminals; additional panes use xterm's interactive DOM renderer. Terminal fitting uses the existing resize coalescing. Chat conversations share the existing query cache and CDC connection.

Embedded Chat file-completion inventories refresh every 30 seconds while visible, avoiding one persistent workspace-file SSE connection per panel (Chromium's HTTP/1 per-origin connection limit). Single-session file watching keeps its existing event subscription. Conversation output still streams through the shared CDC transport. Grid does not acquire wake leases for hibernated Chat sessions; existing human input wakes them through AO's ordinary command path.

## Separate macOS lab build

Build from an isolated checkout with its own dependencies:

```sh
cd frontend
npm ci
npm run build:daemon
npm run build:tmux
npm run browser-runtime:prepare
npm run build:acp-runtime
AO_BUILD_VARIANT=live-grid-lab npx electron-forge package --platform=darwin --arch=arm64
```

For a local unsigned lab bundle, seal it with ad hoc signing while preserving the bundled executables' entitlements:

```sh
codesign --force --deep --sign - --timestamp=none --preserve-metadata=entitlements,flags '/path/to/Agent Orchestrator Live Grid.app'
bash scripts/verify-mac-artifact.sh '/path/to/Agent Orchestrator Live Grid.app'
```

The canonical verifier checks the seal and ACP Node entitlements. An ad hoc build has no Apple notarization ticket, so its stapler check is expected to fail. Production signing/notarization belongs to the normal release conductor.

The bundle is named **Agent Orchestrator Live Grid.app**, with bundle ID `dev.agent-orchestrator.live-grid-lab`. It forces a separate data directory, run file, Electron profile and tmux socket, even if ordinary AO environment variables are inherited. Its default root is `~/.ao/live-grid-lab`, and its port is 32191. `AO_LIVE_GRID_LAB_ROOT` and `AO_LIVE_GRID_LAB_PORT` can select another isolated root and unused port.

This build disables updates, automatic relocation to Applications, stale-copy retirement and registration of the production deep-link protocol. Cloud state and the Browser runtime also use its isolated root. It does not attach to installed AO sessions. Open a disposable project in it; keep commercial projects in the installed version.

The lab rejects production/development state directories and symlink escapes before creating state. Its tmux socket includes a stable hash of the lab root, separating independent lab profiles.

For the full native fixture check, run from the lab checkout:

```sh
AO_GRID_APP_PATH='/absolute/path/Agent Orchestrator Live Grid.app' node e2e/live-grid-desktop.mjs
# Repeat with AO_GRID_SESSION_MODE=tui or chat for twelve panels of one type.
```

The script creates a temporary Git project and a deterministic test provider, then uses the packaged preload, real daemon, SQLite, PTY and ACP runtimes. It rejects an occupied port and verifies the fixture provider before creating sessions. Cleanup addresses only its own recorded session IDs in its isolated daemon. Screenshots, provider PID evidence and a validation report remain in the reported temporary directory.

## Rollout

The lab bundle is for parallel evaluation and does not migrate production data. Do not copy a live AO database into another running daemon, replace the installed app, or point the lab at commercial workspaces. A production transition should use a reviewed, signed release containing this branch, during a planned update window with an AO data backup and the normal session restore procedure. This implementation changes presentation and requires no session or model configuration migration.

Grid supports local and paired remote-host projects using API contract version 1, without a daemon upgrade or new orchestration API. Cloud control-plane project Grid remains outside this implementation. Platform packaging and notarization need the project's normal release infrastructure.

## Ordinary desktop build and isolated verification

The ordinary packaged app retains its name, bundle ID, data paths and remote credentials. Before installation, the exact bundle can be exercised with `AO_DESKTOP_TEST_ROOT=/absolute/scratch/profile` and an unused `AO_DESKTOP_TEST_PORT`. This forces separate data, Chromium, run-file, Browser, cloud state and tmux ownership; it disables updates, relocation and production deep-link registration for that run. A normal Finder launch without these overrides uses the existing `~/.ao` installation. The profile guard rejects production paths and credential symlink escapes.

`live-grid-desktop.mjs` accepts the ordinary `Agent Orchestrator.app` as well as the optional lab bundle, and always supplies a disposable test root. It records native process memory/CPU at 1, 2, 4, 6, 9 and 12 sessions.

For a locally authorized update, quit only the local desktop app, verify its managed local daemon has released the database, and preserve the old app plus all durable `~/.ao` data, pairing/auth settings and Electron profile before replacement. Check SQLite compatibility on a backup snapshot first. Remote proxies close client connections only; they do not stop the remote daemon or its sessions. View-only verification of commercial remote projects must not send prompts or invoke restore, kill, mode-transition or workspace operations. Restore the old app and pre-update local data together for rollback; do not roll back server data.
