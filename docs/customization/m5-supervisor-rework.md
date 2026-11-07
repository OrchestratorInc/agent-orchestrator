# M5 统筹审查与返修

更新日期：2026-11-08。M5 正在实施，以下为已反馈的契约缺口；不以未冻结源码的中间状态判定阶段失败。实现方法由执行方选择，最终必须有实际行为证据。

## M5-SV-01：桌面只读质量与协作请求缺少可信身份链

状态：CLOSED / 2026-11-08 统筹独立核对真人成功授权、Renderer 重载后沿用授权，以及真实 scheduler_updated 触发三项受限 GET 成功。限定该测试 Workflow 的 30 分钟只读授权，不涵盖后续写入审批。唯一执行会话 `01a1b01a-c5fb-7430-b43b-a0000dbeb9f7`。

静态证据：`frontend/src/renderer/hooks/useWorkflows.ts` 的 `useWorkflowCoordination`、`useWorkflowCollaboration` 使用普通 `apiClient.GET`；`backend/internal/httpd/controllers/workflow_quality.go` 的 `identity` 和 `workflows.go` 的 `listMessages`/`listTaskRequests` 进入原 principal 解析器。`principal/operator_approval.go` 将对应 GET 列入可信 Operator 闭集。当前桌面 `operator:runVerb` 映射只有 POST/PUT，未提供这些 GET 的可信调用路径。

影响：Mock GET 返回 200 不能证明真实桌面可以获取协调、质量、消息与 TaskRequest；没有可信身份会被后端拒绝。此记录未运行真实桌面，不能称为完整运行复现。

通过条件：M5 实际契约表列出每种读取的身份来源；正式桌面→Daemon 的受限路径能读取，非可信 Renderer/来源不能越界；不把凭据交给 Renderer，不以放开匿名权限绕过。相关查询、重连和真实 Electron 检查使用实际接线。

执行方已确认缺口，拟复用原有 GET 审批闭集。统筹补充产品边界：实时工作台的 Query 挂载、CDC 失效和断线重连不能反复/并行触发原生确认；用户取消后不能由 Query 默认 retry 再次发起授权。单次可信 GET 通过只证明单次读取；若最终只能显式按次读取，应报告实时交互缺口，不能静默以手动刷新替代既定需求。实现方式仍由执行方决定，原写入的精确一次性审批保持。

## M5-SV-02：Operator 协作消息动作与后端身份不匹配

状态：CLOSED / 2026-11-07 冻结版本独立复核；页面仅显示协作消息，用户干预走原 Attempt 接口，Main 已移除代 Agent 发送消息的动作。容量 1 正式 HTTP 用例与前端 68 项检查通过。此关闭不代表真实 Windows Hello 正向验收通过。

静态证据：`frontend/src/main/operator-ipc.ts` 新增 `message: POST /api/v1/workflows/{id}/messages`。`backend/internal/service/principal/operator_approval.go` 的 OperatorOperations 不包含该 POST；`controllers/workflows.go:createMessage` 明确要求 `LeaseProven` 与来源 Attempt，Operator 不能作为任务发送消息。

影响：固定 IPC 路径存在并不代表该用户动作可成功；不得在 UI 暴露一个必定被拒绝的发送动作，也不得伪造 Agent 身份让它通过。

通过条件：按真实需求区分 Agent 间协作消息与用户对指定 Agent 的干预，产品暴露的用户动作必须有可运行且身份准确的端到端路径；原 Agent 消息身份不可伪造边界保持。最终用行为测试/实际调用证明，不使用源码字符串断言。

## M5-SV-03：候选确认后的执行资源收尾尚缺闭环证据

状态：CLOSED / 2026-11-07 冻结版本独立复核。容量 1、SQLite 关闭重开、正式 Manager 收尾、工件冻结、预算 Reconcile、可信 Stop 后独立 Planner、新任务仅 Spawn 一次均通过；实际 maintain 的失败隔离与未确认资源保留也通过。限定 Windows 离线装配，不代表真实 Provider 或跨操作系统进程重启验收。

