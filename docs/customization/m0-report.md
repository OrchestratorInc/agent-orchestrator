# M0 阶段执行报告（M0-05 预算暂缓，正式验收未完成）

> **本文件记录截至 2026-11-03 的实测结果，不构成 M0 正式验收通过声明。**
> 已执行：M0-01～M0-04 范围内已批准的无预算工作（环境与隔离、构建与测试、独立 Daemon/Electron、W 系列部分用例、A-01 四 Agent 预检）；各子项状态以本报告表格为准，包含 `PARTIAL`、`NOT_TESTED` 与 `BLOCKED`，不把里程碑名称等同于全部子项通过。
> **M0-05：预算暂未批准，后续执行。** A-02～A-15 均未执行；W-03/W-04/W-08 的 Session-scoped 部分受真实 Agent 启动前提阻断，W-07 仍为 `NOT_TESTED`。
> **已消耗 provider turn = 0，token = 0**（§6）；本次文档收尾不会触发 Provider 调用。
> 未实测项仍标注「未执行」或 `NOT_TESTED`；不得由设计假设代替验证结果。
> **M0 正式验收结论仍为 BLOCKED**（§8.1）；M1 仅可按 §8.4 记录的用户授权范围先行，不代表 M0 转为 `PASS` 或 `CONDITIONAL`。

---

## 1. 执行元数据

| 项 | 值 | 来源 |
| --- | --- | --- |
| 报告编写时间 | 2026-11-03（本地，UTC+08:00） | 本次 |
| 工作区 | `E:\code\agent-orchestrator` | — |
| 执行基线 SHA | `7ce17e58247cc3e40669e9a29559c69cc18f04ac` | M0 实测时的 `git rev-parse HEAD` |
| 分支 | `feat/multi-agent-desktop` | `git rev-parse --abbrev-ref HEAD` |
| 试验 Worktree | `E:\code\ao-m0-lab`（detached HEAD，同一 SHA，clean） | `git worktree list` |
| Dirty 状态（探测时） | **clean**（`git status --porcelain` 无输出） | 见 `evidence/env-baseline.txt` 第 1 节 |
| Dirty 状态（本次收尾前） | 仅文档变更：`README.md`、`design.md`、`m0-design.md` 已修改；`m0-report.md`、`m0-capability-matrix.md`、`m0-adapter-gaps.md`、`m1-plan.md` 未跟踪；业务源码零改动 | `git status --porcelain` 复核 |
| 执行类型 | 只读探测 + 静态源码调查 + **A-01 运行时预检**（隔离 daemon，0 provider turn）；构建/测试与 W 系列由另一 worker 执行，本文件引用其证据文件 | — |

设计 §3.1 记录的历史 HEAD 为 `47d594c`，实际 HEAD 是 `7ce17e5`（设计文档自身的提交），
`47d594c` 为 `HEAD~1`。该偏差已由 `env-baseline.txt` 记录，本报告沿用实测值。

---

## 2. 环境基线

### 2.1 工具链

| 项 | 实测值 | 命令 | 备注 |
| --- | --- | --- | --- |
| OS | Windows 11，`CurrentBuild 26200`，`DisplayVersion 25H2`，`UBR 9711`，x64 | `Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion'` | 与设计 §3.1「Windows 64-bit」一致，此处补全版本号 |
| 账户权限 | 非管理员（`IsAdmin=False`） | `evidence/env-baseline.txt` | 提权脚本全程未执行 |
| PowerShell | `5.1.26100.9711`，`PSEdition=Desktop` | `$PSVersionTable` | 所有探测均以 `-NoProfile` 运行 |
| Git | `2.45.1.windows.1` | `git --version` | 与设计 §3.1 预期一致；Worktree 已实测可用（`git worktree list` 列出两条） |
| Node.js | `v22.20.0` | `node --version` | 来自 nvm4w 管理目录；**仓库未声明 Node 主版本**（无 `.nvmrc`/`engines`），设计中的「Node 24」不是仓库要求 |
| npm | `10.9.3` | `npm --version` | 与设计 §3.1 预期一致 |
| Go（本地安装） | `go1.26.3 windows/amd64` | `$env:GOTOOLCHAIN='local'; go version` | 安装于 `D:\tool\go\bin\go.exe`，未改动 |
| Go（go.mod 要求） | `go 1.27.1` | `backend/go.mod:3`、根 `go.work` | 两边都要求 1.27.1 |
| GOTOOLCHAIN | `auto` | `go env GOTOOLCHAIN` | `local` 模式在 1.26.3 上直接失败：`go: ..\go.work requires go >= 1.27.1 (running go 1.26.3; GOTOOLCHAIN=local)` |
| 自动工具链缓存 | `%USERPROFILE%\go\pkg\mod\golang.org\toolchain@v0.0.1-go1.27.1.windows-amd64` 已在本地 | `go env GOROOT` | 另有 `toolchain@v0.0.1-go1.26.10.windows-amd64`；1.27.1 已缓存，故构建不需再下载工具链，但该缓存本身是**隐式下载**的产物（设计禁止依赖该机制） |
| C 工具链 | `gcc 16.1.0`（MinGW），`CGO_ENABLED=1` | `go env CC CGO_ENABLED` | `go test -race` 具备前提，但仍需实测 |

### 2.2 隔离路径（设计 §2.2）实际状态

以 `Test-Path` 逐个核对，结果如下（**这就是实际状态，不是计划**）：

| 设计中的用途 | 设计路径 | 实际状态 | 说明 |
| --- | --- | --- | --- |
| 仓库试验 Worktree | `E:\code\ao-m0-lab` | **已创建** | `git worktree add --detach 7ce17e5`，`git status` clean |
| AO Daemon 数据 | `E:\code\ao-m0-scratch\data` | **已创建（空）** | 尚未被任何 Daemon 写入 |
| AO Run 文件目录 | `E:\code\ao-m0-scratch\run` | **已创建（空）** | `running.json` 尚不存在：Daemon 从未启动 |
| Electron Dev Profile | `E:\code\ao-m0-scratch\electron` | **已创建（空）** | 仅在 dev（未打包）运行下有效 |
| 允许写入的测试仓库 | `E:\code\ao-m0-scratch\sample-repos` | **已创建** | 内含 `repo-a`、`repo-b`、`.gitignore`；用于 W-03/W-04/W-08/A-09/A-14 的受控仓库 |
| 脱敏测试证据 | `E:\code\ao-m0-scratch\evidence` | **已创建** | 内含 `env-baseline.txt`、`lab-environment.txt`、`windows-base-survey.md`、`agents/static-survey.md` |
| （额外）运行时取证脚本 | `E:\code\ao-m0-scratch\harness` | **已创建** | `agent-runtime.ps1` + `README.md`，供 A-01～A-08 |

安全性复核：

- `%USERPROFILE%\.ao` **不存在**（`Test-Path` 返回 `False`），说明到此为止没有任何 AO 实例写过真实用户目录。
- 主工作树 `E:\code\agent-orchestrator` 未被构建、未被安装、未被清理脚本触碰；
  本轮收尾前仅存在项目文档改动（详见 §1 Dirty 状态），业务源码零改动。
- 未改全局 PATH / 代理 / 注册表 / 系统服务，未执行 `git push`。

---

## 3. 命令清单

只登记**实际执行过**的命令与退出码。M0-03 的构建/测试已由另一 worker 完成并落盘为
`evidence/build-go.txt` 与 `evidence/build-frontend.txt`；下表退出码**逐条抄自那两个文件的
COMMAND RESULT TABLE**，未做推断。

### 3.1 后端（cwd = `E:\code\ao-m0-lab\backend`，源 `evidence/build-go.txt`）

| 步骤 | 命令 | 退出码 | 判定 | 耗时 |
| --- | --- | --- | --- | --- |
| Go 构建（强制本地工具链） | `GOTOOLCHAIN=local go build ./...` | **1** | BLOCKED（`go.work requires go >= 1.27.1`） | <1s |
| Go 构建 | `go build ./...` | **0** | PASS | 2m32s |
| Go 构建（离线） | `GOPROXY=off go build ./...` | **0** | PASS | 12.8s |
| Go 静态检查 | `go vet ./...` | **0** | PASS | 20.7s |
| Go 测试 | `go test ./...` | **1** | FAIL（152/211 包通过，59 包失败，0 数据竞态） | 13m56s |
| Go 竞态测试 | `go test -race ./...` | **1** | FAIL（同上 + 2 个超时包；数据竞态 0） | 36m32s |

失败归因见 `evidence/baseline-failures.md`：符号链接权限 92、Windows ACL/权限断言 79+、
Windows/POSIX 假设差异；另有少量真实缺陷（aider、HOME/USERPROFILE）。
`build-go.txt` 结论原文：**「Baseline is NOT green on Windows」**，不得声称绿。

