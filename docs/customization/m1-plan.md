# M1 Workflow 核心开发任务计划

> 状态：**M1-01、M1-02、M1-03、M1-04、M1-05 全部完成，M1-05 验证通过（2026-11-03）**；全程仍然**零 Provider**——未启动真实 Agent、未发送 prompt、未消耗 Token；**M0 正式验收仍为 BLOCKED**，M0-05 预算仍未批准。
> 日期：2026-11-03；工作区：`E:\code\agent-orchestrator`；分支：`feat/multi-agent-desktop`；基线提交 `623871d`。
> 设计依据：[总体设计 V0.1](design.md) §2–§5、§10、§13；[M0 执行报告](m0-report.md) §8.4；M1 继承性分流证据：`E:\code\ao-m0-scratch\evidence\m1-inheritance-triage.md`。
> 验收证据：[M1 验收报告](m1-report.md)（命令、退出码、测试数量、基线隔离证明、Review 结论）；交接历史存档：[m1-handoff.md](m1-handoff.md)。
> M0 状态：**正式验收仍为 BLOCKED**；M0-05 预算暂未批准，后续执行。此计划不改变 M0 验收状态，M1 完成也不改变。

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

**状态：已完成（2026-11-03）**

**目标**：建立 Workflow 核心实体与 SQLite 持久化结构，不触碰已有 migration 和生成文件。

**范围**
- 先复核现有 SQLite migration、schema、sqlc query/store 和 domain 类型约定。
- 新增独立、向前兼容的 migration；核心表优先覆盖：
  - `workflows`：目标、状态、revision、时间戳。
  - `workflow_repositories`：Workflow 与已注册 Project/Repo、base SHA 的绑定。
  - `workflow_tasks`：Task 类型、描述、状态、优先级、revision。
  - `workflow_edges`：依赖边，唯一约束及 Workflow 归属。
  - `workflow_task_repos`：Task 与 Workflow 已绑定 Repo 的关联、允许路径和任务级 base SHA。
  - `workflow_attempts`：attempt 序号、状态、租约字段、时间和错误码；Session 绑定字段可空，M1 不启动 Session。
- 在 `backend/internal/domain` 新增 Workflow / Task / Edge / Attempt 等领域类型和必要的不变量校验。
- 按仓库流程执行 `npm run migration:create -- <snake_case_name>`、更新 migration 注册清单，并通过 `npm run sqlc` 生成 Store 代码。

**交付物**
- 新增 migration、schema/query、sqlc 生成结果。
- 新增领域类型及构造/校验方法。
- 迁移升级、表约束与外键测试；sqlc 生成查询在 M1-01 完成，面向 Service 的 Store 读写/事务方法在 M1-03 与生命周期一起实现，避免提前形成两套事务入口。

**验收标准**
- 全新测试数据库和已有版本测试数据库均可向前迁移。
- 唯一键、外键、revision、attempt 序号等约束符合设计。
- sqlc 查询生成代码成功并通过相关包编译/测试；M1-03 的 Store wrapper 通过生成代码访问数据库，不手改 `backend/internal/storage/sqlite/gen/*`。
- 不修改已发布 migration，不把 Workflow 状态写入既有 Session 状态字段。

**依赖**：无。此项为 M1 首个实施任务。

**执行记录（2026-11-03）**：新增 migration `20261103044048260_workflow_core.sql`，创建 Workflow / Repository / Task / Edge / Task-Repo / Attempt 六类持久化结构；更新 migration ledger，新增 sqlc queries 并生成 `gen/workflow.sql.go` 与模型。领域模型及不变量测试位于 `domain/workflow.go`、`domain/workflow_test.go`；Migration 测试覆盖升级、跨 Workflow 外键、自环、租约约束、Attempt 唯一性及回滚。`npm run sqlc`、Workflow 专项测试、SQLite 与 Store 全量包测试、目标包 `go vet` 均通过。`go test ./internal/domain` 仍有 M0 已记录的 2 条 handoff 基线失败，Workflow 专项测试通过；未修改 `handoff_note.go`。

## 3. M1-02：DAG 校验器

**状态：已完成（2026-11-03）**

**目标**：实现纯本地、确定性的 DAG 合法性验证，不调用 LLM 或 Agent。

**范围**
- 校验 Task ID 唯一、依赖目标存在、无自环、无重复边、无跨 Workflow 边。
- 检测环并提供可定位的错误信息；输出稳定的拓扑顺序。
- 校验 RepoKey 指向 Workflow 已绑定的仓库；仓库路径与任务允许路径必须为安全的相对路径，拒绝绝对路径、盘符/流语法、反斜杠和 `..` 穿越，并检测规范化后的重复路径。
- 对节点数、最大深度、最大分支/扇出设置硬上限；限制由调用方显式传入，校验器不臆造默认数值。深度按根节点为 1 计算，FanOut 按节点的出边数计算。
- 对 Planner/客户端传入的数据一律按不可信输入校验。

