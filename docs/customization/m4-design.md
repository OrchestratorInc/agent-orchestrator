# M4 详细设计 V0.3：自主实施方式、产品运行时协同、代码质量 Gate、审查、集成预演与人工审批

> 当前授权（2026-11-06）：用户已明确恢复 M4 实施；M4-01～09 已完成主控实现和 G4 验证，实际源码与验收进度见 [执行台账](m4-execution-ledger.md) 与 [主控验收](m4-acceptance.md)。M0-05 与高风险操作边界未改变，历史迁移见 [迁移报告](m4-migration-report.md)。

> 日期：2026-11-05；项目 `E:\code\agent-orchestrator`；基线 `129e13ca5d8df210cdafcc2e061d521109130e27`（M3 已提交），分支 `feat/multi-agent-desktop`。
>
> **设计状态：已实现并完成主控 G4；整体等待统筹终验。** M3 的 Windows 离线 G7 已通过，但真实 Provider、原生 Windows Hello、全桌面/安装包及 M0-05 仍未验证/批准。本设计不授权合并目标分支、远程推送、真实外部 Provider 或真实 AO 用户数据读写。

## 1. 目标及决定

来自 `requirements.md` Q5：每个写代码的 Task Attempt 绑定独立 Worktree；自动检查变更范围、运行允许的测试、代码审查和有界修复；**通过 Gate 后允许自动提交任务分支**；**合并进目标分支、远程 Push 及危险动作必须用户批准**。多仓库要独立审查和记录，不能把局部成功说成全局成功。

M4 定义如下流程：

```text
M3 的 Attempt / Session / 真实 Worktree
      │  可靠 Worker 产物事件（不以 Agent 文本为证据）
      v
1. Freeze/Inspect Artifact  →  2. TestGate（按风险选命令）
      │                          │
      │<──── FAIL / BLOCKED ──────┤
      v                          v
3. Independent Review  ←── 含退出码和环境指纹的 GateReport
      │
      ├── CHANGES_REQUESTED → 有界修复（新 ArtifactRevision，重新 Gate）
      ├── UNVERIFIED/BLOCKED → 人工裁决，不得签发 PASS
      └── PASS → 再核验 Git/指纹 → Task Worktree 本地提交
                                    │
                                    v
                 4. Integration Worktree 内预演冲突/差异
                                    │
                                    v
                 5. 人工 Operator 审批（逐 Repo/目标 SHA）
                                    │
                                    v
                 6. 重新查实际 refs → 串行本地集成
                                    │
                                    v
                 7. 记录每仓证据/部分成功并按需人工恢复
                                    │
                Remote Push：单独审批/单独执行，绝不继承 Merge 审批
```

**范围内：** 任务 Git 工件、可复现门禁、不同 Reviewer 审核、有界修复、任务分支本地 Commit、隔离的目标分支集成预演、独立用户审批、串行本地集成、可恢复操作账本、最小 CLI/HTTP/CDC、Fake+真实临时 Git 的安全测试。

**范围外：** M5 的完整双模式 Workbench UI；M6 的实机发布/签名/升级；真实 GitHub/外部仓库推送实测（用本地 bare remote 模拟）；跨仓原子 Git 事务；不经用户批准的自动目标分支 Merge/Push/PR；将 Worktree 冒充 Windows OS 沙箱。

## 2. 现有源码的复用与缺口

