# M0 Adapter 差距单与 M1 输入清单

> 文档性质：**设计文档**，不含代码改动。对应 M0 退出条件 **G0-06**
> （"产出 Adapter 差距单及 M1 输入清单（协议差异、兼容方案、阻断清单）"）。
> 编写时间：2026-11-03。上游基线：`7ce17e58247cc3e40669e9a29559c69cc18f04ac`（`feat/multi-agent-desktop`）。
>
> **证据分级**：本文每条结论都带 `path:line` 或证据文件名。标签含义——
> `[源码]` 只读代码可证；`[实测]` M0 实跑产出（A-01 等）；`[推论]` 由前述事实推导，尚未直接观测。
> 本文件**不**声称任何单元格为 PASS；能力矩阵的最终判定属 `m0-report.md` / `m0-capability-matrix.md`。
>
> **输入证据**：`E:\code\ao-m0-scratch\evidence\baseline-failures.md`（含 Appendix E）、
> `evidence\agents\a01-probe.md`、`evidence\agents\static-survey.md`、
> `evidence\windows-base-survey.md`、`evidence\build-go.txt`、`evidence\build-frontend.txt`、
> `evidence\base.txt`，以及 `docs/customization/design.md` §8/§9/§12/§13。

---

## 1. 协议差异矩阵

### 1.1 四种 Agent 实际属于两个协议族

| Agent | harness id | 协议族 | 传输 | AO 侧实现层 | 注册点 |
| --- | --- | --- | --- | --- | --- |
| Codex | `codex` | **native app-server JSON-RPC**（自定义） | 子进程 stdio，持久 host | `chatdriver/codexappserver/` + `persistenthost` | `chatdriver/registry/registry.go:94-97` |
| Claude Code | `claude-code` | **ACP**（Agent Client Protocol） | AO 自带 `acp-runtime` 里的 `node` 启动 `claude-agent-acp` | `chatdriver/claudeacp/` + 共享 `chatdriver/acp/` | `chatdriver/registry/registry.go:94-97` |
| Oh My Pi | `omp` | **ACP** | 同上共享 transport，`omp acp` 直连 | `chatdriver/ompacp/` + `nativeacp/` + `acp/` | `chatdriver/registry/registry.go:112-113` |
| DeepSeekRuntime | `deepseek-harness` | **ACP** | `dsh --profile acp` | `chatdriver/deepseekharnessacp/` + `nativeacp/` + `acp/` | `chatdriver/registry/registry.go:118-119` |

`[源码]` 关键结构性事实：**只有 Codex 一条自研协议，其余三条共用同一套 ACP transport。**
`acp/` 承担会话生命周期、审批请求、diff 分片、进程清理；三个 binding
（`claudeacp` / `ompacp` / `deepseekharnessacp`）只负责 argv、env、capability 增量与少量值映射。
这意味着 **ACP 侧的差距是"一次修复、三处受益"，而 Codex 侧的差距必须单独处理**。

启动形态差异（`[源码]`，`evidence\agents\static-survey.md` §1-§4）：

```
Codex        : <codex.exe> app-server                                   (driver.go:584)
Claude chat  : <acp-runtime>/node <...>/claude-agent-acp/dist/index.js  (claudeacp/driver.go:537-590)
OMP chat     : <omp> acp [--model] [--append-system-prompt] [--approval-mode write|yolo]
                                                                        (ompacp/driver.go:40-53)
DSH chat     : <dsh> --profile acp                                      (deepseekharnessacp/driver.go:57-59)
```

### 1.2 逐协议的能力语义

#### Codex —— native app-server

- 线程/轮次模型：`thread/*` 与 `turn/*`。Steer 走 `turn/steer`，**必须携带
  `expectedTurnId` 作为前置条件**（`chatdriver/codexappserver/steer.go:51,59`）。
- 恢复走 `thread/resume`；对没有 rollout 文件的 UUID 会被拒绝，此时回退
  `thread/start` 处理保留 id（`driver.go:432-450`）。
  「会话是否存在」由扫描 `CODEX_HOME/sessions/rollout-*-<id>.jsonl[.zst]` 回答
  （`agent/codex/codex.go:243-302`）。
- 拒绝语义是**协议级错误码**：`-32600 "no active turn to steer"`、
  `-32600 "cannot steer a compact turn"`（`steer.go:34-36`）。
- 版本门禁 `0.146.0` 且**只在 chat 侧**（`codexappserver/driver.go:36,186-195`）；TUI 无门禁。

#### ACP 族 —— Claude / OMP / DeepSeek

- 会话/轮次：`session/new`、`session/prompt`、`session/cancel`。
- 恢复有两种**语义不同**的方式，`acp/driver.go:450-519`：
  - `session/load` —— **重放历史**，agent 由客户端提供的历史重建上下文；
  - `session/resume` —— **保留 native 上下文、不重放历史**。
  两者都不被广告时，`ChatCapabilityResume` 在运行时被降级
  （`acp/driver.go:936-939`），`Resume` 返回 `ErrChatResumeFailed`。
