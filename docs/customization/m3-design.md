# M3 详细设计 V0.1：动态调度、资源治理、预算与自动恢复

> 设计日期：2026-11-05；项目：`E:\code\agent-orchestrator`；目标分支：`feat/multi-agent-desktop`。
>
> **文档状态：DESIGN READY / IMPLEMENTATION GATED。** 本文只定义 M3，不表示 M2 已通过独立验收，也不授权运行真实 Provider、提交、推送或部署。M2 未提交代码必须先冻结并独立核验。M0-05 真实 Provider 预算尚未批准，M3 只准使用 Fake/Scripted 驱动验证。
>
> 依据：`requirements.md` 的 Q1/Q4/Q6/Q8/Q9/Q10、`design.md` `5/`9/`12/`13、`m2-design.md`、`m2-acceptance-round5.md` 和 `m2-remediation-round5.md`。实现任务清单见 `m3-plan.md`。本文拟议的新接口、表名、阈值均**尚未实现**。

## 1. 目标、边界与冻结决策

M3 交付的是一个**单机 Windows、持久化且可解释的 Workflow 调度器**。由它决定「哪个已就绪任务在何时获得执行资格」，而不是由 Agent 文本直接决定派发。任何派发仍由 M2 Execution Coordinator + Session Manager + Chat Service 执行。

| 主题 | M3 确定设计 |
| --- | --- |
| 全局并发 | 默认 3；用户可下调/上调，但**硬上限 10**；包括受本系统管理且实际仍在运行的 Agent Session |
| 单 Adapter 并发 | 各 Harness 可配置，默认保守值 2；不得超过全局余量；未知 Harness 不启动 |
| 资源 | Windows CPU、可用 RAM、已占用 AO Session、每 Harness 冷却；数据无效时保守降载 |
| 公平性 | Workflow 级轮转 + 同 Workflow 内优先级/等待老化；不得让高优先级任务无限占位 |
| 预算 | Global / Workflow / Task 三层；准确/估算/未知分级；软提醒与可选硬准入限制 |
| 租约 | 沿用 M1 Attempt lease 和 M2 Session/Generation 事实；经可靠证据续期；失联/不确定不重放 |
| 重试 | 默认不自动重试有副作用的动作；仅明确可安全重试的短暂错误、受 maxAttempts 约束 |
| 安全 | Scheduler 是受信任的 daemon 服务，不向 Agent 暴露资源提升、预算审批或直接 Claim 权力 |
| 远程执行 | 预留 Worker ID/Executor 接口，不开发远程 Worker、分布式调度或跨机器工作区同步 |

**不在 M3：** M4 的自动代码 Review/Test Gate、自动 Commit、跨仓库集成/Push 审批；M5 的双模式完整工作台；M6 的安装包/升级；M0-05 的真实 Codex/Claude/OMP/DeepSeek 消耗型能力验证。M3 可给这些阶段留持久化引用和只读摘要，但不能提前把它们标作已交付。

## 2. 实际代码基线与复用点

| 当前真实模块 | 已有能力 | M3 复用/变更要求 |
| --- | --- | --- |
| `internal/domain/workflow.go` | Workflow/Task/Attempt 状态与 priority、maxAttempts、lease_until、heartbeat_at | 保留现有状态机；新增调度决策/预算独立领域类型 |
| `internal/service/workflow/service.go` | Create/Activate/Claim/Start/Settle/Retry/Resolve/RecoverExpired | 统一所有 Claim 的 admission，不允许旧 HTTP Claim 绕开全局槽位/预算 |
| `internal/storage/sqlite/queries/workflow.sql` | 依赖已完成才 READY；Claim CAS、活跃 Attempt 唯一索引、租约过期变 UNKNOWN | 新增可控的就绪刷新调用、原子入场事务和续租查询；不重写原有约束 |
| `internal/service/execution/coordinator.go` | 真实 Session 绑定、幂等 SpawnKey、观测/重锚、代际栅栏 | Scheduler 只向其提交已获准的 Attempt；不重复 Spawn 内部逻辑 |
| `internal/service/execution/local_gateway.go` 与 Session Manager | Worktree/Controller/Chat 真实观察和验证 | 恢复与发起动作前调用，不用数据库 CreationSHA 伪装实时 HEAD |
| `internal/daemon/execution_recovery.go` | 定期处理部分 M2 绑定不确定状态 | M3 在其之后恢复；两个 sweep 不得互相并发重复决策 |
| `internal/service/usage`、`internal/pricing` | 既有 Session 用量采集、成本摘要、价格快照/回填 | 只追加 Workflow/Attempt 归因账本；禁止复制采集器、重复计费 |
| `internal/service/usage/capabilities.go` | 当前认证采集覆盖 Codex、Claude Code、Kimi 等 | OMP/DeepSeek 可能缺经认证采集，必须返回 UNKNOWN，绝不当作零 |

