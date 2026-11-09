# M6 执行记录

最新状态（2026-11-09）：已将Forge原生输出校验移至构建后，唯一续接取得npm自然exit1；观察器无异常。Electron ZIP缓存命中后，SHASUMS及回退ZIP均在GitHub资产域名TLS建立前ECONNRESET。构建79.690秒，回收1个残留成员后包装exit95，无进程残留；原生meta/二进制标识为ABI149，未运行模块，无Windows候选。已停止，M6整体仍FAIL。

状态：本轮 lint 收口交接，M6 未通过，未提交。五包 lint exit 0、`0 issues.`；一次完整复核 exit 4，763.125 秒，252 项诊断并超时。新增 Windows 分离宿主用例连续三轮失败后已停止修改/重试。主仓基线 `acf9bafa8c14de5533a35f66d4d2c9bfc057baae`，最新 204 文件源码身份见下文。前轮九条 Windows 补验通过保留；完整打包及 gitworktree 失败未改变。旧 runner 未隔离用户目录，已确认污染真实 Claude 配置，撤回“所有结果均为隔离环境”的保证；旧结果保留为问题证据，不代表安全隔离或发布验收通过。

## 测试用户目录污染（优先阻断项）

- 完整 Go 检查 `m4-fast-backend-full` 已于 UTC `2026-11-07T19:45:53` 退出，exit 1，1647.052 秒。最终包统计：174 PASS、50 FAIL、22 SKIP；测试事件 17333 PASS、315 FAIL、357 SKIP（含父子 subtest，不是独立用例数）。原始日志和 `m4-fast-backend-full-analysis.json` 保留。
- 旧 `run.py` 继承父环境，仅覆盖 TEMP/TMP/GOTMPDIR；本执行环境 `USERPROFILE=C:\Users\11459`，HOME/AO_DATA_DIR/CODEX_HOME/CLAUDE_CONFIG_DIR 未设置。统筹的 `supervisor-config-audit.py/.json` 已确认真实 `.claude.json` 出现 8 条本轮临时工作区记录，修改时间 UTC 19:19:09。Claude fixture 只设置 HOME，而 Windows `os.UserHomeDir` 使用 USERPROFILE。执行方没有读取凭据或主动编辑真实配置；测试污染仍未清理，精确回退尚待用户明确授权，执行方与统筹均不得自行修改。
- 本执行方只读进程检查未发现已知 Go PID 链、`*.test.exe`、私有临时目录或隔离副本中的残留可执行进程。未批量终止任何进程。
- 后续 runner 改为系统变量白名单继承，在每个子进程单独设置 USERPROFILE/HOME、APPDATA/LOCALAPPDATA、AO/Provider/XDG 目录及独立 Git/npm 配置路径；不继承 Provider 凭据或 live-test 开关，不改系统环境。每次运行写入路径身份和布尔证据。Go 模块缓存复用已知 C 盘缓存，仅作为依赖缓存；目录隔离不等于 OS 沙箱。
- 隔离预检：`run.py isolation-paths backend go run C:/Users/11459/AppData/Local/Temp/ao-m6-safe/isolation-paths.go`，exit 0、2.346 秒。实际 Go 子进程 `os.UserHomeDir()`、HOME、USERPROFILE 均为 `C:/ao-m6-tests-507820bdd394/run-users/isolation-paths`；AO/Provider/AppData/XDG 均位于其下，`providerCredentialsPresent=false`。只输出路径和布尔，没有读取真实配置；证据 `isolation-paths.log/.json`。后续测试恢复仅使用该 runner。
- SV-M6-03：修复前 `sv03-red` exit 1，真实 Driver 的直接/持久准备路径均复现秘密继承，真实持久宿主子进程也报告泄漏，公用凭据名被错误接受。修复后六包定向 `sv01-03-focused` exit 0、69.671 秒，65 个测试事件 PASS、无 FAIL/SKIP，三份真实配置哈希及 mtime 未漂移。该结果同时覆盖 SV-M6-01 后台 effort、SV-M6-02 空 effort/原生请求、SV-M6-03 凭据隔离；后续相关完整包与 race 结果见下表。
- `isolation-paths-guarded` 预检 exit 0、0.934 秒，实际子进程目录与无父凭据检查通过，真实 `.claude.json`、`.claude/settings.json`、`.codex/config.toml` 前后哈希和 mtime 相同。后续 runner 如检测漂移，记录实际 `commandExitCode`，总结果固定 exit 97，并写 `isolation-blocked.json` 阻止后续运行；不自动清理真实配置。

## 当前变更与证据

证据目录：`C:/Users/11459/AppData/Local/Temp/ao-m6-safe/`。命令在 C 盘副本 `C:/Users/11459/.codex/worktrees/m5-desktop-safety/agent-orchestrator` 中执行。`baseline-source.json` 已核对 M5 的 93 个文件，两树与提交清单一致；本轮改动逐文件同步并记录 SHA-256。

### 新 runner 的最新验证

下表均使用经实际 Go 路径预检的新 runner；三份指定真实配置前后哈希与 mtime 均未漂移。不能据此推定其他 OS、真实 Provider 或整套 CI 通过。

| 检查 | 实际结果 | 证据 |
|---|---|---|
| SV01/02/03 六包定向，`go test -p 2 -parallel 2 … -run 'Test(Profile\|HostProviderEnvironment\|RawHostPins\|Registry\|MergeDrops\|WorkflowNamingAndBackground\|WorkflowResumePreservesDefault\|TaskNamingAllShippedAgents\|ScopeCapability)' -count=1 -timeout 3m -json` | exit 0，69.671 秒，65 个测试事件 PASS | `sv01-03-focused.log/.json/-analysis.json` |
| 12 个相关完整包，`go test -p 2 -parallel 2 … -count=1 -timeout 5m -json` | exit 1，171.041 秒；9 包 PASS、3 包 FAIL；1782 PASS/3 FAIL/6 SKIP 事件。Codex、persistenthost、capability、chat、Reviewer agy/claudecode/devin/droid 等通过；失败为 WSL Bash 入口与 Cursor/Kimi Windows 路径断言 | `sv03-affected-full.log/.json/-analysis.json` |
| Reviewer/Git 漏洞复现定向 | 修复前 exit 1，7.218 秒；三个边界均复现假凭据泄漏 | `sv03-boundary-red.log/.json` |
| Reviewer/Git/进程环境/凭据接管/路径修正定向 | exit 0，11.783 秒；原生凭据保留、平台凭据移除，真实子进程与 Cursor 最终命令环境均通过 | `sv03-boundary-green.log/.json` |
| Reviewer agy/cursor/kimi、processenv、providerprofile 五个完整包 | exit 0，6.516 秒；52 个测试事件 PASS、无 FAIL/SKIP | `sv03-reviewed-full.log/.json/-analysis.json` |
| 最新 integration、session_manager、gitworktree 完整包，`go test -p 2 -parallel 2 ./internal/integration ./internal/session_manager ./internal/adapters/workspace/gitworktree -count=1 -timeout 20m -json` | exit 1，763.569 秒；integration PASS（740.982 秒）、session_manager PASS（336.263 秒）；gitworktree FAIL（370.591 秒）。1779 PASS/9 FAIL/16 SKIP 事件；失败为 Windows 路径断言与 symlink 权限，不能报告整条命令通过 | `m6-integration-manager-full.log/.json/-analysis.json` |
| gitworktree 无远程仓库错误路径定向 | exit 0，5.587 秒；保留拒绝创建和不泄漏分支断言，按错误的引用格式比较路径 | `m6-gitworktree-quoted-path.log/.json` |
| packager → 内嵌 @electron/get → GotDownloader 实际下载链 | 显式 Node v24.22.1，exit 0，1.470 秒；使用本次隔离 LOCALAPPDATA 缓存，下载官方 SHASUMS 后验证 158293649 字节 ZIP。TLS/checksum 未放宽；裸 node 的诊断前次入口 exit 9，不算通过 | `build-node24-chain-explicit.log/.json`、`download-chain-result.json` |
| 后端完整构建，`go build -p 2 ./...` | exit 0，16.337 秒，无错误输出 | `m6-backend-build.log/.json` |
| 后端完整静态检查，`go vet -p 2 ./...` | exit 0，72.384 秒，无错误输出 | `m6-backend-vet.log/.json` |
| HTTP/API 完整包，`go test -p 2 -parallel 2 ./internal/httpd/... -count=1 -timeout 10m -json` | exit 0；5 包 PASS、2 包无测试；1437 PASS/14 SKIP 事件，含 OpenAPI 漂移和路由一致性。真实 Electron、Unix PTY/mode、symlink 与 Windows 非法文件名用例未覆盖 | `m6-httpd-full.log/.json/-analysis.json` |
| 固定 golangci-lint v2.13.2，`run --timeout=10m --concurrency=2` | exit 4，757.392 秒，`Timeout exceeded`；同时输出 2721 项诊断，其中 2439 项 goimports。改动文件 87 项，新增行/文件 12 项，不能只记为取消无输出，也不能判通过 | `m6-golangci-full.log/.json`、`m6-lint-changed-findings.json`、`m6-lint-new-findings.json` |
| SV-M6-05 Stop & Replan 实际 IPC 载荷回归 | 红测 exit 1，12.623 秒，确实提交 `codex + sonnet/low`；修复后完整工作流组件 exit 0，20.501 秒，3 文件/13 用例 PASS。非 Codex 原生任务继承 Codex Planner，固定 profile 任务保留自身完整选择 | `frontend-node24-sv05-red.log/.json`、`frontend-node24-sv05-green.log/.json` |

integration/Manager 的 16 个 SKIP 包括 POSIX fixture、macOS tmux、symlink 权限、显式桌面数据生成，以及因隔离 `AO_DATA_DIR` 已设置而跳过的旧 fixture 环境断言；不是全部业务零跳过。新 runner 的独立 Go 路径与配置哈希预检是目录隔离证据。当前全量通过包含 `TestM4ArtifactThroughRealManager` 177.38 秒、`TestM4IntegrationPartialRestart` 163.72 秒、`TestM6ProfileHTTPManagerRestart` 12.37 秒。

构建副本 Git HEAD 为 `8384bcc2a48d148e611f6d20b596de4eecd15a2d`，打包脚本会把此值嵌入 daemon；它不能表示同步后的完整源码。交接必须同时使用主仓 M5 基线、最终变更文件 SHA-256 清单与产物 SHA-256，不能仅凭候选版本中的 Commit 反推源码。

串行执行偏差：lint 超过预算后准备精确停止时，停止脚本没有提供可靠的退出确认；SV05 红测于 UTC 20:32:30 启动，lint 于 20:32:34.853 自行 exit 4，存在约 5 秒重叠。已确认 lint 进程无残留，三份配置前后未漂移；此偏差不隐藏，也不把该次视为严格串行证据。后续须以运行元数据的退出码及进程核实作为下一项启动条件。

lint 分诊边界：统筹只读核对 `cmd/ao/main.go`、`cmd/gencodexproto/main.go` 的副本分别有 15/1045 个 CRLF，而主仓 HEAD 为 0；归一化后字节一致，这两条 goimports 已证实是 checkout 换行差异。不推定全部 2439 条同因，不全仓格式化。本轮只处理新增诊断及本轮新增 import 的分组；G101 所指为环境变量名常量，没有密钥值，保留限定规则和具体原因的单行误报说明。

### 前端全量失败与受影响范围重验

以下是组合证据，不是全量重新运行 exit 0。原始证据均在上述 `ao-m6-safe` 目录，新 runner 的三份配置前后均未漂移。

| 命令 / 范围 | 实际结果 | 证据前缀 |
|---|---|---|
| Node24 `npm test -- --maxWorkers=2`，完整前端 | exit 1，1771.868 秒；633 文件 PASS / 3 FAIL，10846 用例 PASS / 50 FAIL / 14 SKIP。47 项 browser-profile-import 原生模块 ABI 不匹配；i18n 2 项缺 key/插值失败；覆盖扫描 1 项检出 18 处工作流硬编码 JSX | `frontend-node24-tests-full` |
| `npm rebuild better-sqlite3`，首次 | exit 1，28.126 秒；下载 TLS 断开，源码回退编译报 `MSB8020`，缺 ClangCL 工具集 | `frontend-node24-native-rebuild` |
| 官方 Node ABI137 预编译包校验并进入标准下载缓存，再执行 `npm rebuild better-sqlite3` | exit 0，2.683 秒，`rebuilt dependencies successfully`；仅修复隔离副本依赖，没有改 lockfile、生产校验或测试白名单 | `frontend-node24-native-rebuild-cached`、`sqlite-prebuild-stage` |
| `npm test -- src/renderer/components/workflow src/renderer/i18n src/main/browser-profile-import.test.ts --maxWorkers=2` | exit 1，25.579 秒；89 PASS / 2 FAIL。资料导入完整文件 55 项、i18n 23 项已通过；剩余两项为工作流测试使用旧标签选择器 | `frontend-node24-related-recheck` |
| 更新实际标签选择器后，完整 workflow 目录 | exit 0，16.782 秒，3 文件 / 13 项 PASS；保留实际 IPC 提交参数断言 | `frontend-node24-workflow-localized` |
| `npm run typecheck`、`npm run typecheck:e2e` | 均 exit 0，分别 72.349 / 10.057 秒；在最后两处显示翻译接线前运行 | `frontend-node24-typecheck-20261107T211718`、`frontend-node24-typecheck-e2e-20261107T211830` |
| 最后接线后 `npm test -- src/renderer/components/workflow src/renderer/i18n --maxWorkers=2` | exit 0，18.006 秒，5 文件 / 36 项 PASS；前端源码冻结，未重复运行先前通过的 633 文件 | `frontend-node24-workflow-i18n-final` |

9 种语言各有 141 个 workflow key；补齐已有工作流显示文案和插值，沿用 i18next。任务详情、Planner 和候选列表的供应商回退值复用已有 key。未改全站扫描器、白名单或语言回退规则；未做母语人工审校。

lint 新增项返修后，四包定向 `golangci-lint run --timeout=3m --concurrency=2` 仍 exit 1（52.877 秒），保留 76 项诊断（66 goimports、1 errcheck、3 gosec、1 nilerr、2 revive、3 staticcheck），不能标 lint 通过；本轮新增 profile 文件未再出现诊断。对应 providerprofile/capability/codexappserver 三完整包 `go test -p 2 -parallel 2 … -count=1 -timeout 5m -json` exit 0（20.949 秒），553 PASS / 6 SKIP 事件，证据 `m6-lint-profile-recheck`、`m6-profile-lint-regression`。

### 冻结前端后的完整打包

`npm run build`（显式 Node24 npm 入口，`build-node24-package-cache.log/.json`）exit 1，72.724 秒，UTC `2026-11-07T21:22:46.532650Z` 退出。daemon 与各准备脚本运行完成，Forge 的 main/preload/renderer 生产 Vite 构建完成；随后 `Packaging for x64 on win32` 报：

```text
Copying files [FAILED: Client network socket disconnected before secure TLS connection was established]
Preparing native dependencies [FAILED: Client network socket disconnected before secure TLS connection was established]
RequestError: Client network socket disconnected before secure TLS connection was established
```

同次日志没有给出实际失败 URL，不能凭此断定是 SQLite ABI 编译失败。预置的是本次用户目录中的官方 Electron ZIP，SHA-256 `eee30dc8fa1f5ea95490e59f44e46ea68dd24c6e93d22facf70fe5c2d4c2665c`；独立真实下载链通过不能覆盖完整打包失败。没有可验收桌面候选包，不把中间 daemon 或 Vite 输出标作发布产物，不再盲重试 TLS 问题。三份真实配置哈希/mtime 未漂移，退出后进程检查未发现本次构建残留。

### 冻结源码后的 Windows 检查

后续检查严格串行，命令完整 argv、隔离路径、起止时间、退出码及三份真实配置前后身份均保存在对应 `.json`，原始输出为同名前缀 `.log`。Go JSON 解析中的测试数含父子事件，不能当成独立场景数。

| 检查与命令 | 实际结果 | 证据前缀 |
|---|---|---|
| `go test -p 2 -parallel 2 ./internal/adapters/workspace/gitworktree -count=1 -timeout 10m -json` | exit 1，382.547 秒；183 PASS / 1 FAIL、无 SKIP。路径引用与 Git porcelain 断言均通过；唯一失败为 `TestWorkspaceIntegrationWorkspaceProjectCopiesAssetsAndCleansSessionCopy`，`workspace_integration_test.go:1218` 创建 symlink 返回 `A required privilege is not held by the client.`，未改权限或跳过用例 | `m6-gitworktree-full` |
| `go test -race -p 2 -parallel 2 ./internal/service/providerprofile ./internal/service/capability ./internal/adapters/chatdriver/processenv ./internal/adapters/chatdriver/persistenthost ./internal/adapters/chatdriver/codexappserver -count=1 -timeout 10m -json` | exit 0，216.129 秒；5 完整包 PASS、618 PASS / 6 SKIP 事件，无 race 失败。SKIP 为 3 项 Unix npm launcher 和 3 项明确未启用的真实 Codex live 测试 | `m6-profile-race-full` |
| `go test -race -p 2 -parallel 2 ./internal/session_manager -run Profile -count=1 -timeout 10m -json` | exit 0，41.220 秒；1 项 PASS，无 SKIP；完整 Manager 的非 race 结果另见上表，不能混记为完整 Manager race | `m6-manager-race-focused` |
| `go test -race -p 2 -parallel 2 ./internal/agentlaunch -count=1 -timeout 5m -json` | exit 0，10.815 秒；6 PASS / 2 SKIP，跳过 POSIX shebang / Unix npm launcher；Windows Git Bash AO 入口测试通过 | `m6-windows-environment-race` |
| `go test -race -p 2 -parallel 2 ./internal/adapters/runtime/conpty -run '^(TestConPTYStartsAtRequestedGrid\|TestHostStartGrid\|TestPtyHostArgsCarryInitialGrid\|TestPtyHostArgsCarryHostOptions)$' -count=3 -timeout 5m -json` | exit 1，14.062 秒；21 PASS / 3 FAIL 事件，无 SKIP。三轮 `TestConPTYStartsAtRequestedGrid` 均输出中文 `行: 37` / `列: 146`；源码 `host_conpty_windows_test.go:86` 只匹配英文 `Lines` / `Columns`。确认断言依赖系统语言，未将实际数字正确改记为测试 PASS，未调整系统语言 | `m6-windows-conpty-race` |
| 相同 ConPTY 命令，首次修复后 | exit 0，11.379 秒；24 PASS、无 FAIL/SKIP。沿用真实子进程模式，从 `windows.GetConsoleScreenBufferInfo` 读取 `Size.X/Size.Y`，核对明确的列/行输出及退出码；未改系统语言、代码页、生产启动逻辑或增加通用测试框架 | `m6-windows-conpty-race-20261107T213728` |
| `go test -race -p 2 -parallel 2 ./internal/adapters/workspace/... ./internal/session_manager -run 'Destroy\|Discard\|Sweep\|Kill\|RunPostCreate\|TaskNaming\|RenameTaskBranch' -count=1 -timeout 10m -json` | exit 0，118.807 秒；170 PASS / 2 SKIP，4 包返回 PASS，其中 router 无匹配测试。跳过为既有 attachment symlink 权限检测和 sh-only post-create 用例。此定向结果不覆盖完整 gitworktree 的资产 symlink 失败 | `m6-windows-workspace-race` |
| `go test -race -p 2 -parallel 2 ./internal/adapters/chatdriver/acp -run '^TestPersistentClaudeACPHibernateAndNativeResume$' -count=1 -timeout 5m -json` | exit 0，15.916 秒；1 PASS，无 SKIP | `m6-windows-acp-resume-race` |
| `go test -p 2 -parallel 2 ./internal/integration -run '^TestM6ProfileHTTPManagerRestart$' -count=1 -timeout 4m -json` | exit 0，23.906 秒；1 PASS，用例 13.84 秒。HTTP 常量返修后的最终单例，未重跑已通过的完整 integration | `m6-profile-e2e-final` |

### 隔离修复前的历史检查（保留，不作为安全隔离证明）

| 检查 | 实际结果 | 原始证据 |
|---|---|---|
| 完整 domain：`go test -p 2 -parallel 2 ./internal/domain -count=1 -timeout 2m` | exit 0；`ok .../internal/domain 3.258s` | `domain-handoff.log/.json` |
| 23 项历史 integration 回归，第二轮 | exit 1；22 项通过，Delegate 一项 `CODEX_ACCOUNT_SETUP_INCOMPLETE`，原因 `account_storage_unsafe` | `supervisor-fixtures-round2.log/.json`、`fixtures-green.log/.json` |
| 23 项历史 integration 回归，私有临时目录第三轮 | exit 0；23 项通过，包 15.005 秒、总计 20.063 秒 | `fixtures-green-20261107T184015.log/.json` |
| M4 单例，直接 Git 入口，5 分钟预算 | exit 1；测试 `300.639s` 超时，仍在第二仓质量链；没有证明死锁，也没有完成后续预演/集成 | `m4-fast.log/.json` |
| M4 Git 调用计时，5 分钟预算 | exit 1；2250 次已完成调用，累计 271.461492 秒、最长 0.777983 秒；没有固定停滞点 | `m4-profile.log/.json`、`m4-profile-summary.json` |
| 完整 Go 中的 M4 大场景，包预算 20 分钟 | `TestM4ArtifactThroughRealManager` PASS，177.99 秒；不覆盖此前超时记录 | `m4-fast-backend-full.log` |
| 完整 Go 中的 integration 包（返修前快照） | 包 FAIL，650.076 秒；唯一失败 `TestKillAndCleanupReachASessionGitWillNotRelease`：`trusted admission evidence is required`；已补真实准入 fixture；最新完整包通过，见上表 | `m4-fast-backend-full.log`、`m4-fast-backend-full-analysis.json` |
| Node 24 原生代理 + 未修改 GotDownloader + 官方校验 | exit 0；官方清单 6740 字节，已有 Electron ZIP 校验通过 | `got-node24-result.json` |
| 完整 build 的 Node 24 初次入口 | exit 9；内层 npm 使用 Node 22，拒绝 `--use-env-proxy`，尚未进入实际打包 | `build-node24.log/.json` |
| Node 24 临时 npm 入口验证 | exit 0；`node v24.22.1`，`npm 10.9.3`，PATH 首选均为本轮入口 | `build-node24-pathcheck.log/.json` |
| 完整 build，修正内层 npm | exit 1；50.947 秒，Forge 阶段 `Client network socket disconnected before secure TLS connection was established` | `build-node24-nested.log/.json` |
| Q9 注册表与能力完整包：`go test -p 2 -parallel 2 ./internal/service/providerprofile ./internal/service/capability -count=1 -timeout 2m` | exit 0；分别 2.362 秒、2.497 秒 | `profile-auth.log/.json` |
| Q9 相关九包编译：`go test -p 2 -parallel 2 … -run '^$' -timeout 3m` | exit 0；95.899 秒；仅编译，无行为测试 | `profile-auth-compile.log/.json` |
| Q9 Codex / 持久宿主：`go test -p 2 -parallel 2 ./internal/adapters/chatdriver/codexappserver ./internal/adapters/chatdriver/persistenthost -run 'Test(Profile\|RawHostPins)' -count=1 -timeout 2m` | exit 0；包分别 4.399 秒、2.946 秒 | `profile-driver.log/.json` |
| Q9 HTTP→真实 Manager→Driver 参数与 SQLite 重开：`go test -p 2 -parallel 2 ./internal/integration -run '^TestM6ProfileHTTPManagerRestart$' -count=1 -timeout 4m -v` | exit 0；用例 21.55 秒，包 25.225 秒 | `profile-e2e.log/.json` |
| Q9 API spec：`go generate ./internal/httpd/apispec/...` | exit 0；7.754 秒 | `profile-api-spec.log/.json` |
| Q9 TS API：固定 `openapi-typescript@7.4.4` | offline 首次 ENOTCACHED；正常固定版本生成 exit 0，6.827 秒 | `profile-api-ts.log/.json`、`profile-api-ts-install.log/.json` |
| Q9 前端：`npm run typecheck` | exit 0，100.323 秒；Node 22.20，待 Node 24 全量核验 | `profile-frontend-typecheck.log/.json` |
| Q9 组件：`vitest run src/renderer/components/workflow --maxWorkers=1` | exit 0；3 文件、12 用例通过，26.85 秒；Node 22.20 | `profile-ui-tests.log/.json` |

首轮 23 项检查的原始文件因 runner 重用前缀被第二轮覆盖；首轮只保留此前工具记录，不能再声称存在完整原始日志。统筹已保存第二轮副本；runner 后续遇同名结果自动加 UTC 时间戳，不再覆盖。

### 修复说明

