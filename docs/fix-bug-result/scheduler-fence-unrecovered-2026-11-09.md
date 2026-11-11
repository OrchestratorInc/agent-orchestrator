# Bug 修复报告

## 基本信息

- Bug 名称：scheduler-fence-unrecovered
- 发现日期：2026-11-09
- 修复日期：2026-11-09
- 修复人：omp（fix-bug-workflow v1.6.4）
- 严重级别：P0（调度器永久闭锁，所有 Spawn 失败且无 workaround）

## 问题描述

用户报告：创建会话：

```
spawn: create: scheduler leadership fence is stale or unrecovered
```

这是 `domain.ErrSchedulerFence` 的原始文案，经 `prepareSpawnAdmission` → `PrepareSessionAdmission` →
`currentLeader` 抛出，最终由 `sessions.go` 的 `SPAWN_INTERNAL` 兜底返回 500。

## 根因分析（Step 1）

### 数据证据（隔离副本，`~/.ao/dev/data/ao.db`）

```sql
-- scheduler_leader
owner_id=ef0c242f-…  epoch=2  recovered=0  revision 每 5s +1

-- scheduler_admissions
admission-60ea… state=uncertain  session=agent-orchestrator-1
                ad.controller_generation=845d6339-…  (切换前 codex)
sessions:       agent-orchestrator-1  controller_generation=4a10ccc7-… (omp)  is_terminated=1
```

`change_log` 每 5 秒产出一条 `scheduler_updated`（state 恒为 `uncertain`，revision 单调递增），
而 `recovered` 永远停在 0。

### 数据流追踪

```
daemon boot
 → startM3Scheduler → MaintainLeadership          抢到 epoch=2 租约
 → maintain(leader)  （每轮，20s 预算）
    ├ retirePlanSessions          OK
    ├ Binder.RecoverAdmissions    ← 在这里每轮失败
    │   └ ListSchedulerAdmissions
    │       └ AdoptSchedulerAdmission       槽 → uncertain
    │       └ admissionObservation          Session 已终止
    │       └ ReleaseExecutionAdmission     ← ErrSchedulerAdmissionConflict
    │            !rec.IsTerminated || ad.ControllerGeneration != generation ...
    ├ Budget.Reconcile            （永不执行）
    └ CompleteSchedulerRecovery   （永不执行）
 → recovered 永远 0
 → CurrentFence() 每轮 ErrSchedulerFence
 → PrepareSessionAdmission 拒绝所有 Spawn
```

### 根因

`AdmitSessionRestart` 在上一轮修复（`provider-switch-admission-drift`）中已经确立一条不变量：

> 已终止的普通 Session（`is_terminated=1`、`attempt_id IS NULL`）没有在途控制器，
> 因此执行槽里残留的旧代际必然过期，可以接管。

但**释放端** `ReleaseExecutionAdmission` 没有采用同一条不变量：它仍然要求
`ad.controller_generation == session.controller_generation`。同一份数据因此陷入互相矛盾的两道闸门：

- 准入端说「这个槽过期了，可以重启」
- 恢复端说「这个槽代际不符，拒绝释放」

恢复是**整轮原子**的：`RecoverAdmissions` 返回错误 → `maintain` 提前返回 →
`CompleteSchedulerRecovery` 不被调用 → `recovered` 保持 0。于是该条历史脏数据把整个 daemon
永久锁死在「恢复中」，所有会话都无法新建。

### red-capable 循环（已跑过）

单元级（真实 SQLite，1.78s）：

```bash
cd backend && go test ./internal/storage/sqlite/store/ \
  -run TestTerminatedSessionReleasesDriftedSlotDuringRecovery -count=1
```

修复前：

```
--- FAIL: TestTerminatedSessionReleasesDriftedSlotDuringRecovery
    recovery_drift_release_test.go:39: recovery refused to release a terminated
    session's stale slot: release execution admission: scheduler admission
    conflicts with durable state
```

生产路径级（真实库副本 + 真实 `Coordinator.RecoverAdmissions`）：

```
修复前: RecoverAdmissions FAILED: release execution admission: scheduler admission conflicts with durable state
修复后: RecoverAdmissions OK
        CompleteSchedulerRecovery: RECOVERED
```

### 候选假设与验证

