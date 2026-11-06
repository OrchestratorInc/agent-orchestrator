# M4 产品运行时多 Agent 协同协议 V1.1

> 当前授权（2026-11-06）：用户已明确恢复 M4 实施；M4-01～09 已完成主控实现和 G4 验证。实际进度见 [执行台账](m4-execution-ledger.md)，历史迁移见 [迁移报告](m4-migration-report.md)。本协议仍定义产品运行时能力，不要求开发期间使用 AgentDock 或固定多 Agent 编排。

> 状态：M4 产品运行时协议已按本协议接入当前实现，整体等待统筹终验。基线：`129e13ca5d8df210cdafcc2e061d521109130e27`。
>
> 本协议**只定义 Agent Orchestrator 产品运行时**的多 Agent 协同机制，不规定开发 M4 时必须采用多 Agent 协作，也不要求开发主 Agent 使用 AgentDock、ACP 或宿主 Task Manager。M4 开发期由主 Agent 自主决定独立完成或按需委派；如调用子代理，模型统一使用 Astra，具体见第 13 节。

## 1. 核心原则：复用现有控制面，不造第二套调度器

当前源码已存在以下事实来源，M4-01 必须逐一核对真实调用链：

| 能力 | 现有事实来源 | M4 责任 |
|---|---|---|
| 任务与依赖 | domain.WorkflowTask / WorkflowEdge、workflow_tasks / workflow_edges | 主 Agent 规划 DAG；不新造 DAG |
| 协作消息/任务请求 | service/collaboration、WorkflowMessage、WorkflowTaskRequest | 复用身份派生、消息持久化与 Operator 决策 |
| 动态派发/并发 | M3 service/scheduler、Admission、预算、资源槽、公平轮转 | Scheduler 是唯一派发权威；不由主 Agent 直接 Spawn |
| Attempt / Lease | WorkflowAttempt、Attempt Binding、Scheduler Epoch | 复用 M3 Lease、Generation、UNKNOWN/Fencing |
| Agent Session | session_manager、Execution Gateway、Session Readiness | SessionManager 是进程/Session 唯一事实来源 |
| 质量交付 | M4 Artifact/Gate/Review/Commit/Integration | 只负责交付质量和集成 |
| 协调摘要 | M4 新增只读 Coordination Projection | 汇总状态，不创造第二套可写生命周期 |

**不得重复建设**第二个 Workflow DAG、Scheduler、Attempt、Session Registry、消息总线或审批系统。若源码审计发现某项能力只有模型/文档，没有持久化或真实调用链，必须在 M4-01 记录为 Gap，再设计最小增量。

## 2. 两层主从 Agent 角色

### 2.1 产品运行时角色

- **主 Agent / Workflow Owner：** 将目标拆分为 Workflow Tasks，建立依赖边，定义接口契约、验收标准、Repo/AllowedPaths、预算和优先级；读取全局状态、处理阻塞、裁决冲突、重排任务请求、复核交付。
- **子 Agent / Task Worker：** 只执行已 Claim 的 Task Attempt；只能访问已绑定 Repo、Worktree 和 AllowedPaths；通过受信 Collaboration API 汇报进度、提问、交接或提交 TaskRequest。
- **独立 Reviewer：** 使用不同 Attempt/Session 身份审查冻结 Artifact；提交者不能自审自批。
- **M3 Scheduler / Execution：** 负责 Admission、Claim、Spawn、Session Binding、Lease、恢复。主 Agent 不能直接启动 Provider 进程。

### 2.2 M4 开发方式（不属于产品运行时协议）

M4 开发主 Agent 自主决定工作方式：可以独立编码、测试和 Review，也可以按需委派子代理。没有子代理工具不构成开发阻断，不要求固定并发数、固定调度循环或开发期文件锁系统。凡实际调用子代理，必须显式使用 Astra（当前已知模型标识 `gpt-6-astra`）；无法确认模型时不启动子代理，由主 Agent 自行完成。主 Agent 对代码质量和最终验收负责。

## 3. Task Dispatch Packet：派发必须结构化

每个子任务必须有不可歧义的任务包，至少包含：

1. task_id / parent_workflow_id / expected_workflow_revision；
2. 目标、背景、输入、输出物、验收标准、非目标；
3. 前置 Task IDs、完成条件和失败后的阻塞策略；
4. RepoKey、ProjectID、BaseSHA、TargetRef、AllowedPaths；
5. 独占写路径、只读共享路径、接口/Schema 契约版本；
6. AgentHarness、模型名、协议、思考强度/配置；模型能力探测证据；
7. 预算、优先级、最大尝试数、超时、重试和升级策略；
8. 要运行的 G1 Gate、报告格式、日志/Artifact 引用；
9. 负责人、创建时间、Revision、幂等 ID。