- lifecycle fixture 为真实 Manager 装配已有 SQLite 准入提供者；未放开生产准入。
- M2 fixture 使用真实临时 Git 仓库，并为并发任务按全局唯一 task ID 划分互不重叠的 `allowedPaths`；未取消生产路径预留。
- Handoff Note 在整段裁剪前缩减文件清单，避免 Windows 预算下丢失最新请求和响应；沿用已有回归并补强 Windows cap 与遗漏计数断言。完整 domain 已通过。
- TEMP/TMP 与 GOTMPDIR 统一，消除临时数据包含关系断言的执行环境不一致。
- 统筹发现共享后台调用误清除任务命名的 effort。本轮返修将默认 effort 策略恢复到普通标题/建议入口；任务命名保留解析值，并在首次命名时使用已验证的启动身份查询持久 Profile，避免依赖尚未 ready 的恢复绑定。持久 Session 的短调用通过已有 `SessionSelection` 恢复选择，不套用 Controller 恢复许可。增强实际 Manager 调用断言；最新 Manager 完整包及 Profile/Windows TaskNaming race 均通过。
- SV-M6-02：登记表的空 effort 表示供应商默认策略。Driver Start、Resume 和 FreshIfMissing 仅对非空固定 effort 比较响应；模型仍必须精确一致。Session Metadata/持久 Task 保留空策略，不把 Provider 返回的 medium/high 写成新的固定选择。真实 Driver 三入口和 Manager 恢复断言已通过最新定向及完整包。`ScopeCapabilityRequest` 在没有工作流绑定且未请求 profile 时原样返回，保留原生 Model/Effort；无绑定不能凭请求自行引入 profile。
- Chat Service 的短调用原先再次清空 effort 并选择目录中的最低档。固定供应商现在保留启动与首轮选择（含空默认策略），建议调用使用普通短调用，避免不受支持的原生 fork；空 profile 的既有低成本策略保留。该发现补充在 SV-M6-01 的实际下游链路中，已加 Service 行为回归并通过相关完整包。
- 完整 Go 旧快照已 exit 1。Codex 旧 fixture 中 `/tmp/ws` 被生产 `filepath.IsAbs` 拒绝，不能据此前置失败声称原生/恢复兼容已验。本轮改测试入口为真实临时绝对路径，保留历史协议记录文本；Reviewer 的固定 Unix 二进制路径与 Claude 用户目录也做了可移植修复。最新完整包结果见上表。生产路径与权限检查未放宽，未批量 skip。持久宿主 journal 与 Cursor 的 mode 独占性断言限定在具备 Unix mode 语义的系统；Windows 仍执行 journal 重放/重置/配额和 Cursor 隔离写入全部业务断言，但不据此声称 Windows ACL 独占性通过。
- Git 多仓 fixture 原先使用相同内容、作者和秒级时间生成两个仓库，导致 SHA 偶发相同；只将初始内容加入临时仓库身份，保留独立历史断言。最新 Manager 完整包已 PASS。gitworktree 的错误消息以 `%q` 引用路径，测试改用 `strconv.Quote` 比较；Git porcelain 的路径按 `filepath.ToSlash` 比较，含否定断言，避免 Windows 假失败/假通过。没有修改生产路径校验或符号链接权限要求。
- `icacls` 显示原 Temp 下存在非可信主体的继承修改/删除权限，与账号存储的拒绝条件一致。在 `C:/ao-m6-tests-507820bdd394` 新建空白私有测试目录，仅当前用户、SYSTEM、Administrators 可访问；没有修改用户 Temp 或原生账号目录 ACL。创建命令 exit 0，证据 `secure-temp.json`。第三轮指定回归全部通过；未据此声称真实账号验收通过。

## Q9 已实现的最小接口与信任边界

统筹已通过 `m6-profile-plan.md` 确认宿主只读登记表和 Codex 首条映射。现有 `StaticProfile` 不能充当授权；实现从 AO data 目录读取独立的供应商引用注册表。本轮不写用户真实配置，不提交配置文件。

1. 每项仅有稳定 ID、展示名、Harness、HTTPS API URL、明确模型、可选 effort/上下文窗口和凭据环境变量名。严格拒绝未知字段、URL 内凭据、原始密钥、任意环境变量覆盖及权限/沙箱/hook/MCP/命令字段；第一条可用链限定 Codex Responses 自定义供应商。注册表只由本机可信配置者维护，Renderer/Agent 无写接口。
2. 新增窄的供应商引用 resolver 端口。只读 HTTP 列表返回 ID、配置摘要、模型和支持状态；创建/保存候选时持久化带摘要的 `Profile` 引用，重用现有 task 字段。未知、跨 Adapter、不支持组合、摘要变化均拒绝；确认计划、启动和恢复重新验证。
3. Manager 使用固定引用解析出本次不可变的非敏感参数，向 Codex Driver 的 `thread/start` / `thread/resume` 传递明确的自定义 provider 配置。凭据仅通过后端环境变量供进程读取，缺失即在启动副作用前拒绝，不回退默认账号。持久会话的配置不明确时保持失败/UNKNOWN，不重建 Session。
4. profile 只影响供应商、模型、effort 和上下文。Planner 只读、审批、沙箱、网络限制和受控 hook/MCP 始终由原有可信策略提供，profile 无覆盖入口。空引用/清除选择继续原有路径；UI 包括选择、保存、清除、详情和错误反馈。
5. 正式 HTTP→SQLite→Scheduler→Manager→离线 Driver 参数链已通过上述单例；覆盖无原生账号、规划继承、重开、漂移零重复启动、禁止切换原生路径和 Stop & Replan。Codex 协议映射另由真实 Driver + 内存协议服务测试通过。相关完整包/race、HTTP/API、组件检查有上述证据；M6 总验收仍未通过。

### 认证证据与持久宿主边界

- 平台凭据只接受 `AO_WORKFLOW_CREDENTIAL_` 非空专用名称。共享环境合并、终端与持久宿主最终边界清除全部平台源凭据、固定转发变量和大小写别名，禁止 overlay 重注入。只有已验证 Codex profile 的最后启动步骤重新注入一个规范化 `AO_WORKFLOW_PROVIDER_KEY`。原生公用变量不被清空，但禁止登记为平台凭据；旧任意变量名登记需迁移并重新选择引用，详见用户指南。
- daemon 在配置加载后、任何服务或子进程启动前调用 `CaptureEnvironment`，把专用凭据转入只读内存并从自身可继承环境移除；失败则停止启动。此处理沿用现有 telemetry 环境接管模式，未修改通用 process.Command 的默认环境语义，保留 deliveryGit/testgate 的最小白名单。Reviewer 普通 Chat/TUI 分别走已过滤的 Driver/runtime；agy 与 Cursor 预检、Git 工作树通用/临时 index/cherry-pick/config 启动入口补了独立最终过滤。Git hook/filter 等仓库可影响的子进程有外发风险，因此纳入修复。凭据轮换在更新宿主环境并重启 daemon 后生效。
- 目录读取不发 Provider 请求。执行认证只对宿主登记的 HTTPS `/models` 发送最多三次 GET，总限时八秒、正文上限 1 MiB、禁止重定向；无凭据及随机错误凭据都必须返回 401/403，当前凭据成功且包含目标模型才提供授权证据。公开目录、目录无目标模型、超时、过大响应或重定向均不能授权。
- 探测按固定 profile 与凭据身份合并并发；成功缓存两分钟、失败三十秒，重启重验，轮换即失效。秘密和秘密摘要不进入 API、日志或公开诊断；不会回退原生认证。
- profile 的 Scheduler 与 Bind 使用对应摘要，SQLite 原子准入核对任务选择。Manager 避免给该路径绑定原生账号；原生空引用路径保留。
- Codex raw 持久宿主先前忽略指纹，已补可选身份匹配；内部指纹绑定配置和凭据，经私有宿主环境传递并从 Provider 环境移除，不进入 argv。宿主的私有 `host.json` 保存该摘要以跨 daemon 重开比较，不包含原始密钥。变化后拒绝接管，不终止旧宿主或重建一个进程。
- 非生成式验证只证明身份与模型目录权限，不代表真实 Responses 推理额度、计费或生成已验收；本轮仅使用本地 TLS 假服务和假凭据。

## 未验和外部限制

先前完整 integration 与 Manager 已在新隔离 runner 中通过，本轮受影响行为另列重验；最新五包 lint 通过，完整 lint 仍以 252 项诊断和超时失败，新增 Windows 分离宿主测试失败。完整桌面打包仍失败，没有可验收候选包。完整 gitworktree 仍有一个 symlink 权限失败，Windows CI 表内所有命令范围已在本机覆盖，平台 SKIP 与运行差异保留。本人 Hello、手动启动和 SV-06 桌面 Queue 按用户决定暂缓。打包版 `userData` 固定指向用户目录，尚无经验证的独立账户/虚拟机环境，不能仅设置 AO_DATA_DIR 就启动安装/升级验收。未运行真实 Provider，未提交、Push、Merge、签名或发布。

### CI 与本机证据的范围

- `.github/workflows/go.yml` 的 `windows-workspace` 是 Windows 专用 job：包括路径身份、数据锁、工作树 teardown/kill、环境优先级、ConPTY grid、Cue shell、宿主清理、原生 hibernate/resume 等明确命令。本机扩展的完整 integration/Manager/HTTP 测试不能直接等同这个 job 全部通过。
- 同文件 `backend-core`、`chat-race`、`session-store-race`、`sqlite-race` 和 `lint` 在 Linux 执行。Windows 上的 build/vet、相关 race 和固定版本 lint 只提供本机证据，不冒充 Linux 完整分片结果；Linux shell、Unix mode、tmux 等仍需对应环境。
- `frontend.yml` 的 Node 24 typecheck、typecheck:e2e、vitest 可在本机运行；其 Linux Electron/Xvfb/Playwright renderer 与 macOS 原生更新/helper 检查未执行。Docker 不可用，未运行本地全部 workflow 模拟；没有发布 PR，远端 CI 也未执行。
- 未改查询或迁移，sqlc 生成物未变化；API 修改以生成的 OpenAPI、TS schema 及 HTTP 漂移测试核验。50 个旧失败包的表格是失败观察与后续定位边界，没有干净基线证据的条目不标“历史已证实”或自动豁免。

### `windows-workspace` job 逐项映射

来源：`.github/workflows/go.yml` 的同名 job。下表只表示本轮新隔离 runner 证据；没有把旧全量污染环境中的结果补算为本轮 PASS，也不声称整个 job 已通过。执行时额外限制 `GOMAXPROCS=2`、`-p 2 -parallel 2`，保留 CI 的目标包、筛选条件和 count。

| CI 步骤 / 对应范围 | 本轮映射 |
|---|---|
| SQLite filesystem path identity：`internal/storage/sqlite`，五个 `Open.../FragmentPaths.../DatabaseURI...` 用例，race | 后续补验 exit 0；10 PASS / 2 Windows 非法问号文件名 SKIP，`m6-windows-sqlite-path-race` |
| data-dir lock：`internal/datadirlock` 完整 race | 后续补验 exit 0；5 PASS，`m6-windows-datadirlock-race` |
| workspace teardown / session kill：`Destroy\|Discard\|Sweep\|Kill\|RunPostCreate\|TaskNaming\|RenameTaskBranch` | `m6-windows-workspace-race` exit 0；170 PASS / 2 SKIP，详见上表 |
| Windows Chat environment precedence：processenv / agentlaunch 完整 race | 分别在 `m6-profile-race-full` 与 `m6-windows-environment-race` 通过；agentlaunch 的 2 个 Unix 用例跳过 |
| Windows shell terminal start grid：ConPTY 四项，count=3、race | 首次英文断言失败；首次修复后 `m6-windows-conpty-race-20261107T213728` exit 0、24 PASS |
| Windows Cue shell readiness：shellterm `^TestCue`，race | 后续补验 exit 0；48 PASS，无 SKIP，powershell/pwsh/cmd/git-bash 实际交互均通过。测试禁用宿主 PowerShell profile 与 cmd AutoRun，生产默认不变，`m6-windows-cue-race` |
| Windows Chat provider process cleanup：persistenthost 四项，race | 完整五包 race 中已实际通过 `TestWindowsProviderJobReapsGrandchild`、`TestWindowsHostDescriptorReadAndRemovalDoNotBlockEachOther`、`TestWindowsHostDescriptorReadsPastMaxPath`、`TestExtendedLengthPathMatchesOS` |
| Windows Chat hibernate/native resume：ACP / Codex 两项，race | ACP 单例及完整 Codex race 内的 `TestCodexHibernateStopsAppServerAndNativeResumesThread` 均 PASS |
| background agent commands have no console：pi / omp / primeagent `^TestBackgroundCommandHasNoConsole$`，race | 后续补验 exit 0；3 包 / 3 PASS，`m6-windows-background-console-race` |
| Windows subprocess commands：mobilebridge / process / systemexec `Cloudflared\|Tunnel\|Command`，race | 后续补验 exit 0；3 包 / 60 PASS / 22 POSIX shell connector SKIP，`m6-windows-subprocess-race` |
| PowerShell installers：systemexec `^TestRunInstallScriptPowerShell`，race | 后续补验 exit 0；4 PASS，只有本地 TLS 哈希/失败脚本，`m6-windows-powershell-fixture-race` |
| native Goose identity：binaryutil / goose 完整 race | 后续补验 exit 0；2 包 / 52 PASS / 18 既有平台与子进程入口 SKIP，`m6-windows-goose-identity-race` |
| OpenCode Windows npm shim：`^TestResolveBinaryForMajorAcceptsSlowShim$`，race | 后续补验 exit 0；1 PASS，`m6-windows-opencode-shim-race` |
| Goose readiness/inventory：service/agent 的 CI 指定筛选，race | 后续补验 exit 0；57 PASS / 1 Homebrew opt links SKIP，`m6-windows-goose-readiness-race`；不等同完整 service/agent 包通过 |

Linux 的完整后端/race/lint 分片、macOS 原生更新/helper、Linux Electron/Xvfb/Playwright 和远端 CI 均未验。完整前端采用首次全量失败加受影响范围重验的组合证据；不可写作“全量重跑 exit 0”。

## 首轮 HANDOFF 源码与证据身份（历史快照）

- 主仓：`E:/code/agent-orchestrator`，分支 `feat/multi-agent-desktop`，HEAD/M5 基线 `acf9bafa8c14de5533a35f66d4d2c9bfc057baae`；未提交。
- 验证副本：`C:/Users/11459/.codex/worktrees/m5-desktop-safety/agent-orchestrator`，嵌入 HEAD `8384bcc2a48d148e611f6d20b596de4eecd15a2d`。副本 HEAD 不是最终未提交源码身份。
- 首轮 `final-manifest.py` 核对 190 个源码文件和 6 份依赖清单，exit 0、`mismatches=[]`。当时稳定源码摘要为 `fa680a419542b8b5d460d66458cd38dda910cd656970fd0d1cad9bd08afec340`；此值已经过期，不能表示后续 Cue 修改。证据目录的 `m6-final-source-manifest.json` 已用于最新清单，当前身份见后文。
- 首轮 `git diff --check` exit 0、stdout 为空；当时 Git 提示 84 条现有 LF→CRLF 转换警告，未更改 autocrlf 规则。`m6-final-diff-check.log/.json` 用于记录最新冻结检查。
- 前端冻结于最终工作流/i18n 36 项通过后；随后唯一源码返修是 ConPTY 的现有测试，已通过同组 race/count=3 重验。M6 HTTP/Manager/重开单例最终 exit 0。没有重跑未受改动影响的已通过套件。
- 最后一个测试 UTC `2026-11-07T21:38:28.507333Z` 退出；只读进程检查只发现检查命令自身，未发现本轮 runner、验证副本或 scratch 路径的残留测试/构建进程。无需用户停止后台检查。
- 本轮完整打包失败，`frontend/out` 不存在，候选包及候选 SHA-256 均为“不存在”；不得用中间 daemon、Vite 输出或缓存 ZIP 充当候选。
- 证据目录：`C:/Users/11459/AppData/Local/Temp/ao-m6-safe/`。所有后续新 runner 结果保留真实 commandExitCode 和保护配置前后哈希/mtime；旧真实 `.claude.json` 的 8 条污染仍未获授权清理。

首轮交接结论为 M6 未通过：完整 lint exit 4/后续定向 exit 1，完整桌面打包 exit 1，完整 gitworktree exit 1；当时未跑的 Windows 命令已在下一节补验。用户暂缓项、其他 OS 与真实 Provider 仍未验。本文不授权修改真实配置、提交、发布或安装候选。

## 首轮 HANDOFF 后的自动收口记录

启动前只读核查见证据目录 `windows-followup-safety.md`。剩余 8 组对应 9 条 CI 命令（Goose 身份/就绪各一条）；复用新 runner、C 盘 scratch 和 Go 并发 2，未运行真实安装、Provider 或新全量下载。Cue 既有真实交互夹具为 PowerShell 添加 `-NoProfile`、cmd 添加 `/D`，避免宿主启动脚本；生产 shell argv 不变，不据此声称真实 profile/AutoRun 已验。其他命令只使用临时文件、测试二进制、注入函数或本地 httptest。

| 命令范围（均 `go test -race -p 2 -parallel 2 … -count=1 -json`） | 实际结果 | 证据前缀 |
|---|---|---|
| storage/sqlite，CI 五个路径用例筛选，timeout 10m | exit 0，302.639 秒；10 PASS / 2 SKIP。跳过为 Windows 问号文件名；其余路径、旧库保护与只读重开通过 | `m6-windows-sqlite-path-race` |
| datadirlock 完整包，timeout 5m | exit 0，6.488 秒；5 PASS，无 SKIP | `m6-windows-datadirlock-race` |
| service/shellterm，`-run '^TestCue' -timeout 10m` | exit 0，13.806 秒；48 PASS，无 SKIP。四种本机 shell 实际启动、就绪及适用命令投递通过 | `m6-windows-cue-race` |
| adapters/agent/pi、omp、primeagent，`-run '^TestBackgroundCommandHasNoConsole$' -timeout 5m` | exit 0，14.096 秒；3 包 / 3 PASS，无 SKIP | `m6-windows-background-console-race` |
| mobilebridge、process、adapters/systemexec，`-run 'Cloudflared\|Tunnel\|Command' -timeout 10m` | exit 0，19.160 秒；3 包 / 60 PASS / 22 SKIP，跳过项均为既有 POSIX shell 假 connector | `m6-windows-subprocess-race` |
| adapters/systemexec，`-run '^TestRunInstallScriptPowerShell' -timeout 5m` | exit 0，8.137 秒；4 PASS，无 SKIP；本地 TLS 夹具覆盖成功、exit 7、终止错误与临时清理 | `m6-windows-powershell-fixture-race` |
| adapters/agent/binaryutil、goose 完整包，timeout 10m | exit 0，9.209 秒；2 包 / 52 PASS / 18 SKIP。跳过 Unix PATH/home/package-manager/node-manager 形态、Unix shim 与直接运行的子进程入口 | `m6-windows-goose-identity-race` |
| adapters/agent/opencode，`-run '^TestResolveBinaryForMajorAcceptsSlowShim$' -timeout 5m` | exit 0，11.978 秒；1 PASS，无 SKIP，临时慢 cmd shim | `m6-windows-opencode-shim-race` |
| service/agent，`-run '^Test(FindInstalledBinary\|WarmReadinessAndFindInstalled\|PresslyGoose\|Refresh\|Probe\|ReadinessCoordinator)' -timeout 10m` | exit 0，22.364 秒；57 PASS / 1 SKIP，跳过 Windows 不存在的 Homebrew opt links | `m6-windows-goose-readiness-race` |

上述九轮均 exit 0，合计 240 PASS / 43 SKIP 测试事件；三份保护配置前后哈希/mtime 全部相同。首个 SQLite 命令启动时 C 盘约 15.45 GiB，后续每轮记录 freeCAfter/freeEAfter；未触发 8 GiB 停止阈值。最后一个测试 UTC `2026-11-07T21:51:22.961149Z` 退出。至此表内 Windows job 所有命令范围均已在本机覆盖，仍保留平台 SKIP、分组拆分及测试启动隔离的差异，不冒充远端 CI 运行。完整 argv、退出码和计数另汇总于 `m6-final-evidence-audit.json`。

打包只读定位见 `package-chain-readonly.md`：原生依赖 prePackage 已返回成功；packager 在解包前经内嵌 `@electron/get` 获取 Electron ZIP。缓存命中仍联网重取 SHASUMS，校验步骤抛错还会触发 ZIP 重新下载。既有日志没有 URL/请求序号，不能断言哪次请求失败；尚无明确根因修复或环境条件变化，未再跑完整 build 或小 probe。完整打包保持 FAIL、无候选包。

### 新增 lint 诊断的最终复核

固定 v2.13.2，在验证副本 backend 下通过新 runner 执行：

```text
golangci-lint.exe run --timeout=5m --concurrency=2 ./internal/service/execution ./internal/integration ./internal/session_manager ./internal/adapters/runtime/conpty ./internal/service/shellterm
```

实际 exit 1，96.825 秒，UTC `2026-11-07T21:53:33.183227Z` 退出；原始输出为 `229 issues:`。分类为 goimports 183、errorlint 25、errcheck 6、staticcheck 5、unused 4、govet 2，以及 ineffassign/nilerr/revive/unparam 各 1。三份保护配置前后哈希/mtime 未漂移。命令和原始输出见 `m6-lint-remaining-windows.json/.log`，不能标记 lint 通过，也没有把 183 项 goimports 全部归因于 CRLF。

`m6-lint-remaining-windows-triage.json` 对当前 diff 的映射结果是新增行/文件 0 项诊断；28 项位于改动文件的未改行。`m6-lint-remaining-windows-baseline-map.json` 将这 28 项逐行对应到 M5 HEAD，28/28 行原文相同；这只证明行文本基线存在，未在干净基线运行 lint，不构成问题豁免。

最早完整 lint 的 12 项新增诊断，已有四包复核覆盖其中 10 项；本轮补齐 `service/execution/local_gateway.go` 的 goimports 与 `integration/m6_profile_e2e_test.go` 的 usestdlibvars。两次后续日志均未再报告这 12 项对应路径/规则，逐项复核见 `m6-final-evidence-audit.json`。最新 ConPTY 测试没有诊断；Cue 测试保留的 `OpenCurrentProcessToken` 弃用诊断位于第 139 行，基线原文相同，本次新增启动参数分支没有诊断。未扩范围修改旧行，未重跑完整 lint。

### 191 文件交接时的源码与进程身份（历史快照）

- `rtk proxy python C:/Users/11459/AppData/Local/Temp/ao-m6-safe/final-manifest.py` 实际 exit 0：191 个源码文件、6 份依赖清单，`mismatches=[]`，主仓与验证副本逐文件一致。稳定源码摘要 `5070602346b081bcb4a8c96cb9a17714800a401b420d9c5fa55198f1c6d8691e` 替代首轮 190 文件摘要；`m6-final-source-manifest.json` 另含四份执行文档摘要，文档冻结后再次生成。
- 最后一个测试 UTC `2026-11-07T21:51:22.961149Z` 退出，最后一个 lint UTC `2026-11-07T21:53:33.183227Z` 退出。UTC `21:58:24.7729157Z` 通过 `Get-CimInstance Win32_Process` 只读筛选本轮 runner、验证副本、scratch 和 lint 路径，并检查 `*.test.exe`：`scopedProcesses=[]`、`testProcesses=[]`；未终止任何进程。证据 `m6-final-process-check.json`。
- 同次检查 `frontend/out` 不存在，C 盘余量约 15.40 GiB。没有候选安装包或候选 SHA-256；源码清单不等于发布产物。
- 冻结差异检查 `git diff --check` 实际 exit 0、stdout 为空，85 条现有 LF→CRLF 警告；记录于 `m6-final-diff-check.log/.json`。四份未跟踪执行文档另用 `git diff --no-index --check` 与空文件比较，均无空白错误输出；原始 exit 1 表示文件内容不同，不能写作命令 exit 0，原始记录见 `m6-final-documents-diff-check.json`。未改 autocrlf。
- 当前结论仍为 M6 未通过。Windows 本机命令范围已补齐；lint、打包及完整 gitworktree 失败保留。真实配置的 8 条污染记录未清理，其他 OS、远端 CI 和用户暂缓项仍未验；未提交、Push、Merge、发布或启动候选。

## lint 门禁继续收口

范围按 `m6-lint-closeout-plan.md`：execution、integration、session_manager、conpty、shellterm 及实际依赖边界。191 文件快照作为输入；M6 新增行零诊断不豁免 M1～M5 的交付问题。未修改 Git/lint 配置或真实 Provider 配置，未重跑不受本轮影响的九条 Windows 命令。

### 换行对照与真实诊断

`lint-newline-control.py` 先逐文件核对主仓/副本去掉 CRLF 后正文相同，再仅在 C 盘副本转换已诊断的 183 个 Go 文件。每个文件的主仓原始 SHA、副本原始 SHA、规范化 SHA、CRLF 数均保存于 `m6-lint-newline-control.json`，原始副本字节另存 `lint-newline-original/`；没有批量格式化 E 盘源码。

相同五包/版本/5 分钟预算的换行对照 `m6-lint-five-lf-control` 实际 exit 1，102.710 秒，输出 `48 issues:`。183 项 goimports 中 181 项随仅换行变更消失；剩余 `session_manager/admission.go` 与 `startup_reattach_fixture_test.go` 为真实第三方/本地导入分组问题。此结果只归因已逐项对照的 183 个文件，不能外推旧全量的 2439 项。

| 原诊断 | 最小处理与契约依据 |
|---|---|
| 三处 Store 类型断言（errcheck） | `prepareSpawnAdmission` 和 `prepareSessionReattach` 在正常路径已验证能力，未发现外部可达绕过；不是由 lint 直接确认的安全漏洞。消费内部许可时仍使用显式能力检查，异常接线返回 `ErrAdmissionRequired`；成功发布与失败回执共用重附着验证方法，原任期/身份校验不变 |
| ConPTY 两处释放（errcheck） | 保持 READY 后先关 stdout、再 Release 的顺序；分别检查并记录错误，继续返回已确认启动的地址/PID，不把资源释放失败误报为未启动。没有修改创建标志或无窗口设置 |
| Windows token（errcheck/staticcheck） | 使用 `GetCurrentProcessToken` 的查询伪令牌读取当前进程 SID，无须 Close；没有增加令牌权限，原独占 DACL 逻辑保留 |
| 25 项 errorlint | Gateway 和多仓边界只 wrap 本层权威分类，底层原因显式 `.Error()` 转文本，保留错误文本与分类隔离。`recordSpawnFailure` 先判断 Refused 再判断 UNKNOWN，不能机械扩大 unwrap |
| LiveTarget nilerr | 保留 `live=false/reason=observation_failed/error=nil` 契约；仅在该返回行限定 `nilerr`，说明未知观察不证明死亡、不改变持久绑定。既有 LiveTarget 和禁止发送/中断测试继续验证 |
| govet 永真断言与无效字段写入 | 删除仅检查静态接口赋值非 nil 的测试，编译和真实干预调用已覆盖接口；删除未使用的临时 `ws.RepoPath` 写入，持久 `WorkspaceRepoInfo.RepoPath` 仍取授权 Project |
| fixture 无效赋值、死代码、静态格式项 | 删除被覆盖的初始 probe、未用 stopAllChat/字段/时间常量；去掉无插值 Sprintf、使用等价 status switch、避免 cap 名称遮蔽；不新增输入矩阵 |
| multiRepoRows unparam | 删除未使用的 ctx/rec 参数并更新全部三处调用；记录仍来自持久 rows，不以现场目录推导授权身份 |

### 行为证据

