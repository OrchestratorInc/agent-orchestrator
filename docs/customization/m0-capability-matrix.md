# M0 四 Agent 能力矩阵（骨架）

| 项 | 值 |
| --- | --- |
| 日期 | 2026-11-03 |
| 工作区 commit | `7ce17e58247cc3e40669e9a29559c69cc18f04ac` |
| 分支 | `feat/multi-agent-desktop` |
| 工作区状态 | clean（`git status --porcelain` 无输出） |
| 状态 | **执行中 — A-01 已实测（2/15 行）；其余 13 行仍未填写** |

> 本文件是**骨架**，不是结论。
> 第一节矩阵只有 A-01 支撑的两行（8 格）已填实测结果，其余 52 格是 `NOT_TESTED`；
> A-02～A-15 在本文档编写时**一次也没有执行过**，**没有任何 provider turn 被消耗**
> （详见 `m0-report.md` 的「预算登记」）。
> 第二节是同一张矩阵的**静态源码对照**，用 `STATIC-SUPPORTED / STATIC-UNSUPPORTED / STATIC-UNKNOWN`
> 表达「源码里有没有这条路径」，供 M1 在运行时结果出现之前做设计。**静态结论永远不等于 PASS。**

图例：

- `STATIC-SUPPORTED` — 源码中存在该路径且可达（**假设**，待运行时验证）。
- `STATIC-UNSUPPORTED` — 源码明确不提供或明确拒绝。
- `STATIC-UNKNOWN` — 源码无法回答（取决于 provider 协议、安装或账号状态）。
- 运行时四类结果沿用设计 §4.1：`PASS` 需真实执行且有证据；`FAIL`；`UNSUPPORTED`；`BLOCKED`；`NOT_TESTED`。

---

## 1. 运行时矩阵（设计 §4.1 原文）

只有 A-01 支撑的两行（CLI 路径/版本可信、身份状态诊断）已填，其余 13 行仍未执行。
证据：`E:\code\ao-m0-scratch\evidence\agents\a01-probe.md`（证据 ID **A-01**）。

| 验证维度 | Codex | Claude Code | OMP | DeepSeekRuntime |
| --- | --- | --- | --- | --- |
| CLI 路径/版本可信 | PASS（A-01） | PASS（A-01） | PASS（A-01） | PASS（A-01，带标注） |
| 身份状态诊断 | PASS（A-01） | BLOCKED（A-01） | PASS（A-01，带标注） | PASS（A-01） |
| Chat 能正常启动 | NOT_TESTED | NOT_TESTED | NOT_TESTED | NOT_TESTED |
| TUI 能启动 | NOT_TESTED | NOT_TESTED | NOT_TESTED | NOT_TESTED |
| 结构化事件/流式输出 | NOT_TESTED | NOT_TESTED | NOT_TESTED | NOT_TESTED |
| Chat 发送/回复 | NOT_TESTED | NOT_TESTED | NOT_TESTED | NOT_TESTED |
| Queue 排队与派发 | NOT_TESTED | NOT_TESTED | NOT_TESTED | NOT_TESTED |
| Steer 同轮插入 | NOT_TESTED | NOT_TESTED | NOT_TESTED | NOT_TESTED |
| Stop / Interrupt | NOT_TESTED | NOT_TESTED | NOT_TESTED | NOT_TESTED |
| Session 恢复/重连 | NOT_TESTED | NOT_TESTED | NOT_TESTED | NOT_TESTED |
| 审批/拒绝操作有效 | NOT_TESTED | NOT_TESTED | NOT_TESTED | NOT_TESTED |
| Model / Effort 能力 | NOT_TESTED | NOT_TESTED | NOT_TESTED | NOT_TESTED |
| Usage / Cost 可观测 | NOT_TESTED | NOT_TESTED | NOT_TESTED | NOT_TESTED |
| Worktree 读写边界 | NOT_TESTED | NOT_TESTED | NOT_TESTED | NOT_TESTED |
| Windows 进程退出清理 | NOT_TESTED | NOT_TESTED | NOT_TESTED | NOT_TESTED |

填写规则：每个 `PASS` 必须附一条证据 ID（本节用 A-01；其余待填）；
`UNSUPPORTED` 必须是「驱动明确不提供且已确认」，不能由一次失败反推。

### 1.1 A-01 已填两行的逐格依据