产品运行时的模型与协议从 Workflow Task 配置及 Capability Probe 中解析，必须验证所选 Harness/模型/协议确实可用；不可用时按产品任务策略 BLOCKED，不静默回退。开发期子代理的 Astra 要求仅适用于 M4 开发委派，不得硬编码成产品内所有 Workflow 的模型默认值。

## 4. DAG 与动态调度规则

1. 主 Agent 先创建完整 DAG，不得把一个大任务只写成模糊的并行 TODO。
2. 只有所有前置 Task 已成功完成，后继 Task 才可 Ready；Failed、Blocked、Unknown 不能当作完成。
3. 主 Agent 每轮计算 Ready 集合，但只提交符合依赖、路径、预算和资源条件的任务。
4. **M3 Scheduler 是唯一派发权威**：默认并发 3，全局硬上限 10；遵守 CPU/RAM、Adapter 限额、Workflow 公平轮转和预算准入。
5. 不允许主 Agent 直接 Spawn 来绕过 Claim/Admission；不能先启动 Agent 再补预算。
6. 本节并发规则仅约束产品运行时 Workflow Scheduler，不约束开发 M4 时主 Agent 使用子代理的数量或并行方式。
7. 新任务/依赖图变更走现有 TaskRequest → Operator 决策 → M3 原子物化；子 Agent 不得直接增删 DAG。
8. 接口契约、共享 SQL、迁移、Daemon Wiring、HTTP 路由等高冲突文件必须先由主 Agent 冻结 owner；必要时拆成只读接口草案与单一集成任务。

## 5. 生命周期、状态与心跳

### 5.1 产品状态不重复建模

底层继续使用现有状态：
- Task：pending / ready / claimed / running / verifying / ready_for_integration / awaiting_approval / integrating / retry_wait / blocked / failed / cancelled / succeeded；
- Attempt：claimed / running / settling / succeeded / failed / unknown；
- Session：真实 unknown / provisioning / ready / exited / failed 观察。

只读 Coordination Projection 可显示：
planned / queued / dispatched / running / waiting_input / handoff / verifying / blocked / retry_wait / unknown / completed。
它是派生视图，不是另一张可独立写入、会与 Task/Attempt 冲突的状态表。

### 5.2 心跳与存活判断

- Attempt Lease/Heartbeat 由可信 Daemon Runtime 续期；Agent 自报 Progress 不得续租，也不能延长执行权。
- Progress Heartbeat 只表示近期有进展，不等于进程存活或 Task 成功。
- 无输出/无心跳超过配置阈值后，先查询 Leader/Epoch、Lease、SessionManager、OS 进程、最后事件和 Artifact；不可立即杀进程或重派。
- Spawn、消息或 Git 操作结果为 UNKNOWN 时，用原 SpawnKey/ClientMessageID/OperationID 查询；禁止生成新 ID 盲目重试。
- Session Exited 不等于 Task Succeeded；必须核验 Artifact、Gate、Review 与 Attempt Settlement。

## 6. 持久化路径所有权与文件冲突

Worktree 隔离不代表逻辑文件不会冲突。M4 应将 WorkflowTaskRepository.AllowedPaths 变成调度前可执行的写范围约束：

- 增加持久化 workflow_task_path_reservations（最终表名由 M4-01 冻结），以 Workflow/RepoKey/规范化路径范围为键，绑定 Task/Attempt/Session/Generation/Scheduler Epoch/Lease/Revision。
- Windows 默认大小写不敏感；路径按仓库相对路径规范化。相同路径、父目录/子目录范围视为冲突；空 AllowedPaths 按整个 Repo 写权限处理。拒绝绝对路径、..、符号链接逃逸、驱动器/ADS 语法。
- 同 Repo 重叠写范围不可并行；主 Agent 可以建立 DAG Edge 串行，也可以让 Scheduler 因 Reservation 冲突将后续 Task 保持 Queued。
- 多 Repo 路径 Reservation 必须全有或全无，不能部分锁定；获取应纳入 M3 Admission/Claim 的可信事务或等价 CAS 事务。
- Reservation 绑定 Attempt/Session/Generation/Epoch，不向 Agent/Renderer 暴露锁 Token。由可信 Runtime 随 Lease 续期。
- 只有确认 Session 已停止/被 Fenced，且 Artifact 已冻结、Agent 已无写能力，才能释放 Reservation。UNKNOWN 时保留占位，不能因 TTL 到期自动让第二个 Agent 写入。
- 路径范围变更需要 Owner/Operator 授权，并使旧 Artifact/Gate/Review 失效。
- 若目录范围冲突检测实现成本过高，M4-01 可裁决 V1 先采用静态 AllowedPaths + DAG 串行，但仍必须在持久化调度层强制，不可退化成内存锁。

