# Gates: shutdown-interrupted account switches

OWNS: backend/internal/session_manager/accounts_manager_switch.go, backend/internal/session_manager/accounts_manager_shutdown_test.go, backend/internal/session_manager/accounts_manager_shutdown_e2e_test.go, docs/plans/accounts-manager-completion/production-readiness/SWITCH-SHUTDOWN-GATES.md

Scope: preserve new pre-stop account-switch intent across daemon shutdown and actual manager/database reconstruction. This bounded M03 slice does not clear historical repair, containment or release acceptance. M01 design review can proceed independently; no runtime adoption is included here.

The test shell is fish and the Go toolchain is go1.27.1. Tests use synthetic accounts and temporary databases. Evidence goes under /var/tmp/pr-5769-production-79.Lvj7m9. The 91 protected paths and five guest-design records are unchanged at baseline.

- [x] G1: a deterministic pre-fix test observes shutdown converting a pre-stop switch to terminal failure.
  EVIDENCE: /var/tmp/pr-5769-production-79.Lvj7m9/switch-shutdown-red.log, fish, repository root, go1.27.1, exit 1. All four direct/fallback retry/cancel cases observed phase=failed and code=ADMISSION_CHANGED at the preserved assertion. Toolchain failure in retirement-baseline-red.log is unrelated and is not red evidence.

- [x] G2: requested/waiting intent survives shutdown, SQLite reopen and fresh manager construction; explicit retry or cancellation settles the same operation without fallback.
  CHECK: env GOTOOLCHAIN=go1.27.1 GOMAXPROCS=2 go -C backend test -p 1 -race -v -count=3 -timeout=10m ./internal/session_manager -run '^TestAccountsManagerSwitch(Startup|Handoff|Cancel|Destroy|Shutdown|Readiness)'
  EXPECT: ok
  EVIDENCE: shutdown-final-focused.log, exit 0, 124.171s. All 15 shutdown cases passed three times, including final raw-input fence assertions. Shared oracle with G3; 45 shutdown executions are part of its 315 passing executions, not additional cases.

- [x] G3: stopping remains non-cancellable, cancellation winners remain terminal, genuine target errors remain failures, and existing recovery/ownership race regressions pass.
  CHECK: env GOTOOLCHAIN=go1.27.1 GOMAXPROCS=2 go -C backend test -p 1 -race -v -count=3 -timeout=10m ./internal/session_manager -run '^TestAccountsManagerSwitch(Startup|Handoff|Cancel|Destroy|Shutdown|Readiness)'
  EXPECT: ok
  EVIDENCE: shutdown-final-focused.log, exit 0. 105 distinct leaf cases, repeated three times, 315 passes, no failures or skips. Source hashes match the three-file review manifest.

- [x] G4: complete affected package verification, build/vet, diff audit and protected hashes pass on the frozen delta.
  EVIDENCE: SHUTDOWN-REVIEW.md lists exact commands and logs. Final session-manager full race: 1087 passed, two existing platform-specific skips, exit 0. The other three affected packages passed. Backend build, full vet, tagged manager vet and tagged package lint pass (0 lint issues). All 91 protected paths and five guest-design records match; generated files and source scope are unchanged outside the declared delta. The intermittent process-fixture teardown remains an explicit release note, not a resolved defect.

- [ ] G5: self-review and independent recovery review confirm the bounded correction without clearing the rest of M03.
  EVIDENCE: midpoint self-review below; independent review pending exact final source seal.

- [x] G6: production-created direct and fallback runtimes preserve shutdown intent across SQLite reopen and settle explicit retry or cancellation without unintended execution.
  CHECK: env GOTOOLCHAIN=go1.27.1 GOMAXPROCS=2 go -C backend test -p 1 -tags=e2e -race -v -count=1 -timeout=10m ./internal/session_manager -run '^TestAccountsManagerSwitch(ShutdownRealProductionRecovery|RealProduction)'
  EXPECT: ok
  EVIDENCE: shutdown-final-production-rerun.log, exit 0, 99.195s, 29 passing leaf cases, including all four new shutdown cases; no skips. The first wider run had one existing fixture teardown failure, preserved in shutdown-final-production.log. Its exact case passed three isolated repeats with no source edits. Do not erase that intermittent failure or call it fixed.

## Midpoint self-review

The four-case failed-first regression passed after the four-line production correction. The expanded 15-case matrix passed three times in switch-shutdown-matrix.log. The four actual-process direct/fallback cases passed in switch-shutdown-production.log. A final assertion expansion checks raw input fence restoration and release; all affected checks must run again on that final test file.

- The shutdown test must inspect the manager lifetime, not the per-run context: finish cancels the latter on every path. The correction preserves only requested/waiting journals under manager shutdown. It does not reopen any terminal journal or weaken a durable stopping boundary.
- Cancellation still commits in the store first. Finish reads the authoritative phase; winning cancellation remains terminal and the existing generation-bound cleanup releases its own fences. Old cancellation/retry race controls remain required.
- Shutdown can coincide with another pre-stop error. Retaining the pre-stop operation is conservative: explicit retry revalidates the same account, revision and controller. This is not permission to resume a retired generation or choose a fallback.
- Requested cannot currently advance to itself. Leaving the durable record unchanged avoids inventing a new transition solely for a shutdown diagnostic.
- Reconstruction is a new Manager and an actually reopened SQLite database. The real-process cases also construct a new production runtime wrapper. They use synthetic accounts and are not live-provider authentication evidence.
- The existing public UI offers Cancel for requested/waiting and Retry only for recovery_required. Exposing recoverable pre-stop retry safely is still part of M03; this backend slice does not claim that UI work is finished.
- Older failed records remain unchanged. A repair path still needs exact current binding/controller checks and durable pre-stop proof. No global failed-to-waiting migration is included.

No native switching, Subscriptions, guest design, credential store, API schema, runner, UI or Linux containment source is changed in this slice. M00 baseline integration and ownership review are still open; this independent correction is being prepared while M01 design review is requested. The three retirement groups remain red.

Final ledger: five met, one unmet (G5 independent review), zero abandoned. No broader production milestone is closed by these results.
