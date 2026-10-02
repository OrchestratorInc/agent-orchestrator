# PC4 desktop controls, bounded review request

Request: reviewer82 should review the exact renderer-only snapshot below, including uncertainty/recovery semantics and the removal confirmation boundary. This is a local, bounded UI handoff, not PR completion or release clearance. P6 and P7 remain HOLD for the explicit integration and verification gaps below. Do not change the frozen service, CLI, lifecycle, guest, native switching or Subscriptions surfaces as part of this review.

Worktree: `/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79`.
Branch: `ao/agent-orchestrator-79/accounts-manager`.
HEAD: `048a59775999b60f8276a1f5d1107dbef57f5483`.
Artifacts: `/tmp/pr-5769-pc4-79.hoSn6v` (all artifact names below are relative to this directory).

## Exact source seal

| Artifact | Count or SHA256 |
| --- | --- |
| `pc4-source.sha256` | 22 files; `cf6b09cab3371f7193ab8d3ad4fc181e46780ead9929c157e7e72ae137eb28ce` |
| `pc4-source.tar.gz` | 22 files; `5de5502766adf5332bc36edca5f8ae1031eeb30262f9029d116ec34d8377ac03` |
| `pc4-only.patch` | 22 files; `c78a536c8ed096e765ba7adbb9ccb038a972d2afcf2e0c4586ebe0cd74e7323f` |
| `pc4-source-seal.json` | Exact per-file hashes, six additions, 16 modifications and passing reverse-apply dry run |
| `ui-baseline.sha256` | Seven pre-PC4 files; `447f0b051d6db35d9f3f8f1fe9653d568fadad691c87e3b504e80102bdcf2200` |

The patch is relative to the accepted PC4 starting bytes, not to HEAD. The seven-file baseline and eight locale originals are in `ui-baseline.tar.gz` and `locales-before-pc4.tar.gz`; the original SessionView test is from HEAD. Earlier accepted locale/source edits remain intact. The broad inherited branch diff must not be mistaken for this slice. The archive is independent of later live-tree state.

Exact source list:

```text
frontend/src/renderer/components/SessionAccountControl.test.tsx
frontend/src/renderer/components/SessionAccountControl.tsx
frontend/src/renderer/components/SessionView.test.tsx
frontend/src/renderer/components/SessionView.tsx
frontend/src/renderer/components/settings/AccountRemovalControl.test.tsx
frontend/src/renderer/components/settings/AccountRemovalControl.tsx
frontend/src/renderer/components/settings/AccountsManagerSection.test.tsx
frontend/src/renderer/components/settings/AccountsManagerSection.tsx
frontend/src/renderer/hooks/useAccountsManagerQuery.test.ts
frontend/src/renderer/hooks/useAccountsManagerQuery.ts
frontend/src/renderer/i18n/de.json
frontend/src/renderer/i18n/en.json
frontend/src/renderer/i18n/es.json
frontend/src/renderer/i18n/fr.json
frontend/src/renderer/i18n/ja.json
frontend/src/renderer/i18n/ko.json
frontend/src/renderer/i18n/pt-BR.json
frontend/src/renderer/i18n/zh-CN.json
frontend/src/renderer/lib/accounts-manager-controls.test.ts
frontend/src/renderer/lib/accounts-manager-controls.ts
frontend/src/renderer/lib/api-client.test.ts
frontend/src/renderer/lib/api-client.ts
```

## Implemented contract

The session dialog is available from both local terminal and Chat headers, without altering their native account menus. Committed binding is displayed separately from pending operations. Mode/account and timing must be selected explicitly. Revision, policy, operation ID and bounded request-ID diagnostics are visible. No response optimistically changes the committed account.

Only validated non-secret recovery references are persisted. A reference is stored before submission; lost responses retain the exact ID and request. Remount reads status without creating or resending anything. An unresolved operation blocks a fresh operation; a deliberate same-ID resend remains explicit. Older terminal state cannot hide a later pending operation. Ownership, modes, phases, revisions and safe identifiers are checked at the HTTP boundary. Retry/cancel requests are bodyless.

Removal starts with impact preview. Confirmation binds to the exact returned revision, including zero, and refresh resets confirmation. Retry/cancel first verifies operation ownership. Missing records remain uncertain. Only a daemon terminal response can show completion, and inventory invalidation occurs after observed completion. The old direct deletion action is not called. Saved removal references are available even when the account is absent from inventory. Unavailable capability never exposes a destructive submit action.

