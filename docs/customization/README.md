# 多编码 Agent 桌面编排改造区

> **当前有效状态（2026-11-05）：M2 第六轮 Windows 离线 G0 = PASS；M3 Windows 离线 G7 = PASS。** M2 的 [第六轮独立复验](m2-acceptance-round6.md)、M3 的 [独立验收](m3-acceptance.md)、[本轮再次复验](m3-independent-recheck.md) 及 [M3 实施报告](m3-report.md) 为最新事实来源。下方此前第五轮「NOT ACCEPTED」、M3「尚未编码」等段落为历史阶段记录，**不再代表当前状态**。真实四 Provider、Windows Hello 人工正向、完整桌面/安装包及发布验证仍 **NOT_TESTED**；M0-05 真实 Provider 预算未批准。

> **M2 提交决定（2026-11-05）：** 用户要求先提交现有 M2 实现，将第六轮离线独立复验、真实 Agent 完整验收两项暂缓留档。后续入口见 [M2 提交与后续验收留档](m2-handoff.md)。提交不等于验收通过，M2 仍为 NOT ACCEPTED；真实 Provider 预算未批准。

> **M3 设计已准备（2026-11-05，尚未编码）：** [M3 动态调度与预算详细设计](m3-design.md) + [M3 实施计划（M3-01～M3-09）](m3-plan.md)。M3 仅处于 DESIGN READY / IMPLEMENTATION GATED；M2 最新第五轮独立结论仍未通过，第五轮修复报告不是第六轮独立验收；M0-05 未批，禁止真实 Provider。

> **最新独立验收（2026-11-05，第五轮）：M2 NOT ACCEPTED，M3 不准启动。** 第四轮的 F-01 历史 BaseSHA 已通过独立真实双仓库 Git 反例；F-02 正式 CLI/原生审批链有离线正向证据。但新的 **F-03（P1）单仓库 Workflow Worktree 无实时 Git 验证却报告 READY** 已被运行反向用例证实失败；M2 Fake E2E 与 Race 正向通过，不代表安全门禁已闭合。详见 [m2-acceptance-round5.md](m2-acceptance-round5.md)。旧段落保留阶段历史状态。

> 阶段：基础基座与 Q1–Q10 需求确认已完成；**M0 正式验收仍为 BLOCKED，M0-05 预算暂未批准。M1-01～M1-05 全部完成并验证通过（2026-11-03），已本地提交为 `a6c284a`；M1 核心验收 = PASS，零新增回归。** 全程保持零 Provider（0 turn / 0 token）。M2 首轮的 2026-11-04 独立源码与运行验收为 **NOT ACCEPTED**（生产 Gateway 缺失、Steer/HTTP Bind/权限/恢复路径阻断，B-01～B-08 共 8 项）；实施方此后已完成 P0-01～P2-01 的修复并有本地运行证据（[m2-remediation-report.md](m2-remediation-report.md)），**M2 已完成第二轮独立复验**，但**第二轮独立复验仍为 NOT ACCEPTED**：Send/Interrupt generation fencing、多仓库挂载、Agent MCP 协作、Human 身份边界和 Capability Probe 仍有阻断，M3～M6 不得启动。**实施方已完成第三轮修复（R-01～R-08）并通过最终一轮验证**（[m2-remediation-round3.md](m2-remediation-round3.md)：C1～C23 全跑、M2 E2E 24/24 PASS、4 组 race 全 exit 0 无 data race、净新增回归 0；resume 身份再生已补齐），**但仍开放 8 项问题**（含本轮新增的「operator 路由无出厂客户端」——`bind`/`reconcile`/`resume`/`resolve`/`decide` 现已要求 operator token，而桌面应用无任何路径能带上它），**第三轮独立验收现已完成，M2 仍 NOT ACCEPTED**：24/24 Fake E2E、构建/Vet/Race 通过，但新增真实 Git 反向测试发现指定 BaseSHA 未被 Workspace Create 使用（exit 1），出厂 Operator API 客户端仍缺 token 调用链。独立证据见 [m2-acceptance-round3.md](m2-acceptance-round3.md)。真实 Provider 仍 `BLOCKED / NOT_TESTED`（M0-05 预算未批准）。详见 [m2-reacceptance.md](m2-reacceptance.md)、[m2-acceptance.md](m2-acceptance.md)、[m2-remediation-report.md](m2-remediation-report.md) 与 [m2-remediation-round3.md](m2-remediation-round3.md)。

## 基座信息

- 上游：`https://github.com/OrchestratorInc/agent-orchestrator`
- 本地目录：`E:\code\agent-orchestrator`
- 开发分支：`feat/multi-agent-desktop`
- 初始基线：`23840aa54104`
- 技术架构：Go Daemon + Electron/React + SQLite

## 本地约定

