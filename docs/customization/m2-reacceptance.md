# M2 修复后独立复验报告

> 复验日期：2026-11-04（Asia/Shanghai）
> 工作区：`E:\code\agent-orchestrator`
> 分支：`feat/multi-agent-desktop`
> Git HEAD：`a6c284a4d060431371306072e6c80abfedf22328`（M1）
> 复验依据：源码、Daemon 装配、Session/Chat 调用链、HTTP Handler、SQLite migrations/stores、真实执行命令与 race 测试。
> 安全边界：0 个真实 Provider turn、0 Token；未启动真实 Codex/Claude/OMP/DeepSeek Agent；未读写真实 `~/.ao` / `.codex` / `.claude`；未 commit、push 或 deploy。

**最终结论：M2 整体 NOT ACCEPTED，不准入 M3。**

本次确认上一轮的部分阻断已经得到真实代码修复：生产 `LocalGateway` 已存在并接入 Daemon；Steer 已使用目标 Provider Turn 的 pinned 路径；首次 Bind/Workflow 归属校验、Reconcile/Resume 路由、Daemon Recovery、Stop 原子创建等均有实现和定向测试。

但复验又发现多项仍影响真实产品能力的 P1 问题：普通 Send/Interrupt 没有 generation fencing；多仓库 RepoRefs 没有传入 Session Manager；实际 Harness Agent 没有可调用协作 API 的 MCP/CLI 工具与 lease 注入路径；Human 审批身份仅由 loopback IP 推断；Capability Probe 的 session 证据存在 fail-open/误标。**因此“Fake E2E 17/17 + build/vet 通过”仍不足以证明 M2 的真实多 Agent 闭环完成。**

---

## 1. M2-01～M2-08 复验矩阵

| 项目 | 本次结论 | 复验判断 |
|---|---|---|
| M2-01 源码/接口/风险复核 | **PASS** | 复核了实际代码和调用链；不是功能通过声明。 |
| M2-02 Capability Resolver | **PARTIAL** | AuthState/Readiness/Approval Gate 已修；session generation 证据、能力合并和证据等级仍有问题。 |
| M2-03 Attempt–Session/Repo 持久化 | **PARTIAL** | SQLite migration、Store、CAS 测试通过；RepoRefs 没有进入 Session Manager 的工作区创建路径，多仓库执行未完成。 |
| M2-04 ExecutionGateway | **PARTIAL** | 生产 LocalGateway 已实现并接入 Daemon；但集成 E2E 使用 Fake SessionSpawner，没有通过真实 Session Manager + Fake launcher 验证完整 Spawn/Worktree 路径。 |
| M2-05 Steer/Queue/Stop | **PARTIAL** | SteerPinned 目标 Turn 路径通过；普通 Send 和 Interrupt 仍缺 generation fencing，旧 generation 可能影响新 controller。 |
| M2-06 Agent 协作/任务申请 | **FAIL** | HTTP API 和 Principal 有实现，但真实 Harness 没有 workflow MCP/CLI 工具；lease 与 capability 没有传到 agent 可调用的工具上下文。 |
| M2-07 HTTP DTO/API/CDC/SSE | **PARTIAL** | M2 定向路由测试、OpenAPI 生成和前端类型检查通过；继承的 attempt resolve 写接口没有 Principal/Operator 授权检查。 |
| M2-08 Fake E2E/异常/安全测试 | **PASS（Fake only）** | 17/17 M2 HTTP E2E、race 定向测试通过；测试仍使用 Fake SessionSpawner，不验证真实 Git Worktree 和多仓库挂载。 |

**上一轮 B-01～B-08 对照：**

| 上轮问题 | 本次判断 |
|---|---|
| B-01 生产 ExecutionGateway 缺失 | **代码层已修**：LocalGateway 已接 Session/Chat，并接入 Daemon；真实 SessionManager 端到端仍缺验证 |
| B-02 Steer 伪装普通 Send | **已修**：SteerPinned + target turn + generation + receipt 匹配 |
| B-03 HTTP Bind 缺 TaskID/ProjectID | **已修**：ResolveAttemptOwnership 提供 Task/Project/Repo 归属 |
| B-04 Capability Gate 授权不完整 | **部分修复**：Gate 检查已增强，但 Probe 本身仍有 generation 和能力合并缺陷 |
| B-05 Agent/Human 身份认证 | **部分修复**：API 需要 capability + lease；实际 agent 工具与凭据传递缺失，loopback operator 不是人类身份认证 |
| B-06 Reconcile/Resume/Daemon Recovery 缺失 | **代码层已修**：路由、恢复扫描和 Fake 测试已存在 |
| B-07 Generation Fencing 不完整 | **仍未修完**：Steer 有 fencing，Send/Interrupt 没有 |
| B-08 并发 Stop 原子性 | **已修**：事务/原子 Store API 与 race 测试通过 |

