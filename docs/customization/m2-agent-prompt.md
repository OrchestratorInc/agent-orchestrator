# M2 编码执行与分级协作 — Agent 执行委派提示词 V0.2（M1 已提交基线）

> 复制本文件的「正式提示词」给另一个编码 Agent。**本文件是委派指令草案，不代表已经替用户实际启动或授权 Agent。**
> 设计基线：[m2-design.md](m2-design.md)；准入证据：[m1-acceptance.md](m1-acceptance.md)。
> **已核实 M1 基线提交：`a6c284a4d060431371306072e6c80abfedf22328`**，本地分支 `feat/multi-agent-desktop`。本提示词与 M2 设计文档目前仍是**本地未提交文件**，接手 Agent 必须从当前工作区读取，不能假设远端或新建 worktree 已包含它们。

## 正式提示词（可直接复制）

你现在是 `E:\code\agent-orchestrator` 项目 **M2：编码执行与分级协作** 阶段的主实施 Agent。请先完整阅读仓库当前源码与以下文件，再按依赖顺序实现、测试并记录结果：

- `AGENTS.md`、`CONTRIBUTING.md`、`docs/architecture.md`、`docs/STATUS.md`、`docs/documentation-map.md`
- `docs/customization/requirements.md`（Q1–Q10）
- `docs/customization/design.md`（总体 V0.1）
- `docs/customization/m1-plan.md`、`m1-report.md`、`m1-acceptance.md`
- **`docs/customization/m2-design.md`**（M2 的唯一详细设计入口）
- `docs/customization/m0-capability-matrix.md`、`m0-adapter-gaps.md`

### 0. 环境与版本

- 项目：`E:\code\agent-orchestrator`
- 分支：`feat/multi-agent-desktop`，**M1 已提交的 HEAD**：`a6c284a4d060431371306072e6c80abfedf22328`，提交说明 `feat: implement M1 workflow core and acceptance`。
- M1 前的历史基线：`623871d`（用于重放旧 Windows 失败的对照，不是 M2 开发起点）。**M2 的 Git 基线必须使用 `a6c284a`**；所有 M2 改动以这个提交为起点核对。
- **注意：M1 的 36 个代码/迁移/生成物/验收文档已经提交，绝对不要重做或重复提交。** 当前工作树仍显示 `dirty`，主要来自未提交的 `docs/customization/README.md`、`design.md`、`m2-design.md`、本提示词，以及约 35 个受 Windows `core.autocrlf` 影响而显示 modified、但 `git diff` 无实际内容变化的 `backend/internal/storage/sqlite/gen/*.go`。不要为了制造 clean 状态而 `reset`、`clean`、`restore`、`stash` 或重写生成物；应先对照内容级 diff，并与其他 Agent 协调文件所有权。
- 先执行只读状态检查（Windows PowerShell 使用 `-NoProfile`）：`git branch --show-current`、`git rev-parse HEAD`、`git status --short --branch`、`git diff --check`、`git diff --stat`、`git log -1 --oneline`；确认 M1 HEAD 是 `a6c284a`，且 `git show --name-only a6c284a` 已包含核心源码/验收文档。若 HEAD 已发生变化，先识别变动来源，不擅自将 HEAD 强行重置。
- 如仓库存在其他 Agent 同时修改，立即协调文件所有权；不要让多个 Agent 同时写 migration、`sqlc.yaml`、OpenAPI spec 或生成目录。
- 修改前阅读具体模块的项目规则；遵守现有 daemon/service/ports/domain/store 依赖方向。

### 1. 任务目标

在 **不启动真实 Provider** 的授权范围内，把 M1 Workflow Task/Attempt 薄层对接到现有 AO Session Manager、Chat Service、四首发 Harness（Codex、Claude Code、OMP、DeepSeek Harness）的能力契约，并实现：
- 只依赖现有 Session Manager 的幂等 Spawn、Attempt–Session–Worktree 绑定与恢复。
- 基于实时能力和证据的 Capability Resolver，`UNKNOWN` 不伪装为支持。
- Steer/Queue/Stop & Replan 的受控入口与回执；不能重做 Chat Loop。
- 子 Agent 消息协作、资源/任务申请与主控审批边界；不能因为聊天内容自动增加并发。
- 事务持久化、HTTP API、CDC/SSE、OpenAPI/sqlc 生成及 Fake/Stub 离线端到端测试。

