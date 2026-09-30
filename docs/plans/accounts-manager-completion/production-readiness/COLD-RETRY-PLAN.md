# Cold switch retry during target outage

Baseline: `b0378b674ac9e543a31b7673549360d15046683d` plus the unchanged inventory-ordering seal. Evidence: `/var/tmp/pr-5769-cold-switch-79.2u9WV2`. This is a bounded M03 correction, not full cold runner recovery or historical repair.

## Contract and scope

A recovered requested/waiting switch must keep its recorded account intent, input fences and safe retry/cancel actions when target revalidation is unavailable. It must not stop, interrupt, launch, rotate a binding or select another account. A later explicit retry revalidates the target again. Cancellation remains linearized by the existing durable phase compare-and-swap.

The correction may touch `accounts_manager_recovery.go`, `accounts_manager_switch.go`, new focused/process tests and these slice documents. Preserve the inventory seal, previous first-slice seal, Linux containment files, managed Chat admission and all 91 protected paths. Initial-start target failures retain existing semantics. Historical failed/cancelled journals are not reopened. Post-stop failures remain recovery-required and cannot become cancellable.

## Execution

1. Add failed-first SQLite reopen tests for both handle forms and requested/waiting phases. Require preserved intent through repeated target outages and another restart, followed by explicit cancellation or successful retry.
2. Distinguish retry revalidation failure from initial execution failure. Persist a safe error code without discarding pre-stop proof or releasing fences. Review cancellation races before expanding coverage.
3. Add cancellation-versus-error-publication barriers, historical terminal and post-stop controls, plus real direct/fallback process survival and successful recovery. Exercise public retry eligibility through the manager's existing capability boundary.
4. Run focused races three times, affected account recovery/store/service suites, build/vet and changed-scope lint serially. Recheck all preservation manifests and generated artifacts. Seal source/docs/evidence separately and request independent review before further recovery changes.

## Limits

SQLite reopen plus a new manager is a real durable coordinator restart. Injected target unavailability is not a restarted vault/provider process. Process fixtures use synthetic output and do not prove live account usage, desktop workflows or native Windows/Mac execution. The five retirement failures and platform containment gates remain open.
