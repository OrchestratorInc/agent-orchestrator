---
last_updated: 2026-11-09
source_files:
  - frontend/src/renderer/stores
  - frontend/src/renderer/lib/api-client.ts
  - frontend/src/renderer/hooks/useEventsConnection.ts
kb_version: KB-v1.0
---

# Store 状态管理全景

前端状态分三层：**服务端状态**（TanStack Query，不在 Zustand）、**本地 UI 状态**（Zustand，14 个 store）、**Electron 主进程状态**（IPC 查询）。

## 分层原则

| 状态类型 | 归属 | 例子 |
|---|---|---|
| daemon 数据（会话、PR、项目、通知…） | TanStack Query + `openapi-fetch` | `useSessions`、`usePullRequest`、`useAutomations` |
| 纯 UI 偏好与临时状态 | Zustand | 侧栏顺序、主题、打开的标签、草稿 |
| 需要跨窗口/IPC 的能力 | Electron main（通过 preload 暴露） | 窗口、更新器、浏览器视图、daemon 控制 |

**不要把 daemon 数据放进 Zustand**：那会与 Query 缓存形成两个真相源，CDC 失效更新不完整。

## Store 清单（`frontend/src/renderer/stores/`）

| Store | 负责 | 持久化 |
|---|---|---|
| `ui-store.ts` (`useUiStore`) | 全局 UI 总汇：主题偏好、设置弹层、工作台标签、inspector 视图、文件显示模式、toast、跨面板文件打开请求、Chat 消息聚焦请求 | `localStorage`（主题等） |
| `workflow-ui-store.ts` (`useWorkflowUi`) | 工作流画布临时状态 + `useWorkflowDraft` hook | 否 |
| `sidebar-order-store.ts` | 项目/会话排序、新建项目的待确认顺序 | `localStorage`（`PROJECT_ORDER_STORAGE_KEY` 等） |
| `keybindings-store.ts` | 键位覆盖 | 经 `persist()` 落 Electron 侧 |
| `locale-store.ts` | 界面语言（i18next，含中文） | `localStorage` |
| `terminal-shell-store.ts` | 会话级 shell 终端的状态 | 否 |
| `terminal-reset-store.ts` | 终端重置信号（端口重绑/daemon 重启后重建） | 否 |
| `coder-session-options-store.ts` | 编码会话的选项（模型/权限等） | 否 |
| `artifacts-view-store.ts` | 产物视图筛选与选择 | 否 |
| `session-git-action-store.ts` | 会话 Git 操作（commit/discard 等）的进行态 | 否 |
| `analytics-opt-out-store.ts` | 遥测退出选项 | 否（策略以 daemon 为准） |
| `telemetry-policy-store.ts` | 遥控策略镜像 | 否 |
| `sound-notifications-store.ts` | 提示音偏好 | `localStorage` |
| `local-signin-dialog-store.ts` | 本地登录弹层 | 否 |

每个 store 都有同目录 `*.test.ts`（`ui-store`、`sidebar-order`、`terminal-shell`、`sound-notifications`、`telemetry-policy`、`analytics-opt-out`、`artifacts-view`、`coder-session-options`、`locale` 均有测试）。

## 更新模式

```ts
// 典型形态：create<T>((set, get) => ({ ... }))
export const useUiStore = create<UiState>((set, get) => ({ ... }));
```

- **`set` 局部合并**：只返回变化的键，不要整对象替换。
- **跨 store 读取用 `get()`**，不要互相 import 后订阅（会造成隐式重建）。
- **派生值用 selector**：`useUiStore((s) => s.someField)`，避免订阅整个 store 导致无关重渲染。
- **持久化必须防御 SSR/无 `localStorage`**：`ui-store.ts:319` 先检查 `typeof window` 与 `window.localStorage`。

## API 客户端与事件连接

`lib/api-client.ts`：

- 由 `openapi-fetch` + 生成的 `src/api/schema.ts` 构成**类型安全客户端**；`schema.ts` 是 1 MB 生成物，**永不手改**。
- `baseUrl` 运行时可变（daemon 端口重绑），通过 `baseUrlListeners` 通知订阅者。
- 开发态 base 为 `window.location.origin`，打包态为 `http://127.0.0.1:3001`。
- **SSE 连接数陷阱**：Chromium 对每个 host 只允许 6 条 HTTP/1.1 连接，而每条 `EventSource` 长占一条。renderer 会同时开 3–5 条流，导致其余请求排队（Settings 也卡）。解决方案是把事件流放到 `ao-events.localhost`（Chromium 解析为 loopback 但是独立 host），且**仅当 daemon 报告它也服务 `[::1]` 时**才启用——否则 `::1` 上的其他进程会收到这些流。
- `hooks/useEventsConnection.ts` 负责 SSE 连接与 daemon 重启后的端口重绑。

## 反模式（本仓已踩过）

| 反模式 | 后果 | 正确做法 |
|---|---|---|
| 把会话列表放 Zustand | CDC 失效后两个真相源不同步 | 用 Query，靠 SSE 定向 invalidate |
| store 之间互相订阅 | 隐式渲染环 | 用 selector + `get()` |
| 在 renderer 直接调用 daemon HTTP 绕过客户端 | 丢类型、丢错误信封、丢 requestId | 一律走 `api-client` |
| 手改 `src/api/schema.ts` | CI `api-drift` 失败 | 改 Go DTO 后 `npm run api` |

## 关联

- 组件树与 IPC → 详见 [组件架构与通信.md](组件架构与通信.md)
- daemon REST/SSE/WS 契约 → 详见 [服务层与IPC通信.md](服务层与IPC通信.md)
- 派生状态型别 → 详见 [类型系统与数据模型.md](类型系统与数据模型.md)
