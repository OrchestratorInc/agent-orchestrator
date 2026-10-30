# Selection action review evidence

Base: `c7a99e1ad83c0ea9bfc84e35f679fa2a08e6169f` (`main`).

The screenshots use seeded, throwaway content in the actual Electron renderer,
with a mocked bridge and HTTP transport. The baseline has its own checkout and
its own `npm ci`; dependencies and optimizer caches are not shared. Profiles are
under `~/.ao/dev/renderer-fixtures`. No real AO projects or provider chats are used.

| Surface | Before (`main`) | After |
| --- | --- | --- |
| Chat: selection taller than the pane, scrolled within the selection | ![Chat before](chat-before.png) | ![Chat after](chat-after.png) |
| File: lines 1–80 selected, scrolled within the selection | ![File before](file-before.png) | ![File after](file-after.png) |

The chat toolbar stays inside the text pane. The file toolbar also excludes the
sticky filename header. Both disappear when the complete selection leaves view.
The side-chat action keeps its “Add to side chat” label and remains disabled until
capabilities arrive. The original screenshots below predate that label adjustment.

## Verification

- 237 focused component/hook/geometry/localization tests pass across 11 suites.
- All 12 Electron selection fixtures pass: capture/send, keyboard shortcut,
  unified/split diffs, large chat/file ranges, scrolling, dismissal, row replacement,
  feedback controls, and absence of actions on a TUI-only surface. The additional
  zoom/growth run passes with native 125% zoom and appended transcript content.
- The large chat fixture observes the toolbar within two animation frames after
  selection completion (about 17–23 ms in recorded local runs). This is a local
  observation, not a universal latency guarantee or a full-app benchmark.
- A burst of 100 scroll events produces one chat geometry read and no text reads.
  Code captures retain the same object, perform no row queries, and notify no
  capture subscribers during scrolling. Positioning does not call the backend.
- Production renderer build and frontend/E2E typechecks pass. API, SQL, and cloud
  schema regeneration introduce no content drift. Cloud-client and product-UI
  tests and docs tests/build pass.

The complete Windows suites have platform failures; consult the PR validation
notes for final counts. Do not infer a complete local pass from focused results.
The full Electron Forge app was attempted with separate scratch data/run/profile
paths under `~/.ao/dev/selection-actions-ux`; the `better-sqlite3` native rebuild
failed before launch. Renderer fixtures do not establish full-app or real-provider
verification. Native platform coverage and final-head checks are reported in the PR.

## Follow-up phases

- Phase 0: merged upstream main `dde71cf580cda8aec761eb2d64310b290c70196c` in `1f58b0f78`; 60 focused tests and both typechecks pass.
- Phase 1: retain native selected content across cache completion and unrelated renders, detect shadow-root selections when suspending refresh, remember dismissed chat selections, and consume the first Escape before the stop shortcut (including held-key repeats). The side-chat label stays unchanged while disabled.
- New report (human observation): selecting file text shows a toolbar that automatically closes after some time; whether its highlight also disappears is unknown.
- Agent-collected evidence: shadow-aware selection, deferred refresh completion, explicit navigation, and renderer-prop retention regressions pass. This isolates plausible failure paths; it does not establish the cause in the reporter’s installed app.
- Phase 1 component run: 297 tests pass and one unrelated Windows locale assertion fails (`2,58,400` versus `258,400`). Narrow follow-up: 26 tests pass; frontend and E2E typechecks pass. Native verification is recorded separately when available.

- Phase 2 (historical): selection operations originally froze their creation receipt and destination across retries. This was superseded by the clarified activation semantics below.

## Current activation semantics

Each deliberate Add to side chat click follows the currently active side chat.
Its destination and creation identity are frozen only while that activation is
processing, so repeated activation cannot issue duplicate requests. Success or
failure releases the operation; a later click starts a fresh action, including
after switching side tabs. No automatic creation retries are added. A user click
after an uncertain creation reply may deliberately create another destination;
the app does not interpret that click as an internal retry. Add still attaches to
an existing active chat; New side chat still permits intentional new chats from
the same text or parent state. Disposed surfaces reject stale completion.

This supersedes the original reviewer request to preserve selection operation
identity and destination across failed clicks. Durable command handoffs from
#7088 retain their existing explicit retry/cancel behavior. Toolbar anchoring,
Ctrl+L in chat, and command consolidation are reserved for separate PRs.

- Renderer follow-up: Edge reproduced loss of selection on window focus: ChatComposer refocused its editor, clearing native file/diff selection. Passive focus and visibility restoration now respect document and shadow-root selections. Both new idle/cache/focus scenarios pass after this fix; 208 composer/side-delivery tests pass. This is a confirmed fixture failure path, not a diagnosis of the installed app.
- Complete Node 24 Windows frontend run before the focus follow-up: 573 files passed / 32 failed; 9,555 tests passed / 216 failed / 14 skipped. Broad failures include paths, Unix permissions, native dependencies and locale formatting; not all were independently reproduced on main. No complete local pass is claimed. Shared cloud-client (31) and product-ui (161) tests/builds pass, cloud schema has no content drift, renderer build and both typechecks pass. Docs build passes; docs tests have an alias-resolution suite failure and translation validation reports six untouched markers. Native launch was attempted but Go is unavailable and Electron download timed out.

## Final activation review — 30 October 2026

- An independent subagent reviewed the owner/Ronish comments and final reliability implementation twice with no actionable source findings. The owner explicitly superseded cross-click destination/key persistence.
- Focused selection/viewer suites: 327 tests pass; one existing Windows locale assertion fails (`2,58,400` versus `258,400`). New destination and pending-activation regressions pass. Both frontend/E2E typechecks and the production renderer build pass.
- Complete Windows frontend run: 574 files pass / 31 fail; 9,846 tests pass / 216 fail / 14 skip. Platform, native dependency, locale, and timing failures remain; not all were independently reproduced on main. Two timing-sensitive suites pass their full isolated rerun (65 tests). No clean full local pass is claimed.
- Windows Electron renderer fixtures: two complete runs each pass 21/22, with different timeouts (initial inspector startup; selection toolbar before annotation navigation). The annotation scenario passed the first complete run and all three isolated repetitions afterward. The exact timeout cause remains unconfirmed; this is not a clean complete native pass. These fixtures mock bridge/HTTP and do not verify a live provider.
- Cloud-client 31 tests and product-ui 161 tests, typechecks, and builds pass. Docs build and catalog compilation pass; docs tests retain an alias-resolution failure and translation validation reports six untouched markers.
- Existing dependencies were reused without installation. Full-app/provider recording and native macOS/Linux manual checks remain gaps; verify the current-head remote jobs separately.