**交付物**
- 独立 DAG validator。
- 表驱动单元测试与错误码/字段路径约定。

**验收标准**
- 正常 DAG 可得到稳定拓扑序。
- 重复 ID、缺失依赖、自环、跨 Workflow 依赖、重复边、环和超限图均被拒绝。
- 测试不需要数据库、网络、Provider 或真实 Agent。

**依赖**：M1-01 的领域类型与依赖边结构。

**执行记录（2026-11-03）**：新增纯本地 `domain.ValidateWorkflowGraph`，使用稳定 Kahn 拓扑排序（字典序最小堆）和确定性 DFS 环路定位；覆盖重复/空 Task ID、缺失/重复/自环/跨 Workflow 边、RepoKey 未绑定、跨 Workflow/重复 Repo 绑定、任务仓库引用错误、路径穿越、节点数/深度/FanOut 超限。返回稳定错误码、字段路径、Cycle 路径、深度映射和拓扑顺序；输入切片不会被修改。专项测试与 `go vet ./internal/domain` 通过，按 `--new-from-rev=HEAD` 运行仓库 golangci-lint 为 0 issues。Domain 全量测试仍仅有 M0 基线中的 2 条 handoff 测试失败，未修改相关文件。

## 4. M1-03：Workflow Service 与 Task 生命周期

**状态：已完成（2026-11-03，含 Review 后的修复与验收）**

**目标**：把领域规则落到事务服务，保证任务创建、状态迁移、领取和 Attempt 结算的一致性。

**范围**
- 在 `backend/internal/storage/sqlite/store` 实现面向 Workflow 的读写与事务方法，并在 `backend/internal/service/workflow` 建立 Service 和 Store/Clock 等必要依赖接口，遵循现有 ports/service 分层。
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

**执行记录（2026-11-03，已完成）**：Store 侧实现 Workflow/Task/Attempt/Repository 读写、事务创建、Activate、原子 Claim、Start、Settle、Retry、过期租约恢复与幂等回放；Service 侧在 `backend/internal/service/workflow` 提供 Create/Get/List/Task 查询与完整生命周期方法，注入 Clock/ID/LeaseToken/GraphLimits，不 spawn Agent、不调 Chat Driver、不执行 Git。

代码 Review 结论：**0 P0 / 6 P1 / 9 P2**，6 项 P1 已全部修复——P1-1 在 Store 边界做 UTC 归一化（`modernc.org/sqlite` 中租约排序是 TEXT 比较）；P1-2 恢复后的 Claim 重放改报 `WORKFLOW_CLAIM_REPLAY_EXPIRED`，不再返回无 capability 的结果；P1-3 启动时 + 周期性的租约恢复扫描已接入 Daemon（此前无生产调用方）；P1-4 `UNKNOWN` 原本是终态陷阱，新增 `ResolveUnknownWorkflowAttempt` / `Service.Resolve` 与 `attempt.resolve` 幂等操作，以 task revision 做 CAS 围栏且永不重新派发；P1-5 放宽 drain 语义，`StartWorkflowAttempt` 不再以 Workflow 处于 `active` 为前置，已领取的兄弟 Task 在终态失败后仍可 start/settle；P1-6 零行 UPDATE 路径改为分类定位原因，而非一律折算成 409。另修 P2-2：导出唯一 `domain.CanonicalWorkflowPath`，Service 与 Store 写入路径共用，使直接调用 Store 也无法绕过 `UNIQUE (workflow_id, project_id, path_ref)`。

其余 P2 经确认按 M1 范围内**接受并延后**：响应快照式幂等负载、幂等行随 Workflow 级联删除、含租约结构的 `%+v` 日志、`cancelled` Task 所在的 Workflow 不会自动完成、`claimed → succeeded` 缺 `started_at`、attempt_no 依赖 SQLite 单写者。专项测试与目标包 `go vet`、`-race` 均已通过，详见 [M1 验收报告](m1-report.md) §5。

## 5. M1-04：基础 HTTP API 与 CDC/SSE

**状态：已完成（2026-11-03）**

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

