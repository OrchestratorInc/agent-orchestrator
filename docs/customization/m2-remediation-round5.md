# M2 第五轮 F-03 修复与验证报告

日期：2026-11-05（UTC+08）；工作区：`E:\code\agent-orchestrator`；分支：`feat/multi-agent-desktop`。

**状态：F-03 定向验证、全 M2 与 Race 通过；完整回归 FAIL。第五轮独立结论仍为 NOT ACCEPTED，不进入 M3。** 本文件记录实施证据，不能替代第六轮独立验收；尚不满足第六轮全部申请条件。

## 1. 范围与证据来源

只处理 F-03：显式 M2 Workflow 的单仓/多仓实时工作区核验、缺证据闭锁、执行与恢复调用链。保留 F-01 历史 BaseSHA、F-02 Operator 审批、M1 Legacy 行为。

没有提交、推送、合并、部署、修改 `.codex/`/`.claude/` 配置或清理用户工作区。测试采用临时 Git、隔离 SQLite、Scripted/Fake Agent；真实 Provider 与人工 Windows Hello 不在本轮自动验证范围。

本轮日志目录：`C:\Users\11459\AppData\Local\Temp\ao-m2-round5-f03-20261105`。`entry-files.json` 保存入场文件 SHA256，最终差异清单单独记录，避免将原有 M2 dirty 改动认领为本轮实现。所有 PASS/FAIL 均对应本轮命令；中间红灯、编译窗口和超时保留，不计为最终通过。

本机实测：`go version go1.27.1 windows/amd64`；`GOOS=windows`、`GOARCH=amd64`、`CGO_ENABLED=1`、`CC=gcc`。Shell 为 `rtk proxy pwsh -NoProfile`，外层禁用登录模式。

## 2. 根因与修复机制

原实现存在三个直接根因：Gateway 仅在多于一条 worktree 记录时调用验证器；Manager 将零/一条记录当作 Legacy；Coordinator 对单个期望 Repo 缺记录放行。此外，观察里的 `HeadSHA` 来自历史 `CreationSHA`，无法证明当前 Git 状态。

调用链审查还确认：重复 Bind 返回旧状态；Resume 的部分路径先启动后观察；LiveTarget 没有期望 Repo 门禁；已经排队的消息在实际出队时不会重验。这些均属于本次 F-03 执行链修复范围。

修复后的核心规则：

1. Manager 提供 `ObserveSessionWorktrees`，`VerifySessionWorktrees` 复用同一实现。每次读取持久化绑定、worktree 记录和真实 Git，不缓存成功结果。
2. 通过 `GetWorkflowAttemptBindingBySession`、创建意图 `GetWorkflowAttemptBindingBySpawnRequest` 和显式 Repo 身份判定 M2。记录数量与通用 ClientRequestID 的文本格式均不作为类型依据。
3. 按 RepoKey 对齐完整预期集合，核对 ProjectID、RepoPath、WorktreePath、根工作区、Git common directory、实际分支、CreationSHA/BaseSHA 和当前 HEAD 历史。目录不存在、换库、缺行、历史回退、分支漂移、Git/index 错误均拒绝。
4. `HeadSHA` 只返回本次核验的真实 HEAD；任一仓库无法验证，Gateway readiness 降为 `unknown`，不将历史 SHA 回填为当前 HEAD。缺验证器或无法确认归属同样闭锁。
5. 请求 pin 与验证后的 `BaseSHA` 比较，CreationSHA 必须属于当前 HEAD 的祖先链。合法后继提交及 tracked/untracked dirty 内容保留，不执行清理或重置。
6. 无显式 Repo 绑定的 Legacy 保留原工作区与恢复路径；已知 Workflow 归属但无法读取其预期集合不降级为 Legacy。

## 3. 调用链对照

