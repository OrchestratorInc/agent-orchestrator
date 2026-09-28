# Gates: remaining account isolation and completion

Scope: execute NEXT-IMPLEMENTATION-PLAN.md against the published checkpoint and a reviewed current-main integration. Gate states below distinguish local checks from independent review and release evidence. Existing slice evidence is historical until the relevant final snapshot is reverified.

This ledger contains acceptance outcomes, not permission to implement or publish. Manual gates cover combined product outcomes whose new test oracles have not yet been written, native evidence, or independent review. Before implementing a slice, replace its manual test obligations with exact commands and decisive expectations where automation is possible; confirm every named test actually executes. Do not turn a planned command into passing evidence.

## Baseline and account isolation

- [ ] G01: current-main integration preserves the reviewed source history, protected native behavior and existing data, with every inherited versus introduced failure classified.
  EVIDENCE: pending; P0 requires merge SHAs, six-conflict resolution review, untouched control results and original plus upstream-adjusted protection audits.
- [ ] G02: provider/version/authentication/interface matrix and setup-token support boundary are reviewed without promising unsupported storage, proxying or usage access.
  EVIDENCE: pending; P0/P4 require supported native execution versus API-key proxy decisions and explicit migration/reconnect behavior.
- [ ] G03: explicit initial account choice is durable before provider execution across desktop, CLI and programmatic session creation.
  EVIDENCE: bounded implementation and local checks are sealed in REVIEW-82-NEXT-INITIAL-SELECTION.md; 44 source/test/generated files, repeated creation/launch races, full affected suites and real-component tests pass. Independent acceptance remains pending; native desktop positive-account execution has no authorized credentials in the isolated profile.
- [ ] G04: simultaneous managed A/B isolation passes through actual production runner and controllers for every enabled interface, including managed app-server Chat.
  EVIDENCE: pending; P1 requires conflicting ambient auth, home/keychain separation, exact upstream identity, stale routes, resume and unrelated native preservation.

## Switching and exact retirement

- [x] G05: the two existing production execution and cancellation regressions pass three race repetitions.
  CHECK: go test -v -race -count=3 -timeout=180s ./internal/daemon -run 'TestAccountsManagerControlProduction(Execution|SwitchCancellation)'
  EXPECT: /ok\s+github\.com\/aoagents\/agent-orchestrator\/backend\/internal\/daemon/
  CWD: backend
  EVIDENCE: `/var/tmp/pr-5769-next-79.wos4rh/switch-corrections-race3-post-lint.log`, exit 0; exact four-file correction seal in REVIEW-82-NEXT-SWITCHING.md. Missing exact-inspector capability was a fixture defect; cancellation required a narrow durable-state correction. G06 remains pending independent review.
- [ ] G06: independent switching review clears readiness, retry, cancellation, ownership and queue recovery across direct/fallback runtimes and SQLite reopen.
  EVIDENCE: pending; passing G05 alone is insufficient. P2 includes failed launch publication, replacement takeover, empty history, lost cancel response and no interrupt replay.
- [ ] G07: the preserved Linux escaped-descendant regression passes for exact retirement and account deletion without changing its survival invariant.
  CHECK: go test -v -race -count=3 -timeout=180s ./internal/adapters/chatdriver/persistenthost -run TestProviderOwnerEscapedDescendantBlocksRetirement
  EXPECT: /ok\s+github\.com\/aoagents\/agent-orchestrator\/backend\/internal\/adapters\/chatdriver\/persistenthost/
  CWD: backend
  EVIDENCE: pending; Linux only. Prove both leaf cases actually ran; absence under another OS cannot count as success.
- [ ] G08: reviewed containment and deletion proof covers every matched managed execution, restart/crash cut, refresh worker and revocation lease while preserving unrelated/native/replacement processes.
  EVIDENCE: pending; P3 requires durable identity, no membership/host-execution escape, cancelled-generation admission, idempotent vault removal, final database cleanup and legacy upgrade handling.

## Credential experience and platforms

- [ ] G09: credential input, verification, reconnect, expiry and usage eligibility have truthful capability-specific states and safe migration of existing misclassified input.
  EVIDENCE: pending; P4 requires invalid/revoked/expired/wrong-provider/scope cases, no paid validation and no speculative identity or quota.
- [ ] G10: Harness, initial picker and public session controls distinguish native login, managed availability, pending/committed state and backend capability without fallback or optimistic success.
  EVIDENCE: pending; P4 requires API/CLI/UI boundary tests, locale/accessibility checks, request IDs and stale-response/large-revision controls.
- [ ] G11: native macOS arm64 and x64 establish same-boot keeper-death retirement, complete guest execution, packaging/signing and desktop compatibility.
  EVIDENCE: pending; P3 G1/G4 feasibility precedes production adoption. A design approval or compile does not meet this gate.
- [ ] G12: native Windows passes direct kernel negative controls and the complete creation/publication/shutdown/recovery and replacement-preservation matrix.
  EVIDENCE: pending; no native runner established. Preserve compatibility-runtime red logs; do not weaken the guard to satisfy them.

## Integrated release evidence

- [ ] G13: full backend/runner/frontend and required workflow suites, builds, vet, lint, contract generation and native coexistence checks pass on one final snapshot.
  EVIDENCE: pending; P5 must resolve/classify all five backend package failures and both full lint inventories. Record any unavailable CI environment as a gap, not a pass.
- [ ] G14: actual concurrent A/B provider sessions, switch/retry/cancel, in-use removal, restart/revocation and queues pass in the isolated real desktop with inspected visual evidence.
  EVIDENCE: pending; P5 requires two authorized identities per supported provider, actual routing observations, native coexistence and screenshots/recording, not mock labels.
- [ ] G15: measured responsiveness meets the original specification or a separately reviewed requirement change, with complete latency boundaries and sample counts.
  EVIDENCE: pending; P5 specifies reference load, warm/cold counts, p50/p95, queue drain, provider waits and guest startup breakdown.
- [ ] G16: independent review clears the exact integrated tree and all residual wiring/credential deltas, with protected manifests, secret scan and complete release-gap accounting.
  EVIDENCE: pending; exact source/evidence hashes and final review required. Publication approval is separate and cannot clear technical gates.
