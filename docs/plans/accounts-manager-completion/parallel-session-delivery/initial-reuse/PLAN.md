# Explicit account choice on existing orchestrator reuse

The creation audit found that session-service Spawn returns an already-active orchestrator before reaching initial-account validation. The same automation-run replay has a second early return. Both can report success after the caller explicitly selected a different account or native mode. The atomic store replay already rejects mismatched choices; this service shortcut bypasses it.

Decision: preserve omitted-choice compatibility and successful same-choice replay. An explicit choice may reuse an existing session only when a current, unblocked durable binding proves the exact provider, session, mode and account. Reject missing capability, missing/mismatched proof, blocked state and invalid choice. Never switch, launch, revoke or mutate an existing session to satisfy a spawn retry. The user must use the explicit switch operation for a different account.

1. Reproduce both early-return paths with service tests before changing production code. Keep native and exact-managed positive controls, no-choice compatibility, and zero mutation/launch assertions.
2. Add the smallest service-local reuse check, keeping runtime calls behind the existing service/store boundaries. No route, DTO, schema, migration, UI, native switching or subscription change.
3. Self-review after initial green. Add a real HTTP/service/SQLite regression for mismatched choice, exact replay and request-ID preservation. Recheck ordinary workers and automation replay.
4. Run focused race count 3, full affected service/controller races, backend build/vet, changed-scope lint, API/SQL drift and all preservation manifests. Seal separately and request independent review.

Midpoint finding: the binding reader's blocked bit covers removal, while switch progress lives in a separate journal. The first correction therefore still accepted a requested/waiting/recovery switch and could combine an old binding with a completed journal. The failed-first midpoint table covers all switch phases, foreign journal ownership, binding replacement/disappearance during the read and cancellation. The correction reads the latest journal and then rechecks the binding revision and identity before returning the existing session. No runtime admission or mutation is added.

Scope: session-service reuse helper/call sites and focused tests only. Preserve the newly sealed runner correction/process proof, Linux containment candidate, guest design and 91 protected files. This independent W1 boundary does not adopt containment or enable managed Chat.
