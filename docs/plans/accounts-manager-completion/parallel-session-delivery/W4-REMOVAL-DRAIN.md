# W4 account-scoped removal drain

Continue the removal and credential lifecycle obligations without changing runtime ownership, native switching, public contracts or the sealed containment work. The W4 usage archive stays immutable. A new exact correction will supersede any overlapping live files with separate evidence.

## Design review before implementation

`credentialRuntime.Remove` currently erases the credential and returns without joining an in-flight refresh. Usage cancellation also does not join account workers. The existing shutdown join protects vault closure, but it does not establish account-scoped removal completion.

Use a durable, reserved removal operation in the existing encrypted vault operation journal. The `removing` state fences request admission and every writer before cancellation/drain. It keeps the encrypted record available for retry resolution until the final deletion commit. Startup preserves the fence. Older readers already reject unknown operation states, so they cannot reopen a partially removed account as usable. Completed removal atomically clears the reserved operation with the existing deletion tombstone; no permanent file-format change is intended.

Track every pending usage worker by account, including superseded requests no longer in the visible cache. Cancel and join the selected account's refresh and usage workers outside the global credential mutation lock. Other accounts remain available. Recheck durable admission under each worker-registration lock so a stale caller cannot start work after the drain snapshot.

Midpoint review found the same missing drain in manual credential rechecks. Include these synchronous checks in account registration, cancellation and joins. Shutdown must cancel all worker kinds before waiting for any kind. Preserve completion-before-context-cancellation publication so a successful unrelated worker cannot report a false fence. These findings have separate reproduced failures, rather than being treated as speculative cleanup.

After a request deadline or interrupted drain, leave the durable fence and encrypted record intact for retry. Repeated removal is idempotent. Final credential erasure follows worker acknowledgements; no fallback, re-enable, reconnect, SDK write or stale callback may remove the fence. Do not claim remote requests already sent can be recalled.

## Acceptance and sequence

- [x] D1: failed-first refresh and usage cases prove early acknowledgement/erasure. Correction cancels and joins both, with zero later publication and unrelated B progress.
  EVIDENCE: `removal-red.log` and `recheck-red.log` reproduce early erasure. `final-focused.log` passes the three worker kinds, unrelated B, late results and shutdown sequencing in three race repetitions.
- [x] D2: registration races, superseded usage workers and concurrent retry cannot escape the drain. Wait-group/channel ownership and lock ordering pass midpoint review.
  EVIDENCE: abandoned-worker drain and ten concurrent retries pass. The paused, previously validated request has zero provider calls; removing its final guard in an external overlay fails with one call in `admission-negative.log`. The self-review records lock order and completion-before-cancellation ordering.
- [ ] D3: request cancellation, failed persistence, process restart and already-missing credentials preserve recovery. Reserved journal operations reject malformed evidence and cannot be cancelled/reused through ordinary login APIs.
  EVIDENCE: cancellation, two storage-failure phases, eleven malformed-marker cases, reserved-operation rejection, already-missing retry and vault/runtime reopen pass. Actual installed-process routing restarts pass separately. A pending-removal marker through abrupt runner-process death and fresh Serve construction remains the next local proof; in-process vault reopen alone is not labelled that evidence.
- [x] D4: every logical request and mutation rejects the removing account; final deletion is idempotent and no raw secret enters transport, diagnostics or persistent plaintext.
  EVIDENCE: writer/reconnect/replay/duplicate-key/verification and stale-admission cases pass. Private inventory remains disabled and resolvable until final erasure. Existing encrypted-store, redaction and request-admission suites pass in `final-runner-race.log`. Provider-bound HTTPS authentication retains its existing boundary; no credential-bearing management response or new transport is introduced.
- [x] D5: focused races three times, complete runner tests/races, backend boundary checks, build/vet/lint and preservation pass on the final correction. Freeze for independent review.
  EVIDENCE: 237 focused leaf executions, 293 complete runner leaves in normal and race modes, 2505 backend boundary leaf executions and 15 isolated installed-process scenarios pass. Native builds/vet, pinned lint, three target-platform test compilations, source/archive checks, 91 protected and 33 generated hashes pass. See the exact command table and final manifests in the review handoff.
- [ ] D6: independent review accepts the immutable removal correction and the pending-removal process proof.
  EVIDENCE: review request prepared through the orchestrator; no independent verdict observed.

Independent review, production process containment, real provider/native execution and broader release gates remain open. This slice cannot clear runtime stop/recovery obligations by itself.