证据文件：`E:\code\ao-m0-scratch\evidence\agents\a01-probe.md`（原始 HTTP 响应在
`...\agents\_a01-raw\`）。执行环境：隔离 daemon（`AO_DATA_DIR=E:\code\ao-m0-scratch\data-a01`，
端口 3041），**0 个 provider turn**。

| 维度 | Agent | 判定 | 依据（实测值） |
| --- | --- | --- | --- |
| CLI 路径/版本可信 | Codex | PASS | 解析到 npm 包内原生 `codex.exe`，`codex-cli 0.154.0` ≥ 0.146.0 |
| CLI 路径/版本可信 | Claude Code | PASS | `%USERPROFILE%\.local\bin\claude.exe`，`2.1.303`；打包运行时 Node `v22.23.2` ≥ 22 |
| CLI 路径/版本可信 | OMP | PASS | `%LOCALAPPDATA%\omp\omp.exe`，`omp/18.13.3` ≥ 15.0.0（且 ≥ 17.1.0） |
| CLI 路径/版本可信 | DeepSeekRuntime | PASS（带标注） | 解析到 `D:\tool\nvm\nvm\nodejs\dsh.cmd`，`0.1.5-rc.1`；**驱动无版本门禁**，故只能证明「可解析且可运行」，不能证明版本兼容 |
| 身份状态诊断 | Codex | PASS | `authorized`，`effectiveReadiness=ready`；`codex.exe login status` exit=0 |
| 身份状态诊断 | Claude Code | BLOCKED | `configured` / `auth_configured_unverified`；`provider_validation=provider_unreachable provider=gateway`；`effectiveReadiness=unknown`。根因：`ANTHROPIC_BASE_URL=http://127.0.0.1:15721` 无监听 |
| 身份状态诊断 | OMP | PASS（带标注） | 诊断链路正常并给出 `unknown` / `credential_file_missing`；但该结论与「本机有 6 个 omp 进程在跑」不一致，**3037 那次 10s `auth_check_timeout` 未能复现**，作为未解风险保留 |
| 身份状态诊断 | DeepSeekRuntime | PASS | `authorized`（`~\.dsh\.credentials.yaml`），`effectiveReadiness=ready` |

两点必须随表阅读：

1. **Claude 的 BLOCKED 不是驱动缺陷。** CLI 已登录（`cli_signed_in`），失败在
   `provider_validation`：`pkg/agentcreds/resolve.go:130-131` 只要 `ANTHROPIC_BASE_URL` 非空就选
   `gateway`，而该地址（本机 15721）当前无监听，`DefaultTimeout=3s`（`pkg/agentcreds/agentcreds.go:175`）
   下诊断为 `durationMs=0` 的连接拒绝。解除条件：启动该 gateway 或移除 `ANTHROPIC_BASE_URL`。
2. **OMP 的 PASS 只针对「诊断能力」。** 它没有 `auth status` 命令（`agent/omp/auth.go:19-21`），
   AO 读 `agent.db` 的 `auth_credentials`（实测 0 行）再读 `auth.json`（不存在），
   因此如实返回 `unknown`——这符合设计 §4.3「无法判定即 unknown，不得当成已登出」。
   它同时说明 **OMP 的真实登录状态仍未知**，A-02 若首轮失败应记 `BLOCKED_AUTH`。

同一批实测还澄清了一条**基座侧**问题，与驱动无关但会直接影响 A-02 之后的所有 Claude 用例：
`claudeacp` 按 `exe/../acp-runtime` → `exe/../resources/acp-runtime` 的顺序取**第一个存在的目录**
（`chatdriver/claudeacp/driver.go:573-588`），而 `E:\code\ao-m0-lab\frontend\acp-runtime` 是个只有
`package.json` 的空壳，遮蔽了真正带 `node/node.exe` 与 `claude-agent-acp` 的 `resources\acp-runtime`。
未设 `AO_ACP_RUNTIME_DIR` 时 Claude 的 chat 驱动 Probe 失败（`{"agents":[]}`），
显式指向真实目录后立即恢复。

---

## 2. 静态源码证据（非 PASS）

同一张矩阵的静态对照。单元格 = 静态判定 + 一个锚点（`path:line`），够 M1 用，不取代运行时证据。
完整推理与更多行号见 `E:\code\ao-m0-scratch\evidence\agents\static-survey.md`。

