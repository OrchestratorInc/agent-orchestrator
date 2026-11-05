# M2 第五轮独立源码与运行验收

> 日期：2026-11-05（UTC+08），仓库 `E:\code\agent-orchestrator`，分支 `feat/multi-agent-desktop`，HEAD `a6c284a`（M1）
>
> 依据：`m2-acceptance-round4.md`（上轮独立结论），`m2-remediation-round4.md`（实施方说明）
>
> **结论：NOT ACCEPTED；M3 不准进入。F-01 与 F-02 的离线修复有实测正向证据，但发现新的 F-03（P1）单仓库 Worktree 失效开放。**
>
> 本轮不运行真实 Codex、Claude、OMP、DeepSeek Provider；M0-05 未批准。仅使用 Scripted/Fake Agent、临时 Git Repo、隔离 SQLite 和进程内 API/CLI 测试。无业务源码修改、提交、推送、部署或用户 dirty Worktree 清理。

## 1. 阶段矩阵

| 项目 | 独立判断 | 证据/限制 |
| --- | --- | --- |
| M2-01 代码/归属审计 | PASS | 独立读源码核对 Execution、SessionManager、Gateway、DB、CLI 和 OS Consent 接线 |
| M2-02 Capability Resolver | PASS（离线） | M2 E2E 与核心定向测试；真实 Provider 未验证 |
| M2-03 Attempt / Repo / Git Worktree | **PARTIAL — F-03** | F-01 历史 BaseSHA、双仓库 ProjectID 与恢复已通过；单仓库实时核验缺口 |
| M2-04 ExecutionGateway | **PARTIAL — F-03** | 双仓库真实 Manager/Scripted 集成通过；一个 worktree row 会绕过 verifier |
| M2-05 Queue / Steer / Stop | PASS（离线） | M2 E2E 覆盖 generation fencing、恢复与回执 |
| M2-06 Agent 协作与 Operator 审批 | PASS（离线门禁）；真实原生人工流程未独立重测 | 正式 CLI/MCP/Agent 拒绝与一次性 OS Consent 对接已实现；开发方曾执行 Windows Hello 人工正向，不能代替本轮独立验证 |
| M2-07 API / CDC / OpenAPI | PASS（已运行定向部分）；全量基线另列 | 现有定向 Controller/MCP 和生成契约 |
| M2-08 Fake E2E/异常与恢复 | PASS（Fake only） | `go test ./internal/integration -run TestM2 -count=1 -v` 本轮实际 exit 0；真实 Manager、双 Git、Scripted、SQLite；不代表四真实 Provider |

## 2. F-01 第四轮阻断：历史 BaseSHA 被忽略 — **已关闭（Fake/真实 Git）**

直接源码核验：`session_manager/multi_repo_workspace.go` 将 `plan.baseSHA` 传入 `ports.WorkspaceConfig.BaseSHA`；`gitworktree/workspace.go` 的 `Create` 将 pin 当作 seed commit，不再改用默认分支；创建后读取实际 Git HEAD 与**请求 SHA**比对，再持久化 `BaseSHA` / `CreationSHA`。Repo 层新增 `project_id` / `repo_path` 并在恢复时验证来源。

独立临时反例 `TestIndependentM2HistoricalPinsAndDurableOwnership`：
- 两个不同的临时 Git Repo，均有至少两个 commit；分别指定各自的 `HEAD~1`；
- 使用真实 `Manager.Spawn`、真实 Git Worktree Adapter 与 SQLite；
- 逐仓库执行真实 `git rev-parse HEAD`，检查请求 SHA、实际 HEAD、持久化 BaseSHA/CreationSHA、ProjectID；
- 调用 Manager 恢复同一会话并核验成功。

**实测 PASS / exit 0，TestIndependentM2HistoricalPinsAndDurableOwnership (10.29s)**。原失败现已逆转。临时 Go 测试文件已删除。正式 `TestM2SessionManagerBindHonorsPinnedBaseSHA` 也在本轮 M2 E2E 中 PASS。

## 3. F-02 第四轮阻断：正式 Operator 入口缺失 — **代码接线完成，受限通过**

