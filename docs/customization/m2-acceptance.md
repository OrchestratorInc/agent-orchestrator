# M2 独立代码与运行验收报告

> 验收日期：2026-11-04（Asia/Shanghai）
> 工作区：`E:\code\agent-orchestrator`
> 分支：`feat/multi-agent-desktop`
> M1 基线：`a6c284a4d060431371306072e6c80abfedf22328`
> 验收方式：独立源码追踪 + 定向单测/迁移测试 + 构建/vet/race + API 生成与前端类型检查。
> 安全边界：**0 真实 Provider 调用、0 Token、未启动真实 Agent、未读写真实 `~/.ao` / `.codex` / `.claude`、未 push / deploy。**

**M2 整体验收：NOT ACCEPTED**

**结论摘要：** M2 的 SQLite 持久化、CAS/幂等基础、Fake 离线 E2E、构建和多数目标测试有实际通过证据；但 M2 的关键产品路径仍未完成。仓库没有生产级 `ExecutionGateway`，Daemon 实际接入 FakeGateway；Steer 并未调用目标 turn 的 Steer 能力；HTTP Bind 参数不完整；权限与恢复路径存在缺口。因此只能认定为**离线编排骨架部分通过**，不能验收为可运行的多编码 Agent 编排 M2。

本报告独立于实施方的 [m2-report.md](m2-report.md)。实施方报告中的“离线开发门禁 PASS”不等同于本次整体功能验收通过。

---

## 1. 验收范围与方法

本次不以 `m2-plan.md`、`m2-report.md` 的 Done/PASS 自述作为证据，逐项核查：

- M2-01～M2-08 的真实 Go 实现、调用链、Daemon 装配、HTTP 路由、SQLite migration/query/store。
- 能力门禁、身份授权、generation fencing、Spawn 幂等、未知结果恢复、Stop/Steer 副作用边界。
- Fake E2E 是否真的经过生产 Daemon/HTTP 接线，还是仅手工装配 Service/Store。
- 实际运行 Go 测试、构建、vet、race、OpenAPI 生成、前端 typecheck。
- 不运行会启动真实 Agent/Provider 的全仓测试；M0-05 预算未批准，不做真实 Provider 验证。

## 2. M2-01～M2-08 独立验收矩阵

| 项目 | 独立结论 | 证据与判断 |
|---|---|---|
| M2-01 源码/接口/风险复核 | **PASS** | M2 对 M1 Workflow、Session、Chat、Worktree 的主要代码锚点已识别；但实现偏差见下方阻断项。 |
| M2-02 Capability Resolver | **PARTIAL** | Resolver、静态 profile、TTL、证据等级、FakeProbe 测试存在且通过；没有四 Harness 的生产 Probe/持久化 Recorder；Gate 未校验 AuthState/Readiness/Harness 一致性，Approval 的 advertised-only 映射也有错误。 |
| M2-03 Attempt–Session/Repo DB | **PASS（限定范围）** | binding/repo-binding migration、FK/UNIQUE/CAS、session deletion trigger、store 测试和迁移测试通过。尚不代表真实 Session Manager 集成通过。 |
| M2-04 ExecutionGateway | **FAIL** | `ports.ExecutionGateway` 只有 FakeGateway 实现；`daemon/execution_wiring.go` 在实际 Daemon 装配中使用 `execution.NewFakeGateway()`，没有 Session Manager + Chat Service 的生产组合实现。 |
| M2-05 Steer/Queue/Stop | **FAIL** | Queue/Stop 的 Fake 状态机有测试；Steer 实际落到普通 `Gateway.Send`，请求中没有 target provider turn/generation，不能证明在指定 turn 上执行了 Steer。Stop 也未完整 fencing binding 状态。 |
| M2-06 分级协作/任务申请 | **PARTIAL** | 消息与申请持久化、跨 Workflow FK/校验、payload hash 有实现；但 Attempt ID 和 Human decider 没有可信调用者身份约束，审批请求缺少可供 owner 查询的安全详情 API。 |
| M2-07 HTTP API/CDC/SSE | **FAIL** | 路由、DTO、OpenAPI、CDC migration 存在；但 Bind Handler 传入空 ProjectID，且新 binding 没有 TaskID；binding GET 未核对 URL WorkflowID；缺少 task-request 查询与 binding Reconcile 路由。 |
| M2-08 Fake 离线 E2E/异常测试 | **PASS（Fake only）** | 两 Harness Fake E2E、异常注入矩阵、零 Provider 测试通过；E2E 手工装配 Fake Gateway/Store，不经过实际 Daemon M2 wiring，也不覆盖真实 HTTP Bind 路径。 |