| 验证维度 | Codex | Claude Code | OMP | DeepSeekRuntime |
| --- | --- | --- | --- | --- |
| CLI 路径/版本可信 | STATIC-SUPPORTED — 下限 0.146.0 `chatdriver/codexappserver/driver.go:36` | STATIC-SUPPORTED — 二进制解析 `agent/claudecode/claudecode.go:544`；无 Claude CLI 下限，仅打包 Node ≥22 `chatdriver/claudeacp/driver.go:34` | STATIC-SUPPORTED — 下限 15.0.0 `chatdriver/ompacp/version.go:16` | STATIC-UNKNOWN — 未配置任何版本探测 `chatdriver/nativeacp/driver.go:106` |
| 身份状态诊断 | STATIC-SUPPORTED — `codex login status` `agent/codex/codex.go:321` | STATIC-SUPPORTED — `claude auth status` + 四级本地兜底 `agent/claudecode/auth.go:405` | STATIC-SUPPORTED — 只读本地 `agent.db`/`auth.json` `agent/omp/auth.go:35` | STATIC-UNKNOWN — 仅本地凭据存储，缺失即 unknown `agent/deepseekharness/auth.go:38` |
| Chat 能正常启动 | STATIC-SUPPORTED — 注册 `chatdriver/registry/registry.go:95` | STATIC-SUPPORTED — `chatdriver/registry/registry.go:97` | STATIC-SUPPORTED — `chatdriver/registry/registry.go:113` | STATIC-SUPPORTED — `chatdriver/registry/registry.go:119` |
| TUI 能启动 | STATIC-SUPPORTED — `codex` 交互启动 `agent/codex/codex.go:119` | STATIC-SUPPORTED — `agent/claudecode/claudecode.go:168` | STATIC-SUPPORTED — `agent/omp/omp.go:67` | STATIC-UNKNOWN — 只有一次性 headless `--profile headless` `agent/deepseekharness/deepseekharness.go:128` |
| 结构化事件/流式输出 | STATIC-SUPPORTED — `streaming` `chatdriver/codexappserver/driver.go:120` | STATIC-SUPPORTED — `chatdriver/claudeacp/driver.go:101` | STATIC-SUPPORTED — ACP 基线 `chatdriver/nativeacp/driver.go:84` | STATIC-SUPPORTED — ACP 基线 `chatdriver/nativeacp/driver.go:84` |
| Chat 发送/回复 | STATIC-SUPPORTED — `turn/start` `chatdriver/codexappserver/conversation.go:398` | STATIC-SUPPORTED — `session/prompt` `chatdriver/acp/conversation.go:473` | STATIC-SUPPORTED — 同 ACP `chatdriver/acp/conversation.go:473` | STATIC-SUPPORTED — 同 ACP `chatdriver/acp/conversation.go:473` |
| Queue 排队与派发 | STATIC-SUPPORTED — 控制层实现，与 provider 无关 `internal/service/chat/controller.go:1512` | 同左 `internal/service/chat/controller.go:1512` | 同左 `internal/service/chat/controller.go:1512` | 同左 `internal/service/chat/controller.go:1512` |
| Steer 同轮插入 | STATIC-SUPPORTED — `turn/steer` + `expectedTurnId` `chatdriver/codexappserver/steer.go:62` | STATIC-UNKNOWN — 仅当 agent 声明 `_meta.steering.supported` `chatdriver/acp/driver.go:945` | STATIC-UNKNOWN — 同一门控，且 `ompacp` 未配置任何 steer `chatdriver/ompacp/driver.go:26` | STATIC-UNKNOWN — 同一门控，`deepseekharnessacp` 未配置 `chatdriver/deepseekharnessacp/driver.go:28` |
| Stop / Interrupt | STATIC-SUPPORTED — `turn/interrupt` `chatdriver/codexappserver/conversation.go:707` | STATIC-SUPPORTED — `session/cancel` `chatdriver/acp/conversation.go:1107` | STATIC-SUPPORTED — `chatdriver/nativeacp/driver.go:87` | STATIC-SUPPORTED — `chatdriver/nativeacp/driver.go:87` |
| Session 恢复/重连 | STATIC-SUPPORTED — `thread/resume` + rollout 探测 `chatdriver/codexappserver/driver.go:363` | STATIC-SUPPORTED — `session/load`／`session/resume` `chatdriver/acp/driver.go:529` | STATIC-SUPPORTED — 条件性，握手后可被降级 `chatdriver/acp/driver.go:937` | STATIC-SUPPORTED — 同 ACP `chatdriver/acp/driver.go:937` |
| 审批/拒绝操作有效 | STATIC-SUPPORTED — `approvalSettings`+`approvalReviewer` `chatdriver/codexappserver/driver.go:684` | STATIC-SUPPORTED — 模式 id 映射 `chatdriver/claudeacp/driver.go:483` | STATIC-SUPPORTED — 启动即固定，改动需重启 `chatdriver/ompacp/driver.go:64` | STATIC-SUPPORTED — 逐请求 `allow_once` `chatdriver/deepseekharnessacp/driver.go:145` |
| Model / Effort 能力 | STATIC-SUPPORTED — model+`model_reasoning_effort` `chatdriver/codexappserver/driver.go:490` | STATIC-SUPPORTED — `model`+`effort` 会话选项 `chatdriver/claudeacp/driver.go:513` | Model STATIC-SUPPORTED `chatdriver/ompacp/driver.go:57`；Effort **STATIC-UNSUPPORTED**（Chat 无 effort） | STATIC-SUPPORTED — 不透明 model + effort 映射 `chatdriver/deepseekharnessacp/driver.go:82` |
| Usage / Cost 可观测 | Usage STATIC-SUPPORTED 精确 token `chatdriver/codexappserver/normalize.go:560`；Cost 缺失→估算 | Usage STATIC-SUPPORTED 精确 token `chatdriver/acp/conversation.go:894`；Cost 缺失→估算 | Usage 声明 STATIC-SUPPORTED `chatdriver/ompacp/driver.go:29`；Cost 缺失→估算 | Usage **STATIC-UNKNOWN**（未声明 `chatdriver/deepseekharnessacp/driver.go:27`）；Cost 缺失→估算 |
| Worktree 读写边界 | STATIC-SUPPORTED — accept-edits/auto 用 `workspace-write` `chatdriver/codexappserver/driver.go:684` | STATIC-UNKNOWN — 只有权限模式，无 CLI 级文件系统围栏 `pkg/agentruntime/command.go:170` | STATIC-UNKNOWN — 交给 `--approval-mode` `chatdriver/ompacp/driver.go:48` | STATIC-UNKNOWN — 只有逐请求 allow `chatdriver/deepseekharnessacp/driver.go:145` |
| Windows 进程退出清理 | STATIC-SUPPORTED — 持久宿主 shutdown，超时 3s 后 Kill `chatdriver/codexappserver/driver.go:616` | STATIC-SUPPORTED — `taskkill /PID /T /F` `chatdriver/acp/process_windows.go:23` | STATIC-SUPPORTED — 同一 ACP 传输 `chatdriver/acp/process_windows.go:23` | STATIC-SUPPORTED — 同一 ACP 传输 `chatdriver/acp/process_windows.go:23` |

