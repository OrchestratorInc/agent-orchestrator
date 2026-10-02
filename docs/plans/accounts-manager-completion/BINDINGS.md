# Explicit session bindings and revocation

Contract: persist native or managed intent for each session/provider. Managed bindings contain only a public account ID and a monotonically increasing binding revision. Changing a future-session default never changes an existing binding. Migration preserves draft managed pins and freezes existing supported native sessions as native.

Order:
1. Add an additive binding table, migrate existing intent, expose transactional snapshot/CAS storage, and preserve native restores.
2. Start the runner authorization registry closed. Reconcile a durable-versioned binding snapshot before minting capabilities. Tokens include account, session, provider, and binding revision; check the current registry again for each logical request.
3. Wire reconciliation and revision-aware route preparation through the daemon. Reject stale snapshots and stale capabilities. Exercise concurrent A/B, switching one binding, removal, and runner restart without selecting a fallback.

The snapshot version advances transactionally from database triggers for insert/update/delete, including session cascade deletion. New and changed rows take their revision from this global clock, so removing and recreating a row cannot revive an old capability. Public account IDs in the private registry avoid dependence on whether deleted credentials still appear in inventory. Minting verifies that the private credential reference maps to the selected public ID. Account deletion/disablement remains independently enforced by vault admission on every logical HTTP operation and again in the selector.

The daemon reconciles every second, with a two-second request deadline. The runner's binding lease expires after five seconds without a valid reconciliation. A restarted runner authorizes nothing before reconciliation. Old draft route tokens are deliberately rejected by protocol 3; restoring a managed controller mints a revision-aware capability. Native paths retain their existing credentials.

Switching will use a separate durable operation journal and controller gate. A registry update alone cannot report a session as input-ready. Its drain/interruption choice, stop confirmation, commitment, resume acknowledgement, and recovery remain the next increment.

- [x] B1: Native/managed intent, CAS, global snapshot ordering, migration, and cascade behavior pass store/service tests.
  EVIDENCE: Fish, backend module, isolated environment. Focused race checks exited 0: store 15.881s, migrations 13.039s, service 1.015s. SQL regeneration initially rejected an ambiguous revision reference; qualified it, regenerated successfully, and reran. Twelve concurrent CAS attempts have one winner, with native intent and cascade-clock assertions. A later audit added native Chat intent recording; its cross-boundary verification remains under B3.

- [x] B2: A closed or stale registry cannot authorize a request; changing one revision revokes its old capability without affecting another session.
  EVIDENCE: Fish, runner module, GOWORK=off, isolated environment. Focused race checks passed three repetitions (1.144s); full runner build, vet, and race suite exited 0 (2.860s, then cached rerun). Cases cover unreconciled restart, expired lease, stale snapshot, changed account without revision, public/private ID mismatch, trailing JSON, concurrent reconciliation, cached-selector revocation, and every allowed logical route requiring credential admission.

- [ ] B3: Integrated daemon/runner restart and reconciliation preserve the latest choice without native fallback; full affected checks pass.
  EVIDENCE: Partial. Actual runner A/B requests, A-to-B rebinding, switching back without reviving old tokens, restart-before-reconciliation refusal, and credential removal passed ten repetitions (5.371s). Native Chat recording/refusal passed two repetitions (12.901s). Full affected backend adapter, service, store, and HTTP suites passed, but the complete migration suite found the missing 0162 ledger entry. That entry was added and the focused ledger check passed (0.003s). Full migration rerun, actual daemon/desktop reconciliation, and CLI compatibility remain open. Migration 0163 adds the switch journal and is included in the next complete storage run.
