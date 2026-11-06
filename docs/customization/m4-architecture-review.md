# M4-01 架构审查与独立验收

## 2026-11-06 恢复验收：ACCEPTED（架构阶段）

用户已明确恢复 M4 实施；下方旧草稿及暂停记录作为历史保留。本结论只验收架构与实施约束，不代表业务实现或 M4 总体验收。

主 Agent 重新读取全部七份 M4 文档及项目规范，核对实际 Workflow/Scheduler/Collaboration、TaskRequest 物化、Admission/ExecutionPermit/Binding Store、SessionManager 实时 Git 验证、Review Engine、PR ActionService、OperatorOperations、进程树取消和 CDC。

### 冻结的边界

- 唯一 DAG 为 WorkflowTask/Edge；唯一派发为 `scheduler.Service.Tick → workflow.Claim → Store.ReserveAndClaim`；Execution/SessionManager 持有进程。M4 只添加质量侧车与持久路径 Reservation。
- `ReserveAndClaim` 的 `inSchedulerTx` 先取得 SQLite writer 锁；多 Repo Reservation 从持久 Task/Project scope 派生，与 Claim/Admission/预算同事务。Bind/renew/adopt/release 同步 Reservation，禁止在事务外先检查再写入。
- Artifact 不信任 API 自报路径/SHA。Git 适配器接受服务端 Repo 绑定，返回完整清单与指纹；存储事务重新验证 Attempt/Session/Generation/Fence、持久 Repo scope 和 Artifact revision。Snapshot 不产生 Gate PASS。
- 质量服务使用 Artifact、GateEvidence、ReviewDecision、CommitArtifact、IntegrationPlan/RepoOperation。共享 types 在 `domain/delivery*.go`，SQL/SQLC/Store 单一写入者；不创建第二套任务/会话状态。
- Store 提供保存不可变 Artifact、读取/按 Workflow 列举、质量状态 revision CAS；Gate/Review/操作记录在同一 writer 事务绑定 Artifact fingerprint。所有写接口使用内部 fence，HTTP 不反序列化授权对象。
- 活动期使用 live permit；可信停止后的交付使用专门终止事实校验，拒绝 UNKNOWN/代际或任期漂移。质量 PASS 不 Settle，不释放预算，不重启 Provider。
- Review Engine 必须有 PR，不能直接审核未提交 Artifact；复用 Verdict/适配器和 Collaboration，新增 Artifact 审查入口。旧 PR merge 缺 Workflow 审批，M4 接线必须阻止该旁路。
- 四份 ADR：[Git](m4-adr-git.md)、[Runner](m4-adr-runner.md)、[Review](m4-adr-review.md)、[恢复](m4-adr-recovery.md)。受限 Git、停止后交付、Reservation 生命周期仍须后续代码及反例证明。

### 本轮独立验证

证据目录 `C:/Users/11459/AppData/Local/Temp/ao-m4-resume-20261106-01a1acb1/`。

- HEAD/branch/worktree/status 实查与基线一致，原文档和 Diff 已保全；会话证据见 [核查报告](m4-session-audit.md)。
- `go test ./internal/storage/sqlite/store -run "^TestM3(AdmissionIndependentStoresReplayAndRollback|AdmissionFenceRecoveryAndUnknownResources|ExecutionAcceptanceFencesEpochAndKeepsStableSession|ExecutionUncertainAdmissionCannotAuthorizeActions)$" -count=1 -v`：exit 0，4 PASS，`m401-contracts.log/.exit`。
- 两条原 Handoff Note 定向测试：exit 1，2 FAIL，分别为 `handoff_note_test.go:84`、`:211`；`m401-baseline.log/.exit`。对应文件本轮未修改，保留历史缺陷，不计为 M4 通过。
- `git diff --check` exit 0；四份 ADR 无尾随空白，输出 `ADR_FILES=4 TRAILING_WHITESPACE=0`。

主要风险已转为明确实施门禁；没有以 HANDOFF 代替验收。进入 M4-02，后续阶段仍须逐项核验。