### 2.1 读这张表要注意的三点

1. **Queue 不是 provider 能力。** 四个 Agent 的排队都在 AO 控制层完成
   （`internal/service/chat/controller.go:1512`：`shouldQueue := queueWhenBusy && (c.busy() || queuedAhead)`），
   因此运行时 Queue 失败只可能是 AO 缺陷，不能归因到 CLI。
2. **Steer 的门控是协议事实，不是适配器事实。** `chatdriver/acp/driver.go:945`
   只有 `extensionSupported(init.Meta, "steering")` 才把 `steer` 打开；
   三个 ACP Agent 都必须先握手才能给出答案，所以静态只能是 `STATIC-UNKNOWN`。
   代码中禁止把 stop+send 当作 Steer（设计 §4.3）。
3. **Cost 四个 Agent 都没有 provider 级上报。** 全仓 `chatdriver` 内无任何 `Cost:` 赋值；
   金额只能来自 AO 自己的定价估算（`internal/service/usage/cost_backfiller.go:29`），
   因此在矩阵中必须记 `estimated`，不能记 `exact`。

---

## 3. 本机已安装 CLI 的事实（2026-11-03，A-01 实测后修订）

先给结论：**daemon 实际解析到的二进制，与交互 shell 解析到的不是同一个文件**。
下表两列必须分开读，否则「shell 里 `--version` 是多少」会被误当成「AO 用的是多少」。

