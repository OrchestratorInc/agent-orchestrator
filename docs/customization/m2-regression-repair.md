# M2 完整回归修复记录

日期：2026-11-05。项目：`E:\code\agent-orchestrator`。分支：`feat/multi-agent-desktop`。

本记录续接 `m2-remediation-round5.md`。用户在第五轮 F-03 修复后要求解释并修复完整回归失败，因此本轮包含实际暴露的 Windows 生产兼容问题及测试夹具修复。第五轮报告保留为当时的证据，不覆盖其失败历史。

**当前状态：提示词清单内回归及新增影响包已通过，具备提交第六轮独立复验的本地验证证据。M2 独立验收仍为 NOT ACCEPTED，本轮不进入 M3。**

本轮“完整回归”限定为原提示词列出的包、新增影响包、全 `TestM2` 与前端全套，不代表全仓 `go test ./...`、全仓 CI 或各操作系统发行包验收通过。

## 1. 失败原因

此前前端完整回归是 **37 个文件失败、192 个测试失败、3 个 suite 加载错误**，不是 195 个测试失败。后端 Manager、Controller、Daemon、Chat 完整包分别有 10、4、2、4 个顶层失败。失败不能统一归为 F-03 引入，也不能仅凭平台差异忽略。

| 范围 | 已确认原因 | 修复方向 |
| --- | --- | --- |
| Windows 进程取消 | 同步 `taskkill` 启动和等待挤占 PostCreate 的原有时间预算 | 使用 Windows 原生句柄和进程树终止；额外审查 PID 复用、根提前退出与整树回收 |
| Manager | 同一 tick 的人工输入被严格 `After` 漏判；父组件是文件被当作历史不存在 | 同时间戳保守拒绝重发；检查现存祖先的目录类型 |
| 文件浏览 | Windows 将父组件非目录表现为路径不存在；Go 1.27 将目录 junction 标为 `ModeIrregular`，原目录列表只跟随 `ModeSymlink` | 区分真正缺失与现存非目录；Windows junction 与 symlink 共用目标目录检查和 500 条输出上限 |
| mobile 配置并发 | 打开的读句柄阻止替换；原生替换期间短暂不可见会被读为默认状态 | share-delete 读句柄、原生替换、Windows 读/发布短段同步；慢连接器和临时写入在锁外 |
| 私有文件 | Windows 忽略 POSIX `0600`；创建后再收紧 ACL 不能撤销提前打开的句柄 | 核验并实现创建时的私有 DACL，保留路径与链接边界 |
| Git/脚本/认证夹具 | POSIX `true`/`sh`、无效 Windows file URI、只隔离 HOME、宿主认证污染 | 合法的平台脚本和路径，HOME/USERPROFILE 隔离，仅在外部认证边界使用 fixture |
| ZIP/DMG 夹具 | 102 项共享 cross-zip Windows 同步归档缺陷；时间戳临时目录撞名；CRLF shebang 导致 3 个 suite 无法加载 | 已有异步 ZIP、独占源目录、shebang 保持 LF，保留真实归档内容断言 |
| 前端平台策略 | 命名管道、路径分隔符、macOS 路径与宿主 Windows 混用 | 实际平台使用实际路径；macOS 专属代码使用 POSIX 路径 |
| 前端安全测试 | 用 Windows 原生文件系统验证 POSIX mode/fsync；Vitest 缓存内置模块函数导致 spy 未命中 | 通过已有 I/O 边界验证 POSIX 策略、注入后加载实际模块；目录使用真实 junction |
| HTTP/2 测试清理 | 先等待上游关闭，但代理尚持有连接 | 先关闭代理，再等待上游关闭 |
| 浏览历史容量 | 超限后逐项截断并完整序列化，形成平方级开销 | 超限时二分最大可保留前缀，同时保留未超限快路径 |
| 渲染测试 | 宿主中文地区/时区、流逝时钟及跨用例延迟请求影响精确断言 | 统一测试宿主地区/时区；固定待比较的时刻；清理本用例的延迟请求计时器 |

未通过删除用例、增加 skip、放宽原安全拒绝或时间断言获得通过。SystemExec 的执行失败/取消负例额外要求真实启动标记，缺少解释器已不能产生假绿。