| 真实能力与位置 | 已有 | M4 处理 |
|---|---|---|
| `service/workflow` / M3 Scheduler / Budget | Attempt、Session 准入/任期、人工决策、额度恢复 | 只追加 delivery 阶段；不重写任务 State/Attempt/预算 |
| `session_manager/multi_repo_workspace.go`、`service/execution` | F-03 1..N Repo 真实 Git/Worktree、Generation/ExecutionPermit | 每个 Gate/Commit/Integration 前重验同一身份；冻结当次观察和 Evidence |
| `service/review/review.go` + `internal/review` | 已有 ReviewRun、独立 Reviewer 触发/提交/恢复 | 复用 Review Engine、ReviewVerdict、运行形态；新增**任务 Artifact SHA 绑定**，不得复用与 PR SHA 无关旧结论 |
| `adapters/reviewer/registry.go`、`ports/reviewer.go` | Reviewer 适配器/Runner 接口 | 同一个身份可在产品内 Reviewer 模式使用，但零真实 Provider 下只跑 Fake/静态/人工模式 |
| `service/pr/action_service.go`、`ports/scm_actions.go` | PR 合并前重新 Fetch/校验 head、SCM CAS | 不再写第二套 PR Client；M4 本地目标分支的审批/集成与远程 PR 操作独立建账 |
| `storage/sqlite/queries/{review,pr,pr_actions}.sql` | PR/Review 记录 | 新增 Workflow Gate/Integration 表，关联现有表的稳定 ID，而非挪动旧事件 |
| `httpd/controllers/operator_approvals.go` + `service/principal` | 单次凭据、原始正文/方法/路径 Digest、OS Consent | Commit 策略独立；Merge 和 Push 按精确 Git 目标和不可变计划审批、不可互用 |
| `adapters/workspace/gitworktree` + Git 状态服务 | Workspace/Git 创建、查询 | 新的受限 Git 操作只使用受审查 argv，禁止接受任意用户 Shell 当作可信 Git |
| `change_log → CDC → SSE`、OpenAPI / SQLC | 推送、类型生成 | 新增 Git/Quality 状态事件；不输出秘密和完整任意文件内容 |

**先审计实际源码能力**，不要把 GitHub PR Merge 与本地 branch integration 混同；缺少安全可复用原语时新增最小 ports/adapter。

## 3. 核心对象、不可变工件与指纹

### 3.1 ArtifactSnapshot

每 Repo 独立记录：
- `workflow_id/task_id/attempt_id/session_id/generation`、`repo_key/project_id`；
- `repo_path/worktree_path/git_common_dir`（只存服务端；API 使用无敏感路径引用）；
- `base_sha/head_sha/branch/dirty_tracked_digest/untracked_digest`；
- `allowlisted_paths_digest`、`diff_digest`、`artifact_revision`、`created_at`；
- `snapshot_quality` = `verified/unknown/invalid`。

取 Snapshot 必须实时验证：Worktree 属于明确绑定 Repo、当前 HEAD/branch 历史合法、`git status --porcelain -z` 可读取、变更路径在范围内；符号链接、工作区外路径、重命名和子模块必须遵守显式安全策略。二进制/未追踪文件进入签名清单（不将其原文发给 Reviewer 之外的对象）。检查前后再核对，TOCTOU 不可当作 OS 原子承诺。

一个 Artifact 可关联多 Repo，但**每 Repo 的 SHA、证据和 Gate 结果单独存在**。任一 Repo 不合格则总 Gate 不通过。

### 3.2 GateFingerprint

建议：
```text
sha256(
  repo_key + artifact_revision + base_sha + head_sha + diff_digest
  + allowed_paths_digest + command_policy_revision
  + test_argv/env_allowlist_hash/toolchain/os_arch_hash + dependency_lock_hash
)
```
只有**全部指纹一致且未过有效期**、测试产物和命令可信时才允许复用同一 GateReport。改代码、改工具链/依赖、改 Gate 配置、改仓库身份都必须失效旧 PASS；不允许重用上一 Attempt 的通过结论。

## 4. 质量状态机与事务

不要直接覆盖 M1 `WorkflowTaskState` 或 M3 `Admission`；质量交付是独立侧车状态：
- `discovered → snapshot_verified → testing → review_pending → review_running → gate_passed → committed → integration_planned → awaiting_approval → integrating → integrated`
- 各阶段可 `failed / blocked / unverified / stale / recovery_required`；`cancelled` 为显式终态。
- Git 操作状态另存 `prepared → requested → applied / rejected / unknown`，不能把 HTTP 超时自动判成未执行。

进入下一阶段必须满足当前 `artifact_revision`、`gate_policy_revision`、`repo_head_sha` 与数据库 `revision` 匹配。异步 Reviewer 旧结果、旧 test runner 回执不得推进新 Artifact。M3 Attempt 是否已结算依旧由其独立授权机制决定；质量 PASS 不直接变更 Attempt 成功状态，也不释放未知预算。

## 5. 测试 Gate：四级简化，不牺牲安全

此前 M3 测试门禁重复太多，M4 正式采用**按变更风险触发、结果复用**，只有下列 4 个 Gate 层级。

