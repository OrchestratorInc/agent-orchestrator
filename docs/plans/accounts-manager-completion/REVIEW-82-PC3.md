# PC3: CLI-only independent review request

Ready for reviewer82. Review this frozen thin-client slice only. PC2 is independently CLEAR at `/tmp/pr-5769-pc2-rereview-82.txt`; its thirteen source files remain unchanged. No UI, lifecycle, guest runtime or production capability wiring is included. No commit or publication occurred.

## Exact snapshot

Workspace: `/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79`.
Artifact root: `/tmp/pr-5769-pc3-79.SHopnR`.

| Artifact under that root | Count / SHA256 |
| --- | --- |
| `pc3-source.sha256` | 12 files; `f282f65e6b2dc98131a90ff10e72a6f6b5aa711b0decdbfaf0e97d1a7272e523` |
| `pc3-source.tar.gz` | Same 12 files and hashes; `c4ddddb034fdbd01ed5bbc715f3eada389c78de2101cc13959c11282a2885c04` |
| `pc3-final-checks.patch` | HEAD-relative bounded source patch; `947388c41e152399f0754728d83e92fbbdf24a8def14a84f1f7a26f22f6bb74d` |
| `final-preservation.json` | All preservation checks pass; `48e9ebac884b2d77f0796a04ade954a720e5d82d4b387d3c82f8e27af8e6bbf1` |

```text
backend/internal/cli/accounts_manager.go
backend/internal/cli/accounts_manager_boundary_test.go
backend/internal/cli/accounts_manager_contract_test.go
backend/internal/cli/accounts_manager_dto.go
backend/internal/cli/accounts_manager_removal.go
backend/internal/cli/accounts_manager_test.go
backend/internal/cli/root.go
backend/internal/cli/session.go
backend/internal/cli/session_account.go
backend/internal/cli/session_account_boundary_test.go
backend/internal/cli/session_account_test.go
backend/internal/telemetrymeta/cli.go
```

The retained `session_account_test.go` is unchanged from the PC2-only baseline, SHA256 `10f565d845ef1c6d2b6d1adbef740d44c94d430180113edad0f8bcf8d2fcf0ee`. Eleven files are added or changed by PC3. The patch is relative to HEAD, so it includes that inherited untracked test as well. The earlier `pc3-delta.patch` and `pc3-final-delta.patch` are superseded intermediate artifacts, not the final review source.

The parked draft remains intact at `/tmp/pr-5769-public-api-79.xfBKWE/pc3-deferred.patch`, SHA256 `47017f5535c3cae86ea280840d4c0f317da166c7c85be35e4ae291401a623fa0`. It was inspected against corrected PC2 routes and DTOs before reuse. It was not treated as an accepted implementation.

## Behavior and constraints

`ao session account get|switch|status|retry|cancel` uses only the new managed session routes. Switching requires an account ID or explicit `--native`, positive observed `--expected-revision`, `--policy drain|interrupt`, and `--operation-id`. New conversation is opt-in. Output separates the committed account from a pending operation and reports mode, timing, phase and recovery. Retry/cancel send an empty JSON object to the same operation; no default selection, automatic retry or native endpoint fallback exists.

`ao accounts ls|list`, `login`, `login-status`, `login-cancel`, `add-key`, `import`, `refresh`, `disable`, `enable` and `rename` use the existing public account APIs. Sign-in requires explicit provider and mode; reconnect also needs an account and its observed credential generation. Inventory/status exclude sign-in instructions. Only an explicit login command displays its authorization URL and user code. Secret input is bounded piped stdin, not a flag, local credential file or log. The existing loopback HTTP credential-input contract carries that input to the daemon; this slice does not introduce a new secret transport or direct engine access. Input buffers are cleared where mutable, without claiming guaranteed erasure of Go string copies.

`removal-impact` displays every returned binding and the impact revision. `remove` requires `--confirm`, explicit `--expected-revision` (zero is valid) and a stable operation ID. `removal-status|removal-retry|removal-cancel` require account ID and operation ID. Recovery reads and checks ownership before mutation, checks the returned identity again, and preserves requested/recovery/complete distinctions. It never invokes the older direct account DELETE endpoint. Disabling an account is not described as coordinated teardown or deletion.

Public response types are hand-mirrored in the CLI. Unknown private fields are not re-emitted. All commands use the shared loopback HTTP client; production additions import no storage, service, controller or runtime packages. Transport/daemon message text is replaced with a safe diagnostic, while typed HTTP status, error code and bounded request ID survive. Usage errors exit 2; daemon, response-identity and transport failures exit 1. Successful accepted operations exit 0 without claiming runtime readiness. Existing static command telemetry is unchanged and carries no arguments or credential input. Tests asserting zero product requests permit that existing static telemetry request.

## Failed-first and midpoint review

All logs are under the artifact root.

1. `session-red.log`, SHA256 `737be7d5aed3f0e5733e0a165c754cd16c66768202ba89833fea8e7f61d2b2b0`: retained regression run before production CLI edits, exit 1. Switch fails on the missing flag; all three recovery actions fail to reach the required API/error envelope. The six invalid-choice controls pass.
2. `accounts-red.log`, SHA256 `7687cb9500943110bf998d3866f397d9184d91a154194a773a88fb61e39ebb80`: new table before implementation, exit 1, all four top-level tests and 30 named subtests fail on missing account commands. No storage or runtime dependency is used.
3. `midpoint-output-red.log`, SHA256 `57e1fb311f8b9c5c9dd82ef74c44716a5488728d8fe536ae83689b9ae617cdb0`: after initial green, four human-output cases fail because the draft omits mode and policy. Four JSON controls pass. Corrected output now includes those fields and recovery state.
4. `cli-full-race.log`, SHA256 `b9b767271dad6294048493ffa2fb765f27fb6a6ce9f732534588127713050947`: first full package run fails only the existing command-classification test, listing 22 missing static paths. The correction adds those exact paths in seven lines to `telemetrymeta/cli.go`, leaving classification defaults and telemetry payloads unchanged. The final full run passes.