### 3.2 前端（cwd = `E:\code\ao-m0-lab\frontend`，源 `evidence/build-frontend.txt`）

| 步骤 | 命令 | 退出码 | 判定 | 耗时 |
| --- | --- | --- | --- | --- |
| 依赖 | `npm ci` | **0** | PASS | 11m |
| 类型检查（首跑） | `npm run typecheck` | **1** | FAIL*（缺 `packages/product-ui` 依赖） | 24s |
| 依赖（子包） | `npm ci`（`packages/product-ui`） | **0** | PASS | 15s |
| 类型检查（重跑） | `npm run typecheck` | **0** | PASS | 62s |
| daemon 构建 | `npm run build:daemon -- --dev` | **0** | PASS（产出 `daemon/ao.exe`，52,738,560 B） | 6s |
| 打包 | `npm run build` | **1** | FAIL（代理/TLS：`connect ETIMEDOUT 20.205.243.166:443`） | 125s |
| 打包（清除代理变量） | `npm run build` | **0** | PASS | 182s |
| 根级类型检查 | `npm run frontend:typecheck` | **0** | PASS | 50s |
| 单元测试 | `npm test`（vitest，未管道化，取 npm 自身退出码） | **1** | FAIL：**41 个文件失败 / 591 通过（共 632）；238 个测试失败 / 10563 通过 / 17 跳过（共 10818）；874s**。其中 **约 39 个是本项目自己造成的**——`npm run build`（electron-forge 的 `@electron/rebuild`）把 `better-sqlite3` 换成了 Electron ABI 149 的二进制，而 vitest 跑在 Node 22（ABI 127）；`npm rebuild better-sqlite3` 后单跑 `browser-profile-import.test.ts`，失败数 **47 → 8**，故诚实口径是 **约 199 个真实失败**。主导桶为 `WINDOWS_SYMLINK_PRIVILEGE`（与 Go 侧同源：Developer Mode 关闭、未持有 `SeCreateSymbolicLinkPrivilege`）。**18 个文件共 21 个失败无法用平台/网络/ABI 解释**，必须在 M1 前逐个分诊（清单见 §3.3）。判读依据：CI 的 vitest 作业**只跑 ubuntu-latest**（`.github/workflows/frontend.yml:273-278`，shards 1-4；该文件内 **0 个 `windows-*` runner**），所以「Windows 上红」是**移植缺口而非回归**；但那 18 个文件在**任何平台都未经验证**。 | 874s |

`build-frontend.txt` 明确记录：**Node 22.20.0 足以完成 install/typecheck/build**，
因此设计 §3.1 中「准备 Node 24」的前提在本机并不成立（仓库也未声明 Node 主版本）。

### 3.3 vitest 残余失败清单（18 文件 / 21 测试，M1 前必须分诊）

这 18 个文件的失败**无法**用「符号链接权限 / POSIX 路径 / 网络 / macOS 专用二进制 / ABI 不匹配」
中的任何一类解释——它们的断言里没有平台标记，因此必须当作**可能的产品缺陷**逐个分诊，
不能计入「已知环境基线」。

| # | 文件 | 失败数 |
| --- | --- | --- |
| 1 | `src/main/desktop-telemetry-controller.test.ts` | 2 |
| 2 | `src/renderer/components/GlobalSettingsForm.test.tsx` | 3 |
| 3 | `src/renderer/components/SettingsDialog.test.tsx` | 2 |
| 4 | `src/renderer/components/ProjectSettingsForm.test.tsx` | 1 |
| 5 | `src/renderer/components/settings/HarnessSettingsSection.test.tsx` | 1 |
| 6 | `src/renderer/components/ProjectEnvironmentSettings.test.tsx` | 1 |
| 7 | `src/renderer/components/ArtifactHistory.test.tsx` | 1 |
| 8 | `src/renderer/components/HarnessUpdateNotice.test.tsx` | 1 |
| 9 | `src/renderer/components/settings/BillingSection.test.tsx` | 1 |
| 10 | `src/renderer/components/chat/ChatTimelineItems.test.tsx` | 1 |
| 11 | `src/renderer/components/chat/ChatWorkspace.test.tsx` | 1 |
| 12 | `src/renderer/hooks/useBrowserView.test.tsx` | 1 |
| 13 | `src/main/chat-image-ipc.test.ts` | 1 |
| 14 | `src/main/cloud-agent-proxy.test.ts` | 1 |
| 15 | `src/main/mac-update-progress.test.ts` | 1 |
| 16 | `src/main/provider-auth-flow.test.ts` | 1 |
| 17 | `src/main/sentry-main.test.ts` | 1 |
| 18 | `src/shared/telemetry.test.ts` | 1 |

其中两个断言呈**日期/语言环境**形状（`expected 'May 7' to match /^May 6/`、
`labels yesterday and older messages with calendar dates`），提示可能是时区/locale 而非产品逻辑——
但这仍是**假设，不是证据**，所以在分诊完成前一律计入残余。
逐文件证据见 `evidence/baseline-failures.md` Appendix A.3 / A.3.1。

### 3.4 执行顺序陷阱（给下一位执行者）

`npm run build`（electron-forge 打包）会执行 `@electron/rebuild`，把 `better-sqlite3`
换成 **Electron ABI（149）** 的二进制；而 vitest 跑在 **Node 22（ABI 127）** 上，此后任何
触及该模块的测试都会以 `NODE_MODULE_VERSION 149 ... requires 127` 失败——
本次就是这样自伤了 **39 个测试**（`browser-profile-import.test.ts` 47 失败 → `npm rebuild` 后 8 失败）。

**因此：先跑 `npm test`，再跑 `npm run build`；若顺序反了，必须先 `npm rebuild better-sqlite3`
再取数，否则统计出的失败数偏大且不可复现。**

本报告实际执行的命令（只读探测；下文每条均已实测成功，`env-baseline.txt` 逐条记录了其输出）：

| 命令 | 用途 |
| --- | --- |
| `git rev-parse HEAD`、`git rev-parse --abbrev-ref HEAD`、`git status --porcelain` | 记录 SHA / 分支 / dirty |
| `git worktree list` | 确认试验 Worktree 与主树指向同一 SHA |
| `git --version`、`node --version`、`npm --version` | 基线 |
| `$env:GOTOOLCHAIN='local'; go version`、`go env GOTOOLCHAIN GOROOT GOVERSION` | 区分「本地 Go」与「自动切换后的 Go」 |
| `Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion'` | OS build |
| `$PSVersionTable` | PowerShell 版本 |
| `Test-Path`（逐个隔离路径） | 见 §2.2 |
| `Get-Command codex,claude,omp,dsh`；各自的 `--version` | 见 §5 与本报告 §7.2 |
| `go vet` / `go test -run XXXNONE`（四个 chatdriver 包） | 仅编译检查，不运行任何 Live 测试（详见能力矩阵 §3 与 `agents/static-survey.md`） |

---

## 4. W-01～W-10（Windows 基座）

结果抄自 `E:\code\ao-m0-scratch\evidence\base.txt`，**计数按该文件现算**：
共 **83** 条 `test_id` 记录 = **78 PASS / 2 FAIL / 3 BLOCKED**。其中 2 条是**结案注解**而非独立用例
（`W-01.18-SUPERSEDED`、`W-06.3-RESOLVED`，均为 PASS，已分别解释同 id 的 2 条 FAIL），
故独立用例记录为 81 条，实质 PASS 76 条。`FAIL` 与 `BLOCKED` 逐条注明，不隐瞒。

**判定口径**：`PASS` = 断言全部通过且有原始产物；`PARTIAL` = 同一用例内一部分场景已验证、另一部分被预算或会话前提阻断——**PARTIAL 里已经 PASS 的那一半同样是结果，不得当成 BLOCKED 丢掉**；`BLOCKED` = 前置条件缺失且无零成本替代；`NOT_TESTED` = 从未执行。
任何只到 API/mux 层的 `PASS`，§4.1 逐条列出，**不要把 PASS 读成「在 GUI 里点通过」**。