| 等级 | 何时运行 | 做什么 | 是否必须 |
|---|---|---|---|
| **G1 Change** | 每个实现工作包或模块完成 | 本包/受影响单元测试 + `git diff --check` + 文件权限/路径变更检查 | 必须；不跑全仓 |
| **G2 Integration** | 改跨模块接口或进入集成候选 | 仅真实触达的 SQLite/API/Workflow/Review/Git 链路集成；生成检查仅当源变更 | 条件触发 |
| **G3 Security** | 涉及 Git 写、审批、凭据、恢复、执行代际或其最终合并 | 一次专用负向矩阵：旧 SHA、撤销审批、错误 repo、dirty、未知结果、双执行者、命令注入和部分仓失败；并发触发 Race | 必须覆盖受影响安全边界；不每个 Task 重跑 |
| **G4 Stage Acceptance** | M4 完成、独立验收者复核 | M4 E2E（含 M2/M3 被触达回归）+ 关键 Race + Build/Vet + Git/SQLC/OpenAPI/TypeScript **条件漂移检查** | 一轮阶段最终门禁 |

**去重规则：** 同一 `GateFingerprint` 的可重放通过结论可复用；执行器实际修改代码则只重跑受影响 G1 和被动失效的 G2/G3；主 Agent 不将旧源码上的 Race 或 Review 当成本次 PASS。全仓 `go test ./...`、全前端 Vitest、真实 CI/安装包**不作为每任务或普通 M4 提交的强制门禁**；上线前/大范围重构/用户请求时单独触发。对受 M4 修改影响的高风险路径必须执行反向/静态验证，不能因为缩减门禁而隐藏 P0/P1。

### 5.1 Gate Rule & Runner

- 每个 Rule 仅包含受信的 argv 数组、允许的 cwd scope、环境变量允许列表、超时/输出上限、触发条件和 policy revision；禁止无审查的 `powershell -Command`、`cmd /c`、Shell 拼接和路径逃逸。
- 运行于明确 Workspace，使用子进程树管理、超时取消，记录精确退出码、开始结束 UTC、工具链版本、截断标记、stderr 摘要、log digest、Git snapshot digest。
- 状态：`PASS / FAIL / BLOCKED / UNVERIFIED / SKIPPED`。必选 Rule 为 BLOCKED、UNVERIFIED 或异常 SKIPPED 时**总 Gate 不得 PASS**；正常 Windows 条件不适用项需规则化标注理由，不得被记作通过。
- 外部脚本及测试可产生副作用，不将“在 Worktree 里运行”声明为沙箱；危险命令在执行前经权限策略审批或默认拒绝。禁止访问真实用户 AO 数据、真实 Provider 凭证、`.codex/.claude` 的设置写入。
- 前端未变不强制全量前端测试；源 SQL/API 变化分别运行 `npm run sqlc` / `npm run api` 并比较生成物；失败即 Gate BLOCKED/FAIL，不能当 PASS。
- 预期历史 FAIL 必须用冻结 HEAD 对照归因，不可依靠模型文本陈述忽略；本基线两条 Domain Handoff Note 为开放历史问题，不归为新增 M4 P1。

## 6. Review 及有界修复

Review 必须由非提交代码的 Reviewer/主控完成，关联 `artifact_digest + target_head_sha + gate_policy_revision + review_run_id`，结构化输出文件/行、严重度、依据、审查范围、结论。Reviewer 的“PASS”不能替代 Gate 的真实退出码；无可信 Reviewer 或缺需审计路径时置 `UNVERIFIED`，可人工独立签署，但必须记录具体授权。

失败/ChangeRequested 自动生成受限 FixRequest（仅允许原 Task Worktree 与文件范围），总自动修复循环默认 **2 次**，最多由 Operator 调整至 **3 次**；任何修复后更新 ArtifactRevision，重新适用 G1 与已失效的 G2/G3。超限、工作区漂移、预算不足或现存 Attempt UNKNOWN，进入 `blocked` 等人工裁决，不重复启动 Provider。

## 7. 本地任务分支 Commit

**自动提交仅限该 Attempt 自己的任务分支；不向目标集成分支写入。**

