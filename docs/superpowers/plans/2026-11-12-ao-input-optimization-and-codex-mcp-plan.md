# AO 输入优化、命令目录与 Codex MCP 计划

> **状态：** 仅计划文档。尚未开始实施。两个阶段分别作为独立 PR 交付。

**目标：** 为会话 Chat composer 增加主动预测/提示词优化按钮与真实的斜杠命令目录反馈，随后增加用户级附加指令与 Codex MCP 配置中心。

**范围划分：** 分两个独立阶段交付，避免把 UI 优化与持久化协议、凭据处理混进一个 PR。

- **阶段一：** 为会话 Chat composer 增加主动预测/提示词优化按钮，补齐命令目录为空或不可用时的可见反馈。
- **阶段二：** 增加用户级附加指令；为 Codex 增加参考 CC Switch 的统一 MCP 配置中心。
- 不声称跨所有 agent 的斜杠命令完全兼容；只展示 provider 真实上报的命令。
- 不修改 `.codex/`、`.claude/` 配置，不提交任何配置文件。

## 公共接口

### 阶段一：提示词优化

扩展现有建议接口，不另建一套 composer 建议体系：

```http
POST /api/v1/sessions/{sessionId}/conversation/prompt-suggestion
```

```json
{
  "turnId": "...",
  "draft": "..."
}
```

`draft` 可选。响应保持：

```json
{
  "turnId": "...",
  "prompt": "..."
}
```

语义：

- `draft` 为空：沿用现有行为，生成下一条建议，供空输入框 ghost text 使用。
- `draft` 非空：保守润色当前输入，只补清目标、范围、约束和验收，不新增未表达的需求，保持原语言。
- 输出最多 4000 个 Unicode 字符；空结果表示没有可用结果。
- 继续使用现有 `CHAT_PROMPT_SUGGESTION_BUSY`、409 stale 和 API error envelope。
- 新增错误码仅用于草稿无效或超长，例如 `CHAT_PROMPT_DRAFT_INVALID` / `CHAT_PROMPT_DRAFT_TOO_LONG`。

### 阶段二：附加指令与 MCP

附加指令采用用户级附加指令，不是 provider 级 system prompt：

- 每条用户消息发送时，在用户文本后追加固定、可识别的附加指令块。
- 不修改 provider 的 `SystemPrompt`，不改项目 standing instructions，不把普通消息伪装成 system role。
- 附加指令持久化在会话级配置，分支与重启继承同一份；删除或修改只影响之后发送的消息。
- 设置入口放在会话设置中，展示当前生效文本，支持保存、清空和恢复为空。

MCP 中心先只支持 Codex：

- 新建或编辑 MCP 服务器：名称、transport、command/args 或 URL、环境变量引用、header 引用。
- secret 只保存引用名，不保存实际值；实际值从进程环境或用户原生凭据提供。
- 新增/修改只影响新建会话。
- 运行中的 Codex 会话继续通过既有 `POST /api/v1/sessions/{sessionId}/conversation/mcp/reload` 读取新配置状态。
- 对不支持 AO 注入 `MCPServers` 的 harness 明确显示不可用，不尝试写各 agent 私有配置文件。
- 不做自动重启、不做全局会话批量迁移。

## 实现改动

### 1. 主动预测与提示词优化

- 扩展 `backend/internal/service/chat/prompt_suggestion.go`：请求增加可选 `draft`；`draft` 非空时切换为保守润色专用 system prompt；复用现有并发槽、超时、上下文重校验和 JSON 解码；不新增通用模型调用框架；明确草稿长度、空白和 Unicode 截断规则。
- 扩展 `backend/internal/httpd/controllers/conversation_prompt_suggestion.go` 与 `dto.go`：校验 `draft`；保持现有 turn/controller readiness 约束；增加对应 controller/service 测试。
- 扩展 `frontend/src/renderer/hooks/useConversationPromptSuggestion.ts`：支持手动刷新或带 `draft` 请求；增加明确的 pending、error、manual-trigger 状态；不把空输入预测和优化草稿混为一个缓存键；失败时保留原草稿，不清空、不自动回退。
- 扩展 `ChatComposer` / `ComposerEditor`：工具栏新增预测/优化按钮，仅会话 Chat 显示；空输入点击生成下一句建议，继续用现有 ghost text + Tab；已有文字点击发起保守润色，结果先展示为待确认建议，不直接覆盖；用户确认后才覆盖草稿，按 Tab 也可接受，Escape 取消；请求中禁止重复提交，按钮显示 pending；失败显示可重试错误并保留原文；草稿、附件、队列、starting、queued edit、控制器非 ready 时遵循现有禁用边界。
- 复用现有 i18n、Tooltip、Button 和 composer 样式；只新增必要的键。