直接检查：
- `daemon/workflow_auth_wiring.go` 生产装配 `ApprovalOperatorTrust`，不再铸造或信任 `operator.token`；
- `service/principal/operator_approval.go` 的 approval 要求 method、完整 RequestURI/query、精确 payload digest 匹配，消耗一次后不得重放，且 daemon 重启失效；
- `httpd/controllers/operator_approvals.go` 只接受 loopback，禁止 Agent 身份头触发审批；confirm 委托 `operatorconsent.New()`；
- `adapters/operatorconsent/consent_windows.go` 在系统 PowerShell 与受限环境下展示详情、调用 Windows UserConsentVerifier；
- `cli/operator.go` 和 `frontend/src/main/operator-ipc.ts` 提供正式调用端，Electron Main 校验主窗口/同源和受限动作。Token 不进入 Renderer；
- 正式 `TestM2FormalCLIThroughReopenedSQLite` 本轮真实运行通过，涵盖拒绝/批准、双仓 Bind、MCP 协作、Decide、Resolve、Reconcile/Resume、SQLite 重启和审计。

开发方报告的真实 Windows Hello 人工正向：`verified-windows-hello-live.log`，在本轮**未由独立验收者重复执行**；本轮不把 fake verifier 等同于真实 OS 人工认证。任意同用户进程的内存注入、篡改进程/安全桌面不在当前信任模型证明范围内；需要明确产品安全声明。

## 4. **F-03（P1，新发现）：单仓库 Workflow Worktree 观察绕过实时 Git 核验**

**实际源码根因：**

- `backend/internal/service/execution/local_gateway.go` 的 `observe` 将数据库中的 SessionWorktreeRecord 转成 RepoWorktreeObservation：`HeadSHA` 直接赋为 `record.CreationSHA`，没有读取当前 Git；
- 同一函数只有 `if len(worktrees) > 1` 才调用 `SessionWorktreeVerifier.VerifySessionWorktrees`；**恰好一个工作树记录时，跳过 verifier，仍可能返回 SessionReadinessReady**；
- `backend/internal/session_manager/multi_repo_workspace.go` 的 `multiRepoSessionRows` 对 `len(rows) <= 1` 返回非多仓路径，`VerifySessionWorktrees` 目前不能验单个有 M2 RepoKey 的 worktree；
- `backend/internal/service/execution/coordinator.go` 的 `repoGateDecision` 对单个期望 repo 且 `len(worktrees)==0` 刻意关闭 gate 以兼容旧单仓库 Spawn。对于**M2 Workflow 显式 Repos**，这意味着记录缺失也不能自动从 Session 作出 repo 已验证的推论。

**独立负向测试（真实运行失败，证明缺陷）：**

临时添加 `backend/internal/service/execution/zz_m2_independent_single_repo_recheck_test.go`，复用现有 `newGatewayFixture` / `readyChatSession`，设置仅一条 M2 Repo Worktree row（实际目录指向 `C:/missing-worktree`，Session Controller live），并刻意令 Session 接口无法证明实时 Git。调用生产 `LocalGateway.InspectSession`，要求不得返回 ready。

```text
go test ./internal/service/execution -run ^TestIndependentM2SingleRepoWorktreeRequiresLiveVerification$ -count=1 -v
=== RUN   TestIndependentM2SingleRepoWorktreeRequiresLiveVerification
single-repo observation claimed READY solely from persisted row, with no live Git verification;
WorktreeRef:"C:/missing-worktree", HeadSHA:"0123456789012345678901234567890123456789"
--- FAIL: TestIndependentM2SingleRepoWorktreeRequiresLiveVerification
FAIL (exit 1)
```

此反例使用真实 Gateway + 它的标准测试夹具，证明**在缺少实时验证器时仍放行一个 M2 仓库记录**；并未用真实 Git 直接替换工作树。因此这是运行级 fail-open 证据，不应夸大为已执行单仓库真实 Git 攻击。源码条件已足以确认该漏洞在生产接线中的触发路径。临时反向测试文件在收集证据后已删除。