| Agent | shell 解析到 | **daemon 实际解析到** | **daemon 侧版本** | 驱动强制的下限 | 是否达标 |
| --- | --- | --- | --- | --- | --- |
| Codex | `codex.ps1`（`D:\tool\nvm\nvm\nodejs`）→ `codex-cli 0.163.0` | `%APPDATA%\Roaming\npm\node_modules\@openai\codex\node_modules\@openai\codex-win32-x64\vendor\x86_64-pc-windows-msvc\bin\codex.exe` | **`codex-cli 0.154.0`** | `0.146.0`（`chatdriver/codexappserver/driver.go:36`） | 是（0.154.0 ≥ 0.146.0） |
| Claude Code | `%USERPROFILE%\.local\bin\claude.exe` → `2.1.303` | 同一文件 | `2.1.303 (Claude Code)` | 无 Claude CLI 下限；打包 ACP 运行时要求 **Node ≥ 22**（`chatdriver/claudeacp/driver.go:34`） | 是（运行时 Node `v22.23.2`） |
| OMP | `%LOCALAPPDATA%\omp\omp.exe` → `omp/18.13.3` | 同一文件 | `omp/18.13.3` | `15.0.0`（`chatdriver/ompacp/version.go:16`）；activity 扩展另需 ≥ `17.1.0`（`agent/omp/hooks.go:23`） | 是（同时满足两者） |
| DeepSeekRuntime | `dsh.ps1`（`D:\tool\nvm\nvm\nodejs`）→ `0.1.5-rc.1` | `D:\tool\nvm\nvm\nodejs\dsh.cmd` | `0.1.5-rc.1` | **无**（`deepseekharnessacp` 未配置 `VersionProbe`，`chatdriver/nativeacp/driver.go:106`） | 无门槛可判；`rc` 预发布，兼容性未验证 |

daemon 侧的二进制度量来自 `POST /api/v1/agents/{agent}/models/refresh` 返回的
`metadata.binary`（四个值见 `_a01-raw/41-models-*.json`），版本由对同一路径直接执行 `--version` 得到。

必须随表阅读的四点：

1. **Codex 的版本号此前记错了。** shell 里是 `0.163.0`（`.ps1` shim），AO 解析到的是 npm 包内的原生
   `codex.exe`，为 `0.154.0`。两者都 ≥ 0.146.0，结论不变，但报告里不能再写 0.163.0。
2. **此前担心的「`.ps1` 让 AO 解析不到」没有发生。** AO 的 Windows 候选路径
   （`agent/codex/codex.go:406-443`、`agent/deepseekharness/deepseekharness.go:231-238`）经
   `node_modules` 逐个探测，成功拿到 `codex.exe`；`dsh` 则命中 `.cmd`。
   这仍是**实测结论**而非设计保证——换一台机器（例如仅用 fnm/volta 且无 `%APPDATA%\npm`）时可能不同。
3. **Qwen 不在本次四 Agent 范围内**，其版本门槛对本矩阵不适用。
4. 三个 ACP 驱动的运行时下限都不来自 CLI 自身：OMP 来自 `omp --version`，Claude 来自打包 Node 版本，
   DeepSeek 完全没有版本探测——只有 Codex 的下限是 AI CLI 版本号本身。

### 3.1 Claude 打包运行时的目录遮蔽（实测发现，影响所有 Claude Chat 用例）

`chatdriver/claudeacp/driver.go:573-588` 逐候选返回**第一个存在的目录**：

```
<exe 的父目录的父目录>/acp-runtime
<exe 的父目录的父目录>/resources/acp-runtime
```

本机 `E:\code\ao-m0-lab\frontend\acp-runtime` 存在但只有 `package.json` / `package-lock.json`；
真正的运行时在 `E:\code\ao-m0-lab\frontend\resources\acp-runtime`（含 `node\node.exe` = v22.23.2、
`node_modules\@agentclientprotocol\claude-agent-acp` 与 `.ao-acp-runtime.json` 签名文件）。

后果与验证：

- 未设 `AO_ACP_RUNTIME_DIR`：`GET /api/v1/agents/chat/permission-modes?agents=claude-code` → `{"agents":[]}`，
  即 chat 驱动 Probe 失败，而同一时刻另外三个 Agent 正常。
- 显式 `AO_ACP_RUNTIME_DIR=E:\...\resources\acp-runtime` 重启后：同一路由返回 4 个 permission mode。
- 因此 Claude 的 A-01 chat 判定取**设置该变量之后**的结果；同时必须在基座记录里写明这一前置条件，
  否则 A-02 会以「驱动不可用」失败，而不是以真实原因失败。

### 3.2 A-11 的免费一半：模型目录实测（无 prompt）

