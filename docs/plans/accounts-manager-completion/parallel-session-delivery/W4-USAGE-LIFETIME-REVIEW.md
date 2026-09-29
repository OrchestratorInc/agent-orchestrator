# W4 usage lifetime review

Read-only independent review requested. Base: `ec7efde0652f21690d8e98e2dcfa71d5b132e422`. All work is local; no publication or release claim.

Evidence root: `/var/tmp/pr-5769-w4-usage-79.WpzXO2`.

## Exact source

Five files under `accounts-manager/runner/internal/runner/`:

- `credential_quota.go`
- `credential_runtime.go`
- `credential_refresh.go`
- `serve.go`
- `credential_quota_lifecycle_test.go`

Source manifest: `final/source.sha256`, SHA256 `eec3416f5bfc6032ec7bbf0ca5d930a10ecceadac47f27493480373390b37eb8`.

Source archive: `final/source.tar.gz`, SHA256 `370af7813099f0b0cae2ab2732eb4ee08c24fe3f5c304302a56ac7bca6f2c9d8`.

The final `FREEZE.md` records documentation, evidence and preservation manifests. The archive is independent of later live-tree work.

## Correction and review focus

One bounded worker belongs to all interested readers of an account/fingerprint. Individual cancellation stops only that reader; the last departure cancels the worker. Account removal, disabling, generation replacement and runtime shutdown cancel the appropriate workers. Shutdown closes admission under the same lock as worker registration and joins every worker before vault closure. The existing four-check limiter is unchanged.

Review wait-group admission, last-reader cancellation versus new admission, result publication, fingerprint changes, account fencing, lock order, and removal/disable failures. A late worker cannot replace a newer cached flight. A deadline reports usage unavailable independently of account authorization. Existing provider endpoints, quota eligibility, public contracts, native profiles and UI are unchanged.

The private server constructor accepts a test-only setup callback before reload. The production wrapper passes nil. The server-level fixture exercises actual loopback HTTP and encrypted vault reopening with an injected local transport; it does not contact a provider.

## Failed-first and self-review evidence

- `lifetime-red.log`: first-reader cancellation poisons the remaining reader; close returns before transport drain; another request is admitted after close.
- `lifetime-prefixed-source.tar.gz`: original four-file reproduction snapshot, SHA256 `db013050996dce7cba0dd59429216d5eaf9e21878cc07a0193ab1790fd7c9251`.
- `deadline-red.log`: midpoint regression reports 409 for an upstream deadline. Corrected to 503 while preserving account authorization.
- Disable cancellation was moved immediately after the durable state change, before manager registration, so a later registration error cannot leave the old usage request running.
- Server fixture cleanup now cancels, releases and joins both the client and server on failure. Final checks ran after that edit.
- `lint-first.log` is an invalid relative-config invocation, not a source failure. The corrected absolute-config commands report zero findings.

## Final verification

From `accounts-manager/runner`, Go 1.27.1, Node 24.21.0 credential-stripping wrapper, `GOWORK=off`, `GOMAXPROCS=2`, session-owned temporary directory, `SHELL=/bin/sh` and `GIN_MODE=release`:

| Command | Log | Result |
| --- | --- | --- |
| `go test -mod=readonly -json -race -count=3 -timeout=180s ./internal/runner -run 'Test(CredentialQuota|CredentialRefresh|CredentialAutoRefresh|RunnerParallelSessions)'` | `final-focused.log` | Pass, 28.802s, 78 top-level/153 leaf executions, no skips. |
| `go test -mod=readonly -json -count=1 -timeout=240s ./...` | `final-full.log` | Pass, 10.592s, 107 top-level/269 leaf executions. |
| `go test -mod=readonly -json -race -count=1 -timeout=240s ./...` | `final-full-race.log` | Pass, 14.640s, 107 top-level/269 leaf executions. |
| `go build -mod=readonly ./...` | `final-build-confirm.log` | Exit 0. |
| `go vet -mod=readonly ./...` | `final-vet-confirm.log` | Exit 0. |
| Pinned v2.13.2 full runner lint with the absolute backend configuration | `final-lint.log` | Zero issues. |

The full suites report the command package as having no tests; no individual test was skipped. Additional Windows amd64 and Mac arm64/x64 cross-build logs are compilation evidence only. Exact completion statuses are in the freeze report.

`final-preservation.log` matches the 91-path protected inventory, 33 generated files, nine-file retirement seal, 17-file W1 integrated manifest, 25-file W2 integrated manifest and five-entry guest package. `final-diff-check.log` is clean. Archive members are checked against the source manifest after final commands.

## Remaining gates

Independent review is pending. Real provider quota permissions and credentials, native Windows and Mac execution, production containment, managed Chat/profile isolation, desktop workflows and full integrated verification remain open. The three existing positive retirement groups remain failing on the separate safe-guard snapshot. This runner pass does not clear them. Backend-only behavior needs no new screenshot in this leaf.
