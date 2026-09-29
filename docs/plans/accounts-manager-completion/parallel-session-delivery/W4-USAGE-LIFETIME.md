# W4 shared usage-request lifetime

Bounded follow-up to the credential experience and W1 runner proof. Preserve both W1/W2 seals, the retirement correction, all 91 protected paths, generated contracts and guest design. No provider endpoint, eligibility rule, native profile or renderer change is planned.

## Decision

The runner already caches quota per credential fingerprint and limits all independent credential checks to four. Its shared quota fetch currently belongs to the first caller's context. Cancelling that caller can fail other viewers and briefly cache the cancellation as an outage. A new shared worker must belong to the interested callers collectively, with a bounded runtime-owned context.

Use the data-fetching skill's cancellation, coalescing and explicit-error checklist inside the existing Go HTTP implementation. Keep the established transport, verification, redaction, timeout and concurrency limit. Do not introduce a new client library or move credential logic to the renderer.

1. Reproduce cancellation of the first caller while another waits. Require the surviving caller to receive the same one provider response. Reproduce runtime close returning before a quota worker has drained.
2. Give each in-flight account/generation one joined worker. Cancel when the final caller leaves, the account is removed, the flight is superseded or the runtime closes. Stale completion cannot replace a newer cache entry or authorize a removed credential.
3. Extend runtime close minimally to cancel and join quota workers before the existing vault-close boundary. Retain the already-reviewed refresh join and all public contracts.
4. Self-review lock order, wait-group admission, cancelled cache entries, wrong-account results and cleanup. Verify duplicate reads, A/B independence, limit four, generation/removal races, error redaction, shutdown and restart. Run focused race repetitions, full runner checks, build/vet/lint and preservation, then freeze for independent review.

## Acceptance

- [x] U1: failed-first caller cancellation no longer poisons a live viewer; one fetch serves both until only one caller remains.
  EVIDENCE: `/var/tmp/pr-5769-w4-usage-79.WpzXO2/lifetime-red.log` records the original failure. `final-focused.log` repeats the corrected caller and cache controls three times under race.
- [x] U2: last-waiter cancellation and runtime close cancel the transport and join workers; closed runtimes admit no quota work.
  EVIDENCE: the same red log records premature close and post-close admission. The final focused run also drives the real loopback server through blocked transport cleanup and two vault reopen/restarts per repetition. Vault locking remains held until the worker drains.
- [x] U3: A/B, generation and removal controls reject stale results, preserve unrelated readers and retain the existing four-check cap.
  EVIDENCE: `final-focused.log` covers removal, disabling, fingerprint replacement, abandoned viewers and six accounts contending for four slots. No stale completion replaces a newer cache entry. Existing error/redaction and eligibility checks also pass.
- [x] U4: full runner tests/races, build/vet/lint and preservation checks pass after midpoint findings are fixed; exact source/evidence is sealed.
  EVIDENCE: 153 focused leaf executions across three race repetitions (28.802s), 269 full-suite leaf executions (10.592s), and 269 full-race leaf executions (14.640s). No test skips; the command package has no tests. Build/vet and full pinned runner lint pass. `final-preservation.log` matches 91 protected paths, 33 generated files, the retirement correction, W1/W2 seals and guest design. Exact artifacts and scope are in `W4-USAGE-LIFETIME-REVIEW.md`.
- [ ] U5: independent review accepts the bounded change. Positive live-provider quota, explicit profile migration and native-platform release checks remain separate.
  EVIDENCE: pending.

## Midpoint review

The initial worker change confused a usage deadline with account revocation. `deadline-red.log` records HTTP 409 while the credential remains valid. The corrected worker checks durable admission independently of the expired request and returns unavailable (503); it does not revoke the credential. Disabling also cancels quota immediately after its durable commit, before a later registration failure can return.

Wait-group admission and the closed flag share the quota lock. Removed or superseded workers remain joined even after leaving the cache. Results synchronize through the completion channel; delayed results cannot overwrite a new map entry. The server test injects only its private transport at construction, without global HTTP mutation or a production configuration switch.

This leaf supplies synthetic positive usage and real local shutdown/restart evidence. It does not establish live quota permission, native platform execution, profile migration or complete account isolation. All product gates remain open.
