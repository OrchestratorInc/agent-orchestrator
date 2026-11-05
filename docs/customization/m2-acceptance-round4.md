# M2 第四轮独立复验（源码 + 真实临时 Git + 运行测试）

> 日期：2026-11-04（UTC+08）
> 工作区：`E:\code\agent-orchestrator`
> 分支：`feat/multi-agent-desktop`；Git HEAD：`a6c284a`（M1）
> 对照：`m2-acceptance-round3.md`（第三轮独立验收）
> **结论：NOT ACCEPTED；仍不准入 M3。**
> 实施范围：仅离线 Fake/Scripted Harness、隔离 SQLite 与 `t.TempDir()` 临时 Git Repo。未启动真实 Codex/Claude/OMP/DeepSeek Provider，未使用其凭据或消耗 Token；未提交、推送、部署，也未触及真实 `~/.ao`、`.codex`、`.claude` 运行资料。

## 1. 本轮独立结论

| 阶段 | 判定 | 证据/限制 |
| --- | --- | --- |
| M2-01 调用链/所有权审计 | PASS | Workflow → Execution → Session Manager → Chat，HTTP/Store/CDC 存在 |
| M2-02 Capability Resolver | PASS（离线契约） | 核心包测试通过；真实 Provider 未验 |
| M2-03 Attempt/Repo 持久化与 Worktree | **BLOCKED — F-01** | 指定历史 BaseSHA 在真实 Worktree 创建时被忽略，独立反向测试退出码 1 |
| M2-04 生产 ExecutionGateway | PASS（Fake 集成） | 真实 Session Manager、临时 Git、Scripted Driver 集成集通过 |
| M2-05 Send/Steer/Queue/Stop | PASS（Fake/契约） | 核心 Service 与集成定向测试通过 |
| M2-06 协作 MCP/审批 | **PARTIAL — F-02** | MCP 测试通过；没有出厂 Operator 客户端携带凭据 |
| M2-07 HTTP/API/CDC | **PARTIAL — F-02** | HTTP 权限、OpenAPI、前端类型检查通过；客户端操作链未闭环 |
| M2-08 Fake E2E/恢复 | PASS（Fake only） | 本轮 TestM2 集成集通过；不是对真实四 Provider 的放行 |

## 2. 阻断 F-01：指定历史 BaseSHA 未真正 pin 工作树（P1，独立复现）

**源码确认：**

- `backend/internal/ports/session.go` 的 `SpawnRepo.BaseSHA` 允许指定历史提交。
- `backend/internal/session_manager/multi_repo_workspace.go` 的 `resolveMultiRepoPlans` 将其保存为 `plan.baseSHA`。
- 但 `createMultiRepoSessionWorkspace` 构建 `ports.WorkspaceConfig` 时仅传 `BaseBranch: plan.baseBranch` 和 `BaseRef: plan.baseRef`，**没有将 `plan.baseSHA` 传给工作树创建器**。
- 创建后的 `verifyRepoWorktreeHead(..., ws.BaseSHA)` 核对的是适配器自己选取的 SHA，而非请求中的历史 SHA。后续绑定 Gate 拒绝不等于创建时正确 pin。

**重新运行的负向实测：** 临时加入独立用例 `TestIndependentM2RequestedHistoricalBaseSHA`，使用已有 `newMultiRepoFixture`：两个独立临时仓库、真实 `gitworktree.Workspace`、真实 `Manager.Spawn`；为 repo-a 显式选择 `git rev-parse HEAD~1`，对比实际工作树 `git rev-parse HEAD`。

命令（工作目录 `backend`）：

```powershell
go test ./internal/session_manager -run ^TestIndependentM2RequestedHistoricalBaseSHA$ -count=1 -v
```

**结果：FAIL，退出码 1，核心证据：**

```text
requested BaseSHA: 779622726e3cb8fc998b0804f9c8655eae3ab5ba
observed HEAD:      84f5eec3cf0701f89cfd6a8c6e851d6104cb90ee
main tip:           84f5eec3cf0701f89cfd6a8c6e851d6104cb90ee
--- FAIL: TestIndependentM2RequestedHistoricalBaseSHA
```

这证明任务规划的基线不控制实际 Worktree 启动基线。用例文件 `zz_m2_independent_recheck_test.go` 已在保留失败证据后删除，无测试文件遗留。这是**真实代码缺陷**，不是 Windows 基线故障。

**关闭门槛：** 由每个 Repo 的授权 `BaseSHA` 驱动 Git worktree 的初始 seed commit，而不是仅用事后 Gate 检查。创建前检查 SHA 存在、是 commit 且属于授权 Repo；创建后逐仓库比对请求 SHA、实际 HEAD、持久化 CreationSHA。覆盖历史提交、仓库 HEAD 前进、不同 Repo 指定不同 SHA、无效 SHA 预拒绝、失败回滚与重启恢复；不得重置或删除用户 dirty worktree。必要时扩展 `ports.WorkspaceConfig` 和 gitworktree Adapter 的显式 pin 参数；仅传 `BaseRef` 不足以证明新建分支 seed 被固定。

## 3. 阻断 F-02：Operator Token 服务端具备，默认桌面/CLI 调用端缺失（P1）

