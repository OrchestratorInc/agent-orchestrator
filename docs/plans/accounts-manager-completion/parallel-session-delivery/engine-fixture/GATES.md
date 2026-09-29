# Registration fixture gates

- [x] E1: preserve failed-first results and confirm the scheduling defect on unchanged HEAD.
  EVIDENCE: `engine-auth-focused-before.log` has 18 passes and 12 failures; `engine-auth-head-control.log` has 15 passes and 15 failures. The assertion-only control records Account A still pending while B has completed on all three reproduced failures. No production change is needed to reproduce it.
- [x] E2: correct only fixture observation and joined cleanup, with both enqueue orders.
  EVIDENCE: `engine-auth-focused-corrected.log` passes 40 leaf cases across ten repeats under race. The global hook is cleared only after test-owned workers finish. Runtime scheduling and account policy are unchanged.
- [x] E3: an external control withholding individual completion fails both orders without cleanup races.
  EVIDENCE: `engine-completion-negative.log` fails forward and reverse at the intended assertion: the completed registration waits for the unrelated blocked task. No build error, cleanup timeout or race warning occurs. The overlay suppresses only per-task completion; production source remains unchanged.
- [x] E4: affected-package repeated race checks and complete module checks run on the final correction snapshot.
  EVIDENCE: the complete affected package passes 534 leaf cases across three race repeats. Engine build/vet and ordinary tests pass: 10,043 passes and eight declared skips. Full engine race runs 10,038 passing cases, one failing media-relay case and 11 declared skips; the media failure reproduces three times on unchanged HEAD. No registration failures or race-detector warnings remain. Runner build/vet and both complete ordinary/race commands pass. All final preservation checks pass. A failed complete engine command remains a release gap, not a full-suite pass.
- [x] E5: freeze exact source, documentation and evidence manifests; verify protected boundaries and unchanged prior archives.
  EVIDENCE: `engine-fixture-final/FREEZE.md` records the two-file source seal, exact correction patch, four documentation files and evidence manifest. Final verification compares live and archived source bytes. All 91 protected files, 33 generated files, five guest entries and 12 browser-correction files match. Eight prior source archives and all ten recorded browser-seal artifacts retain their hashes. The integrated manifest has 78 entries; only the upstream-delta note changes among its prior 77 entries.
- [ ] E6: independent review accepts the correction. Full product release remains held on separate containment, native-platform, live-account and integrated-test gates.

Evidence root: `/var/tmp/pr-5769-integrated-79.6t2fcN`. The media-relay test remains unchanged and separately reported.
