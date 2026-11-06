# M5 桌面双模式设计

日期：2026-11-06。状态：设计准备；实施前必须完成 M4 独立验收与本地提交。需求来自 [design.md](design.md) §11～13 与 [requirements.md](requirements.md) Q1/Q3/Q4/Q7/Q8/Q9/Q10；实施拆分见 [m5-plan.md](m5-plan.md)。

## 1. 目标和边界

用户在项目与会话布局、多 Agent 工作台之间切换，始终操作相同的 Workflow、Task、Attempt 和 Session。能够查看 DAG、调度阻塞、预算、质量证据、逐仓集成结果，并准确向指定 Agent 发起干预、完成人工审批。

Renderer 只表达用户意图和服务端事实；不启动第二个执行器、不管理租约、不直接访问 SQLite 或 Git、不把进程退出或 Agent 文本当成任务成功。保留现有单 Agent 入口与终端体验，复用根目录 `DESIGN.md` 的组件和视觉规范。

本阶段交付本机桌面功能。真实 Provider 付费验证和发布授权不随 M5 自动开放；网络控制平面、远程 Worker 调度和云功能改造不在范围内。

## 2. 已核实的复用点与缺口

以下路径相对仓库根，名称来自接手时源码；开工时核对 M4 提交后的变化。

| 能力 | 现有源文件 | M5 接入原则 |
|---|---|---|
| 持久 Shell 与 Session 宿主 | `frontend/src/renderer/routes/_shell.tsx` 中 `LocalSessionHost`、`ShellOutlet` | 工作台选择任务后导航到现有 Session；不得复制或重挂第二个 SessionView |
| 项目/会话导航 | `components/Sidebar.tsx`、`lib/navigate-to-session.ts` | 增加工作流入口和返回工作台的位置，保留项目/主机身份 |
| 对话/终端/Inspector | `components/SessionView.tsx`、`SessionInspectorRail.tsx`、`TerminalPane.tsx` | 复用已有对话、终端缓存、文件和 Diff；Workflow 只补任务上下文 |
| 类型化 HTTP | `frontend/src/renderer/lib/api-client.ts`、`frontend/src/api/schema.ts` | 从生成的 `components/paths` 取类型，禁止复制一份业务 DTO |
| 数据缓存和事件 | `hooks/useWorkspaceQuery.ts`、`lib/event-transport.ts`、`lib/events-connection.ts` | 同一个 QueryClient 和现有 SSE 连接；新增命名事件失效规则 |
| Operator 原生审批 | `frontend/src/main/operator-ipc.ts`、`frontend/src/main.ts`、`frontend/src/preload.ts` | 保留来源/顶层 frame/loopback/正文 digest/OS 验证；按固定操作扩展 |
| 本地 UI 偏好 | `frontend/src/renderer/stores/ui-store.ts` | 只存模式、选择、折叠、筛选；不持久化派生业务成功状态 |
| 文案与测试 | `frontend/src/renderer/i18n/`、`frontend/e2e/`、`frontend/playwright.config.ts` | 复用 i18n、Vitest、Playwright 和现有 fixture 模式 |

当前没有 Workflow 专用路由/组件。现有 Operator IPC 仅接受 `bind/decide/resolve/reconcile/resume` 的 POST 路径，尚不能覆盖创建 Workflow、调度暂停/恢复、策略 PUT 或 M4 集成审批。必须完成正式桌面调用链，不能给用户一个始终 403 的按钮，也不能用通用 HTTP 透传绕过边界。

源码补核发现两项首版闭环缺口：`backend/internal/service/scheduler/planner.go` 的 Tick 只为已有 Task 做公平准入，并非将自然语言目标拆成 DAG 的 Planner；`service/intervention/coordinator.go` 的 Stop 明确只返回 `ReplanRequest`，不会执行新计划。M5 必须把总设计 §4、§6 的自动候选计划与停止后重规划接齐，不能仅展示这个请求便声称功能完成。

## 3. 双模式交互

### 3.1 默认项目与会话视图

- 保留当前项目和会话列表，提供“工作流”入口。选中属于 Workflow 的会话时显示任务标题、当前 Attempt、Agent 和返回工作台入口。
- 中央继续显示现有 Chat/Terminal；切换模式前后的 Session ID、消息草稿、滚动位置及终端连接保持关联。保留用户已有会话，不因切换模式调用 Spawn/Bind/Resume。
- 干预区域固定显示目标 Agent、Task、Session 和当前能力；旧 Attempt 不再有效时要求重新选择，不能悄悄投递给另一个 Session。

