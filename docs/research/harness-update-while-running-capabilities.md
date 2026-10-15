# Updating harnesses while sessions run: capability table (Step 0)

Status: **partially validated**. The update-while-running levels in
`backend/internal/service/systeminstall/update_gate.go` and the version-refresh
eligibility in Phases 2 and 3 code against this table. Rows marked *assumed*
still need the manual spike before their harness leaves beta.

## Install layouts observed on macOS (2026-10-15)

Read-only inspection of one developer machine. A layout that keeps every
release in its own path leaves the running release on disk after an update.

| Harness | Method | Resolved binary | Keeps previous release? |
|---|---|---|---|
| Claude Code | vendor installer | `~/.local/share/claude/versions/2.1.300` (link from `~/.local/bin/claude`) | Yes: `2.1.299` still present |
| Cursor | vendor installer | `~/.local/share/cursor-agent/versions/<date-sha>/cursor-agent` | Yes: two versions present |
| Grok | vendor installer | `~/.grok/bin/grok-1.0.52` (link) | Unknown: only one release present (0.5) |
| Codex | Homebrew cask | `/opt/homebrew/Caskroom/codex/0.162.1/bin/codex` | No: `brew upgrade --cask` removes the old folder (0.4) |
| OMP | Homebrew formula | `/opt/homebrew/Cellar/omp/18.11.0/bin/omp` | Only with `HOMEBREW_NO_INSTALL_CLEANUP=1` (0.6) |
| Kimi | Homebrew formula | `/opt/homebrew/Cellar/kimi-code/2.1.1/...` | Only with `HOMEBREW_NO_INSTALL_CLEANUP=1` (0.6) |
| OpenCode | vendor installer | `~/.opencode/bin/opencode` (single file) | No: replaced in place |
| Auggie | npm | `<nvm prefix>/lib/node_modules/@augmentcode/auggie/` | No: npm rewrites the package tree |

## How an update meets running sessions

Like T3 Code, AO installs the update while sessions run and never touches a
running process: each keeps the release it loaded. Sessions still on the old
release are marked, and restart on the new one once idle (chats and
terminals, at most four at a time, the way Account Manager moves sessions in
#6274). While the installer runs, no session of that harness starts: a launch
waits for the update (up to 45s) instead of failing.

| Level | Methods | Running sessions during the update |
|---|---|---|
| safe | Claude Code, Cursor, Grok vendor installs; Homebrew formulas | Unaffected: the running release stays on disk (formulas: cleanup deferred) |
| warn | Homebrew casks; other self-updaters; npm, Bun, uv, pipx | May fail on their next lazy load until they restart; they restart once idle |
| idle | Every method on Windows | The update waits until they are idle, stops them, installs and resumes them |
| blocked | Only with `AO_UPDATE_WHILE_RUNNING=off` (or Windows with update-when-idle off) | The update waits for them to end |

Grok is listed as safe on the assumption that `grok update` keeps `grok-<v>`;
move it to warn if 0.5 shows otherwise.

## Open tests

| # | Test | Decides | Result |
|---|---|---|---|
| 0.1 | Resume restores model, effort, permission mode, MCP servers and cwd | Which harnesses may auto-switch (P2/P3) | Pending |
| 0.2 | New version resumes a conversation from the old version | Same | Pending |
| 0.3 | Old and new versions share `~/.codex/*.sqlite`, `~/.claude.json` safely | Whether long mixed-version periods are safe | Pending |
| 0.4 | Codex cask: new tool call in an old session after the old folder is purged | Codex level | Pending; warn until shown safe |
| 0.5 | `grok update` keeps the old `grok-<v>` | Grok level | Pending |
| 0.6 | Formula upgrade with `HOMEBREW_NO_INSTALL_CLEANUP=1` keeps the old keg | Formula level | Homebrew documents this; confirm on a real upgrade |
| 0.7 | Windows in-use behaviour per installer | Windows design (P3) | Pending |

## Where each result is used

- **Chat (Phase 2)** moves sessions only through the existing hibernate and
  native-resume path, so it relies on the same resume capability idle
  hibernation already uses. 0.2 (new version resumes an old conversation) is
  the remaining risk; `AO_VERSION_REFRESH=off` turns it off.
- **Terminal (Phase 3A, `AO_TUI_VERSION_REFRESH`)** restarts
  only harnesses listed in `tuiVersionRefreshHarnesses`
  (`backend/internal/session_manager/tui_version_refresh.go`): Claude Code and
  Codex for now. Add a harness only after 0.1 and 0.2 pass for it.
- **Windows (Phase 3B, `AO_WINDOWS_UPDATE_WHEN_IDLE`)** waits for idle
  sessions, stops them, updates and resumes them. 0.7 and a Windows QA pass
  are still owed.

## Managed versioned installs (npm, Bun, uv, pipx)

These package managers rewrite a package's directory in place, so a session
still running the old release could fail on its next lazy load (a JS chunk, a
bundled helper such as ripgrep, a Python module), and on Windows the rewrite
fails while the release runs. AO therefore installs each update of these
methods into a fresh directory of its own and switches to it atomically, the
way Claude Code's native installer keeps each release apart:

```
~/.ao/data/harnesses/<agent>/versions/v12/   one release, never modified after install
~/.ao/data/harnesses/<agent>/current          → versions/v12 (symlink; junction on Windows)
~/.ao/data/harnesses/<agent>/managed.json     method, package, CLI, version
~/.ao/data/harnesses/bin/<cli>                launcher → ../<agent>/current/<bin>
```

1. Install into the next `versions/vN` (npm `--prefix`, `BUN_INSTALL`,
   `UV_TOOL_DIR`/`UV_TOOL_BIN_DIR`, `PIPX_HOME`/`PIPX_BIN_DIR`).
2. Run the new copy's `--version`; on failure delete the directory, nothing changes.
3. Switch `current` (atomic rename on Unix; junction swap under the harness lock on Windows).
4. Harness resolvers check `~/.ao/data/harnesses/bin` first, so new sessions start the new release.
5. Sessions on the old release are marked stale and restart once idle.
6. An old release is deleted once no live session of that harness is stale (retried while Windows holds it).

These updates are `safe` on every platform, Windows included. The user's own
global install (their terminal's `claude`) is left untouched; login and config
are shared. `AO_MANAGED_HARNESS_INSTALLS=off` updates in place instead.

## Flags

| Flag | Default | Effect |
|---|---|---|
| `AO_UPDATE_WHILE_RUNNING` | on | Updates install while sessions run, without asking |
| `AO_VERSION_REFRESH` | on | Kill switch for moving idle sessions to a new release (Settings toggle applies on top) |
| `AO_VERSION_REFRESH_IDLE` | 10m | Quiet time before a session moves |
| `AO_TUI_VERSION_REFRESH` | on | Also restart idle terminal sessions |
| `AO_WINDOWS_UPDATE_WHEN_IDLE` | on | Windows updates wait for idle sessions instead of refusing |
| `AO_MANAGED_HARNESS_INSTALLS` | on | npm, Bun, uv and pipx updates install side by side under the AO data dir |
