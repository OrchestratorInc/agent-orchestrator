# Public account controls

Scope: public HTTP, CLI and session UI boundaries only. PC2 is frozen for independent review. The latest direction pauses PC3 and UI implementation until that review. Runtime retirement remains open; this work does not enable incomplete capabilities or alter lifecycle semantics. Nothing is published.

## Preservation boundary

Lifecycle, deletion, switching and guest-design files are frozen in `/tmp/pr-5769-public-controls-79.PbwFW0/lifecycle-guest-frozen.sha256`: 113 files, manifest SHA256 `ffc37c82bafedcf7223478d3d4a6ff6aefa97dea9f04b615b63ef1df66d3e96b`. All 91 protected files match their existing inventory. The previous F2/F3 archive and bounded review remain intact. No new Windows runtime matrix ran. The requested Windows cross-build, ownership-package vet and acceptance-test compilation passed, with logs under `/tmp/pr-5769-public-api-79.xfBKWE/windows-*.log`. These are compile-time checks, not native acceptance.

Native Windows verification is unavailable. Emulator Job Object evidence remains insufficient. The macOS guest design is accepted for a feasibility experiment per the latest direction, with production held on same-boot keeper-death proof. Escaped-descendant retirement remains open. No public response may describe these gates as passed.

## Inventory and decisions

| Surface | Existing implementation | Missing boundary |
| --- | --- | --- |
| Account HTTP | `controllers/accounts_manager.go`: catalog, sign-in, credential maintenance and defaults | Session binding, switch status/start/retry/cancel, deletion impact and coordinated removal operations |
| Lifecycle operations | Session manager start/retry/cancel for switches and removals; durable store journals and binding facts | Safe public projections and session/account-scoped operation ownership checks |
| OpenAPI/client | Account-level operations in `apispec/specgen/build.go` and generated frontend schema | Session-control DTOs, explicit capability state and generated operation contracts |
| CLI | `cli/session.go` registers session and harness-switch commands | Separate managed-account commands using daemon HTTP only, with explicit target, revision, timing and operation ID |
| UI | `useAccountsManagerQuery.ts` and `AccountsManagerSection.tsx` manage account-level state | Session-scoped picker, current/pending account, drain/interrupt/cancel/retry, deletion impact and confirmation |
| Protected flows | Native switching and Subscriptions have an independent 91-file inventory | No changes permitted |

Use `/api/v1/sessions/{sessionId}/account` for the binding and control-capability view, and `/api/v1/sessions/{sessionId}/account-switches` for operation creation. Address an existing operation with its ID under that session; retry and cancel are explicit POST actions. Account removal uses an impact GET, a POST to the account's `removals` collection, and status/retry/cancel under `/api/v1/accounts-manager/removals/{operationId}`. The existing unbound DELETE is not a substitute for confirmed in-use removal.

Use additive boundary files. Delegate lifecycle actions to the existing coordinator; do not copy lifecycle logic into handlers, the CLI or the renderer. The public view must omit runtime handles, native history identifiers, credential references, tokens and private endpoints. Missing control dependencies return the standard unavailable envelope with a request ID, even when the account catalog is ready. An unavailable or unknown capability must not expose an enabled mutation action.

The first API increment introduces an optional control-service dependency, not production runtime enablement. No daemon/coordinator wiring or frozen lifecycle file changes are included. Focused route/delegation tests use a synthetic implementation. Additional HTTP tests use the actual account admission service, SQLite journals and cancellation coordinator through a test-only adapter; they do not launch controllers. This isolates request validation, session ownership, redaction, error mapping and generated contracts from native acceptance. An absent dependency remains unavailable; account-catalog readiness must never implicitly enable it.

## Implementation order

1. Add and run a compiling HTTP regression against current production route registration. It must fail on the missing public routes, without starting a provider or touching credentials.
2. Add safe DTOs and narrow control delegation, then verify explicit user choices, operation ownership, stale revisions, idempotency, errors and redaction. Keep runtime availability explicit and conservative. Review the boundary before enabling any mutation.
3. Add CLI request/response tests, then thin commands. Verify missing target/timing arguments fail locally, request IDs survive errors and no default or native-switch operation is called.
4. Add renderer interaction tests and controls for session-scoped state. Verify keyboard use, current versus pending identity, cancellation/retry, unavailable capabilities and stale-response rejection. Do not alter Subscriptions or native account hooks.
5. Regenerate API artifacts, run affected checks and review this bounded delta. Real desktop, native platform, provider and performance acceptance remain separate release gates.

