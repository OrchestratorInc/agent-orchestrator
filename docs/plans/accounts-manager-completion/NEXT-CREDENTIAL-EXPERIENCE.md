# Credential format, usage and Harness scope

This bounded P4 slice starts after local checkpoint `4440f5796`. Preserve the lint, initial-selection, switching and integration archives and all 91 protected paths. No containment, native-switching, subscription UI or guest design edits.

## Decisions before implementation

1. Keep the supported API-key form distinct from a provider sign-in token. A known sign-in-token format is rejected before any outbound verification or storage, with a specific safe error and request ID. A format hint cannot establish validity, identity, expiry or usage permission. Existing key verification remains non-generating and provider-backed.
2. Project an existing token-shaped API-key record as `access_token`, without changing its bytes, identifier, generation, enabled state, durable verification, binding or default. Do not silently migrate its secret into another field or enable usage. Reconnect/native-profile migration is a separate still-open support gate.
3. Explain unsupported usage by credential kind. API-key usage is not subscription quota; a stored sign-in token has no proven usage permission. Retain distinct authentication, permission, rate-limit, offline and malformed-response errors, request IDs and observation timestamps. No fabricated zero or account fallback.
4. Show managed-account availability separately from the device login in Harness settings. Only fresh, available, verified inventory counts. Keep installation requirements, native login actions and device readiness untouched. Do not imply that every interface supports the managed execution path.
5. Use existing daemon contracts and hooks. Extend the public credential-kind enum and regenerate its API artifacts. Localize all new text. No new credential reader, background sign-in, provider request or native store access is introduced by opening the UI.

These choices implement the support boundary already documented in NEXT-IMPLEMENTATION-PLAN.md. They do not claim new subscription-token proxy support or complete isolated provider-owned native profiles. Legacy route execution is not rewritten by this display/input correction; its migration and provider-supported execution gate remains explicit.

## Implementation and review order

1. Preserve deterministic failed-first runner and management/public HTTP tests: known token input reaches neither transport nor vault; legacy projection preserves data and cannot grant quota.
2. Implement only the input/projection contract, then inspect error ordering, idempotency and secret redaction before proceeding.
3. Add failed-first renderer tests for usage explanations, token labels, explicit error guidance and fresh managed availability beside unchanged device login. Implement and review stale inventory, unknown kinds, interface claims and accessibility.
4. Run focused races three times, full affected backend/runner suites, full frontend tests/typechecks, build/vet/lint and API/SQL drift. Use the isolated real Electron checkout for inspected screenshots and a short workflow recording; no live-account success without authorized identities.
5. Freeze exact source, correction and evidence manifests. Request independent review while preserving all native, containment, managed app-server and live-provider gaps.

## Acceptance

- [x] C1: the new input rejection is red first, then green with zero private transport/provider calls and zero credential publication, including repeated requests and wrong-provider input.
  EVIDENCE: `credential-format-red-runner.log`, `credential-format-red-management.log` and `credential-format-red-http-valid.log`; focused runner/backend race checks pass three repetitions in `credential-backend-final2-results.json`.
- [x] C2: legacy token projection is accurate, non-mutating and secret-free. Existing API-key/OAuth positive controls remain valid; quota permission is never inferred from a prefix.
  EVIDENCE: focused projection, contradictory quota read/reset and vault-reopen assertions pass; full runner and four affected backend packages pass under race on the final backend snapshot.
- [x] C3: public HTTP and CLI preserve the new error code/request ID without exposing credentials or private endpoints; generated enum and contract checks pass.
  EVIDENCE: HTTP/CLI focused race count 3, HTTP spec/parity race and repeated generation of 33 artifacts pass; `credential-generated-drift.json` records no drift. The real Electron HTTP response retains the same safe category and request ID after the final renderer correction.
- [x] C4: renderer tests distinguish unsupported usage, transient failure, observed exhausted quota and stale observations. Account choice never changes as a side effect.
  EVIDENCE: final ten-file focused set and all 351 frontend files pass in `credential-frontend-final3-results.json`, including request abort on format change, unknown-code redaction and real mutation-helper error normalization.
- [x] C5: Harness tests prove separate device and managed status, fresh-inventory filtering, unknown/unavailable states and unchanged native controls.
  EVIDENCE: availability negative matrix, existing native-action tests and inspected real Electron Harness screenshot pass. No verified account is present in that desktop profile, so positive managed availability remains unit-test evidence only.
- [ ] C6: midpoint findings are corrected; full affected checks, preservation audits, real desktop evidence and exact review handoff are recorded. Missing live-provider/native evidence remains a release gap.
  EVIDENCE: all 17 final verification commands pass, as do generated drift and 91 protected hashes. Exact seal and independent-review handoff are the remaining local checkpoint; independent acceptance is not implied by any local result.