- Steer 是**扩展能力**，由 `initialize` 的 `_meta.steering.supported` 决定
  （`acp/driver.go:943-946`）；调用方法 `_session/steering`，请求
  `idleBehavior:"promptRequired"`（`acp/steer.go:34-96`）。
  返回 `injected` 才算同轮插入；`promptRequired` → `ErrChatNoSteerableTurn`；
  **`startedNewTurn` 被当作契约违约直接报错**，因为"声称已插入同轮"会把
  provider 的独立轮次错误归属到当前轮（`acp/steer.go:84-93`）。
- 审批：ACP 侧是 `session/request_permission` 一类的请求/应答，
  AO 在 `acp/client.go:145-160` 暴露、`acp/conversation.go:1142+` 解析。

### 1.3 会击穿"朴素统一模型"的四类不对称

| # | 不对称 | 具体表现 | 证据 |
| --- | --- | --- | --- |
| A1 | **Steer 可用性不同源** | Codex 恒可用；ACP 三者取决于运行时 `_meta`。Claude 的 adapter **不配置** steering，OMP/DSH 也没有显式配置——三者的真值只能在 session 握手后才知道 | `codexappserver/steer.go:51`；`acp/driver.go:943-946`；`static-survey.md` §2.2/§3.2/§4.2 |
| A2 | **Resume 语义两种** | `session/load`（重放历史）vs `session/resume`（保留上下文不重放）。把两者都叫 "resume" 会导致历史重复或上下文丢失 | `acp/driver.go:450-519,936-939` |
| A3 | **审批模式的生效时机不同** | Codex 每轮可改（`--ask-for-approval` + `approvalSettings`）；Claude 用 ACP session mode；**OMP 在进程启动时固定**，中途改返回 `ErrACPSetterUnsupported`（"restart Chat"）；DSH 逐请求应答 `allow_once` | `driver.go:684-707`；`claudeacp/driver.go:483-494`；`ompacp/driver.go:48-53,64-81`；`deepseekharnessacp/driver.go:145-161` |
| A4 | **Model/Effort 不是通用字段** | OMP 只映射 `model`，**无 chat effort**（TUI 有 `--thinking`，ACP 没有）；DSH 的 model 值是 provider 专属 JSON 数组字符串（如 `["deepseek-official","deepseek-v4-flash"]`），effort 要经 AO 等级→`off/low/high/max` 映射；Codex 是 `model` + `model_reasoning_effort` | `ompacp/driver.go:57-62`；`deepseekharnessacp/driver.go:67-138`；`codexappserver/driver.go:490-496` |

实测补充（`[实测]` `agents\a01-probe.md` §3）：四个 agent 的模型目录**只有 Codex 带 effort**
（4 个模型，`low/medium/high/xhigh/max`，astra/sol/terra 另有 `ultra`）；OMP 58 个模型
**全部 effort 为空**；DSH 4 个模型 effort 需 session 后才有；Claude 0 个（gateway 不可达）。
这直接支持 A4：**"统一 effort 下拉框"是错误抽象**。

### 1.4 工作流层可以安全假设的公共分母

只有下列几条是四家**共同**且已验证存在的（`[源码]` + `[实测]`）：

1. **一个会话一次只有一个 in-flight provider turn**——由 AO controller 串行化保证，
   不是 provider 保证（`service/chat/controller.go:1502-1522`）。
2. **Stop/中断**：Codex `turn/interrupt`，ACP `session/cancel`，四家都有
   （`codexappserver/conversation.go:700-721`；`acp/conversation.go:1107-1147`）。
3. **结构化流式事件**（message/tool/plan/diff 事件族）：`ports/chat.go:953-1021`。
4. **审批作为可选前置**：三家 ACP + Codex 都能发审批请求，但**模式切换时机**不同（A3）。
5. **Windows 进程树清理**：Codex 走 `persistenthost.Shutdown` + 3s 后 Kill；
   ACP 走 `taskkill /PID <pid> /T /F`（`acp/process_windows.go:23-33`）。

**不能**假设的：steer 可用、resume 语义一致、effort 存在、审批可中途切换、
cost 由 provider 提供、任一 agent 有原生 turn-anchored fork
（`acp/driver.go:941-943` 明确 `fork` 为 false："ACP has no turn-anchored fork in the pinned SDK"）。

---

## 2. 兼容方案

每个差距对应一个 AO 层已有的（或唯一合理的）机制。**"已交付"= 本仓库现有代码即具备；
"M1 待做"= 需要新增编排层代码。**