| ID | 场景 | 预期 | 覆盖方式 | 结果 | 依据 |
| --- | --- | --- | --- | --- | --- |
| W-01 | 独立 Daemon loopback 运行 | 仅 `127.0.0.1` 提供主服务；健康检查合理 | 自动化（`/healthz`、`/readyz`、running.json、监听端口） | **PASS**（43 条子记录全 PASS） | `base.txt` W-01…W-01.27；`daemon-run.log`。监听仅 `127.0.0.1`+`[::1]`，无 `0.0.0.0`；`workingDirectory` 在 scratch、`startupWorkingDirectory` 在 lab |
| W-02 | Electron 连接测试 Daemon | 启动即见空项目/测试数据 | 自动化 + 窗口核对 | **PASS**（17 条） | `base.txt` W-02.x；`electron-smoke-summary.txt`（窗口 pid 8988 / `MainWindowHandle=10031440` / 标题 `Orchestrator.inc (dev)`、54 个渲染请求全 2xx、关闭后 daemon 自行退出） |
| W-03 | 新测试 Git Project | UI/API 注册并能创建 Worker Session | 注册半自动；**spawn 需预算** | **PARTIAL**（注册 PASS / Session BLOCKED） | 注册 PASS：`POST /api/v1/projects` → 201 `kind=single_repo`（`base.txt` W-03.0）。Session BLOCKED_PENDING_BUDGET：`POST /api/v1/sessions {harness:"fake"}` → 400 `UNKNOWN_AGENT_ID`（W-03.1），`GET /sessions` 始终 `{"sessions":[]}`、`worktrees` 为空（W-03.2）。**零 token 的 session 路径在本仓库不存在**：`agent/registry/registry.go:63-101` 的 `Constructors()` 不含 fake，`domain/harness.go:46-48` 注明其「not user-selectable」，`registry_test.go:252-259` 断言其不得可选 |
| W-04 | 多仓库 Workspace | 两样板仓建 Worktree，路径/Base SHA 对应 | 注册半 PASS；**会话半需预算** | **PARTIAL**（注册 PASS / Worktree+BaseSHA BLOCKED） | 注册 PASS：`POST /api/v1/projects {asWorkspace:true}` → 201 `kind=workspace`，`workspaceRepos=[repo-a ready, repo-b ready]`（W-04.0/.1）。**产品硬约束**：首次调用返回 **400 `WORKSPACE_NO_READY_REPOS`**——「Workspace 必须至少包含一个带 origin remote 的完整 git 仓库」；给两个子仓各加 `origin=file:///…`（本地、无 push）后即 201，说明 asWorkspace **依赖 origin remote 才能判定仓库 ready**，M1 设计必须考虑无远程的样板仓。Worktree + Base SHA BLOCKED_PENDING_BUDGET：`/desktop/sessions/{id}/workspace`、`/workspace/history` 均 session-scoped（W-04.2 `NOT EXECUTED: requires a spawned session`） |
| W-05 | SSE 订阅与 UI 重连 | 断线后状态/历史正确 | 自动化 | **PASS**（2 条） | `base.txt` W-05.0（7 事件回放 seq 1..7）、W-05.1（`X-Ao-Event-After: 7`、cursor+heartbeat）；首次 SSE 打开见 W-01.25 |
| W-06 | Terminal/ConPTY | 终端可开、可输入输出、退出正确 | 自动化（WebSocket 客户端） | **PASS**（含 1 条 FAIL 后被解释） | W-06.0/.1/.2 PASS（201 spawn → `/mux` 101 → 真实回显并执行 `M0-W06-OK` → 204 close）；**W-06.3 FAIL**：遗留一个 detached `pty-host`（pid 16204，仅占临时端口）；`W-06.3-RESOLVED` PASS：四项归属校验后 `/F` 清理，且下次启动会回收 |
| W-07 | Chat Controller / Host | UI 关开保持原生 Session | 需先有 Chat 会话 | **NOT_TESTED** | `base.txt` 中**无任何 W-07 记录**；W-02 只证明起停，不证明会话保持。见 §4 末第 3 点的定性 |
| W-08 | Review / Git 观察 | 本地 Diff 可检视；离线时区分 PR/CI 不可用 | 需会话 | **BLOCKED** | `base.txt` W-08.0 `budget_not_approved`（`/sessions/{id}/workspace/diffs`、`/workspace/files` 均为 session-scoped） |
| W-09 | 异常停止 | 保留 dirty Worktree、审批与重试边界 | 自动化 | **PASS**（4 条） | W-09.0 建脏 worktree；W-09.1 归属三查后 `taskkill /F`；W-09.2 内容 sha256 前后一致；W-09.3 重启后无幻影会话。标注为「**W-09 via hard-kill + git inspection, no agent**」 |
| W-10 | 清理与退出 | 不关用户自己的 CLI/Daemon/Electron | 自动化 + 归属核对 | **PASS**（3 条） | W-10.0：3001/3002/3037/3039/3041/3043 全清、`.ao` 不存在；W-10.1：只停自己的进程，38 个 node.exe 与他人 A-01 daemon（pid 43836 / 3041）均未被信号 |

三处必须写进结论的细节：

1. **W-06.3 的那条 FAIL 是真实缺口，但已被判为「可接受的设计后果」**：
   ConPTY 主机按设计 `DETACHED_PROCESS` 脱离（`conpty/spawn_windows.go:78-88`），
   因此 daemon 死亡到下次启动之间会有一个孤儿 pty-host 占着临时端口，
   Windows 上无法优雅终止（只能 `/F`，实测 `/PID` 被拒：「只能强行终止」）。下次启动会回收
   （`ptyregistry` `registry.go:282-317` 剪掉死 PID，`shellterm/service.go:867-902` 销毁会话已死的 shell，
   实测日志 `reaped shell terminals from previous app runs count=1`）。
2. **W-01.18 的 FAIL 是快照时序假象**：那次 `Phase After` 在 Electron 仍持有 3037 时抓取。
   在 `-Action Stop` 之后重跑 Phase After + Compare，6/6 通过，且原始 FAIL 记录被保留并标注 SUPERSEDED。
3. **W-07 完全没测，且是全表唯一「既没执行、也不 BLOCKED」的用例。**
   它不能算通过，也不能算阻断，只能记 `NOT_TESTED`——而且它**连测试前提都还不具备**：
   W-07 验证的是「UI 关窗重开后原生会话保持」，必须先有一个**真实跑起来的 Chat 会话**
   （依赖 A-02 拿到对话），再叠加关窗/重开的人为循环。预算未批、A-02 未跑，因此它在预算批准前
   不可能被降级为任何一个「已测」结论。

### 4.1 通过 API/mux 替代 UI 的项目

本节是承重的：**下面任一 `PASS` 都不代表「在 GUI 里点过」**。凡引用这些结论处，一律写成
`W-0x via API/mux, not UI`。每行给出被替代的 UI 动作、实际使用的入口（`文件:行`）以及**明确没证明什么**。

| ID | 被替代的 UI 动作 | 实际入口（path + 代码位置） | 该替代**没有**证明什么 |
| --- | --- | --- | --- |
| W-02 | 打开桌面端、看到空板 | `electron-forge start` + `Get-Process electron`（`MainWindowHandle`/标题），以及渲染进程自发请求 `GET /api/v1/projects`（`controllers/projects.go:27`）、`GET /api/v1/sessions`（`controllers/sessions.go:211`）、`GET /api/v1/settings`、`POST /api/v1/agents/readiness/ensure`（`controllers/agents.go:39`） | 空板/`dev` 角标的**视觉**呈现、布局与文案 |
| W-03 | 在 UI 里注册项目 | `POST /api/v1/projects`（`controllers/projects.go:28`），body `{path, projectId, name, asWorkspace}`（`service/project/dto.go:13-20`） | UI 表单校验、错误提示、克隆流程；Session 半边本身未测 |
| W-04 | 在 UI 里建 workspace 项目 | `POST /api/v1/projects {asWorkspace:true}`（同上）+ `GET /api/v1/projects/{id}`（`controllers/projects.go:34`） | 双仓 tab 布局；Worktree/Base SHA 对照（无 session 未验） |
| W-05 | UI 断线重连 | `GET /api/v1/events?after=0` 与 `?after=latest`（`httpd/events.go:39`） | 渲染进程 EventSource 的重连退避与状态缝合 |
| W-06 | 打开终端、敲命令 | `POST /api/v1/shell-terminals`（`controllers/shell_terminals.go:37`）+ `GET /mux` WebSocket（`httpd/terminal_mux.go:32`），帧格式 `{ch:"terminal", id, type:"open"\|"data", data:base64}`（`terminal/protocol.go:23-28,49-64`） | 键入手感、滚动/TUI 重绘、关闭后不留空白窗 |
| W-09 | 人为中止后检查现场 | `taskkill /F <已核验 PID>` + `git -C <worktree> status --porcelain` + `sha256sum` | 审批对话框、重试边界（需活 agent） |
| W-10 | 退出后确认没误伤 | `Get-NetTCPConnection`、`Get-CimInstance Win32_Process`、`Test-Path %USERPROFILE%\.ao` | 无 |

### 4.2 环境前提与已知限制（重跑 W/A 之前先读）

