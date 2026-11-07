# M5 执行会话派发说明

本说明只在统筹确认 M4 独立验收通过并完成本地提交后派发。派发消息必须附实际 M4 提交 SHA 和验收记录位置；本文件存在不表示 M5 已获准启动。

## 任务

在 `E:/code/agent-orchestrator` 接续多编码 Agent 桌面改造，完成 M5 双模式桌面功能。先读取：

1. 根目录 `AGENTS.md`、`CONTRIBUTING.md`、`DESIGN.md` 及其有效导入规则。
2. `docs/customization/delivery-status.md`。
3. `docs/customization/m5-design.md`、`m5-plan.md`。
4. 派发消息给出的 M4 独立验收报告、提交 SHA 和 M4 实施报告。

你负责 M5 的实现、自测、返修及实施报告。统筹负责独立验收和验收后的本地提交，再开启 M6。当前总并发为 2，本阶段由一个执行会话完成，不创建子 Agent。具体实现方法、文件拆分和测试安排由你决定，不必逐步等待统筹批准。

## 约束

- 当前会话使用 Astra（`gpt-6-astra`），按难度选择思考强度；不派发子 Agent，不静默改其他模型，不使用 `codexhost-delegation`。
- 保护未提交改动，不覆盖未知文件。按真实源码接续 M4，不能把总体设计 API 草案当作已有接口。
- 复用持久 Session 宿主、终端缓存、类型化 API、SSE 和原生 Operator 审批；不得创建第二个 Scheduler/SessionRegistry/审批系统。
- 核心结果包括双模式不丢 Session、DAG/任务与 Agent 定位、能力感知干预、协作/TaskRequest、逐仓质量/审批、预算和恢复状态。
- 新增操作需要完整桌面到 Daemon 的受限调用链；不得用 Renderer 任意 HTTP/命令通道绕过身份和精确审批。
- 不改 `.codex/.claude` 配置；不提交配置；不运行真实付费 Provider；不写用户真实 AO 数据；不 Push、Merge 或发布。真实桌面验证使用隔离 worktree/数据。
- 统筹准备的后续文档不需要重新生成。新写 `m5-contracts.md` 和 `m5-report.md`；统筹验收报告由统筹维护。

## 完成与返修交接

交付内容：实际变更清单、HEAD/工作树状态、产品行为、API 缺口及处理、测试命令/真实退出码/原始日志位置、桌面截图、未验证项目、M6 输入。HANDOFF 不等于统筹 ACCEPTED，测试失败原样报告。

收到返修任务时按缺陷证据修复并提供对应反例复测，保留其他工作。同一问题连续三次修复仍失败时报告根因判断，不盲改。

M5 交付后不要自行进入 M6；等待统筹完成独立验收和本地提交。