前提：
- 已稳定通过 Gate、独立 Review 和允许范围检查；最后一瞬再次确认 Worktree/Repo/Git HEAD/branch、staged files、diff digest、Task ownership 和有效权限。
- 对多仓 RepoKey 逐仓记录提交；允许部分 Repo 成功，但聚合结果是 `partial` 并阻断后续集成，直至人工补偿/确认。
- 不使用 `git add -A` 直接提交用户改动。仅对 Snapshot 所列允许文件暂存/提交；校验暂存内容 hash 与允许清单；不接受未跟踪密钥/临时文件；保留已有 dirty 内容，禁止 reset/clean/stash -u 自行清理。
- 使用受限 argv 构造 Git Command，不执行 Agent 生成的任意脚本或 Git hook（hook 策略必须明确且不靠“可能执行”来通过审计）。
- Commit Author/Message 来源、scope、签名策略可配置；作者身份及签名失败按 `blocked` 处理，不改全局 Git config。
- Git commit 后真实读取 HEAD/树指纹，确保写入对应 RepoKey 和分支；生成不可变 CommitArtifact（commitSHA、parentSHA、treeSHA、source Snapshot ID）。

## 8. 集成预演与人工审批

### 8.1 Preview

在专用 **Integration Worktree**、隔离的本地临时分支中，对目标分支当前 SHA 做 preview（merge/cherry-pick 策略在 ADR 固定，默认非破坏性 merge-tree 或隔离 Worktree 实验）。不得在预演期间更新目标 ref 或远程。生成每 Repo：
- `target_repo_key`、`target_ref`、`expected_target_sha`、`task_commit_sha`、`merge_method`；
- 冲突文件列表、预期新 tree/commit、Git 策略检查和 Diff Digest；
- `preview_status = clean/conflict/unknown`、证据 digest、生成时间。
- 多 Repo 的一份聚合 Plan 中，各 Repo 的预演结果需分别成功；一仓失败不得显示“全部可合并”。

### 8.2 Approval

只由已有 M2/M3 可信 Operator 一次性审批链发起和消费，批准内容绑定：
`workflow_id/integration_plan_id/policy_version/ordered_repo_entries(repo_key,target_ref,expected_target_sha,task_commit_sha,preview_digest,merge_method)/action/expiry/request_body_digest`。

- **提交任务分支**可自动执行；**目标集成分支 Merge 必须人工批准**；**远程 Push 必须再次独立批准**，不得把 merge 审批用于 push；更新任何 target SHA/preview/repo 集合后批准失效。
- 不允许 Agent/capability、普通 loopback 或长期文件 token 自行替代 OS 人工动作。系统级权限/删除等仍需各自批准。
- Approve 不等于立即成功；执行前再查目标 Repo/branch/current HEAD、Worktree、权限和有效 gate，签名身份/epoch/revision，任一漂移 fail-closed。

### 8.3 Apply 与重启

串行处理各 Repo，每步都有持久化操作 ID、意图 CAS 和阶段；对于本地目标分支采用 Git 对象安全写入及原子 `update-ref <ref> <newSHA> <expectedOldSHA>`（具体实现由真实 Git 适配层决定），不能不检查旧 SHA 直接覆盖分支。

- 崩溃/操作超时：重启先查实际 ref/SHA、Git 操作日志与记录；不确定则 `unknown`，**绝不重新自动执行另一轮相同 Merge/Push**。
- 多仓没有跨仓原子事务：前两仓成功、第三仓失败时保留已完成 refs、标记 `partial/recovery_required`，阻止下一阶段，呈现每仓具体结果交人工决定继续/补偿；不自动回滚用户 Git。
- Push 意图使用同样强认证+独立审批，必须配合远程 provider/SCM expected SHA；若仅有盲 push API，则默认阻断或要求另外的安全 ADR。M4 不执行对真实远程的 Push 测试，只能用本地 bare remote 模拟。
- GitHub PR 现有 `service/pr/action_service.go` 的 merge guard 只用作现有 PR 路径，不等于 M4 本地集成权限；不得重复发明第二个 PR observer/poller。

## 9. 持久化草案

新建 append-only Goose migrations，SQLC 生成，不改历史表/迁移：

