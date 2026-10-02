# W4 removal drain: review handoff

Local only. Independent review is required; this record does not clear the product removal, containment or release gates. Evidence root: `/var/tmp/pr-5769-w4-drain-79.S6e2Ns`. Final immutable paths and hashes will be recorded in `final/FREEZE.md` after verification.

## Correction

Removal commits a reserved `removing` operation in the encrypted vault before cancelling work. Logical admission, persistence, reconnect and replay reject that account. Inventory retains a disabled, resolvable record until erasure so the daemon cannot mistake unfinished removal for an already-missing credential. Startup preserves and validates the exact account/provider/generation marker.

Removal cancels and joins refresh, usage and manual recheck workers for that account outside the global mutation lock. The usage registry includes abandoned/superseded workers outside the visible cache. Registration rechecks the fence under its worker lock. Final deletion clears the encrypted record and reserved marker atomically with the existing deleted-generation tombstone. Cancellation during drain preserves recoverable state; cancellation before admission changes nothing. Repeated removal does not rotate the tombstone again.

No native switching, subscriptions, runtime containment, public API or generated schema changes are included. The earlier usage archive stays immutable; its live runtime, refresh and quota files are superseded by this correction. New sign-in/import operations, routed execution leases and process retirement have separate existing owners and are not reclassified as usage workers here.

## Failed-first and midpoint evidence

- `removal-red.log`: refresh and usage deletion returned before worker cleanup and erased the encrypted record early. Pre-correction archive `pre-fix-source.tar.gz`, SHA256 `61d9a6521733645275506fc4cbea5ae3c7206323381c35947aa2462ffd607d51`; log SHA256 `f558cea96acd09485d7e9a6e2920d06a7e34b3c65eed955de14695a23df45cb9`.
- `recheck-red.log`: midpoint review reproduced the same missing drain in manual verification.
- `removal-recheck-green.log` is a failed intermediate run despite its original filename. It caught one unrelated B worker reporting a false fence because context cancellation preceded completion publication. `removal-ordering-green.log` passes the three-kind table ten times after correcting that ordering.
- `shutdown-order-red.log`: shutdown joined a blocked recheck before cancelling usage. The correction requests every worker kind's stop before any join.
- `removal-recovery-first.log`: journal/restart, reserved operations, malformed proof, persistence failure and concurrent retry table passes three race repetitions.
- `admission-schedule-first.log`: a test-only serialization barrier pauses a usage request after validation and before registration. Deletion completes, then the stale request resumes with zero provider calls. The three-repeat race run passes. `admission-negative.log` deliberately removes only the locked revalidation in an external Go overlay and fails with one forbidden provider call. The live source was not weakened for this negative control.
- The first complete runner run exposed the existing verification fixture's circular wait: synchronous removal waited for cleanup, while the fixture released its request only after removal returned. The new schedule must observe cancellation, release its deliberately late successful response, then join removal. It must retain the no-late-verification assertion. That failed run is diagnostic, not final evidence.

## Self-review

Worker registration locks may take the vault lock. Removal releases the vault and global mutation locks before taking worker locks, then joins without holding them. Each registration either precedes the drain snapshot and is included, or observes the durable fence. Clearing the marker cannot reopen admission because final deletion sets the existing tombstone in the same commit.

Usage completion unregisters a worker only after provider response/body cleanup and admission checks. It closes the completion channel before cancelling its context under the same worker lock. Refresh joins include synchronous SDK persistence, and the vault still rejects ignored SDK persistence failures. Manual recheck joins include its verification write attempt. Unrelated account requests and mutation locks are not held for A's network cleanup.

Reserved operation IDs cannot be cancelled or reused through ordinary creation/login. Recovery rejects incomplete or conflicting account/provider/generation evidence, including a reserved marker relabelled as cancelled. The encrypted format version is unchanged. An older reader rejects the unknown in-progress operation state; final deletion removes that state. No acknowledgement promises that a remote request already sent can be recalled.

## Final verification

Final source manifest has 13 entries, SHA256 `8402b6eca104c15dddec549623438f2e0869b8188c28a3a0a561a7154d19d6d8`. The source archive has SHA256 `50ca3d985595dab3d95100ed38217509ebbcfb717636069d058b8e259f7c3b39`. The archive, manifest and exact `source.list` are under `final/` in the evidence root.

