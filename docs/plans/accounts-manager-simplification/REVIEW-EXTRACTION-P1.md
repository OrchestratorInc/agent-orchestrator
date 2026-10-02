# P1 dependency candidate review handoff

Date: 2026-09-30. Status: local candidate sealed, independent review requested. Cutover, source removal and publication have not occurred.

## Review target

- Candidate commit: `f8e08347b8f7667bfaf26de2e59081dc346fc644`.
- Parent upstream commit: `c93978c4ea2e908255a2a06c37599fda3651554a`.
- Candidate directory: `/var/tmp/pr-5769-extraction-79.4UyUrH/candidate`.
- Evidence root: `/var/tmp/pr-5769-extraction-79.4UyUrH`.
- Consumer baseline: `43578e5cba1565bf08f1332656b2e0873bc1e125`, unchanged.

| Immutable artifact, relative to evidence root | Count | SHA256 |
| --- | ---: | --- |
| `candidate-source.sha256` | 1,364 files | `5b0e4e5334ad2f9633e5588f631bebea2c84c0add50d40bce8d07d21339ee5db` |
| `candidate.tar.gz` | Same 1,364 files | `5980eaecdfa25a519251eba2cddf24604285bf47a2c3ee7108c9fec34960b7b9` |
| `engine-source.tar` | Original 1,362 files | `d9a6ad163fe0bbcca5bd5971d54124ea3f41f4919b7ada5c90fa3572f42b0a83` |
| `evidence.sha256` | 97 logs/manifests/helpers | `1ba9a08b5639b7d2db5661ef135a983dd78f848639e09ee3ad0e87b65c6c55f6` |

`freeze.json` indexes these artifacts and every command result. `upstream-delta.json` and `patches/` account for eight modified, three added and 222 previously excluded upstream files. The 1,351 remaining files match upstream. Candidate runtime, test, fixture and asset bytes match the embedded consumer tree; only contributor-document references, `DISTRIBUTION.md` and the new verification workflow differ for distribution. Retained file modes also match.

## Commands and observed evidence

Each log label has `.command.json`, `.log` and `.exit.json`. The harness uses `GOWORK=off`, `GOMAXPROCS=2`, a session-owned temporary directory and an environment allowlist that excludes provider credentials. Local execution used Go 1.27.1, Linux amd64. Engine commands ran from the candidate directory unless noted.

| Label | Command | Result |
| --- | --- | --- |
| `candidate-focused` | `go test -mod=readonly -race -count=3 -p 1 -timeout=8m` in the three patched packages, exact test filter in command record | Pass, 100.914 seconds |
| `candidate-ordinary` | `go test -mod=readonly -p 1 -json -count=1 -timeout=15m ./...` | Pass, 230.123 seconds; 9,952 leaf passes, eight skips |
| `candidate-race` | `go test -mod=readonly -race -p 1 -json -count=1 -timeout=15m ./...` | Fail, 271.505 seconds; 9,947 leaf passes, 11 skips, one failure |
| `candidate-build` | `go build -mod=readonly -p 1 ./...` | Pass |
| `candidate-vet` | `go vet -mod=readonly -p 1 ./...` | Pass |
| `candidate-tidy` | `go mod tidy -diff` | Pass, no module drift |
| `cross-linux-amd64`, `cross-linux-arm64`, `cross-windows-amd64`, `cross-darwin-amd64`, `cross-darwin-arm64` | `env GOOS=<os> GOARCH=<arch> CGO_ENABLED=0 go build -mod=readonly -p 1 ./...` | All five pass; native execution not established |
| `candidate-workflow-lint` | `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 <candidate>/.github/workflows/embedded-verification.yml` | Pass |
| `candidate-integrity-final` | `node verify-candidate.mjs` | Pass, exact 1,364-file inventory |
| `preservation-final-2` | `node verify-preservation.mjs` | Pass, consumer inventory and seven prior manifests |

The only full-race failure is `internal/client/codex/live::TestPionMediaRelayBridgesAudioAndDataChannel`, with `upstream DataChannel was not created` at `media_test.go:268`. The earlier untouched-baseline command and three-repeat failure are preserved under `/var/tmp/pr-5769-cleanup-79.Eur8Lb/media-head-control.*`. This inherited failure remains an open release gate. The new dependency CI keeps the full race check active. No upstream test was weakened or removed during extraction.