| # | 前提 / 陷阱 | 后果 | 规避 |
| --- | --- | --- | --- |
| 1 | **Node ABI 顺序陷阱**：`npm run build` 经 `@electron/rebuild` 把 `better-sqlite3` 换成 Electron ABI 149，而 vitest 跑 Node 22（ABI 127） | 任何触及该模块的测试以 `NODE_MODULE_VERSION 149 ... requires 127` 失败；本次因此虚增 39 个失败（`browser-profile-import.test.ts` 47→`npm rebuild` 后 8） | **先 `npm test`，后 `npm run build`**；顺序已反则先 `npm rebuild better-sqlite3` 再取数 |
| 2 | **符号链接权限**：Developer Mode 关闭，进程未持有 `SeCreateSymbolicLinkPrivilege` | Go 侧 92 条、前端侧 `WINDOWS_SYMLINK_PRIVILEGE` 桶（约 66 个测试）全部 `EPERM ... symlink` | M0 禁止改系统设置，故只作为已知基线；不得计入产品缺陷 |
| 3 | **代理/TLS**：`HTTP(S)_PROXY` 指向本机 7897 | `npm run build` 的 `Copying files` 阶段 `connect ETIMEDOUT 20.205.243.166:443` 失败 | 打包前清空代理变量（本次 step 7 已验证 exit 0） |
| 4 | **Go 工具链**：`go.work`/`go.mod` 要求 1.27.1，本机是 1.26.3 | `GOTOOLCHAIN=local` 直接 exit 1；默认 `auto` 靠模块缓存里的 1.27.1 | 接受 auto（需用户裁决，见 §8.3），或独立安装 1.27.1 |
| 5 | **Claude 的 ACP 运行时目录遮蔽**：`frontend\acp-runtime` 空壳优先于 `resources\acp-runtime` | 未设 `AO_ACP_RUNTIME_DIR` 时 Claude 的 chat 驱动 Probe 返回空数组 | 启 daemon 时设 `AO_ACP_RUNTIME_DIR=E:\code\ao-m0-lab\frontend\resources\acp-runtime` |
| 6 | **端口不可强占**：`AO_PORT` 只是请求，被非 AO 进程占用时会静默回退临时端口 | 客户端连错端口 | 以 `running.json` 的 `port` 为准，不假定 `AO_PORT` |
| 7 | **Windows 无 `ps`**：`readProcessStartMs` 恒返回 `null` | 卡死的 daemon 无法被识别替换，可能出现两个 daemon | 收尾前用 `/healthz` 的 `pid`+`workingDirectory` 核对归属（本次 A-01 与 W-10 均按此执行） |

---

### 4.3 Windows 基座新发现

三条都是「macOS 上绿、Windows 上红」的复发源，移植与 CI 必须先知道。证据统一见
`E:\code\ao-m0-scratch\evidence\baseline-failures.md`。

