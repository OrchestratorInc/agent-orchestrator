# M5 桌面工作台契约核对

状态：实现与验证进行中，尚未 HANDOFF / ACCEPTED。本文只记录源码可定位的契约，不代表运行验收通过。

## 基线与边界

- Checkout：`E:/code/agent-orchestrator`，分支 `feat/multi-agent-desktop`。
- 接续基线：`8384bcc2a48d148e611f6d20b596de4eecd15a2d`（M4；2026-11-07 续作已用 `git rev-parse HEAD` 核实）。
- Renderer 使用现有 Query / API client / EventTransport；工作台覆盖原路由，复用 Session 宿主和 TerminalCache。
- 写操作使用现有 `operator:runVerb` 的封闭 verb 映射；Main 校验顶层窗口、本机 daemon 身份、原始正文摘要和原生审批，最终请求复用相同 method/path/body。
- 受保护 GET 使用显式 `operator:authorizeWorkflow` 获得的 Main 私有只读授权。自动查询、重连和 CDC 只消费授权，不自动弹原生确认。授权当前为内存 30 分钟、单 Workflow、GET 白名单，daemon 重启失效。

## 已有与新增 HTTP 接点

下表的 `W` 为 `/api/v1/workflows/{workflowId}`，`A` 为 `W/attempts/{attemptId}`，`T` 为 `W/tasks/{taskId}`。路径中的 ID 来自持久快照，不接收 Renderer 任意 URL。

| 操作 | method / path | DTO / 回执 | 版本与重放 |
|---|---|---|---|
| 工作流列表、详情、任务历史 | GET `/api/v1/workflows`、`W`、`T` | Workflow/Definition/Task Envelope | Query key 包含 daemon、Workflow、Task；无副作用 |
| 目标规划 | POST `/api/v1/workflows/plan` | `StartWorkflowPlanRequest` → `WorkflowEnvelope` | `idempotencyKey`；先创建只读 Planner，单独激活 |
| 激活 | POST `W/activate` | `ActivateWorkflowRequest` | 图 `expectedRevision` + `idempotencyKey` |
| 候选列表 | GET `W/plan-candidates` | `WorkflowPlansResponse` | Main 已有只读授权 |
| 提取/编辑候选 | POST `W/plan-candidates` | `workflow.PlanInput` → `WorkflowPlanResponse` | 来源 Attempt/turn、图版本、幂等键；服务端校验 |
| 确认候选 | POST `W/plan-candidates/{candidateId}/confirm` | `ConfirmWorkflowPlanRequest` | 候选 ID + digest + expectedRevision；幂等重放原候选 |
| 停止后重规划 | POST `W/replan` | `workflow.ReplanInput` → `WorkflowTaskEnvelope` | 可信 stopInterventionId、图版本、幂等键；前置幂等查询 |
| 调度快照 | GET `W/scheduler` | `WorkflowSchedulerResponse` | 调度 revision 独立于图版本 |
| 暂停/恢复调度 | POST `W/pause`、`W/resume-scheduling` | 调度快照 | 调度 expectedRevision + 幂等键 |
| 并发策略 | PUT `/api/v1/scheduler/policy` | SchedulerPolicy | 策略 revision + 幂等键；上限与真实容量分开 |
| 预算策略 | PUT `/api/v1/scheduler/budget`、`W/budget`、`T/budget` | `PutBudgetPolicyRequest` | expectedRevision + 幂等键；预留来源 `operator_approved` |
| 协调汇总 | GET `W/coordination-summary` | `WorkflowCoordinationResponse` | 受保护 GET，包含质量/预算/调度事实 |
| 只读授权 | POST `W/read-access` | `WorkflowReadAccessResponse` | 一次性原生审批；token 只保存在 Main 内存，Renderer 只得 workflowId/expiresAt |
| 能力与绑定 | GET `/api/v1/workflows/capabilities`、`A/capabilities`、`A/binding`、`A/observe` | Capability / Binding / Observation | 操作按钮使用 Attempt 路径；核对 Session、generation、readiness、authState、有效期，30 秒刷新 |
| 干预 | POST `A/interventions` | `WorkflowInterventionRequest` → Envelope | `clientMessageId`、mode、controllerGeneration；Steer 额外绑定 provider turn |
| 干预回执 | GET `A/interventions` | `WorkflowInterventionReceiptsResponse` | 受保护只读，5 秒刷新；按原 Attempt 隔离，从持久 Chat turn 派生 queued/delivered/cancelled/unknown |
| 原会话队列编辑/取消 | POST `/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/queue/edit`、`.../cancel` | 既有 Chat 请求与 204 | 工作台导航已有 Session；编辑保留 turn，取消不创建新队列或 Session |
| 协作消息 | GET `W/messages` | `ListWorkflowMessagesResponse` | Operator UI 只读；不能代 Agent 发送任务消息 |
| 任务申请 | GET `W/task-requests`、`W/task-requests/{requestId}` | List/DetailResponse | 返回 requestSha、请求 revision、图 revision |
| 审批任务申请 | POST `W/task-requests/{requestId}/decide` | 决策回执 | expectedRevision；原生精确审批 |
| 物化任务申请 | POST `W/task-requests/{requestId}/materialize` | 持久物化回执 | 请求 SHA + 双 revision + 幂等键 |
| 质量策略 | PUT `W/quality-policy` | `QualityPolicyRequest` | policyId + expectedRevision；不可提供命令正文 |
| 工件冻结 | POST `A/artifacts/snapshot` | `QualitySnapshotRequest` | artifactRevision；保留现有执行身份/路径校验 |
| Gate / Review / 提交 | POST `W/artifacts/{artifactId}/gates`、`reviews`、`commit` | `QualityActionRequest` | 幂等键；当前工件与策略由后端核验 |
| 审查结论 | POST `W/reviews/{reviewId}/finish` | `QualityReviewFinishRequest` | expectedRevision、verdict、结构化 findings；人工身份仍须独立 |
| 集成预演 | POST `W/integrations/preview` | `IntegrationPlanResponse` | 选择 artifactIds + 幂等键 |
| 读取/批准集成 | GET `W/integrations/{planId}`；POST `.../apply` | Plan + repositories | planDigest + expectedRevision + 原样 entries + 幂等键；不重排/null 归一化 |
| 集成操作恢复 | GET `W/integration-operations/{operationId}`；POST `.../reconcile` | 整体与逐仓回执 | 原操作 expectedRevision；PARTIAL/UNKNOWN 保留 |

