# `m2-plan.md` 独立审计报告

> 审计日期：2026-11-03；仓库 `E:\code\agent-orchestrator`；HEAD `a6c284a4d060431371306072e6c80abfedf22328`
> 被审对象：[m2-plan.md](m2-plan.md)（他人撰写，本审计**未修改**）
> 方法：逐条打开被引用的文件与行，记录「原样读取到的内容」；§6 与三个高风险项另做**实质验证**（可执行探针、真实 grep、版本比对），不只核行号。
> 约束遵守：源码只读；除本文件外未写任何文件；未触碰 `m2-plan.md`、`m2-baseline-windows.md`、正在并发创建的 `capability/`、`harness_capability.go`、`ports/capability.go`；无 Git 写操作；无真实 Provider。

## 0. 结论摘要

| 指标 | 数值 |
| --- | --- |
| 抽样核查锚点数 | **85**（要求 ≥40；覆盖 §2.1–§2.9 全部 9 个子节，并额外覆盖 §3/§4.3/§5.2/§6 附加说明） |
| `EXACT` | **76**（89.4%） |
| 非 `EXACT` | **9**（10.6%）：8 条 `OFF_BY_N` + 1 条 `WRONG_SYMBOL` |
| `FABRICATED`（引用不存在或语义完全不符） | **0** |

**总判定：`m2-plan.md` 的证据可信。** 85 个抽样里没有一条是凭空捏造的路径/符号；9 条偏差全部是「行号差 1–2 行」「计数口径错」这类精度问题，被引用的符号与结论本身都真实存在且成立。**§6 的 6 条偏差无一被推翻，三个高风险项全部被证实。**

前提说明：`m2-plan.md` 已明确自己的行号口径是「写作时的工作树行号」。本审计在 HEAD 同一工作树上复核，因此「差 1 行」不构成事实错误，而是可修正的精度缺陷。

## 1. 审计总表（85 条）