| 操作 | 修复前缺口 | 修复后边界 |
| --- | --- | --- |
| Bind / 重复 Bind | 单仓缺行放行；重放信任旧 READY | Spawn 观察及持久化期望门禁；重放重新 Reconcile，不重复 Spawn |
| Session Observe | CreationSHA 充当当前 HEAD | Manager 本次完整 Git 核验；失败 unknown、空可信 HEAD |
| Queue / Send / Steer | 当前能力与 generation 不能证明 Repo 归属 | Gateway 当前观察 + LiveTarget Repo 门禁；新 Send/Retry 在落库前核验；Steer 等待 ack 后、reserve/claim 前再次核验；保留 generation fencing |
| Queue 实际出队 | 只在入队时验证 | Manager 注入派发检查；出队 claim 前验证，失败保留已接受的 durable queue；拒绝的新消息不会留下可执行行 |
| Interrupt / Stop | 控制器存活时未独立核验工作区 | 操作前重新核验；仍在既有 generation 栅栏下控制 |
| Resume / 自动恢复 | 可能先启动，或 Legacy 路径重建丢失目录 | 所有相关启动/接管边界先校验；不重建丢失 M2 工作区 |
| 历史分支切换及失败补偿 | 分支恢复先调用 driver，再把回调交给新 Controller | 四个 driver Start/Resume 入口均在环境准备结束后核验，再创建 Provider；替换 Controller 继承回调 |
| Reconcile | 多仓有核验，单仓绕过 | 统一重读完整集合与真实 Git；失败冻结且保留 SessionID |
| Resolve | 人工授权的 UNKNOWN 结算 | 保留审批、revision CAS、审计；不启动 Provider，允许处置失效现场 |
| Collaboration | 持久化协作消息，无直接 Provider 派发 | 保留原授权边界；实际执行仍经过上述网关 |

## 4. 数据与接口

无新增数据库迁移、sqlc query 或 HTTP DTO 字段。内部观察接口返回 live HEAD/branch/dirty；`ports.RepoWorktreeObservation.HeadSHA` 的说明更新为真实观察语义。Controller 的工作区检查回调是进程内依赖，不是用户配置，也不发送给 Provider。

HTTP Observe 现有 DTO 不携带 worktrees；测试通过 HTTP 验证 readiness/live，并从同一真实 Manager/SQLite 装配的公开 LocalGateway 接口验证 SHA、分支与 dirty，未为了测试扩张 HTTP 契约。

## 5. 测试与真实执行记录

以下 `go` 命令均在 `backend/` 执行，`npm` 命令在根目录执行，均经 `rtk proxy`，PowerShell 使用 `-NoProfile`、`login:false`。日志位于 §1 的证据目录。表中秒数默认是 Go 包输出时间，Build/Vet 为外层实测耗时；退出码来自进程，不从日志文本猜测。

### 5.1 最终回归

| 命令 | 退出码 / 结果 | 日志 |
| --- | --- | --- |
| `go test ./internal/integration -run TestM2 -count=1 -v -timeout 30m` | **0；33 个顶层 PASS、0 FAIL、0 SKIP；495.135s**（进程 510.183s） | `final-m2.log`、`final-m2.meta.json` |
| `go test -race ./internal/integration -run TestM2 -count=1 -timeout 30m` | **0；包 PASS，578.724s，无 data race**（进程 613.692s） | `final-m2-race.log`、`final-m2-race.meta.json` |
| `go test ./internal/session_manager -count=1 -timeout 30m` | **1；FAIL，10 个顶层失败，545.794s** | `manager-final-full.log` |
| `go test ./internal/session_manager -run '^TestRunPostCreateUsesEachStepsTimeLimit$' -count=1 -v -timeout 5m` | **1；FAIL，单例 2.77s、包 6.265s**，耗时断言在当前源码再次失败 | `postcreate-current-isolated.log` |
| `go test ./internal/service/execution -count=1 -timeout 30m` | 0；PASS，5.726s | `final-execution.log` |
| `go test ./internal/service/workflow -count=1` | 0；2.430s | `regression-workflow.log` |
| `go test ./internal/service/collaboration -count=1` | 0；0.289s | `regression-collaboration.log` |
| `go test ./internal/service/principal -count=1` | 0；0.291s | `regression-principal.log` |
| `go test ./internal/service/intervention -count=1 -timeout 30m` | 0；2.611s | `final-intervention.log` |
| `go test ./internal/httpd/controllers -count=1 -timeout 30m` | **1；FAIL，4 个顶层失败，86.467s** | `final-controllers.log` |
| `go test ./internal/daemon -count=1 -timeout 30m` | **1；FAIL，2 个顶层失败，40.740s** | `final-daemon.log` |
| `go test ./internal/storage/sqlite/... -count=1 -timeout 30m` | 0；sqlite 167.442s、sqlitetest 2.720s、store 76.463s | `final-sqlite.log` |
| `go test ./internal/service/chat -count=1` | **1；FAIL，4 个顶层失败，165.352s** | `queue-full-final.log` |
| `go build ./...` | 0；无编译错误，15.438s | `final-build.log`、`final-build-result.txt` |
| `go vet ./...` | 0；无诊断，21.990s | `final-vet.log`、`final-vet-result.txt` |
| `npm run frontend:typecheck` | 0；`tsgo --noEmit` | `frontend-typecheck.log` |
| `npm --prefix frontend test` | **1；37 文件失败、192 用例失败、10616 通过、17 跳过**；738.96s | `frontend-full.log` |
| `npm run api` | 0；OpenAPI 与 schema 生成前后差异 0 | `generated-api.log`、`generated-api-result.txt` |
| `go test ./internal/httpd/apispec/... -count=1` | 0；apispec 0.470s、specgen 30.802s | `api-drift.log` |
| `npm run sqlc` | 0；生成文件差异 0，**本轮没有网络 BLOCKED** | `generated-sqlc.log`、`generated-sqlc-result.txt` |