HTTP 契约来源：`backend/internal/httpd/controllers/workflows.go`、`workflow_planning.go`、`workflow_read_access.go`、`workflow_attempt_capabilities.go`、`workflow_intervention_receipts.go`、`dto.go`、质量/预算控制器；正式生成入口为 `backend/internal/httpd/apispec/specgen/build.go`、`planning.go`、`quality.go`。Renderer 使用生成的 `frontend/src/api/schema.ts`。已执行正式 `npm run api`；当前 httpd/apispec/specgen 完整包通过。具体运行记录见报告。

## 规划和收尾

- 当前只读 Planner 仅接受 Codex；原因是必须由实际 Adapter 强制只读，不能仅靠提示词。模型必须显式选择；仓库必须有 baseSha、targetRef；不支持的 profile 明确拒绝。
- Planner 使用既有 Session/Chat 执行路径，输出持久化后再提取。候选不能引入未选仓库、提升路径范围或直接执行 Git/终端命令。
- 候选编辑保留来源 turn 和原 expectedRevision；保存与自动提取进入同一图/仓库校验。
- 确认候选后调度暂停，用户独立恢复。Planner 和可信停止的旧 Attempt 进入收尾队列，由真实 Manager 停止，再由既有租约恢复、工件 Snapshot 和预算 Reconcile 处理资源。
- 单条 Stop/Snapshot 错误记录后继续其他 job，context/fence 错误中断；只有确认物理停止的 job 才进入冻结。容量 1 用例已覆盖关闭并重开 SQLite、保留原工作树、可信停止后创建新 Planner、收尾后只 Spawn 一次。多 job 测试覆盖一项失败不饿死其他项和重启后保留占位；不把该 fake Retirer 测试解释为真实物理停止证据。

## 模型、effort 与 profile 边界

共同链路为 Task → execution Coordinator → LocalGateway → Manager.resolveAgentConfig → ChatStartConfig/实际 Adapter。以下支持指 Manager 入口允许并透传，不代表调用过真实 Provider；M5 仅使用 Scripted Driver。

| Agent | 显式模型 | 显式 effort | M5 自动 Planner |
|---|---|---|---|
| Codex | 透传；显式 effort 时按已有模型目录校验 | 支持；以模型目录约束为准 | 支持，强制只读；Scripted 装配已验证 model/effort/ReadOnly |
| Claude Code | 进入已有模型目录校验/Adapter | 支持；以模型目录约束为准 | 拒绝，未建立同等只读执行契约 |
| Command Code、Hermes | 沿既有 Manager/Adapter 映射 | Manager 透传原值，CLI 决定可用值 | 拒绝 |
| OMP、DeepSeek 及其他 Agent | 沿既有 Manager/Adapter 映射；本阶段未逐个实调 | 非空显式值返回 `ErrUnsupportedEffort` | 拒绝 |