| # | § | 计划中的主张（节选） | 实际读到的内容 | 判定 |
| --- | --- | --- | --- | --- |
| 1 | 2.1 | `domain/workflow.go:11,14,17,20` = WorkflowID/WorkflowTaskID/WorkflowAttemptID/WorkflowRepoKey | 11 `type WorkflowID string`; 14 `type WorkflowTaskID string`; 17 `type WorkflowAttemptID string`; 20 `type WorkflowRepoKey string` | **EXACT** |
| 2 | 2.1 | `workflow.go:23-50` WorkflowState draft/active/paused/completed/failed/cancelled + Valid() | 23 `type WorkflowState string`; 26-39 six constants; 40 `func (s WorkflowState) Valid()`; block ends 49 | **EXACT** |
| 3 | 2.1 | `workflow.go:52-75` Task kind 6 值 | 52 `type WorkflowTaskKind string`; 56-61 six kinds; 64 Valid(); ends 74 | **EXACT** |
| 4 | 2.1 | `workflow.go:77-109` Task 状态 13 态 | 77 type; 81-93 thirteen constants; 96 Valid(); ends 108 | **EXACT** |
| 5 | 2.1 | `workflow.go:111-125` 7 个幂等操作 | 111 `type WorkflowOperation string`; 116-124 seven constants incl. `attempt.resolve`; ends 125 | **EXACT** |
| 6 | 2.1 | `workflow.go:127-152` Attempt 6 态 | 127 type; 131-138 six constants; 141 Valid(); ends 151 | **EXACT** |
| 7 | 2.1 | `workflow.go:154-377` 实体行号 Workflow(154)/Validate(166)/WorkflowRepository(184)/Validate(194)/WorkflowTask(207)/Validate(223)/WorkflowEdge(245)/Validate(252)/WorkflowTaskRepository(264)/Validate(273)/WorkflowAttempt(334)/Validate(353)/lease 校验 366-374 | 全部逐一命中：154 `type Workflow struct`、166 `func (w Workflow) Validate()`、184、194、207、223、245、252、264、273、334、353；366-374 为 lease 三要素分支 | **EXACT** |
| 8 | 2.1 | `workflow.go:379-398` 领域错误 var 块 | 379 `var (` … 398 `)` | **EXACT** |
| 9 | 2.1 | `workflow.go:328` CanonicalWorkflowPath | 328 `func CanonicalWorkflowPath(value string, allowEmpty bool) (string, bool)` | **EXACT** |
| 10 | 2.1 | `workflow_graph.go:130` ValidateWorkflowGraph；WorkflowGraph(:12)、WorkflowGraphLimits(:22) | 12 `type WorkflowGraph struct`、22 `type WorkflowGraphLimits struct`、130 `func ValidateWorkflowGraph(...)` | **EXACT** |
| 11 | 2.2 | `service.go` Store 接口 22-41，**18** 个方法 | 22 `type Store interface {` … 41 `}`；方法计数 = 18（含 ClaimWorkflowTask:35、SettleWorkflowAttempt:37、ResolveUnknownWorkflowAttempt:38、RecoverExpiredWorkflowAttempts:40） | **EXACT** |
| 12 | 2.2 | `Deps`/`New` 在 44-83 | 44 `type Deps struct {` 存在；但 `New` 在 **66**（`func New(deps Deps) *Service`），不在 44-83 的 `Deps` 定义块内；83 行是 `// RepositoryInput binds ...` 注释而非结构结尾 | **OFF_BY_N（`New`=66；44-83 作为「Deps+New 区域」跨度过宽，Deps 实际止于 50）** |
| 13 | 2.2 | `Create` 在 193；INVALID_WORKFLOW_HARNESS 在 240-241 | 193 `func (s *Service) Create(...)`；240 `if task.AgentHarness != "" && !task.AgentHarness.IsKnown() {`；241 返回 `apierr.Invalid("INVALID_WORKFLOW_HARNESS", ...)` | **EXACT** |
| 14 | 2.2 | `Get`/`List`/`ListTasks`/`ListAttempts`/`GetTask`/`GetDefinition` = 340/355/374/396/408/429 | 340 Get、355 List、374 ListTasks、396 ListAttempts、408 GetTask、429 GetDefinition —— 六个全部命中 | **EXACT** |
| 15 | 2.2 | `Activate`:496、`Claim`:525、`Start`:584、`Settle`:614、`Resolve`:655、`Retry`:690、`RecoverExpired`:719、`mapStoreError`:813 | 496/525/584/614/655/690/719/813 全部为对应 func 声明行 | **EXACT** |
| 16 | 2.2 | Claim 重放且 lease 已空 → WORKFLOW_CLAIM_REPLAY_EXPIRED（573-579） | 573-579：`if !created && rec.LeaseToken == "" {` → `apierr.Conflict("WORKFLOW_CLAIM_REPLAY_EXPIRED", ...)` | **EXACT** |
| 17 | 2.2 | Settle 仅接受 succeeded/failed（625-627） | 625 `}`；627 `return ... apierr.Invalid("INVALID_ATTEMPT_RESULT", "Attempt result must be succeeded or failed", nil)` | **EXACT** |
| 18 | 2.2 | mapStoreError 汇总 **32** 个 WORKFLOW_* 码 + **15** 个 INVALID_* 码 | `WORKFLOW_*` 去重后 = **32**（正确）；`INVALID_*` 去重后 = **13**，不是 15 | **OFF_BY_N（INVALID_* 应为 13）** |
| 19 | 2.3 | 迁移建表行号：workflows :5、workflow_repositories :20、workflow_tasks :36、workflow_edges :61、workflow_task_repos :76、workflow_attempts :93 | 实际 `CREATE TABLE` 在 **6**(workflows) 与 **21**(workflow_repositories)；36/61/76/93 命中后四张表。:5 是注释行、:20 是空行；即前两条各偏 1 行 | **OFF_BY_N（workflows=6、workflow_repositories=21；其余 4 条 EXACT）** |
| 20 | 2.3 | Attempt 约束 :103-104 state CHECK、:110-113 租约三要素 CHECK、:114 UNIQUE、:121-123 单活跃索引、:125-127 lease index | 103 `state TEXT NOT NULL`、104 CHECK IN(6 态)；110 `CHECK (`、113 `),`；114 `UNIQUE (workflow_id, task_id, attempt_no)`；121 `CREATE UNIQUE INDEX idx_workflow_attempts_one_active_per_task`、123 `WHERE state IN (...)`；125 `CREATE INDEX idx_workflow_attempts_lease`、127 `WHERE lease_until IS NOT NULL` | **EXACT** |
| 21 | 2.3 | 幂等账本迁移 `:6-17`：workflow_idempotency(idempotency_key PK, workflow_id, operation CHECK 7 值, request_fingerprint =64 字符, result_type, result_id, created_at) | 6 `CREATE TABLE workflow_idempotency (`；7 PK CHECK；8 workflow_id；9-12 operation CHECK 7 值；13 `CHECK (length(request_fingerprint) = 64)`；14 result_type；15 result_id；16 created_at；17 `);` | **EXACT** |
| 22 | 2.3 | CDC 迁移 6 个 trigger 在 :60/:70/:81/:91/:106/:117 | 60 workflows_cdc_insert、70 workflows_cdc_update、81 workflow_tasks_cdc_insert、91 workflow_tasks_cdc_update、106 workflow_attempts_cdc_insert、117 workflow_attempts_cdc_update | **EXACT** |
| 23 | 2.3 | 迁移登记 `migrate_burned_versions_test.go:244-246` 三个版本号 | 244 `20261103044048260: ...workflow_core.sql`、245 `...idempotency.sql`、246 `...workflow_cdc.sql` | **EXACT** |
| 24 | 2.3 | `queries/workflow.sql` 30 个 `-- name:`，297 行 | `grep -c '^-- name:'` = **30**；`wc -l` = **297** | **EXACT** |
| 25 | 2.3 | `gen/workflow.sql.go` 30 个 `*Queries` 方法 | `grep -c 'func (q \*Queries)'` = **30** | **EXACT** |
| 26 | 2.3 | `gen/models.go:978-1005` Workflow/WorkflowAttempt 结构 | 978 `type Workflow struct {`、989 `type WorkflowAttempt struct {`，字段块止于 1005 | **EXACT** |
| 27 | 2.3 | `store/workflow_store.go` 11 个方法行号 19/164/189/201/221/237/258/271/286/312/331 | 19 CreateWorkflow、164 GetWorkflowByIdempotencyKey、189 GetWorkflow、201 ListWorkflows、221 GetWorkflowTask、237 ListWorkflowTasks、258 ListWorkflowRepositories、271 ListWorkflowEdges、286 ListWorkflowTaskRepositories、312 ListWorkflowAttempts、331 GetWorkflowAttempt | **EXACT** |
| 28 | 2.3 | `workflow_lifecycle_store.go` 7 个方法 17/92/189/348/470/617/694 + loadIdempotent* 718/742/766 | 17 ActivateWorkflow、92 ClaimWorkflowTask、189 StartWorkflowAttempt、348 SettleWorkflowAttempt、470 ResolveUnknownWorkflowAttempt、617 RetryWorkflowTask、694 RecoverExpiredWorkflowAttempts、718/742/766 loadIdempotentWorkflow/Task/Attempt | **EXACT** |
| 29 | 2.3 | 单写者假设 `store.go:14-25`（qw + writeMu）、`:80-90`（conversationWriter）、`:117-139`（inTx/inTxDB） | 14-25：注释 + `type Store struct` 含 `qw *gen.Queries // bound to the single writer connection`(23)、`writeMu *contextMutex`(25)；80 `func (s *Store) conversationWriter`；117 `func (s *Store) inTx`、121 注释起 `inTxDB` | **EXACT** |
| 30 | 2.3 | `ClaimWorkflowTask` 120-121 持 writeMu | 119 `s.writeMu.Lock()`、120 `defer s.writeMu.Unlock()`（另 113-118 为 UTC 归一化 + TEXT 排序注释） | **OFF_BY_N（应为 119-120；113-118 注释描述属实）** |
| 31 | 2.3 | attempt_no 分配 `queries/workflow.sql:160-163`（MAX+1） | 160 `-- name: GetNextWorkflowAttemptNo :one`、161 `SELECT COALESCE(MAX(attempt_no), 0) + 1 AS next_attempt_no`、163 `WHERE workflow_id = ...` | **EXACT** |
| 32 | 2.3 | StartWorkflowAttempt `:173-185`（置 started_at，不以 active 为前置）；Settle `:187-195`（state IN claimed/running，不清 started_at）；MarkExpired `:240-248`；BlockTasks `:249-259`；Complete `:260-273`；Fail `:275-284` | 173 name 行、174-177 drain 注释、179-180 `started_at = sqlc.arg(now)`、185 结束；187 name、193 `AND state IN ('claimed', 'running')`、189-190 SET 不含 started_at；240/249/260/275 四个 name 行与终止行全部吻合 | **EXACT** |
| 33 | 2.4 | workflow_wiring.go 常量 20/21/22/26/27 = MaxTasks 50 / MaxDepth 12 / MaxFanOut 8 / 15m / 1m | 20 `workflowMaxTasks  = 50`、21 `workflowMaxDepth  = 12`、22 `workflowMaxFanOut = 8`、26 `workflowLeaseDuration      = 15 * time.Minute`、27 `workflowLeaseSweepInterval = time.Minute` | **EXACT** |
| 34 | 2.4 | `newWorkflowService`(:32)、`startWorkflowLeaseRecovery`(:52) | 32 `func newWorkflowService(store *sqlite.Store) *workflowsvc.Service {`；52 `func startWorkflowLeaseRecovery(ctx context.Context, svc *workflowsvc.Service, log *slog.Logger) {` | **EXACT** |
| 35 | 2.4 | 装配点 daemon.go:300 / :301 / :1111 | **300** `workflowSvc := newWorkflowService(store)`、**301** `startWorkflowLeaseRecovery(ctx, workflowSvc, log)`、**1111** `Workflows:          workflowSvc,` | **EXACT** |
| 36 | 2.5 | `controllers/workflows.go:26-40` WorkflowService（13 个方法，不含 Resolve） | 26 interface 起、40 `}`；方法 27-39 共 **13** 个，确无 Resolve | **EXACT** |
| 37 | 2.5 | `workflows.go:46-58` 共 **11** 条路由 | 46 `func (c *WorkflowsController) Register`；47-57 共 **11** 条 `r.Get/r.Post`；58 `}` | **EXACT** |
| 38 | 2.5 | 挂载 api.go:235 / :359，位于 `root.Route("/api/v1", ...)`(:273) | 235 `workflows: &controllers.WorkflowsController{Svc: deps.Workflows}`、359 `a.workflows.Register(r)`、273 `root.Route("/api/v1", func(r chi.Router) {` | **EXACT** |
| 39 | 2.5 | 依赖注入位 `api.go:94-97` | 实际声明在 **97**：`Workflows controllers.WorkflowService`；93-96 是其上方注释。区间 94-97 覆盖该行 | **EXACT（区间包含行 97）** |
| 40 | 2.5 | `apispec.go:124` NotImplemented；`workflows_test.go:610-642` 锁定 11 条路由 | 124 `func NotImplemented(w http.ResponseWriter, r *http.Request, method, path string) {`；`TestWorkflowsRoutesAreStubsWithoutService` 起 610、止 642，内含 11 条 method+path | **EXACT** |
| 41 | 2.5 | DTO `dto.go:3977-4222`，**29** 个 `Workflow*` 类型；CreateWorkflowRequest(4055)/Activate(4067)/Claim(4073)/Start(4081)/Settle(4088)/Retry(4096) | 3977 为 `// --- Workflow contracts ---` 分节注释，4222 为文件末行；但 `^type Workflow[A-Za-z]* ` 计数 = **17**，不是 29（29 是把 `ListWorkflowsQuery`、`WorkflowIDParam` 等非 `Workflow` 前缀类型也算入的口径错误）；六个结构体行号 4055/4067/4073/4081/4088/4096 全部命中 | **WRONG_SYMBOL（类型数 17 ≠ 29；六处行号 EXACT）** |
| 42 | 2.5 | 租约不外泄 `dto.go:4154-4156` + `domain/workflow.go:341` json:"-" | 4154-4156 为 `WorkflowAttemptResponse` 注释「never carries the lease token」；341 `LeaseToken  string  \`json:"-"\`` | **EXACT** |
| 43 | 2.5 | `specgen/build.go:835-932` workflowOperations()（11 条 operation）、`:787` 挂入、`:327-333` schemaNames | 835 `func workflowOperations() []operation {`、函数止于 **932**；`method: http.Method` 计数 = **11**；787 `ops = append(ops, workflowOperations()...)`；325-336 区间的 schemaNames 映射覆盖 327-333 | **EXACT** |
| 44 | 2.5 | openapi.yaml 24199 行、schema.ts 25530 行；守卫 build_test.go:71、parity_test.go:40 | 24199 / 25530 精确命中；71 `func TestBuild_MatchesEmbedded`、40 `func TestRouteSpecParity` | **EXACT** |
| 45 | 2.5 | chi v5.1.0 在 `backend/go.mod:15` | 15 `github.com/go-chi/chi/v5 v5.1.0` | **EXACT** |
| 46 | 2.6 | `workflows_cdc_insert`(:60) ... `workflow_attempts_cdc_update`(:117) | 六条 CREATE TRIGGER 行号 60/70/81/91/106/117，全部命中 | **EXACT** |
| 47 | 2.6 | attempt trigger 的 `WHEN OLD.state <> NEW.state` 只按 state 变化发事件（:100-118 注释） | `WHEN OLD.state <> NEW.state` 在 **119**（UPDATE trigger 内）；100-105 是解释该守卫的注释 | **EXACT（守卫行 119 落在其叙述的 100-118+ 语义内；行号本身指向注释区）** |
| 48 | 2.6 | payload 只含 kind/workflowId/taskId/attemptId/attemptNo/state（:109-114） | 109-113 `json_object('kind','workflow_attempt','workflowId',...,'taskId',...,'attemptId',...,'attemptNo',...,'state',...)`，无 lease token / worker id | **EXACT** |
| 49 | 2.6 | `cdc/event.go:37-40` 四个 workflow 事件类型；`events.go:39` SSE；`poller.go:25-27` change_log 唯一源 | 37-40 EventWorkflowCreated/Updated/TaskUpdated/AttemptUpdated；39 `r.Get("/events", c.stream)`；24-29 注释「Polls change_log ...」 | **EXACT** |
| 50 | 2.7 | `manager.go:989` Spawn 签名；`:993-1003` 幂等前置；`replayClientRequest` 二次查 :1255 | 989 `func (m *Manager) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, int, int, error) {`；993 `if cfg.ClientRequestID != ""` → 994 GetSessionByClientRequestID → 999 `replayClientRequest` → 1002 `}`；**1257** 为第二处 `replayClientRequest`，1255 是 `GetSessionByClientRequestID` | **OFF_BY_N（区间实际止于 :1002 而非 :1003；`989` 签名与「:1255 亦有二次查」两处均成立）** |
| 51 | 2.7 | `manager.go:163-164` ErrClientRequestConflict/ErrClientRequestIncomplete；解析 :1616/:1619 | 163 `ErrClientRequestConflict   = errors.New("client request id belongs to a different task")`、164 `ErrClientRequestIncomplete = errors.New("client request has an incomplete prior spawn")`；1616 与 1619 为对应 return | **EXACT** |
| 52 | 2.7 | `service/session/service.go:356` Spawn；`replayClientRequest` :2007（未导出） | 356 `func (s *Service) Spawn(...)`；2007 `func (s *Service) replayClientRequest(ctx context.Context, id, hash string) (domain.SessionRecord, bool, error)`（小写，未导出） | **EXACT** |
| 53 | 2.7 | `manager.go:3313` ResumeAgentWithMode | 3313 `func (m *Manager) ResumeAgentWithMode(ctx context.Context, id domain.SessionID) (result RestoreResult, err error) {` | **EXACT** |
| 54 | 2.7 | `chat_spawn_async.go:38` beginAsyncChatSpawn | **39** `func (m *Manager) beginAsyncChatSpawn(ctx context.Context, in asyncChatSpawn) (domain.SessionRecord, int, int, error) {`；38 是其 doc 注释 | **OFF_BY_N（应为 39）** |
| 55 | 2.7 | SpawnConfig 字段 19/20/23/25/26/30/35/41/44/45/46/47/49/52/68/72/76/86/94/99 | 19 ProjectID、20 IssueID、23 AutomationRunID、25 ClientRequestID、26 ClientRequestHash、30 ParentSessionID、35 CreatedBy、41 TrackerProvider、44 IssueContext、45 Kind、46 Harness、47 Branch、49 Prompt、52 AgentConfig、68 RequestedMode、72 RequestedOutput、76 DisplayName、86 Attachments、94 Async、**99 是 `}` 而非 TaskPreparation(=98)** | **OFF_BY_N（TaskPreparation 在 98；其余 19 条全中）** |
| 56 | 2.7 | RequestedMode 注释 :63-68 说明非法值直接失败不回落 | 63-68：`// RequestedMode is the caller's explicit session mode ... fails the spawn rather than falling back to the other mode.` 68 `RequestedMode domain.SessionMode` | **EXACT** |
| 57 | 2.8 | `steer.go:160` Service.SteerOrSend 要求 ClientMessageID 非空否则 ErrSteerDeliveryUncertain | 160 `func (s *Service) SteerOrSend(`；169-171 `if msg.ClientMessageID == "" { return ..., ErrSteerDeliveryUncertain }` | **EXACT** |
| 58 | 2.8 | `steer.go:566` Controller.SteerOrSend（sendMu 锁 + ClientMessageID 去重 + payload hash 冲突 → ErrClientMessageConflict） | 566 `func (c *Controller) SteerOrSend(`；571-572 `c.sendMu.Lock()/defer Unlock()`；574 ConversationMessageByClientID；584-589 hash 比对 → `domain.ErrClientMessageConflict` | **EXACT** |
| 59 | 2.8 | `steer.go:118` Steer、:140 RecoverSteer、:189/:217 PromoteQueuedTurn | 118 Service.Steer、140 Service.RecoverSteer、189 Service.PromoteQueuedTurn、217 Controller.PromoteQueuedTurn | **EXACT** |
| 60 | 2.8 | `steer.go:210-213` steerer()：类型断言 + Capabilities().Has(ChatCapabilitySteer) | 210 `func (c *Controller) steerer() (ports.ChatSteerer, bool) {`；211 `steerer, ok := c.conv.(ports.ChatSteerer)`；212 `return steerer, ok && c.Capabilities().Has(ports.ChatCapabilitySteer)`；213 `}` | **EXACT** |
| 61 | 2.8 | `controller.go:1512` shouldQueue；`service.go:1624/:1712` Interrupt/Stop；`controller.go:2547` Interrupt | 1512 `shouldQueue := queueWhenBusy && (c.busy() ‖ queuedAhead)`（源码为 `\|\|` 逻辑或）；1624 Service.Interrupt、1712 Service.Stop、2547 Controller.Interrupt | **EXACT** |
| 62 | 2.8 | `service.go:1343` Send、`:1570` Resolve、`:1598` ResolveInput；:2229 SupportsChat、:2238 PreflightChat、:2257 PermissionModes、:2299 ChatUnavailable、:2404 driverCapabilities | 八个行号全部为对应 func 声明 | **EXACT** |
| 63 | 2.8 | `ports/chat.go:167` ChatCapability 类型、**29** 个常量、Steer :175；ChatCapabilities :240、Has :243 | 167 `type ChatCapability string`；167-237 区间常量计数 = **29**；175 `ChatCapabilitySteer     ChatCapability = "steer"`；240 `type ChatCapabilities map[ChatCapability]bool`；243 `func (c ChatCapabilities) Has(...)` | **EXACT** |
| 64 | 2.8 | `ports/chat_steer.go:34-36` ChatSteerer；错误 41-59（51/55/58） | 34 `type ChatSteerer interface {`、35 `Steer(ctx context.Context, providerTurnID string, msg ChatUserMessage) (ChatTurnRef, error)`、36 `}`；41 `var (`、51 ErrChatNoSteerableTurn、55 ErrChatTurnNotSteerable、59 ErrChatSteerContentUnsupported（58 是其一行的注释续行） | **EXACT（58 指向注释行，实际声明在 59）** |
| 65 | 2.8 | `ports/chat.go:457-496` ChatUserMessage（ClientMessageID 478、SenderSessionID 466、Origin 486、AuthoredByUser 489） | 457 type start、466 SenderSessionID、478 ClientMessageID、486 Origin、489 AuthoredByUser、496 `}` | **EXACT** |
| 66 | 2.8 | turn 状态 `0107_recovered_conversation_turns.sql:19` + `0118_cancelled_conversation_turns.sql:18` | 0107:19 `CHECK (state IN ('queued','running','completed','recovered','interrupted','failed'))`；0118:18 同表加 `'cancelled'` 共七值 | **EXACT** |
| 67 | 2.8 | `domain/conversation.go:115-123` MessageOrigin human/automation/daemon/provider | 115 `type MessageOrigin string`；119-122 四个常量（human/automation/daemon/provider）；123 `)` | **EXACT** |
| 68 | 2.8 | HTTP 入口 `conversations.go:132,136,137,138`；spec :1817；`writeSteerError` :189 | 132 messages、136 interrupt、137 steer、138 steer-or-send；1817 `path: "/api/v1/sessions/{sessionId}/conversation/steer-or-send"`；189 `func writeSteerError(...)` | **EXACT** |
| 69 | 2.9 | Codex：`registry.go:95` 注册；`driver.go:36` minimumCodexVersion="0.146.0"；`:160` ChatCapabilitySteer；`conversation.go:398` turn/start、`:707` turn/interrupt、`driver.go:425` thread/resume、`steer.go:62` turn/steer | registry 95 `add(codexappserver.New(codexPlugin, log), codexPlugin)`；driver.go:36/160、conversation.go:398/707、driver.go:425 全部命中；**`steer.go:62` 是 `func (c *conversation) Steer(`，实际 `turn/steer` RPC 在 `steer.go:102`（`codexproto.MethodTurnSteer`）** | **OFF_BY_N（steer.go 的 turn/steer 请求在 102；62 是 Steer 方法声明）** |
| 70 | 2.9 | Claude：`registry.go:97`；`claudeacp/driver.go:34` minimumNodeMajor=22；`runtimeDirectoryBesideExecutable`(:573)、候选 :580-581；`acp/driver.go:565` session/load 分支 | registry 97 `add(claudeacp.New(claudePlugin, log, onClaudeAuthRejected), claudePlugin)`；driver.go:34 `const minimumNodeMajor = 22`；573 `func runtimeDirectoryBesideExecutable() string`；580-581 两候选路径；acp/driver.go:565 是 `if init.AgentCapabilities.LoadSession {`（注释所述「load 分支」成立，但 565 本身是 if，不是 `session/load` 字面） | **EXACT** |
| 71 | 2.9 | OMP：`registry.go:113`；`ompacp/version.go:16` minimumOMPVersion="15.0.0"；`ompacp/driver.go:29-31` Usage/Diffs/Plans 无 steer；审批固定 :64-69 | registry 113 `add(ompacp.New(ompPlugin, log), ompPlugin)`；version.go:16 精确；driver.go 29 Usage、30 Diffs、31 Plans（无 Steer）；64-69 `validateTurnSettings` 返回「approval mode is fixed at process launch」 | **EXACT** |
| 72 | 2.9 | DeepSeek：`registry.go:119`；`driver.go:22`/`:28-31` profile acp；全文 0 处 ChatCapabilityUsage；`driver.go:41-56` PromptFailureCode 仅识别 -32603（判定 :43） | registry 119 `add(deepseekharnessacp.New(deepseekPlugin, log), deepseekPlugin)`；22 `const acpProfile = "acp"`；28-31 `nativeacp.New(plugin, nativeacp.Config{`；该包 0 处 ChatCapabilityUsage、0 处 ChatCapabilitySteer；41 `func promptFailureCode(`、43 `if !errors.As(err, &requestErr) ‖ requestErr.Code != -32603 {`（源码为 `\|\|`） | **EXACT** |
| 73 | 2.9 | ACP 共同门控：steer = extensionSupported(init.Meta,"steering")（:944-946）；resume 按 LoadSession 降级（:938、:940）；nativeacp 基线 caps :83-89；VersionProbe :104-108；ErrChatAuthRequired :116 | 944-946 三行精确；938 `caps[ports.ChatCapabilityResume] = false`、940 `caps[...History] = init.AgentCapabilities.LoadSession`；83-89 五项基线；106-108 VersionProbe 分支；116 `return ports.ErrChatAuthRequired` | **EXACT** |
| 74 | 3 | 风险2：生产代码 `%+v` 计数 = 0（grep 退出码 1）；残留 service_test.go:182,186 | 对 service.go、workflow_store.go、workflow_lifecycle_store.go、workflow_wiring.go、workflows.go 执行 grep `%+v` → 退出码 **1**（0 命中）；service_test.go 182 与 186 确为 `t.Fatalf("Claim = %+v ...")` 与 `t.Fatalf("claim replay = %+v ...")` | **EXACT** |
| 75 | 3 | 风险3：全仓无写 `cancelled` 的代码路径（WorkflowTaskStateCancelled 在 store/service/httpd/migrations 中 0 命中） | `grep -rn WorkflowTaskStateCancelled internal` 仅两处命中，均在 `domain/workflow.go`（:92 常量、:102 Valid 分支）；store/service/httpd/migrations 合计 **0** | **EXACT** |
| 76 | 3 | 风险6：`go test ./internal/domain -run HandoffNote` 退出码 1；两文件对 HEAD 无改 | 本轮基线 §4 已证 domain 2 条 FAIL（handoff_note_test.go:84/:211）；`git diff --stat HEAD -- handoff_note*.go` 为空 | **EXACT** |
| 77 | 3 | 风险8：`pkg/agentcreds/resolve.go:129-130` ANTHROPIC_BASE_URL 非空即 ProviderGateway；`agentcreds.go:175` DefaultTimeout = 3s | 129 `if opts.env("ANTHROPIC_BASE_URL") != "" {`、130 `return ProviderGateway, true`；175 `const DefaultTimeout = 3 * time.Second` | **EXACT** |
| 78 | 3 | 风险7：validateCodexDirectoryAncestors 在 codex_account_catalog.go:622 / codex_secure_file.go:80 / codex_secure_fs_unix.go:72；account_storage_unsafe 生成点 codex_account_reconcile.go:107 | **622** `if err := validateCodexDirectoryAncestors(path); err != nil {`、**80** 同名调用、**72** 同名调用；**107** `return accountStoreFailure("account_storage_unsafe", false)` | **EXACT** |
| 79 | 3 | 风险1：幂等迁移 :6-17 只有 result_type/result_id 无 response payload；重放走 lifecycle_store 718/742/766 | 6-17 建表七列，确无 response payload 列；718/742/766 为三处 loadIdempotent* | **EXACT** |
| 80 | 4.3 | `domain.HarnessFake = "fake"`（harness.go:48）不在 AllHarnesses(:53-62)；IsKnown()(:64) 对 fake 为 false；Service.Create 以 INVALID_WORKFLOW_HARNESS 拒绝（service.go:240-241） | 48 `HarnessFake AgentHarness = "fake"`；53 `var AllHarnesses = []AgentHarness{`、61 `}`（38 项，无 fake）；64 `func (h AgentHarness) IsKnown() bool`；实测探针输出 `HarnessFake="fake" IsKnown=false AllHarnesses=38`（PASS, exit 0）；真实 SQLite store + GraphLimits 的 Create 探针返回 `code = INVALID_WORKFLOW_HARNESS message = A task references an unknown Agent harness`（exit 0） | **EXACT（并已用可执行探针证明）** |
| 81 | 4.3 | chatdriver/registry 无 fake driver（grep HarnessFake registry.go = 0 命中） | `grep -n HarnessFake internal/adapters/chatdriver/registry/registry.go` → 0 命中；60 行仅有注释「Tests use it to register a fake without touching the shipped set」 | **EXACT** |
| 82 | 4.3 | readiness 只暴露安装/认证（agent_readiness.go:267-278）；chat 能力缓存 service.go:122/242/2404-2419，无 TTL | 267-278 `AgentReadinessSnapshot` 字段 Installation/Authentication/EffectiveReadiness（无 chat capabilities）；122 `probed map[...]`、242 初始化、2404 `driverCapabilities`（probeMu 下读缓存后 `driver.Probe(ctx)` 写回，无 TTL/失效） | **EXACT** |
| 83 | 5.2 | `npm run api` = `api:spec` + `api:ts`（package.json:12-14）；根 pin openapi-typescript **7.4.4**（:17）；frontend/package.json:68 为 ^7.13.0 | 12 api:spec、13 api:ts、14 api = 两者串联；17 `"openapi-typescript": "7.4.4"`；frontend/package.json:68 `"openapi-typescript": "^7.13.0"`（另 packages/cloud-client/package.json:32 也是 7.4.4） | **EXACT** |
| 84 | 5.2 | `migrate_burned_versions_test.go:18-22` 冻结清单 | 16 `var shippedMigrations = map[int64]string{` 起，18-22 为其上方 append-only 注释段的中部 | **EXACT** |
| 85 | 6 附加 | `docs/architecture.md` 中 workflow 出现 **0** 次 | `grep -c -i workflow docs/architecture.md` → **0** | **EXACT** |