静态证据：`workflow_plan_store.go:ConfirmWorkflowPlan` 调用 `FinishPlanningAttempt`、`CancelStoppedPlanAttempt` 写终态并清除租约；已有 `service/execution/recovery.go` 仅在可信观测为 Terminated 时释放 Admission，`store/path_reservation_release.go` 还要求终止与冻结的 verified 工件。当前计划确认路径未展示其与这些收尾步骤的完整衔接。

风险：新 DAG 写入成功不等于后续任务能启动。旧 Planner 或停止任务若保留物理容量/路径占位，可能持续阻塞新计划；也不能为了放行直接释放仍活跃或 UNKNOWN 的资源。

通过条件：容量上限 1、真实 Manager 与 Scripted Driver 的离线装配中，目标到候选、确认、恢复调度后，新任务实际获准且仅 Spawn 一次；可信 Stop 后重规划也验证资源收尾、预算对账和重启恢复，旧 Session/Attempt/Worktree/工件仍可追溯。实现方案由执行方决定，不能用直接改库清占位的测试替代正式链路。

开发期跟踪：执行方已新增实际 Manager 收尾与调度维护接线，尚待装配验证。统筹发现 `workflow_retirement.go` 无条件 `defer release()`，而 `manager.go:beginShellTerminalTeardown` 的既有合法契约允许未装配 closer 时返回 `(nil, nil)`，该路径会 panic；已反馈同一会话修复并覆盖。此为静态确定缺陷，尚未独立运行复现。

后续重规划必须另取停止之后的受控 Planner turn：初次确认会收尾原 Planner Attempt/Session，而原 `StartPlan` 创建的是新 Workflow。执行方需在同一 Workflow 的完整装配中证明新规划来源和停止时序；不能复用停止前输出或直接注入候选代替自动重规划。

容量为 1 的依赖环已反馈：早期 `ListWorkflowPlanRetirements` 只选择 `confirmed_revision > 0` 的候选，而 Stop 的可信中断未必终止 Session/释放 Admission。若新 Planner 等待旧槽、旧槽又等待新候选确认，将无法推进。必须用正式路径验证可信停止后的收尾和新 Planner 准入，不能扩大测试容量或直接清库绕过。

## M5-SV-04：重规划服务重放先被重复任务校验拒绝

状态：CLOSED / 2026-11-07 冻结版本独立复核。正式 HTTP/Service 同键同正文返回原 Planner，同键改模型返回 409；后续调度没有重复 Spawn。对应 `TestM5PlanningCapacityOne` 独立 PASS 51.78s。

静态证据：`service/workflow/replanning.go:StartReplan` 在调用 `BeginWorkflowReplan` 之前先 `GetDefinition`、构造固定 ID `replan-<idempotencyKey>` 并 `planPatch`。首次成功后，旧图已经包含该 ID，重复同一请求将先遇到正式图的重复任务校验，不能到达 Store 的 `FindWorkflowReplan` 既有结果分支。

通过条件：服务或 HTTP 正式入口同正文/同键返回原 Planner 结果，正文改变返回冲突，丢回执后恢复不会新增 Planner 或再次 Spawn。仅测试 Store 直接调用不足以覆盖这个服务层先后顺序问题。

## 已取得的早期证据

新增 POST/PUT IPC 的独立 Vitest 检查为 1 文件/8 测试 PASS、exit 0（2.83s），包含 PUT 请求原文与方法绑定；日志 `.tmp/supervisor-m5-20261106/operator-ipc-baseline-native.log/.exit`。此结果不覆盖上述真实后端身份缺口，不作为阶段通过结论。

2026-11-07 独立复核：只读授权版 IPC 为 1 文件/10 测试 PASS（5.07s），`TestWorkflowReadAccessScopedRefresh` 所在 principal 包 PASS（4.561s），两者原生 exit 0。日志 `.tmp/supervisor-m5-20261107/`。这两项只覆盖单元/模拟调用边界，真实桌面授权与自动刷新仍待验收，审查项尚未关闭。

## 2026-11-07 续作与验证环境核查