**M1 核心验收已 PASS 且已提交为 `a6c284a`**，不要重写 M1；发现缺陷时给出最小复现与手术式修复，在独立 M2 变更记录中解释必要性，并记录与 M1 已知基线的差异。**M0 正式验收仍 BLOCKED，M0-05 预算没有批准**；不能假设真实 Harness 已通过 Provider 验证。

### 2. 执行清单（按序推进，逐项复核）

| 顺序 | 任务 | 必须交付 |
| --- | --- | --- |
| M2-01 | 实际接口/继承风险复核 | M1/P2/M0 缺口及文件所有权清单，明确要保留的 M1 不变量 |
| M2-02 | Agent Capability Resolver | 证据/版本/TTL/认证/Steer/Queue/Resume/审批模式能力表，四 Harness Fake 测试 |
| M2-03 | Session/Repo Bind DB | 新 migration、sqlc、CAS、spawn request 稳定去重、Session/Worktree 引用和重启恢复测试 |
| M2-04 | ExecutionGateway | 组合现有 Session Manager/Chat Service；先以 Fake 驱动通过 Spawn/Bind/Find/Observe，绝不直接启动 CLI |
| M2-05 | 三种干预通路 | Steer 精确 turn + generation、Queue 回执、Stop 后保留成果/ReplanRequest；失败必须有明确降级 |
| M2-06 | 分级自治协作 | 持久化消息与任务/资源申请，任务来源审计、权限校验、跨 Workflow 拒绝、幂等测试 |
| M2-07 | HTTP + CDC/SSE | DTO/路由、API spec、事件 trigger、回滚无事件/断线续接、`npm run api` 与 `npm run sqlc` |
| M2-08 | 完整离线验收 | 2 个不同 Harness Fake worker 并行、并发/幂等/断线/旧 generation/恢复/日志脱敏/零回归，形成 `m2-report.md` |

每步至少输出：修改文件、关键实现语义、使用的命令及退出码、通过/失败/不适用的用例、M2 安全边界确认。前一步未解决的 P0/P1 不得带入后一步并声称完成。

### 3. 强制约束（不可突破）

1. **零真实 Provider、零 Token**：不得启动、调用或给 Codex CLI、Claude Code CLI、OMP、DeepSeek Harness 发送真实 prompt。即使本机已登录，也不能自行发出任何测试聊天。
2. **不碰真实用户数据**：不得读写真实 `~/.ao`、`.codex/`、`.claude/` 配置，不删除现有 Session/Worktree；只在 `t.TempDir`、隔离数据目录和受控测试 Repo 下执行。
3. **不推送/不发布/不合并**：M1 已按用户授权在本地完成单次提交；此项授权**不自动延伸为 M2 的提交或远程推送权限**。不得 `git push`、`gh pr create`、发布、部署、更新全局配置或向用户目标分支集成；M2 本地 commit 先获得用户明确授权。任何额外预算和危险命令均须明确审批。
4. **不提前实施 M3～M6**：M3 智能动态并发/预算、M4 自动 Gate/提交与审批、M5 React/Electron UI、M6 打包发布都不属于 M2。
5. **不伪造能力和结果**：静态代码存在 ≠ 运行时支持；`configured` ≠ `authorized`；Queue receipt ≠ Provider 实际处理；Host 退出 ≠ 成功；Lease 超时 ≠ Worker 已死。
6. **不改生成物原文**：只能改 sqlc 查询/schema 或 API spec 的生成源后运行项目生成命令；生成不稳定、模块代理不可用时记录 `BLOCKED`，不得手工篡改。
7. **禁止危险重试**：Spawn、Send、Steer、Stop 调用无回执时先寻找 durable 回执；不可证明只执行一次 → `UNKNOWN` 并停止。
8. **M0 未批准的实际测试不得补跑**：A-02～A-15 属 M0-05 审批区，任何“方便验证真实 Agent”的提议都不能视为授权。
9. **Windows 命令规范**：PowerShell 使用 `-NoProfile`，非破坏性命令尽量合并；不得大范围重排上游代码或格式化所有生成文件。

### 4. 特别提醒的技术问题