默认 10 分钟的 M2 初次执行曾超时，保留在 `git-e2e-wired-recheck.log`，不记为 PASS。最终全 M2 与 race 只增加 `-timeout 30m`，不加 `-skip`，不删除用例、不放宽断言。较早的 `-skip TestM2F03` 仅用于诊断旧 Fake 夹具，不能替代最终全套。

最终 race 严格采用任务要求的非 `-v` 命令，只有包级 `ok`，没有逐项 PASS 输出；33 是同一源码 SHA 下相同 `TestM2` 选择集合。`final-m2-evidence.json` 保存两轮确切命令、退出码、耗时、普通回归 33 项名称、0 个 race 警告及源码一致性。

### 5.2 缺陷、兼容与 F-01/F-02 专项

| 命令 / 范围 | 退出码 / 实际结果 | 日志 |
| --- | --- | --- |
| `go test ./internal/service/execution -run 'TestIndependentM2SingleRepoWorktreeRequiresLiveVerification\|TestM2F03MissingExpectedRepoClosesGate' -count=1 -v`，修复前 | **1**；历史记录让缺目录单仓 READY，单仓缺行 `gate=false` | `gateway-red.log` |
| `go test ./internal/service/execution -run '^(TestIndependentM2SingleRepoWorktreeRequiresLiveVerification\|TestM2F03MissingExpectedRepoClosesGate)$' -count=1 -v`，最终 | **0；2/2 PASS，0.220s** | `gateway-final-negative.log` |
| `go test ./internal/session_manager -run 'Test.*(F03\|MultiRepo)' -count=1` | 0；283.622s，含 Legacy 与真实双仓恢复 | `manager-final-targeted.log` |
| `go test -race ./internal/session_manager -run 'Test.*(F03\|MultiRepo)' -count=1` | 0；281.001s；编译早于不可变 callback 捕获修正，不能单独证明最后版本 | `manager-final-race.log` |
| 不可变 SessionID 修正后的恢复/WorkflowContext race（命令见下） | 0；6 项，63.655s；跑前后源码 SHA 一致 | `manager-postfix-recovery-race.log` |
| `go test ./internal/integration -run TestM2F03 -count=1 -v` | 0；5 个顶层，13 个真实 Git 异常和 5 个 HTTP 入口，212.408s | `git-e2e-final-focused.log` |
| `go test ./internal/integration -run '^TestM2SessionManagerBindHonorsPinnedBaseSHA$' -count=1 -v` | 0；33.680s，真实双仓历史 pin 与归属 | `f01-historical-double-git.log` |
| `go test ./internal/cli ./internal/adapters/operatorconsent -run 'Test(MCPWorkflow\|Operator)' -count=1 -v` | 0；CLI 4.667s、consent 2.194s；人工原生 consent 用例按其显式开关跳过 | `final-operator-cli.log` |
| `npm --prefix frontend test -- src/main/operator-ipc.test.ts` | 0；1 文件、7/7 PASS，5.34s | `operator-ipc.log` |
| `go test ./internal/service/chat -run 'TestM2F03' -count=1` | 0；8.600s | `queue-f03-green.log` |
| `go test ./internal/service/chat -run 'TestM2F03\|Test.*(Steer\|Queued\|Branch\|Edit\|Retry\|SendPinned)' -count=1` | 0；58.345s | `queue-related.log` |
| `go test -race ./internal/service/chat -run 'TestM2F03\|Test.*(Steer\|Queued\|Branch\|Edit\|Retry\|SendPinned)' -count=1` | 0；463.423s，无 data race | `queue-race.log` |

