# Harness Maintenance Gates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Serialize same-harness session launches with reinstall, update, and uninstall workers so executable replacement cannot race a launch.

**Architecture:** `systeminstall.Service` owns a lazily initialized `sync.RWMutex` per `domain.AgentHarness`. Session-manager launch paths use the read side through the existing `HarnessUseGate`; maintenance acquires the write side before listing sessions and transfers the release to the asynchronous worker.

**Tech Stack:** Go, `sync.RWMutex`, service and session-manager tests, Go race detector.

**Spec:** `docs/superpowers/specs/2026-10-06-harness-maintenance-gates-design.md`

## Global Constraints

- Same-harness launch and maintenance cannot overlap.
- Different harnesses do not block each other.
- Reinstall, update, and uninstall hold the write side through worker verification.
- Droid and fx installs preserve their existing launch protection.
- Every pre-worker and worker exit releases the write side exactly once.

## Review Focus

- A launch that owns a read lease before maintenance starts must prevent session enumeration under the write side.
- The write lease must survive the HTTP/service method return and remain held for the complete async worker.
- A persistence, planner, ownership, shutdown, or worker failure must not leak the gate.
- Restore and resume must gate the persisted session's actual harness, including project-default resolution for spawn.
- Two different harnesses must be able to launch or maintain concurrently.

---

### Task 1: Per-Harness Maintenance Gate

**Files:**
- Modify: `backend/internal/service/systeminstall/systeminstall.go`
- Modify: `backend/internal/service/systeminstall/systeminstall_test.go`
- Modify: `backend/internal/service/systeminstall/fx_test.go`

**Interfaces:**
- Produces: `harnessGate(harness domain.AgentHarness) *sync.RWMutex`.
- Preserves: `TryBeginHarnessUse(harness domain.AgentHarness) (release func(), ok bool)`.
- `StartAgentOperation` acquires a write lease for reinstall/update/uninstall and for existing Droid/fx protected installs.

- [ ] **Step 1: Write failing same-harness concurrency tests**

Add deterministic tests that hold a Codex read lease and assert reinstall/update/uninstall fail with `ErrHarnessActive` before `ListAllSessions`; pause a Codex update worker and assert a read lease cannot be acquired until completion; and create an active session before releasing a launch lease so the later maintenance snapshot rejects it.

- [ ] **Step 2: Run focused tests and verify RED**

Run: `cd backend && go test ./internal/service/systeminstall -run 'TestHarness(Launch|Maintenance)'`

Expected: FAIL because Codex currently receives a no-op launch lease.

- [ ] **Step 3: Implement the gate registry**

Replace `droidGate` and `fxGate` with `harnessGatesMu sync.Mutex` and `harnessGates map[domain.AgentHarness]*sync.RWMutex`. Initialize the map in `NewWithDeps` and lazily in `harnessGate` for tests constructing `Service` directly.

- [ ] **Step 4: Acquire and transfer maintenance leases**

In `StartAgentOperation`, check for an active job, acquire the write side before session enumeration, and release it on every synchronous error. After `beginWorker` succeeds, move the release into the worker closure and hold it through `runAgentOperation` and post-operation verification.

- [ ] **Step 5: Add independence and compatibility tests**

Assert terminated and other-harness sessions do not block Codex maintenance, Codex and Claude leases coexist, and Droid/fx install behavior remains protected.

- [ ] **Step 6: Run package tests**

Run: `cd backend && go test ./internal/service/systeminstall`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/service/systeminstall/systeminstall.go backend/internal/service/systeminstall/systeminstall_test.go backend/internal/service/systeminstall/fx_test.go
git commit -m "fix: serialize harness launch and maintenance"
```

### Task 2: Session-Manager Gate Coverage

**Files:**
- Modify: `backend/internal/session_manager/manager_test.go`
- Modify: `backend/internal/session_manager/chat_spawn_async_test.go`
- Modify only if a test exposes a gap: `backend/internal/session_manager/manager.go`

**Interfaces:**
- Consumes: `HarnessUseGate.TryBeginHarnessUse(domain.AgentHarness)`.
- Produces: coverage proving spawn, restore, resume, and async chat spawn hold or reject the correct harness lease.

- [ ] **Step 1: Write restore and resume regression tests**

Configure a rejecting gate and assert Codex restore and resume return `ErrHarnessInstallActive` and pass `domain.HarnessCodex` to the gate. Preserve the project-default spawn assertion and async chat lease-lifetime test.

- [ ] **Step 2: Run focused tests and verify behavior**

Run: `cd backend && go test ./internal/session_manager -run 'Test(Spawn|Restore|Resume|AsyncChatSpawn).*HarnessGate'`

Expected: PASS if existing integration is complete; otherwise FAIL at the uncovered launch path.

- [ ] **Step 3: Implement only uncovered integration**

If Step 2 fails, acquire the read lease after resolving the actual harness and before runtime/controller launch, and defer its release to the established launch completion boundary. Do not add a second gate abstraction.

- [ ] **Step 4: Run session-manager tests**

Run: `cd backend && go test ./internal/session_manager`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/session_manager/manager.go backend/internal/session_manager/manager_test.go backend/internal/session_manager/chat_spawn_async_test.go
git commit -m "test: cover harness maintenance launch gates"
```

### Task 3: Maintenance-Gate Verification

**Files:**
- Verify files changed in Tasks 1-2.

**Interfaces:**
- Consumes: all previous maintenance-gate outputs.
- Produces: race-checked gate behavior ready to combine with update-advisory changes.

- [ ] **Step 1: Run focused race tests**

Run: `cd backend && go test -race ./internal/service/systeminstall ./internal/session_manager`

Expected: PASS.

- [ ] **Step 2: Run full backend tests**

Run: `cd backend && go test ./...`

Expected: PASS.

- [ ] **Step 3: Check formatting and whitespace**

Run: `gofmt -w backend/internal/service/systeminstall/systeminstall.go backend/internal/service/systeminstall/systeminstall_test.go backend/internal/service/systeminstall/fx_test.go backend/internal/session_manager/manager.go backend/internal/session_manager/manager_test.go backend/internal/session_manager/chat_spawn_async_test.go`

Run: `git diff --check`

Expected: no formatting or whitespace errors.

