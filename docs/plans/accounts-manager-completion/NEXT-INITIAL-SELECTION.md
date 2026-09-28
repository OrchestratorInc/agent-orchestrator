# Initial account selection: bounded implementation

Status: bounded implementation on switching checkpoint `2ec9d7c80006a6e0afc6e7bea36dea88228571ec`. Backend focused and full affected race checks, build, vet, lint and contract checks are green. The final renderer snapshot passes 208 focused tests, all 350 frontend files (5,583 tests, seven skips), both typechecks and Linux desktop packaging. Independent review and live managed-account desktop execution remain open.

## Contract

An optional initial account object carries an explicit connection mode and public account ID. Managed mode requires exactly one account belonging to the resolved harness provider. Native mode forbids an account ID. An omitted object retains the existing explicit saved-default/native behavior for older clients. It must never pick the first usable inventory entry or write a new default.

The renderer and CLI send the user's choice. Neither calls a runtime, reads a credential store, or turns a failed managed choice into native mode. Account IDs are public references; no key, route token, private endpoint, or controller handle enters this request.

Validate the choice after resolving the harness but before workspace provisioning or provider execution. An explicit managed choice may bypass device-global authentication readiness, but never installation checks, account eligibility, deletion admission, or interface compatibility. Preserve the existing native readiness behavior.

## Durable creation boundary

1. Extend a narrow store capability for session creation with its initial binding in one SQLite transaction. Reuse session numbering and automation idempotency; do not create a second session allocator. Reject a deleting account in the same transaction that inserts the binding.
2. A prepared hidden session requires an equivalent atomic promotion plus binding. Do not expose a promotable/restoreable session between recording its creation and recording its explicit account intent. Async Chat must not start its background controller before this commit.
3. Existing automation-session adoption must compare the durable account intent instead of silently accepting a conflicting repeated request. Missing storage/service capability for an explicit selection fails closed; it must not use the old unbound creation path.
4. Provider route minting still performs current binding and deletion checks at launch. Selection validation is not an authorization lease. Deletion after validation, before commit, or before first request must prevent execution through the selected account.
5. Keep native app-server code and the protected inventory unchanged. Managed app-server execution is a separate following slice; reject unsupported explicit interface combinations before creating a row until that slice is verified.

## Acceptance and order

First failed-first command (from `backend`, credential environment stripped): `go test -mod=readonly -v -count=1 ./internal/httpd/controllers -run '^TestSpawnInitialAccount'`. The current handler ignores the account object. The regression must observe that ignored explicit choice or an invalid choice reaching the session service, not a compile failure.

Wire choice: `account: {mode: "managed", accountId: "public-id"}` or `account: {mode: "native"}`. Selecting a displayed saved default sends that concrete account choice; no new automatic selector or third connection mode is introduced. The optional field must reject explicit null, duplicate/case-aliased account members, and invalid nested member names without changing unrelated legacy spawn decoding behavior.

- [x] A1: failed-first HTTP test proves the initial account object reaches the session boundary rather than being silently ignored. Reject invalid/null/ambiguous choices with request IDs and zero launches. Preserve omitted requests.
  EVIDENCE: initial-account-http-red.log and initial-final-backend-race3.log; initial request and delegation tests pass three race repetitions on the final backend snapshot.
- [x] A2: store tests prove atomic session/binding creation, deletion-vs-creation serialization, rollback, reopen, concurrent A/B creation, native preservation, and automation idempotency/conflict behavior.
  EVIDENCE: initial-account-store-red.log and initial-final-backend-race3.log; full store race also passes in initial-final-backend-race.log.
- [x] A3: manager tests prove the first launch sees the chosen durable binding, prepared-session promotion and async launch obey the same boundary, and stale/default changes cannot replace an explicit choice.
  EVIDENCE: initial-account-launch-boundary-red.log and initial-final-backend-race3.log; the production construction test uses real router/service/manager/SQLite boundaries with synthetic provider/runtime adapters.
- [x] A4: managed selection works with device-global auth unavailable when installation and the selected route are valid. Native selection retains the existing auth gate. Unavailable account services, wrong provider, disabled/removed accounts, and unsupported interfaces launch nothing.
  EVIDENCE: initial-account-client-boundaries-red.log, initial-account-background-red.log and initial-final-backend-race3.log; managed app-server Chat stays unsupported pending its separate implementation.
- [x] A5: thin CLI flags require an explicit valid mode/account pair, preserve daemon request IDs, and distinguish usage errors from runtime errors. Desktop creation shows capability/availability, selected account, explicit native choice, and stale selection without optimistic success or fallback.
  EVIDENCE: initial-final-backend-race3.log and initial-renderer-v2-focused.log establish the HTTP/component boundaries. Actual Electron evidence below covers empty inventory only, not live positive-account execution.
- [ ] A6: regenerate contracts, run affected race and frontend suites, self-review the first complete creation path, verify protected hashes, and freeze a bounded review package before expanding managed Chat.

Each implementation sub-slice records exact test names and commands before its first edit. Current inspection found the missing fields in the spawn DTO/config, a non-transactional standalone session insert, a separate prepared-row promotion, and a device-auth gate ahead of the manager. Addressing only the picker would leave the launch/restart boundary incomplete.

## Midpoint review and corrections

The production construction check uses the public router, real session service, manager and SQLite store with synthetic catalog/runtime adapters. Explicit A, B and native launches preserve the saved default. It does not establish live-provider acceptance.

