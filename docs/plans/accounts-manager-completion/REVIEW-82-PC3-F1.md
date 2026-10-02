# PC3-F1: cancellation acknowledgement re-review

Ready for reviewer82. This supersedes the live source snapshot in REVIEW-82-PC3.md and addresses only the P2 finding in `/tmp/pr-5769-pc3-review-82.txt`. The prior handoff, gate document and twelve-file source archive remain unchanged as historical evidence. No UI, lifecycle, guest, native switching, Subscriptions, provider or desktop work is included. No commit or publication occurred.

## Exact frozen package

Workspace: `/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79`.
Artifact root: `/tmp/pr-5769-pc3-f1-79.sJGjN3`.
Branch: `ao/agent-orchestrator-79/accounts-manager`.
HEAD: `048a59775999b60f8276a1f5d1107dbef57f5483`.

| Artifact under the root | Count / SHA256 |
| --- | --- |
| `pc3-source.sha256` | 13 files; `83b9f328c6c880124ef515e00c2201d81c0e2a2e99f976bbf698c4ab5cddfd6f` |
| `pc3-source.tar.gz` | Same 13 exact files; `45f419b952ee17b20a98b33113ffa945c08c805c170e6b9e821fa39a7821ef3f` |
| `pc3-f1-correction.sha256` | 3 files; `ebacb99ce22b90fe24a445485bbd75b430a3a0e505b0e6eb1a928d7c41cab474` |
| `pc3-f1-correction.tar.gz` | Same 3 exact files; `3a58e78609a58e1770387f2df86b4edf7e5962002e6ba1de12c5d990520f9365` |
| `pc3-f1-correction.patch` | Relative to the reviewed PC3 archive; `89fbea71c0a5c8647632104d74a36530a417277328768ed0293d6231fcf2364e` |
| `pc3-final-checks.patch` | HEAD-relative bounded 13-file patch used for lint; `a0ffaa76510a800a0eb4b9c9a47af931a9204c1c91f2515f37c810e5d385e2bc` |
| `final-preservation.json` | Exact archive/live preservation audit; `02d48863230eefd572d402d830b8e0572d42b94f7478aefd69dd1bd1ef5c2f2d` |
| `sealed-preservation.json` | Final audit also checks the correction archive; `85db552b2ea39b44f04419f97c55c814188caa7db4078d3d78a0e32642ec6ca3` |
| `test-results.json` | Independently counted log records; `c301983570af952c83907b075c7308839e670366c56b47d82dfd3fe3ef88673c` |

The superseding source inventory is:

```text
backend/internal/cli/accounts_manager.go
backend/internal/cli/accounts_manager_boundary_test.go
backend/internal/cli/accounts_manager_contract_test.go
backend/internal/cli/accounts_manager_dto.go
backend/internal/cli/accounts_manager_login_cancel_test.go
backend/internal/cli/accounts_manager_removal.go
backend/internal/cli/accounts_manager_test.go
backend/internal/cli/root.go
backend/internal/cli/session.go
backend/internal/cli/session_account.go
backend/internal/cli/session_account_boundary_test.go
backend/internal/cli/session_account_test.go
backend/internal/telemetrymeta/cli.go
```

Only `accounts_manager.go` and `accounts_manager_dto.go` changed from the reviewed snapshot. `accounts_manager_login_cancel_test.go` is new. The other ten prior source files remain byte-identical. The correction adds sixteen production lines and removes two; the new test file has 290 lines. There are no new production dependencies or direct service, storage or runtime calls. The service/router imports appear only in tests.

## Output contract and midpoint review

After successful bodyless DELETE, human output is:

```text
operation: login-a
cancellation request acknowledged; sign-in outcome not confirmed
```

JSON output is a separate acknowledgement DTO:

```json
{"operationId":"login-a","cancellationRequestAcknowledged":true}
```

It has no status, provider, mode, expiry or credential fields. The CLI makes one product DELETE and performs no inferred status read, automatic retry, fallback or credential removal. Help text describes the acknowledgement. The separate `login-status` command continues to require fresh inventory, reports the actual operation state, and returns an error with empty stdout when a record is absent or pruned. Known committed cancellation errors retain HTTP 409, safe error code, the exact envelope request ID and runtime exit 1. Successful acknowledgement exits 0 without claiming cancellation or revocation.

