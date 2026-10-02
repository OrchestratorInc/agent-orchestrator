# Gates: first Accounts Manager implementation increment

OWNS: accounts-manager/runner/internal/runner/state.go, accounts-manager/runner/internal/runner/state_test.go, docs/plans/2026-09-27-accounts-manager-implementation.md, docs/plans/accounts-manager-implementation-review/**

Scope: Refresh the plan against the attached PR, implement independent runner configuration protection, review the increment, and record what still blocks account lifecycle and session integration. This ledger does not certify the complete feature.

- [x] G1: The plan review uses the attached PR's actual implementation and identifies which earlier integration concerns still apply.
  EVIDENCE: PR 5769 attached with no takeover and head 6f064d626d55dd5aa1c7996c06a1349a73198460 checked out on a session-local branch. Inspected the actual native credential coordinator, shared admission gate, SDK store registration, raw import writer, API-key configuration path, and device-login store. Plan sections 2, 9, and 11 correct the stale restart concern and module-check commands, and record the uncovered persistence paths. Reported PR checks contained only the review-statistics job.

- [x] G2: Configuration rejection tests prove that each forbidden diagnostics setting is independently rejected while valid private configuration remains accepted.
  CHECK: env GOWORK=off go test ./internal/runner -run '^TestLoadState' -count=1
  EXPECT: ok
  CWD: accounts-manager/runner
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/accounts-manager/runner; path=1eef3047a157/35 entries; output=ok  	github.com/aoagents/agent-orchestrator/accounts-manager/runner/internal/runner	0.025s

- [x] G3: The runner builds, passes vet, and passes its complete test suite with the race detector after the change.
  CHECK: env GOWORK=off go build ./...; and env GOWORK=off go vet ./...; and env GOWORK=off go test -race ./... -count=1
  EXPECT: ok
  CWD: accounts-manager/runner
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/accounts-manager/runner; path=1eef3047a157/35 entries; output=?   	github.com/aoagents/agent-orchestrator/accounts-manager/runner/cmd/ao-accounts-manager	[no test files] | ok  	github.com/aoagents/agent-orchestrator/accounts-manager/runner/internal/runner	1.446s

- [x] G4: Review of the actual diff proves that native account features, Subscriptions, and the vendored engine are unchanged by this increment.
  EVIDENCE: Reviewed the entire tracked diff against the attached PR head. Only runner/state.go and runner/state_test.go changed; other task artifacts are untracked Markdown under docs/plans. No backend, frontend, engine, dependency, or generated-file changes. git diff --check passed. Existing native account/admission and route tests passed unchanged in the two relevant backend packages.

- [x] G5: A post-implementation review checks the test fixtures, real parser keys, rejection order, and remaining lifecycle/routing risks before any next increment.
  EVIDENCE: Confirmed real SDK fields and YAML keys, including pprof.enable and UsageStatisticsEnabled. Each forbidden diagnostic flag produced a failing test before the guard was added and passed afterward. Profiling/discovery mutations are independently scoped. Error messages do not include secrets. Direct Serve source confirms LoadState runs before runtime-record creation and engine startup. Plan section 11 records this slice's startup-only guarantee and the remaining live configuration, encryption, mode support, desktop, and performance checks.

## Reproduction and review notes

- Before the production fix, the request-logging, file-logging, and usage-statistics cases each failed because LoadState returned no error. The original valid-configuration checks passed. This is the positive control for the rejection assertions.
- Tests use local fixtures and the runner's existing fake-provider integration. No real provider accounts, native credentials, or user subscriptions were changed.
- This slice has no visual behavior. Screenshots and packaged desktop checks remain requirements for later integration/UI work, not evidence claimed here.
- G2 and G3 require fish and the installed Go toolchain. Validation uses an isolated fish configuration directory in /tmp and reviewed process execution outside the nested-process restriction; no shell configuration is modified.