- **H1（命中）**：恢复端没有复用准入端的 stale-slot 不变量，导致已终止会话的漂移槽无法释放，
  `RecoverAdmissions` 每轮失败，`recovered` 永不置位。验证：上述两处 red-capable 输出。
- **H2（已否证）**：领导权租约过期/换主。`scheduler_leader` 的 `lease_until` 始终在新，revision 单调递增，
  说明本实例确实持有有效租约，问题不在抢主。
- **H3（已否证）**：`CompleteSchedulerRecovery` 的 CAS 或 revision 参数错误。手动在库副本上直接调用
  `CompleteSchedulerRecovery` 成功，说明该步骤本身可用，只是从未被执行到。
- **H4（已否证）**：预算对账或 plan retirement 阻塞。修复前 `maintain` 在 `RecoverAdmissions` 就返回了，
  后续步骤根本没运行。

### Post-mortem

- **为什么花时间**：错误文案指向「领导权」，容易先怀疑抢主/租约/时钟；实际是**同一条历史脏数据**
  在两条闸门上得到相反裁决。真根因在「释放端与准入端不变量不对称」，不是领导权本身。
- **表象 vs 真根因**：只让 `RecoverAdmissions` 忽略这条错误（吞掉）会让槽永远停留 `uncertain`
  并持续占用容量——症状消失、根因仍在。本次修的是让释放端与准入端采用**同一判定函数**。
- **为什么上一轮没发现**：上一轮只验证了「终止会话能被重新准入」，没有覆盖「终止会话的槽在恢复中被释放」。

## 修复方案（Step 2 / Step 3）

1. **提取唯一判定**：把 `AdmitSessionRestart` 里的 `staleAfterTermination` 表达式提为
   `store.staleDriftedSessionSlot(rec, ad, generation)`，附注释说明它是不变量而非临时例外。
2. **准入端改为调用它**（行为不变，纯重构）。
3. **释放端采用它**：`ReleaseExecutionAdmission` 在代际比较处放行 `stale` 情形。

放行的三个条件与准入端逐字一致：`is_terminated` + `attempt_id IS NULL` +
槽 `state ∈ {active, uncertain}` + 槽代际非空且与 Session 不一致。因此：

- live Session 的漂移槽仍然被拒（`ErrSchedulerAdmissionConflict`，有回归用例覆盖）；
- 工作流槽仍走 `executionPermitFence`（Attempt 栅栏未改动）；
- 预算仍被冻结为 `uncertain` 等待对账（`FreezeExecutionBudgetReservations` 未改动）。

修复没有放宽任何一条既有栅栏，只是让两条路径对同一种过期状态给出一致结论。

## 测试验证

| 测试 | 覆盖 | 结果 |
|---|---|---|
| `TestTerminatedSessionReleasesDriftedSlotDuringRecovery` | 恢复释放已终止会话的漂移槽 | PASS（修复前 FAIL） |
| `TestLiveSessionStillRefusesDriftedSlotRelease` | live 会话仍拒绝释放（栅栏未放宽） | PASS |
| `TestM3ExecutionReleaseKeepsUnreconciledBudget` | 释放槽仍冻结预算 | PASS |
| `TestTerminatedSessionRestartsFromUncertainAdmission` | 恢复后重启准入仍可用 | PASS |

回归范围（均 `-count=1`）：

```bash
cd backend
go test ./internal/storage/sqlite/store/            # ok 118.2s
go test ./internal/service/execution/               # ok 3.5s
go test ./internal/session_manager/                 # ok 594.6s
go test ./internal/daemon/                          # ok 50.2s
go vet ./internal/storage/sqlite/store/             # exit 0
go build ./...                                      # exit 0
```

## 真实环境验证（Step 4 / Step 5）

- **真实库副本 + 生产恢复路径**（隔离目录，非用户 `~/.ao`）：
  修复前 `RecoverAdmissions` 失败、调度器留在 unrecovered；修复后 `RecoverAdmissions OK` +
  `CompleteSchedulerRecovery: RECOVERED`。
- **真实守护进程重启**（隔离 `AO_DATA_DIR`，端口 3198）：启动后
  `GET /api/v1/scheduler/status` 返回 `recovered: true`、`epoch: 3`；
  库内漂移槽从 `uncertain` 变为 `released`。