`POST /api/v1/agents/{agent}/models/refresh`（`controllers/agents.go:42`）。三个 Agent 走 CLI/ACP 发现，
Claude 走 gateway 查询失败，全部**未发送任何 prompt**。

| Agent | source | 数量 | 示例 id | effort 选项 |
| --- | --- | --- | --- | --- |
| Codex | `cli`（app-server `model/list`） | 4 | `gpt-6-astra`、`gpt-5.6-sol`、`gpt-5.6-luna`、`gpt-5.6-terra` | `low/medium/high/xhigh/max`，其中 astra/sol/terra 另有 `ultra`；每个模型带 `defaultEffort` |
| Claude Code | `manual` | **0** | — | —（`refreshState=error`：`could not reach the configured gateway ... 127.0.0.1:15721`） |
| OMP | `cli`（`omp models --json`） | **58** | `codegpt/cn:deep-model`、`codegpt/GLM-5.3` … | **全部为空**——与 §4 / 「Chat 无 effort」的静态结论一致 |
| DeepSeekRuntime | `acp`（`dsh --profile acp` → `session/new`） | 4 | `["deepseek-official","deepseek-v4-flash"]` 等 JSON 数组字符串 | 本次不可见（须 session 后的 `reasoning_effort`） |

DeepSeek 的这一读**确认免费**：`chatdriver/acp/driver.go:220-261` 的 `DiscoverConfigOptions` 只做
`spawn → initialize → session/new → 读 configOptions → 关闭`，源码中没有任何 `session/prompt`。

---

## 4. 为什么四个 Agent 不能互换

同一份 AO 代码，四个 Agent 在五个维度上的语义并不相同。M1 的 UI 与状态机不能把它们当成同一种东西。

### 4.1 Steer（同轮插入）

| Agent | 机制 | 证据 |
| --- | --- | --- |
| Codex | 原生 `turn/steer`，带 `expectedTurnId` 前置条件；turn id 保持不变，只有一次 `turn/started`／`turn/completed` | `chatdriver/codexappserver/steer.go:62`；能力声明 `driver.go:153` |
| Claude Code | 只有 ACP 扩展 `_session/steering`，且必须由 agent 在 `initialize` 中声明 `_meta.steering.supported`；`idleBehavior` 固定为 `promptRequired` | `chatdriver/acp/steer.go:34`；门控 `chatdriver/acp/driver.go:945` |
| OMP | 同上，完全取决于 `omp acp` 是否声明该扩展；AO 侧未做任何 provider 特化 | `chatdriver/ompacp/driver.go:26` |
| DeepSeekRuntime | 同上，`dsh --profile acp` 是否声明未在源码中体现 | `chatdriver/deepseekharnessacp/driver.go:28` |

后果：Steer 控件必须由 `capabilities` 数组驱动（`internal/service/chat/steer.go:210`
同时要求 `ports.ChatSteerer` 断言与 `ChatCapabilitySteer`），不能按 harness 名字硬编码；
没有 `steer` 时必须走 Queue 并如实标注（设计 §4.3）。

### 4.2 Resume（会话恢复）

| Agent | 机制 | 证据 |
| --- | --- | --- |
| Codex | `thread/resume`；且 AO 先用 rollout 文件是否存在排除「只预约了 UUID 但没有任何历史」的线程 | `chatdriver/codexappserver/driver.go:363`；`agent/codex/codex.go:243` |
| Claude Code | 有 `loadSession` 就用 `session/load`（可重放历史），否则退到 `session/resume`（只恢复上下文、无类型化历史）；两者都没有时把 `resume` 能力降级 | `chatdriver/acp/driver.go:529`、`:937` |
| OMP | 同 Claude 的 ACP 二选一逻辑 | `chatdriver/acp/driver.go:937` |
| DeepSeekRuntime | 同 ACP；但终端路径的 headless restore 还需要新任务，且通常没有 native id | `chatdriver/acp/driver.go:937`；`agent/deepseekharness/version_refresh.go:3` |

后果：`history` 能力不等于 `resume` 能力。只能 `session/resume` 的 Agent 在重连后没有
可回放的类型化历史，UI 不能假定四者一致。

### 4.3 Approvals（权限模式映射）