## Acceptance gates

- [x] PC1: failed-first HTTP boundary evidence recorded, with the existing catalog/status as a passing control.
  EVIDENCE: `go test -mod=readonly -p=1 ./internal/httpd/controllers -run '^TestAccountsManagerSessionControlsUnavailableBoundary$' -count=1 -v` compiled and exited 1 as expected. All ten missing routes return 404 instead of the required unavailable envelope; the ready-status control passes. Log: `/tmp/pr-5769-public-controls-79.PbwFW0/http-boundary-red.log`. No provider process or credential access was used.
- [x] PC2: bounded HTTP semantics, redaction, explicit choices and operation ownership verified through registered routes; generated contract agrees.
  CHECK: go test -mod=readonly -p=1 -race ./internal/httpd/controllers -count=3 -timeout=4m
  EXPECT: ok
  CWD: backend
  EVIDENCE: corrected F1 snapshot, isolated environment, Fish shell, backend CWD, exit 0: full controller race three repeats (134.149s), route/spec and generator race three repeats (1.824s and 82.341s), API dependency-wiring race three repeats (1.251s), backend build and full vet. Pinned changed-scope lint: 0 issues. API regeneration: byte-identical YAML and frontend schema. Frontend typecheck: exit 0. The Unicode failed-first matrix had 32 failures; all corrected cases pass, including durable journal/fence assertions and the adjacent boundary tests. Exact commands, logs and limits: REVIEW-82-PC2-F1.md. Corrected 13-file source manifest: `/tmp/pr-5769-pc2-f1-79.E8WKAc/pc2-source.sha256`, SHA256 `a3618c483f9411738333c1c0870f921552dd9c58d3a5ba5232f084c6649bddc9`. The earlier twelve-file snapshot is superseded, not independent acceptance; reviewer82 re-review remains pending.
- [ ] PC3: CLI delegates only to managed public routes, preserves request errors and never selects an account implicitly.
  EVIDENCE: paused by direction. Failed-first log `/tmp/pr-5769-public-api-79.xfBKWE/cli-boundary-red.log` is preserved. The briefly written production candidate was removed from the live tree and saved as `pc3-deferred.patch` in that directory. `session.go` has no net change and `session_account.go` is absent. Red CLI boundary tests remain. Candidate-only passing results are not current-tree acceptance.
- [ ] PC4: session UI controls respect server capability and committed versus pending state; no optimistic switch success or fallback.
  EVIDENCE: pending failed-first renderer test, implementation and typecheck.
- [ ] PC5: preservation audit, affected build/vet/race, generated drift and independent boundary review complete.
  EVIDENCE: corrected PC2 checks and preservation pass, including 113 frozen lifecycle/design files and 91 protected files. Reviewer82's P2 Unicode finding is locally corrected; independent re-review remains pending. CLI production is absent and its red test unchanged. New guest-protocol drafts are parked outside the tree; earlier guest design remains preserved. No new lifecycle seal is created. Native Windows has no attached runner and hosted execution is unauthorized. Guest retirement, production control-service wiring, CLI/UI, desktop/provider execution and performance gates remain open.

This ledger records a boundary increment, not product completion. The first HTTP regression is green. CLI regressions intentionally remain red while PC3 is paused; full-branch test completion is not claimed.

## Frozen PC2 inventory

The following thirteen files are covered by the corrected `pc2-source.sha256`; its digest is recorded under PC2 above. Final passing logs and the bounded re-review request are in [REVIEW-82-PC2-F1.md](REVIEW-82-PC2-F1.md).

```text
backend/internal/httpd/accounts_manager_controls_test.go
backend/internal/httpd/api.go
backend/internal/httpd/apispec/openapi.yaml
backend/internal/httpd/apispec/specgen/accounts_manager_controls.go
backend/internal/httpd/apispec/specgen/build.go
backend/internal/httpd/controllers/accounts_manager.go
backend/internal/httpd/controllers/accounts_manager_controls.go
backend/internal/httpd/controllers/accounts_manager_controls_dto.go
backend/internal/httpd/controllers/accounts_manager_controls_service_test.go
backend/internal/httpd/controllers/accounts_manager_controls_test.go
backend/internal/httpd/controllers/accounts_manager_controls_validation_test.go
backend/internal/httpd/controllers/accounts_manager_session_controls_test.go
frontend/src/api/schema.ts
```