### Astra 只读复核补充

Hume 已交接并关闭，无文件修改；主 Agent 采纳以下接点：Reservation 在 CreateAdmittedSession 的启动接受事务绑定，Reanchor/重附着发布代际同步；已 released Admission 的残留路径占位仍须在原恢复流程对账。停止后交付允许 Admission 已释放；下方历史草稿的 live 要求仅适用于 Worker 活动期。

M1 成功结算会刷新后继 Ready，尚无质量依赖。M4-03 应在现有 Claim 事务为启用质量策略的工作流检查前驱交付，不能只展示侧车状态；未启用 M4 的历史工作流保留原语义。TaskRequest 当前只支持 create_task，修复使用受限子任务和独立物化审批，不声称可原位提权。

发现两项与 M4-04 复用相关的 Collaboration 缺陷：满消息队列在幂等查询前拒绝重放；TaskRequest 重放只比 RequestSHA，缺 requester/kind 比较。进入 M4-04 时按真实反例修复，不扩大到无关消息功能。

## 原 Turing 草稿（历史原文）

状态：**IN_PROGRESS / NOT_ACCEPTED**。本次仅保存已有审计进度并交接，不代表接口已获批准、M4-01 已通过或功能已实现。

- 审查日期：2026-11-05；工作区：`E:/code/agent-orchestrator`，沿用当前 checkout。
- 已核对分支：`feat/multi-agent-desktop`；HEAD：`129e13ca5d8df210cdafcc2e061d521109130e27`。
- 子 Agent：`01a1ac47-6b0a-7823-9626-b6b3ede30e56`；本次明确指定模型：`gpt-6-astra`。用户最新授权覆盖文档旧模型 `cn:deepseek-v4.1-flash`，不构成产品内真实 Provider 验收许可。
- 用户最新范围：仅迁移 M4 任务体系并暂停；本 Agent 保存草稿后 HANDOFF，剩余审计/ADR 迁移为待批准，不启动下游。
- 主控持久任务：`tsk_e36725bd9a1f5cf8`，由主控 checkpoint；本 Agent 未操作 Task Manager、未创建或编辑 Ledger。
- 本次业务代码、测试源码、迁移、配置均零修改；仅新增本草稿。未执行本仓 Commit/Push/Merge/部署、真实 AO 数据访问或真实 Provider 调用。

## 已读取与外部变更

已读取 `AGENTS.md`、`CONTRIBUTING.md`、`requirements.md`、`m3-acceptance.md`、原 M4 设计/计划/提示词，以及 `C:/Users/11459/.codex/RTK.md`、`TESTING.md`。
随后读取新增 `m4-coordination-protocol.md` 及设计/计划/提示词的协调增量；没有修改这些文件。
入场 `git status --short` 为 README 已修改，`m4-design.md`、`m4-plan.md`、`m4-agent-prompt.md` 未跟踪，均保留。
主控随后通知协议于 23:37:18 创建、三份设计文档于 23:40～23:41 外部更新；这些时间来自主控通知，不是本 Agent 文件时间核验。
本 Agent 未触碰 `m4-coordination-protocol.md`、README、既有设计/计划/提示词、Ledger；未写四份 ADR，未删除任何已有文件。

## 已查证源码边界

下列路径相对仓库根；行号来自本轮读取。仅代表读取时的静态证据，不代表相关功能本轮测试通过。

