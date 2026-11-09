# Bug 修复报告

## 基本信息

- Bug 名称：provider-switch-admission-drift
- 发现日期：2026-11-09
- 修复日期：2026-11-09
- 修复人：omp（fix-bug-workflow v1.6.4）

## 问题描述

在编排器会话内把 provider 从 codex 切换到 omp 后，替换编排器失败，回退重试建了一个 codex 会话；随后恢复原 omp 会话时界面报错：

```
AO could not relaunch the agent (step: execution admission). Check the daemon log for details.
```

该会话从此**永久不可恢复**：每次 Restore 都走同一条准入路径，必然被拒。

## 根因分析（Step 1）

### 数据证据（`~/.ao/dev/data/ao.db`）

| 行 | harness | controller_generation |
|---|---|---|
| `sessions/agent-orchestrator-1` | omp | `4a10ccc7-79a0-4809-a553-289a09628902` |
| `scheduler_admissions/admission-60ea005e…`（state=active） | codex | `845d6339-e89f-4167-ab33-728333fff090` |

`conversation_activities` 记录 `Switched provider from codex to omp`（08:31:06），同一秒 `change_log` **没有任何 `scheduler_updated` 事件**。

### 数据流追踪

```
POST /api/v1/sessions/{id}/provider-switch
 → chat.Service.switchProvider
 → ControllerReady(started)                        session_controller.go
 → store.CommitProviderSwitch                     conversation_store.go:586
     └ ActivateConversationBranchSession          queries/sessions.sql:213
          UPDATE sessions SET controller_generation = …   ← 只写 sessions
 → scheduler_admissions 保持不变（旧代际 845d…）

之后 POST /api/v1/sessions/{id}/restore
 → Manager.resumeChatController
 → admitSessionRestart → store.AdmitSessionRestart execution_restart_store.go:42
      ad.ControllerGeneration != oldGeneration  →  ErrSchedulerAdmissionConflict
 → relaunchFailed("execution admission") → AGENT_RESUME_FAILED
```

唯一的自动补齐触发器 `scheduler_initial_session_generation`（`migrations/20261105101553243_m3_session_execution_generations.sql:23-29`）带 `WHEN … controller_generation=''` 条件，切 provider 时该列已非空，因此永远不会触发。

同族路径 `CreateAndActivateConversationBranch`、`ActivateConversationBranch` 有相同缺陷。

### red-capable 循环（强制要求，已跑过）

```bash
cd backend && go test ./internal/storage/sqlite/store/ \
  -run TestProviderSwitchKeepsExecutionAdmissionGenerationInSync -count=1
```

修复前输出：

```
--- FAIL: TestProviderSwitchKeepsExecutionAdmissionGenerationInSync (3.14s)
    provider_switch_admission_test.go:41: admission generation after provider switch = "first", want the session's "switched"
FAIL
```

在**真实数据库副本**上跑生产 store 代码（一次性程序，已删除）：

```
before: session.harness=omp session.gen=4a10ccc7-… admission.state=active admission.gen=845d6339-… rev=3
AdmitSessionRestart(restore step) = admit session restart: scheduler admission conflicts with durable state
```

修复后同一副本：

```
AdmitSessionRestart(restore step) = <nil>
after:  admission.state=active admission.gen=verify-generation rev=4
```

判定为 P1：功能异常，有 workaround（新建编排器会话），但原会话上下文与历史永久不可访问。

### 假设与验证

- **H1（命中）**：Provider 切换只发布 session 代际，未同步执行槽；恢复准入因此拒绝。验证方式：上述 store 级与真实数据副本两处 red-capable 输出。
- **H2（已否证）**：scheduler 领导权/租约过期。`scheduler_leader` 显示 owner/epoch 与 admission 一致、`recovered=1`、租约有效。
- **H3（部分成立）**：daemon 启动恢复会把旧任期槽 `AdoptSchedulerAdmission` 置为 `uncertain`，使恢复路径再多一道阻塞；已纳入修复。
- **H4（已否证）**：`capability evidence` 不足。修复前的错误是 `ErrSchedulerAdmissionConflict`，不是 `ErrCapabilityEvidenceInsufficient`。