不可变 SessionID 修正后的确切命令：

```powershell
rtk proxy go test ./internal/session_manager -race -count=1 -timeout 10m -run '^(TestF03RecoveryEntryPointsRejectMissingWorktree|TestF03SingleRepoRestoreNeverRecreatesMissingWorktree|TestResumeRecoversWorkflowMCPEnvFromDurableState|TestResumeOmitsAnExpiredLease|TestResumeWithoutABindingAddsNoWorkflowKeys|TestWorkflowSpawnContextRequiresABoundSession)$'
```

Chat 独立源码审查发现三项 P1 后，先运行红灯再修复：`queue-steer-red.log`（ack 等待后继续派发）、`queue-acceptance-red.log`（拒绝 Send/Retry 残留可执行消息）、`queue-history-red.log`（分支恢复在核验前启动）；`queue-fallback-red.log` 覆盖 fallback 拒绝不能误终止已接受 followers。各红灯 exit 1 保留，最终绿灯与 race 不与其混记。

真实单仓异常矩阵均由上表 `go test ./internal/integration -run TestM2F03 -count=1 -v` 执行，并在最终无 skip 全 M2 再次通过；日志有逐项 `--- PASS`，整体 exit 0：

| 场景 | 结果 / 测试 |
| --- | --- |
| 正常、历史 pin、合法后继 HEAD、tracked/untracked dirty | READY；`TestM2F03SingleRepoHTTPThroughSQLiteReopen` PASS |
| Worktree 记录删除、RepoKey 错绑、ProjectID 错绑、RepoPath 错绑 | 非 READY；`TestM2F03SingleRepoLiveGitRechecks` 对应 4 子项 PASS |
| WorktreePath 替换、Worktree 换成相同历史的独立仓库 | 非 READY；对应 2 子项 PASS，不能靠相同 HEAD 绕过 common-dir |
| Repo 目录丢失、Repo 换成另一仓库 | 非 READY；对应 2 子项 PASS |
| BaseSHA 篡改、CreationSHA 篡改、HEAD 非法回退 | 非 READY；对应 3 子项 PASS |
| 分支漂移、Git 索引损坏 | 非 READY；对应 2 子项 PASS，索引损坏使真实 Git 命令失败 |
| Worktree 目录删除但旧 CreationSHA 仍在 DB | `unknown`、`live=false`；`TestM2F03SingleRepoDeletedWorktree` PASS |
| 验证器不可用、缺 worktree reader、缺 binding reader、只剩 Spawn intent | `unknown`；Gateway F03 测试在最终 execution 全包通过，不用 Fake 结果宣称真实 Git |
| 工作区入队后失效、恢复后 drain | 已接受 queued 保留，失效期间发送数不增，恢复后仅发送一次；真实 Git/SQLite/Scripted PASS |
| Queue / Steer / Resume / Reconcile / 重复 Bind 时失效 | 5 个 HTTP 子项均 409、driver 调用及资源数不增；恢复后 dirty 内容不变 |

### 5.3 失败归类与基线边界

Manager 完整包 **FAIL**。将旧 HEAD `a6c284a4d060431371306072e6c80abfedf22328` 导出到隔离临时目录后，在同一 Windows 工具链实跑：10 个当前失败中 **9 个同名同因复现，1 个未复现**。没有把旧 HEAD 的 PASS 或“没有该测试”误称基线失败。