- 唯一执行会话仍为 `01a1b01a-c5fb-7430-b43b-a0000dbeb9f7`，未正式 HANDOFF；总并发保持统筹与执行方两会话，无新增子 Agent。
- 执行方恢复 `WorkflowTaskPanel.tsx` 的命令返回 exit 0，实际输出为 `bytes=5315, validUtf8=true, nulBytes=0`；原损坏文件已由执行方备份。这只证明恢复后的文件完整性，不证明 TypeScript 或桌面行为通过。
- 统筹命令 `go test -p 2 ./internal/session_manager -run TestWorkflowRetirementWithoutShellCloser$ -count=1 -timeout 2m -v` 长时间没有编译或断言输出。只读进程核查显示 Go PID 49240 累计 CPU 0.53125 秒、无 compiler/test 子进程；不能把测试运行超时参数当作测试已开始的证据。
- 为避免重复堆积验证，统筹按已确认的命令与父子关系精确清理上述 Go/RTK（49240/31320）及本次挂起的 rg/RTK（18276/38792）。两个原工具句柄最终均为 exit -1，无可采信的测试结果；未终止其他会话进程。
- `Win32_PerfFormattedData_PerfDisk_LogicalDisk` 三次采样显示 E 盘 `CurrentDiskQueueLength` 为 38、42、60，`DiskBytesPersec` 均为 0；部分定向文件读取延迟后能够返回。`Win32_DiskDrive` 报告两块盘 Status 为 OK；近六小时 System 日志中查询事件 7/51/55/129/153/157 返回 `NoMatchingEventsFound`。这些证据只支持存在明显 I/O 等待，不能据此确定硬件故障或归因文件损坏。
- 当前四项返修仍 OPEN。文件恢复、接口接线或历史单元结果均不能代替当前版本的编译、契约、容量 1 集成与隔离 Electron 验收；M5 未提交，M6 未启动。

## 2026-11-07 16:02 环境恢复后续作

- 唯一 M5 执行会话上一轮已 completed，本轮已续派同一 Astra high 会话，未新建会话或子 Agent。
- 统筹只读采样 E 盘 CurrentDiskQueueLength=0；四个关键文件读取 3–88ms，内容摘要与前轮一致。
- 独立临时文件 `.tmp/m5-io-probe-1794038519331.bin` 写入 4096 字节、fsync、关闭及读回内容一致，耗时 144ms，SHA256 `461b4c6c22cff114048c90449658f54bd8a6c5ea9e49fe90e2f9a1c55d674f4c`。这是恢复执行的有限依据，不证明磁盘故障根因已解决或长时间可靠性。
- 当前 `planning_retirement.go` 为 2232 字节拟改版，`scheduler_runtime.go` 仍旧调用；已向执行方反馈确定签名不一致及临时恢复文件清理要求。当前未编译，不得标为可交付。
- M5-01～07 和 SV-01～04 范围保持，正式 HANDOFF 后独立验收；PASS 后提交再进入 M6。

## 2026-11-07 16:18 安全续作与快照负载处理

- 用户反馈 E 盘高负载后，统筹停止唯一 M5 执行回合；线程工具确认 `idle / interrupted`，回合 `01a1b507-d2b2-76d0-80a0-652e977dd1a0`。用户随后明确要求继续安全开发，已续派同一 Astra high 会话。
- C 盘桌面日志（`codex-desktop-bd606621-7f39-4595-afd0-6bee1c578184-27048-t0-i1-074226-0.log`）确认 `2026-11-07T08:02:32.084Z` 的 `git add --sparse --pathspec-from-file=- --pathspec-file-nul` 来源为 `turn_diff_analytics / turn-diff-capture-start`。08:02:33.046Z 日志记 canceled；系统在随后数分钟仍观测 Git PID 15788，且读取进程工作目录为本仓库，之后自行退出。不能将请求取消等同子进程停止。
- 未被忽略的根 `.tmp/` 含统筹之前生成的 Go 缓存、race 缓存及测试临时目录，是快照扫描放大的明确风险；尚未证明这是此前全部 I/O 异常或文件损坏的唯一原因。
- 仅追加本机 `.git/info/exclude` 的 `/.tmp/` 与 `/backend/.tmp/`，原文备份到 `C:/Users/11459/AppData/Local/Temp/ao-m5-git-exclude-original-20261107.txt`。`git ls-files --cached -- .tmp backend/.tmp` 输出为空、exit 0；`git check-ignore -v` 对两个目录及两种缓存均命中、exit 0。保留原目录和日志，不改受保护配置或仓库 `.gitignore`，本机规则不提交。
- 续作约束：高 I/O 编译/测试/安装/生成一次一个；缓存与临时产物放 C 盘，通过进程环境设置，Go 初期限制构建并行；不同时叠加测试或同路径补丁。出现持续挂起即停止精确任务、核实真实进程和读回状态，不以取消请求当作安全重试依据。
- 恢复执行后的首个系统采样 E 盘队列为 0、写入约 364KB/s；继续监控。当前 M5 未 HANDOFF、未验收或提交，M6 未启动。