| # | 差距 | AO 层机制 | 锚点 | 状态 |
| --- | --- | --- | --- | --- |
| C1 | Steer 未必可用 | **能力标志 + 类型断言双门禁**：`ChatCapabilitySteer` 与 `ports.ChatSteerer` 断言同时成立才走 steer | `service/chat/steer.go:210-213`；`ports/chat.go:175` | 已交付 |
| C2 | Steer 语义可能被偷换 | `SteerOrSend`：能力不足时**降级为发送**，绝不把 stop+send 称为 steer | `service/chat/steer.go:160-212,566` | 已交付 |
| C3 | 排队必须跨 provider 一致 | **Queue 在 controller 层**，与 provider 无关：忙时持久化排队、`NextQueuedTurn` 取队首、只向 driver 派发一轮 | `service/chat/controller.go:1502-1522`；`provisioning_queue.go:20-30` | 已交付 |
| C4 | Resume 两种语义 | 按握手结果**运行时降级** `ChatCapabilityResume`；`loadSession` 存在才有 `ChatCapabilityHistory`。**恢复失败必须显式失败**，不得伪称继承 | `acp/driver.go:450-519,936-940` | 已交付 |
| C5 | TUI→Chat 交接可能丢历史 | **fail-closed 交接门**：必须实现 `AgentInterfaceHandoff`；要换 surface 还需 `HistoryProbe`，否则 `ErrInterfaceHandoffUnsupported`。OMP/DSH 正是因此被拒 | `session_manager/interface_transition.go:695-706,813`；`agent/omp/omp.go:44`；`static-survey.md` §3.4/§4.4 | 已交付 |
| C6 | 审批模式不能中途改 | 模式值由 driver 声明；不支持 setter 时返回 `ErrACPSetterUnsupported` 并要求重启 Chat，**UI 必须如实显示"需重启"** | `ompacp/driver.go:64-81` | 已交付（UI 文案待 M2 确认） |
| C7 | 模型/effort 不可通用 | 每个 adapter 自带映射与校验；未知值透传但不臆造；OMP 不提供 effort 就不显示 effort | `deepseekharnessacp/driver.go:67-138`；`ompacp/driver.go:57-62` | 已交付（缺"能力查询"给编排层） |
| C8 | 用量/成本不精确 | `ChatUsage.TotalsKnown` / `ContextKnown` 区分"缺失"与"真零"；`Cost` 指针 nil = provider 未报；AO 用定价目录**估算**回填 | `ports/chat.go:623-640`；`service/usage/cost_backfiller.go:29` | 已交付 |
| C9 | 编排层需要**推断**能力 | **缺少**把 driver capabilities 暴露给 Workflow 的聚合层（设计 §6.1 的 "Capability Resolver"） | `docs/customization/design.md:224-241` | **M1/M2 待做** |
| C10 | 消息语义未区分"已接受"与"已执行" | ACP 的 `injected` / `promptRequired` / `startedNewTurn` 已有区分，但 Workflow 层尚无对应状态机 | `acp/steer.go:84-93`；设计 §6.2 | **M2 待做** |

补充说明：`static-survey.md` §5.5 指出 **chat Probe 结果按 harness 缓存且只缓存成功**
（`service/chat/service.go:2404-2420`），所以"修好安装后能力仍显示不可用"是既知行为，
不是缺陷——但 M2 的 capability resolver **不能**依赖这个缓存做长生命周期决策。

---

## 3. 阻断清单

按"阻断能力范围"降序。每条给出：阻断什么 / 证据 / 性质 / 清除动作。

### B1 — `validateCodexDirectoryAncestors` 在标准 Windows 上不可能通过（产品缺陷）

- **阻断**：AO 的 Codex **多账号面**——账号列表、切换、逐账号 usage/capacity/reset-credit、
  login/logout/delete。
- **证据**：`baseline-failures.md` Appendix E.2。祖先遍历一路到卷根，只信任
  当前用户/SYSTEM/Administrators/TrustedInstaller（`codex_secure_fs_windows.go:116-148,318`），
  而标准机 `C:\Users` 有 `BUILTIN\Users:(OI)(CI)(F)`、`C:\` 有 `Authenticated Users:(M)`。
  实跑 daemon 记录 `reasonCode=account_storage_unsafe retryable=false`。
  四次探针（含 `icacls /inheritance:r` 的 owner-only 目录）全部同样失败。
- **性质**：**产品缺陷**（Windows 端口语义不对称，`//go:build windows`）。
  永久锁存：`retryable=false` 使 `codex_account_reconcile.go:34-40` 永不再试。
- **不阻断**：Codex Chat/TUI。就绪经 native fallback 正常解析为
  `installed → authorized → ready`（`_api_agents_readiness.json`）。
- **清除动作**：需改代码（放宽 Windows 祖先策略，或把遍历停在受信根）。**无环境动作可解**。

### B2 — `BuildHandoffNote` 裁剪时丢掉最新一轮（产品缺陷）