最后一次 Manager 完整复验曾暴露 `TestSpawn_UnconfirmedNativeModelPreventsTask`：模型确认测试使用默认单仓项目与 `/ws/mer-1` 假路径，真实 Git DiffBase 探测耗尽 150ms，尚未进入 `/model opus` 投递便失败。该用例改用 Scratch 项目以隔离原本要验证的模型确认行为；原 150ms、必须投递模型切换指令、不得投递任务的断言全部保留。定向四项通过后，Manager 完整包通过。

## 2. 平台证据的边界

- 本机实跑 Windows、Go 1.27.1；原生 Windows 文件操作与进程行为须由实际执行证明。
- Windows 上的 POSIX I/O 注入证明的是策略与调用参数，不等于 Windows ACL 验收。原生 ACL 另行检查。
- 目录 junction 的拒绝测试先证明链接实际指向外部文件，再要求生产边界拒绝；没有以创建链接失败作为成功。
- Git Bash/MSYS 的 POSIX 链接经真实 GNU tar 归档、提取和 `readlink` 验证；它在 Windows 原生 `lstat` 下是普通文件，不代表 Windows 原生文件 symlink 验收。fsbrowser 的文件目标 junction 在 Windows 返回 `ERROR_DIRECTORY`，只能证明错误过滤，不能冒充文件 symlink 的成功 `Stat` 分支覆盖。
- 原有原生人工审批和特定平台测试开关保持；定向 `-run`/`-t` 未选中的测试不能算已执行。
- 本轮没有新增数据库迁移或修改对外 API 契约；API/sqlc 生成内容无漂移。
- 未调用真实 Provider；未提交、推送、合并或部署；未修改 `.codex/`、`.claude/` 配置。

## 3. 验证证据

完整输出与命令元数据保存在 `C:\Users\11459\AppData\Local\Temp\ao-regression-repair-20261105`。以下 Go 命令均在 `backend` 执行，npm 命令在仓库根执行，实际执行经 `rtk proxy` 包装。Go 耗时为测试输出的包耗时；包装器 JSON 另记包含启动/编译的总耗时。

已完成的分组验证包括：前端打包 170 项、平台 228 项、安全 180 项、渲染 434 项；这些分组通过不能替代完整套件。