- 异常 Store 接线红测 `m6-lint-contract-red` exit 1，21.024 秒：`panic: interface conversion: *sessionmanager.fakeStore is not sessionmanager.sessionAdmissionStore: missing method CreateAdmittedSession`。这是内部契约失配的定向证据，不声称正常入口可触发。同期 Gateway 混合错误反例按原分类通过。
- `m6-lint-unwrap-counterexample-red` 仅在 C 盘副本临时将 UNKNOWN 原因改为 `%w`，沿真实 Gateway→Coordinator 调用，exit 1，8.654 秒。实际结果变成 `State:failed_confirmed`、`FailureCode:spawn_refused`，回归准确失败；随后恢复源码，恢复前后原始 SHA 完全相同，见 `m6-lint-error-boundary-mutation.json`。E 盘未发生此临时变异。
- 不把上述预期红测计入最终通过数量。以下检查均使用同一修复快照和安全 runner，`GOMAXPROCS=2`；每轮三份保护配置的 SHA/mtime 未漂移。

| 最终行为命令（backend 下） | 实际结果 | 证据前缀 |
|---|---|---|
| `go test -race -p 2 -parallel 2 ./internal/service/execution -count=1 -timeout 5m -json` | exit 0，67.725 秒；242 PASS，无 SKIP；包括 Gateway→Coordinator 混合错误反例恢复后的通过结果 | `m6-lint-execution-race` |
| `go test -race -p 2 -parallel 2 ./internal/session_manager -run '^(TestAdmissionStoreCapabilityIsRequired\|TestM3.*\|TestMultiRepo.*\|TestF03.*)$' -count=1 -timeout 10m -json` | exit 0，216.159 秒；50 PASS，无 SKIP；正常准入/重附着、多仓身份与异常接线拒绝 | `m6-lint-manager-race` |
| `go test -race -p 2 -parallel 2 ./internal/adapters/runtime/conpty ./internal/service/shellterm -count=1 -timeout 5m -json` | exit 0，19.229 秒；361 PASS / 1 SKIP，两个完整包 PASS；包含真实 ConPTY、Cue 交互和命令目录 DACL | `m6-lint-windows-race` |
| `go test -p 2 -parallel 2 ./internal/integration -run '^(TestM2.*\|TestM4ArtifactThroughRealManager\|TestM4IntegrationPartialRestart\|TestM5PlanningCapacityOne\|TestM5PlanRejectsChangedExecutionFacts)$' -count=1 -timeout 20m -json` | exit 0，625.572 秒；55 PASS / 1 SKIP；M4 工件链 169.74 秒、部分成功重启恢复 156.74 秒，M2/M5 接线回归通过 | `m6-lint-integration` |

上述 Windows 整包结果在新增真实分离宿主用例之前取得；不能覆盖后续失败。该轮唯一 SKIP 是 `TestTmuxUserShellExitReconciliation`。

### 新增真实宿主用例失败（停止继续修复）

`TestWindowsDefaultSpawnHostEndToEnd` 沿 Windows `defaultSpawnHost`→`RunHost`→ConPTY 路径验证 READY 后关闭 stdout/Release、取消启动请求后的交互及清理，沿用仓库 Linux/macOS 的 TestMain 入口模式。没有新增工具或独立 fixture 框架。

1. `m6-lint-detached-host-race` exit 1，13.643 秒：新增用例 FAIL，原网格/退出用例 2 PASS；输出包含输入回显，未出现执行后的标记。
2. 等待 shell 的 `AO_START` 标记再发输入后，`m6-lint-detached-ready-race` exit 1，12.209 秒；只看到尾部空行。
3. 为排除整屏重绘截断，将读取范围从 20 行改为 200 行后，`m6-lint-detached-transcript-race` exit 1，12.230 秒；实际输出为 `ESC[?9001h ESC[?1004h ESC[?25l ESC[2J ESC[m ESC[H` 与空行，仍未看到 shell 标记。

三轮失败后按用户规则停止继续修改/重试此问题，保留当前失败用例及全部日志。当前证据不足以区分终端协议初始化、夹具入口或产品路径原因；不能断言 READY 后资源释放验证通过，也不能以此前整包结果覆盖新增失败。三轮保护配置 SHA/mtime 均未漂移，清理回调已执行。UTC `22:37:34.3198974Z` 通过 `Get-CimInstance Win32_Process` 核对三轮各自创建时间窗口内的 `conpty.test.exe` 和 `cmd.exe`：每轮 `remainingHostOrCmdProcesses=[]`，未杀进程；未保存逐轮即时退出快照，不伪造退出时点。证据 `m6-lint-detached-process-check.json`。Go1.27.1 的 Windows `Process.Release` 不修改 PID，因此不能按 Unix 行为归因此问题。

### 最终五包 lint 与完整复核

- 首次修复后 `m6-lint-five-fixed` exit 1，96.926 秒，剩余 1 项 `G702: Command injection via taint analysis`，位于 `spawn_windows.go` 的当前 AO 二进制启动点。这里使用 `os.Executable()`、固定 `pty-host` 入口和分离 argv，终端执行本身为功能契约；在该调用点添加仅 `#nosec G702` 的原因说明，未关闭整个 gosec 或包，也未修改执行方式。
- 相同五包/固定 v2.13.2/5 分钟预算的 `m6-lint-five-contract` 实际 exit 0，37.264 秒，UTC `2026-11-07T22:37:27.363551Z` 退出，原始输出 `0 issues.`；三份保护配置前后未漂移。这是五包结果，不能替代全仓 lint 或新增宿主用例的行为失败。
- 完整复核前，`lint-full-newline-control.py` 逐文件核对旧全量日志中的 2439 个 goimports 文件，主仓/副本去掉 CRLF 后正文全部相同；其中 2245 个副本文件新转换为 LF，其余已经规范化或本轮定向格式化。`m6-lint-full-newline-control.json` 保留每个原始/规范化 SHA 和 CRLF 数；主仓未批量归一化。
- 在上述新源码与明确换行对照条件下，仅执行一次完整 `golangci-lint.exe run --timeout=10m --concurrency=2`，`m6-lint-full-closeout` 实际 exit 4，763.125 秒，UTC `2026-11-07T22:50:58.955552Z` 退出。原始输出 `252 issues:` 和 `Timeout exceeded: try increasing it by passing --timeout option`，三份配置 SHA/mtime 未漂移。没有改预算或重跑；超时结果不是零诊断，也不保证已列尽全部问题。

完整日志分类：copyloopvar 9、errcheck 18、errorlint 2、forbidigo 1、gocritic 18、goimports 27、gosec 36、ineffassign 1、nilerr 3、prealloc 3、revive 77、staticcheck 31、unconvert 6、unparam 4、unused 14、usestdlibvars 1、wastedassign 1。解析补齐两条跨行 gocritic 签名诊断，逐项 252 与原始汇总完全一致；最初单行解析得到的 250 不再作为当前证据。

| 剩余诊断归属 | LF 后仍有格式/导入诊断 | 注释、未用项及其他机械清理 | 行为或安全诊断，尚须核对契约 | 合计 |
|---|---:|---:|---:|---:|
| M1～M6 工作流业务与验收边界 | 26 | 120 | 27 | 173 |
| 本交付也涉及的共用平台边界 | 1 | 4 | 19 | 24 |
| 其他既有通用平台边界 | 0 | 8 | 47 | 55 |

归属按 M0 `623871d` 到当前工作树的文件范围，并区分工作流功能与共用平台职责；合计 197 项落在本交付涉及文件，55 项为其他既有文件。这是保守的任务归属，不是逐行引入原因或干净基线 lint 证明，尤其不能因相对 M5 未改行而豁免。逐项路径、行、规则、原文和归属见 `m6-lint-closeout-summary.json` 与 `m6-lint-full-remaining.md`；27 个剩余 goimports 文件当前副本均 `CRLF=0`，不再归为换行差异。

工作流相关待审重点包括：scheduler 资源/重试和 SQLite admission 的整数转换，intervention/capability 的 nilerr 契约，质量目录/路径预留的 Close、delivery 临时目录清理，Scripted Driver 的类型断言，Windows operator consent 的原生进程边界，以及 MCP 错误包装。Git 对象 SHA-1、协议常量的 G101 等需先核对用途，诊断不直接等于已确认漏洞。26 项业务格式诊断和 120 项机械项主要分布在 HTTP DTO、审批、调度、质量/工件和 SQLite 支撑；不在本轮最后结果后继续扩大修改。

共用平台相关包括本交付涉及的 mobilebridge/pricing/chat 私有文件与 process tree，以及其他既有 Codex 账号 ACL、persistenthost 子进程、PID 存活、内存采样、runfile 和 session 路径处理。相关包已用于本交付并不意味着这些剩余项可忽略；也不把它们全部归为 M6 新漏洞。后续由统筹据清单安排。

### 首批交接身份（第二批前历史快照）

- 主仓分支 `feat/multi-agent-desktop`，HEAD/M5 仍为 `acf9bafa8c14de5533a35f66d4d2c9bfc057baae`；验证副本嵌入 HEAD 仍为 `8384bcc2a48d148e611f6d20b596de4eecd15a2d`。没有提交。
- 当前 204 个交付源码文件、6 份依赖文件，双树原始字节一致；额外 2439 个换行对照文件中 2080 个主仓/副本原始字节不同，去掉 CRLF 后全部一致。`m6-final-source-manifest.json` 分别记录原始和规范化身份，不能混称全树字节一致。稳定源码摘要 `5ceb139cb7e345e70074a5c0a68ec751aa1828feeb24c9f15999219e7267b4d4`；输入 191 文件清单保留为 `m6-lint-input-source-manifest.json`。
- 最新进程核对 UTC `2026-11-07T22:52:43.3713499Z`：本轮 runner/lint/测试路径 `scopedProcesses=[]`、`testProcesses=[]`。次秒按三轮宿主测试时间窗口再次核对，无测试宿主/cmd 残留。未终止任何进程。C 盘约 14.43 GiB，`frontend/out` 不存在。
- 最终 `git diff --check` 实际 exit 0、stdout 为空，99 条现有 LF→CRLF 警告；四份未跟踪执行文档的 `--no-index --check` 无空白错误输出，原始 exit 1 表示文件不同。记录 `m6-final-diff-check.json/.log` 与 `m6-final-documents-diff-check.json`；四份执行文档同步后再生成清单，清单同时核对文档双树 SHA。
- 最终结论是 M6 未通过：五包 lint 收口通过，但完整 lint 252 项加超时失败，新增宿主真实交互失败；打包 TLS、symlink 权限、人工暂缓与真实 Provider 缺口保留。真实 Claude 配置的 8 条测试记录未清理。

## 首批测试文件超限申报（历史快照）

新增测试文件均不超过 300 行（当前最大 197 行）；下列 16 个既有超限文件发生最小修改，没有为规避上限拆散既有用例。ConPTY 与 Cue 返修文件均低于 300 行。行数证据 `test-line-report.json`：53 个变更测试文件全文 21017 行，变更生产文件排除生成的 TS schema 后全文 54156 行；这是变更文件范围统计，包含既有代码，不冒充新增行数或独立模块总量，也不单凭此比值证明每个被测模块满足两倍上限。临时 runner/分诊脚本均不进入代码仓，当前无单文件超过 400 行；本轮未新增 runner 测试体系。

| 既有测试文件（backend/internal/ 下） | 当前行数 / 超出 300 行 | 修改原因与验收项 |
|---|---:|---|
| adapters/chatdriver/codexappserver/driver_test.go | 1868 / 1568 | 临时绝对路径与最终启动环境参数；原生启动/恢复兼容 |
| adapters/chatdriver/codexappserver/history_test.go | 480 / 180 | Windows 临时绝对 workspace；历史回放 |
| adapters/chatdriver/codexappserver/input_test.go | 498 / 198 | Windows 临时绝对 workspace；输入协议 |
| adapters/chatdriver/codexappserver/steer_test.go | 392 / 92 | Windows 临时绝对 workspace；同轮 Steer |
| adapters/reviewer/claudecode/claudecode_test.go | 556 / 256 | 同时隔离 USERPROFILE；原生恢复 |
| adapters/reviewer/cursor/cursor_test.go | 617 / 317 | 区分 Unix mode，解码 JSON 路径断言；Reviewer 隔离配置 |
| adapters/workspace/gitworktree/workspace_integration_test.go | 1733 / 1433 | 按已引用格式比较 Windows 错误路径；无远程仓库拒绝创建 |
| integration/lifecycle_sqlite_test.go | 533 / 233 | 真实 SQLite 准入 fixture；生命周期门禁 |
| integration/m2_e2e_test.go | 1714 / 1414 | 真实仓库、准入与路径预留 fixture；删除无效 probe 赋值及未用方法，M2 |
| integration/m2_full_flow_e2e_test.go | 1814 / 1514 | 同上，保留生产校验；删除无插值 Sprintf，M2 完整流程 |
| service/execution/coordinator_test.go | 2234 / 1934 | 删除永真的接口赋值检查，不增加测试；由真实干预路径继续验证 |
| service/execution/local_gateway_test.go | 2265 / 1965 | 补强既有丢失响应回归，经 Gateway→Coordinator 验证混合错误不会将 UNKNOWN 误判为拒绝；删除未用字段 |
| service/execution/ownership_test.go | 350 / 50 | 仅删除未用时间常量与导入；归属回归保持 |
| session_manager/multi_repo_basesha_test.go | 604 / 304 | 更新移除未用参数后的调用；多仓恢复/身份行为不变 |
| session_manager/multi_repo_workspace_test.go | 439 / 139 | 两仓内容携带身份，避免秒级同 SHA；多仓历史 |
| session_manager/task_naming_test.go | 636 / 336 | 命名 effort 保留断言；SV-M6-01 |

## 旧全量检查的 50 个失败包分诊

来源为旧快照 `m4-fast-backend-full-analysis.json`。以下列代表失败和已确认/待确认原因，不构成逐包完整根因证明，也未与干净基线对照；不能统一称为“已有失败”或“Windows 豁免”。本轮只修首版必需与实际受影响边界。

| 包（省略 internal/） | 失败观察 / 原因判断 | 与本次关系及处理 | 所需验证环境 / 缺口 |
|---|---|---|---|
| `adapters/agent/agy` | HOME 与 USERPROFILE 不一致；另有 symlink 权限失败 | 非首批 profile Adapter；保留失败 | 修正 fixture 的 Windows / 支持 symlink 的环境 |
| `adapters/agent/aider` | 本地认证返回 unknown，预期 authorized；具体读取来源未定 | 其他 Adapter，不据此改生产认证 | 独立用户目录和该 Adapter 可执行 fixture，待定位 |
| `adapters/agent/claudecode` | 只设 HOME，生产读取 USERPROFILE，已实际写入真实 .claude.json | 原生安全相关；旧结果无隔离保证 | 先用新 runner；fixture 仍需各自隔离两类目录 |
| `adapters/agent/codex` | symlink 创建失败：A required privilege is not held by the client | 原生兼容相关，未放宽权限 | 具备 symlink 权限的 Windows 或 Linux/macOS，未重验 |
| `adapters/agent/commandcode` | 测试二进制未在 Windows PATH 解析成功 | 其他 Adapter/注册表；未扩展改造 | 具备对应可执行 fixture 的 Windows 或现有 Unix CI |
| `adapters/agent/copilot` | 信任配置路径及 JSON 转义断言不匹配，部分预期文件不存在 | 其他 Adapter；根因未完全确认 | 双 HOME 隔离后按 Windows 路径语义复现 |
| `adapters/agent/fake` | POSIX sh 未找到；tmux 还存在系统二进制解析差异 | 运行时相关；共享凭据过滤另行验证 | 明确 Git Bash/系统 tmux 环境；本机默认 bash 原为 WSL 入口 |
| `adapters/agent/kilocode` | 测试二进制未在 Windows PATH 解析成功 | 其他 Adapter/注册表；未扩展改造 | 具备对应可执行 fixture 的 Windows 或现有 Unix CI |
| `adapters/agent/kimchi` | 本地认证返回 unknown，预期 authorized；具体读取来源未定 | 其他 Adapter，不据此改生产认证 | 独立用户目录和该 Adapter 可执行 fixture，待定位 |
| `adapters/agent/kimi` | credentials 目标不是 reparse point，预期链接 | 其他 Adapter；未放宽账号链接校验 | 支持预期链接语义的环境，待定位 |
| `adapters/agent/opencode` | symlink 创建失败：A required privilege is not held by the client | 原生兼容相关，未放宽权限 | 具备 symlink 权限的 Windows 或 Linux/macOS，未重验 |
| `adapters/agent/modelcatalog` | 配置目录返回空；另有无扩展名的 Unix fixture 无法执行 | 原生目录发现相关；未改全目录 | 隔离 HOME/USERPROFILE 并提供平台可执行 fixture |
| `adapters/agent/pi` | 测试二进制未在 Windows PATH 解析成功 | 其他 Adapter/注册表；未扩展改造 | 具备对应可执行 fixture 的 Windows 或现有 Unix CI |
| `adapters/agent/primeagent` | 固定 /ao-data 路径分隔符断言及本机 CLI 解析失败 | 其他 Adapter | 平台路径 fixture，待重验 |
| `adapters/agent/registry` | 测试二进制未在 Windows PATH 解析成功 | 其他 Adapter/注册表；未扩展改造 | 具备对应可执行 fixture 的 Windows 或现有 Unix CI |
| `adapters/agentaccount` | symlink 创建失败：A required privilege is not held by the client | 原生兼容相关，未放宽权限 | 具备 symlink 权限的 Windows 或 Linux/macOS，未重验 |
| `adapters/chatdriver/agyacp` | launch Command/Args 与预期不同（Windows .par 路径） | 其他 Adapter | 对应 Windows 启动模型，待定位 |
| `adapters/chatdriver/codexappserver` | /tmp/ws 被 Windows filepath.IsAbs 拒绝，挡住协议测试 | Q9 直接相关；已改为真实临时绝对路径 | 本轮完整 Codex 包重验，见最新结果 |
| `adapters/chatdriver/cursoracp` | symlink 创建失败：A required privilege is not held by the client | 原生兼容相关，未放宽权限 | 具备 symlink 权限的 Windows 或 Linux/macOS，未重验 |
| `adapters/chatdriver/kimiacp` | 持久 ACP host 启动超时，尚未证明死锁或具体根因 | 其他 Adapter | 隔离可执行 fixture 和宿主启动日志，待定位 |
| `adapters/chatdriver/processenv` | bash 解析到 System32 WSL 入口，Windows 环境变量未传入 Bash | Q9 直接相关；新 runner 指定已存在 Git Bash PATH | Git Bash 的 Windows 环境语义，见重验 |
| `adapters/chatdriver/persistenthost` | Windows mode 返回 0666，与 Unix 0600 断言冲突 | Q9 直接相关；保留业务断言 | Windows 验重放/配额，Unix 验 mode；不声称 ACL 已验 |
| `adapters/reviewer/agy` | 固定 Unix binary 路径被 Windows 绝对路径门禁拒绝 | 首版 Reviewer fixture；已改本机绝对临时路径 | 本轮 Windows 重验；Kimi 追加 ToSlash 消息断言 |
| `adapters/reviewer/claudecode` | Restore 返回 ok=false；fixture 原只设 HOME | 首版 Reviewer fixture；补 USERPROFILE | 本轮 Windows 完整包重验 |
| `adapters/reviewer/cursor` | Unix 0700/0600 mode 断言；放行后发现 JSON 路径转义断言 | 首版 Reviewer fixture；改为解码 JSON 比较路径 | Windows 业务断言；Unix mode 单列，ACL 未验 |
| `adapters/reviewer/devin` | 固定 Unix binary 路径被 Windows 绝对路径门禁拒绝 | 首版 Reviewer fixture；已改本机绝对临时路径 | 本轮 Windows 重验；Kimi 追加 ToSlash 消息断言 |
| `adapters/reviewer/droid` | 固定 Unix binary 路径被 Windows 绝对路径门禁拒绝 | 首版 Reviewer fixture；已改本机绝对临时路径 | 本轮 Windows 重验；Kimi 追加 ToSlash 消息断言 |
| `adapters/reviewer/kimi` | 固定 Unix binary 路径被 Windows 绝对路径门禁拒绝 | 首版 Reviewer fixture；已改本机绝对临时路径 | 本轮 Windows 重验；Kimi 追加 ToSlash 消息断言 |
| `adapters/runtime/conpty` | 原网格断言依赖英文 `mode con` 标签 | 首版 Windows 运行时相关；改为真实子进程读取 Windows API | 相同 CI 四项 race/count=3 已通过；不能推定完整 ConPTY 包或 Cue shell 通过 |
| `adapters/runtime/tmux` | POSIX sh 未找到；tmux 还存在系统二进制解析差异 | 运行时相关；共享凭据过滤另行验证 | 明确 Git Bash/系统 tmux 环境；本机默认 bash 原为 WSL 入口 |
| `adapters/telemetry` | 并发读取时无法删除空 install-id：文件被其他进程使用 | 非本次 Q9 改动；潜在 Windows 文件锁问题 | Windows 并发复现，未修 |
| `agentlaunch` | 旧 Git Bash AO 入口校验退出 127 | 首版启动相关；新 runner 指定已有 Git Bash | 新 runner 完整 race 已通过，6 PASS / 2 Unix SKIP |
| `browserchrome` | 安装渠道探测与测试期望不符；fakeRegistry 已注入 | 非 Q9；未对真实注册表作修复 | Windows 渠道探测 fixture，待定位 |
| `cli` | symlink 创建失败：A required privilege is not held by the client | 原生兼容相关，未放宽权限 | 具备 symlink 权限的 Windows 或 Linux/macOS，未重验 |
| `adapters/workspace/gitworktree` | 旧路径断言与资产 symlink 权限失败 | 修复引用/分隔符断言；新凭据边界通过 | 最新完整包 183 PASS / 1 symlink FAIL；CI teardown/race 通过，不覆盖资产复制失败 |
| `observe/usage` | 目录 anthropic hash mismatch；continuation 未绑定；文件替换 identity 不符 | 用量相关，不能制造成本成功证据 | 核对资源字节/换行及 Windows 文件身份；未收口 |
| `review` | Node shim runtime PATH 的目录顺序断言不符 | 首版 Reviewer 相关 | Windows PATH 与 bundled Node 解析，待定位 |
| `reviewgateway` | Windows mode 0777 与 Unix 0700 断言冲突；agent 还有 symlink 权限失败 | 首版安全/账号相关 | 业务/ACL/Unix mode 分别验证，未全面收口 |
| `service/agent` | Windows mode 0777 与 Unix 0700 断言冲突；agent 还有 symlink 权限失败 | 首版安全/账号相关 | 业务/ACL/Unix mode 分别验证，未全面收口 |
| `service/agentauth` | Kimi 信任记录为空，Copilot 配置路径不存在 | 其他 Adapter 认证 | 统一用户目录后逐 Adapter 复现，未修 |
| `service/importer` | AO 状态目录导入未按 fixture 预期阻断 | 非 Q9；目录来源需核查 | 隔离 USERPROFILE/AO_DATA_DIR 的 Windows，待定位 |
| `service/project` | Windows clone 输入被 INVALID_GIT_URL 拒绝；Git show-toplevel 分隔符断言不符 | 首版多仓相关；不放宽 URL 验证 | Windows file URL/路径 fixture，未收口 |
| `integration` | 唯一失败 trusted admission evidence is required | 直接相关；补真实 SQLite 准入 fixture | 最新隔离完整 integration PASS（740.982 秒） |
| `service/shellterm` | ConPTY 网格/交互 Git Bash 就绪信号不符 | 首版 Windows 运行时相关；不伪称平台豁免 | 真实 Windows 控制台与 shell 就绪诊断，未收口 |
| `service/systemcheck` | TmuxMissing 预期 Ready=false，实际 true | 非 Q9，平台 systemcheck 分支未定位 | Windows 与 Unix 对应要求，未修 |
| `service/systeminstall` | npm prefix 可写性预期不符；cloudflared fixture 成功状态等待超时 | 非 Q9；不改真实安装/系统权限 | 独立安装 fixture 的对应 OS，待定位 |
| `service/usage` | Claude continuation 未注册；默认 roots 跑到真实 USERPROFILE | 用量相关，旧隔离失效 | 双 HOME/原生目录隔离后复现，未收口 |
| `service/session` | PR 文本为 CRLF；不可读目录权限断言在 Windows 未触发 | 首版会话相关 | Git 换行与 Windows ACL 分别核验，未收口 |
| `session_manager` | 两仓相同内容与秒级时间产生同一 SHA；命名 effort 被清空 | 直接相关：fixture 内容加入仓库身份；SV01 已修 | 最新隔离 Manager 完整包 PASS（336.263 秒） |
| `tmuxbin` | symlink 创建失败：A required privilege is not held by the client | 原生兼容相关，未放宽权限 | 具备 symlink 权限的 Windows 或 Linux/macOS，未重验 |

## 第二批 lint 交接：197 项交付范围

本批基于204文件输入快照 `2332967c461798a3983457b83fd61897f914a2c95caaa42e03db80af5875fd27`（清单自身 SHA），保留在 `m6-lint-b2-input-source-manifest.json`。原完整输出252项中，本批197项逐项处置并通过所属包分组 lint；通过范围共25个包。另55项逐项保留未解决，未按平台或未改行豁免。未重跑完整 lint；`m6-lint-full-closeout` 的 exit4/763.125秒/252项+超时仍是最近完整结果，M6整体门禁仍未通过，交由统筹独立验收。

逐项依据、每次真实命令及输出、源码和超限申报分别见 `C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-lint-b2-dispositions.md`、`m6-lint-b2-summary.json` 与 `m6-final-source-manifest.json`。原191文件输入清单和两份换行对照没有覆盖。

### 改动与契约

- 格式/导出注释、旧循环复制、同类型转换、无用返回值及死入口按实际调用搜索清理。内部 `capability.Store`、`integration.Path` 和 context 首参同步全部调用；删除的旧恢复入口由现行 scheduler recovery 路径承担职责，迁移正式登记与回滚断言保留。
- Scripted Driver 在创建具体会话时注入恢复 ID，再选择 Steerable 包装，移除不受检查的向下断言。新增一个 Resume 行为用例覆盖两种真实包装；删除注入 ID 的逻辑会使断言变红。
- 三个只读文件读取显式返回关闭错误；私有 Git index 清理失败并入返回错误，保留原错误身份。现有工件快照测试补充私有 index 目录清空断言，用户 index/HEAD/越界内容断言仍保留。
- Windows SID 查询改用无需 Close 的 TOKEN_QUERY 伪句柄，真实 DACL/所有权/首次私有创建保持。审批调用复用 process.CommandContext，后续显式 SysProcAttr 仍只含 CREATE_NO_WINDOW，不继承 HideWindow；固定系统 PowerShell、stdin 数据与净化环境不变。没有发起 Hello 手动流程。
- 无 Driver 保持 unknown/not_probed，探测及栅栏失败保持 Frozen。MCP transport 保持文本错误边界，不扩大 unwrap。Git SHA-1 对象协议、已校验整数范围和同步 Windows API 指针逐语句说明对应 gosec 规则；跨平台 handoff 限额未按 Windows 分析折叠。