**影响：** 一个仓库的 Workflow 可能在其 worktree 被删除、替换、分支漂移或 Git 记录不可读后仍被报告为 READY。后续 Queue/Steer/Bind/Reconcile 的能力与归属判断可能错误。不能以双仓库 E2E 已通过代替单仓库场景的安全性。

**修复门槛：**

1. Session Manager 提供可验证 `1..N` 个显式 M2 Repo 的接口，持久化 RepoKey/ProjectID/RepoPath 与真实 Git HEAD/branch/common-dir/CreationSHA；不能把 “只有一个记录” 判成普通 legacy 路径。
2. Gateway 对任何 M2 owned Worktree（包括恰好一条）都调用实时验证器，缺失接口、路径缺失、git 错误、路径/ProjectID/历史不匹配时 fail-closed，清除冒充 live 的 HeadSHA，不返回 READY。
3. 单仓库 M2 expected binding 的 worktree 行缺失必须阻断，不得套用普通非 Workflow 的单仓库兼容分支；旧非 Workflow 会话无 worktree row 的原有行为保持。
4. 增加 **真实 Session Manager + 单个临时 Git Repo + SQLite + HTTP Workflow** 的正/负 E2E：单仓 Bind、删除 worktree、替换 repo、切支、HEAD 回退、dirty 保留、daemon 重启恢复；Queue/Steer/Resume/Reconcile 必须尊重新的核验。
5. 复跑刚才失败的独立 negative，以及原双仓库 E2E、Race、M1 原有行为；不得只放宽验证或删除旧测试。

## 5. 本轮执行结果

| 检查 | 第五轮实际结果 |
| --- | --- |
| `go test ./internal/integration -run TestM2 -count=1 -v` | **PASS / exit 0**，本轮 28 个顶层用例、118.580s |
| 独立真实 Git 历史 BaseSHA 双仓库 + 恢复 | **PASS / exit 0**，10.29s |
| 独立单仓库 Worktree 未验证反例 | **FAIL / exit 1**，新 P1 真实复现 |
| `go test -race ./internal/integration -run TestM2 -count=1` | **PASS / exit 0**，202.130s，未检测到 data race |
| Go 七核心包 + CLI/MCP/OperatorConsent、Controller/Daemon、Git Adapter pin 定向 | **PASS / exit 0**；该轮调用链全部完成 |
| `go build ./...` / `go vet ./...` | **PASS / exit 0**（两项均实跑完成） |
| `npm run frontend:typecheck` + `npm --prefix frontend test -- src/main/operator-ipc.test.ts` | **PASS / exit 0**，Electron Main IPC 7/7 测试通过 |
| `npm run api` + `go test ./internal/httpd/apispec/... -count=1` | **PASS / exit 0**，OpenAPI/specgen 生成与漂移测试通过 |
| M2 SQLite / Store 定向迁移与审计 | **PASS / exit 0**，`go test ./internal/storage/sqlite ./internal/storage/sqlite/store -run 'TestM2|WorkflowAudit|SessionWorktree|AttemptBindings' -count=1` |
| `npm run sqlc` | **BLOCKED / exit 1**：原生 `npm run sqlc` 与 `rtk proxy npm run sqlc` 两次均因 `proxy.golang.org` 连接超时失败，本轮未能独立确认 sqlc 生成物稳定性 |
| `git diff --check`、临时文件清理 | **PASS / exit 0**；两个独立负向/正向临时 Go 测试均已清理 |
| 真实 Provider、完整安装包与桌面人工交互 | NOT_TESTED（M0-05/人工独立验证边界） |

**禁止将 PENDING/NOT_TESTED/实施方先前 PASS 计为本轮独立通过。**

## 6. 最终结论与副作用

**M2 NOT ACCEPTED，禁止准入 M3。** 必须先关闭 F-03，并提供真实单仓库 Git 回归；F-01 已在本轮实际关闭，F-02 的正式流程已有离线证据，但独立原生 Windows Hello 正向留作后续最终环境核验。

本轮未提交、推送、合并或部署；临时独立测试文件在运行后删除；保留全部 M2 未提交实现。本报告是第五轮独立记录，不覆盖前四轮或实施方修复报告。
