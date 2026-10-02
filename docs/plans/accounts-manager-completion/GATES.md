# Gates: remaining Accounts Manager implementation

Scope: complete the product requirements, not merely the prior review fixes. Manual gates require cross-boundary evidence from the implementation ledgers and real workflows. Do not mark them met from a green package suite alone.

- [ ] G1: Every advertised credential method has encrypted persistence, lifecycle fencing, identity validation, and restart-recovery evidence without modifying native credential files or exceeding the approved callback extension.
  EVIDENCE: Unmet. Serve uses encrypted storage, admission, private credential commands, direct callbacks, and encrypted device transport. Draft recovery, actual A/B requests across restart, reconnect identity/CAS, and manual refresh coalescing pass synthetic checks. Legacy writers, watching, and error-request logging are disabled. Account organization, complete lifecycle/platform evidence, and live sign-in remain open. The approved callback extension is the sole engine exception.

- [ ] G2: Explicit per-session choices and persisted connection mode enforce concurrent A/B isolation, revocation, and restore without automatic fallback.
  EVIDENCE: pending

- [ ] G3: Session-scoped switching and active-account removal have verified controller acknowledgement, user-selected timing, cancellation, and restart recovery.
  EVIDENCE: R4 corrections independently CLEAR. Deletion remains HOLD after the original host-parent-death defect and subsequent late-fork census finding. The D2 correction has failed-first evidence and three-repeat late-census plus real Chat-host/SQLite-reopen tests; bounded frozen-snapshot verification is tracked in REVIEW-82-DELETION-D2.md. Independent deletion CLEAR, public controls, and integrated desktop behavior remain open.

- [ ] G4: The real desktop provides initial account choice, two-action saved-account switching, maintenance controls, and accessible pending/recovery states.
  EVIDENCE: pending

- [ ] G5: Exact supported CLI modes and live native coexistence have executable proof; unsupported paths are unavailable and explained.
  EVIDENCE: pending

- [ ] G6: Full relevant module checks, generated contracts, protected paths, platform checks, and measured responsiveness establish release readiness.
  EVIDENCE: Unmet. Earlier 13-package, 105-scenario, runner and static-check results belong to historical snapshots. The v3 full race command was stopped after a new ownership-proof finding; its partial log is diagnostic only. D2 is limited to the requested host-package race, focused coordinator recovery, scoped lint and integrity checks before independent review. Non-Linux exact teardown and automatic-refresh draining remain implementation gaps. Full final-snapshot verification, supported-platform execution, live-provider workflows, public controls, desktop evidence and latency measurements remain open. Historical HTTP shutdown flakiness, prior whole-branch lint findings and the baseline engine media-test failure remain recorded.