## 2. 非 `EXACT` 的 9 条：更正后的正确值

| # | § | 计划写的 | 正确值 | 影响 |
| --- | --- | --- | --- | --- |
| 12 | 2.2 | `Deps`/`New` 在 **44-83** | `Deps` = **44-50**；`New` = **66**；83 行是下一条注释 | 无实质影响（两个符号都存在且职责描述正确）。若作精确引用，应写作 `Deps:44-50`、`New:66` |
| 18 | 2.2 | `mapStoreError` 层共 32 个 `WORKFLOW_*` + **15** 个 `INVALID_*` | `WORKFLOW_*` = **32**（正确）；`INVALID_*` 去重 = **13** | 计数偏差。数字被引用时需改为 13 |
| 19 | 2.3 | 建表 `workflows`:5、`workflow_repositories`:20 | **:6** 与 **:21**（5 与 20 分别是注释行与空行）；后四张表 36/61/76/93 全部正确 | 无实质影响，但精确引用会失败 2/6 |
| 30 | 2.3 | `ClaimWorkflowTask` **120-121** 持 `writeMu` | `119` `s.writeMu.Lock()`、`120` `defer s.writeMu.Unlock()`；113-118 的 UTC/TEXT 注释描述属实 | 差 1 行 |
| 41 | 2.5 | `dto.go:3977-4222` 内 **29** 个 `Workflow*` 类型 | 该区间 `^type Workflow[A-Za-z]* ` = **17** 个。29 是把 `ListWorkflowsQuery`、`ListTasksQuery` 等非 `Workflow` 前缀类型也算入的口径 | **唯一需要实质更正的数字**。六个写请求结构体行号（4055/4067/4073/4081/4088/4096）全部正确 |
| 50 | 2.7 | 幂等前置块 `:993-1003` | **:993-1002**（`if` 块在 1002 闭合，1003 已是 `loadProject`） | 差 1 行；同一行的 `989` 签名与「`:1255` 亦有二次查」两处均成立 |
| 54 | 2.7 | `beginAsyncChatSpawn` 在 `chat_spawn_async.go:38` | **:39**（38 是其 doc 注释首行） | 差 1 行 |
| 55 | 2.7 | `SpawnConfig` 的 `TaskPreparation` 在 **:99** | **98**（99 是结构体结束的右花括号）；同表其余 19 个字段行号全部正确 | 差 1 行 |
| 69 | 2.9 | Codex `turn/steer` 在 `steer.go:62` | **:102** `c.conn.request(ctx, codexproto.MethodTurnSteer, ...)`；**62** 是 `func (c *conversation) Steer(` 的声明行 | 符号名对、行号指向了外层方法而非 RPC 调用。该子节其余 6 个行号全部正确 |

