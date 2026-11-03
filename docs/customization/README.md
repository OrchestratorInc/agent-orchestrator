# 多编码 Agent 桌面编排改造区

> 阶段：基础基座已落地，正在逐问逐答确认需求。**尚未开始详细设计或业务代码改造。**

## 基座信息

- 上游：`https://github.com/OrchestratorInc/agent-orchestrator`
- 本地目录：`E:\code\agent-orchestrator`
- 开发分支：`feat/multi-agent-desktop`
- 初始基线：`23840aa54104`
- 技术架构：Go Daemon + Electron/React + SQLite

## 本地约定

1. 尽量复用上游桌面应用、Agent Session、Worktree、终端、审查及 Git/CI 能力。
2. 需求问答结束前，不预先实施编排器重构和 Codex 风格 UI 改造。
3. 不更改 `.codex/`、`.claude/` 目录配置；不触碰用户已有 AO 数据。
4. 不直接提交主分支、不推送远程仓库；功能实现与安全边界在需求确认后决定。
5. 开发和调试严格遵循仓库根目录 `AGENTS.md`、`CLAUDE.md` 及相关设计文档。

## 文档入口

- [需求确认](requirements.md)：持续记录已确认需求、待答问题与取舍。
- [详细设计占位](design.md)：需求确认后补充设计、模块边界与验收标准。

## 当前环境

- Git：2.45.1.windows.1
- Node.js：v22.20.0（上游开发文档最低 Node.js 24，待决定工具链升级方式）
- Go：go1.26.3 windows/amd64（上游开发文档最低 Go 1.27.1，待决定工具链升级方式）
- 状态：已克隆源码、建立开发分支；未安装依赖、未编译、未运行桌面应用。