### 真实验证

全部高 I/O 检查串行，沿用隔离 runner、8 GiB 空间门禁、GOMAXPROCS=2 和三份真实配置 SHA/mtime 阻断；以下每条命令自身的前后记录均 `protectedConfigUnchanged=true`。但跨执行时段并非完全不变：2026-11-07 23:49:55 UTC 的存储补验结束后，到2026-11-08 05:52:35 UTC 的存储 lint 开始前，真实 config.toml 的 SHA 从 `fd328b296c0308b068af2bad3d0719d78a3aaefa57dbad5d082c06e1ef790fe4` 变为 `ae0378cb89f44241a4aef3526bcbe3d5e0b557cd0a4434de552ac9d97b760931`，mtime 同时变化。两份 Claude 配置不变。该间隔变化尚未归因，`protectedConfigsAcrossRunsUnchanged=false` 如实保留；未读取配置内容、回写配置或解除 runner 门禁。记录及最终只读快照见 m6-lint-b2-config-identity.json，不能据单次无漂移宣称整批配置无变化。

| 证据 label | 实际命令（API 在隔离仓库根目录，其余在 backend） | 实际结果 |
|---|---|---|
| `m6-lint-b2-api-ts-cached` | `node.exe C:/Users/11459/AppData/Local/npm-cache/_npx/0949cc204516dc58/node_modules/openapi-typescript/bin/cli.js backend/internal/httpd/apispec/openapi.yaml -o frontend/src/api/schema.ts` | exit 0 / 2.509 秒；`openapi-typescript 7.4.4` 成功生成 schema.ts；产物 SHA 不变 |
| `m6-lint-b2-api` | `cmd.exe /d /c npm run api` | exit 1 / 8.469 秒；spec 生成成功；TS 失败：`'openapi-typescript' is not recognized as an internal or external command` |
| `m6-lint-b2-callers-compile` | `go test -p 2 -parallel 2 ./internal/daemon ./internal/integration ./internal/session_manager ./internal/service/workflow -run ^$ -count=1 -timeout=5m -json` | exit 0 / 27.629 秒；只编译，无用例执行 |
| `m6-lint-b2-control-tests` | `go test -p 2 -parallel 2 ./internal/httpd/... ./internal/cli -run ^(TestOperator\|TestMCPWorkflow\|TestBuild_\|TestRouteSpecParity\|TestWorkflows\|TestWorkflow) -count=1 -timeout=10m -json` | exit 0 / 69.096 秒；52 PASS |
| `m6-lint-b2-control` | `golangci-lint.exe run --timeout=10m --concurrency=2 ./internal/httpd ./internal/httpd/controllers ./internal/httpd/apispec/specgen ./internal/cli ./internal/daemon` | exit 0 / 115.294 秒；`0 issues.` |
| `m6-lint-b2-core-block-comments` | `golangci-lint.exe run --timeout=10m --concurrency=2 ./internal/domain ./internal/ports ./internal/adapters/chatdriver/scripted ./internal/service/capability ./internal/service/intervention ./internal/service/principal ./internal/service/scheduler ./internal/service/budget ./internal/service/artifact ./internal/service/qualitygate ./internal/service/integration` | exit 0 / 55.799 秒；`0 issues.` |
| `m6-lint-b2-core` | `golangci-lint.exe run --timeout=10m --concurrency=2 ./internal/domain ./internal/ports ./internal/adapters/chatdriver/scripted ./internal/service/capability ./internal/service/intervention ./internal/service/principal ./internal/service/scheduler ./internal/service/budget ./internal/service/artifact ./internal/service/qualitygate ./internal/service/integration` | exit 1 / 63.644 秒；实际诊断保留在同名 .log，完成最小返修后仅重验受影响 lint |
| `m6-lint-b2-delivery-local-style` | `golangci-lint.exe run --timeout=10m --concurrency=2 ./internal/adapters/workspace/gitworktree` | exit 0 / 24.086 秒；`0 issues.` |
| `m6-lint-b2-delivery-race` | `go test -race -p 2 -parallel 2 ./internal/adapters/workspace/gitworktree -run ^TestM4 -count=1 -timeout=10m -json` | exit 0 / 66.36 秒；9 PASS |
| `m6-lint-b2-delivery` | `golangci-lint.exe run --timeout=10m --concurrency=2 ./internal/adapters/workspace/gitworktree` | exit 1 / 26.835 秒；实际诊断保留在同名 .log，完成最小返修后仅重验受影响 lint |
| `m6-lint-b2-domain-race` | `go test -race -p 2 -parallel 2 ./internal/domain ./internal/ports ./internal/adapters/chatdriver/scripted ./internal/service/capability ./internal/service/intervention ./internal/service/principal ./internal/service/scheduler ./internal/service/budget ./internal/service/artifact ./internal/service/qualitygate ./internal/service/integration -count=1 -timeout=5m -json` | exit 0 / 120.536 秒；846 PASS |
| `m6-lint-b2-http-remaining` | `go test -p 2 -parallel 2 ./internal/httpd/controllers -run <精确顶层名称正则；完整 argv 见同名 JSON> -count=1 -timeout=10m -json` | exit 0 / 7.122 秒；84 PASS |
| `m6-lint-b2-platform-race` | `go test -race -p 2 -parallel 2 ./internal/process ./internal/mobilebridge ./internal/pricing ./internal/adapters/operatorconsent -count=1 -timeout=5m -json` | exit 0 / 23.687 秒；222 PASS, 25 SKIP |
| `m6-lint-b2-platform` | `golangci-lint.exe run --timeout=10m --concurrency=2 ./internal/process ./internal/mobilebridge ./internal/pricing ./internal/service/chat ./internal/adapters/operatorconsent` | exit 0 / 117.975 秒；`0 issues.` |
| `m6-lint-b2-render-race` | `go test -race -p 2 -parallel 2 ./internal/service/chat -run (Render\|RunningProviderTurn) -count=1 -timeout=5m -json` | exit 0 / 132.724 秒；34 PASS |
| `m6-lint-b2-storage-conversions` | `golangci-lint.exe run --timeout=10m --concurrency=2 ./internal/storage/sqlite ./internal/storage/sqlite/store ./internal/storage/sqlite/sqlitetest` | exit 0 / 108.345 秒；`0 issues.` |
| `m6-lint-b2-storage-race` | `go test -race -p 2 -parallel 2 ./internal/storage/sqlite ./internal/storage/sqlite/store ./internal/storage/sqlite/sqlitetest -count=1 -timeout=15m -json` | exit 1 / 970.157 秒；600 PASS, 2 SKIP；2 个包 FAIL；命令失败，完整失败/超时输出见同名原始日志，不能用已通过事件覆盖 |
| `m6-lint-b2-storage-remaining-race` | `go test -race -p 2 -parallel 2 ./internal/storage/sqlite ./internal/storage/sqlite/store -run <精确顶层名称正则；完整 argv 见同名 JSON> -count=1 -timeout=10m -json` | exit 0 / 364.855 秒；100 PASS |
| `m6-lint-b2-storage` | `golangci-lint.exe run --timeout=10m --concurrency=2 ./internal/storage/sqlite ./internal/storage/sqlite/store ./internal/storage/sqlite/sqlitetest` | exit 1 / 101.324 秒；实际诊断保留在同名 .log，完成最小返修后仅重验受影响 lint |

本批行为检查合计 1947 个 PASS、27 个 SKIP、0 个 FAIL 事件（含子测试，不当作独立场景数；包超时可能没有单测试FAIL事件）；失败命令为 m6-lint-b2-storage-race，只编译记录另列。核心组完整日志最终为846 PASS、无测试FAIL/SKIP、8包PASS/3包无测试文件，已刷新途中823 PASS摘要，不据途中统计重跑。所有跳过用例的原始名称在第二批 summary 的 runs/skips 中；手动 Hello 和平台限定跳过不算验收通过。

控制面首轮仅52个 PASS事件，不能称为全部 M2/审批 HTTP 覆盖。依据本批实际修改的12个控制器测试文件逐函数核对（其中11个含顶层测试，另1个为共享 fixture），补跑未完成的68个顶层用例；包括 TestM2OperatorConsentRefusalsAreDistinct、TestM2BindSurfacesOwnershipFailures、TestM2InterventionAcceptsControllerGeneration 和 TestQualityReplayLocalExpiryFence。补验 exit0，既有52项没有重复；合并日志后修改文件的83个顶层用例都有 PASS，missing=[]，逐项来源见 m6-lint-b2-http-coverage.json。此结论只覆盖修改文件中的函数，不扩充为全部 M2/审批测试通过。

完整控制面修改文件核对另见 m6-lint-b2-control-coverage.json：specgen/quality_test.go 仅调整 import，TestBuild_ArtifactFilesPreserveFrozenNull 已在首轮通过；daemon/execution_recovery_test.go 仅9处 ExecutionContext 参数顺序同步，context、testing.TB 及其余参数值不变，共享 helper 也只调整签名顺序。对应14个顶层行为用例未重跑，采用四个调用包的 -run '^$' 编译验证，不计入 PASS；逐行依据保存在 m6-lint-b2-control-compile-only.diff。

API 原命令 `npm run api` 实际 exit1、8.469秒：spec 生成完成，TS 阶段因命令入口未解析而失败。经用户授权直接使用已缓存的 openapi-typescript 7.4.4 CLI 等价入口，exit0、2.509秒；没有联网、安装或修改 package/lock/config。生成前后及主仓/副本的 openapi.yaml 与 schema.ts SHA 均一致，详见 m6-lint-b2-api-identity.json；原命令失败保留，不记作 npm run api 通过。

存储完整 race 实际 exit1、970.157秒，sqlite 与 store 均输出 `panic: test timed out after 15m0s`；分别运行到 `TestMigrateRecognizesPreRenumberedChatSchema`（本用例18秒）与 `TestSessionTelemetryFactsRoundTrip`（本用例51秒）。没有断言失败事件，仍是两个包失败。按修改测试文件与完整事件计算77个未完成顶层用例，单独补验 exit0、364.855秒、100 PASS事件；两次日志并集覆盖本批存储修改文件全部97个顶层用例，`m6-lint-b2-storage-coverage.json` 的 missing=[]。这只证明受影响范围，不改写全包超时结果，不延长预算重跑整套。

Git 交付组初次分组 lint 实际5项（两处局部 err 赋值、两处同类型声明及一个嵌套注释导致的 G505 审计说明未识别）；最小修正后0项。存储组初次剩一处同类型 Kind 转换，删除后0项。新增诊断连同原始197项处置保留在每次原始日志，不删除失败轮次。

核心组首次 lint 只给组内首项加注释，实际仍报告21项后续符号；已改为整组契约注释，第二次返回0。两轮 formatter 仅处理精确文件：先基础格式，随后按仓库已有 goimports local-prefixes 校正；没有修改 lint/Git 配置或整仓换行。

### 最新源码与规模

本批实际改动 124 个 Go 文件（含必要调用同步）；当前完整交付源码清单 302 项、4文档、6依赖，主仓/副本源码原始字节一致。额外换行对照仍为 2439 项，其中 2079 项原始字节不同，仅 CRLF→LF 后一致；不声称全树原始字节一致。稳定源码摘要 `14a3d593849e30293a6bbad0e3772e271a4f53e61aa792ecd3db94a934f46256`。主仓 HEAD 仍为 M5 `acf9bafa8c14de5533a35f66d4d2c9bfc057baae`，未提交/Push。

变更测试文件 104 个、全文 40210 行；变更生产源码排除生成 TS schema 后 133 个、全文 68564 行。这是含既有代码的文件范围计数，不冒充新增行或模块比值证明。新增测试最大 197 行；本批新增 Resume 文件的实数见逐项源码和新测试清单。既有超限测试 34 个，临时脚本超过400行的数量为 0。逐文件申报如下。

| 既有测试文件 | 全文行数 / 超出300行 | 原因及验收边界 |
|---|---|---|
| `backend/internal/adapters/chatdriver/codexappserver/driver_test.go` | 1868 / 1568 | 首批累计变更；本批未触及此文件，保留既有超限申报。 SV-M6-01/02/03：原生模型/effort、输入和凭据隔离 |
| `backend/internal/adapters/chatdriver/codexappserver/history_test.go` | 480 / 180 | 首批累计变更；本批未触及此文件，保留既有超限申报。 SV-M6-01/02/03：原生模型/effort、输入和凭据隔离 |
| `backend/internal/adapters/chatdriver/codexappserver/input_test.go` | 498 / 198 | 首批累计变更；本批未触及此文件，保留既有超限申报。 SV-M6-01/02/03：原生模型/effort、输入和凭据隔离 |
| `backend/internal/adapters/chatdriver/codexappserver/steer_test.go` | 392 / 92 | 首批累计变更；本批未触及此文件，保留既有超限申报。 SV-M6-01/02/03：原生模型/effort、输入和凭据隔离 |
| `backend/internal/adapters/reviewer/claudecode/claudecode_test.go` | 556 / 256 | 首批累计变更；本批未触及此文件，保留既有超限申报。 Q9：Reviewer Windows 路径与 fixture 隔离 |
| `backend/internal/adapters/reviewer/cursor/cursor_test.go` | 617 / 317 | 首批累计变更；本批未触及此文件，保留既有超限申报。 Q9：Reviewer Windows 路径与 fixture 隔离 |
| `backend/internal/adapters/workspace/gitworktree/workspace_integration_test.go` | 1733 / 1433 | 首批累计变更；本批未触及此文件，保留既有超限申报。 Q9/M4：多仓基点、工件与工作区保全 |
| `backend/internal/daemon/execution_recovery_test.go` | 818 / 518 | 既有测试整文件规模；本批机械/接口同步或原断言加强，未新增同规模矩阵。 M2/Q9：准入、绑定状态与恢复所有权 |
| `backend/internal/domain/workflow_binding_test.go` | 392 / 92 | 既有测试整文件规模；本批机械/接口同步或原断言加强，未新增同规模矩阵。 M2/Q9：准入、绑定状态与恢复所有权 |
| `backend/internal/httpd/controllers/operator_approvals_fixture_test.go` | 389 / 89 | 既有测试整文件规模；本批机械/接口同步或原断言加强，未新增同规模矩阵。 M2：主体身份、逐操作授权和审批失败闭锁 |
| `backend/internal/httpd/controllers/workflows_m2_authz_test.go` | 856 / 556 | 既有测试整文件规模；本批机械/接口同步或原断言加强，未新增同规模矩阵。 M2：主体身份、逐操作授权和审批失败闭锁 |
| `backend/internal/httpd/controllers/workflows_m2_bind_test.go` | 1000 / 700 | 既有测试整文件规模；本批机械/接口同步或原断言加强，未新增同规模矩阵。 M2/Q9：准入、绑定状态与恢复所有权 |
| `backend/internal/httpd/controllers/workflows_m2_identity_test.go` | 879 / 579 | 既有测试整文件规模；本批机械/接口同步或原断言加强，未新增同规模矩阵。 M2：主体身份、逐操作授权和审批失败闭锁 |
| `backend/internal/httpd/controllers/workflows_m2_intervention_test.go` | 339 / 39 | 既有测试整文件规模；本批机械/接口同步或原断言加强，未新增同规模矩阵。 M2：Steer/Queue/Stop 回执与 Frozen 闭锁 |
| `backend/internal/httpd/controllers/workflows_test.go` | 877 / 577 | 既有测试整文件规模；本批机械/接口同步或原断言加强，未新增同规模矩阵。 M1/M3：工作流生命周期、CAS/CDC 与准入 |
| `backend/internal/integration/lifecycle_sqlite_test.go` | 533 / 233 | 首批累计变更；本批未触及此文件，保留既有超限申报。 M2/Q9：真实 SQLite/Manager/Scripted Driver 装配与重启 |
| `backend/internal/integration/m2_e2e_test.go` | 1714 / 1414 | 首批累计变更；本批未触及此文件，保留既有超限申报。 M2/Q9：真实 SQLite/Manager/Scripted Driver 装配与重启 |
| `backend/internal/integration/m2_full_flow_e2e_test.go` | 1814 / 1514 | 既有测试整文件规模；本批机械/接口同步或原断言加强，未新增同规模矩阵。 M2/Q9：真实 SQLite/Manager/Scripted Driver 装配与重启 |
| `backend/internal/service/execution/coordinator_test.go` | 2234 / 1934 | 首批累计变更；本批未触及此文件，保留既有超限申报。 M2/Q9：准入、绑定状态与恢复所有权 |
| `backend/internal/service/execution/local_gateway_test.go` | 2265 / 1965 | 首批累计变更；本批未触及此文件，保留既有超限申报。 M2/Q9：准入、绑定状态与恢复所有权 |
| `backend/internal/service/execution/ownership_test.go` | 350 / 50 | 首批累计变更；本批未触及此文件，保留既有超限申报。 M2/Q9：准入、绑定状态与恢复所有权 |
| `backend/internal/service/intervention/coordinator_test.go` | 2486 / 2186 | 既有测试整文件规模；本批机械/接口同步或原断言加强，未新增同规模矩阵。 M2：Steer/Queue/Stop 回执与 Frozen 闭锁 |
| `backend/internal/service/principal/principal_test.go` | 732 / 432 | 既有测试整文件规模；本批机械/接口同步或原断言加强，未新增同规模矩阵。 M2：主体身份、逐操作授权和审批失败闭锁 |
| `backend/internal/service/workflow/service_test.go` | 659 / 359 | 既有测试整文件规模；本批机械/接口同步或原断言加强，未新增同规模矩阵。 M1/M3：工作流生命周期、CAS/CDC 与准入 |
| `backend/internal/session_manager/multi_repo_basesha_test.go` | 604 / 304 | 首批累计变更；本批未触及此文件，保留既有超限申报。 Q9/M4：多仓基点、工件与工作区保全 |
| `backend/internal/session_manager/multi_repo_workspace_test.go` | 439 / 139 | 首批累计变更；本批未触及此文件，保留既有超限申报。 Q9/M4：多仓基点、工件与工作区保全 |
| `backend/internal/session_manager/task_naming_test.go` | 636 / 336 | 首批累计变更；本批未触及此文件，保留既有超限申报。 SV-M6-01：后台命名 effort |
| `backend/internal/storage/sqlite/migrate_m2_rollback_test.go` | 323 / 23 | 既有测试整文件规模；本批机械/接口同步或原断言加强，未新增同规模矩阵。 M2：回滚事务/CDC；保留 M1 已知回滚缺陷记录 |
| `backend/internal/storage/sqlite/store/m2_anomaly_test.go` | 715 / 415 | 既有测试整文件规模；本批机械/接口同步或原断言加强，未新增同规模矩阵。 M2/Q9：真实 SQLite/Manager/Scripted Driver 装配与重启 |
| `backend/internal/storage/sqlite/store/m2_e2e_test.go` | 467 / 167 | 既有测试整文件规模；本批机械/接口同步或原断言加强，未新增同规模矩阵。 M2/Q9：真实 SQLite/Manager/Scripted Driver 装配与重启 |
| `backend/internal/storage/sqlite/store/workflow_binding_store_test.go` | 1272 / 972 | 既有测试整文件规模；本批机械/接口同步或原断言加强，未新增同规模矩阵。 M2/Q9：准入、绑定状态与恢复所有权 |
| `backend/internal/storage/sqlite/store/workflow_cdc_test.go` | 422 / 122 | 既有测试整文件规模；本批机械/接口同步或原断言加强，未新增同规模矩阵。 M1/M3：工作流生命周期、CAS/CDC 与准入 |
| `backend/internal/storage/sqlite/store/workflow_collaboration_store_test.go` | 577 / 277 | 既有测试整文件规模；本批机械/接口同步或原断言加强，未新增同规模矩阵。 M2：消息授权、容量和资源请求 |
| `backend/internal/storage/sqlite/store/workflow_store_test.go` | 1673 / 1373 | 既有测试整文件规模；本批机械/接口同步或原断言加强，未新增同规模矩阵。 M1/M3：工作流生命周期、CAS/CDC 与准入 |

### 保留失败与未验

SV-M6-06 的真实分离宿主用例/路径本批未修改或重试，前三轮失败继续保留。完整 lint 超时、其他55项诊断、既有 symlink 权限失败、打包 TLS、真实 Provider、桌面 Queue、手动 Hello、安装升级与其他 OS 不因本批分组通过而改变状态。真实 `.claude.json` 的8条旧测试记录仍未经授权清理，双方均不得自行回写。

本批只完成上述有界验证和证据交接；整体 M6、后续完整 lint 或提交均由统筹根据独立验收决定。

## Windows 用量与项目回归交接

本批输入为第二批已验收的302项源码快照 `14a3d593849e30293a6bbad0e3772e271a4f53e61aa792ecd3db94a934f46256`，原清单冻结在 `m6-wr-input-source-manifest.json`。继续同一M6会话，未派子Agent、未提交/Push。先在完整隔离runner复现旧全量中24个顶层目标：exit1、25.796秒、32 FAIL/14 PASS事件，三个包均失败；没有把旧失败归为环境因素而直接豁免。

详细原始命令/退出码见 `C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-wr-summary.json` 和同名 .json/.log；24项逐次结果见 `m6-wr-targets.md`，原始输入和所有失败轮次均保留。

### 根因及最小修正

| 项目 | 实证根因与修正 | 验证边界 |
|---|---|---|
| Claude 续接 | 三条失败来自 fixture 把 Windows 反斜杠直接拼入 JSON。复用/使用 json.Marshal 编码 cwd，同步五条 collector fixture（包括错归属、错文件名的拒绝场景），防止负例因非法 JSON 偶然通过。 | 真实 Collector→SQLite→Ingestor；旧绑定和续接绑定保留，50 tokens 聚合及重复 reconcile/ingest 不翻倍断言保留。 |
| 默认 roots | 新runner复现时实际路径已是隔离 USERPROFILE，确认该次默认roots解析落在隔离用户目录；失败源于测试只设 HOME，而 Windows os.UserHomeDir 使用 USERPROFILE。该用例同步两者，保留 CLAUDE_CONFIG_DIR 覆盖断言。 | 未改生产 roots 逻辑、runner 或用户环境。 |
| replacement 身份 | Windows os.Stat 的身份可到 SameFile 时才按路径加载，Rename 后两份快照可能都读到新文件。改用生产 SourceIdentity 在替换前后读取真实句柄身份。 | 保留真实文件替换、身份必须不同、legacy 修复拒绝错误来源的断言；生产 GetFileInformationByHandle 逻辑未改。 |
| 同一描述符原子替换 | 第一轮仅开放 FILE_SHARE_DELETE 仍失败；定向诊断明确 os.Rename 返回 Access is denied。第二轮沿用仓库 ReplaceFileW 方式，仅在用例里使用真实只读共享句柄和原生替换，flags=0，无删除目标或重试回退。 | 旧句柄先计120、新 generation 累计340的断言保留；race 定向验证通过。未改生产打开方式、文件身份或计量算法。 |
| 本地项目 Clone | 17处 fixture 用 Windows 原生路径构造 URL.Path，盘符/反斜杠导致 INVALID_GIT_URL。复用现有 HTTP Clone fixture 的 ToSlash＋盘符前缀规则生成合法 file URL。两处 show-toplevel 断言转换 Git 斜杠；恢复测试在首次 Open 后即登记 Close，覆盖提前失败清理。 | 真实临时 Git 仓库与 SQLite；保留 origin、注册/准备/取消/超时/恢复、旧ID拒绝、用户修改保全和精确清理断言；不放宽 helper/凭据 URL 拒绝。 |

本批8个Go文件全部是测试或测试辅助文件；没有新增顶层测试用例、修改生产接口、更新依赖或新增架构抽象。两个新文件只实现该文件替换用例的 Windows/Unix OS 边界，没有系统权限设置、真实认证或 Provider 调用。

### 定价输入身份与持久修复缺口

三个 provider blob 在主仓和输入副本各有一个CRLF；只替换为LF后的摘要、git show HEAD 的提交blob摘要和原manifest sha256逐一精确相同。仅C盘副本执行了这个已授权对照，原始字节保留于 wr-pricing-before/，完整记录在 m6-wr-pricing-control.json。主仓三个blob、两个树的manifest及生产严格hash校验保持不变。

需区分三个来源：该用例直接从仓库 pricing/catalog/v1 加载；生产 daemon 使用 AO_DATA_DIR 下的缓存和生产 HTTP fetcher（usage_pricing.go），没有以仓库文件替代运行时输入；当前 forge extraResources 清单未列定价目录。本批仅静态核对分发与运行时装配，没有运行打包或访问远端目录，不能据仓库测试通过证明发布包/线上价格服务可用。

持久检出修复需要获准修改仓库 `.gitattributes`，建议精确添加 `pricing/catalog/v1/providers/**/*.json -text`，使内容寻址的blob保留提交原字节，再按登记范围恢复工作树字节并验证。没有获得该配置变更授权，因此本批未实施；主仓仍有CRLF，直接以其字节运行该用例仍会摘要失败。不能重写manifest、忽略摘要或把C盘条件通过写成主仓已修。

### 实际验证

所有高I/O命令串行，GOMAXPROCS=2、-p 2/-parallel 2、有界超时、8GiB空间门禁和Provider凭据隔离沿用原runner。本批每条命令及跨命令间的三份指定配置SHA/mtime均一致；config.toml保持 ae0378…。前一批跨执行空档的变化仍未归因，没有读取其内容或回写。

