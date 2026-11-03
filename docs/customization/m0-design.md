# M0 详细设计：Windows 基座与四种编码 Agent 能力验证

> 文档版本：V0.1，2026-11-03
> 状态：**设计已完成；M0-01～M0-04 的无预算验证工作已执行，子项包含 PARTIAL / NOT_TESTED / BLOCKED；M0-05 预算暂未批准，后续执行。** 结果见 [m0-report.md](m0-report.md)，能力矩阵见 [m0-capability-matrix.md](m0-capability-matrix.md)，差距单见 [m0-adapter-gaps.md](m0-adapter-gaps.md)。
> 当前判定：**M0 正式验收 BLOCKED；M1 可按用户授权的零 Provider 范围先行**（见 m0-report.md §8.4 与 [m1-plan.md](m1-plan.md)）。
> 工作区：`E:\code\agent-orchestrator`；开发分支：`feat/multi-agent-desktop`。
> 上游基线：`23840aa54104`；对应总体设计 `design.md` 第 13—15 节，需求 `requirements.md` Q1—Q10。
> 范围：M0-01（环境、现有桌面应用和 Codex CLI / Claude Code CLI / Oh My Pi / DeepSeek Harness 的真实能力验证）。

## 1. 目标、成果和退出门槛

### 1.1 目标

M0 不开发新的 DAG / Workflow 业务功能。它的作用是确定后续 M1—M6 可以依赖哪些**真实可用**的执行器、Chat 协议、会话生命周期、Windows 本地能力与工具链，尤其不要把“仓库里有 Adapter”误认为“本机能运行且支持全部交互方式”。

M0 要得到五类结论：

1. 仓库现有 Go Daemon 与 Electron/React 在 Windows 隔离工作区中可构建、可启动、可连接。
2. 四种 CLI 的可执行文件、版本、身份状态、实际协议和能力边界均有可复现证据。
3. 区分原生 Steer / Queue / Stop / Resume；不能把“中断后重新发送”描述成 Steer。
4. 可在独立、无敏感数据的 Git 测试仓库创建、运行、保留/恢复 Worktree；不会误动真实开发项目。
5. 明确第一版 Adapter Capability 映射、Windows 权限限制、未支持项与安全降级策略。

### 1.2 退出条件

| 代号 | 退出门槛 | 必须提供的证据 |
| --- | --- | --- |
| G0-01 | 记录工具链版本与基座 commit；确认环境隔离 | `env-baseline`、Git 信息、路径快照 |
| G0-02 | Go/前端基线检查完成且失败原因可复现 | 编译、测试、类型检查退出码及脱敏日志 |
| G0-03 | 隔离数据目录下 Daemon/桌面端联通 | 进程/端口/健康检查、UI 可见会话状态 |
| G0-04 | 四 Agent 均有能力矩阵结论 | 每项 PASS/FAIL/UNSUPPORTED/BLOCKED/NOT_TESTED + 证据 |
| G0-05 | 关键交互、恢复、安全用例有验证记录 | Chat/Queue/Steer/Stop/Resume 和权限/清理用例 |
| G0-06 | 产出 Adapter 差距单及 M1 输入清单 | 协议差异、兼容方案、阻断清单 |

**判定**：全部关键门槛 PASS 方可无条件进入 M1；部分执行器缺失时可以提出“有限范围进入 M1”的评审建议，但不能伪称首发四 Agent 已验证齐全。`BLOCKED` 不等于通过。

## 2. M0 边界、安全规则和先决条件

### 2.1 执行范围

- Windows 本地；只验证**当前仓库已有实现**，不新增 Workflow 业务代码。
- 允许在明确创建的临时 Git Worktree、临时 AO 数据目录中安装依赖、编译、启动测试进程，**前提是进入执行阶段且用户已批准会修改磁盘/消耗配额的步骤**。
- 任何涉及实际模型调用的用例可能消耗 token/API 配额：先在 M0 报告中登记模型与预算上限；没有预算许可只做静态/离线探测。
- 不执行 Git Push、不创建真实远程 PR、不碰生产仓库、不执行提权脚本，不改 Windows 全局 PATH、代理、注册表或系统服务。
- 不访问/改写 `.codex/`、`.claude/`、其他 CLI 的个人认证文件；不得将 Token、Cookie、密钥、完整用户数据或账户信息写进可提交的日志。
- **Worktree 不是 OS 沙箱**：对于未经证实能隔离权限的 CLI，只在无敏感数据的测试用户/测试虚拟机内运行破坏性/逃逸相关测试；不得直接对宿主敏感目录执行“验证性删除”。

