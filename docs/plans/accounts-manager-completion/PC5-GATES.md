# Gates: PC5 production account controls

OWNS: backend/internal/service/session/accounts_manager_controls*.go, backend/internal/daemon/accounts_manager_controls*.go, backend/internal/daemon/daemon.go, docs/plans/accounts-manager-completion/PC5*.md, docs/plans/accounts-manager-completion/REVIEW-82-PC5*.md

Scope: connect the reviewed public controls to the existing coordinated manager, then verify production behavior locally in independently reviewed slices. No publication.

- [x] P0: preserve the inherited shutdown diagnosis and the independent F1 CLEAR.
  EVIDENCE: `/tmp/pr-5769-pc5-readonly-79.6uQkQt/read-only-evidence.sha256`, SHA256 `fe20236ca4bd672299260077234530047f5838ce1ec0f99ccddce0e5817dda21`; current/main/sealed shutdown failures 5/20, 7/20, 3/20 with identical first-request waits. `/tmp/pr-5769-pc4-f1-rereview-82.txt` clears the bounded renderer correction. Server and fixture remain unchanged.
- [x] P1: failed-first construction and public-router tests demonstrate the missing production service boundary, then pass through the real session service and manager.
  CHECK: env TMPDIR=/var/tmp/ao79.uF9PZF GOMAXPROCS=2 node /tmp/pr-5769-rebase.reOkRW/run-isolated.mjs go test -mod=readonly -race -p=1 ./internal/daemon ./internal/service/session -run AccountsManagerControl -count=3 -timeout=180s -v
  EXPECT: /ok\s+.*internal\/daemon[\s\S]*ok\s+.*internal\/service\/session/
  CWD: backend
  EVIDENCE: Fish, backend CWD, exit 0. `/tmp/pr-5769-pc5-79.0k7pqi/final-focused-race.log`: daemon 15.254s and session service 1.022s, three repetitions, no skips or race reports. `production-red.log` preserves all three missing-construction/501 assertions before the production edits.
- [x] P2: service and public-router behavior preserves explicit choices, ownership, revision checks, pending/committed separation, retry/cancel, removal confirmation and safe errors.
  EVIDENCE: Same final focused race log covers the service adapter and actual startSession/manager/store/router with synthetic outbound runtime/catalog ports. Cancellation releases the real input fence without revision rotation; in-use removal preserves B/native bindings and runtimes. Wrapped unavailability remains 503 with a request ID and safe templated telemetry. Midpoint read-projection defect was reproduced in `projection-failed-first.log`, corrected with a binding recheck and rerun three times. Actual-process recovery and successful target launches remain P4.
- [x] P3: first green wiring slice is self-reviewed, verified under race/build/vet, sealed with exact delta and preservation manifests, and ready for independent midpoint review before runtime/UI work.
  EVIDENCE: `REVIEW-82-PC5-WIRING.md` requests the read-only review. Four-file source manifest `/tmp/pr-5769-pc5-79.0k7pqi/pc5-wiring-source.sha256`, SHA256 `63312b8885fadfdf4129c4ff1bf9031160965e06741269849e3fbc5d5618b82f`. Full daemon/session and controller/spec race suites, backend build/vet, four-file lint, generated drift and preservation checks pass. `review-freeze.json` indexes the final source, six-file slice, evidence and integrated inventory. This gate means review-ready, not independently cleared; P4 cannot start before the verdict.
- [ ] P4: after independent review, actual-process A/B switch, retry/cancel, in-use removal, restart/revocation and authorization leases preserve queues and unrelated native/B sessions.
  EVIDENCE: pending
- [ ] P5: full affected backend/runner/frontend checks, generated drift, changed-scope lint, build/vet and preservation audit have exact final-snapshot evidence, with inherited failures separated.
  EVIDENCE: pending
- [ ] P6: isolated real desktop flows, inspected screenshots/recording and supported provider/platform evidence are recorded; unavailable native/live checks remain explicit external release gaps.
  EVIDENCE: pending

## Implementation decision and frozen boundary

Use a new session-service adapter file with a narrow optional manager capability. The session service already owns the real manager and session reads. Delegate mutations to `Start/Retry/CancelAccountsManagerSwitch` and `Start/Retry/CancelAccountsManagerRemoval`; never duplicate their journals, stop logic, queue fences, revocation or credential access in HTTP.

The only planned existing production-file edit is assigning `AccountsManagerControls: sessionSvc` in the daemon's HTTP dependency literal. `daemon.go` is in the 113-file lifecycle snapshot, not the 91 protected-path inventory. The new delivery explicitly authorizes this construction wiring. Preserve its pre-PC5 hash `311b614fc2c8de722c60f504ffd08ea01d2dfdc3a88789b45d3dfb4a7fd2c9a7` and old archives, isolate this exact delta, and rehash the other 112 lifecycle files unchanged. Preserve all PC2/PC3/PC4 F1, guest and protected paths.

Production-construction coverage must check both the actual daemon dependency assignment and the real `startSession` service used by the public router. An admission-only controller fake does not prove execution. Unsupported provider/interface combinations remain explicit; a read must not pick an account, invent a binding, mint a route or launch work.

## Sequence

1. Preserve the baseline and add deterministic failing construction/router tests before production edits.
2. Implement the thin adapter and dependency assignment. Add service-boundary negative controls and real-manager durable switch/removal schedules. Review after the first green path.
3. Freeze and request midpoint review. Do not start real runtime/UI evidence before that handoff.
4. After clearance, continue actual process and desktop verification, then final full-scope verification. Keep live-provider and native-platform gaps visible rather than weakening guards.