- **阻断**：provider 切换 / handoff 的"已验真"声明。
- **证据**：`baseline-failures.md` Appendix E.1。`clipHandoffNote`
  （`handoff_note.go:143-153`）→ `clipHandoffText`（`:336-346`）做**首尾各半**裁剪，
  而 `## Latest turn` 位于文档 60–90% 处，被整段丢弃。实测（Windows 16,000 rune 上限）：
  21 轮历史中 **存活 0 个 request、1 个 response**，`## Latest turn` 段不存在；
  同时正文仍断言 "really took place"，唯一遗漏提示是 "20 earlier turns are not shown"。
- **性质**：**产品缺陷**（纯逻辑，跨平台；`runtime.GOOS` 只改阈值：Windows 16,000 / 其他 32,000）。
- **平台无关**：LF-only clone 上同样失败；与 CRLF、路径分隔符无关。
- **清除动作**：需改代码（为 Latest turn 预留预算，或裁剪时按节保留而非首尾各半）。

### B3 — Windows 符号链接权限（**环境**，可清除）

- **阻断**：19 个 Go 测试 + 前端 ≥47 个测试（`cli-link`、`browser-profile-import` 残余 8 个、
  `build-host-bundle`、`build-acp-runtime-helpers` 等）。
- **证据**：`build-go.txt` §5 cause A；`baseline-failures.md` Appendix A。
  本机 `AllowDevelopmentWithoutDevLicense` **值缺失**、`SeCreateSymbolicLinkPrivilege` 未持有、
  `IsAdmin=False`；`ln -s` 复现 `ERROR_PRIVILEGE_NOT_HELD`。
  Go 的 `os.Symlink` 已带 `ALLOW_UNPRIVILEGED_CREATE`（`os/file_windows.go:371`），该标志
  **仅在开发者模式开启时生效**。
- **性质**：**环境**（可满足）。
- **清除动作**：开启 Windows 开发者模式（`设置 → 系统 → 开发者选项`），
  或为本账户授予 `SeCreateSymbolicLinkPrivilege`。**需用户批准，M0 不得自行更改。**
- 附带发现：`fsbrowser/service_test.go:18-21` 的跳过条件只认
  `ENOSYS/EOPNOTSUPP/os.ErrPermission`，而 Windows 返回的 `ERROR_PRIVILEGE_NOT_HELD`
  不满足 `os.IsPermission`，于是 `require.NoError` 触发而非 `t.Skipf`——这是**测试可移植性缺陷**。

### B4 — ConPTY 终端栅格（W-06 直接阻断）

- **阻断**：Windows 终端能力声明（设计 §5 W-06「Terminal/ConPTY 可打开、输入/输出和退出正确」）。
- **证据**：`TestConPTYStartsAtRequestedGrid` → `host_conpty_windows_test.go:84:
  "mode con did not finish"`（`build-go.txt` §5 cause E）。
- **性质**：**待定**——可能是环境（`cmd`/conhost 行为）也可能是产品。**不能标 PASS。**
- **清除动作**：先单独复跑该用例并抓 `ELECTRON_ENABLE_LOGGING=1` 级日志，判断是
  `mode con` 在当前 console 宿主下不可用还是 AO 的 READY/栅格握手有问题。

### B5 — `git worktree list --porcelain` 正斜杠 vs 反斜杠（**测试缺陷**，已确认）

- **阻断**：worktree 注册校验相关断言（影响 W-04 多仓库 Workspace、A-14 Worktree 保留的可信度）。
- **证据**：`baseline-failures.md` §3.5。实测 `git worktree list --porcelain` 输出
  `worktree E:/code/agent-orchestrator`（**正斜杠**），而
  `workspace_orphan_test.go:92` 断言 `"worktree "+second.Path+"\n"`（`second.Path` 是反斜杠），
  **在 Windows 上永不可能匹配**。
- **性质**：**测试缺陷（已确认）**。生产侧已核实为正确：`workspace.go:2436-2444`
  `findWorktree` 用 `filepath.Clean(path)` 与 `filepath.Clean(rec.Path)` 比较，
  实测 `Clean("E:/code/agent-orchestrator") == Clean("E:\\code\\agent-orchestrator")` → `true`，
  正斜杠被归一化。**失败仅发生在测试的裸字符串断言**（未走 `findWorktree`）。
  **M1 不受影响**；详见 `m1-inheritance-triage.md` §3.6。
- **清除动作**：把 `workspace_orphan_test.go:92` 的裸 `strings.Contains` 改成走
  `parseWorktreePorcelain` + `findWorktree`（与生产同一路径），或在断言前 `filepath.Clean` 两侧。

### B6 — `core.autocrlf=true` 破坏内容寻址与字节相等（环境，可清除）

- **阻断**：任何按哈希/字节比较的验证。已实测受害：定价目录
  `provider "anthropic" hash mismatch`、`PR contents\r\n` vs `\n`。
