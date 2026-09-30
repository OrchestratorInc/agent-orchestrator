# Compact session switching

The user's 2026-09-30 screenshots replace the tabbed proposal. Preserve the original compact agent/model row, add a Switch account action and open a small account popup. Choosing the current agent must open that same account popup instead of being disabled. Do not send a same-agent native handoff.

Scope: renderer and its tests/locales only. Reuse the existing account HTTP operations and safety state. Keep account and timing choices explicit, preserve unknown operations and cancellation/retry, and never show optimistic completion. Show names/email rather than opaque IDs when identity is available. Hide technical details and full usage panels from the ordinary compact flow; retain error request IDs and recovery actions. Fix the screenshot's projectless-session error by omitting the synthetic workspace ID from project-only reads. Backend, generated API, Subscriptions and the 91 protected paths stay unchanged.

Plan:

1. Add failed-first tests for the compact dialog, same-agent account entry and projectless model/config reads.
2. Keep native agent/model controls intact. Replace tabs with a compact account view and back action. Reuse the existing account coordinator UI logic with a compact presentation and optional technical details.
3. Review mutation separation, pending locks, same-agent semantics, recovery, identity labels and close/reopen behavior. Fix introduced issues before broader verification.
4. Run focused and full frontend tests, types and renderer build. Verify the real isolated Electron flow, inspect screenshots/recording, preserve session bindings, check source/protected hashes and request independent review. No publication.