| 边界 | 真实源码、符号与行号 | 审计结论 |
|---|---|---|
| Review 服务 | `backend/internal/service/review/review.go:64` `Manager`；`:484` `TriggerRequested`；`:882` `submitOne` | 现有入口围绕 worker/PR；提交检查 run 归属和运行状态，不能直接充当本地 Artifact 独立审查授权。 |
| Review Engine | `backend/internal/review/review.go:282` `TriggerWithOptions`，`:337`～`:343` 查询 PR 并拒绝无 PR；`:514` 创建 `ReviewRun` | PR URL/head SHA 是现有运行身份；不得伪造 PR 或借旧 PR 结论给未提交工件签 PASS。 |
| Reviewer 端口 | `backend/internal/ports/reviewer.go:15` `Reviewer`；`:87` `ReviewInvocation`；`backend/internal/adapters/reviewer/registry.go:80` `NewResolver` | 可复用适配器注册及运行能力；接口目前包含 PRURL/TargetSHA。本地 Artifact 入口与可信回执仍为 Gap。 |
| Reviewer 启动 | `backend/internal/review/launcher.go:319` `invocation`；`:345` `prepareInvocation`；`backend/internal/adapters/reviewer/codex/codex.go:50` `ReviewCommand` | 现有 prompt/身份/工作区传递可参考；单个适配器的只读参数不能证明所有 Reviewer 都具备相同隔离能力。 |
| PR 合并 | `backend/internal/service/pr/action_service.go:133` `Merge`，`:168` 重新读取，`:179` SCM 合并；`backend/internal/ports/scm_actions.go:28` `SCMMergeRequest` | 已有 PR 服务执行 expected head 校验与 provider CAS；复用它，禁止再造 PR client/observer。它不是本地目标 ref 集成服务。 |
| PR 授权缺口 | `backend/internal/httpd/controllers/prs.go:30` `merge`，`:49` 调用服务；`backend/internal/httpd/api.go:271` 装配 | 控制器未接 Workflow principal/一次性审批；不能将 SHA 校验表述为人工审批。后续接线须明确覆盖旧路由或保持 M4 不可达，不能留下绕行入口。 |
| Task 路径来源 | `backend/internal/domain/workflow.go:264` `WorkflowTaskRepository`、`:328` `CanonicalWorkflowPath`；`backend/internal/service/workflow/service.go:303` 规范化 | AllowedPaths 来自持久 Task scope；不能信任 Agent 自报路径、SHA 或 PASS。现有规范化不是磁盘 symlink/仓库身份验证。 |
| 真实 Repo 身份 | `backend/internal/session_manager/multi_repo_workspace.go:335` `verifyRepoWorktreeRepo`、`:363` `verifyRepoWorktreeHistory`、`:537` `ObserveSessionWorktrees` | 读取 Project/Repo 记录并比较 Git root/common-dir、符号分支和历史；允许保留 dirty。仅 SHA 相同不能证明同一仓库。 |
| 执行观察 | `backend/internal/service/execution/local_worktrees.go:20` `observeWorktrees`；`backend/internal/service/execution/permit.go:43` `executionContext` | 优先实时求证 Manager；不能用旧 Repo 快照或观察许可代替业务写许可。 |
| Git 摘要局限 | `backend/internal/adapters/workspace/gitworktree/workspace.go:1362` `ObserveWorkspace`、`:1408` `parseObservedWorkspaceChanges` | 当前是有界展示摘要、按行解析；不能当作完整二进制安全的 Snapshot 清单。 |
| M3 permit | `backend/internal/domain/execution_permit.go:25` `ExecutionPermit`；`backend/internal/storage/sqlite/store/execution_permit_store.go:81` `executionPermitFence` | 同事务比较 Admission、Attempt、Binding、generation、epoch、租约；live 校验拒绝 uncertain、过期及非 claimed/running。读取 permit 本身不等于完整 live 校验。 |
| Attempt 结算 | `backend/internal/storage/sqlite/store/workflow_lifecycle_store.go:264` `SettleWorkflowAttempt`，`:293` live fence | Gate PASS 不等于 Attempt succeeded；质量落库、预算释放及结算不得混成一个模型文本决定。 |
| Operator | `backend/internal/service/principal/principal.go:177` `Resolve`；`operator_approval.go:108` `OperatorOperations`、`:294` `ApprovalStore`、`:486` `Consume` | 审批按具体方法/路径/正文摘要一次消费；内存表随 daemon 重启失效；当前闭集尚无 M4 集成路径。 |
| 人工确认 | `backend/internal/service/principal/operator_consent.go:28` `CreateWithPayload`、`:49` `VerifyAndConfirm`；`backend/internal/httpd/controllers/workflows.go:950` `withApprovalDigest` | 必须核验原始正文并经 OS consent；loopback、Agent capability、长期 token 不能替代人工动作。 |
| Runner 基础 | `backend/internal/process/command.go:17` `CommandContext`、`:26` `ConfigureTreeCancellation`；`tree_windows.go:35` `killProcessTree` | 已有 argv 进程与树取消原语可复用；它们不是命令信任策略或 OS 沙箱。 |
| CDC | `backend/internal/storage/sqlite/migrations/20261103073516664_workflow_cdc.sql:70`、`:117` 触发器；`backend/internal/cdc/poller.go:107` `Poll` | 持久状态经 trigger→change_log→CDC；质量/Reservation 事件须增量接线，不另造事件总线或手工双发。 |

