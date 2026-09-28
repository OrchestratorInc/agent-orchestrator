# Review request: runner responsiveness measurement

Status: bounded local checks complete, independent review requested. Test-only slice, no production behavior or protected source changes. G15 and broader release acceptance remain open.

Base: `dbb33545351c8916dda20bdc316e4388cb67c35a`.
Frozen tree: `2d4e69bbd00199f87e6491420e27d90e857a170a`.
Artifact root: `/var/tmp/pr-5769-next-79.wos4rh/responsiveness-freeze`.

## Scope and integrity

Two test files: `accounts-manager/runner/internal/runner/responsiveness_metrics_test.go` and `accounts-manager/runner/internal/runner/responsiveness_test.go`. The archive includes `NEXT-RUNNER-RESPONSIVENESS.md`; this handoff and ledger update remain outside the frozen tree. The plan's P4/P5 local integrity and handoff obligations are completed by this record, without claiming independent acceptance.

| Manifest | Files | SHA256 |
| --- | ---: | --- |
| `correction.sha256` | 2 | `7d618cb4264388084678cd726eaa61d8c84070ffa8a574f73e54d8371c399380` |
| `integrated-source.sha256` | 5716 | `2d1bdd3ea7730e5aeb293626c12be2337d11e4823113b59e9102765cb9adf963` |
| `plan.sha256` | 1 | `abcb3e46a7bdd710b98bba440f0f4bbadc6436a346efc5401111ce0536bd0aaf` |
| `protected.sha256` | 91 | `3438055a85087fe789dfb0aa5f2dc583ab52dac8a51bb683e0100647b096396f` |
| `generated.sha256` | 33 | `ccfb0a3263f7bf322af29bb35e880334d515572c15d3e01965d25317f2ca42dd` |
| `evidence.sha256` | 26 | `2433edd4030cd60b5a21df813d7a8df10dd9ff34b5dc417dbc00419700cee469` |

- `source.tar`: `8aa45b534be5d519f39de006b2f4eda8302fd3d7d58cae2033f8a4e1f97a7aff`
- `correction.tar`: `dcd2e0c703b2a5f1b22c43cb786222f00bbe563dddda2517050e25a798e58b9e`
- `correction.patch`: `833b1930e3933aa150bcde2a23c35ec638a42bcce7b63e86a64e217a7437dec9`

`SUMMARY.json` contains exact commands/logs, raw samples, independently recomputed distributions and integrity of all six prior seals. No generated drift or protected-byte changes. No visual evidence is applicable to these test-only files.

## Checks and review corrections

Evidence is in `/var/tmp/pr-5769-next-79.wos4rh`. `responsiveness-midpoint-red.log` shows completion-only and metadata-only responses were incorrectly accepted by the first measurement oracle. The correction requires the controlled upstream text delta before a sample can pass, and still consumes the whole stream. Controls cover rejected, empty, malformed, truncated and post-completion-error responses, plus signed paired differences and nearest-rank percentiles.

Midpoint review also strengthened blocked-route checks to require 401 and zero upstream requests, counted rejected upstream attempts, verified each of the 50 identities and asserted the complete streaming-request total. No retries or failed samples are discarded. One tagged-lint constant finding was fixed; final1 results are superseded.

`responsiveness-final2-results.json`: all 12 checks pass on unchanged test-source hashes. Exact commands are recorded rather than inferred from planned gates:

1. Oracle race x3, all cases executed.
2. Short real-runner race x3, each with 50 verified accounts, 20 bindings, 20 warm pairs, two restarts and exactly 86 streaming upstream requests.
3. Non-race measurement, 100 warm pairs and 30 real process restarts, 302 streaming upstream requests and zero failures.
4. Complete ordinary runner race suite.
5. Runner build.
6. Ordinary runner vet.
7. Performance-tagged vet.
8. Complete pinned runner lint, zero findings.
9. Performance-tagged lint, zero findings.
10. Windows amd64 tagged test cross-compile.
11. Mac arm64 tagged test cross-compile.
12. Mac amd64 tagged test cross-compile.

Go 1.27.1, credential-stripped invocation, GOMAXPROCS=2, Fish command host and explicit test-child SHELL=/bin/sh. No skipped selected cases. Cross-compiles do not establish native platform behavior.

## Measured result and limits

Additional first-delta latency: p50 0.527 ms, p95 1.288 ms over 100 paired direct/proxy samples. Private capability preparation: p95 0.253 ms. Reopened runner through authorized full response: p95 127.130 ms over 30 starts. First migration plus verification/admission: 308.055 ms, a separate single observation. Full-stream proxy p95 is 4.163 ms, maximum 29.677 ms, retained unchanged. The plan includes all distributions and raw-data locations.

This is a controlled local upstream, not live provider A/B evidence. Tokens and encrypted state are synthetic. Timing excludes warm binding-lease reconciliation and includes cold inventory/model readiness, a rejection probe, reconciliation and admission. Forced process stop occurs before the cold clock starts; the observation includes the existing 50 ms health-poll granularity. Measurements use the test process, not a release desktop build. Host hardware and volume details are preserved in `responsiveness-final2-machine.json`.

Review the exact timing boundaries, no-fallback identity oracle, negative controls, sample accounting, percentile recomputation and child cleanup. Return a bounded measurement verdict only. Complete route preparation, controller restart/reconnect, queue drain, UI feedback, guest/provider waits and reference desktop acceptance remain open. Managed Chat, containment, native platforms and independent integrated review are unchanged release gaps. Nothing was published.