Existing inventory/add/login controls keep the daemon boundary. Login cancellation receipts acknowledge a request without inventing terminal cancellation or credential revocation. Cached inventory errors disable mutations. Late GET responses cannot overwrite a newer event revision. Ten new route templates prevent IDs leaking into telemetry labels. New controls, accessible labels, confirmations and safe error strings use all eight existing locale catalogs, 92 additions per catalog.

## Failed-first evidence and self-review

| Evidence | Observed defect and correction |
| --- | --- |
| `session-path-red.log`, `session-path-initial.log` | Initial four-failure session boundary became green. |
| `first-path-typecheck.log`, `first-path-typecheck-fixed.log` | Introduced TS2322 from an empty body on bodyless retry/cancel; corrected without API changes. |
| `midpoint-red.log`, `midpoint-correction.log` | Eight failures reproduced stale terminal masking, lost-response intent and validation/diagnostic issues. The null-safe follow-up passes. `midpoint-initial-green.log` is a failed intermediate run despite its historical name. |
| `removal-red.log`, `removal-initial.log` | Four failed-first removal boundaries became 12 passing tests. |
| `settings-boundary-red.log`, `removal-settings-green.log` | Three failures at inventory/removal and cancellation-acknowledgement integration became green. |
| `inventory-diagnostics-red.log`, `inventory-diagnostics-green.log` | Three request-ID/unavailable diagnostics failures became green. |
| `recovery-review-red.log`, `recovery-review-green.log` | Two self-review failures: completed saved intent masking later pending work and permissive reference writes; corrected. |
| `refresh-row-red.log`, `public-ui-focused.log` | Refresh error request ID was missing; corrected. |
| `inventory-late-read-red.log`, `final-focused-1.log` | Late revision 2 GET replaced revision 3 event state and resurrected inventory. Existing revision-selection logic now governs query structural sharing too. |

`PC4-MIDPOINT.md` and `PC4-REMOVAL-REVIEW.md` preserve the staged review decisions. The initial full suite also caught untranslated strings. Localization is corrected, not excluded. No source edit followed the final 22-file verification snapshot.

## Final verification

Commands were launched through Fish. Local versions: Node 22.22.0, Go 1.27.1, installed TypeScript/Vitest and lockfile dependencies. Resource-heavy commands ran serially with `GOMAXPROCS=2`; Go used `-mod=readonly -p=1`. Test temporary files used `/var/tmp/ao79.uF9PZF`. The credential-stripping wrapper is `/tmp/pr-5769-rebase.reOkRW/run-isolated.mjs`.

| Command or check | Result | Evidence |
| --- | --- | --- |
| `npm test -- src/renderer/hooks/useAccountsManagerQuery.test.ts src/renderer/components/settings/AccountsManagerSection.test.tsx src/renderer/components/SessionAccountControl.test.tsx src/renderer/components/settings/AccountRemovalControl.test.tsx src/renderer/lib/accounts-manager-controls.test.ts --maxWorkers=1` in main `frontend/`, repeated three times | 64 tests, five files, each exit 0 | `final-focused-1.log` through `final-focused-3.log` |
| `npm test -- --maxWorkers=1` in the isolated checkout's `frontend/` | 340 files; 5,395 passed, seven skipped, 522.59 seconds; exit 0 | `final-frontend-tests.log` |
| `npm run typecheck` in main `frontend/` | Exit 0 | `final-typecheck.log` |
| `npm run typecheck:e2e` in main `frontend/` | Exit 0; typecheck only, not E2E execution | `final-typecheck-e2e.log` |
| `npm run build` in isolated `frontend/` | Complete Linux x64 Electron package, including daemon/runtime resources; exit 0 | `final-frontend-build.log` |
| `go build -mod=readonly -p=1 ./...` and `go vet -mod=readonly -p=1 ./...` in `backend/` | Both exit 0 | `final-backend-build.log`, `final-backend-vet.log` |
| Serial `go test -race` for `./internal/httpd/... ./internal/accountsmanager ./internal/service/accountsmanager` | Root HTTP package fails shutdown deadline; other packages pass | `final-backend-affected-race.log` |
| Root HTTP package `-race -count=3` | One shutdown deadline failure | `http-package-repeat.log` |
| Shutdown endpoint alone `-race -count=3`; untouched-HEAD root HTTP package `-race -count=3` | Both controls pass | `http-shutdown-repeat.log`, `untouched-head-http-race.log` |
| Installed `openapi-typescript` generates YAML into `schema.generated.ts`; compare with frozen frontend schema | Byte-identical; exit 0. API specification generation/parity tests also pass in the affected package run | `generated-drift.log`, `final-backend-affected-race.log` |
| `git diff --check`, source and preservation manifests | Exit 0 | `final-diff-check.log`, `sealed-source-live.log`, preservation evidence below |