**设计关键缺口：** M1 `Claim` 是数据库原子“领取任务”，但没有同时分配全局资源槽与预算预留；M1 `RecoverExpired` 会把过期尝试置 UNKNOWN，却没有可信心跳续期；M2 有绑定恢复但不是自动公平调度。这三处是 M3 的核心。

## 3. 架构与所有权

```text
CDC/Wakeup + 周期扫描 + 启动恢复
                |
                v
     Scheduler Coordinator (daemon 单领导者)
       |         |          |
       v         v          v
 DAG/Eligibility ResourceSampler   BudgetAdmission
       \         |          /
        \        v         /
          Admission TX (SQLite)
          资源槽 + 预算预留 + Claim Attempt + 审计
                        |
                        v
             M2 Execution.Coordinator
                        |
                 Session Manager / Chat
                        |
            Observer + Usage Attribution
                        |
              LeaseRenewal / Reconcile
                        |
             Workflow Settle / Retry
```

建议新增：
- `backend/internal/service/scheduler/`：Eligibility、FairQueue、Dispatch、Pause、Backoff、Recovery，全部依赖 ports。
- `backend/internal/service/budget/`：BudgetPolicy、Reservation、Attribution、Threshold、CostQuality。
- `backend/internal/ports/scheduler.go`：ResourceSampler、SessionInventory、AdmissionStore、UsageReader 等小接口。
- `backend/internal/adapters/resources/`：Windows 采样实现和跨平台“不可信/不可用”回退。
- `backend/internal/daemon/scheduler_wiring.go`：注入 M2 真实 Gateway 与 Store、时钟、采样器；负责生命周期。
- `backend/internal/httpd/controllers/scheduler.go`：M3 控制/只读入口；API 变化必须重新生成 schema。
- `backend/internal/storage/sqlite/{migrations,queries,store}/`：调度原子性与预算账本。

**所有权约束：** Planner/Agent 只能提出建议，Workflow Service 负责 DAG 持久变更，Scheduler 负责 admission，Execution Coordinator 负责绑定，Session Manager 负责启动/恢复，Usage Collector 负责用量证据，Pricing Manager 负责价格。不能在多个模块中各自启动 Provider。

## 4. 配置、资源模型与默认阈值

全局有效执行槽的上限为：

```text
limit = min(userGlobalLimit, 10, resourceCapacity)
availableGlobal = max(0, limit - observedOwnedSessions - reservedButNotStartedSlots
                         - unresolvedUnknownSlots)
adapterAvailable = max(0, adapterLimit[harness] - occupiedAdapterSlots[harness])
newAdmissions = min(availableGlobal, adapterAvailable, eligibleCount)
```

这些计数集合必须互斥：同一已绑定 Attempt/Session **不能同时按 occupied 和 reserved 计两次**。手动启动且由 AO 管理的活跃 Agent Session 也要占全局观测余量；系统外进程只能反映在 CPU/RAM 余量，不能假称能控制全系统 Agent 总数。

**配置初值（可审计、可调，未被用户另行确认的参数先作为 M3 V0.1 默认）：**

| 参数 | 初值及语义 |
| --- | --- |
| globalConcurrency | 3；可选 1–10；上限 10 不可配置为更大 |
| adapterConcurrency | 各已知 Harness 默认 2；允许 1–10，仍受全局限制 |
| schedulerTick | 15s；状态变更触发提前唤醒；无事件时仍可恢复 |
| resourceSampleInterval | 5s，使用最近约 15s 观测，采样过期视为不可观测 |
| CPU soft / hard | 系统持续 >=75% 限制新启动速率；>=85% 不新增 |
| RAM soft / hard | 可用 RAM 低于 25% 时降速；低于 max(15%, 2GiB) 时不新增 |
| 滞回 | CPU <65% 且可用 RAM >25% 持续 3 个有效采样，方可逐步恢复 |
| dispatchBatch | 一轮最多检查 100 个待选任务，最多新增 3 个，避免瞬时突发 |
| resourceUnknown | 保守最多 1 个调度受控活跃槽，不发起并发扩张；如安全硬限制无法证明则 0 |
| workflowWeight | 默认 1，策略可配置 1–5；不以业务用户提供的任意负数参与调度 |

