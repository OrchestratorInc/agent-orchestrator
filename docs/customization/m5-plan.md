# M5 桌面双模式实施任务

日期：2026-11-06。状态：任务设计；**M4 整体独立验收并本地提交前禁止启动 M5 编码**。目标与验收依据：[m5-design.md](m5-design.md)。调度规则：[delivery-status.md](delivery-status.md)。

## 交付方式

执行会话使用 `gpt-6-astra`；建议整体实现 high，复杂身份/恢复问题可提高思考强度。用户最新明确总并发为 2，本任务只保留统筹和一个执行会话，不派发子 Agent。具体实现方法和任务粒度由执行方决定；本计划定义结果与依赖，不要求机械照抄步骤。

复用当前 checkout 或合适现有 worktree，保护 dirty；真实 Electron 验证遵循仓库桌面隔离规范。不开真实 Provider turn、不改 `.codex/.claude` 配置、不发布或远程推送。阶段提交由统筹独立验收后执行。

## 工作包与依赖

| ID | 交付结果 | 前置 | 建议文件/接点 |
|---|---|---|---|
| M5-01 | M4 提交基线、实际接口/权限/事件映射、必需缺口清单 | M4 PASS + commit | `frontend/src/api/schema.ts`、`backend/internal/httpd/controllers/`、`frontend/src/main/operator-ipc.ts`；输出 `docs/customization/m5-contracts.md` |
| M5-02 | Workflow 查询、事件同步、错误与连接状态 | 01 | 新 `frontend/src/renderer/hooks/useWorkflows.ts`，`lib/event-transport.ts` 和相应测试 |
| M5-03 | 默认会话视图与工作台切换、任务到现有 Session 导航 | 02 | `routes/_shell.tsx`、`components/Sidebar.tsx`、新 Workflow 路由及 `components/workflow/` |
| M5-04 | 自动候选计划/安全重规划、DAG/任务详情、创建/激活、能力感知干预与协作 | 02、03 | Workflow 组件、`backend/internal/service/workflow/`、现有 Session/Chat/Intervention/TaskRequest、必要持久化与生成 API 接线 |
| M5-05 | Operator 审批、调度/预算控制、逐仓质量与集成结果 | 01、02、04 | `main/operator-ipc.ts`、`main.ts`、`preload.ts`、`lib/bridge.ts`、Workflow 审批/预算面板 |
| M5-06 | 断线/重启/旧数据/可访问性与既有会话回归 | 03～05 | `frontend/e2e/workflow-*.spec.ts`、组件行为测试、受影响 Go 契约测试 |
| M5-07 | 隔离 Electron 实测、报告和 M6 交接 | 06 | `docs/customization/m5-report.md`，截图与原始日志在隔离证据目录 |

文件名是建议落点，执行方可按现有架构调整并在报告说明。禁止手写生成的 API 类型、复制 Scheduler 或原生审批系统。

## M5-01：可执行契约

- [ ] 核对真实 M4 提交 SHA 和工作树差异，读取统筹验收记录。
- [ ] 对每个页面动作列出实际 HTTP method/path、生成 DTO、Principal、revision、幂等键、成功/失败回执和 CDC 名称；记录源文件位置。
- [ ] 对照 M5 设计，明确当前 Operator IPC 五个 POST 动作与新增创建/策略 PUT/集成动作的差距。
- [ ] 检查自动规划、模型/effort/profile 透传是否已存在。已知 Scheduler Tick 不是目标拆分器，M2 Stop 只生成 ReplanRequest；在 M5-04 接齐总设计要求的闭环。其他缺口以源码定位，不假装已经存在，也不静默移出首版范围。

验收：接口表可逐项映射到源码和生成 spec；无使用草案 API 的组件。必需缺口的最小后端接线纳入对应工作包，不用无效按钮代替功能。

## M5-02：数据连接

- [ ] Workflow/Task/Attempt/质量数据从现有 apiClient 获取；Query key 具备实际作用域。
- [ ] 在既有事件连接接入实际命名事件，重连/reset 后重新获取快照。
- [ ] 显示加载、空态、权限拒绝、错误 request ID、失联和重新同步状态。
- [ ] 有副作用操作不通用自动重试；同一请求重试保留幂等键/正文，冲突刷新事实。

验收用例：两个 Workflow 间切换后注入旧请求响应及 CDC；选中页面不被旧结果覆盖，重连后显示新事实。删除作用域/失效判断应让该行为用例失败。

## M5-03：双模式与会话保留

- [ ] 工作台入口、Workflow 列表、只读 DAG 和等价任务列表可访问。
- [ ] 任务定位到现有 Session 路由；无 Session 时仅展示阻塞原因。
- [ ] 切换模式保留当前 Session、草稿、终端与返回位置；复用 `LocalSessionHost` 和 TerminalCache。
- [ ] 新视图按现有模式延迟加载，采用根目录 DESIGN.md 与 i18n。