## 尚未批准的最小接口草案

以下仅保存已形成的方向；类型字段、事务签名及所有权尚待主控审查，不是可立即执行的开发授权。

```go
Snapshot(ctx context.Context, attemptID domain.WorkflowAttemptID, repoKey domain.WorkflowRepoKey) (ArtifactSnapshot, error)
Run(ctx context.Context, artifactID string, policyRevision int64) (GateReport, error)
RequestReview(ctx context.Context, artifactID string, expectedRevision int64) (ReviewRequest, error)
CommitTask(ctx context.Context, artifactID string, expectedRevision int64, operationID string) (CommitArtifact, error)
Preview(ctx context.Context, workflowID domain.WorkflowID, commitIDs []string) (IntegrationPlan, error)
Apply(ctx context.Context, planID string, expectedRevision int64, operationID string) (IntegrationResult, error)
CoordinationSummary(ctx context.Context, workflowID domain.WorkflowID) (CoordinationProjection, error)
```

- HTTP 请求只定位受权对象；服务端从 Task/Workflow/Session/Repo binding 读取路径与权限，从实时 Git 读取 SHA，从 Runner 获取退出码。不能让请求体构造可信 principal/permit。
- Gate policy 必须来自服务端可信、版本化且有 Operator 授权记录的策略；不从 Agent 任意 argv、仓内新脚本或自报 PASS 推导信任。策略持久化与变更入口仍为 Gap。
- `quality` 写事务必须在同一 SQLite writer 事务执行 `executionPermitFence(..., live=true)` 与 Artifact/Policy/quality revision CAS、幂等审计；先 Validate 再另事务写入不足以闭锁竞态。
- 长运行测试/审查回执还需核对原执行身份及当前 Artifact/Policy，租约续期造成快照变化不可直接忽略或跨 epoch 换发；可接受回执的精确续租规则尚待补定。
- 已结算 Attempt 的交付推进需要明确只读证据与受权 Git 操作的边界，不能借 live=false 或伪造新 permit 绕过；此处尚未冻结。

## 四份 ADR 已有方向（未成文、未批准）

