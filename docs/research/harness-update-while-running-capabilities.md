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

## Levels shipped behind `AO_UPDATE_WHILE_RUNNING`

| Level | Methods | Why |
|---|---|---|
| safe | Claude Code, Cursor, Grok vendor installers; Homebrew formulas | The running release stays on disk (formulas: cleanup deferred while sessions run) |
| warn | Homebrew casks; other vendor self-updaters | The running release is removed or overwritten; a session may fail on its next lazy load |
| blocked | npm, Bun, uv, pipx, winget; every method on Windows | Shared trees are rewritten, and Windows cannot replace a running executable |

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
- **Terminal (Phase 3A, `AO_TUI_VERSION_REFRESH`, off by default)** restarts
  only harnesses listed in `tuiVersionRefreshHarnesses`
  (`backend/internal/session_manager/tui_version_refresh.go`): Claude Code and
  Codex for now. Add a harness only after 0.1 and 0.2 pass for it.
- **Windows (Phase 3B, `AO_WINDOWS_UPDATE_WHEN_IDLE`, off by default)** waits
  for idle sessions, stops them, updates and resumes them. Needs 0.7 and a
  Windows QA pass before the default changes.

## Flags

| Flag | Default | Effect |
|---|---|---|
| `AO_UPDATE_WHILE_RUNNING` | on | Safe/warn updates may run alongside sessions after confirmation |
| `AO_VERSION_REFRESH` | on | Kill switch for moving idle sessions to a new release (Settings toggle applies on top) |
| `AO_VERSION_REFRESH_IDLE` | 10m | Quiet time before a session moves |
| `AO_TUI_VERSION_REFRESH` | off | Also restart idle terminal sessions |
| `AO_WINDOWS_UPDATE_WHEN_IDLE` | off | Windows updates wait for idle sessions instead of refusing |