## 7. Agent 消息、问题升级与交接

复用 M2 Collaboration，不新建私有消息总线：

- 消息类型 progress / question / result / handoff / reference；交付状态 persisted / forwarded / rejected / unknown。
- Sender Task/Session 必须由服务端 Attempt Binding 派生；请求体中的 sender 字段不可信。
- 子 Agent 遇到问题必须发结构化 Question：阻塞与否、所需决策、受影响任务、已尝试方案、继续执行是否安全。
- 只有无依赖的任务可在其他任务等待时继续；依赖该决策的分支应 Blocked/WaitingInput。
- 子 Agent 申请新增 Task、改变 Repo/AllowedPaths、提升预算/并发、改变接口契约，必须走 TaskRequest 和 Operator 决策。
- Handoff 至少包含：摘要、修改文件、Repo/Branch/HEAD、Artifact SHA、接口变更、测试命令/真实退出码、日志引用、剩余问题、风险、未提交状态、下一步建议。
- Agent → Chat 的转发身份必须是 automation，不可伪装 human；不允许消息中携带 API Key、LeaseToken、Operator Token 或 Provider 凭据。
- Delivery UNKNOWN 先查询原 ClientMessageID；不能换 ID 重发同一副作用请求。

## 8. 文件所有权、集成和冲突处理

1. 主 Agent 维护单一 File Ownership Ledger：路径/目录、Repo、读写类型、唯一 Owner Agent、开始时间、释放条件、关联 Task/Attempt。
2. 同一文件/迁移/查询/共享接口同一时刻只有一个写 Owner；其他 Agent 只能读或通过 Question 提出变更。
3. 每个开发子 Agent 使用独立 Worktree/分支或等效隔离目录。共享接口先由主 Agent批准契约，再分派并行消费者。
4. 子 Agent 交付后先 Freeze Snapshot，主 Agent 验收其文件范围与 Diff，确认 Agent 已停止写入后释放路径所有权。
5. 多 Agent 改动集成由主 Agent串行处理；同一逻辑文件有重叠 Diff 时必须人工解决，不自动以最后写入覆盖。
6. 任何文件冲突都暂停相关任务的新派发；主 Agent 记录冲突、裁决归属、更新 DAG/契约 Revision，并通知受影响 Agent。
7. 不允许子 Agent 直接 Merge 到用户目标分支、Push 远端或覆盖其他 Agent 的 Worktree。

## 9. 动态协调循环

主 Agent 每轮按固定顺序运行：

1. **Reconcile：** 读取 Workflow/DAG Revision、Task/Attempt/Session、Leader/Epoch、Lease、路径 Reservation、消息、Gate/Review 和 Git Operation 状态。
2. **Classify：** 归类 blocked / ready / running / waiting_input / handoff / verifying / done / unknown。
3. **Plan：** 计算依赖已完成、模型可用、预算/资源可用、路径无冲突的 Ready 集合。
4. **Dispatch：** 仅向 M3 Scheduler 提交合法 Ready Task；每次最多填充空闲并发槽，不批量盲启。
5. **Observe：** 跟踪 Session Readiness、可信 Lease、Progress、消息、用量、Artifact 与执行超时。
6. **Review：** 收到 Handoff 后 Freeze Artifact → G1/G2/G3 → 独立 Review；主 Agent 亲自作最终验收。
7. **Resolve：** 处理 Question、接口/路径冲突、模型不可用、预算不足和测试失败；修订必须带 Revision 和审计。
8. **Persist：** 更新持久任务板、进度事件、未派发原因、下一动作和证据引用。

不能以“Agent 正在工作”作为无期限状态；每轮都要记录下一次观察/决策条件。

## 10. 主 Agent 崩溃/重启恢复

重启后先进入 reconciling，对账完成前禁止新增派发：