1. **Git preflight / Integration / hooks / signing**：以 Project 注册、真实 root/common-dir、任务符号分支、Base→Creation→HEAD 历史及完整 Snapshot 为前提；任务分支不得等于目标 ref。Snapshot 覆盖全工作区关联内容（包括范围外 dirty、索引、未追踪及影响测试的依赖）；allowlist 只限定可交付内容。范围外 dirty 不提交、不清理；相关内容漂移使证据失效。依赖未交付 dirty 才通过的测试不能签交付 PASS；须验证隔离的交付树。提交只处理服务端清单，不能 `add -A`、reset/clean/stash 用户成果。预演在专用 Integration Worktree，优先 merge、预先固定对象与 SHA；具体安全 plumbing、原索引保留及签名方案待 ADR。目标已被任何 Worktree 检出时拒绝自动 update-ref。未审 hooks、filters、外部 merge driver、签名程序不得隐式执行；缺少受信签名能力时 BLOCKED，不能改全局配置或静默降级。
2. **Test Runner**：可信 policy 固定 executable/argv/env/cwd、超时/输出界限与 OS 条件；禁止 Agent shell 拼接，Windows `.cmd`/解释器边界须显式审查。复用现有树取消；无法证明停止时 UNVERIFIED/UNKNOWN。Worktree 不等于沙箱。指纹包含工件、命令、工具链、依赖和环境；缺工具、异常 SKIP、伪 PASS、非零退出均不产生总 PASS。
3. **Review / 有界修复 / Attempt 分离**：非作者的服务端绑定 Reviewer Attempt/Session；绑定 Artifact digest、实际 tree/head、policy、run 与 generation。Fake 仅证明协议，不能冒充真实独立审查；缺可信审查为 UNVERIFIED。默认最多两轮自动修复，Operator 策略上限三轮，每轮新 ArtifactRevision；修复仍走 M3 准入与预算。旧 SHA/旧回执不推进新工件，Quality PASS 不自动 Settle。
4. **多仓 Partial / 恢复 / Merge / Push**：逐仓持久 operationID、expected old/new SHA、阶段与结果；整计划和有序 Repo 集合绑定一次性审批；串行 CAS，任一漂移停止。跨仓无原子 Git 事务，部分成功保留 refs，标 partial/recovery_required，不自动回滚。内存 Approval 消费与 SQLite 非原子：消费后崩溃可能烧掉审批；先落持久意图才发副作用，恢复只对账，不能复活审批。UNKNOWN 不自动重试，继续写需新批准。Push 独立审批、精确远程/ref/SHA；M4 仅临时 bare remote 验证，不真实推送。

## 新协调协议需要的最小增量（Gap）

- 暂定表名 `workflow_task_path_reservations`；复用 M3 Admission/Claim 事务，原子获取全部 Repo 范围，任一冲突全回滚；不能只靠进程内锁或只靠 DAG 文本约定。
- 冲突按真实仓库身份比较，不能仅比较 Workflow 局部 RepoKey；Windows 大小写折叠、相同路径及父子范围冲突，按路径组件比较，避免将 `src/a` 与 `src/ab` 误判。空 AllowedPaths 按新协议视为整仓；它不等于允许提交现存全部 dirty。
- Reservation 从可信 Task scope 派生；Claim 时绑定 Attempt/epoch，Session/generation 在可信 Bind 事务补齐，未完成绑定不得执行。Lease/Revision 由 Runtime 同事务维护，Agent progress 不续租。
- UNKNOWN/TTL 到期保留占位；只有可信证明原 Session 已停止或失去写能力、工件已冻结才释放。旧 epoch/generation/owner/revision 不能释放新 Owner；恢复接管不等于释放。详细 SQL/API 与双 Store 反例由待批准后续任务落实。
- 候选内部方法为 `ReserveTaskPaths(ctx, claim, scopes)`、`BindPathReservations(ctx, permit)`、`ReleasePathReservations(ctx, permit, stopEvidence)`；必须嵌入既有可信事务，不提供 Agent 直接获取/释放接口，最终类型签名未冻结。
- Coordination Projection 只读聚合现有 DAG、Attempt/Session、Lease、预算、消息、Reservation 和质量结果；不新增可写调度状态、不重新 Spawn，不回传 token、完整环境或敏感绝对路径。
- M2 Collaboration 与 M3 Scheduler 新增协同要求仅完成协议对齐，未扩大源码核验或实跑；不能将文档列出的全部能力写成已验收。后续仍复用现有控制面，不新造 Scheduler/消息系统。

## 建议权限与后续文件所有权（待批准）

