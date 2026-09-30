# Accounts Manager upstream engine

The runner compiles CLIProxyAPI from a publicly fetchable, pinned Go dependency.
No source download occurs when the packaged application starts.

- Upstream: https://github.com/router-for-me/CLIProxyAPI
- Tag: `v7.3.8`
- Commit: `c93978c4ea2e908255a2a06c37599fda3651554a`
- Fork: https://github.com/Ayash-Bera/CLIProxyAPI
- Fork commit: `f8e08347b8f7667bfaf26de2e59081dc346fc644`
- Go replacement: `github.com/Ayash-Bera/CLIProxyAPI/v7 v7.0.0-20260929230337-f8e08347b8f7`
- License: MIT; copied from the verified module as `CLIProxyAPI-LICENSE`

`runner/go.mod` selects the dependency. `dependency.json` records the full source
identity, checksums and license hash; packaging fails if these disagree. Builds
disable Go workspace overrides, verify cached modules, inspect the binary's
replacement module and bundle both notices and the dependency record.

Account lifecycle, storage, routing policy and product integration stay in this
repository. The fork retains the patches below and its own test workflow.

This is an embedded-library distribution. The standalone server, terminal UI,
alternate credential stores, examples and upstream release tooling are omitted.
AO builds `runner/cmd/ao-accounts-manager`; it does not run the upstream server.

The pinned fork omits these upstream standalone-only paths:

```text
.github/
cmd/
examples/
internal/auth/empty/
internal/cmd/
internal/store/
internal/tui/
.dockerignore
Dockerfile
docker-build.ps1
docker-build.sh
docker-compose.cluster.yml
docker-compose.yml
```

It adds its own `.github/workflows/embedded-verification.yml` for dependency
builds, tests and cross-compilation instead of the upstream release workflows.

These private packages are used only by the omitted standalone entry points.
Keep `internal/homeplugins`, all public SDK packages, embedded catalogs,
`config.example.yaml`, test fixtures, the license and local patches below.
The example configuration is read by an executor regression. Original upstream
reference documentation may describe commands omitted from this distribution.

## Updates and rollback

The Accounts Manager maintainers own review of fork patches and security updates.
Publish a reviewed immutable fork commit before changing the consumer pin. Resolve
its canonical version with Go, update `runner/go.mod`, run `go mod tidy` and update
`dependency.json` and this record together. Do not select a branch or mutable tag.

Run the dependency tests in an exact checkout, the full runner suites, packaging
boundary tests and all five cross-builds. Compare production/test source and asset
closures before accepting a new revision. Native platform acceptance is separate.
No unrelated module upgrade belongs in a pin change. Revisit an exclusion if a new
upstream version imports that path.

Rollback restores the preceding pin, checksums and provenance record as one
reviewed change. Existing credentials and session bindings need no migration for
this packaging change. Preserve these local patches until upstream supplies their
tested equivalents. A failing dependency verification must stop packaging rather
than select another source.

## Local callback extension

`sdk/auth/LoginOptions` accepts an owned loopback callback listener and a private
authorization-URL delivery function. The two existing browser authenticators use
the in-memory callback receiver when supplied; their default flows and provider
exchange implementation remain available. Callback state and redirect binding
are checked before delivery, errors omit callback values, and cancellation closes
the listener. Callback-related logging no longer prints authorization codes or
state values.

The extension and boundary tests live in `sdk/auth/listener_callback*.go`; the
only existing SDK files changed are `interfaces.go` and the two browser
authenticator entry points. Preserve or reapply this surface when updating the
snapshot. Credential persistence and lifecycle integration stay in the runner.

## Streaming event delimiter correction

The Codex-to-Responses stream translator preserves the empty line terminating
an event after a data line. The executor scans upstream lines without their
line endings; forwarding an empty chunk previously lost that delimiter and
held a data-only event until the next event or stream closure. Leading empty
events remain empty so they do not commit a request before its first payload.

The correction and regression live in
`internal/translator/codex/openai/responses/codex_openai-responses_response.go`
and `stream_delimiter_test.go`. The runner's overlapping-session tests verify
client delivery before the upstream stream is released. Preserve or reapply
this boundary when updating the snapshot; it adds no account-selection policy.

## Registration test isolation

`sdk/cliproxy/service_auth_sync_test.go` observes which account's registration
completed instead of assuming concurrent worker order. Its cleanup joins the
blocked batch and same-revision waiter before removing global model fixtures.
Both enqueue orders retain the per-account completion assertion. This test-only
change does not alter production registration or scheduling.