| 表（拟议） | 关键字段 / 唯一约束 |
|---|---|
| `workflow_artifact_snapshots` | (attempt_id,repo_key,artifact_revision) UNIQUE；base/head/tree/diff/status/allowed_path_digest |
| `workflow_gate_policies` | scope/namespace/revision、规则 JSON digest、批准操作者 |
| `workflow_gate_runs` | (artifact_id, gate_fingerprint, run_kind) UNIQUE、argv_hash、exit_code、quality、status、log_digest |
| `workflow_review_decisions` | artifact_id、review_run_id、reviewer_identity、target_sha、verdict、evidence_digest；约束旧 Verdict 不被复用 |
| `workflow_fix_cycles` | artifact_id、round、fix_request_id、last_verdict、状态、审计 |
| `workflow_task_commits` | (attempt_id,repo_key,artifact_revision) UNIQUE、commit/parent/treeSHA、worktree_ref |
| `workflow_task_path_reservations`（拟议） | repo_key + 规范化路径范围 + active owner；绑定 task/attempt/session/generation/epoch/lease/revision；前缀重叠检查必须在同一事务完成 |
| `workflow_integration_plans` | plan_id、workflow_id、revision、entries_digest、status、operator_approval_ref |
| `workflow_integration_repo_ops` | (plan_id,repo_key) UNIQUE、expected/observed target SHA、request_id、status、Git结果 |
| `workflow_push_intents` | repo_key、remote/ref、expected_remote_sha、独立批准ID、状态；M4本机模拟，不真实发远程 |

质量事件使用已有 `change_log/CDC` 触发器；审批签名/原文凭据保留在可信内存/受控持久化摘要，不向 Agent、MCP、普通 HTTP GET 输出原始令牌。所有状态变更需 revision CAS、幂等、详细失败原因。注意现有 Review/PR 记录的唯一 SHA 约束，不能重复创建语义相同但关联错误的审查。

## 10. 最小 API/CLI 提议

以下路径仅为设计，开发前先查重和 OpenAPI：
- `POST /api/v1/workflows/{id}/tasks/{taskId}/artifacts/snapshot`：受信 Executor 或 Operator；取得 Snapshot，不接受自报 Git SHA。
- `POST /api/v1/workflows/{id}/tasks/{taskId}/gates` 与 `GET .../gates`：触发 Gate/列证据（受信控制面，不能由子 Agent 给自己签 PASS）。
- `POST /api/v1/workflows/{id}/tasks/{taskId}/reviews`：独立 Reviewer / Operator 申请，沿现有 Review Engine。
- `POST /api/v1/workflows/{id}/tasks/{taskId}/commit`：仅写 Attempt Task Branch，需 Gate PASS / Operator 策略授权。
- `POST /api/v1/workflows/{id}/integrations/preview`、`GET .../integrations/{planId}`：本地 Git 预演/逐仓结果。
- `POST /api/v1/workflows/{id}/integrations/{planId}/apply`：只能使用**该请求完全一致**的已消费 Operator Approval。
- `POST /api/v1/workflows/{id}/integrations/{planId}/push`：独立 Push Approval；真实远程默认禁用。
- `GET /api/v1/workflows/{id}/quality-summary`：只读 Gate/Review/Commit/Integration 进展，脱敏。

CLI 仅通过 daemon API；不存在 CLI 直连 SQLite 或 Git，不能绕过 Operator。写接口支持 IdempotencyKey、revision 和 RequestDigest，且审批动作只绑定特定路径；M5 再完成界面。

## 11. 关键 P0/P1 验收矩阵

| 必须证明的情况 | 正确行为 |
|---|---|
| 工作树切库、路径逃逸、symlink、HEAD/branch 漂移 | Gate / Commit / Preview 拒绝且不破坏原工作区 |
| 测试命令非零、超时、未安装工具链、假 JSON 的 `PASS` | Gate FAIL/BLOCKED，不能交付 |
| 测试通过后又修改文件、Review SHA 变了 | 原 Gate/Review 变 stale，禁止 Commit/Integration |
| 两个 Worker 同时 Commit 同 Task、两个 Operator 同时 Apply | 唯一结果、CAS 冲突；不出现双重写目标 |
| 没批准 Merge、错路径/请求摘要、旧审批重放、跨仓扩大 Repo 列表 | 禁止目标 ref 更新 |
| 已批准但 target_ref 从 SHA A 变 B | 全计划拒绝或重审；不能继续按旧批准合并 |
| 多仓 2/3 成功后崩溃 | 每仓状态精确，聚合 partial/unknown，不伪造成功或盲重试 |
| 只有 Merge Approval，尝试远程 Push | 拒绝；仅单独 Push Approval 才可继续，真实远程仍默认禁用 |
| M3 UNKNOWN/预算不足/旧 epoch | 不取得新执行/Reviewer 权限；保留占位 |
| 旧 M2/F-03 Repo 实时核验、M3 独立 Agent/Operator 物化 | 回归通过，不破坏已有 Session/queue/预算 |

