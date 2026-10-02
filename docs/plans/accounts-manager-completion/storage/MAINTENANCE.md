# Account maintenance increment

Contract: labels are bounded user metadata, persist independently of provider identity, and survive refresh/reconnect. Rename uses an expected generation and cannot silently overwrite another edit. Key/import retries carry an explicit operation ID across the public boundary. Enablement works for both credential kinds. Account filtering and label editing stay inside the separate Accounts surface.

- [x] M1: Rename is durable, stale edits fail, refresh cannot revert a label, and other accounts remain unchanged.
  CHECK: env GOWORK=off go test -race ./internal/runner -run TestCredentialMaintenance -count=20
  EXPECT: ok
  CWD: accounts-manager/runner
  EVIDENCE: Fish, runner module, credential-free environment with GOWORK=off. Twenty label/refresh race repetitions exited 0 (4.662s). Tests compare the other account ciphertext, reopen the vault, and release an old SDK refresh after a new registration.

- [x] M2: Public label and idempotency commands preserve scope, reject ambiguous input, and pass adapter/service/controller checks.
  EVIDENCE: Fish, backend module, credential-free environment. Focused adapter/service/controller race checks exited 0 (1.081s, 1.010s, 1.063s). Public tests cover repeated operation IDs, private reference redaction, generation forwarding, and empty/mixed patch rejection. API regenerated from source DTOs.

- [x] M3: Accounts supports label editing and filtering with pending/error controls; focused UI tests and typecheck pass.
  EVIDENCE: Fish, frontend, Node 24. Focused component/hook suite exited 0, 22 tests passed (43.11s under disk pressure). Full frontend tsc --noEmit exited 0. A status-filter label initially conflicted with row-status assertions; changed to Available and reran successfully.

Real desktop maintenance verification remains a required product gate. Do not claim it from component tests.