Midpoint self-review followed the initial three-repeat green run (`correction-race.log`, 1.156s). The important boundary was acknowledgement versus observation: request success is not a lifecycle observation. Review strengthened the tests to check exact human/JSON observed status, help wording, and visible request-ID rendering. No further production correction was required. Every final verification command below ran after that test-only strengthening; the initial green log is not final-snapshot evidence.

## Failed-first evidence and required controls

`red-corrected-fixture.log`, SHA256 `11e39c3afeec9fb98c4dd59f03ddb02f22909c6054ea08895093c6c0b1c84a73`, records the pre-production-edit command:

```text
go test -mod=readonly -p=1 ./internal/cli -run '^TestManagedAccountsLoginCancel' -count=1 -timeout=2m -v
```

Exit 1: two top-level tests and eight named output cases fail on invented `cancelled` state. The real public-router/service test reaches completed-operation pruning before both repeated cancellation overclaims. Its lifecycle and credential-preservation controls do not fail. The earlier `red.log` is retained as diagnostic evidence: its actual-router case also made an incorrect test assumption that the request ID would be in a response header. The authoritative red run reads the production JSON error envelope instead and reaches every required stage before source correction.

The generic loopback fixture proves output semantics and exact bodyless DELETE shape in human/JSON, including repetition. It does not define lifecycle semantics. The second test uses the actual public router and unchanged account service, with an in-process HTTP transport and a synthetic runner below the service boundary. `testing/synctest` advances the real one-minute service prune timer without changing service code or sleeping in wall-clock time. The test establishes:

1. Unknown operation 204, repeated, without a private-runner cancellation call.
2. Pending cancellation acknowledgement, followed by an observed pending state until the runner emits the expired/cancelled event.
3. A later completed sign-in with a committed credential. Cancellation before pruning returns 409 with the exact request ID and leaves observed completed state intact.
4. After real service pruning, the login is absent and the credential remains. Repeated DELETE returns 204 without reaching the private runner. Status still reports not found; public account inventory still contains the committed credential.
5. Exactly two runner cancellation calls (pending and committed controls), zero credential-removal calls, no private-state output. Help independently distinguishes acknowledgement from observed status.

These tests establish CLI/public-service semantics, not live provider execution or real credential storage behavior.

## Final verification

Go checks ran serially from workspace `backend/`, orchestrated with Fish and this prefix:

```text
env -u TMUX GOMAXPROCS=2 SHELL=/bin/sh TMUX_TMPDIR=/tmp/ao79-tmux.QMXnIq GOTMPDIR=/var/tmp/ao79.uF9PZF TMPDIR=/var/tmp/ao79.uF9PZF node /tmp/pr-5769-rebase.reOkRW/run-isolated.mjs
```

The wrapper strips session/provider credential environment, uses session-owned temporary directories and leaves test fixtures to allocate isolated run state. Pipeline status was the command status, not `tee` status. All listed final commands exited 0.

| Command after prefix | Result | Log under artifact root |
| --- | --- | --- |
| `go test -mod=readonly -p=1 -race ./internal/cli -run '^TestManagedAccountsLoginCancel' -count=3 -timeout=3m -v` | 1.168s; 3 distinct top-level tests and 10 subtests, each repeated 3 times | `final-correction-race.log` |
| `go test -mod=readonly -p=1 -race ./internal/cli -run '^Test(SessionAccount|ManagedAccounts|TelemetryMetaClassifiesRegisteredCommandPaths)' -count=3 -timeout=3m -v` | 2.574s; 22 distinct top-level tests and 129 subtests, each repeated 3 times | `final-focused-race.log` |
| `go test -mod=readonly -p=1 -race ./internal/cli -run '^(TestManagedAccountsLoginCancelHTTPServicePruning|TestSessionAccountHTTP.*)$' -count=3 -timeout=3m -v` | 1.508s; 3 distinct top-level tests and 16 subtests, each repeated 3 times | `actual-router-race.log` |
| `go test -mod=readonly -p=1 -race ./internal/cli ./internal/telemetrymeta -count=1 -timeout=5m` | Complete affected packages: CLI 46.170s; metadata 1.006s | `final-packages-race.log` |
| `go build -mod=readonly -p=1 ./...` | Full backend build; empty successful output | `build.log` |
| `go vet -mod=readonly -p=1 ./...` | Full backend vet; empty successful output | `vet.log` |
| `/tmp/pr-5769-life-f2f3-79.CYTx36/golangci-lint-go1271 run --timeout=3m --concurrency=2 --new-from-patch=/tmp/pr-5769-pc3-f1-79.sJGjN3/pc3-final-checks.patch ./internal/cli ./internal/telemetrymeta` | 0 issues | `lint.log` |
| `go test -mod=readonly -p=1 ./internal/httpd/apispec/... -count=1 -timeout=2m -v` | 15 tests pass, including route/spec parity and embedded YAML drift | `api-contract.log` |

