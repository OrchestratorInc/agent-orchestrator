# M5 实施与验证记录

更新：2026-11-07。状态：**验证进行中，未 HANDOFF / ACCEPTED，不进入 M6。** 本文替换环境挂起时期的中间记录；历史失败日志仍保留。

最新收口：SV-07/08直接回归、活跃离线Chat草稿与终端往返通过；Browser预览实际展示未通过。实例已清理，93文件冻结一致；真人授权及其他未过门禁保持，详见文末收口记录。

## 基线与范围

主 checkout 为 `E:/code/agent-orchestrator`，分支 `feat/multi-agent-desktop`，本轮 `git rev-parse HEAD` 输出 `8384bcc2a48d148e611f6d20b596de4eecd15a2d`，exit 0。隔离桌面 worktree 为 `C:/Users/11459/.codex/worktrees/m5-desktop-safety/agent-orchestrator`，由 create_worktree 创建，detached M4 基线。

未提交、推送、发布，未调用真实付费 Provider，未改受保护配置或真实 AO 数据。统筹设计/计划/验收/返修文档保持原状，执行方只维护 contracts/report。安装、生成、编译和测试串行，Go 使用 GOMAXPROCS=2、-p 2 -parallel 2，缓存和临时数据在 C 盘。

实现包括目标规划/候选持久化、只读 Planner、重规划幂等恢复、真实 Manager 收尾与维护、Main 可信只读授权、封闭 Operator 写入、工作台/原 Session 保活、DAG/任务/候选编辑/干预/质量/逐仓集成/预算面板。接口和 Manager 模型/effort 支持矩阵见 m5-contracts.md。OMP/DeepSeek 的非空显式 effort 在 Manager 返回 ErrUnsupportedEffort，profile 明确拒绝；底层 Adapter 字段不代表产品支持。

## 返修状态

| 项目 | 当前行为 | 证据及限制 |
|---|---|---|
| SV-01 | Main 私有只读授权按 Workflow/daemon 隔离；Query/CDC/reconnect 只消费已有授权 | IPC、principal、正式 HTTP 读取通过；真实 Electron 带任务工作台已验证未授权拒绝且无自动弹窗，成功原生授权链仍待验 |
| SV-02 | 移除不成立的 Operator 协作发送；Agent 消息只读，人工消息走指定 Attempt 干预 | 正式 Queue HTTP 路径通过，原 Agent 身份边界保留 |
| SV-03 | 先真实停止再续租恢复、工件冻结、预算对账；单 job 失败继续其他项，ctx/fence 中止 | 容量 1 Manager/Scripted/SQLite 重开通过；多 job 维护通过，fake Retirer 不算物理终止证据 |
| SV-04 | 重复任务校验前查已有重规划；同键同正文返回原 Planner，改正文冲突 | 容量 1 正式 HTTP 验证，无额外 Spawn |
| SV-05 | Main 返回可序列化 499 + OPERATOR_CANCELLED；清 pending 保留草稿；未知网络结果保留原正文/键 | IPC/组件通过；真实 Electron 原生取消后草稿保留、改正文后新键通过；未在真实桌面注入网络超时 |
| SV-06 | 可信 Operator Queue 为 human，Agent 仍 automation；导航已有 Session 管理原 Chat 队列；GET 派生实际状态，5 秒刷新 | HTTP→Chat 编辑→SQLite→取消/实际投递→GET 通过；浏览器往返用例 PASS 26.6s，不等同真实 Electron 原生授权 |
| SV-07 | Task预算选择值与global/workflow范围分离，HTTP保留原Task ID | 组件→真实IPC回归及69项回归、typecheck通过，见最新收口记录 |
| SV-08 | 采样完成后取得评估时间，避免合法新样本误判未来 | 原源码红灯、正式Service/资源包绿灯、无overlay桌面8次RESOURCE_READY；原阈值与滞回保持 |


Queue GET 不回写初始干预行，不再 Send。原 POST 同键重放可返回初始 queued，页面 GET 获取当前事实；取消后重放不得恢复消息。Session 不匹配或缺可信投递证据显示 UNKNOWN，读取错误显式失败。

## 命令与实际结果

所有命令经 `rtk proxy powershell -NoProfile -Command '…'` 执行，下表省略包装。日志根为 `C:/Users/11459/AppData/Local/Temp/ao-m5-safe/`，PowerShell重定向日志为 UTF-16LE。Go 可执行文件为 `C:/Users/11459/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.1.windows-amd64/bin/go.exe`；GOCACHE/GOTMPDIR/TEMP 分别是日志根下 go-cache/go-tmp/tests，TMP=TEMP。

| 命令 | 实际结果 | 日志 |
|---|---|---|
| npm run api | 前轮正式生成 exit 0；本轮完整 httpd/apispec/specgen PASS | 前轮工具结果，go-packages-unsandboxed.log |
| npm.cmd run frontend:typecheck | 本轮 tsgo --noEmit 无错误，exit 0 | frontend-typecheck-final.log |
| npm.cmd --prefix frontend test -- src/renderer/components/workflow src/main/operator-ipc.test.ts src/renderer/lib/event-transport.test.ts --maxWorkers=1 | 4 files / 68 tests PASS，40.80s，exit 0 | frontend-workflow-event-final.log |
| go test -p 2 -parallel 2 ./internal/integration -run '^TestM5PlanningCapacityOne$' -count=1 -timeout 3m -v | 前轮PASS60.18s/包65.283s；本轮扩展后PASS40.05s。两用例合跑中另一个夹具首次失败，整组exit1；修复夹具后单跑边界exit0 | go-m5-queue-final.log |
| go test -p 2 -parallel 2 ./internal/httpd/controllers ./internal/service/intervention ./internal/service/execution ./internal/integration ./internal/daemon -run 'TestM5\|TestM2OperatorOperationsAllowlistIsClosed' -count=1 -timeout 8m | controllers/integration/daemon PASS；intervention/execution 为 no tests to run，只算编译 | 前轮工具输出 |
| go test -p 2 -parallel 2 ./internal/service/execution ./internal/service/intervention ./internal/httpd/... ./internal/storage/sqlite/... ./internal/session_manager -timeout 10m | 默认沙箱回环 socket 被拒，精确停止，exit 1，不算整套通过 | go-packages-final.log |
| 上述完整包命令经 require_escalated 获准重跑 | execution/intervention/httpd/controllers/apispec/SQLite/Store PASS；Go 后处理长时间未返回，精确中断，exit 1，Session Manager 最终结果未知 | go-packages-unsandboxed.log |
| go test -p 2 -parallel 2 ./internal/session_manager -count=1 -timeout 10m -v | 独立完整包 PASS，519.114s，SESSION_MANAGER_EXIT=0；本次关闭结果缓存后正常返回 | go-session-manager-nocache.log |
| gofmt 检查与 git -c core.safecrlf=false diff --check | 桌面首轮 89 个源码严格 UTF-8/NUL 检查通过，56 个 Go 文件格式通过，diff --check exit 0；后续桌面数据装配器单独验证 | 工具输出 |

默认沙箱错误为 `connectex: An attempt was made to access a socket in a way forbidden by its access permissions`。精确终止 Go 29000、SQLite 33852、Store 20820，原句柄 exit 1；后续 CIM 无这三个 PID。提升权限后 Go 37892 对应同组回归，HTTP/controllers 通过。Session Manager 子进程结束后 Go 长时间未返回，精确停止后原句柄 exit 1；统筹读取约50MB testlog 的首尾证实进入真实测试，可能停留在后处理，不能标为包测试超时或PASS。未修改防火墙、端口排除或全局网络配置；对照支持执行权限影响，不证明端口耗尽或硬件故障。

## 初次完整套件失败与 M4 基线

初次执行：
```text
go test -p 2 -parallel 2 ./internal/httpd/... ./internal/cli/... ./internal/daemon ./internal/service/execution ./internal/service/intervention ./internal/service/principal ./internal/service/workflow ./internal/session_manager ./internal/integration ./internal/storage/sqlite/... ./internal/adapters/chatdriver/scripted -timeout 10m
```

结果 FAIL / exit 1，go-affected.log，不能由聚焦 PASS 替换成全套通过。

- M5 引入并已修复：Operator 白名单测试的 candidateId 替换；旧 Session 测试 fake 缺 Planner 身份查询；迁移到最新 schema 的旧列断言。聚焦回归已 PASS，完整包本轮复核。
- CLI TestRenderCheckReplacesItsEarlierScreenshot 因 Windows symlink privilege 失败；未改测试绕过，不算通过。
- integration 的23项明确断言失败现已全部取得干净M4逐项对照；另有包10分钟超时，详情见下面补充。归因不等于测试通过。

干净 C 盘 M4 工作树（复制 M5 前）运行：
```text
go test -p 2 -parallel 2 ./internal/integration ./internal/session_manager -run '^(TestClaudeResumeHookConfirmsNativeIdentityForCurrentLaunch|TestM2CreateActivateClaimStart|TestChatSpawnOmitsLegacyWorkflowLease|TestM4ArtifactThroughRealManager)$' -count=1 -timeout 5m
```