## M5-SV-05：明确取消被当作未知结果，后续输入被旧请求锁住

状态：CLOSED / 2026-11-07 冻结版本独立复核。真实 Electron 确认取消保留草稿，A 改 B 后显示新正文与新幂等键；审批与 Workflow 写请求均为 0，原调度仍暂停。独立前端测试覆盖明确取消清除旧请求、未知结果保留原请求，两者分别验证。

静态证据：`WorkflowAction.tsx` 在 IPC 前 `rememberRequest`，`onError` 只按 HTTP status 400/403/404/409/422 清除原请求；`operator-ipc.ts` 原生取消抛 `OperatorIpcError(OPERATOR_CANCELLED)`，没有 HTTP status。下一次点击仍取 `saved.request`，用户修改草稿也只会重发取消前正文。真实 Electron invoke 对 Error 自定义字段的序列化亦需验证，不能仅以同进程单元异常对象推断。

通过条件：原生确认明确取消且最终请求未发送时保留草稿，修改后下一次点击使用当前正文和新请求身份；网络超时/执行结果未知仍保留原目标、正文和幂等键。不能清除所有错误或自动重发来通过。此项尚无统筹运行复现，行为与实际 IPC 证据由执行方交付后独立复核。

## M5-SV-06：Queue 后续编辑、取消与投递事实未进入工作台

状态：NOT_TESTED / DEFERRED（2026-11-08用户明确先跳过本人操作继续开发）。Queue事实接线与独立离线回归已通过；真实工作流绑定Attempt的桌面编辑/取消/状态回显仍未验。合法离线fixture已准备，不再等待手动启动或Hello；不将此项标CLOSED/PASS。

总设计 `design.md` 第 245 行要求未派发消息可编辑/取消，并显示 queued / delivered / cancelled。统筹检查 `service/intervention/receipts.go:ListReceipts`、`store/workflow_intervention_store.go:ListWorkflowInterventionsForAttempt` 与 `WorkflowIntervention.tsx`，当前 GET 仅返回初始持久干预回执，未读取 Chat turn 后续事实；工作台直接展示 receipt.state。执行方已复核同一缺口。原 Session ChatWorkspace 已有编辑/取消能力，应完整接通既有入口，不复制队列。

通过条件：从工作台 Queue 正式入口产生原 Chat turn，进入既有会话编辑/取消未派发消息；返回工作台及刷新/重连后准确显示取消/投递等实际事实。只读取不得再次 Send/Spawn，原幂等键重放不得恢复已取消消息，查无可信事实不得伪造已投递，跨 Workflow/Attempt 身份隔离仍有效。具体实现由执行方决定。

## 2026-11-07 20:25 冻结版本独立复核

冻结清单为 C 盘任务证据目录的 `desktop-final-source-manifest.json`，SHA256 `11d3b780734ff333062b539835ef68688071d54a94b0433084d715d01c7b4f0b`；统筹逐一复核主仓和隔离 worktree 的 91 文件、4,571,163 字节，摘要一致且严格 UTF-8/NUL 检查通过。HEAD 仍为 `8384bcc2a48d148e611f6d20b596de4eecd15a2d`。验证命令与输出见 `m5-acceptance.md` 本轮续记。