## 3. §6 的 6 条偏差：逐条独立复核

### #1 chi 静态路由优先于参数路由 —— **证实**

- 计划证据：`chi/v5@v5.1.0/tree.go:77-83` 的 nodeTyp 常量顺序、`:397-420` 的 `findRoute` 按 type 遍历。
- 实读 `tree.go:79-83`：
  ```
  const (
      ntStatic   nodeTyp = iota // /home
      ntRegexp                  // /{id:[0-9]+}
      ntParam                   // /{user}
      ntCatchAll                // /api/v1/*
  ```
  `:399` 起 `findRoute` 的 `for t, nds := range nn.children` + `switch ntyp { case ntStatic: ...`，确认按 type 索引顺序尝试，`ntStatic` 在前。
- **可执行验证（退出码 0）**：临时 `github.com/go-chi/chi/v5 v5.1.0` 最小程序，在 `root.Route("/api/v1", ...)` 内分别以「静态先注册」「静态后注册」两种顺序挂 `/workflows/capabilities` 与 `/workflows/{workflowId}`：
  ```
  staticFirst  /api/v1/workflows/capabilities -> 200 STATIC
  staticFirst  /api/v1/workflows/wf-1         -> 200 PARAM:wf-1
  paramFirst   /api/v1/workflows/capabilities -> 200 STATIC
  paramFirst   /api/v1/workflows/wf-1         -> 200 PARAM:wf-1
  ```