结果 FAIL / exit 1，go-m4-baseline.log。ClaudeResumeHook 同样 trusted admission evidence is required；M2CreateActivateClaimStart 同样 claim500；M4ArtifactThroughRealManager 运行 4m57s，包 5m 超时，堆栈在 Windows GetFileAttributesEx→os.Stat→exec.LookPath→Git delivery verification。不能仅解释为原完整包剩余预算不足或唯一硬件根因。基线 Session Manager 过滤项 PASS 3.280s，证明 Planner fake 不兼容确属本阶段问题。

### 2026-11-07 补齐逐项干净 M4 对照

本轮只做分诊，冻结源码不修。新建干净工作树为 `C:/Users/11459/.codex/worktrees/m5-baseline-triage/agent-orchestrator`，HEAD 为 `8384bcc2a48d148e611f6d20b596de4eecd15a2d`；运行前 `git status --porcelain` 为空。此前已对照的两项不重复，本轮将其余 21 个精确用例名用 `|` 拼成锚定正则，执行：

`go.exe test -p 2 -parallel 2 ./internal/integration -run "^(<21项精确名字>)$" -count=1 -timeout 3m -json`

完整正则、cwd、基线和开始时间保存于 [integration-triage-baseline-run.json](C:/Users/11459/AppData/Local/Temp/ao-m5-safe/integration-triage-baseline-run.json)。GOMAXPROCS=2、GOFLAGS=-p=2；使用前述 C 盘 GOCACHE/GOTMPDIR/TEMP/TMP，提升执行环境允许 httptest 回环。实际 **21 FAIL、包 5.309s、BASELINE_TRIAGE_EXIT=1**。23 项原始断言失败逐项对照如下；前轮与本轮均未给失败用例改 fixture：

| 序号 | 失败用例 | 原始失败位置 | 干净 M4 对照 | 根因 |
|---|---|---|---|---|
| 1 | `TestClaudeResumeHookConfirmsNativeIdentityForCurrentLaunch` | `claude_resume_hook_test.go:35` | FAIL / 3.17s（前轮） | A：准入装配缺失 |
| 2 | `TestDelegateEndpointDoesNotDependOnCodexDeviceReconciliation` | `lifecycle_sqlite_test.go:287` | FAIL / 1.67s（本轮） | A：准入装配缺失 |
| 3 | `TestMergedPRUsesSessionManagerOnlyWhenOptedIn` | `lifecycle_sqlite_test.go:319` | FAIL / 0.03s（本轮） | A：准入装配缺失 |
| 4 | `TestSpawnPRKillRoundTrip` | `lifecycle_sqlite_test.go:357` | FAIL / 0.03s（本轮） | A：准入装配缺失 |
| 5 | `TestRestoreRoundTripPreservesMetadata` | `lifecycle_sqlite_test.go:391` | FAIL / 0.03s（本轮） | A：准入装配缺失 |
| 6 | `TestCDCPollerReceivesSessionAndPREvents` | `lifecycle_sqlite_test.go:520` | FAIL / 0.03s（本轮） | A：准入装配缺失 |
| 7 | `TestM2CreateActivateClaimStart` | `m2_e2e_test.go:705` | FAIL / 0.18s（前轮） | B：项目缺少 Git 元数据 |
| 8 | `TestM2BindIsDurableAndSpawnsOnce` | `m2_e2e_test.go:744` | FAIL / 0.06s（本轮） | B：项目缺少 Git 元数据 |
| 9 | `TestM2TwoHarnessesBindTwoSessions` | `m2_e2e_test.go:813` | FAIL / 0.05s（本轮） | B：项目缺少 Git 元数据 |
| 10 | `TestM2QueueDeliversANormalMessage` | `m2_e2e_test.go:868` | FAIL / 0.06s（本轮） | B：项目缺少 Git 元数据 |
| 11 | `TestM2SteerOnTheSteerableHarness` | `m2_e2e_test.go:922` | FAIL / 0.06s（本轮） | B：项目缺少 Git 元数据 |
| 12 | `TestM2SteerRefusedThenQueueableOnTheQueueOnlyHarness` | `m2_e2e_test.go:1012` | FAIL / 0.06s（本轮） | B：项目缺少 Git 元数据 |
| 13 | `TestM2AgentCollaborationMessageAndCrossWorkflowRefusal` | `m2_e2e_test.go:1071` | FAIL / 0.06s（本轮） | B：项目缺少 Git 元数据 |
| 14 | `TestM2TaskRequestRaisedByAgentDecidedByOperator` | `m2_e2e_test.go:1175` | FAIL / 0.06s（本轮） | B：项目缺少 Git 元数据 |
| 15 | `TestM2StopConfirmsTheBindingAndRefusesLaterQueue` | `m2_e2e_test.go:1246` | FAIL / 0.06s（本轮） | B：项目缺少 Git 元数据 |
| 16 | `TestM2InterruptedSessionReportsNonLiveWithAReason` | `m2_e2e_test.go:1303` | FAIL / 0.05s（本轮） | B：项目缺少 Git 元数据 |
| 17 | `TestM2RestartReadsBindingsWithoutInMemoryState` | `m2_e2e_test.go:1399` | FAIL / 0.05s（本轮） | B：项目缺少 Git 元数据 |
| 18 | `TestM2ReconcileAdoptsACommittedSession` | `m2_e2e_test.go:1477` | FAIL / 0.05s（本轮） | B：项目缺少 Git 元数据 |
| 19 | `TestM2CrossWorkflowIsolation` | `m2_e2e_test.go:1565` | FAIL / 0.06s（本轮） | B：项目缺少 Git 元数据 |
| 20 | `TestM2StaleGenerationIsRefused` | `m2_e2e_test.go:1632` | FAIL / 0.05s（本轮） | B：项目缺少 Git 元数据 |
| 21 | `TestM2MCPToolsOverTheRealServer` | `m2_mcp_e2e_test.go:237` | FAIL / 0.05s（本轮） | B：项目缺少 Git 元数据 |
| 22 | `TestM2E2ENeedsNoProviderEnvironment` | `m2_zero_provider_test.go:91` | FAIL / 0.05s（本轮） | B：项目缺少 Git 元数据 |
| 23 | `TestM2E2ENeverTouchesAUserDataDirectory` | `m2_zero_provider_test.go:119` | FAIL / 0.06s（本轮） | B：项目缺少 Git 元数据 |

**A（6项）**：`lifecycle_sqlite_test.go:187` 的 `newStack` 创建真实 SQLite/Manager，却未接入 `SetSessionAdmissionProvider`；ClaudeResumeHook 也调用此装配器。Delegate 自建 Manager 同样未注入。`session_manager/admission.go:34` 的 `prepareSpawnAdmission` 对实现准入存储的普通 Spawn 要求可信准入，缺 provider 返回 `ErrAdmissionRequired`。本组直接报 `spawn: create: trusted admission evidence is required`，Delegate 的 HTTP 包装为 `500 / SPAWN_CREATE_FAILED`。这些用例在待验证的生命周期行为之前失败，不能声称其后续断言通过。

**B（17项）**：`m2_e2e_test.go:345` 已注入 `sqlitetest.NewAdmissionFixture`，不属于 A。其项目装配在 `m2_e2e_test.go:442` 使用 `Path: t.TempDir()`，目录存在但未初始化 `.git`。`ReserveAndClaim → reserveTaskPaths → taskPathReservations → reservationRepoIdentity` 解析该项目时，在 `path_reservation_identity.go:20` 检查 Git 元数据并返回 `ErrArtifactInvalid`；`service/workflow/service.go:836` 的 `mapStoreError` 未专门映射该错误，最终暴露为 `500 / WORKFLOW_CLAIM_FAILED`。表内每项均已实际复现该 HTTP 失败；同组共享的底层错误链另用一项诊断验证，未冒充逐项打印内部错误。

诊断命令为 `go test -p 2 -parallel 2 -overlay <日志根>/m4-cause-overlay.json ./internal/integration -run "^TestM2BindIsDurableAndSpawnsOnce$" -count=1 -timeout 2m -v`。overlay 只把 HTTP envelope 的诊断副本映射到 C 盘临时文件，增加逐层错误打印，不修改仓库源码、断言或响应。实际输出：

```text
M5_TRIAGE_CAUSE *apierr.Error: Failed to claim workflow task
M5_TRIAGE_CAUSE *fmt.wrapError: reserve and claim: invalid delivery artifact
M5_TRIAGE_CAUSE *errors.errorString: invalid delivery artifact
--- FAIL: TestM2BindIsDurableAndSpawnsOnce (1.65s)
```

包 4.168s，DIAGNOSTIC_EXIT=1。

这次 overlay 是诊断运行，与前面的无 overlay 干净 21 项对照分开计数。证据：[逐项原始/基线比较](C:/Users/11459/AppData/Local/Temp/ao-m5-safe/integration-triage-comparison.json)、[原始选择清单](C:/Users/11459/AppData/Local/Temp/ao-m5-safe/integration-triage-selection.json)、`integration-triage-baseline.jsonl`、`integration-triage-baseline.exit`、`integration-triage-cause.log`、`integration-triage-cause.exit`。前两项对应 `go-m4-baseline.log`。

23 项明确断言失败均已在干净 M4 复现；另一个 `TestM4ArtifactThroughRealManager` 的包超时沿用前轮独立 5 分钟基线证据，本轮没有重跑，具体挂起根因仍未知。原完整套件仍为 FAIL；超时后未执行的测试仍未验。本轮不修 fixture、不扩展错误映射、不用已归因代替回归通过。

