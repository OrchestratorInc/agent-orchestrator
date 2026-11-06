# M4 开发任务计划 V0.3：自主执行、按需委派 Astra、精简质量 Gate、Review、任务 Commit、集成审批

> 当前授权（2026-11-06）：用户已明确恢复 M4 业务实施；M4-01～09 已完成主控实现和 G4 验证，整体等待统筹终验。下文迁移时的暂停与 TaskManager 状态仅为历史。当前进度见 [执行台账](m4-execution-ledger.md)，历史迁移证据见 [迁移报告](m4-migration-report.md)。M0-05 和高风险操作边界继续有效。

> 基线提交：`129e13ca5d8df210cdafcc2e061d521109130e27`；工作区：`E:\code\agent-orchestrator`；设计：`m4-design.md`。
>
> **迁移完成 / 实施等待批准。** M4 业务代码未实施；M4-01 原 Agent 已 completed/HANDOFF，但阶段未验收，ACCEPTED=0。实施任务原地 blocked（USER_APPROVAL_REQUIRED），不属于功能失败；M0-05 仍未批准。

## 1. 研发与委派约定（自主执行 V2）

- **主 Agent 对 M4 最终结果负责，并自主决定如何完成工作**：可以独立分析、编码、测试、Review 和修复，也可以在确有收益时调用子代理；不要求所有工作必须委派，不要求必须采用多 Agent 编排。
- **唯一子代理模型约束：凡是调用子代理，必须显式选择 Astra。** 当前环境已知模型标识为 `gpt-6-astra`；实际使用时以宿主模型选择器中确认的 Astra 对应项为准。无法确认 Astra 时，不启动该子代理，由主 Agent 自行完成或向用户说明具体限制，不得静默替换模型。
- 不规定开发子代理的固定数量、并发上限、调度循环或心跳频率；主 Agent 根据任务复杂度、工具能力和文件冲突自行判断串行或并行。需要并行修改时，采取足以避免覆盖的隔离措施；单人串行工作不强制创建 Worktree。
- **不要求调用 AgentDock、ACP、Task Manager 或其他特定 Agent 平台。** 不得为了进度打卡、心跳或状态轮询而反复调用 AgentDock；只有具体工作确实依赖某项工具时才调用。没有子代理工具不构成 M4 实施阻断。
- 子代理（如使用）是开发工具，不代表 M0-05 产品内真实 Harness 测试获批。产品测试禁止真实 Codex/Claude/OMP/DeepSeek Turn、真实账单和用户生产数据。
- 无用户单独许可，不 Push、Merge 远端/目标分支、不改 `.codex/.claude`、不清理既有 dirty Worktree、不写真实 AO 主数据目录。
- 无论由主 Agent 还是子代理实现，均须保留真实测试证据；“测试报告已写”≠测试 PASS。

## 2. 任务依赖图

```text
M4-01 基线、源码能力审计、DAG/协同协议与 ADR 冻结
  │
  v
M4-02 ArtifactSnapshot / SQLite / 持久化 Path Reservation
  ├────> M4-03 GatePolicy / 受限 Runner / M3 Scheduler 最小接线
  └────> M4-04 Independent Review / M2 Collaboration 消息交接 / 有界修复
                     │
                M4-03+04
                     v
M4-05 通过 Gate 后受限任务分支本地 Commit
                     |
                     v
M4-06 Integration Worktree 预演 / 多 Repo Plan
                     |
                     v
M4-07 Operator 批准 / 串行本地集成 / Partial 恢复 / Push 安全边界
                     |
                     v
M4-08 HTTP/CLI/CDC/生成物接线（显式依赖 M4-05 和 M4-07）
                     |
                     v
M4-09 综合 Fake/真实 Git E2E + 主 Agent 独立验收/交接
```

## 3. 交付工作包