| 当前失败测试 | 实际错误摘要 | 旧 HEAD 对照 |
| --- | --- | --- |
| `TestSpawnAsyncChat_PublishesStartChecklist` | 缺 `agent` checklist 写入 | 同因 FAIL |
| `TestChatStartsPassTheAOToolServerWithOnlyDaemonCoordinates` | MCPServers 为空 | 同因 FAIL |
| `TestInterfaceTransitionReservedTranscriptRequiresUntouchedTerminal/lookup_error` | `unsafe fresh handoff = <nil>`，未拒绝 | 同因 FAIL |
| `TestInterfaceTransitionNativeHistoryOwnership/claude-code` | `agent requires authentication`，4 个子场景 | 同因 FAIL |
| `TestSpawn_AfterStartPromptReservesCallerDeadlineForFallbackDelivery` | `deliver prompt/guard: context deadline exceeded` | 同因 FAIL |
| `TestSpawn_DefaultsBranchUnderDevNamespaceForDevDataDir` | `ao/mer-1/root`，预期 `ao/dev/mer-1/root` | 同因 FAIL |
| `TestSpawnAndRestore_PrependsResolvedBinaryAndNodeDirsToRuntimePATH` | 缺预期 Node PATH | 同因 FAIL |
| `TestSpawn_DoesNotAddNodeRuntimeForNativeBinary` | Windows 路径分隔符不符 | 同因 FAIL |
| `TestSend_NoEnterNudgeAfterTheOperatorTypes` | `Send calls = 3, want 1` | 同因 FAIL |
| `TestRunPostCreateUsesEachStepsTimeLimit` | 全包 `1.5886262s`、当前单例 `2.6891136s`，均超过 1.5s 断言 | **旧 HEAD PASS，不能列为已复现的历史失败** |

旧 HEAD 导出和完整日志位于证据目录的 `baseline-head/a6c284a-54de52f6188d/`；[baseline-comparison.md](C:/Users/11459/AppData/Local/Temp/ao-m2-round5-f03-20261105/baseline-head/a6c284a-54de52f6188d/baseline-comparison.md) 保存三条完整 regex 命令、离线环境与逐项日志行号。原 8 个 Manager 定向 exit 1 / 4.836s，补充 3 个 exit 1 / 13.238s，Chat 4 个 exit 1 / 5.270s；15 个请求的顶层测试均真实运行，15 个关键源码/模块文件归一化 SHA 全部匹配 HEAD。此对照只说明已执行的同名用例，不能宣称所有 M1 全套通过；没有为历史/环境失败扩大 F-03 修改范围。

PostCreate 补充诊断只真实执行当前单例一次，仍 FAIL：预期的脚本超时错误正确，但等待 Windows 子进程结束耗时 `2.6891136s`。该测试直接调用 `manager.go` 的 `runPostCreate`，不经过本轮 Worktree/Spawn 校验；完整 PostCreate 区段与 HEAD 一致，10 个相关测试/进程链/模块文件与 HEAD 和入场一致。Windows 同步 `taskkill /T /F` 本身有 5s 预算、`WaitDelay=1s`，测试的 1.5s 不是整个取消链硬上界；没有因此放宽断言。具体延迟触发因素仍未确定，不能把同时运行的 M2 race 直接当作根因。第一次命令在测试启动前因额外 `GOSUMDB=off` 与自动工具链校验冲突 exit 1；保留这次前置失败，改用已缓存 Go 1.27.1 的进程级 PATH/GOTOOLCHAIN 后才执行上述唯一一次测试，未改配置或依赖。

Controller 完整包 **FAIL（4 项）**：`TestListDirsRejectsRelativeAndMissingPaths` 返回 `404/FS_NOT_FOUND` 而预期 `400/FS_NOT_A_DIRECTORY`；`TestBridgeStatusConcurrentSecurePairing` rename `mobile.json: Access is denied`；`TestArtifactFileRouteServesOnlyRegularFilesInsideTheDirectory` 缺 symlink 权限；`TestProjectsAPI_Clone` 返回 `400 INVALID_GIT_URL` 而预期 201。Daemon 完整包 **FAIL（2 项）**：`TestStabilizeWorkingDirectoryChdirsToDataDir` 的 TempDir 被进程占用；`TestWiring_StartSessionSpawnsScratchWithoutGitRepo` credential rejected。它们与本轮早先及既有基线记录同名同因，不能记为包级 PASS。

Chat 最终完整包 **FAIL（4 项）**：两个 `TestHandoffHistoryFiles...` 创建 `.factory/sessions/C:` 失败；`TestRecordReportedArtifactSkipsWhatItCannotShow` 缺 symlink 权限；`TestSaveRenderAsArtifactWritesTheServedPage` 实际 0666、预期 0600。与首次全包及旧 HEAD 对照同名同因；本轮新增 F03/Steer/Queue/History 未列为失败。

前端全套失败包括 symlink 权限、Windows 路径/进程行为、日期/语言、界面断言。本轮未修改前端实现，但尚未在隔离旧 HEAD 重跑全部 10825 用例，因此不能将所有失败笼统标记为已证明的历史失败，也不能称前端全套 PASS。