---

## 2. 本次发现的阻断项

### R-01（P1）普通 Send 和 Interrupt 忽略 ControllerGeneration

证据：

- `backend/internal/ports/execution.go: MessageRequest` 定义了 `ControllerGeneration`。
- `backend/internal/service/execution/local_gateway.go:475` 的 `Send` 通过 `requireReady` 检查 Session 是否 Ready，但没有比较 `request.ControllerGeneration` 与当前 observation 的 generation；随后调用 Chat Service 普通 Send。
- `backend/internal/service/execution/local_gateway.go:613` 的 `Interrupt` 同样没有比较 `request.ControllerGeneration`；Chat Service 的 Interrupt 接口只按 SessionID 取当前 controller，没有 pinned generation 参数。
- `backend/internal/ports/execution.go:304` 的 `SessionObservation.ValidateFence` 只有在 observation generation 和 expected generation 都非空时才比较；expected 非空而 observation 为空仍可能通过。
- `intervention.Coordinator.messageOutcome` 对 Send 只检查 turn/queued 事实，没有像 Steer 一样验证 receipt generation 与请求 generation 一致。

**影响：** 在 `resolveTarget` 检查后、实际 Send/Interrupt 前发生 controller generation 切换时，旧 Attempt 的 Queue/Stop 可能作用于新 controller。Steer 的 pinned 路径不能替代普通 Send/Interrupt 的 fencing。

**修复门槛：**

1. 增加 generation-pinned Chat Send 和 Interrupt 接口；在 Chat Service 内部取得 controller 后原子校验 generation，再执行副作用。
2. Gateway 必须拒绝缺失/不匹配 generation。
3. Send receipt 必须与请求 generation 精确匹配；不匹配不得记为 applied/delivered。
4. Stop receipt 必须证明 Interrupt 命中了指定 generation。
5. 增加 generation 切换并发测试：ResolveTarget 后替换 controller，旧 Send/Stop 必须在 provider 调用前被拒绝。

### R-02（P1）RepoRefs 被持久化，但没有真正挂载到 Session 工作区

证据：

- `backend/internal/ports/execution.go:166` 的 `SpawnRequest` 有 `Repos []WorkflowRepoKeyRef`。
- `backend/internal/service/execution/local_gateway.go:358` 构造 `ports.SpawnConfig` 时只传入一个 `ProjectID`、Harness、Prompt、ClientRequestID 等字段，没有消费 `request.Repos`。
- `backend/internal/ports/session.go:18` 的 Session Manager `SpawnConfig` 只有单个 `ProjectID`，没有多 Repo 列表或多 Worktree 规划输入。
- `execution.ResolveAttemptOwnership` 可以为跨多个 Project 的 Repo 计算绑定，并确定性选出一个 ProjectID；但 LocalGateway 只把这个单一 ProjectID 交给 Session Manager。
- `backend/internal/integration/m2_e2e_test.go` 的 Fake SessionSpawner 明确说明只创建一个已有目录作为 Workspace，测试不执行 Git 操作，也不创建多 Repo Worktree。

**影响：** M2 的 Repo Binding 数据可以保存多仓库事实，但实际 Agent Session 没有收到全部 RepoRefs。对于多 Project/多 Repo Task，不能证明每个仓库都在 Agent 的本地工作区中可见、可写、可恢复。这不满足需求记录 Q6 的“首版多仓库、本地执行”。

**修复门槛：**

1. 扩展 Session Manager 的受信任 SpawnConfig/Workspace Provisioning 输入，接收完整 Repo 列表及每个 Repo 的 ProjectID/BaseSHA。
2. 在同一个 Attempt 的 Workspace 中为每个 Repo 建立独立 Worktree/路径引用，不以第一个 ProjectID 代替整个 Repo 集合。
3. 每个 RepoBinding 必须在实际 Worktree 创建、路径归属和 HEAD SHA 核验后才进入 ready；缺一项不得把 Attempt 标为 bound_ready。
4. 增加至少两个不同 Project 的 Repo 同时 Spawn、恢复、Stop 后保留、Daemon 重启后核对的 Fake Git/本地临时仓库测试。

### R-03（P1）Agent 协作 API 没有真实 Harness 可用的调用通道和凭据上下文

证据：

