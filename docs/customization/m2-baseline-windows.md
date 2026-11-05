# M2 Windows 测试基线（pre-M2）

> 采集日期：2026-11-03（Asia/Shanghai，UTC+08）
> 仓库：`E:\code\agent-orchestrator`，分支 `feat/multi-agent-desktop`
> HEAD：`a6c284a4d060431371306072e6c80abfedf22328`（`feat: implement M1 workflow core and acceptance`）
> 用途：M2（编码执行与分级协作）开工前的**只读**基线快照，供后续 diff，区分「既有失败」与「M2 回归」。
> 本次未修改任何源码、迁移或生成文件；唯一写入为本文件。

## 1. 环境与工具链

| 项 | 值 |
| --- | --- |
| OS | Windows 10.0.26200（win32 x64） |
| Go | `go1.27.1 windows/amd64` |
| `GOCACHE` | `E:\cache\go-build` |
| `go.work` | `go 1.27.1`，仅 `use ./backend` |
| Git | `git version 2.45.1.windows.1` |
| Shell | PowerShell `-NoProfile`（原生进程调用） |
| 工作树状态 | `git status`：37 modified（其中 35 个为 `gen/*.go` 的 CRLF-only 差异，另有 `docs/customization/README.md`、`docs/customization/design.md`）、2 untracked（`m2-design.md`、`m2-agent-prompt.md`）。本次**未做任何 Git 写操作** |

**决定下述失败性质的关键环境事实：**

| 事实 | 实测证据 |
| --- | --- |
| 进程**没有**创建符号链接的权限 | `New-Item -ItemType SymbolicLink` → `此操作需要管理员权限。`；测试内 `symlink …: A required privilege is not held by the client.` |
| 无 POSIX 权限位 | 文件 `mode` 一律 `-rw-rw-rw-`（0666）/ 0777，`want 600`/`want 0700`/`want 0600` 全部失败 |
| `HOME` 未设置，只有 `USERPROFILE` | `HOME=`（空）、`USERPROFILE=C:\Users\11459` |
| 测试大量硬编码 POSIX 路径 | 48 条为 `workspace path must be absolute, got "/tmp/ws"` |
| 无 `tmux` | `which tmux` 未命中 |
| Chrome 位于 `C:\Program Files\Google\Chrome\Application\chrome.exe` | `LOCALAPPDATA` 下不存在 |
| Git Bash 位于 `D:\tool\Git\bin\bash.exe` | 存在 |
| `ao` 不在 PATH | `which ao` 未命中 |

## 2. 执行命令与退出码

主工作树命令均在 `E:\code\agent-orchestrator\backend` 下执行。

| # | 命令 | 退出码 | 说明 |
| --- | --- | --- | --- |
| 1 | `go build ./...` | **0** | 无输出，全部包可编译（耗时 ~11.6s） |
| 2 | `go test ./... -count=1` | **1** | 228 个包：**156 ok / 56 FAIL / 16 no test files**；**267 条失败测试**（耗时 442s） |
| 3 | `go test ./... -count=1`（第二次复跑） | **1** | 与第 2 次**逐条一致**：267 条失败集合完全相同，新增 0 / 消失 0（耗时 439s），证明失败是确定性而非随机 |
| 4 | `go test ./internal/service/workflow ./internal/storage/sqlite/store ./internal/storage/sqlite ./internal/cdc ./internal/httpd ./internal/httpd/apispec/... -count=1` | **0** | M1 目标包全绿（明细 §3.1） |
| 5 | `go vet ./internal/service/workflow ./internal/storage/sqlite/store ./internal/cdc ./internal/httpd/... ./internal/daemon` | **0** | 无输出，无诊断 |
| 6 | `go test ./internal/domain ./internal/httpd/controllers ./internal/daemon -count=1 -v` | **1** | 逐包计数核对（明细 §3.2） |

补充对照（**只读**：用 `git archive 623871d | tar -x` 导出到临时目录 `%TEMP%\m2base_623871d`，未创建 git worktree，未触碰主工作树）：

| # | 命令（在 `%TEMP%\m2base_623871d\backend`） | 退出码 | 说明 |
| --- | --- | --- | --- |
| 7 | `go build ./...` | **0** | 基线可编译 |
| 8 | `go test ./... -count=1` | **1** | 227 个包：**154 ok / 57 FAIL / 16 no test files**；**241 条失败测试**。包清单与 HEAD 的差异仅多出 `internal/service/workflow`（M1 新增），无其它增删（耗时 741s） |

> 基线对照的用途只有一个：证明「哪些失败在 M1 之前就已存在」。它**不构成** M2 准入标准，也**不**代表 M2 必须从全绿起步。

## 3. M1 目标包结果（本次复测）

### 3.1 目标包测试（命令 4，退出码 **0**）

| 包 | 结果 | 耗时 |
| --- | --- | --- |
| `internal/service/workflow` | ok | 2.481s |
| `internal/storage/sqlite/store` | ok | 53.708s |
| `internal/storage/sqlite` | ok | 117.919s |
| `internal/cdc` | ok | 2.170s |
| `internal/httpd` | ok | 7.648s |
| `internal/httpd/apispec` | ok | 0.434s |
| `internal/httpd/apispec/specgen` | ok | 20.745s |

顶层测试数量（`go test -list`）：`service/workflow` 14、`storage/sqlite/store` 435、`storage/sqlite` 142、`cdc` 5、`httpd` 165、`httpd/apispec/...` 20。

### 3.2 三个「既有红」包的逐包计数（命令 6）

| 包 | 顶层 RUN | 顶层 PASS | 顶层 FAIL | 顶层 SKIP | 子测试 | 与 M1 报告一致？ |
| --- | --- | --- | --- | --- | --- | --- |
| `internal/domain` | 302 | 70 | **2** | 0 | 230 | **是**（报告：72 run / 70 pass / 2 fail） |
| `internal/httpd/controllers` | 964 | 622 | **4** | 6 | 332 | **是**（报告：632 run / 622 pass / 4 fail / 6 skip） |
| `internal/daemon` | 139 | 101 | **2** | 0 | 36 | **是**（报告：103 run / 101 pass / 2 fail） |

> 计数口径：`=== RUN` 行总数包含子测试。`domain` 302 = 72 顶层 + 230 子测试；`controllers` 964 = 632 顶层 + 332 子测试；`daemon` 139 = 103 顶层 + 36 子测试。三包的 pass / fail / skip **与 M1 报告逐项吻合**。

## 4. 声称的 8 条基线失败：复现判定