中途 Controller/Daemon/Store 的 FakeGateway 夹具缺少显式 Repo 观察，被新门禁正确拒绝；已在保留业务断言的前提下补齐授权 Repo、pin 与 live HEAD。旧“显式单仓缺记录仍成功”的测试与修复要求冲突，保留缺行场景并改为更严格拒绝断言，另以真正无 RepoBindings 的场景证明 Legacy 成功，未删除缺陷用例。

### 5.4 格式、来源和文件保护

`gofmt -l` 检查本轮全部 34 个 Go 文件，exit 0、无文件输出；`git diff --check` exit 0，只有现有 `core.autocrlf` 的 LF/CRLF 提示，无空白错误。日志：`final-gofmt.log`、`final-diff-check.log`、`final-static-result.txt`。

全 M2 普通与 race 两轮验证前后，1475 个 backend 生产 Go/SQL 与 Go 依赖文件、12 个 M2 integration 测试文件 SHA 均一致。生产聚合 SHA256 为 `7b9fc87050e6bc2bee22c7b77568d41f6e0d6990eba761dd2851a3021da1b3f7`；`final-m2-source-before.json`、`final-m2-source-after.json` 及 race 两份 manifest 留存完整清单。之后只有报告和计划更新。

`snapshot_delta.py` 比较入场快照，exit 0，实际输出 `CHANGED_FILES=36; DELETED_ENTRY_FILES=0`；差异只包含 §6 的 34 个 Go 文件与 2 个文档，不含前端、配置或生成文件。HEAD 仍为 `a6c284a4d060431371306072e6c80abfedf22328`，分支仍为 `feat/multi-agent-desktop`，未提交。

## 6. 修改清单与规模

以下以入场 SHA256 比较，不将原有 M2 未提交文件认领为第五轮新增。共 36 个本轮变化文件，无入场文件删除，文件保护检查见 §5.4。

### 6.1 生产文件（12 个）

路径均相对 `backend/internal/`：

| 文件 | 变化 |
| --- | --- |
| `ports/execution.go` | `HeadSHA` 必须代表本次真实 Git 观察的语义 |
| `ports/chat_controller.go` | 进程内 `ValidateWorkspace` callback |
| `service/execution/local_worktrees.go`（新增） | 观察能力选择、持久化 Legacy 证明与缺能力闭锁 |
| `service/execution/local_gateway.go` | observer 接口、live 观察、失败清除可信 HEAD、Send/Steer/Interrupt/Resume 门禁及拒绝映射 |
| `service/execution/coordinator.go` | 显式完整 Repo 门禁、重复 Bind 重验、LiveTarget、BaseSHA 比对 |
| `session_manager/multi_repo_workspace.go` | 绑定识别、完整集合、真实 Git 身份/分支/历史/status 统一验证 |
| `session_manager/manager.go` | 启动、恢复、接管相关边界复用验证 |
| `session_manager/chat_spawn.go` | 为 Chat 注入验证回调，捕获不可变 SessionID |
| `service/chat/controller.go` | 新请求接受前与已接受消息派发前校验 |
| `service/chat/service.go` | Controller 装配、回调传递 |
| `service/chat/steer.go` | ack 后再次校验、fallback 保留已接受队列 |
| `service/chat/history.go` | 分支启动/恢复及失败补偿前校验，替换 Controller 保留回调 |

### 6.2 新增测试与对应生产检查