- The desktop uses task delegation as well as standalone spawn. A failed-first delegation test proved that endpoint also discarded the choice. Both endpoints now share strict account-member validation and forward the same intent.
- Older daemons can ignore an unknown JSON field. `GET /sessions/account-selection` advertises actual manager/store support; clients must require positive capability before sending explicit intent. A missing capability is not native mode.
- Optional background title calls use an unbound provider process. Failed-first tests proved a managed binding could fall through to device credentials there, and during launch when the router was absent. Managed sessions retain their deterministic local title until a separately owned, routed background task exists. Native background tasks hold an input lease through completion so an account change cannot overtake their admission. Binding read failures fail closed. This does not narrow foreground account switching.
- A fake Chat launcher validates manager-to-controller binding publication, not the service's later route minting. The async test now checks that actual boundary; route minting remains covered by service and production integration tests.
- Optional account objects are non-null in the generated contract, matching the decoder. The store maps only exact supported harnesses, rather than accepting names with a matching suffix.

Failed-first evidence under `/var/tmp/pr-5769-next-79.wos4rh`: `initial-account-http-red.log`, `initial-account-store-red.log`, `initial-account-launch-boundary-red.log`, `initial-account-client-boundaries-red.log`, `initial-account-background-red.log`. Passing bounded evidence: `initial-account-http-first-green.log`, `initial-account-store-first-green.log`, `initial-account-launch-first-green.log`, `initial-account-production-first.log`, `initial-account-client-boundaries-green.log`. Later final-snapshot checks must supersede these as relevant.

## Desktop discovery correction

The first real desktop run exposed a missing boundary in the shared task harness menu: device-auth filtering can hide an installed harness even when a verified managed account is available. Add an opt-in eligibility input for the task composer only. Preserve native readiness records, installation checks, other menu callers and explicit account selection. Discover account capability independently of the currently selected harness, but make no local account requests for cloud tasks. No account becomes a default implicitly.

Model discovery uses an unbound device process. Pending or managed account choices must not trigger that discovery, its refresh, or its revalidation, and must not display a cached native catalog as evidence for a managed account. Keep explicit model entry and saved project configuration available. Explicit native mode and legacy capability-false behavior retain device model discovery.

- [x] D1: the real task dialog, without a mocked harness field, exposes an installed managed-capable harness despite device logout, requires an explicit account and sends that exact choice. Missing installation, missing capability or stale/unusable inventory does not unlock it.
  CHECK: npm test -- src/renderer/components/NewTaskDialog.test.tsx -t 'managed account discovery'
  EXPECT: passed
  CWD: frontend
  EVIDENCE: initial-account-desktop-boundary-red.log preserves the absent menu option; initial-renderer-v2-focused.log passes the real component cases and eight negative controls.
- [x] D2: managed and pending account choices never request or refresh the device model catalog and do not inherit its cached default. Explicit native mode remains a positive control.
  CHECK: npm test -- src/renderer/components/NewTaskDialog.test.tsx -t 'account model isolation'
  EXPECT: passed
  CWD: frontend
  EVIDENCE: initial-account-desktop-boundary-red.log preserves unexpected native refresh; initial-account-native-preflight-red.log preserves the managed submit's device probe. Both corrections and explicit native controls pass in initial-renderer-v2-focused.log.
- [x] D3: rerun the complete affected frontend checks, inspect a fresh isolated desktop capture, and verify all protected paths before the review freeze.
  EVIDENCE: initial-renderer-v2-results.json records all six commands at exit 0 and unchanged tested hashes; initial-renderer-protected.log records all 91 paths. Native screenshots and recording frames were inspected from initial-desktop/correction-captures-v3. Live managed-account desktop behavior remains open.

Midpoint review kept account eligibility separate from device readiness rather than rewriting the native catalog. Only the task composer opts into the shared field extension. All existing project-setup callers retain their native behavior. A pending account capability temporarily shows model loading instead of an editable empty catalog, and rechecking an already-known native choice does not discard its cached model or reasoning selection. The native model refresh callback is absent for a managed choice, including when native results are already cached. Managed submit skips the device-only advisory probe; the daemon still validates installation and the selected account.

The first broad correction check exposed one introduced union-type mismatch. The comparison now uses a predicate rather than passing an unrestricted harness-derived string to a provider-union lookup. Typechecking and all later evidence must come from the `initial-renderer-v2-*` logs, not the failed earlier run.

The real isolated Electron app uses an untouched device-login catalog and an empty managed inventory. Native-unavailable harnesses remain hidden without a verified account; the Accounts panel reports zero accounts. Screenshot and four-second native-window recording are under `initial-desktop/correction-captures-v3`. This proves unavailable-state behavior only. No credentials were copied from another profile and no provider task was started. Real managed-account positive selection, concurrent A/B execution and live-provider identity remain explicit release gaps.

## Managed Chat follow-up constraints

The protected native driver accepts a child environment and a deferred environment callback, but owns its process arguments and preflight. Its preflight opens an unscoped process for model discovery. Reusing that preflight unchanged would perform provider work before the new managed binding exists.

The first managed-only composition candidate is a separate driver wrapper with its own preflight and session-owned configuration home, reusing the existing exported conversation driver for the wire protocol. Configuration may name the restricted route-token environment variable; the token itself must not be written to configuration. Do not copy a device home or share its credential store. Local version/help inspection found version 0.153.4 with explicit configuration overrides and stdio transport; this is not a passing managed launch test.

Before adoption, prove effective provider precedence, rejected ambient overrides, no unbound model discovery, private credential-store behavior, reconnect to the exact host generation, and stable history across an explicit account change. An account-scoped configuration home alone does not establish containment. Keep native/global readiness events separate from managed controller events. If these boundaries require changes to a protected driver, document the exact missing extension and obtain the bounded design review before editing that driver.