1. 获取当前 Scheduler Leader/Epoch，确保旧 Leader 已 Fence。
2. 读取 DAG Revision、Task/Attempt/Lease、Session Binding/Readiness、SpawnKey、路径 Reservation、消息 Delivery State、Artifact/Gate/Review 与 Git 操作账本。
3. 仍存活且身份绑定一致的 Session 只 Reattach，不重复 Spawn。
4. 已交接且 Artifact/Gate/Review 指纹仍匹配的 Task 不重复执行。
5. 只有证明原执行已终止且无未知副作用，才允许在 MaxAttempts/预算范围内 Retry。
6. Spawn/消息/Commit/Merge 结果 UNKNOWN 时冻结该 Task 和关联 Reservation，先查真实状态或人工处理；不盲目重派。
7. 重试产生新 Attempt/Revision，旧 Generation/Epoch/Lease 回调拒绝，旧 Gate/Review 按指纹失效。
8. 恢复动作幂等并写审计；对账失败保持全局派发暂停，而不是假定空闲。

## 11. 协调看板与状态查询

M4 先交付只读 Coordination Summary API/CLI（路由查重后确定），M5 再做完整 UI。每个 Task 展示：
- DAG 依赖与 Task/Attempt/Session 派生状态；
- AgentHarness、显式模型标识、Capability Probe 结果；
- last heartbeat、Lease 是否有效（不返回 Token）、Session Readiness；
- 当前并发槽、Adapter 限额、预算预留/Usage Quality；
- Repo/AllowedPaths 摘要、路径 Reservation 冲突及持有 Task；
- 最近 Progress/Question/Handoff、Blocked 原因、Retry 次数；
- Artifact/Gate/Review/Commit/Integration Revision 与状态。

复用 CDC/SSE；日志不输出密钥、原始环境变量、绝对敏感路径和租约 Token。协调摘要是只读投影，不允许客户端直接写任务状态。

## 12. M4 协同验收矩阵

| 场景 | 预期 |
|---|---|
| 前置 Task 未成功 | 后继不派发 |
| 双 Scheduler 竞争同一 Ready Task | 仅一个 Claim/Attempt/SpawnKey |
| 并发默认 3、硬上限 10 | 超额排队 |
| 同 Repo AllowedPaths 重叠 | 写 Reservation 冲突，不能并发 |
| 多 Repo Reservation 部分失败 | 全事务回滚，不留半套锁 |
| Agent 无输出但 Session 仍活着 | 观察/告警，不误杀、不重复 Spawn |
| Lease 过期且 Spawn 结果 UNKNOWN | Attempt UNKNOWN，保留 Reservation |
| Agent 伪造 sender / human 身份 | 服务端绑定派生，伪造拒绝 |
| Agent 自行新增任务/改 DAG/提升预算 | 拒绝，等待 Operator TaskRequest 决策 |
| 指定模型不可用或协议不兼容 | BLOCKED，不静默换模型 |
| 主 Agent/Daemon 重启 | Reconcile 后 Reattach，不重复 Spawn |
| 两 Agent 争用接口/文件 | Reservation 或 DAG 串行，冲突升级 Owner |
| Handoff 后文件变化 | Artifact Revision 改变，旧 Gate/Review stale |
| 一支完成、一支失败 | 保留完成证据，只重试失败分支 |
| 旧 Epoch/Generation 回调 | Fencing 拒绝，不得释放新 Owner 的锁 |

## 13. M4 开发期自主执行约定（V2）

本协议第 1～12 节定义 Agent Orchestrator **产品运行时**的多 Agent 协同机制；本节只说明 M4 开发方式，不把产品运行时机制强加给开发过程。

- 主 Agent 自主决定独立完成还是按需委派子代理；不强制使用多 Agent，不强制真实 Spawn，不强制 AgentDock/ACP/Task Manager，也不规定开发子代理数量、并发上限、心跳频率或固定调度循环。
- **凡是调用子代理，必须显式选择 Astra**（当前已知模型标识 `gpt-6-astra`）。如果无法确认模型确实为 Astra，不启动子代理，由主 Agent 自行完成该工作；不得静默切换模型。
- 不要为了心跳、进度打卡、状态轮询或重复同步而高频调用 AgentDock。只有具体动作确实依赖 AgentDock 时才使用；不使用不构成阻断。
- 已有 `m4-execution-ledger.md` 是本次任务迁移的历史记录和状态参考。仅在阶段进度、交付、阻塞、验收发生实质变化时按需更新，不要求每条命令、每次思考或每轮状态查询都写入。
- 主 Agent 可以按风险使用独立 Worktree、文件范围约定和代码 Review；这些是防止实际改动冲突的工程手段，不要求额外建设开发期锁服务或第二套任务状态机。
- 主 Agent 对最终交付负责。子代理（如使用）的报告是参考证据，主 Agent 必须核对实际 Diff、关键测试和真实退出码，不能把自报完成当成 PASS。