The isolated checkout has its own `npm ci`, not linked dependencies. The first diagnostic full-suite run failed on localization, missing zip fixtures and a SQLite Node ABI mismatch. The task-owned zip binary came from a signature-verified distribution package; a clean lockfile install resolved the Node ABI environment. The final complete suite excludes no failing files. The prior 5,394-pass isolated run predates the late-read fix and is superseded. The final suite has 5,395 passes. Linux packaging rebuilt Electron-native dependencies after that suite.

No backend lint success is claimed by this renderer-only slice. The frontend defines no independent lint command. Full repository lint/full-engine/native-platform suites were not rerun here. CI uses Node 24; this local Node 22 run is not pinned-CI parity. Native platform jobs and the complete renderer smoke/E2E CI matrix remain separate verification gaps.

## Actual desktop evidence

Native lab: `/var/tmp/pr-5769-pc4-desktop-79.YrcqJM/checkout`, detached at the same HEAD with an exact source overlay. `desktop-source-inventory.json` records 5,559 entries and SHA256 `bbe9cb65eb701f174569cdd094bc02282cb264a40c9570b6e3f368432b07a373`. One repository gitlink is recorded but not copied. No mock bridge, mock page or synthetic provider catalog was used.

The launch uses a private mounted home and AO state. Inside the app, data is `/home/ghoul/.ao/pc4/data`, run file `/home/ghoul/.ao/pc4/running.json`, and Electron profile `/home/ghoul/.ao/pc4/electron`; these map under the lab's `ao-home/pc4`. Actual renderer URL is `http://localhost:5173/`, and the private daemon is `http://127.0.0.1:39778`. The compiled daemon executable belongs to this checkout. The native window has the dev marker and real preload bridge; the capture helper asserts the Electron user agent and bridge before acting.

The desktop skill and AO preview/browser guides were followed. Native-window interaction used its separate inspection connection, not the AO Browser panel runtime. Linux launch needed X11, disabled GPU/background throttling, and `--no-sandbox` because the environment rejected both Chromium sandbox forms. An outer read-only-root/private-home filesystem isolation remained active. These launch limitations are recorded in `launch-desktop.fish` and the preserved failed launch logs. This is not a production security configuration or a native macOS/Windows result.

Observed in the actual app:

1. Existing Accounts and Subscriptions navigation coexists. Add options are real. One intentionally invalid synthetic API key is saved through the actual daemon, displayed as Saved and never represented as authenticated.
2. No default account or routing is selected implicitly. Explicit default selection and clearing leave routing off. Refresh is disabled for the saved-key account, with no request sent.
3. Removal impact returns 501, preserves a safe request ID and offers capability refresh without a removal submit action. Manual missing-operation recovery also stays unavailable; no cancellation/completion is invented and the account remains visible.
4. App and daemon restart retain the same account ID/generation and routing-off/default-cleared state. Daemon PID changes in `desktop/before-restart.json` and `desktop/after-restart.json`. The runner remained alive across this restart; this is not cold-vault-reopen or cold-coordinator recovery evidence.
5. The app was stopped after evidence capture. Exact executable/start identities were checked before cleaning the private residual terminal. `desktop-processes-stopped.json` records zero owned processes; the app/daemon listeners are closed. The encrypted scratch data and captures remain preserved.

Inspected images: `desktop/accounts-empty.png`, `desktop/removal-unavailable.png`, `desktop/explicit-default-route-off.png`, `desktop/restarted-account-preserved.png` and frames extracted from the actual recording. Supporting captures include `desktop/account-saved-unverified.png`, `desktop/recovery-unavailable.png`, `desktop/removal-unavailable-refreshed.png` and `desktop/refresh-disabled.png`.

