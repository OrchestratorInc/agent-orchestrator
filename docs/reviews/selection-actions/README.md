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
