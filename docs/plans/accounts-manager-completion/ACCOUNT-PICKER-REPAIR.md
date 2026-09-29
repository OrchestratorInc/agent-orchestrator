# Account picker and usage repair

Scope: restore the task composer's provider, model and effort controls; put an explicit account dropdown after them; report real account quota and the limits of prior verification. No lifecycle, native credential switching, Subscriptions or automatic account fallback changes. No publication is authorized for this new slice.

## Decisions and order

1. Reproduce the detached picker and empty managed model catalog in renderer tests. Add a regression for quota remaining readable after generation becomes unavailable.
2. Add a trailing control slot to the shared composer without changing clients that omit it. Keep account choice explicit, identify accounts by email when available, retain disabled accounts visibly, and use the existing compact control styling. Status and errors stay separate from the controls.
3. Discover models for the selected managed account only. Pass bounded advertised effort levels through the existing public models contract. Validate managed launch model/effort against that same account catalog through a port, not the native credential catalog. Native launches retain their current path. No invented default model, effort or quota.
4. Separate generation readiness from usage eligibility. Display provider percentages, reset times and observation time, including exhausted accounts. Explain that exact remaining-token counts are not supplied. Never interpret conversational answers as account measurements.
5. Self-review isolation, stale account/model responses, disabled choices and narrow-window layout. Run focused tests, affected races, generation/drift, typechecks and renderer checks. Exercise the actual isolated desktop and report current live percentages separately from earlier A/B execution evidence.

The existing live run proved bounded A/B responses, selected switching/cancellation and daemon reattachment. It did not prove permanent deletion, cold runner recovery, native platform containment or universal provider billing attribution. Those gates remain open.

## Acceptance gates

- [x] P1: failed-first tests reproduce compact picker, scoped model/effort and unavailable-account quota defects
  EVIDENCE: `frontend-red.log` and `backend-red.log` in the local evidence root retain the assertion failures before correction.
- [x] P2: provider/model/effort/account controls work with explicit selection and no native fallback
  EVIDENCE: `frontend-final-focused.log` (167 passed), `shared-ui-full.log` (134 passed), `backend-final-focused.log` (four packages, race count 3), and `desktop-submit.json` (HTTP 201, exact requested account/model/effort). Cross-account late responses, explicit native choice, disabled accounts and unsupported models/efforts have negative controls.
- [x] P3: quota observations remain separate from generation state and never invent remaining tokens
  EVIDENCE: `desktop-controls.json` records all three public quota responses. `desktop-unavailable-usage.json` and the inspected `desktop-usage-details.png` prove two positive quota bars remain visible for a generation-unavailable account. Unsupported credential and verification states remain gated in renderer tests.
- [x] P4: affected tests, types, API generation and protected-path checks pass after self-review
  EVIDENCE: `frontend-full-correct-env.log` (352 files, 5632 passed, 7 skipped); frontend and shared-package typechecks; renderer/shared-package builds; `backend-packages.log`; `session-manager-full-race.log`; final backend build/vet/lint (zero findings); API parity/generation/drift; 91 protected-file and five guest-design hashes; whitespace check. Commands and diagnostic exclusions are below.
- [x] P5: actual desktop flow and fresh three-account observations are recorded with a truthful verification summary
  EVIDENCE: `desktop-response.json` proves the exact assistant reply and requested model from the newly created Chat session, not a prompt echo. `desktop-restart-final.log` verifies three accounts and five binding revisions survived a graceful daemon restart. `desktop-narrow.json` verifies controls fit a 699x1037 emulated viewport in the actual Electron renderer. This does not certify native platform containment or cold runner recovery.

Local evidence root: `/var/tmp/pr-5769-account-picker-79.wFVwLz`.

## Self-review and corrections

- The selected managed model was being checked against an unrelated native catalog. The new optional model-catalog port resolves the explicitly selected account through the existing service. Native discovery retains its existing path, and a missing managed capability never falls back to it.
- The private models endpoint already advertised effort levels, but the public projection discarded them. The projection now passes only bounded recognized levels. Public HTTP coverage rejects private metadata leakage.
- Managed starts require an explicit advertised model in the renderer. Account changes clear model and effort, and query identity includes account and credential generation. Cached or late responses cannot authorize a different account's model.
- The first four-column layout passed interaction tests but truncated the controls in the real desktop. The corrected layout preserves the original provider/model/effort sizing and puts the account dropdown immediately below, with email and quota visible. It leaves shared-package clients that omit the account slot unchanged.
- Generation readiness and quota-read eligibility are separate. A generation error no longer hides a valid quota response. Disabled, unverified, invalid and unsupported credentials still cannot claim readable usage.
- Test harness corrections were kept out of product source: native menus use `menuitem`, not `option`; a modal can be hidden from accessibility queries while its menu is open; account labels also appear in a non-interactive defaults list; daemon readiness can precede run-file publication. Original diagnostic logs remain preserved.

