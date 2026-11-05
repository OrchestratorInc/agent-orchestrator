# M2 编码执行与分级协作 — 开发任务计划

> **用户提交决定（2026-11-05）：先提交现有 M2 实现，暂缓第六轮离线独立复验与真实 Agent 完整验收。** 两项的恢复前提、检查范围和完成证据已记录在 [M2 提交与后续验收留档](m2-handoff.md)。提交不等于验收通过，仍保留 **M2 NOT ACCEPTED**；M0-05 未批准，不启动真实 Provider 或 M3。

> **最新独立验收（2026-11-05 第五轮）：NOT ACCEPTED。** F-01 历史 BaseSHA 与 F-02 正式 Operator 链已有独立离线证据；新增 F-03（P1）单仓 Worktree 实时验证缺口阻断 M2，M3 不得开始。以 [m2-acceptance-round5.md](m2-acceptance-round5.md) 为准。下方第三轮及更早记录保留为历史，不代表最新验收状态。

> **完整回归补修（2026-11-05）：提示词清单内回归及新增影响包通过，可供第六轮独立复验。** 根据用户追加要求，已修复 Windows 进程取消、私有文件原子创建/发布、并发替换、目录 junction 分类及平台夹具问题。M2 普通 **33/33 PASS、0 SKIP、539.503s**；M2 race **33/33 PASS、0 SKIP、658.859s，无 data race**。Manager 完整包 **508.685s**；fsbrowser 普通/race **1.985s / 5.225s**，其后 Controller/Daemon 完整包 **57.062s / 30.353s**，均 exit 0。前端 **633 文件通过、10877 PASS、17 原有 SKIP、1521.31s，exit 0**。Chat、核心服务、SQLite、其他影响包、F-03 原始反例、F-01/F-02 离线回归、最终 Build/Vet/Typecheck、API/sqlc 无漂移均通过；最终后端 1214 个生产/依赖文件核对零漂移，入场文件删除与受保护配置变更均为 0。详见 [m2-regression-repair.md](m2-regression-repair.md)。本轮“完整回归”指原提示词列出的包与新增影响包、M2 集成和前端全套，未声明全仓 `go test ./...` 或全仓 CI 通过。原生人工审批、其他操作系统及真实 Provider 未验。仍保持 **M2 NOT ACCEPTED**，本任务不进入 M3；下方第五轮失败记录保留为本次补修前的历史。

> **第五轮 F-03 修复进度（2026-11-05）：F-03 定向、全 M2 与 Race 通过；完整回归 FAIL。** 详见 [m2-remediation-round5.md](m2-remediation-round5.md)。无 skip 全 M2 **33/33 PASS，495.135s，exit 0**；全 M2 Race **578.724s，exit 0，无 data race**，两轮源码 SHA 一致。单仓真实 Git/SQLite/HTTP、13 个现场异常、5 个执行入口、dirty 保留、原始反例及双仓历史 BaseSHA 均实跑通过；独立复核支持关闭补充发现的 Steer 等待、拒绝消息残留、历史分支恢复三项 P1。Build/Vet、API/sqlc 无漂移、TypeScript、F-02 CLI/IPC 已通过；Manager、Controller、Daemon、Chat 及前端完整套件仍有 FAIL，第六轮申请条件尚未全部满足。保持 **M2 NOT ACCEPTED**。
>
> | 第五轮任务 | 实际进度 | 证据 |
> | --- | --- | --- |
> | F03-01 单仓/多仓统一实时验证 | 实现与焦点测试通过，待独立验收 | `ObserveSessionWorktrees`；真实 Git 归属、分支、Base/Creation/live HEAD 及 status |
> | F03-02 Gateway Fail-Closed | 实现与焦点测试通过，待独立验收 | 原第五轮负例最终 2/2 PASS；缺目录/缺行/缺验证能力不返回 READY |
> | F03-03 Legacy 与 M2 身份区分 | 实现与焦点测试通过，待独立验收 | 持久化 Attempt/Spawn 意图与 Repo 集合；`TestF03LegacySingleRepoCompatibility` |
> | F03-04 全调用链重验 | 实现、焦点、全 M2 与 Race 通过，待独立验收 | HTTP Queue/Steer/Resume/Reconcile/重复 Bind；出队/ack 后/分支恢复前门禁；generation fencing |
> | 完整回归与交付报告 | 报告已更新；完整回归 FAIL，未满足全部申请条件 | Manager 10 失败中旧 HEAD 同因复现 9；PostCreate 当前单例也 FAIL，取消链时延归因未定；前端 192 失败；其余明细见第五轮修复报告 |

> 日期：2026-11-03；工作区：`E:\code\agent-orchestrator`；分支：`feat/multi-agent-desktop`
> Git 基线：**`a6c284a4d060431371306072e6c80abfedf22328`**（`feat: implement M1 workflow core and acceptance`）。
> 设计依据：[m2-design.md](m2-design.md)（M2 唯一详细设计入口 V0.1）、[m2-agent-prompt.md](m2-agent-prompt.md)。
> 继承证据：[m1-report.md](m1-report.md) §5.4/§8、[m1-acceptance.md](m1-acceptance.md) §4、[m0-capability-matrix.md](m0-capability-matrix.md)、[m0-adapter-gaps.md](m0-adapter-gaps.md)。
> **状态：第二轮独立代码/运行复验（2026-11-04）结论仍为 NOT ACCEPTED，列出 B-01～B-08 共 8 项阻断。实施方已完成 P0-01～P2-01 的修复并有本地运行证据，详见 [m2-remediation-report.md](m2-remediation-report.md)。第二轮独立复验已完成，但仍未通过；最终结论以 [m2-reacceptance.md](m2-reacceptance.md) 为准，本表中的任何 Done/Partial 标记都不构成验收结论。**
> **零 Provider 边界不变**：本轮 0 个 provider turn、0 token、未启动任何真实 Agent、未读写真实 `~/.ao` / `.codex` / `.claude`。M0 正式验收仍为 **BLOCKED**，M0-05 预算未批准。