Windows 实现应通过可靠的系统接口获得 CPU、物理可用内存和 AO 已知活跃 Session；不要把 Windows Worktree 当沙箱。采样中断**不得自动终止**已经运行的 Session，只阻止新的 admission 并报告原因。软阈值使用滞回；连续两轮采样应能确定不会发生 3→10→1 的振荡。

## 5. 调度算法与公平性

1. **醒来先对账**：先跑有界恢复，再读 Workflow/Attempt/Binding/Reservation，不直接发起 Provider。
2. **刷新 READY**：使用现有 `RefreshWorkflowReadyTasks` 查询，仅前驱全部 `succeeded`、无 UNKNOWN Attempt、无人工审批/预算阻断才可准备领取；单事务维护 revision。
3. **候选集**：仅 `workflow.state=active`、`task.state=ready`、未超过 maxAttempts、可运行 Harness 已被 Capability Resolver 证实、Repo 可验证、尚未进入 cooldown 的任务。
4. **跨 Workflow 公平**：Weighted Deficit Round Robin。每轮对活跃且可入场的 Workflow 加 quantum=weight，按持久化轮转游标选择；单 Workflow 不连续霸占全部释放的槽。
5. **Workflow 内选择**：`priority` 降序，等待每 5 分钟增加一级 effectivePriority（最多 +5），同分按 ready_since、task_id 排序；权重和老化有上限，且不绕过权限或任务依赖。
6. **能力与资源匹配**：先验证 Harness、模型配置和预算；不可用时标记 reason，不偷偷替换成另一个更强权限的 Adapter。
7. **执行**：经 AdmissionStore 原子申请成功后，才调用 M2 Bind/Spawn；结果未知则冻结并观察，不再次派发相同副作用。

公平性**限定条件**：只在两个 Workflow 都持续有 eligible 任务且资源/预算均足够时保证无永久饥饿；因预算、依赖、认证或审批阻断的任务不享受虚假的等待时限保证。公平指针可持久化，重启不会总从列表第一条开始。

## 6. 原子 Admission、持久化槽位和崩溃一致性

现有 `WorkflowService.Claim` 的事务需扩展为单一 **ReserveAndClaim** 路径，覆盖 Scheduler 自动 Claim 与既有手工/HTTP Claim；不能先 Claim、另起事务预留预算/槽位，也不能仅依赖进程内 mutex。

**同一 SQLite 写事务：**
1. 读取政策版本、Workflow/Task revision、可运行状态及完整依赖。
2. 重新计算当前有效占位数量和三层预算可用额。
3. 为此次 Attempt 预留全局 + Adapter 槽位，并预留 Global/Workflow/Task 预算。
4. 执行旧 `ClaimWorkflowTask` 的状态 CAS、唯一 Attempt 插入、幂等账本写入。
5. 写入 admission decision（reason 与 policy_version、epoch、reservation_id）。
6. 提交成功后才允许 Execution Coordinator 处理 Spawn。

多实例/双 Daemon 情况用 SQLite 持久化 `scheduler_leader`（owner_id、epoch、expires_at）抢领导权；每次 ReserveAndClaim 必须核对当前 epoch；上一领导者即使线程未退出也不能派发。**不**把领导租约失效等同于 Attempt 失效。

建议状态：
- Admission：`reserved → active → released`；无法判断是否启动过则 `uncertain`（仍计占位），禁止自动二次 Spawn。
- Reservation：`held → consumed/released`；失联且未证实未产生费用则保持 held 并需要恢复事实。
- Dispatch：`planned → claimed → spawn_requested → bound/provisioning → running/unknown → settled`；该状态是执行流水账，不覆盖现有 WorkflowAttempt 状态。