| Agent | default | accept-edits | auto | bypass |
| --- | --- | --- | --- | --- |
| Codex（Chat） | `never` + `danger-full-access` | `on-request` + `workspace-write` | `on-request` + `workspace-write` + reviewer `auto_review` | 同 default |
| Codex（TUI） | `--dangerously-bypass-approvals-and-sandbox` | `--ask-for-approval on-request` | 同 + `approvals_reviewer="auto_review"` | 同 default |
| Claude Code | 不加 `--permission-mode`（由 `~/.claude/settings.json` 决定） | `acceptEdits` | `auto` | `bypassPermissions` |
| OMP | 不加 `--approval-mode` | `write` | `write` | `yolo` |
| DeepSeekRuntime | 一律挂起等人工 | 仅对 `edit/delete/move` 类型的工具自动 `allow_once` | 任何请求都自动 `allow_once` | 无需条目（profile 自己决定） |

证据：`chatdriver/codexappserver/driver.go:684`、`pkg/agentruntime/command.go:199`、
`chatdriver/claudeacp/driver.go:483`、`chatdriver/ompacp/driver.go:48`、
`chatdriver/deepseekharnessacp/driver.go:145`。

后果：同一个「accept-edits」在四个 Agent 上的实际约束强度不同——Codex 是沙箱级、
Claude 是模式级、OMP 是启动参数级（**会话中不可改**，`chatdriver/ompacp/driver.go:64`）、
DeepSeek 是逐请求级。不能用一个布尔值向用户解释。

### 4.4 Usage / Cost

| Agent | token 来源 | 精确度 | Cost |
| --- | --- | --- | --- |
| Codex | `thread/tokenUsage/updated` 通知，含 `Input/Output/Cached/Total` 与 `ModelContextWindow` | exact | 无 provider 上报 → estimated |
| Claude Code | ACP `session/prompt` 返回的 `Usage`，`TotalsKnown: true` | exact | 无 provider 上报 → estimated |
| OMP | 声明了 `usage` 能力，实际取决于 agent 是否填 `resp.Usage` | 有则 exact，无则 unknown | estimated |
| DeepSeekRuntime | **未声明** `usage` 能力 | unknown | estimated |

证据：`chatdriver/codexappserver/normalize.go:560`、`chatdriver/acp/conversation.go:894`、
`chatdriver/ompacp/driver.go:29`、`chatdriver/deepseekharnessacp/driver.go:27`、
估算来源 `internal/service/usage/cost_backfiller.go:29`。

后果：设计 §4.3「Provider 不提供准确 Token 时标 unknown/estimated；预算硬限制只约束新派发」
必须逐 Agent 执行——四个 Agent 都不能声称精确成本。

### 4.5 DeepSeekRuntime 的 model / effort 是 Harness 专属值

- model 不是普通字符串，而是 Harness 自己编码的 JSON 数组字符串，
  形如 `["deepseek-official","deepseek-v4-flash"]`，AO 只做透传
  （`chatdriver/deepseekharnessacp/driver.go:79`）。
- effort 需要映射：`none/minimal/off→off`、`low→low`、`medium→high`、`high→high`、
  `xhigh/max→max`，并在开轮前校验，非法值直接拒绝而不是静默忽略
  （`chatdriver/deepseekharnessacp/driver.go:67`、`:127`）。
- 终端（headless）profile **没有** model/effort 参数，路由由 profile 自己的配置决定
  （`agent/deepseekharness/deepseekharness.go:32`）。因此同一 Agent 的 TUI 与 Chat
  在模型选择上不等价。
- 启动方式是 `dsh --profile acp`（`chatdriver/deepseekharnessacp/driver.go:57`），
  与其他三个的 `app-server` / `acp` / 打包 Node 桥完全不同。

---

## 5. 降级策略（对应设计 §4.3）