> **第三轮修复（2026-11-04）**：针对 [m2-reacceptance.md](m2-reacceptance.md) 的 R-01～R-08，实施方已完成源码与定向测试修复，逐项记录见 [m2-remediation-round3.md](m2-remediation-round3.md)。本段仅追加，不改写上方任何历史行与他处结论。
>
> | 编号 | 第三轮状态 | 修复落点（详见报告 §1） |
> | --- | --- | --- |
> | R-01 Send/Interrupt generation fencing | **已修复（待独立复验）** | `ports/execution.go:402-422,288-299,374-385,451-455`；`service/chat/service.go:1455,1800`；`chat/controller.go:1510`；`execution/local_gateway.go:530,700` |
> | R-02 RepoRefs 真正挂载到 Session 工作区 | **已修复（待独立复验）** | `ports/session.go:100-117`；`execution/local_gateway.go:386`；`session_manager/multi_repo_workspace.go:67,363`；`session_manager/manager.go:1343,4612` |
> | R-03 Agent 协作 MCP 工具与凭据上下文 | **已修复（待独立复验）** | `cli/mcp_workflow.go`（5 工具）；`cli/mcp.go:133,147`；`session_manager/chat_spawn_workflow.go`；`chat_spawn.go:369`；**`workflow_context.go:60,110` + `queries/workflow_binding.sql:21`（Change A：resume 从 durable state 恢复身份）** |
> | R-04 Operator/Human 身份边界与 Resolve 门禁 | **已修复（待独立复验）** | `service/principal/operator_token.go`；`verifier.go:152`；`daemon/workflow_auth_wiring.go:27`；`httpd/controllers/workflows.go:1488,1718,912,1289,1621,1671` |
> | R-05 Capability Probe generation / 证据等级 | **已修复（待独立复验）** | `service/capability/probe.go:82-87,222-237,264-273`；`resolver.go:280-305`；`controllers/workflows.go:1134-1175` |
> | R-06 Resume 严格 SpawnKey | **已修复（待独立复验）** | `service/execution/local_gateway.go:802` |
> | R-07 真实 Session Manager + Git Worktree 集成 | **已修复（待独立复验）** | `integration/m2_session_manager_e2e_test.go`（6 场景） |
> | R-08 Task Request 批准与 materialization 边界 | **已修复（待独立复验）** | `domain/workflow_collaboration.go:442-451`；`controllers/workflows.go:1794-1845`（M2 不 materialize Task，202 + `pending`） |
>
> 新增迁移 `20261104094706356_workflow_audit_log.sql`（append-only，无 UPDATE/DELETE 路径）。
>
> **最终一轮验证（2026-11-04）**：C1～C23 全部串行执行完成；`go build`/`go vet` exit 0；**M2 E2E RUN 24 / PASS 24（FAIL 0 / SKIP 0）**，含新增 MCP 链与真实 SessionManager 链；**4 组 race 全部 exit 0、无 data race**（含 resume 恢复路径）；`npm run sqlc`（gen 清单 41→41，前后 diff 相同）与 `npm run api`（openapi/schema sha256 前后一致）**无漂移**，`typecheck` exit 0。Windows 既有基线失败 **15 条**（4+2+1+7+1）逐条归类；前一轮暴露的 2 条本轮自身缺陷（`TestNoSchedulerReadsWorkflowTaskRequests`、`TestTaskRequestResponseCarriesNoRawPayload`）**均已修复并复测 exit 0**；**净新增回归 0**。详见报告 §6/§6.4/§7。
>
> 本轮另有两处能力性变更：**Change A** —— resume/重启后的 workflow 会话从 durable state（binding + attempt 行）再生 5 个协作工具，过期 lease 产出空 token 使工具 403 fail-closed，**关闭报告 §9 第 7 项**；**Change B** —— 审批边界守卫收窄为「SQL 动词 + 表名」并新增 `TestWorkflowTaskRequestReadMatcher`（11 例）自证。
>
> 报告 §9 现有 **8 项**开放（算式：原 9 项 − 已关闭的第 7、8 项 = 7，再 + 本轮新增的第 10 项 = 8），即第 1、2、3、4、5、6、9、10 项：真实 Provider `NOT_TESTED`、lease 15m 无续租（超窗口 resume 只剩 fail-closed 403）、Windows 不强制 0600、单仓库 repo binding 停在 `pending`、M2 不 materialize Task、`RestoreAll` 无生产调用方、`workflow_audit_log` 无 HTTP 读接口、以及**新增的「operator 路由无出厂客户端」**（`bind`/`reconcile`/`resume`/`resolve`/`decide` 现已要求 operator token，但桌面应用无任何路径能带上它，只能手写 curl；主进程 IPC 代理修法本轮未实施）。**上述“已修复”均为实施方主张，不构成 M2 最终 PASS，不得进入 M3。**

---

## 1. 任务进度表

| 编号 | 任务 | 状态 | 交付物 | 证据（命令 + 退出码） | 备注 |
| --- | --- | --- | --- | --- | --- |
| **M2-01** | M1/M0 继承项与代码所有权复核 | **Done** | 本文件 §2–§5 | `git rev-parse HEAD` = **0**；`git status --short --branch` = **0**；`git diff --check` = **0**；`cd backend && go build ./...` = **0**；`go vet ./internal/service/workflow ./internal/storage/sqlite/store ./internal/cdc ./internal/httpd/... ./internal/daemon` = **0**；`go test ./internal/domain -run HandoffNote -count=1` = **1**（2 条既有失败，见 §3 第 6 项） | 真实入口表（9 个域）、**8** 项 M1 继承 P2 风险分类、四 Agent 预检阻断、文件归属与 3 个串行化点、**6** 条设计-代码偏差 |
| **M2-02** | Capability Resolver + 四驱动证据映射 | **Partial** | 纯本地能力快照/TTL/证据来源/错误码；Gate 授权链 | 本轮：`go build ./...` = **0**；`go vet ./...` = **0**；`go test ./internal/service/capability -count=1` = **0**；`go test -race ./internal/service/capability` = **0** | Gate 的 AuthState/Readiness/Approval 修复通过；但 `ChatDriverProbe` 的 generation 回填、能力合并和 confirmed_runtime 证据仍有问题，见 [m2-reacceptance.md](m2-reacceptance.md) R-05。实施方原始记录见 [m2-remediation-report.md](m2-remediation-report.md)。 |
| **M2-03** | Attempt–Session/Repo 绑定的 DB/CAS/幂等迁移 | **Partial** | `20261103143320688_workflow_attempt_bindings.sql` + queries + store | 实施方记录：`go test ./internal/storage/sqlite ./internal/storage/sqlite/store -count=1` = **0**；`npm run sqlc` ×2 = **0**（gen 两次 sha256 一致）。独立复验：SQLite/Store 的 M2 定向测试与 race 通过；`npm run sqlc` 因 proxy.golang.org 超时未能复核 | SQLite migration/Store/CAS 测试通过；但 `LocalGateway.Spawn` 未把 RepoRefs 传入 Session Manager，实际多仓库工作区未完成，见 [m2-reacceptance.md](m2-reacceptance.md) R-02。 |
| **M2-04** | Local ExecutionGateway 与 Fake Session Manager | **Partial** | `internal/ports/execution.go`、`internal/service/execution/local_gateway.go`、`daemon/execution_wiring.go` | 本轮：`go test ./internal/service/execution -count=1` = **0**；`go test -race ./internal/service/execution` = **0**；`go test ./internal/daemon -run TestM2 -count=1` = **0**；`go test -race ./internal/integration -run TestM2 -count=1` = **0**（17/17 PASS） | LocalGateway 已实现并接入 Daemon；但本次 E2E 使用 Fake SessionSpawner，真实 Session Manager + Worktree 集成尚未验证，且 RepoRefs 未传入 SpawnConfig，见 [m2-reacceptance.md](m2-reacceptance.md) R-02/R-07。实施方记录见 [m2-remediation-report.md](m2-remediation-report.md)。 |
| **M2-05** | Steer/Queue/Stop & Replan 协调入口 | **Partial** | `internal/service/intervention/`、`ports/execution.go`、`service/chat/steer.go`、`20261103160303854_*` | 本轮：`go test ./internal/service/intervention -count=1` = **0**；`go test -race ./internal/service/intervention` = **0**；`go test ./internal/integration -run 'TestM2Steer\|TestM2Stop\|TestM2StaleGeneration' -count=1` = **0** | SteerPinned/target turn 已修；但 LocalGateway.Send/Interrupt 未验证 ControllerGeneration，旧 generation 仍可能作用于新 controller，见 [m2-reacceptance.md](m2-reacceptance.md) R-01。Stop 原子性 race 测试通过。实施方记录见 [m2-remediation-report.md](m2-remediation-report.md)。 |
| **M2-06** | 分级自治消息与任务申请 | **Fail** | `internal/service/collaboration/`、`service/principal/`、`controllers/workflows.go`、`20261103163208174_*` | 本轮：`go test ./internal/service/collaboration -count=1` = **0**；`go test -race ./internal/service/collaboration` = **0**；`go test ./internal/httpd/controllers -run 'TestM2AgentCredentialRequiresBothCapabilityAndLease\|TestM2TaskRequestRedactionAtTheWire\|TestM2AttemptRoutesRefuseCrossWorkflow' -count=1` = **0** | 服务端 capability+lease 校验和 task-request API 已实现；但 `ao mcp` 无 Workflow 工具且 MCP 环境无 capability/lease，实际 Agent 无法调用，见 [m2-reacceptance.md](m2-reacceptance.md) R-03；loopback Operator 与 Resolve 权限见 R-04。实施方记录见 [m2-remediation-report.md](m2-remediation-report.md)。 |
| **M2-07** | HTTP DTO/API/CDC/SSE + 生成代码 | **Partial** | `controllers/dto.go`、`controllers/workflows.go`、`api.go`、`specgen/build.go`、`20261103170009848_*` | 本轮：`npm run api` ×2 = **0**（两次校验和一致）；`npm run frontend:typecheck` = **0**；`go test ./internal/httpd/controllers -run TestM2 -count=1` = **0**；`go test -race ./internal/httpd/controllers -run TestM2 -count=1` = **0**；完整 `controllers` 包仍有 **4** 条既有 Windows 基线失败（见基线 §4 第 3–6 行） | Bind/归属/Observe/Reconcile/Resume 路由通过 M2 定向测试；但继承的 `resolveAttempt` 写接口无 Principal/Operator 检查，见 [m2-reacceptance.md](m2-reacceptance.md) R-04。实施方记录见 [m2-remediation-report.md](m2-remediation-report.md)。 |
| **M2-08** | 零 Provider E2E、恢复/安全/回归、阶段报告 | **Pass（Fake only）** | `m2-report.md`、本表状态回填、E2E/异常/安全测试 | `go test ./internal/storage/sqlite/store -run 'TestM2OfflineEndToEndTwoHarnesses\|TestM2AnomalyInjectionMatrix\|TestM2ZeroProviderGuarantees' -count=1` = **0**（异常矩阵 10/10）；`go test ./internal/httpd/controllers -run TestM2 -count=1` = **0**；`go test ./internal/storage/sqlite -run 'TestM2LocalRollbacksEmitZeroEvents\|TestM1DownPathLeavesChangeLogInvalid' -count=1` = **0**；`go test ./... -count=1` = **1**（241 条 / 58 包；相对 267 基线 **新增 5 条全部为 flaky、新增回归 0**，见 `m2-report.md` §3.3） | Fake/SQLite 服务层 E2E 与异常矩阵通过，第二轮独立复验 17/17；但 E2E 使用 Fake SessionSpawner，不创建真实 Git Worktree，不能证明多仓库工作区完成，见 [m2-reacceptance.md](m2-reacceptance.md) R-02/R-07。 |

