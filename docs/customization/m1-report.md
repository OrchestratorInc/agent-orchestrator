# M1 阶段验收报告（Workflow 核心后端，零 Provider）

> **本文件记录截至 2026-11-03 的实测结果，用于 M1-01～M1-05 的核心验收归档。**
> 本报告**主张**：M1 的六个完成定义项（持久化事实、DAG 校验、原子领取、revision/CAS + 幂等、HTTP API + CDC/SSE、可重复的测试证据）已由自动化证据支撑，M1 核心验收判 **PASS**；M1 未引入任何新的测试回归。
> 本报告**不主张**：M0 正式验收通过、四 Agent 已具备真实对话证据、M2～M6 的任何能力、Windows 全仓测试基线为绿、Provider 侧行为已验证。
> **已消耗 provider turn = 0，token = 0**；未启动任何真实 Agent。M0 正式验收仍为 **BLOCKED**，M0-05 预算仍未批准，M1 完成不改变该状态。
> 未实测项一律标注「未验证」，不以设计假设代替实测结果。

---

## 1. 执行元数据

| 项 | 值 | 来源 |
| --- | --- | --- |
| 报告编写时间 | 2026-11-03（本地，UTC+08:00） | 本次 |
| 工作区 | `E:\code\agent-orchestrator` | — |
| 分支 | `feat/multi-agent-desktop` | `git rev-parse --abbrev-ref HEAD` |
| 对照基线提交 | `623871d`（"docs: finalize M0 report and define M1 tasks"） | 基线隔离用的 pristine worktree |
| 工作树状态 | **dirty**：M1 全部实现未提交（新增 migration / domain / store / service / controller / 测试，以及 sqlc 与 API 生成物） | `git status --porcelain` |
| 生成物状态 | `gen/*.go` 中有 35 项显示为 modified，属 `core.autocrlf` 行尾产物、内容字节一致（§3.5） | 运行前副本字节 diff |
| 执行类型 | 本地构建 + 目标包测试 + 专项扫描 + `-race` + 生成可复现性核验 + 基线对照重放；**无 Provider 调用** | — |

---

## 2. 交付物清单

### 2.1 M1-01 数据库迁移与领域模型

| 类别 | 路径 |
| --- | --- |
| Migration（核心结构） | `backend/internal/storage/sqlite/migrations/20261103044048260_workflow_core.sql` |
| Migration（幂等记录） | `backend/internal/storage/sqlite/migrations/20261103050931629_workflow_idempotency.sql` |
| sqlc 查询 | `backend/internal/storage/sqlite/queries/workflow.sql`、`backend/sqlc.yaml` |
| 生成代码 | `backend/internal/storage/sqlite/gen/workflow.sql.go`、`gen/models.go`（不手改） |
| 领域类型 | `backend/internal/domain/workflow.go`、`workflow_test.go` |
| 迁移/约束测试 | `backend/internal/storage/sqlite/migrate_workflow_core_test.go`、`migrate_workflow_idempotency_test.go`、`migrate_burned_versions_test.go` |

六类持久化结构：`workflows`、`workflow_repositories`、`workflow_tasks`、`workflow_edges`、`workflow_task_repos`、`workflow_attempts`。会话绑定字段可空，M1 不启动 Session。

### 2.2 M1-02 DAG 校验器

| 类别 | 路径 |
| --- | --- |
| 校验器 | `backend/internal/domain/workflow_graph.go`（`domain.ValidateWorkflowGraph`） |
| 测试 | `backend/internal/domain/workflow_graph_test.go` |

纯本地、无数据库/网络依赖；稳定 Kahn 拓扑排序（字典序最小堆）+ 确定性 DFS 环路定位。

### 2.3 M1-03 Workflow Service 与 Task 生命周期

