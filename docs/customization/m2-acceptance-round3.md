# M2 第三轮独立代码与运行验收

> 复验日期：2026-11-04（UTC+08）
> 仓库：`E:\code\agent-orchestrator`；分支：`feat/multi-agent-desktop`
> Git HEAD：`a6c284a4d060431371306072e6c80abfedf22328`（M1）
> 实施方记录：[m2-remediation-round3.md](m2-remediation-round3.md)；上次独立验收：[m2-reacceptance.md](m2-reacceptance.md)
> **独立结论：NOT ACCEPTED；不得进入 M3。**
> 验收边界：仅隔离的 Fake/Scripted Harness 与临时 Git 仓库；未调用真实 Codex/Claude/OMP/DeepSeek Provider，未消耗 Token，未写入真实 `~/.ao`、`.codex`、`.claude`；未提交/推送/部署。

## 一、直接核验的实现与阶段矩阵

此次从真实源码、HTTP/Daemon 接线、Git Worktree、MCP stdio 进程和独立运行测试核验，不把实施报告中的 PASS 当作证据。

| 项目 | 第三轮独立判断 | 实际源码/实测证据 |
| --- | --- | --- |
| M2-01 代码/所有权审计 | **PASS** | Workflow → Execution → Session Manager → Chat、Store/CDC 的调用链存在 |
| M2-02 Capability Resolver | **PASS（离线契约）** | `service/capability/probe.go` 不再回填请求 Generation；会话协商清除旧能力；SessionGeneration 必须可观察。真实四 Provider 仍 NOT_TESTED |
| M2-03 Attempt/Repo 绑定 | **PARTIAL** | CAS/Store、多仓库 Worktree 行、创建/恢复测试通过；但 Workflow 指定的 `BaseSHA` 在实际 `workspace.Create` 时未被使用（见 F-01） |
| M2-04 生产 ExecutionGateway | **PASS（Fake Provider 集成）** | `LocalGateway` 接入 Daemon，真实 Session Manager + scripted Chat/临时 Git Worktree 的 E2E 通过；不代表真实 Provider 已通过 |
| M2-05 Send/Steer/Stop | **PASS（离线契约）** | `SendPinned`、`InterruptPinned`、`SteerPinned`，原控制器 generation/turn 严格拒绝路径通过 |
| M2-06 协作 MCP 与审批 | **PARTIAL** | 真正 `ao mcp` stdio `tools/list` / `tools/call`、capability/attempt lease、消息、任务请求通过；但缺出厂 Operator 客户端（见 F-02） |
| M2-07 HTTP/API/CDC | **PARTIAL** | 路由、审计 token/Principal、消息 CDC、OpenAPI 生成有代码和测试；Operator 生产调用端缺位 |
| M2-08 Fake E2E/恢复 | **PASS（Fake only）** | 本次重新运行 `go test ./internal/integration -run TestM2 -count=1 -v`，**24/24 PASS**；涵盖 2 Repo 实际 Git Worktree、MCP/HTTP、重启恢复、Steer/Queue/Stop、越权与零 Provider |

## 二、独立新发现（阻断 M2 完成）

### F-01 — P1：指定 `BaseSHA` 未用于真实多仓库 Worktree 创建（**反向测试真实 FAIL**）

**根因与可追溯代码：**
- `backend/internal/ports/session.go`：`SpawnRepo` 允许为每个 repo 指定 `BaseSHA`。
- `backend/internal/session_manager/multi_repo_workspace.go`：`resolveMultiRepoPlans` 把 `repo.BaseSHA` 放进 `plan.baseSHA`（约第 236 行），但 `createMultiRepoSessionWorkspace` 构造 `WorkspaceConfig` 时**从未消费 `plan.baseSHA`**（约第 91～103 行）；实际使用项目默认分支 `baseRef`。
- 该文件 `verifyRepoWorktreeHead(ctx, ws.Path, ws.BaseSHA)`（约第 134 行）只检验 Adapter 自己选择的 SHA，不检验请求指定的 SHA。
- `service/execution/coordinator.go` 的 `classifyRepoBindings` 虽会比较预期 BaseSHA 与观察到的 HeadSHA 并拒绝 ready，但检查发生在 Session/Worktree 创建之后，不能让 Session 按需求指定的历史提交启动。

