# Gates: isolated desktop integration

- [x] E1: the lab source matches the current repository snapshot and all prior frozen boundaries remain intact.
  CHECK: node /var/tmp/pr-5769-browser-recovery-79.AAbpSt/verify-lab-source.mjs
  EXPECT: LAB_SOURCE_MATCHES_CURRENT
  EVIDENCE: Observed exit 0 before and after final verification. The original 5758-file source snapshot and 60-file archive remain intact; an explicit 12-file frontend correction overlay yields 5760 recorded files. All current frontend/backend/runner/shared-package source bytes match the live worktree. The 58 prior integrated, 91 protected, 33 generated and five guest entries match. The old baseline-only check is retained but superseded for the intentional overlay.
- [x] E2: the real Electron app uses scratch data, a private profile, its own freshly built daemon/runner and the real provider catalog.
  EVIDENCE: launch-first.json/log, launch-restart.json/log and launch-browser.json/log identify the owned foreground launches, isolated home/data/profile, exact daemon ports and fresh binaries. Preload and Electron user agent were asserted. API readiness and the real 30-entry catalog were observed. User data and existing desktop processes were untouched. See REVIEW.md.
- [ ] E3: local empty-account, explicit-choice and invalid-input paths preserve truthful state and request IDs through the real UI and restart.
  EVIDENCE: capture-result.json proves empty inventory, disabled routing, wrong-method HTTP 400 with request ID, cleared input and separate device status. restart-before.json/restart-after.json prove persisted settings, empty inventory, no sessions and the same catalog IDs after daemon/app restart. The requested provider is not authenticated in the lab, so positive initial account/native selection could not be exercised. Gate remains partial, not waived.
- [x] E4: actual screenshots and a short recording are inspected, with secrets excluded and positive live-account gaps stated.
  EVIDENCE: captures/first-window.png, captures-corrected screenshots and local-controls.mp4 were inspected, plus the browser-recovery native captures and recording frames. No provider authorization was completed or credential displayed. All login operations were cancelled. Positive quota, live A/B execution and in-use deletion are not demonstrated.
- [ ] E5: every introduced defect has failed-first evidence and final focused/package verification, followed by independent review of the exact snapshot.
  EVIDENCE: Browser handoff and stale-completion regressions were reproduced before correction; the bounded browser-recovery seal includes 89 focused tests repeated three times, 5626 full-suite passes, 60 renderer smoke passes, typechecks and Linux packaging. Independent review requested, no verdict observed. Three gates met, two unmet, zero abandoned.

Actual-account A/B execution, positive quota, confirmed in-use removal, native Mac/Windows and release performance remain open in the parent ledger. These local gates cannot clear them.
