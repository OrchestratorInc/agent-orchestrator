# Next switching correction

Scope: the reproduced public execution readiness failure and intermittent pre-stop cancellation conflict. Preserve exact-owner runtime checks, durable stop boundaries, queued work and the reviewed cancellation/retry protections. No change to protected native switching or subscription modules.

## Decisions before implementation

1. The public execution fixture lacks `ExactSupervisedProcessInspector`, while the production hybrid runtime exposes it. Correct the fixture only after checking the concrete failure path. Do not replace exact child-generation proof with generic liveness or a visible composer. Add negative controls for missing capability, failed probe and mismatched generation.
2. Cancellation currently reads a phase and then advances that exact phase. A concurrent requested-to-waiting transition can reject cancellation even though neither state has begun stopping. Reproduce the interleaving using barriers and the real SQLite store before correction. Cancellation must still lose once stopping has durably begun. Avoid an unbounded retry loop or widening the irreversible boundary.
3. Retain real direct/fallback runtime coverage. A corrected fake proves a public-service contract, not native process readiness or live-provider behavior.

## Acceptance

- [ ] S1: public managed/native switches acknowledge the exact ready generation, while missing capability, unknown ownership and stale generations remain fenced.
  CHECK: go test -v -race -count=3 -timeout=180s ./internal/daemon -run 'TestAccountsManagerControlProductionExecution|TestAccountsManagerControlReadiness'
  EXPECT: /ok\s+github\.com\/aoagents\/agent-orchestrator\/backend\/internal\/daemon/
  CWD: backend
  EVIDENCE: red preserved in `published-blockers-red.log`; correction pending. Confirm each new negative control executes.
- [ ] S2: deterministic cancellation-versus-waiting regression succeeds without stopping, launching or rotating the binding; stop-wins and cancelled-retry controls remain strict.
  EVIDENCE: pending failed-first interleaving, repeated cancellation, SQLite reopen and lost-response classification.
- [ ] S3: affected manager/store/service/router race tests and actual direct/fallback process checks pass, with no saved-task replay or queue loss.
  EVIDENCE: pending commands after correction identifies the precise affected scope.
- [ ] S4: self-review, protected-path audit and independent review cover one exact source/evidence snapshot.
  EVIDENCE: pending immutable correction manifest and reviewer verdict. No dependent safety gate closes on fixture tests alone.
