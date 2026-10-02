# Deletion midpoint review

State: final verification passed; the source is frozen in REVIEW-82-DELETION.md. No independent deletion clearance. No public controls or publication.

## Findings corrected

- Runtime slots were destroyed again after confirmed absence. The red table and post-error takeover schedules reproduced the issue. Deletion now probes the captured generation immediately before each destructive call, skips confirmed absence, acknowledges a positively replaced recorded owner without touching the replacement, and holds on uncertainty. Both handle forms, repeated retry, and database reopen passed three race-tested runs.
- The terminal adapter used the replacement reason for coexisting supervisor generations. A dedicated negative test failed before correction. Full process-tree inspection now distinguishes ambiguity from positive replacement. Switching still sees unknown liveness and retains its prior fail-closed behavior.
- A retry could cache requested state, lose to cancellation, and install a worker after cleanup. The deterministic SQLite-reopen regression failed in both handle forms. Admission now rereads the journal under the cancellation lock. Cleanup keeps the running flag until fence release finishes. Durable stop admission is repeated before each affected-session teardown and finalization.
- A dormant binding fenced its session's unrelated active native controller. A dedicated regression reproduced the intake restriction. Only positively associated controllers are reserved or stopped; dormant bindings remain in the deletion inventory and final cleanup. A different active provider can continue without changing its native binding.
- Session-level stop acknowledgements can cover several provider bindings. The coordinator now stops every matching entry for a session before persisting that session's acknowledgement.
- A dead persistent host with an unpublished lock was mistaken for confirmed absence. A child can outlive its parent, so any remaining launch lock now holds deletion. The failed-first test and three-repeat host suite cover both live and dead lock owners.
- A successful cancellation followed by a response-read failure retained local fences. The failed-first test is `/tmp/pr-5769-deletion-cancel-response-red-79.log`. Cancellation marking and cleanup now depend on the committed CAS, before reading the response. Expanded direct/fallback schedules reopen SQLite, pause a stale retry, inject the response failure, restore reads, and prove the cancelled state persists with no stop, interrupt, launch, or finalization. Only the cancelled run's fence is released; an unrelated pending deletion stays fenced.

## Persistent Chat ownership decision

The existing broad provider shutdown targets a reusable session key. Deletion uses a separate exact-owner path. The authenticated attachment exposes only a SHA-256 fingerprint of its random host capability. A managed-only table records that non-secret fingerprint against the controller generation before publication. It is never a login credential, bearer token, public DTO, or log field.

Cold shutdown compares the persisted fingerprint with the current private descriptor before sending any command. A positive replacement survives untouched. Missing identity with a present host, malformed state, or any unpublished launch lock holds recovery. No-label and no-session-ID inference is permitted. A live controller must also match the persisted fingerprint. Existing native shutdown callbacks and credential paths are unchanged.

## Evidence so far

- Exact-owner red: `/tmp/pr-5769-deletion-takeover-red-79.log`.
- Exact-owner three-repeat race pass: `/tmp/pr-5769-deletion-takeover-green-79.log`, 23.971s.
- Mixed-generation probe red: `/tmp/pr-5769-deletion-mixed-probe-red-79.log`.
- Cancelled cached retry red: `/tmp/pr-5769-deletion-cancel-red-79.log`.
- Expanded three-repeat controller pass: `/tmp/pr-5769-deletion-expanded-green-79.log`, manager 36.225s and Chat 15.420s. Includes all six restart cuts, switch/delete admission orders, and stop/cancel queue cases.
- Actual persistent-host attachment, reattachment, replacement survival, and missing-descriptor/live-lock cases passed in `/tmp/pr-5769-deletion-chat-expanded-79.log`. That earlier combined run also contains the failed Chat cancellation fixture; its missing provider turn identifier was corrected and the unchanged queue assertion subsequently passed in the expanded run above.
- Dormant/native intake red: `/tmp/pr-5769-deletion-dormant-red-79.log`; the corresponding three-repeat manager pass is in `/tmp/pr-5769-deletion-service-dormant-79.log`. The service half of that combined run failed a fixture schema constraint. Adding the required worker session kind corrected only the fixture.
- Finalization with present, already-missing, and removed-but-response-lost credentials passed three times under race in `/tmp/pr-5769-deletion-service-chat-green-79.log`: service 15.721s, Chat 16.684s. The latter includes cold identity persistence and the unchanged stop/cancel queue assertions.
- Eighteen actual production-selected runtime cases passed in `/tmp/pr-5769-deletion-real-79.log`, 82.602s. Both direct and fallback slots cover retained panes, live owners, and post-error replacement, followed by SQLite reopen and repeated retry. Unrelated account B and native processes remain alive. Vault outcomes are injected, not live provider evidence. This earlier manager result requires final revalidation after the cancellation correction.
- Migration, store, and persistent-host three-repeat race checks passed in `/tmp/pr-5769-deletion-storage-host-green-79.log`: 15.556s, 20.781s, and 10.302s respectively. These include concurrent switch/delete, no-fallback cleanup, immutable host identity, attachment/restart, and dead-owner lock rejection.
- The final cancellation correction and complete deletion matrices passed three times under race in `/tmp/pr-5769-deletion-final-matrix-79.log`: manager 39.262s and store 20.928s. Earlier manager snapshots and results are superseded for the changed files.

## Final adversarial review

Admission rereads the authoritative journal while holding the same mutex as cancellation. The durable stop CAS is repeated before revocation, each session stop, and finalization. A cancelled or superseded operation cannot restart or advance. A committed cancellation marks its run before any fallible response read. Cleanup retains non-retryable ownership until fence release, and releases each run only once.

Every destructive runtime retry obtains fresh exact-generation evidence. A positive replacement acknowledges only the recorded owner's departure. Coexisting generations, missing identity, and probe errors remain blocked. Persistent shutdown carries the captured host capability after checking its non-secret fingerprint; it never looks up a fresh replacement capability to authorize a retry. Broad native shutdown remains untouched.

Final binding/default cleanup and the blocked deleted-choice record share one SQLite transaction. Credential removal is a separate idempotent vault operation, protected by the durable deletion fence. Recovery accepts an already-missing credential without authorizing a fallback. Pre-coordination journals lacking a captured provider remain fail-closed; no migration invents an owner for an ambiguous historical operation.

## Final verification and hold

All final post-edit checks passed: full race suites for 13 backend packages, 105 production-process executions (308.719s), 21 actual-runner proof executions (3.619s), runner full race/build/vet, backend build/vet/tagged vet, and all five runnable three-repeat acceptance gates. The concrete lint findings were limited to required exported comments, an unchecked connection close, and an unused fixture variable. The corrected backend-relative deletion patch now yields zero issues. The earlier wrongly rooted lint filter is superseded. API/SQL regeneration produced zero drift in 32 artifacts; all 91 protected paths and the historical four-file R4 delta match.

The source delta has 42 files; two retained-pane files in the earlier 44-file candidate belong to a prior runtime correction, as recorded in REVIEW-82-CORRECTIONS.md. They remain in the integrated supporting snapshot, not the deletion delta. The final 1,802-file source snapshot matches its pre-verification hashes. Exact manifests, logs, and the independent review request are in [REVIEW-82-DELETION.md](REVIEW-82-DELETION.md). No source changes follow this freeze. Public controls/UI and independent CLEAR remain held; desktop, live-provider, native-platform runtime, and responsiveness evidence remain broader release gates.
