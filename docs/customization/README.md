# 多编码 Agent 桌面编排改造区

> 阶段：基础基座与 Q1–Q10 需求确认已完成；**M0 无预算范围的环境、构建、部分桌面和 Agent 预检已执行，正式验收仍为 BLOCKED；M0-05 预算暂未批准，后续执行。业务代码尚未开始改造。** 用户已授权建立 M1-01～M1-05 的零 Provider 开发任务计划；当前尚未开始 M1 业务实现。

## 基座信息

- 上游：`https://github.com/OrchestratorInc/agent-orchestrator`
- 本地目录：`E:\code\agent-orchestrator`
- 开发分支：`feat/multi-agent-desktop`
- 初始基线：`23840aa54104`
- 技术架构：Go Daemon + Electron/React + SQLite

## 本地约定

1. 尽量复用上游桌面应用、Agent Session、Worktree、终端、审查及 Git/CI 能力。
2. M1 按 [m1-plan.md](m1-plan.md) 的范围与顺序实施；未列入 M1 的 M2～M6 能力及 Codex 风格 UI 改造不得提前实现。
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
- [M1 开发任务计划](m1-plan.md)：M1-01～M1-05 的范围、依赖、交付物、验收标准和零 Provider 边界。

## 当前环境

- Git：2.45.1.windows.1
- Node.js：v22.20.0。`m0-report.md` §2.1 的结论是：仓库**未声明** Node 主版本（无 `.nvmrc`/`engines`），Node 24 只是 CI pin（`.github/workflows/frontend.yml`，其中 `:134` 另有 22.23.2 的 pin，说明 22 也是官方支持的版本），且 v22.20.0 已实测足以完成 `npm ci`、`typecheck` 与前端构建；是否升到 24 未作决定，也不影响当前构建基线。
- Go：本地 go1.26.3 windows/amd64，而 `backend/go.mod` 与 `go.work` 要求 1.27.1。`m0-report.md` §2.1 的结论是：`GOTOOLCHAIN=local` 直接失败，默认 `auto` 会从模块缓存取用已下载的 1.27.1 并成功完成 `go build`/`go vet`；**是否接受这种隐式取用**仍未决定（见报告 §8.3 第 1 项）。
- 状态：源码已克隆、开发分支已建立、首版设计已完成；依赖安装与构建均在**本仓库之外的隔离工作树** `E:\code\ao-m0-lab` 完成（`npm ci`、`go build`、前端 build、dev Electron 启动），主工作树 `E:\code\agent-orchestrator` 本身**仍未安装依赖、未编译、未运行桌面应用**（有意的隔离）。M0 正式验收仍为 **BLOCKED**；M1 仅按 [m1-plan.md](m1-plan.md) 的限定范围推进，详见 [m0-report.md](m0-report.md) §8。
