# Orchestrator.inc 二次开发源码差距分析

> 调研基准：上游 `23840aa54104`（克隆于 2026-11-03）；本地分支 `feat/multi-agent-desktop`。结论来自本地源码与仓库自带文档的静态核查，**不等同于功能运行验证**。

## 1. 已有能力与复用落点

| 需求 | 证据（仓库相对路径） | 评估 |
| --- | --- | --- |
| Go Daemon / CLI / Electron | `docs/architecture.md`、`backend/internal/daemon/daemon.go`、`frontend/src/main.ts` | 保留现有进程与 API 边界 |
| 编码 Agent 注册 | `backend/internal/adapters/agent/registry/registry.go` | Codex、Claude、OMP、DeepSeek Harness 均已注册，无需重建基础注册器 |
| Native Chat / ACP Driver | `backend/internal/adapters/chatdriver/registry/registry.go` | 四个指定执行器已有 Chat Driver，需逐项实测本机版本和认证 |
| OMP Chat | `backend/internal/adapters/chatdriver/ompacp/driver.go` | 以 `omp acp` 运行；声明 Usage/Diffs/Plans，审批模式在进程启动时确定 |
| DeepSeek Harness Chat | `backend/internal/adapters/chatdriver/deepseekharnessacp/driver.go` | 使用 `dsh --profile acp`，配置项映射模型/思考强度，不可误用通用参数 |
| Steer / Queue | `backend/internal/ports/chat_steer.go`、`backend/internal/service/chat/steer.go`、`backend/internal/httpd/controllers/conversation_steer.go`、`conversation_queue.go` | 已有严格 Steer 与 durable Queue 路线；不应重写发送和幂等逻辑 |
| Chat 会话、恢复与事件 | `backend/internal/service/chat/`、`backend/internal/session_manager/`、`backend/internal/storage/sqlite/queries/conversations.sql` | 复用原生会话 ID、Host 续接、事件/历史归档；保持单会话单控制器约束 |
| Agent/Agent 方向信息 | `backend/internal/service/chat/steer.go` 的 SenderSessionID、`backend/internal/ports/session_interaction.go` | 有会话来源记录基础；跨任务协作授权仍需补充 |
| Git Worktree + Windows | `backend/internal/adapters/workspace/gitworktree/`、`docs/architecture.md` | 已有工作区创建、保留、Windows 恢复与终端 ConPTY；沿用而非再造 |
| 多仓库项目 | `backend/internal/domain/project.go` 的 ProjectKindWorkspace / WorkspaceRepoRecord / SessionWorktreeRecord | 已有根仓库和子仓库结构；工作流的跨仓库依赖/集成仍需新增 |
| PR / Reviewer / CI 观察 | `backend/internal/service/review/`、`backend/internal/ports/reviewer.go`、`backend/internal/observe/scm/`、`docs/STATUS.md` | 复用已有 Review/PR 能力，为本地无 PR 场景补质量门禁 |
| 定时任务 | `backend/internal/service/automation/scheduler.go` | 该 Scheduler 面向定时运行（RRule、到期扫描与租约），**不是**通用任务 DAG 调度器 |
| SQLite / CDC / SSE | `backend/internal/storage/sqlite/`、`backend/internal/cdc/`、`docs/architecture.md` | 新表使用新 migration、SQLite trigger/change_log，复用既有 SSE；不要额外自造一套事件总线 |
| 桌面 UI | `frontend/src/renderer/routes/_shell.tsx`、`components/Sidebar.tsx`、`components/SessionView.tsx`、`components/SessionInspectorRail.tsx` | 保留 shell / SessionView，增加工作流视图与全局模式切换 |
| UI 设计边界 | `DESIGN.md` | 后续改动遵循现有 token、shadcn 风格和增量迁移原则 |
| 模型与用量基础 | `backend/internal/service/usage/`、`backend/internal/service/agent/`、`backend/internal/ports/agent.go` | 有模型探测、Agent 认证和用量基础；统一预算执行是新增能力 |

## 2. 需要新增或增强的业务能力

1. **Workflow 领域模型**：独立于已有 Session 的 workflow/task/attempt/dependency/approval 记录，支持任务图、跨仓库目标和终态验收。
2. **DAG Planner + Validator**：结构化输出、无环校验、预算与权限校验，禁止子 Agent 直接创建未授权的可执行任务。
3. **DAG Scheduler**：就绪判断、原子领取、负载/预算/执行器配额、超时与心跳、失败状态推进、重启重建。
4. **编排能力映射**：聚合现有 Agent + Chat Driver 的能力数据；不假定四个执行器都支持同轮 Steer 或相同权限等级。
5. **跨 Agent 协作邮箱**：可追溯发送者、接收者、任务关联、去重、权限及过期信息，底层交付复用既有消息能力。
6. **Quality Gate 与集成计划**：本地测试、静态审查、目标分支集成预演、冲突处理、审批后提交/推送；与 PR/CI 现有能力统一呈现。
7. **安全策略与恢复控制**：命令/路径风险、人工审批、恢复可验证性、人工介入提示；Worktree 不等于 OS 沙箱。
8. **预算与调度策略**：全局/工作流/单任务用量和限额；默认并发 3、硬上限 10，真实资源采集和保守退化。
9. **桌面工作流工作台**：基于已有 Electron/React 的双模式 UI，包含 DAG、任务详情、跨 Agent 消息、审批、成本视图。

## 3. 不能直接假定的能力

- 四种 CLI 在用户机器上可用、已登录、支持相同 ACP 版本或跨接口会话恢复——**未运行验证**。
- `Steer` 在全部执行器实时生效——**只有能力声明或实际 Probe 通过时才能启用**。
- 本地 Git 检查、PR Review 和 CI 观察已经形成完整的通用质量门禁——**需要编排层组合与验证**。
- 当前 `automation/scheduler.go` 已经是多任务 DAG 引擎——**不是同一问题域**。
- Worktree 能限制 Agent 访问其他文件或系统命令——**不能，必须额外提供权限及执行沙箱**。
- 上游完整云控制平面可以作为本地产品内核——`AGENTS.md` 指出部分 Cloud 控制平面为私有仓库，不纳入本次复用边界。

## 4. 代码变更原则与验证障碍

- 后端所有新增用例遵循 `httpd/controller → service → domain/ports → adapter/storage` 现有依赖方向；CLI 是 Daemon HTTP 的薄客户端。
- 新迁移文件追加而非编辑已经发布的 migration；通过仓库脚本 `npm run migration:create -- <name>`、`npm run sqlc` 和 `npm run api` 生成相关产物，不手动编辑 `storage/sqlite/gen`。
- 现有 Session 的 UI 显示状态保持**按 durable facts 派生**；新增 Workflow Task 的执行状态是独立领域事实，不得篡改 Session 状态派生规则。
- 当前 Windows 环境已核实 Node.js `v22.20.0`；`backend/go.mod` 声明 `go 1.27.1`，本机 Go 命令启动时尝试下载工具链。按仓库要求，先准备 Node 24 和相匹配的 Go 工具链再跑构建/测试。
- 不升级全局工具、不修改 `.codex/`、`.claude/`、真实 `~/.ao` 用户数据；开发使用独立数据目录和明确测试仓库。
- 本次仅做静态分析与设计文档，不宣称已完成本机端到端 Agent 运行验证。
