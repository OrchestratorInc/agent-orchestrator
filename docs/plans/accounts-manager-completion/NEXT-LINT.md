# Account completion: full lint closure

Baseline: `c399189d241c707fdd40e2373a82c1028820636a`. This slice is separate from the sealed initial-selection work and does not change containment or expand credential support.

## Plan

1. Preserve the complete pinned-linter failures and classify each finding against the branch scope and protected inventory.
2. Fix contract documentation, standard constants, dead wrappers and equivalent expressions without weakening lint configuration. Audit unchecked operations individually. Preserve cancellation, ownership and persistence semantics.
3. Review the delta before verification. Add a regression before any behavior correction; existing tests remain the oracle for equivalent cleanup. Do not suppress a substantive failure merely to pass lint.
4. Run complete backend and runner lint, relevant focused race checks, builds and vet. Run full affected package races where feasible. Record the existing escaped-descendant failure separately; it cannot be cleared by this slice.
5. Verify generated drift, all 91 protected paths and the immutable initial-selection archive. Freeze the exact correction and request independent review.

## Acceptance

- [x] L1: full backend and runner lint report zero issues using pinned version 2.13.2 and the existing backend configuration.
  EVIDENCE: `lint-final2-backend.json` and `lint-final2-runner.json`, both zero issues; exact commands and exit 0 in `lint-final2-checks.log`.
- [x] L2: reproduced boundary defects have failed-first regressions and repeated passing evidence; equivalent cleanup and conservative error propagation are reviewed separately, with fault-injection limits stated.
  EVIDENCE: four valid failed-first logs below; final runner focused race repeats three times in 11.344s, backend focused race repeats three times in 233.048s. The runtime-selection focused filter selects no tests; its complete race suite supplies coverage instead. OS descriptor/directory-close failures were not injected. Their conservative error propagation is source-reviewed, not represented as a failed-first runtime proof.
- [x] L3: affected race suites, build and vet pass on the final correction; any unrelated known failure is named and remains open.
  EVIDENCE: all 11 commands in `lint-final2-results.json` pass with unchanged tested hashes. The 13 complete affected backend race packages pass in 751.649s command time; full runner race passes in 10.016s. Backend/runner builds and vet, plus tagged manager vet, pass. Persistent-host containment is outside this correction and retains its known escaped-descendant failure; this is not a full-repository race pass.
- [x] L4: generated artifacts, protected paths and prior seals remain unchanged; a self-review records ownership, cancellation, persistence and secret-redaction consequences.
  EVIDENCE: `lint-generated-drift.json` reports 33 generated artifacts with zero drift after API and SQL regeneration. All 91 protected hashes match. The seal verifies prior initial-selection manifests and archives byte-for-byte; it does not require the reopened live tree to match that earlier snapshot.
- [ ] L5: exact source manifest, correction archive and independent review request are recorded. Review acceptance remains separate from local verification.
  EVIDENCE: sealing under `/var/tmp/pr-5769-next-79.wos4rh/lint-freeze`; final paths and digests belong in REVIEW-82-NEXT-LINT.md after seal creation.

## Preserved red evidence

Artifact root: `/var/tmp/pr-5769-next-79.wos4rh`.

- `next-full-backend-lint.json` and `.log`: 86 findings, exit 1.
- `next-full-runner-lint.json` and `.log`: 53 findings, exit 1.
- `next-full-lint-results.json`: exact commands, durations and exit status.

The runner intentionally uses the same existing configuration for the complete quality audit. No linter rule is disabled for this slice. Native-platform, live-provider and deletion-safety release gates remain open.

## Midpoint self-review

- Missing Chat handoff capability and malformed internal route-cache entries must reject readiness/admission, not panic. Failed-first logs: `lint-backend-boundary-red.log` and `lint-runner-boundary-red-valid.log`. Neither negative test implies that an external caller can populate the internal cache.
- Failed HTTP body cleanup cannot produce a verified payload or successful private operation response. Both errors remain sanitized. The first runner fixture accidentally supplied nil headers and panicked before the intended assertion; that log is diagnostic only. The corrected failed-first log exercises cleanup. `lint-oauth-close-red.log` separately proves the legacy private HTTP boundary.
- A first cleanup correction returned an error after committing the credential. Midpoint review rejected that ordering. `lint-browser-order-red.log` proves it. Callback closure now happens before credential commit; failed closure cancels the durable login. The positive control includes an authenticator that already closed its listener.
- File reads discard data on closure error. Vault scratch cleanup errors fence admission. Existing successful writes still check write, sync and close before directory sync; the deferred fallback close cannot turn an earlier failure into success.
- The AES-GCM buffer change retains the same nonce-prefix wire layout and authenticated format marker. Existing opaque-token, restart and no-fallback tests remain unchanged.
- Error-category tests still assert sanitized exact messages in addition to wrapped-error matching. CLI cancellation remains an acknowledgement, not an invented observed state.
- Three public string constants trigger credential-pattern false positives; their annotations identify format/path literals, never secret values. Self-execution uses the current executable with fixed arguments and no shell. The legacy observer deliberately outlives its initiating request and checks coordinator closure/operation expiry; its narrow annotation does not certify joined shutdown.
- All 91 protected paths match the initial-selection baseline. No containment proof, native switching logic, subscription UI, SQL query, migration or generated contract is changed.

## Verification classification

`lint-final2-results.json` is the final ordinary-check record. The earlier `lint-final` batch reached an external 300-second command limit while serial focused backend tests were progressing. Its partial log and result are preserved; no test assertion failed and no source changed. The replacement command allowed the complete serial check to finish without increasing a test oracle's deadline. The first generation command failed before generation because of an invalid Fish PATH expansion; `lint-generated-drift-valid.log` records the corrected successful invocation.

The final bounded actual-process recheck passes under race: all four top-level production-runtime, cold-recovery, cancelled-retry and handoff-ownership tests execute, with direct and fallback cases and no skips. Package time is 73.150s. Exact command and exit status are in `lint-final-process-results.json`, with unchanged source hashes.

Source edits are paused for the seal. A full lint pass is not release completion or independent review acceptance. No credential, provider task, shared runtime or user's desktop data was used by these checks.