## 12. M4 准入、退出与风险

准入：M3 提交 `129e13c` 干净，可用于 M4 基线；M3 离线 G7/ M2 离线 G0 已通过。仍需先记录 `go test ./...` 已有 2 条 Domain Handoff Note 基线失败，不能宣称全仓绿。M0-05 未批，测试只允许本地 Git/SQLite/HTTP/Scripted Fake；子 Agent 作为开发模型的使用不代表产品内真实 Harness 的消耗型测试获批。

退出：M4 新增数据/接口与源码审查通过；四级精简门禁合理触发，真实 Git 与 SQLite 正负路径、Review stale 防护、自动本地 Task Commit、无审批不得 Merge/Push、多仓 Partial 与重启恢复，独立验收全 PASS。所有新增 P0/P1 清零；完整实机 Provider、Windows Hello、GitHub 真实远程、发布 CI 保持 NOT_TESTED 而非 PASS。

**需要在 M4-01 冻结的 ADR：** 目标分支集成采用 merge vs cherry-pick 的预览与对象身份、Git hooks/签名及外部副作用的 allowlist、Reviewer 真伪/来源及审查等效等级、已有 PR Merge 路由如何接入 Operator、跨仓部分成功后恢复语义，以及通过 Gate 自动提交与 M3 Attempt 结算关系。

**M5 交接：** 只读质量摘要、逐 Repo 工件/审查/冲突/审批信息、Git 操作状态及稳定事件；M5 负责完整双模式 UI，不反向控制质量真实性。

## 13. 产品运行时多 Agent 协同控制协议（V0.3）

本节描述 Agent Orchestrator **产品运行时**的多 Agent 协同能力，不规定 M4 开发阶段必须使用多 Agent，也不要求主 Agent 通过 AgentDock、ACP 或宿主 Task Manager 执行开发。M4 开发方式见 [自主执行提示词 V2](m4-agent-prompt.md)：主 Agent 可独立完成，也可按需委派；只有调用子代理时才要求使用 Astra。

产品运行时继续复用 M1/M2/M3 已有 WorkflowTask/WorkflowEdge、service/collaboration、M3 Scheduler/Admission/Lease、Execution Gateway 和 SessionManager。**不得重复实现第二个 DAG、Scheduler、Attempt、Session Registry、消息总线或审批系统。** M4 新增质量交付侧车、持久化路径 Reservation 与只读 Coordination Projection。

详细状态来源、动态 DAG 派发、Agent 生命周期/心跳、Task Dispatch Packet、消息升级/交接、路径所有权、重启恢复、协调摘要 API 和产品运行时反例矩阵，见 [M4 产品运行时协同协议](m4-coordination-protocol.md)。

关键约束：
- M3 Scheduler 是产品运行时唯一派发权威；产品 Agent 不能绕过 Admission 直接 Spawn。
- 任务 DAG 的动态扩展只能走 TaskRequest → Operator 决策 → M3 原子物化。
- 同 Repo 的重叠 AllowedPaths 不得并行写；Reservation 必须持久化、原子获取并绑定 Attempt/Session/Generation/Epoch。UNKNOWN 时不能因 TTL 到期释放。
- 产品 Agent 的 Progress 不是 Lease Heartbeat；Session Exited 不等于 Task Succeeded；Spawn outcome UNKNOWN 不得盲目重派。
- Agent 间消息复用 M2 Collaboration，Sender 由服务端 Attempt Binding 派生；Agent 不可伪装 human、批准请求或提升预算/权限。
- 产品运行时的并发和预算边界由 M3 Scheduler/Workflow 策略决定；这些限制不用于约束 M4 开发期间主 Agent 使用多少子代理。
- 主 Agent 对 M4 代码和阶段结果独立验收；子代理（如使用）的自报 PASS 不能代替证据。
