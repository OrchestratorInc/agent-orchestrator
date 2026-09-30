# Cold retry outage review

Evidence root: `/var/tmp/pr-5769-cold-switch-79.2u9WV2`. Published baseline `b0378b674ac9e543a31b7673549360d15046683d`; inventory-ordering changes remain a separate frozen slice.

## Failed-first and initial green

`outage-red.log` contains eight assertion failures: both source handle forms, requested/waiting phases, and eventual retry/cancel choices. Each became terminal failed with TARGET_UNAVAILABLE after the first recovered validation error. Compilation succeeded; an earlier shell-quoting mistake did not execute a test and is not red evidence.

The retry worker now reports TARGET_REVALIDATION_UNAVAILABLE. Only requested/waiting finalization maps that code to waiting, using the existing phase compare-and-swap. This keeps recorded intent and intake fences while exposing the existing retry/cancel capabilities. Requested moves forward to waiting with no stop/launch permission. Initial execution still uses TARGET_UNAVAILABLE and retains its existing failure semantics. Post-stop phases remain recovery-required.

`outage-green.log` passes the eight scenarios three times under race detection, including two SQLite reopens and repeated unavailability per scenario before explicit recovery.

## Midpoint review

- No target/runtime action occurs after validation failure. There is no inferred account choice, automatic retry, historical terminal repair or new durable schema.
- Error publication uses the observed phase as its compare-and-swap condition. Add deterministic controls for cancellation while validation is pending and after finalization read but before its write, so neither a late error nor a stale retry can replace cancellation.
- Add historical failed/cancelled controls and a stopping-phase control. A zero target revision alone must never soften a durable stop boundary.
- Verify actual direct/fallback processes survive outage without input, interrupt or teardown. These use production runtime construction with synthetic output; they do not certify provider/vault cold reconstruction or live billing.
- Existing initial-start failure, ownership, queue, shutdown and cancellation suites remain regression obligations. Linux containment and all prior immutable manifests remain outside the edit scope.

`midpoint-controls.log` passes all 19 deterministic schedules three times under race detection. Cancellation remains durable after both barrier schedules and another SQLite reopen; historical failed/cancelled rows are rejected, and a stopping-phase outage remains non-cancellable.

`process-first.log` passes four production direct/fallback schedules three times under race detection with no skips. A live supervised source survives two cold manager/SQLite reconstructions and revalidation outages without interrupt, input, destroy or launch calls. Cancellation preserves that source. Explicit retry with a recovered target interrupts exactly once and launches the chosen account without replaying the saved task.

## Public status boundary

The existing response omits durable operation error codes. That public projection gap is under separate review and is not corrected here. The new error remains in the durable journal; this slice preserves the existing phase and retry/cancel capability contract without changing DTOs, API generation or renderer behavior.

## Final verification and handoff

Final focused race coverage passes all 19 deterministic schedules three times. The broader switch/cancel/handoff/recovery selection passes three times (204.127s). Full domain, SQLite store, session service, Chat service and session-manager race suites pass; the full session-manager package completes in 218.656s. Backend build/vet, tagged vet and both changed-scope lint checks pass. The explicit patch lint includes the two new untracked test files. No source edits occurred during these final checks.

The preservation audit verifies four cold source files, three cold documents and a separate inventory review addendum. The inventory seal, previous first-slice source/design seal, 91 protected paths and nine containment files match their recorded bytes. Generated API/SQL artifacts and runner dependencies are unchanged. The final external seal-report.md and cold-retry-v1-summary.json record exact immutable manifests, archive hashes, logs and commands.

Request independent review of the four-file cold source archive, focusing on pre-stop phase preservation, cancellation/error-write linearization and post-stop refusal. Initial-start target failure semantics remain unchanged. Historical terminal repair, full runner/vault cold recovery, public failure-code projection, settings-generation fencing, platform containment and live acceptance remain open. Independent acceptance of this slice is still required; the inventory-ordering CLEAR is recorded separately in INVENTORY-RECOVERY-CLEAR.md. No publication.