- **证据**：`baseline-failures.md` §3.6。仓库本地与全局 `core.autocrlf` **均为 true**；
  `pricing/.../11fc....json` 文件名 sha256 为 `11fc6828…`，
  文件实际 sha256 为 `5a6086…`，**LF 归一化后正好等于文件名**。
- **性质**：**环境**（检出配置）。
- **清除动作**：对本仓库设 `git config core.autocrlf false` 后重新检出（或加 `.gitattributes`
  的 `* text=auto eol=lf` 覆盖面）。**注意：加 `.gitattributes` 会改仓库，需单独批准。**

### B7 — `electron-forge package` 受代理影响（环境，可清除）

- **阻断**：前端打包产物的可重复性（不阻断 typecheck/build:daemon）。
- **证据**：`build-frontend.txt` §4。同一命令：设代理 → `TLS connection ... disconnected`（exit 1）；
  去掉 `HTTP(S)_PROXY/ALL_PROXY/NODE_USE_ENV_PROXY` → **exit 0**（产出 654 MB 的
  `out/Agent Orchestrator-win32-x64/`）；再去掉代理重跑 → `connect ETIMEDOUT`（exit 1）。
  即 `@electron/rebuild` 的 `got` 走代理会断 TLS，不走代理又超时。
- **性质**：**环境**（网络/代理）。
- **清除动作**：为 forge 单独配置可达的 registry/mirror，或在有直连时执行打包。
  另注：`npm ci` **不会**解出 Electron 二进制，需 `node node_modules/electron/install.js`。
- **副作用（已实测）**：`npm run build` 会把 `better-sqlite3` 重编译为 Electron ABI 149，
  此后在同一 `node_modules` 上跑 `npm test`（Node ABI 127）会有 39 个假失败。
  **顺序规则：先 `npm test` 后 `npm run build`，或之后 `npm rebuild better-sqlite3`。**

### B8 — pty-host 孤儿进程（产品缺陷，范围有限）

- **阻断**：W-10「清理与退出」的"不留隐藏进程"声明。
- **证据**：`base.txt:1010` —— daemon 3039 退出后仍残留
  `pid 16204 "ao.exe pty-host --surface=lazy shellterm-... cmd.exe"`，父进程 33648 已死。
  分离是有意的（`conpty/spawn_windows.go:78-88` `DETACHED_PROCESS`，让终端在 daemon 重启后存活），
  但 **daemon 没有清扫"会话从未持有过的 shell 终端"**（`base.txt:1016`）。
- **性质**：**产品缺陷**（生命周期清扫缺口），非环境。
- **清除动作**：M2/M6 需补 sweep；当前测试环境需人工 reap。
  相关风险：pty-host 监听回环端口且**无认证**（`conpty/host_main.go:27-31`），
  孤儿进程等于长期暴露一个无认证本地端口。

### B9 — 缺失的外部 CLI（环境，**但污染全量套件声明**）

- **阻断**：任何"整套测试通过"的声明；也阻断这 6 个 harness 的 A-01。
- **证据**：`build-go.txt` §5 cause D —— `command-code`、`kilocode`、`pi`、`codewhale` 二进制缺失
  （`agent: binary not found on PATH`，3+5 条记录）；`gemini`、`opencode` 的 shim 存在但
  `exit status 1` / 20s 超时。
- **性质**：**环境**（未安装）。
- **重要限定**：这 6 个**都不是 M0 的四个目标 agent**。它们只影响"全量套件"这一主张，
  不影响 Codex / Claude / OMP / DeepSeek 的能力结论。
- **清除动作**：若需全量绿，按 manifest 安装；若不需要，**在报告中明确声明
  "全量套件在 Windows 上不可作为门禁"**（与 CI 一致：见 B10）。

### B10 — 基线本身在 Windows 上不是门禁（结构性）

- **阻断**：把 `go test ./...` 或 `npm test` 当作 Windows 门禁。
- **证据**：`.github/workflows/go.yml` 的 `backend-core`/`chat-race`/`session-store-race`/
  `sqlite-race` 全部 `runs-on: ubuntu-latest`；Windows 只有 `windows-workspace` 一个窄 job
  （6 个 `-run` 过滤器）。`.github/workflows/frontend.yml:274` 的 `vitest` 也是 `ubuntu-latest`，
  全文件**没有任何 `windows-*` runner**。workflow 注释自陈："The suite above only ever runs on
  Linux … has never been exercised on Windows"。
- **性质**：**结构性缺口**（非缺陷、非环境）。
- **清除动作**：M6 前不要把 Windows 全量套件设为门禁；先用窄集（CI 已定义的 6 个 Windows 用例）
  作为回归集，逐步扩面。

### 3.1 阻断清单速查