| 编号 | 实现工作包 / 建议文件范围（主 Agent 自行实现或按需委派） | 前置 | 完成物与局部门禁 |
|---|---|---|---|
| **M4-01** | 现有 `service/review`、`service/pr`、`adapters/reviewer`、Git/Workspace、M2 Collaboration、M3 Scheduler/Session/Admission 审计；核实产品运行时能力、事务边界、路径 Reservation 和恢复 ADR | 已有 M3 本地 commit | `m4-architecture-review.md`、ADR、能力复用矩阵、产品运行时协同与恢复方案；不修改生产代码 |
| **M4-02** | `domain/delivery*.go`、`ports/quality*.go`、`storage/sqlite/queries/quality*.sql`、新迁移及登记、`gen/`、新增 Store；持久化 Path Reservation 与 M3 reserve/claim 事务接点（`scheduler_admission_store` 唯一 Owner） | 01 | Artifact/Attempt/Repo 指纹、状态 CAS、Git Snapshot、原子多 Repo 路径 Reservation；迁移 up/down/upgrade、路径前缀冲突、重启恢复定向测试 |
| **M4-03** | `service/qualitygate`、`adapters/testgate`；与 M3 Scheduler 做最小协调接线；共享 `scheduler_admission_store` 仍由 02 修改，不创建第二个 Scheduler | 02 | G1/G2/G3/G4 规则执行和条件触发、GateFingerprint 失效；Ready Task 仅经 M3 Admission/Claim/Spawn；状态投影与路径冲突阻塞 |
| **M4-04** | `service/qualityreview` + 现有 review/collaboration 最小适配；复用 M2 Collaboration 的 question/result/handoff/TaskRequest | 02 | independent Review 绑定 Artifact SHA；结构化消息、身份派生、阻塞升级、有界修复；禁止 Agent 自批 TaskRequest |
| **M4-05** | `adapters/workspace/git...` 新受限 Git 原语、`service/taskcommit` | 03+04 | 任务独立分支 allowlisted 文件提交、重复/漂移拒绝、保留其他 dirty 文件；真实 temp Git 测试 |
| **M4-06** | `service/integration` 预演、专用 Integration Worktree、逐仓 conflict plan | 05 | 多 Repo Git preview 不更新目标 ref；冲突/脏工作区/相同历史换仓反例 |
| **M4-07** | 接续 06 独占 `service/integration`/Git 文件；apply、`service/principal` 必要审批适配、部分失败/重启对账；共享 Port 交 02 | 06 | 未批拒绝，按 Repo+SHA+PreviewDigest 审批，本地 merge 串行，单独 Push 授权；双并发反例 |
| **M4-08** | `httpd/controllers/workflow_quality*.go`、`daemon/quality_wiring.go`、`cli/quality.go`、CDC/API；新增只读 Coordination Summary | 05+07 | 权限+幂等、脱敏、Operator 一次性审批、任务/Agent 状态/Lease/消息/路径冲突摘要；OpenAPI/TS 唯一生成 Owner 为 08；SQLC 仍交 02 |
| **M4-09** | QA Owner：`m4*_test.go`、集成 E2E/负向/Race、`m4-report.md` | 08 | `m4-acceptance.md` 仅主控独立判断和编写；主控亲自运行关键反例并确认无新 P0/P1 |

以上路径为建议，不得和实际已存在路径冲突。无论由主 Agent 还是按需委派的 Astra 子代理实现，开工前都必须读取相关代码并建议最小差异；M4-02 统一维护 migrations，后续新增表先由对应文件 Owner 修改，避免多人同时改动相同查询文件。

## 4. 正式四级测试门禁及频率

| 级别 | 触发 | 允许的最小检查 | 不执行什么 |
|---|---|---|---|
| **G1 变更自测** | 每个实现工作包或模块完成 | 改动包单元测试、`git diff --check`、必要格式检查、关键路径范围负例 | 不跑整个 M2/M3 E2E、全仓 race |
| **G2 交互集成** | 变动跨模块契约/DB/HTTP/Git 接线 | 仅被触达链路端到端、迁移、API/SQLC 漂移和 TypeScript（**实际有改才触发**） | 不在每次小修复后重跑全部包 |
| **G3 安全反例** | Git 写/Operator/ReviewSHA/自动 Commit/跨仓 Partial/恢复变化 | 强制受影响安全反向矩阵；并发 race 选中关键包；保留真实前后红绿证据 | 不用“子 Agent 自测通过”代替 |
| **G4 阶段独立验收** | 全部任务完成，一次为主 | 完整 M4 Fake E2E + M2/M3 **受影响回归**、Build/Vet、关键 Race、必要生成漂移和有限 Windows 基线对照 | 非发布场景不默认要求全前端 10k+ / 全 Go tests 重复运行 |

**Gate 最终状态要求：** 新增 P0/P1 必须关闭。任何必须 Rule 缺失工具、超时、FAIL、UNVERIFIED、异常 SKIPPED 都阻断 PASS，不得通过删用例或放宽断言解决。测试策略变更须留下 policy revision 与理由。基于完整 GateFingerprint 复用结果，不能复用旧 Git SHA/旧命令环境的 PASS。第三方 CI/完整全仓回归在发布/跨公共核心大变更/用户要求时追加，而非日常重复。

## 5. 分阶段精确验收点

### M4-01 / G0：只读准备

