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

## Current activation and recovery semantics

Each Add to side chat activation reads the currently active destination. Matching
activations share one pending action; failures release that processing lock.
Unanswered automatic creation requests separately retain their random request key
and original excerpt payload. A reselected highlight matches by session,
conversation, message, revision and text, rather than its temporary UI ID.

If a side is active on the next click, attach there. If none is active, retry a
matching unanswered creation using its original key. A successful creation clears
that receipt. A later completed action is never permanently reserved by text or
position. New side chat continues to use a fresh key and force-new semantics.
No automatic retries or new button text are introduced.

Late creation results may retain their original reference in the original side,
without switching tabs or changing composer focus after the user selects another
destination. Source replacement, disposal and closed/closing destinations reject
stale results. Inactive recovered drafts are loaded and merged before saving,
preserving existing and simultaneously queued edits.

The daemon also remembers request aliases when anchor reuse returns an existing
side created under a different key. This prevents a lost response followed by an
advanced parent from creating a second destination. Aliases are session/launch
scoped; a closed destination rejects its old keys.

Recovery is in memory only: the session surface and daemon process must remain
alive. Renderer reload/disposal clears client receipts. Supervised daemon restart
can lose alias-only keys; primary keys retain the existing desktop recovery
behavior. This change does not extend the recovery export or saved draft format.

These semantics supersede the 30 October behavior that gave every post-failure
creation a fresh key, while preserving the owner's requirement that each new Add
click follows the current destination. #7088 command handoff retry/cancel behavior
is unchanged. Toolbar positioning/shortcuts, command consolidation and the newly
reported code-block excerpt validation issue remain separate work.

- Renderer follow-up: Edge reproduced loss of selection on window focus: ChatComposer refocused its editor, clearing native file/diff selection. Passive focus and visibility restoration now respect document and shadow-root selections. Both new idle/cache/focus scenarios pass after this fix; 208 composer/side-delivery tests pass. This is a confirmed fixture failure path, not a diagnosis of the installed app.
- Complete Node 24 Windows frontend run before the focus follow-up: 573 files passed / 32 failed; 9,555 tests passed / 216 failed / 14 skipped. Broad failures include paths, Unix permissions, native dependencies and locale formatting; not all were independently reproduced on main. No complete local pass is claimed. Shared cloud-client (31) and product-ui (161) tests/builds pass, cloud schema has no content drift, renderer build and both typechecks pass. Docs build passes; docs tests have an alias-resolution suite failure and translation validation reports six untouched markers. Native launch was attempted but Go is unavailable and Electron download timed out.

## Historical activation review — 30 October 2026

- An independent subagent reviewed the owner/Ronish comments and final reliability implementation twice with no actionable source findings. The owner explicitly superseded cross-click destination/key persistence.
- Focused selection/viewer suites: 327 tests pass; one existing Windows locale assertion fails (`2,58,400` versus `258,400`). New destination and pending-activation regressions pass. Both frontend/E2E typechecks and the production renderer build pass.
- Complete Windows frontend run: 574 files pass / 31 fail; 9,846 tests pass / 216 fail / 14 skip. Platform, native dependency, locale, and timing failures remain; not all were independently reproduced on main. Two timing-sensitive suites pass their full isolated rerun (65 tests). No clean full local pass is claimed.
- Windows Electron renderer fixtures: two complete runs each pass 21/22, with different timeouts (initial inspector startup; selection toolbar before annotation navigation). The annotation scenario passed the first complete run and all three isolated repetitions afterward. The exact timeout cause remains unconfirmed; this is not a clean complete native pass. These fixtures mock bridge/HTTP and do not verify a live provider.
- Cloud-client 31 tests and product-ui 161 tests, typechecks, and builds pass. Docs build and catalog compilation pass; docs tests retain an alias-resolution failure and translation validation reports six untouched markers.
- Existing dependencies were reused without installation. Full-app/provider recording and native macOS/Linux manual checks remain gaps; verify the current-head remote jobs separately.

## Creation recovery validation — 31 October 2026

- Independent subagent found and re-reviewed fixes for reused-anchor key aliases,
  focus changes through reference insertion, stale same-session source completion,
  and a queued draft-edit overwrite. Final source review: no actionable findings
  within the documented same-surface/same-daemon-process boundary.
- Four complete recovery/component suites pass: 145 tests. The new creation suite
  includes a rendered composer focus check and same-batch draft edit regression.
- Backend side/fork suite: 89 top-level tests pass. The new reused-anchor/advanced-
  parent regression fails against the previous implementation and passes here.
- Broader selection/composer suites: 418 pass, one known Windows number-format
  assertion fails (2,58,400 versus 258,400). Both typechecks and renderer build pass.
- Complete Windows frontend run: 576 files pass / 30 fail; 9,867 tests pass /
  215 fail / 14 skip. This included a new fixture missing scrollTo; its corrected
  complete suite and the final four-suite run pass. Other failures include native,
  platform and locale assumptions; not every failure was reproduced on main.
- Complete Windows backend run: 153 packages pass / 56 fail. Broad platform and
  timing failures include a late goroutine assertion in an unrelated resume test.
  Go build/vet pass. Full pinned lint reports 2,164 findings, mostly Windows/CRLF
  goimports; diff-scoped pinned lint reports zero. Race testing requires unavailable
  CGO support; Linux race/native macOS jobs must be checked in CI.
- Native Windows Electron renderer fixtures: final complete selection suite passes
  22/22, including Escape then Shift, held Escape, real window blur/focus, the
  unchanged disabled label, file/diff refreshes and navigation. Earlier cold-start
  navigation timeouts are retained in local logs; no universal timing claim.
- Real Electron browser postcondition/navigation integration passes. Renderer
  selection fixtures mock bridge/HTTP; no live-provider full-app recovery pass is
  claimed. macOS/Linux manual verification remains a platform gap.
- Cloud-client 31 tests and product-ui 161 tests, typechecks and builds pass. Docs
  build passes; docs tests retain the existing alias-resolution suite failure and
  translation checks report six markers in untouched content. Generated locale
  changes were restored. Existing dependencies/runtimes were reused via pnpm.
- Logs and synthetic before/after evidence stay outside Git under
  C:/Users/Lenovo/.ao/dev/recovery-*.log and recovery-evidence/. The native captures
  remain under side-creation-audit/electron-results/. No new media was committed.