| 文件（相对 `backend/internal/`） | 行数 | 验收证据；删除何处检查会变红 |
| --- | ---: | --- |
| `integration/m2_f03_fixture_test.go` | 163 | 真实 Manager/Git/SQLite/HTTP 装配，不以脚手架本身为被测对象 |
| `integration/m2_f03_git_test.go` | 151 | 单仓 live HEAD、后继提交、dirty、13 个异常；删除 Repo/branch/history/status 校验即失败 |
| `integration/m2_f03_queue_test.go` | 112 | 失效时保留已接受队列，恢复后只发送一次；删除出队检查即失败 |
| `integration/m2_f03_workflow_test.go` | 219 | 工作树删除、SQLite Close/Open、generation fencing、Bind/Queue/Steer/Resume/Reconcile/Resolve；删除执行入口门禁即失败 |
| `integration/m2_fake_worktree_fixture_test.go` | 98 | 旧 Fake 的显式 observer 装配；固定假 HEAD 与历史 CreationSHA 分离，不宣称真实 Git |
| `session_manager/f03_live_worktrees_test.go` | 186 | 单仓、Creation/Base 历史、外置 Git dir、Legacy；删除识别或历史检查即失败 |
| `session_manager/f03_binding_worktrees_test.go` | 182 | 持久化预期集合、Spawn 意图、缺 reader；删除完整集合/归属检查即失败 |
| `session_manager/f03_recovery_worktrees_test.go` | 61 | 缺工作区时恢复入口不启动；删除启动前检查即失败 |
| `service/execution/f03_gateway_test.go` | 145 | 原始单仓反例、缺验证器/reader、每次操作重验；删除 observer 与单仓门禁即失败 |
| `service/chat/f03_dispatch_test.go` | 202 | 拒绝新 Send/Retry 不残留可执行行；已接受队列保留；环境准备后分支重验 |
| `service/chat/f03_steer_test.go` | 219 | ack 等待期间失效与 fallback followers；删除 reserve 前校验或误 Fail 旧队列即失败 |

新增测试及夹具 11 文件、1738 行，均 ≤300 行（夹具也 ≤400 行）。本轮涉及生产文件整文件合计 22080 行、测试文件整文件 12674 行，比例 0.574，低于 2；该口径含继承文件，不将旧文件全部行数说成本轮新增。

### 6.3 既有测试/夹具适配及超限申报

只补真实行为要求的 observer、授权 Repo/pin 与无绑定 Legacy 情境，保留业务断言。以下相对 `backend/internal/` 的文件在入场已存在；不得为了消除超限统计做无关拆分。

| 文件 | 最终行数 | 超过测试 300 行上限 | 适配原因 / 验收项 |
| --- | ---: | ---: | --- |
| `daemon/execution_recovery_test.go` | 794 | 494 | 恢复观察 Fake 提供显式 Repo / F03-04 |
| `httpd/controllers/operator_approvals_fixture_test.go` | 400 | 0（脚手架上限 400） | Operator 授权装配 / F-02 |
| `integration/m2_e2e_test.go` | 1707 | 1407 | 旧 Fake observer；由 1753 行缩减 / M2 全套 |
| `integration/m2_session_manager_e2e_test.go` | 1265 | 965 | 真实 Manager 工作流审计、重启 ID、防竞态事件屏障 / F03-04 |
| `service/execution/coordinator_test.go` | 2311 | 2011 | Repo gate、重放与 Legacy 夹具 / F03-03 |
| `service/execution/local_gateway_test.go` | 2262 | 1962 | live HEAD/缺 reader/Legacy / F03-02 |
| `service/execution/reanchor_test.go` | 719 | 419 | reanchor 使用有效观察 / F03-04 |
| `service/execution/repo_identity_test.go` | 47 | 0 | 缺行必须拒绝，真正 Legacy 单独证明 |
| `session_manager/workflow_context_test.go` | 251 | 0 | 持久化绑定恢复与回调验证兼容 |
| `storage/sqlite/store/m2_anomaly_test.go` | 713 | 413 | 异常与恢复 Fake Repo 观察 / SQLite 回归 |
| `storage/sqlite/store/m2_e2e_test.go` | 467 | 167 | Repo 身份与 live 观察 / SQLite 回归 |

另修改本文与 `docs/customization/m2-plan.md` 两个文档。无本轮数据库/schema/配置/前端实现改动。前端全套测试曾触发 `ScriptRunPanel.test.tsx.snap` 字节变化；仅在 HEAD 内容 SHA 与入场 SHA 一致后恢复该文件，未覆盖用户原有内容，证据为 `restore_test_snapshot.py` 与最终 delta。

## 7. 遗留限制与逐项关闭

