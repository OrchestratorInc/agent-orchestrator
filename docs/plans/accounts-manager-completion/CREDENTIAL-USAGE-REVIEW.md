# Credential verification and usage review handoff

Status: bounded correction implemented and ready for independent review. Full PR acceptance remains HOLD. No commit, push, PR edit or publication was performed.

Artifact root: `/tmp/pr-5769-credential-usage-79.AVs5wx`. `review-seal.json` records exact counts and SHA256 digests for the source manifest, archive, patch, preservation audit and evidence manifest. Paths below are relative to this root unless stated otherwise.

## Implementation

Manual keys and imported tokens are checked before commit with bounded, cancellable, non-generating requests. Provider rejection and inability to verify have distinct safe errors. Redirects are forbidden; response bodies and private destinations never enter the public result. A four-request limit bounds concurrent checks.

Verification is an encrypted durable fact tied to the exact normalized credential. Existing records without proof remain stored but cannot authorize new managed work until explicit verification or authenticated reconnect succeeds. Production admission checks committed material, enabled state, generation and verification together. Private protocol version 4 prevents attachment to older acceptance behavior.

Supported quota uses the current account token inside the runner and returns normalized windows, remaining fractions, reset times and observation time. Missing measurements never become zero. Coalescing and a 30-second cache avoid a request per render; errors have a short cache. Quota failures do not revoke credentials, select accounts or change defaults. API keys do not claim subscription-quota support.

The session picker includes usage or an explicit unavailable state. Expanded account details show quota windows, reset/observation times and refresh. A failed refresh labels retained observations as stale. Renderer cache keys include account, generation and observation identity. Manual verification is available for saved keys. All eight locale catalogs contain the new messages.

## Corrections made during implementation

1. Reusing an older runner would bypass manual verification. Both private protocol endpoints now require version 4. The existing old-protocol rejection test covers the previous version.
2. Verification checked separately from committed identity introduced a check-to-use gap. Production route selection and request admission now check proof, enabled state, generation, and committed credential under the same vault lock.
3. A missing quota measurement could become zero usage through numeric zero values. Provider parsers use optional numeric fields, reject missing or out-of-range observations, and retain observation/reset times. Unsupported credentials advertise no quota capability. No provider quota-reset action is offered by the desktop.
4. Cached observations must not cross account or generation changes. Runner results are checked against current vault identity before and after requests. Renderer queries include account identity, generation, update time and verification time. A failed refresh labels any retained observation as stale.
5. Previous synthetic integration fixtures assumed migrated or arbitrary imported credentials could route immediately. They now explicitly verify migrated keys and inject synthetic verification transport only inside the test subprocess. No test needs real provider credentials.
6. A replacement or a replay could inherit verification for previous credential material. `replacement-red.log` preserves all three failing assertions before correction. Verification now includes an encrypted normalized credential fingerprint, and replay may only attest the material actually checked. The unchanged regression passes in the final complete runner suite.

## Failed-first evidence

- Failed-first rejected-key regression: both providers returned HTTP 200 before correction. `verification-red.log`.
- Failed-first managed quota route: both providers returned HTTP 404. `quota-red.log`.
- Failed-first desktop assertions: no usage percentage and no unverified state. `ui-red.log`.
- Credential replacement and replay: three assertions failed before correction. `replacement-red.log`.

## Final verification

Go commands use `-mod=readonly -p=1`, isolated temporary data, `GOMAXPROCS=2`, and `run-isolated.mjs` to remove unrelated credential/session environment variables. Runner commands additionally set `GOWORK=off`.

| Command/scope | Observed result | Log |
| --- | --- | --- |
| Runner `go test -race ./... -count=3 -timeout=240s` | Pass, 17.478 seconds | `runner-final-proof-race.log` |
| Backend management, account service, controllers and API specification packages, `go test -race -count=3 -timeout=240s` | Pass; controllers rerun after final test-only formatting | `backend-verified-race.log`, `controllers-final-race.log` |
| Backend and runner `go build ./...`, `go vet ./...` | All exit 0 | `backend-build.log`, `backend-vet.log`, `runner-build.log`, `runner-vet.log` |
| Backend and runner changed-scope lint against `correction.patch` | Both exit 0, 0 issues | `backend-lint-final.log`, `runner-lint-final.log` |
| Focused frontend suite | 81 tests pass | `ui-expanded.log` |
| Full `npm --prefix frontend test -- --maxWorkers=2` | 341 files, 5419 passes, seven skips; no exclusions | `frontend-final-full.log` |
| `npm --prefix frontend run typecheck` | Exit 0 | `typecheck-final.log` |
| Isolated checkout `npm run build` | Complete Linux x64 Electron package, exit 0 | `frontend-build.log` |
| `npm run api`, then compare generated checksums | Both generated artifacts byte-identical | `generated-drift.log`, `generated-before.sha256` |
| Original protected manifest check | All 91 paths match | `protected-audit.log` |

