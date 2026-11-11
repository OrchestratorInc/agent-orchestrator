---
last_updated: 2026-11-09
kb_version: KB-v1.0
---

# 04 · 数据与组件设施（索引）

前端状态、组件树、通信契约、数据模型。

| 文档 | 一句话 | 什么时候读 |
|---|---|---|
| [Store状态管理全景.md](Store状态管理全景.md) | 14 个 Zustand store 清单；Query/Zustand/主进程三层分工；SSE 连接数陷阱 | 加前端状态前；排查「状态不同步」 |
| [组件架构与通信.md](组件架构与通信.md) | Electron 三层边界、路由/组件组织、四条通信通道、IPC 命名空间 | 加页面/弹层；改 IPC |
| [服务层与IPC通信.md](服务层与IPC通信.md) | 366 个生产路由的域分布、错误信封与 Kind→HTTP 映射、CLI 客户端、生成链路与漂移门禁 | 加/改 API；查错误码 |
| [类型系统与数据模型.md](类型系统与数据模型.md) | 三层类型关系、状态枚举、关键记录字段陷阱、109 张表分组 | 改 DTO/表；理解状态语义 |

## 改动前三问

1. 这个状态该放 **Query**（daemon 数据）还是 **Zustand**（本地 UI）？
2. 这个能力该走 **REST** 还是 **IPC**？（daemon 业务 → REST；系统/窗口/浏览器 → IPC）
3. 改的是 DTO 还是内部事实？`json:"-"` 的字段改了不用 regen OpenAPI。

## 生成物黑名单（永不手改）

- `frontend/src/api/schema.ts`
- `backend/internal/storage/sqlite/gen/**`
- `backend/internal/httpd/apispec/openapi.yaml`（由生成器产出）
- `frontend/src/renderer/routeTree.gen.ts`
- `backend/internal/adapters/chatdriver/codexappserver/codexproto/protocol.gen.go`
