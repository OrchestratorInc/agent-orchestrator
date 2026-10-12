# Live Grid: remote desktop validation

Validated on macOS ARM64 (Apple M1 Pro, 16 GB RAM). The ordinary desktop bundle uses the existing AO name, bundle ID and profile. No backend orchestration API or remote daemon update is introduced.

## Remote integration

- Host-qualified Grid routes and Open Grid actions reuse AO's saved pairing, authenticated loopback proxy, workspace queries and per-host CDC connection.
- One pooled terminal mux per host, with separate session ownership and reset keys. Remote failures preserve retained terminals and Chat drafts; a replacement proxy reconnects them.
- Input and assets use the session's owning host. Chat remains Chat, TUI remains TUI. Shared conversation fallback polling avoids duplicate header/surface requests.
- Review found and fixed composer selection loss caused by re-appending an already visible portal, minimap ticks disappearing after inspector updates, and non-atomic encrypted auth writes observed by concurrent surfaces. Grid disables outer scroll anchoring during layout changes.

## Verification

| Check | Result |
| --- | --- |
| Complete frontend Vitest suite | 454 files; 7450 passed, 7 skipped |
| Frontend and E2E TypeScript | Passed |
| Focused remote Electron renderer cases | 8 passed: 1/2/4/6/9/12 TUI, mixed Chat/TUI input/CDC, reconnect |
| Complete browser renderer smoke | 92/93 passed; Chromium 148 pointer-selection case also fails with uncached Chat |
| Same composer cases in shipped Electron | 2 passed |
| Real packaged daemon/preload/SQLite/PTY/ACP | Mixed, TUI and Chat fixtures; 1/2/4/6/9/12 panels, input, resize, reconnect, layout and unchanged fixture provider PIDs |
| Installed ordinary app with existing paired remote | Sign-in and connection survived a full client restart; 15 panels, all eligible workers, every displayed TUI visited, live worker output, Grid/Kanban, unchanged remote daemon and terminal generations across the update checks |
| Existing local database snapshot | Migrations and integrity passed; all projects/session records retained |
| Go build/vet and targeted race suites | Passed: HTTP/API/controllers, project service, process environment |
| Product UI / Cloud client | Typechecks, generation and 157 / 29 tests passed |
| Browser action integration / macOS helper tests | Passed / 13 tests passed |
| Pinned Node 22.23.2 lockfile dry run | Passed |
| Pinned gitleaks v7.4.0 | No leaks in branch commits |

The browser pointer-selection failure is reproduced without the retained Chat wrapper and passes in Electron's Chromium. It is documented as a browser test gap, not reported as passed. Full backend/cloud deployment suites and native Windows/Linux release gates are not locally claimed. Remote CI results require separate verification; fork workflows may await a maintainer's approval.

## Performance

With 6/9/12 real test TUI sessions, Electron renderer working set was approximately 191/201/221 MiB, CPU approximately 4.5/4.9/5.8 percent during the measured live fixture. All 12 used one mux connection. Real remote mixed sessions used approximately 354 MiB renderer working set in the initial 15-panel measurement. These are fixture/snapshot observations, not an exhaustive sustained-load benchmark. WebGL remains capped at eight renderers; other terminals retain the interactive DOM renderer.

## Packaging and rollback

Custom version: `0.13.7+live.grid.2`, source built from `af2408e91` with upstream `95dd4d712` merged. Later documentation-only commits do not change the installed executable code. The build uses the installed release's public WorkOS client configuration; the source default differs from the official production artifact and cannot substitute for that configuration when preserving sign-in.

The macOS bundle has a valid ad hoc code-signing seal and verified ARM64 ACP Node entitlements. It has no Developer ID notarization ticket; the canonical verifier's stapler check fails as expected. No macOS security setting was changed. This local build is not an official notarized release.

A complete backup of the old app, checkpointed local database, Electron profile and durable AO settings was made before replacement. Caches, downloaded runtimes, sockets and prior lab state are excluded. SQLite integrity and byte equality to the closed source were checked. The installed app uses the existing `~/.ao` profile; pairing and update settings remain unchanged. The custom updater points to the fork, so official updates cannot silently replace Grid; future custom builds require an explicit update.