### 2.2 Windows 数据目录

开发运行前，另建一个完全独立的测试目录（示例，**这里只是计划，不在编写文档时执行**）：

| 用途 | 建议测试路径 |
| --- | --- |
| 仓库试验 Worktree | `E:\code\ao-m0-lab`（从当前 commit 新建） |
| AO Daemon 数据 | `E:\code\ao-m0-scratch\data` |
| AO Run 文件 | `E:\code\ao-m0-scratch\run\running.json` |
| Electron Dev Profile | `E:\code\ao-m0-scratch\electron` |
| 允许写入的测试仓库 | `E:\code\ao-m0-scratch\sample-repos` |
| 脱敏测试证据 | `E:\code\ao-m0-scratch\evidence` |

执行进程使用显式环境变量：`AO_DATA_DIR`、`AO_RUN_FILE`、`AO_DEV_ELECTRON_DIR`；变量是否适用于每种启动方式需要核验 Electron/Daemon 启动代码。Windows 下 `AO_RUN_FILE` 的父目录先创建。对 Dev Electron 需使用独立 Profile，避免与已安装 AO 或其他开发实例共享 Chromium 锁。主进程和 Go Daemon 的实际环境都应从启动日志核对，不只看变量是否在 shell 中存在。

不清理用户已有 `~\.ao` 目录；清理本次试验路径必须核对完整路径、Git dirty 状态、运行进程归属，得到批准后才操作。

## 3. 环境基线与工具链准备（M0-01）

### 3.1 当前已知环境（**历史探测事实，不是 M0 实测通过**）

| 项 | 当前已知 | M0 预期 |
| --- | --- | --- |
| OS | Windows 64-bit | 记录版本、PowerShell 版本、账户权限类型 |
| Git | 2.45.1.windows.1 | 记录 `git --version` 与 Worktree 支持 |
| Node.js | `v22.20.0` | 用隔离方式准备 Node 24，重新验证 lockfile 兼容性 |
| npm | `10.9.3` | 记录真实版本 |
| Go | 本机启动曾报告 1.26.3；`backend/go.mod` 声明 1.27.1 | 检查实际 `GOTOOLCHAIN`、可用 Go 1.27.1，避免静默下载 |
| 桌面端 | Electron + React | 确保打包依赖和 native 模块支持 Windows |
| Git HEAD | 上轮设计提交 `47d594c` | 执行时重新记录 commit 与 dirty 状态 |

**工具链隔离顺序**：先只读探测；若缺版本，优先使用已有版本管理器或非全局目录下的独立工具链；在独立试验 Worktree 执行依赖安装。升级系统工具必须另行批准。

### 3.2 只读探测命令（执行阶段使用）

```powershell
# 由 PowerShell -NoProfile 运行；这些命令本身不安装依赖。
git rev-parse HEAD
git status --short
git --version
node --version
npm --version
$env:GOTOOLCHAIN = "local" # 防止版本探测自动拉取 Go 1.27.1
go version
go env GOVERSION GOTOOLCHAIN GOOS GOARCH
Get-Command codex,claude,omp,dsh -ErrorAction SilentlyContinue |
  Select-Object Name,Source,CommandType
```

后续版本探测（仅对查到的 CLI 做，不要假设所有 CLI 都已安装）：

```powershell
codex --version
claude --version
omp --version
dsh --version
```

注意：`--version` 也可能被第三方 shim 包装，需核对可执行文件真实路径与返回状态。认证状态采用官方 CLI 的非泄密只读诊断；若会触发登录、修改配置或打印凭据，则**跳过并标为待用户验证**。

