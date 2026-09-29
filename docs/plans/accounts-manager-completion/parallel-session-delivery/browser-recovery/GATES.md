# Gates: browser handoff recovery

- [x] B1: failed-first tests prove that opener failure must retain a valid login operation and offer manual recovery without another start.
  EVIDENCE: /var/tmp/pr-5769-browser-recovery-79.AAbpSt/red.log records two failing assertions on pre-fix production code; red-source.tar.gz preserves that source and the regressions. late-handoff-red.log records the separate stale-completion failure before its correction. See REVIEW.md for exact hashes.
- [x] B2: browser/device recovery, unsafe links, clipboard errors, terminal observations and cancellation races pass focused verification.
  EVIDENCE: lab-checks/focused-{1,2,3}.log each records 89 passing tests across five files, zero skips, with Node 24.21.0 under Fish in the isolated frontend checkout. MIDPOINT.md records the reproduced generation race and fixture corrections. Current-worktree focused checks also passed three times.
- [x] B3: localized messages, frontend typechecks, the full affected frontend suite and desktop build pass on the corrected snapshot.
  CHECK: /usr/bin/fish --no-config /var/tmp/pr-5769-browser-recovery-79.AAbpSt/verify-ci.fish
  EXPECT: source-after:0
  EVIDENCE: Observed exit 0 on the final source. ci-checks/status.txt has 18 successful stages: typechecks, shared packages, generated cloud contract, docs build, full frontend (5626 passed, six pre-existing mobile skips), renderer smoke (60 passed) and Linux x64 desktop packaging. The earlier missing-zip, native-module ABI and missing sibling-package dependency failures are preserved, not relabelled. The final check uses the pinned browser runtime and matching Node native module.
- [x] B4: the corrected controls are inspected in the actual isolated Electron app without claiming live provider sign-in.
  EVIDENCE: desktop-result.json and desktop-result-reload.json each prove one accepted start, reopening the same link, one cancellation acknowledgement and zero saved accounts; the latter also proves renderer-reload recovery. Three captures and a recording from each run were inspected. The native opener did not observably reject; exception behavior is unit-tested. No provider authorization, positive quota or live A/B claim.
- [ ] B5: protected paths, generated contracts, prior backend seals and guest design remain unchanged, and independent review covers the exact correction.
  EVIDENCE: The 58-file prior integrated snapshot, 91 protected paths, 33 generated paths and five-entry guest design match. final/source.sha256 and final/FREEZE.md identify the exact 12-file correction. Independent review requested through the project orchestrator; no verdict observed. Four gates met, one unmet, zero abandoned.