统筹另报告 principal 2.112s / daemon 5.831s / integration 64.805s 的回归命令 exit0，以及前端 68 项 26.05s / exit0；本轮只记录其转述，不重复执行，不据此改写本地原完整套件的 FAIL。

## 新规划边界证据

| 验收点 | 实际用例 | 结果 |
|---|---|---|
| 正常规划、收尾、SQLite 重开、可信停止后的独立 Planner | m5_planning_e2e_test.go:TestM5PlanningCapacityOne | 本轮扩展后 PASS 40.05s（go-m5-boundaries.log） |
| 未选仓库拒绝、确认重放、StartReplan 同键改正文冲突 | 同上 | 前轮 PASS |
| 成环、图 revision 漂移、停止前旧 Planner turn 拒绝 | 同上本轮新增断言 | 本轮 PASS（go-m5-boundaries.log） |
| 准备后状态漂移、仍运行任务停用拒绝、UNKNOWN 存在时 Prepare/Confirm 拒绝 | m5_plan_boundary_test.go:TestM5PlanRejectsChangedExecutionFacts | PASS 10.71s，包13.409s，exit0（go-m5-boundary-final.log）；内部 Service 创建限定路径 Planner，HTTP/Scheduler/Manager 生成并存状态，正式过期恢复产生 UNKNOWN，不直接改库 |
| Queue 编辑/取消/实际投递/GET；取消后同键重放不恢复 | m5_queue_test.go:m5VerifyQueue，由容量1调用 | 包含本轮重放断言的容量1用例 PASS |

旧 M3 UNKNOWN 恢复 PASS 只证明旧恢复路径，不算新 Prepare/Confirm 证据。

## 桌面与测试规模

frontend/e2e/workflow-navigation.spec.ts 复用 FakeAgent，验证真实 Renderer 模式切换、草稿、键盘 DAG、Chat 编辑/取消和回执；不能证明 Main/Daemon 原生授权。本轮 e2e 类型检查 exit0，浏览器导航 1 passed (26.6s)，exit0，日志 e2e-typecheck-final.log/e2e-navigation-ready.log。首次缺 Chromium，安装112.4MiB headless运行时到C盘任务目录；首次冷加载5秒等待失败，明确就绪等待30秒后通过，没有放宽业务断言。隔离 npm ci 已添加1446包、37s、exit0，日志 desktop-npm-ci.log。

### 完整打包失败

隔离 checkout 内 `npm.cmd run build` 三次均 FAIL，不再盲重试：

1. desktop-build.log：agent-browser 0.38.1 下载 `fetch failed`；daemon/extension 构建已执行。
2. desktop-build-proxy.log：进程级 Undici 代理后 prepackage、Main/preload/Renderer Vite 构建完成，Electron package TLS 失败。
3. desktop-build-final.log：启用官方 Electron 代理开关仍返回 `RequestError: Client network socket disconnected before secure TLS connection was established`，exit 1。

未改依赖、版本、checksum、Forge 配置或全局代理。packager 实际使用内嵌旧版 `@electron/get`/GotDownloader/global-agent；根依赖 5.1.0 的网络行为不能代替该路径。Vite 成功不等同完整 build 成功。

#### 有界下载链与当前网络取证

实际依赖解析为 `@electron/packager 18.4.4 → 内嵌 @electron/get 3.1.0 → GotDownloader / got 11.8.6`；根 `@electron/get 5.1.0` 不属于 packager 的下载实现。当前已安装 Electron 为 `44.7.0 / win32 / x64`，`createDownloadOpts` 和 `getArtifactRemoteURL` 解析出：

- ZIP：`https://github.com/electron/electron/releases/download/v44.7.0/electron-v44.7.0-win32-x64.zip`。
- 校验清单：`https://github.com/electron/electron/releases/download/v44.7.0/SHASUMS256.txt`。
- 第一次失败目标由 prepare-agent-browser 源码确定：`https://github.com/vercel-labs/agent-browser/releases/download/v0.38.1/agent-browser-win32-x64.exe`，日志为内部3次尝试后的 fetch failed，并非 checksum mismatch。

`build-proxy.cjs` 的 Undici dispatcher 只影响 fetch/Undici，不影响 Got。内嵌 3.1.0 在 `ELECTRON_GET_USE_PROXY` 存在时调用 `initializeProxy`；其 `getEnv('GLOBAL_AGENT_')` 优先取 GLOBAL_AGENT 变量并回退标准 HTTP_PROXY/HTTPS_PROXY/NO_PROXY，然后在当前进程初始化 global-agent。不能归因为“内嵌版忽略标准代理”。第三次构建曾启用官方开关；历史日志未记录实际 URL、缓存命中或完整进程环境，无法确定当时是 ZIP 还是 SHASUMS 请求失败，也无法凭当前代理检查反推历史状态。日志中的 Copying/Preparing/Finalizing 并行阶段共享同一 RequestError，不计作三个独立根因。

本轮两次只读检查的范围明确限定为**当前诊断进程**：

| 诊断执行环境 | 默认缓存根 | ZIP | SHASUMS256.txt |
|---|---|---|---|
| 默认沙箱 | AppData/Local/Packages/openai.codex…/AC/#!sandbox…/electron/Cache | 缺失 | 缺失 |
| 获准主机进程 | C:/Users/11459/AppData/Local/electron/Cache | 存在，158293649字节，mtime=2026-11-03T02:45:00.179Z | 缺失 |

两个当前检查进程均显示标准 HTTP_PROXY/HTTPS_PROXY/ALL_PROXY/NO_PROXY 已设置，GLOBAL_AGENT_*、ELECTRON_GET_USE_PROXY、NODE_OPTIONS、NODE_EXTRA_CA_CERTS、NODE_TLS_REJECT_UNAUTHORIZED 未设置；只保存 SET/UNSET，不输出代理地址或凭据。这不是第三次 build 的历史环境。缓存子目录由下载 URL 父路径的 SHA256 决定；3.1.0 即使找到 ZIP 仍需验证 checksum。已实读隔离 checkout 内嵌 `@electron/get/dist/cjs/index.js:59`：`validateArtifact` 下载 SHASUMS 时明确使用 `cacheMode: ElectronDownloadCacheMode.Bypass`，注释为 `Never use the cache for loading checksums, load them fresh every time`。因此本构建路径每次需重新获取清单，补齐校验清单缓存并不足以恢复原 build；已有 ZIP 校验一致也不保证离线或完整打包成功。

仅对官方小校验文件和一次 ZIP HEAD 做探测，每请求15秒、retry=0；没有下载 Electron ZIP，也没有执行第四次 build。第一次诊断脚本成功通过 Undici EnvHttpProxyAgent 得到6740字节清单，但因解析未接受文件名前的二进制标记 `*` 退出1；当次 HEAD 结果未落盘，保持未知。修正临时解析并增加文本/二进制两种格式断言后，只补一次 Got 小文件 GET，复用已下载清单作本地校验，未再发 HEAD 或 Undici 请求。

最终命令 `rtk proxy node <日志根>/build-network-probe.cjs` 返回 exit0，表示**诊断脚本完成，网络并未通过**：官方代理开关=1，globalAgentInitialized=true，进程级 HTTP/HTTPS proxy 均已设置；实际 GotDownloader 请求 SHASUMS 仍失败，`ECONNRESET: Client network socket disconnected before secure TLS connection was established`，未收到重定向。Undici 取得的官方清单中 Electron ZIP SHA256 为 `eee30dc8fa1f5ea95490e59f44e46ea68dd24c6e93d22facf70fe5c2d4c2665c`，只读计算主机现有 ZIP 得到同值；agent-browser 现有文件 SHA256 为 `70bd758b2a5a72b18055f60ff877d2297d036c4104e3bc94af6f7c1ceb941408`，也匹配源码预期。

因此目前能确认的是 Got 路径在当前进程网络条件下仍有 TLS 阻塞，而当前两份二进制的校验值匹配；不能进一步确定是代理兼容、TLS中间层还是对端断开，也不能保证第四次 build 能通过。建议统筹后续单独恢复该下载路径的小文件访问，再决定是否授权新构建；不改版本、checksum、全局代理或配置，不跳过校验，不将临时下载文件手工注入官方缓存。本轮取证至此停止。

证据：[沙箱检查](C:/Users/11459/AppData/Local/Temp/ao-m5-safe/build-download-inspection.json)、[主机检查](C:/Users/11459/AppData/Local/Temp/ao-m5-safe/build-download-inspection-host.json)、[最终网络与校验结果](C:/Users/11459/AppData/Local/Temp/ao-m5-safe/build-network-probe.json)、`build-download-inspect.cjs`、`build-network-probe.cjs`、`electron-current-SHASUMS-undici.txt`，均留在C盘证据目录。

### 第一轮真实 Electron：空态与草稿

在隔离 worktree 的 frontend 执行 `npm.cmd run dev`（含正式 predev），使用：

```text
AO_DATA_DIR=C:/Users/11459/AppData/Local/Temp/ao-m5-safe/desktop/data
AO_DEV_ELECTRON_DIR=C:/Users/11459/AppData/Local/Temp/ao-m5-safe/desktop/data/electron
AO_RUN_FILE=C:/Users/11459/AppData/Local/Temp/ao-m5-safe/desktop/data/running.json
AO_PORT=3002
```