状态取值：Done / Partial / Fail / Blocked / Pass（Fake only）/ In Progress / Pending / Deferred。状态以第二轮独立复验报告为准：M2-01 PASS；M2-02/M2-03/M2-04/M2-05/M2-07 PARTIAL；M2-06 FAIL；M2-08 PASS（Fake only）。实施方原始修复记录仍保留在 [m2-remediation-report.md](m2-remediation-report.md)，不覆盖独立复验结论。M2-02～M2-08 的依赖链见 m2-design §9；本表不含日期承诺。

---

## 2. M2-01 证据：真实入口表

**核查方法**：对每条 `path:symbol` 直接读取文件确认符号存在，不采信设计文档的命名。行号为本文件写作时的工作树行号（HEAD `a6c284a` + 已知 dirty 文档）。

### 2.1 Workflow 领域模型与状态机

| 事实 | 位置 | 已核实内容 |
| --- | --- | --- |
| ID 类型 | `backend/internal/domain/workflow.go:11,14,17,20` | `WorkflowID` / `WorkflowTaskID` / `WorkflowAttemptID` / `WorkflowRepoKey` |
| Workflow 状态机 | `backend/internal/domain/workflow.go:23-50` | `WorkflowState`：`draft/active/paused/completed/failed/cancelled` + `Valid()` |
| Task kind | `backend/internal/domain/workflow.go:52-75` | `analysis/development/testing/review/integration/approval` |
| Task 状态机 | `backend/internal/domain/workflow.go:77-109` | 13 态：`pending/ready/claimed/running/verifying/ready_for_integration/awaiting_approval/integrating/retry_wait/blocked/failed/cancelled/succeeded` |
| 幂等操作枚举 | `backend/internal/domain/workflow.go:111-125` | 7 值：`workflow.create/workflow.activate/task.claim/attempt.start/attempt.settle/task.retry/attempt.resolve` |
| Attempt 状态机 | `backend/internal/domain/workflow.go:127-152` | 6 态：`claimed/running/settling/succeeded/failed/unknown` |
| 实体与不变量 | `backend/internal/domain/workflow.go:154-377` | `Workflow`(154)/`Validate`(166)、`WorkflowRepository`(184)/`Validate`(194)、`WorkflowTask`(207)/`Validate`(223)、`WorkflowEdge`(245)/`Validate`(252)、`WorkflowTaskRepository`(264)/`Validate`(273)、`WorkflowAttempt`(334)/`Validate`(353，lease 三要素校验 366-374) |
| 领域错误 | `backend/internal/domain/workflow.go:379-398` | `ErrInvalidWorkflow` 等 `var (...)` 块 |
| 路径规范化 | `backend/internal/domain/workflow.go:328` | `CanonicalWorkflowPath`（P2-2 修复导出的唯一入口） |
| DAG 校验器 | `backend/internal/domain/workflow_graph.go:130` | `ValidateWorkflowGraph(graph, limits)`；`WorkflowGraph`(:12)、`WorkflowGraphLimits`(:22) |

### 2.2 Workflow Service 入口（Create/Claim/Start/Settle/Retry/Resolve）

位置：`backend/internal/service/workflow/service.go`

| 入口 | 行 | 关键事实 |
| --- | --- | --- |
| `Store` 接口 | 22-41 | **18** 个方法，含 `ClaimWorkflowTask`/`SettleWorkflowAttempt`/`ResolveUnknownWorkflowAttempt`/`RecoverExpiredWorkflowAttempts` |
| `Deps` / `New` | `Deps` 44-50、`New` 66 | 注入了 `Clock`/`NewID`/`NewLeaseToken`/`GraphLimits`/`LeaseDuration`；默认 `uuid` + `randomLeaseToken` |
| `Create` | 193 | DAG 校验 + 幂等；`INVALID_WORKFLOW_HARNESS` 在 240-241 |
| `Get`/`List`/`ListTasks`/`ListAttempts`/`GetTask`/`GetDefinition` | 340/355/374/396/408/429 | 读路径 |
| `Activate` | 496 | revision CAS + 幂等 |
| `Claim` | 525 | 生成 `leaseToken`；重放且 lease 已空 → `WORKFLOW_CLAIM_REPLAY_EXPIRED`（573-579） |
| `Start` | 584 | 要求 lease token + 幂等 |
| `Settle` | 614 | 仅接受 `succeeded`/`failed`（625-627） |
| `Resolve` | 655 | 唯一 UNKNOWN 出口；**无 HTTP/CLI 调用方**（§4 #2） |
| `Retry` | 690 | 重开 `retry_wait` |
| `RecoverExpired` | 719 | lease 扫描入口 |
| `mapStoreError` | 813 | 错误码归一（service/store/controller 层共 **32** 个 `WORKFLOW_*` 码 + **13** 个 `INVALID_*` 码） |

### 2.3 Workflow Store / CAS / 迁移