- 记录 `git status`、`git log -1`，确认 M3 基线是 `129e13c`、M2 离线 G0/M3 G7 已通过，且工作区没有未授权新增 dirty。
- 复核现实的 PR/Review 实现，决定能共享的 Reviewer/SCM 部分与不能混用的审批动作。
- 冻结四份 ADR：Git preflight+预演策略、Runner 命令允许列表/OS 边界、Review 结果质量、跨仓部分成功及独立 Push 审批。
- 输出旧 Domain 2 条 Handoff Note 基线失败；不自作主张修与 M4 无关的历史问题。

### M4-02 / G1：持久工件

- Snapshot 绑定 Attempt+Session+RepoKey+ProjectID+真实 Git 共通目录+HEAD+branch+diff+allowed paths。
- SQLite migration 使用新增文件并登记 shippedMigrations；Store 事务 CAS、幂等、崩溃重启恢复。
- 一个 Attempt 多仓分别签发证据；缺行、换库、symlink/path traversal、分支漂移 fail closed。
- 测试：真实两个临时 Git 仓库 + SQLite Close/Open + Worktree dirty 内容不被修改。

### M4-03 / G1-G2：Gate Runner

- Rule 明确 argv、cwd/env allowlist、timeout、最大日志量/哈希、触发条件和风险分级。
- TestGate 只接受可信配置命令，不执行任何未经审核的 Agent Shell 原文。
- `PASS/FAIL/BLOCKED/UNVERIFIED/SKIPPED` 真实区分；未安装工具链绝不能为 PASS。
- 变动后命令/依赖/源码/环境哈希不一致必须重新跑相应 Gate。
- 逐项用小型 Fake Command、真实本地退出码和超时测试证明。

### M4-04 / G1-G2：Reviewer & Fix

- Review 绑定真实 Artifact+Commit/TargetSHA；Reviewer 与修复者身份不同。
- 模型评论只能是证据，不是进程退出码；缺合法审查身份时要人工签。
- `changes_requested` 时派生受限 FixRequest，不重启不明副作用 Session；最多 2 次自动修复（策略授权最多 3）。
- 每次修复后 ArtifactRevision 改变，旧 Gate/Review 必须 stale。

### M4-05 / G1-G3：任务分支本地提交

- 验证白名单路径、staged 内容、Worktree/git common-dir、Branch/HEAD 真实匹配。
- 本地 Commit 只允许受控 Task Branch，无其他 Repo 或目标 Branch 写入。
- 不执行 `git add -A`、`reset --hard`、`clean`、无审查 Hook。
- Commit 结果不确定先检查 Git ref/commit，不另开一个相同副作用。

### M4-06 / G2-G3：隔离集成预演

- Dedicated Integration Worktree 按 RepoKey 建对象；预演不得更改现有目标分支。
- 每 Repo 返回 `target_sha`、`task_commit_sha`、`preview_digest`、冲突、预计变更摘要；全部完整才可申请批准。
- target HEAD 漂移或 Git 冲突 => Preview stale/blocked，不继续合并。

### M4-07 / G3：合并审批与多仓恢复

- 已有可信 Operator 授权必须与整个 Plan 精确字段绑定；拒绝审批重放、错目标、错 Repo、过期、旧 SHA。
- 审批后再次获取实时 Git target SHA，执行本地 `update-ref` 原子 CAS。
- 多仓中途出错不自动回滚/重试；逐仓结果保留，整体 partial/unknown。
- Remote Push 必须再次批准；本期测试只能使用本地 bare origin，不得真实推送。
- 双操作请求竞争、Daemon 进程中断和重启必须有闭锁证据。

### M4-08 / G2-G3：API & CDC

- 仅新增必要服务端路由和 CLI HTTP 客户端；不构建 M5 完整工作台。
- 写 API 统一 `idempotencyKey + revision + request digest`、Operator 按路径/正文一次性批准。
- 事件中无凭据、原始 Shell 或无限 diff；OpenAPI/TS/schema & sqlc 仅源变更时生成并验证。
- 禁止纯 loopback 或 Agent capability 绕过审批。

### M4-09 / G4：独立验收

主 Agent 亲自执行，不得把子 Agent 写的日志当作独立证明：
1. 真实临时 Git + SQLite + HTTP + Scripted/Fake 实现一条 Gate→Review→Task Commit→Preview→Human Approval stub→Local Integration 的完整链路。
2. 一条测试失败/未知阻断反例、一条 Review 旧 SHA 反例、一条无审批 Merge/Push 反例、一条目标 SHA 漂移、一条多 Repo partial+restart，一条双 Operator 并发。
3. 定向 M2 F-03 Git、M3 Admission/UNKNOWN/Lease/Operator 回归，新增 race 只对真正并发触达的包。
4. `go build ./...` / `go vet ./...`；新增 migrations/schema/API 后才做 `npm run sqlc/api/frontend:typecheck`。
5. 对尚未运行的真实 Provider、原生人工 UI、远程发布、其他 OS 标为 NOT_TESTED；不得宣称全产品 PASS。