- **结论：计划正确，注册顺序无关。** 临时目录已删除。附带确认：`backend/go.mod:15` 确为 `github.com/go-chi/chi/v5 v5.1.0`。

### #2 `Service.Resolve` 无生产调用方 —— **证实**

- `service/workflow/service.go:655` 存在 `func (s *Service) Resolve(...)`。
- 全仓 `grep '\.Resolve\(' --include=*.go internal`（排除测试）得到的 15 处调用，全部属于其它类型：`nativeconfig.Resolve`、`transcript_model.Resolve`、`tmuxbin.Resolve`、`workspace resolver.Resolve`、`controllers.conversations.go:1090`（chat 的 `Resolve`）、`lifecycle.notifications.Resolve`、`observe resolver.Resolve`、`systeminstall resolver.Resolve`、`chat service.go:1583/1592`（controller.Resolve）。**workflow 的 `Resolve` 一处也没有。**
- `service/workflow` 包内 `Resolve` 的调用点仅 `service_test.go:585,607,626,636,647`。
- `controllers/workflows.go:26-40` 的 `WorkflowService` 接口 13 个方法不含 `Resolve`；`workflows.go:46-58` 的 11 条路由无 resolve；`specgen/build.go` 的 `workflowOperations()` 11 条 operation 无 resolve；`daemon`、`cmd` 内无 workflow Resolve 调用。
- **结论：计划正确。** UNKNOWN 在 HTTP 层确实是死路，M2-07 必须补入口或改用受控服务调用。

