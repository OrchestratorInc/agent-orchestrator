# M1 交接说明（已关闭）

> 归档时间：2026-11-03
> 工作区：`E:\code\agent-orchestrator`
> 分支：`feat/multi-agent-desktop`
> **交接状态：CLOSED。M1 已完成，本文件不再是待办工作入口。**

## 1. 交接已关闭

M1-01～M1-05 全部完成并验证通过，M1 核心验收 = **PASS**，M1 新增回归为零。原先由本文件承载的「下一 Agent 接手步骤」已全部执行完毕，不再适用。

**验收证据见 [M1 验收报告](m1-report.md)**：命令与退出码、目标包测试计数、Workflow 专项扫描、`-race` 结果、生成物可复现性、`623871d` pristine worktree 的基线对照，以及 Review 结论与修复清单。

| 子任务 | 状态 |
| --- | --- |
| M1-01 数据库迁移与领域模型 | 已完成 |
| M1-02 DAG 校验器 | 已完成 |
| M1-03 Workflow Service 与 Task 生命周期 | 已完成（含 Review 后的修复与验收） |
| M1-04 基础 HTTP API 与 CDC/SSE | 已完成 |
| M1-05 回归测试、恢复验证与验收归档 | 已完成 |

范围与交付物见 [m1-plan.md](m1-plan.md)；未覆盖项与 M2/M3 未决事项见 [m1-report.md](m1-report.md) §6、§8。

## 2. 不可突破的授权边界（对 M2+ 继续有效）

以下边界不随 M1 关闭而失效，M2 及之后各阶段同样适用：

- 只在 `E:\code\agent-orchestrator` 的 `feat/multi-agent-desktop` 分支工作。
- 不调用 Codex、Claude Code、OMP、DeepSeek 或任何真实 Provider；不启动真实 Agent、不发送 prompt、不消耗 Token。
- 不读写真实 `~/.ao`，只使用临时测试目录/测试数据库。
- 不修改 `.codex/`、`.claude/` 下任何配置。
- 不修改 M0 handoff 基线文件，不做无关重构。
- 不推送远程仓库、不发布、不部署；不擅自合并分支。
- 不提前开展 M2 Session/Agent 绑定、M3 动态调度、M4 Review Gate 或 M5 Electron/React UI。

补充：**M0 正式验收仍为 BLOCKED，M0-05 预算未批准**。M1 完成不改变该状态，也不代表获得 Provider 调用授权。

## 3. Git 注意事项

工作区处于未提交状态——M1 的全部实现（新增 migration、domain、store、service、controller、测试与生成物）都尚未提交。不要 reset、clean、checkout 覆盖或批量格式化整个仓库。

sqlc 生成目录在 `git status` 中显示多项 modified，但多数并非真实内容改动：本轮已用运行前副本做字节 diff 核实，只有 `gen/models.go`（+77，workflow 结构体）与新增的 `gen/workflow.sql.go` 是真实变化，其余 35 项是 `core.autocrlf` 的行尾产物、内容字节一致。核实时应以 `git diff --name-only`、`git diff --numstat`、`git diff --check` 为准，不要因行尾噪声重跑生成或手工修改生成文件。

禁止直接编辑 `backend/internal/storage/sqlite/gen/*`，应修改 query/config 后运行 `npm run sqlc`；`npm run api` 同理，OpenAPI 与前端类型必须由命令生成而非手改。