- Collaboration HTTP Handler 要求 Agent 提供 capability、AttemptID 和 lease 三类身份事实。
- `backend/internal/cli/mcp.go:17-18,123-134` 说明 AO 传给每个 Chat Session 的 `ao mcp` 只暴露 `html_preview`、`html_render` 和 Computer Use 工具；没有 Workflow Message / Task Request 工具。
- `backend/internal/session_manager/chat_spawn.go:365-366` 给 MCP Server 的环境白名单只有 `EnvSessionID`、`EnvRunFile`、`EnvDataDir`，明确不传递 `AO_BROWSER_CAPABILITY` 等凭据，也没有 Attempt lease。
- `ports.SpawnRequest` / `ports.SpawnConfig` 没有向 Session Manager/MCP Server 传递 Agent workflow context 或 lease 的字段。
- `backend/internal/integration/m2_e2e_test.go:1058-1064` 的协作测试手工构造 `X-AO-Agent-Capability`、`X-AO-Attempt-Id`、`X-AO-Attempt-Lease`，并非由真实 Spawn 后的 Harness/MCP 工具获得。
- `ao mcp` 注释说明它在 shell sandbox 外运行，能访问 loopback daemon；但目前没有对应的 M2 Workflow 工具。

**影响：** HTTP API 可以被测试代码调用，但 AO 实际启动的 Codex/Claude/OMP/DeepSeek Agent 没有受支持的工具来发送协作消息、请求任务或读取任务请求结果。Fake E2E 手工注入 Header 证明了服务端校验逻辑，不证明 Agent 产品链可用。

**修复门槛：**

1. 增加真正的 Agent MCP 工具，例如 Workflow Message、Task Request、Task Request Status；不能让 Agent shell 绕过受控边界直接访问数据库。
2. MCP Server 必须获得受限的 session-scoped Agent Capability 和对应 Attempt Lease 的受信上下文；不要把 lease 明文塞入 Prompt、日志或普通 API 响应。
3. 工具调用时由服务端从 session/attempt 绑定推导身份，不允许模型自由填写 sender AttemptID。
4. 增加从 Fake Harness 的 MCP `tools/list` / `tools/call` 开始的端到端测试，验证 Agent A 向 Agent B 发消息、提交 Task Request、读取审批结果；测试不得手工在 HTTP 测试中注入本应由生产工具提供的 Header。

### R-04（P1）Operator 身份不是 Human 身份；Attempt Resolve 写接口没有 Principal 门禁

证据：

- `backend/internal/service/principal/principal.go:134-147` 的 `LoopbackOperatorTrust` 仅凭 `RemoteAddr` 是否为 loopback 就返回 Operator。
- Loopback 地址只能证明请求来自本机网络栈，不能证明请求来自桌面 UI/真实用户进程。项目的 `ao mcp` 本身就是一个在 shell sandbox 外运行、可以访问 loopback daemon 的子进程。
- `backend/internal/httpd/controllers/workflows.go:1385` 的 `resolveAttempt` 直接调用 `WorkflowService.Resolve`，没有调用 `c.caller` 或其他 Operator 授权校验；注释却将它描述为“operator's decided outcome”。
- Task Request Decide 使用了 Principal，但不能弥补 Resolve 这个继承写入口的缺失。

**影响：** 目前代码证明的是“loopback caller 被视为本机 Operator”，不是“只有真实 Human 能审批”。此外，Resolve 可改变 Attempt 最终状态，却没有统一的 Operator Principal 门禁。若安全目标是防止 Agent/本地 Worker 自主批准或自行结算任务，这个边界还不完整。

**修复门槛：**

1. 对 Resolve 增加明确的 Operator/Human 授权检查和审计记录；拒绝 Agent Principal。
2. 明确本地信任模型：若决定信任所有本机进程，必须把它写成产品安全假设，不能声称 loopback 本身证明 Human 身份。
3. 若要求区分桌面用户与 Agent 子进程，应设计独立可信 UI 通道/进程隔离/OS 级授权；不要简单把 loopback IP 当作进程身份。
4. 增加无凭据、Agent capability、无效 lease、合法 Operator 四类请求的 Resolve/Decide 测试。

### R-05（P1）Capability Probe 的 session generation 和 feature evidence 存在 fail-open

证据：