| 检查 | 实际结果 | 日志 |
| --- | --- | --- |
| `go test ./internal/integration -run TestM2 -count=1 -v -timeout 30m` | **exit 0；33 PASS / 0 FAIL / 0 SKIP，539.503s** | `final-m2.log` |
| `go test -race ./internal/integration -run TestM2 -count=1 -v -timeout 30m` | **exit 0；33 PASS / 0 FAIL / 0 SKIP，658.859s；无 data race** | `final-m2-race.log` |
| `go test -p 1 ./internal/service/execution ./internal/service/workflow ./internal/service/collaboration ./internal/service/principal ./internal/service/intervention ./internal/storage/sqlite/... -count=1 -timeout 30m` | exit 0；所有有测试的包 PASS；SQLite 612.033s、store 148.588s | `final-backend-core.log` |
| `go test ./internal/session_manager -count=1 -timeout 30m` | **exit 0；完整包 PASS，508.685s** | `manager-final-full-2.log` |
| `go test ./internal/service/fsbrowser -count=1 -v -timeout 10m` | **exit 0；完整包 PASS，1.985s**；保留原有 Windows 不适用的 Unix-mode 权限测试 SKIP | `final-fsbrowser-junction-full.log` |
| `go test -race ./internal/service/fsbrowser -count=1 -v -timeout 10m` | **exit 0；完整包 PASS，5.225s，无 data race**；同一原有权限测试 SKIP | `final-fsbrowser-junction-race.log` |
| `go test -p 1 ./internal/httpd/controllers ./internal/daemon -count=1 -timeout 30m` | fsbrowser 冻结后完整复验 **exit 0；Controller 57.062s、Daemon 30.353s** | `post-fsbrowser-callers-full.log` |
| `go test -p 1 ./internal/session_manager ./internal/httpd/controllers ./internal/daemon ./internal/service/chat ./internal/process ./internal/adapters/systemexec ./internal/mobilebridge ./internal/service/fsbrowser -count=1 -timeout 30m` | **原组合命令 exit 1**；Manager 模型确认夹具与 fsbrowser 两项失败。其余完整包已 PASS：Controller 68.143s、Daemon 41.210s、Chat 190.892s、process 1.082s、systemexec 10.617s、mobilebridge 5.222s。失败包单独完整复跑，原退出码不改写 | `final-backend-affected.log` |
| `go test ./internal/service/execution -run '^(TestIndependentM2SingleRepoWorktreeRequiresLiveVerification\|TestM2F03MissingExpectedRepoClosesGate)$' -count=1 -v -timeout 5m` | exit 0；原始两项负例均 PASS，2.935s | `final-f03-negative.log` |
| `go test ./internal/cli ./internal/adapters/operatorconsent -run 'Test(MCPWorkflow\|Operator)' -count=1 -v -timeout 5m` | exit 0；CLI 4.002s、consent 1.929s；原有人工确认用例因未提供显式开关跳过 | `final-f02-operator.log` |
| 首轮完整前端复验 | **exit 1；6 文件失败、28 项失败、10849 项通过、17 项原有跳过；846.95s** | `final-frontend-full.log` |
| `npm --prefix frontend test -- --maxWorkers 4` | **exit 0；633 文件全部通过；10877 PASS / 17 原有 SKIP / 10894 total；1521.31s** | `final-frontend-full-2.log` |
| 后续 updater 与焦点两文件整组 | exit 0；251 项 PASS，23.46s | `platform-followup-pair.log` |
| 后续三个打包脚本文件整组 | exit 0；50 项 PASS，15.19s | `artifact-bridge-final.log` |
| 下载管理器类型修复后的完整文件 | exit 0；33 项 PASS | `frontend-browser-download-typefix-full.log` |
| 下载 fixture 修复后的 `npm run frontend:typecheck` | exit 0 | `frontend-browser-download-typecheck.log` |
| 最终 `npm run frontend:typecheck` | exit 0，无类型错误；44.893s，补齐机器可读退出码 | `final-typecheck-record.log` / `.json` |
| `go build ./...`、`go vet ./...` | fsbrowser 分类补修后分别 **exit 0**；16.451s / 17.309s | `post-fsbrowser-build.log`、`post-fsbrowser-vet.log` |
| `go test ./internal/httpd/apispec/... -count=1 -timeout 10m` | exit 0；apispec 4.008s、specgen 34.365s | `final-api-tests.log` |
| `npm run api`、`npm run sqlc` | 生成命令分别 exit 0；归一化生成内容漂移 0 | `final-api.log`、`final-sqlc.log`、`final-contract-verified.json` |

首轮完整复验暴露的 28 项包括：两个文件链接夹具、17 项 Bash/脚本权限、5 项 macOS updater 路径、3 项下载链接，以及 1 项焦点测试。它们已在后续完整前端复验中全部通过。焦点问题仅在全套触发，隔离未复现；修复为等待切换边界的 `act` 完成，保留焦点断言与时限，不宣称已证明生产焦点缺陷。Updater/焦点后的一次 Typecheck 曾真实失败于下载 fixture 的 `metadata` 可能为空（TS18048），已补存在性检查后重新通过。

M2 两轮包含双仓历史 BaseSHA、ProjectID 归属、单仓真实 Git 异常、SQLite 重开、正式 Operator CLI、队列/恢复/失效门禁和零 Provider 边界；`TestM2SessionManagerBindHonorsPinnedBaseSHA` 两轮均 PASS。F-03 的两项原始负例另行通过。人工原生审批未执行，不能由离线 CLI 结果代替。

API 与 sqlc 生成命令本身均 exit 0，生成内容按 LF 归一化比较未漂移。首次日志打印器因 Windows GBK 无法打印工具的 Unicode 符号而 exit 1；该包装器失败单独保留，不能把其退出码写成 0，也不能把生成命令误报为失败。

## 4. 文件保护与审查

本轮入场 `entry-files.json` 保存 5846 个文件 SHA256。修改清单以该快照比较，不认领原有 M2 未提交工作。最终核对：入场文件删除 **0**，快照范围内 `.codex/`、`.claude/` 变更 **0**；`docs/customization/README.md`、`m3-design.md`、`m3-plan.md` 的并发文档变化不属于本轮，保持原状并从本轮交付清单排除。

