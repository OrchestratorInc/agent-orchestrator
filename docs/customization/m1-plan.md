# M1 Workflow 核心开发任务计划

> 状态：**任务已拆解，代码实施尚未开始**。
> 日期：2026-11-03；工作区：`E:\code\agent-orchestrator`；分支：`feat/multi-agent-desktop`。
> 设计依据：[总体设计 V0.1](design.md) §2–§5、§10、§13；[M0 执行报告](m0-report.md) §8.4；M1 继承性分流证据：`E:\code\ao-m0-scratch\evidence\m1-inheritance-triage.md`。
> M0 状态：**正式验收仍为 BLOCKED**；M0-05 预算暂未批准，后续执行。此计划不改变 M0 验收状态。

## 1. 目标与授权边界

M1 只交付 Workflow 核心后端能力：SQLite 持久化、领域模型、DAG 校验、任务生命周期与原子领取、基础 CRUD/API、CDC/SSE 状态变更，以及不依赖真实 Agent 的自动化测试。

用户已授权按此计划先行推进**零 Provider**范围。该授权不等于 M0-05 预算批准，也不允许为了测试而启动真实 Agent、发送 prompt 或消耗 Provider Token。

### M1 完成定义

- Workflow、Repository、Task、Dependency、Attempt 的核心事实可持久化、查询和恢复。
- DAG 校验独立于 LLM，能拒绝重复 ID、缺失依赖、自环、跨 Workflow 依赖和环。
- 同一 Task 的并发领取具备数据库事务保证，同一时刻最多一个有效 Attempt/租约。
- 状态迁移有 revision/CAS、幂等键和审计事实；不确定的副作用不自动重放。
- HTTP API 通过现有 Controller / Service / Store 边界提供基础 CRUD；数据库变更通过现有 change_log → CDC → SSE 发布。
- 迁移、领域、DAG、并发领取、幂等和重启恢复均有可重复的测试证据。

## 2. M1-01：数据库迁移与领域模型

**状态：待开始**

**目标**：建立 Workflow 核心实体与 SQLite 持久化结构，不触碰已有 migration 和生成文件。

**范围**
- 先复核现有 SQLite migration、schema、sqlc query/store 和 domain 类型约定。
- 新增独立、向前兼容的 migration；核心表优先覆盖：
  - `workflows`：目标、状态、revision、时间戳。
  - `workflow_repositories`：Workflow 与已注册 Project/Repo、base SHA 的绑定。
  - `workflow_tasks`：Task 类型、描述、状态、优先级、revision。
  - `workflow_edges`：依赖边，唯一约束及 Workflow 归属。
  - `workflow_attempts`：attempt 序号、状态、租约字段、时间和错误码；Session 绑定字段可空，M1 不启动 Session。
- 在 `backend/internal/domain` 新增 Workflow / Task / Edge / Attempt 等领域类型和必要的不变量校验。
- 按仓库流程执行 `npm run migration:create -- <snake_case_name>`、更新 migration 注册清单，并通过 `npm run sqlc` 生成 Store 代码。

**交付物**
- 新增 migration、schema/query、sqlc 生成结果。
- 新增领域类型及构造/校验方法。
- 迁移升级与 Store 读写测试。

**验收标准**
- 全新测试数据库和已有版本测试数据库均可向前迁移。
- 唯一键、外键、revision、attempt 序号等约束符合设计。
- Store 通过 sqlc 生成代码访问数据库；不手改 `backend/internal/storage/sqlite/gen/*`。
- 不修改已发布 migration，不把 Workflow 状态写入既有 Session 状态字段。

**依赖**：无。此项为 M1 首个实施任务。

## 3. M1-02：DAG 校验器

**状态：待开始**

**目标**：实现纯本地、确定性的 DAG 合法性验证，不调用 LLM 或 Agent。

