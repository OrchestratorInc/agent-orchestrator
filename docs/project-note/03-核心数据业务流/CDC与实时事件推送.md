---
last_updated: 2026-11-09
source_files:
  - backend/internal/cdc/poller.go
  - backend/internal/cdc/broadcast.go
  - backend/internal/cdc/retention.go
  - backend/internal/httpd/events.go
  - backend/internal/storage/sqlite/migrations
kb_version: KB-v1.0
---

# CDC 与实时事件推送

一次数据库写入如何变成前端的一次 UI 更新。

## 触发点

任何对 CDC 覆盖表的 INSERT/UPDATE/DELETE（`sessions`、`conversations`、`pull_requests`、`notifications`、`workflow_*` 等）。

## 数据流路径

```
SQLite 写入
 → DB 触发器 append 一行到 change_log(seq, table_name, row_id, operation, old_data, new_data)
 → cdc.Poller.Poll（DefaultPollInterval 轮询，批量 DefaultBatch）
     按 seq 顺序读取 EventsAfter(lastSeq, batch)
     游标仅在内存（lastSeq）；重启 SeekToHead，**不**重播历史
 → cdc.Broadcaster（进程内）
     Subscribe(fn) → fn 在 poller 循环中**同步**执行
 → 订阅者：
     ├ SSE 写入 /api/v1/events
     ├ 终端 sessions 通道 fanout
     └ 缓存失效
 → 客户端
     ├ SSE 事件 → TanStack Query 定向 invalidate → refetch 快照
     └ 或 Last-Event-ID / after 游标自行补读
```

## SSE 端点 `GET /api/v1/events`

| 行为 | 规则 | 原因 |
|---|---|---|
| 游标来源 | `Last-Event-ID` 头 > `?after=` 查询参数 | EventSource 自动带头 |
| `?after=latest` | 无 `Last-Event-ID` 时从当前 head 开始，并先写一条 `event: cursor` | 让 EventSource 立即拥有 id，重连能补缺口 |
| 游标超前于 head | 从 head 开始，**不**从 0 | change_log 被截断/替换；从 0 重放会让所有客户端同时雪崩 |
| 客户端消费过慢 | 缓冲满 → 主动关闭流 | **绝不阻塞 broadcaster**；客户端重连后按游标重放 |
| 空闲保活 | 定时心跳事件 | 中间代理会缓冲孤立的单次小写，远端客户端可据此验证链路 |
| 响应头 | `text/event-stream`、`no-cache`、`X-Accel-Buffering: no` | 防代理缓冲 |

## 关键节点详解

| 节点 | 位置 | 核心逻辑 | 异常处理 |
|---|---|---|---|
| Poller 游标 | `cdc/poller.go:30` | 内存 `lastSeq`，只做 **live push** | 耐久补读是客户端的事（从自己的偏移读 change_log） |
| 重启语义 | `poller.go` | `SeekToHead()`，不重播历史 | 避免新 broadcaster 重发全部历史 |
| Broadcaster | `cdc/broadcast.go:29` | `Subscribe(fn)` 返回 unsubscribe；fn 同步执行 | 订阅者必须非阻塞，需缓冲就自己投递到 channel |
| 保留期 | `cdc/retention.go` | change_log 有保留策略 | 截断后客户端游标兜底为 head |
| SSE 订阅 | `httpd/events.go:86` | 缓冲满即 `cancel()` 关闭流 | 宁断不丢 |

## 事件契约

- 事件载体是**表行变化**，不是业务语义事件；客户端按 `table_name` + `row_id` 做定向失效。
- 展示状态不随事件下发：客户端收到 `sessions` 行变化后重新拉快照，由 daemon 派生状态。
- `workflow_*` 表同样走 CDC（`migrate_workflow_cdc_test.go` 有覆盖）。

## 异常路径

| 异常 | 处理 |
|---|---|
| 客户端落后于保留窗口 | 兜底从 head 开始；客户端靠全量快照恢复 |
| SSE 缓冲溢出 | 关闭连接，客户端重连带 `Last-Event-ID` |
| Poller 重启 | 从 head 开始，历史由客户端补读 |
| 订阅者阻塞 | 这是订阅者的 bug；broadcaster 本身无超时保护 |

## 关联组件

- `cdc.Poller`、`cdc.Broadcaster`、`cdc.Event`
- `httpd/events.go`（SSE）、`httpd/terminal_mux.go`（`sessions` 通道）
- 前端：`renderer/hooks/useEventsConnection.ts`（SSE 连接、端口重绑）
- 存储：`change_log` 表 + per-table 触发器

## 相关业务流

→ 详见 [会话生命周期与终止判定.md](会话生命周期与终止判定.md)
→ 详见 [PR观测与反馈回路.md](PR观测与反馈回路.md)
→ 详见 [终端会话与mux.md](终端会话与mux.md)