1. 尽量复用上游桌面应用、Agent Session、Worktree、终端、审查及 Git/CI 能力。
2. M1 已通过限定范围验收；M2 第三轮独立复验仍为 **NOT ACCEPTED**。上轮 Send/Interrupt generation fencing、真实 Session Manager、多仓库 Git 工作区和 MCP 工具已实现并通过正向测试；但独立反向测试发现指定 BaseSHA 不生效，且 Operator 操作无出厂客户端。以 [m2-acceptance-round3.md](m2-acceptance-round3.md) 为准，不得提前启动 M3～M6。
3. 不更改 `.codex/`、`.claude/` 目录配置；不触碰用户已有 AO 数据。
4. 不直接提交主分支、不推送远程仓库；功能实现与安全边界在需求确认后决定。
5. 开发和调试严格遵循仓库根目录 `AGENTS.md`、`CLAUDE.md` 及相关设计文档。

## 文档入口

- [需求确认](requirements.md)：Q1–Q10 已确认的产品需求与决策。
- [源码差距分析](gap-analysis.md)：实地核查的可复用能力、需要新增的能力与验证障碍。
- [详细设计 V0.1](design.md)：架构、调度、数据库、接口、桌面端、安全、测试与实施里程碑。
- [M0 独立详细设计](m0-design.md)：Windows 基线环境、四 Agent 能力验证矩阵、测试步骤、安全隔离和验收门槛。
- [M0 执行报告](m0-report.md)：环境基线、构建与测试退出码、W-01～W-10 与 A-01～A-15 的逐条证据；M0 正式验收仍为 **BLOCKED**，M0-05 预算暂缓，A-02～A-15 未执行。
- [M0 四 Agent 能力矩阵](m0-capability-matrix.md)：设计 §4.1 的 15×4 矩阵；A-01 已填 8 格（CLI 路径/版本可信、身份状态诊断），其余 52 格仍为 NOT_TESTED。
- [M0 Adapter 差距单与 M1 输入清单](m0-adapter-gaps.md)：四 Agent 协议差异矩阵、兼容方案、阻断清单 B1–B10，以及 M1 可立即开工与必须不假设的事项。
- [M1 开发任务计划](m1-plan.md)：M1-01～M1-05 的范围、依赖、交付物、验收标准与零 Provider 边界；**五个子任务均已标记完成并回填执行记录**。
- [M1 验收报告](m1-report.md)：M1 交付物清单、构建/vet/测试命令与计数、基线隔离证明（8 条既有失败、M1 新增回归为零）、Review 结论与 6 项 P1 修复、未覆盖范围，以及 M2/M3 的接口与未决事项。
- [M1 交接说明](m1-handoff.md)：已归档关闭的历史交接记录——记录 M1 收口时的状态，并保留对 M2+ 仍然有效的授权边界与 Git 注意事项。
- [M1 独立验收与 M2 准入记录](m1-acceptance.md)：独立复核 M1-01～M1-05，保留 Windows 旧基线与 M0 预算阻断。
- [M2 详细设计 V0.1](m2-design.md)：Session 绑定、四 Agent 能力解析、三种干预、协作消息、API/CDC、测试与 M2-01～M2-08 计划。
- [M2 提交与后续验收留档](m2-handoff.md)：当前交付证据，以及暂缓的第六轮离线独立复验、真实 Agent 完整验收的恢复前提与完成条件。
- [M2 Agent 委派提示词](m2-agent-prompt.md)：可直接交给其他 Agent 的实施指令、范围、约束与验收口径。
- [M2 开发任务计划](m2-plan.md)：M2-01～M2-08 的范围、文件归属与实施记录；状态已按第二轮独立复验修订，M2-02/M2-03/M2-04/M2-05/M2-07 标为「Partial」，M2-06 标为「Fail」。
- [M2 阶段报告](m2-report.md)：实施方的变更与测试记录，原结论为「离线开发门禁通过」；该结论不等于完整产品路径验收通过。
- [M2 独立代码与运行验收](m2-acceptance.md)：首轮直接核对源码/Daemon/HTTP/迁移并运行测试；结论为 M2 整体 NOT ACCEPTED，列出 B-01～B-08 共 8 项阻断及重新验收门槛。该文件为历史证据，不随后续修复改写。
- [M2 修复后独立复验](m2-reacceptance.md)：第二轮直接复核修复后的真实代码和运行结果；定向测试、build/vet、Fake E2E 通过，但发现 Send/Interrupt generation、多仓库 Session 工作区、Agent MCP/lease、Human 身份和 Capability Probe 等新阻断，M2 仍 NOT ACCEPTED。
- [M2 修复报告](m2-remediation-report.md)：针对 P0-01～P2-01 的逐项根因、修改位置、新增接口与调用链、数据库变更、安全设计、真实命令退出码、17 条 Fake E2E 场景与 11 条新发现缺陷；结论为「已修复并有本地运行证据，重新申请独立验收」。
- [M2 第三轮修复报告](m2-remediation-round3.md)：针对 [m2-reacceptance.md](m2-reacceptance.md) R-01～R-08 的逐项根因、修改位置与调用链、新增测试名、`workflow_audit_log` 迁移与 append-only 证据、Operator token 与 Principal 拒绝矩阵、多仓库 Worktree 与 Agent MCP 测试结果（含 resume 从 durable state 恢复身份的 Change A）、E2E 场景清单、**最终一轮 C1～C23 实测退出码与 §6.4 Windows 基线归类**（M2 E2E 24/24 PASS、4 组 race 全 exit 0 且无 data race、15 条基线失败逐条归类、2 条本轮自身缺陷已修复复测 exit 0、净新增回归 0），与 R-01～R-08 的对照表，以及仍开放的 8 项问题（含本轮新增的「operator 路由无出厂客户端」）；结论是「具备重新申请独立验收条件」，**不构成 M2 PASS**。
- [M2 第三轮独立代码与运行验收](m2-acceptance-round3.md)：实测 M2 24/24 E2E、核心测试、构建、Race；新反向用例证实请求 BaseSHA 未被 Worktree pin，另有 Operator 客户端缺位；整体 NOT ACCEPTED，暂不进入 M3。
- [M2 第四轮独立验收](m2-acceptance-round4.md)：真实 Git pin 反向 FAIL、Operator 出厂客户端缺失，M2 NOT ACCEPTED。
- [M2 第四轮实施修复](m2-remediation-round4.md)：BaseSHA、逐 Repo 归属、正式 CLI/Electron Operator 审批与 OS Verifier 的实施方交付证据，非独立 PASS。
- [M2 第五轮独立验收](m2-acceptance-round5.md)：F-01 实测已修，F-02 离线链路已验证；新增 F-03 单仓库 Worktree 实时核验绕过，仍 NOT ACCEPTED。
- [M2 Windows 测试基线](m2-baseline-windows.md)：M2 开工前的只读全仓红基线（56 包 / 267 条失败）与两处已知噪声源，供 M2 集合差判读。
- [M2 计划审计](m2-plan-audit.md)：对 `m2-plan.md` 的独立抽样复核（85 个锚点，76 条 `EXACT`、0 条 `FABRICATED`），以及 §6 六条偏差的逐条复核。
- [M3 详细设计 V0.1](m3-design.md)：单机 Scheduler/公平调度、Windows 资源感知、原子 Admission、三层预算、租约恢复、权限、API 与验收矩阵；目前仅设计。
- [M3 实施计划](m3-plan.md)：M3-01～M3-09 的任务拆分、文件边界、依赖、测试命令与准入/退出门槛；M3 已完成 Windows 离线交付。
- [M2 第六轮独立离线验收](m2-acceptance-round6.md)：F-01/F-02/F-03 关闭、完整离线回归与 Pricing 复核；M3 G0 准入 PASS。
- [M3 实施报告](m3-report.md)：调度、预算、运行闭环、迁移/API 与完整普通/Race 证据。
- [M3 独立验收](m3-acceptance.md)：限定 Windows 离线 G7 PASS、剩余真实环境边界。
- [M3 最新独立复验](m3-independent-recheck.md)：再次执行 M3 6 项/Race、M2 F-03 真实 Git 异常回归、代码审查与已知两项历史 Domain 失败。