| 事实 | 位置 |
| --- | --- |
| 建表迁移 | `backend/internal/storage/sqlite/migrations/20261103044048260_workflow_core.sql`（`workflows` :6、`workflow_repositories` :21、`workflow_tasks` :36、`workflow_edges` :61、`workflow_task_repos` :76、`workflow_attempts` :93） |
| Attempt 约束 | 同上 :103-127：state CHECK（103-104）、租约三要素 CHECK（110-113）、`UNIQUE (workflow_id, task_id, attempt_no)`（114）、单活跃 partial unique index（121-123）、lease index（125-127） |
| 幂等账本迁移 | `...20261103050931629_workflow_idempotency.sql:6-17`：`workflow_idempotency(idempotency_key PK, workflow_id, operation CHECK 7 值, request_fingerprint =64 字符, result_type, result_id, created_at)` |
| CDC 迁移 | `...20261103073516664_workflow_cdc.sql`：6 个 trigger（:60/:70/:81/:91/:106/:117） |
| 迁移登记 | `internal/storage/sqlite/migrate_burned_versions_test.go:244-246` 三个版本号已登记，**append-only** |
| sqlc 查询 | `internal/storage/sqlite/queries/workflow.sql`（30 个 `-- name:`，297 行） |
| sqlc 配置 | `backend/sqlc.yaml`（schema = `migrations/`，out = `internal/storage/sqlite/gen`） |
| 生成代码 | `gen/workflow.sql.go`（30 个 `*Queries` 方法）、`gen/models.go:978-1005`（`Workflow`/`WorkflowAttempt` 结构） |
| 读写 Store | `store/workflow_store.go`：`CreateWorkflow`(19)、`GetWorkflowByIdempotencyKey`(164)、`GetWorkflow`(189)、`ListWorkflows`(201)、`GetWorkflowTask`(221)、`ListWorkflowTasks`(237)、`ListWorkflowRepositories`(258)、`ListWorkflowEdges`(271)、`ListWorkflowTaskRepositories`(286)、`ListWorkflowAttempts`(312)、`GetWorkflowAttempt`(331) |
| 生命周期 Store | `store/workflow_lifecycle_store.go`：`ActivateWorkflow`(17)、`ClaimWorkflowTask`(92)、`StartWorkflowAttempt`(189)、`SettleWorkflowAttempt`(348)、`ResolveUnknownWorkflowAttempt`(470)、`RetryWorkflowTask`(617)、`RecoverExpiredWorkflowAttempts`(694)；幂等重放读回 `loadIdempotentWorkflow`(718)/`loadIdempotentTask`(742)/`loadIdempotentAttempt`(766) |
| 单写者假设 | `store/store.go:14-25`（`qw` 单写连接 + `writeMu`）、`:80-90`（`conversationWriter`）、`:117-139`（`inTx`/`inTxDB`）；每个写方法取 `s.writeMu.Lock()`（如 `ClaimWorkflowTask` :119 取锁、:120 延迟解锁） |
| attempt_no 分配 | `queries/workflow.sql:160-163`（`MAX(attempt_no)+1`），依赖单写者（§3 第 5 项） |
| 关键迁移语义 | `StartWorkflowAttempt` 查询 `queries/workflow.sql:173-185`（置 `started_at`，**不**以 workflow active 为前置：drain 语义）；`SettleWorkflowAttempt` :187-195（接受 `state IN ('claimed','running')`，**不清 `started_at`**）；`MarkExpiredWorkflowAttemptsUnknown` :240-248；`BlockTasksWithUnknownWorkflowAttempts` :249-259；`CompleteWorkflowIfAllTasksSucceeded` :260-273；`FailWorkflowIfAnyTaskFailed` :275-284 |

### 2.4 Daemon 接线

`backend/internal/daemon/workflow_wiring.go`
- 常量：`workflowMaxTasks=50`(:20)、`workflowMaxDepth=12`(:21)、`workflowMaxFanOut=8`(:22)、`workflowLeaseDuration=15m`(:26)、`workflowLeaseSweepInterval=1m`(:27)。
- `newWorkflowService(store *sqlite.Store) *workflowsvc.Service`(:32)：`Clock`/`NewID`/`NewLeaseToken` 留空走默认。
- `startWorkflowLeaseRecovery(ctx, svc, log)`(:52)：启动时同步扫一次 + ticker 周期扫；失败只 `log.Warn`，不重派。
- 装配点：`daemon.go:300` `workflowSvc := newWorkflowService(store)`、`:301` `startWorkflowLeaseRecovery`、`:1111` `Workflows: workflowSvc`。

### 2.5 HTTP 控制器 / DTO / 路由注册 + 静态路由顺序风险

| 事实 | 位置 |
| --- | --- |
| 控制器接口 | `controllers/workflows.go:26-40` `WorkflowService`（13 个方法，**不含 `Resolve`**） |
| 注册 | `controllers/workflows.go:46-58`，共 **11** 条路由（list/create/recover-expired/definition/listTasks/task/activate/claim/start/settle/retry） |
| 挂载 | `httpd/api.go:235`（`workflows: &controllers.WorkflowsController{Svc: deps.Workflows}`）、`api.go:359`（`a.workflows.Register(r)`），位于 `root.Route("/api/v1", ...)`(:273) 内 |
| 依赖注入位 | `httpd/api.go:94-97` `Workflows controllers.WorkflowService`（nil → 全部 501） |
| 501 契约 | `apispec/apispec.go:124` `NotImplemented(w, r, method, path)`；由 `controllers/workflows_test.go:610-642` 锁定 11 条路由 |
| DTO | `controllers/dto.go:3977-4222`（到文件末尾），**17** 个 `Workflow*` 类型（`^type Workflow[A-Za-z]*` 匹配；若把 `ListWorkflowsQuery`、`Workflow*Param` 等也计入则为 29）；写请求的幂等键在 body：`CreateWorkflowRequest`(4055)/`ActivateWorkflowRequest`(4067)/`ClaimWorkflowTaskRequest`(4073)/`StartWorkflowAttemptRequest`(4081)/`SettleWorkflowAttemptRequest`(4088)/`RetryWorkflowTaskRequest`(4096)，字段 `IdempotencyKey json:"idempotencyKey"` |
| 租约不外泄契约 | `dto.go:4154-4156`：`WorkflowAttemptResponse` 注释明示不携带 lease token；`WorkflowAttempt.LeaseToken` 为 `json:"-"`（`domain/workflow.go:341`） |
| API spec 源 | `apispec/specgen/build.go:835-932` `workflowOperations()`（11 条 operation）、`:787` 挂入 `operations()`、`:327-333` `schemaNames` 映射 |
| 生成物 | `apispec/openapi.yaml`（24199 行）、`frontend/src/api/schema.ts`（25530 行）；漂移守卫 `specgen/build_test.go:71` `TestBuild_MatchesEmbedded`、`apispec/parity_test.go:40` `TestRouteSpecParity` |
| 中间件预算 | `httpd/api.go:270-334`：默认 `config.DefaultRequestTimeout = 60s`（`internal/config/config.go:37`）；workflow 路由不享受 setup/clone 延长 |

**静态路由顺序风险（实测结论，见 §4 #1）**：chi v5.1.0（`backend/go.mod:15`）按 node type 分树匹配，`ntStatic` 先于 `ntParam` 尝试（`chi/v5@v5.1.0/tree.go:77-83`、`:397-420`），因此 `/workflows/capabilities` 不会被 `/workflows/{workflowId}` 吞掉，**与注册顺序无关**。我用同版本 chi 写了最小复现验证：正向/反向注册两种顺序下 `GET /workflows/capabilities` 均命中静态 handler（退出码 0，临时目录已删除）。路由测试仍应保留，但它是回归守卫，不是规避顺序依赖。

### 2.6 CDC / change_log 触发器

- 6 个 trigger（`20261103073516664_workflow_cdc.sql`）：`workflows_cdc_insert`(:60)/`workflows_cdc_update`(:70)/`workflow_tasks_cdc_insert`(:81)/`workflow_tasks_cdc_update`(:91)/`workflow_attempts_cdc_insert`(:106)/`workflow_attempts_cdc_update`(:117)。
- attempt trigger 的 `WHEN OLD.state <> NEW.state` 只按 state 变化发事件，避免心跳刷屏（:100-118 注释）。
- payload 只含 `kind/workflowId/taskId/attemptId/attemptNo/state`（:109-114）——**不含 lease token / worker id**，由 `store/workflow_cdc_test.go:280-288` 断言。
- 事件类型：`internal/cdc/event.go:37-40`（`EventWorkflowCreated`/`Updated`/`TaskUpdated`/`AttemptUpdated`）；SSE 路由 `internal/httpd/events.go:39` `r.Get("/events", c.stream)`；poller `internal/cdc/poller.go:25-27`（`change_log` 为唯一事件源，事务回滚零事件）。
- 约束：`AGENTS.md`（"SQLite change events come from DB triggers into `change_log`；do not add parallel manual CDC emission"）。M2 只能新增 trigger。