The verbose final race logs contain no skips, failures or race warnings. The pinned lint tool is the preserved v2.13.2-source build for Go 1.27.1, binary SHA256 `e79a6f2732687c8cba200fd61a3124b93aeb7daad64d9c5823e979cbf0071c11`. The test wrapper SHA256 is `97b95a938b6b2a7733ab35ee618f795e01d7a2a84562848f77e2c14bcc35b238`.

No API artifacts changed. Root-CWD Node regenerated the TypeScript string in memory using installed `openapi-typescript` and compared it byte-for-byte with `frontend/src/api/schema.ts`. `generated-drift.log` records equality and SHA256 `86e7b7f807ea28ae480936e6acfe0a00116840050f4be0f78464074b9d266f8d`. OpenAPI YAML SHA256 remains `6410427cb3cc71ea1eeaea7d0e5d38e4b057dff9c9780cf843e196601c8a86f2`. `git diff --check` also exits 0 (`diff-check.log`). No frontend or desktop verification is claimed for this CLI-output-only correction.

## Preservation and independent gate

`verify-freeze.mjs` verifies live hashes, exact archive paths and contents, the three-file correction boundary, the thirteen-file lint patch inventory, retained CLI regression and historical PC3 docs. `final-preservation.json` records zero mismatches; `sealed-preservation.json` repeats these checks and also verifies the separate three-file correction archive:

| Preserved manifest | Count | SHA256 |
| --- | --- | --- |
| `/tmp/pr-5769-pc2-f1-79.E8WKAc/pc2-source.sha256` | 13 | `a3618c483f9411738333c1c0870f921552dd9c58d3a5ba5232f084c6649bddc9` |
| `/tmp/pr-5769-public-controls-79.PbwFW0/lifecycle-guest-frozen.sha256` | 113 | `ffc37c82bafedcf7223478d3d4a6ff6aefa97dea9f04b615b63ef1df66d3e96b` |
| `/tmp/pr-5769-public-controls-79.PbwFW0/protected.sha256` | 91 | `462fd9b445fe2d6764f6ac0d8371aedb742aed22071bbb4afb2e6b943fca6358` |
| `/tmp/pr-5769-macos-guest-design-79.7xIxWq/review-package.sha256` | 5 | `bcb421310148ec0976f2fb406b82de8e93e2d61003cc1c1690852a8adfca2d3a` |

Counts overlap and are not summed. The old PC3 source manifest remains SHA256 `f282f65e6b2dc98131a90ff10e72a6f6b5aa711b0decdbfaf0e97d1a7272e523`; its exact twelve-file archive remains SHA256 `c4ddddb034fdbd01ed5bbc715f3eada389c78de2101cc13959c11282a2885c04`.

Independent request: re-review PC3-F1 on this superseding immutable package. Confirm the acknowledgement cannot be mistaken for observed cancellation or credential revocation, the real service/pruning controls are meaningful, and error/status/help behavior preserves the thin HTTP boundary. Return bounded CLEAR or exact findings. PC3-F1-GATES.md has seven met gates, one independent-review gate pending, and zero abandoned gates. Source edits stop here.

Release gates are unchanged: production control-service wiring, UI and real desktop/provider flows, native Windows verification, and native arm64/x64 guest keeper-death/escaped-descendant retirement proof remain open. This correction does not certify full-backend tests, cross-platform CLI execution, performance or release readiness.