**崩溃场景：**
- 事务未提交：没有 Claim/槽位/预算副作用。
- 事务已提交、尚未请求 Spawn：按持久化意图检查，证明未启动后才可重试**相同** SpawnKey。
- Spawn 已请求、结果丢失：查 M2 Binding/Session ID/Worktree/Controller；证据不全标 UNKNOWN，不产生第二个 Session。
- 已启动、进程失联：保留槽位并冻结自动派发，等待恢复或人工 Resolve。
- 已结算但通知丢失：重放同幂等键返回旧结果，不重扣预算。
- Daemon 崩溃：新 epoch 先接管账本/恢复，再开启新 admission；一切旧 epoch 的派发回执必须被拒绝或降级人工处理。

**禁止**把超时后删除 reservation 当作释放物理资源；释放需关联 Session 已终止、正确 generation、账本结算或明确人工裁决等证据。

## 7. 预算、用量与报价

### 7.1 预算模型

三级作用域：`global / workflow / task`。初版硬限制只对同一结算币种 USD 生效；其他币种/无法确认汇率时不得混加。金额使用 `int64 microUSD` 或经审查的定点十进制，**禁止浮点比较硬限额**。预算政策包括：
- `mode` = `observe`（记录）、`soft`（阈值提醒）、`hard_admission`（禁止新 Claim）。
- `limit`、`warningPercent`（建议 80/95）、`currency`、`period`（全局可按月；Workflow/Task 生命周期）、`revision`、`operatorAudit`。
- 未配置预算时不伪造“无限提供商额度”，仅表示“平台本地预算不限制”。

预算可用额按 **confirmedSpend + outstandingReservations** 计算，同一费用事件在 Global/Workflow/Task 仅**归因一次**，在三个作用域中各自投影，不把三份投影加成三倍成本。入场必须同时满足全部适用硬策略。

每个 Attempt 的 reservation 由所选模式的可证明用量上限/执行上界或用户显式的任务预留额产生；不得把平均价格估算冒充 Provider 绝对上限。若供应商缺少可控上界，`hard_admission` 只能保证“不批准超出当前预留预算的新启动”，**无法保证已经在跑的第三方模型不会超支**，界面/报告必须明确该限制。

### 7.2 采集证据分级

| 质量 | 条件 | 处理 |
| --- | --- | --- |
| `exact` | Adapter/Provider 提供可去重、可核实用量和同币种价格快照 | 汇入账本，附 source_event_key + price_version |
| `estimated` | 有 Token 统计或合理区间、但价格/计费规则并非确认事实 | 标为估算且保留算法/置信区间，不报精确账单 |
| `unknown` | 统计/计价不可获得、来源未校验或尚未收到事件 | 不记为 0，展示未知，硬限制下默认拒绝新增用量不可控的 Attempt |

复用 `service/usage`、`pricing` 已有源事件与快照；M3 新增 Workflow/Task/Attempt 归因层，通过 `session_id` 和可靠 binding 关联。每个源事件有稳定唯一键（含 source/generation/sequence）；回填价格使用补偿事件，不覆写已审计数字。当前 Codex/Claude 等可有已认证采集，但 OMP/DeepSeek 未获得证据前按 unknown。自定义供应商 profile 只保存**引用**与费用策略，不能复制原生凭据、修改 `.codex`/`.claude` 或随意发送 `reasoning.effort` 等不兼容参数。

### 7.3 限额动作

- `soft`：80%/95% 警示、降低建议并发，但不擅自停止执行。
- `hard_admission`：可用余额不足、未知用量且无法界定、报价未生效或币种不符，均拒绝**新**入场；已在跑 Attempt 只能按其真实能力安全停止，不能宣称费用马上归零。
- `unknown` 费用可由经过 OS 人工审批的 Operator 显式切到 `observe` 并承受风险，但 Agent 不能替自己升级预算或模式。
- 价格和用量发生校正必须重新计算余量，原事件和修正来源均可追溯。

## 8. 租约续期、异常重试和 Rate Limit

沿用 M1 当前租约默认配置（设计基线 15 分钟），建议 Scheduler 每 60s 观察活跃 Attempt，证实 Session/Controller、generation、owner、Worktree 与绑定合法后才将 `lease_until` 续至 now+15m（可按配置调整）。验证不通过不续；不能仅凭 Worker 发一条心跳就认定 Provider 活着。