| # | 发现 | 证据指针 | 性质与影响 |
| --- | --- | --- | --- |
| 1 | **`git worktree list --porcelain` 输出正斜杠，而代码比的是反斜杠** —— 输出 `worktree E:/code/agent-orchestrator`，断言却是 `strings.Contains(list, "worktree "+second.Path+"\n")`（`second.Path` 含 `\`），**在 Windows 上永远不可能匹配** | `baseline-failures.md:200-204`（原始命令与输出）、`:105-107`（`WINDOWS_GIT_WORKTREE` 桶，4 用例 / 9 行） | **测试侧缺陷**（断言未归一化路径）。但它同时标记了产品侧同类风险：任何依赖 `worktree list` 文本比较的生产代码也会失败，应统一先 `filepath.ToSlash`。本次 W-09 用 `git status --porcelain`（相对路径）未受影响 |
| 2 | **`TestConPTYStartsAtRequestedGrid` 失败：`mode con did not finish`** —— 起 ConPTY 后跑 `mode con`，10s 内没读完输出即 `t.Fatal`，期望 `Columns: 146` / `Lines: 37` | `build-go.txt:182`（`(10.01s)`）、`_go.raw.log:416-417`、用例本体 `conpty/host_conpty_windows_test.go:72-88`、归因 `baseline-failures.md:66` | **测试侧探测手法/时序问题**，不是终端功能坏掉：W-06.1 已独立证明真实 ConPTY 可开、可回显、可执行命令（`/mux` 101 + `M0-W06-OK`）。需另设计一个不依赖 `mode con` 的栅格断言；在此之前不得用该测试判定 ConPTY 是否可用 |
| 3 | **`core.autocrlf=true` 打断内容寻址哈希校验** —— 本机 local 与 global 均为 `true`，`pricing/catalog/v1/providers/anthropic/<sha256>.json` 的实际字节哈希与文件名不符，LF 归一化后才相等 | `baseline-failures.md:206-220`（`git config --get core.autocrlf -> true` + 三种哈希对比）、`:111`（`CRLF_CHECKOUT`，2 用例 / 2 行） | **检出配置缺陷，不是代码缺陷**。任何按内容哈希/字节相等的验证（pricing catalog、PR 内容比对、golden 文件）都必须在 `core.autocrlf=false` 或 LF 工作树上跑；本次 W-09 的 sha256 是同一工作树前后对比，故不受影响 |

---

## 5. A-01～A-15（四 Agent 用例）

「成本」列 = 该用例**至少**需要消耗的 provider turn 数（0 = 不产生模型调用），
即 §6.3 的**低界**；高界与逐项推演在 §6.3，两处数字互相一致（A-03 表内 0 对应 §6.3 的 0–1，
A-08 表内「0（基线）／1（确认轮）」对应 §6.3 的 1–2，基线读取本身不产生 turn）。

| ID | 用例 | 自动化入口 | Codex | Claude | OMP | DSH | 结果 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| A-01 | 预检 | `POST /agents/{agent}/probe`、`GET /agents/chat/permission-modes`（`controllers/agents.go:40`、`controllers/conversations.go:146`） | 0 | 0 | 0 | 0 | **Codex PASS ／ Claude Code BLOCKED ／ OMP PASS（带标注）／ DeepSeekRuntime PASS**（证据 A-01：`E:\code\ao-m0-scratch\evidence\agents\a01-probe.md`） |
| A-02 | Chat 基本轮次 | `POST /sessions` → 轮询 `GET /sessions/{id}/conversation` | 1 | 1 | 1 | 1 | NOT_TESTED |
| A-03 | 流式与历史 | `GET .../conversation` ×2、`.../models`、`.../config-options` | 0 | 0 | 0 | 0 | NOT_TESTED |
| A-04 | TUI 基本运行 | `POST /sessions {mode:"tui"}` + `POST .../exit-agent` | 1 | 1 | 1 | 1 | NOT_TESTED |
| A-05 | Queue | `POST .../conversation/messages` ×2 | 2 | 2 | 2 | 2 | NOT_TESTED |
| A-06 | Steer | `POST .../conversation/steer` | 1 | 0 或 1 | 0 或 1 | 0 或 1 | NOT_TESTED |
| A-07 | Stop | `POST .../conversation/interrupt`（无 body，204） | 1 | 1 | 1 | 1 | NOT_TESTED |
| A-08 | Resume | 重启 Daemon 后 `GET .../conversation` + 1 轮追问 | 0（基线）／1（确认轮） | 同左 | 同左 | 同左 | NOT_TESTED |
| A-09 | Git 改动 | `GET .../workspace/diffs` + 原仓 `git status` | 1 | 1 | 1 | 1 | NOT_TESTED |
| A-10 | 审批拒绝 | `POST .../approvals/{requestId}/resolve` | 1 | 1 | 1 | 1 | NOT_TESTED |
| A-11 | 模型参数 | `PATCH .../config-options/{configId}`、`PATCH .../settings` | 1–2 | 1–2 | 1–2 | 1–2 | NOT_TESTED |
| A-12 | 用量 | 快照 `usage` 字段 + 事件流 | 1 | 1 | 1 | 1 | NOT_TESTED |
| A-13 | 故障 | 断 UI/网络、杀测试进程 | 1 | 1 | 1 | 1 | NOT_TESTED |
| A-14 | 工作区保留 | 取消后 `git status` | 1 | 1 | 1 | 1 | NOT_TESTED |
| A-15 | 受控清理 | `POST /shutdown`、进程表核对 | 0 | 0 | 0 | 0 | NOT_TESTED |

### 5.1 A-01 已执行（0 turn）

A-01 已在一台独立 daemon（`AO_DATA_DIR=E:\code\ao-m0-scratch\data-a01`，端口 3041）上**真实执行**，
证据 `E:\code\ao-m0-scratch\evidence\agents\a01-probe.md`（证据 ID **A-01**）。

| Agent | A-01 结果 | 一句话依据 |
| --- | --- | --- |
| Codex | **PASS** | `codex.exe` 0.154.0 ≥ 0.146.0；installed / authorized / ready；chat Probe 通过；模型目录 4 个带 effort |
| Claude Code | **BLOCKED** | CLI 与 Node 均达标、chat Probe（补 `AO_ACP_RUNTIME_DIR` 后）通过；但 `provider_validation=provider_unreachable provider=gateway`，`effectiveReadiness=unknown`，模型目录 0 |
| OMP | **PASS（带标注）** | 18.13.3 达标、chat Probe 通过、模型目录 58 个；auth 如实为 `unknown`（凭据库 0 行），3037 的 10s 超时未复现 |
| DeepSeekRuntime | **PASS** | installed / authorized / ready；chat Probe 通过；ACP 模型目录 4 个（免费路径） |

**读这一列之前必须知道的一件事：**本表「验证通过」的含义只到
**「驱动存在 + `Probe` 通过 + 模型目录可读」**，**不包含**
「发过一轮 chat 并收到回复」——四个 Agent 的 A-02 都还是 `NOT_TESTED`，
`agent-runtime.ps1` 在未加 `-AllowTurns` 时只打印请求体、不发送。
因此 A-01 的 PASS **不能**被引用为「该 Agent 的 Chat 可用」的证据。


因此 §5 表格中 A-01 那一行的「结果」列应读作：Codex PASS / Claude BLOCKED / OMP PASS(标注) /
DSH PASS；同一行的 A-02～A-15 仍然全部 NOT_TESTED。

A-01 顺带修正了三处既有认识（详见 `a01-probe.md` §5）：
Codex 的 daemon 侧版本是 **0.154.0**（不是 shell 里的 0.163.0）；
Claude 的 chat 驱动在默认基座下**不可用**，根因是 `frontend\acp-runtime` 空壳目录遮蔽了
`resources\acp-runtime`（`chatdriver/claudeacp/driver.go:573-588`）；
OMP 的 `auth_check_timeout` **未复现**（10 次 96–293 ms），真实结论是 `credential_file_missing`。

关于 A-06 的「0 或 1」：只有 `capabilities` 含 `steer` 时才需要一次真实插入；
否则正确结果是 `UNSUPPORTED` 且不应消耗任何 turn（`chatdriver/acp/driver.go:945` 门控）。

覆盖边界（必须写清楚）：

- 自动化脚本 `E:\code\ao-m0-scratch\harness\agent-runtime.ps1` **只覆盖 A-01～A-08**。
  A-09～A-15 目前没有脚本，属于待建。
- A-04 的终端内容、A-03 的逐 delta 流式，无法通过 HTTP 观测（终端是 `/mux` WebSocket，
  事件是全局 SSE），脚本会把这两处留在 `NOT_TESTED` 并注明原因。

---

## 6. 预算登记

本节的数字全部是**实测或源码读出**，没有估算的 token 数——token 取决于所选模型与提示长度，
在设计阶段给出 token 数字只能是假的。因此一律以 **provider turn（轮次）** 计量。

### 6.1 已消耗：0 turn / 0 token

| 项 | 值 | 依据 |
| --- | --- | --- |
| 已消耗 provider turn | **0** | 全部 A-01 请求均为 `probe` / `readiness` / `models` / `permission-modes`，无 `POST /sessions`、无 `conversation/messages`、无 `steer` |
| 已消耗 token | **0**（无账单产生） | 同上；且没有任何一次 `session/prompt` 或 `turn/start` 被发出 |
| 登录动作 | **0** | 未执行任何 login / logout / account switch |
| 写入 `%USERPROFILE%\.ao` | **0 次** | `Test-Path` 全程 `False`（W-10.0、A-01 结束时各验一次） |

### 6.2 A-01 期间启动过的 4 个 provider 进程，逐个证明「无 prompt」

它们都要与 provider 通信，所以必须逐个交代；**这 4 个都不发提示词**。
下表最后多列一条 Claude 的 `Probe` 作为对照——它**一个进程都没起**。

| 进程 | 谁启动的 | 做了什么 | 为何无 prompt（源码） |
| --- | --- | --- | --- |
| `codex.exe app-server` | Codex 的 chat 驱动 `Probe`（`GET /agents/chat/permission-modes`） | `initialize` → `model/list` → 关闭 | `chatdriver/codexappserver/driver.go:196-218`：handshake 后**只**调 `model/list`，随后 `conv.Close()`。该函数内无 `thread/start`、无 `turn/start` |
| `codex.exe app-server`（第二次） | `POST /agents/codex/models/refresh` | `DiscoverModels` → `model/list` | `driver.go:224-242` 调用 `listModels`；`conversation.go:516-560` 是纯读 `model/list`，不含 turn |
| `dsh --profile acp` | `POST /agents/deepseek-harness/models/refresh`（wiring 见 `internal/daemon/daemon.go:504-511`） | `initialize` → `session/new` → 读 `configOptions` → 关闭 | `chatdriver/acp/driver.go:220-261`（`DiscoverConfigOptions`）整函数体只有 `spawn`、`initialize`、`session/new`、`normalizeConfigOptions`；全文无 `session/prompt` |
| `omp models --json` | `POST /agents/omp/models/refresh` | 列出可用模型 | 命令表 `internal/adapters/agent/modelcatalog/catalog.go:128`，`args: ["models","--json"]`，一次性 CLI 查询，非 ACP 会话 |
| （对照）Claude 的 chat `Probe` | `GET /agents/chat/permission-modes?agents=claude-code` | **什么都没启动** | `chatdriver/claudeacp/driver.go:112-124` 只做三件事：`resolveRuntime`（检查文件存在）、`ResolveBinary`、`validateClaudeACPExecutable`；不进 ACP 握手，不 spawn provider |

清理：以上进程均已自行退出；A-01 结束时 `codex`/`node` 无新增残留，
daemon（pid 43836、41076）经 `POST /shutdown` 后进程数 0、端口 3041 无监听。

### 6.3 关闭 M0-05 强制子集的精确轮次成本

设计 §8.1 要求每个 Agent 至少完成 **A-01 / A-02 / A-03 / A-07 / A-08**。
按 REST 语义逐项拆解（A-01 已用 0 轮完成，余下四项）：

| 用例 | 动作序列（REST） | 增量 turn（低） | 增量 turn（高） | 为什么要高位 |
| --- | --- | --- | --- | --- |
| A-01 | `probe` + `readiness/ensure` + `chat/permission-modes` | **0** | 0 | 已完成，且不含 prompt |
| A-02 | `POST /sessions`（带 opening prompt）→ 轮询 `GET .../conversation` | **1** | 2 | 首次 spawn 可能因 check 失败被拒后重试一次 |
| A-03 | 只是读：`GET .../conversation` ×2 + `/models` + `/config-options`；若复用 A-02 的会话则 **0** | **0** | 1 | 若必须单开会话复验，则多 1 轮；**daemon 重启本身不算 turn** |
| A-07 | `POST .../messages`（长命令）→ `POST .../interrupt` → 轮询终态 | **1** | 2 | 若轮次在 interrupt 落地前就结束，需重跑一次 |
| A-08 | 复用既有会话 → 重启 daemon → `POST .../messages`（追问 codeword） | **1** | 2 | 基线读取 0 轮；追问 1 轮；失败重问至多 +1 |

**合计（每个 Agent，强制子集）：低 3 轮 / 高 7 轮。**

| Agent | 当前是否可执行 | 低 | 高 |
| --- | --- | --- | --- |
| Codex | 可（A-01 PASS，ready） | 3 | 7 |
| OMP | 可（A-01 PASS 带标注；auth=unknown，首轮可能 BLOCKED_AUTH） | 3 | 7 |
| DeepSeekRuntime | 可（A-01 PASS，ready） | 3 | 7 |
| Claude Code | **不可**，先修 gateway（见 §6.5） | 3 | 7 |

三个可执行的 Agent 合计：**低 9 轮 / 高 21 轮**；四个全跑：**低 12 轮 / 高 28 轮**。

### 6.4 建议的轮次上限（供用户批准）

| 项 | 建议 |
| --- | --- |
| 每 Agent 轮次上限 | **8 轮**（强制子集高位 7 轮 + 1 轮余量） |
| 本次批准总量 | **24 轮**（Codex + OMP + DeepSeekRuntime 各 8；Claude 暂不批准） |
| 全局熔断 | 任一时刻累计到 24 轮即停，回到评审，不再追加 |
| 模型限制 | 每个 Agent 只用一个模型，且用该模型**最低**的 advertised effort；禁止 A-11 的模型切换，禁止跨 Agent 对比不同模型 |
| 具体选择（依据能力矩阵 §3.2 的实测目录，三个 id 均已核对存在） | Codex：`gpt-5.6-luna` + effort `low`（该模型 efforts 含 `low`，默认 `medium`）；OMP：`codegpt/gpt-5.6-luna`（OMP 的 58 个模型**全部没有 effort 字段**，无可选）；DeepSeekRuntime：`["deepseek-official","deepseek-v4-flash"]` + effort `off`；Claude（修好后）：`sonnet` 别名 + effort `low` |
| 提示词限制 | 全部用例使用固定短提示（`Reply with exactly: pong` 及 A-07/A-08 的固定长/追问句），不粘贴长上下文 |
| 不批准的 | A-04/A-05/A-06/A-09～A-15（不在强制子集内），A-11 的模型遍历 |

理由：token 由模型与提示长度决定，给定「一个模型 + 最低 effort + 一句短提示」后，
轮次就是唯一可控变量，因此用轮次做上限是可审计的；用 token 做上限在事前无法算准。

### 6.5 Claude 的额外前置条件（不消耗 turn）

Claude 要进入可执行状态，需要用户处置其一（**不需要花 turn，但必须由用户决定**）：

1. 启动 `ANTHROPIC_BASE_URL` 指向的 gateway（`http://127.0.0.1:15721`，当前无监听）；或
2. 从 `%USERPROFILE%\.claude\settings.json` 的 `env` 块移除 `ANTHROPIC_BASE_URL`，改走官方登录（CLI 已 `cli_signed_in`）。