**本次独立反向测试：**
1. 使用仓库已有测试辅助方法，在两个不同临时 Git Repo 各准备 2 个 commit。
2. 在 `SpawnConfig.Repos` 的 `repo-a` 指定 `BaseSHA = git rev-parse HEAD~1`，确保不同于当前 `main` 的 HEAD。
3. 直接调用**真实** `sessionmanager.Manager.Spawn` / 真实 `gitworktree.Workspace`，读取实际 Repo Worktree 的 `git rev-parse HEAD`。
4. 命令：`go test ./internal/session_manager -run '^TestIndependentM2PinsRequestedBaseSHA$' -count=1 -v`。
5. **退出码 1，测试 FAIL（独立复现的代码缺陷，不是 Windows 旧基线）**。实际输出关键行：
   `base SHA pin ignored: requested 193987f44b384229893df8893f81ace228f4fa9c, observed 98f0ea0037fea1fe50d1dbc2c04ac40eb5c1d7eb (default tip 98f0ea0037fea1fe50d1dbc2c04ac40eb5c1d7eb)`。
6. 临时测试文件 `backend/internal/session_manager/zz_m2_independent_acceptance_test.go` 已在失败证据采集后删除，没有留下业务文件变更。

**影响：** 当 Workflow 固定某个基线 SHA 后，主分支又前进时，M2 仍在最新 HEAD 建立 Worktree；后续绑定 gate 会拒绝，但此前副作用已经发生，Task 不能按指定基线实际执行。**不符合 M2 多 Repo 严格 BaseSHA 绑定与可恢复性要求。**

**修复要求：** 在发起任何 Worktree Create 之前验证每个 Repo 的 `BaseSHA` 是否存在并符合项目/Task 授权范围；用该 SHA 作为每个仓库工作树的真实创建基准（或显式拒绝无法 pin 的输入），创建后再次比对请求 SHA 与实际 HEAD；失败时不得错误写成功绑定，保证不动用户现有 dirty Worktree。补充 `BaseSHA = HEAD~1`、仓库 HEAD 前进、非法 SHA、两个不同基线、恢复后核对的真实 Git 反向测试。

### F-02 — P1：Operator Token 已受服务端校验，但无出厂桌面/CLI 客户端携带它

**证据：**
- `service/principal/operator_token.go` 与 `daemon/workflow_auth_wiring.go` 建立了 `<dataDir>/operator.token` 和 `X-AO-Operator-Token` 验证；`workflows.go` 对 Bind、Reconcile/Resume、Resolve/Decide 等写入均要求 Operator。
- 全仓检查 `backend/internal/cli` 与 `frontend` 的生产代码，**没有**读取该 token / 附加该 header 的客户端实现；仅 Daemon/Principal 与测试夹具引用 token。实施方自己的 `m2-plan.md` 和 `m2-remediation-round3.md` 也将其列为开放问题。
- Handler 的安全拒绝测试通过；它们不证明用户可通过现有桌面应用/CLI 实际发起操作。

**影响：** 安全验证采用“明确 token + loopback”比旧规则更合理，但产品默认客户端无法提供此 token，关键 Operator 操作无法通过出厂控制面调用；需手工读取文件并构造 curl/HTTP 才可用。未来 M5 UI 不是本次 M2 HTTP 操作缺少出厂调用端的有效验收证据。

**修复要求：** 由可信主进程/本地 CLI 以严格限权方式附加 Operator Token，避免把 token 暴露给 Renderer、MCP、Agent、日志；用出厂客户端走一次真实 HTTP Bind→审批/Resolve→恢复正向与 Agent 伪造拒绝的 Fake E2E。Windows 需明确 NTFS ACL 边界，不能以 `0600` 作为 Windows 下已强制隔离的证据。

## 三、已完成的独立测试