续期必须在 Store 条件 UPDATE 中验证 `attempt_id + worker_id + lease_token + expected_lease_until/revision + state IN(claimed,running) + 当前 scheduler epoch`，防止迟到心跳或旧实例延长已过期 lease。已在 M2 MCP 环境中的 token 不应为了续租而轮换导致工具无效；需单独评审 token 生命周期和存储权限。

发生异常时采用以下分类：
- `rate_limited`（有明确 Retry-After）/可确认未发出的短暂网络/资源失败：设置 adapter 冷却与 `next_eligible_at`；指数退避 5s→10s→20s，上限 5min，加入不超过 20% 的确定性 jitter。
- `unsupported/auth_required/budget_hard_blocked`：保持 blocked，等待环境或人工修复，不反复请求。
- `spawn_unknown/controller_lost/lease_expired/可能已发生副作用`：UNKNOWN + reconciliation，绝不直接增加 attempt_no 重跑。
- `failed_confirmed` 且 Task 显式允许自动重试：最多 `min(maxAttempts,3)` 次；默认自动重试关闭（继承 M1 默认 maxAttempts=1），不超过用户/策略批准上限。

重试路径必须经 revision CAS、完整依赖复核、预算与资源的**重新** admission；旧任务使用的预算账本/审计不删除。M3 不应把超时强行标记为 failed，也不负责 M4 质量 Gate 的代码验收。

## 9. 人工权限、TaskRequest 与控制面

Agent 只能发协作消息或创建 TaskRequest，**不能**直接更改 globalConcurrency、预算 hard limit、Worker 容量、Workflow DAG 或同意高风险操作。写策略、暂停/继续、预算模式变更、Materialize TaskRequest 必须通过 M2 已有可信 Operator 单次审批机制，按 HTTP method + 具体路径 + 原始请求 digest 绑定；无授权 fail-closed。

M2 批准 TaskRequest 后仍可能是 `materialization=pending`。M3 增加受控 materializer：
1. 查已批准、未物化的 TaskRequest、审批 revision 与原范围；
2. 根据 Q4 策略检查任务数、扇出、最大深度、RepoKey、allowed_paths、并发/预算上界；
3. 在 Workflow Service 一次事务内 CAS 增加 Task/Edge/TaskRepo 与记录 materialization result；
4. 如果 DAG 循环、任务超出仓库范围、revision 改变，拒绝且保留申请/审批事实；
5. 新任务仍按依赖从 pending→ready，不通过聊天文本直接启动 Agent。
此处只消费**已有**结构化申请，不引入“LLM 随意重写 DAG”的 Planner。

建议新的 HTTP 路径（**拟议，开发前先校验现有路由是否重名**）：
| 方法 | 路径 | 权限/响应 |
| --- | --- | --- |
| GET | `/api/v1/scheduler/status` | 只读：cap、resourceQuality、占位、eligible/blocked、epoch、安全原因 |
| PUT | `/api/v1/scheduler/policy` | Operator：全局/Adapter 限额与阈值，revision CAS + audit |
| GET | `/api/v1/workflows/{workflowId}/schedule` | 只读：任务优先级、排队原因、下一次可运行时间 |
| POST | `/api/v1/workflows/{workflowId}/pause` | Operator：停止新 Claim，已运行尝试继续或人工干预 |
| POST | `/api/v1/workflows/{workflowId}/resume-scheduling` | Operator：重新启用新 Claim，不能绕过恢复冻结 |
| GET | `/api/v1/workflows/{workflowId}/usage` | 只读：总费用、UNKNOWN/ESTIMATE 与来源 |
| PUT | `/api/v1/workflows/{workflowId}/budget` | Operator：预算、硬限制/软提醒、revision CAS |
| POST | `/api/v1/workflows/{workflowId}/task-requests/{requestId}/materialize` | Operator/可信 Worker Policy：CAS、一次性、审计，不自动放大权限 |

读 API 不包含 LeaseToken、Operator 凭据、Provider API key、未脱敏 Prompt 或完整机器路径。所有写接口均加入 OpenAPI/typed schema、Idempotency-Key/If-Match 和 M2 Operator 闭集路由；旧 `claim` 与其它写入口必须接受同样 admission 约束。状态推送沿用现有 `change_log → CDC → SSE`，必要时迁移仅添加小型调度/预算事件类型，不复制会话事件流。

## 10. SQLite 草案与迁移策略