### 3.2 工作流列表与工作台

- 列表展示标题、项目/仓库、执行状态、需要处理的问题和更新时间，具有真实空态、加载态、错误态和分页；不使用演示数据冒充运行状态。
- 工作台提供任务图和任务列表两种查看方式，共用选中 Task。首版 DAG 只读，节点可键盘访问；不提供拖动修改依赖的隐式写操作。
- 节点显示服务端状态、Agent、依赖阻塞原因及质量状态。RUNNING、BLOCKED、UNKNOWN、Gate 未通过必须可区分；颜色不是唯一信息载体。
- 点击有绑定的任务进入原 Session；无绑定时显示“尚未创建会话”和原因，不能为查看详情而启动 Agent。
- 详情包括 Attempt 历史、逐仓 Artifact、测试、Review、路径冲突、协作消息与审批。历史结果显示对应版本，不能当成最新工件的质量证据。

### 3.3 创建与控制

- 用已有项目/仓库创建 Workflow，支持标题/目标、任务、依赖、Agent 及服务端支持的 Repo scope。默认从目标生成结构化候选计划，用户可手工调整；候选计划先预览、后确认，不能直接派发未经校验的模型输出。
- 服务端仍负责 DAG、路径、权限和预算校验；前端校验只提高表单可用性，不授予执行权。
- 调度暂停表示停止新领取，明确区分停止当前 Agent。恢复调度、并发与预算修改通过正式 Operator 通道。
- 并发界面显示策略值和实际可用容量，保留默认 3/硬上限 10 的产品语义；开发会话的调度方式不改变产品运行策略。

### 3.4 干预与协作

- Steer 仅在实时能力允许时可选；不支持时解释原因并让用户选择 Queue 或 Stop & Replan，不能静默改模式。
- Queue 显示实际回执的已排队/已投递/失败/未知；网络超时不生成新的幂等键盲重发。
- Stop & Replan 分开显示停止请求、停止确认与后续计划。未知停止结果不能显示“已安全停止”。
- 展示跨 Agent 消息的源/目标和 TaskRequest；批准任务申请仍经 Operator，由服务端执行物化，不在 UI 自行扩展资源或 DAG。

### 3.5 审批、质量与预算

- 审批详情展示每仓库的目标分支、当前/预期 SHA、任务提交、预演结果与冲突。用户确认的正文即原生审批与最终提交的同一份正文。
- 表单编辑、目标 SHA 漂移、计划版本变更或审批过期后重新获取事实并重新确认；禁止重用旧批准。
- 多仓部分成功显示逐仓结果与整体 PARTIAL/UNKNOWN，不显示整体成功，不自动补执行或回滚。
- Gate/Review 绑定 ArtifactRevision、fingerprint 和 policy；缺证据、失败、未验证、跳过和过期有明确状态。
- 预算显示 global/workflow/task 范围、准确/估算/未知、币种及价格来源；缺失不格式化为 0。已运行任务的开销不能承诺被硬限额瞬时截断。

## 4. 数据和协议约束

### 4.1 已存在的 API

已核实 `workflows.go`、`scheduler.go` 提供：

- `GET/POST /api/v1/workflows`、`GET /api/v1/workflows/{workflowId}`。
- `GET /api/v1/workflows/{workflowId}/tasks` 与 `/tasks/{taskId}`。
- `POST /api/v1/workflows/{workflowId}/activate`。
- `GET /api/v1/scheduler/status`、`PUT /api/v1/scheduler/policy`。
- `GET /api/v1/workflows/{workflowId}/scheduler`。
- `POST /api/v1/workflows/{workflowId}/pause` 与 `/resume-scheduling`。
- global/workflow/task 的 budget GET/PUT。

M2 的能力、干预、协作和 M4 的质量/审批/协调接口，以交付后的 OpenAPI 为准。总体设计中的 `/plan`、`/start`、`/replan` 是草案，不能当成已实现 API。M5-01 产出实际 method/path/DTO/身份/幂等/事件映射；产品必需入口缺失则形成具体接口缺口，完成对应后端接线并重新验证，不能在 Renderer 伪造业务逻辑。

### 4.2 状态同步