- 第五轮独立结论仍为 NOT ACCEPTED；本次实施不等于 M2 PASS，未启动 M3～M6。
- 真实 Provider、原生人工审批、非 Windows 与安装包验收未执行。
- 任意外部进程可以在只读 Git 检查完成后继续改动磁盘；本次不声明 OS 沙箱或文件系统事务隔离。generation fencing 保护 Controller 所有权，不能替代操作系统隔离。
- 独立审查指出历史 `ActivateBranch` 的校验发生在关闭 idle 源 Controller 之后；原 Controller 已被要求无 pending turn、durable queue 为空，因此未见不可信 Provider 启动或已接受队列丢失，但失效工作区上的此操作仍可能停止 idle Controller。此恢复行为限制保留，不宣称所有副作用都发生在检查之后。
- 裸内部 `Manager.Spawn(cfg.Repos)` 若从未有 Workflow 持久化意图，再外部删除全部 worktree 行，当前模型没有剩余的显式 M2 标志。本次没有扩张 schema；生产 Workflow 先有持久化 intent，全删行场景已有闭锁测试。
- 前端全套和若干后端完整包当前 **FAIL**；Manager PostCreate 耗时失败和前端失败尚未全部归因，不得声明“净新增回归 0”。

### 7.1 第五轮缺陷与补充审查

| 编号 | 实施方复验状态 | 证据 |
| --- | --- | --- |
| F-01 历史 BaseSHA | 保持通过（真实 Git / 离线） | 历史双仓专项及最终全 M2；不以 live HEAD 代替 ExpectedBaseSHA |
| F-02 Operator CLI/IPC | 保持离线通过 | 最终 CLI/Consent 负向链、IPC 7/7、全 M2 FormalCLI；人工原生验证未重复 |
| F03-01 统一 1..N 个 Repo 验证 | 已修复，焦点及全 M2 普通回归通过，待独立复验 | 真实单仓/双仓 Git，Repo/Project/common-dir/HEAD/分支/history/status |
| F03-02 缺证据闭锁 | 已修复，焦点及全 M2 普通回归通过，待独立复验 | 原始负例最终 2/2 PASS；缺验证器/记录/目录均不 READY、无伪造 HEAD |
| F03-03 Legacy 区分 | 已修复，定向通过，待独立复验 | 绑定与 Spawn intent 识别；真实 Legacy 恢复与缺行反例 |
| F03-04 全执行链 | 已修复，焦点、全 M2 与 Race 通过，待独立复验 | HTTP 入口、queued drain、generation、SQLite 重开与 Resolve |
| 补充 P1：ack 后 Steer | 限定独立源码复核支持关闭 | `steer.go` 在 ack 后 reserve/claim 前重验；红绿测试 |
| 补充 P1：拒绝消息残留 | 限定独立源码复核支持关闭 | 新 Send/Retry 落库前校验；已接受 followers 保留；红绿测试 |
| 补充 P1：历史分支恢复 | 限定独立源码复核支持关闭 | 四个 Start/Resume 前统一校验；红绿测试；idle 源关闭行为限制仍见 §7 |

独立复核与父代理源码抽查在本轮修改范围内未发现仍可复现的具体 P0/P1；这不替代第六轮独立验收，也不证明全仓无缺陷。

## 8. 第六轮申请条件

| 条件 | 本次证据判断 |
| --- | --- |
| 单仓 F-03 实时验证、正向及全部异常 | 焦点 + 全 M2 普通回归通过 |
| 双仓原有测试 | Manager MultiRepo 与全 M2 普通回归通过 |
| Legacy 兼容 | 专项通过；完整 Manager 仍 FAIL，不能扩称全部 M1 通过 |
| Bind/Queue/Steer/Resume/Reconcile 等重验 | 焦点与全 M2 普通回归通过；已接受队列不丢失 |
| dirty 保留、SQLite 重启恢复 | 真实集成通过，tracked/untracked 内容逐字校验 |
| M2 集成全套 | **33/33 PASS，exit 0** |
| Race Detector | 全 M2 exit 0 / 578.724s；Chat 相关范围 exit 0 / 463.423s；Manager 恢复定向 race 通过 |
| Build / Vet | 两者 exit 0 |
| 前端及 API 契约回归 | API/sqlc/typecheck/IPC 通过；**前端完整套件 FAIL，条件未满足** |
| F-01 / F-02 | 真实 Git 与离线调用链回归通过；人工边界如上 |
| 没有新增 P0/P1 | 本轮限定复核未发现未修复 P0/P1；待独立验收 |
| 报告与计划 | 已更新，包含确切命令、原始失败、超限申报与最终判断 |

**目前不具备“所有条件同时满足”的第六轮正式申请条件。** F-03 可供针对性独立复验；完整回归 FAIL 及未归因项必须如实带入，不能凭定向绿灯宣告 M2 PASS。未启动 M3～M6。