### 3.3 依赖安装及构建检查（执行阶段，经确认后运行）

为遵守根目录 `AGENTS.md`，在新建、独立的实验 Worktree 中进行构建。以执行时的当前 HEAD 为准，**不得在用户真实生产工作树中运行安装或清理脚本**。推荐检查顺序：

1. 在测试路径新建 Git Worktree，记录试验 SHA 与主工作区 SHA；不要重置已有工作树。
2. 安装/选用适配的 Node 24 和 Go 1.27.1 工具链，记录来源、校验和与 PATH，不覆盖用户默认 CLI 配置。
3. 先核对根目录和 `frontend/package.json` / lockfile 的安装入口。按仓库已知规则，`frontend` 目录执行 `npm ci`，不能把其他 Worktree 的 `node_modules` 链接过来。
4. Go：`go build ./...`、`go test ./...`、`go vet ./...`；`go test -race ./...` 仅在 Windows 当前工具链和 C compiler 支持时运行，否则明确标 BLOCKED 而非 PASS。
5. 前端：在 `frontend` 下运行 `npm run typecheck` 与 `npm run build:daemon -- --dev`，根据依赖情况运行 `npm run test`；从仓库根目录按脚本检查 `npm run frontend:typecheck`。
6. 按 `AGENTS.md` 约束启动真实 Dev Electron，观察 log、窗口内容、Daemon 健康、SSE 和会话查看；与真正的打包安装验证区分开。
7. 对每一条命令记录命令工作目录、环境变量（脱敏）、开始结束时间、退出码、日志位置、失败分类。

可复现实验清单应放进 M0 报告；文档中的命令**不是本次已执行记录**。

## 4. 四个 Agent 的接入基线

当前源码已有注册，不重复发明新的 Agent 主循环：

| Agent | 代码位置 | Chat 实现 | 预期启动方式（以源码为准） | 特殊注意 |
| --- | --- | --- | --- | --- |
| Codex CLI | `backend/internal/adapters/agent/codex/` | `chatdriver/codexappserver/` | 现有 Codex native app-server | Steer 要绑定当前 Provider Turn ID |
| Claude Code CLI | `backend/internal/adapters/agent/claudecode/` | `chatdriver/claudeacp/` | 现有 Claude + ACP bridge | 会话模式切换、认证和审批依版本探测 |
| Oh My Pi（OMP） | `backend/internal/adapters/agent/omp/` | `chatdriver/ompacp/driver.go` | `omp acp` | ACP 版本/运行时审批模式，调整权限可能需重启 |
| DeepSeek Harness | `backend/internal/adapters/agent/deepseekharness/` | `chatdriver/deepseekharnessacp/driver.go` | `dsh --profile acp` | 模型选项为 Harness 专属值；effort 要映射，不直接透传泛用字段 |

四者在 `backend/internal/adapters/agent/registry/registry.go` 和 `backend/internal/adapters/chatdriver/registry/registry.go` 注册；“源码包含驱动”只能证明**支持该适配路线**，不证明本机 CLI 安装、认证或 ACP 协议验证通过。

### 4.1 统一能力矩阵（M0 报告必须填写）

所有项目默认 `NOT_TESTED`。每个单元格最终写 `PASS`、`FAIL`、`UNSUPPORTED`、`BLOCKED` 或 `NOT_TESTED`，并附一条证据 ID。严禁静态推测填 `PASS`。

| 验证维度 | Codex | Claude Code | OMP | DeepSeek Harness |
| --- | --- | --- | --- | --- |
| CLI 路径/版本可信 | NOT_TESTED | NOT_TESTED | NOT_TESTED | NOT_TESTED |
| 身份状态诊断 | NOT_TESTED | NOT_TESTED | NOT_TESTED | NOT_TESTED |
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

**结果含义**：PASS = 真实执行并有证据；FAIL = 应支持但验证失败；UNSUPPORTED = 驱动明确不提供且已经确认；BLOCKED = 因环境/认证/额度而无法验证；NOT_TESTED = 尚未执行。对于使用量，区分 `exact`、`estimated`、`unknown`。