**以下是设计结构，不是可直接运行的 migration。** 实施时为新版本创建 append-only Goose migrations，增量 SQLC；禁止修改 M1/M2 已执行的历史 migration。

| 表/扩展 | 唯一约束 / 字段要点 | 理由 |
| --- | --- | --- |
| `scheduler_leader` | 单行 owner_id、epoch、lease_until、revision | 防止多 daemon 双重调度，旧 epoch 不得派发 |
| `scheduler_policy` | scope=global/adapter/workflow、scope_id、limits_json、revision、updated_at | 参数审计与动态调整 |
| `scheduler_admissions` | admission_id、attempt_id UNIQUE、workflow/task/harness、epoch、state、reserved_at、released_at、revision | 可恢复的占位事实 |
| `scheduler_retry_due` | workflow_id+task_id 唯一、retry_at、reason、policy_version | 持久化退避，不因重启归零 |
| `budget_policies` | scope+scope_id+currency 唯一、mode、limit_micro、period、revision | 三级预算同币种硬门禁 |
| `budget_reservations` | attempt_id+scope 唯一、micro_amount、state、epoch、revision | 三层同事务预留与释放 |
| `workflow_usage_attributions` | source_event_key UNIQUE、workflow/task/attempt/session、quality、amount、currency、price_version、correction_of | 单源一次性归因、修订可追溯 |
| `scheduler_decisions` | decision_id、workflow/task、reason_code、policy_version、observed_at、非秘密摘要 | 解释为什么没调度及安全审计 |

容量和预算约束不能通过“先 SELECT COUNT 再各自 UPDATE”的多个事务实现；必须同一 Store 写事务，以持久化行和 CAS 确保单赢家。所有时间为 UTC；超时/退避使用注入 Clock，虚拟时钟下可复测。金额范围检查溢出与非负。敏感字段不存在普通日志和 CDC 响应。

## 11. 关键不变量与失败处理矩阵

### 11.1 必须保证

1. `effectiveGlobalConcurrency <= 10`，同一 Attempt/Session 恰占一个逻辑槽；同一 Harness 不超过配置上限。
2. 没有通过资源 + 预算 + Capability + DAG admission，不能调用 Spawn。
3. 旧 HTTP/CLI Claim 不得绕过 Scheduler Admission。
4. 预算事件唯一、重播不二次计费；未知≠零，未确认的成本不得释放预留为可用余额。
5. Workflow Task 的 UNKNOWN、RecoveryUncertain 不重派、不假定为失败；旧 epoch 回执不能更新新代。
6. 已运行任务被 Pause 或资源突降时不直接强杀；保持既有 Generation Fence 和人工 Stop 权限。
7. Worktree 真实验证、权限检查来自 M2，不被 Scheduler 的缓存/优先级覆盖。
8. 未经人工审批 Agent 不得提高并发/预算、创建高风险任务或突破 Repo 范围。
9. M3 不产出 M4 的“测试通过”“已合并”“已推送”假结果。
10. 断电/重启后对同一 ClaimKey、SpawnKey、UsageEventKey 重放仍至多一份执行意图/预算入账。

### 11.2 有限状态恢复

| 观察到的情况 | Scheduler 动作 |
| --- | --- |
| 已有预留、无 Attempt | 原子事务不允许形成；发现则告警、冻结并审计 |
| Attempt 已 Claim、未确认是否启动 | 使用既有 SpawnKey 查真实 Session；不发新 Spawn |
| Provider 活着、Lease 接近过期 | 逐项核验后带 CAS 续期 |
| Provider 失联但可能执行过 | 置 UNKNOWN，持有占位与待定费用，等 Reconcile/Operator |
| budget usage 迟到或修改 | 追加修正事件、重算可用额，下一次 admission 使用新数值 |
| 资源过载 | 停止新 Claim、原运行会话保持并可由用户介入 |
| 重启时 Workflow 已 Paused | 不调度新任务；仅做只读/持久化恢复检查 |
| 旧 epoch 的 Driver/Timer 回调 | 拒绝派发/状态前进；只把结果作为待核验证据 |
| 授权 TaskRequest 已审批但 DAG revision 改变 | 拒绝自动物化，请求重新确认；不扩大范围 |

## 12. 可复现实验与验收矩阵