---

## 3. 阻断验收的代码问题

### B-01（P0）没有生产级 ExecutionGateway，Daemon 直接接入 Fake

证据：

- `backend/internal/ports/execution.go:216` 定义 ExecutionGateway 接口。
- 全仓具体实现搜索只找到 `backend/internal/service/execution/fake_gateway.go`。
- `backend/internal/daemon/execution_wiring.go:47`：`gateway := execution.NewFakeGateway()`。
- `newM2Services` 没有装配现有 Session Manager、Session Service 或 Chat Service；Resolver 也没有注册生产 Probe。
- `backend/internal/service/session/service.go` 已增加按 ClientRequestID 查找 Session 的能力，但它没有被组合成 ExecutionGateway。

**影响：** 当前 Daemon 只能展示静态能力并使用 Fake。它不能通过 M2 路径真正创建/恢复 AO Session，也不能把消息送入现有 Chat Service。即使 M0-05 预算未批准，也不妨碍编写并用 Fake 下层依赖测试 SessionManager/ChatService 组合适配器；预算只应阻止真实 Provider E2E，而不是省略生产 Gateway 代码。

### B-02（P0）Steer 被实现成普通 Send，未把目标 turn 传给执行层

证据：

- `backend/internal/service/intervention/coordinator.go:235` 的 `Steer` 最终进入 `performMessage`。
- `backend/internal/service/intervention/coordinator.go:533` 调用的是 `c.gateway.Send`。
- `backend/internal/ports/execution.go:162` 的 `MessageRequest` 没有 `TargetProviderTurnID`，也没有 `ControllerGeneration`。
- `ExecutionGateway` 接口没有 Steer 方法，也没有 ChatSteerer/SteerOrSend 的组合入口。
- `messageOutcome` 仅根据返回的 ChatTurnID/ProviderTurnID 等字段决定是否 applied；Fake 可以返回这些字段，但这不证明目标 turn 收到了 Steer。

**影响：** 当前代码无法保证“对正在执行的指定 Provider Turn 注入指令”。它可能只是向 Session 发送一条普通新消息，却记录成 Steer applied。这是语义错误，不是缺少真实 Provider 测试那么简单。

**修复门槛：** 复用现有 ChatSteerer / `service/chat` SteerOrSend；将 target turn 和 controller generation 传入；校验回执的 Session、ClientMessageID、目标 Provider Turn 与 generation 全部匹配后才能记为 applied。

### B-03（P1）HTTP Bind 路径必然缺少 ProjectID，新 Attempt 也缺少 TaskID

证据：

- `backend/internal/httpd/controllers/workflows.go:724` 的 `bind` Handler 只从已有 binding 读取 TaskID。
- 若尚无 binding（正常首次 Bind），TaskID 保持空字符串。
- Handler 在 `backend/internal/httpd/controllers/workflows.go` 中将 `ProjectID` 固定传为 `""`，Repositories 传 `nil`。
- `backend/internal/ports/execution.go:240` 起的 `SpawnRequest.Validate` 明确要求 WorkflowID、TaskID、AttemptID 和 ProjectID 非空。
- 当前路由表没有另一个可先创建 binding intent 的 API。

**影响：** 首次 HTTP Bind 会在参数校验阶段被拒绝；即使已有 binding，ProjectID 仍为空，仍然无法通过校验。M2 Service/Fake 单测通过并不能证明这个 HTTP 入口可用。