### 4.2 Agent 单项用例

每个 Agent 在独立 Session 中执行同一批最小用例，防止彼此污染：

| ID | 用例 | 操作与观测 | 通过标准 |
| --- | --- | --- | --- |
| A-01 | 预检 | 检查 binary/版本与 Chat driver Probe | 正确识别版本、认证问题、协议不兼容和可用能力 |
| A-02 | Chat 基本轮次 | 在空的只读测试仓库发送短问答 | 返回结构化消息与终态、无空白会话 |
| A-03 | 流式与历史 | 观察事件，重开同一 Session | turn/message ID 稳定，历史无重复/断裂 |
| A-04 | TUI 基本运行 | 启动终端会话、查看状态并停止 | 仅在支持时标 PASS；终端退出状态可追踪 |
| A-05 | Queue | 运行中的 Agent 收到第二条排队消息 | 先 queued，最终只派发一次，顺序明确 |
| A-06 | Steer | 在确定的 running turn 发送补充指令 | Provider 确认插入同一 turn；不支持则明确 UNSUPPORTED/排队 |
| A-07 | Stop | 在安全任务运行时发出 cancel | 不会误停止其他 Session；留下停止回执与实际最终状态 |
| A-08 | Resume | 关闭 UI、替换/重启测试 Daemon 后附着 | 实际恢复已有会话；不伪造上下文继承 |
| A-09 | Git 改动 | 只在空白测试 Worktree 写小文件 | Diff 仅在批准目录，Git 原仓库状态不变 |
| A-10 | 审批拒绝 | 使用安全的受控“询问权限”测试操作 | 拒绝后没有产生被禁止的副作用 |
| A-11 | 模型参数 | 用两个可用模型/effort 做只读会话配置 | Unsupported 字段在本地或 provider 明确拒绝，不隐式忽略 |
| A-12 | 用量 | 在受控 token 限额内完成一个短轮次 | Token 数据可信度有定义，缺失明确 unknown |
| A-13 | 故障 | 断开测试 UI/网络或终止测试进程（按权限） | 恢复、暂停或 UNKNOWN 状态可辨，不重复交付 |
| A-14 | 工作区保留 | 取消任务后检查 Worktree 的 Git 状态 | 已生成成果仍可审查，未被强制删除 |
| A-15 | 受控清理 | 退出测试 Session / Daemon | 只清理归属明确的测试进程与测试目录，不遗留监听 |

备注：`Stop & Replan` 的**工作流重规划**属于 M2；M0 只证明当前底层 Stop、Session 结算和 Worktree 保留能否支撑该设计。

### 4.3 无法支持时的降级与结论

- 无 native Steer：隐藏或禁用实时 Steer，推荐 Queue；**不得将 stop+send 伪称 Steer**。
- Chat 不可用但 TUI 可用：标明 TUI-only，使用已有终端运行时；不从终端字节假造结构化 Chat 事件。
- 认证未完成：标 `BLOCKED_AUTH`，提供用户自行登录入口；不抓取密钥。
- Provider 不提供准确 Token：标 unknown/estimated；预算硬限制只约束新派发，不对不支持的模型声称精准截断。
- 恢复无明确原生会话证据：展示 `RECOVERY_UNCERTAIN`，暂停自动重放并允许人工选择。
- 权限模式无法被 provider 强制：不启用危险写操作的自动审批路径。

## 5. Windows 基座端到端验证