Forge 输出 `Launched Electron app`，daemon 输出 `daemon listening`；真实窗口标题 `Orchestrator.inc (dev)`，Renderer `http://localhost:5173/`。Electron PID 5888、daemon PID 30672，监听 `127.0.0.1:3002` 与 `[::1]:3002`。`GET /api/v1/projects`、`/sessions`、`/workflows` 均 200 且为空。desktop-processes.json 记录 Electron 子进程的 `--user-data-dir` 指向上述 C 盘目录。

通过 computer-use 原生窗口操作验证 Workflows 空态→New workflow→填入 `M5隔离桌面未提交草稿`→Return to session→Workflows；标题保留，缺少项目/目标时 Create planning task 禁用。证据为 desktop-draft-return.png、desktop-draft-return.txt、desktop-dev.log。此证据不覆盖带任务工作台、既有 Session/终端保留、Main→Daemon 可信读取/操作成功或 Windows Hello。

隔离 ao.exe 执行 `preview http://localhost:5173/`，实际输出 `ao preview must run inside an AO session (AO_SESSION_ID is not set)`；CLI exit 2，RTK 包装返回 exit 1，日志 desktop-ao-preview.log。不能据此称预览成功，带 Session 数据后继续验证。

Ctrl+C 后原 dev 句柄 exit 1。默认沙箱 CIM 查询失败后，经获准的只读 CIM/监听查询确认：2026-11-07 18:43:21，checkout 相关进程数 0，3002/5173 监听数 0；desktop-cleanup.json 留证。未按进程名批量结束进程，未留下该轮 Electron/daemon。

### 可自主推进与真人输入边界

带任务工作台、历史 Session/真实 Shell Terminal 保留、未授权读取和业务确认取消已有后续证据，见第二、三轮记录；不能因空数据把这些项目一起转成 NOT_TESTED。新增显式选择的 m5_desktop_fixture_test.go 复用正式 Service/HTTP/Manager/SQLite/Scripted 装配已暂停 Workflow 和已收尾 Planner，只用于生成隔离桌面数据，不计自动回归或原生授权成功证据。生产 Electron、daemon、权限与配置不替换。

成功获取 Workflow 只读授权需要用户在真实 `工作流操作确认` 中选择继续验证，并亲自通过 Windows Hello；每次有副作用的操作同样需要对其精确 method/path/body 完成人工验证。原生验证由 `operatorconsent.New()` 强制执行，当前自动化不能代办认证输入。待隔离数据与可自主验证项就绪后，分别列出需要的动作、正文及预期结果；不提前宣称 HANDOFF PASS。真实 Provider、跨平台打包/升级仍 NOT_TESTED。

继承的大文件申报：m2_full_flow_e2e_test.go 1796 行（fixture混合，超400行1396，本轮补 Conversations 装配以复用正式 Chat）；migrate_burned_versions_test.go 509 行（超300行209，仓库强制登记 migration）；event-transport.test.ts 1217 行（超300行917，在既有 daemon 切换/重连用例增加 Workflow CDC 断言）。fake-bridge.ts 1320行，超400行920，本轮仅补两处默认拒绝 operator 契约。不拆出重复 harness；其他新增测试均低于300行，m5_planning_fixture_test.go 206行低于400行。桌面数据装配器96行，testdata/m5-codex/main.go 109行，均低于400行。

未验项目仍属 M5，不转写成 PASS，不用本报告替代统筹独立验收。

复制对象：desktop-source-manifest.json 共89个backend/frontend变更文件，source/目标sha256逐一核对；清单SHA256 da00ccb15aed94c0f8fa0ed7563af08c41ad38efd18cea9f738fae3337c5c262。未复制统筹文档、配置和临时恢复文件。3个源旁临时文件已验证SHA256备份到日志根 recovery-backups/，再精确移除；备份清单 manifest.json 保留。

第一轮桌面实际使用的 desktop-dev-source-manifest.json 已包含 local_gateway.go 的 gofmt 同步，SHA256 `a4d6c9a58ed0f7580f371ae7268a4c87f5d6f2805ba8ccebe7b272b7e419024e`。后续新增桌面数据装配文件需另外记录，不能继续使用89文件清单声称完全一致。

### 第二轮真实 Electron：带任务、历史 Session 与 Shell 保留

显式运行 `AO_M5_DESKTOP_FIXTURE_ROOT=<日志根> go test -p 2 -parallel 2 ./internal/integration -run '^TestM5DesktopFixture$' -count=1 -timeout 3m -v`，装配 14.43s、包 18.708s、exit 0，日志 desktop-fixture-build.log。它只生成隔离验收数据，不计回归测试或真实 Provider 成功。新建两份无远程仓库，经正式 HTTP/Service/Manager/SQLite/Scripted 规划、候选确认和资源收尾，断言 Workflow 暂停且 Planner Session 已终止；没有直接修改数据库状态。

数据目录为 `C:/Users/11459/AppData/Local/Temp/ao-m5-safe/m5-desktop-fixture-2168354748/data`，Electron userData、running.json 均在其下。Workflow 为 `workflow-2fb604f2-2e57-496b-a7eb-d785d11c5ade`，Planner Session 为 `m2ff-project-a-1`，任务为 `plan-desktop-plan`。真实 Electron/Main/daemon/权限实现保持正式路径。

- 列表与工作台显示 `M5离线桌面验收 active · r3`、Planner succeeded、desktop-worker ready；调度暂停。未授权显示“请授权读取此工作流”，干预按钮禁用，没有自动授权弹窗。
- Planner 详情的 `Open existing session` 打开原 Chat，规划请求与 Scripted 输出保留。已终止 Session 的编辑器禁用，不能计作活跃 Chat 草稿通过。
- 未发送任务干预草稿 `M5原生任务未发送干预草稿` 往返保留。
- 从原 Session 的 `Open shell` 创建正式 Shell Terminal，工作台往返后仍为同一页签、提示符和工作目录。`GET /api/v1/shell-terminals` 前后均一项，handle 都为 `shellterm-762bc07f25359515`；没有通过 UI 执行终端命令。
- 在该 Session 工作树设置实际 `AO_SESSION_ID` 后执行 `ao preview http://localhost:5173/`，CLI exit 0；Browser 面板实际显示 `Session ended — browser preview unavailable.`，所以仅 URL 设置成功，预览展示未通过。

证据：desktop-fixture-dev.log、desktop-fixture-http-before.json、desktop-existing-session.png/.txt、desktop-task-draft-return.png/.txt、desktop-shell-before.png/.json、desktop-shell-return.png/.txt、desktop-shell-after.json、desktop-session-preview.log、desktop-ao-preview.png/.txt。

第二轮 Electron 21148、daemon 19192、Forge 35108；Ctrl+C 后 dev 句柄 exit 1。精确核对残留 pty-host 35280 及其 conhost 24896、pwsh 29320 的父子关系和隔离路径后终止，仅处理本轮进程。`desktop-fixture-cleanup-final.json` 在 2026-11-07 19:30:39 记录 `processCount=0, listenerCount=0`。

### 第三轮真实 Electron：离线 Provider 与准入阻塞

新增 `backend/internal/integration/testdata/m5-codex/main.go` 是显式选择的离线 stdio 协议装配器，不在 Go 自动测试扫描范围内。要求 `AO_M5_OFFLINE_PROVIDER=1`，只支持桌面验证必需的 JSON-RPC 方法；不联网、不读配置或凭据、不执行工具，未知方法返回 -32601。它的 `login status` 自报状态只描述 fixture，不证明真实账号认证。正式 Electron/Main/daemon/Codex Driver 和 OS 授权器不替换。

将该装配器编译至隔离 data/harnesses/bin/node_modules/@openai/codex/bin/codex.exe，通过隔离 data/harnesses/bin/codex.cmd 被正式 managed harness 规则解析。`GET /api/v1/agents/readiness` 实测 Codex 为 ready，diagnostics source 指向该隔离 exe，exitCode=0。第三轮 `npm.cmd run dev` 正常启动，Electron PID 23744、daemon PID 38196，端口 3002；证据 desktop-offline-readiness.json、desktop-offline-processes.json、desktop-offline-dev.log。

正式 `POST /api/v1/sessions` 使用 clientRequestId=`m5-offline-active-chat`、projectId=`m2ff-project-a`、mode=`chat`、harness=`codex`、model=`offline-fixture` 创建验收会话，返回 `SPAWN_CREATE_FAILED: spawn: create: scheduler capacity is exhausted or unknown`，请求 ID `YMINGLIANG/oC6cEd7BGO-000217`。没有盲目重发或改库伪造活跃状态。

随后正式 `GET /api/v1/scheduler/status` 实测 recovered=true、occupied=0、limit=0、reasonCode=`RESOURCE_SOFT_LIMIT`。2026-11-07 19:43:27 样本 totalBytes=34067255296、availableBytes=6212947968，约 18.2%，低于既有 ramSoftPercent=25；不是 Workflow 暂停或旧 Planner 占位造成。证据 desktop-offline-capacity.json。已请求用户释放内存至稳定高于 7.94 GiB，不降低阈值、不关闭其他应用、不替换资源采样器。

第三轮再次观察真实带任务工作台：两项任务和 r3 正常显示，未授权读取明确拒绝，没有自动弹授权框；desktop-offline-workflow-unauthorized.png/.txt 留证。已准备用户只读授权入口：`POST /api/v1/workflows/workflow-2fb604f2-2e57-496b-a7eb-d785d11c5ade/read-access`，空正文，作用域为该 Workflow、有效期 30 分钟。原生确认及 Windows Hello 由用户亲自完成，尚未取得成功证据。

