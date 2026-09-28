# Gates: bounded runner responsiveness

Base: `dbb33545351c8916dda20bdc316e4388cb67c35a`. This is a test and measurement slice. No runner production behavior, containment, availability, protected path or credential admission changes are authorized by its results. Existing review requests and release gates remain open.

## Contract and design review

Use an actual runner child, its private HTTP control routes and encrypted store, plus a controlled local streaming upstream. Seed and explicitly verify 50 synthetic accounts. Reconcile 20 explicit bindings and check exact upstream credential identity for every response. Use existing model-registration readiness so late startup refresh cannot be mistaken for an input-ready process.

Collect 100 warm capability preparations and 100 paired direct/proxy requests, alternating pair order. Record time to the first upstream data event and time to complete response separately. Read the full stream and require completion. Headers alone, errors, partial streams or retries must not become successful samples. Preserve raw paired measurements, including negative deltas from scheduling noise. Warm both connections explicitly. Reconcile the production binding lease outside each warm sample and disclose that exclusion.

Record first migration/startup separately. Measure 30 real process restarts against the existing encrypted store, beginning before child creation and ending after inventory/model readiness, reconciliation, capability preparation and a correct response. Before reconciliation, a previously valid route must reject without reaching upstream. The restart diagnostic includes this negative check; disclose it rather than subtracting time. Preserve unrelated bindings and demonstrate stale revisions, cross-account mismatch and blocked bindings cannot fall back.

Run a short correctness matrix with race detection separately from the full non-race measurement. A dedicated build tag keeps machine-sensitive timing out of ordinary tests. Never assert a product latency pass from a race-instrumented run. Capture toolchain, host hardware, process limits, sample counts and all errors. No live provider traffic or credentials are used.

The specification's first-event proxy target is 25 ms p95. Capability minting is only part of the 100 ms route-preparation boundary. Forced runner restart is not controller/desktop restart. These results cannot close G15: UI feedback, label/default persistence, complete child preparation, queue drain, controller recovery and guest/provider waits remain unmeasured. Release-build reference-machine evidence is still required.

## Scope

Own only new runner performance test files and this plan/handoff/ledger documentation. Preserve route-configuration and all prior seals. Midpoint review must inspect timing boundaries, sample accounting, exact identity oracle, request cancellation and child cleanup before the full measurement.

## Midpoint self-review

The first short real-runner matrix passed. Review then found that the measurement parser could count metadata or a completion-only response as its first useful upstream event. Two failed-first oracle cases in `responsiveness-midpoint-red.log` reproduce that false acceptance. The corrected parser requires the controlled text-delta event and still requires full completion, including rejecting a later error.

The negative-route check now requires HTTP 401, not just any stream error. The upstream counter includes rejected credential attempts, so an unauthorized request cannot disappear from the no-upstream assertion. All 50 explicit verification calls must reach the corresponding synthetic upstream identity. An exact total request count prevents skipped, extra or retried streaming requests from hiding in a successful sample set. Timing starts before request serialization and ends after body consumption, with first-delta arrival separately recorded.

## Acceptance

- [x] P1: the measurement oracle rejects partial/error streams and measures data arrival rather than headers, with distribution and signed-delta controls.
  CHECK: go test -tags=performance -race -count=3 -timeout=90s ./internal/runner -run '^TestRunnerMeasurementOracle$'
  EXPECT: /ok\s+.*\/internal\/runner/
  CWD: accounts-manager/runner
  EVIDENCE: `responsiveness-midpoint-red.log` preserves two pre-correction failures. `responsiveness-final2-oracle.log` passes all cases three times under race, exit 0. Fish command host, test-child SHELL=/bin/sh, Go 1.27.1, credential-stripping wrapper.
- [x] P2: short real-runner correctness covers 50 accounts, 20 bindings, exact upstream selection, restart admission and no fallback without changing protected bytes.
  CHECK: go test -tags=performance -race -short -v -count=3 -timeout=180s ./internal/runner -run '^TestRunnerResponsiveness$'
  EXPECT: /runner responsiveness correctness passed/
  CWD: accounts-manager/runner
  EVIDENCE: `responsiveness-final2-correctness.log`, exit 0, three real-runner race repetitions with 20 warm pairs and two restarts each. Every run verifies all 50 accounts and executes exactly 86 streaming upstream requests. Blocked/unreconciled routes return 401 without upstream traffic, mismatched/stale preparation returns 409, unrelated bindings still work.
- [x] P3: the non-race experiment records at least 100 warm samples and 30 actual restarted-store samples, first-event/completion distributions, failures and machine details.
  CHECK: go test -tags=performance -v -count=1 -timeout=240s ./internal/runner -run '^TestRunnerResponsiveness$'
  EXPECT: /runner responsiveness measurement complete/
  CWD: accounts-manager/runner
  EVIDENCE: `responsiveness-final2-measurement.log`, exit 0, 100 warm pairs, 30 real process restarts, 302 exact upstream requests, zero failures and no skipped cases. Raw samples and distributions are emitted together. Machine details are in `responsiveness-final2-machine.json`. This diagnostic is not a release desktop measurement.
- [ ] P4: midpoint review, complete runner race/build/vet/lint, tagged checks, prior-seal integrity and protected/generated preservation pass on the final source snapshot.
  EVIDENCE: pending; record exact final commands and hashes before declaring this manual integration gate met.
- [ ] P5: exact source/test/evidence seal and independent handoff report distinguish bounded measurements from unmet product/native/live-provider acceptance.
  EVIDENCE: pending.

## Observed bounded timings

Final evidence root: `/var/tmp/pr-5769-next-79.wos4rh`. `responsiveness-final2-results.json` records all 12 verification commands passing, with unchanged hashes of both test files: repeated oracle/correctness races, non-race timing, complete ordinary runner race, build, ordinary/tagged vet, ordinary/tagged pinned lint and three platform test cross-compiles. Final1 is superseded after one tagged-lint constant correction. No production source changed.

| Boundary | Samples | p50 ms | p95 ms |
| --- | ---: | ---: | ---: |
| Private capability HTTP preparation | 100 | 0.208 | 0.253 |
| Direct first upstream delta | 100 | 0.106 | 0.135 |
| Proxy first upstream delta | 100 | 0.634 | 1.396 |
| Paired additional first-delta latency | 100 | 0.527 | 1.288 |
| Direct full stream completion | 100 | 0.109 | 0.138 |
| Proxy full stream completion | 100 | 3.557 | 4.163 |
| Reopened runner to authorized response | 30 | 125.382 | 127.130 |

The first migration plus all verification/admission takes 308.055 ms once, outside the reopened-store distribution. Host: Linux 6.19.6, six physical CPU cores/twelve logical CPUs, about 32 GB RAM and SSD-backed artifact storage. Each Go process is limited to GOMAXPROCS=2. The startup observation uses the existing 50 ms health poll plus model-registry barrier, includes an unadmitted-route negative request, and ends after a complete authorized response. Forced process stop happens before the clock starts. Warm lease reconciliation happens before each preparation/stream sample. No time is subtracted from cold observations and no failed sample is discarded.

Only the controlled gateway component is below the 25 ms first-event target on this host. Complete route preparation, real controller restart/reconnect, desktop feedback and guest/provider waits remain open. The full-stream maximum was 29.677 ms and is retained in the raw data; it is not clipped or confused with first-event latency. G15 is not cleared by these results.