**实际源码与搜索：**

- `backend/internal/service/principal/operator_token.go`：服务端要求 loopback + `X-AO-Operator-Token`，Token 从 `<dataDir>/operator.token` 读取/铸造。
- `backend/internal/httpd/controllers/workflows.go`：Bind、Reconcile/Resume、Resolve、Task Request Decide 写入操作有 Operator Gate；授权定向测试通过。
- 对 `backend/internal/cli/**/*.go` 和 `frontend/**/*.{ts,tsx}` 搜索 `X-AO-Operator-Token|operator\\.token|HeaderOperatorToken`：**生产调用端零命中**。现有 `mcp_workflow.go` 是 Agent 协作工具，携带的是 Agent capability/lease，不是可信 Human/Operator 通道。

结果是 HTTP 测试夹具可手工加 Header，但用户从正式桌面或 CLI 发起关键 Operator 操作的路径未闭合。

**关闭门槛：** 可信 Electron Main/本地交互式 CLI 实现正式的 Operator 操作调用链，严格控制 Token 读取与生命周期；禁止泄漏至 Renderer、Agent、MCP Tool、Prompt、普通环境变量、日志或 API 响应。必须验证正式客户端执行 Bind→审批/Resolve→Reconcile/Resume，且 Agent/伪造调用无法获得审批能力。根据 Q5/Q10 的人工批准要求，不能让 Agent 通过同用户 Shell 直接运行自动注入 Token 的 CLI 来自行批准危险操作。**Windows 的 `0600` 不等价于经过证实的 NTFS ACL/进程隔离**；应记录威胁模型并以实际访问控制测试证实 Agent 无法读取 Operator 凭据。

## 4. 本轮运行证据

| 检查 | 本轮结果 |
| --- | --- |
| 7 核心包：`go test ./internal/ports ./internal/service/{execution,intervention,capability,principal,collaboration,workflow} -count=1`（实际以完整包路径调用） | PASS / exit 0 |
| `go test ./internal/integration -run TestM2 -count=1` | PASS / exit 0；真实 Manager + Fake/Scripted + 临时双 Git Repo |
| `go test ./internal/cli -run TestMCPWorkflow -count=1` | PASS / exit 0 |
| `go test ./internal/httpd/controllers -run TestM2 -count=1` | PASS / exit 0 |
| `go test ./internal/daemon -run TestM2 -count=1` | PASS / exit 0 |
| Session Manager 的 MultiRepo/WorkflowMCP/Resume 定向测试 | PASS / exit 0 |
| `go build ./...` 与 `go vet ./...` | PASS / exit 0 |
| `npm run frontend:typecheck` | PASS / exit 0 |
| `npm run api`（Go 生成 OpenAPI + TS Schema） | PASS / exit 0 |
| `npm run sqlc` | **BLOCKED / exit 1**；连接 `proxy.golang.org` 获取 `sqlc@v1.31.1` 超时，非已证明源码失败；未能复核 sqlc 生成漂移 |
| `go test -race ./internal/integration -run TestM2 -count=1` | **PASS / exit 0**；93.297s，无检测到 data race |
| 独立历史 BaseSHA pin 反向测试 | **FAIL / exit 1**；本轮确认产品缺陷 |
| 真实 Provider 与全仓全部测试 | NOT TESTED；M0-05 预算未批，且全仓有既有 Windows 基线失败风险 |

此处 PASS 表示对应命令实际退出 0，不是声称所有产品场景已通过。生成器网络阻断不应误报为成功，也不影响 F-01/F-02 的明确阻断结论。

## 5. 其他风险（未计作新增独立 P1）

1. `session_manager/multi_repo_workspace.go` 中 `multiRepoRows` 恢复每行时使用 `ProjectID: rec.ProjectID`，对第二个独立 Project 归属不准确；临时双 Repo 场景应补恢复断言与持久化 ProjectID 映射（P2）。
2. Workflow Attempt Lease 目前存在自然到期，长任务需要受控续租或暂停/重新授权设计；不可用过期凭据悄悄继续（后续阶段明确归属）。
3. Task Request `approved` 仍映射 `materialization=pending`（HTTP 202），按 M2/M3 边界不是“已创建任务”。
4. 全部验证都遵守 0 真实 Provider/0 Token，M0-05 仍需独立审批；不得将 Fake 成功宣传为四 Provider 可用。

## 6. 下一轮准入标准

**M2 继续 NOT ACCEPTED，不能启动 M3。**

再次申请验收时必须至少提交：
1. F-01 的真实 Worktree pin 修复和上述负向测试 PASS。
2. F-02 的正式可信 Operator 客户端及拒绝冒充、泄露边界测试 PASS。
3. Workflow HTTP→Session Manager→双仓库（不同历史 SHA）→绑定→重启恢复的可重复 E2E PASS。
4. 出厂客户端操作 Bind/Decide/Resolve/Reconcile/Resume 且保留审计；Agent 无法自批。
5. 重跑 M2 E2E、Build/Vet、关键 Race、API/TS；网络可用时补 `sqlc` 生成稳定性；真实 Provider 仍需 M0-05 单独批准。

本轮除新增此报告外没有修改业务源码；负向测试 Go 文件已删除，没有 commit/push/deploy。