后端 M2 普通/race 与组合包、Build/Vet/API 检查期间，1214 个生产源码及依赖文件 SHA256 零漂移（`final-backend-summary.json`）。该窗口之后仅追加 fsbrowser 目录 junction 分类生产修复，另补该包普通/race、Controller/Daemon、Build/Vet，全部通过；Manager 最后仅调整测试项目类型，不再改生产逻辑。目录列表仅由目录 HTTP Controller 和 Daemon 装配调用，不在 M2 Worktree Git 校验链内，因此未无理由重跑已通过的全 M2。

最终静态核对：`git diff --check` exit 0；本轮 47 个 Go 文件执行 `gofmt -l`，exit 0，未格式化文件列表为空。最终补验期间 1214 个后端生产/依赖文件再次零漂移（`final-hygiene.json`、`post-fsbrowser-source-{before,after}.json`）。分支仍为 `feat/multi-agent-desktop`，HEAD 仍为 `a6c284a4d060431371306072e6c80abfedf22328`。

前端完整复验前后 1891 个文件 SHA256 零漂移（`final-frontend-source-drift.json`）。测试曾自动将 `ScriptRunPanel.test.tsx.snap` 转为 LF，后恢复为入场 CRLF 字节：SHA256 `b71ec68a419f8f7f5f7a4a70801dd8733c50155c8496446da4cd04b61a343691` 与入场一致，内容未变化（`snapshot-format-restored.json`）；不计作本轮修改。

独立审查识别的 Windows 进程裸 PID 回退、根提前退出后代回收、浏览历史未超限快路径、显式 `null` locale 被测试默认值吞掉等问题，均已修复并复核关闭。

process 三项反例均完成红→绿，并经独立复核关闭：原生句柄生命周期固定身份；根退出后仍回收已固定后代；整树未知错误不携带会被 `os/exec` 吞掉的 `os.ErrProcessDone` 哨兵。最终全包 exit 0 / 0.983s，race exit 0 / 8.347s，证据见 `postcreate-review-resolution.md`。

浏览历史快路径和显式 locale 语义已由独立精准探针复核：5000 条、767818B 的正常记录只序列化一次；显式 `null` 仍报 TypeError，`zh-CN` 和 locale 数组不变。相关 30 项测试 exit 0。

私有文件的补充真实反例证明：按文本路径创建临时身份文件后，替换父目录 junction 可使外部同名文件被发布、原临时内容残留。现统一在固定 `os.Root` 目录内创建、发布和清理；Windows 原生相对句柄创建同时提交私有 DACL。独立审查已关闭，未留确定问题：mobilebridge 完整包 8.161s / race 11.420s；chat 十项创建/保存边界 5.708s / race 75.013s，全部 exit 0。日志为 `final-v2-identity-mobilebridge-{full,race}.log`、`final-v2-render-native-{boundaries,race}.log`。

fsbrowser 真实 native junction 先在旧分类代码上红灯：应显示 5 个目录却只显示 `normal`，500 项目录测试返回 0 项；修复后目录链、悬空/循环、500/501 截断及目标内容断言通过（`red-fsbrowser-real-junction-classification.log` → `green-fsbrowser-real-junction-classification.log`）。仅给 Windows `ModeIrregular` 增加候选资格，仍须 `Stat` 成功且为目录。生产文件最终 SHA256 为 `4F0913045F58DB8B0EF4F8C9CBC888219DEC30C2021153E243CF712914B0F5FB`。独立只读复核已关闭，确认非 Windows 分类、错误过滤、输出上限及 HTTP 响应形状不变；局部 vet 的所报日志路径缺失，不计作已核对证据，最终采用主代理 `post-fsbrowser-vet.log` / `.json` 中全仓 `go vet ./...` exit 0 的证据。

## 5. 收口与验收边界

- 已通过：完整前端、M2 普通/race、F-01/F-02 离线回归、F-03 原始反例、提示词后端完整包、新增影响包、API/sqlc 无漂移、最终 Build/Vet/Typecheck、格式及文件保护检查。
- 原组合命令和首轮完整前端的失败日志原样保留，最终结论由修复后受影响完整包/套件的真实通过结果关闭；不将旧失败退出码改为 0。
- 未执行：全仓 `go test ./...` / 全仓 CI、其他原生操作系统、真实 macOS 签名/安装/发布、原生人工审批、真实 Provider；不改写历史报告中其他全仓失败。
- 风险：Windows 进程身份绑定依赖已核验的 Go 1.27.1 句柄生命周期，升级 Go 时须复核；原生 Windows 文件 symlink 受当前权限限制，其边界不能由 junction 或 MSYS 链接替代。