另需：daemon 以 `AO_ACP_RUNTIME_DIR=E:\code\ao-m0-lab\frontend\resources\acp-runtime` 启动，
否则 chat 驱动会被空壳 `frontend\acp-runtime` 遮蔽（见 `m0-capability-matrix.md` §3.1）。

### 6.6 预算许可：**尚未取得**

> 任何会触发真实模型调用的用例，**必须先取得用户的显式预算批准**（设计 §2.1、§4.3）。
> **在此批准之前，0 轮会被消耗**——这一条由脚本强制：`agent-runtime.ps1` 不加 `-AllowTurns`
> 时只打印请求体、不发送；加了之后仍需 `-Force` 或逐项 y/N 确认。

**请回答下面这一块（是 / 否 / 或改任意一行）：**

```text
M0-05 预算批准请求（强制子集 A-01/A-02/A-03/A-07/A-08）

允许的 Agent : Codex、OMP、DeepSeekRuntime        （Claude Code：暂不批准，待 gateway 处置）
允许的用例   : 仅 A-02 / A-03 / A-07 / A-08        （A-01 已用 0 轮完成）
轮次上限     : 每个 Agent 8 轮；合计 24 轮；累计到 24 轮即停
模型与 effort: 每 Agent 固定一个模型 + 该模型最低 effort
               Codex  gpt-5.6-luna / low
               OMP    codegpt/gpt-5.6-luna（OMP 无 effort 选项）
               DSH    ["deepseek-official","deepseek-v4-flash"] / off
提示词       : 固定短句，不粘贴长上下文；不做模型对比（A-11 不批准）
不包含       : A-04 / A-05 / A-06 / A-09～A-15
未批准时行为 : 不发送任何会触发模型的请求（当前状态，0 轮已消耗）
Claude 解阻   : 由用户在「启动 15721 gateway」与「移除 ANTHROPIC_BASE_URL」之间选一个
```

| 待批准项 | 状态 |
| --- | --- |
| 预算许可 | **未取得** |
| 轮次上限 | 建议 24（每 Agent 8），**未确认** |
| 允许的 Agent 范围 | 建议 3 个（Claude 暂缓），**未确认** |
| 批准记录（用户原话 / 时间） | **待填** |

---

## 7. 阻断项与差距清单

### 7.1 当前阻断（按严重度）

| # | 阻断项 | 影响 | 证据 | 解除方式 |
| --- | --- | --- | --- | --- |
| B-1 | **预算许可未取得** | A-02～A-14、W-03/W-04/W-08 全部不可执行 | §6.6（0 轮已消耗） | 用户回答 §6.6 的批准块 |
| B-2 | **Claude 的 provider 网关不可达** | Claude 的 A-02 及之后全部无法执行，整条 Agent 线缺一个样本 | `a01-probe.md` §2.3：`ANTHROPIC_BASE_URL=http://127.0.0.1:15721` 无监听 | 启动该 gateway，或从 `~/.claude/settings.json` 移除 `ANTHROPIC_BASE_URL` |
| B-3 | **基线测试不是绿的** | §8.1「基本测试」无法判 PASS；M1 前提不成立 | `build-go.txt`（59 包失败）、`build-frontend.txt`（vitest 失败） | 用户裁决：接受为已知环境基线，或要求修到绿 |
| B-4 | **Go 工具链来源未决** | 「工具链匹配」只能记「条件满足」 | `build-go.txt` step 1 exit 1（`GOTOOLCHAIN=local` 失败），step 2 靠 auto 成功 | 用户裁决：接受缓存的 1.27.1，或独立安装后重跑 |
| B-5 | **W-07 完全未测** | 「UI 关开保持原生会话」无任何证据 | `base.txt` 无 W-07 记录 | 需在 A-02 跑通后单独测一次 |
| B-6 | ~~`sample-repos` 未创建~~ **已解除** | W-03/W-04/W-08/A-09/A-14 的受控仓库 | `Test-Path` 现为 `True`，内含 `repo-a`、`repo-b`、`.gitignore`（W-06.0 的终端 cwd 即 `sample-repos\repo-a`） | 无需操作 |
| B-7 | session 级 `capabilities` 未知 | Steer/Usage/Plans 只能停在静态判定 | 能力矩阵 §1 其余 13 行 | 预算批准后跑 A-02 取一次快照 |

已解除：A-01 预检（四个 chat 驱动 Probe 全部跑通）、独立 Daemon 联通（W-01 PASS）、
Electron 联通（W-02 PASS）、清理与隔离（W-09/W-10 PASS）。

### 7.2 设计假设与实际不符（已核实）

| # | 设计/文档的假设 | 实际情况 | 证据 |
| --- | --- | --- | --- |
| D-1 | `go run ./cmd/ao start` 起 Daemon | **不成立**：`ao start` 只解析/下载/打开桌面 App 后退出；Daemon 是另一个隐藏子命令 `ao daemon` | `backend/internal/cli/start.go:70-74`（Long 文本明示）、`:87`（`runStart`）；`backend/internal/cli/root.go:219,363-373`（`newDaemonCommand`） |
| D-2 | 设 `AO_DATA_DIR` 即可隔离 AO 数据 | **只覆盖一部分**：不覆盖 Electron `userData`、agent-browser 运行时根、`daemon.log`、下载目录 | `frontend/src/main.ts:825,2305,1080`；`windows-base-survey.md` §1.4 |
| D-3 | `AO_PORT` 能 pin 端口 | **只是请求**：端口被非 AO 进程占用时会**静默回退到 OS 临时端口**，真实端口只在 `running.json`/日志里 | `backend/internal/httpd/server.go:50-63` |
| D-4 | `AO_RUN_FILE` 只管 `running.json` | 它是 `app-state.json`、keybindings、editor 偏好、browser profile 状态、**ConPTY pty-host 注册表**、Windows 命名管道的锚点 | `frontend/src/main.ts:799-801,833-840`；`backend/internal/adapters/runtime/conpty/ptyregistry/registry.go:65-86` |
| D-5 | 手工起一个 Daemon 就能被 Dev Electron 接管 | dev 模式有**身份校验**（cwd/startup cwd/executable 三者需与 launch 一致），否则报 `identity_mismatch` 并拒绝 | `frontend/src/main.ts:1631-1652`；`frontend/src/shared/daemon-attach.ts:163-176` |
| D-6 | Windows 上能识别「卡死的 Daemon」 | **不能**：Windows 没有 `ps`，`readProcessStartMs` 恒为 `null`，端口持有者身份无法确认 → 可能两个 Daemon 并存 | `frontend/src/main/process-start.ts:52-63`（源码内已留 TODO） |
| D-7 | `AO_DEV_ELECTRON_DIR` 可用于隔离 Electron profile | 仅对**未打包**运行有效；打包版固定 `~/.ao/electron` 且不可覆盖 | `frontend/src/main.ts:295-306` |
| D-8 | 仓库要求 Node 24 | 仓库**未声明**任何 Node 主版本（无 `.nvmrc`/`engines`/`packageManager`） | `evidence/env-baseline.txt` 第 2 节 |
| D-9 | 设计 §3.1 记录 HEAD `47d594c` | 实际执行时 HEAD 为 `7ce17e5`（`47d594c` 是 `HEAD~1`） | `evidence/env-baseline.txt` 第 1 节 |
| D-10 | 四 Agent 能力可比 | 见能力矩阵 §4：Steer / Resume / Approvals / Usage / model-effort 五项语义各不相同 | `m0-capability-matrix.md` §4 |

### 7.3 Agent 侧静态差距（供 M1 设计规避）