## 修复方案（Step 2 / Step 3）

1. **写入端同步（根因）**：`adoptSessionAdmissionGeneration`（`store/execution_restart_store.go`）在同一事务里把 active、无 workflow attempt 的执行槽推进到新代际；接入三处发布新代际的写入：
   - `CommitProviderSwitch`
   - `CreateAndActivateConversationBranch`
   - `ActivateConversationBranch`
2. **读取端修复（存量脏数据 + 启动恢复）**：`AdmitSessionRestart` 对**已终止**的普通 Session 允许接管 `active`/`uncertain` 的旧代际槽（新增 `AcceptStaleSessionRestart`，`state IN ('active','uncertain')`）。已终止会话不存在“尚未发布的控制器”，因此旧代际必然过期；floating 的 live Session 仍按原规则拒绝，栅栏未放宽。工作流槽仍必须过 `executionPermitFence`。

方案评审要点：

- 最小改动：不改 schema、不加迁移；仅 1 个新 SQL 查询 + 1 个 helper + 3 处调用 + 1 处准入判定。
- 不破坏语义：`live` 与 `workflow attempt` 的栅栏条件逐字保留；新增例外只在 `rec.IsTerminated && ad.AttemptID == ""` 生效。
- 顺带消除同类漏点（另两条 branch 激活路径），避免同一 bug 在未来换入口复现。

## 测试验证

| 测试 | 覆盖 | 结果 |
|---|---|---|
| `TestProviderSwitchKeepsExecutionAdmissionGenerationInSync` | 切换后槽代际跟随；重启准入成功 | PASS（修复前 FAIL） |
| `TestTerminatedSessionRepairsDriftedAdmissionGeneration` | 存量脏槽被修复 | PASS（去掉修复后 FAIL） |
| `TestLiveSessionStillRefusesDriftedAdmissionGeneration` | live 会话仍拒绝（栅栏未放宽） | PASS |
| `TestTerminatedSessionRestartsFromUncertainAdmission` | 启动恢复后的 uncertain 槽可重启 | PASS（去掉分支后 FAIL） |

测试选择：`test-pyramid-builder/select_tests.py --depth L3` → `execution_permit_test.go`，另加同包回归用例。

回归范围：

```bash
cd backend && go test ./internal/storage/sqlite/... ./internal/service/chat/... ./internal/session_manager/... -count=1
```

全部 PASS。

`go test ./internal/service/session/` 有 2 个**与本次改动无关的既有失败**（已在 `git stash` 到基线后复现）：

- PR checkout 用例：断言期望 `PR contents`，实际多出 `\r\n`（Windows 换行）。
- workspace 文件版本用例：Windows 未授予 `SeCreateSymbolicLinkPrivilege`（创建符号链接权限）。

## 服务重启验证（Step 4）

用改动后源码重建 dev daemon（`npm run build:daemon -- --dev`），在**隔离数据副本**（`AO_DATA_DIR` 指向临时目录）上启动 `ao daemon`：

- 启动无 `ERROR`；`GET /api/v1/sessions/agent-orchestrator-1` 返回正常。
- 真实入口 `POST /api/v1/sessions/agent-orchestrator-1/restore` 不再返回 `AGENT_RESUME_FAILED`，而是继续走到工作区阶段并返回 `BRANCH_CHECKED_OUT_ELSEWHERE`——因为沙箱里同一分支同时被用户真实 worktree 占用。准入步骤已通过，报错点已推进到工作区，这正是本次修复的目标边界。

## 兼容性 / 未修部分

- 本修复不再让同一 branch 同时被两个 worktree 占用时的恢复成功（既有行为，与本次 bug 无关）。
- spawn 阶段 500（`ORCHESTRATOR_SPAWN_FAILED`，`error_type=filesystem`，13.6s）是替换失败的直接原因，但无 daemon 日志可复原具体 syscall。建议把桌面拉起的 daemon stderr 也落到 `~/.ao/daemon.log`（当前只在 keep-alive 模式重定向）。


## 全局冒烟（Step 6）