OWNS: frontend/src/renderer/components/SwitchAgentDialog*, frontend/src/renderer/components/TerminalSwitchAgentButton*, frontend/src/renderer/components/SessionAccountControl*, frontend/src/renderer/components/SessionView*, frontend/src/renderer/i18n/*.json, frontend/src/renderer/i18n/instance.test.ts, docs/testing/accounts-manager.md, docs/plans/accounts-manager-completion/COMPACT-SWITCH.md

- [x] G1: Compact controls, same-agent account routing and existing native handoff regressions pass.
  CHECK: npm --prefix frontend test -- --maxWorkers=1 src/renderer/components/SwitchAgentDialog.accounts.test.tsx src/renderer/components/SwitchAgentDialog.test.tsx src/renderer/components/TerminalSwitchAgentButton.test.tsx src/renderer/components/SessionActionsMenu.switch-agent.test.tsx src/renderer/components/SessionAccountControl.test.tsx src/renderer/components/SessionView.test.tsx src/renderer/i18n/instance.test.ts src/renderer/i18n/renderer-coverage.test.ts
  EXPECT: Test Files
  EVIDENCE: /var/tmp/pr-5769-compact-switch-79.cnQmJt/focused.log and focused.exit.json, exit 0, 8 files and 242 tests passed, 22.996 seconds wall time. Includes the 16 compact boundary cases, existing native dialog/menu/focus/recovery cases, account controls, session integration and locale coverage.

- [x] G2: Frontend types and renderer build pass.
  CHECK: npm run typecheck; and npm run typecheck:e2e; and npm exec -- vite build --config vite.renderer.config.ts
  EXPECT: built in
  CWD: frontend
  EVIDENCE: /var/tmp/pr-5769-compact-switch-79.cnQmJt/typecheck.log and typecheck.exit.json, exit 0 in 120.064 seconds; build-e2e.log and build-e2e.exit.json, E2E types plus Vite renderer build exit 0 in 11.226 seconds. Non-failing chunk-size and mixed-import warnings remain. Packaged native installers were not built by this renderer-only slice.

- [ ] G3: The complete frontend suite passes without exclusions.
  CHECK: npm --prefix frontend test -- --maxWorkers=2
  EXPECT: Test Files
  EVIDENCE: /var/tmp/pr-5769-compact-switch-79.cnQmJt/full-frontend-rerun.log and full-frontend-rerun.exit.json, exit 1 in 274.777 seconds. 352 of 353 files completed, 5661 tests passed, 7 skipped and 3 unfinished. The unchanged browser-profile-import fixture aborts inside the native SQLite cleanup hook. No test exclusions or product changes to bypass this error. The JSON report's success field is not authoritative: it warns that tests are still running, while the command exits 1.

- [x] G4: Actual Electron demonstrates the compact original row, current-agent account popup, identity choices, explicit timing and unchanged bindings.
  EVIDENCE: /var/tmp/pr-5769-compact-switch-79.cnQmJt/desktop.log, desktop.exit.json and desktop/result.json, exit 0. Ten checks passed in the actual isolated Electron app with three readable managed identities, no renderer errors, no account mutations and identical before/after bindings for all eight sessions. Inspected compact-agent.png, compact-account.png, small-popup.png and recording frames 35 and 62; the 1440x784 recording is 4.4 seconds. The smaller viewport uses a temporary renderer size override, restored afterward, not a native window resize. No live provider request or account switch was submitted.

- [x] G5: Self-review and exact source/preservation audit pass, with the old tabbed seal retained and no backend or protected-path changes.
  EVIDENCE: /var/tmp/pr-5769-compact-switch-79.cnQmJt/source-seal/audit.json verifies 15 source/test/catalog files, 17 total changed files, all 12 lab production/catalog files matching, 91 protected paths, 5 guest-design files, 2 generated files and 3 shutdown files. Backend and runner unchanged from HEAD 43578e5cba1565bf08f1332656b2e0873bc1e125; git diff --check passes. Self-review below. Independent verdict not claimed.

ABANDON: G3 The native browser-import fixture cleanup defect reproduces independently of the renderer changes and cannot be corrected within this renderer-only task without unrelated main-process or dependency work. Hand off the preserved failing control and final full-suite log; full-suite release verification remains open, not passed.

The old tabbed source seal `f49992aceb4ddf4b0a8475a7f6360759a81c1787be9b2bcd18aef037f3f45ccc` is retained under `/var/tmp/pr-5769-unified-switch-79.7RGc7d/source-seal` as superseded evidence, not the accepted design. Existing deletion, native-platform and broad live-provider release gaps remain open.

## Review and handoff

- Failed-first evidence: `red.log` has 13 failed and 3 passed tests before the compact correction. It reproduces the disabled current-agent item and projectless model-query error. `first-green.log` retains five fixture-only failures (empty asynchronous model catalog and an ambiguous email query); these were corrected without relaxing the product assertions. The final focused set is green.
- Full-suite control: `browser-fixture-control.log` reproduces `node::RemoveEnvironmentCleanupHook` with `Assertion failed: (env) != nullptr` in `Database::~Database()` under Node 24.21.0, exit 1, 26 of 30 cases completed. `source-seal/untouched-browser-import.sha256` verifies the test, implementation, immediate local dependencies, renderer test configuration and package manifest match HEAD. No main-process or dependency manifest changes are in this slice. The original `full-frontend.log` has no completed summary and its invoking process ended with exit 143; it is retained as incomplete diagnostic evidence. Only `full-frontend-rerun.log` supplies the final full-suite result. A complete green frontend suite is not claimed.
- The native agent/model row and handoff request remain separate from account switching. Selecting the current agent calls the account entry callback and never sends a native handoff. Account and timing are explicit; acceptance alone never updates the committed account. Pending, unknown, recovery, retry and cancellation state still use the existing public service contract.
- The compact panel presents readable account identity and puts revisions, IDs and new-conversation choice in More options. Unresolved requests retain their durable ID and block switching back into a competing native action. Native recovery takes priority over account entry. Closing and reopening preserves unknown operations; going back preserves an unsent draft.
- Projectless sessions omit the synthetic workspace ID from project configuration and model reads. Ordinary project reads are unchanged. Cloud and unsupported sessions gain no local managed-account controls.
- The retired draft plan was archived to `/var/tmp/pr-5769-compact-switch-79.cnQmJt/superseded-plan.tar.gz` (SHA256 `946bd3c77ac7c00aca8642540879715f4e3654c3f31601a6bd7e5a54f72d1d33`) before removing only that newly created, superseded plan from the working tree. The previously frozen source archive is untouched.
- The source manifest is `/var/tmp/pr-5769-compact-switch-79.cnQmJt/source-seal/source.sha256`, 15 files, SHA256 `91ca847dc0dd85049eb82765181f9856ab9e308a018022a7181bfcd897e63659`. Source archive: `source-seal/source.tar.gz`, SHA256 `cf0b74fc7357d9292ed2f213cb3738b7957f332516e8033d1dc85ec35db1bbff`.
- Real lab: `/var/tmp/pr-5769-desktop-final-79.yrWkBr/checkout`, with isolated data under its `ao-home/desktop/data`. The 12 changed production/catalog files match the seal. The lab's historical backend source is not claimed to match this checkout; no backend binary or lifecycle behavior was changed here.
- Independent read-only review should check same-agent routing, current/pending identity separation, unknown-response locking, native recovery priority, menu focus teardown, projectless reads and the protected-path audit. No commit, push, PR edit or evidence publication is included.
