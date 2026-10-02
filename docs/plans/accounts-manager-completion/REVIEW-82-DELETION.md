# Coordinated deletion freeze for reviewer 82

Disposition: locally verified and frozen for independent review. Public session controls and UI remain held until reviewer 82 returns CLEAR. No commit, push, PR edit, publication, or real-account deletion was performed.

R4 was independently cleared in `rpt_3b54e7fc-bd40-4e04-951f-edc73173d20f`. Its four correction files still match the historical delta manifest. This handoff covers the next deletion slice, not whole-PR completion.

## Implemented boundary

- Enumerate every positive managed binding, including inactive sessions and dormant provider records. Native rows with an empty account identifier are unrelated and remain untouched. No identity is inferred from labels or native credential files.
- Preserve queues and reserve affected controller/input gates. The durable requested phase remains cancellable until a validated stop CAS wins. Revalidate admission before irreversible steps; reject cancelled and superseded work.
- Reconcile runner revocation, stop every captured owner, and persist acknowledgements before credential removal. Exact-generation proof is required before every destructive retry. Positive replacement acknowledges only departure of the recorded owner; the replacement survives. Unknown or coexisting ownership holds recovery.
- Persist a non-secret host-capability fingerprint against the managed Chat controller generation. Cold teardown uses exact host ownership and a captured capability. Missing identity or any unpublished launch lock holds recovery. Existing broad native shutdown stays unchanged.
- Remove the vault credential idempotently, then atomically remove active bindings, clear only the matching default, and preserve a blocked deleted-choice record in SQLite. No automatic replacement account or relaunch is permitted. The two stores use a durable blocking saga, not a claimed physical cross-store transaction.
- Restore fences after SQLite reopen without automatic destructive work. Explicit retry resumes the recorded obligation. A lost credential-removal response is recoverable even when the credential is already absent.

The final cancellation correction makes cleanup depend on successful durable cancellation, before a fallible response read. The regression reopens SQLite and pauses a cached retry in both handle forms. After cancellation commits and its response read fails, only that operation's fences are released. Restoring reads cannot admit the stale retry; an unrelated deletion remains fenced. No stop, interrupt, launch, revocation, or completion replacement occurs.

## Failed-first and review evidence

[DELETION-REVIEW.md](DELETION-REVIEW.md) records the midpoint and final adversarial reviews, including exact-owner retries, coexisting generations, stale cancellation admission, dormant/native preservation, queue preservation, and unpublished-host uncertainty.

Important red artifacts are preserved:

- `/tmp/pr-5769-deletion-red-79.log`: missing coordinator, inactive impact, cancellation, and recovery foundation.
- `/tmp/pr-5769-deletion-takeover-red-79.log`: unsafe or repeated teardown after an error.
- `/tmp/pr-5769-deletion-mixed-probe-red-79.log`: coexisting owners incorrectly classified as replacement.
- `/tmp/pr-5769-deletion-cancel-red-79.log`: cached retry after durable cancellation.
- `/tmp/pr-5769-deletion-dormant-red-79.log`: unrelated native intake fenced by a dormant binding.
- `/tmp/pr-5769-deletion-host-death-red-79.log`: dead host with a leftover lock incorrectly treated as absence.
- `/tmp/pr-5769-deletion-cancel-response-red-79.log`: committed cancellation retained local fences after a response-read failure.

Fixture-only failures are not product regressions: Chat queue cancellation needed the provider turn identifier that the fixture had omitted; finalization needed a valid worker session kind. The original queue invariant and all present, already-missing, and lost-response finalization cases remain intact.

## Final verification

All results below are after the final source correction. Fish, Go 1.27.1, `GOMAXPROCS=2`, serial Go package execution, the reviewed environment sanitizer, private tmux state, and scratch `/var/tmp/ao79.uF9PZF` were used. The runner is a separate module and requires `GOWORK=off`.

- All five executable acceptance gates passed three-repeat race checks. [DELETION-GATES.md](DELETION-GATES.md) retains the exact commands and current evidence. D5 records the manual freeze audit. The gate-run log initially exits 1 only because D5 was deliberately still open, not because a runnable check failed.
- Runner build, vet, and its complete race suite passed. Full race: 4.407s. Logs: `/tmp/pr-5769-deletion-runner-{build,vet,full-race}-final-79.log`. An initial build attempt without `GOWORK=off` failed before compiling; the corrected run is the accepted evidence.
- Runner restart/revocation proof passed 21 test executions, no skips, 3.619s: `/tmp/pr-5769-deletion-runner-proof-final-79.log`. It launches actual runner subprocesses against local synthetic upstreams and verifies account isolation, durable removal, restart admission, and old-token refusal.
- Backend-wide build and vet passed, as did tagged session-manager vet. Logs: `/tmp/pr-5769-deletion-backend-{build,vet}-final-79.log` and `/tmp/pr-5769-deletion-e2e-vet-final-79.log`. Successful static checks emitted no diagnostics; their exit statuses were observed.
- The complete affected-package race run passed all 13 tested packages. Log: `/tmp/pr-5769-deletion-full-race-final-79.log`.
- The complete production-process suite passed 105 scenario executions (35 scenarios, each repeated three times), including 18 deletion cases, with no skips, failed tests, or race reports. Duration 308.719s. Log: `/tmp/pr-5769-deletion-actual-process-final-79.log`.
- Pinned deletion-scope lint passes with zero issues: `/tmp/pr-5769-deletion-changed-lint-green-79.log`. The correctly normalized backend-relative patch first exposed 18 findings, all narrowly fixed. The earlier repository-relative filter's zero result is superseded and is not evidence.
- `npm run api` and `npm run sqlc` were rerun after the final source edits. All 32 generated files match their captured pre-generation hashes. Logs: `/tmp/pr-5769-deletion-{api,sql}-generate-final-79.log`.
- Deletion-scope `gofmt -l`, `git diff --check`, all source-manifest checks, the R4 four-file check, and all 91 protected-path comparisons passed.