建议命令（按最终实际包名调整，不存在时不得假装已执行）：

```powershell
# backend
go test ./internal/service/qualitygate ./internal/service/qualityreview ./internal/service/taskcommit ./internal/service/integration -count=1
go test ./internal/integration -run 'TestM4' -count=1 -timeout 30m
go test -race ./internal/integration -run 'TestM4' -count=1 -timeout 30m
go test ./internal/storage/sqlite/store -run 'Test.*(Gate|Artifact|Integration|Review)' -count=1
go build ./...
go vet ./...
# 仅在触达对应源码时：npm run sqlc、npm run api、npm run frontend:typecheck
```

## 6. 迁移历史快照（不覆盖当前台账）

本节记录 2026-11-06 迁移时的原生 TaskManager 状态，**不是当前 M4 阶段状态，也不是当前实施指令**。当前状态以 [m4-execution-ledger.md](m4-execution-ledger.md)、[m4-report.md](m4-report.md) 和 [m4-acceptance.md](m4-acceptance.md) 顶部为准。

| 包 | 状态 |
|---|---|
| M4-01 | 原生 `in_progress`；Turing completed/HANDOFF；120 行审查草稿已落盘，IN_PROGRESS/NOT_ACCEPTED；四 ADR 未落盘，接口/事务边界未冻结 |
| M4-02～M4-09 | 原生 `pending`；未来 Agent/Worktree 均未创建 |
| 实施任务 `tsk_e36725bd9a1f5cf8` | `blocked` / `USER_APPROVAL_REQUIRED`；9 个原 step ID/状态/history 保留，ACCEPTED=0 |
| 迁移行政任务 `tsk_5690f562ed17fe19` | 主控 final_review pass（`rev_83df0ccb5afa5a87`），complete 成功；`inventory/mapping/documents/verify_pause` 均 completed |
| M4 生产源码 | **未修改** |
| M3 Git 提交 | `129e13ca5d8df210cdafcc2e061d521109130e27` |
| M0-05 真实 Provider | **仍 BLOCKED / 未批准** |

完整新旧映射、依赖、Owner、证据及原生状态能力限制见 [执行台账](m4-execution-ledger.md)；十项迁移结果见 [迁移报告](m4-migration-report.md)。当前无可派发编码任务，迁移不等待 M4-01 完成；恢复须用户批准，且后继必须等待 01 独立验收。
最终 M4 共 4 条：3 completed 文档行政类+1 blocked 实施类；01 HANDOFF 未验收、02～09 pending，ACCEPTED=0。主控验证日志 `C:/Users/11459/AppData/Local/Temp/ao-m4-20261105-main/migration-validation.log` / `migration-validation.exit`（0）；本次仅回填终态后停止。

实施方创建 `docs/customization/m4-report.md`，独立主 Agent 创建 `docs/customization/m4-acceptance.md`。只有实际源码/数据库/安全测试、证据和主 Agent 独立验收全部满足，才允许标记 M4 PASS。不得私自提交、推送或合并；用户本次只授权**已完成的 M3 提交**，并未授权后续 M4 代码自动 commit。

## 7. V0.3 产品运行时协同与开发期自主执行补充

完整协议见 [m4-coordination-protocol.md](m4-coordination-protocol.md)，与本计划冲突时，以更严格的权限、持久化、原子性和 UNKNOWN/Fencing 要求为准。

### 7.1 产品运行时任务调度与生命周期

- **M3 Scheduler 是产品运行时唯一调度器**；M4 只做 DAG 计划、质量门禁、路径 Reservation 和只读协调投影，不新建第二套调度器/Attempt/Session/消息系统。
- WorkflowTask/WorkflowEdge 是唯一 DAG；M2 Collaboration 负责消息/TaskRequest；M3 Admission/Claim/Lease/Execution/SessionManager 负责真正派发和运行。
- 主 Agent 维护 DAG，动态派发 Ready Task；默认并发 3、硬上限 10。依赖未完成、预算不足、模型不可用、路径冲突时排队或 BLOCKED，不预先 Spawn。
- 产品运行状态从现有 Task/Attempt/Session 派生，不再建立一份互相矛盾的可写状态机。Progress 消息不能续 Lease；Session Exited 不代表 Task 成功。
- TaskRequest 只能由 Operator 决策后经 M3 原子物化；Agent 不能自己增删 Task/Edge 或提升权限。