本轮阻塞分别记录：活跃 Chat 草稿/活跃预览依赖真实内存准入恢复；可信读取/操作成功依赖真实原生授权。历史 Session、任务草稿和 Shell 保留已有独立证据，不再笼统列为全部 NOT_TESTED。M5 仍未 HANDOFF / ACCEPTED。

### 第三轮补充：真实业务确认取消与未授权拒绝

统筹指令到达后停止活跃 Chat 启动，不再重试容量拒绝、不改阈值、不自动关闭用户其他应用；环境恢复由统筹向用户询问并转达。本轮只操作业务确认框的“取消”，没有操作 Windows Hello 或“继续验证”。

1. 在原 Workflow 点击 `Resume scheduling`，真实 Main 弹出“工作流操作确认”，包含 `POST /api/v1/workflows/<id>/resume-scheduling`、expectedRevision=2 和幂等键。取消后 Renderer 显示“操作已取消”，按钮恢复 `Resume scheduling`。第一次取消按钮 UIA 索引不可用，重新观察后按当前截图取消；没有盲重发。
2. 填入合法隔离新规划草稿：标题 `M5取消后正文A`、目标 `仅验证原生取消与草稿，不能提交或启动任务`、model=`offline-fixture`、repo=`m2ff-project-a`、实际 baseSHA=`224598dbdd4c54ca623c9da3f9ebbde09e9e5276`、targetRef=`refs/heads/main`。点击创建只进入原生确认，path=`POST /api/v1/workflows/plan`，幂等键为 `4ac7384c-6f7d-444b-a051-a24fa46cc286`。明确取消后，所有草稿字段保留，按钮恢复 `Create planning task`。
3. 将标题改为 `M5取消后正文B`，再次打开原生确认。实际正文已使用 B，幂等键变为 `9344f3e4-5b65-43dc-97f2-db5f9757751a`。再次取消，B 草稿保留。这验证真实 Electron invoke 的取消结果能到达 Renderer 并解除旧请求绑定；499 数值本身由既有 IPC 契约测试断言，本轮未抓取内部 IPC 原始包。
4. 第三轮 daemon 日志解析出 853 条 HTTP 请求：`POST /api/v1/operator/approvals` 为 0、Workflow 写请求为 0；唯一 Session POST 为先前容量拒绝的 500。取消后正式 GET 显示 paused=true、scheduler revision=2、Workflow/Session 各1个，没有新增创建或恢复调度。
5. 对真实 daemon 不带授权分别 GET coordination-summary、messages、task-requests、plan-candidates，四条均 `403 / COLLAB_NOT_AUTHORIZED`，请求 ID 尾号 000418～000421。该证据是正式 HTTP 身份拒绝；与真实页面未授权 Main 读取失败分别记录。

证据：desktop-native-cancel-before.png/.txt、desktop-native-cancel-after.png/.txt、desktop-native-draft-a.png/.txt、desktop-native-draft-a-cancelled.txt、desktop-native-draft-b.png/.txt、desktop-native-draft-b-cancelled.png/.txt、desktop-native-cancel-http.json、desktop-native-cancel-state-after.json、desktop-offline-anonymous-reads.json。对这些实际观察做一致性断言输出 `newKey=true, approvalPosts=0, workflowWrites=0, paused=true, revision=2, sessionCount=1, workflowCount=1, anonymous403=true`，保存 desktop-native-cancel-verification.json；它是验收证据核对，不新增回归测试数量。

`operator-ipc.test.ts` 既有“拒绝其他窗口、子 frame、伪装 app 来源和远程来源”已包含在4文件/68测试的 PASS 中；本轮复核日志末尾及生产 requireTrustedSender 接线。没有额外构造真实恶意窗口/iframe攻击，不能把该项写为真实 Electron 来源攻击验证通过。真实网络未知结果保留仍只有组件/IPC契约证据。

### 本轮收口与剩余边界

最终 `desktop-final-source-manifest.json` 覆盖91个 backend/frontend 变更文件（89个既有文件加2个桌面fixture），主仓与隔离副本逐文件字节一致，严格 UTF-8/NUL 检查通过，SHA256 `11d3b780734ff333062b539835ef68688071d54a94b0433084d715d01c7b4f0b`。与 `git diff --name-only` 加 `git ls-files --others --exclude-standard` 的源码变更集合比对一致；实际输出 `MANIFEST_COVERS_CHANGED_SOURCE=91, GOFMT_PASS=58, DIFF_CHECK_EXIT=0`。两份新fixture SHA256 分别为 `28763400095fe44be5471e4d04fc5b0181c28e503d8b1bc75a3e2ac722ca8c29`、`63d2745b775c94522e600775a07c6a9478c43c2ca72f38300441c6e545e47099`。

前端实现未再改动，复核 frontend-typecheck-final.log、e2e-typecheck-final.log、frontend-workflow-event-final.log 的已有结果，不重复运行相同已通过套件。contracts/report 的后续编辑不计入源码副本清单。

第三轮清理前精确记录13个启动链进程，包括 Electron 23744、daemon 38196、Forge 33988/34032，见 desktop-offline-processes-before-cleanup.json。向本轮 dev 句柄58616发送 Ctrl+C，返回 exit1。2026-11-07 20:11:52 再查 CIM 和3002/5173监听，`processCount=0, listenerCount=0`，见 desktop-offline-cleanup-final.json；没有追加批量kill，没有残留本轮Provider/pty-host。隔离worktree和数据保留用于后续M5验收，未归档、未提交。

仍未通过：完整build三次网络/TLS失败；初次完整Go套件并非全绿；integration的23项断言失败已有逐项M4对照，M4工件超时的具体根因仍未知。仍未验证：活跃Chat草稿/活跃Browser预览、成功Windows Hello授权后的实时只读和操作链、真实Queue操作往返与重新连接、真实付费Provider和跨平台打包升级。空态、Mock、历史Session、原生取消及正式HTTP403证据均不替代这些项目。本轮不是HANDOFF PASS，M5不关闭、不进入M6。

### 分诊收口记录

本轮仅更新主仓 m5-report.md 与 m5-contracts.md；源码、配置和生成物均未修改。重新逐文件核对91个冻结源码的主仓/隔离副本共182份内容，mismatches=[]，冻结清单SHA256仍为 `11d3b780734ff333062b539835ef68688071d54a94b0433084d715d01c7b4f0b`，见 `triage-source-freeze-verification.json`。

2026-11-07 20:51:51 的获准只读检查：基线 `git status --porcelain` 为空、HEAD仍为8384bcc2、`git diff --check` exit0，CIM查询 `go.exe` / `*.test.exe` 数量为0，见 `integration-triage-final-cleanup.json`。原生archive_worktree首次因基线已成为本聊天主工作树而拒绝；经原生attach_worktree恢复m5-desktop-safety后再次归档，list_artifacts确认基线为 `archived_worktree`、桌面工作树仍为 `worktree`。见 `integration-triage-archive.json`；未手工删除工作树，C盘日志/overlay/对照/诊断清单均保留。

本轮未启动桌面、未再试活跃Chat、未操作真人认证、未执行第四次build、未提交。内存与真人授权仍待用户/统筹；两项分诊结果不构成M5 PASS/HANDOFF。

### 第四轮真实 Electron：稳定预检后正式准入再次阻塞

统筹取得新的可用内存证据后，本轮按明确指令自行复查，不把释放内存视为额外授权门禁。三次 CIM 采样分别在 2026-11-07 21:05:13、21:05:23、21:05:34（北京时间），可用内存为8.700、8.685、8.675 GiB，比例27.421%、27.375%、27.343%，均高于原25%门禁；E盘队列/读/写均0，隔离进程与3002/5173监听均0。命令为 `rtk proxy powershell -NoProfile -File <日志根>/desktop-active-preflight.ps1`，exit0；完整结果见 `desktop-active-preflight.json`。

沿用 `m5-desktop-safety/agent-orchestrator` 和 `<日志根>/m5-desktop-fixture-2168354748/data`，AO_DEV_ELECTRON_DIR=data/electron、AO_RUN_FILE=data/running.json、AO_PORT=3002、AO_M5_OFFLINE_PROVIDER=1，Go仍为GOMAXPROCS=2、GOFLAGS=-p=2，缓存和临时文件均在C盘。仅启动一次 `npm.cmd run dev` 及其串行predev必需步骤，没有执行完整build/package。第一次工具启动申请在自动审批超时后未执行；按工具允许仅重试一次相同命令，实际启动句柄39051。源码91文件继续冻结。

启动前已核对managed包装器只执行 `data/harnesses/bin/node_modules/@openai/codex/bin/codex.exe`，离线二进制为4122624字节，路径/哈希保存在 `desktop-active-fixture-identity.json`。启动后正式 `GET /api/v1/agents/readiness` 显示Codex ready，其认证诊断的source实际指向同一隔离exe、exitCode=0。这只证明离线fixture解析及协议探测，不是账号认证或真实Provider调用成功。

