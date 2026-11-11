---
last_updated: 2026-11-09
source_files:
  - backend/internal/service/chat/controller.go
  - backend/internal/service/chat/service.go
  - backend/internal/service/chat/hibernate.go
  - backend/internal/service/chat/steer.go
  - backend/internal/domain/conversation.go
  - backend/internal/adapters/chatdriver/codexappserver
  - backend/internal/adapters/chatdriver/acp
kb_version: KB-v1.0
---

# Chat 对话与 turn 生命周期

Chat 模式会话的消息主线：用户发送 → 排队/派发 → provider turn → 事件投影 → 结算；含审批、input、压缩、回滚、休眠。

## 触发点

| 入口 | 路由 / CLI | 说明 |
|---|---|---|
| 用户发消息 | `POST /api/v1/sessions/{id}/conversation/messages` | 桌面 composer / 移动端 |
| 生成提示词续接 | 内部 continuation 路径 | 自动携带上下文 |
| 生命周期 nudge | lifecycle → chat service | CI 失败 / 评审反馈 / 冲突 |
| 工作流协作 | `service/collaboration`、`service/intervention` | queue / steer / stop |
| Side chat | `POST .../side-chats/{sideId}/messages` | 独立 side 会话 |

## 数据流路径

```
POST .../conversation/messages
 → chat.Service.Send → SendForOwner
 → Service 定位 Controller（按 session + 当前 generation）
 → Controller.Send                 ← sendMu 串行化「工作区核验 + 队列 claim + 派发」
   ├ 计算 ClientPayloadHash（幂等）
   ├ handoff 检查                  controllerHandoff != none → ErrControllerHandoff
   ├ prepareContinuation           绑定重试到 client request，再补上下文
   ├ NextQueuedTurn 探测是否已有人排队（queuedAhead）
   ├ shouldQueue = busy || queuedAhead
   │    ├ 是 → AppendUserMessage 写 queued 行，返回 TurnStateQueued
   │    └ 否 → setSendingTurn + checkWorkspace + AppendUserMessage(dispatched)
   ├ 重复 client message id → created=false → 返回空 turn（不二次派发）
   └ 派发 → chat driver（Codex app-server JSON-RPC / ACP JSON-RPC）
 → provider 事件流 → Controller 事件泵
   ├ 合并 account / threadState / usage / mcpServers（分片上报，不能整块覆盖）
   ├ delta 批量写（flushRequests 触发）
   ├ turn/started → 绑定 dispatchingTurnID（处理 eager provider 提前上报）
   └ turn 结算 → TurnState = completed / failed / interrupted / recovered
 → DB trigger → change_log → SSE（定向失效，不轮询整段历史）
```

## Turn 状态（`domain/conversation.go:82-90`）

`queued → running → completed`，旁支 `recovered`（重启后恢复的已完成 turn）、`interrupted`（用户中断）、`failed`、`cancelled`。

## 关键节点详解

| 节点 | 位置 | 核心逻辑 | 异常处理 |
|---|---|---|---|
| `sendMu` | `controller.go:229` | 只有它可变 provider 对话；一次只允许一个操作 | 串行等待，不并发派发 |
| 代际围栏 | `controller.go:1492` (`SendPinned`) | 空期望 generation 直接拒绝；generation 构造时固定、永不改 | `ErrStaleControllerGeneration` |
| 幂等 | `controller.go:1471`（`Send`） | `ClientMessageID` + `ClientPayloadHash` | 重复 id → 返回空 turn，不重复派发 |
| 工作区核验 | `controller.go:1605` | 新派发必须先过 `checkWorkspace` | 已接受的 client id 继续由 Append 校验冲突，避免把旧成功重放成拒绝 |
| 排队 vs 派发 | `controller.go:1565` | `busy()` 或 `queuedAhead` → queued | queued 永远不渲染为 sent |
| `sendingTurnID` 标记 | `controller.go:258` | 派发中但行仍是 queued 时，快照不能显示「空闲 agent 有排队消息」 | 派发失败/重复/入队 → 清除标记 |
| 分片合并 | `controller.go:302-322` | account / threadState / mcpServers / usage 各自分片上报 | 整块覆盖会清空前一个分片 |
| MCP 持久化顺序 | `controller.go:319` | `mcpPersistMu` 让单条通知写入不与权威替换乱序 | reload 在事件泵外运行 |
| 审批与 input | `conversations.go` 路由 | approval / input request 有独立 resolve 端点 | 未决期间 `waiting_input`；自动化**禁止**向 blocked 会话注入输入 |
| 压缩 | `controller.go:264` | `compactionPending`：请求先被接受，provider turn 稍后开始 | 期间不派发新 turn |

## 会话认证事实（credential 失配）

daemon 把 provider 的凭据拒绝投影到 `conversations.account_json` 的 `authenticationState`（`unknown`/`required`/`authenticated`）+ `reauthRequiredAt`/`reauthReason`。

**清除条件严格**：必须是「活跃 provider 分支 + 拥有该会话的 controller generation + 在需求/账号变更屏障之后**开始并完成**的权威 turn，且无错误」。以下一律**不能**清除：导入/合成历史、进程就绪、本地 CLI 登录、无关联的恢复上报、嵌套 Codex 子线程完成。

部分 account/plan 上报**永不**暗示可用凭据；auth mode 变化会为在途 turn 建立屏障。

## Chat 休眠

| 状态 | 是否休眠 | 原因 |
|---|---|---|
| provisioning / connecting / recovering | 否 | 控制器归属或 provider 状态未定 |
| active / busy / 有排队或运行中 turn | 否 | 有在途工作 |
| 等待 input / 等待审批 | 否 | provider 请求仍挂起 |
| 未确认 turn 后的 idle | 否 | 最新 prompt 无耐久终态 |
| 已结算 idle + 主 Chat 标签打开 | 否 | 视图租约保持 provider 就绪 |
| 已结算 idle + 无 Chat 视图 | 是（Developer Mode 开启、支持 resume、无转移/待办） | 关标签立即检查，30s 清扫补漏 |

休眠记录的是**进程边界**，不是展示状态：会话的看板状态继续由 activity/PR 事实派生。

## 异常路径

| 异常 | 处理 |
|---|---|
| provider 进程丢失 | 宿主断开 → 重新 connect/adopt；原生 load/resume 仅在真实 host 失败时才是修复路径 |
| 事件迟到（旧 generation） | 按 `controller_generation` 拒绝 |
| 工作区失效 | 拒绝派发（`ErrWorkspaceInvalid`），已入队消息仍被接受 |
| 审批/input 超时 | 保持 `waiting_input`，不自动注入 |
| 凭据被拒 | 写 auth 需求事实，UI 显示通知；恢复需满足上面的严格条件 |

## 关联组件

- 端口：`ports.ChatConversation`、`ports.ChatController`、`ports.ChatSteer`
- 驱动：`adapters/chatdriver/codexappserver`（原生 app-server）、`adapters/chatdriver/acp` + 20+ provider 子包
- 持久化：`conversations`、`conversation_turns`、`conversation_messages`、`conversation_activities`、`conversation_branches`、`conversation_edit_deliveries`
- 前端：`components/chat/ChatWorkspace.tsx`（246 KB）+ 子组件；移动端 `packages/mobile/lib/*`

## 相关业务流

→ 详见 [接口切换.md](接口切换.md)
→ 详见 [会话生命周期与终止判定.md](会话生命周期与终止判定.md)
→ 详见 [PR观测与反馈回路.md](PR观测与反馈回路.md)