Quit AO completely before rollback. Preserve the current app/data, restore the previous app, and restore the matching pre-update local data if needed. Never delete or roll back remote data, or stop the remote daemon. Rotated OAuth tokens may require ordinary sign-in after a data rollback.

## Changed files

- `frontend/docs/live-grid-validation.md`
- `frontend/docs/live-grid.md`
- `frontend/e2e/live-grid-desktop.mjs`
- `frontend/e2e/live-grid-remote-desktop.mjs`
- `frontend/e2e/live-grid-remote.spec.ts`
- `frontend/e2e/live-grid.spec.ts`
- `frontend/e2e/support/fake-bridge.ts`
- `frontend/e2e/support/live-grid-agent.cjs`
- `frontend/forge.config.ts`
- `frontend/src/main.ts`
- `frontend/src/main/cloud-auth.test.ts`
- `frontend/src/main/cloud-auth.ts`
- `frontend/src/main/live-grid-lab.test.ts`
- `frontend/src/main/live-grid-lab.ts`
- `frontend/src/renderer/components/CenterPane.tsx`
- `frontend/src/renderer/components/LiveGrid.remote.test.tsx`
- `frontend/src/renderer/components/LiveGrid.tsx`
- `frontend/src/renderer/components/OpenGridButton.tsx`
- `frontend/src/renderer/components/SessionTopbarPortal.tsx`
- `frontend/src/renderer/components/SessionView.tsx`
- `frontend/src/renderer/components/SessionsBoard.tsx`
- `frontend/src/renderer/components/ShellTopbar.tsx`
- `frontend/src/renderer/components/TerminalPane.remote.test.tsx`
- `frontend/src/renderer/components/TerminalPane.test.tsx`
- `frontend/src/renderer/components/TerminalPane.tsx`
- `frontend/src/renderer/components/chat/ChatComposer.test.tsx`
- `frontend/src/renderer/components/chat/ChatComposer.tsx`
- `frontend/src/renderer/components/chat/ChatWorkspace.test.tsx`
- `frontend/src/renderer/components/chat/ChatWorkspace.tsx`
- `frontend/src/renderer/components/chat/SessionChatCache.test.tsx`
- `frontend/src/renderer/components/chat/SessionChatCache.tsx`
- `frontend/src/renderer/components/chat/SessionChatSurface.tsx`
- `frontend/src/renderer/components/settings/HarnessSettingsSection.test.tsx`
- `frontend/src/renderer/components/settings/HarnessSettingsSection.tsx`
- `frontend/src/renderer/hooks/useConversation.remote-poll.test.tsx`
- `frontend/src/renderer/hooks/useConversation.test.tsx`
- `frontend/src/renderer/hooks/useConversation.ts`
- `frontend/src/renderer/hooks/useWorkspaceQuery.ts`
- `frontend/src/renderer/i18n/de.json`
- `frontend/src/renderer/i18n/en.json`
- `frontend/src/renderer/i18n/es.json`
- `frontend/src/renderer/i18n/fr.json`
- `frontend/src/renderer/i18n/ja.json`
- `frontend/src/renderer/i18n/ko.json`
- `frontend/src/renderer/i18n/pt-BR.json`
- `frontend/src/renderer/i18n/zh-CN.json`
- `frontend/src/renderer/lib/live-grid-layout.test.ts`
- `frontend/src/renderer/lib/live-grid-layout.ts`
- `frontend/src/renderer/lib/session-viewport.tsx`
- `frontend/src/renderer/lib/terminal-webgl-pool.test.ts`
- `frontend/src/renderer/lib/terminal-webgl-pool.ts`
- `frontend/src/renderer/routeTree.gen.ts`
- `frontend/src/renderer/routes/_shell.host.$hostId.project.$projectId_.grid.tsx`
- `frontend/src/renderer/routes/_shell.projects.$projectId_.grid.tsx`
- `frontend/src/renderer/routes/_shell.tsx`
- `frontend/src/renderer/test/shell-new-session-shortcut.test.tsx`
- `frontend/vite.main.config.ts`
- `frontend/docs/live-grid-remote-validation.md`