### 2.7 Session Manager 入口与 `ports.SpawnConfig`

| 事实 | 位置 |
| --- | --- |
| Manager 正式入口 | `internal/session_manager/manager.go:989` `func (m *Manager) Spawn(ctx, cfg ports.SpawnConfig) (domain.SessionRecord, int, int, error)` |
| 幂等前置 | 同上 :993-1002：`cfg.ClientRequestID != ""` → `store.GetSessionByClientRequestID` → `replayClientRequest`（:1255 亦有二次查） |
| 冲突/未完成语义 | `manager.go:163-164`：`ErrClientRequestConflict` / `ErrClientRequestIncomplete`；解析在 :1616/:1619 |
| Session 服务入口 | `internal/service/session/service.go:356` `func (s *Service) Spawn(ctx, cfg ports.SpawnConfig) (domain.Session, int, int, error)`；先走 `replayClientRequest`(:2007，未导出) |
| 恢复入口 | `manager.go:3313` `ResumeAgentWithMode` |
| Async 分支 | `internal/session_manager/chat_spawn_async.go:39` `beginAsyncChatSpawn`（只发布 session row，worktree/controller 后台完成） |
| `SpawnConfig` 字段 | `internal/ports/session.go:18` 起：`ProjectID`(19)、`IssueID`(20)、`AutomationRunID`(23)、`ClientRequestID`(25)、`ClientRequestHash`(26)、`ParentSessionID`(30)、`CreatedBy`(35)、`TrackerProvider`(41)、`IssueContext`(44)、`Kind`(45)、`Harness`(46)、`Branch`(47)、`Prompt`(49)、`AgentConfig`(52)、`RequestedMode`(68)、`RequestedOutput`(72)、`DisplayName`(76)、`Attachments`(86)、`Async`(94)、`TaskPreparation`(98) |
| 备注 | `Async` 注释明确"仅 session row 与 conversation 已存在即返回"，呼应 m2-design §4.2 步骤 5。`RequestedMode` 非法值直接失败、不回落（:63-68）。 |

### 2.8 Chat 服务与 `ports.ChatSteerer`

| 事实 | 位置 |
| --- | --- |
| `SteerOrSend`（Service） | `internal/service/chat/steer.go:160`（要求 `ClientMessageID` 非空，否则 `ErrSteerDeliveryUncertain`） |
| `SteerOrSend`（Controller） | `steer.go:566`（同 `sendMu` 锁，`ClientMessageID` 去重 + payload hash 冲突 → `ErrClientMessageConflict`） |
| `Steer` | `steer.go:118`；`RecoverSteer` :140；`PromoteQueuedTurn` :189/:217 |
| 能力门禁 | `steer.go:210-213` `steerer()`：类型断言 `ports.ChatSteerer` **且** `Capabilities().Has(ChatCapabilitySteer)` |
| 队列实现 | `internal/service/chat/controller.go:1512` `shouldQueue := queueWhenBusy && (c.busy() || queuedAhead)` |
| Interrupt / Stop | `internal/service/chat/service.go:1624` `Interrupt`、`:1712` `Stop`；`controller.go:2547` `Interrupt` |
| 发送/审批 | `service.go:1343` `Send`、`:1570` `Resolve`、`:1598` `ResolveInput` |
| 能力集合 | `internal/ports/chat.go:167-237`（`ChatCapability string` 类型 167，**29** 个 `ChatCapability*` 常量，`ChatCapabilitySteer` :175）；`ChatCapabilities` map :240、`Has` :243 |
| 探针缓存 | `service.go:2404` `driverCapabilities`（`s.probed` map 按 harness 缓存一次，`:122`/`:242`） |
| Chat 可用性 | `service.go:2229` `SupportsChat`、`:2238` `PreflightChat`、`:2257` `PermissionModes`、`:2299` `ChatUnavailable` |
| `ChatSteerer` | `internal/ports/chat_steer.go:34-36` `Steer(ctx, providerTurnID string, msg ChatUserMessage) (ChatTurnRef, error)` |
| Steer 错误语义 | `chat_steer.go:41-59`：`ErrChatNoSteerableTurn`(51)、`ErrChatTurnNotSteerable`(55)、`ErrChatSteerContentUnsupported`(58) |
| 消息 DTO | `internal/ports/chat.go:457-496` `ChatUserMessage`（含 `ClientMessageID`(478)、`SenderSessionID`(466)、`Origin`(486)、`AuthoredByUser`(489)） |
| turn 状态 | 迁移 `0107_recovered_conversation_turns.sql:19` + `0118_cancelled_conversation_turns.sql:18`：`queued/running/completed/recovered/interrupted/failed/cancelled` |
| 源归属 | `internal/domain/conversation.go:115-123` `MessageOrigin`：`human/automation/daemon/provider` |
| HTTP 入口 | `controllers/conversations.go:132,136,137,138`（`messages`/`interrupt`/`steer`/`steer-or-send`）；spec `specgen/build.go:1817`；错误映射 `conversation_steer.go:189` `writeSteerError` |
| controller generation | `internal/domain/session.go:233-237` `ControllerGeneration`（控制器重启即轮转，旧 generation 事件被拒）；声明接口 `service/chat/controller.go:58`、`service/chat/service.go:284` |

### 2.9 chatdriver 包（codexappserver + 三家 ACP）

| Harness | 包 | 注册点 | 关键锚点 |
| --- | --- | --- | --- |
| Codex | `internal/adapters/chatdriver/codexappserver/` | `registry/registry.go:95` | 版本下限 `minimumCodexVersion = "0.146.0"` (`driver.go:36`)；`ChatCapabilitySteer: true` (`driver.go:160`)；`turn/start` (`conversation.go:398`)、`turn/interrupt` (`:707`)、`thread/resume` (`driver.go:425`)、`turn/steer`（`Steer` 方法声明 `steer.go:62`、JSON-RPC 调用 `codexproto.MethodTurnSteer` 在 `steer.go:102`） |
| Claude Code | `chatdriver/claudeacp/` + `chatdriver/acp/` | `registry/registry.go:97` | `minimumNodeMajor = 22` (`claudeacp/driver.go:34`)；运行时目录解析 `runtimeDirectoryBesideExecutable` (`:573`，候选 `:580-581` 逐"第一个存在目录")；`session/load` 分支 `acp/driver.go:565` |
| OMP | `chatdriver/ompacp/` + `nativeacp/` + `acp/` | `registry/registry.go:113` | `minimumOMPVersion = "15.0.0"` (`ompacp/version.go:16`)；`ChatCapabilityUsage/Diffs/Plans` (`ompacp/driver.go:29-31`，**无 steer 声明**)；审批固定于启动 `ompacp/driver.go:64-69` |
| DeepSeekRuntime | `chatdriver/deepseekharnessacp/` + `nativeacp/` + `acp/` | `registry/registry.go:119` | `profile acp` (`driver.go:22`、`:28-31`)；**未声明 usage / steer**（全文 0 处 `ChatCapabilityUsage`）；`PromptFailureCode` 仅识别 `-32603` 路由诊断 (`driver.go:41-56`，判定在 `:43`) |
| ACP 共同门控 | `chatdriver/acp/` | — | steer = `extensionSupported(init.Meta, "steering")` (`acp/driver.go:944-946`)；resume 能力按 `LoadSession` 降级 (`:938`、`:940`)；`nativeacp` 基线 caps `Streaming/Tools/Approvals/Interrupt/Resume` (`nativeacp/driver.go:83-89`)；版本探针可选 `cfg.VersionProbe` (`:104-108`)；auth 必需 `ErrChatAuthRequired` (`:116`) |

---

## 3. M1 继承 P2 风险清单（含当前状态实测）

分类定义：**已修** = 代码已消除该问题；**已测试** = 行为已被测试固定，但设计承诺未补；**保守阻断** = M2 不得进入该路径直到另行授权；**经用户批准延期** = m1-report/m1-acceptance 明确接受并移交。