本次修复及复核未发现尚未处理的确定 P0/P1；这是本轮范围内的实施与审查结论，不是全仓安全保证。本记录可供第六轮独立复验，不替代其验收结论。

## 6. 本轮文件清单与规模申报

本节以入场 SHA256 对比识别，共 90 个本轮变化文件；并发的 3 个文档与测试自动转换换行的 snapshot 不计入本轮实现。

| 文件（相对项目根目录） | 类型 | 新建 | 行数 |
| --- | --- | --- | ---: |
| `.gitattributes` | 换行属性 | 否 | 7 |
| `backend/internal/adapters/systemexec/remote_script_test.go` | 测试 | 否 | 299 |
| `backend/internal/adapters/systemexec/systemexec_test.go` | 测试 | 否 | 75 |
| `backend/internal/daemon/cwd_test.go` | 测试 | 否 | 48 |
| `backend/internal/daemon/wiring_test.go` | 测试 | 否 | 1210 |
| `backend/internal/httpd/controllers/artifact_file_escape_test.go` | 测试 | 是 | 18 |
| `backend/internal/httpd/controllers/artifact_file_escape_windows_test.go` | 测试 | 是 | 43 |
| `backend/internal/httpd/controllers/artifact_files_test.go` | 测试 | 否 | 538 |
| `backend/internal/httpd/controllers/projects_test.go` | 测试 | 否 | 705 |
| `backend/internal/mobilebridge/config.go` | 源码 | 否 | 169 |
| `backend/internal/mobilebridge/config_file_other.go` | 源码 | 是 | 14 |
| `backend/internal/mobilebridge/config_file_windows.go` | 源码 | 是 | 90 |
| `backend/internal/mobilebridge/config_file_windows_test.go` | 测试 | 是 | 31 |
| `backend/internal/mobilebridge/config_test.go` | 测试 | 否 | 132 |
| `backend/internal/mobilebridge/identity.go` | 源码 | 否 | 158 |
| `backend/internal/mobilebridge/identity_directory_windows_test.go` | 测试 | 是 | 113 |
| `backend/internal/mobilebridge/identity_permissions_other_test.go` | 测试 | 是 | 20 |
| `backend/internal/mobilebridge/identity_permissions_windows_test.go` | 测试 | 是 | 182 |
| `backend/internal/mobilebridge/identity_private_other.go` | 源码 | 是 | 21 |
| `backend/internal/mobilebridge/identity_private_windows.go` | 源码 | 是 | 176 |
| `backend/internal/mobilebridge/identity_test.go` | 测试 | 否 | 201 |
| `backend/internal/process/tree_test.go` | 测试 | 否 | 148 |
| `backend/internal/process/tree_windows.go` | 源码 | 否 | 215 |
| `backend/internal/process/tree_windows_test.go` | 测试 | 是 | 183 |
| `backend/internal/service/chat/artifact_link_test.go` | 测试 | 是 | 18 |
| `backend/internal/service/chat/artifact_link_windows_test.go` | 测试 | 是 | 43 |
| `backend/internal/service/chat/artifact_test.go` | 测试 | 否 | 212 |
| `backend/internal/service/chat/handoff_history_files.go` | 源码 | 否 | 159 |
| `backend/internal/service/chat/handoff_history_files_test.go` | 测试 | 否 | 112 |
| `backend/internal/service/chat/handoff_paths_test.go` | 测试 | 是 | 41 |
| `backend/internal/service/chat/render.go` | 源码 | 否 | 554 |
| `backend/internal/service/chat/render_permissions_test.go` | 测试 | 是 | 20 |
| `backend/internal/service/chat/render_permissions_windows_test.go` | 测试 | 是 | 71 |
| `backend/internal/service/chat/render_private_file.go` | 源码 | 是 | 16 |
| `backend/internal/service/chat/render_private_file_windows.go` | 源码 | 是 | 78 |
| `backend/internal/service/chat/render_private_file_windows_test.go` | 测试 | 是 | 269 |
| `backend/internal/service/chat/render_test.go` | 测试 | 否 | 539 |
| `backend/internal/service/fsbrowser/service.go` | 源码 | 否 | 113 |
| `backend/internal/service/fsbrowser/service_link_other_test.go` | 测试 | 是 | 16 |
| `backend/internal/service/fsbrowser/service_link_windows_test.go` | 测试 | 是 | 58 |
| `backend/internal/service/fsbrowser/service_test.go` | 测试 | 否 | 152 |
| `backend/internal/session_manager/chat_spawn_async_test.go` | 测试 | 否 | 1305 |
| `backend/internal/session_manager/chat_spawn_test.go` | 测试 | 否 | 2021 |
| `backend/internal/session_manager/interface_transition.go` | 源码 | 否 | 1927 |
| `backend/internal/session_manager/interface_transition_history_auth_fixture_test.go` | 测试 | 是 | 19 |
| `backend/internal/session_manager/interface_transition_history_integration_test.go` | 测试 | 否 | 352 |
| `backend/internal/session_manager/manager.go` | 源码 | 否 | 7427 |
| `backend/internal/session_manager/manager_test.go` | 测试 | 否 | 12243 |
| `docs/customization/m2-plan.md` | 文档 | 否 | 332 |
| `docs/customization/m2-regression-repair.md` | 文档 | 是 | 237 |
| `frontend/forge.config.test.ts` | 测试 | 否 | 409 |
| `frontend/makers/maker-dmg.test.ts` | 测试 | 否 | 276 |
| `frontend/scripts/blockmap-reconstruction.test.mjs` | 测试 | 否 | 256 |
| `frontend/scripts/build-acp-runtime-helpers.test.mjs` | 测试 | 否 | 704 |
| `frontend/scripts/build-host-bundle.test.mjs` | 测试 | 否 | 72 |
| `frontend/scripts/build-mac-dmg.mjs` | 仅换行 | 否 | 333 |
| `frontend/scripts/build-mac-dmg.test.mjs` | 测试 | 否 | 294 |
| `frontend/scripts/test-fixtures/git-posix-tools.mjs` | 夹具 | 是 | 26 |
| `frontend/scripts/test-fixtures/mac-differential-v2.mjs` | 夹具 | 否 | 45 |
| `frontend/scripts/verify-mac-artifact.sh` | 仅换行 | 否 | 269 |
| `frontend/scripts/verify-mac-artifact.test.mjs` | 测试 | 否 | 218 |
| `frontend/src/main/auto-updater.test.ts` | 测试 | 否 | 5306 |
| `frontend/src/main/auto-updater.ts` | 源码 | 否 | 3228 |
| `frontend/src/main/browser-download-manager.test.ts` | 测试 | 否 | 844 |
| `frontend/src/main/browser-history-store.test.ts` | 测试 | 否 | 72 |
| `frontend/src/main/browser-history-store.ts` | 源码 | 否 | 260 |
| `frontend/src/main/browser-profile-import.test.ts` | 测试 | 否 | 1648 |
| `frontend/src/main/chat-image-ipc.test.ts` | 测试 | 否 | 107 |
| `frontend/src/main/cloud-agent-proxy.test.ts` | 测试 | 否 | 299 |
| `frontend/src/main/cloud-auth.test.ts` | 测试 | 否 | 687 |
| `frontend/src/main/desktop-telemetry-controller.test.ts` | 测试 | 否 | 362 |
| `frontend/src/main/mac-update-progress.ts` | 源码 | 否 | 118 |
| `frontend/src/main/provider-auth-flow.test.ts` | 测试 | 否 | 163 |
| `frontend/src/main/provider-auth-flow.ts` | 源码 | 否 | 658 |
| `frontend/src/main/relocation.ts` | 源码 | 否 | 104 |
| `frontend/src/main/remote-proxy.test.ts` | 测试 | 否 | 1988 |
| `frontend/src/main/remotes-ipc.test.ts` | 测试 | 否 | 97 |
| `frontend/src/main/remotes-main.test.ts` | 测试 | 否 | 459 |
| `frontend/src/main/remotes-store.test.ts` | 测试 | 否 | 224 |
| `frontend/src/main/sentry-main.test.ts` | 测试 | 否 | 446 |
| `frontend/src/main/stale-app-copies.ts` | 源码 | 否 | 231 |
| `frontend/src/main/supervisor-link.test.ts` | 测试 | 否 | 203 |
| `frontend/src/main/telemetry-policy-file.test.ts` | 测试 | 否 | 198 |
| `frontend/src/renderer/components/SessionMemoryPanel.test.tsx` | 测试 | 否 | 675 |
| `frontend/src/renderer/components/SettingsDialog.test.tsx` | 测试 | 否 | 692 |
| `frontend/src/renderer/components/chat/IndependentSideCreation.test.tsx` | 测试 | 否 | 204 |
| `frontend/src/renderer/test/setup.ts` | 夹具 | 否 | 502 |
| `frontend/src/shared/cli-link.test-fixtures.ts` | 夹具 | 是 | 129 |
| `frontend/src/shared/cli-link.test.ts` | 测试 | 否 | 291 |
| `frontend/src/shared/telemetry.test.ts` | 测试 | 否 | 266 |