- 首发四个 Harness 不是同一个协议：Codex app-server 与 ACP 三家分开处理，ACP 各家的认证/permission/Steer/Resume 不可硬编码。
- `ports.SpawnConfig` 已有 `ClientRequestID`、`ClientRequestHash`、`ParentSessionID`、`RequestedMode`、`Async`；必须优先复用，而不是自己 `exec.Command`。
- `ports.ChatSteerer`/Chat Capability 已有门禁，`service/chat/SteerOrSend` 与持久化队列已经存在；不可另起消息循环。
- Session/Chat/Worktree 是现有服务的唯一所有者；Workflow 只存关联、权限、审计与协调事实。
- M1 已有 `ResolveUnknownWorkflowAttempt` 和 Lease 扫描；必须和 M2 恢复路径组合，而不是额外做一个冲突的自动恢复控制器。
- 继承的高风险缺口：幂等重放响应不是快照、LeaseToken 可能因 `%+v` 被泄露、`cancelled` 完成语义、`claimed → succeeded` 未记录启动时刻、Windows Codex 账号诊断、两条 handoff 基线失败。任何安全关键路径先修/阻断/记录，不要掩盖。
- M2 的 Session/协作表需要事务/外键、旧 generation fencing、请求 fingerprint 与 rollback test；`change_log → CDC → SSE` 是唯一事件流。

### 5. 每项验证与阶段报告

在能使用隔离工具链的 Windows 环境执行并记录退出码：

```powershell
# 以下为各自独立命令；实际执行使用 PowerShell -NoProfile
cd E:\code\agent-orchestrator\backend
go build ./...
go test ./internal/service/workflow ./internal/storage/sqlite/store ./internal/storage/sqlite ./internal/cdc ./internal/httpd ./internal/httpd/apispec/... -count=1
go test -race ./internal/service/workflow ./internal/storage/sqlite/store -run 'Workflow|Capability|Binding|Intervention|Collaboration' -count=1
go vet ./internal/service/workflow ./internal/storage/sqlite/store ./internal/storage/sqlite ./internal/cdc ./internal/httpd/... ./internal/daemon
cd E:\code\agent-orchestrator
npm run sqlc
npm run api
npm run frontend:typecheck
git diff --check
```

若 Windows 全量测试失败，先与 M1 报告复现的 8 条旧失败对照；必须列出 M2 新失败集合，不可因为红基线直接取消目标包验收。若某些命令因环境/网络/编译时间无法执行，报告 `NOT_TESTED/BLOCKED` 和准确原因，不要宣称通过。

**M2 离线开发门禁 PASS 的最低证据**：所有新测试通过；跨 Workflow 拦截/授权拒绝/重复 Spawn/Queue/Steer/Stop/Lease 恢复等 Fake 测试全部有真实运行结果；构建、生成物、API/CDC 与既有 M1 功能无回归；至少两种不同 Harness 的**Fake** 驱动可完成并行工作流任务并显示正确绑定/结果。状态不满足时不得合并或推进下一里程碑。

**M2 真实 Agent 端到端验收另列 `BLOCKED/NOT_TESTED`**：待用户以后明确批准 M0-05 预算、测试时段和隔离测试 Repo 才可执行。M2 不得用 Fake 测试冒充四个真实 Provider 已验证。

### 6. 输出要求

在仓库中维护：

- `docs/customization/m2-plan.md`：M2-01～M2-08 任务进度表，每项 Done/Blocked/Deferred 说明。
- `docs/customization/m2-report.md`：版本/分支、变更清单、测试命令与退出码、关键日志摘录、M1 既有失败对照、四 Agent 能力证据矩阵（真实 Provider 不测试就标 NOT_TESTED）、剩余风险。
- `docs/customization/README.md`：链接到 M2 状态与报告。
- M2 的 migration、sqlc/API 生成产物与用例按 `AGENTS.md` 正确归档。

最终只在证据足够时报告 **M2 离线开发 PASS**；**不要**报告“M2 全部通过/四 Agent 全部支持”或“M0 已通过”。

若关键权限/预算条件缺失，停止相应真实外部动作，保留现有文件，明确输出所需授权与下一步；**不要主动索取或读取任何密钥**。

### 7. 交付完成时给我的总结

请按下面顺序汇报，不要只说“已完成”：

1. `M2-01～M2-08` 逐项状态和最终离线验收结论。
2. 关键代码文件及是否修改/保留 M1 不变量。
3. 所有核心测试的运行命令、退出码和新旧失败对照。
4. 本地 Fake 与真实 Provider 证据的区别。
5. 工作区 `git status`、M1 基线是否仍为 `a6c284a` 的可追溯祖先、是否产生未经用户授权的 M2 commit 或远程副作用（默认应为无）。
6. 仍待批准的 M0-05 Provider 测试、危险操作和 M3 前置事项。

**现在开始 M2-01：只做源码核对与实现计划，确认文件归属和零 Provider 边界，然后按照依赖逐项实现；不要先启动真实 Agent。**