同一控制器的 `binding` GET（`workflows.go:697`）按 AttemptID 查询后没有验证返回的 `binding.WorkflowID` 是否等于 URL 中的 `workflowId`，可能跨 Workflow 读取 binding/repo 信息。必须补齐路径归属检查。

### B-04（P1）Capability Gate 没有把认证/Ready 状态纳入授权条件

证据：

- `backend/internal/service/capability/resolver.go:223` 的 `Gate` 检查 session/generation、证据等级、新鲜度和 feature state，但没有拒绝 `AuthState != authorized`、`Readiness != ready`，也没有校验返回 snapshot 的 Harness 与请求 Harness 相同。
- `backend/internal/domain/harness_capability.go:211` 的 `CapabilityApproval.State()` 把 `CapabilityApprovalAdvertisedOnly` 映射为 `CapabilitySupported`。这与类型注释“advertised 不等于 enforced”矛盾。
- `intervention.Coordinator.Stop` 没有调用 `CapabilityFeatureInterrupt` 门禁。
- Fake 测试均使用授权/ready 的正向快照，未覆盖上述拒绝分支。

**影响：** 将来接入真实 Probe 后，未经授权/未 Ready 的快照仍可能通过能力 Gate；仅宣称支持审批的 Harness 可能被当作真正执行了审批。必须先修 Gate 和负向测试，才能接真实 Provider。

### B-05（P1）协作 API 没有可信 Actor 身份，Human 审批可以被本机任意调用者冒充

证据：

- `backend/internal/service/collaboration/coordinator.go:204` 的 `ResolveCaller` 根据 HTTP 请求传入的 AttemptID 查 binding，然后直接把它当成发送者身份；没有验证请求者持有该 Attempt 的 Lease/Session 身份。
- `backend/internal/httpd/controllers/workflows.go:941` 的 createMessage 从请求体接收 AttemptID。
- `backend/internal/httpd/controllers/workflows.go:1012` 的 decide Handler 将每个调用都硬编码为 `WorkflowTaskRequestDeciderHuman`，`DecidedByRef` 也由请求体提供。
- `DeciderAttemptID` 没有从可信上下文传入。当前 loopback API 没有可供该 Handler 使用的用户/Agent 身份主体。
- `RegisterM2`（`workflows.go:658`）有 POST task-request/decide，但没有 GET task-requests 路由；返回 DTO 又隐藏 RequestJSON，没有单独的安全详情查询。

**影响：** 知道/取得另一个 AttemptID 的本地调用者可冒充该 Agent；任意本地调用者也可把自己声明成 Human decider。当前任务申请即使尚未 materialize，也不能把这种状态记录当作可信授权。Owner 也无法通过 API 查看待审批请求的安全摘要。

**修复门槛：** 引入可信 caller context（Lease/Attempt 绑定的认证主体或等价的受信机制）；Human decider 只能由可信本机 UI/用户主体提供；增加只返回经脱敏、可审查字段的 task-request 列表/详情 API。

### B-06（P1）Reconcile/Resume 有内部接口，但没有可用的恢复入口

证据：

- `AttemptBinder` 接口定义了 `ObserveSession`、`Reconcile`（`workflows.go:584` 附近），但 `RegisterM2` 没有对应 HTTP 路由。
- `POST .../resolve`（`workflows.go:887`）调用的是 M1 Workflow Attempt Resolve，不是 M2 Binding Reconcile。
- `daemon/daemon.go` 启动的是 M1 Workflow Lease Recovery；M2 wiring 明确没有 background recovery loop。
- `ExecutionGateway.Resume` 只有 Fake 实现，没有被 Coordinator 暴露为可调用的恢复流程。

**影响：** `spawn_unknown` / `reconcile_required` 可以被内部单测覆盖，但用户/后续 UI 无法通过 API 恢复它；Daemon 重启后也没有 M2 binding 恢复扫描。安全地“不盲目重试”是正确的，但必须同时提供受控的 Reconcile 入口。

