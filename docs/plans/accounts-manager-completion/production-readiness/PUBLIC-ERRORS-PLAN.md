# Durable operation failure projection

Baseline: `b0378b674ac9e543a31b7673549360d15046683d`. Local-only slice after the immutable cold-retry seal. Audit: `/tmp/pr-5769-next-gap-audit-82.txt`, finding 2. Evidence root: `/var/tmp/pr-5769-public-errors-79.BBRYzt`.

## Contract

Expose an optional `errorCode` on switch and removal responses, including the nested session switch. Project only exact known diagnostic constants. Arbitrary stored strings, private paths, runtime identities, endpoints and credentials stay redacted. Suppress codes on successful or cancelled operations, even if an older journal or server retains one. Missing or unknown codes are not evidence of success.

Keep HTTP errors and their request IDs distinct from durable operation diagnostics. Retry/cancel permissions still come from existing capability and phase fields, never from an error code. Do not change execution, admission, persistence, containment, or account selection.

The HTTP allowlist includes the cold correction's `TARGET_REVALIDATION_UNAVAILABLE`; inclusion does not certify that correction. It also includes existing bounded source, target, admission, persistence, revocation and restart constants. CLI and renderer validate the same bounded vocabulary before display. Unknown future codes are omitted while the rest of the operation remains usable. Older servers may omit the field.

## Execution

1. Write failing HTTP projection tests with real SQLite close/reopen, typed-client shape and validation tests, CLI human/JSON tests, and switch/removal renderer assertions. Preserve the pre-fix logs before production edits.
2. Add the narrow projection, mirrored CLI field/output, client sanitization and localized renderer label. Regenerate the code-first API artifacts.
3. Review acknowledgement versus durable state, capabilities, stale success suppression, unknown-code redaction and old-server compatibility. Fix substantiated findings before broad checks.
4. Run focused races three times, complete touched backend package races, backend build/vet/lint, focused frontend tests, typecheck, locale checks, API generation/drift/parity and preservation audits. Seal exact source, docs and evidence for independent review.

Checkpoint extension: capture privacy-safe evidence in the real isolated desktop with a real provider catalog. Follow the repository change-count rules and prepare local commits containing only the cold-retry/public-projection checkpoint. Freeze the exact source for independent review before publication; the latest checkpoint direction permits local commit preparation while that review is pending. Any push or PR edit requires the orchestrator's specifically announced action and fresh approval.

## Boundaries

The cold-retry, inventory and first-slice seals, nine containment files and 91 protected paths must remain byte-for-byte unchanged. Settings-generation fencing, historical journal repair, combined runner/vault recovery, native-platform and live-provider acceptance are separate open gates. Component rendering is not desktop evidence. No publication or release-completion claim belongs to this slice.