| 类别 | 路径 |
| --- | --- |
| Service | `backend/internal/service/workflow/service.go`、`service_test.go` |
| Store 读写/事务 | `backend/internal/storage/sqlite/store/workflow_store.go` |
| 生命周期/原子领取 | `backend/internal/storage/sqlite/store/workflow_lifecycle_store.go` |
| Store 测试 | `backend/internal/storage/sqlite/store/workflow_store_test.go` |
| Daemon 租约恢复接线 | `backend/internal/daemon/workflow_wiring.go`（P1-3 修复） |

### 2.4 M1-04 基础 HTTP API 与 CDC/SSE

| 类别 | 路径 |
| --- | --- |
| Controller | `backend/internal/httpd/controllers/workflows.go`、`workflows_test.go` |
| DTO | `backend/internal/httpd/controllers/dto.go`（新增 30 个类型） |
| 路由注册 | `backend/internal/httpd/api.go` |
| API spec | `backend/internal/httpd/apispec/specgen/build.go`、`apispec/openapi.yaml`、`frontend/src/api/schema.ts` |
| CDC 事件模型 | `backend/internal/cdc/event.go` |
| CDC migration/测试 | `backend/internal/storage/sqlite/migrations/20261103073516664_workflow_cdc.sql`、`migrate_workflow_cdc_test.go`、`store/workflow_cdc_test.go` |
| SSE 测试 | `backend/internal/httpd/events_test.go` |

11 条路由：`GET /workflows`、`POST /workflows`、`POST /workflows/recover-expired`、`GET /workflows/{workflowId}`、`GET /workflows/{workflowId}/tasks`、`GET /workflows/{workflowId}/tasks/{taskId}`、`POST /workflows/{workflowId}/activate`、`POST /workflows/{workflowId}/tasks/{taskId}/claim`、`POST /workflows/{workflowId}/attempts/{attemptId}/start`、`POST …/settle`、`POST …/tasks/{taskId}/retry`。

### 2.5 M1-05 回归测试与验收归档

| 类别 | 路径 |
| --- | --- |
| 验收报告 | `docs/customization/m1-report.md`（本文件） |
| 任务计划（状态回填） | `docs/customization/m1-plan.md` |
| 交接存档 | `docs/customization/m1-handoff.md` |
| 文档入口 | `docs/customization/README.md` |

---

## 3. 验收证据

### 3.1 构建与静态检查

| 命令 | 范围 | 退出码 | 判定 |
| --- | --- | --- | --- |
| `go build ./...` | 全仓后端 | **0** | PASS |
| `go vet ./internal/domain` | 领域 | **0** | PASS |
| `go vet ./internal/service/workflow` | Service | **0** | PASS |
| `go vet ./internal/storage/sqlite` | 迁移层 | **0** | PASS |
| `go vet ./internal/storage/sqlite/store` | Store 层 | **0** | PASS |
| `go vet ./internal/cdc` | CDC | **0** | PASS |
| `go vet ./internal/httpd/...` | HTTP 层 | **0** | PASS |
| `go vet ./internal/daemon` | Daemon | **0** | PASS |

### 3.2 目标包测试（dirty tree）

| 包 | 结果 | 备注 |
| --- | --- | --- |
| `internal/service/workflow` | 全部通过 | — |
| `internal/storage/sqlite/store` | **429+ 测试通过 / 0 失败** | 含并发单赢家、幂等、租约过期、重启恢复 |
| `internal/storage/sqlite` | ok | 约 120s |
| `internal/cdc` | ok | — |
| `internal/httpd/apispec` + `specgen` | ok | 含 `TestBuild_MatchesEmbedded` |
| `internal/httpd` | ok | 165 个顶层测试 |
| `internal/domain` | **70 通过 / 2 失败** | 2 条为既有 handoff 基线（§4） |

### 3.3 Workflow 专项扫描

| 命令 | 结果 |
| --- | --- |
| `go test ./... -run 'Workflow'` | **212 个包 ok，0 FAIL**；67 个顶层 PASS + 45 个子测试 PASS，0 FAIL |

