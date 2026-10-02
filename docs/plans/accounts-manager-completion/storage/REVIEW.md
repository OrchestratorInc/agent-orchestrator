# Credential storage implementation review

Status: the foundation is connected to `Serve`. The approved callback extension is integrated, legacy management writers and file watching are disabled, and private key/import commands acknowledge encrypted persistence. Device transport, draft recovery, reconnect identity/CAS, and manual refresh coalescing now have synthetic integration evidence. Binding revocation, remaining maintenance, and complete lifecycle/platform verification remain open. This increment is not release-ready.

## Integration review, session 79

- Runtime admission compares committed generation, metadata, attributes, and transport settings. SDK memory publication alone does not authorize a request.
- Public SDK model registration replaces watcher registration. Access-only credentials do not acquire a false refresh-success timestamp.
- Integrated tests exposed a metadata race: SDK normalization adds an explicit disabled flag. The vault now canonicalizes that flag before durable commit; twenty repeated race runs pass.
- Full runner build, vet, and race suite passed after integration. The daemon management adapter race suite passed. These are current local checks, not a full backend/platform release run.
- The SDK callback test attached its simulated browser to the URL-delivery context, making response reads race with callback completion. The simulated browser now has an independent lifetime; 100 callback race repetitions and the complete authentication package pass. Production listener cancellation remains bounded.
- Device sign-in now uses a direct SDK authenticator in a private subprocess with an encrypted result pipe. The old file writer is removed. [Device evidence](DEVICE.md) covers bounded parsing, cancellation, and durable completion.
- Supported draft gateway credentials migrate as an encrypted batch before plaintext cleanup, retaining their old indexes. [Recovery evidence](MIGRATION.md) covers interrupted cleanup, changed sources, tombstones, and restarts. Unsupported source shapes remain untouched and fail closed. No native credential files are discovered or imported.
- Actual A/B runner requests exposed a separate default SDK error logger. Request-log=false does not disable that error path. The public request-logger factory now returns nil; rejected requests create no log files. Ten integration repetitions and the final runner build/vet/race suite pass.
- Reinspection corrected an earlier assumption: the SDK serializes refreshes but does not coalesce simultaneous forced refresh requests. Runner-level coalescing and a four-account pool are now tested. A late forced refresh cannot overwrite a completed reconnect or rename; SDK registration epochs and durable generation admission both remain in force.

The sections below retain the earlier foundation review as historical evidence. Their unwired-state and pending-decision statements describe that earlier increment, not the integrated tree.

## Implemented

- Authenticated encryption with a separate random installation key, fresh nonces, authenticated account identity/provider/generation, and encrypted operation records.
- Owner-only storage checks, confined file access, atomic encrypted replacement, single-writer locking, and fail-closed handling of key loss, tampering, and ambiguous directory-sync failure.
- Opaque account identity and stable SDK index across restart. Provider storage objects become encrypted metadata without invoking their file writers.
- Durable cancellation, including cancellation before start; idempotent completion; durable deletion tombstones; rejection of late SDK writes after removal; pending-operation cancellation on restart.
- Tests for concurrent completion/cancellation and deletion/refresh. The plaintext scanner has a positive control so a broken absence check cannot pass unnoticed.

The key-file model does not protect against a process able to read both ciphertext and key as the same OS user. Windows builds, but its ACL enforcement and replacement durability still require actual runtime evidence. Reconnect identity checks, credential-generation replacement, account status commands, and runtime admission are not implemented by this foundation.

## Pinned SDK boundary evidence

The executable diagnostics in `accounts-manager/runner/internal/runner/sdk_persistence_contract_test.go` establish these limitations using fake values and private temporary directories:

| Path | Observed behavior | Required integration |
| --- | --- | --- |
| Management browser callback | The public callback helper writes the authorization code as plaintext. The management login workers read that file directly. | Replace the callback handoff or use a direct authenticator with a safe listener. A token-store override does not intercept this path. |
| Import | The handler writes the supplied credential directly, even with a rejecting encrypted store attached to the runtime manager. | AO-owned bounded import parsing and durable commit before runtime registration. |
| Runtime update | The SDK reports success and updates memory even when the store rejects the write after deletion. The vault remains deleted. | Validate live credential generation at request admission; do not treat SDK update success as durable acknowledgement. |

Relevant source:

- `accounts-manager/engine/internal/api/handlers/management/oauth_sessions.go`: callback serialization and file publication.
- `accounts-manager/engine/internal/api/handlers/management/auth_files_provider_oauth.go`: management login workers poll plaintext callback files.
- `accounts-manager/engine/internal/api/handlers/management/auth_files_crud.go`: import writes precede runtime registration.
- `accounts-manager/engine/sdk/cliproxy/auth/conductor_lifecycle.go`: registration/update persistence errors do not prevent runtime publication.

API-key configuration also persists through configuration handlers rather than the token store. The current device-login subprocess explicitly creates a file store. Neither is wired to the new vault yet.

## M0 decision needed

The direct SDK authenticators avoid the management callback files and return credential objects in memory. However, their callback servers bind `:<port>` on all interfaces. `sdk/auth/interfaces.go` exposes a callback port but no callback host, listener, or in-memory callback receiver. `internal/auth/codex/oauth_server.go` demonstrates the listener behavior; the other supported callback implementation has the same boundary. Starting those listeners would violate AO's loopback-only rule.

Safe alternatives inspected:

1. Management callbacks plus a custom token store: insufficient, callback files bypass the store.
2. Direct SDK authenticators: insufficient without a loopback listener option. `NoBrowser` suppresses browser launch, not the listener.
3. Custom watcher/model registry: useful for credential reload and model registration, but does not replace either callback transport.
4. Device sign-in and explicit key/import paths: can be implemented without this browser extension, but withholding browser sign-in is a product-scope decision.

Recommended next decision: authorize a minimal pinned-SDK extension for a caller-supplied loopback callback listener/address, retaining upstream defaults and provider exchange logic. Then run direct authenticators in cancellable private workers, send their result to the sole vault writer, and fence completion by operation state. Preserve Subscriptions and all native account-switching modules. Do not copy provider OAuth logic, use reflection into private SDK fields, or weaken the listener/storage rules.

Until that decision, this work does not enable another login method or change the vendored engine. There is no safe claim that the remaining feature is complete.

## Verification

Observed on Linux:

- Focused storage and pinned-SDK diagnostics: race detector passed, including concurrent cancellation and removal tests.
- Runner `go build ./...`, `go vet ./...`, and `go test -race -count=1 ./...`: passed.
- Focused daemon adapter, service, and controller regression suites: passed under the race detector.
- Windows amd64 and macOS arm64 `go build ./...`: passed. These are compile checks, not platform runtime or packaging validation.
- Final gate execution: five storage checks passed; live integration and full persistence/platform review remain unmet. All six product release gates remain unmet. No gates were abandoned.
- Protected-path comparison against integration base `b398a95c59425c381ec2f3d36d507097c9ccc2de` found no changes to the inventoried Subscriptions/native account-switch paths. Vendored engine diff is empty; `git diff --check` passed.

No live provider login or credential migration was attempted. All test credentials are synthetic. No frontend changes were made in this increment, so new visual evidence is not applicable. Earlier desktop evidence covers only the earlier increment. Nothing was committed or pushed.