验收用例：在含已有对话和终端的 Session 输入未发送草稿，切到工作台再返回；草稿/会话一致，网络记录中无 Spawn/Bind。键盘可选节点并进入详情。

## M5-04：任务控制与干预

- [ ] 将目标交给现有受控 Session/Chat 执行路径，生成结构化候选；使用既有 DAG/Repo/权限/预算校验，记录候选 ID、来源、摘要与预期 revision。测试使用 Scripted/Fake Driver，不调用真实付费 Provider。
- [ ] 候选预览/手工调整/确认走同一后端边界；正式任务由 Workflow 服务持久化，重放不多建任务、不多次 Spawn。模型输出不得提升权限、引入未选仓库或直接写 Git/终端命令。
- [ ] Stop 可信确认后生成候选新 revision，显示计划差异；保留旧 DAG/Attempt/Worktree/已验工件，阻断 UNKNOWN、旧候选及对仍运行任务的静默变更。确认后才恢复可执行任务。
- [ ] 核实所选 Agent 的模型/effort/profile 真实透传，复用现有 Adapter 映射；缺失支持明确拒绝，不写原生 CLI 配置、不静默换模型。
- [ ] 创建/激活展示实际 DAG 与仓库 scope，后端校验错误定位到字段。
- [ ] 展示当前/历史 Attempt、Agent、路径阻塞、消息来源、TaskRequest 与实际调度原因。
- [ ] Steer/Queue/Stop & Replan 依据服务端能力；发送前后绑定准确 Attempt/Session，显示原始状态含 UNKNOWN。
- [ ] 拒绝无效降级和重复副作用；用户取消保留草稿与现有工作。

验收用例：自然目标经 Scripted Driver 到候选、校验、确认、持久 DAG 和统一调度的装配路径通过；循环/越界候选失败；旧候选 CAS 拒绝；可信 Stop 后保留原工件并产生可确认新计划，UNKNOWN 不推进。不支持 Steer 时不能发 steer；切换 Attempt 后旧请求不能写新会话；Queue 超时后查询/重放仍是同一请求。

## M5-05：审批、质量和预算

- [ ] 封闭映射新增必要 Operator 动作；保留旧五动作兼容性，按实际 method 支持 POST/PUT。
- [ ] 原生确认、OS 验证、最终操作使用相同 method/path/body；Main 每步验证调用来源和当前 daemon。
- [ ] 逐仓显示目标 SHA/预演/冲突/部分成功；审批漂移或过期拒绝继续。
- [ ] Gate/Review 关联最新工件与策略，未知/缺失/旧证据不显示 PASS。
- [ ] 三级预算、实际容量和配置上限区分显示；未知数值不转为零。

验收用例：新增 PUT 审批与最终请求 method/digest 一致；修改正文或目标拒绝；iframe/外部来源不能调用；多仓一个成功一个失败显示 PARTIAL；旧 Review 与 unknown budget 真实呈现。

## M5-06/07：验证和交接

以下为已存在脚本的调用方式；测试路径按实际交付文件收敛，命令和实际输出须写进报告。命令通过 RTK 执行，PowerShell 使用 `-NoProfile`。

```text
# 仓库根
rtk npm run frontend:typecheck
rtk git diff --check

# frontend，先相关测试，阶段收口运行受影响完整套件
rtk npm test -- src/renderer/components/workflow src/renderer/hooks/useWorkflows.test.tsx src/main/operator-ipc.test.ts
rtk npm run test:e2e -- e2e/workflow-navigation.spec.ts e2e/workflow-control.spec.ts
rtk npm run build

# backend，仅有契约/服务端接线变化时触发相应范围
rtk go test ./internal/httpd/... ./internal/cli/...
# 修改 DTO/specgen 后，回仓库根运行
rtk npm run api
```

新增测试验证真实导出行为，不断言源码字符串、配置字段或脚本清单；一项缺陷只保留有力反例。测试文件不超过 300 行、脚手架不超过 400 行；超限按 TESTING.md 申报。

真实桌面检查在隔离工作树和独立 AO_DATA_DIR 运行，真实 `npm ci`，不共享 node_modules。记录实际构建、启动命令、临时数据路径与进程清理结果。根据项目要求用 `ao preview` 展示前端；浏览器验证和真实 Electron 分别取证。Windows Hello 真人验证若需用户操作，保留明确 NOT_TESTED，不伪造人工确认。

交付文件：

- `m5-contracts.md`：已核实契约和处理后的接口缺口。
- `m5-report.md`：基线/文件、行为变化、验证命令及退出码、截图/日志路径、未验范围。
- M6 输入：剩余首版需求、原生审批/真实 Provider/打包升级验证条件。

统筹验收通过后编写 `m5-acceptance.md`，本地提交并记录 SHA，再派发 M6。任务文档准备不构成 M5 编码放行。