**执行记录（2026-11-03）**：新增 `backend/internal/httpd/controllers/workflows.go`，在 `/api/v1/workflows…` 下提供 11 条路由：`GET /workflows`、`POST /workflows`、`POST /workflows/recover-expired`、`GET /workflows/{workflowId}`、`GET /workflows/{workflowId}/tasks`、`GET /workflows/{workflowId}/tasks/{taskId}`、`POST /workflows/{workflowId}/activate`、`POST /workflows/{workflowId}/tasks/{taskId}/claim`、`POST /workflows/{workflowId}/attempts/{attemptId}/start`、`POST …/settle`、`POST …/tasks/{taskId}/retry`。Controller 沿用既有约定：Service 为 nil 时经 `apispec.NotImplemented` 返回 501；幂等键作为 JSON body 字段（与其余 API 一致，不用 Header）；create 限制 1 MiB body；仅当返回页恰好填满时才给 `nextCursor`（Service 无 total）；租约 token 只作为 claim 响应的顶层 `leaseToken` 出现，绝不进入 `attempt` 对象。

DTO 与契约：`dto.go` 新增 30 个 DTO 类型；`specgen` 新增 `workflowOperations()`、`workflows` tag 与 30 条 `schemaNames`；测试新增 15 条 controller 测试、1 条 SSE 测试、1 条回滚/CDC 测试。

CDC：新增 migration `20261103073516664_workflow_cdc.sql`，加入 4 个事件类型（`workflow_created`、`workflow_updated`、`workflow_task_updated`、`workflow_attempt_updated`）与 6 个触发器。`event_type` 的 CHECK 通过 `PRAGMA writable_schema` **原地放宽**（不重建表——重建需手工重建 34 个既有 change_log 触发器），并带内容级后置校验，因为不匹配的 `replace()` 会报告 1 行已改却什么都没改。Workflow 事件固定 `project_id = NULL`、`session_id = NULL`，标识符放在 payload：Workflow 跨多个 Project，且 `workflows` INSERT 时 `workflow_repositories` 尚未写入，按 Project 扇出会产出 0 条 `workflow_created`。Attempt 触发器只以 `state` 为条件，避免租约/心跳写入刷爆 change_log。

已验证：回滚的 Workflow 事务不产生 CDC 事件；`TestRouteSpecParity` 与 `TestBuild_MatchesEmbedded` 通过；`npm run api` 两次连续运行，`openapi.yaml` 与 `frontend/src/api/schema.ts` 字节一致。

## 6. M1-05：回归测试、恢复验证与验收归档

**状态：已完成（2026-11-03，验证通过）**

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

**执行记录（2026-11-03，M1-05 验证完成）**

构建与静态检查：

| 命令 | 范围 | 退出码 |
| --- | --- | --- |
| `go build ./...` | 全仓后端 | **0** |
| `go vet` | `internal/domain`、`internal/service/workflow`、`internal/storage/sqlite`、`internal/storage/sqlite/store`、`internal/cdc`、`internal/httpd/...`、`internal/daemon` | **0** |

目标包测试（dirty tree）：

| 包 | 结果 |
| --- | --- |
| `internal/service/workflow` | 全部通过 |
| `internal/storage/sqlite/store` | 429+ 测试通过，0 失败 |
| `internal/storage/sqlite` | ok（约 120s） |
| `internal/cdc` | ok |
| `internal/httpd/apispec` + `specgen` | ok |
| `internal/httpd` | ok（165 个顶层测试） |
| `internal/domain` | 70 通过 / 2 失败（均为既有 handoff 基线） |

Workflow 专项扫描 `go test ./... -run 'Workflow'`：**212 个包 ok、0 FAIL**；67 个顶层 PASS + 45 个子测试 PASS，0 FAIL。`-race` 运行 claim/settle/start/recover/retry 相关套件：退出码 **0**（service 52s、store 399s）。

前端与生成物：`npm run typecheck` 在 `packages/product-ui` 依赖安装后 **PASS**（退出码 0），`frontend/src` 与 `src/api/schema.ts` 均零错误。`npm run sqlc` 可复现，重复运行输出字节一致；真实内容变化只有 `gen/models.go`（+77，workflow 结构体）与新增的 `gen/workflow.sql.go`，其余 35 个显示为 modified 的 `gen/*.go` 是 `core.autocrlf` 行尾产物、内容字节一致（用运行前副本做字节 diff 核实）。`npm run api` 可复现，两次连续运行的 `openapi.yaml` 与 `frontend/src/api/schema.ts` 字节一致。

基线隔离（本阶段的关键证据）：在提交 `623871d`（"docs: finalize M0 report and define M1 tasks"）上建立 pristine worktree，确认 clean 且**不含任何 Workflow 文件**（无 `internal/service/workflow`、无 `domain/workflow.go`），作为对照基线逐包重放。