Computer Use只做窗口枚举，返回该checkout唯一 `Orchestrator.inc (dev)` 窗口（id12193510）；未进行任何UI输入。CIM确认Electron主PID27976、daemon PID10964、Forge/Vite PID20352；daemon实际可执行文件为 `frontend/daemon/dev-1794056964443-29784/ao.exe`。3002由10964监听127.0.0.1和::1，5173由20352监听127.0.0.1；子Electron的user-data-dir指向隔离data/electron。正式GET scheduler/status、agents/readiness、sessions、projects、workflows、shell-terminals均200，记录在 `desktop-active-before.json`。这些响应未取得X-Request-Id头，因此本轮不补造请求ID。重定向日志 `desktop-active-dev.log` 只保留到Vite preStart阶段，未包含完整运行期HTTP记录；启动和身份结论以实际进程、窗口枚举和正式HTTP响应为据。

**停止点**：21:12:01的正式scheduler样本为recovered=true、occupied=0、limit=0、reasonCode=RESOURCE_SOFT_LIMIT，totalBytes=34067255296、availableBytes=8231665664，即7.666 GiB / 24.163%，再次低于原25%门禁（约7.932 GiB）。21:12:05的CIM独立采样availableBytes=8207491072。启动前余量未能在本次运行中维持；不能把差值完全归因为应用自身。按本轮明确停止条件立即停止，**没有发送Session创建请求、Workflow写请求、ao preview命令，也没有循环重试**。`desktop-active-stop-decision.json` 记录STOPPED_RESOURCE_SOFT_LIMIT；活跃Chat草稿往返、同一活跃Session/终端身份、活跃预览实际展示均仍NOT_TESTED。普通离线Session也未创建，不计Workflow授权写入成功；Windows Hello和真实Workflow Queue仍待真人授权。

清理：向唯一dev句柄39051发送Ctrl+C，工具实际exit1，保留该退出码；没有按进程名kill。首次只读清理查询把启动前即存在的4个Codex诊断工具进程因命令行含工作目录而误计，原结果保留在 `desktop-active-cleanup-query-first.json`，未终止这些进程。随后精确匹配桌面/Forge/fixture路径，并处理系统进程CommandLine为空的情况。最终 `desktop-active-cleanup-check.ps1` 在21:16:47返回exit0，processCount=0、listenerCount=0，见 `desktop-active-cleanup-final.json`。

停止后执行 `rtk proxy python <日志根>/desktop-active-readback.py`，SQLite URI使用mode=ro并启用query_only，exit0。数据库仍只有Session `m2ff-project-a-1`（is_terminated=1、activity_state=exited）和Workflow `workflow-2fb604f2-2e57-496b-a7eb-d785d11c5ade`（active、revision=3），与停机前正式HTTP身份一致，见 `desktop-active-after-data.json`；本轮没有创建Shell Terminal。再次核对主仓/隔离副本的91源码共182份哈希，mismatches=0，见 `desktop-active-source-verification.json`。本轮只更新report/contracts，未修改源码、配置、阈值，未提交；仍未完成M5桌面验收，不宣告PASS/HANDOFF。

### 完整 build 下载阻塞：新一轮进程级设置验证

本轮限定于下载环境，未启动Electron/Chat、未派Agent、未修改仓库源码、依赖、版本、锁文件、node_modules、全局代理或配置；未关闭TLS/证书/checksum校验。只有实际GotDownloader取得新的官方清单并完成校验，且资源条件适合，才获准执行一次新的正式build。本轮未满足前一条件，因此**没有执行第四次npm run build**；原三次原生完整build仍为FAIL，本节小文件成功结果不能替代它。

先只读比较当前进程环境：Got与Undici选择相同的本机HTTP代理，代理无内嵌认证信息；GitHub与release-assets.githubusercontent.com均未命中NO_PROXY。Node=v22.20.0，TLS默认最小1.2/最大1.3，证书校验开启。NODE_USE_ENV_PROXY虽存在，但运行时 `process.allowedNodeEnvironmentFlags.has('--use-env-proxy')` 实际为false，不能据环境变量存在就认为原生HTTPS已使用代理。完整脱敏结果见 `got-process-inspection.json`；未打印代理地址或凭据。

所有网络请求均指向原官方 `https://github.com/electron/electron/releases/download/v44.7.0/SHASUMS256.txt`，每请求12或15秒，retry=0，不下载大ZIP；高I/O与探测串行。

| 本轮命令（均经rtk proxy，日志根同前） | 实际结果 | 能证明的边界 |
|---|---|---|
| `node <日志根>/got-tunnel-diagnostic.cjs` | exit1 / ECONNRESET | 仅透明观察网络事件，不替换原下载器或响应；GitHub CONNECT200、TLSv1.3 authorized=true；重定向目标release-assets.githubusercontent.com的CONNECT也200，但后续TLS握手重置 |
| `node --tls-max-v1.2 <日志根>/got-official-checksum-probe.cjs` | exit1 / ECONNRESET | 原GotDownloader+官方global-agent初始化；仅将当前Node进程TLS上限设为1.2，保持证书校验。仍在重定向下载域名失败，未进入sumchecker成功路径 |
| `node --require=<日志根>/build-existing-proxy-agent.cjs <日志根>/got-official-checksum-probe.cjs` | exit1 / ECONNRESET | 原GotDownloader不变；临时预加载仅使用已安装https-proxy-agent7.0.6/http-proxy-agent7.0.2公开接口设置当前进程Agent，并停用该进程旧global-agent初始化。恢复默认TLS1.2～1.3仍同处失败；该helper只作失败诊断，未用于build |
| `node <日志根>/undici-official-control.cjs` | exit0，HTTP200，6740字节，cachedZipSHA256Verified=true | 当前21:36:38（北京时间）Undici经相同代理成功跳转至同一下载域名，原sumchecker按新下载的官方清单验证现有ZIP通过；这仍不是Got或完整build成功 |

第二种设置复用现有依赖，不安装或升级任何包，不伪造响应；其HTTP/HTTPS Agent设置仅存在于该短命诊断进程。临时探测脚本让下载或校验失败返回非零；三次Got运行均明确exit1，不把“脚本已结束”写成网络通过。当前Undici清单保存为 `undici-official-current-SHASUMS.txt`，未注入Electron缓存。内嵌get3.1.0对SHASUMS的 `cacheMode: Bypass` 行为未变，补缓存仍不能解决原构建。

结论：当前代理隧道能建立，原Got路径及现有HTTPS代理Agent路径在下载重定向后的TLS阶段失败，而Undici在同一时点成功。证据将问题缩小到传输路径相关的网络/TLS兼容阻塞，尚不能确定是代理、对端还是具体握手差异，也不能认定换代理或再次build就能通过。两项进程级修复假设均未验证成功，本轮停止，不继续堆叠TLS参数、替换下载器或扩大尝试。

证据：`got-tunnel-diagnostic.json`、`got-official-tls12-result.json`、`got-official-installed-agent-result.json`、`undici-official-current-result.json`及同名C盘临时脚本。21:37:45执行 `rtk proxy powershell -NoProfile -File <日志根>/build-network-cleanup.ps1` 返回exit0，probeOrDesktopProcessCount=0、desktopListenerCount=0，HEAD仍为8384bcc2a48d148e611f6d20b596de4eecd15a2d，见 `build-network-cleanup.json`。所有原始探测进程均已返回退出码，无残留，无需kill。主仓/隔离副本91源码共182份哈希再次一致，mismatches=0，见 `build-network-source-verification.json`，冻结清单SHA256仍为11d3b780734ff333062b539835ef68688071d54a94b0433084d715d01c7b4f0b。

本轮仅更新report/contracts；未提交或发布。下载成功门禁未满足，故未进入新的build资源检查和构建步骤；完整打包、活跃Chat及真人授权验收状态均未转为PASS，M5仍未HANDOFF。

### SV-07 / SV-08 修复与释放内存后的桌面收口（2026-11-07）

本节为最新执行方状态。SV-07、SV-08 的修复和直接受影响回归已通过；真实 Electron 活跃离线 Chat 草稿、Session 与 Shell Terminal 往返保留通过；Browser 预览实际展示未通过。M5 仍未 PASS / HANDOFF，不进入 M6。

用户回复已释放后，三次预检约21.67 GiB；上一实例正式HTTP仍在约18.48 GiB可用内存下返回 RESOURCE_UNKNOWN，按既有停止条件未创建 Session。`desktop-active-resumed-before.json` 保存原响应，`desktop-resumed-cleanup.json` 记录21:52:20进程/监听均0，原句柄52867的Ctrl+C退出码为1。该HTTP投影不包含完整CPU ticks、PreviousAt或底层资源原因码，不能倒推那一轮所有UNKNOWN均由SV-08引起。

SV-07仅修改 `frontend/src/renderer/components/workflow/WorkflowBudgetPanel.tsx`：Task Select值使用 `task:${id}`，取范围时剥离此前缀，HTTP仍用原始Task ID；合法ID `global` / `workflow` 不再误选全局或Workflow预算。一个真实组件→Hook→WorkflowAction→registerOperatorIpc回归覆盖Task global、Task workflow、全局、Workflow四个目标，核对独立策略回显、确认method/path/body、审批payload及最终HTTP原文；仅替换系统确认和HTTP，不复制生产分支。

