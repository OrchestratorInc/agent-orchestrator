# Next switching correction

Scope: the reproduced public execution readiness failure and intermittent pre-stop cancellation conflict. Preserve exact-owner runtime checks, durable stop boundaries, queued work and the reviewed cancellation/retry protections. No change to protected native switching or subscription modules.

## Decisions before implementation

1. The public execution fixture lacks `ExactSupervisedProcessInspector`, while the production hybrid runtime exposes it. Correct the fixture only after checking the concrete failure path. Do not replace exact child-generation proof with generic liveness or a visible composer. Add negative controls for missing capability, failed probe and mismatched generation.
2. Cancellation currently reads a phase and then advances that exact phase. A concurrent requested-to-waiting transition can reject cancellation even though neither state has begun stopping. Reproduce the interleaving using barriers and the real SQLite store before correction. Cancellation must still lose once stopping has durably begun. Avoid an unbounded retry loop or widening the irreversible boundary.
3. Retain real direct/fallback runtime coverage. A corrected fake proves a public-service contract, not native process readiness or live-provider behavior.

## Acceptance

- [x] S1: public managed/native switches acknowledge the exact ready generation, while missing capability, unknown ownership and stale generations remain fenced.
  CHECK: go test -v -race -count=3 -timeout=180s ./internal/daemon -run 'TestAccountsManagerControlProductionExecution|TestAccountsManagerControlReadiness'
  EXPECT: /ok\s+github\.com\/aoagents\/agent-orchestrator\/backend\/internal\/daemon/
  CWD: backend
  EVIDENCE: red preserved in `published-blockers-red.log`. Final three-repeat matrix in `switch-corrections-race3-post-lint.log`: daemon 57.518s, manager 34.004s. All four new negative controls execute; no readiness guard was weakened.
- [x] S2: deterministic cancellation-versus-waiting regression succeeds without stopping, launching or rotating the binding; stop-wins and cancelled-retry controls remain strict.
  EVIDENCE: `cancel-phase-assertion-red.log`, `cancel-response-assertion-red.log`, then `switch-corrections-race3-post-lint.log`. Includes both handle forms, stop-wins, repeated cancellation, stale retry, startup/reopen boundaries, and ambiguous response/read results.
- [x] S3: affected manager/store/service/router race tests and actual direct/fallback process checks pass, with no saved-task replay or queue loss.
  EVIDENCE: `switch-runtime-process-final-post-lint.log`, `switch-daemon-race-final-post-lint.log`, `switch-manager-race-final-post-lint.log`, `switch-services-race-final.log`, `switch-http-cli-race-final.log`. Exact commands and observed exit codes are in `switch-final-checks.log` and `switch-post-lint-checks.log`. Actual processes use synthetic provider frames, not live credentials.
- [ ] S4: self-review, protected-path audit and independent review cover one exact source/evidence snapshot.
  EVIDENCE: exact seal and review request in REVIEW-82-NEXT-SWITCHING.md. Local checks and preservation pass; independent verdict remains pending. No dependent safety gate closes on fixture tests alone.

## First green and midpoint self-review

Baseline: local integration commit `1e243310fa29c3a4a71920df463180947cc8954c`. No publication.

- `cancel-phase-assertion-red.log` reproduces stale requested-phase cancellation against waiting and already-cancelled outcomes for both handle forms. Stop-wins controls pass before correction. The earlier `cancel-phase-red.log` is a fixture compile error, not a valid red regression.
- `cancel-response-assertion-red.log` reproduces lost cancellation response and lost first outcome read, including persistent local fences after the durable cancellation.
- The correction retries only the single requested-to-waiting transition, so there are at most two cancellation writes. It reads durable state after an ambiguous result, reports success only after observing cancellation, and performs matching-run cleanup on repeated cancellation. The stopping boundary is unchanged.
- The public execution fixture now implements the exact supervised-process capability already present in production. No readiness guard was relaxed. `switch-corrections-first-race3.log` records the initial three-repeat green public execution/cancellation and deterministic cancellation matrix.
- Self-review moved store decoration before startup reconciliation, avoiding changes to a manager dependency after background startup. Final race evidence must include this test-fixture correction.
- Four service-backed negative controls reject missing exact capability, probe failure, a replacement generation and an absent target despite a visible ready composer. They preserve the input fence and do not perform extra teardown or launch. `readiness-negative-race.log` passes under race.
- Remaining verification: repeated final matrix, existing stale-retry/stop-boundary/reopen and queue suites, actual direct/fallback processes, affected full packages, build/vet/lint, protected audit and independent review. Synthetic provider frames do not establish live-provider acceptance.

## Final bounded verification

The first complete verification passed the repeated matrix, real runtime/reopen/ownership schedules, full daemon and manager race suites, full account/Chat/session/store/runtime-selection race packages, focused public controller/CLI races, backend build/vet, and tagged manager vet. Lint then requested tagged switches in two new test functions. The post-lint rerun passed the repeated matrix, real processes, full affected daemon/manager packages, build/vet/tagged vet, and changed-scope lint (zero issues). The other package sources and tests did not change after their green results. Independent review remains required for S4.

All 91 integrated protected paths match the P0 manifest. The original protected manifest and P0 archive digests remain unchanged. Two historical protected differences are upstream-only, as recorded in the P0 audit. No frontend, runner, generated contract, lifecycle containment, or protected source was changed by this four-file correction.