| label | 实际命令（隔离 backend） | 退出结果 |
|---|---|---|
| `m6-wr-reproduce` | `go test -p 2 -parallel 2 ./internal/observe/usage ./internal/service/usage ./internal/service/project -run <24项精确名单；完整argv见同名JSON> -count=1 -timeout=10m -json` | exit 1 / 25.796秒；32 FAIL, 14 PASS；包结果 3 FAIL |
| `m6-wr-format` | `gofmt -w internal/service/project/service_test.go internal/service/usage/collector_continuation_test.go internal/service/usage/collector_test.go internal/observe/usage/continuation_integration_test.go internal/observe/usage/ingestor_integration_test.go internal/observe/usage/legacy_repair_test.go internal/observe/usage/transcript_replace_unix_test.go internal/observe/usage/transcript_replace_windows_test.go` | exit 0 / 0.137秒 |
| `m6-wr-targets-fixed` | `go test -p 2 -parallel 2 ./internal/observe/usage ./internal/service/usage ./internal/service/project -run <24项精确名单；完整argv见同名JSON> -count=1 -timeout=10m -json` | exit 1 / 40.372秒；59 PASS, 1 FAIL；包结果 1 FAIL, 2 PASS |
| `m6-wr-replacement-diagnosis` | `go test -p 2 -parallel 2 ./internal/observe/usage -run ^TestIngestorReadsIdentityAndContentFromSingleDescriptorAcrossAtomicReplacement$ -count=1 -timeout=5m -json` | exit 1 / 9.181秒；1 FAIL；包结果 1 FAIL |
| `m6-wr-replacement-fixed-race` | `go test -race -p 2 -parallel 2 ./internal/observe/usage -run ^TestIngestorReadsIdentityAndContentFromSingleDescriptorAcrossAtomicReplacement$ -count=1 -timeout=5m -json` | exit 0 / 65.009秒；1 PASS；包结果 1 PASS |
| `m6-wr-packages` | `go test -p 2 -parallel 2 ./internal/observe/usage ./internal/service/usage ./internal/service/project -count=1 -timeout=10m -json` | exit 0 / 148.644秒；421 PASS, 5 SKIP；包结果 3 PASS |
| `m6-wr-lint` | `golangci-lint.exe run --timeout=10m --concurrency=2 ./internal/observe/usage ./internal/service/usage ./internal/service/project` | exit 0 / 52.817秒；`0 issues.` |

受影响三个完整包实际 exit0、148.644秒，421 PASS / 5 SKIP / 0 FAIL事件；含子测试，不冒充独立场景数。24个原失败顶层目标最新结果：{'pass': 24}。原子替换经历一次初始复现、一次共享句柄修正失败和一次仅定位错误的诊断运行，随后第二次修正通过；未达到三次修复失败后继续盲改。

完整包中的跳过项及原因：

- `service/usage/TestCollectorRejectsPathOutsideProviderRootAndSymlinkEscape`：创建 symlink 返回 `A required privilege is not held by the client.`；未改变权限或跳过条件。
- `service/usage/TestDiscoverKimiPathRejectsUntrustedIndexRecords/symlink_escape`：创建 symlink 返回 `A required privilege is not held by the client.`；未改变权限或跳过条件。
- `observe/usage/TestTranscriptWatcherResolvesSymlinkedRoot`：创建 symlink 返回 `A required privilege is not held by the client.`；未改变权限或跳过条件。
- `service/project/TestManager_AddRejectsEquivalentRepositoryPaths/alias-first=false`：创建 symlink 返回 `A required privilege is not held by the client.`；未改变权限或跳过条件。
- `service/project/TestManager_AddRejectsEquivalentRepositoryPaths/alias-first=true`：创建 symlink 返回 `A required privilege is not held by the client.`；未改变权限或跳过条件。

### 源码与测试规模

最新清单为310项源码、4文档、6依赖；源码原始字节双树一致。原2439项换行对照保留，另列3个定价blob的授权字节对照，不能声称全部文件原字节一致。源码集合摘要 `5eda13418ff8736f5cea6b88ddf49b30f50896a5b345f84d5714f52bfaea4328`，清单见 m6-final-source-manifest.json；302项输入清单未覆盖。

本批影响的三个包全部Go测试文件共27个/12713行，生产Go文件共20个/8808行，测试/生产比 1.443，低于2倍。它们是实际包范围，不以本批生产新增0行作分母。本批新增顶层测试0个，新增两个平台辅助文件；只改既有行为用例和输入。

| 本批测试文件 | 原行数→当前行数 | 超出300行 | 作用/验收 |
|---|---|---|
| `backend/internal/observe/usage/continuation_integration_test.go` | 77→77 | 0 | Q6：续接发现、旧/新归属与幂等计量；既有超限不拆矩阵，新文件仅平台辅助。 |
| `backend/internal/observe/usage/ingestor_integration_test.go` | 1272→1279 | 979 | Q6/Q9：文件身份、同一描述符读取与拒绝错误来源；既有超限不拆矩阵，新文件仅平台辅助。 |
| `backend/internal/observe/usage/legacy_repair_test.go` | 757→758 | 458 | Q6/Q9：文件身份、同一描述符读取与拒绝错误来源；既有超限不拆矩阵，新文件仅平台辅助。 |
| `backend/internal/observe/usage/transcript_replace_unix_test.go` | 0→15 | 0 | Q6/Q9：文件身份、同一描述符读取与拒绝错误来源；既有超限不拆矩阵，新文件仅平台辅助。 |
| `backend/internal/observe/usage/transcript_replace_windows_test.go` | 0→45 | 0 | Q6/Q9：文件身份、同一描述符读取与拒绝错误来源；既有超限不拆矩阵，新文件仅平台辅助。 |
| `backend/internal/service/project/service_test.go` | 2565→2579 | 2279 | Q9：Clone、取消/恢复、用户工作保全及SQLite清理；既有超限不拆矩阵，新文件仅平台辅助。 |
| `backend/internal/service/usage/collector_continuation_test.go` | 225→234 | 0 | Q6：续接发现、旧/新归属与幂等计量；既有超限不拆矩阵，新文件仅平台辅助。 |
| `backend/internal/service/usage/collector_test.go` | 2270→2271 | 1971 | Q6：Windows默认roots与显式目录覆盖；既有超限不拆矩阵，新文件仅平台辅助。 |

### 保留事项

本批的Windows回归结果不覆盖完整lint、完整存储race超时、SV-M6-06前三轮失败，也不替代真实Provider、桌面Queue、Hello、安装升级、打包TLS、symlink权限和其他OS验收。旧Claude配置8条记录未清理。未修改统筹计划/验收文档；M6是否验收及是否提交仍由统筹决定。

## M6 lint 第三批：55项 Windows 平台诊断（2026-11-08）

本批13个包的分组 lint 已通过，55项原诊断逐项处置：{'已修复': 36, '已审计并局部说明': 18, '已修复并审计': 1}。这只关闭本批已观测诊断，不宣布 M6 或全仓门禁通过。无提交、Push、部署或下一阶段。

输入冻结为 m6-lint-b3-input-source-manifest.json（310源码，集合摘要 5eda13418ff8736f5cea6b88ddf49b30f50896a5b345f84d5714f52bfaea4328），原 b2-summary 原字节另存输入副本，未改写。显式 UTF-8 读取中文正常、U+FFFD=0。预检脚本首次因 Windows 路径键分隔符 KeyError 退出1；修正脚本键后退出0，第一次未写任何冻结输入或源码。

### 改动与调用边界

- 外部 PID：processalive.Alive → CLI/runfile/持久宿主；ptyregistry.defaultPidAlive → 登记表扫描；previewProcessStartTime → 启动身份/树终止。三处入口在 DWORD 转换前拒绝越界，各加一条真实自身 PID 高位别名回归。AccessDenied 和未知结果原语义保留。
- 系统 PID：persistenthost 在成功 cmd.Start 后转换一次，以 uint32 传递给线程恢复；procmem 直接使用 Toolhelp32 DWORD。Job Object 缓冲显式 Pinner 固定到同步调用结束，已有两个子孙回收场景验证。
- Codex 账户：account factory/secure-file 调用链只查询当前进程 SID，改为 TOKEN_QUERY 伪令牌；不 Close 伪令牌。所有者、reparse/hardlink、DACL、身份漂移拒绝逻辑保留；关闭清理不能伪装成副作用未发生。测试输入均为 t.TempDir 和假凭据，登录终端仅用 fake。
- unsafe 逐点核对 ACL/ACE/SID 布局与 Go 可达性、PROCESS_MEMORY_COUNTERS/MEMORYSTATUSEX/FILETIME 结构及同步调用生命周期。多返回值保留六项安全契约，局部说明 tooManyResults。
- runfile 源码实际是 MoveFileExW（任务范围表称 ReplaceFileW），本批保留实际原子发布实现和错误；session 路径解析直接使用已有 uint32 容量，未改变长路径/UNC/身份判断。
- 两处 unused 均有 darwin/linux 调用者，保留函数并说明 Windows 编译范围；systemexec 仅构造已有三项 Path 切片，browserstream 仅补导出说明。没有配置、依赖或公开API/生成物变更。

55项原 id/path/linter/message、处置和证据：`C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-lint-b3-findings.json` 与同名 `.md`。源码增量为21文件（15生产、6测试，其中2个新测试文件）；m6-lint-b3-source-changes.json 保存修改前/最终哈希。

### 实际验证

下列均由原 run.py 在 C盘隔离 backend 执行：GOMAXPROCS=2、-p 2/-parallel 2、lint concurrency2、8GiB空间门禁；单会话串行，无真实 Provider 凭据继承。每条及跨命令的指定配置 SHA/mtime 均一致，config.toml保持 ae0378…。之前时段配置漂移未归因记录保留。

`m6-lint-b3-core-race`

```text
go test -race -p 2 -parallel 2 ./internal/processalive ./internal/adapters/runtime/conpty/ptyregistry ./internal/procmem ./internal/runfile -count=1 -timeout=5m -json
```
实际 exit 0 / 14.145秒；测试事件 {'pass': 52}，顶层 {'pass': 52}。

`m6-lint-b3-security-race`

```text
go test -race -p 2 -parallel 2 ./internal/service/agent ./internal/adapters/chatdriver/codexappserver ./internal/service/session ./internal/adapters/chatdriver/persistenthost -run ^(TestCodexWindows|TestOpenCodexDeviceFile|TestWindows(Vault|Ancestor|NoFollow|PathMetadata|DeviceCredential)|TestPrepareLoginTerminalForCommit|TestManagedHome|TestAccountFactory|TestResolveWorkspacePath|TestWorkspaceFileRevisionLiveReadResolvesRegisteredChildRoot|TestWindowsProvider|TestConfigureProviderProcess|TestWindowsHostDescriptor|TestReadDescriptorRetriesSharingViolation|TestExtendedLengthPath) -count=1 -timeout=5m -json
```
实际 exit 0 / 32.26秒；测试事件 {'pass': 101, 'skip': 2}，顶层 {'pass': 33, 'skip': 2}。

`m6-lint-b3-preview-race`

```text
go test -race -p 2 -parallel 2 ./internal/previewserver -count=1 -timeout=5m -json
```
实际 exit 0 / 42.13秒；测试事件 {'pass': 14}，顶层 {'pass': 14}。

`m6-lint-b3-platform`

```text
golangci-lint-2.13.2 run --timeout=10m --concurrency=2 ./internal/processalive ./internal/adapters/runtime/conpty/ptyregistry ./internal/adapters/chatdriver/persistenthost ./internal/previewserver ./internal/procmem ./internal/runfile ./internal/adapters/systemexec ./internal/browserstream ./internal/adapters/chatdriver/unrealagent ./internal/browserchrome
```
实际 exit 0 / 32.332秒；`0 issues.`。

`m6-lint-b3-security`

```text
golangci-lint-2.13.2 run --timeout=10m --concurrency=2 ./internal/service/agent ./internal/adapters/chatdriver/codexappserver ./internal/service/session ./internal/adapters/systemexec
```
实际 exit 0 / 74.743秒；`0 issues.`。

`m6-lint-b3-job-race`

```text
go test -race -p 2 -parallel 2 ./internal/adapters/chatdriver/persistenthost -run ^TestWindowsProvider(JobReapsGrandchild|Bridge|Grandchild)$ -count=1 -timeout=3m -json
```
实际 exit 0 / 10.388秒；测试事件 {'pass': 5}，顶层 {'pass': 3}。

`m6-lint-b3-job`

```text
golangci-lint-2.13.2 run --timeout=10m --concurrency=2 ./internal/adapters/chatdriver/persistenthost
```
实际 exit 0 / 21.581秒；`0 issues.`。

4条测试命令共 {'pass': 172, 'skip': 2}；按包+测试名去重为 {'pass': 167, 'skip': 2}，去重顶层 {'pass': 99, 'skip': 2}。事件含子测试及辅助子进程入口，不冒充独立业务场景数。Job缓冲固定后的末次定向复验覆盖两个实际回收子场景。

完整包通过仅为 processalive、ptyregistry、procmem、runfile、previewserver 五包；service/agent、codexappserver、service/session、persistenthost 为明确受影响范围定向通过。其余四包仅 lint/编译验证，未运行 browserchrome 的 Setup 注册表副作用路径。

跳过（原条件未改）：
- `TestResolveWorkspacePathFollowsSymlinksBeforeAcceptingContainment`：创建 symlink 返回 `A required privilege is not held by the client.`。
- `TestResolveWorkspacePathAppliesParentAfterFollowingSymlink`：创建 symlink 返回 `A required privilege is not held by the client.`。

### 规模与源码身份

13包全平台 Go 测试 39483 行，生产 37961 行，比值 1.04；各包均不超过2倍。新增3条顶层用例，本批6个测试文件均不超过300行；没有新增测试脚手架，C盘编辑/报告脚本均不超过400行。

| 本批测试文件 | 输入→最终行数 | 超出300行 | 验收 |
|---|---|---|---|
| `backend/internal/adapters/chatdriver/codexappserver/account_home_windows_test.go` | 202→196 | 0 | PID越界/ACL凭据/Job回收边界；既有用例保留 |
| `backend/internal/service/agent/codex_secure_fs_windows_test.go` | 284→281 | 0 | PID越界/ACL凭据/Job回收边界；既有用例保留 |
| `backend/internal/adapters/chatdriver/persistenthost/child_windows_test.go` | 298→298 | 0 | PID越界/ACL凭据/Job回收边界；既有用例保留 |
| `backend/internal/processalive/pid_range_windows_test.go` | 0→24 | 0 | PID越界/ACL凭据/Job回收边界；既有用例保留 |
| `backend/internal/adapters/runtime/conpty/ptyregistry/pid_range_windows_test.go` | 0→24 | 0 | PID越界/ACL凭据/Job回收边界；既有用例保留 |
| `backend/internal/previewserver/process_windows_test.go` | 45→61 | 0 | PID越界/ACL凭据/Job回收边界；既有用例保留 |

包级明细及辅助脚本规模见 m6-lint-b3-test-scale.json。既有未改大型测试文件未因本批拆分或删除，其历史超限申报保持。

最终源码331项、4文档、6依赖；集合摘要 `3498ab07e29607b6707d2d740b55ab8bcdd5d2ede0b8396c045d2ee9f9dfec8a`。源码原始字节双树一致；2439项历史换行对照中2054项原字节不同但CRLF→LF后一致（本批19个原文件按编辑后的字节同步）；3个定价blob授权对照仍保留，不能宣称整个checkout原字节一致。最终清单/进程/配置/差异审计及哈希见 m6-lint-b3-handoff.json。

### 未关闭的完整门禁与风险

最近完整 lint 仍是 m6-lint-full-closeout：exit4、763.125秒、252项并超时；55项此次已分组关闭，未再次启动全仓lint。旧完整后端失败保留；service/agent 的12条失败事件包含mode、symlink及原生登录/retryable和凭据打开失败，不将其全部归咎权限或宣布已修复。service/session三条为CRLF、权限模式和symlink；persistenthost一条journal POSIX mode；codexappserver旧112条事件主要为/tmp/ws绝对路径假设。原事件全文在 m6-lint-b3-historical-failures.json，均非本批新执行结果。

存储完整race超时、完整前端/打包TLS等既有失败保留。SV-M6-06已三次失败，本批未修改或复跑父conpty交互套件；ptyregistry独立PID测试不等于分离宿主验收。定价主仓CRLF、真实Provider/计费、Hello/桌面Queue、安装升级、其他OS/远端CI未验；symlink权限未改变。旧 .claude.json 的8条记录未授权清理，保持原状。统筹文档未改，由统筹决定下一工作包和M6状态。


## Windows 私有目录与账号安全回归（2026-11-08）
本批原18项目标为16 PASS、2 FAIL（既有 symlink 权限不足）；M6整体仍不通过。所有实跑均经C盘隔离runner，单会话、无子Agent、高I/O串行、GOMAXPROCS=2、go -p 2 -parallel 2、lint concurrency=2，8GiB门禁持续启用。未提交、Push或修改统筹文档。
输入源码摘要 `3498ab07e29607b6707d2d740b55ab8bcdd5d2ede0b8396c045d2ee9f9dfec8a`，冻结清单 SHA `6379100ddb36ac738d2ef0b9b9df3609f3b6977650ffe725ecb1c41101954985`；目标、计划、双树原文件备份见 m6-ws-input-* 与 ws-before。最终源码/文档身份和进程审计由 m6-ws-source-manifest.json、m6-ws-handoff.json 给出。
### 根因与最终边界
- Reviewer原目录与manifest仅有Unix mode：真实测试父目录的Everyone读取ACE被继承。现创建时带私有DACL，逐级父句柄拒绝reparse，复用时拒绝非当前所有者和硬链接，保护当前对象后才读取/写入。父reviewer-runtime也纳入host-trusted路径。内容寻址、重复准备及改变授权新建manifest保持；文件身份比较改用调用前后句柄取得的卷号/文件ID，避免Windows os.Stat延迟身份假阳性。
- Reviewer首版使用SetSecurityInfo时，硬链接拒绝前已传播ACL并改了外部目标；该真实失败保留。改为SetKernelObjectSecurity只设已验证句柄后，外部目标内容及SDDL均保全。实际junction、创建前私密性、旧manifest修复和身份保全均通过。
- 模型发现从MkdirAll切换到现有ensurePrivateDirectory；追加审查通过真实modelDiscoveryRequest证明共享目录SetSecurityInfo同样会改外部硬链接目标。红测删除了外部目标的Everyone读取ACE，字节未变；目录分支首轮修复后字节和SDDL均不变。仅目录用SetKernelObjectSecurity，私有文件与device credential文件策略未改；账号所有者/授权/继承保护的事后验证继续执行。未知既有子项不递归改权，凭据文件仍逐文件验证。
- 账号槽位真实ACL原已合格，旧mode断言改为平台断言。成功登录fixture在外部设备修改完成后才模拟子进程退出，等待自动验证与手工验证的共同结果；重复关闭按真实shellterm返回NotFound，closed仅记实际关闭。原Windows prepareLoginTerminalForCommit接受NotFound的用例补强，生产登录状态机未改。globalCredential读取用device策略，saved/pending继续private策略并验证原始假凭据字节。
- 不可读工作区fixture实际以DACL拒绝FILE_LIST_DIRECTORY，日志证明ReadDir返回Access is denied；生产原先Stat成功便放行，现只试读一个条目后返回既定SESSION_WORKSPACE_UNREADABLE，空目录EOF仍成功。
- 默认AO状态测试补USERPROFILE。另证实自定义AO_DATA_DIR原未防护：daemon将配置层已经绝对化的cfg.DataDir传入内部Deps，Validate和每个Git动作前共用拒绝边界；状态根、后代及会包含状态的祖先均拒绝，相邻同名前缀项目允许。没有新增配置、依赖、schema或HTTP/API字段。
### 原18项目标逐项结果
| 测试 | 最终结果与证据 | 归因 |
|---|---|---|
| `TestPrepareEnvironmentUsesPrivateAOOwnedDirectoriesAndImmutableManifest` | PASS；m6-ws-path-race | 产品 ACL 缺口；创建与复用逐对象保护、授权内容与身份保留 |
| `TestPrepareHostTrustedEnvironmentUsesPrivateAOOwnedDirectories` | PASS；m6-ws-path-race | 产品 ACL 缺口；创建与复用逐对象保护、授权内容与身份保留 |
| `TestGlobalModelDiscoveryUsesAODirectoryWithoutProject` | PASS；m6-ws-agent-race-full | 产品 ACL 缺口；复用账号私有目录校验并修正目录 ACL 传播 |
| `TestCodexBootstrapPermanentSafetyFailure` | FAIL；m6-ws-agent-race-full | OS 权限阻断；保留 FAIL，未修改策略或新增 skip |
| `TestCodexAccountCatalogCommitsStrictPrivateOpaqueSlot` | PASS；m6-ws-agent-race-full | Unix mode 假设；实际所有者及受保护 DACL 原已合格 |
| `TestNativeLoginCreatesAndActivatesFirstAccountWithoutProviderCheck` | PASS；m6-ws-agent-race-full | 假子进程退出时序/自动验证竞争；设备读取按 device policy，生产登录状态机未改 |
| `TestNativeLoginVerificationDoesNotReplaceExistingDeviceAccount` | PASS；m6-ws-agent-race-full | 假子进程退出时序/自动验证竞争；设备读取按 device policy，生产登录状态机未改 |
| `TestNativeLoginVerificationSavesAccountWhileDeviceReconciliationRetries` | PASS；m6-ws-agent-race-full | 假子进程退出时序/自动验证竞争；设备读取按 device policy，生产登录状态机未改 |
| `TestNativeReauthenticationReplacesTheExistingAccountSlot` | PASS；m6-ws-agent-race-full | 假子进程退出时序/自动验证竞争；设备读取按 device policy，生产登录状态机未改 |
| `TestActiveReauthenticationRevalidatesAndReplacesTheDeviceCredential` | PASS；m6-ws-agent-race-full | 假子进程退出时序/自动验证竞争；设备读取按 device policy，生产登录状态机未改 |
| `TestActiveReauthenticationPreservesCredentialsWhenDeviceChangesAfterTerminalOpens` | PASS；m6-ws-agent-race-full | 假子进程退出时序/自动验证竞争；设备读取按 device policy，生产登录状态机未改 |
| `TestActiveReauthenticationCommitsWhenProviderIsUnavailable` | PASS；m6-ws-agent-race-full | 假子进程退出时序/自动验证竞争；设备读取按 device policy，生产登录状态机未改 |
| `TestLogoutActiveAPIKeyRejectsExternalCredentialReplacement` | PASS；m6-ws-agent-race-full | 测试误用 private reader；按真实 device policy 验证外部字节保全 |
| `TestLoginDeduplicatesExistingAccountByProviderID` | PASS；m6-ws-agent-race-full | 假子进程退出时序/自动验证竞争；设备读取按 device policy，生产登录状态机未改 |
| `TestValidateProjectImportRejectsAOStatePath` | PASS；m6-ws-path-race | HOME/USERPROFILE fixture；另补自定义 DataDir 产品拒绝边界 |
| `TestPRCheckoutByVerifiedSHAWithFETCHHEADBranch` | PASS；m6-ws-services-full | 新隔离首轮即 PASS；保留已验证 SHA 和原字节断言，源码未改 |
| `TestWorkspaceLocationReportsUnreadableWorkspace` | PASS；m6-ws-path-race | 真实拒绝读取 fixture + 产品仅 Stat 的缺口；现实际试读目录 |
| `TestWorkspaceFileRevisionLiveReadAvoidsGitAndChecksRevision` | FAIL；m6-ws-services-full | OS 权限阻断；保留 FAIL，未修改策略或新增 skip |

### 实跑命令与退出码
所有命令通过 `rtk proxy python -X utf8 C:/Users/11459/AppData/Local/Temp/ao-m6-safe/run.py <label> backend <argv>` 执行；以下展开argv。完整metadata/log/exit及逐项失败输出在同名证据文件，汇总m6-ws-results.json。

`m6-ws-reproduce`

```text
go test -p 2 -parallel 2 ./internal/reviewgateway ./internal/service/agent ./internal/service/session ./internal/service/importer -run ^(TestPrepareEnvironmentUsesPrivateAOOwnedDirectoriesAndImmutableManifest|TestPrepareHostTrustedEnvironmentUsesPrivateAOOwnedDirectories|TestGlobalModelDiscoveryUsesAODirectoryWithoutProject|TestCodexAccountCatalogCommitsStrictPrivateOpaqueSlot|TestNativeLoginCreatesAndActivatesFirstAccountWithoutProviderCheck|TestNativeLoginVerificationDoesNotReplaceExistingDeviceAccount|TestNativeLoginVerificationSavesAccountWhileDeviceReconciliationRetries|TestNativeReauthenticationReplacesTheExistingAccountSlot|TestActiveReauthenticationRevalidatesAndReplacesTheDeviceCredential|TestActiveReauthenticationPreservesCredentialsWhenDeviceChangesAfterTerminalOpens|TestActiveReauthenticationCommitsWhenProviderIsUnavailable|TestLogoutActiveAPIKeyRejectsExternalCredentialReplacement|TestLoginDeduplicatesExistingAccountByProviderID|TestValidateProjectImportRejectsAOStatePath|TestPRCheckoutByVerifiedSHAWithFETCHHEADBranch|TestWorkspaceLocationReportsUnreadableWorkspace)$ -count=1 -timeout=5m -json
```
exit 1 / 10.938秒；测试事件 {'fail': 15, 'pass': 1}，顶层 {'fail': 15, 'pass': 1}。

`m6-ws-model-acl-red`

```text
go test -p 2 -parallel 2 ./internal/service/agent -run ^TestModelDiscoveryPreservesExternalHardLinkACL$ -count=1 -timeout=3m -json
```
exit 1 / 7.55秒；测试事件 {'fail': 1}，顶层 {'fail': 1}。

`m6-ws-model-acl-fix1`

```text
go test -p 2 -parallel 2 ./internal/service/agent -run ^(TestModelDiscoveryPreservesExternalHardLinkACL|TestGlobalModelDiscoveryUsesAODirectoryWithoutProject|TestCodexAccountCatalogCommitsStrictPrivateOpaqueSlot|TestCodexWindows.*|TestPrepareLoginTerminalForCommit.*)$ -count=1 -timeout=3m -json
```
exit 0 / 7.964秒；测试事件 {'pass': 16}，顶层 {'pass': 7}。

`m6-ws-services-full`

```text
go test -p 2 -parallel 2 ./internal/service/agent ./internal/service/session ./internal/service/importer ./internal/adapters/reviewer/pi ./internal/adapters/reviewer/agy -count=1 -timeout=5m -json
```
exit 1 / 297.026秒；测试事件 {'pass': 1161, 'fail': 2, 'skip': 11}，顶层 {'pass': 704, 'fail': 2, 'skip': 10}。

`m6-ws-agent-race-full`

```text
go test -race -p 2 -parallel 2 ./internal/service/agent -count=1 -timeout=5m -json
```
exit 1 / 54.948秒；测试事件 {'pass': 354, 'fail': 1, 'skip': 2}，顶层 {'pass': 242, 'fail': 1, 'skip': 1}。

`m6-ws-path-race`