### 3.4 竞态检测

| 命令 | 结果 | 耗时 |
| --- | --- | --- |
| `-race` 运行 claim / settle / start / recover / retry 套件 | **退出码 0** | service 52s、store 399s |

### 3.5 前端与生成物可复现性

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| 前端类型检查 | `npm run typecheck` | **PASS（退出码 0）**；前提是先安装 `packages/product-ui` 依赖；`frontend/src` 与 `src/api/schema.ts` 均零错误 |
| sqlc 生成 | `npm run sqlc`（重复运行） | 输出**字节一致**；真实内容变化仅 `gen/models.go`（+77，workflow 结构体）与新增 `gen/workflow.sql.go` |
| sqlc 行尾噪声核实 | 运行前副本字节 diff | 另有 35 个 `gen/*.go` 显示 modified，均为 `core.autocrlf` 行尾产物，**内容字节一致** |
| API 契约 | `npm run api`（连续两次） | `openapi.yaml` 与 `frontend/src/api/schema.ts` **字节一致** |

---

## 4. 基线隔离证明

### 4.1 对照方法

在提交 `623871d`（"docs: finalize M0 report and define M1 tasks"）上建立 pristine worktree，确认 `git status` clean 且**不含任何 Workflow 文件**（无 `internal/service/workflow`、无 `domain/workflow.go`），随后与 dirty tree 逐包对照。这是判断「M1 是否引入回归」的唯一依据——不按包名或失败消息推断。

### 4.2 HEAD 基线 vs dirty tree

| 包 | HEAD 基线（`623871d`） | dirty tree（含 M1） | 差异 |
| --- | --- | --- | --- |
| `internal/domain` | 2 fail | 2 fail（72 run / 70 pass） | 相同 |
| `internal/httpd/controllers` | 4 fail（617 run / 607 pass / 4 fail / 6 skip） | 4 fail（632 run / 622 pass / 4 fail / 6 skip） | 相同 |
| `internal/daemon` | 2 fail（103 run / 101 pass） | 2 fail（103 run / 101 pass） | 相同 |

集合差：8 条可复现失败全部在基线中已存在，**M1 新增回归 = 0**。

补充确定性核对：`internal/domain` 以 `-count=5` 复跑，同样两条测试在 5 次迭代中全部失败，属确定性失败而非 flaky。`handoff_note.go` 与 `handoff_note_test.go` 在工作树中未被修改（两者 `git diff --stat HEAD` 为空）。

### 4.3 8 条既有失败的原因分类

| # | 测试 | 位置 | 原因分类 |
| --- | --- | --- | --- |
| 1 | `TestBuildHandoffNoteStaysUnderTheCapAndKeepsTheLatestTurn` | `handoff_note_test.go:84` | 既有 handoff 缺陷（M2 路径），M1 未修改该文件 |
| 2 | `TestHandoffNoteKeepsArtifactsWithinTheByteCap` | `handoff_note_test.go:211` | 同上 |
| 3 | `TestListDirsRejectsRelativeAndMissingPaths` | `fs_test.go:133` | 错误码断言不匹配 |
| 4 | `TestBridgeStatusConcurrentSecurePairing` | `mobile_concurrency_test.go:106` | 原子重命名 `Access is denied`（Windows） |
| 5 | `TestArtifactFileRouteServesOnlyRegularFilesInsideTheDirectory` | `artifact_files_test.go:238` | 缺符号链接权限（Windows） |
| 6 | `TestProjectsAPI_Clone` | `projects_test.go:297` | 沙箱下 git URL 校验 |
| 7 | `TestStabilizeWorkingDirectoryChdirsToDataDir` | — | TempDir 在其为进程 cwd 时被清理 |
| 8 | `TestWiring_StartSessionSpawnsScratchWithoutGitRepo` | `wiring_test.go:443` | 沙箱拒绝 fake-harness 凭据 |