本轮关闭 SV-02/03/04/05。SV-01 保留真实原生授权后的读取/刷新验证，SV-06 保留真实桌面活跃 Chat 的 Queue 往返与重连验证；HTTP 和 Renderer 模拟证据均已通过，不能替代上述剩余桌面观察。M5 整体仍未 PASS，完整 build 失败和完整 Go 套件缺口继续保留，不提交、不启动 M6。

## M5-SV-07：预算范围与合法 Task ID 重名导致目标混淆

状态：CLOSED / 2026-11-07 新冻结版本独立复测通过。真实预算组件至 Main IPC 的同名 Task 回归通过，相关 4 文件/69 项 PASS，原生 exit 0（32.38s）；测试前后双树 93 文件摘要一致。真人 Hello 正向仍由 SV-01 单独跟踪。

`WorkflowBudgetPanel.tsx:22` 用字符串 `global`、`workflow` 判断预算范围，第 37 行同时把原始 Task ID 直接作为同一个 Select 的 value。后端 `domain/workflow.go:227`、`workflow_graph.go:153` 及 `service/workflow/planning.go:222` 未禁止这两个非空 ID。因此合法任务 ID 为 `global` 或 `workflow` 时，选择对应 task 项会被第 22～31 行解析为全局或工作流范围，匹配错误策略，并生成 `/api/v1/scheduler/budget` 或工作流 budget 路径，无法编辑该任务预算。原生审批仍会显示实际请求，但不能补偿界面把任务选择解析成另一预算范围的问题。

通过条件：保持合法 Task ID 的既有语义；三个预算范围的选择、策略回显、最终审批 method/path/body 始终对应用户选中的目标。同名任务可以独立设置预算，全局/工作流预算仍可单独操作。使用真实预算组件及既有桥接边界验证所生成的请求，不以源码字符串或复制分支断言代替。

执行方真实组件回归在旧实现应回显 3、实际回显 1，exit 1；最终只为内部 Task 选择值加前缀，保持原 Task ID。新用例依次覆盖两种同名任务及全局/Workflow 选择，验证回显、确认、审批创建与最终 PUT 的 method/path/body；聚焦新用例 PASS，完整相关组件/IPC/事件 69 项 PASS，TypeScript exit 0。证据位于 C 盘 `ao-m5-safe/sv07-*.log/.exit`。新冻结版本由同一会话继续正式桌面验收，结束并清理后统筹串行独立复测。

## M5-SV-08：资源采样完成时间被采样前时钟误判为未来

状态：CLOSED / 2026-11-07 新冻结版本独立复测通过。scheduler/resources 完整包 PASS 5.415s/2.225s、exit 0；M5 容量 1、重开数据库、Queue、停止后重规划与维护失败隔离定向回归 exit 0，容量用例 PASS 51.01s。正式无 overlay 桌面恢复准入证据有效；未放宽资源阈值。

新一轮真实桌面启动后内存仍约 18.48 GiB，执行方正式 scheduler 响应为 `recovered=true, limit=0, maxNewClaims=0, RESOURCE_UNKNOWN`。统筹读取 `service/scheduler/resources.go:30`：`RefreshResources` 在读取策略和调用采样器前取 `now`，随后第 65 行把该旧时间传给 `window.evaluate`。真实 `adapters/resources/sampler.go:38` 则在读取系统计数之后生成 `ObservedAt`；第 76 行的有效性判断要求 `!r.ObservedAt.After(now)`。正常采样期间时钟前进即可使刚取得的合法样本被判为未来，容量清零。`scheduler.New` 与 `resources.New` 默认各自使用 `time.Now`，并非共享冻结时钟。

现有 `resources_test.go` 直接把同一个时间传入样本与 evaluate，未覆盖 Service 采样期间时钟前进的真实调用次序。此静态缺陷尚不能独自证明本次所有 UNKNOWN 原因；必须保留原始 Resources 字段，区分初次采样、失效间隔、未来时刻与该时序问题。