| ID | 阻断对象 | 性质 | 可清除？ | 责任里程碑 |
| --- | --- | --- | --- | --- |
| B1 | Codex 多账号面 | **产品** | 否（需改码） | M2 |
| B2 | Handoff "已验真" | **产品** | 否（需改码） | M2 |
| B3 | 19 Go + ≥47 前端测试 | 环境 | 是（开发者模式） | M0 重跑 |
| B4 | W-06 终端声明 | 待定 | 待定 | M0/M2 |
| B5 | W-04 / A-14 worktree 断言 | **测试缺陷**（生产侧已核实正确） | 是（改测试断言） | 不阻断任何里程碑 |
| B6 | 一切哈希/字节比较 | 环境 | 是（autocrlf=false） | M1 前 |
| B7 | 打包可重复性 | 环境 | 是（代理/顺序） | M6 |
| B8 | W-10 "不留进程" | **产品** | 需改码 | M2/M6 |
| B9 | "全量套件绿" | 环境 | 是（安装）或声明豁免 | 报告 |
| B10 | Windows 门禁本身 | 结构性 | 需扩 CI | M6 |

---

### 3.2 分流结论（来自 `m1-inheritance-triage.md`）

上述 B1–B10 已逐条对照 M1 爆破半径重新分流。**唯一改变判定的是 B5**（见上，降级为
测试缺陷，不阻断任何里程碑）。其余判定不变。分流计数（Go 59 个失败包）：

| 类别 | 包数（按主导原因） | 代表 |
| --- | --- | --- |
| (a) 产品缺陷 · M1 路径内 | 1 | `internal/domain` handoff（函数在 M1 包内，路径属 M2） |
| (b) 产品缺陷 · M1 路径外 | 4 | B1、B2、B8、`service/project` clone |
| (c) 测试缺陷 | 6 簇 | B5、FS 错误码、tmux 负向、CRLF、`HOME`、句柄清理 |
| (d) 环境前提 | 3 | B3、B6、B9（B7 归 M6） |
| (e) 未知 | 2 | ConPTY 栅格、`service/project` 合法本地路径 |

**M1 依赖的核心包在 Windows 上全绿**：`internal/storage/sqlite`（含 `/store`、`/sqlitetest`）、
`internal/cdc`、`internal/httpd`、`internal/httpd/apispec`。红只出现在
`internal/domain`（2 个既有 handoff 测试）与 `internal/httpd/controllers`（5 条测试/环境产物）。
→ **M1 可立即开工**，详见 `m1-inheritance-triage.md` §5–§7。

---

## 4. M1 输入清单

### 4.1 M1 可以立即开始，不需要等预算

设计 §13 把 M1 定义为「DB migration、domain、DAG validator、CRUD、API、CDC」，验收是
「复杂 DAG 无环验证、事务领取、幂等与重启恢复单元测试通过」。这些**全部是纯本地逻辑**：

| M1 可立即做 | 为什么不需要 provider | 参考 |
| --- | --- | --- |
| migration + sqlc | 纯 schema | `AGENTS.md` 的 migration 规则 |
| domain 对象（Workflow/Task/Attempt/Approval） | 纯类型 | 设计 §2 |
| DAG validator（无环、缺失依赖、跨 Workflow、重复 ID、过大图拒绝） | 纯图算法 | 设计 §4、§13.1 第 1 条 |
| CRUD + API + DTO | 不触达执行器 | 设计 §10 |
| CDC（`change_log` → SSE） | 既有机制 | 设计 §9.2 |
| 事务领取 / 幂等 / 重启恢复 | 用 fake store，不需真实 agent | 设计 §13.1 第 2 条 |

**结论：M1 不需要任何 provider turn。** 唯一的外部依赖是既有的 `go test` 可跑（已满足：
`go build`/`go vet` PASS，`build-go.txt`）。

### 4.2 M1 必须**不**假设的事

1. **不得**在 domain 里硬编码任何 agent 能力。能力一律来自运行时 Probe。
   反例证据：OMP 的 model 目录 58 项**全无 effort**，而 Codex 4 项全带 effort
   （`a01-probe.md` §3）——任何静态表都会错。
2. **不得**假设 "steer 存在"。ACP 三家的 steer 取决于 `initialize` 的 `_meta`
   （`acp/driver.go:943-946`），M0 无 session，故三者真值仍为 **UNKNOWN**
   （`static-survey.md` §6 矩阵）。
3. **不得**假设 resume 语义统一（`session/load` 重放 vs `session/resume` 不重放）。
4. **不得**假设成本可精确。四家 driver 中**没有任何一个**给 `Cost` 赋值
   （`static-survey.md` §5.3），AO 显示的金额一律是 `estimated`（`cost_backfiller.go:29`）。
5. **不得**假设预算能在执行器内部精确截断——设计 §12 明确"不能承诺"。
6. **不得**把 `steady` 的 `ChatCapability*` 缓存当作编排输入：
   Probe 只缓存成功且按 daemon 生命周期（`service/chat/service.go:2404-2420`）。

### 4.3 M1/M2 必须在 domain 里固化的能力契约

建议形态（字段名示意，具体以 M1 实现为准）：