### B-07（P1）Session observation 与 controller generation fencing 不完整

证据：

- `intervention.Coordinator.resolveTarget`（`coordinator.go:631`）和 `execution.Coordinator.LiveTarget`（`coordinator.go:668`）主要检查 readiness，没有校验 observation 返回的 SessionID、ControllerGeneration 与 binding 完全一致，也没有拒绝 `Terminated=true` 的矛盾 observation。
- `MessageRequest` 不携带 ControllerGeneration，发送前后的 controller ownership 变化无法由 Gateway 进行 fencing。
- Stop 只更新 intervention 记录；Intervention Coordinator 的 binding 依赖是只读 BindingReader，没有把 binding 状态推进到 stop_requested/stop_confirmed。确认 Stop 后 binding 仍可能保持 bound_ready，后续 Queue/Steer 仍可能通过 liveness 检查。

**影响：** 旧 generation/错误 Session 的观察可能被当作 live；Stop confirmed 后没有 durable binding 层面的执行冻结。

### B-08（P2）并发 Stop 的“检查未完成 Stop → 插入 Stop”不是原子操作

`Stop` 先调用 `CountOpenStopInterventions`，随后单独调用 `CreateWorkflowIntervention`；二者不在同一事务，migration 也没有“每个 Task 只能有一个 open stop”的部分唯一索引。两个不同 ClientMessageID 的并发请求可能都读到 open=0，然后各自插入并调用 Interrupt。现有测试只覆盖同一个 ClientMessageID 的顺序重试，未覆盖不同 ID 的并发 Stop。

---

## 4. 实际运行验证记录

执行目录：`E:\code\agent-orchestrator\backend`（除 npm 命令外）。

| 命令/验证 | 结果 | 说明 |
|---|---:|---|
| `go test ./internal/service/capability ./internal/service/execution ./internal/service/intervention ./internal/service/collaboration -count=1` | **0** | 四个 M2 Service 包通过 |
| `go test ./internal/storage/sqlite -run TestM2 -count=1` | **0** | M2 migration/rollback 相关定向测试通过 |
| `go test ./internal/storage/sqlite/store -run TestM2 -count=1` | **0** | 两 Harness Fake E2E、异常矩阵、零 Provider 测试通过 |
| `go test ./internal/httpd/controllers -run TestM2 -count=1` | **0** | M2 Controller 定向测试通过；未覆盖首次 Bind HTTP 路径 |
| `go test ./internal/daemon -run TestM2Wiring -count=1` | **0** | 只验证 Fake wiring 形状，不验证真实 Gateway |
| 目标包完整测试（含 domain/ports/M2 services/sqlite/store/httpd/controllers/cdc） | **1** | 5 条失败：2 条 Domain handoff、3 条 Controller Windows 测试；均与 `m2-baseline-windows.md` 记录的既有失败吻合，M2 新增 Service/Store/API Spec/CDC 包无新失败 |
| `go build ./...` | **0** | 全后端编译通过 |
| M2/集成相关 `go vet` | **0** | 无诊断 |
| `go test -race`：M2 Service 包 | **0** | capability/execution/intervention/collaboration 通过 |
| `go test -race ./internal/storage/sqlite ./internal/storage/sqlite/store -run TestM2 -count=1` | **0** | M2 migration 与 Fake E2E 定向 race 测试通过 |
| 全 Store 包 `go test -race` | **TIMEOUT** | 10 分钟后停在既有 `TestSessionTelemetryFactsRoundTrip` 的 migration/schema parse；不能记为 PASS，也没有证据表明是 M2 新 race |
| `npm run api` | **0** | OpenAPI spec generation + openapi-typescript 成功 |
| `npm run frontend:typecheck` | **0** | `tsgo --noEmit` 通过 |
| `npm run sqlc` | **BLOCKED** | `proxy.golang.org` 连接超时，sqlc v1.31.1 无法下载/启动；未能独立复核 sqlc 生成稳定性 |
| `git diff --check` | **0** | 无 whitespace/error |