## Verification commands

Go commands ran from `backend`, with Go 1.27.1 and a session-owned temporary directory. Final build/vet/lint used `GOMAXPROCS=2`.

```text
go test -p 1 -race -count=3 ./internal/accountsmanager ./internal/service/accountsmanager ./internal/httpd/controllers ./internal/session_manager -run 'Test(CredentialModels|ManagedLaunch|ManagedModel|AgentAccountModels|AccountModelHTTP)'
go test -p 1 -race -count=1 ./internal/accountsmanager ./internal/service/accountsmanager ./internal/httpd/controllers
go test -p 1 -race -count=1 ./internal/session_manager
go build -p 1 ./...
go vet -p 1 ./...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2 run --timeout=6m --new-from-rev=HEAD ./...
go test -p 1 -count=1 ./internal/httpd/apispec/...
go generate ./internal/httpd/apispec/...
```

Frontend commands used Node 22.23.2. Final full tests used the credential-stripping test wrapper, the existing session-owned zip executable and a Node-compatible native SQLite module, with one worker. Only test dependencies were rebuilt, not product source or the running desktop's dependency tree.

```text
vitest run --config vite.renderer.config.ts --maxWorkers=1
vitest run --config vite.renderer.config.ts --maxWorkers=1 src/renderer/components/NewTaskDialog.test.tsx src/renderer/components/TaskComposer.test.tsx src/renderer/components/settings/AccountUsage.test.tsx src/renderer/components/settings/AccountsManagerSection.test.tsx src/renderer/hooks/useAccountsManagerQuery.test.ts
tsc --noEmit
tsc --noEmit -p tsconfig.e2e.json
vite build --config vite.renderer.config.ts --outDir <evidence>/renderer-build
openapi-typescript ../backend/internal/httpd/apispec/openapi.yaml -o src/api/schema.ts
```

Shared product UI: full Vitest suite, `tsc --noEmit`, Vite library build and declaration build passed. Generation before/after hashes match for both public API artifacts. Existing protected and guest manifests also match.

The first full renderer run was diagnostic: 5506 passed, 126 failed, 7 skipped. It lacked the zip executable and loaded an Electron-ABI SQLite module under Node; two unchanged browser tests also timed out under load. After fixing the test environment, the complete suite passed without changing those product files or tests. The final result is 5632 passed and 7 skipped, not a claim that skipped native/runtime checks ran. The earlier three lint findings were corrected import grouping and missing exported-symbol documentation; the final lint reports zero.

## Real desktop result and limits

The actual isolated Electron app uses `/var/tmp/pr-5769-desktop-final-79.yrWkBr/checkout`, scratch state under that lab's `ao-home/desktop`, and the public daemon on `127.0.0.1:43429`. The touched production files matched the prior committed baseline before mirroring; the new daemon was built from this worktree. No real home directory or credentials were copied. Captures were inspected locally and not published.

- The normal task dialog offered all three account emails and quota summaries. It required explicit account and model choices. The unavailable account remained visible but disabled.
- Account B, an advertised model and High effort created `standalone-5` with HTTP 201 and binding revision 39. The actual assistant returned `PICKER_MODEL_B_OK`; its recorded response model matched the request. No terminal model workaround was used. The disposable session remains available for inspection.
- Graceful daemon restart preserved all three account identities and five session bindings. This was runner reattachment, not cold runner/vault recovery.
- The positive-quota settings view remains readable for the unavailable account, including window resets and observation time. Native `/usage` is not integrated with this managed quota view. Asking a session to state its token budget does not measure subscription quota.

## Still open, not claimed fixed

1. The unavailable account has readable quota again, but its generation-error status was not automatically cleared or revalidated by this UI repair.
2. The earlier pending-switch restart failure remains open. This pass did not retry, cancel or replace that failed operation.
3. After the new session's daemon restart, its public saved model still matched the selected model, but the Chat composer displayed a different model and permission choice. That discrepancy is recorded in the local desktop diagnostic and requires separate runtime/UI reconciliation. Model/effort persistence is not certified.
4. Safe permanent deletion and crash recovery remain blocked by the existing retirement failures. No account was deleted in this pass.
5. Other-provider managed Chat, isolated native profiles, explicit legacy-token migration/reconnect, native Windows execution and Mac arm64/x64 containment retain their prior gates.
6. Earlier live A/B concurrency, selected switch/cancel and Chat queue tests remain bounded evidence in `parallel-session-delivery/user-flow/LIVE-RESULTS.md`. They are not replaced by these picker tests. Cold runner restart, permanent provider revocation and comprehensive recovery remain unverified.
7. This correction still needs independent review. Main integration and the broader backend release failures are not resolved by the four passing affected packages. No commit, push or PR update occurred in this slice.