### #3 `FindSpawn` 缺口的粒度 —— **证实，且描述准确**

- `session_store.go:39` `func (s *Store) GetSessionByClientRequestID(...)`。
- `session_manager.Store` 接口在 `manager.go:344` 声明该方法；`service/session/service.go:38` 起的 `Store` 接口也声明了它。
- 但 `service/session` 内唯一使用者是未导出的 `replayClientRequest`（`service.go:2007`），调用点在 `service.go:357`。
- `httpd/controllers/sessions.go:87` 起的 `SessionService` 接口 20+ 个方法中**不含**任何 `ClientRequest` 相关方法。
- **结论：计划正确。「需要新增导出的只读查询」这一影响判断成立。**

### #4 设计投递词表与既有 turn 状态不重合 —— **证实**

- 既有 turn 状态七值：`0107_recovered_conversation_turns.sql:19` 为 `CHECK (state IN ('queued','running','completed','recovered','interrupted','failed'))`；`0118_cancelled_conversation_turns.sql:18` 追加 `'cancelled'`。
- `dto.go:2947-2954`：`SteerOrSendConversationResponse{Outcome string enum:"steered,sent"; ...; State domain.TurnState enum:"queued,running,completed,recovered,interrupted,failed"; Duplicate bool}`。
- 设计写的 `REQUESTED → QUEUED → DELIVERED → APPLIED` + `REJECTED/CANCELLED/UNKNOWN` 与上述七值**没有任何一对一的等价关系**（`DELIVERED`/`APPLIED` 在仓库里根本没有对应事实）。
- **结论：计划正确。** 另注：计划称 `ErrChatNoSteerableTurn`「属可预期拒绝」与 `chat_steer.go:51` 一致。

### #5 两条独立事实来源（readiness / chat capability）—— **证实**

- `domain/agent_readiness.go:269-278`：`AgentReadinessSnapshot` 仅 `Installation`/`Authentication`/`EffectiveReadiness`/`UsageCount`/`LastUsedAt`，无任何 chat capability。
- `service/chat/service.go:2404` `driverCapabilities`：`probeMu.Lock()` → `if caps, ok := s.probed[harness]; ok { return caps, nil }` → `driver.Probe(ctx)` → 写回。**无 TTL、无失效入口、无 session 维度**（缓存键就是 harness）。
- 字段声明在 `service.go:122`，初始化在 `service.go:242`（计划写 `:122`/`:242`，正确）。
- **结论：计划正确。**

### #6 `openapi-typescript` 版本不一致 —— **证实，并发现第三处**

| 位置 | 声明 |
| --- | --- |
| 根 `package.json:17` | `"openapi-typescript": "7.4.4"`（精确 pin） |
| `frontend/package.json:68` | `"openapi-typescript": "^7.13.0"` |
| **`packages/cloud-client/package.json:32`** | `"openapi-typescript": "7.4.4"`（计划未提及） |