| 包 | HEAD 基线 | dirty tree（M1） | 结论 |
| --- | --- | --- | --- |
| `internal/domain` | 2 fail | 2 fail（72 run / 70 pass） | 相同 |
| `internal/httpd/controllers` | 4 fail（617 run / 607 pass / 4 fail / 6 skip） | 4 fail（632 run / 622 pass / 4 fail / 6 skip） | 相同 |
| `internal/daemon` | 2 fail（103 run / 101 pass） | 2 fail（103 run / 101 pass） | 相同 |

集合差：8 条可复现失败**全部为既有失败，M1 新增回归为零**。（§6 范围里原写「`internal/httpd/controllers` 5 条既有失败」是计划期估计，本轮实测为 4 条。）

8 条既有失败及其归因（均属 Windows 环境产物，非 M1 引入）：

1. `TestBuildHandoffNoteStaysUnderTheCapAndKeepsTheLatestTurn`（`handoff_note_test.go:84`）——既有 handoff 缺陷，属 M2 路径，M1 未修改。
2. `TestHandoffNoteKeepsArtifactsWithinTheByteCap`（`handoff_note_test.go:211`）——同上。
3. `TestListDirsRejectsRelativeAndMissingPaths`（`fs_test.go:133`）——错误码断言不匹配。
4. `TestBridgeStatusConcurrentSecurePairing`（`mobile_concurrency_test.go:106`）——原子重命名报 `Access is denied`。
5. `TestArtifactFileRouteServesOnlyRegularFilesInsideTheDirectory`（`artifact_files_test.go:238`）——缺符号链接权限。
6. `TestProjectsAPI_Clone`（`projects_test.go:297`）——沙箱下 git URL 校验。
7. `TestStabilizeWorkingDirectoryChdirsToDataDir`——TempDir 在作为进程 cwd 时被清理。
8. `TestWiring_StartSessionSpawnsScratchWithoutGitRepo`（`wiring_test.go:443`）——沙箱拒绝 fake-harness 凭据。

`handoff_note.go` 与 `handoff_note_test.go` 在工作树中**未被修改**（两者的 `git diff --stat HEAD` 为空）。`internal/domain` 用 `-count=5` 复核：同样两条测试在 5 次迭代中全部失败，为确定性失败而非 flaky。

边界：全程 0 Provider turn、0 token，未启动任何真实 Agent；未读写真实 `~/.ao`（测试使用 `t.TempDir()` / `sqlitetest`）；未改动 `.codex/`、`.claude/`；未执行 reset/clean/checkout/stash/commit/push。完整证据见 [M1 验收报告](m1-report.md)。

## 7. 执行顺序与边界

| 顺序 | 任务 | 前置 | 状态 |
| --- | --- | --- | --- |
| 1 | M1-01 数据库迁移与领域模型 | 无 | **已完成** |
| 2 | M1-02 DAG 校验器 | M1-01 | **已完成** |
| 3 | M1-03 Workflow Service 与 Task 生命周期 | M1-01、M1-02 | **已完成** |
| 4 | M1-04 基础 HTTP API 与 CDC/SSE | M1-01、M1-03 | **已完成** |
| 5 | M1-05 回归测试、恢复验证与验收归档 | M1-01～M1-04 | **已完成** |

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
- `internal/httpd/controllers` 有 **4 条**既有失败（实测；计划期估计的「5 条」不准确）。本轮另行测得 `internal/daemon` 2 条既有失败，故既有基线合计 **8 条**（domain 2 + controllers 4 + daemon 2）。这 8 条均须保留基线对照并证明未新增，**不得误报为 M1 新增回归**。
- Codex Windows 账号目录 `account_storage_unsafe` 是已知启动 WARN，M1 不依赖该功能；另列 M2 follow-up，不在本任务范围内。
- 仓库 `CONTRIBUTING.md` 要求向上游贡献的 Feature 先取得 core-team approval；本计划用于用户本地定制分支，若后续准备向上游提交 PR，需先满足该审批要求。

## 8. 完成状态

截至 2026-11-03，**M1-01～M1-05 全部完成并验证通过**，M1 核心验收判 **PASS**（逐条证据见 [M1 验收报告](m1-report.md)）。

- **零新增回归**：8 条可复现失败全部为既有 Windows 环境基线（`internal/domain` 2 + `internal/httpd/controllers` 4 + `internal/daemon` 2），已在同一提交 `623871d` 的 pristine worktree 上复现，集合差为零。
- **生成物锁定可复现**：`npm run sqlc` 与 `npm run api` 各连续两次运行，输出字节一致。
- **边界保持**：0 Provider turn / 0 token，未启动真实 Agent，未读写真实 `~/.ao`。

M0 正式验收仍为 **BLOCKED**，M0-05 预算未批准；**M1 完成不改变 M0 状态**。M2～M6 未开始。交接细节见 [m1-handoff.md](m1-handoff.md)（已归档关闭）。
