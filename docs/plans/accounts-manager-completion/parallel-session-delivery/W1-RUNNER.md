# W1 runner concurrency slice

Base: `ec7efde0652f21690d8e98e2dcfa71d5b132e422`, with the existing retirement correction preserved unchanged. Evidence root: `/var/tmp/pr-5769-parallel-79.7QugRY`.

Use the installed Superpowers plan-execution, test-first, good-tests and verification workflows. This is a bounded child of PLAN.md, not full W1 or product completion.

## Scope and rulings

- Verify the existing real runner with two independently verified synthetic Codex credentials and session-scoped routes. A channel barrier must prove both upstream responses remain active concurrently. A shared client session/cache hint must not override either chosen account.
- Rebind, fence, disable and remove A while B is streaming. New A requests must use only its committed account or fail; B's stream must complete unchanged. Restart must require reconciliation and must not restore removed credentials.
- Use public production request handling and authenticated private control routes. Do not substitute a fake selector, manager or vault. Only the external provider is a local test server.
- Name any production break before adding its test. Tests that pass on the baseline are characterization evidence, not a fabricated red regression. New behavior changes require a genuine failing assertion first.
- Ruling: reuse this AO-linked worktree and local evidence directory rather than creating another branch or deleting older work. Cost if wrong: workspace interference, checked through the preserved manifests and narrow diff inventory.
- Ruling: retain user-required midpoint and independent reviews through AO, despite the generic inline workflow's end-only review default. Do not use built-in task delegation. Cost if wrong: slower review delivery, never implicit acceptance.
- Ruling: retain evidence and do not commit or publish during this slice. Existing lifecycle HOLDs are unaffected. No provider request uses personal credentials.
- Midpoint finding: the valid data-only SSE fixture reaches the upstream barrier but never the client barrier before a second event. The Codex response translator drops the scanner's empty line, so the downstream framer loses the event delimiter. Preserve this failed-first check and correct only that generic translation boundary, recording the local engine delta in UPSTREAM.md. Preserve leading-empty-event behavior so an empty heartbeat cannot commit a request. This is not a selector failure or a reason to replace the provider library.

## Task 1: overlapping identity and independent mutation oracle

Create `accounts-manager/runner/internal/runner/parallel_sessions_test.go` with real subprocess startup, verified credentials, stream barriers, exact upstream identity/count assertions, cancellation-aware cleanup and redaction checks. Use the existing model-refresh barrier to avoid treating unfinished startup as isolation failure.

Breaks caught: global/first-account selection, a shared session hint crossing route scope, serialized execution disguised as concurrency, mutation of B by an A-only binding/credential change, and stale/restarted A authorization.

Run the tests before any production correction. If they expose a defect, record the exact failure and fix only the owning boundary. If baseline behavior passes, retain the characterization and test the oracle using a controlled wrong-identity negative case without changing production logic to manufacture failure.

## Task 2: self-review and broaden verification

Review barrier cleanup on every failure, bounded waits, exact request counts, fixture secret isolation, startup/restart and admission clock behavior. Add missing negative controls first. Run focused races three times, the full runner suite and race suite, runner build/vet/lint, then preservation and generated checks. Record every failure rather than weakening an assertion or increasing a timeout to hide it.

## Task 3: freeze and independent review

Freeze exact changed source and logs. Request read-only review through the orchestrator of the oracle and any correction. Preserve native-platform, managed Chat, actual terminal-controller and live-provider gaps; a runner process is not a real provider CLI or a desktop session.

## Acceptance

- [x] W1A: both selected identities have overlapping live upstream responses through the production runner and a shared client hint cannot cross the bindings.
  CHECK: env GOWORK=off TMPDIR=/var/tmp/pr-5769-next-79.wos4rh/tmp SHELL=/bin/sh GIN_MODE=release /tmp/pr-5769-publish-79.p3jxPQ/node-v24.21.0-linux-x64/bin/node /tmp/pr-5769-rebase.reOkRW/run-isolated.mjs go test -v -mod=readonly -race -count=3 -timeout=180s ./internal/runner -run TestRunnerParallelSessions
  EXPECT: --- PASS: TestRunnerParallelSessionsOverlap
  CWD: accounts-manager/runner
  EVIDENCE: `/var/tmp/pr-5769-parallel-79.7QugRY/runner-final2-focused.log`, exit 0, three race repetitions. Separate-account and shared-account cases prove upstream overlap and client delivery before either response completes. Source manifest and limits are in W1-RUNNER-REVIEW.md.
- [x] W1B: A-only binding/credential mutations preserve B's existing stream, reject stale A requests and survive runner restart with exact upstream counts.
  CHECK: env GOWORK=off TMPDIR=/var/tmp/pr-5769-next-79.wos4rh/tmp SHELL=/bin/sh GIN_MODE=release /tmp/pr-5769-publish-79.p3jxPQ/node-v24.21.0-linux-x64/bin/node /tmp/pr-5769-rebase.reOkRW/run-isolated.mjs go test -v -mod=readonly -race -count=3 -timeout=180s ./internal/runner -run TestRunnerParallelSessions
  EXPECT: --- PASS: TestRunnerParallelSessionsMutation
  CWD: accounts-manager/runner
  EVIDENCE: the same final focused log passes fence, rebind, native, disable and remove cases three times. B's existing stream and a new B request remain usable before restart; startup rejects admission until reconciliation and never restores stale A access. This exercises runner credential removal, not coordinated controller retirement.
- [x] W1C: the independent identity oracle rejects a deliberately wrong account, and cleanup/error paths are reviewed without weakening either overlap or isolation assertions.
  EVIDENCE: wrong-identity and partial-response no-replay controls pass three times in the final focused log. Self-review corrected cleanup ordering, joined workers, expanded secret scans to every minted token and added fresh B admission before restart. The genuine delimiter defect has separate pre-fix runner and translator failures.
- [x] W1D: the complete runner tests/races, build/vet/lint and preserved source/generated/design manifests pass on the final slice snapshot, with exact evidence.
  EVIDENCE: W1-RUNNER-REVIEW.md records final commands and logs. Full runner lint has zero findings. Additional engine-package lint has three findings in two unchanged upstream files and zero changed-file findings; it is not claimed as a full engine lint pass. All 9 retirement, 91 protected, 33 generated and 5 guest-package entries match.
- [ ] W1E: independent read-only review accepts the exact bounded correction and its concurrency oracle before this slice is treated as cleared.
  EVIDENCE: request package is W1-RUNNER-REVIEW.md and `/var/tmp/pr-5769-parallel-79.7QugRY/final/source.sha256`. Verdict pending. Full W1 terminal/controller, live-provider and product acceptance remain separate.