全部 8 条均为 **Windows 环境产物或既有缺陷**，与 M1 改动无因果关系。

### 4.4 结论

M1 在 `internal/domain`、`internal/httpd/controllers`、`internal/daemon` 三个基线为红的包上**未新增任何失败**；新增测试全部通过。故不得把上述 8 条误报为 M1 回归，也不得以 `go test ./...` 在当前 Windows 红基线上的整体结论替代目标包验收。

---

## 5. 代码 Review 结论与修复

### 5.1 Review 统计

| 级别 | 数量 | 状态 |
| --- | --- | --- |
| P0 | 0 | — |
| P1 | 6 | **全部已修复** |
| P2 | 9 | 1 项（P2-2）已修复，其余按 M1 范围接受并延后（§5.4） |

### 5.2 P1 修复清单

| 编号 | 问题 | 修复 |
| --- | --- | --- |
| P1-1 | 租约时间未归一化 | 在 Store 边界统一做 UTC 归一化——`modernc.org/sqlite` 中租约排序是 TEXT 比较，混用本地时区会破坏排序 |
| P1-2 | 恢复后的 Claim 重放返回无 capability 的结果 | 改报 `WORKFLOW_CLAIM_REPLAY_EXPIRED` |
| P1-3 | 租约恢复扫描无生产调用方 | 在 Daemon 中接入**启动时 + 周期性**扫描（`workflow_wiring.go`） |
| P1-4 | `UNKNOWN` 是终态陷阱，无人能解除 | 新增 `ResolveUnknownWorkflowAttempt` / `Service.Resolve` 与 `attempt.resolve` 幂等操作，以 task revision 做 CAS 围栏，且**永不重新派发** |
| P1-5 | drain 语义过严 | `StartWorkflowAttempt` 不再以 Workflow 处于 `active` 为前置，已领取的兄弟 Task 在终态失败后仍可 start/settle |
| P1-6 | 零行 UPDATE 一律折算为 409 | 改为分类定位原因（状态不符 / revision 冲突 / 目标不存在等） |

### 5.3 已修复的 P2

- **P2-2**：导出唯一 `domain.CanonicalWorkflowPath`，Service 与 Store 写入路径共用，使直接调用 Store 也无法绕过 `UNIQUE (workflow_id, project_id, path_ref)`。

### 5.4 按 M1 范围接受并延后的 P2

| 项 | 说明 |
| --- | --- |
| 响应快照式幂等负载 | 当前幂等记录不保存原响应体，重放依赖重新读取当前状态；M2 引入跨进程重试时再补 |
| 幂等行随 Workflow 级联删除 | 便于清理，但会丢失审计痕迹；需与保留策略一起决定 |
| 含租约结构的 `%+v` 日志 | 存在把 lease token 写入日志的风险；M1 未启用该日志路径 |
| `cancelled` Task 所在 Workflow 不会自动完成 | 完成判定语义未定义，留待 M2/M3 明确 |
| `claimed → succeeded` 缺 `started_at` | 与设计的状态机细节不一致，当前路径未启用 |
| attempt_no 依赖 SQLite 单写者 | 换用多写者存储前必须改为显式序列 |

---

## 6. 未覆盖与已知限制

| 项 | 说明 |
| --- | --- |
| 真实 Agent 行为 | **未验证**。全程 0 Provider turn / 0 token，未启动真实 Agent；任何依赖真实 Agent 的语义均无证据 |
| 非 Windows 平台 | **未验证**。本轮全部命令在 Windows 11 环境执行；基线 8 条失败本身即 Windows 环境产物 |
| Windows 全仓测试基线 | 非绿。本轮只以目标包 + 专项扫描 + 基线对照作为验收依据（§4） |
| 多写者数据库 | 未验证。`attempt_no`、租约排序等设计假设建立在 SQLite 单写者之上（§5.4） |
| Workflow 完成判定（含 cancelled 任务） | 未实现，见 §5.4 |
| 预算账本 / 资源调度 | 未实现，属 M3 |
| Session 绑定 | 字段可空占位，未实现，属 M2 |
| M0 正式验收 | 仍为 BLOCKED；M0-05 预算未批准。M1 完成不改变该结论 |
| M0 已知 W/A 缺口 | W-07 `NOT_TESTED`、A-02～A-15 未执行（沿用 `m0-report.md` §8.1） |