| 主体/工作包 | 范围与拒绝边界 |
|---|---|
| Task Agent | 仅自身绑定范围的进展/请求；不得自报可信 SHA/PASS、改 scope/policy、签独立 Review、释放 Reservation、Merge/Push。 |
| 可信 Runtime/M3 | 按当前 permit、准入预算及事务 CAS 执行；UNKNOWN 保留资源，不凭进程无输出重派。 |
| Reviewer | 独立绑定身份，只交审查证据；不得替代测试退出码、批准自身修复或目标写。 |
| Operator | 经精确正文的一次性 OS 审批调整策略、scope、Merge；Push 再单独审批，仍不能绕过 Git/质量/身份校验。 |
| M4-02 | `domain/delivery*.go`、`ports/quality*.go`、质量/Reservation 查询与 Store、新迁移；统一数据库/公共契约所有者。 |
| M4-03 / 04 | 分别 `service/qualitygate`、`adapters/testgate` 与 `service/qualityreview`；共享 Review/调度接线由主控串行授权。 |
| M4-05 / 06 / 07 | Git 受限写与 taskcommit；integration preview；integration apply/recovery，顺序转交文件所有权。 |
| M4-08 / 09 | API/CLI/daemon/生成物/CDC 串行接线；集成反例及主控独立验收。以上全部尚未启动。 |

## 实际验证与失败记录

命令外层使用 `rtk proxy powershell -NoProfile -Command '…'`，`login:false`；最新文档读取使用 `rtk proxy pwsh -NoProfile`。没有全仓测试或生产环境操作。

| 实际命令与目录 | 退出码与输出 |
|---|---|
| 仓库根 `git status --short` / `git branch --show-current` / `git rev-parse HEAD` | 各 0；入场四项 dirty 如上，分支和 HEAD 匹配。 |
| `backend` 下 `go version` | 0；`go version go1.27.1 windows/amd64`。 |
| `backend` 下 `git diff HEAD -- internal/domain/handoff_note.go internal/domain/handoff_note_test.go` | 无差异输出；该子命令未单独保存退出码，不据此声称完整源码哈希验证。 |
| `backend` 下 `go test ./internal/domain -run "^(TestBuildHandoffNoteStaysUnderTheCapAndKeepsTheLatestTurn\|TestHandoffNoteKeepsArtifactsWithinTheByteCap)$" -count=1 -v` | **1，2 FAIL**；包耗时 `0.883s`。失败原文见下方。 |
| 仓库根 `git diff --check`（草稿落盘前） | 0；仅 README 的 LF→CRLF 提示，无空白错误。此命令不覆盖未跟踪草稿。 |

```text
handoff_note_test.go:84: the latest turn must never be dropped
handoff_note_test.go:211: note must keep the latest request and count the artifacts it left out
FAIL github.com/aoagents/agent-orchestrator/backend/internal/domain 0.883s
TEST_EXIT=1
```

本 Agent 日志：`C:/Users/11459/AppData/Local/Temp/ao-m4-01-astra-20261105/domain-baseline.log`、`domain-baseline.exit`。没有修复这两项历史问题。
主控另报相同两项失败：`go test ./internal/domain -run "Test(BuildHandoffNote|HandoffNote|QuotedTextCannotAdd|ClipHandoffNote)" -count=1`，exit 1；`%TEMP%/ao-m4-20261105-main/m401-domain-baseline.log`、`.exit`。本 Agent 未重新读取主控日志，不把它计作自己的执行。
工具层另有嵌套 PowerShell 引号解析失败、Windows rg 通配路径错误及可选 `Get-FileHash` 不可用，均未算通过；没有因此修改环境配置。主控已另行保存基线哈希，本 Agent 未核验该哈希集合。

## 交接与未完成项

本草稿保存后停止写入；当前交接请求为 HANDOFF，主控尚未验收，不能标记 M4-01 ACCEPTED。
四份 ADR 未落盘，最终类型/事务签名、跨阶段 permit、Git 索引/对象/签名方案、Reservation SQL 与完整权限接线未冻结；没有实现或验证这些未来能力。
下一动作仅由主控迁移任务体系、维护 Ledger 并暂停待批准；本 Agent 不编辑 Ledger/plan，不启动 M4-02 或其他下游。