| Full race package | Duration |
| --- | --- |
| `internal/session_manager` | 208.487s |
| `internal/service/chat` | 173.947s |
| `internal/service/accountsmanager` | 14.814s |
| `internal/accountsmanager` | 6.062s |
| `internal/storage/sqlite` | 522.098s |
| `internal/storage/sqlite/sqlitetest` | 13.211s |
| `internal/storage/sqlite/store` | 114.822s |
| `internal/adapters/runtime/tmux` | 7.153s |
| `internal/adapters/runtime/runtimeselect` | 1.010s |
| `internal/adapters/runtime/conpty` | 8.564s |
| `internal/adapters/chatdriver/acp` | 32.820s |
| `internal/adapters/chatdriver/persistenthost` | 23.932s |
| `internal/daemon` | 17.737s |

The final backend race command uses `go test -mod=readonly -p=1 -race -count=1 -timeout=900s` over the packages listed above, with `./internal/storage/sqlite/...` also including the generated package, which has no tests.

The final process command is:

```text
go test -mod=readonly -p=1 -race -tags=e2e -count=3 -timeout=600s
  ./internal/session_manager
  -run '^TestAccountsManager(SwitchReal(Production|ExitedSupervisorPane|ReservedTargetSurvivesColdRecovery)|RemovalReal)' -v
```

Forced native-host failure warnings select the fallback fixture intentionally. Context-cancelled queue-delivery warnings occur during fixture cleanup; the recorded test results remain passing.

Real-runtime deletion tests exercise production dependency construction, supervised direct/fallback processes, live owners, retained panes, post-error replacement, SQLite reopen, and repeated retry. Account B and native processes survive. Their vault outcomes are injected. The runner authorization proof is a separate actual-process integration test. This is not a claimed single desktop-to-live-provider end-to-end test.

## Exact source freeze

Worktree: `/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79`.
Branch: `ao/agent-orchestrator-79/accounts-manager`.
HEAD: `048a59775999b60f8276a1f5d1107dbef57f5483`.

- Deletion-only delta: `/tmp/pr-5769-deletion-delta-79.sha256`, 42 files, SHA256 `d2a46585d10558183c6f0cca43c1c0bb4fc71a4652dbf589846a5eb75d72ae44`.
- Integrated source: `/tmp/pr-5769-deletion-source-79.sha256`, 1,802 files, SHA256 `1464a9b69e4a2c60994920e706efa1f6eb290e880c0eb96faccd20cb9726a513`. It matches the pre-verification snapshot byte-for-byte.
- Source archive: `/tmp/pr-5769-deletion-frozen-source-79.tar.gz`, SHA256 `b965957649932cbbd0f3019eba63e5fb10f849bb4b97c2c9728384792e88369f`.
- Protected inventory: `/tmp/pr-5769-deletion-protected-79.sha256`, 91 files, SHA256 `462fd9b445fe2d6764f6ac0d8371aedb742aed22071bbb4afb2e6b943fca6358`. All match baseline `b398a95c59425c381ec2f3d36d507097c9ccc2de`.
- Delta provenance and audit: `/tmp/pr-5769-deletion-integrity-79.json`, SHA256 `fb7cf8db4ba25278442c1e72819653ec5141c5d0d9d3b9f50da9e5847584de8a`. Reproducible read-only audit: `/tmp/pr-5769-deletion-audit-79.cjs`.
- Verification counts, generated-file hashes, and checked manifests: `/tmp/pr-5769-deletion-verification-79.json`.
- Final reports and test-artifact hashes: `/tmp/pr-5769-deletion-evidence-79.sha256`.

The original pre-deletion archive remains `/tmp/pr-5769-pre-deletion-79.tar.gz`, SHA256 `3a8d75672aeac68bce875380b25c3e808a052e15b82533646a88af8d9fc48f3a`. Historical R1-R4 evidence was not overwritten.

The earlier 44-file candidate incorrectly counted two inherited retained-pane files as new deletion work. Their earlier introduction is documented in REVIEW-82-CORRECTIONS.md. They are included in the integrated source snapshot but excluded from the 42-file delta. No missing historical byte baseline for those two files is invented. Earlier snapshots preceding the final cancellation assertion or lint corrections are superseded.

## Independent review request and remaining gates

Reviewer 82: review this exact snapshot read-only and return CLEAR or bounded findings. Focus on durable cancellation versus cached retry, pre-stop cleanup despite a lost response, exact-owner stop/retry in both slots, immutable cold-host identity, dormant/native preservation, runner revocation before vault removal, cross-store crash recovery, and atomic no-fallback cleanup. Confirm the prior R4 protection still holds. Keep all review output local.

Stop at this freeze. Public session routes/CLI, initial/session controls, desktop interaction and coexistence, supported-platform runtime execution, live-provider workflows, and measured responsiveness remain release gates. Other-platform compilation from earlier slices is not runtime evidence. Ambiguous pre-coordination journals lacking a captured provider remain fail-closed; this migration does not invent ownership for them.

Full-branch test/lint and release readiness are not claimed. Historical HTTP shutdown flakiness, the prior full-branch lint findings, and the baseline engine media-test failure remain recorded outside this slice. There is no new visual surface here, so screenshots are not applicable to this backend freeze. Real desktop evidence is still required before product completion.