**范围**
- 校验 Task ID 唯一、依赖目标存在、无自环、无重复边、无跨 Workflow 边。
- 检测环并提供可定位的错误信息；输出稳定的拓扑顺序。
- 校验 RepoKey 指向 Workflow 已绑定的仓库。
- 对节点数、最大深度、最大分支/扇出设置硬上限；具体数值在实现时依据现有配置和测试场景确定，不在本计划中臆造。
- 对 Planner/客户端传入的数据一律按不可信输入校验。

**交付物**
- 独立 DAG validator。
- 表驱动单元测试与错误码/字段路径约定。

**验收标准**
- 正常 DAG 可得到稳定拓扑序。
- 重复 ID、缺失依赖、自环、跨 Workflow 依赖、重复边、环和超限图均被拒绝。
- 测试不需要数据库、网络、Provider 或真实 Agent。

**依赖**：M1-01 的领域类型与依赖边结构。

## 4. M1-03：Workflow Service 与 Task 生命周期

**状态：待开始**

**目标**：把领域规则落到事务服务，保证任务创建、状态迁移、领取和 Attempt 结算的一致性。

**范围**
- 在 `backend/internal/service/workflow` 建立 Service 和 Store/Clock 等必要依赖接口，遵循现有 ports/service 分层。
- 创建 Workflow 时，在同一事务中持久化 Workflow、Repo 绑定、Task 和 Edge；提交前完成 DAG 校验。
- 实现 Task 状态迁移与 revision/CAS；READY 由依赖和门禁事实决定，不允许客户端任意伪造。
- 实现原子 Claim：生成 Attempt、attempt_no、lease_token、worker_id、lease_until；并发竞争只允许一个领取成功。
- 为有副作用的操作使用幂等键；重复请求返回既有结果或明确冲突。
- 处理租约过期与 Daemon 重启：无法证明是否执行过副作用时标记 `UNKNOWN` 并等待核对，不自动重复派发。

**交付物**
- Workflow Service、Task lifecycle、Claim/settle 服务方法。
- 事务、并发、幂等和恢复测试。

**验收标准**
- 两个并发领取者竞争同一 Task，只能有一个成功。
- 相同幂等键重放不会产生第二个 Attempt 或重复状态变更。
- 过期租约和不确定状态按 fail-closed 规则处理。
- Service 不直接 spawn Agent、不调用 Chat Driver、不执行 Git 命令。

**依赖**：M1-01、M1-02。

## 5. M1-04：基础 HTTP API 与 CDC/SSE

**状态：待开始**

**目标**：通过现有 Daemon API 暴露 Workflow 核心读写能力，并复用既有 CDC/SSE。

**范围**
- 新增 `backend/internal/httpd/controllers/workflows.go` 及 DTO/路由，遵循现有统一错误封装、Request ID 和 API 版本约定。
- 实现 Workflow 创建、列表、详情和必要的元数据更新；实现 Task 创建、列表/详情等 M1 必需入口。具体方法和路径以现有 Controller 约定及总体设计 §10 为准。
- 写操作使用 Idempotency-Key、revision/If-Match 或等效并发保护。
- 通过现有数据库 Trigger 写入 `change_log`，复用 CDC → SSE；不从 Store 手工并行发事件。
- 修改 API 源文件后执行 `npm run api`，同时提交生成的 OpenAPI 与前端类型。

**交付物**
- Workflow HTTP Controller、DTO、路由和 API spec。
- API/路由契约测试、CDC/SSE 事件验证。

**验收标准**
- API 只调用 Service，不直接访问 SQLite/raw SQL。
- CRUD 返回结构、错误码、Request ID 和 API spec 一致。
- 事务提交后能观察到对应变更事件；事务回滚不产生成功变更事件。
- 不新增前端 UI，不改变既有 Session/Chat API。

**依赖**：M1-01、M1-03。

## 6. M1-05：回归测试、恢复验证与验收归档

**状态：待开始**

**目标**：以可复现证据完成 M1 核心验收，并明确区分本次回归与 M0 已知基线失败。