| 执行命令/场景 | 结果 | 说明 |
| --- | --- | --- |
| `go test ./internal/ports ./internal/service/execution ./internal/service/intervention ./internal/service/capability ./internal/service/principal ./internal/service/collaboration ./internal/service/workflow -count=1` | **PASS / exit 0** | 7 个核心包全部通过 |
| `go test ./internal/integration -run TestM2 -count=1 -v` | **PASS / exit 0** | **24/24 E2E 通过**，含真实 Session Manager + 临时双 Git Repo + Scripted Driver、MCP 进程 |
| `go test ./internal/cli -run TestMCPWorkflow -count=1` | **PASS / exit 0** | CLI MCP workflow 工具定向测试 |
| `go test ./internal/session_manager -run 'TestMultiRepo\|TestResumeRecoversWorkflowMCPEnv\|TestResumeOmitsAnExpiredLease\|TestWorkflowSpawnContext\|TestWorkflowMCP' -count=1` | **PASS / exit 0** | 双仓库 Worktree、重启恢复身份定向 |
| `go test ./internal/daemon -run TestM2 -count=1` | **PASS / exit 0** | 实际 Daemon wiring 相关测试 |
| `go test ./internal/httpd/controllers -run TestM2 -count=1` | **PASS / exit 0** | M2 HTTP/权限/操作回执定向测试 |
| `go test ./internal/storage/sqlite -run 'TestM2|WorkflowAudit' -count=1` | **PASS / exit 0** | M2 Migration 与审计相关定向用例 |
| `go test ./internal/storage/sqlite/store -run 'TestM2|WorkflowAudit|TestCreateWorkflowInterventionIfNoOpenStopIsAtomic' -count=1` | **PASS / exit 0** | M2 Store、审计、Stop 单赢家相关测试 |
| `go build ./...` + `go vet ./...` | **PASS / exit 0** | 后端全量构建与 vet |
| `go test -race ./internal/service/execution ./internal/service/intervention ./internal/service/capability ./internal/service/principal ./internal/service/collaboration -count=1` | **PASS / exit 0** | 5 个 M2 Service race 定向测试 |
| `go test -race ./internal/integration -run TestM2 -count=1` | **PASS / exit 0** | M2 全部 Fake 集成 E2E race 通过，约 93.5s，无 data race |
| `npm run sqlc` | **BLOCKED / exit 1** | `proxy.golang.org` 连接超时，无法独立复核 sqlc v1.31.1 生成物；不是已证明的代码失败 |
| `npm run api` | **PASS / exit 0** | 代码优先的 OpenAPI Spec 与前端 TypeScript schema 生成成功 |
| `npm run frontend:typecheck` | **PASS / exit 0** | `tsgo --noEmit` 无类型错误 |

**明确不计入本次 PASS：** 完整全仓 `go test ./...`、真实四 Provider E2E；前者涉及已知大量 Windows 基线失败及潜在真实 Harness，后者 M0-05 预算尚未批准。实施方报告所述既有失败仅作对照，不伪装为本次全部重现。

## 四、其他限制与风险

- Token Trust 仅证明受控 Operator 凭据，不证明本地同用户进程的 OS 级身份；`0600` 在 Windows 未必强制 NTFS ACL。只有不把 token 传给 Agent/MCP 才能维持当前信任边界。
- `session_manager/multi_repo_workspace.go` 的 `multiRepoRows` 在恢复时对所有 RepoRow 填 `rec.ProjectID`（根 Repo 项目），没有分别解析各行实际 ProjectID。当前测试以 repo path 恢复通过，但内存 `WorkspaceRepoInfo.ProjectID` 对第二个 Repo 不准确，应补持久化映射/恢复断言（P2）。
- 尝试租约约 15 分钟，没有完整续租链路；Agent 长任务的 MCP lease 可能到期后被正确拒绝，属于尚未闭合的运行时能力（后续阶段设计前需明确归属）。
- 批准 Task Request 与真正 materialize Task 的边界仍为 202/待创建；本次按 M2/M3 分工暂不计成已调度。
- M0-05 未批准，真实 Codex/Claude/OMP/DeepSeek 的授权、Steer、Resume、Tool 交互仍为 **NOT_TESTED**。

## 五、最终判定与重新验收门槛

**M2：NOT ACCEPTED；M3：不得开始。**

至少先修复 **F-01**（按请求 BaseSHA 真实创建/验证双仓库 Worktree），并补齐 **F-02**（出厂 Operator 客户端安全携证调用，不暴露给 Agent），随后：

1. 复跑前述负向 BaseSHA Pin 测试，且必须 PASS。
2. 用两个不同历史基准的真实临时仓库验证 Workflow→HTTP Bind→SessionManager→Worktree→恢复，不得只用当前 HEAD 为基准。
3. 由正式本地客户端启动 Operator API，验证 Bind/审批/Resolve/Reconcile 与权限拒绝。
4. 复跑 24 项 M2 E2E、核心 Go/build/vet、关键 race；恢复网络后复核 sqlc/api 生成漂移。
5. 保持 M0-05 真实 Provider 测试独立审批、0 Provider Token 边界；保留之前的验收与实施报告。

## 六、Git 与副作用

本次验收只新增此报告/更新文档状态。反向测试 Go 文件**临时创建、执行、删除**；没有修改已有业务源码。未提交/推送/部署；Git HEAD 仍为 M1 `a6c284a`，M2 工作树未提交。本次使用 `t.TempDir()` 与真实临时 Git Repo，而非用户生产仓库。