Initial full frontend diagnostics failed on missing ZIP and a SQLite binary with the wrong Node ABI. The test checkout's existing SQLite dependency was rebuilt for Node; the earlier task-owned, signature-verified ZIP tool was put on the test PATH. No source workaround or suite exclusion was used. The local runtime is Node 22, so pinned Node 24 CI parity is not claimed. Desktop dependencies are separately installed, not linked to this checkout.

Additional compatibility checks: session service and session manager account-control race checks pass. `TestAccountsManagerControlProductionExecution` fails in both managed and native modes with `TARGET_NOT_READY` at the existing readiness assertion. `production-control-baseline.log` reproduces both failures on the untouched pre-correction archive. Its test file is byte-identical in both trees (SHA256 `e83b167841ef6c036838f98a2513cbe39ea60de3dd5cd7510c7a85e90605097d`). `routing-compatibility-race.log` is retained as failing evidence. No unrelated readiness path was changed. The previously documented HTTP shutdown interaction also remains outside this slice.

## Actual desktop evidence

The desktop skill and preview/browser guides were followed. The actual app runs from `/var/tmp/pr-5769-pc5-desktop-79.E7xDus/checkout`, with isolated AO data/profile and a real provider catalog. Inspection asserts the native Electron user agent, preload bridge and exact daemon executable. No mock renderer, mocked endpoint or synthetic provider catalog is used for these captures.

Observed before and after restart:

1. Four existing accounts remain stored and show unverified state. Inventory reads do not automatically verify, delete or select accounts.
2. Both real provider endpoints reject an intentionally invalid synthetic key. The public error is HTTP 400 with `ACCOUNTS_MANAGER_INVALID_CREDENTIAL` and a request ID. The input is cleared and account IDs/generations remain unchanged.
3. Expanded usage and Verify credential are present. No quota percentage is invented for unverified records.
4. App and runner restart retain those records. `runtime-identity.json` matches the running runner hash to the final binary. It is not an old retained runner.

Evidence: `desktop-verify.log`, `desktop-restart-verify.log`, `desktop-result.json`, `runtime-identity.json`; `accounts-unverified.png`, `account-usage-unavailable.png`, `invalid-credential-0.png`, `invalid-credential-1.png`, `accounts-final.png`; `credential-usage-native.mp4`, `frames.json`, `frames.ffconcat`, and `recording-review-frame.png`. Screenshots and a decoded recording frame were inspected. Some captures contain a local account label/email and remain private, not approved for publication. `desktop-selector-diagnostic.log` preserves an initial evidence-script button-label mistake, corrected without product changes.

The isolated app remains running for user testing. The desktop skill led to a scratch-data rebuild and actual Electron verification instead of treating component tests as desktop evidence.

## Boundaries and remaining acceptance

Model-list checks authenticate ordinary API keys without generating a response. Scope-limited tokens may not support that endpoint. A denied permission, unavailable endpoint, rate limit or connectivity failure is not treated as proof of an invalid key. Non-generating verification support for a real scope-limited token remains a live-provider acceptance check.

Quota checks use the same account's token only inside the runner. They do not select accounts, change defaults, mutate bindings, or revoke credentials on a quota-reading failure. Normal requests still receive the provider's own authorization and model-access decisions; verification is an observed authentication fact, not a guarantee of future requests or available credit.

Existing platform containment and production A/B runtime acceptance gates remain separate. This bounded correction is not a claim that the entire PR is releasable. No publication is authorized by this handoff.

- Independent review of this correction is requested; no CLEAR is claimed.
- A successful quota response, reset time and refresh from a real verified supported account remain unobserved. Fixture success is not live-provider evidence.
- Resolve the inherited production-readiness fixture and finish production A/B switch, retry/cancel, in-use removal, queue/revocation and restart evidence.
- Native Windows ownership acceptance, macOS guest containment/keeper-death on both architectures, pinned CI runtime and broader PR checks remain open. No platform lifecycle files were changed here.

Review request: inspect `correction-source.sha256` and `correction-source.tar.gz`, starting with verification/commit ordering, exact-credential proof, runtime admission, quota cache invalidation, secret-safe projection and renderer stale-response behavior. Keep this bounded review separate from unresolved production and platform gates.
