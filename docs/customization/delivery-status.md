# 后续阶段调度与验收

日期：2026-11-06（Asia/Shanghai）。总设计：[design.md](design.md)。本文件记录统筹层的阶段门禁；M4 内部实施进度仍以 [m4-execution-ledger.md](m4-execution-ledger.md) 和实际源码为准。

## 用户最新约束

- 统筹负责后续任务文档、派发、监控、独立验收及返修，不规定执行会话如何实现，也不管理它及其子 Agent 是否同时推进。
- 所有执行会话及子 Agent 使用 Astra（`gpt-6-astra`），思考强度按难度选择；不得使用 `codexhost-delegation`。
- **每个里程碑阶段先验收成功，再本地 Git 提交，最后开启下一阶段开发。** 当前按总体设计的 M4、M5、M6 划分，不把内部工作包当作新增里程碑。
- M4 会话结束后可新开会话实施下一阶段，不必复用旧会话。实现方法、内部拆分和子 Agent 使用由执行会话自行决定。
- 新授权包含阶段验收后的本地提交；不包含远程 Push、目标分支 Merge、发布、真实 Provider 预算或配置文件提交。`.codex/`、`.claude/` 配置和真实 AO 数据边界继续有效。

## 当前责任与状态

| 阶段 | 执行责任 | 统筹状态 | 放行条件 |
|---|---|---|---|
| M4 代码验收与审批 | 会话 `01a1acb1-e2b9-7383-b0c9-02e5eef0ad67` 已完成交接 | **本地离线阶段 PASS**：完整集成 race exit 0（910.933s），契约/build/vet/TS 通过，127 文件验收前后一致；SV-01/03/04/05 关闭，SV-02 环境限制保留；准备本地提交 | 详见 [统筹验收](m4-supervisor-acceptance.md)；提交成功才放行 M5 |
| M5 桌面双模式 | M4 完成并提交后派发新 Astra 会话 | [设计](m5-design.md)、[任务计划](m5-plan.md)、[派发说明](m5-agent-prompt.md) 已落盘；未派发编码 | M4 提交、API 契约核对 |
| M6 稳定发布准备 | M5 完成并提交后派发 Astra 会话 | [阶段计划](m6-plan.md) 已落盘；未派发编码 | M5 提交、M6 计划中适用门禁 |

当前仓库为 `E:/code/agent-orchestrator`，分支 `feat/multi-agent-desktop`，接手时 HEAD 为 `129e13ca5d8df210cdafcc2e061d521109130e27`。M4 源码、测试、生成物与文档已交付并通过限定验收；统筹按明确清单本地提交，运行态 `.tmp/`、`backend/.tmp/` 不纳入提交。执行方旧文档中的“提交未授权/尚待统筹验收”为其交接时状态，统筹提交依据用户后续明确授权及本文件最新记录。

## 验收与提交规则

1. 接收报告、文件范围、实际测试命令和退出码。Agent 自报完成只表示 HANDOFF。
2. 对照需求、源码、关键失败路径和真实执行证据独立检查。静态阅读、Fake E2E、真实 Git、桌面运行、安装升级分别记录，不互相替代。
3. 发现缺陷形成带位置、复现及验收条件的返修单，派回执行会话；实现方法由执行方选择。同一问题连续三次修复未通过时停止盲改并报告。
4. 验收对象绑定 HEAD 和未提交变更；验收后若源代码再次变化，复核受影响项后才能提交。新增 P0/P1、必需门禁失败或缺失不得标为 PASS。
5. 按确认的阶段文件逐个暂存；不使用全仓 `git add -A`。检查暂存 diff，不纳入配置、凭据、运行态、构建输出或无关 dirty 文件。确需提交配置时单独请求明确授权。
6. 使用 conventional commit，例如 `feat: add workflow quality and integration gates`。记录真实 SHA、`git status` 和 `git show --stat`，再派发下一阶段。
7. 提交失败时保留验收与暂存状态，不宣称阶段提交完成，也不开启下一阶段编码。

## 已知完整验收缺口

- M2、M3 历史最新结论为 Windows 离线限定范围 PASS；不代表四个真实 Provider、Windows Hello 真人人工正向或安装包验收。
- M0-05 真实 Provider 预算未批准，保持 BLOCKED；研发代理的使用不等于产品内 Provider E2E 获批。
- 两项既有 Handoff Note 测试历史失败仍需在 M6 对照处理，不能把全仓回归描述为全绿。
- M4 已提供 [技术交接](m4-m5-handoff.md)，M5 用实际生成 OpenAPI 核定质量 API；网络 Push 尚无生产 transport，不能将本地 bare 模拟标为可用 GitHub Push。原生审批、自动修复和全仓历史失败等限制见统筹验收，不能用阶段 PASS 覆盖这些缺口。