The five-target runner dependency baseline is in `dependencies-summary.json` and target JSON files. Each target resolves 110 engine package records with source/test/assembly/embedded-file hashes. This is the baseline for the future cutover comparison, not evidence that an external version has already resolved.

## Failed controls retained

| Label | Removed behavior | Observed failure |
| --- | --- | --- |
| `without-callback-red` | Restored stock upstream callback interface in a disposable fixture | Compile rejection for required listener/delivery fields |
| `without-delimiter-red` | Restored stock translator | Delimiter and response-isolation assertions fail |
| `without-license-red` | Moved fixture license to a recoverable external path | Integrity check rejects missing license |
| `without-catalog-red` | Moved fixture catalog to a recoverable external path | Build rejects missing embedded catalog |
| `packaging-without-engine-red` | Existing desktop builder, no embedded sibling tree, fake Go command supplies a module-cache license | Assertion confirms hard-coded sibling license path fails |

The packaging fixture is deliberately a path-boundary test. It does not prove real module fetch, build or package acceptance. Consumer code remains unchanged. The initial preservation helper's absolute-path resolution error is recorded in `preservation-final.*`; the corrected complete pass is `preservation-final-2.*`.

## Preservation

All 5,590 consumer baseline entries remain unchanged outside the new simplification documents. This includes the embedded engine, pending compact UI work and existing recovery guards. No account, credential, listener or running session was touched.

The prior seal `/var/tmp/pr-5769-compact-switch-79.cnQmJt/source-seal` still verifies:

| Manifest | Files | SHA256 |
| --- | ---: | --- |
| `source.sha256` | 15 | `91ca847dc0dd85049eb82765181f9856ab9e308a018022a7181bfcd897e63659` |
| `docs.sha256` | 2 | `44f9da0d476b005d0460ebc17ff844b1fc1994fa4e9bc48a92907f84156a402f` |
| `protected.sha256` | 91 | `3438055a85087fe789dfb0aa5f2dc583ab52dac8a51bb683e0100647b096396f` |
| `guest.sha256` | 5 | `bcb421310148ec0976f2fb406b82de8e93e2d61003cc1c1690852a8adfca2d3a` |
| `generated.sha256` | 2 | `68f22515838f73329569cc60634ce2b90404ec46ea3e931a231d3051a16e74f3` |
| `shutdown.sha256` | 3 | `911e15c0850d1c31d6680cea396a29ef5c48c28cc04b17c5d233873bc271d8c5` |
| `untouched-browser-import.sha256` | 7 | `ca6eb28888f8478bbf8356570c34bde4e5cf41ede31a44d98db2dad839b1a9a1` |

## Self-review and independent request

Self-review checked the complete upstream delta, all retained patch tests, license/catalog ownership, compile-only platform claims, test failure accounting, unchanged consumer source, file modes and the archive's content against the candidate commit. The full race result is explicitly red. The ordinary suite does not certify that red check. No binary identity or public-fetch claim is made before cutover.

Request an independent read-only review of this exact candidate for patch fidelity, source/asset/test completeness, distribution-only changes and CI responsibility. Report bounded findings before any pin change or embedded-tree removal. No independent verdict has been received.

## Remaining boundary

Publication approval was requested for a public `accounts-proxy` fork under the project organization and this exact candidate on `ao/agent-orchestrator-79/engine`. No reply or publication has occurred. Repository access and a named dependency maintainer must be established before cutover; an inaccessible repository is not a usable dependency.

After approved publication and independent review: let Go resolve the commit, verify a fresh-cache fetch/module archive/checksums, then implement runner pin, packaging identity/license resolution and exact-dependency CI. Only after those checks may the archived embedded tree be removed. Public source-fetch verification, offline warm-cache build, installed startup, integrated behavior and native platforms are still open. Terminal wait-for-turn still has its separately recorded essential failure.

This bounded implementation changes no visual behavior, so new screenshots are not applicable. Existing UI changes stay outside this slice. No consumer commit, push, PR edit or diff reduction is claimed.