```
HarnessCapability (运行时探测，永不硬编码)
  harnessID              // domain/harness.go 的既有 id，如 deepseek-harness
  probedAt / probeSource // cli | acp | static，用于判断可信度
  steer:      {supported bool, source: capability|unknown}
  queue:      {supported bool}        // AO 恒 true（controller 层，见 4.4）
  interrupt:  {supported bool}
  resume:     {supported bool, mode: load|resume|none}   // 关键：区分语义
  history:    {supported bool}        // 依赖 loadSession
  fork:       {supported bool, turnAnchored bool}
  approval:   {modes []string, switchable bool, applyAt: launch|turn|request}
  model:      {enumerable bool, effort: exact|none|unknown}
  usage:      {tokens: exact|estimated|unknown, cost: estimated|unknown}
```

`applyAt` 就是为了表达 A3（OMP = `launch`）。`resume.mode` 是为了表达 A2。

### 4.4 证据直接支持的四个不变量

| 不变量 | 证据 | 落地要求 |
| --- | --- | --- |
| **Queue 与 provider 无关** | `service/chat/controller.go:1502-1522`：忙时持久化排队、`NextQueuedTurn` 取首、只派发一轮 | 编排层可无条件依赖 Queue；A-05 对四家可用同一套用例 |
| **Steer 必须能力门禁** | `service/chat/steer.go:210-213` 双门禁；`acp/steer.go:84-93` 对 `startedNewTurn` 报错 | 编排层不得以"支持 steer"为默认；不支持时降级为 Queue 并**如实标注** |
| **Handoff 需要历史重放** | `session_manager/interface_transition.go:695-706,813`；OMP/DSH 无 `AgentInterfaceHandoff` 故被拒 | TUI→Chat 必须 fail-closed；`RECOVERY_UNCERTAIN` 而非猜测 |
| **成本只能是 estimated/unknown** | 无 `Cost:` 赋值（`static-survey.md` §5.3）；`ChatUsage.Cost` 为指针，nil = 未报（`ports/chat.go:636-640`） | 任何金额展示必须带"估算"与价格来源；缺失显示 unknown，不得补零 |
| **预算只能门禁新派发** | 设计 §12「不能承诺能在不支持取消的执行器内部精确截断开销」 | 硬限额实现为"拒绝新领取 + 人工决策"，不做运行中截断 |

---

## 5. 降级策略落地表

把设计 §4.3 的五条降级映射到**实际承载它的字段/标志**，使 M2 不需要重新推导。

| 设计 §4.3 降级 | 承载字段 / 标志 | 代码锚点 | M2 需做什么 |
| --- | --- | --- | --- |
| 无 native Steer → 隐藏/禁用实时 Steer，推荐 Queue；**不得 stop+send 伪称 Steer** | `ChatCapabilitySteer` + `ports.ChatSteerer` 断言；`SteerOrSend` 的降级路径；`ErrChatNoSteerableTurn` / `"ACP steering started a detached turn"` | `service/chat/steer.go:160-212`；`acp/steer.go:84-93`；`ports/chat.go:175` | domain 增加 `steer.available`，UI 依此隐藏输入框而非发送后报错 |
| Chat 不可用但 TUI 可用 → 标 TUI-only；不从终端字节伪造结构化 Chat 事件 | `AgentInterfaceHandoff` 缺失即为"无 Chat 交接"；TUI 仅 `AgentInterfaceHandoffHistoryProbe` | `session_manager/interface_transition.go:695-706`；`agent/omp/omp.go:44` | capability 增加 `uiSurface: chat|tui|both`，并禁止由终端字节生成 Chat 事件 |
| 认证未完成 → `BLOCKED_AUTH`，提供自助登录入口，不抓密钥 | `authentication.state` ∈ `authorized|configured|unknown`；`reasonCode` 如 `auth_configured_unverified`、`auth_check_inconclusive`、`account_store_unavailable`；`ErrAgentAuthRequired` | `_api_agents_readiness.json`；`claudeacp/driver.go:195-211` | 把 reasonCode 映射为 UI 文案；**不得**把 `configured` 当 `authorized` |
| Provider 无准确 Token → unknown/estimated；预算只约束新派发 | `ChatUsage.TotalsKnown` / `ContextKnown`；`Cost *float64`（nil=未报）；`estimated` 来自 `CostBackfiller` | `ports/chat.go:623-640`；`service/usage/cost_backfiller.go:29` | 账本记 `quality` 三态；硬限额只挡新领取（设计 §12） |
| 恢复无原生证据 → `RECOVERY_UNCERTAIN`，暂停自动重放，人工选择 | `ChatCapabilityResume` 运行时降级 + `ErrChatResumeFailed`；交接门 fail-closed | `acp/driver.go:936-939`；`interface_transition.go:695-706` | attempt 状态需有 `RECOVERY_UNCERTAIN`，禁止自动 replay |
| 权限模式无法被 provider 强制 → 不启用危险写操作自动审批 | `applyAt`/`switchable`（本文件 §4.3）；`ErrACPSetterUnsupported`；`permissionsEnforced`（设计 §6.1） | `ompacp/driver.go:64-81`；设计 §6.1 | 审批记录需带 `permissionsEnforced`，未强制时强制人工确认 |

