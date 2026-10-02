# Gates: coordinated deletion

Current status: HOLD. The checked results below describe the original 42-file freeze only. Subsequent independent review found host-parent-death and late-fork defects, so these results do not certify the current source or close any release gate. Current correction evidence is in [DELETION-HOST-CRASH-GATES.md](DELETION-HOST-CRASH-GATES.md) and [REVIEW-82-DELETION-D2.md](REVIEW-82-DELETION-D2.md). Preserve this historical record; do not reuse its full-suite results for D2.

Scope: safely delete a positively identified vault account across every binding and controller, preserving queues, recovery, and unrelated sessions. Linux local execution is the verification platform for this slice; other platform and live-provider release gates remain open.

- [x] D1: Journal tests prove complete impact, consent/revision checks, pre-stop cancellation, stop/revocation acknowledgement ordering, atomic final cleanup, and durable no-fallback choices.
  CHECK: env -u TMUX GOMAXPROCS=2 SHELL=/bin/sh TMUX_TMPDIR=/tmp/ao79-tmux.QMXnIq GOTMPDIR=/var/tmp/ao79.uF9PZF TMPDIR=/var/tmp/ao79.uF9PZF node /tmp/pr-5769-rebase.reOkRW/run-isolated.mjs go test -mod=readonly -p=1 -race -count=3 -timeout=300s ./internal/storage/sqlite ./internal/storage/sqlite/store -run '^Test(AccountsManagerRemoval|Migration0168)' -v
  EXPECT: /^ok\s+.*internal\/storage\/sqlite\/store\s/m
  CWD: backend
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/backend; path=82483691bfc4/41 entries; output=PASS | ok  	github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store	20.889s

- [x] D2: Controller tests prove every affected queue is preserved, exact owners stop before credential removal, unrelated sessions continue, cancellation/retry is bounded, and restart never relaunches a deleted account.
  CHECK: env -u TMUX GOMAXPROCS=2 SHELL=/bin/sh TMUX_TMPDIR=/tmp/ao79-tmux.QMXnIq GOTMPDIR=/var/tmp/ao79.uF9PZF TMPDIR=/var/tmp/ao79.uF9PZF node /tmp/pr-5769-rebase.reOkRW/run-isolated.mjs go test -mod=readonly -p=1 -race -count=3 -timeout=300s ./internal/session_manager ./internal/service/chat ./internal/adapters/chatdriver/persistenthost ./internal/daemon -run '^Test(AccountsManager(Removal|Deletion)|RemovalHost)' -v
  EXPECT: /^ok\s+.*internal\/session_manager\s/m
  CWD: backend
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/backend; path=82483691bfc4/41 entries; output=PASS | ok  	github.com/aoagents/agent-orchestrator/backend/internal/daemon	1.020s

- [x] D3: Cross-boundary tests prove runner revocation, idempotent credential tombstones, concurrent switch/delete refusal, and recovery after every durable deletion phase.
  CHECK: env -u TMUX GOMAXPROCS=2 SHELL=/bin/sh TMUX_TMPDIR=/tmp/ao79-tmux.QMXnIq GOTMPDIR=/var/tmp/ao79.uF9PZF TMPDIR=/var/tmp/ao79.uF9PZF node /tmp/pr-5769-rebase.reOkRW/run-isolated.mjs go test -mod=readonly -p=1 -race -count=3 -timeout=300s ./internal/service/accountsmanager ./internal/accountsmanager -v
  EXPECT: /^ok\s+.*internal\/service\/accountsmanager\s/m
  CWD: backend
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/backend; path=82483691bfc4/41 entries; output=PASS | ok  	github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager	16.246s

- [x] D4: Production-selected direct/fallback processes obey exact-owner teardown and unrelated-process survival under repeated deletion recovery.
  CHECK: env -u TMUX GOMAXPROCS=2 SHELL=/bin/sh TMUX_TMPDIR=/tmp/ao79-tmux.QMXnIq GOTMPDIR=/var/tmp/ao79.uF9PZF TMPDIR=/var/tmp/ao79.uF9PZF node /tmp/pr-5769-rebase.reOkRW/run-isolated.mjs go test -mod=readonly -p=1 -race -tags=e2e -count=3 -timeout=360s ./internal/session_manager -run '^TestAccountsManagerRemovalReal' -v
  EXPECT: /^ok\s+.*internal\/session_manager\s/m
  CWD: backend
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/backend; path=82483691bfc4/41 entries; output=PASS | ok  	github.com/aoagents/agent-orchestrator/backend/internal/session_manager	74.252s

- [x] D5: Midpoint and final adversarial reviews, affected regressions, static checks, all 91 protected paths, and exact source/evidence manifests establish the frozen slice for independent reviewer82.
  EVIDENCE: DELETION-REVIEW.md and REVIEW-82-DELETION.md record the reviewed boundary and final freeze. Post-edit full race suites pass in all 13 affected backend packages; 105 actual-process scenario executions pass in 308.719s, with zero skips or races. Runner build/vet/full race, backend build/vet/tagged vet, deletion-only lint (0 issues), API/SQL drift (32 unchanged files), formatting and diff checks pass. /tmp/pr-5769-deletion-delta-79.sha256 has 42 files; /tmp/pr-5769-deletion-source-79.sha256 has 1802 files matching the pre-verification snapshot; /tmp/pr-5769-deletion-protected-79.sha256 has 91 baseline matches. Exact verification is /tmp/pr-5769-deletion-verification-79.json; final report/log hashes are /tmp/pr-5769-deletion-evidence-79.sha256. Independent CLEAR and broader desktop/platform/provider release gates remain open.

- [x] D6: Real runner restart and request admission prove durable revocation of the deleted account, no fallback, and continued unrelated-account authorization.
  CHECK: env -u TMUX GOWORK=off GOMAXPROCS=2 SHELL=/bin/sh TMUX_TMPDIR=/tmp/ao79-tmux.QMXnIq GOTMPDIR=/var/tmp/ao79.uF9PZF TMPDIR=/var/tmp/ao79.uF9PZF node /tmp/pr-5769-rebase.reOkRW/run-isolated.mjs go test -mod=readonly -p=1 -race -count=3 -timeout=300s ./internal/runner -run '^Test(RunnerDurableMigrationAndIsolation|CredentialRuntime|CredentialVaultConcurrentDeletionAndRefresh|BindingsRevokeOnlyChangedSessionAndCachedSelectorScope|DurableSwitchFenceRevokesWithoutChangingSelectedAccount)' -v
  EXPECT: /^ok\s+.*internal\/runner\s/m
  CWD: accounts-manager/runner
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/accounts-manager/runner; path=82483691bfc4/41 entries; output=PASS | ok  	github.com/aoagents/agent-orchestrator/accounts-manager/runner/internal/runner	3.401s

Run with the repository root as checker --cwd, explicit /usr/bin/fish, and --timeout 600. Commands use the reviewed environment sanitizer, private tmux state, and session-owned scratch. A package success with no matching tests is not evidence. No gate is marked met from inherited results.
