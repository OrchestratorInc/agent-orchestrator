# Accounts Manager simplification specification

Date: 2026-09-30. Status: proposed implementation contract, documentation only.

## Decision

Move the patched proxy engine out of this PR into an immutable, AO-maintained Go dependency. Keep account policy, encrypted credentials, session isolation and recovery in this repository. Externalize first; review integration duplication afterward. Do not combine dependency extraction with runtime rewrites or an upstream upgrade.

The dependency is still compiled into the bundled runner. This introduces no new remote service, request-routing hop or runtime source download.

Implementation order and acceptance gates: [PLAN.md](PLAN.md). Planning verification: [GATES.md](GATES.md).

This specification narrows the packaging and cleanup scope of the earlier [production specification](../accounts-manager-completion/production-readiness/SPEC.md). It does not declare its unresolved safety requirements complete. Advanced guest-containment implementation may be deferred, but exact teardown remains mandatory for any operation that claims it stopped or deleted a bound account. All existing evidence and source archives remain intact.

## Measured starting point

Measured from exact Git objects, independently reconciled with PR metadata:

- Base: `e853b39601876d4be6d51c7f99dce0adcea5fa10`.
- Published head: `43578e5cba1565bf08f1332656b2e0873bc1e125`.
- Comparison: `git diff --numstat <base>...<head>`.
- Local compact-switch edits are outside these published counts and must be preserved separately.

| Scope | Changed files | Added lines | Deleted lines | Binary files |
| --- | ---: | ---: | ---: | ---: |
| Published PR | 1,878 | 602,994 | 4,776 | 31 |
| Embedded engine tree | 1,362 | 532,876 | 0 | 22 |
| Engine Go tests, a subset of the engine row | 658 | 292,955 | 0 | 0 |
| Arithmetic remainder without the engine tree | 516 | 70,118 | 4,776 | 9 |

These are diff lines, not handwritten production lines or a measure of installed size. The 70,118 figure is an estimate before dependency metadata, packaging and documentation changes. Externalization does not remove engine code or its maintenance cost from the product. A normal follow-up commit can reduce the PR's final net diff without rewriting existing branch history; old Git objects remain in history.

## Essential product contract

| Capability | Required behavior after simplification |
| --- | --- |
| Secure credentials | Keep encrypted storage, validation, refresh/reconnect, joined refresh shutdown and secret-safe errors. A provider rejection must not become a verified account. SDK persistence failures must not bypass vault admission. |
| Explicit choice and parallel sessions | User chooses managed account or native mode. Session A/account A and session B/account B run concurrently without shared credential-home contamination, silent repinning or automatic fallback. Preserve provider/model/effort compatibility checks. |
| Safe switching | Preserve stop-now and wait-for-current-turn, durable revisions, exact runtime ownership, readiness acknowledgement, queue retention and history. Publish committed state only when the server confirms it. Cancellation and retry must respect irreversible boundaries. |
| Revocation and restart | Deny revoked or stale leases on every logical request, including after daemon/runner restart. Keep tombstones, binding generations, durable journals, idempotent recovery and rejection of stale callbacks or refresh writes. |
| Removal and unaffected sessions | Exact stop/revoke acknowledgements precede credential removal. Unknown ownership never authorizes input, termination or completion. Unrelated managed and native sessions survive; native bindings without an account association are not guessed to belong to a managed account. |

Retain account inventory, add/sign-in, refresh, usage eligibility, explicit initial selection and public HTTP/CLI/desktop controls. Usage remains provider-reported quota or explicitly unavailable, never an invented remaining-token balance. Existing Subscriptions and native switching stay unchanged.

## Ownership boundary

```text
Desktop / thin CLI
        |
Public daemon API and account services
        |                         |
Bindings, switch/removal journals  Session controllers and queues
        |
AO accounts runner: vault, admission, exact-account routing
        |
Pinned patched engine dependency: SDK, provider protocol and translation
```

| Keep in this repository | Move to the dependency | Eligible for later audit only |
| --- | --- | --- |
| Runner and its integration tests | Retained upstream engine implementation | Unreachable experimental containment implementations |
| Vault, leases, route admission and revocation | Upstream regression tests and their fixtures | Duplicate wrappers with identical ownership and error semantics |
| Daemon supervision and narrow service/port boundaries | Engine callback, delimiter and test-isolation patches | Repeated historical prose, preserving immutable review records |
| Session bindings, switching, queues, removal recovery | Engine module files and embedded catalogs | Proven dead feature-only scaffolding |
| Public contracts, generated API/SQL, desktop controls | Upstream license source and original provenance | Nothing merely because it looks complex or has many tests |