- `backend/internal/service/capability/probe.go:82` 初始化时将 `snapshot.Generation = request.Generation`，这只是请求方声称的 generation，不是实际观察结果。
- `applyLiveController`（约 :172 起）调用 `ChatCapabilities` 并使用它返回的 live 标记；随后调用 `SessionGeneration`，但没有把其 `live=false` 当作拒绝条件。若未取得实际 generation，之前从请求复制的 generation 仍留在 snapshot。
- `probe.go:191` 只因 Controller 正在运行并报告能力，就把 Source 提升为 `confirmed_runtime`；但 Domain 注释将 `confirmed_runtime` 定义为“观察到 Provider 在本 Session 实际执行过该操作”，Controller 报告已协商能力最多是 handshake/session negotiation 证据。
- `applyAdvertised`（约 :202 起）对 absent capability 保留 snapshot 原值；当全局 Driver Probe 宣称 Steer=true，而当前 Session Controller 没有报告 Steer 时，旧的 supported 值不会被清除。注释声称“narrows”，实际逻辑并不保证缩窄。
- Steer Handler 在 `backend/internal/httpd/controllers/workflows.go:1328` 将请求体的 `req.Harness` 传入 Coordinator，而不是从 Attempt Task/实际 Session Observation 推导 Harness。

**影响：** session-scoped Gate 可能把调用者提供的 generation 当作已观察 generation；也可能把 Harness 级广告误用于没有协商该能力的具体 Session；证据等级会错误显示为 confirmed_runtime。Steer 的底层 pinned Chat Service 能挡住部分错误执行，但上层 Capability Gate 的结论和 UI/审计仍不可信。

**修复门槛：**

1. session-scoped Probe 必须同时证明 SessionGeneration 的 live=true、实际 generation 非空且精确匹配；不得把 request.Generation 回填为 observation。
2. Session 能力必须以当前 Controller 的实际能力集合替换/约束 Harness 级能力；缺失项不得沿用旧的 supported。
3. negotiated capability 使用 provider_handshake/session-negotiated 证据；只有成功的实际操作回执才记录为 confirmed_runtime。
4. Steer Harness 从 Attempt Ownership 或可信 Session Observation 推导，拒绝客户端伪造 Harness。
5. 增加全局 Steer=true、Session Steer 缺失；SessionGeneration=false；generation 空值；Harness 不匹配等负向测试。

### R-06（P2）Resume 对空 ClientRequestID 未 fail-closed

`backend/internal/service/execution/local_gateway.go:694` 使用：

`if rec.ClientRequestID != "" && rec.ClientRequestID != request.SpawnKey`

当 Session 记录的 ClientRequestID 为空时，SpawnKey 不匹配检查被跳过。对于由 M2 Spawn 创建并被 Binding 引用的 Session，必须要求 `rec.ClientRequestID == request.SpawnKey`，否则拒绝 Resume。增加缺失 ClientRequestID、不同 SpawnKey、相同 SpawnKey 三个测试。

### R-07（P2）Fake E2E 没有验证真实 Session Manager / Git Worktree 组合

`backend/internal/integration/m2_e2e_test.go:1-100` 明确使用 Fake SessionSpawner；它创建 Session Row、启动 Scripted Chat Controller，但不执行 Git 操作。Daemon Wiring 测试验证 LocalGateway 类型/装配约束，但没有用真实 Session Manager + Fake Runtime/Chat Launcher 跑完整 Spawn → Worktree → Chat → Resume 路径。

这不否定 LocalGateway 代码已经接入真实 Manager，但目前缺少不消耗 Provider Token 的真实 Session Manager 集成证据。M0-05 预算未批准不妨碍用 Fake Launcher 验证 Session Manager 本身。

### R-08（P2）Task Request 批准尚未形成可执行 Task

当前 Decide 可以记录审批结果，但当 Workflow Task 创建/依赖修改能力尚未提供时，批准不会 materialize 为新的 Task，Handler 返回未实现/未 materialized 状态。这可以作为 M3 Planner 的边界，但必须在 UI/API 中明确“已批准但尚未创建任务”，不能让用户误以为调度已经发生。若 M2 的验收定义要求批准后创建 Task，则该项仍未完成。

---

## 3. 实际测试与构建结果

执行环境：Windows；Go 1.27.1（自动工具链）；Node 22.20.0。