### 6.1 单文件超限

以下均为继承文件，本轮只修改对应缺陷/夹具。保留原验收用例，不为压低统计进行无关拆分或删测。新建测试文件均不超过 300 行，新夹具均不超过 400 行。

| 文件 | 最终行数 | 超出上限 | 对应验收范围 |
| --- | ---: | ---: | --- |
| `backend/internal/daemon/wiring_test.go` | 1210 | 910 | Daemon 装配 |
| `backend/internal/httpd/controllers/artifact_files_test.go` | 538 | 238 | Controller HTTP/工作区边界 |
| `backend/internal/httpd/controllers/projects_test.go` | 705 | 405 | Controller HTTP/工作区边界 |
| `backend/internal/service/chat/render_test.go` | 539 | 239 | Chat artifact 保存/恢复 |
| `backend/internal/session_manager/chat_spawn_async_test.go` | 1305 | 1005 | Manager 历史/输入/启动回归 |
| `backend/internal/session_manager/chat_spawn_test.go` | 2021 | 1721 | Manager 历史/输入/启动回归 |
| `backend/internal/session_manager/interface_transition_history_integration_test.go` | 352 | 52 | Manager 历史/输入/启动回归 |
| `backend/internal/session_manager/manager_test.go` | 12243 | 11943 | Manager 历史/输入/启动回归 |
| `frontend/forge.config.test.ts` | 409 | 109 | 前端打包布局与脚本行为 |
| `frontend/scripts/build-acp-runtime-helpers.test.mjs` | 704 | 404 | 前端打包布局与脚本行为 |
| `frontend/src/main/auto-updater.test.ts` | 5306 | 5006 | 前端平台、权限或生命周期 |
| `frontend/src/main/browser-download-manager.test.ts` | 844 | 544 | 前端平台、权限或生命周期 |
| `frontend/src/main/browser-profile-import.test.ts` | 1648 | 1348 | 前端平台、权限或生命周期 |
| `frontend/src/main/cloud-auth.test.ts` | 687 | 387 | 前端平台、权限或生命周期 |
| `frontend/src/main/desktop-telemetry-controller.test.ts` | 362 | 62 | 前端平台、权限或生命周期 |
| `frontend/src/main/remote-proxy.test.ts` | 1988 | 1688 | 前端平台、权限或生命周期 |
| `frontend/src/main/remotes-main.test.ts` | 459 | 159 | 前端平台、权限或生命周期 |
| `frontend/src/main/sentry-main.test.ts` | 446 | 146 | 前端平台、权限或生命周期 |
| `frontend/src/renderer/components/SessionMemoryPanel.test.tsx` | 675 | 375 | 渲染、语言、时序隔离 |
| `frontend/src/renderer/components/SettingsDialog.test.tsx` | 692 | 392 | 渲染、语言、时序隔离 |
| `frontend/src/renderer/test/setup.ts` | 502 | 102 | 渲染、语言、时序隔离 |

### 6.2 整文件统计的超限申报

保守按本轮触及文件的整文件统计：测试及夹具 39599 行，源码 16318 行，超过源码两倍 6963 行。该数字包含既有大型套件，并非本轮新增行数；这些套件也覆盖同包未修改源码。本轮保留既有验收边界，未借此扩展为大型测试重构。

文件保护以最终 `current-delta.json` 和哈希校验结果为准；临时日志和验证脚本位于系统 Temp，未加入仓库。