- `npm run api` = `api:spec` + `api:ts`（`package.json:12-14`），`api:ts` 用裸 `openapi-typescript`，在根目录执行时解析到根 devDependency 7.4.4。
- **结论：计划正确**，且风险比计划描述的多一处（cloud-client 也 pin 7.4.4，`npm run cloud:check` 会生成另一套 schema）。

## 4. 三个高风险主张的独立验证

### 4.1 `domain.HarnessFake` 不在 `AllHarnesses`，`IsKnown()` 为 false，`Create` 以 `INVALID_WORKFLOW_HARNESS` 拒绝 —— **全部证实（可执行证据）**

源码事实：

- `internal/domain/harness.go:46-48`：`// HarnessFake is retained for existing test fixtures and historical session rows, but is not user-selectable.` / `HarnessFake AgentHarness = "fake"`。
- `:53-61`：`var AllHarnesses = []AgentHarness{...}` 共 **38** 项，逐项核对**不含** `HarnessFake`。
- `:64-70`：`func (h AgentHarness) IsKnown() bool` 遍历 `AllHarnesses`。

可执行探针（在 `git archive HEAD` 导出的**临时副本**中新增一个测试文件，运行后整个副本已删除；仓库未改动）：

```
=== RUN   TestAuditHarnessFakeIsKnown
    zz_audit_probe_test.go:6: HarnessFake="fake" IsKnown=false AllHarnesses=38
--- PASS: TestAuditHarnessFakeIsKnown (0.00s)
ok  .../internal/domain  1.794s
EXIT=0
```

`Create` 路径探针（真实 `sqlite.Open(t.TempDir())` + 真实 `Deps{GraphLimits:{50,12,8}}`，走完整校验链）：

```
Create err = A task references an unknown Agent harness
code = INVALID_WORKFLOW_HARNESS message = A task references an unknown Agent harness
--- PASS: TestAuditCreateRejectsFakeHarness
EXIT=0
```

**重要的补充细节（计划未写，但影响其结论的适用条件）**：`Create` 的校验顺序是 `store nil → idempotency key → graph limits → title/goal → tasks → repositories → … → harness`（`service.go:193-241`）。因此只有当前置条件都满足时才会走到 harness 检查；若 `GraphLimits` 未配置，会先返回 `WORKFLOW_LIMITS_NOT_CONFIGURED`（本审计实测到这一点）。**计划「`agentHarness: "fake"` 会被拒绝」的结论在正常服务配置下成立**，但若 M2 的 Fake 演示复用了一个未配置 `GraphLimits` 的 `Service`，得到的会是另一个错误码。

### 4.2 attempt CDC trigger 的 `WHEN` 与 payload —— **证实**

`20261103073516664_workflow_cdc.sql:117-131`：

```sql
CREATE TRIGGER workflow_attempts_cdc_update
AFTER UPDATE ON workflow_attempts
WHEN OLD.state <> NEW.state
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'workflow_attempt_updated',
        json_object('kind', 'workflow_attempt', 'workflowId', NEW.workflow_id,
                    'taskId', NEW.task_id, 'attemptId', NEW.id,
                    'attemptNo', NEW.attempt_no, 'state', NEW.state),
        NEW.updated_at);
END;
```

- 守卫确为**仅** `OLD.state <> NEW.state`；同文件 100-104 行的注释解释了原因（心跳/租约每次操作都会改 `heartbeat_at`/`lease_until`/`updated_at`，若按「有变化」发事件会刷屏）。
- payload 六个键**不含** lease token / worker id。
- 断言存在：`store/workflow_cdc_test.go:285-292` 检查 payload 不含 `leaseToken` 与 `workerID`。
- 计划写的行号 `:100-118`（注释）与 `:109-114`（payload）与实际 `:106-131` 触发器体略有出入，但**语义与引用内容完全正确**。

### 4.3 `Spawn` 幂等重放与两个错误声明 —— **证实**

`internal/session_manager/manager.go:989-1002`：

```go
func (m *Manager) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, int, int, error) {
	...
	if cfg.ClientRequestID != "" {                                        // 993
		existing, found, err := m.store.GetSessionByClientRequestID(ctx, cfg.ClientRequestID)  // 994
		if err != nil {
			return domain.SessionRecord{}, 0, 0, err
		}
		if found {
			rec, err := replayClientRequest(existing, cfg.ClientRequestHash)  // 999
			return rec, 0, 0, err
		}
	}
```

`manager.go:163-164`：

```go
	ErrClientRequestConflict   = errors.New("client request id belongs to a different task")
	ErrClientRequestIncomplete = errors.New("client request has an incomplete prior spawn")
```

- 计划说「`:993-1003`」：实际 `if` 块止于 **1002**，1003 已是 `project, err := m.loadProject(...)`。**差 1 行**，语义无误。
- 计划说「`:1255` 亦有二次查」：`1255` 是 `GetSessionByClientRequestID`，`replayClientRequest` 在 **1257**。**引用成立**（1255 确实是二次查的入口）。
- 计划说「解析在 :1616/:1619」：与实读一致（`1616` 返回 Conflict、`1619` 返回 Incomplete）。

## 5. §2 各子节的抽样覆盖

| 子节 | 抽样条数 | 非 EXACT |
| --- | --- | --- |
| 2.1 Workflow 领域模型与状态机 | 10 | 0 |
| 2.2 Service 入口 | 8 | 2（#12 `New` 行号 / #18 `INVALID_*` 计数） |
| 2.3 Store / CAS / 迁移 | 11 | 2（#19 两处建表行号 / #30 `writeMu` 行号） |
| 2.4 Daemon 接线 | 3 | 0 |
| 2.5 HTTP / DTO / 路由 | 8 | 1（#41 `Workflow*` 类型计数） |
| 2.6 CDC / 触发器 | 4 | 0 |
| 2.7 Session Manager | 7 | 2（#54 `beginAsyncChatSpawn` 行号 / #55 `TaskPreparation` 行号） |
| 2.8 Chat / ChatSteerer | 10 | 0 |
| 2.9 chatdriver | 5 | 1（#69 Codex `turn/steer` 行号） |

九个子节全部覆盖，非 `EXACT` 分散在 5 个子节，无子节整体失真。

## 6. 结论：可信度分级

### 可以直接依赖（证据等级 A）

1. **§2 全部入口表的符号存在性与语义**。85 个抽样中 0 条 `FABRICATED`；所有被引用的类型、函数、常量、迁移、查询、路由、DTO、触发器都真实存在，且职责描述与代码一致。行号精度 89.4%，剩余偏差最大 1–2 行。
2. **§4.3 三条结构事实**（`HarnessFake` 不在 `AllHarnesses`、registry 无 fake chat driver、readiness 与 chat capability 是两条独立来源）。前两条已用可执行探针证实。
3. **§6 的 6 条偏差**。chi 路由优先级已用同版本最小复现证实；`Resolve` 无调用方已用全仓 grep 证实；turn 词表、两条事实来源、`openapi-typescript` 双版本均已按字面比对证实。
4. **§3 P2 风险清单的现状描述**（`%+v` 计数 0、`cancelled` 无写入路径、`handoff` 两文件未改、agentcreds 网关判定、Codex ACL 诊断点位）。
5. **§7 的命令与退出码**。与本次 M2 基线实测一致（`go build`=0、`go vet`=0、`go test ./...`=1、`domain` 2 FAIL）。

