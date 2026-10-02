# Gates: bounded Windows creation and evidence correction

OWNS: backend/internal/adapters/chatdriver/persistenthost/**, docs/plans/accounts-manager-completion/LIFECYCLE*, docs/plans/accounts-manager-completion/REVIEW-82-LIFECYCLE*

Scope: verify and freeze the F2 creation-time containment and F3 canonical evidence corrections for independent review. This leaf does not waive any macOS, escaped-descendant, native Windows or broader lifecycle requirement.

Plan: strengthen both retirement-entry assertions at the exact byte boundary; rerun canonical duplicate and complete-proof regressions; verify the existing creation-time job path without repeating the old implementation; compile/vet Windows and execute Wine crash/collision controls; run bounded Linux compatibility and lint checks; capture exact source, correction and protected manifests; request reviewer82 review and stop source edits.

- [x] W1: duplicate semantic fields and oversized complete proofs cannot authorize either retirement entry point
  CHECK: go test -mod=readonly -p=1 -race -count=3 -timeout=120s ./internal/adapters/chatdriver/persistenthost -run '^TestProviderOwnerEvidence' -v
  EXPECT: PASS
  CWD: backend
  EVIDENCE: final-linux-host-race.log under /tmp/pr-5769-life-f2f3-79.CYTx36 includes the complete evidence prefix, three repetitions under race, PASS in 70.512 s. Eight duplicate cases, exact-limit and one-byte-over controls, fifteen incomplete proofs and two out-of-range Windows PID cases exercise reader and both retirement entry points. Earlier failed-first logs remain preserved.

- [x] W2: Windows x64 creation-time job code compiles and passes vet
  EVIDENCE: final-windows-build.log, final-windows-compile.log and final-windows-vet.log, all exit 0. Entire backend cross-build plus persistent-host test compilation and package vet. Compile is not native runtime proof.

- [x] W3: deterministic creation crash and collision cases preserve containment and unrelated processes under Wine
  EVIDENCE: final-windows-birth-evidence-wine.log, exit 0, three repetitions. Original create-then-assign failed-first executable/source/log remain preserved. Only the bounded birth/collision/proof cases are certified here; the recovery and same-owner controls remain failing under Wine.

- [x] W4: affected Linux host paths and changed-scope lint checks pass on the frozen correction
  CHECK: go test -mod=readonly -p=1 -race -count=3 -timeout=240s ./internal/adapters/chatdriver/persistenthost -run '^Test(ProviderOwnerEvidence|RemovalHost|ACPHost|HostReconnectsSameProviderAndReplaysDetachedOutput)' -v
  EXPECT: PASS
  CWD: backend
  EVIDENCE: final-linux-host-race.log, PASS 70.512 s; final-linux-build.log and final-linux-vet.log, exit 0 for entire backend; final-linux-lint.log and final-windows-lint.log, 0 issues each. Full suites are not represented as green while the escaped-descendant regression remains red.

- [x] W5: bounded review archive has verified exact source hashes and preserves protected, R4, D2 and generated files
  EVIDENCE: final-source-archive.json verifies all 1,821 archived files; final-audit.json verifies the 12-file correction, 19-file lifecycle context, 69-file integrated deletion union, 91 protected paths, 32 generated files, four R4 files, three D2 correction files and ten historical D2 artifacts. final-seal.json verifies every review-archive member. freeze.cjs also verifies the protected files against the original Git baseline, the complete source inventory, formatting and diff checks. Reviewer handoff: REVIEW-82-LIFECYCLE-F2F3.md.

- [ ] W6: native Windows recovery and all publication cuts establish the complete retirement contract
  EVIDENCE: HOLD; final-windows-recovery-hold-wine.log fails three repetitions. Wine's zero-filled extended-limit query prevents certifying recovery; its stricter query-right requirement also rejects the same-owner positive control. No native Windows host available, no production check relaxation or test waiver.

- [ ] W7: reviewer82 independently clears the bounded frozen correction
  EVIDENCE: pending; broader lifecycle L1-L6 and L3a remain tracked separately, public controls held

Leaf tally: 5 met, 2 unmet, 0 abandoned. This is a bounded review checkpoint, not completed lifecycle acceptance. The full product contract remains unchanged.