| # | 风险 | 分类 | 当前状态证据（file:line） | M2 处置要求 |
| --- | --- | --- | --- | --- |
| 1 | 幂等重放返回"当前状态"而非原响应快照 | **经用户批准延期** | `20261103050931629_workflow_idempotency.sql:6-17` 只有 `result_type`/`result_id`，**无 response payload 列**；重放走 `workflow_lifecycle_store.go:718/742/766` 重新读库 | M2-04 不得把"读到当前状态"当作"原请求已被 Provider 执行"；外部副作用操作用稳定 receipt（m2-design §8） |
| 2 | `LeaseToken` 经 `%+v` 泄露到日志 | **已测试**（生产面收敛；残留仅测试失败输出） | 生产代码 `service/workflow`、`store/workflow_*.go`、`daemon/workflow_wiring.go`、`controllers/workflows.go` 中 `%+v` 计数 = **0**（grep 退出码 1）；DTO 排除 `dto.go:4154-4156` + `domain/workflow.go:341` `json:"-"`；CDC payload 排除并断言 `workflow_cdc_test.go:285-292`（`leaseToken` :287-289、`workerID` :290-292）；**残留**：`service_test.go:182,186` 对 `ClaimResult` 使用 `%+v`（含 token），仅测试日志 | M2-05/M2-08 的初始化/协作日志必须继续白名单化；不得对 `ClaimResult`、`WorkflowAttempt`、`SpawnConfig` 使用 `%+v` |
| 3 | `cancelled` Task 的 Workflow 完成语义未定义 | **保守阻断** | `CompleteWorkflowIfAllTasksSucceeded` 要求所有 task `state = 'succeeded'`（`queries/workflow.sql:260-273`），`cancelled` 会永久阻止完成；且**全仓无任何写 `cancelled` 的代码路径**（`domain/workflow.go:92` 只有常量与 `Valid()`；`grep WorkflowTaskStateCancelled` 在 store/service/httpd/migrations 中 0 命中） | M2 不得引入 `cancelled` 完成路径；先定义并加测试（m1-report §8.5） |
| 4 | `claimed → succeeded` 缺 `started_at` | **已测试**（行为已固定，语义未决） | `SettleWorkflowAttempt` 接受 `state IN ('claimed','running')`（`queries/workflow.sql:187-195`），而 `started_at` 仅由 `Start` 写入（`:178-180`）；`TestWorkflowSettleReplayWithNewKeyReportsCurrentOutcome`(`store/workflow_store_test.go:878-905`) 正是从 claimed 直接 settle | M2 严禁把 `attempts.started_at` 当作 Session/Provider 启动时刻；绑定必须写独立的 `bound_at`/观察事实（m2-design §4.2 步骤 4） |
| 5 | SQLite 单写者假设（`attempt_no`、租约排序） | **经用户批准延期** | 写路径统一单连接 `qw` + `writeMu`（`store/store.go:14-25`、`:80-90`、`:117-139`）；`ClaimWorkflowTask` 全程持锁（`workflow_lifecycle_store.go:119-120`）；`attempt_no = MAX+1`（`queries/workflow.sql:160-163`）；租约排序为 TEXT 比较，靠 UTC 归一化（`workflow_lifecycle_store.go:113-118` 注释） | M2 不得引入第二个写进程；若 M3 要并发度更高，必须先给显式序列（m1-report §8.8） |
| 6 | `handoff_note.go` 两条旧失败 | **经用户批准延期**（M0 B2，目标 M2） | 本轮实测：`go test ./internal/domain -run HandoffNote -count=1` **退出码 1**，2 条 FAIL：`handoff_note_test.go:84`（latest turn dropped）、`:211`（artifact cap）；两文件对 HEAD 无修改（`git diff --stat HEAD -- backend/internal/domain/handoff_note*.go` 输出空，退出码 0） | 作为既有基线对待，不计 M2 回归；修复须独立提交且不得掩盖（m0-adapter-gaps B2，:151/:269） |
| 7 | Codex Windows 账号诊断（`account_storage_unsafe`） | **保守阻断** | `validateCodexDirectoryAncestors` 存在：`service/agent/codex_account_catalog.go:622`、`codex_secure_file.go:80`、`codex_secure_fs_unix.go:72`；`account_storage_unsafe retryable=false` 生成点 `codex_account_reconcile.go:107`（同 :100-108 的 IO 故障分类）；M0 记为 B1（`m0-adapter-gaps.md:136`、待验证项 `:407`） | M2 只允许读脱敏 `reasonCode`；禁止读写真实 `.codex`、禁止改账号目录 ACL |
| 8 | Claude gateway 阻断 | **保守阻断** | `pkg/agentcreds/resolve.go:129-130`：`ANTHROPIC_BASE_URL` 非空即选 `ProviderGateway`；探测超时 `agentcreds.go:175` `DefaultTimeout = 3s`；M0 实测 `provider_unreachable`（`m0-capability-matrix.md` §1.1、§5） | 未经用户同意不得移除/替换 `ANTHROPIC_BASE_URL`、不得改 `~/.claude/settings.json`、不得打印 `ANTHROPIC_AUTH_TOKEN` |

---

## 4. 四 Agent 预检阻断与零 Provider 边界

### 4.1 已证明 vs 未证明

`m0-capability-matrix.md` §1（运行时矩阵）实测状态：

- **已证明（A-01，8 格）**：`CLI 路径/版本可信` ×4 —— Codex `0.154.0`（≥0.146.0）、Claude `2.1.303`（打包 Node `v22.23.2` ≥22）、OMP `omp/18.13.3`（≥15.0.0 且 ≥17.1.0）、DeepSeek `0.1.5-rc.1`（**无版本门禁**，只能证明可解析可运行）。`身份状态诊断` —— Codex `PASS(authorized)`、OMP `PASS(带标注, 返回 unknown)`、DeepSeek `PASS(authorized)`、**Claude `BLOCKED`**（gateway `127.0.0.1:15721` 无监听）。
- **未证明（52 格，`NOT_TESTED`）**：Chat 启动、TUI 启动、结构化事件/流式、Chat 发送/回复、Queue、Steer、Stop、Session 恢复、审批拒绝、Model/Effort、Usage/Cost、Worktree 边界、Windows 进程清理 —— **每个 Harness × 每个维度均无运行时证据**。A-02～A-15 一次都没执行（`m0-report.md:257-270`）。
- **结论**：M2 不能声明任何 Harness 具备 Steer/Resume/Queue/Usage 的真实能力；`static` 源码路径存在 ≠ 运行时 PASS（`m0-capability-matrix.md` §2 图例）。

### 4.2 M2 零 Provider 实施边界（硬边界）

1. **不启动** Codex / Claude Code / OMP / DeepSeekRuntime 的任何进程，不发送任何 real prompt，不消耗 token。
2. **不读写** 真实 `~/.ao`、`.codex/`、`.claude/`；测试只用 `t.TempDir()` / `sqlitetest` / 受控 fake host。
3. **不修改** 用户 Claude 配置或 `ANTHROPIC_BASE_URL`；不修复真实 Codex 账号目录。
4. **Fake 只能在 M2 自有边界（`ExecutionGateway` / Capability Resolver / Chat 假实现）落地**；禁止用 Fake 结果污染 M0 矩阵，禁止把 `Fake PASS` 写成任何 Harness 的运行时 `PASS`。
5. `m2-design.md` §10.2 的真实 Agent 端到端验收一律标 **`BLOCKED / NOT_TESTED`**，直到用户另行批准 M0-05 预算与隔离测试 Repo。
6. 不启动 M3（调度/预算）、M4（Gate/提交合并）、M5（UI）、M6（打包）。

### 4.3 实现阶段必须尊重的既有结构事实（不属于阻断，但约束设计）