```text
go test -race -p 2 -parallel 2 ./internal/reviewgateway ./internal/service/session ./internal/service/importer -run ^(TestPrepare(Environment|HostTrustedEnvironment).*|TestReviewer.*|TestWorkspaceLocation.*|TestArtifactLocation.*|TestValidateProjectImportRejectsAOStatePath|TestImportRejectsConfiguredStateDirectory)$ -count=1 -timeout=5m -json
```
exit 0 / 23.905秒；测试事件 {'pass': 24}，顶层 {'pass': 19}。

`m6-ws-lint`

```text
golangci-lint-2.13.2 run --timeout=10m --concurrency=2 ./internal/reviewgateway ./internal/service/agent ./internal/service/session ./internal/service/importer ./internal/daemon ./internal/adapters/reviewer/pi ./internal/adapters/reviewer/agy
```
exit 0 / 89.776秒；测试事件 {}，顶层 {}。`0 issues.`

`m6-ws-lint-final`

```text
golangci-lint-2.13.2 run --timeout=10m --concurrency=2 ./internal/reviewgateway ./internal/service/agent
```
exit 0 / 35.12秒；测试事件 {}，顶层 {}。`0 issues.`

完整services检查1161 PASS、2 FAIL、11 SKIP，没有超时；其中agent/session分别仅保留一项symlink权限失败，importer、pi、agy完整包通过。追加目录修复后的agent完整race仍保留原symlink失败，没有报告DATA RACE；不能将这条命令标记exit0。最终path-race覆盖reviewgateway全部现有用例、session定位与importer拒绝边界。daemon仅lint/编译验证，本批未启动真实daemon/桌面验收。
以services-full、agent-race-full、path-race按包+测试名保留最后结果，去重为 {'pass': 1175, 'fail': 2, 'skip': 11}；含子测试，不能当作独立业务场景数。新增顶层用例6条，没有新增skip；全部跳过原因与失败全文保存在m6-ws-results.json。
中间失败原样保留：boundary-red有三个包编译失败（ACL参数类型和残留os import）、boundary-red2尚有agent残留import；这些不计为运行时安全证据。agent-red揭示过早退出fixture的自动验证竞争；agent-reviewer-green中硬链接保全失败后才修正目录ACL传播。追加model-acl-red→model-acl-fix1为一次生产修复即转绿，两个源码快照单独冻结。没有触及三次失败停手的SV-M6-06。
### 测试规模与输入保全
本批24个源码文件（8生产、16测试）；新增测试文件均≤300行，辅助脚本均≤400行。包级测试/生产行数均≤2倍，详见m6-ws-test-scale.json。既有大型测试仅最小修改，不为满足行数拆散历史用例，以下逐项申报：

| 测试文件 | 输入→最终行数 | 超出300行 | 原因与验收 |
|---|---|---|---|
| `backend/internal/service/agent/catalog_test.go` | 2848→2841 | 2541 | 既有大文件；仅替换权限/设备读取/退出fixture或用户目录输入，对应原目标及完整包回归 |
| `backend/internal/service/agent/codex_account_catalog_test.go` | 372→366 | 66 | 既有大文件；仅替换权限/设备读取/退出fixture或用户目录输入，对应原目标及完整包回归 |
| `backend/internal/service/agent/codex_accounts_test.go` | 2513→2520 | 2220 | 既有大文件；仅替换权限/设备读取/退出fixture或用户目录输入，对应原目标及完整包回归 |
| `backend/internal/service/importer/importer_test.go` | 1189→1190 | 890 | 既有大文件；仅替换权限/设备读取/退出fixture或用户目录输入，对应原目标及完整包回归 |

所有测试使用假凭据。逐命令与整批真实配置SHA/mtime均与预检一致；只监测身份，没有读取配置内容用于测试或回写。ACL只更改本用例新建的scratch对象，并在清理前通过保留句柄恢复；新增junction仅位于scratch且已清理。历史2439换行对照及3个定价blob对照延续，不能声称全checkout原字节一致。最终差异、遗漏、源码摘要和剩余进程证据见handoff。
### 仍未关闭
M6整体仍不通过：两项symlink真实FAIL及原条件SKIP保留；全仓lint最近仍是m6-lint-full-closeout exit4/252项/超时，未全仓重跑。本批局部0 issues不能覆盖该结果。存储完整race超时、完整前端与打包TLS、定价主仓CRLF问题保持原状态。SV-M6-06未修改或复跑；真实Provider、Hello/桌面Queue、安装升级、其他OS和远端CI未验。旧真实.claude.json的8条记录未获清理授权，保持原状。

收尾首轮交接审计 exit1：计划当前字节与冻结输入不同。实际差异为统筹追加本轮进度及已在用户消息中下发的共享目录 ACL 审查要求；执行未修改该统筹计划。冻结计划 SHA 4f7e8b981d81d9827d3f22c925ed89bff72c8b848b4841659b2eab78a4ea8a95，当前 SHA 6f14112919c6ed7e1220abe1980ec06fc302e9e2481bf32a45cd7c4e9ba8aaa4；原快照保留，另存 m6-ws-plan-current.md 与 m6-ws-plan-drift.json，不把输入计划变化写成未变化。源码范围检查仍严格匹配24项，依赖及真实配置未漂移。

## SV-M6-07 进程观察失败与恢复（2026-11-08）

本批修复与限定自动化验证已交接，待统筹独立验收；M6整体仍不通过，未提交、Push、Merge或发布。唯一执行会话、无子Agent，高I/O串行，沿用C盘副本、隔离runner及8GiB门禁；没有运行SV-M6-06分离ConPTY交互或browserchrome Setup/Remove。

### 根因、调用闭环与真实恢复

- Windows旧共享观察把非AccessDenied的OpenProcess错误和Wait错误压成false。`m6-ro-boundary-red` 的3条业务用例实测锁/描述符/run-file/注册文件丢失、Prepare及假Spawner再次被调用；没有实际启动第二个Provider。红测前只加入两处未导出系统调用绑定，并将重复Windows registry接到仍保留原错误分类的共享Alive；具体原字节在同名source清单，不将其说成完全未改动基线。
- 新增`processalive.Probe(pid) (bool,error)`：非法/越界PID、有效非零PID的ERROR_INVALID_PARAMETER、WAIT_OBJECT_0为确认死亡；WAIT_TIMEOUT及既有AccessDenied存在性语义保留；其余OpenProcess错误、Wait错误及未知wait值返回错误。`Alive`保留布尔签名，但只用于保守保留占位，错误返回true并不表示已证实存活。Unix Kill/ps的原有判断未改；仅适配新增观察接口和registry返回签名。
- `acquireHostLock`、`ConnectOrStart`、`Reconcile`、`Shutdown`和provider退出轮询经共享Alive保留未知所有权；描述符协议、指纹和令牌校验不变。内存计量`HostPID`改用Probe，未知时返回`0,false`，不修改宿主状态。fixture同时验证正常存活PID、未知不计量、真实退出后锁和描述符可回收。
- 单纯保留registry条目仍不完整：`m6-ro-registry-refused-red`实测完整扫描后，下游在端口拒连时把未知宿主/子进程报告为`false,nil`。现在`Scan`遇到任一PID查询错误返回原列表、`complete=false`及错误，不重写文件；`resolveWithEvidence`、IsAlive/IsChildAlive/Fenced和Create沿用原错误传播，不能据此判死或竞争启动。此时同一registry的注册/注销也会闭锁，直到下一次观察成功；这是保留完整证据边界的代价。
- `runfile.CheckStale`改用Probe：未知返回错误，daemon的现有错误分支在绑定/覆盖前停止。`ao start`当前仅负责打开桌面应用，真正daemon启动门禁是此处，不是旧版CLI的本地spawn。
- `cli/root.go -> Deps.ProcessAlive -> client/status/stop`保留现有接线：未知不会跳过实际HTTP调用或标成stale；PID/服务名均通过健康核验后才报告ready，失败保留unhealthy及原run-file。未核实所有权的stop和ConPTY teardown错误去掉“已确认存活”措辞；停止已获应答但PID仍未知且run-file仍在时不能报告stopped。既有run-file由daemon自行删除的停止承诺语义、并发替代PID保护和明确死亡清理均保留。
- 故障仅施加于C盘fixture子进程，未耗尽真实资源；恢复先关闭其stdin并Wait，再撤销注入，且保留一个只读进程句柄，实际验证WAIT_OBJECT_0路径。未知源分别覆盖OpenProcess和Wait两个丢失信息的位置；AccessDenied保留是静态分支核对，不宣称真实系统拒权实验。跨模块测试只在测试构建导出替换入口，未新增通用故障框架或生产配置/API字段。

### 实跑证据

以下均用`rtk proxy python -X utf8 C:/Users/11459/AppData/Local/Temp/ao-m6-safe/ro-run.py <label> backend <argv>`；ro-run继续调用原run.py，仅增加指定HKCU键的前后只读身份核对。完整argv、时间、配置身份和原始日志见同名`.json/.log/.exit`及`m6-ro-results.json`。

| label | exit | 秒 | 测试事件 |
|---|---:|---:|---|
| `m6-ro-boundary-red` | 1 | 22.057 | {'fail': 3} |
| `m6-ro-fix1` | 0 | 23.341 | {'pass': 3} |
| `m6-ro-registry-red` | 1 | 9.95 | {'fail': 1} |
| `m6-ro-registry-refused-red` | 1 | 9.921 | {'fail': 1} |
| `m6-ro-registry-fix1` | 0 | 15.339 | {'pass': 33} |
| `m6-ro-ownership-race-full` | 0 | 110.005 | {'pass': 93} |
| `m6-ro-cli-conpty-race` | 1 | 31.121 | {'pass': 25, 'fail': 1} |
| `m6-ro-conpty-assertion-race` | 0 | 8.589 | {'pass': 1} |
| `m6-ro-lint` | 0 | 89.324 | 0 issues. |
| `m6-ro-unix-compile` | 0 | 17.39 | 仅交叉编译 |

最终四个完整包race为93 PASS，CLI/ConPTY定向首次25 PASS/1 FAIL，失败原因为旧断言仍要求`still alive`；实际错误是`exit was not confirmed after teardown`。同步该单条断言并保留access denied和占位保留断言后，精确race复验1 PASS。按包+测试名保留最后结果，共119 PASS/0 FAIL/0 SKIP，没有DATA RACE；这是含子测试和fixture入口的事件数，不能说成119个独立业务场景。中间exit1仍保留，不把该条原命令改写为exit0。新增业务回归3条，另1个受控子进程入口；没有新增skip。六包固定2.13.2 lint实际`0 issues.`。Linux/amd64仅交叉编译processalive和ptyregistry，未运行Linux测试。

关键命令如下，其余完整命令也保存在m6-ro-results.json：

`m6-ro-boundary-red`

```text
go test -p 2 -parallel 2 ./internal/processalive -run ^TestObservationFailure -count=1 -timeout=3m -json
```

`m6-ro-registry-refused-red`

```text
go test -p 2 -parallel 2 ./internal/processalive -run ^TestObservationFailurePreservesRegistry$ -count=1 -timeout=3m -json
```

`m6-ro-ownership-race-full`

```text
go test -race -p 2 -parallel 2 ./internal/processalive ./internal/runfile ./internal/adapters/runtime/conpty/ptyregistry ./internal/adapters/chatdriver/persistenthost -count=1 -timeout=5m -json
```

`m6-ro-cli-conpty-race`

```text
go test -race -p 2 -parallel 2 ./internal/cli ./internal/adapters/runtime/conpty -run ^(TestStatusStoppedJSON|TestStatusKeepsLiveProbeFailureUnhealthy|TestStopRemovesStaleRunFile|TestStopDoesNotShutdownUnverifiedReusedPID|TestStopUsesShutdownEndpoint|TestStopRefusesUnverifiedLivePID|TestWaitForStopped.*|TestVersionEmitsCLIInvocationBestEffort|TestAPIErrorString|TestProbeFencedRuntime.*|TestRegistryResolutionHonorsCallerCancellation|TestDestroyRetainsSessionWhenPIDCannotBeStopped|TestIsChildAlivePreserves.*)$ -count=1 -timeout=5m -json
```

`m6-ro-conpty-assertion-race`

```text
go test -race -p 2 -parallel 2 ./internal/adapters/runtime/conpty -run ^TestDestroyRetainsSessionWhenPIDCannotBeStopped$ -count=1 -timeout=3m -json
```

`m6-ro-lint`

```text
C:/Users/11459/AppData/Local/Temp/ao-m6-safe/golangci-2.13.2/golangci-lint-2.13.2-windows-amd64/golangci-lint.exe run --timeout=10m --concurrency=2 ./internal/processalive ./internal/runfile ./internal/adapters/runtime/conpty/ptyregistry ./internal/adapters/chatdriver/persistenthost ./internal/adapters/runtime/conpty ./internal/cli
```

`m6-ro-unix-compile`

```text
python -X utf8 C:/Users/11459/AppData/Local/Temp/ao-m6-safe/ro-unix-compile.py
```

### 源码、规模与隔离边界

输入353项源码摘要`8e1b120f1cb21be5cf3a1e81838acb293dddf6d57f9f72887eba92e342d354bb`，清单SHA`47301c41ddab56c367b95f024cd122cced7f6e25fb13ed34edd3e4458a0931fa`。本批实际修改17项源码（10生产/7测试）；最终清单366项，双树这些源码原字节一致，摘要`f96d699e492a3aa3ecac5e8f9eeb6d52b30f7369952dc8dfa62a1f0ea4dbeb69`。4执行文档、6依赖及源码身份由m6-ro-source-manifest.json和m6-ro-handoff.json冻结。额外2439项换行对照有2037项主/副本原字节不同，3个定价blob仍仅C盘LF对照；不声称整个checkout原字节一致。

新增测试文件131/37/144/90行，均不超过300。历史ptyregistry_test.go保持460行（超160），仅适配既有fake签名；runtime_test.go保持1169行（超869），仅同步停止文案断言。**规模例外申报：processalive包测试455行、生产102行，包内两倍上限超251行**；新增外部测试实际覆盖宿主、registry、runtime和CLI装配，集中在此以避免向业务包扩散生产故障注入接口。其余5个受影响包均未超两倍。没有用删除断言或压缩可读性掩盖超限；详情m6-ro-test-scale.json，供交接审查。所有本批C盘辅助脚本均小于400行。

三份指定真实配置的SHA/mtime在全部10条验证前后均与预检一致。指定HKCU键`Software\Google\Chrome\NativeMessagingHosts\dev.ao.browser_bridge`从本批首个snapshot起仅做只读身份核对，每条实跑前后均未变；CLI只运行审过的status/stop/version等用例，未运行Installer。旧browserchrome测试污染指向TestSetupWithoutChromeFails212292381 scratch manifest是统筹已确认的历史问题，不将HOME隔离说成HKCU隔离，本批未清理真实键。最终进程/空间审计及批次前后核对见m6-ro-handoff.json。

执行中统筹仅追加已实跑红测进度，与日志一致；原计划冻结SHA`83c3a33e0ca6425fb4e6f275914288b35d3c35298e6ca6f1e3139cad849c75f8`，当前SHA`235bbbcd62da35bc0248c4379965a2b69126cfb253cd05bfab0b3af9d5053227`，差异和当前只读副本在m6-ro-plan-drift.json及m6-ro-plan-current.md。执行未修改统筹计划/验收文档，没有因这项已知进度更新重复暂停。

按统筹本轮核实，m6-lint-b3-historical-failures.json中的codexappserver 112条及persistenthost journal 1条已全部匹配m6-profile-race-full.log的PASS，missing=[]。它们不是待返修批次；本批没有为这113条旧失败重跑codexappserver，persistenthost仅因当前观察变更纳入完整race。journal当时只是取消Unix mode断言，PASS不等于Windows实际ACL验收。本批不扩大ACL范围。

M6整体仍不通过：既有symlink权限失败、全仓lint exit4/252项/超时、存储完整race超时、完整前端与打包TLS等状态不变。SV-M6-06三次失败停手，未修改/复跑该交互用例；真实Provider、Hello/桌面Queue、安装升级、其他OS实际运行和远端CI未验。旧.claude.json八条记录和HKCU旧污染键均未授权清理；本批未清理。后续浏览器测试隔离任务由统筹另行下发，本批未进入。

文档收尾首轮四个no-index检查均exit3，输出new blank line at EOF；仅移除新增的末尾空行后复验，原输出保留m6-ro-documents-first-diff-check.json。该格式修正未改变源码或重跑业务套件。

## SV-M6-08 Chrome 注册测试隔离（2026-11-08）

执行范围为 `m6-browser-test-isolation-plan.md`。输入是已限定验收的 SV-M6-07：366 项源码摘要 `f96d699e492a3aa3ecac5e8f9eeb6d52b30f7369952dc8dfa62a1f0ea4dbeb69`。计划冻结 SHA 为 `363c0e21460ad2e8042aee291affddffc69cb86000bd71eb88fccb80d545230e`。沿用唯一执行会话、C 盘副本/依赖/隔离 runner 与 8GiB 空间门禁，全部验证串行；没有子 Agent，也未扫描、复制或清理 E 盘旧 `.tmp`。

### 根因与最小修复

`chrome_test.go` 的旧 `testInstaller` 仅隔离文件路径，没有注入 `registry`；Windows 的 `Installer.reg()` 因而回落到真实 HKCU。旧 `TestSetupRegistersInstalledChannelsOnly` 实际只得到一个注册表通道，`TestSetupWithoutChromeFails` 实际返回 nil。旧日志提取为 `m6-bi-input-historical-failures.json`；没有为了红测再次执行旧污染路径。

本批仅修改两个源码文件：

- `backend/internal/browserchrome/setup.go`：私有注册注入改为 `func() hostRegistry`。未提供函数继续使用平台默认实现；提供函数且返回 nil 明确选择目录注册；返回已有 fake 则使用内存注册表。Windows 注册实现和生产 daemon 构造入口均未改。
- `backend/internal/browserchrome/chrome_test.go`：helper 要求显式传入注册实现，六次构造调用全部闭合。三个目录测试传 nil，两个内存注册测试的三次构造传已有 fake；删除事后赋值。原 Setup/Status/Refresh/Remove、外部 manifest 保全、另一安装注册保全断言全部保留。没有新增测试、依赖、配置、框架或 skip。

本包其他测试入口已审查：host 测试使用临时 endpoint 和进程内本地 broker；peer 测试仅 darwin/linux 编译；没有 TestMain/init 注册副作用。真实 Chrome E2E 的三个显式环境开关不在 runner 继承白名单内，保持其既有 SKIP。

### 实跑证据

证据目录：`C:/Users/11459/AppData/Local/Temp/ao-m6-safe`。工作目录为 C 盘副本的 `backend`。下列命令均由 `rtk proxy python -X utf8 .../ro-run.py <label> backend <command>` 启动；`.json` 保存命令、时间、配置身份、磁盘与退出码，`.log` 保存原始输出，`.exit` 保存原码。

| label | command | exit | 秒 | 实际结果 |
|---|---|---:|---:|---|
| `m6-bi-package` | `go test -p 2 -parallel 2 ./internal/browserchrome -count=1 -timeout=3m -json` | 0 | 4.889 | 13 PASS / 1 SKIP，包 PASS |
| `m6-bi-package-race` | `go test -race -p 2 -parallel 2 ./internal/browserchrome -count=1 -timeout=3m -json` | 0 | 7.627 | 13 PASS / 1 SKIP，包 PASS，无 DATA RACE |
| `m6-bi-lint` | `C:/Users/11459/AppData/Local/Temp/ao-m6-safe/golangci-2.13.2/golangci-lint-2.13.2-windows-amd64/golangci-lint.exe run --timeout=10m --concurrency=2 ./internal/browserchrome` | 0 | 16.995 | `0 issues.` |

两个旧失败及三个其他 Installer 业务测试在完整包、完整 race 中均 PASS。两轮是同一组 13 个通过测试，不能合计成 26 个独立场景。唯一 SKIP 是 `TestChromeE2E`，原文为 `set AO_CHROME_E2E_ENDPOINT, AO_CHROME_E2E_HOST and AO_CHROME_E2E_CDP to run against real Chrome`；这不构成真实 Chrome 桥接验收。

源码冻结辅助检查最初误放进隔离 HOME：`m6-bi-source-freeze` exit 1，其内部 `git rev-parse HEAD` exit 128；`m6-bi-source-git-diagnosis` exit 128，实际错误为 `fatal: detected dubious ownership in repository at 'E:/code/agent-orchestrator'`。两次原始失败保留。未更改 global safe.directory、所有者或配置，改按冻结清单核对文件；`m6-bi-freeze-fixed` exit 0。产品测试始终在原隔离 runner 内运行。另一次格式化合并命令的 PowerShell 退出码判断发生转义错误，该次 shell exit 0 不作为验证成功依据；最终格式与静态检查以本包 lint 结果为准。

### 源码身份、宿主保全与剩余项

最终源码清单 367 项，摘要 `3a7591eea63d9b304455382b491b7e77359bc7d6b669fe84bcf10f2f0ed2f0f4`；比输入新增纳入已跟踪的 `chrome_test.go`，不是新增源码文件。367 项源码双树原字节一致，四执行文档与六依赖一致；原 2439 项换行对照及三个定价 blob 对照继续保留，不声称整个 checkout 原字节一致。测试前清单为 `m6-bi-tested-source-manifest.json`，最终清单为 `m6-bi-source-manifest.json`；两源码的本批独立差异、完整命令/失败、配置与注册表身份、串行时间、进程和空间见 `m6-bi-handoff.json` 及 `m6-bi-HANDOFF.md`。

修改后的 `chrome_test.go` 为 259 行，无单文件超限；本包测试 798 行、生产 1098 行，未超两倍。未新增业务测试或 fixture 矩阵，既有 SV-M6-07 规模例外不在本批重排。

每次实跑前后只读核对三份指定真实配置的 SHA/mtime，以及精确键 `HKCU\Software\Google\Chrome\NativeMessagingHosts\dev.ao.browser_bridge`。完整包、race、lint 均记录未变化，且与本批预检一致。旧键仍为 REG_SZ，默认值 `C:\ao-m6-tests-507820bdd394\TestSetupWithoutChromeFails212292381\001\data\browser-chrome\dev.ao.browser_bridge.json`，subkeys=0，modified=`134385530039501919`。未清理或改写此键；缺少可信污染前备份，不能声称恢复原注册。三份配置只取摘要/mtime，未输出内容或修改配置，旧 `.claude.json` 八条测试记录继续保留为未关闭事件。

SV-M6-08 只关闭测试再次落入真实注册表的路径，待统筹独立验收。未运行真实 Chrome setup/remove、真实浏览器交互、Provider/Agent/Hello 或真实 daemon；没有复跑 SV-M6-06、其他包或全仓 CI。M6 整体仍未通过，历史失败及真实环境验收缺口不变；未提交、Push、Merge 或发布。

## M6 剩余自动门禁收口（2026-11-08）

执行`m6-remaining-gates-plan.md`，输入严格使用`m6-bi-source-manifest.json`：367项摘要`3a7591eea63d9b304455382b491b7e77359bc7d6b669fe84bcf10f2f0ed2f0f4`，清单SHA`fae5b9d50dc5dd1537f2e424a57347301f04097bb3a3fd3178ea9498c70b94fc`。SV-M6-08已由统筹限定验收，前节“待验收”为历史时点。本批单会话，无子Agent、真实Provider/Agent、Chrome setup/remove、Hello或生产安装；未提交/Push/Merge/发布，M6整体仍未通过。

### 315事件与50包的精确对账

原315 FAIL为204顶层/111子事件，分布50包。读取92份原始Go日志，排除watch日志回显；按完整包名和完整测试名对齐，重复count的FAIL不会被同命令末尾PASS覆盖。最终关闭287项，保留28项：外部能力限制19项；自动工作待续6项；平台限制3项。原始PASS事件分布为`{'pass': 288, 'missing': 24, 'fail': 3}`，不能直接当作当前关闭数。

Codewhale只选择一个子测试，实际父级PASS属于部分执行，父测试仍未关闭。TaskNaming历史源码适用性不足，按统筹追加指令仅补整个顶层测试的race，当前实际结果见下表；不重跑完整Manager。其余历史PASS逐项绑定源码anchor；输入113个Codex/persistenthost已关闭目标未重复运行。Reviewer的agy/pi与Agent同名包严格区分。

原未终态`TestResumeScopesRequestsBeforeHandshakeCompletes`已关联`m6-profile-race-full.log:2491`的PASS；原命令和包FAIL不改写。50包最近完整包结果为`{'fail': 29, 'pass': 21}`，定向包PASS不替代完整包。存储完整race的两个15分钟超时和7个未终态继续保留；315之外的SV-M6-06三次交互失败停手，本批没有修改或复跑。

逐项见C盘`m6-gates-final-events.json`、`m6-gates-final-packages.json`、`m6-gates-final-unfinished.json`、`m6-gates-remaining-matrix.md`。矩阵分别列自动待续与具体平台/能力限制，不用“环境问题”归并。

### 修复及实跑

本批30源码文件：4生产（Go3、TS1）与26既有测试。HOME/USERPROFILE和JSON路径fixture、Windows假程序扩展名/临时路径、归一化进程快照均保持业务断言；没有新增测试名、skip、依赖或配置。OMP两个只读SQLite URI增加OmitHost，防止Windows盘符被解释为主机；仍保留转义与mode=ro。

telemetry的Go与Electron读取、创建、空文件恢复共用原目录锁；Windows短暂Mkdir拒绝在既有截止内重试，过期锁删除错误不吞，截止检查覆盖重试分支。两端都实跑各自并发用例，但未同时启动Go/Electron做跨语言并发实验。统筹追加完整telemetry包race时完整lint已启动，因此实际先结束lint再补包race，源码未变；未重复已有count=10或完整前端。

全部命令经C盘既有runner，测试并发和Go包并发均2。精确argv、时间、原输出、原退出码和身份见同名.json/.log/.exit；长检查另有每秒watch记录。下表PASS数是事件数，count=10不是10个独立场景。