### 2. 斜杠命令目录反馈

- 保留现有 `/` 菜单、ACP `available_commands_update`、Codex `ListSkills` 和 AO `/compact`。
- `useConversationSkills` 暴露 `isLoading`、空目录和错误状态；不要把所有失败显示成“没有命令”。
- composer 在 `/` 打开后：有命令时现有排序和 Tab/Enter 行为不变；已加载但为空时显示“该 agent 未上报可用命令”；暂不可用或加载失败时显示可重试状态与错误原因，不伪造 `/goal`、不静默把 `/` 当作普通字符。
- 不改 provider 命令协议，不增加跨 agent 命令别名。

### 3. 用户级附加指令

- 在会话配置中新增持久化文本字段，并在写 API、快照读取、分支继承、重启恢复、side chat 配置快照中贯通。
- 发送路径统一追加附加指令块，覆盖普通发送、steer、queued send；不改变选择的模型、permission mode 或 provider system prompt。
- 空值不追加任何内容，避免改变现有会话。
- 设置 UI 提供多行编辑、长度限制、保存、清空和错误反馈；不做“每次消息临时输入”的第二个入口。
- 新增 SQLite migration，不改已发布 migration；按仓库规范生成 ID 并登记 shipped migration。
- 生成的 OpenAPI 和前端 typed client 必须一起更新。

### 4. Codex MCP 配置中心

- 新增 daemon-owned、按用户/机器共享的 Codex MCP 配置存储，不写 provider 的 `config.toml`。
- 配置模型与现有 `ports.ChatMCPServerConfig` 对齐，但对外只暴露引用名，不返回 secret 值：stdio 为 name、command、args、env reference names；http 为 name、url、header reference names。
- 会话启动时把已启用配置转换为 `ChatMCPServerConfig`：对 Codex 通过现有 `threadConfig` 注入；未启用配置不注入；与 AO 自身 MCP server、用户原生 Codex 配置保持既有分层，不覆盖无关服务器。
- 增加 MCP 列表、创建、更新、启停、删除 API，复用现有 error envelope 和 typed client；不在 renderer 直接读写文件。
- UI 放在设置中的 Codex/MCP 配置页，展示 transport、启用状态、启动失败原因、环境变量引用是否缺失。
- 保存后提示“新建会话生效”；提供“重载当前会话”入口，调用既有 reload，不自动重启。
- 仅对 Codex 开放可编辑入口；其他 harness 显示不支持或不显示该表单。

## 测试计划

### 阶段一

- Go 单元测试：空 `draft` 保持现有 suggestion 行为；非空 `draft` 调用润色提示，不调用普通 suggestion 提示；草稿长度、空白、Unicode 边界、malformed JSON、timeout、busy、stale；API 校验和错误码。
- 前端 vitest：空输入点击按钮显示 ghost text，Tab 接受，Enter 不误发；已有草稿点击按钮不立即覆盖，确认后覆盖，Tab 接受，Escape 取消；请求中禁用重复触发，失败保留原文并显示可重试错误；队列、附件、starting、controller 非 ready、queued edit 的禁用边界；命令目录有值、空值、加载中、失败四条路径。
- 运行相关 `go test`、前端 vitest、`npm run frontend:typecheck`。

### 阶段二

- SQLite/store/service 测试：附加指令保存、读取、清空、分支/重启继承，旧会话空值兼容。
- 发送路径测试：普通发送、steer、queued send 都只追加一次；空值不追加。
- MCP 测试：Codex stdio/http 注入到 `threadConfig`；环境变量引用缺失时报明确错误，不把 secret 写入快照或日志；禁用配置不注入；编辑只影响新会话；reload 在运行中 turn、unsupported harness、无服务器、失败恢复下的行为。
- API/OpenAPI drift 测试、前端 MCP 表单测试、设置页权限与错误反馈测试。
- 运行仓库要求的相关 backend、frontend、API 生成和 CI 校验；不宣称未执行的 Docker、跨 OS 或真实 provider 验证通过。

## 假设

- 阶段一先交付，阶段二独立后续 PR。
- 主动按钮只进入会话 Chat composer，不进入新任务页或独立侧聊。
- 已有草稿的优化结果必须用户确认或 Tab 接受后才覆盖。
- 优化是保守润色，不新增未表达需求。
- 斜杠命令只展示真实目录；`/goal` 是否能出现取决于对应 agent 是否上报。
- MCP 只先支持 Codex；secret 只保存引用，配置只对新会话生效。
- 不修改 `.codex/` 或 `.claude/` 配置，不自动提交配置文件。
- 所有改动通过 AO worker 实施，并按 worker/PR 流程验证。