Every command below completed after the last source edit with exit 0. `verify.fish` records serial commands, environment and `.status` files. Go 1.27.1, pinned lint v2.13.2 and the existing credential-stripping Node wrapper are used. `GOWORK=off`, session-owned temporary storage, `GOMAXPROCS=2`, and `SHELL=/bin/sh` are supplied to the test subprocesses. Source hashes match before and after all checks, and all 13 archive members match the final manifest.

| Scope and command | Evidence | Result |
| --- | --- | --- |
| Runner `go test -mod=readonly -json -race -count=3 -timeout=180s ./internal/runner -run '^TestCredential(Removal\|Quota\|Refresh\|AutoRefresh\|VerificationLifecycle)\|^TestRunnerParallelSessions'` | `final-focused.log` | 237 leaf executions, 31.062s, no skips. |
| Runner `go test -mod=readonly -json -count=1 -timeout=180s ./...` | `final-runner.log` | 293 leaves, 11.151s. |
| Runner complete race, same command plus `-race` | `final-runner-race.log` | 293 leaves, 15.336s. |
| Runner `go build -mod=readonly ./...`, `go vet -mod=readonly ./...`, pinned full lint | `final-runner-{build,vet,lint}.log` | All pass; zero lint issues. |
| Runner `go test -mod=readonly -c ... ./internal/runner`, Windows amd64, Mac amd64/arm64, `CGO_ENABLED=0` | `final-compile-*.log` | All compile. No native execution claim. |
| Backend `go test -mod=readonly -json -race -count=3 -timeout=180s ./internal/accountsmanager ./internal/service/accountsmanager ./internal/httpd/controllers` | `final-backend-boundaries.log` | 2505 leaf executions; package times 16.254s, 19.263s, 150.244s. No skips. |
| Backend `go test -mod=readonly -tags=e2e -json -race -count=3 -timeout=240s ./internal/adapters/agent/codex -run '^TestManagedRouteInstalledParallel$'` | `final-installed-process.log` | 135.249s; 15 embedded actual-process scenarios, no skips. |
| Backend `go build -mod=readonly ./...`, `go vet -mod=readonly ./...` | `final-backend-{build,vet}.log` | Both pass. |
| Source/archive checks, `git diff --check`, preservation | `final-source-{before,after}.log`, `final-archive-check.log`, `final-*.log` | 13 source entries; 91 protected, 33 generated, nine retirement, 25 W2 integrated, five guest and three installed-proof entries unchanged. |

The command-only runner package has no tests; there are no skipped individual runner cases. `verification-summary.json` records exact counts and package outcomes. The installed executable and production runner built inside its fixture are not race-instrumented; the Go fixture is. The separate full runner race suite supplies runner instrumentation.

`before-admission/` retains earlier passing full checks before the last test addition. `verify-final-remainder.fish` was prepared but not executed and is not acceptance evidence. `full-runner-fixture-timeout.log` preserves the 180-second failed schedule before its fixture correction. The old runner-lifetime archive and all earlier source archives remain unchanged.

API/SQL artifacts were not changed; their 33-file preservation manifest passes. No frontend behavior was edited, so frontend rebuilds and desktop captures were not part of this bounded verification. Full backend release suites were not substituted with the selected package runs.

## Review request and remaining gates

Review account-specific drain ownership, stale registration, cancelled request/restart recovery, final erasure ordering, reserved marker compatibility, response publication and unrelated B preservation. Review the exact final archive, not an older live-file manifest that overlaps this correction.

D3 remains open for an abrupt runner-death/fresh-Serve proof while removal is durably pending. The existing vault/runtime reopen and separate routing restart controls are recorded precisely, not combined into a claim they did not test. That disjoint next proof can proceed without editing these 13 frozen files.

Real provider accounts/positive usage, production containment, all three existing positive retirement groups, native Mac/Windows execution, managed Chat/profile support, interactive desktop evidence and final integrated release verification remain open. Synthetic transports and cross-compilation cannot clear those gates. This backend-only slice has no visual behavior requiring new screenshots.

Read-only PR inspection still reports draft, conflicting with main, review required, and no checks on its currently published head `04e12ca3dc78ba96674a63c2039ac6b0b271b52f`. That head is an ancestor of local `ec7efde0652f21690d8e98e2dcfa71d5b132e422` by 47 commits, with no remote-only commits. No commit, push, rebase or PR edit was performed for this correction.
