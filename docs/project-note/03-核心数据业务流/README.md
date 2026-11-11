---
last_updated: 2026-11-09
kb_version: KB-v1.0
---

# 03 · 核心数据业务流（索引）⭐

每条流程按「触发点 → 数据流路径 → 关键节点 → 异常路径 → 关联组件」组织。机器可解析字段：**触发点**、数据流路径、异常处理均齐全。

| 文档 | 一句话 | 触发 → 终点 |
|---|---|---|
| [会话创建全链路.md](会话创建全链路.md) | 点 New Task 到 agent 首个 token，含 15 步管线与分阶段回滚 | `POST /sessions` → `MarkSpawned` |
| [终端会话与mux.md](终端会话与mux.md) | WebSocket 多路复用 → 运行时 attach → PTY 字节流 | `/mux` upgrade → PTY 数据循环 |
| [Chat对话与turn生命周期.md](Chat对话与turn生命周期.md) | 消息排队/派发 → provider turn → 投影结算 → 休眠 | `POST .../messages` → turn 终态 |
| [接口切换.md](接口切换.md) | 同一会话内替换控制器，跨 8 个耐久阶段 | `interface-transition` → 新 generation 提交 |
| [会话生命周期与终止判定.md](会话生命周期与终止判定.md) | 探针/hook/SCM 事实归约；终止的 5 项必要条件与熔断 | 观察 → `is_terminated` |
| [PR观测与反馈回路.md](PR观测与反馈回路.md) | 30s 轮询 → PR 事实 → nudge → 看板状态 | `Observer.Poll` → 派生 PR 状态 |
| [工作流编排与执行绑定.md](工作流编排与执行绑定.md) | 规划 → 准入 → 绑定 → 门禁 → 集成（多 Agent 层） | `POST /workflows` → task `succeeded` |
| [CDC与实时事件推送.md](CDC与实时事件推送.md) | 触发器 → poller → broadcaster → SSE | 任意写入 → UI 失效 |

## 读这些文档时的三条注意事项

1. **异常路径不是可选项**：本项目多数设计复杂度来自「未知态」与「不该误判」。改动前先读对应文档的异常表。
2. **展示状态是派生的**：任何流程都不应「写状态列」。若你的改动想写状态，先看 `会话生命周期与终止判定.md` 的派生优先级。
3. **一会话一控制器**：只有 `接口切换.md` 描述的过程能改 `sessions.mode`。

## 未单独成文的流程

以下流程存在但复用上述主线，未单独成文（避免重复）：
- 项目 CRUD / 项目配置 / Cues（触发点简单，逻辑在 `service/project`）
- 自动化（`automations` + scheduler，走会话创建链路）
- 审查执行（`service/review` → reviewer adapter，产出走 PR 反馈回路）
- 用量与预算（`observe/usage` + `budget_*` 表，工作流层的预算预留见 `工作流编排与执行绑定.md`）