- `domain.HarnessFake = "fake"`（`internal/domain/harness.go:48`）**不在 `AllHarnesses`（:53-62）**，因此 `IsKnown()`(`:64`) 对 fake 返回 false，`Service.Create` 会以 `INVALID_WORKFLOW_HARNESS` 拒绝（`service/workflow/service.go:240-241`）。M2 的 Fake 演示**不能**把 `agentHarness: "fake"` 写进 Workflow task 输入；应留空或使用真实 harness 名 + Fake Gateway。注意 `Create` 的校验顺序是 store-nil → 幂等键 → graph limits → title/goal → tasks → repositories → … → harness（`service/workflow/service.go:193-241`），harness 校验在 `GraphLimits` 校验**之后**：若该 `Service` 未配置 graph limits，即使传入 `agentHarness: "fake"` 也只会先拿到 `WORKFLOW_LIMITS_NOT_CONFIGURED` 而非 `INVALID_WORKFLOW_HARNESS`，因此不能以「返回了哪个错误码」反推 harness 是否被接受。
- `chatdriver/registry` 中**没有** fake chat driver（`grep HarnessFake registry.go` = 0 命中），所以 "两个不同 Harness 并行"（M2-08）只能在 Gateway 层伪造，不能依赖 Chat 层 fake driver。
- 现有 readiness 只暴露安装/认证（`domain/agent_readiness.go:267-278`），**不含 chat capabilities**；chat 能力缓存是按 harness 的一次性探针（`service/chat/service.go:122,242,2404-2419`），**无 TTL、无 per-session generation**。M2 Capability Resolver 不能直接复用该缓存作为"当前 Session 已证实的 Steer 能力"。

---

## 5. 文件归属与并发写约束

### 5.1 逐任务归属（M2-02～M2-08）

| 任务 | 独占（owner）新增/修改 | 只读依赖 |
| --- | --- | --- |
| M2-02 | `backend/internal/domain/workflow_capability.go`（新）、`backend/internal/service/capability/`（新包）、`internal/ports/capability.go`（新）、对应 `*_test.go` | `ports/chat.go`、`chat_steer.go`、四个 chatdriver 包、`domain/agent_readiness.go` |
| M2-03 | `internal/storage/sqlite/migrations/<新版本>_workflow_bindings.sql`、`migrate_burned_versions_test.go`（追加登记）、`internal/storage/sqlite/queries/workflow_binding.sql`（新）、`internal/storage/sqlite/store/workflow_binding_store.go`（新）、`store/*_test.go` | `queries/workflow.sql`、既有 3 个 workflow migration（**不可改**） |
| M2-04 | `internal/service/execution/`（新包：ExecutionGateway + Fake 实现）、`internal/ports/execution.go`（新）、`internal/daemon/execution_wiring.go`（新）、测试 | `session_manager` 的 `Spawn`、`ports.SpawnConfig`、`service/session`、`service/chat` |
| M2-05 | `internal/service/workflow/intervention*.go`（新）、`internal/service/execution/intervention*.go`、测试 | `service/chat/steer.go`、`ports/chat_steer.go`、`controllers/workflow_*`（只读，改动归 M2-07） |
| M2-06 | 与 M2-03 **同一批次**的 migration/queries（`workflow_messages`/`workflow_task_requests`/`workflow_interventions`）、`service/workflow/collaboration*.go`（新）、测试 | M2-03 的 store 层、M2-05 的干预回执 |
| M2-07 | `httpd/controllers/workflows*.go`（新 handler + Register 追加）、`controllers/dto.go`（workflow 段 3977+）、`apispec/specgen/build.go`（workflowOperations + schemaNames）、`httpd/api.go`（如需新依赖位）、路由/spec 测试 | M2-04～M2-06 的服务接口 |
| M2-08 | `docs/customization/m2-report.md`、`docs/customization/m2-plan.md`（状态回填）、e2e/fake 测试文件 | 全部 |

### 5.2 串行化点（必须单 Agent、单批次）

| 点 | 涉及文件 | 原因 |
| --- | --- | --- |
| **S1 — schema/sqlc** | `migrations/`（新文件）、`migrate_burned_versions_test.go`、`queries/*.sql`、`backend/sqlc.yaml`、`internal/storage/sqlite/gen/*` | 版本号 append-only（`AGENTS.md`：不得改已合并 migration；`migrate_burned_versions_test.go:18-22` 冻结清单）；`gen/` 由 `npm run sqlc` 一次性重生成，两个 Agent 同时写必然撕裂。M2-03 与 M2-06 的建表**合并成一批**，或 M2-06 严格排在 M2-03 生成完成之后。 |
| **S2 — API 生成物** | `httpd/controllers/dto.go`（workflow 段）、`apispec/specgen/build.go`、`apispec/openapi.yaml`、`frontend/src/api/schema.ts` | `npm run api` = `api:spec` + `api:ts`（`package.json:12-14`）；`openapi.yaml` 由 specgen 生成、`schema.ts` 由 yaml 生成（`openapi-typescript` 根 pin **7.4.4**，`package.json:17`；`frontend/package.json:68` 为 `^7.13.0`，见 §6 #6）。三者必须由同一 Agent 依次提交。 |
| **S3 — 共享只读边界** | `httpd/api.go`、`daemon/daemon.go` | M2-04/M2-07 都要加装配位；约定 M2-04 只新增 `daemon/execution_wiring.go` 并在 `daemon.go` 加 1 行，M2-07 才可改 `api.go`。同一时刻只允许一个 Owner。 |

### 5.3 只读、禁止触碰

- 禁止改：M1 三个 migration、`queries/workflow.sql` 中既有语句（只能新增文件）、`gen/*`（只能由生成器写）、`handoff_note*.go`、`.codex`/`.claude`、真实 `~/.ao`。
- 不要碰：`git commit/push/reset/clean/stash/restore/checkout --/rebase`。
- 当前 dirty 面（本文件首次写入**之前**实测）：**39** 项 = 35 个 `gen/*.go`（**内容与 HEAD 一致**，`git diff --quiet -- backend/internal/storage/sqlite/gen` 退出码 0，仅 CRLF 属性差异）+ `docs/customization/README.md`、`docs/customization/design.md`（真实内容改动，非本任务）+ 未跟踪 `m2-design.md`、`m2-agent-prompt.md`。收尾时实测 **41** 项：新增未跟踪 `m2-plan.md`（本任务产出）与 `m2-baseline-windows.md`（**并发写入的其他 Agent 产出，本任务未创建、未修改**）。**其余全部保持原样**。

---

## 6. 设计与代码偏差（编号发现）

逐条对照 m2-design.md V0.1 的断言与真实代码。**除下列 6 条外，其余被抽查的锚点（§1.1 表格 12 行、§2.1 接口字段、§3.2 四 Harness 风险、§4.1 `workflow_attempts.session_id` 可空、§5.1 三条通路、§7 路由与错误封装、§9 任务编号）均与代码一致。**

1. **§7 "新增 `/workflows/capabilities` 必须静态注册并以路由测试证明不被 `{workflowId}` 吞掉" —— 机制描述错误（风险实际不存在）。**
   证据：chi v5.1.0 按 node type 分树，`ntStatic` 优先于 `ntParam`（`chi/v5@v5.1.0/tree.go:77-83`、`findRoute` 遍历 `nn.children` 按 type 顺序 `:397-420`）。我用同版本最小程序验证：静态路由**先注册**与**后注册**两种顺序，`GET /workflows/capabilities` 都返回静态 handler（`/workflows/wf-1` 仍返回 param，退出码 0）。**影响**：M2-07 不需要为顺序做特殊安排，但必须保留路由测试作为回归守卫（并覆盖"静态路径恰为合法 workflowId 形态"的情况）。