全部验收先用 `Fake/Scripted`、真实 SQLite/SessionManager/临时 Git/HTTP Router，严格 0 真实 Provider。测试不依赖 wall-clock sleep，注入 Fake Clock/ResourceSampler/Pricing/UsageStream；竞态另做 Race 检查。

**必须红绿验证：**
1. 默认 3 槽、配置 10 槽、尝试 11 槽被拒。
2. 两个 Scheduler/Daemon 竞争同一 Task，仅一个 Admission/Attempt/SpawnKey。
3. 工作流 A 连续高优先级且 B 也持续 ready，B 在有限释放槽内被选中（公平性）。
4. DAG 依赖未完成、paused、failed、unknown、审批阻断时不 Claim。
5. CPU 达 85%、RAM 跌破硬线、新旧资源样本过期、资源恢复滞回。
6. 单 Adapter 达限、错误/不支持的模型 profile 不回退为别的供应商。
7. Global/Workflow/Task 任一级硬预算耗尽，立即拒绝新 Claim。
8. 未知价格/用量、USD 与其他币种混用、回填修正、重复源事件，绝不把 UNKNOWN 算零。
9. 预算 reservation 竞争、崩溃在 Claim 前/后/Spawn 前/后、释放与重复结算。
10. 租约续期合法与旧 token/旧 epoch/过期 token/错误 generation 被拒。
11. 有限指数退避、限流 Retry-After、maxAttempts 用尽，不自动重放未知副作用。
12. Operator 提升并发/硬预算要真实绑定批准；Agent/协作工具/伪造 loopback 不可升级。
13. 批准 TaskRequest 在 DAG 合法时仅物化一次；环/越 Repo/Revision 漂移拒绝。
14. 已接入的 M2 F-01/F-02/F-03、单仓/多仓 Git、Steer/Queue、Resume/Reconcile 全部回归。
15. Daemon 重启前后独立预算、Session、Attempt、占位、调度公平指针一致。

**通过标准：** Go 构建/Vet、M3 单元/集成/Race、M2 原有 E2E/Race、SQLite migration up/down/up、API/SQLC 生成无漂移、TypeScript 检查、Windows 隔离环境启动；所有新增失败与旧 HEAD 已知基线区分。必须记录真实命令、退出码、用例数、失败明细，不以 Agent 的文字报告代替测试。

## 13. 准入门槛、M4 交接与开放风险

### M3 编码开始前

- M2 至少通过独立第六轮安全与执行链复验，或由用户书面明确签署风险豁免，记录具体未关闭的阻断；**设计完成不等于可以不受限启动 M3**。
- 在当前 `a6c284a` 的 M1 HEAD + 大量未提交 M2 文件上先做差异清单和备份/检查点；未授权不得 reset/clean/commit/push。
- 全量 Windows 基线若仍红灯，精确列出“历史/环境/新增未归因”，不能把 192 项前端失败整体归为旧问题。
- M0-05 不批准时，无论 M3 已完成多少代码都不得启动真实四 Agent 作消耗型测试。

### M3 退出门槛

1. 默认 3、硬上限 10、每 Adapter 上限实际生效，资源降载与公平性可重复验证。
2. READY → Admission → M2 Bind/Spawn → Observe → Settle/Retry 的全链可审计，单/双 daemon 无重复执行。
3. 三层预算完整归因、unknown 受控、额度阈值和并发策略可解释。
4. 长时任务续租、断线恢复、UNKNOWN 冻结、幂等重放和重启恢复全通过。
5. Operator 权限、Agent 无法自批、M2 回归、Race、Build/Vet/API/SQLC 全通过。
6. `m3-report.md` 和 `m3-acceptance.md` 独立验收完成；M3 设计文档本身不构成验收。

### M4 交接

仅交付已绑定 Attempt/Repo 的可靠工件引用、用量账本、权限/审核事件和任务状态，不自动生成“Review PASS”、Git Commit 或合并决策。M4 在此基础上接入质量 Gate、Review/Test、任务分支 Commit、集成预演与人审 Merge/Push。

**仍需在实施前 ADR 复核：** Windows 资源采样实现与权限、Global Hard Budget 只是准入限制而非绝对 Provider 计费上界、跨 daemon leader 失联时的安全策略、M1 lease token 明文持久化兼容性、M3 TaskRequest 物化的审批与 DAG 扩展限制。ADR 未通过时对应特性保持 fail-closed，不可扩大权限代替验证。