- **API 冒烟**：`/api/v1/scheduler/status`、`/api/v1/sessions`、`/api/v1/projects`、
  `/api/v1/agents`、`/api/v1/orchestrators` 全部 200。
- **原报错路径**：`POST /api/v1/sessions/agent-orchestrator-1/restore` 不再返回
  `scheduler leadership fence is stale or unrecovered`，而是推进到工作区阶段并返回
  `unsafe workspace path … is outside managed root`——因为隔离副本记录的是原始机器的工作区路径。
  准入闸门已放行，这正是本次修复的边界。

## 全局冒烟（Step 6）

触发条件：改动 2 个生产文件 + 1 个测试文件。

- **Phase A 功能清单**：数据来源 KB（`docs/project-note/03-核心数据业务流/`，8 条业务流）。
- **Phase B API 冒烟**：见上，5 个端点全 200。
- **Phase C/D**：纯后端存储层修复，无前端可见行为，未跑浏览器链路。
- **Phase E 业务质量门禁**（run id `bug-scheduler-fence-unrecovered-20261109c`）：

  | 项 | 结果 |
  |---|---|
  | `--no-sdd` | 本仓无 `docs/vX.Y.Z/契约规范.md` |
  | scenario 覆盖 | 8/8 业务流，`mock_ceiling_risk=false` |
  | pytest（仅本轮生成） | 84 passed / 0 failed |
  | 真实业务验证 | `exit 0`，`evidence_valid=true`，4 条业务断言全 passed |
  | 引擎严格判定 | **BLOCK：`engine_output_invalid`** |

  阻断原因与本轮改动无关：仓内捆绑的 `ai-quality-engine` 为 **0.7.2**
  （`pyproject.toml` 声明 0.7.4），其 `check` 子命令**不存在 `--strict-gate`**：

  ```
  ai-quality: error: unrecognized arguments: --strict-gate
  ```

  因此严格业务门禁**未能取得 PASS**。按流程记录为阻塞项：需把引擎升级到含 `--strict-gate`
  的版本后，用**新 run id** 重跑 Phase E，不得复用本次证据。
- **Phase F/G**：冒烟未发现与本次改动相关的新 bug。

## 涉及文件

- `backend/internal/storage/sqlite/store/execution_restart_store.go`
  （提取 `staleDriftedSessionSlot` 不变量）
- `backend/internal/storage/sqlite/store/execution_permit_store.go`
  （释放端复用同一不变量）
- `backend/internal/storage/sqlite/store/recovery_drift_release_test.go`（新增回归用例 ×2）
- `.claude/artifacts/bugs/scheduler-fence-unrecovered/verify_recovery_drift.py`（Phase E 真实业务验证器）

## 相关错题本

- 错题本：./.claude/artifacts/bugs/scheduler-fence-unrecovered/knowledge/lesson.md

## 相关经验检索记录

- Step 0 命中：`.claude/artifacts/bugs/provider-switch-admission-drift/knowledge/lesson.md`
  （同一份数据、同一族「代际漂移」，判定为**部分相关**——该 lesson 只覆盖准入端，未覆盖恢复释放端）
- 检索结果：predev 后端 1 条同类 lesson；KB `02-功能结构和策略/设计模式与策略.md`「代际围栏」与
  `06-迭代与变更/已知问题与陷阱.md` 相关条目
- 采纳结论：
  - 「代际围栏」——已采用：确认 Session 代际与执行槽代际是两份事实，必须同事务推进。
  - provider-switch-admission-drift lesson——**已采用其不变量，并发现其覆盖缺口**：
    该 lesson 的修复只让准入端接受过期槽，未让恢复释放端接受同一状态，
    于是同一份数据把 daemon 锁死在「恢复中」。本次把判定提为共享函数并补上释放端。

## 脱敏审计

- 9.0 预扫描：报告与错题本各扫一次，均为 `✅ 干净`（high/medium/low 命中数全 0）
  - `report-draft.md` → 无命中
  - `lesson-draft.md` → 无命中
- 审计目录：`.claude/artifacts/bugs/scheduler-fence-unrecovered/.audit`（sensitive-info-filter 默认审计格式，只存 hash 不存原文）