补充：设计 §8.1 已明确「Git Worktree 不构成安全沙箱」，因此"权限模式"在四家都只能
**降低信任**而非**保证隔离**；Codex 有 `workspace-write` sandbox，其余三家只有 permission mode
（`static-survey.md` §6 最后一行 `Worktree 读写边界`：Claude/OMP/DSH 均为 STATIC-UNKNOWN）。

---

## 6. 仍然未知

以下问题 M0 **无法**在不消耗 provider turn / 不接触凭证的前提下回答。
每条都写成对应 A 用例能回答的**确切问题**，以便 M1/M2 按需消费。

| # | 未知 | 由哪个用例回答 | 需要什么前提 |
| --- | --- | --- | --- |
| U1 | ACP 三家**是否真的**广告 `_meta.steering.supported`？ | A-06 | 一次 session 握手（`initialize`），无需 prompt 即可读 capability |
| U2 | `session/load` 还是 `session/resume`？各自被广告吗？ | A-03 / A-08 | 一次 session + 一次重开 |
| U3 | DeepSeek 的 `reasoning_effort` 实际可选值集合？ | A-11 | session 建立后读 `config-options`（A-01 已证明模型目录可免费取，effort 不行） |
| U4 | OMP 的 10s auth 超时（3037 记录 `duration_ms=12962/14224`）可复现吗？ | A-01 重跑 | 在 `agent.db` 被活跃 OMP 进程写入时并发探测；本次 10 次均为 96–293 ms，未复现 |
| U5 | 四家 usage 的**可信度**：`resp.Usage` 是否真的填充？ | A-12 | 一次受控 token 限额的短轮次 |
| U6 | Queue 的端到端语义：queued → 最终只派发一次、顺序明确？ | A-05 | 一次 running 轮次 + 第二条消息 |
| U7 | Steer 的"已接受"与"已执行"在 UI 上如何区分？ | A-06 | 一次 running turn + 插入指令 |
| U8 | Stop 是否只影响本 session？最终状态回执是否可信？ | A-07 | 一次安全长任务 + cancel |
| U9 | 会话恢复后历史是否重复/断裂？turn/message ID 是否稳定？ | A-03 / A-08 | 重开同一 session |
| U10 | 审批拒绝后是否真的没有副作用？ | A-10 | 一次"询问权限"的受控操作 |
| U11 | Worktree 读写边界：agent 能否越出分配目录？ | A-09 | 在空白测试 worktree 写文件；**不得**对真实目录做破坏性验证 |
| U12 | Windows 进程退出清理是否完整（含 pty-host 孤儿，B8）？ | A-15 / W-10 | 启停 daemon + 枚举进程树 |
| U13 | ConPTY 栅格是否真能工作（B4）？ | W-06 | 单独复跑 `TestConPTYStartsAtRequestedGrid` + 真实终端 |
| U14 | Codex 多账号修复后 `account_storage_unsafe` 是否消失（B1）？ | A-01 重跑 | 需先改码 |
| U15 | Claude 的 gateway `127.0.0.1:15721` 由谁提供？ | A-02 前置 | 用户启动该 gateway，或从 `~/.claude/settings.json` 移除 `ANTHROPIC_BASE_URL` |

**关于 U15 的安全性**：`a01-probe.md` §2.3 记录 AO **只读取存在性与长度，未读取或打印值**。
M1/M2 若要在 UI 暴露该诊断，必须沿用同一约束：**只报 reasonCode，不报 URL 之外的凭据信息**。

---

## 7. 一页结论（M1 应据此行动）

1. **协议不是四份，是两份**：Codex 自研 app-server，其余三家共用 ACP transport。
   ACP 侧修一次三家受益；Codex 侧必须单独处理。
2. **任何能力表都不能硬编码**：steer、resume 语义、effort、审批切换时机四者在四家全不一致，
   且都只能靠运行时 Probe。M1 的 domain 只能定义"契约形状"，不能填"取值"。
3. **Queue 是唯一可以无条件依赖的原语**（controller 层，provider 无关）；
   Steer 必须能力门禁，不支持就如实降级，绝不把 stop+send 叫成 Steer。
4. **成本永远是 estimated/unknown**：四个 driver 没有一个报 `Cost`，
   预算只能门禁新派发，不能承诺执行器内部精确截断。
5. **两个产品缺陷必须在 M1/M2 修**：B1（标准 Windows 上 Codex 账号存储永久锁存）
   与 B2（handoff 裁剪丢掉最新一轮）。二者都不阻断 M1 开工，但各自阻断一条 M1/M2 能力声明。
