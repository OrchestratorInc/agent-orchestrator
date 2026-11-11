---
last_updated: 2026-11-09
source_files:
  - backend/internal/httpd/api.go
  - backend/internal/httpd/router.go
  - backend/internal/httpd/controllers
  - backend/internal/httpd/apispec
  - backend/internal/httpd/apierr/apierr.go
  - backend/internal/cli/client.go
  - frontend/src/renderer/lib/host-clients.ts
kb_version: KB-v1.0
---

# 服务层与 IPC 通信

daemon 的 HTTP 契约、错误信封、CLI 客户端，以及 Electron 侧 IPC。

## REST 表面（`/api/v1`，chi router）

路由注册入口：`backend/internal/httpd/api.go:366 Register`。当前索引到 **366 个生产路由**（全库 551 个 route 节点含测试中的外部 URL），分布在约 40 个控制器文件中。

| 前缀 | 路由文件 | 内容 |
|---|---|---|
| `/api/v1/sessions/{sessionId}/…` | `sessions.go`（68 路由） | 增删改查、send、activity、kill、restore、rollback、cleanup、PR claim、preview、auto-review/inject、pin、merge-policy、interface-transition |
| `/api/v1/sessions/{sessionId}/conversation/…` | `conversations.go`（65 路由） | turn、message、approval/input resolve、settings、draft、compact、side-chats、chat-view |
| `/api/v1/workflows/…` | `workflows.go`（34）+ `workflow_quality.go`（15）+ `scheduler.go`（6） | 计划/候选/任务/预算/暂停/质量门禁/集成 |
| `/api/v1/projects/…` | `projects.go`（13） | CRUD、config、clone、tasks/prepare |
| `/api/v1/agents/…` | `agents.go`（8）、`agent_accounts.go`（11）、`codex_accounts.go`（12）、`agent_auth.go`（3） | 目录、readiness、模型、账号与登录 |
| `/api/v1/reviews/…` | `reviews.go`（10） | 执行与发送审查 |
| `/api/v1/prs/…` | `prs.go`（2） | merge、resolve-comments |
| `/api/v1/notifications/…` | `notifications.go`（6） | 分页历史、已读、推送注册 |
| `/api/v1/automations/…` | `automations.go`（6） | 规则与运行历史 |
| `/api/v1/computer-use/…` | `computeruse.go`（6） | 状态、权限、调用 |
| `/api/v1/browser/…` | `browser.go`（5）+ `browser_live.go` | 状态、命令、chrome 模式、实时视图 |
| `/api/v1/scheduler/…` | `scheduler.go` | status / decisions / budget / policy |
| `/api/v1/cues/…` | `cues.go`（6） | 项目提示词/命令 |
| `/api/v1/settings`、`/usage/…`、`/system/…`、`/push/…`、`/github/…`、`/imports/…`、`/shell-terminals/…` | 各自控制器 | — |
| 非 REST | `events.go` | `GET /api/v1/events`（SSE） |
| 非 `/api/v1` | `terminal_mux.go` | `GET /mux`（WebSocket） |
| 健康/控制 | `router.go` | `/healthz`、`/readyz`、`/shutdown` |
| 未认证（LAN 唯一） | `identity.go` | `GET /api/v1/identity` |

### 中间件顺序（`router.go:57`）

```
RequestID → requestLogger → recoverer → rendererOrigin → cors → surface
```

- per-request timeout **不全局**：只包裹有界 REST 路由，长连接（`/mux`）与健康探针不加。
- `rendererOrigin` 在 handler 之前拒绝非法来源（CORS 头本身不阻止副作用）。

## 错误信封

`apierr.Kind` 是**语义分类**（不直接等于 HTTP 状态），只有 envelope 层做翻译：

| Kind | HTTP |
|---|---|
| `KindInternal` | 500 |
| `KindInvalid` | 400 |
| `KindNotFound` | 404 |
| `KindConflict` | 409 |
| `KindForbidden` | 403 |
| `KindTooManyRequests` | 429 |
| `KindNotImplemented` | 501 |
| `KindUnavailable` | 503 |

线上信封：`{ message, code, requestId }`。500 对外恒为 `Internal server error`，真实原因通过 `envelope.WithErrorCapture` 记录到请求作用域，再进入访问日志与 Sentry（`ownership.OwnerOf(err)` 决定归属层）。

## CLI 客户端

`backend/internal/cli/client.go`：

| helper | 用途 |
|---|---|
| `getJSON` | `GET /api/v1/<path>`，解码 2xx |
| `postJSON` | `POST`，非 2xx → 由信封构造错误 |
| `postSetupJSON` | 无客户端超时（spawn/restore 会跑项目 Setup；客户端超时会是 Setup 的天花板） |

- `commandTimeout = 2m` 约束变更类调用；`maxDrainedBodyBytes = 4KB` 限制未用响应体的丢弃量以复用 keep-alive。
- 错误分层：`errDaemonNotRunning`（run-file 缺失/PID 已死）与 `errDaemonUnavailable`（传输失败）区分，避免 hook 场景把重启噪音写进 agent stderr。
- 只有幂等 CLI 操作会对 `daemonUnavailableError` 做安全重试；其余立即返回。
- 退出码：用法错误 2，运行时/daemon 失败 1。

## 生成链路与漂移门禁

```
dto.go + specgen/build.go   → npm run api:spec → backend/internal/httpd/apispec/openapi.yaml
openapi.yaml                → npm run api:ts   → frontend/src/api/schema.ts
queries/*.sql + migrations  → npm run sqlc     → backend/internal/storage/sqlite/gen/
```

| 门禁 | 内容 |
|---|---|
| `TestBuild_MatchesEmbedded` | 内嵌 spec 与生成器输出一致 |
| `TestRouteSpecParity` | 每个注册路由都出现在 spec 中 |
| `api-drift` CI job | 重新生成 `schema.ts` 后 `git diff` 必须为空 |
| `sqlc-drift` CI job | `gen/` 必须与查询/迁移一致 |

**CLI 的 DTO 手写**：`internal/cli` 刻意不用生成类型，wire 兼容性由 `dto_drift_e2e_test.go` 等测试守住。

## Electron IPC

见 `组件架构与通信.md` 的命名空间表。IPC 通道命名规律：`<域>:<动作>`
（`daemon:start`、`browser:ensure`、`updates:check`、`app:chooseDirectory`、`operator:readWorkflow`）。

主进程还需要 **daemon ↔ Electron 的本地套接字**：浏览器自动化走专用 socket（Unix `browser.sock`，Windows 命名管道 `ao-browser[-dev]`），**不用** supervisor 存活套接字，也**不**开放任何未认证远程调试端口。daemon 负责命令授权与关联，Electron 负责真实浏览器目标。

## 多主机（remote host）

`frontend/src/renderer/lib/host-clients.ts` 为每台已连接主机维护独立 API 客户端；路由前缀 `_shell.host.$hostId.*` 承载远程主机视图。本地主机即默认 daemon。

## 相关

- SSE/CDC 语义 → 详见 `../03-核心数据业务流/CDC与实时事件推送.md`
- IPC 与组件层 → 详见 [组件架构与通信.md](组件架构与通信.md)
- 契约层权威说明 → `docs/documentation-map.md`
