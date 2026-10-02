# Gates: PC3-F1 cancellation acknowledgement

Scope: correct the CLI acknowledgement of bodyless sign-in cancellation responses. The reviewed HTTP, lifecycle, protected native paths and guest design stay unchanged. The previous PC3 archive remains evidence of the reviewed defect.

Plan: reproduce with human and JSON output first, including the real public router and service pruning path. Return a distinct cancellation-request acknowledgement. Review its meaning against observed status, then run bounded and package checks and seal the superseding PC3 slice for independent review.

- [x] F1: deterministic pre-correction tests expose the invented terminal state
  EVIDENCE: /tmp/pr-5769-pc3-f1-79.sJGjN3/red-corrected-fixture.log, exit 1, two tests with eight failing output subcases. Actual router/service pruning retained the committed credential. The first diagnostic red.log additionally had a test-fixture error reading request ID from a nonexistent response header; the authoritative red run reads the production error envelope and reaches every required lifecycle control before any production edit.

- [x] F2: acknowledgement, observed state, pending cancellation, committed conflict, repetition and completed/pruned credential preservation pass
  CHECK: go test -mod=readonly -p=1 -race ./internal/cli -run '^TestManagedAccountsLoginCancel' -count=3 -timeout=3m -v
  EXPECT: PASS
  CWD: backend
  EVIDENCE: final-correction-race.log, exit 0, 1.168s. Three top-level tests and ten named subtests each pass three times. Human/JSON acknowledgements, observed states, exact 409 envelope and visible request ID, repeated requests, help, real-service pruning and retained credential all pass. Fish, workspace backend CWD, isolated environment prefix in the handoff; no skips or races.

- [x] F3: midpoint self-review confirms the CLI claims only what each response establishes
  EVIDENCE: Initial correction-race.log passes three runs. The acknowledgement has only operationId and cancellationRequestAcknowledged, while observed status still requires fresh inventory. A 409 returns an error with request ID and empty stdout; no retry, status lookup, fallback or credential-removal request was added. Review tightened help and exact observed-status assertions plus visible request-ID rendering. Final verification must follow this test-only edit. The real-router test uses the real service's event consumer and prune timer with a virtual clock and an injected runner; it is not provider execution evidence.

- [x] F4: original PC3 focused race set passes on the corrected source
  CHECK: go test -mod=readonly -p=1 -race ./internal/cli -run '^Test(SessionAccount|ManagedAccounts|TelemetryMetaClassifiesRegisteredCommandPaths)' -count=3 -timeout=3m -v
  EXPECT: PASS
  CWD: backend
  EVIDENCE: final-focused-race.log, exit 0, 2.574s, all original PC3 tests plus the correction pass three times under race. Fish, workspace backend CWD, isolated environment prefix in the handoff; no skips or races.

- [x] F5: complete CLI and telemetry metadata race suites pass
  CHECK: go test -mod=readonly -p=1 -race ./internal/cli ./internal/telemetrymeta -count=1 -timeout=5m
  EXPECT: ok
  CWD: backend
  EVIDENCE: final-packages-race.log, exit 0. CLI 46.170s and telemetry metadata 1.006s. Fish, workspace backend CWD, isolated environment prefix in the handoff.

- [x] F6: backend build, vet, changed-scope lint and generated drift checks pass
  EVIDENCE: build.log and vet.log each exit 0 with empty output. lint.log exits 0 with 0 issues. api-contract.log passes all 15 spec/parity tests, including generated YAML drift; generated-drift.log proves regenerated TypeScript byte equality. actual-router-race.log passes three repeats (three top-level tests, sixteen subtests) in 1.508s. diff-check.log exits 0. Commands and environment are recorded in REVIEW-82-PC3-F1.md.

- [x] F7: all preserved manifests match and the superseding source archive is exact
  EVIDENCE: /tmp/pr-5769-pc3-f1-79.sJGjN3/final-preservation.json and verify-freeze.mjs verify 13 corrected PC3 sources, three correction files, 13 PC2 files, 113 lifecycle/design files, 91 protected paths and five guest files. Both old and superseding source archives match their own exact inventories. Only two prior PC3 source files changed and one test file was added; the other ten source files and both historical PC3 handoff/gate docs are unchanged. Source manifest SHA256 83b9f328c6c880124ef515e00c2201d81c0e2a2e99f976bbf698c4ab5cddfd6f. No source edits after final verification began.

- [ ] F8: independent reviewer confirms the bounded correction
  EVIDENCE: pending; stop after the immutable handoff. No UI, lifecycle, guest, desktop, provider or publication work is authorized in this slice.