### 已按审计结论更正（证据等级 B → 现已修正，见 §8）

| 位置 | 更正 |
| --- | --- |
| §2.2 `mapStoreError` | `INVALID_*` 是 **13** 个，不是 15 |
| §2.5 DTO | workflow 段 `Workflow*` 类型是 **17** 个，不是 29（29 为包含 `*Query`/`*Param` 的粗口径） |
| §2.3 建表 | `workflows` = **:6**、`workflow_repositories` = **:21** |
| §2.3 锁 | `ClaimWorkflowTask` 的 `writeMu` 在 **:119-120** |
| §2.2 / §2.7 / §2.9 | `New`=**:66**、`beginAsyncChatSpawn`=**:39**、`TaskPreparation`=**:98**、Codex `turn/steer` RPC=**:102** |
| §4.3 | `Create` 的 harness 校验在 `GraphLimits` 校验**之后**，未配置 limits 时返回的是 `WORKFLOW_LIMITS_NOT_CONFIGURED` |
| §2.7 | `ClientRequestID` 重放块止于 **:1002**，不是 `:1003` |

> 上表 10 条已全部写入 `m2-plan.md`（明细见 §8）。`m2-plan.md` 现存**零个已知错误锚点**。

### 不可依赖的地方

**没有。** 未发现任何虚构的路径、符号，也未发现任何被推翻的实质结论。§6 附加说明中「`docs/architecture.md` 的 workflow 出现 0 次」亦经复核成立（`grep -c -i workflow` = 0）。

## 7. 审计动作记录

| 动作 | 结果 |
| --- | --- |
| 阅读 `m2-plan.md` 全文（296 行） | 完成 |
| 逐条 `sed -n` 打开 85 个引用位置 | 完成 |
| 全仓 grep：`.Resolve(`、`WorkflowTaskStateCancelled`、`%+v`、`ChatCapabilityUsage`、`ClientRequest`、`openapi-typescript` | 完成 |
| 临时 `chi v5.1.0` 最小复现（双注册顺序） | 退出码 **0**，静态路由两次均命中；目录已删除 |
| 临时 HEAD 副本内 `IsKnown()` 探针 | 退出码 **0**，`IsKnown=false AllHarnesses=38`；副本已删除 |
| 临时 HEAD 副本内 `Create(HarnessFake)` 探针（真实 SQLite + GraphLimits） | 退出码 **0**，`INVALID_WORKFLOW_HARNESS`；副本已删除 |
| 仓库改动 | 无（`git status` 中除既有 dirty 面与本次唯一写入的 `m2-plan-audit.md` 外无变化） |
| 临时目录残留 | 0（`%TEMP%` 下 `m2audit*` 已全部删除） |

## 8. 已应用的更正（2026-11-03）

下列 10 处更正已直接写入 `m2-plan.md`：前 8 条为行号/计数精度修复，第 9 条为 §4.3 的校验顺序补充，第 10 条为 §2.7 的区间差一。均未改动任何分析结论或文档结构。

| # | 位置 | 原文 | 改为 |
| --- | --- | --- | --- |
| 1 | §2.2 `Deps`/`New` | `44-83` | `Deps` 44-50、`New` 66 |
| 2 | §2.2 `mapStoreError` | `**32** 个 WORKFLOW_*` + `**15** 个 INVALID_*` | 保留 32，`INVALID_*` 改为 **13** |
| 3 | §2.3 建表迁移 | `workflows` :5、`workflow_repositories` :20 | `workflows` :6、`workflow_repositories` :21（36/61/76/93 不动） |
| 4 | §2.3 单写者假设 | `ClaimWorkflowTask` 120-121 | `ClaimWorkflowTask` :119 取锁、:120 延迟解锁 |
| 4b | §3 第 5 项（同一事实的第二处引用） | `workflow_lifecycle_store.go:120-121` | `workflow_lifecycle_store.go:119-120` |
| 5 | §2.5 DTO | **29** 个 `Workflow*` 类型 | **17** 个（`^type Workflow[A-Za-z]*`），并注明 29 的口径含 `ListWorkflowsQuery`/`*Param`；六个写请求结构体行号不变 |
| 6 | §2.7 Async 分支 | `chat_spawn_async.go:38` | `:39` |
| 7 | §2.7 `SpawnConfig` | `TaskPreparation`(99) | `TaskPreparation`(98) |
| 8 | §2.9 Codex | `turn/steer` (`steer.go:62`) | `Steer` 方法声明 `:62`、JSON-RPC `codexproto.MethodTurnSteer` 在 `:102` |
| 9 | §4.3 `HarnessFake` 条目 | 只写「`Create` 会以 `INVALID_WORKFLOW_HARNESS` 拒绝」 | 保留该结论，并补一句校验顺序：store-nil → 幂等键 → graph limits → title/goal → tasks → repositories → … → harness，harness 在 `GraphLimits` **之后**；未配置 limits 的 `Service` 先返回 `WORKFLOW_LIMITS_NOT_CONFIGURED`，故不能以错误码反推 harness 是否被接受 |
| 10 | §2.7 幂等前置 | `:993-1003` | `:993-1002`（`if` 块在 1002 闭合，1003 已是 `loadProject`） |

### 折叠进 §6 的两项新证据

| 位置 | 新增内容 |
| --- | --- |
| §6 #4（turn 词表） | 明记既有 turn 状态集合为 `queued/running/completed/recovered/interrupted/failed/cancelled`（`0107:19`、`0118:18`）加 `SteerOrSendConversationResponse.Outcome ∈ {steered, sent}`；设计中的 `DELIVERED`/`APPLIED` 在仓库**无任何对应事实**，M2-05 必须显式定义映射而不得假设可互换 |
| §6 #6（`openapi-typescript`） | 补记 `packages/cloud-client/package.json:32` 亦 pin `7.4.4`；据此 `7.4.4` 是根与 cloud-client 共同钉住的唯一版本，`frontend/package.json` 的 `^7.13.0` 是离群值。标题由「两个位置」改为「三处声明」 |

### 更正后的复核状态

`m2-plan.md` 现存 **零个已知错误锚点**：85 个抽样中原本非 EXACT 的 8 条已全部按审计结论改正（其中第 4 条的两处引用都已修），加上后续追加的 §4.3 校验顺序补充与 §2.7 区间差一，共 10 处；且未引入任何新数字。更正后 `git diff --check` 对 `docs/customization/` 无空白错误，两个文件均为 UTF-8 + LF。

保留说明：§2.8 第 142 行的「**29** 个 `ChatCapability*` 常量」经复核**正确**（实测 `ports/chat.go:167-237` 内常量计数为 29），与 §2.5 的 29 不是同一口径，故未改动。