| # | 差距 | 证据 |
| --- | --- | --- |
| G-1 | OMP 的 Chat 路径**没有 effort**（只有 model），TUI 才有 `--thinking` | `chatdriver/ompacp/driver.go:57` 对比 `agent/omp/omp.go:80` |
| G-2 | OMP 审批模式启动即固定，会话中改会返回 `ErrACPSetterUnsupported` | `chatdriver/ompacp/driver.go:64` |
| G-3 | DeepSeekRuntime 未声明 `usage`，token 只能 `unknown` | `chatdriver/deepseekharnessacp/driver.go:27` |
| G-4 | DeepSeekRuntime 无版本探测，`rc` 预发布版本可能带来协议差异 | `chatdriver/nativeacp/driver.go:106` |
| G-5 | Claude 的 Chat 依赖 AO 打包的 `claude-agent-acp` + Node ≥ 22，不是用户 CLI 单独可跑 | `chatdriver/claudeacp/driver.go:34,537` |
| G-6 | `codex`/`dsh` 在本机解析为 `.ps1` shim，AO 的 Windows 解析器只认 `.cmd`/`.exe`/裸名 | `agent/codex/codex.go:406`；`agent/deepseekharness/deepseekharness.go:231` |
| G-7 | `ao doctor` 没有 `deepseek-harness` 的二进制名映射，会去找名为 `deepseek-harness` 的可执行文件并必然 WARN | `internal/cli/doctor.go:80-101,219-224` |

---

## 8. M0 正式验收结论与 M1 限定启动边界

### 8.1 M0 正式验收结论：**BLOCKED**

依据设计 §8.1「强制通过项」与 §8.3，**M0 正式验收仍为 BLOCKED**。基础门禁的**编译、独立 Daemon、Electron、本地安全、异常与清理**已有 PASS 证据，但仍有两项强制缺口：

1. **Windows 基线测试不是绿的**（`go test ./...` 59 个包失败、Vitest 失败），其中前端还有 18 个文件 / 21 个测试未完成归因；不能把平台差异全部当作环境噪声。
2. **四 Agent 尚无真实对话证据**：A-02/A-03/A-07/A-08 均为 `NOT_TESTED`；A-01 只验证驱动预检与模型目录，不代表完成过 Chat 轮次。

因此 M0 不能判 `PASS`，也不能判 `CONDITIONAL`；当前没有足以支撑 M0 降级验收的真实 Agent 对话证据，且 §8.3 中的工具链、基线和 Agent 前置决策仍未全部确认。

| M0 强制验收门禁（设计 §8.1） | 状态 | 依据 / 未完成原因 |
| --- | --- | --- |
| 工具链：Node/Go 可用且版本匹配 | **条件满足** | Node 22.20.0 实测可完成 install/typecheck/build（`build-frontend.txt` 第 45-57 行）；Go 靠 `GOTOOLCHAIN=auto` 隐式取 1.27.1，`GOTOOLCHAIN=local` 直接失败（`build-go.txt` step 1 exit 1）。机制是否接受仍待用户决策 |
| 编译：`go build ./...` | **PASS** | `build-go.txt` step 2 exit 0（2m32s）；step 3 `GOPROXY=off` 亦 exit 0（12.8s），证明可离线复现 |
| 静态检查：`go vet ./...` | **PASS** | `build-go.txt` 结论段「go vet ./... PASS (exit 0)」 |
| 基本测试：`go test ./...` | **FAIL（已知且已归因）** | `build-go.txt`：152/211 包通过、59 包失败，0 数据竞态；失败集中于符号链接权限(92)、Windows ACL 断言(79+)、Windows/POSIX 假设差异。基线**不是绿的**，不得声称 |
| 前端：`typecheck` / `build:daemon` / `build` | **PASS（带环境注记）** | `build-frontend.txt`：step 4 exit 0、step 5 exit 0（产出 `daemon/ao.exe`）、step 7 exit 0（step 6 失败纯粹是代理/TLS）；`npm run frontend:typecheck`（仓库根）exit 0 |
| 前端单元测试：`npm test` | **FAIL（移植缺口，非回归）** | 权威一次：41 文件失败 / 591 通过（632）；238 测试失败 / 10563 通过 / 17 跳过（10818）；exit 1，874s（`build-frontend.txt` step 9；`_vitest2.log:5121-5124`）。扣掉自伤的 ~39（ABI 不匹配，`npm rebuild better-sqlite3` 后 47→8 可复现）后**约 199 个真实失败**，主导为 `WINDOWS_SYMLINK_PRIVILEGE`。**18 个文件 / 21 个测试属残余**，须先分诊。CI 的 vitest 只在 `ubuntu-latest` 跑（`.github/workflows/frontend.yml:273-278`，无 `windows-*`），故 Windows 红不构成回归证据，但那 18 个文件在所有平台均未验证 |
| 独立 Daemon 联通（W-01） | **PASS** | `base.txt` W-01 系列 43 条记录全 PASS；`running.json` 落在 scratch 目录（W-01.1 PASS） |
| Electron 联通（W-02） | **PASS** | `base.txt` W-02 系列 17 条全 PASS；`electron-smoke-summary.txt`：窗口 `Orchestrator.inc (dev)`、daemon 自启于 3037、渲染器请求全 2xx、关闭后 daemon 自行退出 |
| Chat Controller / Host（W-07） | **NOT_TESTED** | `base.txt` 中**没有任何 W-07 记录**；W-02 只证明 daemon 起停，不证明关窗重开保住原生会话 |
| 会话类 W（W-03/W-04/W-08） | **BLOCKED** | `base.txt` W-03.2 / W-04.2 / W-08.0 均 `BLOCKED`，原因 `budget_not_approved`：它们需要真实 spawn 一个 agent |
| 异常与清理（W-09/W-10） | **PASS** | W-09.0–.3 全 PASS（dirty worktree 内容哈希前后一致、无幻影会话）；W-10.0/.1 全 PASS（3001/3002/3037/3039/3041/3043 全清、`.ao` 不存在、未动他人进程，含明确「未向我方 A-01 daemon 发信号」） |
| 四 Agent 就绪：至少完成 A-01/A-02/A-03/A-07/A-08 | **未完成：驱动 3/4，对话 0/4** | A-01 已实测：Codex PASS、OMP PASS(标注)、DSH PASS、**Claude BLOCKED**（gateway `127.0.0.1:15721` 无监听）。A-02/A-03/A-07/A-08 四个 Agent 全 `NOT_TESTED`；`agent-runtime.ps1` 未加 `-AllowTurns` 不发请求 |
| 本地安全：未改真实个人配置 | **PASS** | `%USERPROFILE%\.ao` 全程不存在（W-10.0）；隔离目录 `data-a01`/`run-a01` 独立；主树仅有文档改动，业务源码零改动 |
| 代码证据：可复现、无泄密 | **大部分满足** | 构建/前端/W 系列均有带退出码的证据文件；A-01 有 41 份原始 HTTP 响应。已用程序校验：gateway token 值不出现在任何证据文件中 |
| 协议诚实：Steer/Queue/Stop/Resume 结论来自实测或驱动声明 | **PASS** | 全部结论只有两个来源：源码行号，或带 HTTP 证据的实测；没有任何一条来自 UI 文案或终端字节推断 |

一句话：**基础门禁的「编译 + 独立 Daemon + Electron + 本地安全 + 清理」已有 PASS 证据，
但「Windows 基线测试绿」和「四 Agent 真对话」未满足，因此 M0 正式验收仍为 BLOCKED；
M1 是否可在限定范围先行，单独按 §8.4 判断。**

### 8.2 解除 M0 BLOCKED 的最短路径

已完成、无需再做的：构建（`go build`/`go vet`/前端 build）、独立 Daemon（W-01）、
Electron 联通（W-02）、异常与清理（W-09/W-10）、A-01 预检、本地安全。

仍然需要做的，按顺序：

1. **用户裁决 Go 工具链**：接受已缓存的 1.27.1（`GOTOOLCHAIN=auto`），还是在独立目录装 1.27.1 后用 `local`。
2. **用户裁决基线测试**：接受 `go test ./...` 在 Windows 上的 59 包失败为「已知环境基线」
   （`baseline-failures.md` 已逐条归因），还是要求修到绿。不裁决则 §8.1 的「基本测试」无法判定。
   前端同理：238 个失败中约 199 个是真实的，且**其中 18 个文件 / 21 个测试无法归因**（§3.3），
   必须在裁决前分诊；不能把 238 整包当作「Windows 噪音」一笔勾销。
3. **M0-05 预算暂未批准，后续执行**：只有用户另行明确批准 §6.6 的预算（3 个 Agent、每 Agent 8 轮、合计 24 轮）后，才可执行真实 Provider 轮次；当前继续保持 0 turn。
4. 批准后按 **A-02 → A-03 → A-07 → A-08** 顺序执行，每轮结束即回填能力矩阵与本节。
5. **Claude**：先在 `启动 15721 gateway` 与 `移除 ANTHROPIC_BASE_URL` 之间二选一，
   并确保 daemon 带 `AO_ACP_RUNTIME_DIR` 启动；之后再申请它的 8 轮。
6. W-07（关窗重开保住原生会话）需要在有会话之后单独测一次——它依赖 A-02 先跑通。

