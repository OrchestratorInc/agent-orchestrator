# PC4 removal and integration self-review

Scope remains renderer-only. The immutable HTTP, CLI, lifecycle, guest, native switching and Subscriptions boundaries are unchanged. Artifacts are under /tmp/pr-5769-pc4-79.hoSn6v.

## Reviewed invariants

- Inventory removal opens an impact preview. No old direct DELETE is reachable from the new control. Confirmation binds to the exact returned impact revision, including zero. Refresh resets confirmation, and a stale-revision response requires confirmation again.
- The operation reference is saved before submission. Lost responses and missing operation records retain uncertainty and the original ID. Remount does not create or resend an operation. A deliberate resend uses the original request, never a fresh revision or ID.
- Removal retry/cancel reads the named operation and checks account ownership before mutation. Completion and cancellation labels come only from daemon state. Account inventory is invalidated only after observed completion, without optimistic deletion.
- Saved recovery references contain only validated account/operation IDs and the explicit request. Unknown fields, corrupt records and storage failures block mutation. A recovered reference cannot overwrite another unresolved operation for the account.
- Session committed binding and pending switch remain separate. A later pending operation cannot be hidden by a saved completed intent. A stale revision does not change the displayed committed account.
- Request-ID diagnostics are bounded and omit raw daemon messages, endpoints and runtime handles. Telemetry labels use reviewed route templates. Existing add/login/rename/refresh/disable flows keep their service boundary.
- A successful sign-in cancellation request is an acknowledgement. It is not evidence of terminal cancellation or credential revocation, including after operation pruning.

## Findings corrected

The second self-review found a saved completed session intent masking a later pending operation and permissive recovery-reference writes accepting unknown fields. Both were reproduced in recovery-review-red.log before correction; recovery-review-green.log passes 30 tests.

The complete frontend suite then caught untranslated new controls. The correction uses the existing translation system, including accessible labels, confirmations and safe error text, with 92 entries in every supported catalog. The original locale bytes are preserved in locales-before-pc4.sha256 and locales-before-pc4.tar.gz. localization-focused.log passes 80 tests; localization-typecheck.log exits zero.

The initial complete frontend run is diagnostic only: 121 failures in five files, 5,273 passes and seven skips. One failing file is the corrected localization regression. The other four are environment failures: missing zip for three update/archive fixture files, and a native SQLite binary built for module ABI 137 while Node requires 127. The isolated desktop checkout uses a real lockfile install, and the task-owned zip binary is extracted from a signature-verified distribution package. No suite exclusions or product-source workarounds were introduced.

## Remaining gates

The final inventory audit reproduced a late mount refresh overwriting a newer event snapshot. inventory-late-read-red.log fails with revision 2 instead of 3 and a reappearing removed account. Query structural sharing now uses the existing revision-selection rule, also used by events and mutation receipts. The full isolated run started before this correction is retained as superseded diagnostic evidence. The corrected final run passes 340 files, 5,395 tests and seven skips; the focused 64-test matrix passes three times. Typecheck, Linux desktop packaging and generated drift pass on that same 22-file source snapshot.

Backend build and vet pass. The root HTTP race package still fails TestServerShutdownEndpoint at its five-second deadline. Other affected packages pass; isolated test and untouched-HEAD package controls pass. This does not establish an inherited-HEAD cause, and no frozen backend source was changed. A separate bounded investigation is requested through the orchestrator.

Actual Electron evidence covers saved-key inventory, explicit default selection and clearing with routing off, unavailable removal/recovery, disabled refresh and retained metadata after app/daemon restart. The key was deliberately invalid and no provider authorization is claimed. The sidecar survived that restart. The production daemon does not wire the optional coordinated-control port, so successful switching, retry, cancellation or removal cannot be claimed from this UI slice. Native account storage is unavailable in the private home. Native platform, guest retirement and independent review remain open gates. REVIEW-82-PC4.md records the exact source seal, evidence and limits.