Midpoint source review also corrected the untrusted draft's missing nested pending-session ownership guard and raw-error propagation. Tests cover foreign session/operation responses, pre/post-mutation removal ownership, no target fallback, exact request bodies, explicit zero versus omitted revisions, secret-input size/parse failures and safe diagnostics. No broader refactor or protected-path edit was needed.

## Final verification

All Go commands below ran serially from workspace `backend/`, orchestrated with Fish, after the final source edit. Prefix:

```text
env -u TMUX GOMAXPROCS=2 SHELL=/bin/sh TMUX_TMPDIR=/tmp/ao79-tmux.QMXnIq GOTMPDIR=/var/tmp/ao79.uF9PZF TMPDIR=/var/tmp/ao79.uF9PZF node /tmp/pr-5769-rebase.reOkRW/run-isolated.mjs
```

The existing wrapper removes session/provider credential environment; test fixtures allocate their own run state. The prefix selects the session-owned temporary directories. Pipeline status is the command status, not `tee` status. Logs do not contain live credentials.

| Command after prefix | Result | Log |
| --- | --- | --- |
| `go test -mod=readonly -p=1 -race ./internal/cli -run '^Test(SessionAccount|ManagedAccounts|TelemetryMetaClassifiesRegisteredCommandPaths)' -count=3 -timeout=3m -v` | Exit 0; 2.299s; 19 distinct top-level tests and 119 distinct named subtests each repeat three times; no failures, skips or race warnings | `final-focused-race.log` |
| `go test -mod=readonly -p=1 -race ./internal/cli ./internal/telemetrymeta -count=1 -timeout=5m` | Exit 0; CLI 46.932s, metadata 1.006s | `final-packages-race.log` |
| `go build -mod=readonly -p=1 ./...` | Exit 0; empty successful output | `build.log` |
| `go vet -mod=readonly -p=1 ./...` | Exit 0; empty successful output | `vet.log` |
| `/tmp/pr-5769-life-f2f3-79.CYTx36/golangci-lint-go1271 run --timeout=3m --concurrency=2 --new-from-patch=/tmp/pr-5769-pc3-79.SHopnR/pc3-final-checks.patch ./internal/cli ./internal/telemetrymeta` | Exit 0; 0 issues; pinned v2.13.2 tool built with Go 1.27.1 | `lint.log` |

The actual HTTP-router/controller tests run through the CLI transport against a synthetic control dependency. They verify corrected request DTO decoding, repeated operation intent, stale revisions, foreign session rejection, error request IDs, removal revision zero, recovery state and nil-dependency unavailability. They do not prove native execution or durable coordinator semantics; those remain the existing lifecycle gates.

No API source or generated artifact changed. As an additional read-only drift check, root-CWD Node invoked the installed `openapi-typescript` generator in memory and compared `COMMENT_HEADER + astToString(ast)` with `frontend/src/api/schema.ts`. Exit 0, byte equality true, both SHA256 `86e7b7f807ea28ae480936e6acfe0a00116840050f4be0f78464074b9d266f8d`. Output: `generated-drift.log`. `git diff --check` exited 0. No frontend implementation, typecheck, desktop or provider execution was performed for this CLI-only increment.

## Preservation and review gates

Final preservation audit at `2026-09-28T08:31:41.031Z` verified each listed file hash and decoded the source archive in memory, checking its exact twelve unique regular-file paths and contents against the source manifest.

| Preserved manifest | Count | SHA256 |
| --- | --- | --- |
| `/tmp/pr-5769-public-controls-79.PbwFW0/lifecycle-guest-frozen.sha256` | 113 | `ffc37c82bafedcf7223478d3d4a6ff6aefa97dea9f04b615b63ef1df66d3e96b` |
| `/tmp/pr-5769-public-controls-79.PbwFW0/protected.sha256` | 91 | `462fd9b445fe2d6764f6ac0d8371aedb742aed22071bbb4afb2e6b943fca6358` |
| `/tmp/pr-5769-macos-guest-design-79.7xIxWq/review-package.sha256` | 5 | `bcb421310148ec0976f2fb406b82de8e93e2d61003cc1c1690852a8adfca2d3a` |
| `/tmp/pr-5769-pc2-f1-79.E8WKAc/pc2-source.sha256` | 13 | `a3618c483f9411738333c1c0870f921552dd9c58d3a5ba5232f084c6649bddc9` |

All entries match. These inventories overlap; their counts are not summed. No guest draft was restored, no lifecycle seal was replaced, and protected native switching and Subscriptions remain unchanged.

Independent request: review the source manifest/archive above and the final evidence for explicit intent, exact identity, secret-safe output, usage/runtime exits, request error propagation and thin-client boundaries. Return bounded CLEAR or concrete findings before UI or runtime integration resumes. PC3-MIDPOINT-GATES.md has four met gates, one independent-review gate pending, and no abandoned gates.

Remaining release gaps: production control-service wiring remains unavailable by design; UI controls and real desktop/provider flows remain unimplemented or unverified in this slice; native Windows execution is unavailable; guest keeper-death and escaped-descendant retirement proof remain HOLD; broad full-backend tests, current cross-platform CLI execution, performance and release validation are not certified by these checks. Inventory, login and maintenance command tests use synthetic responses, not live credentials. No release-completion claim is made.