| 触发条件（能力事实） | 降级动作 | 不得做的事 | 证据 |
| --- | --- | --- | --- |
| `capabilities` 中没有 `steer`（Claude/OMP/DSH 的 ACP 门控 `chatdriver/acp/driver.go:945`） | 隐藏实时 Steer，改推荐 Queue；用 `steer-or-send` 确认消息确实入队 | 不得把 stop+send 伪称 Steer | `internal/service/chat/steer.go:210`；`controllers/conversation_steer.go:176` |
| Chat 驱动不存在（`chatdriver/registry/registry.go:139` 返回 `ErrChatUnsupported`）或 Probe 失败 | 标明 TUI-only，使用已有终端运行时 | 不得从终端字节伪造结构化 Chat 事件 | `internal/service/chat/service.go:2244` |
| `driver.Probe` 返回 `ErrChatAuthRequired`（`chatdriver/nativeacp/driver.go:115`） | 标 `BLOCKED_AUTH`，提供用户自行登录入口 | 不得抓取或代填凭据 | `chatdriver/ompacp/driver.go:22`、`agent/omp/auth.go:19` |
| provider 未上报 token（DSH 未声明 `usage`，`chatdriver/deepseekharnessacp/driver.go:27`） | 标 `unknown`；预算硬限制只约束新派发 | 不得声称精准截断 | 设计 §4.3；`chatdriver/acp/conversation.go:894` |
| 只有 `session/resume` 没有 `session/load`（`chatdriver/acp/driver.go:937`） | 恢复上下文但显示「无历史回放」，必要时展示 `RECOVERY_UNCERTAIN` 并暂停自动重放 | 不得声称历史无损 | `chatdriver/acp/driver.go:529` |
| Codex `thread/resume` 报 "no rollout found"（`agent/codex/codex.go:234`） | 视为「会话未真正开始」，允许新开 | 不得在已有真实内容时静默新开 | `chatdriver/codexappserver/driver.go:432` |
| 权限模式无法被 provider 强制（OMP 启动即固定 `chatdriver/ompacp/driver.go:64`；Claude 无 CLI 级文件系统围栏 `pkg/agentruntime/command.go:170`） | 不启用危险写操作的自动审批路径 | 不得把「模式」当成「沙箱」 | 设计 §4.3；`chatdriver/codexappserver/driver.go:684` 是唯一有沙箱语义的映射 |
| Claude 的 `provider_validation=provider_unreachable`（`ANTHROPIC_BASE_URL` 指向的本机 gateway 无监听，实测 A-01） | 标 `BLOCKED_AUTH`/`BLOCKED`，把 gateway 端点作为待处置项交给用户；不得改用其他 provider 冒充 Claude | 不得读取或打印 `ANTHROPIC_AUTH_TOKEN`；不得把「CLI 已登录」当成「AO 可发轮次」 | `pkg/agentcreds/resolve.go:130-131`；`pkg/agentcreds/agentcreds.go:175`；`agent/claudecode/auth.go:296-301` |
| Claude chat 驱动 `resolveRuntime` 被空壳 `acp-runtime` 目录遮蔽（实测 A-01） | 显式设 `AO_ACP_RUNTIME_DIR` 指向含 `node/node.exe` 与 `claude-agent-acp` 的目录，再判定驱动可用性 | 不得把「`permission-modes` 返回空」当成 Claude 不支持 Chat | `chatdriver/claudeacp/driver.go:573-588`；`_a01-raw/50-pm-claude-code.json` 与 `73-permission-modes-final.json` |

---

## 6. 待补（本文档的占位清单）

| 位置 | 待补内容 | 触发条件 |
| --- | --- | --- |
| §1 剩余 13 行 × 4 Agent（52 格） | 运行时结果 + 证据 ID | A-02～A-15 真实执行后 |
| §1「Model / Effort 能力」 | 免费的一半已测得（见 §3.2），仍需 session 内的 `config-options` 才能确认 effort 是否可设 | A-11 实测 |
| §4.1 Steer 列（Claude/OMP/DSH） | 实测 `capabilities` 里是否出现 `steer` | 有 session 后读一次快照，或 A-06 |
| §4.4 Usage 列（OMP/DSH） | 实测是否收到 `usage` 事件 | A-03/A-12 |
| 全表 | 成本列 `exact/estimated/unknown` 的最终判定 | A-12 实测 |
| §3 | Claude 的 gateway 端点（`127.0.0.1:15721`）是否会被启动；否则 A-02 永远 BLOCKED | 用户处置 `~/.claude/settings.json` 的 `ANTHROPIC_BASE_URL` |
| §3 | OMP 的 10s `auth_check_timeout` 复现条件 | 需在 OMP 活跃写库时重放 |

### 6.1 本次已用 A-01 填掉的项目

- §1 的 `CLI 路径/版本可信` 与 `身份状态诊断` 两行 × 4 Agent（8 格），全部带证据。
- §3 的「shell 解析 vs daemon 解析」对照（修正了 Codex 版本号）。
- §3.1 Claude 打包运行时目录遮蔽问题。
- §3.2 四个 Agent 的模型目录（免费部分）。

本文件在 M0 真正执行前不代表任何能力已可用；已填的 8 格是实测值，其余 52 格不是。