**范围**
- 覆盖 migration/Store、领域约束、DAG、Service 事务、并发 Claim、幂等、revision 冲突、租约过期、重启恢复和 API/CDC。
- 先跑目标包测试，再跑适用的构建、vet 和 CI 对应检查；按仓库要求验证 sqlc/OpenAPI 生成结果无漂移。
- 复核 M0 已知失败：`internal/domain` 的 2 条 handoff 测试与 `internal/httpd/controllers` 的 5 条既有失败。不得将其误报为 M1 新增；本阶段不修改 `handoff_note.go`。
- 记录命令、退出码、运行环境、测试数量、已知失败和未覆盖范围。

**交付物**
- M1 验收报告与测试证据。
- 已知基线与本次新增失败的差异清单。
- 后续 M2/M3 的明确接口和未决事项。

**验收标准**
- M1 新增测试全部通过；既有失败没有新增或扩大。
- 事务领取、幂等和重启恢复场景有自动化证据。
- 生成代码和 API 契约无漂移。
- 不以 `go test ./...` 在当前 Windows 红基线上的整体失败/通过替代目标包验收；必须单独说明基线限制。

**依赖**：M1-01～M1-04。

## 7. 执行顺序与边界

| 顺序 | 任务 | 前置 | 状态 |
| --- | --- | --- | --- |
| 1 | M1-01 数据库迁移与领域模型 | 无 | **待开始** |
| 2 | M1-02 DAG 校验器 | M1-01 | 待开始 |
| 3 | M1-03 Workflow Service 与 Task 生命周期 | M1-01、M1-02 | 待开始 |
| 4 | M1-04 基础 HTTP API 与 CDC/SSE | M1-01、M1-03 | 待开始 |
| 5 | M1-05 回归测试、恢复验证与验收归档 | M1-01～M1-04 | 待开始 |

### M1 允许范围

- 仅本地后端核心：SQLite migration / sqlc / domain / workflow service / DAG / HTTP API / CDC。
- 使用临时测试数据库、Fake/Stub 和本地 fixture；可进行不触发 Provider 的纯本地构建与测试。
- 允许为服务注册做最小 Daemon wiring，但启动时不得创建真实 Agent Session。

### 明确不包含

- 不调用 Codex、Claude、OMP、DeepSeek，不发送 prompt，不消耗 Provider Token；M0-05 预算暂缓期间保持 0 turn。
- 不做 M2 的 Session 绑定、Agent Capability Resolver、Steer/Queue/Stop/Resume、跨 Agent 消息与协作。
- 不做 M3 的动态并发、资源/预算调度与自动派发。
- 不做 M4 的 Review/Test Gate、集成、审批和远程 Push。
- 不做 M5 的 Electron/React 工作台 UI；不做 M6 安装包/发布。
- 不接入 Remote Worker，不引入 Redis/独立消息队列，不复制现有 Session/Chat 生命周期。
- 不修改 `.codex/`、`.claude/` 配置，不读写真实 `~/.ao`，不改系统配置，不清理用户数据。
- 不推送远程仓库；目标分支合并与远程发布仍需用户批准。

### M0 已知问题的隔离

- `internal/domain` 的 2 条 handoff 测试失败属于既有 M2 路径缺陷；M1 仅新增领域文件，不改 `handoff_note.go`。
- `internal/httpd/controllers` 的 5 条失败属于既有测试/fixture 问题；如 M1 需要改该包，必须保留基线对照并证明未新增失败。
- Codex Windows 账号目录 `account_storage_unsafe` 是已知启动 WARN，M1 不依赖该功能；另列 M2 follow-up，不在本任务范围内。
- 仓库 `CONTRIBUTING.md` 要求向上游贡献的 Feature 先取得 core-team approval；本计划用于用户本地定制分支，若后续准备向上游提交 PR，需先满足该审批要求。

## 8. 完成状态

本文件创建只代表 M1-01～M1-05 的任务拆解完成，**不代表任何 M1 代码已经实现或验收通过**。下一项执行任务为 **M1-01**。