边界复核（本轮实测而非声明）：0 Provider turn、0 Token、未启动真实 Agent；测试使用 `t.TempDir()` / `sqlitetest`，**未读写真实 `~/.ao`**；未改动 `.codex/`、`.claude/`；未执行 git reset / clean / checkout / stash / restore / commit / push。

---

## 7. 结论

| 判定项 | 结论 | 依据 |
| --- | --- | --- |
| M1 核心验收 | **PASS** | §3 构建/vet/目标包/专项/`-race`/生成物全部达标；§4 证明零新增回归 |
| M1 新增回归 | **0** | §4.2 集合差为零 |
| 生成物漂移 | **无** | §3.5 两次运行字节一致 |
| Provider 消耗 | **0 turn / 0 token** | §6 |
| M0 正式验收 | **BLOCKED**（不变） | `m0-report.md` §8.1；M0-05 预算未批准 |
| M2～M6 | **未开始** | — |

M1 的授权范围是「零 Provider 的核心后端能力」，该范围已交付并具备可复现证据。M1 的 PASS **不构成** M0 降级验收、不构成四 Agent 就绪证据、不构成任何 Provider 侧行为结论。

---

## 8. 后续 M2/M3 接口与未决事项

以下是 M2/M3 开工前必须明确的具体接口与决策点，均来自本轮实现与 Review 的实际缺口，不是设计推演清单。

1. **Attempt 的 Session 绑定**：`workflow_attempts` 的 Session 字段当前可空且从未写入。M2 需定义：领取（claim）时是否即绑定 Session、绑定失败的补偿路径、以及 Session 结束与 Attempt settle 的先后约束；`started_at` 与 Session 启动时刻的对应关系也需一并确定。
2. **Agent Capability Resolver**：`StartWorkflowAttempt` 目前只校验状态与租约，不校验执行者的能力。M2 需给出「Task 类型 → 所需能力 → 可用 Agent」的解析接口与拒绝语义，并明确能力不足时是 fail-closed 阻塞还是退回 READY。
3. **租约恢复的触发策略与调度**：P1-3 已接入启动 + 周期性扫描，但周期、批量上限、与 `POST /workflows/recover-expired` 手动入口的关系（是否互斥、是否共享幂等键）尚未定义。
4. **预算账本**：M1 只有 Attempt 级别的次数预算，没有 token/金额账本。M3 需要账本的数据结构与扣减时机，并与 M0-05 的 Provider 预算批准机制对齐——否则无法证明「零超支」。
5. **`cancelled` Task 的 Workflow 完成语义**：当前 `cancelled` 任务的 Workflow 永不自动完成。需定义完成判定（cancelled 是否算终止态、是否解锁下游、是否允许强制完成），并补齐对应 Store 方法与测试。
6. **幂等响应快照**：当前幂等记录不保存响应体，跨进程重放时返回的是「重新读取的当前状态」而非原响应。M2 若引入多客户端重试，需要把响应快照纳入幂等记录（含 TTL 与级联删除策略）。
7. **状态机对齐**：`claimed → succeeded` 缺 `started_at` 的路径需与设计状态机统一，并决定是否允许该捷径；建议随 M2 的 Session 绑定一起收口。
8. **多写者存储假设**：`attempt_no` 的分配与租约排序都依赖 SQLite 单写者。若 M3 引入并发度更高的调度或独立进程，必须先给出显式序列（如带 CHECK 的递增表），否则存在重复 attempt_no 风险。
