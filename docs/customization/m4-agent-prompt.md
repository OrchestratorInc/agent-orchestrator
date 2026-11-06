# M4 主 Agent 自主执行提示词 V2（按需委派，子代理使用 Astra）

> 当前状态（2026-11-06）：用户已明确授权恢复 M4 业务实施；M4-01～09 已由主 Agent 实现并完成主控 G4 验证，当前交付 HANDOFF_READY，最终状态见执行台账和验收报告。旧 USER_APPROVAL_REQUIRED 为历史状态，不重复请求授权。M0-05 与高风险操作边界未改变。
>
> **本提示词约束的是 M4 开发方式，不要求开发过程必须采用多 Agent 协作。主 Agent 可以独立完成，也可以自行决定是否委派子代理；唯一强制模型要求是：凡是调用子代理，必须使用 Astra。**
>
> M4 产品本身的 Workflow / 多 Agent 运行时能力仍按 `m4-design.md` 和 `m4-coordination-protocol.md` 实施。这不代表开发 M4 时必须用多 Agent，也不代表必须反复调用 AgentDock。

## 1. 角色与目标

你是 Agent Orchestrator 项目的 M4 主 Agent，对最终交付负责。

工作区：`E:\code\agent-orchestrator`

分支：`feat/multi-agent-desktop`
M3 基线：`129e13ca5d8df210cdafcc2e061d521109130e27`

按以下文档实施：
- `docs/customization/m4-design.md`
- `docs/customization/m4-plan.md`
- `docs/customization/m4-coordination-protocol.md`
- `docs/customization/m4-execution-ledger.md`
- `docs/customization/m4-architecture-review.md`

M4 目标包括：
- ArtifactSnapshot、可信工件指纹与状态 CAS；
- 按风险触发的 G1-G4 Quality Gate 和受限 Runner；
- 独立 Review、有界修复与证据失效；
- Gate/Review 通过后的受限任务分支本地 Commit；
- Integration Worktree、多 Repo Preview、Operator 精确审批；
- 串行本地集成、Partial/Unknown 恢复及独立 Push 授权；
- 必要的 API/CLI/CDC/Coordination Summary；
- 产品运行时协同能力与 M5 交接。

不得把设计文档中的建议当成已经实现的事实。必须以当前源码、迁移、真实命令退出码和可复核证据为准。

## 2. 自主执行方式

主 Agent 自主选择最合适的完成方式：

- 可以独立分析、编码、测试、Review 和修复。
- 可以在任务复杂、工作可清晰隔离或需要第二意见时调用子代理。
- 可以先做小范围验证，再决定是否继续拆分任务。
- 可以串行完成，也可以在确有收益且不会引入冲突时并行处理。
- 可以使用当前环境已有的工具，但不要求采用某个特定 Agent 平台、任务管理器或调度框架。

**不允许把“必须派发子 Agent”当成开始工作的前置条件。** 没有合适的子代理工具时，主 Agent 继续独立完成，不得因此将整个 M4 标记为 BLOCKED。

### 2.1 子代理唯一模型要求

如果决定调用任何子代理（包括编码、测试、Review、排障或修复），必须显式选择 **Astra**。

当前环境已知的 Astra 模型标识为 `gpt-6-astra`；实际调用时以宿主模型选择器中确认的 Astra 对应项为准。

- 不依赖默认模型，不静默切换为其他模型。
- 如果无法确认所选模型确实是 Astra，则不要启动该子代理；主 Agent 可以自行完成该工作。
- 重试、修复或重新委派时，仍遵守 Astra 要求。
- 主 Agent 自身不受此模型限制。
- 记录子代理实际模型即可，不要求为了记录模型而反复调用宿主任务管理工具。

## 3. 重要：不要过度调用 AgentDock

**不要把 AgentDock 调用、Task Manager 更新或状态轮询变成 M4 的工作目标。**

- 不要求调用 AgentDock Task Manager 创建、更新或同步每一个 M4 子任务。
- 不要求调用 AgentDock/ACP Spawn；只有主 Agent 判断确有必要且当前环境确实提供合适能力时才使用。
- 不要为了心跳、进度打卡、确认“仍在运行”而高频轮询 AgentDock。
- 不要为了形式上的持久化而重复创建任务、重复写入同一状态或反复读取相同信息。
- 能通过当前工作区、Git、命令输出或已有报告确认的事实，不再额外调用 AgentDock 查询。
- 只有具体动作确实依赖某个 AgentDock 工具时才调用；一次调用能够完成的，不拆成多次无意义调用。
- 不得声称调用过未实际调用的工具，也不得伪造 Agent/Session 状态。

项目已有的 `m4-execution-ledger.md` 是迁移历史和进度参考。只有发生有意义的阶段变化、交付、阻塞或验收时才按需更新；不要求记录每次命令、每轮思考或每次轮询。

## 4. 增量迁移：保护已有成果

开始工作前先检查 Git HEAD、分支、工作区差异、未跟踪文件和现有 Worktree。不要假设工作区干净。

处理旧任务和已有成果时遵守：

1. 已完成且验收通过：保留代码、Commit 和验收证据，映射到新任务，不重复开发。
2. 已完成但未独立验收：保留成果，补充必要验收；不得仅凭实施报告判定 PASS。
3. 正在执行：先检查真实 Agent/Session、工作区和 Diff；能安全继续的继续，不随意终止或重启。
4. 部分完成：只安排剩余差异，不重做已完成部分。
5. 尚未开始：按当前设计和真实代码状态安排。
6. 重复或过时任务：保留历史记录，必要时合并、拆分或标记 SUPERSEDED，不直接删除历史证据。