| ID | 场景 | 预期 |
| --- | --- | --- |
| W-01 | 独立 Daemon 以 loopback 运行 | 仅在本机 `127.0.0.1` 提供主服务；健康检查及 API 响应合理 |
| W-02 | Electron 连接测试 Daemon | 启动时看到空项目/测试数据，避免加载真实用户数据库 |
| W-03 | 新增测试 Git Project | 通过 UI/API 注册测试仓库并能创建 Worker Session |
| W-04 | 多仓库 Workspace | 为两个样板仓库建立会话 Worktree，路径/Base SHA 一一对应 |
| W-05 | SSE 订阅与 UI 重连 | 客户端断线后状态与历史正确，不依赖前端猜测终态 |
| W-06 | Terminal/ConPTY | Windows 终端可打开、输入/输出和退出正确；不会崩溃留空窗 |
| W-07 | Chat Controller / Host | UI 关闭与重开保持原生 Session 状态（仅在驱动有此能力时） |
| W-08 | Review / Git 观察 | 本地 Diff 可检视；没有联网权限也能明确区分 PR/CI 不可用 |
| W-09 | 异常停止 | 在测试数据内模拟有限异常，验证保留 dirty Worktree、审批和重试边界 |
| W-10 | 清理与退出 | 不会关闭用户自己的 CLI 会话、Daemon 或其他 Electron 实例 |

端到端测试必须使用小型、可删除的本地样板仓库。禁止以真实业务仓库直接验证危险命令或数据清理逻辑。

## 6. 执行步骤与依赖关系

| 步骤 | 输入 | 操作 | 交付物 | 前置条件 |
| --- | --- | --- | --- | --- |
| M0-01 | 当前仓库 | 记录基线、工具版本、是否 dirty | 环境基线清单 | 无 |
| M0-02 | 隔离路径 | 建立试验 Worktree、AO 数据目录、样板仓库 | 试验环境清单 | M0-01 |
| M0-03 | Node 24 / Go 1.27.1 | 安装依赖、Go build/test、前端 typecheck/build | 构建与测试报告 | M0-02 + 操作批准 |
| M0-04 | 独立测试 Daemon/桌面端 | 验证 W-01～W-10 | 基座联通性报告 | M0-03 |
| M0-05 | 四个 CLI | 分别执行 A-01～A-15，填写矩阵 | 四 Agent 能力矩阵 | M0-04 + 额度批准 |
| M0-06 | 全部证据 | 问题归因、降级策略、M1 准入评审 | M0 验收报告与差距清单 | M0-05 |

**中断规则**：发现工具链不兼容、真实 AO 数据路径被读取、构建试图覆盖个人配置、CLI 打印凭证、测试请求危险操作等情况，立即停止该测试并记录 BLOCKED；不得为追求完成率绕过安全边界。

## 7. 证据与报告结构（执行阶段产出）

建议只提交经过脱敏的 Markdown 摘要，不将原始运行日志、凭证或个人账户数据提交 Git。

```text
E:\code\ao-m0-scratch\evidence\          # 原始、本地保存、不纳入 Git
  env-baseline.txt
  build-go.txt
  build-frontend.txt
  daemon-health.txt
  electron-smoke.txt
  agents\
    codex.txt
    claude-code.txt
    omp.txt
    deepseek-harness.txt

E:\code\agent-orchestrator\docs\customization\
  m0-design.md                           # 本设计文档
  m0-report.md                           # M0 真正执行后再创建
  m0-capability-matrix.md                # M0 真正执行后再创建
```

每个测试记录建议统一为：

```text
test_id:
agent / adapter:
os / version / binary_path:
git_commit / session_id (仅测试数据):
command_summary:                # 脱敏，不提交原始密钥
expected:
observed:
result: PASS | FAIL | UNSUPPORTED | BLOCKED | NOT_TESTED
evidence_ref:
failure_category:
fallback:
owner:
next_action:
```

`m0-report.md` 应包含执行时间、工作区 SHA、命令清单与退出码、矩阵概览、复现步骤、阻断项和决定进入 M1 的审批记录；不能只有“成功/失败”两个字。

## 8. 验收规则

### 8.1 强制通过项（首版基础）

- **基础门禁**：工具链、编译、基本测试和独立 Daemon/Electron 联通 PASS。
- **四 Agent 就绪**：四个适配器均至少完成 A-01 / A-02 / A-03 / A-07 / A-08 的验证，未支持的功能有正确能力标记和明确降级。若某个 CLI 缺失/无认证，则说明首版目标未完全达标，不能对外声称全量通过。
- **本地安全**：测试期间未修改真实个人配置和已安装 AO 的数据；测试 Worktree 可追溯，不遗失 dirty 成果。
- **代码证据**：测试记录可复现，所有失败项都有原因或阻断说明；无泄密提交。
- **协议诚实**：Steer/Queue/Stop/Resume 和模型参数差异来自实测或驱动明确声明，不从界面文案推断。