No paid generation, live credential, native authentication store or shared desktop profile is authorized for test fixtures in this slice.

## Midpoint: input and projection

The initial runner/management tests fail before correction: both supported provider routes accept and publish token-format input in the API-key form, and the legacy projection reports API-key or unknown. Public HTTP initially fails before its intended boundary because the private fixture omitted the JSON content type; the corrected `credential-format-red-http-valid.log` proves HTTP 201, four private calls and mutation where HTTP 400 and zero calls are required. The invalid first fixture log is diagnostic only.

Runner and management first-path races pass three repetitions after the narrow input/projection correction. Public HTTP passes three repetitions with the specific error and request ID. Existing vault contents, identity, generation and verification remain unchanged by projection.

Self-review found a contradictory private quota flag could still enable legacy-token usage even with the corrected kind. `credential-format-quota-red.log` and `credential-format-quota-commands-red.log` reproduce the projection and direct read/reset paths. The correction rejects that combination at both boundaries. A prefix identifies the wrong form, not a valid credential, account identity or proven scope. No native profile or execution strategy is inferred from it.

A further proposed readiness/admission block was rejected during design review. The generic executor request helper and streaming executor do not share one authentication path, so a header mismatch in one helper is not proof that every existing route fails. The exploratory `credential-format-admission-red-*` logs test that rejected policy, not a substantiated product defect, and are excluded from acceptance evidence. The final regression instead requires prior readiness and durable admission to remain unchanged. No production admission guard was changed. Any future migration must make its execution consequences explicit.

Renderer failed-first log: `credential-experience-ui-red.log`, five selected assertion failures covering unsupported usage explanations, legacy-token label, safe form guidance and separate managed availability. Seventy unrelated tests were filtered out by this narrow reproduction; they are not treated as executed coverage. The device-login positive-control label is corrected to the existing localized `Login` text before the green run.

The first complete renderer path passes all 75 tests across the three existing settings suites. Post-green review adds the stale/unavailable inventory matrix and finds that usage cache identity lacks credential format. `credential-usage-causality-red-valid.log` proves the old in-flight request is not cancelled when format changes. The first exploratory check without the abort assertion passed before the scheduled notification and is diagnostic only. The correction includes format in cache identity and hides request errors when usage is no longer supported. Account/generation isolation and real zero-quota rendering remain covered.

## Desktop review and final correction

The first actual Electron workflow exposed a missing renderer error-code allowlist entry. The real daemon returned HTTP 400 and the new category, but the UI showed a generic add-key error. The original component fixture constructed an already-normalized error and bypassed that boundary. Preserve `credential-desktop-capture.log` and `credential-desktop-rejection-red.json` as the real reproduction.

`credential-error-code-red-valid.log` then fails deterministically in the error normalizer and the component using the actual mutation helper. The correction adds only the known safe code to the allowlist. Unknown codes, response text and private endpoints still do not enter the error object. The first command used the wrong external wrapper path and never ran tests; `credential-error-code-red.log` is diagnostic only.

The corrected real Electron run uses detached snapshot `9859e5593ff1693de82ad931d9ca596fa33d7838`, isolated data and the actual daemon/provider catalog. It displays the API-key/Base URL guidance, rejects synthetic wrong-form input with the specific message and request ID, clears the form secret and keeps zero stored accounts. Harness preserves the native signed-out state and separately shows no verified managed accounts. Three screenshots and recording frames were inspected. The recording is 1.68 seconds, with 56 captured frames and 26 encoded frames. This is negative-path evidence, not live-provider or positive quota evidence. The owned app and daemon were stopped after capture; the user's separate desktop was not touched.

The two introduced type errors were confined to new tests: a mock-call assertion inferred `never`, and an availability fixture used a value outside the public enum. Both were corrected. The first packaging attempt selected the host Go 1.26.0; pinning the required Go 1.27.1 fixes the environment without altering source. Runner lint's public token-prefix false positive has one local explanation, not a package suppression. Final checks after these corrections replace their earlier affected results.

Final backend evidence is `credential-backend-final2-results.json`: 11 commands, unchanged tested files, focused race count 3, four full affected backend packages, API spec/parity race, full runner race, backend/runner builds and vet, and full pinned lint with zero findings. Final frontend evidence is `credential-frontend-final3-results.json`: six commands, unchanged tested files, ten-file focused set, both typechecks, 351 test files with 5,606 passes and seven existing skips, and Linux desktop packaging. Node 24.21.0 and Go 1.27.1 are pinned; the private ZIP helper resolves the inherited missing executable. These checks do not replace full-branch containment or native-platform acceptance.
