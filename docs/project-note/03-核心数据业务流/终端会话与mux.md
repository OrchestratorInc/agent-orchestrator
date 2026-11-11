---
last_updated: 2026-11-09
source_files:
  - backend/internal/httpd/terminal_mux.go
  - backend/internal/terminal/protocol.go
  - backend/internal/terminal/manager.go
  - backend/internal/terminal/attachment.go
  - backend/internal/adapters/runtime/runtimeselect
  - backend/internal/adapters/runtime/tmux
  - backend/internal/adapters/runtime/conpty
  - backend/internal/adapters/agent/*/hooks.go
kb_version: KB-v1.0
---

# 终端会话与 mux

TUI 模式会话的真实终端链路：WebSocket 多路复用 → 运行时 attach → PTY 字节流。

## 触发点

- 前端打开 SessionView（TUI 模式）→ `WebSocket /mux` + `ch:"terminal"` + `msgOpen{terminalId}`。
- 移动端 TUI 路由做同样的事，Origin 为已认证的原生移动 origin。
- 用户或会话需要 shell 时，另开 `shell-terminals` 资源（不是 agent 控制器）。

## 数据流路径

```
ws upgrade /mux
 → exactAllowedOrigins 校验 Origin           app://renderer（打包）/ 显式 Vite origin / 已认证移动 origin
 → websocket.Accept（InsecureSkipVerify，因为已在上面做精确校验）
 → terminal.Manager.Serve
 → msgOpen{terminalId, rows, cols, role}
 → Manager.track → attachment → runtime.Attach(handle, rows, cols)
     新会话：分离式 native PTY host（macOS/Linux）
              host 启动失败 → 回落 tmux
     legacy 持久 handle：tmux attach
     Windows：ConPTY loopback host
 → 数据循环：
     运行时输出 → mux → {"ch":"terminal","type":"data","data":base64}
     客户端输入 → writeInput → PTY
     resize：primary 决定共享 PTY 网格，服务端把权威 cols/rows 推给所有 secondary
 → close → attachment 关闭，PTY 存活（分离式 host 继续跑）
```

## 关键节点详解

| 节点 | 位置 | 核心逻辑 | 异常处理 |
|---|---|---|---|
| Origin 门禁 | `terminal_mux.go:41` | 精确 origin 匹配，**在升级之前**拒绝 | `403 ORIGIN_FORBIDDEN` + `Vary: Origin, Sec-Fetch-Site` |
| 无终端管理器 | `terminal_mux.go:29` | `mgr == nil` 时不挂 `/mux` 路由 | 路由 404，不是错误码 |
| 超时豁免 | `terminal_mux.go:24` | `/mux` 挂在 per-request Timeout 之外 | 长连接不被 60s 掐断 |
| 协议通道 | `protocol.go:5-13` | `terminal` / `subscribe` / `sessions` / `system` 单 ws 多路 | 未知 ch 忽略 |
| 二进制负载 | `protocol.go` | PTY 输出任意字节 → base64 放在 `data` 字段 | 不用裸 JSON 字符串（会破坏非 UTF-8） |
| 输入栅栏 | `manager.go:194-263` | `BeginInputDrain` / `HoldInput` / `TerminalOnScreen` / `inputRefused` | 交接 drain 期间新输入被拒绝 |
| 网格权威 | `protocol.go` | primary（桌面真实工作区）决定共享 PTY 尺寸 | secondary 按权威网格缩放渲染 |

## 输入栅栏（与 TUI↔Chat 交接耦合）

drain 策略下，AO 在检查静默之前**先**门禁新终端输入：
- `SetSessionInputLease` 注入每会话的输入租约；
- `BeginInputDrain` 返回最后输入时间与释放函数；
- `HoldInput` 在持有期间拒绝写入；
- `inputRefused` 让上层知道本次输入被拒（而不是丢弃）。

只有「重复观察到 idle 表面 + 空 composer 且跨过 settle 窗口」才算静默；可见草稿一律失败（源会话原样保留，要求用户提交/清空/显式丢弃）。

## 异常路径

| 异常 | 处理 |
|---|---|
| Origin 不在白名单 | 403，升级前拒绝，不建立任何 attachment |
| native PTY host 启动失败 | 回落到 tmux（`runtimeselect`），不静默降级到无终端 |
| 客户端断开 | 关闭 attachment，PTY/宿主继续运行（分离式） |
| 同一 PTY 多客户端 | primary 控网格；secondary 只读跟随 |
| 输入栅栏生效期间写入 | 拒绝并记录，不缓冲（避免交接后误投递） |

## 关联组件

- 端口：`ports.TerminalSurface`、`ports.Runtime`
- 实现：`adapters/runtime/{runtimeselect,tmux,conpty,ptyexec}`
- 前端：xterm 6.1 + webgl/fit/search addons；终端 store 为 `stores/terminal-shell-store.ts`、`stores/terminal-reset-store.ts`
- 会话级 shell：`service/shellterm`（与 agent 控制器无关）

## 相关业务流

→ 详见 [会话创建全链路.md](会话创建全链路.md)
→ 详见 [接口切换（TUI↔Chat）.md](接口切换.md)
→ 详见 [Chat对话与turn生命周期.md](Chat对话与turn生命周期.md)