通过条件：正式 `RefreshResources` 路径在采样期间时钟正常前进时接纳合法连续样本、按既有恢复滞回恢复容量；真实未来/过期/失败或不完整样本仍闭锁，资源阈值保持。补一个能在旧实现变红的 Service 行为回归并跑受影响包，之后用隔离正式 Daemon 的连续资源样本核实修复；不得伪造采样或绕过准入来创建 Session。由同一执行会话在当前实例清理后串行处理，实现方式由执行方决定。

最终源码只在 `Sample` 返回后重新读取评估时钟，原阈值和采样失败闭锁保持。执行方旧实现 overlay 复测 exit 1，新实现 Service 用例 PASS，完整 scheduler/resources 包分别 PASS 8.778s/2.826s、exit 0。诊断 overlay 仅记录原始样本及评估时间，观察首样闭锁和后续容量恢复后已退出；随后正式无 overlay 桌面 8 次 HTTP 200 均为 `RESOURCE_READY, limit=1`，23:05 快照显示唯一新离线 Chat 为 Working、occupied=1。统筹已读取上述正式 HTTP 快照。早期第五轮只留投影字段，不能事后声称其全部 UNKNOWN 均由此单一原因导致。

## M5-SV-09：活跃 Session 已登记预览地址，Browser 面板仍为空

状态：CLOSED / 2026-11-08 合法离线页面真实展示与 Workflows 往返保留已独立核验。未修改业务源码；当前实例后续清理由本轮统一记录。

第六轮正式桌面唯一离线 Chat `m2ff-project-a-2` 状态 Working。执行 `ao preview http://localhost:5173/` 原生 exit 0；`sv08-final-terminal-before.json` 23:23:49 的正式 Session API 返回该 Session 的 `previewUrl=http://localhost:5173/`、`previewRevision=1`。统筹读取 `sv08-preview-empty.jpg/.txt`，当前 Session 路由正确，Browser 面板 URL 为空并显示 `Enter a URL or click one in the terminal.`。不能将 CLI 成功当作页面展示通过，也不能在未取证时认定为网络加载失败。

通过条件：既有正式预览入口登记后，当前活跃 Session 的 Browser 实际展示目标内容；进入 Workflows 再返回保留同一 Session 与浏览器状态，无额外 Spawn/Bind。先根据已有日志、实际接线和必要的定向复现区分事件刷新、Browser 激活及加载环节；若为既有缺陷须有基线证据，若为验收目标或步骤问题也要实际修正并重新取证，不复制状态绕过产品链路。执行方清理实例并交回后，由同一会话处理，源码变更后重新冻结受影响范围。

SV-09 排查更新：`browser-view-host.ts:isAllowedBrowserURL` 会拒绝与 rendererOrigin 相同的地址，导航在创建标签前返回 NAVIGATION_FAILED；此前目标 localhost:5173 正是当前 AO Renderer。执行方对照 M4，四个 Browser 源码/测试文件摘要一致；既有 isAllowedBrowserURL 两项实跑 PASS、276 SKIP、exit 0（5.56s），见 `sv09-baseline-evidence.json`、`sv09-origin-test.log/.exit`。该证据支持同源拒绝的既有规则，尚不等同合法预览目标展示通过；同一执行会话已开始用会话内离线页面重新验收，业务源码保持冻结。

统筹于 01:13 独立读取正式 `ao preview` 退出码 0，并检查 `sv09-preview-before.jpg`、`sv09-workflows-between.jpg`、`sv09-preview-after.jpg`：活跃 Session 始终为 `m2ff-project-a-3`，返回后仍显示本地 HTML 和输入标记 `SV09-往返保留-20261108`。01:06:15～01:10:57 的 435 条 HTTP 记录中只有 chat-view 和 readiness/ensure 写请求，没有创建/绑定请求。主仓与隔离副本 93 文件共 186 份 SHA256 全部匹配冻结，证据 `supervisor-sv09-preview-check.json`。据此关闭预览验收缺口；并未放宽 Renderer 同源导航限制。
