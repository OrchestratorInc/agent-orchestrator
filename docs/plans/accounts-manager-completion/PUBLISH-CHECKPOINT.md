# Account-management checkpoint

Date: 2026-09-28. Status: draft, release gates open.

This is the current status for the accumulated branch. Earlier slice reports remain historical evidence for their exact snapshots; a bounded review clearance does not certify the complete product.

## Included

- Encrypted credential storage, admission checks, browser/device login, verification and refresh, safe public inventory, and provider-reported usage.
- Durable per-session bindings and coordinated switch, retry, cancellation, removal and restart-recovery machinery. Account choice is explicit. A failed selected account must not trigger automatic fallback.
- Public daemon routes, thin HTTP CLI commands, desktop session controls, removal impact/confirmation, and production service construction.
- Failed-first regressions and the staged design/review records. The source contains deliberately preserved failing acceptance tests for unresolved lifecycle and execution requirements.

## Verification at this checkpoint

| Check | Result |
| --- | --- |
| Complete frontend suite, Node 24.21.0 | 341 files passed; 5,432 tests passed, seven skipped; 304.95 seconds |
| Frontend and E2E TypeScript checks | Passed |
| Linux x64 Electron package, Node 24.21.0 and Go 1.27.1 | Passed; the first preflight used the system Go 1.26 outside a module, then passed with the required toolchain explicitly selected |
| Backend and runner build/vet, Go 1.27.1 | Passed |
| Complete runner race suite | Passed |
| SDK callback package race suite | Passed |
| Complete backend race suite | Completed with five failing packages; see below. Session manager, SQLite migrations/store, HTTP and public CLI packages passed |
| Full backend lint | 86 findings; narrower slice lint results do not clear this gate |
| Full runner lint | 27 findings |
| OpenAPI, frontend API types and SQL generation | Passed with no source drift |
| Formatting and whitespace | Passed |
| Pinned offline secret scan of all 319 accumulated changed files | No leaks found |
| Protected native-switching and Subscriptions inventory | All 91 paths unchanged |
| Native Electron inventory and usage refresh | Passed; two windows match HTTP 200 responses; selection and credential identity unchanged |

The original remote PR series was reconciled with a normal history merge. Its resulting source tree is identical to checkpoint `ed7e8de18`; no force push or automatic update to current main was used. Current main is 38 commits beyond the prior integration base. A read-only merge preview identifies six conflicts, so base integration remains open.

The five failing backend packages are `adapters/chatdriver/persistenthost`, `adapters/runtime/tmux`, `daemon`, `observe/activity`, and `terminal`. Their complete logs are preserved; the suite was neither filtered nor cancelled. The native desktop lab's 256 changed implementation/test files also match this checkpoint byte-for-byte.

## Remaining implementation, in order

1. Close the production execution and lifecycle blockers. `TestAccountsManagerControlProductionExecution` still reaches `TARGET_NOT_READY` for managed and native targets. `TestProviderOwnerEscapedDescendantBlocksRetirement` demonstrates both an incorrect retirement acknowledgement and account deletion completion while a moved descendant survives. The production cancellation test returned 409 where its schedule expected 202 in the full run, then passed three isolated repetitions; reproduce and classify the intermittent schedule before changing its assertion.
2. Implement the supported macOS containment boundary and complete exact shutdown/recovery coverage. The guest design is a feasibility plan, not a shipped runtime. Native Windows Job Object recovery still needs execution on Windows; compatibility-runtime limitations must not weaken ownership guards.
3. Finish account-selection integration: distinguish native Harness login from managed account availability, finish explicit initial per-session choice, and correct setup-token classification/expiry/usage eligibility. Confirm the supported subscription-authentication architecture before treating model-request acceptance as approval to collect or proxy subscription credentials. Preserve API-key routing and explicit user choice.
4. Resolve current-main conflicts and full lint findings, then review the complete integrated snapshot. Keep the existing native switching and Subscriptions paths protected. No automatic fallback is a product requirement, not deferred functionality.

## Remaining verification

- Repeat actual simultaneous account-A/account-B sessions through the production stack, including explicit switch, retry, pre-stop cancel, queue preservation, in-use deletion, crash recovery, daemon restart, revocation and authorization leases. Confirm unrelated native and managed sessions continue unchanged.
- Finish the native macOS arm64/x64 and native Windows ownership/recovery matrices. A cross-compile is not runtime evidence. Exercise the remaining interrupted/corrupt ownership-proof, reuse and escaped-descendant cases.
- Diagnose the tmux, observer and terminal-attachment failures. The tmux/observer failures persisted in three serial repetitions using a test-owned socket directory. The observed failures include server exit and working-directory mismatch; no assertion was weakened. The HTTP shutdown fixture has separately reproduced failures on untouched control code; this checkpoint does not change that server or fixture to hide them.
- Obtain independent review of the production wiring/execution supplement, credential validation and usage corrections, then the final integrated tree. Re-run complete required checks and measure account-control responsiveness after corrections.
- Complete the remaining real desktop workflows and live-provider checks. The evidence below proves inventory and usage refresh only. Hosted/native platform jobs and unrelated Cloud/product-package workflows are not claimed locally passed by this checkpoint.

## Desktop evidence

[Screenshots and recording](evidence/README.md) were captured from the actual isolated Electron app with a real provider catalog. Only personal account labels are masked. Unsupported usage is displayed as unavailable, not invented quota. The recording is 3.8 seconds long.

Local verification logs and exact manifests are preserved under `/tmp/pr-5769-publish-79.p3jxPQ`. No credentials, private daemon data, raw provider responses, or unredacted account screenshots are included in the branch.
