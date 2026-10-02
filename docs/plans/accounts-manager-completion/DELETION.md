# Coordinated account deletion

State: independent review HOLD on crash-stable Chat provider teardown. D2 corrects the late-fork census proof and adds combined real host-parent-death plus SQLite coordinator-reopen coverage. The source is frozen for bounded verification and reviewer 82, described in [REVIEW-82-DELETION-D2.md](REVIEW-82-DELETION-D2.md). No current independent CLEAR or complete final-snapshot suite is claimed. R4 remains independently CLEAR in `rpt_3b54e7fc-bd40-4e04-951f-edc73173d20f`. Non-Linux, refresh-drain and public controls/UI work remains held. Work is local, with no publication.

## Contract and design review

Enumerate every positive account binding, including inactive sessions and dormant provider bindings. Capture binding revisions and exact controller/runtime generations. Pause all affected queues before stopping anything. Block new selection, launch, switch, credential mutation, and route authorization while deletion is pending. Only captured owners may be stopped. Unknown or coexisting ownership retains a recoverable fence and must not delete credentials. A positive replacement-generation result acknowledges departure of the recorded owner only; it cannot authorize any signal or destructive call against the replacement. Continue account revocation with the deletion fence retained. Never choose a replacement account.

Native connection-mode rows have an empty account identifier by schema. They are not evidence that a native credential belongs to a vault account. Mixed-mode tests must include unrelated native sessions and prove they continue. Terminal and Chat controllers with positive managed-account bindings are in scope. Session59 explicitly confirmed this boundary and the cross-store saga below. No protected native credential path will be changed to infer that relationship.

The runner vault and daemon SQLite database have independent commit boundaries. Physical cross-store atomic deletion is not available. The implementation provides a durable logical deletion: block authorization, record stop/revocation acknowledgements, tombstone the credential idempotently, then atomically finalize binding/default cleanup in SQLite. A crash between stores retains the blocking obligation. No successful completion or automatic native/default fallback is permitted during that interval.

Deletion has a durable cancellable pre-stop boundary. Cancellation must win a journal CAS before any interrupt, stop, or irreversible revocation step. A restart must preserve that proof. After stopping starts, only retry/recovery is allowed. Per-session queues remain durable until the user explicitly chooses how to continue. Removing an active binding must leave an explicit blocked session choice rather than an absent row that can silently resolve a new default.

## Work order

1. Storage and lifecycle: reproduce incomplete impact, cancellation, unsafe finalization, and missing post-removal launch fences. Extend the journal, owner/revision checks, durable queue obligations, and atomic final cleanup. Add a new migration and regenerate queries.
2. Controller coordination: reserve affected session gates, pause queues, stop exact owners across both runtime slots and Chat, acknowledge runner revocation, then remove credentials. Add cancellation/retry and startup recovery with no automatic relaunch.
3. Cross-boundary review: inspect concurrent switch/delete, missing resources, generation changes, cancellation races, and partial completion. Add crash cuts and actual runtime/runner controls, including unrelated sessions.
4. Verification and freeze: run focused race and actual-process suites, build/vet, generated-contract checks relevant to this slice, and the 91-path protected inventory. Record exact hashes and request reviewer82 before public session controls/UI.

Pre-implementation findings: the current impact scan skips inactive/dormant bindings; creation begins at stopping with no cancellation state; stopped acknowledgement trusts a session lookup without a coordinator; finalization removes the credential before an explicit runner-revocation acknowledgement; no deletion worker or startup reconciliation exists. Existing tests cover only the storage/service foundation. The first deterministic red run observed all five implemented assertions fail, including both inactive cases and SQLite reopen: `/tmp/pr-5769-deletion-red-79.log` (store 0.502s, manager 0.550s, exit 1).

Progress and evidence belong in [DELETION-GATES.md](DELETION-GATES.md). Broader product and release gates remain open independently of this slice.

## Historical deletion snapshot

The [original reviewer handoff](REVIEW-82-DELETION.md) and its immutable archive preserve the earlier snapshot. The results below are historical, not evidence for the corrected source. The current correction is tracked in [DELETION-HOST-CRASH-GATES.md](DELETION-HOST-CRASH-GATES.md).

- All 13 affected backend packages passed their full race suites after the final edit. Backend and runner build/vet passed; the runner full race suite passed in 4.407s.
- Five executable acceptance gates passed three-repeat race checks. The complete production-process suite passed 105 scenario executions (35 scenarios repeated three times), including 18 deletion cases, in 308.719s with no skips or race reports. Separate actual-runner restart/revocation proof passed 21 executions, no skips, in 3.619s.
- Changed-scope lint reports zero issues after correcting its 18 concrete findings. API/SQL regeneration has zero drift across 32 artifacts. Formatting and diff checks pass.
- The deletion delta has 42 files. Its manifest is `/tmp/pr-5769-deletion-delta-79.sha256`, SHA256 `d2a46585d10558183c6f0cca43c1c0bb4fc71a4652dbf589846a5eb75d72ae44`. The integrated 1,802-file snapshot is `/tmp/pr-5769-deletion-source-79.sha256`, SHA256 `1464a9b69e4a2c60994920e706efa1f6eb290e880c0eb96faccd20cb9726a513`; every entry matches the pre-verification source. All 91 protected paths still match the baseline. The existing dirty worktree is preserved, not claimed clean.

Linux real-process execution is established with synthetic workloads and credentials. Native Windows/macOS runtime behavior, live-provider sign-in and usage, full desktop coexistence, public routes/CLI/session controls, and latency measurements remain open. Earlier cross-platform compilation is not runtime evidence. Ambiguous historical deletion journals without a captured provider remain fail-closed. No visual surface was introduced in this backend slice; product desktop evidence remains required.