### 8.2 允许评审后降级的项目

- 某些执行器不支持原生 Steer：可采取 Queue 降级，只要 UI 如实标记。
- 原生 TUI 与 Chat 之间无法无损切换：保持单模式或要求显式确认中断；不得假称上下文无损。
- Token/成本不可观测：显示 unknown/estimated，禁用无法保证的硬预算预测。
- 远程 Worker：M0 不测试真实远程执行；只检查后续接口规划完整即可。

### 8.3 M0 完成定义与 M1 限定授权

由用户确认 `m0-report.md` 的结果分级：`PASS`（全部强制项）、`CONDITIONAL`（记录降级且明确批准）、`BLOCKED`（基础或安全门槛未过）。**M0 正式验收只有在 PASS 或用户明确批准的 CONDITIONAL 下才能关闭。**

若 M0 仍为 BLOCKED，用户可以在完成 M1 继承性分流后，单独授权一个明确限定的 M1 工作范围；该授权不改变 M0 状态，也不能被描述为 M0 已通过。当前授权仅覆盖 [m1-plan.md](m1-plan.md) 中 M1-01～M1-05 的本地后端核心，严格禁止真实 Agent/Provider 调用，M0-05 预算仍暂缓。后续如需扩大到 M2～M6，必须重新确认范围与安全边界。

所有结论应带具体证据/日期/版本，不能自动用设计假设替代验证结果。

## 9. 风险、待决策项与下一步

| 风险 | 影响 | 处理 |
| --- | --- | --- |
| 工具链版本与获取方式 | Go 本地安装为 1.26.3，仓库要求 1.27.1；本机通过 GOTOOLCHAIN=auto 构建，但是否接受隐式工具链仍待确认。Node 22.20.0 已实测可完成 install/typecheck/build，仓库未声明 Node 主版本 | 不改全局安装；按 m0-report.md §8.3 留待用户确认 Go 工具链策略 |
| CLI 未安装、登录状态未知 | 不能运行真实 Chat | 分类 BLOCKED；用户自行完成登录后重试 |
| OMP / DeepSeek Harness 的 ACP 版本或特定模型协议有差异 | 模型/effort/审批不一致 | 使用各 Adapter 的探测和字段映射；最小样本验证 |
| Windows shell 进程/终端行为不同 | 进程残留或启动失败 | 测试 ConPTY、host adopt、进程树与退出清理 |
| Worktree 不是真隔离 | 外部路径操作风险 | 使用低权限试验环境与无敏感数据仓库，危险用例禁真删 |
| Provider API 限流或费用 | 测试不完整、超额 | 先设置明确预算，再按最小轮次采样 |
| 旧 Daemon / Electron 实例共用 Profile | 会话污染/锁冲突 | 检查 AO_DATA_DIR、AO_RUN_FILE、AO_DEV_ELECTRON_DIR 及 PID |

**已执行**：M0-01～M0-04 中已批准的无预算验证工作，包括环境/隔离、Go 与前端构建、独立 Daemon、Dev Electron、W-01/W-02/W-05/W-06/W-09/W-10 的部分场景，以及 A-01 四 Agent 预检（0 provider turn）；各子项结论以 [m0-report.md](m0-report.md) 为准。

**暂缓**：M0-05 预算尚未批准，后续执行；A-02～A-15 未执行，W-07 未测试，W-03/W-04/W-08 的 Session-scoped 场景未完成。Windows 的 `go test ./...` 与 `npm test` 基线非绿，前端仍有 18 个文件 / 21 个测试待分诊。

**下一项开发任务**：按 [m1-plan.md](m1-plan.md) 从 **M1-01 数据库迁移与领域模型** 开始，严格保持零 Provider 边界；M0-05 只有在用户后续明确批准预算后再执行。**本设计文件本身不代表任何 M1 代码已实现；验收结论以 M0/M1 各自报告为准。**
