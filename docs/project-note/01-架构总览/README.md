---
last_updated: 2026-11-09
kb_version: KB-v1.0
---

# 01 · 架构总览（索引）

系统骨架：一个 Go daemon + 三个瘦客户端，五层单向依赖，CDC 作为唯一事件来源。

| 文档 | 一句话 | 什么时候读 |
|---|---|---|
| [系统分层架构.md](系统分层架构.md) | 顶层拓扑、五层依赖方向、关键入口、三个不变式 | 第一次理解项目；判断代码该放哪一层 |
| [技术栈与中间件.md](技术栈与中间件.md) | 版本基线、依赖清单与选型理由、SQLite PRAGMA、周期常量、端口 | 加依赖前；调定时器；排查端口/DB 行为 |
| [部署与运行架构.md](部署与运行架构.md) | 三种运行形态、命令、`~/.ao` 数据布局、环境变量、网络边界、发布红线 | 本地跑起来；改启动/配置/打包逻辑 |

## 本分类的机器可读事实

- 索引规模：5365 文件 / 110,865 符号节点 / 276,705 边（`.codegraph/codegraph.db`）
- 代码量分布：Go 2786 文件（29 MiB）、TSX 915（12 MiB）、TS 1434（11 MiB）
- 最大单文件：`frontend/src/api/schema.ts`（1004 KB，生成物）、`backend/internal/session_manager/manager.go`（317 KB）

## 三句话记忆

1. 展示状态**不落库**，读时派生。
2. 每个会话**恰好一个**已提交控制器（`tui` 或 `chat`）。
3. 事件**只从** `change_log` CDC 出。

## 上游权威文档

`docs/architecture.md`（1263 行，含完整 mermaid 图）、`docs/stack.md`、`docs/backend-code-structure.md`。本目录是它们的**分层摘要**，冲突时以上游 + 代码为准（见 `docs/documentation-map.md`）。
