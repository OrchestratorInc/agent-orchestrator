# Gates: pending-removal process recovery

OWNS: accounts-manager/runner/internal/runner/credential_removal_process_test.go, docs/plans/accounts-manager-completion/parallel-session-delivery/removal-process/**

Scope: prove the existing removal fence through abrupt runner death, fresh Serve, retry and subsequent restart without modifying the sealed correction. Checks run with the installed Fish shell, Go 1.27.1 and the existing credential-stripping wrapper.

- [x] G1: each worker kind survives actual process loss as a durable removal fence, then retries exactly once without affecting B.
  CHECK: env GOWORK=off TMPDIR=/var/tmp/pr-5769-next-79.wos4rh/tmp SHELL=/bin/sh GIN_MODE=release /tmp/pr-5769-publish-79.p3jxPQ/node-v24.21.0-linux-x64/bin/node /tmp/pr-5769-rebase.reOkRW/run-isolated.mjs go test -mod=readonly -race -v -count=3 -timeout=120s ./internal/runner -run '^TestCredentialRemovalProcessRecovery$'
  EXPECT: --- PASS: TestCredentialRemovalProcessRecovery
  CWD: accounts-manager/runner
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/accounts-manager/runner; path=0714becd3eff/40 entries; output=PASS | ok  	github.com/aoagents/agent-orchestrator/accounts-manager/runner/internal/runner	4.614s
- [x] G2: the complete runner race suite remains green with the new subprocess proof.
  CHECK: env GOWORK=off TMPDIR=/var/tmp/pr-5769-next-79.wos4rh/tmp SHELL=/bin/sh GIN_MODE=release /tmp/pr-5769-publish-79.p3jxPQ/node-v24.21.0-linux-x64/bin/node /tmp/pr-5769-rebase.reOkRW/run-isolated.mjs go test -mod=readonly -race -v -count=1 -timeout=180s ./...
  EXPECT: /ok\s+.*\/internal\/runner/
  CWD: accounts-manager/runner
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/accounts-manager/runner; path=0714becd3eff/40 entries; output=PASS | ok  	github.com/aoagents/agent-orchestrator/accounts-manager/runner/internal/runner	16.507s
- [x] G3: the correction's complete 47-file integrated snapshot is unchanged.
  CHECK: sha256sum -c /var/tmp/pr-5769-w4-drain-79.S6e2Ns/final/integrated.sha256
  EXPECT: credential_removal_recovery_test.go: OK
  CWD: .
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79; path=0714becd3eff/40 entries; output=docs/plans/accounts-manager-completion/RETIREMENT-CORRECTION-PLAN.md: OK | docs/plans/accounts-manager-completion/RETIREMENT-CORRECTION-REVIEW.md: OK
- [ ] G4: independent review accepts the crash schedule, fresh construction and negative-control evidence, with no native/live-provider overclaim.
  EVIDENCE: pending