Recording: `desktop/pc4-native-flow.mp4`, SHA256 `a681fff7a73cc2a5c9f1d933194f2803cf3d775d1446763617490bc127956cad`, 10.875 seconds including final hold. Its 60 native frames and timestamps are preserved. It shows explicit default selection/clearing and unavailable removal/recovery in the real app. It does not show successful account switching. Restart capture SHA256: `25e47d6cd00fef6e012bc38304d94d29207e8d24af77c0c3ef9ea028d3ca0ba7`.

## Preservation and open gates

`sealed-preservation.json` SHA256 `abdd1f2f1e28207573c2c325e010698cb82d36ebfb2a05711f914d0901b3b3ba` verifies these unchanged manifests and prior PC3 archives:

| Frozen manifest | Count | SHA256 |
| --- | --- | --- |
| `/tmp/pr-5769-pc3-f1-79.sJGjN3/pc3-source.sha256` | 13 | `83b9f328c6c880124ef515e00c2201d81c0e2a2e99f976bbf698c4ab5cddfd6f` |
| `/tmp/pr-5769-pc3-f1-79.sJGjN3/pc3-f1-correction.sha256` | 3 | `ebacb99ce22b90fe24a445485bbd75b430a3a0e505b0e6eb1a928d7c41cab474` |
| `/tmp/pr-5769-pc2-f1-79.E8WKAc/pc2-source.sha256` | 13 | `a3618c483f9411738333c1c0870f921552dd9c58d3a5ba5232f084c6649bddc9` |
| `/tmp/pr-5769-public-controls-79.PbwFW0/lifecycle-guest-frozen.sha256` | 113 | `ffc37c82bafedcf7223478d3d4a6ff6aefa97dea9f04b615b63ef1df66d3e96b` |
| `/tmp/pr-5769-public-controls-79.PbwFW0/protected.sha256` | 91 | `462fd9b445fe2d6764f6ac0d8371aedb742aed22071bbb4afb2e6b943fca6358` |
| `/tmp/pr-5769-macos-guest-design-79.7xIxWq/review-package.sha256` | 5 | `bcb421310148ec0976f2fb406b82de8e93e2d61003cc1c1690852a8adfca2d3a` |

PC3's 15-file slice and 26-file evidence checks also pass, recorded in `sealed-pc3-slice.log` and `sealed-pc3-evidence.log`. Generated YAML and TypeScript bytes remain frozen. `sealed-scope.json`, SHA256 `3f37be9a7d0a01b06a675d50433f53dba270888d4911b9ae88fe79643077dc7d`, compares all 5,558 copied regular source entries: no desktop checkout byte changed after its final source snapshot, and the only live-tree changes since that snapshot are PC4 gate/review documents. No new CLI, HTTP, lifecycle, guest or protected-path delta is included.

Required follow-up remains explicit:

- Investigate the five-second HTTP shutdown race independently. It reproduces in the integrated package but not in the isolated test or clean-HEAD controls. No root cause is established. Do not classify it as passed or confirmed inherited.
- Supply the optional coordinated-control service through a separately authorized, reviewed wiring slice. Production daemon construction currently omits it, so session control and coordinated removal return 501. UI tests with synthetic HTTP responses cannot certify real stop/revoke/switch/delete behavior.
- Run authenticated login/refresh, per-session selection, switch/retry/cancel, complete removal, cold recovery and unrelated native coexistence in the real app once capabilities and local test credentials permit. Login forms were inspected, but no external provider login was initiated. Private native account storage reports `account_storage_unsafe`; its protected implementation was not changed.
- Retain native Windows, macOS guest retirement and pinned-CI verification gaps. No guest or lifecycle adoption claim is made.
- Obtain independent PC4 review of this immutable UI snapshot before further integration. No commit, push, PR edit, publication or external evidence upload has occurred.

Final slice/evidence manifests and their digests are recorded in the external `pc4-review-seal.json`, avoiding a self-referential handoff hash. `pc4-slice.sha256` and `pc4-slice.tar.gz` contain the 22 sources plus this handoff, PC4-GATES.md, PC4-MIDPOINT.md and PC4-REMOVAL-REVIEW.md. `pc4-evidence.sha256` and `pc4-evidence.tar.gz` preserve final results, failed-first and superseded diagnostic logs, scope/preservation records and actual desktop captures. No credential store or scratch account data is included. The ledger records six met gates and three unmet gates (P6, P7, P9), with none abandoned. Stop source edits at this snapshot and route the exact seal for read-only review.