SV-08仅修改 `backend/internal/service/scheduler/resources.go`，在Sample返回后重新取得 `Clock().UTC()` 作为评估时间；未放宽未来/过期判断、阈值、失败替换或滞回。一个Service回归使用正式SQLite和RefreshResources/Status/MaintainLeadership，验证采样中前进1秒、首样闭锁、健康样本恢复、真正未来/过期/失败/gap/不完整样本闭锁、再次恢复，以及重复Refresh不推进滞回。`fixture_test.go`暂改已恢复，不在最终改动内。

以下命令均经 `rtk proxy powershell -NoProfile -File <日志根>/...` 执行，Go保留GOMAXPROCS=2、-p 2、-parallel 2，日志根同上；此次收口只复核已有输出，不重复跑整套。

| 命令 | 实际输出 | 证据 |
|---|---|---|
| `run-sv07.ps1 -Phase red` | FAIL：选择task: global，金额期望3、实际1 | sv07-red.log / .exit |
| `run-sv07.ps1 -Phase regression`；底层 `npm test -- src/renderer/components/workflow src/main/operator-ipc.test.ts src/renderer/lib/event-transport.test.ts --maxWorkers=1` | 4 files / 69 tests passed，34.94s，exit0 | sv07-regression.log / .exit |
| `run-sv07.ps1 -Phase typecheck`；底层 `npm run typecheck` | tsgo --noEmit，无错误，exit0 | sv07-typecheck.log / .exit |
| `run-sv08.ps1 -Phase red`；最终用例配原HEAD resources.go overlay | 首样闭锁；healthy_1实际RESOURCE_UNKNOWN、期望RESOURCE_READY，FAIL，exit1 | sv08-service-red.log / .exit；sv08-original-overlay.json |
| `run-sv08.ps1 -Phase green` | TestRefreshResourcesAcceptsSampleCompletedAfterRefreshStarted PASS；包4.100s，exit0 | sv08-service-green.log / .exit |
| `run-sv08.ps1 -Phase packages`；底层 `go test -p 2 -parallel 2 ./internal/service/scheduler ./internal/adapters/resources -count=1 -timeout 3m` | scheduler 8.778s、resources 2.826s均ok，exit0 | sv08-service-packages.log / .exit |

SV-07类型检查曾因隔离副本product-ui依赖缺失而失败；按既有lockfile执行 `npm ci --offline --ignore-scripts --no-audit --no-fund`，added 123 packages in 9s / exit0，未改依赖声明或锁文件、未做symlink。随后补齐测试真实响应必填字段并去掉无效ByRole参数，最终第三次typecheck通过；初始失败保留在sv07-typecheck-initial-fail.log、sv07-typecheck-fixture-fail.log。新增回归所在文件workflow-behavior.test.tsx为192行、resources_test.go为167行，均低于300行。早期sv08-red.log曾被覆盖为PASS，不能作为红灯依据；上表最终原源码overlay红灯有效。

#### 资源采样的在线证据边界

先以临时Go overlay增加JSON日志，保留判断分支，用 `run-sv08-daemon.ps1 -Phase build` 构建成功。诊断daemon PID31748的 `sv08-raw-resources.json` 保存13个完整Resources样本；首样resource_first_sample、容量0，第三份健康样本恢复容量1；其中样本ObservedAt比refreshStartedAt晚约0.6107ms，新评估时钟正确接纳。在线未出现gap或真正未来样本，对应闭锁由Service回归证明。该overlay只计观测证据，不冒充无改动正式二进制；POST /shutdown返回202、句柄20356 exit0，日志daemon stopped cleanly。首个诊断启动曾因PowerShell把stderr INFO作为错误中止，无残留。

随后22:39:51至22:40:12正式桌面预检可用18.324～18.365 GiB，E盘队列0、隔离进程/端口0，见desktop-sv08-preflight.json。普通 `npm run dev` 使用无overlay源码及必需predev，非完整build/package；Electron PID37604、daemon PID35752、Forge/Vite PID31028。`sv08-desktop-samples.json` 连续8次正式HTTP均200 / RESOURCE_READY / limit1 / maxNewClaims3 / recovered=true。global策略revision2、maxConcurrent1、RAM soft25%/hard15%/floor2GiB保持原值。

#### 活跃 Chat、终端与预览

通过真实Electron正常UI仅创建 `m2ff-project-a-2`，使用已有M5 Offline Fixture，未联网或调用付费Provider。原 `m2ff-project-a-1` 保持terminated；Workflow `workflow-2fb604f2-2e57-496b-a7eb-d785d11c5ade` 始终active / revision3。

| 验收 | 实际结果 | 证据 |
|---|---|---|
| 活跃Chat草稿往返 | Working期间输入未发送草稿，Workflows→Return to session后原文完整保留 | sv08-active-chat.jpg/.txt、sv08-draft-before.jpg/.txt、sv08-workflows-between.txt、sv08-draft-after.jpg/.txt |
| 活跃Session及Shell Terminal往返 | Terminal 1提示符可见；Workflows往返后仍在a-2，handleId均为shellterm-c6ef444f86f67eee，workingDir和createdAt相同；没有在终端UI输入命令 | sv08-terminal-before.jpg/.txt、sv08-terminal-between.txt、sv08-terminal-after.jpg/.txt；sv08-final-terminal-before.json、sv08-final-terminal-after.json |
| `ao preview http://localhost:5173/` | CLI exit0；a-2的previewUrl=http://localhost:5173/、previewRevision=1；Browser面板仍显示Enter a URL or click one in the terminal.，无页面，**未通过** | sv08-preview.ps1、sv08-preview.exit、sv08-preview.log（0字节）；sv08-preview-empty.jpg/.txt；终端往返后的同一面板截图和正式Session响应 |

预览注册事实已通过正式HTTP读取确认，页面实际展示没有成功；现有证据不足以区分CDC/Renderer事件刷新、Browser标签创建及页面加载的具体失败环节。`desktop-sv08-dev.log`仅1460字节UTF-16启动输出，止于Checking your system；没有本次preview的事件、导航或加载错误日志。隔离数据目录检索到的其他*.log仅是Chromium存储日志，未据此猜测根因；没有为此重启、长时间盲点或运行期间改源码。

#### 清理与冻结复核

正式 `DELETE /api/v1/shell-terminals/shellterm-c6ef444f86f67eee` 返回204，`POST /api/v1/sessions/m2ff-project-a-2/kill`（deleteArtifacts=false）返回200、ok=true、freed=true。随后HTTP确认两个Session均terminated，shellTerminals=[]，Workflow仍active/revision3。见sv08-lifecycle-cleanup.json、sv08-final-after-lifecycle-cleanup.json。

dev句柄57100经Ctrl+C返回exit1，保留实际退出码。`rtk proxy powershell -NoProfile -File <日志根>/sv08-cleanup-check.ps1` 于23:27:54输出processCount=0、listenerCount=0、exit0，匹配隔离Electron/daemon/Forge/fixture/Provider路径及3002/5173，无批量kill。工作树与隔离数据留作后续验收。

`rtk proxy python <日志根>/sv08-final-verify.py` 最终exit0：SQLite mode=ro + query_only确认仅原a-1和新增a-2，二者exited/is_terminated=1/runtime_handle_id为空；a-2仅1个turn、1条human用户消息，未发送草稿不在消息表，关闭后该turn为failed，不标作Provider正常完成。Workflow身份与revision不变，见sv08-final-data-verification.json。

SV-07/08只同步4个目标源码/测试文件到主仓，新冻结 `desktop-sv0708-source-manifest.json` 为93文件，SHA256 `0a3f37231692a3eb7879892cd6ed9a0c1c38a32672ea610d264ac2cede80ec8f`；旧91文件清单保留。23:30:38再次比对主仓和隔离副本共186份内容，mismatches=[]，严格UTF-8、NUL=0，双仓HEAD均8384bcc2、git diff --check均exit0，见sv08-final-source-verification.json。运行桌面期间未修改源码/测试。本次收口仅更新执行方report/contracts，未改统筹文档、配置、阈值、未提交或发布。

证据采集首次在默认沙箱遇到PowerShell AuthorizationManager和回环WinError10013；只读核查经require_escalated后成功，没有调整ExecutionPolicy。冻结检查首次默认沙箱无法访问E盘Git，随后默认GBK解码RTK输出失败，显式UTF-8后成功；这些工具失败不算业务测试失败，也不覆盖原始测试日志。

保留门禁：预览实际展示未通过；Windows Hello正向授权、授权后的实时只读刷新、受保护Workflow Queue往返/重连仍待真人。普通离线Chat草稿与终端保留不能替代这些项目。完整build仍三次FAIL，不做第四次；初次完整Go套件未全绿，M4工件超时原因未明；真实付费Provider和跨平台打包升级未验。统筹独立复测单列记录，本节不预先宣称通过。


### SV-01 / SV-09 正式桌面收口（2026-11-08）

本轮使用原隔离数据 `m5-desktop-fixture-2168354748/data`、原 worktree 和无 overlay 的正式 dev 实例，未修改业务源码、测试或配置。主进程 Electron 33100、daemon 32360、Forge/Vite 1960；启动句柄77080。以下证据根均为 `C:/Users/11459/AppData/Local/Temp/ao-m5-safe/`，主日志 `desktop-sv09-dev.log` 为 UTF-16 且有 PowerShell 折行，按 `time=` 合并记录后分析。