Required runtime guards, durable migrations, generated contracts and regression coverage are not pruning candidates. Do not treat all provider code outside the visible product scope as dead: SDK registration and shared imports can still depend on it.

## Dependency contract

### Distribution choice

Use an AO-controlled, publicly fetchable source fork with the existing canonical engine module/import path. The runner's versioned module replacement points to one immutable fork revision resolved by Go, with committed checksums. Public fetchability is required for a clean contributor or CI build without private repository credentials.

The destination repository and its publication approval are prerequisites, not artifacts created by this specification. Until the reviewed dependency is fetchable, retain the embedded tree. Do not substitute an unpublished local path, a mutable branch, a startup download or an unpatched upstream release.

Go supports a module-path/version replacement; the runner must retain a matching requirement. Use Go to derive the canonical revision version, and record the full Git commit independently. Module checksums and `go mod verify` are complementary to provenance review, not proof of implementation safety. See the [Go module replacement reference](https://go.dev/ref/mod#go-mod-file-replace) and [verification reference](https://go.dev/ref/mod#go-mod-verify).

### Initial revision

Start with the exact retained engine bytes currently in this branch, traced to upstream `v7.3.8`, commit `c93978c4ea2e908255a2a06c37599fda3651554a`. Preserve the previously approved standalone exclusions and module cleanup. Do not restore omitted server/deployment components or upgrade transitive versions during extraction.

The initial fork must reproduce all retained implementation, test, fixture and embedded-data bytes except separately reviewed dependency-distribution metadata. Its reviewed delta against upstream includes exclusions and module-file changes, not only the three patches named below. Check module-archive filtering and platform build tags before claiming equivalent contents.

### Required patches

| Patch group | Contract and proof to retain |
| --- | --- |
| Owned browser callback listener | Preserve `sdk/auth/LoginOptions`, listener/address extension, both browser authenticator integrations and `listener_callback*.go`. Loopback binding, state validation, listener ownership transfer, cancellation and redaction remain mandatory. No plaintext callback file may enter the product login path. |
| Streaming delimiter | Preserve the Responses translator correction and `stream_delimiter_test.go`: data-only events arrive before the next event/stream close; leading empty lines do not commit an account; delimiter state stays per response. Keep the runner's overlapping-session regression. |
| Registration test isolation | Preserve `sdk/cliproxy/service_auth_sync_test.go` corrections for per-account completion, both enqueue orders and joined cleanup. Do not drop this test-only delta as irrelevant to distribution. |

The current [upstream record](../../../accounts-manager/UPSTREAM.md) is the starting inventory, not proof of an exhaustive patch audit. Compare against the pinned upstream object before packaging and account for every difference.

### Reproducibility and maintenance

1. Record canonical module, replacement module, Go-resolved version, full fork/upstream commits, module checksums, license checksum, patch inventory and retained-tree checksum in a small dependency record. Keep one authoritative pin in runner `go.mod`; fail checks when metadata disagrees.
2. Resolve and verify the exact dependency before building. Do not patch the shared module cache or fetch mutable source during application startup. Keep `GOWORK=off`, including clean-clone checks, so a local workspace cannot mask a missing pin.
3. Verify a fresh isolated module cache and a populated-cache build with module-network access disabled. Packaged application startup must not require a source repository, Go installation or dependency download. Provider network access is unaffected by this requirement.
4. Assign an AO maintainer for patch rebases and security updates. Every update reviews source/patch changes and reruns the same contract tests; mutable tags and broad dependency upgrades are not acceptable shortcuts.
5. Keep a recoverable source snapshot and prior pin. A missing module, mismatch or unsupported build must fail with an actionable error, not fall back to a different engine or account.

## Build, packaging and test responsibility

Current coupling is concrete: runner `go.mod` replaces the engine with `../engine`; [the desktop builder](../../../frontend/scripts/build-accounts-manager.mjs) copies its license from that tree; [the nested-module workflow](../../../.github/workflows/accounts-manager.yml) runs both local modules; [the package verifier](../../../frontend/forge.config.ts) checks the bundled executable and notices.

After extraction:

- The runner resolves the pinned module normally, without an engine sibling directory or committed dependency source/archive disguised elsewhere in the PR.
- Packaging resolves the verified module location through Go's structured metadata and copies its license. Preserve the current bundled notice filenames and checks. Verify the packaged executable's replacement module/version using `go version -m` against the dependency record; the existing upstream version string alone is insufficient. Add bounded patch-revision provenance without exposing paths, credentials or private endpoints; retain daemon/runner protocol compatibility.
- Engine tests run against an exact fork checkout in its own CI and in the dependency acceptance job for this PR. Runner tests continue here. The runner's `go test ./...` result must not be presented as an engine-suite result.
- Cover Linux amd64/arm64, Windows amd64, and Mac amd64/arm64 builds. Native execution, signing and packaged app checks remain separate gates; cross-compilation is insufficient.
- Compare normalized production/test dependency closures and required source/embedded assets on all five targets. Expected module build-information changes can change binary hashes; do not demand or falsely report byte-identical executables solely to certify a local-to-remote replacement.

Preserve public APIs, wire formats, database schema, launch policy and user account IDs through extraction. No credential export/import or database migration is required for a packaging-only change. Cache warming must not contact live accounts.

Compare first-stream delivery, switch readiness and desktop responsiveness against the recorded pre-extraction baseline under the same workload. Investigate a material regression instead of accepting a smaller diff as compensation. Do not convert a fresh build's dependency-download latency into a runtime regression claim.

## Containment deferral and safe refusal

Guest VMs are not a prerequisite for basic per-session account routing. This decision permits deferring their production integration and reviewing unused experimental code for a later change. It does not redefine process disappearance, a missing descriptor or a replaced runtime handle as proof of descendant retirement.

| Evidence available for this exact launch | Allowed outcome |
| --- | --- |
| Exact stop and revocation proven; all bound sessions acknowledged | Existing removal coordinator may progress to its durable final cleanup |
| Retirement unavailable on this platform/runtime | Expose capability/unavailable reason; do not begin irreversible deletion or report completion |
| Deletion already started but stop evidence becomes unknown | Keep tombstone/admission fence and encrypted credential; retain recoverable operation ID; no new authorization, fallback or false success |
| Foreign, reused or ambiguous runtime identity | Never signal or destroy the replacement; require recorded-owner proof through the existing recovery boundary |
| Cancellation wins before the irreversible phase | Release only that operation's fences and preserve queues according to the existing cancellation contract |

UI and CLI must distinguish revoking future managed requests from complete credential deletion or proof that all descendants stopped. Manual process termination is not automatically a verified retirement acknowledgement. Safe switching must also refuse when its own ownership/stop proof is unavailable. Do not remove a mechanism needed by a supported switch merely because its deletion feature is gated.

Keep the native-platform capability matrix explicit. Windows native execution and Mac arm64/x64 containment remain unverified release gates unless new evidence closes them. Deferring VM work must be reflected in the supported-feature declaration, not hidden behind a generic production-ready label.

## Known baseline failures remain visible

The latest isolated live desktop run completed four scenarios: Chat stop-now and wait-for-turn passed; terminal stop-now passed; terminal wait-for-turn failed after source completion with `SOURCE_NOT_QUIESCENT`. Account A and its revision remained unchanged. Both Chat queues survived. Eight original bindings stayed unchanged.

The terminal failure must remain a named essential switching gate and be fixed in a separate behavior slice before release. Extraction must preserve the failure reproducer, not make it disappear by deleting the test or disabling the timing choice. Local evidence: `/var/tmp/pr-5769-midprompt-79.17t6Bj/REPORT.md`, sealed manifest digest `f4c5391535ebb3912bac5d5546d5d0602b3c5e60e7a14708c12f9335cd0992da`.

The earlier [remaining-work ledger](../accounts-manager-completion/production-readiness/REMAINING.md) records additional retirement, isolation, native-platform and integrated-suite gaps. An inherited engine media race failure also remains recorded. None was rerun or fixed by this specification. Cleanup may be reviewed as behavior-preserving while known failures are explicit; that is not production approval.

## Acceptance

Extraction is accepted only after the exact dependency is fetchable and verified, all retained patches/assets/tests have owners, clean builds no longer require `accounts-manager/engine`, packaging retains license/provenance, and no account behavior or protected path changes inadvertently. Final counts must be remeasured against the then-current base and compared by category.

Product release requires the essential account contract, including the failing terminal drain path, to pass on every advertised supported mode. Broader native/containment capabilities remain explicitly unavailable until verified. No repository, commit, push, PR change or dependency publication is authorized by writing this plan.
