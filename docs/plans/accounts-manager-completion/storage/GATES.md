# Gates: credential persistence

OWNS: accounts-manager/runner/internal/runner/**, backend/internal/accountsmanager/**

Scope: replace every enabled credential persistence path with protected runner-owned storage and prove lifecycle safety before enabling dependent session capabilities.

- [x] S1: Encrypted records survive reopen, reject tampering and key loss, and contain no plaintext credential markers on disk.
  CHECK: env GOWORK=off go test -race -count=1 -v ./internal/runner -run '^TestCredentialVault'
  EXPECT: --- PASS: TestCredentialVault
  CWD: accounts-manager/runner
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/accounts-manager/runner; path=1eef3047a157/35 entries; output=PASS | ok  	github.com/aoagents/agent-orchestrator/accounts-manager/runner/internal/runner	1.156s

- [ ] S2: Live SDK login/import/key/refresh integration uses the encrypted store and rejects late or cancelled writes.
  EVIDENCE: Unmet. Serve uses durable admission, encrypted private import/key/device paths, direct browser callbacks, and draft-data recovery. Integrated runner build/vet/full race passed after disabling error-request logging. Reconnect identity/CAS, refresh coalescing, and complete platform/lifecycle evidence remain open. DEVICE.md and MIGRATION.md record the verified slices; historical foundation checks below do not establish full completion.

- [x] S3: Runner build, vet, and full race tests pass after the integration review.
  CHECK: env GOWORK=off go build ./...; and env GOWORK=off go vet ./...; and env GOWORK=off go test -race -count=1 ./...
  EXPECT: ok
  CWD: accounts-manager/runner
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/accounts-manager/runner; path=1eef3047a157/35 entries; output=?   	github.com/aoagents/agent-orchestrator/accounts-manager/runner/cmd/ao-accounts-manager	[no test files] | ok  	github.com/aoagents/agent-orchestrator/accounts-manager/runner/internal/runner	1.515s

- [x] S4: Daemon account adapter and service tests pass with safe projections and no secret-bearing public response.
  CHECK: go test -race -count=1 ./internal/accountsmanager ./internal/service/accountsmanager ./internal/httpd/controllers -run 'AccountsManager|Management|Credential|OAuth|APIKey|Routing|Service|Supervisor|Removal|TerminalLogin'
  EXPECT: ok
  CWD: backend
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/backend; path=1eef3047a157/35 entries; output=ok  	github.com/aoagents/agent-orchestrator/backend/internal/service/accountsmanager	1.013s | ok  	github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers	1.337s

- [ ] S5: Persistence-path review verifies all enabled writers, protected files, recovery boundaries, and platform permission behavior.
  EVIDENCE: Unmet. REVIEW.md records the complete inspected path inventory and gaps. Windows ACL/runtime durability and live integration have no passing evidence.

- [x] S6: Executable SDK boundary diagnostics reproduce callback/import store bypasses and runtime success despite rejected persistence.
  CHECK: env GOWORK=off go test -race -count=1 -v ./internal/runner -run '^TestPinnedSDKCredentialBoundaries$'
  EXPECT: --- PASS: TestPinnedSDKCredentialBoundaries
  CWD: accounts-manager/runner
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/accounts-manager/runner; path=1eef3047a157/35 entries; output=PASS | ok  	github.com/aoagents/agent-orchestrator/accounts-manager/runner/internal/runner	1.073s

- [x] S7: The new storage source compiles for Windows amd64 and macOS arm64.
  CHECK: env GOWORK=off GOOS=windows GOARCH=amd64 go build ./...; and env GOWORK=off GOOS=darwin GOARCH=arm64 go build ./...; and printf 'Cross-platform runner builds passed\n'
  EXPECT: Cross-platform runner builds passed
  CWD: accounts-manager/runner
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/accounts-manager/runner; path=1eef3047a157/35 entries; output=Cross-platform runner builds passed