> **预算与 M1 无关**：第 3 步的预算批准决定的是 **M0-05（A-02 起的真实对话）** 能否执行，
> **不决定 M1 能否开工**。M1 的四个交付物不触达任何 provider 路径（见 §8.4），
> 因此**未批准的预算只阻断 M0-05 与「首发四 Agent 已验证」这一主张，不阻断 M1**。

### 8.3 待用户确认的事项

| # | 事项 | 影响 | 对应阻断 | 当前状态 |
| --- | --- | --- | --- | --- |
| 1 | 是否接受 `GOTOOLCHAIN=auto` 隐式取 1.27.1（还是独立安装后重跑） | 决定「工具链匹配」能否判 PASS | B-4 | **待确认** |
| 2 | 是否接受当前基线（`go test ./...` 59 包失败、vitest 失败）为已知环境基线 | 决定 M0「基本测试」能否判 PASS | B-3 | **待确认** |
| 3 | §6.6 预算批准块（3 个 Agent / 每 Agent 8 轮 / 合计 24 轮 / 单模型最低 effort） | 决定 A-02 起能否执行 | B-1 | **用户已明确暂缓，后续另行批准；当前不得执行** |
| 4 | Claude：在「启动 127.0.0.1:15721 gateway」与「移除 `ANTHROPIC_BASE_URL`」之间选一个 | 决定 Claude 能否进入可执行状态 | B-2 | **待确认** |
| 5 | DeepSeekRuntime 的 `0.1.5-rc.1` 预发布版本是否可接受 | 决定其结论是否需记为 `CONDITIONAL`，以及是否要求先升级 | — | **待确认** |

注：原先列在此处的「是否允许在打包版 Electron 上跑 W-02」已作废——
W-02 已用 `npm run dev` 在隔离 profile 下跑完并 PASS（`electron-smoke-summary.txt`），
不需要在打包版上重跑。

### 8.4 M1 爆破半径分流（为何 BLOCKED 不等于 M1 不能开工)

**用户授权记录（2026-11-03）**：用户已明确要求按建议执行，授权建立 M1-01～M1-05 任务计划，并允许按本报告限定的零 Provider 范围先行推进。该授权**不包含**M0-05 预算，不改变 M0 正式验收状态，也不授权任何真实 Agent/Provider 调用。

§8.1 判定的 **BLOCKED** 是**整机基线与四 Agent 就绪**的结论——它回答"这台 Windows 机器
能否作为首版四 Agent 的验收基座"；它**不是**"M1 的交付物能否开工"的判定。两者口径不同：
前者要求 `go test ./...` 全绿并跑通四 Agent 真实对话，后者只要求 **M1 自己要改/要依赖的那些包**
可用。混读会误把整机红基线当成 M1 停工令，从而停掉一个实际已具备开工条件的工作流。
依据：`E:\code\ao-m0-scratch\evidence\m1-inheritance-triage.md`（逐包重放 `_go.raw.log`，非按包名推断）。

**M1 交付物依赖包的 Windows 实测状态**

| M1 交付物依赖包 | Windows 测试状态 | 判定 |
| --- | --- | --- |
| `internal/storage/sqlite` | **ok** 221.6s | 绿 |
| `internal/storage/sqlite/store` | **ok** 107.3s | 绿 |
| `internal/storage/sqlite/sqlitetest` | **ok** 30.1s | 绿 |
| `internal/cdc` | **ok** 26.6s | 绿 |
| `internal/httpd` | **ok** 29.8s | 绿 |
| `internal/httpd/apispec` + `/specgen` + `/envelope` | **ok** | 绿 |
| `internal/domain` | **FAIL**：2 个测试，均为 handoff（`handoff_note_test.go:84`、`:211`） | 函数在 M1 包内，但调用路径属 M2（见下）；M1 交付物不调用 |
| `internal/httpd/controllers` | **FAIL**：5 条记录 = 3 条测试缺陷（FS 错误码断言、tmux 负向用例前提、句柄清理）+ 2 条畸形 `file://` fixture | 全为测试/环境产物，非产品缺陷 |

即：**M1 最核心的四块（migration/sqlc、CDC、httpd 基础设施、store）在 Windows 上全绿**，
红只出现在 `internal/domain`（既有 handoff 缺陷）与 `internal/httpd/controllers`（测试产物）。

**五类分流计数**（Go 59 个失败包，按主导原因）

| 类别 | 数量 | M1 后果 |
| --- | --- | --- |
| (a) 产品缺陷 · M1 路径内 | 1 簇 / 2 条记录 | `internal/domain` 的 handoff 红——**不改为 M1 当前交付物**，但 M1 跑该包测试会看到 |
| (b) 产品缺陷 · M1 路径外 | 4 簇 / ~110 条 | 记录即可，不阻断；归属 M2（B1、B2、B8、`service/project` clone） |
| (c) 测试缺陷 | 6 簇 / ~37 条 | 可忽略，但**这些包不可作 Windows 门禁** |
| (d) 环境前提 | 3 簇 / ~120 条 | 有明确用户动作（开发者模式、`core.autocrlf=false`、装缺失 CLI），需批准 |
| (e) 未解 | 2 簇 / ~20 条 | ConPTY 栅格、`service/project` 的合法本地路径；均不在 M1 路径 |

**两个已证实的发现与 M1 的精确关系**

- **B1（Codex 祖先 ACL 锁存）：M1 daemon 会「触发」但「不依赖」。**
  `internal/daemon/daemon.go:962` 的 `agentSvc.WarmCodexAccounts()` 是**无条件**执行的
  （无 feature flag、无平台判断、无 env 开关），所以每台标准 Windows 上启动的 M1 daemon
  都会打一条
  `Codex account store initialization failed reasonCode=account_storage_unsafe`。
  但该失败被降级为 WARN，Codex 就绪仍经 native fallback 解析为 `ready`
  （`_api_agents_readiness.json`），M1 的任何交付物都不读 Codex 账号。
  → **开一条独立 follow-up issue（归属 M2），不阻断 M1**；否则这条 WARN 会污染
  M1 期间每一份 Windows 启动日志与故障排查。

- **B2（handoff 裁剪丢最新一轮）：函数在 `internal/domain`，调用路径在 M2。**
  `domain.BuildHandoffNote`（`handoff_note.go:143`）→ `clipHandoffText`（`:336-346` 为缺陷点）
  只被 `internal/service/chat/provider_switch.go:275` 与 `handoff_note.go:134` 调用；
  M1 的 migration、domain 对象、DAG validator、CRUD+API+CDC 均不调用它。
  → **M1 期间不要修改 `handoff_note.go`，`internal/domain` 只新增文件**，
  避免与 M2 的手续修复产生冲突。

**M1 开工条件（三条）**

1. **每个 M1 PR 必须声明**这 7 条既有红为「先前已存在、非本次引入」：
   `internal/domain` ×2（`handoff_note_test.go:84`、`:211`）与
   `internal/httpd/controllers` ×5（`fs_test.go:133` ×2、`mobile_concurrency_test.go:106`、
   `artifact_files_test.go:238`、`projects_test.go:297`）。
2. **B1 单独开 issue**（Windows daemon 启动必现的 `account_storage_unsafe` WARN），
   否则无法区分「M1 引入」与「既有噪声」。
3. **M1 不触达任何 provider 路径，因此需要 0 个 provider turn**；
   未批准的预算**不阻断 M1**，只阻断 M0-05 与「首发四 Agent 已验证」。

**结论的严格区分**：**M1 可以开工（有条件）；M0-05 与「首发四 Agent 已验证」仍为 BLOCKED。**
本条不软化 §8.1——整机基线与四 Agent 就绪的判定维持 BLOCKED 不变。

## 9. 本文档的占位清单

A-01 已回填的行已从此表移除；只剩下面这些。

| 位置 | 占位内容 | 回填条件 |
| --- | --- | --- |
| §4 W-07 行 | 结果列（关窗重开保住原生会话） | 需要先有一个真实 Chat 会话（依赖 A-02） |
| §4 W-03 / W-04 / W-08 | 结果列目前为 BLOCKED（`budget_not_approved`） | 预算批准后 spawn 一个 agent |
| §5 A-02～A-15 | 结果列（A-01 已回填，A-02 起 14 行 × 4 Agent 未动） | 预算批准 + 用例执行 |
| §6.6 | 预算许可、轮次上限、允许的 Agent 范围、批准记录 | 用户回答 §6.6 的批准块 |
| §8.1 | 「四 Agent 就绪」「基本测试」两行的最终判定 | 预算批准后跑完强制子集 / 用户裁决基线 |
| §8.3 | 四项待用户确认 | 用户答复 |

本文件在任何一行被真实证据回填之前，都不构成 M0 的完成声明；M0 的完成分级
（`PASS` / `CONDITIONAL` / `BLOCKED`）由用户在证据齐备后确认（设计 §8.3）。