### 7.2 M4 开发期自主执行与按需委派

- 主 Agent 可以独立完成任何 M4 工作包，也可以自行决定是否委派子代理；不强制真实 Spawn，不强制 AgentDock/ACP，不因缺少子代理工具而 BLOCKED。
- **凡是实际调用子代理，必须显式使用 Astra（当前已知模型标识 `gpt-6-astra`）。** 如果不能确认 Astra，主 Agent 自行完成该部分，不静默切换其他模型。
- 不规定开发子代理数量、并发上限、固定调度循环或心跳频率；是否并行由主 Agent 根据依赖、风险、工具能力和文件冲突决定。
- Worktree、文件范围和接口契约按实际风险设置；并行编辑同一迁移、SQL Query、公共接口或接线文件时应避免覆盖，串行单人工作不强制建 Worktree。
- AgentDock Task Manager、ACP、执行账本均不是每轮必调工具。不得为了心跳、进度打卡或重复同步状态而高频调用 AgentDock；只有具体工作确实需要时才调用。
- 子代理交付（如有）须说明改动文件、当前 HEAD/Diff、测试命令和退出码、遗留问题及未提交状态；主 Agent 独立检查关键 Diff 和测试后再采纳。

### 7.3 进度记录与恢复（轻量、按需）

- 已有 [m4-execution-ledger.md](m4-execution-ledger.md) 保留本轮增量迁移的历史映射、原任务状态和证据，不新建第二份任务账本。
- Ledger 仅在阶段状态、交付、阻塞、验收或授权发生实质变化时更新；不记录每条命令、每轮思考、每次轮询或心跳。
- 不要求宿主 Task Manager 与 Ledger 双写，不要求每次执行 M4 都调用 AgentDock Task Manager。若具体环境已有合适的任务管理能力，可由主 Agent 自行判断是否使用；不使用也不构成阻断。
- 恢复时先检查实际工作区、Git 状态、未提交 Diff、已有报告及仍在运行的 Agent/Session（仅当本次工作确实使用子代理时）；确认事实后继续，不因聊天上下文丢失而重做已有成果。
- 保留原 M4-01～09 step ID、旧任务映射和迁移证据；不手工修改宿主任务底层状态文件。

### 7.4 技术依赖与实施顺序（不强制开发并行）

- M4-01：先完成只读审计、能力盘点、ADR 和接口/事务边界确认。
- M4-02：依赖 01 的设计与事务边界结论；数据库、Artifact、Reservation 的变更需保持一致。
- M4-03、M4-04：均依赖 02 的基础模型和接口冻结。主 Agent 可选择串行或并行实现，不要求为并行而派发子代理。
- M4-05：必须等待 Gate 与 Review 的实际能力可用并通过验证。
- M4-06：必须依赖任务分支 Commit 的可信结果；预演阶段不得修改目标 Ref。
- M4-07：必须依赖有效 Preview 和精确审批；多仓库部分失败与 UNKNOWN 必须可恢复。
- M4-08：依赖 05、07 的稳定服务契约；Controller/Daemon/DTO/specgen/OpenAPI/schema 和 SQLC 生成按实际变更统一接线，避免多人覆盖。
- M4-09：全部实现工作交付后进行综合验收；各阶段仍需在推进前确认其关键依赖和安全证据，不能把 HANDOFF 直接视为 ACCEPTED。

以上是技术顺序，不要求用宿主任务管理器创建 DAG，也不规定开发期 Agent 数量或并行度。

### 7.5 G1-G4 测试与产品运行时协同专项门禁

保留第 4/5 节四级精简门禁。以下测试验证的是 AO **产品运行时**协同能力，不是要求 M4 开发阶段使用多 Agent；按影响触发，集中在 G3/G4，不在每个工作包重复：
- DAG 依赖与双 Scheduler 唯一 Claim；
- 默认 3 / 上限 10、Adapter 并发、预算和公平性；
- AllowedPaths 重叠、跨 Repo Reservation 原子性、旧 Epoch 释放锁拒绝；
- Session 无输出但存活、Lease 到期、Spawn UNKNOWN、主 Agent 重启 Reattach；
- Sender 身份伪造、Agent 自批 TaskRequest、重复消息和 UNKNOWN 消息不盲重发；
- 指定模型缺失/协议不兼容时 BLOCKED、不静默替换；
- 子 Agent 部分完成/部分失败时只恢复失败分支，已验收 Artifact 不重复开发。