触发条件：改动 3 个文件（跨查询/存储层）。

- **Phase A 功能清单**：数据来源为 KB（`docs/project-note/03-核心数据业务流/`，8 条业务流全部被 scenario extractor 解析）。
- **Phase B API 冒烟**（隔离数据副本 + 改动后 dev daemon，port 3198）：

  | 端点 | 结果 |
  |---|---|
  | `GET /api/v1/projects` | 200 |
  | `GET /api/v1/sessions` | 200 |
  | `GET /api/v1/orchestrators` | 200 |
  | `GET /api/v1/agents` | 200 |
  | `GET /api/v1/sessions/agent-orchestrator-1` | 200 |
  | `GET /api/v1/sessions/agent-orchestrator-1/conversation` | 200 |

- **Phase C/D**：本次为纯后端存储层修复，无前端可见行为，未跑浏览器链路。
- **Phase E 业务质量门禁**（`business-quality-gate.py`，run id `bug-provider-switch-admission-drift-20261109a`）：
  - `--no-sdd`（无 `docs/vX.Y.Z/契约规范.md`），KB 为 `03-核心数据业务流/`；
  - `scenario_extract`：8/8 业务流覆盖（normal 8 / error 8 / boundary 7），`mock_ceiling_risk=false`；
  - `pytest`：84 passed / 0 failed，仅本轮生成的 oracle 测试；
  - `real_verification`：`exit 0`，`evidence_valid=true`（`business-verification.json` 绑定 run id 与 SDD/KB/目标代码 hash，4 条业务断言全 passed）；
  - **门禁结论：`BLOCK`，原因 `engine_output_invalid`** —— 本仓捆绑的 `ai-quality-check.py` 为 0.7.2（`pyproject.toml` 声明 0.7.4），其 `check` 子命令**不存在 `--strict-gate`**：
    ```
    ai-quality: error: unrecognized arguments: --strict-gate
    ```
  - 因此严格业务门禁**未能取得 PASS**。按流程记录为阻塞项：需把引擎升级到含 `--strict-gate` 的版本后，用**新 run id** 重跑 Phase E，不得复用本次证据。
- **Phase F/G**：冒烟未发现与本次改动相关的新 bug；日志中 `reconcile ... scheduler leadership fence is stale` 属沙箱内多实例争用领导权造成的既有现象，未在单实例真实环境下复现。

## 涉及文件

- `backend/internal/storage/sqlite/queries/execution_permit.sql`（新增 `AcceptStaleSessionRestart`）
- `backend/internal/storage/sqlite/gen/execution_permit.sql.go`（`npm run sqlc` 生成）
- `backend/internal/storage/sqlite/store/execution_restart_store.go`（`adoptSessionAdmissionGeneration` + 准入例外）
- `backend/internal/storage/sqlite/store/conversation_store.go`（3 处代际发布点接入同步）
- `backend/internal/storage/sqlite/store/provider_switch_admission_test.go`（4 个回归用例）

## 相关错题本

- 错题本：./.claude/artifacts/bugs/provider-switch-admission-drift/knowledge/lesson.md

## 相关经验检索记录

- Step 0 命中：`docs/project-note/02-功能结构和策略/设计模式与策略.md`（「代际围栏」与「工作流层准入」两节）、`docs/project-note/06-迭代与变更/已知问题与陷阱.md` P3-14（generation 与控制器对象绑定）
- 检索结果：predev 后端 0 条错题本规则（本仓 `.claude/artifacts/bugs/` 为空）；project-note 命中 5 条
- 采纳结论：
  - 「代际围栏」——已采用：确认 session 与 admission 是两处代际事实，必须同事务推进。
  - 「工作流层准入」——已采用：工作流槽继续走 attempt 栅栏，不允许被普通会话例外绕过。
  - 已知问题 P3-14——不适用：该条讲控制器对象内的 generation 不可变，与本次跨表同步不同层。

## 脱敏审计

- 9.0 预扫描：见下方 9.0 记录
- 审计文件：`.claude/artifacts/bugs/provider-switch-admission-drift/.filter-audit.json`
