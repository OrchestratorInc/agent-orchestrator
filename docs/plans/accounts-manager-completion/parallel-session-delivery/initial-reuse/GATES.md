# Gates: explicit account reuse

OWNS: backend/internal/service/session/service.go, backend/internal/service/session/accounts_manager_reuse.go, backend/internal/service/session/accounts_manager_reuse_test.go, backend/internal/httpd/controllers/sessions_account_reuse_test.go, docs/plans/accounts-manager-completion/parallel-session-delivery/initial-reuse/**

- [x] G1: ordinary and automation replay reject conflicting account intent and preserve matching/native/omitted-choice reuse without launch or mutation.
  CHECK: env GOWORK=off TMPDIR=/var/tmp/pr-5769-next-79.wos4rh/tmp SHELL=/bin/sh /tmp/pr-5769-publish-79.p3jxPQ/node-v24.21.0-linux-x64/bin/node /tmp/pr-5769-rebase.reOkRW/run-isolated.mjs go test -mod=readonly -race -v -count=3 -timeout=120s ./internal/service/session -run '^TestSpawnExistingOrchestratorAccount(Choice|SwitchSnapshot)$'
  EXPECT: --- PASS: TestSpawnExistingOrchestratorAccountChoice
  CWD: backend
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/backend; path=0714becd3eff/40 entries; output=PASS | ok  	github.com/aoagents/agent-orchestrator/backend/internal/service/session	1.033s
- [x] G2: real public routing, session service and SQLite preserve account choice, request IDs and exact replay semantics.
  CHECK: env GOWORK=off TMPDIR=/var/tmp/pr-5769-next-79.wos4rh/tmp SHELL=/bin/sh /tmp/pr-5769-publish-79.p3jxPQ/node-v24.21.0-linux-x64/bin/node /tmp/pr-5769-rebase.reOkRW/run-isolated.mjs go test -mod=readonly -race -v -count=3 -timeout=120s ./internal/httpd/controllers -run '^TestSpawnExistingAccountPublicContract$'
  EXPECT: --- PASS: TestSpawnExistingAccountPublicContract
  CWD: backend
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/backend; path=0714becd3eff/40 entries; output=PASS | ok  	github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers	17.664s
- [x] G3: affected package races, build, vet, lint, generated contracts and prior seals remain green or byte-identical.
  CHECK: /usr/bin/fish --no-config /var/tmp/pr-5769-initial-reuse-79.9BArne/verify.fish
  EXPECT: verification-passed
  CWD: .
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79; path=0714becd3eff/40 entries; output=verification-passed
- [ ] G4: independent review accepts the exact correction and evidence.
  EVIDENCE: pending