依据 `backend/internal/session_manager/manager.go:resolveAgentConfig`。DeepSeek ACP 内部存在 reasoning_effort 字段不等于产品 Manager 入口支持。没有为 M5 扩展此范围。非空 profile 在规划和 execution 边界明确拒绝，不写原生配置；其他 Agent/模型的真实可用性保持 NOT_TESTED。

## 页面事实与恢复

- `WorkflowAction` 保存原 path、body、幂等键；传输失败/结果未知保持原请求。明确 400/403/404/409/422 拒绝或 Main 序列化的 `499 + OPERATOR_CANCELLED` 清除 pending request，保留草稿，下次使用新正文/新键。目标切换不接收旧成功回调，daemon 地址变化不接收旧写回包。
- 草稿按 daemon/资源保存于工作台 UI store；业务状态不由草稿推导。
- Task 详情复用现有 `useConversation`，能力需匹配所选 Session/generation；不支持操作不隐式降级。
- Gate 仅在当前 Attempt 的最新工件、当前策略、有效期和完整快照均匹配时作为当前证据；先选最新 Gate，再判断有效性，不能回退旧 PASS。Review 展示持久回执及 target SHA，执行时由后端复核。
- 金额 null/缺失显示 UNKNOWN，不能归零。预算 mode 为 observe/soft/hard_admission，周期为 lifetime/monthly_utc。
- CDC：`workflow_created`、`workflow_updated`、`workflow_task_updated`、`workflow_attempt_updated`、`scheduler_updated`；reset/reconnect 刷新快照，旧 EventSource 回调需拒绝。
- Operator Queue 由后端已校验 Principal 标记 `human`，Agent Queue 仍为 `automation`，HTTP 正文不能伪造该标记。GET 回执只是派生事实，不回写干预行，不再次 Send。POST 同键重放仍可能返回初始 queued 回执；页面 GET 读取当前事实，不恢复取消消息。

## 验证状态

接口与生成物均已落盘。SV-07/08修复后TypeScript检查、工作台/IPC/事件69项回归及scheduler/resources完整直接受影响包通过；无overlay正式桌面连续8次RESOURCE_READY。真实Electron活跃离线Chat草稿、同一Session及Shell Terminal工作台往返已通过，原生取消及未授权读取拒绝证据保留；来源攻击仍为IPC契约测试证据。ao preview登记成功但Browser实际页面仍为空，预览未通过。成功原生授权仍待真人Windows Hello；完整build仍三次FAIL。容量1规划、Queue正式HTTP链和浏览器导航的此前结果不替代本次真人授权验收。当前命令、证据和未验范围见 `m5-report.md` 最新收口记录；M5未PASS/HANDOFF。

本轮仅补充验证归因，没有更改接口或实现。原 integration 日志的23项明确断言失败均在干净 M4 复现：6项缺少 Session 准入装配、17项因项目临时目录缺少 Git 元数据导致路径预留失败并包装成 claim500；另一个 M4 工件超时仍未解决，未执行项仍未验。21项新对照包5.309s / exit1；临时overlay仅作内部错误链诊断，不能计作干净回归。完整打包仍三次FAIL；当前主机诊断中Got在官方代理初始化后仍TLS失败，虽现有Electron与agent-browser校验值匹配，也不证明历史build环境或完整打包通过；内嵌下载器对SHASUMS使用 `cacheMode: Bypass`，补齐清单缓存也不足以恢复构建。逐项表、命令、诊断范围和证据见 m5-report.md。

第四轮在连续三次可用内存8.700/8.685/8.675 GiB的预检通过后启动一次隔离dev；正式scheduler随即实测7.666 GiB、24.163%、limit=0 / RESOURCE_SOFT_LIMIT，按停止条件退出，没有创建活跃Session或执行UI输入、ao preview。managed Codex实际解析已核对为现有M5离线fixture；活跃Chat/终端/预览仍NOT_TESTED，Windows Hello及真实Workflow Queue仍待真人授权。dev句柄Ctrl+C后exit1，最终进程/监听均0、只读数据库身份不变、91源码冻结哈希不变。细节见报告第四轮记录。

本轮下载专项仍未取得新的Got成功证据：透明诊断确认GitHub TLS成功、release-assets下载域名CONNECT200后TLS重置；仅限制TLS1.2和仅预加载现有HTTPS代理Agent两种进程级设置均ECONNRESET / exit1。当前Undici同URL下载6740字节并由原sumchecker验证缓存ZIP通过，只是小文件对照。未满足正式build前置条件，未执行第四次build；原三次完整build仍FAIL。探测/桌面进程和端口均0，91源码哈希不变，详见报告下载专项记录。

