# W1 installed-process proof: review handoff

Read-only review requested. Local only, no publication. Base `ec7efde0652f21690d8e98e2dcfa71d5b132e422` plus the separately sealed W1/W2/W4 and retirement corrections.

Evidence root: `/var/tmp/pr-5769-w1-installed-79.SeryiP`.

## Exact source

One new test file: `backend/internal/adapters/agent/codex/accounts_manager_parallel_e2e_test.go`.

Manifest `final/source.sha256`: SHA256 `e98adabae9abd6a1679e936297be9f3ef10dec182077f910a24f408d26451453`.

Archive `final/source.tar.gz`: SHA256 `526408591b68deef240d149fc2f63246da13f4f8fde9a790c8e5f78dfe7f88f1`.

No production source was changed. Final documentation, evidence and preservation hashes are recorded in `final/FREEZE.md`.

## What the fixture proves

The production runner is built before network isolation. The test, runner and installed client processes run in a private network/PID namespace with synthetic credentials and a scratch home. The production adapter generates account routing flags. Noninteractive JSON mode replaces interactive display, and test-owned activity hooks are suppressed. No host login or external provider is used.

Five scenarios run in every repetition:

1. A and B have concurrent upstream requests authenticated with their distinct selected credentials, despite conflicting synthetic native file and environment credentials.
2. Two sessions choose the same account using separate session capabilities and complete concurrently.
3. The wrong-route oracle observes the actual B credential and refuses an A-only prompt. Account labels alone cannot pass.
4. Disabling A stops its new requests at the runner with zero upstream calls. B's existing stream remains pending until its own response is released, then completes.
5. Restart denies requests until binding reconciliation, preserves exact A/B routing after reconciliation, and rejects an obsolete binding revision. Native profile bytes and file-login status remain intact.

Completion requires parsed stdout `item.completed` and `turn.completed` events, the account-specific result, and successful process exit. Diagnostics are checked for every synthetic provider/route/native credential before output. Processes and renewal workers are joined in cleanup.

Review the identity/overlap oracle, private-network boundary, production versus fixture adjustments, rejected-request counts, child cleanup, restart ordering and evidence limits. There was no substantiated production defect in this leaf; the negative oracle is not presented as failed-first correction evidence.

## Verification

Go 1.27.1, installed client 0.153.4, Node 24.21.0 credential-stripping wrapper, `GOWORK=off`, `GOMAXPROCS=2`, session-owned temporary directory, `SHELL=/bin/sh`. Commands run from `backend/`:

| Command | Log | Result |
| --- | --- | --- |
| `go test -mod=readonly -tags=e2e -json -race -count=3 -timeout=240s ./internal/adapters/agent/codex -run '^TestManagedRouteInstalledParallel$'` | `final-focused.log` | Exit 0, 135.305s, three parent invocations and 15 embedded process scenarios. No skips. |
| `go test -mod=readonly -tags=e2e -json -race -count=1 -timeout=180s ./internal/adapters/agent/codex` | `final-package.log` | Exit 0, 65.587s, 47 top-level passes/125 parent leaves plus five embedded scenarios. |
| `go build -mod=readonly ./...` | `final-build.log` | Exit 0. |
| `go vet -mod=readonly ./...` | `final-vet.log` | Exit 0. |
| `go vet -mod=readonly -tags=e2e ./internal/adapters/agent/codex` | `final-tagged-vet.log` | Exit 0. |
| Pinned v2.13.2 lint, `--build-tags=e2e --path-mode=abs ./internal/adapters/agent/codex` | `final-lint.log` | Exit 0, zero issues. |

Each final command has a separate `.status` file. The complete package skips exactly two existing Windows-only binary-discovery tests on Linux: `TestResolveCodexBinaryFindsLocalAppDataNPMShimOnWindows` and `TestResolveCodexBinaryPrefersNPMOverWindowsAppsExecutable`. Earlier `first-process.log`, `expanded-process.log` and `repeated-process.log` are valid intermediate passes but precede the stricter event oracle.

The Go fixture is race-instrumented; the production runner built inside it and the installed client are not. The runner's independent full race suite is recorded in the W4 seal. This is no claim of race instrumentation of an external executable.

`final-preservation.log` matches 91 protected paths, 33 generated files, the nine-file retirement seal, the 25-file W2 integrated seal, the 32-file W4 integrated seal and five guest-package entries. The W2/W4 integrated manifests also preserve the earlier W1 runner seal. Source/archive member checks and `git diff --check` pass at sealing.

## Remaining work

Independent review remains pending. Actual live accounts, interactive terminal/controller execution, Chat/profile support, exact production containment, native platform verification, desktop behavior and full release suites are not cleared. The three positive retirement groups remain separate unresolved blockers. This test-only leaf has no visual behavior requiring screenshots.
