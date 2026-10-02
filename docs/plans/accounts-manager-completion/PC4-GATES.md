# Gates: desktop account controls

Scope: desktop UI and public session-control integration only. PC3 is independently CLEAR at /tmp/pr-5769-pc3-f1-rereview-82.txt. No frozen HTTP, CLI, lifecycle, guest, native switching or Subscriptions files change. No commits or publication.

Artifact root: /tmp/pr-5769-pc4-79.hoSn6v.

## Plan and boundaries

1. Preserve PC3 source/slice/evidence and the PC2, 113-file lifecycle/design, 91-path protected and five-file guest manifests. Inspect existing account and session surfaces.
2. Add thin typed public-control helpers with ownership checks and secret-safe request-ID diagnostics. Build a session dialog with separate committed state and pending operation, explicit account/native and timing choices, safe revision, stable operation ID, refresh/retry/cancel and no optimistic completion. Write failed-first tests. Self-review the first complete path before expanding.
3. Integrate coordinated removal impact, exact revision and explicit confirmation, operation status/retry/cancel and restart recovery. Preserve account creation and inventory flows; distinguish sign-in cancellation acknowledgement from observed terminal state.
4. Run focused tests per behavior, then full frontend tests/typecheck/build, affected backend race and generated drift checks. Preserve all unrelated seals.
5. Use the real Electron app in an isolated checkout and isolated data with a real provider catalog. Capture and inspect actual screenshots and a short recording. Record unavailable provider credentials or runtime capability honestly. Seal and request reviewer82 review.

The current daemon does not supply the optional control service to the reviewed public routes. Frozen lifecycle and HTTP construction must remain unchanged. A 501 response is a visible unavailable capability, never inferred success. Desktop switching/removal and restart execution cannot be claimed without a real enabled implementation; synthetic tests only establish the UI/public-contract boundary.

- [x] P1: preserved manifests and initial UI inventory are recorded
  CHECK: node /tmp/pr-5769-pc3-f1-79.sJGjN3/verify-freeze.mjs
  EXPECT: "mismatches": []
  EVIDENCE: root-CWD Fish, exit 0, midpoint-preservation.json; corrected PC3 source/correction, PC2 13, lifecycle/design 113, protected 91 and guest design 5 all match. Separate pc3-slice and pc3-evidence checks exit 0. The seven-file UI baseline manifest is ui-baseline.sha256, SHA256 447f0b051d6db35d9f3f8f1fe9653d568fadad691c87e3b504e80102bdcf2200. Inventory and production 501 boundary recorded above and in PC4-MIDPOINT.md.

- [x] P2: one complete session interaction passes red-to-green tests and midpoint self-review
  CHECK: cd frontend && npm test -- src/renderer/components/SessionAccountControl.test.tsx src/renderer/lib/accounts-manager-controls.test.ts --maxWorkers=1 && npm run typecheck
  EXPECT: Test Files  2 passed
  EVIDENCE: midpoint-focused-1.log through midpoint-focused-3.log each pass 17 tests. Self-review and eight-failure regression evidence are in PC4-MIDPOINT.md. Final midpoint typecheck exits zero in midpoint-typecheck.log. This is the panel's complete component interaction, not yet a desktop/session-view entry proof.

- [x] P3: session controls cover explicit choices, committed/pending separation, stale revision, capability/unavailable, recovery, cancellation and safe diagnostics
  EVIDENCE: public-ui-focused.log passes 63 tests in five files. entry-diagnostics-green.log passes all 211 tests in four files, including both session entry forms and route-label redaction. recovery-review-red.log records two failed-first assertions; recovery-review-green.log passes 30 tests after correction. This establishes component behavior, not production execution.

- [x] P4: removal impact, exact revision, explicit confirmation, ownership and restart recovery pass red-to-green tests with no fallback
  EVIDENCE: removal-red.log records four failed-first assertions. removal-initial.log passes 12 tests; removal-settings-green.log passes 36. Exact revision zero, impact confirmation, no direct deletion, unresolved reference remount, missing-operation uncertainty, terminal daemon responses, retry/cancel ownership and corrupt storage are covered.

- [x] P5: existing add/login/inventory coexistence and acknowledgement semantics pass without secret disclosure
  EVIDENCE: settings-boundary-red.log records three failures before correction; inventory-diagnostics-red.log records three before correction. inventory-diagnostics-green.log passes 42 tests. refresh-row-red.log records the request-ID regression, corrected in public-ui-focused.log. Sign-in DELETE acknowledgement remains distinct from observed status. localization-focused.log passes 80 tests across seven files after the full suite caught untranslated new controls. localization-typecheck.log exits zero.

- [ ] P6: full frontend tests, typecheck/build, affected backend races and generated drift pass on one final source snapshot
  EVIDENCE: partial, HOLD. The final 22-file manifest is pc4-source.sha256, SHA256 cf6b09cab3371f7193ab8d3ad4fc181e46780ead9929c157e7e72ae137eb28ce. final-focused-1.log through final-focused-3.log each pass 64 tests. final-frontend-tests.log passes 340 files and 5,395 tests with seven skips; final-typecheck.log and final-frontend-build.log exit zero. Backend build/vet and generated drift pass. All affected race packages except the root HTTP package pass. TestServerShutdownEndpoint exceeds its five-second shutdown deadline in both affected-suite attempts and once in the three-repeat root package. Its isolated three-repeat control and the untouched-HEAD root package control pass. No cause or inherited-HEAD defect is established; backend bytes remain frozen. Full pinned CI and native-platform parity are not claimed. See REVIEW-82-PC4.md.

- [ ] P7: actual isolated desktop flows, persistence, screenshots and recording are verified, with any provider/capability gaps explicit
  EVIDENCE: partial, HOLD. The actual Electron app used an isolated checkout, private home/data, compiled daemon and real provider catalog. desktop/pc4-native-flow.mp4 and actual screenshots were inspected. Saving an intentionally invalid synthetic key, explicit default selection and clearing with routing off, unavailable removal/recovery, disabled refresh, and app/daemon restart retention were verified. The runner stayed alive during the restart, so this is not cold-vault-reopen evidence. The optional session/removal control service remains unwired and returns 501. Live login, authenticated refresh, successful switching/retry/cancel/removal and native switching coexistence remain unverified. Private native storage reports account_storage_unsafe. Desktop cleanup confirms no lab-owned process remains. See REVIEW-82-PC4.md.

- [x] P8: exact UI snapshot and preservation audit are sealed for independent review
  EVIDENCE: pc4-source.sha256 seals 22 files, SHA256 cf6b09cab3371f7193ab8d3ad4fc181e46780ead9929c157e7e72ae137eb28ce; pc4-source.tar.gz SHA256 5de5502766adf5332bc36edca5f8ae1031eeb30262f9029d116ec34d8377ac03. pc4-only.patch has only those 22 files and passes a reverse-apply dry run. sealed-scope.json verifies all 5,558 copied regular source entries remain unchanged in the tested desktop checkout; the live tree differs only in this slice's handoff/gate documentation. sealed-preservation.json verifies PC2, PC3, lifecycle/design 113, protected 91 and guest 5 unchanged. Separate PC3 slice/evidence checks pass. REVIEW-82-PC4.md requests bounded independent review with P6/P7 gaps explicit. The final external pc4-review-seal.json records source, 26-file source/documentation slice and evidence digests.

- [ ] P9: independent reviewer82 returns bounded CLEAR
  EVIDENCE: pending