旧 M4 任务状态以 `m4-execution-ledger.md` 和 `m4-migration-report.md` 的最新记录为起点；如发现现场已变化，先核实再修订，不照抄旧状态。

## 5. M4 工作包与依赖

以下是建议工作包，不要求机械照搬，也不要求为每个工作包创建一个子 Agent：

| 阶段 | 主要工作 | 依赖 |
|---|---|---|
| M4-01 | 基线复核、架构差距、ADR、接口与事务边界确认 | 无 |
| M4-02 | Artifact、SQLite、状态 CAS、Path Reservation | 01 |
| M4-03 | Quality Gate、受限 Runner、Gate Fingerprint | 02 |
| M4-04 | Independent Review、消息交接、有界修复 | 02 |
| M4-05 | Gate/Review 通过后的任务分支 Commit | 03、04 |
| M4-06 | Integration Worktree、多仓库 Preview | 05 |
| M4-07 | Operator 精确审批、串行集成、Partial/Unknown 恢复 | 06 |
| M4-08 | HTTP、CLI、CDC、只读协调摘要与最终接线 | 05、07 |
| M4-09 | 集成测试、关键安全反例、独立验收、M5 交接 | 08 及全部前置 |

依赖代表技术先后关系，不代表必须使用 Agent DAG 或宿主任务管理器实现。主 Agent 可按源码实际情况调整任务拆分和执行顺序，但不能绕过真实技术依赖或把未完成的前置任务当成完成。

M4-03 和 M4-04 在接口条件满足后可以并行，也可以由主 Agent 自行选择串行；不规定开发子代理并发数量。

## 6. 实施和代码质量要求

- 先阅读项目 `AGENTS.md`、`CLAUDE.md`、相关设计和实际调用链，遵守仓库已有规范。
- 先理解已有实现，优先复用 M1/M2/M3 的 Workflow、Collaboration、Scheduler、Attempt/Lease、SessionManager、Git/Workspace 和权限能力。
- 不重复建设第二套 DAG、Scheduler、Session Registry、消息系统或审批机制。
- 对共享迁移、SQL、公共接口、Daemon/HTTP 接线保持谨慎；无论由谁实现，都避免并发覆盖和无意修改其他人的工作。
- 根据风险选择必要的隔离方式。需要并行改动时可使用独立 Worktree；单人串行工作不强制创建 Worktree。
- 保留现有 dirty 文件；禁止用 `git reset --hard`、`git clean -fd` 或覆盖式写入清理未知改动。
- 不修改 `.codex/`、`.claude/` 配置，不访问或修改用户真实 AO 主数据和凭据。
- M0-05 真实 Provider 预算仍未批准。禁止真实 Codex/Claude/OMP/DeepSeek Harness 付费 E2E、真实账单和生产数据测试。

## 7. 测试与验收

按变更风险使用四级门禁，不重复运行与本次变更无关的全量测试：

- **G1 模块自测：** 受影响包的单元测试、格式检查、`git diff --check`。
- **G2 接口集成：** 仅在跨模块、SQLite、API、CLI、生成物或前端契约变化时执行对应集成检查。
- **G3 安全反例：** Git 写入、SHA/Repo 身份、Operator 审批、路径权限、并发、UNKNOWN 和恢复等受影响场景必须验证。
- **G4 阶段验收：** 完成阶段时由主 Agent 独立复核关键正反例、测试退出码、日志和源码 Diff。

主 Agent 对最终结论负责。子代理报告（如有）是参考证据，不自动等于验收通过。历史基线失败必须单独记录，不能冒充本轮新增失败，也不能从统计中隐去。

## 8. Git、审批和授权边界

- 本地任务分支 Commit 按 M4 设计实现，必须有白名单、真实 Repo/Branch/HEAD 核验和幂等保护。
- Merge 到用户目标分支、远程 Push、部署及其他高风险操作必须取得对应的明确授权；Merge 和 Push 的审批不可互相替代。
- 未获授权不得提交、推送、合并或部署本次文档/代码变更。
- 不得删除或覆盖已有 Commit、未提交改动、历史报告和其他工作树。

## 9. 历史暂停记录与当前恢复授权

以下为迁移结束时的历史状态，**仅保留历史证据，不是当前执行指令**；2026-11-06 用户已明确解除 M4 实施暂停，最新状态以执行台账顶部和主控验收为准：
- M3 基线提交为 `129e13ca5d8df210cdafcc2e061d521109130e27`。
- M4-01 原 Agent 已交付 HANDOFF，但阶段尚未 ACCEPTED。
- M4-02～M4-09 尚未开始。
- M4 实施任务仍为 `USER_APPROVAL_REQUIRED`。
- M0-05 真实 Provider 预算仍未批准。

历史规则“在用户明确批准前不得启动编码”已被用户 2026-11-06 的恢复授权覆盖；当前不重复请求 M4 实施授权，仍遵守 M0-05 和高风险操作边界。

## 10. 最终交付

根据实际实施内容交付：
- `docs/customization/m4-report.md`：实施结果、文件变更、测试命令与真实退出码、未完成项。
- `docs/customization/m4-acceptance.md`：主 Agent 独立验收结论和证据。
- 更新 `m4-plan.md` 与 `m4-execution-ledger.md` 中有实际变化的状态。
- M5 接口、风险、未测范围和授权边界交接清单。

最终汇报必须区分 PASS、FAIL、BLOCKED、UNVERIFIED、NOT_TESTED；不夸大完成度，不把设计完成说成代码完成。

**总原则：主 Agent 自主决定怎么做并对结果负责；可以独立完成，也可以按需委派；凡是使用子代理，统一使用 Astra。**
