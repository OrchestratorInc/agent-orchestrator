# 多编码 Agent 桌面编排改造区

> 阶段：基础基座已落地，Q1–Q10 核心需求已确认，**V0.1 详细设计与源码差距分析已写入；尚未进行业务代码改造或本地运行验收。**

## 基座信息

- 上游：`https://github.com/OrchestratorInc/agent-orchestrator`
- 本地目录：`E:\code\agent-orchestrator`
- 开发分支：`feat/multi-agent-desktop`
- 初始基线：`23840aa54104`
- 技术架构：Go Daemon + Electron/React + SQLite

## 本地约定

1. 尽量复用上游桌面应用、Agent Session、Worktree、终端、审查及 Git/CI 能力。
2. 详细设计完成评审与执行计划确认前，不擅自实施编排器重构和 Codex 风格 UI 改造。
3. 不更改 `.codex/`、`.claude/` 目录配置；不触碰用户已有 AO 数据。
4. 不直接提交主分支、不推送远程仓库；功能实现与安全边界在需求确认后决定。
5. 开发和调试严格遵循仓库根目录 `AGENTS.md`、`CLAUDE.md` 及相关设计文档。

## 文档入口

- [需求确认](requirements.md)：Q1–Q10 已确认的产品需求与决策。
- [源码差距分析](gap-analysis.md)：实地核查的可复用能力、需要新增的能力与验证障碍。
- [详细设计 V0.1](design.md)：架构、调度、数据库、接口、桌面端、安全、测试与实施里程碑。

## 当前环境

- Git：2.45.1.windows.1
- Node.js：v22.20.0（上游开发文档最低 Node.js 24，待决定工具链升级方式）
- Go：go1.26.3 windows/amd64（上游开发文档最低 Go 1.27.1，待决定工具链升级方式）
- 状态：已克隆源码、建立开发分支并完成首版设计；未安装依赖、未编译、未运行桌面应用。