| label | exit | 秒 | 原始结果 |
|---|---:|---:|---|
| `m6-gates-home-targets` | 0 | 42.384 | 26 PASS, 0 FAIL, 0 SKIP |
| `m6-gates-telemetry-before-race` | 1 | 15.131 | 0 PASS, 1 FAIL, 0 SKIP |
| `m6-gates-telemetry-fixed-race` | 1 | 9.108 | 9 PASS, 1 FAIL, 0 SKIP |
| `m6-gates-telemetry-lock-race` | 0 | 10.057 | 10 PASS, 0 FAIL, 0 SKIP |
| `m6-gates-telemetry-shared-race` | 0 | 9.598 | 10 PASS, 0 FAIL, 0 SKIP |
| `frontend-node24-m6-gates-telemetry` | 0 | 8.47 | 1文件18 PASS；仅既有MIXED_EXPORTS警告 |
| `m6-gates-omp-before-race` | 1 | 10.207 | 0 PASS, 2 FAIL, 0 SKIP |
| `m6-gates-catalog-race` | 0 | 10.344 | 11 PASS, 0 FAIL, 0 SKIP |
| `m6-gates-platform-race` | 0 | 21.796 | 13 PASS, 0 FAIL, 0 SKIP |
| `m6-gates-install-command-race` | 0 | 12.054 | 5 PASS, 0 FAIL, 0 SKIP |
| `m6-gates-final-fixtures-race` | 0 | 17.723 | 27 PASS, 0 FAIL, 0 SKIP |
| `m6-gates-codewhale-race` | 0 | 8.879 | 2 PASS, 0 FAIL, 0 SKIP |
| `frontend-node24-m6-gates-typecheck` | 0 | 71.08 | > agent-orchestrator@0.13.0 typecheck<br>> tsgo --noEmit |
| `m6-gates-lint-full` | 4 | 623.267 | 0 issues.<br>level=error msg="Timeout exceeded: try increasing it by passing --timeout option" |
| `m6-gates-telemetry-package-race` | 0 | 9.351 | 146 PASS, 0 FAIL, 0 SKIP |
| `m6-gates-task-naming-race` | 0 | 39.446 | 7 PASS, 0 FAIL, 0 SKIP |

完整lint使用固定2.13.2、`run --timeout=10m --concurrency=2 ./...`，exit 4 / 623.267秒 / 0 issues.
level=error msg="Timeout exceeded: try increasing it by passing --timeout option"。完整lint仍失败；本批只运行一次，不加超时或原样重跑。完整telemetry命令`go test -p 2 -parallel 2 -race ./internal/adapters/telemetry -count=1 -timeout 3m -json`，exit0，146 PASS, 0 FAIL, 0 SKIP。所有sink使用roundTripClient.Do假传输，无真实telemetry请求。

保留辅助失败：platform-edit首次锚点匹配断言exit1发生在任何源码写入前，收紧完整行锚点后执行成功；一次临时日志探查把多行JSON首行当完整JSON导致解析失败，未改写产物，最终账本使用逐行容错且校验315原FAIL集合。这些失败不计业务通过。

### 源码与规模例外

最终397项源码摘要`6349d3a0234c0836670788216af39db75793a7236c643b8c37e1dc18c0e2d139`，双树397项原字节相同；四文档、六依赖及2439换行对照/三个定价blob继续核对，不代表整个checkout原字节一致。本批独立diff与规模在`m6-gates-source.diff`、`m6-gates-test-scale.json`，最终身份与完整证据目录见`m6-gates-handoff.json`。

以下修改的是历史大测试文件，未为比例搬移用例。超限申报对应既有失败的fixture修复或telemetry并发回归；新测试名和skip均0。

| 测试文件 | 修改前行数 | 当前行数 | 超300行 |
|---|---:|---:|---:|
| `backend/internal/adapters/agent/agy/bridge_test.go` | 328 | 329 | 29 |
| `backend/internal/adapters/agent/claudecode/claudecode_test.go` | 1700 | 1703 | 1403 |
| `backend/internal/adapters/agent/commandcode/commandcode_test.go` | 392 | 397 | 97 |
| `backend/internal/adapters/agent/copilot/copilot_test.go` | 1221 | 1223 | 923 |
| `backend/internal/adapters/agent/fake/fake_test.go` | 400 | 400 | 100 |
| `backend/internal/adapters/agent/kilocode/kilocode_test.go` | 637 | 644 | 344 |
| `backend/internal/adapters/agent/kimchi/auth_test.go` | 307 | 308 | 8 |
| `backend/internal/adapters/agent/modelcatalog/catalog_test.go` | 1393 | 1398 | 1098 |
| `backend/internal/adapters/agent/modelcatalog/config_test.go` | 328 | 332 | 32 |
| `backend/internal/adapters/agent/registry/registry_test.go` | 346 | 352 | 52 |
| `backend/internal/adapters/chatdriver/agyacp/driver_test.go` | 352 | 358 | 58 |
| `backend/internal/adapters/runtime/tmux/tmux_test.go` | 2327 | 2343 | 2043 |
| `backend/internal/adapters/telemetry/identity_test.go` | 651 | 671 | 371 |
| `backend/internal/service/agentauth/service_test.go` | 545 | 559 | 259 |
| `backend/internal/service/systeminstall/agentplans_test.go` | 831 | 831 | 531 |
| `backend/internal/service/systeminstall/managed_official_test.go` | 504 | 505 | 205 |

受影响目录直接源码口径的两倍上限例外如下；不声称全仓比例合格。

| 目录 | 测试行数 | 生产行数 | 超两倍行数 |
|---|---:|---:|---:|
| `backend/internal/adapters/agent/registry` | 478 | 139 | 200 |
| `backend/internal/adapters/chatdriver/kimiacp` | 385 | 78 | 229 |

本批辅助脚本均≤400行。完整存储race、完整前端、打包TLS、真实浏览器/Provider/桌面与其他OS没有本批通过证据；不重复下载依赖、不扫描或清理E盘旧.tmp。宿主三配置和精确HKCU键前后及运行间隔核对、每秒空间样本、进程与执行时间重叠审计均纳入HANDOFF，且包含frontend-node24-m6-gates前缀。旧Claude八条记录和HKCU旧指针未清理。


### 收尾串行审计失败（2026-11-08）

首轮`gates-close.py`返回exit1：`AssertionError: [('frontend-node24-m6-gates-typecheck', 'm6-gates-source-freeze')]`。类型检查08:58:01.194327—08:59:12.274242 UTC，与只读源码摘要核对08:58:49.384030—08:58:53.885582 UTC重叠4.501552秒。因此不能声明全部验证/审计记录串行通过；该审计失败原样保留，不豁免或回写PASS。两条命令各自exit0，不消除调度审计失败。首轮源码核对、Git差异检查和进程清点均exit0。

交接补充保留此次失败与时间区间，并重新核对文档更新后的摘要；没有重跑业务测试或完整lint。更新后源码/文档身份见C盘`m6-gates-delivery-source-manifest.json`；旧`m6-gates-source-manifest.json`保留为文档订正前快照。交接文件生成成功仅表示证据已写出，M6、完整lint和本次串行审计均未通过。

另保留一次只读探查命令的PowerShell/Python引号错误：`SyntaxError: unterminated string literal (detected at line 1)`，exit1；发生在脚本解析阶段，无文件写入；改用字面here-string后读取exit0。


## 六项自动遗留与平台边界收口（2026-11-08）

本节为最新状态。输入为`m6-gates-delivery-source-manifest.json`的397项摘要`6349d3a0234c0836670788216af39db75793a7236c643b8c37e1dc18c0e2d139`，已核对双树源码、四文档、六依赖和前批221份证据SHA。未使用订正前的旧文档清单。统筹计划`m6-residual-fixtures-plan.md`只读，不修改计划或验收文件。

原315 FAIL事件现在关闭291、保留24。新增关闭Registry完整顶层组、Brew detached hub保全、KeptKeg迁移后一次清理、Kimi确认目录内固定版本安装计划四项。两个Kimi共享用例只从自动待续重分类为外部能力限制，原FAIL不关闭：剩余21项外部能力限制、3项平台限制，本批六项中自动待续0不代表M6通过。50包最新完整包仍21 PASS/29 FAIL，定向包PASS没有覆盖完整包失败。

### 契约与最小修改

Kimi `GetAgentHooks`及ACP `ShareCredentials`同走seedKimiCredentials。`hooks.go:185-245`明确符号链接失败后保留/更新副本，按mtime保护较新本地凭据。`kimi_test.go:564-565`及`700-709`要求真实链接和AO写入立即同步源端，不能改mtime或期望把复制算共享。已有复制新旧保护用例`TestSeedKimiCredentialReplacesAnOlderCopy`在原日志7323为PASS（完整命令exit1），生产和用例未改，不重复运行。两共享原FAIL在7309、7319；另有7300非reparse point及原symlink权限不足证据。不改权限、不加skip、不触真实凭据，不把mode0600当Windows ACL验证。

本批仅四个既有测试文件、生产0：Registry的Windows PATH限定假程序目录，避免优先.exe别名抢先命中用户程序；共享安装fixture用宿主临时Cellar，同源verifier/--cellar/进程快照，未知命令失败而非执行真实命令。Brew可变launcher使用另一临时bin路径，保留其引用保全。Kimi Darwin计划fixture去除Windows盘符并转正斜杠，仍断言确认目录、固定2.2.0、禁止改PATH及去除旧bin。全部12处共享fixture调用已审并纳入回归；安装器、版本核验、命令均fake，无真实安装或出站。

Registry 38个Harness逐一审查：工作区/DataDir/用户目录均隔离；仅OMP/Pi/Codewhale版本或配置探测执行临时假程序；Hooks文本写入不执行。详见C盘`m6-residual-contract-review.md`，以及完整顶层日志，不以单子项PASS替代父组。

### 实跑与证据

所有验证和源码冻结均等待上一条watch结束后串行。共两条race和一条两包lint，固定并发2；逐秒8GiB监测、三配置及精确HKCU键前后观察见`m6-residual-handoff.json`。精确命令、原退出码和结果见`m6-residual-product-results.json`，命令在既有C盘隔离runner执行。

| label | exit | 秒 | 实际输出 |
|---|---:|---:|---|
| `m6-residual-registry-race` | 0 | 10.505 | {'pass': 39} |
| `m6-residual-install-fixtures-race` | 0 | 13.397 | {'pass': 20} |
| `m6-residual-fixtures-lint` | 0 | 39.873 | 0 issues. |

Registry为1父+38子项39 PASS；安装组13顶层+7子项20 PASS。均0 FAIL/0 SKIP，无DATA RACE；不是59个独立顶层场景。两包lint固定2.13.2，`run --timeout=10m --concurrency=2 ./internal/adapters/agent/registry ./internal/service/systeminstall`，exit0、0 issues。没有完整包或全量测试的新通过声明。

前批完整lint仍exit4/623.267秒：`0 issues.`后`level=error msg="Timeout exceeded: try increasing it by passing --timeout option"`，本批未再运行；日志未提供阶段耗时，不能定位具体分析器。前批严格串行审计4.501552秒重叠仍FAIL，不能由本批无重叠覆盖。SV-M6-06三次停手，完整存储race15分钟超时、完整前端历史FAIL及打包TLS均保留。

最终源码399项摘要`7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590`，本批两份首次进入变更清单的测试文件使397增至399；四文件双树原字节相同。2439项换行登记保留，其中本批四行SHA变化，两份原CRLF/LF差异转为一致；依赖及三个定价blob登记不变。最终四文档SHA以`m6-residual-source-manifest.json`为准，独立diff见`m6-residual-source.diff`。

辅助失败保留：首次fixture-edit在update_gate_test.go原字节比较处exit1，Registry已写、其余三文件未写；发现属于已登记的主仓CRLF/副本LF，第二次先按原登记核对所有输入、验证已写Registry再继续。没有隐去部分写入或重跑前批检查。原脚本及双树前字节留档。

### 测试规模申报

新测试名/skip/依赖/配置0，没有搬测试。历史大文件只改既有fixture及必要说明，保留原断言；对应本批Registry完整组及三项安装验收。行数如下：

| 文件 | 修改前 | 修改后 | 超300行 |
|---|---:|---:|---:|
| `backend/internal/adapters/agent/registry/registry_test.go` | 352 | 356 | 56 |
| `backend/internal/service/systeminstall/update_gate_test.go` | 491 | 492 | 192 |
| `backend/internal/service/systeminstall/managed_processes_test.go` | 238 | 247 | 0 |
| `backend/internal/service/systeminstall/systeminstall_test.go` | 2068 | 2071 | 1771 |

registry目录测试482/生产139行，超两倍204行（历史478/139，原超200行）；systeminstall测试10203/生产8236行，未超两倍。前批其他规模例外原样保留。本批辅助脚本均≤400行，完整规模见`m6-residual-test-scale.json`及HANDOFF。

本批配置SHA/mtime及精确HKCU键未漂移，旧Claude八条记录和Chrome旧指针未清理。未提交/Push/Merge/发布；M6整体仍未通过。


## 完整门禁只读归因（2026-11-08）

四项完整门禁已按现有证据只读归因，M6仍FAIL：sqlite/store耗尽整包15分钟预算，活动测试仅18/51秒且在迁移SQL栈；五项t.Parallel等待不构成业务死锁证据。原完整前端50失败均有历史局部覆盖，但当前better_sqlite3.node导出node_register_module_v149（SHA86f68f08…），不能直接套用Node137下55 PASS。完整lint固定C盘缓存，exit4/623.267秒只有0 issues及Timeout，阶段未知。Forge prePackage/Vite成功后TLS失败；ZIP缓存仍需联网取SHASUMS，具体失败URL/代理/cache命中未知，暂无新修复条件。

建议由统筹依次决定：C盘Node137测试依赖阶段恢复和导入单文件检查；两条存储活动单例；lint保持10m并加verbose/profile；打包先补脱敏请求/缓存观测，暂不重跑。生产/测试/依赖/配置0修改，四文档同步；详细原始行号、精确命令、未知项与限制见下方报告。本批事实提取首轮exit1，原因是db.go已登记CRLF/LF差异被原字节断言拒绝；确认输入登记的两份SHA后仅接受该登记差异，续次exit0。原失败证据保留，未改源码。

C盘短报告：`C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-diagnosis-report.md`；事实表与最终身份/资源/串行证据见同目录`m6-diagnosis-facts.json`、`m6-diagnosis-HANDOFF.md`。399项输入源码摘要保持`7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590`，最终四文档SHA以本批清单为准。24项平台/能力限制暂缓，SV-M6-06三次停手，前批串行重叠FAIL及旧完整门禁FAIL保留。未提交/Push/Merge/发布；旧Claude/HKCU状态未清理。


## 存储与ABI有界复验（2026-11-08）

已离线恢复唯一已登记的Node137模块，归档SHA核验通过；原Electron149模块及.forge-meta完整备份，移除依赖目录内不再适用的149 sidecar。Node24.22.1实际加载ABI137，内存SQLite建表/写入/读回/关闭全部成功（exit0/0.193秒）。browser-profile-import单文件55 PASS、0 FAIL/0 SKIP（exit0/22.425秒），旧47项失败名称逐项映射为passed。

存储两活动单例race分别exit0/70.061秒（用例55.27秒）、exit0/71.427秒（用例54.80秒）；其余五个暂停顶层组exit0/64.887秒，5顶层与2子项均PASS。七个旧未终态均已获得明确PASS，没有源码/测试修改，没有替换迁移fixture或延长时限。更晚日志未发现可复用的这七项PASS，故本次按计划实跑。

本批沿用399项源码清单，摘要7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590；生产/测试/配置/依赖清单0修改，仅C盘原生模块与四执行文档变化。完整命令、依赖前后身份、资源/串行/宿主核对及交接见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-bounded-HANDOFF.md。未跑完整前端/存储/lint、下载、打包或桌面；24项限制、SV-M6-06三次停手、旧完整门禁和前批串行FAIL保留，M6仍FAIL。未提交/Push/Merge/发布，旧Claude/HKCU状态未清理。


## 完整自动门禁单次复核（2026-11-08）

本批唯一一次完整lint及唯一一次完整前端均exit0。固定golangci-lint2.13.2运行run --timeout=10m --concurrency=2 -v --cpu-profile-path C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-final-lint.cpu ./...：29.712秒、0 issues；加载17.8611052秒、linters阶段7.9403247秒（goanalysis_metalinter7.652881秒），工具总执行27.7293426秒。配置仍原28个linters与既有排除/nolint；日志处理前3437条、处理后0，本批没有删规则。CPU profile49612字节已保存，因已通过未扩展分析。旧两次exit4超时原样留档，本次通过不能反推旧超时具体根因。

完整前端npm test -- --maxWorkers=2，增加现有default/JSON reporter：exit0、1894.283秒；636文件PASS、10896用例PASS/14 SKIP/0 FAIL，总10910。Vitest自身耗时1888.86秒；原50项失败逐项映射为passed。此次是当前Node137依赖条件下完整套件通过，不再仅凭局部覆盖。14项跳过逐名列入本批results，不记作通过。

本批完整命令、原始日志、JSON断言、阶段耗时和身份/资源/串行审计见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-final-auto-HANDOFF.md及m6-final-auto-results.json。未改源码、测试、依赖清单或配置，399项源码摘要仍7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590，复用既有清单；仅同步四执行文档。未下载、重装、清缓存、打包或启动桌面；前批串行FAIL与旧Claude/HKCU状态保留，未提交/Push/Merge/发布。


## 存储完整race逐包复核异常停手（2026-11-08）

本批sqlite逐包完整race未取得正常终态，store未启动。实际命令go test -race -p 1 -parallel 2 ./internal/storage/sqlite -count=1 -timeout=15m -json，于11:26:52.089504 UTC启动；约7分钟时监测命令exit1，无异常栈输出，watch.json/.exit及测试metadata结束字段均缺失。HKCU观察记录commandExitCode=1，但Go子树仍运行，不能把该码解释为完整测试退出码。最后可见监测点409.6秒，源码/模块/宿主身份均未变且空间超过8GiB；缺失连续完整watch样本，不能声称本批监测完整通过。

只读进程核对确认Go PID29832（创建时间1794137212103ms）→Go40116→sqlite.test.exe16120仍在运行；核验PID/创建时间/命令后于11:35:08 UTC精确taskkill /PID 29832 /T /F，exit0、remaining=[]。未停止其他进程，未启动store。原始日志73行：15 PASS（8顶层+7子项）、2 SKIP子项、无单项FAIL及包终态；最后活动TestMigrateRepairsRenumberedAgentInventoryHistory（72–73行），没有panic超时栈。只能判本次执行/监测边界异常后中断，根因未知，不能判产品死锁或测试通过。

原始结果、精确停止证据、缺失门禁与当前身份见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-storage-full-HANDOFF.md。399项源码和六依赖清单未变，沿用摘要7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590，仅同步四文档。未新增Agent、下载、打包或桌面；sqlitetest/前端/lint未重跑。M6仍FAIL，24项限制、SV-M6-06三次停手、旧串行FAIL和Claude/HKCU状态保留，未提交/Push/Merge/发布。


### 存储监测异常的有界只读归因（2026-11-08）

已查19:33:40–19:34:39 +08:00的Application、WER-Diag/Operational和System资源耗尽事件，三项均NoMatchingEventsFound，无拒绝权限；没有扩大窗口或系统扫描。现有外层exit1、ro-run记录的子命令exit1与缺失watch/测试结束metadata只能确认异常退出，触发原因仍未知，不能推定OOM、系统杀进程或业务缺陷。源码确认监测/执行链缺少统一finally收尾，异常可能绕过终态落盘及残留子树回收；这是代码可确认的健壮性缺口，并非已证实的此次触发原因。

本批没有修改runner/生产/测试/配置，没有故障子进程或业务测试。最小建议是由统筹另行授权补足异常证据落盘与精确回收，再决定短故障验证；当前不具备直接恢复存储全量的证据。简短证据与最终进程/身份观察见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-runner-cause-HANDOFF.md。399项清单不重生成，M6仍FAIL；既有前端/lint PASS与存储异常停手边界不变。


## 验证脚本异常收尾补强（2026-11-08）

仅修C盘既有验证脚本：gates-audit.py提供共用Job进程所有权及异常finally收尾，run.py与监测入口复用；final-gates/storage/bounded入口改直接调用，删除层层字符串代码替换。启动进程先暂停、用持有句柄和创建时间确认并分配私有Windows Job后再恢复；异常保存原类型/脱敏信息、自然退出码未知则null，回收Job成员并记录精确残留。观测逐行落盘，最终失败终态独立保存。原监测exit1触发原因仍未知，这次只补已确认的收尾边界；OS强杀/断电不能靠finally保证终态落盘。

唯一启动后故障自检2.373秒：真实run.py路径在短进程及后代创建后触发RuntimeError injected_after_runner_start，内层commandExitCode=null、exitCode95；外层传播95，metadata/.exit/terminal/watch/注册观察均落盘，没有业务PASS。私有Job内3个进程PID39132/34864/8504及创建时间已记录，全部回收，remaining=[]、errors=[]；最终OS进程核对无自检/测试进程。没有运行第二条自检或业务套件。

修改前后脚本SHA、最小差异、自检完整命令与终态/回收证据见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-cleanup-HANDOFF.md。399项源码清单未重生成，摘要仍7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590；产品/测试/依赖/配置0修改，仅验证脚本和必要文档变化。8GiB、三配置/HKCU门禁保持，M6仍FAIL；不自行恢复完整race，24项限制/SV-M6-06三次停手/旧污染保留，未提交/Push/Merge/发布。

自检后的验证边界：run.py仅恢复两行原有身份漂移锁止逻辑（protectedUnchanged=false时写入isolation-blocked.json）；该补丁仅做语法检查，未进行第二次自检，不能宣称最终全部字节或全部分支均已动态验证。自检版本与交付版本SHA分别记录在m6-cleanup-delivery-script-changes.json。


## store 完整 race 单次续接（2026-11-08）

本次仅运行此前未启动的store完整包：go test -race -p 1 -parallel 2 ./internal/storage/sqlite/store -count=1 -timeout=15m -json。Go包终态FAIL，耗时900.464秒；内层命令918.022秒、外层918.512秒，真实退出码均为1。原始输出为panic: test timed out after 15m0s；超时时当前用例TestReportStoreRequeuesInterruptedClaimOnRestartAndFencesOldToken只运行56秒，活动栈处于runnable的modernc/sqlite迁移SQL路径。15分钟是全包累计预算，不能将最后一个用例视作独占15分钟或据此认定死锁/环境根因。

Go JSON日志记录427项PASS（250顶层、177子项），0单项FAIL、0 SKIP，但包FAIL；另5项在t.Parallel暂停、1项运行中未取得终态，不能用局部结果替代完整包。内外层独立terminal均commandExitCode=1、exitCode=1、originalError=null，Job before=[]、remaining=[]、errors=[]、terminated=false；进程已自然退出，无需额外终止。

内外层JSONL逐秒观测保存，源码监测21次；最低C盘11925147648字节，三配置/HKCU未变。收尾实查无本轮/Go/测试进程，重新核对双树源码/依赖810份SHA、六最终脚本及Node137模块均不变，399项清单未重生成。证据及完整命令见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-storage-full-store-race-resume-20261108T133802-HANDOFF.md。M6仍FAIL，sqlite旧中断根因未知；本批无自检或其他套件、不追加重试、不提交，24项限制及SV-M6-06三轮停手保持。


## store 首次建库 fixture 最小修复与单次复验（2026-11-08）

本批仅修改report_store_test.go、session_telemetry_store_test.go、budget_restart_test.go、search_store_test.go、workflow_store_test.go五个测试文件，10处首次空库创建改为现有sqlitetest.Open，覆盖12个既有用例。openWorkflowStorePair的3个调用者均审查并定向验证；4处生产sqlite.Open重开、3处OpenPreMigrated第二实例/重开、并发/CDC/旧token及业务断言全部保留。预算用例仅植入历史generation，本来就没有真实关闭重开，不把它写成重开验收。生产迁移、schema、共享helper、依赖及配置未改，未新增用例或框架。

定向race 12/12 PASS、包92.556秒、exit0；store包golangci-lint 2.13.2为103.550秒、exit0、0 issues。报告重启旧68.43秒，定向2.17秒、完整包2.09秒；预算旧66.77秒，定向作为首个模板初始化承担72.42秒，完整包复用后1.25秒。说明重复首次迁移成本被减少，但不能将整包其他慢点归因于此。

随后唯一完整store race仍FAIL：go test -race -p 1 -parallel 2 ./internal/storage/sqlite/store -count=1 -timeout=15m -json，包900.460秒、真实exit1，原始输出panic: test timed out after 15m0s；内层911.378秒、外层912.529秒。732项PASS（488顶层、244子项）、0单项FAIL/0 SKIP，包FAIL；9项暂停、TestWorkflowBindingProvisioningIsSessionBearing运行1秒未终态，其栈在runnable调度准入检查，并非旧报告首次迁移栈。5项工作流受影响用例在完整包尚未启动，仅有定向PASS，不能冒充整包覆盖。当前快照用例63.33秒、首次模板初始化相关投影用例63.10秒等仍占用预算。不再修改或重跑。

三次命令持续JSONL、独立terminal及HKCU观察齐全，内外层均自然退出、Job before/remaining/errors为空；最后只读核对本轮/Go/测试/lint进程为0，最低C盘11893485568字节，三配置/HKCU未变。源码清单由399扩至403项（4个既有文件新纳入清单，不是新增源码文件），最终摘要0df24e5a17c80454a07125b3b5b2c53bb14647cc0271d2beaffd7dab01dfe4fc；双树818份源码/六依赖SHA实查一致，六脚本及Node137模块不变，旧清单与原文件备份保留。

既有超长测试文件申报：search_store_test.go由301到302行，超过300行2行；workflow_store_test.go由1673到1674行，超过1374行。新增仅导入/必要方法注释，保留原测试组织与全部断言，不为比例拆文件。最小diff、全部调用者/保留重开审查、逐项耗时、完整命令及SHA见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-store-fixture-HANDOFF.md及m6-store-fixture-results.json。M6仍FAIL；sqlite完整race、旧监测中断原因、24项限制、SV-M6-06三次停手、打包及真实环境未验均保留；未重跑前端/全仓lint/sqlite/自检，不提交或Push/Merge/发布。


## 存储剩余顶层覆盖收口（2026-11-08）

真实-list目录为sqlite 168、store 561顶层；列目录exit0但没有Test运行事件，不计覆盖。合并适用历史race父项证据后，store由543/561补至561/561，sqlite仍31/168；合计592/729顶层具备分段PASS，137项缺口保留。