| 命令/验证 | 结果 | 说明 |
|---|---:|---|
| `go test ./internal/service/capability ./internal/service/execution ./internal/service/intervention ./internal/service/collaboration ./internal/service/principal -count=1` | **0** | M2 Service/Principal 包通过 |
| `go test ./internal/daemon -run TestM2 -count=1` | **0** | M2 Daemon wiring 定向测试通过 |
| `go test ./internal/httpd/controllers -run TestM2 -count=1` | **0** | M2 HTTP Handler 定向测试通过 |
| `go test ./internal/integration -run TestM2 -count=1 -v` | **0** | M2 Fake HTTP E2E 17/17 通过 |
| `go test ./internal/storage/sqlite/store -count=1` | **0** | 完整 Store 包通过 |
| `go test ./internal/storage/sqlite -count=1` | **0** | 完整 SQLite 包通过；第一次与其他检查并发时曾遇到内存不足，GOMAXPROCS=4 串行复跑通过 |
| `go build ./...` | **0** | 后端全量构建通过 |
| `go vet ./...` | **0** | 后端全量 Vet 通过 |
| `go test -race`：M2 Service/Principal 包 | **0** | 串行复跑通过；第一次并发运行时 TSan 报内存分配失败 |
| `go test -race ./internal/integration -run TestM2 -count=1` | **0** | M2 Fake E2E race 通过；第一次与前端 typecheck 并发时 OOM，串行复跑通过 |
| `go test -race ./internal/httpd/controllers -run TestM2 -count=1` | **0** | M2 Controller race 通过 |
| `go test -race ./internal/storage/sqlite -run TestM2 -count=1` | **0** | M2 Migration race 通过 |
| `go test -race ./internal/storage/sqlite/store -run TestCreateWorkflowInterventionIfNoOpenStopIsAtomic -count=1` | **0** | 并发 Stop 原子性 race 测试通过 |
| `npm run api` | **0** | OpenAPI spec + TypeScript schema 生成成功 |
| `npm run frontend:typecheck` | **0** | 串行复跑通过 |
| `npm run sqlc` | **BLOCKED** | `proxy.golang.org` 连接超时；`GOPROXY=off` 也因 sqlc module lookup 缺失而不能运行，未能独立验证 sqlc 生成稳定性 |
| `go test ./internal/httpd/controllers -count=1` | **1** | 3 条 Windows 既有失败：ListDirs 路径错误码、Mobile secure pairing rename Access denied、Projects Clone URL；M2 定向 Controller 测试通过 |
| `go test ./internal/daemon -count=1` | **1** | 2 条 Windows 既有失败：TempDir cleanup file-in-use、scratch session project credential rejected；M2 定向 Daemon 测试通过 |

**未执行** `go test ./...` 和完整 Integration 包：全仓包含与本次零 Provider 验收不相关、可能进入真实 Harness/本机凭据路径的测试；本轮只运行 M2 变更相关包及安全的 Fake E2E。真实 Codex/Claude/OMP/DeepSeek Provider 行为仍为 `NOT_TESTED`，M0-05 预算未批准。

---

## 4. 最终状态与重新申请验收条件

**M2 当前仍 NOT ACCEPTED，M3 不得启动。**

重新申请 M2 独立验收前，至少完成：

1. R-01：Send/Interrupt generation-pinned；receipt 与请求 generation 精确匹配。
2. R-02：RepoRefs 真实进入 Session Manager，所有 Repo Worktree 均创建、绑定、恢复。
3. R-03：为真实 Harness Agent 提供 workflow MCP 工具及安全的 capability/lease 上下文；从 MCP 工具入口做 Fake E2E。
4. R-04：Resolve/Decide 的 Human/Operator 权限边界明确并测试；loopback 只能证明本机，不得伪称 Human 身份。
5. R-05：Capability Probe 必须以真实观察 generation 和 Session Controller 能力作门禁，修复误标 confirmed_runtime 与旧能力残留。
6. R-06：Resume 的 SpawnKey 严格匹配。
7. R-07：用真实 Session Manager + Fake Runtime/Chat Launcher 验证 Spawn/Worktree/Resume；不启动真实 Provider。
8. R-08：明确 Task Request “approved but not materialized”的 M2/M3 边界，或实现安全的 Task materialization。
9. 网络恢复后运行 `npm run sqlc`，确认生成物稳定；保留真实 Provider 验证为 M0-05 单独审批项。

---

## 5. Git 与副作用

- HEAD 仍为 M1：`a6c284a4d060431371306072e6c80abfedf22328`。
- M2 代码、迁移、生成文件、实施报告和本次复验报告均在本地未提交工作区；暂存区为空。
- `git diff --check` 退出码 **0**。
- 本次没有修改 M2 业务源码；只新增本报告并同步文档状态。
- `npm run api` 成功生成 OpenAPI 与 TypeScript schema；`npm run sqlc` 因网络阻断未生成。
- 没有调用真实 Provider、没有 Token 消耗、没有读取或修改真实 Provider 凭据、没有 push/deploy。

本报告是第二轮独立复验的最终证据；`m2-remediation-report.md` 保留为实施方报告，`m2-acceptance.md` 保留为首轮历史验收，不相互覆盖。