2. **§1.1 把 `Service.Resolve` 列为 M1 已完成入口，但它在生产链路中无任何调用方。**
   证据：`service/workflow/service.go:655` 存在；`Service.Resolve` 的调用点仅在 `service_test.go:585,607,626,636,647`；`controllers/workflows.go:26-40` 的 `WorkflowService` 接口**不含** `Resolve`；`workflows.go:46-58` 只有 11 条路由，无 resolve；`specgen/build.go` 无对应 operation；`daemon/`、`cli/` 均无调用（grep 退出码 0，命中全为 chat 的 `Resolve`）。**影响**：m2-design §8 "Lease 过期 → M2 收集证据后通过 Resolve" 目前**没有可用的入口**；M2-07 必须新增该路由（或在 M2-04 内以受控服务调用），否则 UNKNOWN 状态在 HTTP 层面仍是死路。

3. **§2.1 "`FindSpawn` … 若既有 Session 层缺少按 `ClientRequestID` 查找的正式接口，应补充最小只读查询" —— 该前提成立，且缺口比表述更具体。**
   证据：store 层有 `session_store.go:39` `GetSessionByClientRequestID`，`session_manager.Store` 接口声明于 `manager.go:344`，`service/session` 的读接口声明于 `service.go:38`；但 `service/session` 里唯一使用它的是**未导出**的 `replayClientRequest`（`service.go:2007`），httpd 的 `SessionService` 接口（`controllers/sessions.go:87-102`）也不暴露它。**影响**：M2-04 需要在 `service/session` 增加一个导出的只读查询（或让 Gateway 依赖一个自带该方法的窄接口），不能"用进程列表/目录名猜"。

4. **§5.2 建议的投递状态词表与仓库既有 turn 状态不重合。**
   设计写 `REQUESTED → QUEUED → DELIVERED → APPLIED` + `REJECTED/CANCELLED/UNKNOWN`；仓库既有事实是 chat turn `queued/running/completed/recovered/interrupted/failed/cancelled`（迁移 `0107:19`、`0118:18`）与 `SteerOrSendConversationResponse.Outcome ∈ {steered, sent}` + `Duplicate bool`（`dto.go:2947-2954`）。**影响**：M2-05/M2-06 必须显式定义映射表（尤其 `DELIVERED`/`APPLIED` 对应哪些既有事实），不能假设两者可互换；`ErrChatNoSteerableTurn` 属"可预期拒绝"这一条与代码一致（`chat_steer.go:41-51`）。
   补充约束（已实测）：既有 turn 状态集合就是 `queued/running/completed/recovered/interrupted/failed/cancelled`（`0107:19`、`0118:18`），加上 `SteerOrSendConversationResponse.Outcome ∈ {steered, sent}`；设计里的 `DELIVERED`/`APPLIED` 在仓库中**没有任何对应事实**。故 M2-05 必须显式定义映射表（哪个既有 turn 状态/Outcome 对应 `DELIVERED`、哪个对应 `APPLIED`），不能假设两套词表可互换。

5. **§3.1 未提及现有 readiness 与 chat capability 两条独立事实来源，容易在 M2-02 重复造第二套权威。**
   证据：`domain/agent_readiness.go:267-277` 只有 installation/authentication/effectiveReadiness；chat 能力来自 `service/chat/service.go:2404` 的 `driverCapabilities`，按 harness 一次性缓存（`:122`/`:242`），**无 TTL、无 session generation、无失效入口**。**影响**：M2-02 的 `HarnessCapabilitySnapshot` 必须明确它是**新增的第三层**（其 TTL/来源/`observedSessionID` 语义不能与既有两套混用），并在文档中写明不复用 `s.probed` 的缓存值作为"本 Session 已证实"。

6. **`openapi-typescript` 版本在三处声明中不一致（M2-07 生成前置风险）。**
   证据：根 `package.json:17` pin `7.4.4`（`api:ts` 脚本 `:13` 用裸 `openapi-typescript`，实际取根 devDependency），而 `frontend/package.json:68` 声明 `^7.13.0`；此外 `packages/cloud-client/package.json:32` 同样 pin `7.4.4`。**影响**：即在三处声明中，`7.4.4` 是根与 cloud-client 共同钉住的唯一版本，`frontend/package.json` 的 `^7.13.0` 是**离群值**。M2-07 生成 `frontend/src/api/schema.ts` 时必须走**根**脚本（`npm run api`），不要走 `frontend` 的 `api:ts`，否则两个版本会产出不同字节并制造伪漂移。

附加说明（非设计错误，但 M2 必须知道）：`docs/architecture.md` 中 `workflow` 关键字出现 **0 次** —— M1 的 Workflow 核心尚未进入架构心智模型文档；按 `AGENTS.md` 的"改行为同步更新 docs"要求，M2 收口时应补一节（不属 M2-01 交付范围）。

---

## 7. M2-01 验证命令与退出码

在 `E:\code\agent-orchestrator` 与 `E:\code\agent-orchestrator\backend` 执行（本机 Windows 11，Go 默认 `GOTOOLCHAIN=auto` 取 `go 1.27.1`）：

| # | 命令 | 退出码 | 结果摘要 |
| --- | --- | --- | --- |
| 1 | `git rev-parse HEAD` | **0** | `a6c284a4d060431371306072e6c80abfedf22328` |
| 2 | `git status --short --branch` | **0** | `## feat/multi-agent-desktop`；执行前 39 项、收尾 41 项（35 `gen/*.go` CRLF-only + 2 docs 内容改动 + 4 未跟踪：本文件、并发的 `m2-baseline-windows.md`、`m2-design.md`、`m2-agent-prompt.md`），见 §5.3 |
| 3 | `git diff --check` | **0** | 无空白错误；仅有 LF→CRLF 提示（无副作用） |
| 4 | `git diff --quiet -- backend/internal/storage/sqlite/gen` | **0** | 确认 35 个生成文件**内容零差异** |
| 5 | `git diff --stat HEAD -- backend/internal/domain/handoff_note.go backend/internal/domain/handoff_note_test.go` | **0** | 输出为空：两条基线失败的文件未被改动 |
| 6 | `cd backend && go build ./...` | **0** | 全仓后端编译通过（10.56s） |
| 7 | `cd backend && go vet ./internal/service/workflow ./internal/storage/sqlite/store ./internal/cdc ./internal/httpd/... ./internal/daemon` | **0** | 目标包静态检查通过（2.47s） |
| 8 | `cd backend && go test ./internal/domain -run HandoffNote -count=1` | **1** | 2 条**既有**失败：`handoff_note_test.go:84`、`:211`（§3 第 6 项，属 M0 B2）。非 M2 引入 |
| 9 | 临时 `chi` 路由最小复现（`/tmp/chiroute`，已删除） | **0** | 静态/参数路由两种注册顺序均解析正确（§6 #1） |

未执行（按 M2-01 约束）：`npm run sqlc`、`npm run api`、`npm run frontend:typecheck`、`go test -race`、全量 `go test ./...`。原因：M2-01 不涉及 schema/API 变更，且任务书明确禁止在本轮运行生成命令。它们属于 M2-03/M2-07/M2-08 的门禁项。

---

## 8. M1 不变量（M2 全程必须保留）

1. `Claim/Start/Settle/Resolve/Retry` 是 Attempt 状态的唯一迁移入口；不得新增第二套 Task 状态。
2. 幂等键 + 64 位 `request_fingerprint` 语义不变；同一 key 换请求体必须 409。
3. UNKNOWN 只可通过 `Resolve`（task revision CAS）解除，且**永不**触发重派。
4. 租约操作全部 UTC 归一化；TEXT 比较排序的假设不变。
5. Lease token 不进入任何 GET / SSE / 日志；`json:"-"` 与 DTO 契约不得放宽。
6. 事件只经 `change_log` trigger → CDC → SSE；store 层不手写 CDC。
7. `StartWorkflowAttempt` 的 drain 语义（不以 workflow `active` 为前置）不得回退。
8. `POST /workflows/recover-expired` 与 daemon 周期扫描语义一致；不引入第三个恢复控制器。
9. `config.DefaultRequestTimeout`（60s）与 setup-budget 白名单不变；新路由默认走普通预算。
10. 生成物只能由 `npm run sqlc` / `npm run api` 产出；`gen/*` 永不手改。