本批唯一补验使用go test -race -p 1 -parallel 2 ./internal/storage/sqlite/store -run <18项精确锚定表达式> -count=1 -timeout=15m -json，实际表达式、隔离入口及退出码见m6-storage-remaining-results.json。18顶层、19子项PASS，0 FAIL/SKIP，exit0；包100.504秒，内层112.424秒、外层113.743秒。已PASS项未重复选择，未启动第二条业务补验。

sqlite的137项缺口按预算分支暂缓：55项直接迁移、98项使用历史快照（两类可重叠），已有migrationSnapshots缓存。参考历史同类25.33、34.67、55.27秒，55项按25–55秒估算1375–3025秒，超过剩余单条900秒预算；这是量级估算，不是137项实测，也不表示每项同耗时或均重新空库迁移。

补验内外层各113次资源采样，最大间隔1.008126/1.507869秒，外层3次源码核对；最低C盘11889790976字节。三配置和精确HKCU身份未变，两层Job before/remaining/errors均空、terminated=false，收尾未见Go/test/lint/本批runner残留。本批未改源码、fixture、runner、配置或依赖；403项源码摘要仍为0df24e5a17c80454a07125b3b5b2c53bb14647cc0271d2beaffd7dab01dfe4fc。同步前826份双树SHA及674份历史适用性文件核对一致，七脚本与Node137模块不变；冻结清单不重生成，四文档新SHA单列在本批结果。

短交接：[m6-storage-remaining-HANDOFF.md](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-storage-remaining-HANDOFF.md)。逐项覆盖、137项缺口及源码/日志行号：[m6-storage-remaining-coverage.md](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-storage-remaining-coverage.md)；命令、身份、监测及SHA：[m6-storage-remaining-results.json](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-storage-remaining-results.json)。

分段覆盖不改写完整命令结果：历史完整store超时FAIL、sqlite监测INCOMPLETE继续保留，M6仍FAIL。旧sqlite异常根因、24项限制、SV-M6-06三轮停手、打包及真实环境未验、run.py两行身份锁止仅静态检查的边界不变；本批未新增测试/框架，未重跑完整套件或桌面，未提交/Push/Merge/发布，旧HKCU状态未清理。


## SQLite 三组有界补验（2026-11-08）

本批仅执行三组互不重叠的缺口，全部保留-race -p 1 -parallel 2 -count=1 -timeout=15m -json、父17分钟：第1组32顶层/18子项PASS，281.291秒；第2组37顶层/20子项PASS，233.390秒；第3组22顶层/4子项PASS，520.223秒。三组exit0、0 FAIL/SKIP，无已PASS重复；后两组按首组快照81.98秒与实际迁移成本选取。合计新增91顶层/42子项；sqlite由31/168增至122/168，剩余46项，store仍561/561分段覆盖。测试已停止，不派第四组。

交付影响：剩余46项没有专门直接断言M4六迁移或M5规划迁移的用例；当前M4迁移往返/UNKNOWN回填已有PASS，本批WorkflowCore也检查M5新增列，M6没有新增生产迁移。剩余24项历史升级与数据保留、9项harness约束、8项用量/额度、5项当前运行/资源约束，仍与含M4/M5的当前schema、预算事实、Session/CDC及磁盘稳定性相关；未执行不等于缺陷，也不按“历史”豁免。真正交付阻塞及下一步由统筹决定。

三组内外层terminal均自然退出、Job before/remaining/errors为空，配置/HKCU身份稳定，最低C盘11892817920字节；执行窗口不重叠。沿用403项源码摘要0df24e5a17c80454a07125b3b5b2c53bb14647cc0271d2beaffd7dab01dfe4fc，既有runner持续核对源码/依赖，未改源码、fixture、runner、配置或依赖。未重做目录枚举/广泛审计，只更新本报告和C盘增量覆盖索引，其他三文档待存储收口统一更新。

[短HANDOFF](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-sqlite-batched-HANDOFF.md) · [三组精确命令与原始证据](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-sqlite-batched-results.json) · [增量覆盖索引](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-sqlite-batched-coverage.json) · [剩余46项及影响](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-sqlite-batched-remaining.md)。

历史完整store FAIL、sqlite INCOMPLETE、既有子项SKIP保留，M6仍FAIL；24项外部/平台限制、SV-M6-06三轮停手、打包/真实环境及run.py两行仅静态验证边界不变。未提交/Push/Merge/发布。

## Windows 唯一构建被观察器中止（2026-11-09）

在C盘副本经diagnosis-run.py执行既有node24-bin/npm.cmd run build，check_source=True、check_native=False、父上限1020秒。Node v24.22.1、npm 10.9.3；45.955秒时Running prePackage hook from forgeConfig尚未结束。diagnosis-run.py第29行在check_native分支前无条件读取better_sqlite3.node，Forge按Electron44.7.0/ABI149重建期间文件暂缺，抛FileNotFoundError。这是监测装配缺陷，不是产品失败；自然commandExitCode=null，监测exitCode95，回收后根进程95，外层rtk工具exit1。唯一构建额度已用，不再启动第二次。

Job记录16个成员及创建时间，terminated=true、remaining=[]、errors=[]。收尾CIM中PID36324已复用为conhost（创建时间134386283036803010，原成员134386281377722990），不属于残留，未终止它。内层terminal/.exit与ro-run注册终态因被外层一并终止未落盘，外层独立terminal及JSONL完整保留。三配置/HKCU身份稳定，双树403源码/六依赖818份SHA一致，冻结清单未重生成；最低C盘10.959 GiB，未触发8GiB门禁。

原生模块初态为Node ABI137，SHA e75b8c024a85179d8e0e51203a8b8867916e9a51327ce3953db5f8483cc9a91e；最终.node及.forge-meta均不存在，config.gypi显示目标runtime=electron、ABI149、x64，不能称重建成功。既有149备份及x64--149元数据保留，未恢复或再编译；frontend/out不存在。此次仅记录到Forge将HTTPS Agent设为BoundHttpsProxyAgent，未到packager缓存/下载观察阶段，不能声称复现TLS或验证缓存。此前四个同SHASUMS小请求中仅Node原生代理通过，三种Forge代理路径ECONNRESET；具体协议根因未证实，不再请求。

最小修复建议：仅在Forge观察分支将实际读取抛出的FileNotFoundError记为“重建中”，默认ABI137断言及其他读取错误保持严格；构建终态另验.node和.forge-meta为目标149。本批不修改此缺陷、不恢复模块、不续接构建，等待统筹审查。仅改C盘既有run.py/diagnosis-run.py并加脱敏观察器，最小diff和SHA已有记录；源码/依赖/产品配置无改动。双树本报告及短HANDOFF更新，其余文档不动。

[短HANDOFF](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-package-observed-HANDOFF.md) · [精确终态](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-package-observed-results.json)。历史store FAIL、sqlite INCOMPLETE及46项缺口、SV-M6-06三轮停手、旧污染、安装升级/E2E未验保留；M6仍FAIL，未提交/Push/Merge/发布。

## 观察器最小修复与唯一续接（2026-11-09）

仅修改C盘diagnosis-run.py：check_native=False下分别捕获.node/.forge-meta的FileNotFoundError，记录missing/present，其他异常与默认Node137检查不放宽；compile()通过，最小diff及SHA见m6-package-observed-resume-script-change.json。脚本SHA为02585b7e8fc2afe5bddf02d3725f8bfc2a720e825a4c4678a924ae509b50ff05，其余七脚本未变。没有代理小请求、故障自检或其他业务补验。

新标签build-node24-package-observed-resume-20261108T162404，经既有C盘隔离入口执行npm.cmd run build，父1020秒/8GiB/源码及身份门禁保持。实际两次将原生文件及meta记为missing，未误判成功；91.626秒在prePackage原生重建期间，第34行读取.node抛PermissionError: [Errno 13] Permission denied，按“其他异常不放宽”中止。自然码null、监测码95、回收后根进程95、rtk工具1；没有下载/cache事件，不能称产品失败、TLS复现或自然终态。是否为写入共享冲突尚未证实。

外层Job记录17成员及创建时间，remaining=[]、errors=[]；最终CIM精确检查和本批目录进程匹配均为空。内层terminal/.exit及ro-run注册终态因被外层一起终止未落盘；外层terminal/JSONL及最终身份核对保留。最低C盘10.840GiB，403源码/六依赖双树818份SHA一致，三配置/HKCU未变。最终.node存在、2097152字节，SHA5647f05ec18958947d32874eeb788fa396a05d0bab7c1b71f112ceb7e9b31eee；.forge-meta仍缺失，config.gypi仅显示目标ABI149，未验证二进制完整性或ABI，不可作为有效模块。frontend/out不存在，无候选，未恢复备份或运行模块。

本批到此停止，不再次续接。建议统筹另审是否将构建中对原生文件的字节读取移至自然终态后，运行中仅记重建状态；这超出本次只捕获FileNotFoundError的授权，未实施。[短HANDOFF](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-package-observed-resume-HANDOFF.md)与[精确终态](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-package-observed-resume-results.json)保留旧中断证据。只更新双树m6-report及C盘短交接，未改产品源码/配置或其他指南；M6仍FAIL，存储46缺口、旧失败/污染、SV-M6-06三轮停手及安装升级/E2E未验边界不变，未提交/Push/Merge/发布。

## 输入与输出分离后的唯一构建（2026-11-09）

仅删去C盘diagnosis-run.py的Forge原生文件轮询，check_native=False返回deferred-to-post-build，默认Node137分支及403源码/六依赖实时核对不变。compile()通过；脚本SHA f8d41981ccc6fe66eea2dcbc2d59188753789ad274f37ea9f4de495f211404f2，其余七脚本SHA不变，最小diff见m6-package-observed-post-build-script-change.json。未泛化忽略任何PermissionError，无额外自检/测试/代理小请求。

新标签build-node24-package-observed-postbuild-20261108T163044，在C盘隔离frontend执行node24-bin/npm.cmd run build，父1020秒/8GiB及原Job门禁保持。prePackage原生重建和Vite阶段结束后，npm自然exit1，内层79.690秒、外层80.575秒；两层originalError=null。内层结束时Job仍有PID28452/creationFileTime134386290840204008，已精确回收，故最终包装95；外层接收95并自然退出。两层remaining/errors为空，最终CIM无匹配进程，所有terminal/.exit及注册表终态均已落盘。最低C盘10.703GiB，源码观察均unchanged，三配置/HKCU终态一致；没有重新生成源码清单或重复全仓SHA。

实际请求证据：官方Electron44.7.0 ZIP缓存hit=true；请求1为SHASUMS256.txt，重定向至release-assets.githubusercontent.com/github-production-release-asset/9384267/67eadd61-030b-4104-a38f-59b3ff6ccc06后ECONNRESET。上游随后回退请求2下载ZIP，重定向至同域/9384267/58d61a29-56d9-4a2a-9f2c-b8cb1429201e后同错。代理为BoundHttpsProxyAgent，原始错误为Client network socket disconnected before secure TLS connection was established；URL查询参数已脱敏。已确认真实失败资源与回退顺序，不能归因于ZIP损坏；具体代理/TLS协议原因尚未证明。

仅在自然退出且Job/CIM确认生成进程结束后读取产物：.node为1921536字节、SHA bfc272e5d1d374c09986284934e5887469ae3244030a1389ed852098733c03b6，.forge-meta为x64--149，二进制含node_register_module_v149标识；这是静态ABI证据，未运行模块。frontend/out不存在，无候选，失败中间产物不作为验收通过。没有再次修复或续接；下一步由统筹审查Forge代理到资产域名的下载链，保留TLS/checksum。

[短HANDOFF](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-package-observed-postbuild-HANDOFF.md) · [精确终态与脱敏请求](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-package-observed-postbuild-results.json)。只手工修改C盘观察脚本与双树本报告/短交接；原生及构建中间输出变化已记录，其他指南不动。历史两次观察中断、存储FAIL/INCOMPLETE及46缺口、旧污染、SV-M6-06停手与安装升级/E2E未验保持，M6仍FAIL，未提交/Push/Merge/发布。


## 本轮收口窗口：Windows候选已生成（2026-11-09）

本节为最新增量结论，保留前述全部失败。已在约10分钟有界定位窗口内确认global-agent/bootstrap.js:19已有GLOBAL_AGENT标记会跳过代理替换。仅本轮C盘run.py与既有preload按新标签启用局部适配，保留固定Node24原生代理；不改node_modules/产品或系统配置，不关闭TLS/checksum。该做法不是上游正式禁用开关，也不证明历史失败的具体双代理机制。最小diff及前后SHA见m6-package-native-owner-changes.json。

唯一官方SHASUMS请求自然/包装exit0、2.023秒，实际调用Forge initializeProxy后六个http/https原生Agent及get/request引用不变；同GitHub资产URL返回6740字节，清单与既有ZIP摘要一致。随后唯一完整npm.cmd run build自然exit0、133.726秒，外层135.073秒；ZIP缓存hit=true，唯一SHASUMS下载成功，没有回退下载ZIP。两层观察器originalError=null；内层收尾回收1个Job成员PID33592/creationFileTime134386298348974483，故包装95，外层接收95。两层remaining/errors为空，CIM终态无生成/runner进程；不能将包装95改写为整条验证全绿。

Windows x64候选目录为C:/Users/11459/.codex/worktrees/m5-desktop-safety/agent-orchestrator/frontend/out/Agent Orchestrator-win32-x64，不是安装器。仅在构建自然退出且生成进程清零后核对：EXE及ASAR包版本0.13.0、Electron44.7.0；ASAR主入口1213143字节、renderer入口2187字节，配置要求的Windows额外资源均存在。六个关键文件SHA已记录；EXE为6c9d65911fcd552d30d6388c3ad8cedcd422bc64ae455cbd5eae4656512714d8，app.asar为f19b7751708035ebf36f24b25cc4424de8782c7f55ed09f28745dba7e6f2bc8a。包内/源码侧原生模块均1921536字节、SHA2a40e145a39e7963b2c86ad9ce8bdc35932bb7a81402260b2bbbea0121b49b5a，meta=x64--149、二进制含node_register_module_v149；仅静态核对，未运行模块或桌面。

daemon静态Go构建信息版本0.13.0，嵌入Commit=8384bcc2a48d、vcs.modified=true；它是隔离副本旧HEAD，必须同时使用本次403项源码摘要0df24e5a17c80454a07125b3b5b2c53bb14647cc0271d2beaffd7dab01dfe4fc，不得单凭Commit反推源码。最低C盘9.892GiB，实时源码/依赖核对、三配置/HKCU身份门禁正常；未重算全仓长表。两次ASAR只读核对因Windows分隔符处理错误失败，已据已安装库源码改用path.normalize后实际读取成功；该辅助错误已记账，没有重跑构建。

[短HANDOFF](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-package-native-owner-HANDOFF.md) · [实际命令、终态与候选SHA](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-package-native-owner-results.json)。本批到此停止，仅追加双树本报告及C盘交接；不提交/Push/Merge/发布。M6仍FAIL：未验桌面/安装升级/E2E和动态模块有效性，SQLite46项及原完整store FAIL/sqlite INCOMPLETE、旧污染、SV-M6-06三轮停手等历史边界保持。


## Windows候选运行预检：隔离条件未满足，未启动（2026-11-09）

本批应用ao-desktop-dev隔离原则，但以实际打包源码和候选ASAR为准。现有runner确实重定向USERPROFILE/HOME/AppData/AO_DATA_DIR/AO_RUN_FILE/TEMP；打包userData仍由os.homedir()/.ao/electron派生，不能仅以AO_DATA_DIR证明实际运行隔离。源码及候选包内均确认：main.ts:582-585取得单实例锁后调用cloud-auth.ts:692的setAsDefaultProtocolClient(ao-app)，跳过分支要求!app.isPackaged；环境目录覆盖不隔离Windows用户令牌的HKCU。另main.ts:3727-3731启动时调用desktop-version-floor.ts:36的硬编码生产fetch；auto-updater.ts:2432-2465说明enabled=false仅关闭下载，仍检查更新，候选app-update.yml仍指向生产Untrivial-ai/agent-orchestrator。当前入口没有注册表/网络隔离，也没有可覆盖上述打包行为的现成开关，因此按计划不启动，不新增拦截框架或修改产品。

候选启动0次，自然/包装退出码均null（未执行）；Daemon/profile/数据实际身份、API就绪、ABI149动态加载、窗口和正常退出均未验。只读包内代码及实际fuse成功：RunAsNode/NodeCliInspect关闭，ASAR完整性与OnlyLoadAppFromAsar开启，未用Node137或开发版替代候选。候选ASAR与原生模块SHA仍符合上批记录，8脚本未变；三配置/HKCU身份一致，CIM候选进程匹配0，C盘9.877GiB，无本批Job或回收动作。

Windows安装路径只读确认：Forge MakerNSIS调用app-builder-lib26.15.3的buildForge，per-user默认、允许目录选择、创建快捷方式，publish=null；Forge CLI支持make --skip-package --platform win32 --arch x64，可复用当前候选避免premake/重新打包。工具按现有toolsets从指定ELECTRON_BUILDER_NSIS_DIR或缓存/下载解析；已知宿主electron-builder缓存根及out/make均不存在，新隔离根也未预置工具。没有执行make、下载、安装或升级；当前NsisUpdater及生产更新元数据不能当隔离升级验收条件。精确待执行argv与隔离前提见结果文件。

[短HANDOFF](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-candidate-runtime-HANDOFF.md) · [实际只读证据及未启动记录](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-candidate-runtime-results.json)。最小下一步是先确定独立HKCU和阻断生产外联且保留loopback的Windows运行环境，再核验原候选；不是对现有runner追加几行路径覆盖即可安全启动。本批未改源码、runner、依赖或配置，只追加双树m6-report及C盘短交接；M6仍FAIL，历史失败/未验和未提交边界不变。


## Windows 安装器生成：宿主配置门禁阻塞，未执行 make（2026-11-09）

- 本批按 m6-installer-plan 复用既有候选。初始核验六项候选 SHA、403 项源码及 6 项依赖摘要一致；候选总大小 689724058 字节，初始 C 盘可用 10605887488 字节。
- 已确认 Forge CLI 的 --skip-package 路径；直接 CLI 避免 npm premake/prepackage。现有 builder 默认工具为 nsis-3.0.4.1、nsis-resources-3.4.1、7zip@1.0.0，均走依赖内固定 GitHub 来源及 SHA 校验。未指定签名输入，publish=null。
- 已获澄清：允许构建用 BUILD_UNINSTALLER 临时生成器，禁止实际安装/卸载及最终安装器运行。源码确认无项目自定义 NSIS include/preInit、日志宏未开启、前置空间方法只设置 SectionSize；默认 $INSTDIR 与工具自带初始化仍未完成实证，未据此放行。
- 唯一工具准备入口 build-node24-installer-tools-20261108T170906 被现有门禁在创建 Job/子进程前拦截：包装码95，自然退出码null，原错误 AssertionError。真实 .codex/config.toml 从 ae0378cb89f44241a4aef3526bcbe3d5e0b557cd0a4434de552ac9d97b760931 变为 ac0837da07a9caec03bb258391508d7c85bfe1526e8efdd567e4ddcea906e966；本批未写该文件，变更来源未归因。未改门禁基线、未重试。
- make 0 次，工具下载/生成器/候选/安装器/卸载器运行均0；out/make 与隔离 builder 缓存均未创建。无候选相关进程残留；两份 Claude 配置与指定 Chrome HKCU 身份保持一致，Codex 配置漂移如实保留。六项候选 SHA 与冻结源码终态一致。
- 仅最小调整既有 C 盘 run.py：新增 installer 标签复用原生代理设置、指定隔离 builder 缓存、关闭 Jiti 文件缓存以免写 node_modules，并记录环境字段。修改前后及统一 diff 在 m6-installer-run.before.py / m6-installer-run.diff；未改产品、依赖或受保护配置。
- 原始证据和短交接：C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-installer-HANDOFF.md、m6-installer-results.json、m6-installer-preflight.json、build-node24-installer-tools-20261108T170906-watch.json / -watch.terminal.json。
- 未验：安装器生成及全部静态产物核对、候选运行、ABI149 动态加载、安装升级；历史失败不变。风险：宿主配置身份不再匹配原基线；M6 仍 FAIL，未提交、未发布。


## Windows 安装器续接：已认可基线，NSIS 工具下载失败（2026-11-09）

- 真实用户已明确认可当前 Codex 配置。仅将 m6-gates-preflight.json 中该配置身份更新为 SHA ac0837da07a9caec03bb258391508d7c85bfe1526e8efdd567e4ddcea906e966、mtimeNs 1794157698713543900；真实配置未写，其他配置/HKCU基线不变。旧基线保存为 m6-installer-gates-preflight.before-approval.json，授权记录为 m6-installer-baseline-approval.json，原退出95证据保留。
- 续接入口 build-node24-installer-tools-approved-20261108T172541：固定 Node24.22.1 经既有原生代理设置调用已安装 app-builder-lib 的 getMakeNsisPath(null) / getNsisPluginsPath(null)。首个 nsis-3.0.4.1.7z 从固定 GitHub 发布地址重定向至 release-assets.githubusercontent.com 后，在TLS建立前报 ECONNRESET / Client network socket disconnected before secure TLS connection was established。未改TLS/校验，未重试。
- Node自然/包装退出码1/1，耗时32.117秒；父进程自然/包装退出码1/1，耗时33.53秒，保留1020秒父上限。最低C盘 10572488704 字节（9.846 GiB），未触发8GiB门禁；两个Job均自然收尾，terminated=false、remaining=[]、errors=[]，CIM范围进程0。
- 下载观察1次开始、0次完成；progress=100%不代表资源或校验成功。隔离builder缓存无文件，out/make不存在。make、临时生成器、候选、安装器及卸载器运行均0次，无产物可核验。
- 六项候选输入SHA复核一致；已有源码观察通过，当前认可基线的配置与指定HKCU保持一致。续接未再改runner、产品、依赖或配置。
- 短交接及证据：C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-installer-approved-HANDOFF.md、m6-installer-approved-results.json，以及 build-node24-installer-tools-approved-20261108T172541.log / .terminal.json / -watch.json。
- 最小下一步：只处理首个固定NSIS资源的重定向下载链后续接工具准备，不原样反复make；尚未消耗make尝试。安装器生成、运行、安装升级及历史缺口仍未验，M6仍FAIL，未提交/发布。


## Windows 安装器生成：显式 Agent 与生成器隔离适配后成功（2026-11-09）

- 本批授权已包含临时 BUILD_UNINSTALLER EXE 及其必要隔离适配。只改既有 C 盘 run.py/preload，未改产品、node_modules、系统代理、TLS/checksum、真实配置或注册表。原配置漂移和下载失败证据全部保留。
- 显式 Agent 的实际链为 electronGet → buildGotProxyAgent → GotDownloader → got → https.request。仅本批固定 Node24 将 builder 显式 Agent 指向 Node 原生代理；真实下载入口记录 explicitHttpsIsNative=true。工具准备 build-node24-installer-agent-tools-20261108T173310 自然/包装0/0，5.024秒，NSIS/7zip/resources共3项固定资源下载与依赖SHA校验成功，独立摘要见 m6-installer-agent-tools-checksums.json。本次make复用缓存，新增下载0次。
- 生成器环境缺口为 NsisTarget.js:374 仅传 __COMPAT_LAYER，Windows WineVm.js:28-29 与 builder-util.exec 直接使用该env。preload按实际BUILD_UNINSTALLER编译定义及绝对输出路径登记身份，编译成功后只放行一次；补齐已有隔离env并保留RunAsInvoker，以 /D=C:\ao-m6-tests-507820bdd394\run-users\build-node24-installer-agent-make-20261108T173856\build-uninstaller 明确INSTDIR。实际TEMP/TMP、USERPROFILE/HOME、AppData、cwd与生成器路径断言通过并记录，随后WriteUninstaller写出245567字节并正常返回；最终编译后由原库删除中间卸载器。最终安装器、卸载器、候选应用运行均0次。
- 唯一make命令：固定Node24.22.1执行 frontend/node_modules/@electron-forge/cli/dist/electron-forge-make.js --skip-package --platform win32 --arch x64，cwd为C盘验证副本frontend。未执行npm premake/prepackage或重新package；日志中的Forge preMake/postMake内部hook照常出现。日志实际输出 Making distributables 成功及 Artifacts available at: frontend/out/make。
- 构建自然/包装0/0，98.325秒；父级自然/包装0/0，99.293秒，1020秒上限未触发。持续最低C盘 10026823680 字节（9.338 GiB），8GiB门禁未触发。两个Job均 terminated=false、before=[]、remaining=[]、errors=[]；收尾CIM匹配0。三次源码/依赖监测通过，指定配置与HKCU相对认可基线保持一致。
- 安装器：C:/Users/11459/.codex/worktrees/m5-desktop-safety/agent-orchestrator/frontend/out/make/Agent Orchestrator Setup 0.13.0.exe；193863342字节；SHA256 1d80e81d4997fba4553503a38ca517c25a3c24f7f9f3ab90288914a830f4cd44。文件/产品版本均0.13.0，PE ProductName实际为Orchestrator.inc。Get-AuthenticodeSignature实际返回 NotSigned(2)，无签名证书；日志中的“signing with signtool.exe”不能视为已签名。
- 仅在进程退出并确认无残留后读取产物。签名检查首次默认模块自动加载失败且PowerShell返回0，已保留错误并判为失败；显式加载现有Security模块、启用ErrorActionPreference=Stop后exit0确认NotSigned，未改策略。blockmap为202795字节，SHA256 bc14a3c5a1a5100a1e464913f0b4b7a62124b531d68e93be959f0e4b55367e51；gzip静态解析版本2，9273项块大小/摘要一一对应，总字节等于安装器。publish=null，未生成latest.yml、未发布；builder-debug.yml只是NSIS脚本记录。
- 六项关键候选SHA全部保持；目录清单新增resources/elevate.exe（107520字节，SHA256 9b1fbf0c11c520ae714af8aa9af12cfd48503eedecd7398d8992ee94d1b4dc37），其余无新增/删除/大小差异。这是builder生成行为，不能称候选目录逐字节未变。
- 短交接：C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-installer-agent-HANDOFF.md；结构化结果：同目录m6-installer-agent-results.json；原始日志：build-node24-installer-agent-make-20261108T173856.log / .terminal.json / -watch.terminal.json；入口修改前副本、diff及语法验证保留。
- 未验：最终应用运行、动态模块加载、安装/卸载/升级、E2E和历史未通过项，本批不重复全套测试。风险：本地未签名安装器尚未进行安装验收；M6仍FAIL，未提交/Push/Merge/发布。