## 当前环境

- Git：2.45.1.windows.1
- Node.js：v22.20.0。`m0-report.md` §2.1 的结论是：仓库**未声明** Node 主版本（无 `.nvmrc`/`engines`），Node 24 只是 CI pin（`.github/workflows/frontend.yml`，其中 `:134` 另有 22.23.2 的 pin，说明 22 也是官方支持的版本），且 v22.20.0 已实测足以完成 `npm ci`、`typecheck` 与前端构建；是否升到 24 未作决定，也不影响当前构建基线。
- Go：本地 go1.26.3 windows/amd64，而 `backend/go.mod` 与 `go.work` 要求 1.27.1。`m0-report.md` §2.1 的结论是：`GOTOOLCHAIN=local` 直接失败，默认 `auto` 会从模块缓存取用已下载的 1.27.1 并成功完成 `go build`/`go vet`；**是否接受这种隐式取用**仍未决定（见报告 §8.3 第 1 项）。
- 状态：源码已克隆、开发分支已建立、首版设计已完成；依赖安装与构建均在**本仓库之外的隔离工作树** `E:\code\ao-m0-lab` 完成（`npm ci`、`go build`、前端 build、dev Electron 启动），主工作树 `E:\code\agent-orchestrator` **现已执行 M1 Go 编译、目标包测试和静态检查，以及 M2 修复后的构建/vet/定向与 race 测试、sqlc/api 生成和前端类型检查**；M0 的 Electron 桌面运行验证仍在隔离工作树进行。M0 正式验收仍为 **BLOCKED**；M1 限定范围已验收 PASS，源码和验收材料已提交在 `a6c284a4d060431371306072e6c80abfedf22328`；M2 首轮独立验收 NOT ACCEPTED，其 8 项阻断已修复并有本地运行证据，**第二轮独立复验已完成，M2 仍 NOT ACCEPTED**。真实 Provider 仍 `BLOCKED / NOT_TESTED`（M0-05 预算未批准）。所有 M2 实现均处于本地未提交状态，HEAD 仍为 M1；详见 [m2-acceptance.md](m2-acceptance.md) 与 [m2-remediation-report.md](m2-remediation-report.md)。