- Query key 包含 host/Workflow/Task/Attempt 等真实作用域；切换 Workflow 或本机 daemon 后不展示另一作用域缓存。
- 复用 `event-transport.ts` 的命名事件订阅并失效相关 Query；每节点不新开 SSE。
- 断线时显示最后一次数据与离线状态；重连、游标失效或 daemon 重启时重取相应快照。写入仅在可信连接与当前 revision 可用时发起。
- 对有副作用的写操作关闭通用自动重试；相同操作恢复查询或重放时沿用同一幂等键和正文。版本冲突显示新事实，由用户重新确认。
- 前端不得将 Task、Session 和 Gate 三个不同状态折叠为一个自定义持久状态机。

### 4.3 原生审批接线

扩展现有 `operator:runVerb` 的封闭操作映射（也可在相同模块内按实际类型组织），每项固定 method 与路径模板。保留现有五个动作兼容性；POST 与 PUT 的审批创建、确认及最终请求必须使用一致 method/path/body digest。

Main 从可信主窗口和 daemon 管理器取得来源与地址，Renderer 不提供 baseURL、凭据或任意 header。审批不写 localStorage、URL、遥测或 Renderer 日志。浏览器预览可展示页面，但没有原生审批能力时明确禁用特权动作；Fake 成功只用于测试 fixture。

### 4.4 自动规划与停止后重规划

- 复用已有 Session/Chat Driver 调用所选主控 Agent；产品内规划执行仍受统一准入、能力和预算边界约束，不引入另一个 Provider Loop。离线开发用 Scripted/Fake Driver 验证真实装配，真实 Provider 留在已明确的预算门禁下。
- Planner 输出候选结构化 JSON，包含目标、已选择的 Repo 引用、任务、依赖、角色与范围。服务端独立执行既有 DAG 校验、规模/深度限制、Repo/SHA/权限/预算校验；模型不能添加未选仓库、提高权限或自行批准。
- 候选有不可混淆的 ID、来源请求及预期 Workflow revision，重复请求不再次生成不同计划并误应用。采纳时核对候选内容摘要和当前事实；确认与运行分开，只有 Workflow 服务可写入正式任务图。
- Stop & Replan 必须持有可信停止结果才能产生可采纳的调整。旧 DAG、Attempt、消息、Worktree 与工件保持可追踪；新 revision 不能静默修改仍在执行或 UNKNOWN 的任务及依赖。旧候选遇到新状态必须拒绝或重新生成。
- 用户在 UI 查看计划差异、选择 Agent，并使用已有受控权限入口确认；已验收任务不因重规划被重复执行。手工编辑候选和自动生成使用同一服务端校验入口。
- 所选模型、effort 与 provider/profile 引用只沿已支持的 Adapter 字段传递；不支持时明确报错，不静默替换，不写原生 CLI 配置。M5-01 必须核实是否存在完整透传；缺失的首版必要接线纳入 M5，不只增加无效选择器。

## 5. 质量和验收

1. 同一 Session 在两模式间来回切换后，消息/草稿/终端仍存在，没有额外启动请求。
2. 两个 Workflow 的选择与事件隔离；断线/重连/旧游标后结果与 HTTP 快照一致。
3. 不支持 Steer、UNKNOWN 停止、旧 Attempt、旧 revision、重复点击和断线重试不能触发错误目标或重复副作用。
4. 原生审批真实调用链覆盖新增 POST/PUT 操作；不可信 iframe/来源/路径、正文变更和目标漂移被拒绝。
5. 预算 unknown 和多仓 partial 按事实显示；旧 Review/Gate 不呈现为当前通过。
6. 大图、键盘导航、可读状态、中文文案和既有会话路径回归通过；不因引入工作流拖慢单会话首屏主路径。
7. TypeScript、相关 Vitest/Playwright、受影响后端契约和真实隔离 Electron 检查分别记录命令、退出码及截图。真实桌面未验证不得用浏览器结果替代。
8. 从目标通过 Scripted/Fake 主控实际生成候选，经服务端校验/确认/准入创建任务；循环 DAG、越界仓库/权限和旧候选均失败。可信 Stop 后可采纳新 revision；UNKNOWN 停止不能推进，已验收任务及原工件保留。离线装配通过不等于真实 Provider 验收。

## 6. 实施与交接

统筹只约束交付结果和安全边界，具体算法、文件拆分、测试顺序和子 Agent 安排由执行会话决定。复用本仓现有 React、TanStack、Zustand、组件与测试设施，不为 DAG 添加未经论证的依赖。

完成后交付 `m5-report.md`、实际接口映射、截图/日志引用和文件清单；统筹独立编写 `m5-acceptance.md`。M5 整体验收通过并本地提交后才能派发 M6。真实 Provider/Hello/安装升级尚未完成的范围在 M6 继续保留，不能从离线证据推导正式发布通过。