本文不是阶段交接；未关闭项仍属于 M5。

## SV-07 / SV-08 契约补充

- Task预算UI选择值采用 `task:${id}`，解析后恢复原始scopeId；`global`、`workflow`仍是合法Task ID，不能与全局/Workflow预算混用。正式确认、审批创建和最终HTTP的method/path/body保持一致。没有改API、生成物或服务端ID规则。
- RefreshResources在采样后重新取得评估时钟，使用完整原样本判断新鲜度；首样、真正未来、过期、失败、gap与不完整样本继续闭锁，健康恢复及重复Refresh滞回规则不变。没有修改内存/CPU阈值、失败原因码或准入权限。
- 普通离线Chat的未发送草稿与Shell往返通过，不代表受保护Workflow Queue或Windows Hello成功；Browser预览字段已登记（a-2，revision1），实际展示未通过，缺少事件/导航日志，根因待定位。
- 正式API关闭临时终端和新增Session后，进程/监听均0；只读SQLite确认原Workflow保持active/revision3、草稿未发送。冻结93文件SHA256为 `0a3f37231692a3eb7879892cd6ed9a0c1c38a32672ea610d264ac2cede80ec8f`，主仓与隔离副本一致，双仓diff --check exit0。独立复测、完整build、真人授权和真实Provider的状态以各自证据为准。


## SV-01 / SV-09 桌面核验补充（2026-11-08）

- 正式只读授权已成功：用户本人新发起并完成PIN，confirm200/read-access200，随后协调汇总、协作消息、任务申请GET均200；原三次60秒403保留，具体错误envelope缺失。
- Main内已有授权在Renderer正式Reload后继续可用于上述受限GET；00:59:34 scheduler_updated后00:59:36三项自动GET200，与保留挂载、无轮询/不聚焦刷新、真实CDC失效路径共同构成自动刷新证据。凭证未交Renderer或工具，写操作仍独立精确审批。
- 合法workspace HTML通过ao preview在真实Browser展示，a-3→Workflows→返回后URL、会话及页面输入标记均保留。前轮AO自身Renderer origin被正式规则拦截且错误未显式展示；规则和相关四文件与M4相同，不通过放宽规则修正验收。
- 本轮正式关闭a-3后进程/监听均0；93文件双仓冻结保持。后续SV-06仅拟调整测试fixture任务为离线目录支持的model/effort，需新的独立冻结记录；不直接改活跃DB或扩大生产能力。
- 以上更新替代前文对应的当前未验状态；前文历史记录保留。SV-06、完整构建及真实Provider等剩余门禁没有因此通过，M5未PASS/HANDOFF。详见m5-report.md最新收口。


## SV-06 续验准备（2026-11-08）

测试装配器仅将预置worker改为offline-fixture/空effort，正式生成新容量1且暂停的隔离Workflow；装配用例PASS不代表Queue通过。新数据m5-desktop-fixture-2231360002，Workflow workflow-d3867d27-4231-462d-adc5-85ae8408d9a1；下一步本人Hello恢复调度的expectedRevision=2，正式调度负责新Attempt绑定。Queue须取得实际Attempt、controllerGeneration和clientMessageId再独立精确审批，不能将旧只读授权当写授权。

新冻结仍93文件，唯一变化为m5_desktop_fixture_test.go；最终清单SHA256 `a89b63f2404a271bef22f89c6056267552dda7a94379e111f4757254bbd7df30`（bytes/lines元数据已校正）。01:33预检进程/监听均0，下一桌面尚未启动，SV-06仍OPEN。已发送一个本人就绪问题；完整命令、日志和续验步骤见报告最新小节。


SV-06启动工具阻塞补记：用户已回复本人Hello“已准备”。desktop-sv06-start.ps1两次require_escalated调用均在自动审批审核截止前未完成，命令未执行；默认沙箱同命令exit1 / AuthorizationManager check failed。检查desktop-sv06-dev.log不存在，未发起resume-scheduling或Queue、未再次弹Hello。已请用户手动运行准备好的同一启动命令，保持窗口打开并回复“已启动”；用户启动后继续同一chat，无需重新授权测试。该阻塞不是Windows Hello失败，也不推翻SV-01/SV-09。机器进程/监听最近预检为0，尚无新启动成功证据；SV-06仍OPEN。证据sv06-launch-blocked.json。原93文件新冻结及参数不变。