**原生只读授权及重连：通过。** 原三次 confirm 均403，耗时60.0744s、60.0407s、60.0292s；原始403 envelope未采集，只保存状态/耗时与Main通用错误，不推定Windows Hello不可用，也不归咎用户未输入。00:35:09由用户本人新发起，00:35:18.729 confirm200 / 9.2395991s，00:35:18.764 read-access200，随后coordination-summary/messages/task-requests均200。此成功不是自动第四次盲重试。请求固定为该Workflow的POST read-access、空正文；凭证保持Main私有，工具未提取。

00:39:08通过真实View→Reload重载Renderer，原SSE请求正常结束；重新打开Workflows并选择原Workflow后，00:42:09.810～.817三项受限GET再次200，没有新增审批。重载回到首页且需重选Workflow，不据此宣称页面选择跨重载保留。证据：sv09-authorized.jpg/.txt、sv09-reload.jpg/.txt、sv09-reconnected-workflow.jpg/.txt、sv09-restore-and-authorization.log、sv09-final-runtime-evidence.json。

**事件触发自动读取：通过。** 00:59:31为普通离线会话创建和会话路由挂载阶段的第一批读取，不计自动刷新证据；00:59:34的scheduler_updated（CDC seq321/323/332）之后，00:59:36.068～.070出现另一批coordination-summary/messages/task-requests 200，请求尾号001895～001897。该区间唯一UI动作是发送创建a-3的初始离线消息，之后只观察；未点击Workflows/刷新/再授权。`ShellOutlet`将已加载Workbench保留挂载并隐藏，`useWorkflowCoordination`与Collaboration无refetchInterval、retry=false，QueryClient的refetchOnWindowFocus=false；event-transport对scheduler_updated真实失效当前daemon的workflows查询。三项HTTP、CDC和实际挂载路径共同构成证据，不仅依赖时间接近。30分钟授权不延长；超过原有效期的拒绝不判为回归。

**SV-09根因与合法目标：通过。** 前轮 `ao preview http://localhost:5173/` 指向AO Renderer自身origin，Main的isAllowedBrowserURL明确拒绝；openTab在创建标签前抛NAVIGATION_FAILED，useBrowserView的通用catch未显示该错误，因而表现为空面板。涉及四个Browser文件与M4 HEAD归一化内容一致，见sv09-baseline-evidence.json。`run-sv09-origin.ps1`底层 `npm test -- src/main/browser-view-host.test.ts -t 'isAllowedBrowserURL' --maxWorkers=1` 实际1file / 2passed / 276skipped、5.56s、exit0；没有放宽同源规则，也没有把前轮失败改成通过。

原a-2恢复通过正式POST /restore返回409，服务日志为 `read native conversation history: thread/read: app-server error -32601: not implemented by M5 offline fixture`；原409body未保存，未重复恢复或伪造历史。归档a-2上的 `ao preview m5-preview.html` exit0仅登记了字面值，旧文件后来不存在，不能计实际展示。

确认RESOURCE_READY / occupied0、M5 Offline Fixture后，通过真实Electron正常入口仅创建a-3。活跃workspace写入纯静态HTML，通过 `sv09-active-preview.ps1`执行正式 `ao preview m5-preview.html`，exit0，previewRevision1，URL为 `http://ao-preview.nuzgmzrnobzg62tfmn2c2yjngm.localhost:3002/m5-preview.html`。Browser实际显示标题、会话a-3和输入框。填写 `SV09-往返保留-20261108`，Workflows→Return to session后原标记、URL和会话均保留。证据：sv09-preview-before.jpg/.txt、sv09-workflows-between.jpg/.txt、sv09-preview-after.jpg/.txt；HTML保存为sv09-preview-artifact.html。统筹独立核对01:06:15～01:10:57的435条HTTP，仅有chat-view/readiness写，无创建/绑定，见supervisor-sv09-preview-check.json。

**清理与冻结。** `sv09-close-session.py`正式POST a-3/kill（deleteArtifacts=false）返回200 / ok=true；Ctrl+C启动句柄77080实际exit1。`rtk proxy powershell -NoProfile -File <证据根>/sv09-cleanup-check.ps1`于01:21:09输出processCount=0、listenerCount=0、exit0。`rtk proxy python <证据根>/sv09-freeze-check.py`于01:22:18输出93files、mismatches=[]、双仓diffCheckExit0；只读SQLite确认a-1/a-2/a-3均terminated、runtime_handle_id为空，原Workflow仍active/revision3。93文件冻结SHA256保持 `0a3f37231692a3eb7879892cd6ed9a0c1c38a32672ea610d264ac2cede80ec8f`。关闭动作首次自动审批超时未执行，允许一次重试后成功；不算业务拒绝。

**SV-06续验前提。** 当前预置desktop-worker为worker-model/high、无活跃绑定Attempt；原离线目录仅offline-fixture且无显式effort选项。不能用普通a-3冒充Workflow Queue验收，也不能直接改活跃DB绑定。下一步只调整 `m5_desktop_fixture_test.go` 的测试任务模型/effort，生成新的容量1、保持暂停的scratch工作流，保留旧数据；生产Manager/协议/准入不改。正式桌面恢复调度和Queue仍各自需要精确method/path/body的本人Hello。该测试fixture调整单独列入下一份冻结，不悄然覆盖本轮93文件基线。

本轮SV-01/SV-09通过不代表M5整体完成。SV-06待下一轮正式绑定与Queue闭环；完整build原三次FAIL、完整Go套件缺口、M4工件超时、真实Provider和跨平台未验保持。


### SV-06 续验准备交回（2026-11-08，未启动下一桌面）

只修改测试装配器 `backend/internal/integration/m5_desktop_fixture_test.go`：在原m5Patch结果上将desktop-worker设为model=offline-fixture、effort为空，使下一次正式Manager启动匹配现有离线模型目录。原测试数据、生产实现、离线Provider协议、内存阈值和配置不变。文件当前4971字节/100行（按换行split计，含末尾空项），未新增脚手架回归测试。

`rtk proxy powershell -NoProfile -File <证据根>/run-sv06-fixture.ps1`，底层 `go test -p 2 -parallel 2 ./internal/integration -run '^TestM5DesktopFixture$' -count=1 -timeout 3m -v`：TestM5DesktopFixture PASS 21.50s，包ok 29.590s，exit0。此结果仅证明新数据通过正式规划、确认及收尾链生成，不算Queue通过。证据sv06-fixture.log/.exit。

新scratch为m5-desktop-fixture-2231360002/data；Workflow `workflow-d3867d27-4231-462d-adc5-85ae8408d9a1`，调度paused=true/revision2、容量1，原Planner已terminated，desktop-worker ready、offline-fixture/空effort。复制既有离线fixture二进制，SHA256仍5f5e8a5538508bfb0bbe5718d10768a8bd348cc66da80eda76a7153177c207aa；未调用真实Provider、未修改活跃DB。sv06-prepared.json保存身份与下一准确操作。

单文件已同步主仓，93文件双树186份SHA一致、gofmt无输出、双仓diff --check exit0。原desktop-sv0708-source-manifest.json保持；新desktop-sv06-source-manifest.json最终SHA256 `a89b63f2404a271bef22f89c6056267552dda7a94379e111f4757254bbd7df30`。清单初版误沿用该文件旧bytes/lines，已仅校正元数据并更新核验记录，源文件SHA保持4f7d2efaa26cea9a13412ae5740fb5e5f4f8819f410df50c9d6e207d20daa0f4，见sv06-source-verification.json。

01:33:15～01:33:37预检可用内存15.865/15.783/15.812 GiB、E盘队列0，隔离进程[]、3002/5173监听[]，exit0，见desktop-sv06-preflight.json。**下一桌面未启动、无活跃验收进程；SV-06仍OPEN。** 预检仅描述此时刻，延后启动应重新确认资源。

续验顺序：在同一执行会话启动唯一正式隔离桌面；通过普通UI选择新Workflow；核对并发起POST `/api/v1/workflows/workflow-d3867d27-4231-462d-adc5-85ae8408d9a1/resume-scheduling`，正文 `{"expectedRevision":2,"reason":"用户调整工作流调度"}`，由本人完成Hello。正式调度器负责Claim/Bind/Start，验证真实Attempt/Session/generation，再准备Queue文本和clientMessageId，核对实际POST interventions的完整正文后单独本人Hello。只读授权如需用于新Workflow单独发起，不能沿用旧Workflow授权。随后只使用原绑定Session编辑/取消队列、Workflows往返和Reload；读取不得再次Send/Spawn，最终正常停止并精确清理。

普通准备均已自主完成；已发出一个本人就绪问题，等待“已准备”后继续，不重复请统筹授权。业务确认可由执行方点击，PIN本人完成。没有宣称正式绑定或Queue通过，没有重跑已通过全套或第四次完整build。


SV-06启动工具阻塞补记：用户已回复本人Hello“已准备”。desktop-sv06-start.ps1两次require_escalated调用均在自动审批审核截止前未完成，命令未执行；默认沙箱同命令exit1 / AuthorizationManager check failed。检查desktop-sv06-dev.log不存在，未发起resume-scheduling或Queue、未再次弹Hello。已请用户手动运行准备好的同一启动命令，保持窗口打开并回复“已启动”；用户启动后继续同一chat，无需重新授权测试。该阻塞不是Windows Hello失败，也不推翻SV-01/SV-09。机器进程/监听最近预检为0，尚无新启动成功证据；SV-06仍OPEN。证据sv06-launch-blocked.json。原93文件新冻结及参数不变。