**全仓 `go test ./...` 未在本次验收中执行**：该命令会进入与 M2 无关的真实 Harness/系统环境测试；当前约束是不启动真实 Agent，且 Windows 全仓既有失败基线已有 241/267 条失败记录。本次采用覆盖 M2 变更包的定向测试，不把全仓测试未执行伪装成通过。

## 5. Fake E2E 证据的准确边界

通过的 `TestM2OfflineEndToEndTwoHarnesses` / `TestM2AnomalyInjectionMatrix` / `TestM2ZeroProviderGuarantees` 证明：

- SQLite 迁移、binding/store、Fake ExecutionGateway、Fake capability probe、intervention/collaboration 在受控测试装配下可以协作。
- 两种 Harness 标识可在 Fake worker 下并行走完离线工作流。
- 测试把 HOME/USERPROFILE/XDG 目录重定向到临时目录，并验证未产生 Provider 配置/凭据文件。
- 未调用真实 Provider。

它们**不能证明**：

- Daemon 的 `newM2Services` 可以创建真实 Session（该 wiring 明确使用 FakeGateway）。
- HTTP 首次 Bind 可以通过（Handler 的 ProjectID/TaskID 参数缺失）。
- Steer 确实命中指定 Provider Turn。
- 真实 Codex/Claude/OMP/DeepSeek Harness 的协议、权限、Resume/Steer/Interrupt 能力已验证。
- task-request Human approval 已具备可信身份认证。

## 6. 最终结论与重新申请验收的门槛

**M2 整体不通过，暂不进入 M3。** 重新验收至少满足：

1. 提供真实 SessionManager + ChatService 的生产 ExecutionGateway；FakeGateway 只用于测试；保留零真实 Provider 的 mock/fake 适配器测试。
2. Steer 使用真正的 ChatSteerer/SteerOrSend，携带 target provider turn + controller generation，并对回执做精确匹配。
3. 修复 HTTP Bind：从可信 Workflow Task/Repo 事实取得 TaskID、ProjectID、repo refs；首次 Bind 不依赖“先有 binding”；GET binding 必须校验 workflowId。
4. Capability Gate 必须同时检查 Harness 匹配、AuthState、Readiness、证据级别、TTL、SessionID 和 generation；advertised_only 不得当作 enforced；Stop 也必须有明确 interrupt 策略。
5. 建立可信 Agent/Human caller 身份；AttemptID/DecidedByRef 不能作为身份凭证；增加安全的 task-request list/detail API。
6. 暴露受控 Reconcile/Observe/Resume 路径，并在 Daemon 重启后执行有界、不会盲目重 Spawn 的 binding recovery。
7. 对 SessionObservation 校验 SessionID/generation/Terminated；发送路径携带 generation；Stop confirmed 后推进 binding 状态并阻止后续旧任务操作。
8. 将 open Stop 的检查与创建放入同一事务或用唯一约束保证并发不同 key 也只能有一个 interrupt。
9. 网络恢复后重跑 `npm run sqlc` 并核对生成物；保留 M0-05 Provider E2E 为单独审批项，不以 Fake 测试冒充真实 Provider 验收。

---

## 7. Git 与副作用

- 当前 HEAD 仍为 M1：`a6c284a4d060431371306072e6c80abfedf22328`。
- M2 代码、迁移、生成物、M2 实施报告均处于本地未提交工作树；本次未创建 commit、未 push。
- 本次执行 `npm run api` / typecheck 生成和检查 API 文件；未执行会写入 sqlc 生成目录的命令（sqlc 在网络下载阶段失败）。
- 未改动 M2 业务源码；本次验收新增的文档只有本报告及状态索引同步。
- 未调用真实 Provider、未消耗 Token、未修改真实用户配置、未部署。

**本报告为独立验收结论；实施方 `m2-report.md` 保留为实施记录，不覆盖本报告。**