| # | 包 | 测试 | HEAD 第 1 次 | HEAD 第 2 次 | `623871d` 基线 | 当前失败信息 |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | `internal/domain` | `TestBuildHandoffNoteStaysUnderTheCapAndKeepsTheLatestTurn` | FAIL | FAIL | FAIL | handoff_note_test.go:84: the latest turn must never be dropped |
| 2 | `internal/domain` | `TestHandoffNoteKeepsArtifactsWithinTheByteCap` | FAIL | FAIL | FAIL | handoff_note_test.go:211: note must keep the latest request and count the artifacts it left out |
| 3 | `internal/httpd/controllers` | `TestListDirsRejectsRelativeAndMissingPaths` | FAIL | FAIL | FAIL | fs_test.go:133: status/code = 404/"FS_NOT_FOUND", want 400/"FS_NOT_A_DIRECTORY" |
| 4 | `internal/httpd/controllers` | `TestBridgeStatusConcurrentSecurePairing` | FAIL | FAIL | FAIL | mobile_concurrency_test.go:106: SetSecurePairing: rename C:\Users\11459\AppData\Local\Temp\TestBridgeStatusConcurrentSecurePairing4249149125… |
| 5 | `internal/httpd/controllers` | `TestArtifactFileRouteServesOnlyRegularFilesInsideTheDirectory` | FAIL | FAIL | FAIL | artifact_files_test.go:238: symlink C:\Users\11459\AppData\Local\Temp\TestArtifactFileRouteServesOnlyRegularFilesInsideTheDirectory231822221… |
| 6 | `internal/httpd/controllers` | `TestProjectsAPI_Clone` | FAIL | FAIL | FAIL | projects_test.go:297: POST clone = 400, want 201; body={"error":"bad_request","code":"INVALID_GIT_URL","message":"Enter a valid HTTPS, SSH, … |
| 7 | `internal/daemon` | `TestStabilizeWorkingDirectoryChdirsToDataDir` | FAIL | FAIL | FAIL | testing.go:1617: TempDir RemoveAll cleanup: unlinkat C:\Users\11459\AppData\Local\Temp\TestStabilizeWorkingDirectoryChdirsToDataDir158189089… |
| 8 | `internal/daemon` | `TestWiring_StartSessionSpawnsScratchWithoutGitRepo` | FAIL | FAIL | FAIL | wiring_test.go:443: Spawn scratch: The agent is installed but the project credential was rejected |

**结论：8 条全部 100% 复现**，并且在原始基线 `623871d` 上同样全部失败 —— 属于 M1 之前就存在的既有失败，**不是** M1 或 M2 引入。

## 5. HEAD 全仓失败总览（命令 2）

228 个包：**156 PASS / 56 FAIL / 16 no test files**；**267 条失败测试**（顶层测试名，未展开子测试）。

### 5.1 失败原因分布

| 原因类别 | 条数 | 代表证据 |
| --- | --- | --- |
| POSIX 权限位 / ACL 断言（Windows 无 POSIX mode） | 88 | `permissions = 666, want 600`；`mode = -rw-rw-rw-, want 0600`；`codex directory ancestor ACL is unsafe` |
| 测试硬编码 POSIX 绝对路径 | 48 | `workspace path must be absolute, got "/tmp/ws"` |
| 其他（见 §5.3 明细） | 42 | 主要来自 panic 截断与超时类 |
| 断言值不匹配 | 34 | `status = ("unknown", false), want ("authorized", true)` |
| 缺少创建符号链接权限 | 21 | `symlink …: A required privilege is not held by the client.` |
| git URL 校验拒绝本地路径 | 17 | `INVALID_GIT_URL: Enter a valid HTTPS, SSH, Git, or file repository URL.` |
| 外部二进制缺失 / PATH 解析差异 | 8 | `agent: binary not found on PATH`；`exit status 127` |
| workspace trust 写入 / 读取失败 | 6 | `workspace not trusted in the project config`；`read trust record: … The system cannot find the file` |
| Windows 路径缺失 | 3 | `open …\.copilot\config.json: The system cannot find the file specified.` |

**全部 267 条都是 Windows 环境产物或既有跨平台缺陷**：没有一条的失败信息落在 M1 新增的 workflow 代码路径上，且 M1 目标包（命令 4）退出码为 0。

### 5.2 按包汇总（失败包 56 个）

| 包 | 失败测试数 | 主要类别 |
| --- | --- | --- |
| `internal/adapters/agent/agy` | 2 | 其他×1；无 symlink 权限×1 |
| `internal/adapters/agent/aider` | 1 | 断言不匹配×1 |
| `internal/adapters/agent/claudecode` | 3 | workspace trust×2；断言不匹配×1 |
| `internal/adapters/agent/codex` | 1 | 无 symlink 权限×1 |
| `internal/adapters/agent/commandcode` | 1 | 外部二进制/PATH×1 |
| `internal/adapters/agent/copilot` | 2 | workspace trust×2 |
| `internal/adapters/agent/fake` | 2 | 断言不匹配×2 |
| `internal/adapters/agent/kilocode` | 1 | 外部二进制/PATH×1 |
| `internal/adapters/agent/kimchi` | 1 | 断言不匹配×1 |
| `internal/adapters/agent/kimi` | 6 | 断言不匹配×3；POSIX 权限位/ACL×1 |
| `internal/adapters/agent/modelcatalog` | 7 | 其他×4；断言不匹配×2 |
| `internal/adapters/agent/opencode` | 1 | 无 symlink 权限×1 |
| `internal/adapters/agent/pi` | 2 | 外部二进制/PATH×2 |
| `internal/adapters/agent/primeagent` | 2 | 其他×1；断言不匹配×1 |
| `internal/adapters/agent/registry` | 1 | 外部二进制/PATH×1 |
| `internal/adapters/agentaccount` | 5 | 无 symlink 权限×5 |
| `internal/adapters/chatdriver/agyacp` | 1 | 其他×1 |
| `internal/adapters/chatdriver/codexappserver` | 48 | 硬编码 POSIX 路径×48 |
| `internal/adapters/chatdriver/cursoracp` | 1 | 无 symlink 权限×1 |
| `internal/adapters/chatdriver/kimiacp` | 1 | 其他×1 |
| `internal/adapters/chatdriver/persistenthost` | 1 | POSIX 权限位/ACL×1 |
| `internal/adapters/chatdriver/processenv` | 1 | 其他×1 |
| `internal/adapters/reviewer/agy` | 1 | 其他×1 |
| `internal/adapters/reviewer/claudecode` | 1 | 断言不匹配×1 |
| `internal/adapters/reviewer/cursor` | 1 | POSIX 权限位/ACL×1 |
| `internal/adapters/reviewer/devin` | 1 | 其他×1 |
| `internal/adapters/reviewer/droid` | 2 | 其他×2 |
| `internal/adapters/reviewer/kimi` | 2 | 其他×2 |
| `internal/adapters/runtime/conpty` | 1 | Windows 路径缺失×1 |
| `internal/adapters/runtime/tmux` | 3 | 断言不匹配×2；其他×1 |
| `internal/adapters/telemetry` | 1 | 其他×1 |
| `internal/adapters/workspace/gitworktree` | 4 | 断言不匹配×2；无 symlink 权限×1 |
| `internal/agentlaunch` | 2 | 外部二进制/PATH×2 |
| `internal/browserchrome` | 2 | 其他×2 |
| `internal/cli` | 1 | 无 symlink 权限×1 |
| `internal/daemon` | 2 | 其他×2 |
| `internal/domain` | 2 | 其他×2 |
| `internal/httpd/controllers` | 4 | 断言不匹配×1；其他×1 |
| `internal/integration` | 1 | 其他×1 |
| `internal/mobilebridge` | 1 | POSIX 权限位/ACL×1 |
| `internal/observe/usage` | 4 | 其他×4 |
| `internal/pricing` | 2 | POSIX 权限位/ACL×2 |
| `internal/review` | 1 | 断言不匹配×1 |
| `internal/reviewgateway` | 2 | POSIX 权限位/ACL×2 |
| `internal/service/agent` | 85 | POSIX 权限位/ACL×79；其他×5 |
| `internal/service/agentauth` | 2 | workspace trust×1；Windows 路径缺失×1 |
| `internal/service/chat` | 4 | 其他×2；无 symlink 权限×1 |
| `internal/service/fsbrowser` | 2 | 无 symlink 权限×2 |
| `internal/service/importer` | 1 | 断言不匹配×1 |
| `internal/service/project` | 17 | git URL 校验×16；断言不匹配×1 |
| `internal/service/session` | 3 | 其他×1；断言不匹配×1 |
| `internal/service/systemcheck` | 1 | 断言不匹配×1 |
| `internal/service/systeminstall` | 9 | 断言不匹配×6；无 symlink 权限×2 |
| `internal/service/usage` | 3 | 断言不匹配×2；其他×1 |
| `internal/session_manager` | 7 | 断言不匹配×4；其他×2 |
| `internal/tmuxbin` | 1 | 无 symlink 权限×1 |

### 5.3 失败测试明细（包 + 测试名 + 一行原因）


**`internal/adapters/agent/agy`** — 2 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestBridgeBrainHandlesExistingSymlinksSafely` | bridge_test.go:285: symlink C:\Users\11459\AppData\Local\Temp\TestBridgeBrainHandlesExistingSymlinksSafely3533981399\001\outside-victim-dir C:\Users\11459\AppData\Local\Temp\TestBridgeBrainH… |
| `TestGeminiHomePrefersSessionThenDaemonThenDefault` | bridge_test.go:147: default GeminiHome = "C:\\Users\\11459\\.gemini" |

**`internal/adapters/agent/aider`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestAiderLocalAuthStatusAuthorizedWithDocumentedAPIKeyList` | auth_test.go:55: status = ("unknown", false), want ("authorized", true) |

**`internal/adapters/agent/claudecode`** — 3 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestClaudeLaunchConfigPathsFollowTheLaunchConfigDir` | claudecode_test.go:1599: paths = "C:\\Users\\11459\\.claude\\settings.json", "C:\\Users\\11459\\.claude.json"; want "C:\\Users\\11459\\AppData\\Local\\Temp\\TestClaudeLaunchConfigPathsFollow… |
| `TestPreLaunchMarksAutoDefaultNudgeSeenOnlyWhenOffered` | claudecode_test.go:1497: hasSeenAutoDefaultNudge = <nil>, want true |
| `TestPreLaunchUsesTheLaunchClaudeConfigDir` | claudecode_test.go:1658: workspace not trusted in the project config: map[hasSeenAutoDefaultNudge:true projects:map[C:/Users/11459/AppData/Local/Temp/TestPreLaunchUsesTheLaunchClaudeConfigDi… |

**`internal/adapters/agent/codex`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestNativeConversationExistsFollowsALinkedSessionsFolder` | codex_test.go:1182: symlink C:\Users\11459\AppData\Local\Temp\TestNativeConversationExistsFollowsALinkedSessionsFolder553454646\001\sessions C:\Users\11459\AppData\Local\Temp\TestNativeConve… |

**`internal/adapters/agent/commandcode`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestResolveBinaryFindsCommandCodeOnPath` | commandcode_test.go:347: ResolveBinary: command-code: agent: binary not found on PATH |

**`internal/adapters/agent/copilot`** — 2 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestEnsureWorkspaceTrustedCreatesConfig` | trust_test.go:23: read config: open C:\Users\11459\AppData\Local\Temp\TestEnsureWorkspaceTrustedCreatesConfig2476326329\001\.copilot\config.json: The system cannot find the path specified. |
| `TestOnlyASwitchTargetTrustsTheWorkspaceForCopilot` | copilot_test.go:1219: config.json = "{\n  \"trustedFolders\": [\n    \"C:\\\\Users\\\\11459\\\\AppData\\\\Local\\\\Temp\\\\TestOnlyASwitchTargetTrustsTheWorkspaceForCopilot652498866\\\\002\"… |

**`internal/adapters/agent/fake`** — 2 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestGetLaunchCommandIsScriptedTimeline` | fake_test.go:51: launch command shape = []string{"D:\\tool\\Git\\bin\\sh.exe", "-lc", "printf '%s\\n' 'fake-agent: spawning'\nsleep 0.5\nprintf '%s\\n' 'fake-agent: active'\nao hooks fake se… |
| `TestResolveBinaryReturnsResolvedShellPath` | fake_test.go:90: ResolveBinary = "D:\\tool\\Git\\bin\\sh.exe", want an absolute resolved sh path |

**`internal/adapters/agent/kilocode`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestResolveKilocodeBinaryFindsOfficialKiloCommand` | kilocode_test.go:37: ResolveKilocodeBinary: kilocode: agent: binary not found on PATH |

**`internal/adapters/agent/kimchi`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestAuthStatusAuthorizedFromConfig` | auth_test.go:224: AuthStatus = "unknown", want "authorized" |

**`internal/adapters/agent/kimi`** — 6 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestEnsureWorkspaceTrustedSeedsRealKimiHome` | trust_test.go:128: read trust record: open C:\Users\11459\AppData\Local\Temp\TestEnsureWorkspaceTrustedSeedsRealKimiHome2327080025\001\.kimi-code\workspace-trust\wd_kimi_8338a105d971: The sy… |
| `TestGetAgentHooksReseedsHookOnlyAOManagedConfigFromOAuthUserKimiHome` | kimi_test.go:564: AO credentials = {"refresh_token":"ao-refresh"}, want {"access_token":"user-access"} |
| `TestGetAgentHooksSeedsAOManagedCredentialsFromUserKimiHome` | kimi_test.go:665: AO credentials permissions = 666, want 600 |
| `TestGetAgentHooksSeedsCredentialReferencedByOAuthConfig` | kimi_test.go:529: AO credentials link = "", readlink C:\Users\11459\AppData\Local\Temp\TestGetAgentHooksSeedsCredentialReferencedByOAuthConfigscoped_a3774696003\003\credentials: The file or … |
| `TestGetAgentHooksSharesTheUsersKimiLoginInPlaceOfAStaleCopy` | kimi_test.go:701: AO credentials = {"access_token":"ao-token"}, want {"access_token":"user-token"} |
| `TestPrepareACPInstructionsDoesNotWriteThroughALinkedFolder` | kimi_test.go:1046: symlink C:\Users\11459\AppData\Local\Temp\TestPrepareACPInstructionsDoesNotWriteThroughALinkedFolder28361364\002 C:\Users\11459\AppData\Local\Temp\TestPrepareACPInstructio… |

**`internal/adapters/agent/modelcatalog`** — 7 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestAutoHandDiscoveryReadsConfigurationWithoutRunningBinary` | config_test.go:62: autohand model configuration was not found |
| `TestAutohandConfiguredProvidersReadKeyedSections` | autohand_providers_test.go:33: configured = [], want [zai deepseek] |
| `TestConfiguredProvidersOMPDisabledCredentialsDoNotFallBack` | providers_test.go:89: providers = [] |
| `TestOMPProviderCacheUsesModelProviderNotAccountCacheKey` | provider_catalog_test.go:122: catalog = ports.AgentModelCatalog{Providers:[]string(nil), ConfiguredProviders:[]string(nil), RunnableModels:[]string(nil), AgentID:"", SelectionMode:"", Models… |
| `TestOpenCodeV2RetriesEmptyFirstRunInNewFolder` | catalog_test.go:41: opencode-v2 model discovery: exec: "C:\\Users\\11459\\AppData\\Local\\Temp\\TestOpenCodeV2RetriesEmptyFirstRunInNewFolder2530919893\\001\\opencode2": executable file not … |
| `TestQwenSharedCatalogMarksConfiguredProviders` | config_test.go:119: qwen model configuration was not found |
| `TestVibeConfiguredProvidersReadKeyVariables` | vibe_providers_test.go:48: configured = [], want [mistral zai] |

**`internal/adapters/agent/opencode`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestPrepareV2DataHomeSkipsToolCache` | v2home_test.go:114: symlink ..\astro\bin\ls.js C:\Users\11459\AppData\Local\Temp\TestPrepareV2DataHomeSkipsToolCache3523820051\001\opencode\bin\node_modules\.bin\astro-ls: A required privile… |

**`internal/adapters/agent/pi`** — 2 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestGetAgentHooksInstallsManagedActivityExtension` | pi_test.go:325: GetAgentHooks err = pi.GetAgentHooks: probe pi --version: exec: "C:\\Users\\11459\\AppData\\Local\\Temp\\TestGetAgentHooksInstallsManagedActivityExtension4054251092\\002\\pi"… |
| `TestPiAgentSettledSupportedFromBinaryVersion` | hooks_test.go:55: pi.GetAgentHooks: probe pi --version: exec: "C:\\Users\\11459\\AppData\\Local\\Temp\\TestPiAgentSettledSupportedFromBinaryVersion0.80.568238798\\001\\pi": executable file n… |

**`internal/adapters/agent/primeagent`** — 2 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestAugmentRuntimeEnvUsesAOPrimeStateRoot` | lifecycle_test.go:59: PRIME_AGENT_CODING_AGENT_DIR = "\\ao-data\\agent-runtime\\prime-agent", want "/ao-data/agent-runtime/prime-agent" |
| `TestTerminateNativeSessionStopsTheActiveOwnerOfTheTranscript` | lifecycle_test.go:48: prime-agent: list live sessions: binary="prime-agent" cwd="\\ao-data\\agent-runtime\\prime-agent": |

**`internal/adapters/agent/registry`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestGetAgentHooksFootprintIsGitignored` | registry_test.go:91: GetAgentHooks: codewhale.GetAgentHooks: probe codewhale --version: codewhale: agent: binary not found on PATH |

**`internal/adapters/agentaccount`** — 5 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestClaudePrepareKeepsChangesMadeOnTheAccount` | agentaccount_test.go:103: the account's folder cannot link to this computer's history: link projects: symlink C:\Users\11459\AppData\Local\Temp\TestClaudePrepareKeepsChangesMadeOnTheAccount2… |
| `TestClaudePrepareSharesHistoryNotLogin` | agentaccount_test.go:62: the account's folder cannot link to this computer's history: link projects: symlink C:\Users\11459\AppData\Local\Temp\TestClaudePrepareSharesHistoryNotLogin149813941… |
| `TestCodexPrepareKeepsLoginAndDaemonPrivate` | agentaccount_test.go:203: the account's folder cannot link to this computer's history: link logs: symlink C:\Users\11459\AppData\Local\Temp\TestCodexPrepareKeepsLoginAndDaemonPrivate91693114… |
| `TestCodexPrepareSharesFilesTheComputerDoesNotHaveYet` | agentaccount_test.go:222: the account's folder cannot link to this computer's history: link sqlite: symlink C:\Users\11459\AppData\Local\Temp\TestCodexPrepareSharesFilesTheComputerDoesNotHav… |
| `TestPrepareDropsLinksToDeletedEntries` | agentaccount_test.go:239: the account's folder cannot link to this computer's history: link archived_sessions: symlink C:\Users\11459\AppData\Local\Temp\TestPrepareDropsLinksToDeletedEntries… |

**`internal/adapters/chatdriver/agyacp`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestLaunchPointsTheServerAtItsHarness` | driver_test.go:183: launch = {Command:C:\Users\11459\AppData\Local\Temp\TestLaunchPointsTheServerAtItsHarness143779059\001\agy_acp_server.par Args:[] Env:map[AGY_ACP_DISABLE_WORKSPACE_TRUST:… |

**`internal/adapters/chatdriver/codexappserver`** — 48 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestApprovalIsParkedUntilResolved` | driver_test.go:533: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestBackgroundReadOnlySandboxPersistsAcrossTurnOverrides` | driver_test.go:1238: workspace path must be absolute, got "/tmp/ws" |
| `TestCanHibernateRequiresAnEmptyBackgroundTerminalInventory` | hibernate_test.go:34: workspace path must be absolute, got "/tmp/ws" |
| `TestCompactSendsOnlyTheThreadID` | driver_test.go:1578: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestCompactionIsReportedOncePerTurn` | driver_test.go:1658: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestCompactionReportsWhatItReclaimed` | driver_test.go:1615: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestControllerStopIsAnnounced` | driver_test.go:1312: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestDecisionNotOfferedIsRefusedAndLeavesTheRequestPending` | driver_test.go:637: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestForkRejectsAResponseWithNoThreadID` | history_test.go:388: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestForkReturnsTheNewThreadIDAndInheritsTheWorkingDirectory` | history_test.go:331: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestForkThroughTurnSendsLastTurnID` | history_test.go:363: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestListModelsKeepsCatalogAndUsesThreadEffort` | driver_test.go:1407: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestListModelsUsesConfiguredThreadDefault` | driver_test.go:1445: workspace path must be absolute, got "/tmp/ws" |
| `TestLiveCodexCitationEventsBecomeMarkdown` | citations_test.go:144: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestNestedThreadDoesNotReplaceRootActiveTurn` | driver_test.go:813: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestNoTurnSettingsSendsNoSettingsFields` | driver_test.go:1375: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestNotificationsBecomeNeutralEvents` | driver_test.go:714: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestReadHistoryMakesMissingItemIDsUniqueAcrossTurns` | history_test.go:117: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestReadHistoryPreservesStructuredProviderFailure` | history_test.go:99: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestReadHistoryReconstructsNativeTurnsForTheChatTimeline` | history_test.go:54: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestReadHistoryRejectsAnUnsettledNativeTurnInsteadOfOmittingIt` | history_test.go:139: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestReadHistoryResolvesCodexCitationSources` | citations_test.go:180: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestReadRateLimitsFromProviderResult` | driver_test.go:1517: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestReconnectAdoptsInitializedHostWithoutNativeResume` | driver_test.go:316: Resume: workspace path must be absolute, got "/tmp/ws" |
| `TestReconnectInitializesAHostThatNeverWas` | driver_test.go:364: Resume: workspace path must be absolute, got "/tmp/ws" |
| `TestRefreshHistoryPerformsANewThreadRead` | history_test.go:155: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestResolveUnknownRequestIsRefused` | driver_test.go:682: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestResolvingAnUnknownRequestIsTyped` | driver_test.go:666: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestResumeDoesNotCompeteWithAttachedHostDuringDaemonOverlap` | driver_test.go:448: Resume error = workspace path must be absolute, got "/tmp/ws", want attached-host refusal |
| `TestResumeFailureDoesNotFallBackToStart` | driver_test.go:861: err = workspace path must be absolute, got "/tmp/ws", want ErrChatResumeFailed |
| `TestResumeReappliesWorkspaceAndStandingInstructions` | driver_test.go:884: Resume: workspace path must be absolute, got "/tmp/ws" |
| `TestRollbackCountsTurnsFromTheNamedOneToTheEnd` | history_test.go:193: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestRollbackKeepsATransportFailureAsAFailure` | history_test.go:318: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestRollbackOfTheLastTurnDiscardsOne` | history_test.go:232: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestRollbackRefusesATurnTheProviderDoesNotHave` | history_test.go:255: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestRollbackTranslatesTheMidTurnRefusal` | history_test.go:296: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestRollbackUsesRevertWhenTheProviderRemovedRollback` | history_test.go:276: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestSendTurnCarriesIdempotencyKey` | driver_test.go:472: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestSendTurnRejectsEmptyText` | driver_test.go:517: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestSetTitleRefusesABlankName` | history_test.go:423: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestSetTitleSendsTheTrimmedName` | history_test.go:397: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestStartAndResumePassAOToolServersInThreadConfig` | driver_test.go:1802: thread/start: workspace path must be absolute, got "/tmp/ws" |
| `TestStartCompletesHandshakeAndOpensThread` | driver_test.go:240: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestStructuredDecisionIsAnsweredWithTheProvidersOwnPayload` | driver_test.go:610: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestThreadRenamedNotificationBecomesATitleEvent` | history_test.go:440: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestThreadRenamedNotificationReportsAClearedName` | history_test.go:452: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestTurnSettingsUseTheTurnLevelWireShapes` | driver_test.go:1332: Start: workspace path must be absolute, got "/tmp/ws" |
| `TestUnmodelledServerRequestIsRefused` | driver_test.go:698: Start: workspace path must be absolute, got "/tmp/ws" |

**`internal/adapters/chatdriver/cursoracp`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestVersionProbeUsesStandardInstallerSymlinkWithoutLaunchingCLI` | version_test.go:64: symlink C:\Users\11459\AppData\Local\Temp\TestVersionProbeUsesStandardInstallerSymlinkWithoutLaunchingCLI2978345345\001\versions\2026.08.11-e8db854\cursor-agent C:\Users\… |

**`internal/adapters/chatdriver/kimiacp`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestKimiHandoffReachesKimiCodeWhenTheProjectCommitsItsInstructions` | tracked_instructions_test.go:94: chat driver unavailable: persistent ACP host: context deadline exceeded |

**`internal/adapters/chatdriver/persistenthost`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestACPPromptJournalReplaysResetsAndEnforcesQuota` | acp_journal_test.go:24: journal permissions = -rw-rw-rw- |

**`internal/adapters/chatdriver/processenv`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestMergePreservesVariableSpellingForBash` | spelling_test.go:20: Bash project variable: <nil>: ":" |

**`internal/adapters/reviewer/agy`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestReviewCommandLaunchesHostTrustedInteractiveTUI` | agy_test.go:46: agy reviewer: resolved binary must be absolute |

**`internal/adapters/reviewer/claudecode`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestReviewRestoreCommandEmitsResumeWithPolicySystemPromptAndTask` | claudecode_test.go:323: ReviewRestoreCommand = (ok=false, err=<nil>), want command |

**`internal/adapters/reviewer/cursor`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestPreLaunchWritesIsolatedReviewerConfig` | cursor_test.go:229: profile mode = 777, want 700 |

**`internal/adapters/reviewer/devin`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestReviewCommandUsesVisibleInteractiveTUI` | devin_test.go:21: devin reviewer: resolved binary must be absolute |

**`internal/adapters/reviewer/droid`** — 2 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestReviewCommandPreflightValidationAndCancel` | droid_test.go:78: spec = ports.ReviewCommandSpec{Argv:[]string(nil), Env:map[string]string(nil), AgentSessionID:"", NativeResumed:false, InitialMessage:"", WorkingDirectory:""}, droid review… |
| `TestReviewCommandUsesAutonomousInteractiveSettings` | droid_test.go:27: droid reviewer: resolved binary must be absolute |

**`internal/adapters/reviewer/kimi`** — 2 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestReviewCommandPreflightValidationAndCancel` | kimi_test.go:47: spec = ports.ReviewCommandSpec{Argv:[]string(nil), Env:map[string]string(nil), AgentSessionID:"", NativeResumed:false, InitialMessage:"", WorkingDirectory:""}, kimi reviewer… |
| `TestReviewCommandUsesPlanModeAndEmptySkillsDirectory` | kimi_test.go:22: kimi reviewer: resolved binary must be absolute |

**`internal/adapters/runtime/conpty`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestConPTYStartsAtRequestedGrid` | host_conpty_windows_test.go:87: mode con output "\x1b[?9001h\x1b[?1004h\x1b[?25l\x1b[2J\x1b[m\x1b[H\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\… |

**`internal/adapters/runtime/tmux`** — 3 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestExecRunnerFallsBackWhenTempDirMissing` | tmux_test.go:218: precondition: os.TempDir() "C:\\Users\\11459\\AppData\\Local\\Temp" should not exist, stat err = <nil> |
| `TestExecRunnerRunsFromStableDir` | tmux_test.go:206: execRunner ran from "/tmp", want os.TempDir() "C:\\Users\\11459\\AppData\\Local\\Temp" |
| `TestNewUsesSystemTmuxForLegacyDefaultSocket` | tmux_test.go:158: legacy binary = "", want system tmux "C:\\Users\\11459\\AppData\\Local\\Temp\\TestNewUsesSystemTmuxForLegacyDefaultSocket2579151467\\001\\tmux" |

**`internal/adapters/telemetry`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestDestinationSinkConcurrentEmitAndClose` | shutdown_test.go:112: timed out waiting for telemetry worker or producer |

**`internal/adapters/workspace/gitworktree`** — 4 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestWorkspaceIntegrationAutoRejectsUnmarkedRemotelessRepo` | workspace_integration_test.go:889: Create error = workspace: default branch is unresolved: repository "C:\\Users\\11459\\AppData\\Local\\Temp\\TestWorkspaceIntegrationAutoRejectsUnmarkedRemo… |
| `TestWorkspaceIntegrationCreateOrchestratorMovesOrphanedWorktreeAside` | workspace_orphan_test.go:92: new orchestrator worktree not registered: |
| `TestWorkspaceIntegrationCreateRefusesOccupiedPathAndLeavesItUntouched` | workspace_orphan_test.go:214: create error = workspace: worktree path is occupied: "C:\\Users\\11459\\AppData\\Local\\Temp\\TestWorkspaceIntegrationCreateRefusesOccupiedPathAndLeavesItUnto37… |
| `TestWorkspaceIntegrationWorkspaceProjectCopiesAssetsAndCleansSessionCopy` | workspace_integration_test.go:1217: symlink nested\context.txt C:\Users\11459\AppData\Local\Temp\TestWorkspaceIntegrationWorkspaceProjectCopiesAssetsAndCleansSes3387697907\001\root\repo\note… |

**`internal/agentlaunch`** — 2 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestSharedWindowsInstallSelectsOnlyCanonicalAO` | pin_windows_test.go:69: Git Bash AO selection: exit status 127 |
| `TestWindowsAOExecutableFallsBackToCopyAcrossFilesystems` | pin_windows_test.go:124: Git Bash copied AO selection: exit status 127 |

**`internal/browserchrome`** — 2 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestSetupRegistersInstalledChannelsOnly` | chrome_test.go:121: channels = []browserchrome.ChannelStatus{browserchrome.ChannelStatus{Name:"Google Chrome", Available:true, Registered:true, ManifestPath:"C:\\Users\\11459\\AppData\\Local… |
| `TestSetupWithoutChromeFails` | chrome_test.go:202: error = <nil> |

**`internal/cli`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestRenderCheckReplacesItsEarlierScreenshot` | render_test.go:194: symlink C:\Users\11459\AppData\Local\Temp\TestRenderCheckReplacesItsEarlierScreenshot1090310644\002\secret.txt C:\Users\11459\AppData\Local\Temp\TestRenderCheckReplacesIt… |

**`internal/daemon`** — 2 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestStabilizeWorkingDirectoryChdirsToDataDir` | testing.go:1617: TempDir RemoveAll cleanup: unlinkat C:\Users\11459\AppData\Local\Temp\TestStabilizeWorkingDirectoryChdirsToDataDir158189089\001\ao-data: The process cannot access the file b… |
| `TestWiring_StartSessionSpawnsScratchWithoutGitRepo` | wiring_test.go:443: Spawn scratch: The agent is installed but the project credential was rejected |

**`internal/domain`** — 2 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestBuildHandoffNoteStaysUnderTheCapAndKeepsTheLatestTurn` | handoff_note_test.go:84: the latest turn must never be dropped |
| `TestHandoffNoteKeepsArtifactsWithinTheByteCap` | handoff_note_test.go:211: note must keep the latest request and count the artifacts it left out |

**`internal/httpd/controllers`** — 4 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestArtifactFileRouteServesOnlyRegularFilesInsideTheDirectory` | artifact_files_test.go:238: symlink C:\Users\11459\AppData\Local\Temp\TestArtifactFileRouteServesOnlyRegularFilesInsideTheDirectory2318222212\001\secret.html C:\Users\11459\AppData\Local\Tem… |
| `TestBridgeStatusConcurrentSecurePairing` | mobile_concurrency_test.go:106: SetSecurePairing: rename C:\Users\11459\AppData\Local\Temp\TestBridgeStatusConcurrentSecurePairing4249149125\001\.mobile.json.2936620055 C:\Users\11459\AppDat… |
| `TestListDirsRejectsRelativeAndMissingPaths` | fs_test.go:133: status/code = 404/"FS_NOT_FOUND", want 400/"FS_NOT_A_DIRECTORY" |
| `TestProjectsAPI_Clone` | projects_test.go:297: POST clone = 400, want 201; body={"error":"bad_request","code":"INVALID_GIT_URL","message":"Enter a valid HTTPS, SSH, Git, or file repository URL.","requestId":"YMINGLI… |

**`internal/integration`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestDelegateEndpointDoesNotDependOnCodexDeviceReconciliation` | lifecycle_sqlite_test.go:297: local device reconciliation: Codex account setup did not complete |

**`internal/mobilebridge`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestEnsureIdentityWritesOwnerOnly` | identity_test.go:121: identity file mode 666, want 600 |

**`internal/observe/usage`** — 4 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestEveryWriteRatedOpenAIModelPricesBothCodexPayloadShapes` | codex_write_rate_test.go:28: load shipped catalog: provider "anthropic" hash mismatch |
| `TestIngestorReadsIdentityAndContentFromSingleDescriptorAcrossAtomicReplacement` | ingestor_integration_test.go:338: ingest opened generation: usage source 1: artifact_missing |
| `TestLegacyRepairerRefusesUnverifiableSourcesAndMismatchedEvents` | legacy_repair_test.go:87: replacement reused the file identity of C:\Users\11459\AppData\Local\Temp\TestLegacyRepairerRefusesUnverifiableSourcesAndMismatchedEvents2611761882\003\rollout.json… |
| `TestPipelineIngestsDiscoveredClaudeContinuation` | continuation_integration_test.go:47: continuation binding ok=false err=<nil> |

**`internal/pricing`** — 2 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestCacheInstallRepairsCorruptProviderBlob` | fetch_cache_test.go:363: repaired blob mode = -rw-rw-rw-, <nil> |
| `TestCacheInstallsPrivateManifestLastAndLoadsLKG` | fetch_cache_test.go:217: C:\Users\11459\AppData\Local\Temp\TestCacheInstallsPrivateManifestLastAndLoadsLKG706105090\001\pricing\catalog\v1\manifest.json mode = 666, want 600 |

**`internal/review`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestLauncherSpawnPrependsNodeRuntimeForNodeShimReviewer` | launcher_test.go:268: runtime PATH = "C:\\Users\\11459\\AppData\\Local\\Temp\\TestLauncherSpawnPrependsNodeRuntimeForNodeShimReviewer1718660260\\004;C:\\Users\\11459\\AppData\\Local\\Temp\\T… |

**`internal/reviewgateway`** — 2 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestPrepareEnvironmentUsesPrivateAOOwnedDirectoriesAndImmutableManifest` | gateway_test.go:44: directory "C:\\Users\\11459\\AppData\\Local\\Temp\\TestPrepareEnvironmentUsesPrivateAOOwnedDirectoriesAndImmutableM4020931162\\001\\reviewer-runtime\\reviewer-1" mode = -… |
| `TestPrepareHostTrustedEnvironmentUsesPrivateAOOwnedDirectories` | gateway_test.go:79: private AO-owned directory "C:\\Users\\11459\\AppData\\Local\\Temp\\TestPrepareHostTrustedEnvironmentUsesPrivateAOOwnedDirectories3230794741\\001\\reviewer-runtime\\revie… |

**`internal/service/agent`** — 85 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestActivationRejectsExternalAuthorizedAPIKeyReplacement` | codex_accounts_test.go:2106: codex directory ancestor ACL is unsafe |
| `TestActiveReauthenticationCommitsWhenProviderIsUnavailable` | codex_accounts_test.go:774: codex directory ancestor ACL is unsafe |
| `TestActiveReauthenticationPreservesCredentialsWhenDeviceChangesAfterTerminalOpens` | codex_accounts_test.go:708: codex directory ancestor ACL is unsafe |
| `TestActiveReauthenticationRevalidatesAndReplacesTheDeviceCredential` | codex_accounts_test.go:627: codex directory ancestor ACL is unsafe |
| `TestAuthenticationRequestCancellationDoesNotCancelSharedRead` | codex_accounts_test.go:2217: codex directory ancestor ACL is unsafe |
| `TestAuthorizedInactiveAccountDoesNotMaskTheActiveAccount` | codex_account_launch_readiness_test.go:245: codex directory ancestor ACL is unsafe |
| `TestBootstrapImportsUnknownDeviceCredentialOnlyOnce` | codex_accounts_test.go:1102: codex directory ancestor ACL is unsafe |
| `TestCachedCodexAccountsProjectsOnlySafeActiveLoginMetadata` | codex_accounts_test.go:395: Codex login could not be prepared |
| `TestCheckingReconciliationKeepsLastMatchedAccountVisibleWithoutUsingGlobalHome` | codex_accounts_test.go:326: codex directory ancestor ACL is unsafe |
| `TestCodexAccountCatalogAllowsDuplicateEmailAndOrdersByCreation` | codex_account_catalog_test.go:219: codex directory ancestor ACL is unsafe |
| `TestCodexAccountCatalogCommitsStrictPrivateOpaqueSlot` | codex_account_catalog_test.go:165: codex directory ancestor ACL is unsafe |
| `TestCodexAccountCatalogDeletesSignedOutSlot` | codex_account_catalog_test.go:337: codex directory ancestor ACL is unsafe |
| `TestCodexAccountCatalogLazilyUpgradesLegacyDescriptorIdentity` | codex_account_catalog_test.go:128: codex directory ancestor ACL is unsafe |
| `TestCodexAccountCatalogPersistsSafeProviderAccountID` | codex_account_catalog_test.go:106: codex directory ancestor ACL is unsafe |
| `TestCodexAccountCatalogRejectsSymlinkedCredentialHome` | codex_account_catalog_test.go:272: codex directory ancestor ACL is unsafe |
| `TestCodexAccountCatalogRetainsSignedOutSlotAndReplacesItsCredential` | codex_account_catalog_test.go:300: codex directory ancestor ACL is unsafe |
| `TestCodexAccountCatalogSurfacesUnsafeAndMalformedSlotsWithoutMetadataLeak` | codex_account_catalog_test.go:243: codex directory ancestor ACL is unsafe |
| `TestCodexAccountStoreAndDeviceReconciliationNeverOpenProvider` | codex_account_bootstrap_test.go:31: codex directory ancestor ACL is unsafe |
| `TestCodexBootstrapPermanentSafetyFailure` | codex_account_bootstrap_test.go:153: symlink C:\Users\11459\AppData\Local\Temp\TestCodexBootstrapPermanentSafetyFailure679121935\002 C:\Users\11459\AppData\Local\Temp\TestCodexBootstrapPerma… |
| `TestCodexCapacityDefinitiveRejectionRequiresSignInAgain` | codex_capacity_test.go:229: codex directory ancestor ACL is unsafe |
| `TestCodexCapacityRevokedAccessTokenDoesNotRefreshOrRetry` | codex_capacity_test.go:162: codex directory ancestor ACL is unsafe |
| `TestCodexCapacitySuccessDoesNotMutateAuthentication` | codex_capacity_test.go:189: codex directory ancestor ACL is unsafe |
| `TestCodexCapacityTemporaryFailureDoesNotSignAccountOut` | codex_capacity_test.go:262: codex directory ancestor ACL is unsafe |
| `TestCodexDeviceReconciliationFailureKeepsSavedAccountsReadable` | codex_account_bootstrap_test.go:183: codex directory ancestor ACL is unsafe |
| `TestCodexDeviceReconciliationReportsMalformedCredentialSafely` | codex_account_bootstrap_test.go:64: codex directory ancestor ACL is unsafe |
| `TestCodexDisplayExplainsPendingLocalAccountReconciliation` | auth_diagnostics_test.go:78: codex directory ancestor ACL is unsafe |
| `TestConsumeResetCreditVerifiesAvailabilityAndRefreshesCapacity` | codex_accounts_test.go:2197: codex directory ancestor ACL is unsafe |
| `TestCredentialActivationDoesNotOpenCodex` | codex_accounts_test.go:2158: codex directory ancestor ACL is unsafe |
| `TestDeleteAccountOwnsNativeLogoutBeforeCatalogRemoval` | codex_accounts_test.go:965: codex directory ancestor ACL is unsafe |
| `TestDeleteAlreadySignedOutAccountSkipsNativeLogout` | codex_accounts_test.go:987: codex directory ancestor ACL is unsafe |
| `TestDisplayReadInFlightCannotClearARequiredReauthentication` | codex_account_launch_readiness_test.go:224: codex directory ancestor ACL is unsafe |
| `TestEnsureCodexAccountsReconcilesRecentlyRemovedGlobalCredentialBeforeAccountChecks` | codex_accounts_test.go:1374: codex directory ancestor ACL is unsafe |
| `TestEnsureCodexAccountsRetriesSavedAccountWhenGlobalCredentialDisappearsDuringCheck` | codex_accounts_test.go:1519: codex directory ancestor ACL is unsafe |
| `TestEnsureCodexAccountsTargetedRefreshDoesNotReconcileDeviceCredential` | codex_accounts_test.go:1456: codex directory ancestor ACL is unsafe |
| `TestExternalDeviceSwitchImportsBWithoutWritingIntoA` | codex_accounts_test.go:1290: codex directory ancestor ACL is unsafe |
| `TestExternallyReplacedCredentialsDoNotGetAttributedToOldActiveSlot` | codex_account_launch_readiness_test.go:342: codex directory ancestor ACL is unsafe |
| `TestGlobalAccountMatchingUsesUniqueOpaqueCredentialIdentity` | codex_accounts_test.go:1592: codex directory ancestor ACL is unsafe |
| `TestGlobalModelDiscoveryUsesAODirectoryWithoutProject` | catalog_test.go:1791: model discovery directory permissions = 777, want 700 |
| `TestGlobalReconciliationExactCredentialMatchRemainsActiveOffline` | codex_accounts_test.go:1250: codex directory ancestor ACL is unsafe |
| `TestGlobalReconciliationKeepsMatchingDeviceAccountActiveWithoutProactiveRefresh` | codex_accounts_test.go:1140: codex directory ancestor ACL is unsafe |
| `TestGlobalReconciliationMatchesAPIKeyDirectlyWhenFileBytesChange` | codex_accounts_test.go:1691: codex directory ancestor ACL is unsafe |
| `TestGlobalReconciliationMatchesRotatedOAuthByAccountID` | codex_accounts_test.go:1200: codex directory ancestor ACL is unsafe |
| `TestGlobalReconciliationMissingGlobalClearsActiveProjectionWithoutRestoringSavedAccount` | codex_accounts_test.go:1333: codex directory ancestor ACL is unsafe |
| `TestGlobalReconciliationReactivatesSignedOutIdentityFromDeviceCredential` | codex_accounts_test.go:1732: codex directory ancestor ACL is unsafe |
| `TestGlobalReconciliationRefusesAmbiguousProviderAccountID` | codex_accounts_test.go:1617: codex directory ancestor ACL is unsafe |
| `TestGlobalReconciliationReusesImportedAccountAcrossExternalTokenRotation` | codex_accounts_test.go:1646: codex directory ancestor ACL is unsafe |
| `TestGlobalSwitchCapabilityAllowsMissingCredentialButRejectsUnsafeCredential` | codex_accounts_test.go:1885: codex directory ancestor ACL is unsafe |
| `TestImportedGlobalCredentialDoesNotBlockNormalAuthentication` | codex_accounts_test.go:1773: codex directory ancestor ACL is unsafe |
| `TestInactiveLogoutDoesNotAcquireGlobalGate` | codex_accounts_test.go:898: codex directory ancestor ACL is unsafe |
| `TestInspectCodexAccountSwitchClassifiesExternalAPIKeyReplacement` | codex_accounts_test.go:2125: codex directory ancestor ACL is unsafe |
| `TestInspectCodexAccountSwitchClassifiesTargetWithoutChangingProjection` | codex_accounts_test.go:2140: codex directory ancestor ACL is unsafe |
| `TestLaunchFallsBackToNativeReadinessOnTransientProtectedFailure` | codex_account_launch_readiness_test.go:200: codex directory ancestor ACL is unsafe |
| `TestLaunchFallsBackToNativeReadinessWhileDeviceReconciliationIsUnavailable` | codex_account_launch_readiness_test.go:180: codex directory ancestor ACL is unsafe |
| `TestLocalReconciliationPreservesActiveSlotProjection` | codex_accounts_test.go:1914: codex directory ancestor ACL is unsafe |
| `TestLoginCloseFailureRetainsPendingOperation` | codex_accounts_test.go:1085: Codex login could not be prepared |
| `TestLoginDeduplicatesExistingAccountByProviderID` | codex_accounts_test.go:2272: codex directory ancestor ACL is unsafe |
| `TestLogoutActiveAPIKeyRejectsExternalCredentialReplacement` | codex_accounts_test.go:1039: codex directory ancestor ACL is unsafe |
| `TestLogoutActiveAccountClearsDeviceCredentialAndActiveProjection` | codex_accounts_test.go:1007: codex directory ancestor ACL is unsafe |
| `TestLogoutRetainsInactiveAccountAsSignedOut` | codex_accounts_test.go:839: codex directory ancestor ACL is unsafe |
| `TestNativeLoginCreatesAndActivatesFirstAccountWithoutProviderCheck` | codex_accounts_test.go:440: Codex login could not be prepared |
| `TestNativeLoginTerminalUsesOnePrivatePendingHomeAndNoName` | codex_accounts_test.go:366: Codex login could not be prepared |
| `TestNativeLoginVerificationDoesNotReplaceExistingDeviceAccount` | codex_accounts_test.go:471: codex directory ancestor ACL is unsafe |
| `TestNativeLoginVerificationSavesAccountWhileDeviceReconciliationRetries` | codex_accounts_test.go:532: Codex login could not be prepared |
| `TestNativeLogoutFailurePreservesInactiveAccountCredential` | codex_accounts_test.go:856: codex directory ancestor ACL is unsafe |
| `TestNativeReauthenticationReplacesTheExistingAccountSlot` | codex_accounts_test.go:565: codex directory ancestor ACL is unsafe |
| `TestOpenReauthenticationReconcilesBeforeCapturingActiveOwnership` | codex_accounts_test.go:673: codex directory ancestor ACL is unsafe |
| `TestPrepareSwitchRejectsChangedAPIKeyCredential` | codex_accounts_test.go:2096: codex directory ancestor ACL is unsafe |
| `TestProbeRecognizesFirstCodexLoginWithoutDaemonRestart` | codex_accounts_test.go:1824: codex directory ancestor ACL is unsafe |
| `TestRecentVerifiedReconciliationDoesNotScheduleAnotherRead` | codex_accounts_test.go:2025: codex directory ancestor ACL is unsafe |
| `TestRecheckAfterExternalLoginClearsStaleReauthentication` | codex_accounts_test.go:2425: codex directory ancestor ACL is unsafe |
| `TestRecheckAfterExternalLoginFindsTheNewDeviceCredential` | codex_accounts_test.go:2481: codex directory ancestor ACL is unsafe |
| `TestRepeatedReconciliationRequestsJoinOneBackgroundRead` | codex_accounts_test.go:2044: codex directory ancestor ACL is unsafe |
| `TestRepeatedSettingsEnsuresReuseProtectedConfirmation` | codex_account_launch_readiness_test.go:287: codex directory ancestor ACL is unsafe |
| `TestRequiredReauthenticationStaysSignedOutUntilLogin` | codex_accounts_test.go:817: codex directory ancestor ACL is unsafe |
| `TestRotatedCredentialsDoNotReVerifyAnAuthorizedAccount` | codex_account_launch_readiness_test.go:319: codex directory ancestor ACL is unsafe |
| `TestServiceInactiveLogoutDoesNotDependOnDeviceReconciliation` | codex_accounts_test.go:932: codex directory ancestor ACL is unsafe |
| `TestSettingsUsesProtectedCallInsteadOfRefreshAccountRead` | codex_account_launch_readiness_test.go:163: codex directory ancestor ACL is unsafe |
| `TestSuccessfulReauthenticationKeepsCapacitySeparateFromLaunchReadiness` | codex_account_launch_readiness_test.go:269: codex directory ancestor ACL is unsafe |
| `TestSwitchAdmissionDoesNotOpenCodexWhenProviderIsUnavailable` | codex_accounts_test.go:2362: codex directory ancestor ACL is unsafe |
| `TestSwitchAdmissionIgnoresCachedUnauthorizedState` | codex_accounts_test.go:2396: codex directory ancestor ACL is unsafe |
| `TestSwitchAdmissionIgnoresStaleAuthenticationFailure` | codex_accounts_test.go:2386: codex directory ancestor ACL is unsafe |
| `TestUnsupportedNativeLogoutPreservesAccountCredential` | codex_accounts_test.go:880: codex directory ancestor ACL is unsafe |
| `TestUnverifiedDeviceAssociationUsesSavedHomeDuringTemporaryReconciliationFailure` | codex_accounts_test.go:295: codex directory ancestor ACL is unsafe |
| `TestUserAuthenticationRetryBypassesFreshCachesAndBackoff` | codex_account_launch_readiness_test.go:304: codex directory ancestor ACL is unsafe |
| `TestVerifySwitchTargetDoesNotRequireManagedDeviceSource` | codex_accounts_test.go:1999: codex directory ancestor ACL is unsafe |

**`internal/service/agentauth`** — 2 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestStartPreparesCopilotAuthWorkspaceWithSeededTrust` | service_test.go:450: read copilot config: open C:\Users\11459\AppData\Local\Temp\TestStartPreparesCopilotAuthWorkspaceWithSeededTrust1076127172\001\.copilot\config.json: The system cannot fi… |
| `TestStartPreparesKimiAuthWorkspaceWithSeededTrust` | service_test.go:407: seeded trust records = [] (err <nil>), want exactly one |

**`internal/service/chat`** — 4 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestHandoffHistoryFilesPointAtAgentsAOHasNoAnswersFor` | handoff_history_files_test.go:35: mkdir C:\Users\11459\AppData\Local\Temp\TestHandoffHistoryFilesPointAtAgentsAOHasNoAnswersFor3612106944\001\.factory\sessions\C:: The filename, directory na… |
| `TestHandoffHistoryFilesSkipChatsOlderThanTheSession` | handoff_history_files_test.go:68: mkdir C:\Users\11459\AppData\Local\Temp\TestHandoffHistoryFilesSkipChatsOlderThanTheSession3500494333\001\.factory\sessions\C:: The filename, directory name… |
| `TestRecordReportedArtifactSkipsWhatItCannotShow` | artifact_test.go:108: symlink C:\Users\11459\AppData\Local\Temp\TestRecordReportedArtifactSkipsWhatItCannotShow390586863\004\outside.html C:\Users\11459\AppData\Local\Temp\TestRecordReported… |
| `TestSaveRenderAsArtifactWritesTheServedPage` | render_test.go:436: mode = -rw-rw-rw-, want 0600 |

**`internal/service/fsbrowser`** — 2 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestListDirectorySymlinks` | service_test.go:35: |
| `TestListSymlinkEntryLimit` | service_test.go:74: |

**`internal/service/importer`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestValidateProjectImportRejectsAOStatePath` | importer_test.go:198: result = importer.ImportValidationResult{ImportKind:"project", IsValid:true, BlockingErrors:[]string{}, Root:importer.RepoGitStatus{RepoPath:"C:\\Users\\11459\\AppData\… |

**`internal/service/project`** — 17 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestCloneDestinationPreflightPreservesExistingFolders` | service_test.go:2387: available destination: {RemoteURL: Path: Exists:false CanUseExisting:false}, Enter a valid HTTPS, SSH, Git, or file repository URL. |
| `TestCloneDestinationPreflightRecognizesPreparedAndRegisteredCheckout` | service_test.go:2415: Enter a valid HTTPS, SSH, Git, or file repository URL. |
| `TestEmptyCloneOnboardingCreatesFirstWorkspace` | service_test.go:2339: prepare clone: Enter a valid HTTPS, SSH, Git, or file repository URL. |
| `TestKeepPreparedCloneReleasesCleanupOwnership` | service_test.go:2448: Enter a valid HTTPS, SSH, Git, or file repository URL. |
| `TestManager_AddPreparedCloneRejectsStalePreparationID` | service_test.go:374: PrepareClone: Enter a valid HTTPS, SSH, Git, or file repository URL. |
| `TestManager_CleanupPreparedClonePreservesRegisteredProject` | service_test.go:395: PrepareClone: Enter a valid HTTPS, SSH, Git, or file repository URL. |
| `TestManager_CleanupPreparedCloneRejectsStalePreparationID` | service_test.go:358: PrepareClone: Enter a valid HTTPS, SSH, Git, or file repository URL. |
| `TestManager_CloneCleansUpFailedAndEmptyCheckouts` | service_test.go:585: code = "INVALID_GIT_URL", want "GIT_CLONE_FAILED" |
| `TestManager_CloneRegistersRepositoryAndPreservesOrigin` | service_test.go:256: Clone: Enter a valid HTTPS, SSH, Git, or file repository URL. |
| `TestManager_InitializeRepositoryRecovery` | service_test.go:1158: show-toplevel = "C:/Users/11459/AppData/Local/Temp/TestManager_InitializeRepositoryRecoveryplain_folder_nested_in_4065096220/001/parent/universe", want "C:\\Users\\1145… |
| `TestManager_PrepareCloneCancellationLeavesNoCheckout` | service_test.go:431: code = "INVALID_GIT_URL", want "GIT_CLONE_CANCELLED" |
| `TestManager_PrepareCloneDoesNotReclaimExistingCheckoutAfterRestart` | service_test.go:465: Enter a valid HTTPS, SSH, Git, or file repository URL. |
| `TestManager_PrepareClonePreservesEmptyRepositoryForImportSetup` | service_test.go:282: PrepareClone: Enter a valid HTTPS, SSH, Git, or file repository URL. |
| `TestManager_PrepareCloneReportsRequestTimeout` | service_test.go:448: code = "INVALID_GIT_URL", want "GIT_CLONE_TIMED_OUT" |
| `TestManager_PreparedCloneCanBeCleanedUpAfterAddFailure` | service_test.go:314: PrepareClone: Enter a valid HTTPS, SSH, Git, or file repository URL. |
| `TestPrepareCloneCreatesDestinationParents` | service_test.go:2315: PrepareClone: Enter a valid HTTPS, SSH, Git, or file repository URL. |
| `TestPreparedCloneRegistrationRetryFindsRegisteredProject` | service_test.go:2433: PrepareClone: Enter a valid HTTPS, SSH, Git, or file repository URL. |

**`internal/service/session`** — 3 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestPRCheckoutByVerifiedSHAWithFETCHHEADBranch` | claim_checkout_test.go:40: PR working file = "PR contents\r\n", err=<nil> |
| `TestWorkspaceFileRevisionLiveReadAvoidsGitAndChecksRevision` | workspace_paths_test.go:127: symlink C:\Users\11459\AppData\Local\Temp\TestWorkspaceFileRevisionLiveReadAvoidsGitAndChecksRevision2730002884\003\secret.txt C:\Users\11459\AppData\Local\Temp\… |
| `TestWorkspaceLocationReportsUnreadableWorkspace` | workspace_location_test.go:86: error = <nil>, want API code SESSION_WORKSPACE_UNREADABLE |

**`internal/service/systemcheck`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestCheck_TmuxMissing` | systemcheck_test.go:207: Ready = true, want false |

**`internal/service/systeminstall`** — 9 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestBrewCleanupRetainsDetachedProviderHub` | managed_processes_test.go:81: cleanup did not run after provider exit: [] |
| `TestDroidDownloadsThePinnedReleaseWithoutItsScript` | managed_official_test.go:186: argv = ["/bin/sh" "-c" "set -eu\nurl=\"$1\"; sum_url=\"$2\"; dest=\"$3\"\nmkdir -p \"$(dirname \"$dest\")\"\ntmp=\"$dest.download\"\ncurl -fsSL -o \"$tmp\" \"$u… |
| `TestKeptKegIsCleanedUpOnceSessionsMove` | version_refresh_test.go:106: cleanups = 0, want exactly one once sessions moved |
| `TestKimiUpdateReinstallsAdvisoryReleaseIntoConfirmedDirectory` | systeminstall_test.go:1696: installer environment = [CI=1 NONINTERACTIVE=1 HOMEBREW_NO_AUTO_UPDATE=1 NPM_CONFIG_AUDIT=false NPM_CONFIG_FUND=false KIMI_INSTALL_DIR=C:\Users\11459\AppData\Loca… |
| `TestLaunchDropsAdvisoryForBinaryReplacedOutsideAO` | update_gate_test.go:380: symlink C:\Users\11459\AppData\Local\Temp\TestLaunchDropsAdvisoryForBinaryReplacedOutsideAO3389440008\001\versions\2.1.0 C:\Users\11459\AppData\Local\Temp\TestLaunch… |
| `TestManagedProcessReferencesRespectReleaseBoundaries` | managed_processes_test.go:101: release referenced=false want true |
| `TestManagerOwnsBinaryTracesSymlinkIntoNPMPackage` | update_advisory_test.go:456: symlink C:\Users\11459\AppData\Local\Temp\TestManagerOwnsBinaryTracesSymlinkIntoNPMPackage3548357153\001\node_modules\@openai\codex\bin\codex.js C:\Users\11459\A… |
| `TestNPMPlanRequiresWritableGlobalPrefix` | agentplans_test.go:674: plan = {Target:codex Command:[npm install -g @openai/codex] Prepare:[] Cleanup:[] Script:<nil> Package:@openai/codex PackagePrefix:/Users/test/.npm PackageCask:false … |
| `TestOfficialManagedRecipesInstallInsideTheRelease` | managed_official_test.go:67: HOME = "\\data\\harnesses\\x\\versions\\v3\\.install-home", want a directory inside the release |

**`internal/service/usage`** — 3 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestCollectorContinuationDiscoveryIsIdempotent` | collector_continuation_test.go:95: bindings = map[native-a:1], want exactly [native-a 6d19d8df-continuation] |
| `TestCollectorDiscoversClaudeContinuationInWorkspaceDir` | collector_continuation_test.go:70: continuation binding "6d19d8df-continuation" not registered; bindings=map[native-a:1] |
| `TestDefaultSourceRootsFollowsClaudeConfigDir` | collector_test.go:1878: Claude projects = "C:\\Users\\11459\\.claude\\projects", want "C:\\Users\\11459\\AppData\\Local\\Temp\\TestDefaultSourceRootsFollowsClaudeConfigDir4108494539\\001\\.c… |

**`internal/session_manager`** — 7 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestChatStartsPassTheAOToolServerWithOnlyDaemonCoordinates` | chat_spawn_test.go:1480: start 0: MCPServers = [], want [{Name:ao Type:stdio Command:/opt/ao/bin/ao Args:[mcp] Env:map[AO_DATA_DIR:/ao-test-data AO_RUN_FILE:/ao-test/running.json AO_SESSION_… |
| `TestInterfaceTransitionNativeHistoryOwnership` | interface_transition_history_integration_test.go:173: first switch: failed agent requires authentication |
| `TestInterfaceTransitionReservedTranscriptRequiresUntouchedTerminal` | interface_transition_fresh_terminal_test.go:165: unsafe fresh handoff = <nil> |
| `TestSend_NoEnterNudgeAfterTheOperatorTypes` | send_composer_guard_test.go:494: Send calls = 3, want 1: Enter nudged into the operator's typing (["" ""]) |
| `TestSpawnAndRestore_PrependsResolvedBinaryAndNodeDirsToRuntimePATH` | manager_test.go:8238: runtime env PATH = "C:\\Users\\11459\\AppData\\Local\\Temp\\TestSpawnAndRestore_PrependsResolvedBinaryAndNodeDirsToRuntimePA2206825718\\001;C:\\Users\\11459\\AppData\\L… |
| `TestSpawn_DefaultsBranchUnderDevNamespaceForDevDataDir` | manager_test.go:5901: worker branch = "ao/mer-1/root", want ao/dev/mer-1/root |
| `TestSpawn_DoesNotAddNodeRuntimeForNativeBinary` | manager_test.go:8307: runtime env PATH = "\\ao\\bin;C:\\Users\\11459\\AppData\\Local\\Temp\\TestSpawn_DoesNotAddNodeRuntimeForNativeBinary266757314\\001\\native\\bin;/usr/bin", want "/ao/bin… |

**`internal/tmuxbin`** — 1 失败

| 测试 | 失败信息（截断） |
| --- | --- |
| `TestResolveWithFollowsExecutableSymlinkIntoBundle` | resolve_test.go:84: symlink C:\Users\11459\AppData\Local\Temp\TestResolveWithFollowsExecutableSymlinkIntoBundle1061418535\001\AO.app\Contents\Resources\daemon\ao C:\Users\11459\AppData\Local… |

### 5.4 全包结果表（228 个包）

| 包 | 结果 | 耗时 | 失败测试数 |
| --- | --- | --- | --- |
| `internal/adapters/agent/agy` | FAIL | 1.089s | 2 |
| `internal/adapters/agent/aider` | FAIL | 0.777s | 1 |
| `internal/adapters/agent/claudecode` | FAIL | 4.636s | 3 |
| `internal/adapters/agent/codex` | FAIL | 1.768s | 1 |
| `internal/adapters/agent/commandcode` | FAIL | 9.146s | 1 |
| `internal/adapters/agent/copilot` | FAIL | 3.272s | 2 |
| `internal/adapters/agent/fake` | FAIL | 4.046s | 2 |
| `internal/adapters/agent/kilocode` | FAIL | 0.657s | 1 |
| `internal/adapters/agent/kimchi` | FAIL | 1.802s | 1 |
| `internal/adapters/agent/kimi` | FAIL | 5.618s | 6 |
| `internal/adapters/agent/modelcatalog` | FAIL | 54.104s | 7 |
| `internal/adapters/agent/opencode` | FAIL | 10.707s | 1 |
| `internal/adapters/agent/pi` | FAIL | 2.652s | 2 |
| `internal/adapters/agent/primeagent` | FAIL | 2.483s | 2 |
| `internal/adapters/agent/registry` | FAIL | 7.846s | 1 |
| `internal/adapters/agentaccount` | FAIL | 1.574s | 5 |
| `internal/adapters/chatdriver/agyacp` | FAIL | 2.106s | 1 |
| `internal/adapters/chatdriver/codexappserver` | FAIL | 7.858s | 48 |
| `internal/adapters/chatdriver/cursoracp` | FAIL | 0.462s | 1 |
| `internal/adapters/chatdriver/kimiacp` | FAIL | 21.173s | 1 |
| `internal/adapters/chatdriver/persistenthost` | FAIL | 29.609s | 1 |
| `internal/adapters/chatdriver/processenv` | FAIL | 1.654s | 1 |
| `internal/adapters/reviewer/agy` | FAIL | 0.320s | 1 |
| `internal/adapters/reviewer/claudecode` | FAIL | 0.505s | 1 |
| `internal/adapters/reviewer/cursor` | FAIL | 0.781s | 1 |
| `internal/adapters/reviewer/devin` | FAIL | 0.425s | 1 |
| `internal/adapters/reviewer/droid` | FAIL | 0.447s | 2 |
| `internal/adapters/reviewer/kimi` | FAIL | 0.299s | 2 |
| `internal/adapters/runtime/conpty` | FAIL | 10.624s | 1 |
| `internal/adapters/runtime/tmux` | FAIL | 3.664s | 3 |
| `internal/adapters/telemetry` | FAIL | 15.851s | 1 |
| `internal/adapters/workspace/gitworktree` | FAIL | 390.329s | 4 |
| `internal/agentlaunch` | FAIL | 1.917s | 2 |
| `internal/browserchrome` | FAIL | 0.888s | 2 |
| `internal/cli` | FAIL | 84.642s | 1 |
| `internal/daemon` | FAIL | 22.044s | 2 |
| `internal/domain` | FAIL | 5.518s | 2 |
| `internal/httpd/controllers` | FAIL | 78.197s | 4 |
| `internal/integration` | FAIL | 15.659s | 1 |
| `internal/mobilebridge` | FAIL | 3.651s | 1 |
| `internal/observe/usage` | FAIL | 20.933s | 4 |
| `internal/pricing` | FAIL | 2.076s | 2 |
| `internal/review` | FAIL | 5.779s | 1 |
| `internal/reviewgateway` | FAIL | 0.463s | 2 |
| `internal/service/agent` | FAIL | 17.524s | 85 |
| `internal/service/agentauth` | FAIL | 0.331s | 2 |
| `internal/service/chat` | FAIL | 165.816s | 4 |
| `internal/service/fsbrowser` | FAIL | 0.427s | 2 |
| `internal/service/importer` | FAIL | 90.332s | 1 |
| `internal/service/project` | FAIL | 141.931s | 17 |
| `internal/service/session` | FAIL | 374.538s | 3 |
| `internal/service/systemcheck` | FAIL | 3.278s | 1 |
| `internal/service/systeminstall` | FAIL | 15.603s | 9 |
| `internal/service/usage` | FAIL | 14.250s | 3 |
| `internal/session_manager` | FAIL | 274.382s | 7 |
| `internal/tmuxbin` | FAIL | 0.279s | 1 |
| `cmd/ao` | NO TEST FILES | — | — |
| `cmd/gencodexproto` | NO TEST FILES | — | — |
| `cmd/genspec` | NO TEST FILES | — | — |
| `cmd/pricingcatalog` | PASS | 0.387s | — |
| `(module root)` | NO TEST FILES | — | — |
| `internal/adapters` | NO TEST FILES | — | — |
| `internal/adapters/agent/activitydispatch` | PASS | 0.364s | — |
| `internal/adapters/agent/activitystate` | PASS | 0.324s | — |
| `internal/adapters/agent/agentbase` | PASS | 0.348s | — |
| `internal/adapters/agent/amp` | PASS | 0.762s | — |
| `internal/adapters/agent/auggie` | PASS | 1.296s | — |
| `internal/adapters/agent/authprobe` | PASS | 0.522s | — |
| `internal/adapters/agent/autohand` | PASS | 0.626s | — |
| `internal/adapters/agent/binaryutil` | PASS | 0.320s | — |
| `internal/adapters/agent/cline` | PASS | 1.566s | — |
| `internal/adapters/agent/codewhale` | PASS | 0.584s | — |
| `internal/adapters/agent/continueagent` | PASS | 0.554s | — |
| `internal/adapters/agent/crush` | PASS | 1.308s | — |
| `internal/adapters/agent/cursor` | PASS | 1.260s | — |
| `internal/adapters/agent/deepseekharness` | PASS | 1.120s | — |
| `internal/adapters/agent/devin` | PASS | 1.243s | — |
| `internal/adapters/agent/droid` | PASS | 0.944s | — |
| `internal/adapters/agent/fx` | PASS | 0.618s | — |
| `internal/adapters/agent/fx/herdr` | PASS | 0.579s | — |
| `internal/adapters/agent/gemini` | PASS | 1.273s | — |
| `internal/adapters/agent/goose` | PASS | 0.889s | — |
| `internal/adapters/agent/grok` | PASS | 0.697s | — |
| `internal/adapters/agent/hermes` | PASS | 24.454s | — |
| `internal/adapters/agent/hooksjson` | PASS | 0.354s | — |
| `internal/adapters/agent/hookutil` | PASS | 0.346s | — |
| `internal/adapters/agent/kiro` | PASS | 1.484s | — |
| `internal/adapters/agent/memcode` | PASS | 2.281s | — |
| `internal/adapters/agent/mimocode` | PASS | 1.240s | — |
| `internal/adapters/agent/muse` | PASS | 2.176s | — |
| `internal/adapters/agent/nativeconfig` | NO TEST FILES | — | — |
| `internal/adapters/agent/omp` | PASS | 0.881s | — |
| `internal/adapters/agent/opencodev2` | PASS | 1.600s | — |
| `internal/adapters/agent/openhands` | PASS | 0.990s | — |
| `internal/adapters/agent/qwen` | PASS | 2.496s | — |
| `internal/adapters/agent/terminalui` | PASS | 0.257s | — |
| `internal/adapters/agent/unrealagent` | PASS | 0.321s | — |
| `internal/adapters/agent/vibe` | PASS | 2.642s | — |
| `internal/adapters/chatdriver/acp` | PASS | 43.836s | — |
| `internal/adapters/chatdriver/citation` | PASS | 0.203s | — |
| `internal/adapters/chatdriver/claudeacp` | PASS | 4.356s | — |
| `internal/adapters/chatdriver/codexappserver/codexproto` | PASS | 0.359s | — |
| `internal/adapters/chatdriver/commandcodeacp` | PASS | 0.528s | — |
| `internal/adapters/chatdriver/commanddetail` | PASS | 0.223s | — |
| `internal/adapters/chatdriver/copilotacp` | PASS | 0.251s | — |
| `internal/adapters/chatdriver/deepseekharnessacp` | PASS | 0.406s | — |
| `internal/adapters/chatdriver/droidacp` | PASS | 0.399s | — |
| `internal/adapters/chatdriver/geminiacp` | PASS | 0.295s | — |
| `internal/adapters/chatdriver/kimchiacp` | PASS | 0.343s | — |
| `internal/adapters/chatdriver/nativeacp` | PASS | 0.322s | — |
| `internal/adapters/chatdriver/ompacp` | PASS | 0.290s | — |
| `internal/adapters/chatdriver/opencodeacp` | PASS | 0.387s | — |
| `internal/adapters/chatdriver/opencodeidentity` | NO TEST FILES | — | — |
| `internal/adapters/chatdriver/opencodev2acp` | PASS | 0.400s | — |
| `internal/adapters/chatdriver/piacp` | PASS | 0.939s | — |
| `internal/adapters/chatdriver/qwenacp` | PASS | 0.396s | — |
| `internal/adapters/chatdriver/registry` | PASS | 0.342s | — |
| `internal/adapters/chatdriver/unrealagent` | NO TEST FILES | — | — |
| `internal/adapters/container/dockerreap` | PASS | 0.373s | — |
| `internal/adapters/cuadriver` | NO TEST FILES | — | — |
| `internal/adapters/reviewer` | PASS | 0.371s | — |
| `internal/adapters/reviewer/agentrestore` | NO TEST FILES | — | — |
| `internal/adapters/reviewer/aider` | PASS | 0.362s | — |
| `internal/adapters/reviewer/amp` | PASS | 0.652s | — |
| `internal/adapters/reviewer/auggie` | PASS | 0.375s | — |
| `internal/adapters/reviewer/autohand` | PASS | 0.481s | — |
| `internal/adapters/reviewer/cline` | PASS | 0.362s | — |
| `internal/adapters/reviewer/codex` | PASS | 0.355s | — |
| `internal/adapters/reviewer/copilot` | PASS | 0.331s | — |
| `internal/adapters/reviewer/crush` | PASS | 0.302s | — |
| `internal/adapters/reviewer/grok` | PASS | 0.389s | — |
| `internal/adapters/reviewer/kilocode` | PASS | 0.769s | — |
| `internal/adapters/reviewer/kimchi` | PASS | 0.352s | — |
| `internal/adapters/reviewer/kiro` | PASS | 0.302s | — |
| `internal/adapters/reviewer/muse` | PASS | 0.311s | — |
| `internal/adapters/reviewer/opencode` | PASS | 0.568s | — |
| `internal/adapters/reviewer/opencodev2` | PASS | 0.371s | — |
| `internal/adapters/reviewer/pi` | PASS | 0.553s | — |
| `internal/adapters/runtime/conpty/ptyregistry` | PASS | 0.745s | — |
| `internal/adapters/runtime/inputdelay` | PASS | 0.274s | — |
| `internal/adapters/runtime/ptyexec` | PASS | 0.463s | — |
| `internal/adapters/runtime/runtimeselect` | PASS | 0.381s | — |
| `internal/adapters/scm/github` | PASS | 0.943s | — |
| `internal/adapters/scm/gitlab` | PASS | 1.630s | — |
| `internal/adapters/scm/multi` | PASS | 0.340s | — |
| `internal/adapters/systemexec` | PASS | 6.994s | — |
| `internal/adapters/telemetry/policyauthority` | PASS | 0.387s | — |
| `internal/adapters/tracker/github` | PASS | 0.453s | — |
| `internal/adapters/tracker/gitlab` | PASS | 0.486s | — |
| `internal/adapters/tracker/httpkit` | PASS | 0.314s | — |
| `internal/adapters/tracker/multi` | PASS | 0.265s | — |
| `internal/adapters/workspace/router` | PASS | 0.323s | — |
| `internal/adapters/workspace/scratch` | PASS | 0.494s | — |
| `internal/artifactversions` | PASS | 9.390s | — |
| `internal/attachmentstore` | PASS | 1.681s | — |
| `internal/autoreview` | PASS | 0.390s | — |
| `internal/browserruntime` | PASS | 1.348s | — |
| `internal/browserstream` | PASS | 0.412s | — |
| `internal/cdc` | PASS | 3.140s | — |
| `internal/codexops` | PASS | 0.391s | — |
| `internal/config` | PASS | 3.991s | — |
| `internal/daemon/supervisor` | PASS | 3.837s | — |
| `internal/daemonmeta` | NO TEST FILES | — | — |
| `internal/datadirlock` | PASS | 4.157s | — |
| `internal/deliverytelemetry` | PASS | 3.419s | — |
| `internal/devimport` | PASS | 7.223s | — |
| `internal/gitdefault` | PASS | 53.720s | — |
| `internal/httpd` | PASS | 12.165s | — |
| `internal/httpd/apierr` | NO TEST FILES | — | — |
| `internal/httpd/apispec` | PASS | 2.878s | — |
| `internal/httpd/apispec/specgen` | PASS | 36.209s | — |
| `internal/httpd/envelope` | PASS | 0.440s | — |
| `internal/httpd/requestscope` | NO TEST FILES | — | — |
| `internal/legacyimport` | PASS | 0.416s | — |
| `internal/lifecycle` | PASS | 3.419s | — |
| `internal/notify` | PASS | 0.240s | — |
| `internal/observe` | PASS | 0.390s | — |
| `internal/observe/activity` | PASS | 4.578s | — |
| `internal/observe/artifacts` | PASS | 0.261s | — |
| `internal/observe/automations` | PASS | 0.229s | — |
| `internal/observe/ownership` | PASS | 0.234s | — |
| `internal/observe/reaper` | PASS | 0.305s | — |
| `internal/observe/scm` | PASS | 62.415s | — |
| `internal/observe/sentryobs` | PASS | 0.367s | — |
| `internal/observe/trackerintake` | PASS | 0.304s | — |
| `internal/ports` | PASS | 0.549s | — |
| `internal/presence` | PASS | 0.465s | — |
| `internal/preview` | PASS | 0.604s | — |
| `internal/previewserver` | PASS | 39.359s | — |
| `internal/pricing/catalogsync` | PASS | 1.111s | — |
| `internal/process` | PASS | 1.549s | — |
| `internal/processalive` | NO TEST FILES | — | — |
| `internal/procmem` | PASS | 0.366s | — |
| `internal/push` | PASS | 2.294s | — |
| `internal/renderpage` | PASS | 0.278s | — |
| `internal/reqid` | NO TEST FILES | — | — |
| `internal/runfile` | PASS | 0.386s | — |
| `internal/service/agentaccounts` | PASS | 0.552s | — |
| `internal/service/automation` | PASS | 0.232s | — |
| `internal/service/awake` | PASS | 0.304s | — |
| `internal/service/browser` | PASS | 0.329s | — |
| `internal/service/computeruse` | PASS | 5.631s | — |
| `internal/service/cue` | PASS | 0.260s | — |
| `internal/service/devimport` | PASS | 3.183s | — |
| `internal/service/githubpat` | PASS | 0.390s | — |
| `internal/service/hookprocess` | PASS | 2.996s | — |
| `internal/service/linkpreview` | PASS | 0.285s | — |
| `internal/service/notification` | PASS | 0.300s | — |
| `internal/service/pr` | PASS | 0.383s | — |
| `internal/service/report` | PASS | 0.242s | — |
| `internal/service/review` | PASS | 0.418s | — |
| `internal/service/settings` | PASS | 0.254s | — |
| `internal/service/shellterm` | PASS | 16.195s | — |
| `internal/service/workflow` | PASS | 3.721s | — |
| `internal/sessionartifacts` | PASS | 32.915s | — |
| `internal/sessionguard` | PASS | 0.381s | — |
| `internal/skillassets` | PASS | 0.468s | — |
| `internal/storage/sqlite` | PASS | 151.951s | — |
| `internal/storage/sqlite/gen` | NO TEST FILES | — | — |
| `internal/storage/sqlite/sqlitetest` | PASS | 3.055s | — |
| `internal/storage/sqlite/store` | PASS | 74.533s | — |
| `internal/telemetrymeta` | PASS | 0.242s | — |
| `internal/terminal` | PASS | 5.691s | — |
| `internal/termtheme` | PASS | 0.346s | — |
| `internal/workspacewatch` | PASS | 7.417s | — |
| `pkg/agentcreds` | PASS | 1.105s | — |
| `pkg/agentruntime` | PASS | 0.236s | — |
| `pkg/contract` | PASS | 0.354s | — |

## 6. 基线与 HEAD 的集合差（`623871d` → `a6c284a4`）

| 方向 | 条数 |
| --- | --- |
| 基线失败 | 241 |
| HEAD 失败 | 267 |
| **HEAD 相对基线新增** | **31** |
| **基线独有（HEAD 已绿）** | **5** |

### 6.1 新增 31 条的归因（**均非 M1/M2 回归**）

- **`internal/adapters/chatdriver/codexappserver`（30 条）**：该包在测试进程内触发
  `panic: Fail in goroutine after TestResumeFailureDoesNotFallBackToStart has completed`
  （`driver_test.go:117` → `scriptedServer.awaitFrame`）。**panic 会中止同包剩余测试**，因此一次 `go test` 里该包报出多少条失败取决于 panic 发生的时刻。
  实测：**单独**运行该包稳定得到 **58 条失败 + 1 次 panic**（连跑 3 次完全相同）；在 `./...` 并发负载下只跑到 48 条就 panic。
  基线仓单独运行同样稳定得到 **58 条失败 + 1 次 panic**，且两次的失败测试集合**完全相等**（`bcx1.log` vs `cx1.log`：`equal=True`）。
  所以该包真实的既有失败集合是 58 条，`./...` 里的 48 条只是 panic 截断的产物，`31` 这个差值同样只是截断时机不同造成的假差。
- **`internal/adapters/telemetry:TestDestinationSinkConcurrentEmitAndClose`**：`shutdown_test.go:112: timed out waiting for telemetry worker or producer`（11.25s）。
  单独运行该包两次均 **exit 0 全绿**，属并发负载下的计时敏感 flaky；`telemetry` 包未被 M1 修改。

> **净结论：M1 相对基线的真实新增回归 = 0。** 本文件记录的 267 条是全仓 Windows 红基线，M2 应以「本文件 §5.3 的 267 条集合」为对照基准，而不是以全绿为前提。

### 6.2 基线独有、HEAD 已绿的 5 条

| 包 | 测试 |
| --- | --- |
| `internal/adapters/agent/gemini` | `TestVersionProbeAcceptsSupportedCLIAndRejectsOldCLI` |
| `internal/adapters/agent/opencode` | `TestResolveBinaryForMajorAcceptsSlowShim` |
| `internal/adapters/reviewer/opencode` | `TestReviewCommandBuildsBothOpenCodeConfigSources` |
| `internal/daemon` | `TestWiring_NewMultiTracker_DoesNotProbeGHCLIAtBoot` |
| `internal/session_manager` | `TestSpawn_AfterStartPromptReservesCallerDeadlineForFallbackDelivery` |

这 5 条与 M1 改动无关（涉及 `agent/gemini`、`agent/opencode`、`reviewer/opencode`、`daemon`、`session_manager`），属 flaky：单独复跑 `TestWiring_NewMultiTracker_DoesNotProbeGHCLIAtBoot` 在 HEAD 上 `ok`，单独复跑 `agent/gemini`、`reviewer/opencode` 包在 HEAD 上均 `ok`。

## 7. 结论与 M2 使用方式

1. **命令退出码**：`go build ./...` = **0**；`go vet <目标包>` = **0**；`go test <M1 目标包> -count=1` = **0**；`go test ./... -count=1` = **1**（两次复跑逐条一致）。
2. **当前失败**：56 个包、267 条测试失败，全部为 Windows 环境或既有跨平台缺陷，明细见 §5.3。
3. **声称的 8 条基线失败：全部复现**（`domain` 2、`httpd/controllers` 4、`daemon` 2），且在 `623871d` 上同样失败。
4. **无「无法测试」的包**：228 个包全部跑完，无 hang（最慢 `internal/adapters/workspace/gitworktree` 390s、`internal/service/session` 375s、`internal/session_manager` 274s，均远低于 10 分钟阈值）。
5. **M2 判红 / 判绿方法**：以 §5.3 的 267 条集合为基线，M2 改动后重跑 `go test ./... -count=1` 做集合差；**新增失败即为 M2 回归**。判定时须注意两处已知噪声：
   - `codexappserver` 的 panic 截断效应 → 单独 `go test ./internal/adapters/chatdriver/codexappserver -count=1` 复核，其基线为 **58 条失败**；
   - `telemetry` 的计时 flaky → 单独复跑该包确认。
