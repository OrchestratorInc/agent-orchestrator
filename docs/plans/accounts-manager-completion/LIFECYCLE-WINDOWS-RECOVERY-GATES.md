# Gates: complete Windows lifecycle matrix

OWNS: backend/internal/adapters/chatdriver/persistenthost/**, docs/plans/accounts-manager-completion/LIFECYCLE-WINDOWS-RECOVERY-GATES.md

Scope: continue Windows verification on the live tree while the sealed F2/F3 archive remains independently reviewable. Every requested Windows case must pass before a new runtime seal is routed. No test waiver, weakened ownership check, native fallback or narrowed descendant contract is allowed.

Plan:

1. Preserve and verify the previous source/archive and exact red logs. Keep the reauthorized bounded review request active; do not edit or replace that archive.
2. Reproduce the complete Windows matrix and independently test the job-query/membership API contract, without AO launch or recovery logic.
3. Correct substantiated product defects only. A nonconforming execution environment requires a conforming Windows runner, not fabricated evidence or a production bypass.
4. Run the complete matrix three times, strict JSON/size/range regressions, cross-build, vet, race/lint and protected audits on one final snapshot.
5. Freeze and route only after every required matrix check is green. Preserve native platform, macOS and escaped-descendant holds.

Live continuation evidence: /tmp/pr-5769-windows-live-79.bXJnu7. First rebuild the current Windows test executable, then run the complete three-repeat matrix with its kernel API controls intact. Classify observed failures before making any source correction. Native evidence and guest feasibility remain separate; no guest implementation is part of this continuation.

- [ ] WR1: the Windows kernel ownership-query contract is usable and distinguishes invalid handles from valid proof
  EVIDENCE: FAIL, three repetitions in /tmp/pr-5769-windows-recovery-79.TPYVn9/windows-kernel-contract-red.log. Basic and extended queries return flags 0 after successful configuration of 0x2000, and accept an invalid handle. Limited-query membership fails while the full-query control passes. No AO host, ownership reader or coordinator participates in these controls. Native Windows execution required.

- [ ] WR2: complete Windows shutdown, ACP/reconnect, birth/publication and ownership controls pass three repetitions
  CHECK: wine persistenthost-windows-recovery.test.exe -test.run='^Test(RemovalHost|ACPHost|HostReconnectsSameProviderAndReplaysDetachedOutput|ProviderOwnerWindows|ProviderOwnerEvidence)' -test.count=3 -test.timeout=180s -test.v
  EXPECT: PASS
  EVIDENCE: FAIL, exit 1, windows-complete-matrix-red.log. 35 unique leaves repeated three times: 75 passes, 30 failures, zero skips. Exact shutdown, ACP/reconnect, publication/resume and ownership controls remain release blockers. Wine prefix /var/tmp/ao79-lifecycle-wine.qrH2XN, test executable under the evidence directory. No claim of native certification.

- [x] WR3: strict JSON, complete exact-size and PID-range regressions remain green on the correction
  CHECK: go test -mod=readonly -p=1 -race -count=3 -timeout=120s ./internal/adapters/chatdriver/persistenthost -run '^TestProviderOwnerEvidence' -v
  EXPECT: PASS
  CWD: backend
  EVIDENCE: PASS, evidence-race.log, exit 0, 2.842 s; 26 unique leaves repeated three times, 78 passes, no failures or skips. Executed serially from backend through Fish and the existing isolated environment wrapper.

- [x] WR4: affected compile, build, vet, race and changed-scope lint pass without protected-path drift
  EVIDENCE: Windows test cross-compile, entire backend Windows cross-build and package vet all exit 0; windows-diagnostic-lint.log has 0 issues; strict-evidence race check passes. diagnostic-integrity.json verifies all 1,821 prior source files unchanged, one new Windows-only contract test, zero production edits and zero differences in all 91 protected paths, R4, D2 and generated files. These checks do not satisfy WR1/WR2 or full native runtime acceptance.

- [ ] WR5: a replacement freeze is routed only after the entire requested Windows matrix is green
  EVIDENCE: HOLD for a new runtime seal; previous bounded archive remains byte-identical and independently reviewable. The earlier withdrawal is superseded by the reauthorized read-only review routed through orchestrator59, with REVIEW-82-LIFECYCLE-F2F3-REISSUE.md and its exact evidence. That request is not withdrawn. No new seal is permitted while the complete matrix remains failing.

The previous lifecycle milestone remains incomplete. No public controls or publication are authorized.

Measured tally: 2 met, 3 unmet, 0 abandoned. A native Windows 10+ x64 target was requested through a durable needs-input report. The live message to session59 returned INTERNAL_ERROR, request Arch/xQO0CDwDLg-002270; report persistence succeeded. No reply or provisioned target has been observed.
