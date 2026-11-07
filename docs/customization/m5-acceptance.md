# M5 统筹独立验收

更新日期：2026-11-08。状态：**开发检查点收口 / 完整验收未通过**。由统筹维护；执行方 HANDOFF、静态代码存在和 Fake 测试不自动等于桌面验收通过。

## 基线与责任

- M4 已验收并提交：`8384bcc2a48d148e611f6d20b596de4eecd15a2d`，分支 `feat/multi-agent-desktop`。
- M5 执行会话：`01a1b01a-c5fb-7430-b43b-a0000dbeb9f7`。前一会话因服务并发限制退出，没有留下业务改动。
- 当前总并发为 2，保留统筹与一个执行会话；执行方不开子 Agent。禁止 `codexhost-delegation`。
- 实施依据：[M5 设计](m5-design.md)、[M5 计划](m5-plan.md)；实际契约以执行方交付的 `m5-contracts.md`、生成 OpenAPI 和源码为准。
- 执行方交接后冻结文件清单、HEAD与内容摘要，独立复核受影响验证。2026-11-08用户明确允许先跳过本人操作继续开发，按本次例外先提交开发检查点再启动M6自动工作；M5完整验收仍未通过，不宣称PASS。
- 开发期契约缺口记录在 [统筹审查与返修](m5-supervisor-rework.md)，最终验收前必须逐项取得修复/行为证据并关闭。

## 必须取得的行为证据

| 范围 | 验收观察 | 当前状态 |
|---|---|---|
| 双模式及原 Session | 从有草稿/对话/终端的 Session 进入工作台再返回，保留原状态；无额外 Spawn/Bind，任务能定位现有 Chat/终端/Diff | OFFLINE PASS：真实活跃离线 Chat 草稿、同句柄终端与合法预览往返保留通过；未增加创建/绑定请求，SV-09 已关闭 |
| 查询与事件 | 两个 Workflow/Task 切换、延迟回包、旧 CDC、断线重连后数据属于当前作用域；失联可见、未知事实不当作成功；后台刷新不反复/并行触发原生确认，用户取消不自动重试授权 | OFFLINE PASS：独立作用域/IPC/事件回归通过；真人只读授权、Renderer 重载后读取、真实调度事件自动刷新均已验，SV-01 已关闭 |
| 自动候选规划 | 目标通过现有受控 Session/Chat 路径和 Scripted/Fake Driver 形成候选；后端独立校验后确认持久 DAG；并发上限 1 时旧规划执行收尾，新任务经原调度获准且只 Spawn 一次 | OFFLINE PASS：冻结版本正式 HTTP/Manager/SQLite/Scripted 独立通过 |
| 计划边界与重放 | 循环 DAG、未选仓库、越界路径/权限、旧 revision 拒绝；重复请求不生成第二次计划或重复 Spawn；模型结果不能直接授予执行权 | PARTIAL：循环/仓库/CAS/幂等/旧 turn 独立通过；其他边界保留原相关包证据 |
| Stop 后重规划 | 仅可信停止结果允许采纳新计划；UNKNOWN/仍运行任务闭锁；容量、路径占位和预算按可信事实收尾，重启后仍可继续；旧 DAG/Attempt/Worktree/已验工件可追踪，已完成任务不重跑 | OFFLINE PASS：容量1重开数据库、收尾、状态漂移拒绝通过；非真实 Provider |
| Agent 选择与干预 | model/effort/profile 从 UI 到实际 Adapter 字段可追踪，不支持明确拒绝；Steer 按能力、Queue 用原幂等键，旧 Attempt 不投递到新会话 | PARTIAL：离线透传、HTTP Queue 与组件能力边界通过；真实活跃桌面待验 |
| 协作和 TaskRequest | 显示真实来源/目标和实际回执；物化受原 Operator 权限与资源约束，不能在 Renderer 自行扩展 DAG/预算 | PARTIAL：身份链HTTP和真实授权后的消息/TaskRequest读取通过；有副作用桌面操作待验 |
| 原生 Operator IPC | 固定动作 POST/PUT 与准确 method/path/body 贯穿确认、审批、最终执行；iframe/外部来源/过期批准/正文或 daemon 漂移拒绝；旧五动作回归 | PARTIAL：独立IPC、真实取消/403和真人Hello只读授权正向通过；写操作正向待验 |
| 质量/集成/预算 | 旧 Gate/Review 不当最新通过；逐仓 partial/unknown 原样显示；冻结 entries 不排序/改 null；预算未知不转零；未实现网络 Push 不展示为可用 | PARTIAL：SV-07 新冻结独立复测通过并关闭；原生授权后的桌面操作待验 |
| 可用性和既有路径 | 键盘导航、中文文案、加载/空态/错误 request ID、实际任务图/列表；既有 Session 路由及终端生命周期不回归 | PARTIAL：Renderer 键盘、真实 DAG、活跃离线 Chat/Shell/预览保留通过；受保护写操作仍待验 |
| 构建、测试和契约 | 相关 Vitest/E2E、受影响 Go 行为、生成漂移、TypeScript 和构建取得命令/退出码；历史基线失败逐项区分 | FAIL：完整build三次失败；完整Go套件非全绿，定向/类型/契约通过另列 |
| 隔离真实 Electron | 独立 worktree 与真实 npm ci；独立 data/run-file/dev-profile；确认实际窗口/Daemon/Renderer 后操作交付流程，截图并核实退出清理 | PARTIAL：第六轮已清理；第七轮真人只读授权、自动读取和预览通过，01:21清理后进程/监听为零；Queue需补齐离线测试装配后续验 |

## 证据规则和环境

早期独立观察（不作最终门禁）：统筹运行 `npm.cmd test -- src/main/operator-ipc.test.ts`，工作目录 `frontend/`，exit 0，1 文件/8 测试 PASS，Vitest 2.83s。日志 `.tmp/supervisor-m5-20261106/operator-ipc-baseline-native.log/.exit`。虽然日志初始命名为 baseline，运行时执行方已经修改 IPC/main 与测试，故不能称为 M4 原始基线；这是尚未冻结的 M5 IPC 早期检查。首轮 PowerShell `ErrorActionPreference=Stop` 把 Vite `MIXED_EXPORTS` 警告转为终止，未得到测试结果，保留 `operator-ipc-baseline.log` 和工具回执 `b16c55`（exit 1）；改用原生退出码后测试成功，没有修改构建配置或吞掉测试失败。

2026-11-07 只读授权改动后的独立定向检查：根目录 `npm.cmd --prefix frontend test -- src/main/operator-ipc.test.ts` exit 0，1 文件/10 测试 PASS，5.07s；backend 目录 `go test ./internal/service/principal -run '^TestWorkflowReadAccessScopedRefresh$' -count=1` exit 0，principal 包 4.561s。原始日志和退出码分别在 `.tmp/supervisor-m5-20261107/operator-readonly-independent.log/.exit`、`principal-readonly-independent.log/.exit`。前端保留既有 `MIXED_EXPORTS` 警告，原生退出码为 0。此轮未冻结整个 M5，证明有限期只读授权的单元边界与 IPC 模拟路径，不证明真实 Electron/Daemon、完整规划或重规划通过；SV-01 仍 OPEN。

记录每项的实际命令、工作目录、源码版本、退出码、原始日志/截图位置及有效范围。正式 UI/API 行为优先，不能用源码字符串断言、类型检查或截图静态外观替代业务闭环。仅在变化、失败或未解问题影响时重复相关验证。

M4 原始失败与环境差异见 [M4 统筹验收](m4-supervisor-acceptance.md)。Windows 沙箱中的 Git 初始化/清理权限问题要与业务断言区分；不能把未启动或 shell 启动失败但外壳 exit 0 记作测试通过。PowerShell 使用 `-NoProfile`、UTF-8 读取，npm 明确用可执行入口。

真实 Electron 验证采用 `ao-desktop-dev` 的启动、身份核对和精确进程清理流程，并遵守仓库要求的独立 worktree/scratch 数据。开发实例明确设置 `AO_DATA_DIR`、`AO_RUN_FILE`、`AO_DEV_ELECTRON_DIR`，不得继承真实用户数据路径或复用其他 checkout 的 node_modules。浏览器预览与 Electron 运行证据分列。

## 2026-11-07 续作检查点

执行会话上一轮明确报告 M5 未完成、不能 HANDOFF，已交付 `m5-contracts.md` 与 `m5-report.md` 检查点。统筹恢复同一 Astra 会话继续完成干预回执恢复等剩余工作；没有新开执行会话、阶段提交或启动 M6。

统筹原生缓存 Go 的 `version` 实跑输出 `go version go1.27.1 windows/amd64`，exit 0。限定两份语言文件的 `git diff --numstat` 实跑输出各 `124` 新增、`0` 删除，exit 0，没有基线内容删除。全仓 `git status` 长时间无结果，不能用它声称工作树已经冻结。

统筹独立短用例 `go test -p 2 ./internal/session_manager -run TestWorkflowRetirementWithoutShellCloser$ -count=1 -timeout 2m -v` 当前仅确认 Go 进程存在，尚无编译/断言输出；`-timeout` 尚不能证明测试已开始。上一轮集成验证包装进程也没有有效测试结果。上述等待不计 PASS，不冒充业务断言失败，四项返修仍需正式行为证据。

## 未授权与未验边界

- M0-05 真实 Provider 预算未批准；研发 Astra 会话不等于产品 Provider E2E 授权。
- 原生 Windows Hello 真人流程未测时明确 NOT_TESTED；consent stub 仅验证调用与权限边界。
- M4 Push 只有可信注入的本地 bare 模拟，生产网络 transport 未实现；不得把未实现与未运行混淆。
- 两项 Domain Handoff Note 历史失败、CLI Windows symlink privilege 失败保留；新失败先对照基线分诊，影响首版交付的问题须在 M6 处理。
- 本文件尚未形成整体阶段通过结论。M5 交付和统筹独立证据齐备后才能给结论、精确暂存、本地提交并开启 M6。

## 2026-11-07 安全续作后的执行方证据（非最终验收）

- 用户反馈磁盘负载后已停止执行回合，随后按用户明确续作要求恢复同一 Astra 会话。统筹只追加本机 Git exclude 排除 `/.tmp/`、`/backend/.tmp/`，规则实跑匹配；旧缓存保留。高 I/O 操作串行，执行方本轮 `GOCACHE`、`GOTMPDIR` 和测试 TEMP 均在 `C:/Users/11459/AppData/Local/Temp/ao-m5-safe/`，统筹已核对 realpath。详细取证见返修记录。
- 执行方 `go test -p 2 ./internal/session_manager -run TestWorkflowRetirementWithoutShellCloser$ -count=1 -timeout 2m -v`：原生 exit 0，PASS，包耗时 3.057s。原回执 `exec-1efed3ed-b51e-4c5a-b46b-97284147908c`。取代此前无输出挂起状态，但只覆盖 nil closer 分支。
- 执行方容量 1 正常流程一度 PASS（32.94s）；统筹审查发现其辅助函数串联服务，没有进入 Daemon maintain 或模拟重启/单项失败，已反馈补齐既定验收证据。
- 扩展 HTTP 覆盖后的 `TestM5PlanningCapacityOne` 返回真实失败：`GET .../interventions: 403 OPERATOR_APPROVAL_INVALID`、`approval_context_unbound`，exit 1（`exec-9544a978-01af-459c-a3ff-f3c62a1abc47`）。执行方已调整回执 GET 的审批中间件接线，复测结果待记录；早期 PASS 不能覆盖该失败。
- 执行方 `npm.cmd run api` exit 0（`exec-aa3ff8fd-1a1d-41c7-a857-4216d875215d`），重新生成 OpenAPI 与 TypeScript。随后 `npm.cmd run frontend:typecheck` exit 1，5 处类型错误，包含预算字段、可空观测、动态翻译键与测试桥接方法；正在返修。
- 所有证据来自执行会话的实际工具输出，统筹未并发另起编译或测试。M5 仍未 HANDOFF/冻结/独立验收/提交；SV-01～04 保持 OPEN，M6 未启动。

## 2026-11-07 16:44 后开发证据续记

- 执行方 `npm.cmd --prefix frontend test -- src/renderer/components/workflow src/main/operator-ipc.test.ts --maxWorkers=1`：3 文件、18 测试 PASS，11.19s，exit 0（`exec-ed07ae29-3048-40fa-b900-ed68404abc8d`）。包含明确取消与未知结果的不同恢复语义；真实 Electron IPC 仍待验。
- 执行方 `go test -p 2 ./internal/daemon -run TestM5MaintenanceRetainsUnconfirmedResources$ -count=1 -timeout 2m -v`：PASS 2.05s、包 2.262s，exit 0（`exec-4fb951c2-371b-4d94-8b2f-4f4d2d4120f5`）。实际进入 maintain，验证停止失败不阻断维护及未终止 Session 不释放占位；每轮重建服务/Leader，非完整进程重启。预期日志包括 stop outcome unavailable 和 invalid delivery artifact。
- 执行方 `go test -p 2 ./internal/integration -run 'TestM5PlanningCapacityOne$|TestM3LeaseRenewalAndRestartFence$' -count=1 -timeout 3m -v`：两项分别 PASS 31.99s、15.27s，包 49.909s，exit 0（`exec-e99e6642-7d7f-4d1b-859c-d53152008d73`）。统筹已读 `m5Restart`，确认关闭原 HTTP/Chat/数据库后打开同一隔离数据库；正常收尾仍由辅助函数调用正式服务，maintain 入口另有上述失败隔离用例。
- 执行方 `npm.cmd run frontend:typecheck`：exit 0，32.301s（`exec-fd185d7b-e533-495d-b426-4e60c85b6e17`），先前五项类型错误已复测通过。
- 此轮统筹只读采样 E 盘队列为 0、读写为 0；未与执行方并行运行测试。完整受影响 Go 套件仍在收口；M5 未 HANDOFF/冻结/独立验收。SV-01～05 保持 OPEN，M6 未开始。

## 2026-11-07 受影响完整 Go 套件失败记录

统筹只读检查执行方 `C:/Users/11459/AppData/Local/Temp/ao-m5-safe/go-affected.log`（UTF-16LE，约 1.34MB），尾部为 FAIL；不得用前述定向 PASS 替代。待执行方报告补齐原命令及工具退出码。当前可核实：

- controllers：`TestM2OperatorOperationsAllowlistIsClosed` 未替换 `{candidateId}`，包 FAIL 44.784s。执行方后续称定向修复 PASS，最终待复核原工具输出。
- cli：`TestRenderCheckReplacesItsEarlierScreenshot` 创建 symlink 返回 `A required privilege is not held by the client.`，包 FAIL 49.031s。环境权限限制保留，不能擅自修改系统权限。
- session_manager：`TestChatSpawnOmitsLegacyWorkflowLease` 返回 `workflow planner identity unavailable`，包 FAIL 446.640s，需本轮兼容修复及复测。
- integration：旧 Session fixture 返回 `trusted admission evidence is required`；旧 M2 claim 返回 500；是否早于本轮须对照 `8384bcc`。10m 包超时发生时 `TestM4ArtifactThroughRealManager` 运行 2m9s，栈位于 Git executable 的 Windows LookPath/文件属性查询；不能仅凭超时认定业务死锁。包 FAIL 600.264s。
- sqlite：`TestMigrationWorkflowCoreCreatesDurableTablesAndConstraints` 仍断言旧列，实际增加 model/effort/profile/planner；包 FAIL 165.071s。

执行方按根因分组修复和对照；统筹未并行启动复测。最新物理盘采样 E 盘队列 0、写入约 287KB/s。本阶段仍未 HANDOFF、未 PASS、未提交。

### 干净 M4 对照结果

执行方在 `C:/Users/11459/.codex/worktrees/m5-desktop-safety/agent-orchestrator/backend`、HEAD `8384bcc2a48d148e611f6d20b596de4eecd15a2d` 执行：

```text
go test -p 2 -parallel 2 ./internal/integration ./internal/session_manager -run '^(TestClaudeResumeHookConfirmsNativeIdentityForCurrentLaunch|TestM2CreateActivateClaimStart|TestChatSpawnOmitsLegacyWorkflowLease|TestM4ArtifactThroughRealManager)$' -count=1 -timeout 5m
```

工具 `exec-93688f11-54b7-4db2-950d-53fa2cd5bceb` 明确 exit 1，日志 `C:/Users/11459/AppData/Local/Temp/ao-m5-safe/go-m4-baseline.log`。统筹已读取：ClaudeResume 仍是 trusted admission evidence 错误，M2Create 仍为 claim 500；只能证明这两项具体失败基线已存在，不能自动归因其他同类用例。M4Artifact 单例运行4m57s时包预算5m耗尽，仍无通过结果。session_manager 包 PASS 3.280s，说明 ChatSpawnOmitsLegacyWorkflowLease 在M5的失败为本轮需处理的回归。

### Queue 修复后的完整容量 1 用例

执行方 `go test -p 2 -parallel 2 ./internal/integration -run '^TestM5PlanningCapacityOne$' -count=1 -timeout 3m -v`：PASS 60.18s，包 65.283s，exit 0（`exec-49ce50fe-6d16-4733-87d4-85ab43d3325a`），日志 `C:/Users/11459/AppData/Local/Temp/ao-m5-safe/go-m5-queue-final.log`。统筹已读新增 `m5VerifyQueue`：Operator Queue 经 HTTP 入真实 Chat/SQLite，既有 edit/cancel API 更新原 turn；读取回执分别为 queued/cancelled/delivered，Session 数未增加。该用例同时覆盖原容量 1 关闭/重开数据库和 Stop 后新 Planner 流程。前次加入两条 Queue 后沿用只允许一条回执的旧断言返回 FAIL，已改为按原 Stop ID 查验并复测。

同一复合命令前置 gofmt 使用了错误相对路径，报 `The system cannot find the path specified.`；本次 exit 0 只证明后续 Go 测试通过，不能证明该格式化步骤成功，已要求收口单独核实。SV-06 仍需前端/桌面与最终冻结版本验证，未关闭。

### 前端事件与 Queue 聚焦回归

执行方 `npm.cmd --prefix frontend test -- src/renderer/components/workflow src/main/operator-ipc.test.ts src/renderer/lib/event-transport.test.ts --maxWorkers=1`：4 文件、68 测试 PASS，40.80s，exit 0（`exec-5c4aed30-8df6-4165-acc0-1a79851ec67f`）。日志 `C:/Users/11459/AppData/Local/Temp/ao-m5-safe/frontend-workflow-event-final.log`；PowerShell 把既有 MIXED_EXPORTS stderr 包装为 NativeCommandError 文本，实际 Vitest 全通过且原生退出码 0。此证据覆盖组件/模拟桥接与事件传输测试，不代表 Electron 原生 IPC 或浏览器用例通过。

## 新规划边界的待补证据

统筹本轮检查 `backend/internal` 所有 `*_test.go` 中的 plan-candidates/replan 调用，并回读容量 1 用例：当前覆盖正常流程、越界 repo 拒绝、确认重放和重规划同键改正文冲突；尚未取得候选成环、准备后 revision/状态漂移的确认拒绝、UNKNOWN/仍执行任务不可停用、停止前旧 turn 不可采纳的行为证据。上述均为原 M5-04 验收要求，不是新增范围；已反馈执行方补齐或提供已有证据。旧 M3 恢复用例不替代新 Prepare/Confirm 入口验证。

## 受影响包复测的 socket 权限故障

执行方 `go test -p 2 -parallel 2 ./internal/service/execution ./internal/service/intervention ./internal/httpd/... ./internal/storage/sqlite/... ./internal/session_manager -timeout 10m` 在默认工具权限下返回 exit 1（`exec-6244088e-ac94-4161-9f21-6e4790e02b68`，146.905s）。日志 `C:/Users/11459/AppData/Local/Temp/ao-m5-safe/go-packages-final.log` 含本机 `127.0.0.1:63489–63498` 的 `connectex: An attempt was made to access a socket in a way forbidden by its access permissions.`；本次不计通过。执行方按已核实的命令和父子关系停止 Go/测试 PID 29000、33852、20820，统筹随后 `Get-Process -Id 29000,33852,20820 -ErrorAction SilentlyContinue` 无输出，确认原进程退出。

统筹只读运行 `netsh interface ipv4 show excludedportrange protocol=tcp`，排除范围不含上述失败端口；TCP 状态计数也不足以证明端口耗尽，未更改网络、防火墙或全局权限。执行方报告其工具 `require_escalated` 已经自动审查允许，随后串行重跑；HTTP 主包 PASS 8.719s、controllers PASS 57.643s，整组仍在运行。权限环境差异支持本轮连接错误与执行边界有关，不能推断整套通过或系统网络故障。统筹最新磁盘采样 E 盘队列 0、写入约 254KB/s，无并行测试。M5 仍未交接或验收。

后续完整包重跑最终为中断，exit 1、965.152s（`exec-e95fb729-badb-428f-84ab-4766c4a8fda2`）：SQLite 主包 PASS 212.084s、Store PASS 118.354s；Session Manager 没有最终结果。执行方核实测试子进程已退出，Go 主进程 37892 无子进程且日志长期无变化，精确停止该主进程后再次 CIM 核实 37892/37048 均不存在（`exec-e0e3744f-be9d-4cbe-836e-e87b9341933b`）。不得把本次中断写成整套 PASS，也不能据此认定 Session Manager 业务断言失败。

统筹只读取 Session Manager 的已知 `b569/testlog.txt` 首 1.2KB、尾 2.4KB；该 Go 缓存依赖日志约 49.95MB，包含真实测试临时库访问、PATH/PATHEXT 查找和末尾 Windows cleanup 用例文件访问。它证明测试已进入实际运行，不证明测试结论或缓存是唯一根因；未全量扫描该日志。当前受影响包的完整验证缺口保留，执行方先完成新增规划用例，后续验收不能用单项通过替代此缺口。

## 新规划边界的执行方实跑结果

`go test -p 2 -parallel 2 ./internal/integration -run '^TestM5' -count=1 -timeout 5m -v` 整体 exit 1（`exec-ffa447b8-e852-43d8-b6c0-b8a8cdaf1898`）：扩展后的 `TestM5PlanningCapacityOne` PASS 40.05s，包含成环、旧图 revision、停止前旧 turn 以及取消 Queue 后同键重放的拒绝/不重复副作用断言；状态边界用例因为读取确认前的 `confirmedRevision=0`，提前收到 `409 PLAN_STALE` 而失败。这一轮不能描述为两项全通过。

执行方将夹具改为使用正式确认回执后，单独运行 `go test -p 2 -parallel 2 ./internal/integration -run '^TestM5PlanRejectsChangedExecutionFacts$' -count=1 -timeout 2m -v`：PASS 10.71s、包 13.409s、exit 0（`exec-e49e0f94-ec3b-4272-b929-680725fdeff1`）。日志 `C:/Users/11459/AppData/Local/Temp/ao-m5-safe/go-m5-boundary-final.log`。统筹已回读断言：正式调度产生 running，准备后事实变化的 Confirm 被拒；仍运行任务不可停用；通过正式租约过期恢复得到 UNKNOWN 后 Prepare/Confirm 均拒绝；数据库不存在新增任务且 Session 数不增加。上述为开发期执行方证据，未替代冻结后的独立验收。

## 浏览器往返与队列验证

执行方 `npm.cmd run typecheck:e2e` exit 0。随后 `npm.cmd run test:e2e -- e2e/workflow-navigation.spec.ts --workers=1 --trace retain-on-failure --output C:/Users/11459/AppData/Local/Temp/ao-m5-safe/e2e --reporter=list`：1 passed，26.6s，exit 0（`exec-65a3523a-7dff-4c08-aeb4-97063ed2152e`）；日志 `e2e-navigation-ready.log`。首次因 Chromium 未安装未启动浏览器，安装到 C 盘任务目录后，首个运行仍因会话输入框 5 秒等待超时失败；明确等待页面就绪后复跑通过，不能把前两次记为 PASS。

此用例真实加载 Renderer，使用 FakeAgent/桥接响应，覆盖键盘选择 DAG、工作台 Queue、进入既有 Chat 编辑/取消、返回工作台刷新取消回执、重连、再返回原会话保留草稿/对话，并断言无 Spawn/Bind。统筹已查看 `e2e/workflow-navigation-工作台往返保留草稿，复用会话队列编辑取消并刷新回执-T0/workflow-session-return.png`，可见原对话和未发送草稿。截图和用例不证明原生 Main/Daemon/Windows Hello 或实际终端生命周期；隔离 Electron 仍待验。

## C 盘桌面副本完整性检查

执行方于 2026-11-07 18:05 生成 `C:/Users/11459/AppData/Local/Temp/ao-m5-safe/desktop-source-manifest.json`：M4 基线 `8384bcc2a48d148e611f6d20b596de4eecd15a2d`，从 E 盘主 checkout 向已有 `C:/Users/11459/.codex/worktrees/m5-desktop-safety/agent-orchestrator` 复制 backend/frontend 变更 89 个文件、4,562,324 字节。清单不含旧 `.tmp`、`.recover` 或数字后缀恢复残留。

统筹独立通过 Node `fs.readFile`、`crypto.createHash('sha256')` 和严格 UTF-8 解码逐一复核两棵树与清单，实际输出 `checkedFilesPerTree: 89, mismatches: [], invalid: []`；没有摘要不符、非法 UTF-8 或 NUL。此检查绑定复制时的开发快照；后续修改需核实同步，尚不等于阶段冻结或桌面运行通过。

`local_gateway.go` 格式化后，执行方更新 `desktop-dev-source-manifest.json`（18:19）；统筹再次逐一核对双树 SHA-256，仍为 89 文件、`mismatches: []`。

### 隔离安装及完整构建失败

C 盘工作树 `frontend` 的真实 `npm.cmd ci`：exit 0、38.943s，实际 `added 1446 packages in 37s`（`exec-20798b8e-cd62-4d21-9f19-6e4d27f74498`）；没有共享原 checkout 的 node_modules。

同目录 `npm.cmd run build` 三次结果均为 exit 1：第一次固定 agent-browser 0.38.1 下载 `fetch failed`（`exec-60cedd71-0a2d-47e0-9338-d05f2c40a344`）；进程级代理使资源准备通过后，第二次和第三次均在 Electron 打包报 `Client network socket disconnected before secure TLS connection was established`（`exec-d70560a7-e7a3-47bc-8dba-ba032626b1fa`、`exec-361487f4-745f-47ae-bb8a-774d3f1fd1e3`）。Main/preload/Renderer 的 Vite 构建有成功输出，但不等于完整 build 通过。原始日志为 `desktop-build.log`、`desktop-build-proxy.log`、`desktop-build-final.log`，执行方停止继续重复打包。

统筹用 Node `createRequire` 核实 packager 实际依赖为其内嵌 `@electron/get/dist/cjs/index.js`，使用 GotDownloader 和 global-agent；根目录另一个版本的配置不能直接证明它的代理已生效。执行方表示已检查内嵌版本且官方开关未消除故障。该证据用于分诊，尚未证实 TLS 故障根因，也没有放宽版本或校验要求。

### 开发版真实桌面观察（进行中）

`desktop-dev.log` 有 `Launched Electron app`、`daemon listening addr=127.0.0.1:3002 pid=30672`、Renderer `http://localhost:5173/`，以及 `GET /api/v1/workflows status=200`。统筹读取独立 `desktop/data/running.json` 的非敏感字段，pid/port 与日志一致。执行方观察到真实 dev 窗口、空工作台和创建表单；尚在取证草稿往返、完整隔离路径与退出清理。此时仍不能判定真实原生授权、已有 Session/终端或完整工作流通过。

后续统筹已查看 `desktop-draft-return.png/.txt`：真实 `Orchestrator.inc (dev)` 窗口里标题仍为“M5隔离桌面未提交草稿”，创建按钮禁用；没有项目或任务。`desktop-processes.json` 记录 Electron 主 PID 5888、其 daemon PID 30672，Chromium 子进程的 `--user-data-dir` 为 `C:/Users/11459/AppData/Local/Temp/ao-m5-safe/desktop/data/electron`。执行方 Ctrl+C 退出后，统筹独立 CIM 查询主进程、daemon 和三项已记录子进程均无结果，3002/5173 无监听，命令 exit 0；E 盘队列 0、读写 0。

`ao preview http://localhost:5173/` 实际输出 `ao preview must run inside an AO session (AO_SESSION_ID is not set)`，原生 CLI 退出码 2；外层工具 exit 1 不覆盖该输出。没有有效 Session，预览展示未完成。真实桌面已有 Session/终端、带任务工作台、Main→Daemon 原生授权仍未验，已反馈唯一执行方继续完成可自主运行的离线路径，不能以空态检查关闭上述门禁。

## Session Manager 完整包后续通过

执行方在已核对源码摘要的 C 盘工作树 `backend` 单独运行 `go test -p 2 -parallel 2 ./internal/session_manager -count=1 -timeout 10m -v`，最终 `PASS`、包 `519.114s`、exit 0（`exec-8e35201c-e4f3-487b-a4cb-51f31cdfff4b`，工具总时长 543.811s）。统筹已读取原生退出码和 `C:/Users/11459/AppData/Local/Temp/ao-m5-safe/go-session-manager-nocache.log` 末尾结果。该运行禁用测试结果缓存，保留 Windows 平台既有 POSIX skips；没有通过筛选仅跑少量测试。

此确定结果消除此前 Session Manager 整包 UNKNOWN，不改写那次中断的历史结论，也不推断缓存或 E 盘是唯一故障原因。完整 build 的 TLS 失败、整体 integration 历史/待分诊失败、真实桌面剩余交互和最终独立验收仍未关闭。

## 带任务的原版 Electron 离线检查（进行中）

执行方新增显式选择的 `TestM5DesktopFixture` 复用正式规划/收尾和现有离线 Driver，仅在新建目录生成持久数据，不计入自动回归或原生审批通过数。装配命令 exit 0，包 18.708s；统筹已核对 `m5-desktop-fixture-2168354748/fixture.json`：`schedulerPaused=true`、`sessionTerminated=true`，Session 为 `m2ff-project-a-1`，数据位于 C 盘任务目录。原版 Electron/Daemon 重开后，执行方观察到真实 DAG、任务导航及原规划对话。

真实 Shell Terminal 通过正式入口创建后，切到工作台再返回。统筹独立解析 `desktop-shell-before.json` 与 `desktop-shell-after.json`，两份结构完全一致：终端数量 1、handle `shellterm-762bc07f25359515`、Session/工作目录/createdAt 均相同；已查看 `desktop-shell-return.png`，原 Terminal 1 提示符仍显示。任务侧未发送草稿另有 `desktop-task-draft-return.png/.txt`。该证据覆盖原工作树的 Shell 保留，不证明已终止 Chat 的编辑器可用或活跃 Provider 生命周期。

在真实隔离 Session 中再次执行 `ao preview http://localhost:5173/`，CLI exit 0；但 Browser 面板随后显示 `Session ended — browser preview unavailable`，因此只算设置预览成功，实际展示仍未通过。执行方继续核查持续运行的既有离线 Provider 装配，以补活跃 Chat 草稿与预览。成功原生授权仍需单独核实 Windows Hello 条件，不能由离线 Driver 或旧测试批准替代。

为补活跃 Chat，执行方已编译显式隔离的 `M5-OFFLINE-FIXTURE` stdio Provider；其入口要求 `AO_M5_OFFLINE_PROVIDER=1`，不读取凭据、不联网、不执行工具，未知协议明确拒绝。第三轮启动前，正式 Daemon 的内存准入拒绝了 Chat：可用内存约 5.79 GiB，阈值要求高于 7.94 GiB，约差 2.2 GiB。执行方没有降低调度阈值，也没有继续启动；该项记为环境门禁阻断，等待释放内存后再验证。此前残留 pty-host 已按终端归属精确清理，最终 `desktop-fixture-cleanup-final.json` 为 `processCount=0`、`listenerCount=0`。

## 第三轮原生取消、身份拒绝与清理

统筹已读取执行方真实 Electron 的确认框及取消后 UI 文本：新规划标题 A 首次确认使用键 `4ac7384c-6f7d-444b-a051-a24fa46cc286`；取消后保留所有草稿，改为标题 B 再次确认使用键 `9344f3e4-5b65-43dc-97f2-db5f9757751a`，第二次取消后显示“操作已取消”并恢复创建按钮。证据为 `desktop-native-draft-a.txt`、`desktop-native-draft-b.txt`、`desktop-native-draft-b-cancelled.txt` 及同名截图。这里只操作业务确认取消，没有执行 Windows Hello。

`desktop-native-cancel-http.json` 汇总 853 条 HTTP 记录：审批 POST 为 0，Workflow 写请求为 0；唯一 Session POST 为之前容量拒绝的 500。`desktop-native-cancel-state-after.json` 保留 `paused=true`、调度 revision=2、Workflow/Session 各 1 项，证明取消没有恢复调度或创建新对象。四条受保护 GET 的原始结果均为 `403 / COLLAB_NOT_AUTHORIZED`，见 `desktop-offline-anonymous-reads.json`；不把未授权拒绝当作授权后的读取成功。

第三轮原启动句柄 58616 经 Ctrl+C 返回 exit 1；执行方最终 `desktop-offline-cleanup-final.json` 于 20:11:52 记录进程和监听均为 0。统筹再次用 CIM 查询该轮 13 个已记录 PID，并检查 3002/5173 监听，全部无结果、命令 exit 0。没有按进程名批量结束用户应用。

## 2026-11-07 冻结版本的统筹独立运行

基线仍为 `8384bcc2a48d148e611f6d20b596de4eecd15a2d`，`git rev-parse HEAD` exit 0。`desktop-final-source-manifest.json` 包含 91 个源码文件、4,571,163 字节，SHA256 `11d3b780734ff333062b539835ef68688071d54a94b0433084d715d01c7b4f0b`。统筹于 20:20:52 独立逐一核对主仓和 C 盘 worktree：摘要全部一致、严格 UTF-8 有效且无 NUL，记录 `supervisor-frozen-source-check.json`。`git diff --check` exit 0；保留既有 LF/CRLF 提示。

执行方本轮开发回合结束、隔离进程清零后，统筹在 C 盘 worktree 串行运行下列检查。Go 缓存/TEMP、npm 缓存及日志都位于 `C:/Users/11459/AppData/Local/Temp/ao-m5-safe/`，GOMAXPROCS=2，包并行度 1；未与执行方测试重叠。

```text
# backend；经 rtk proxy powershell -NoProfile 调用
go test -p 1 -parallel 2 ./internal/service/principal ./internal/daemon ./internal/integration -run "^(TestWorkflowReadAccessScopedRefresh|TestM5.*)$" -count=1 -timeout 5m -v

# frontend；前项已取得实际 exit 0 后执行
npm.cmd test -- src/renderer/components/workflow src/main/operator-ipc.test.ts src/renderer/lib/event-transport.test.ts --maxWorkers=1
```

Go 整组 exit 0：principal 包 2.112s；维护失败隔离用例 PASS 2.27s、daemon 包 5.831s；状态漂移拒绝 PASS 12.78s；容量 1 正式规划/Queue/重规划 PASS 51.78s，integration 包 64.805s。桌面数据生成 fixture 因未提供显式目录按设计 SKIP，不计自动回归通过项。维护用例的预期失败日志为 `stop outcome unavailable` 和 `invalid delivery artifact`。原始日志和退出码为 `supervisor-frozen-go.log/.exit`，原工具 session 91864 最终 exit 0。

前端实际 `4 passed / 68 passed`，26.05s，exit 0；日志 `supervisor-frozen-frontend.log/.exit`，原工具 session 49368。PowerShell 保留 Vite MIXED_EXPORTS 的 stderr 包装文本，不能据此覆盖原生退出码或伪造无警告。

以上证据关闭 SV-02/03/04/05。SV-01 的真实成功授权、SV-06 的真实活跃 Chat Queue 往返仍保留；完整 build 三次失败及完整 Go 集成未归因项均未放行。统筹已续派同一 Astra 会话补限定基线归因与下载故障取证，不第四次盲重跑 build，不改已冻结实现。M5 整体未 PASS、未提交，M6 未启动。

## 剩余人工与环境步骤

用户内存释放问题及 Windows Hello 配合时间已分别询问，尚无答复。第三轮实例已经退出；后续实际采样已显示可用内存 9,072,664 KB（约 8.65 GiB），高于 7.94 GiB 门禁，因环境条件已发生可验证变化，统筹续派同一会话先复查资源、再尝试活跃 Chat/预览。启动后若准入再次拒绝，停止该项并精确清理；不降低 25% 门禁、不关闭用户应用，不用历史终止 Session 代替活跃 Chat。原生成功授权仍等待用户本人配合，不因内存恢复而自动操作 Windows Hello。

首个真人操作为仅限测试 Workflow 的只读授权：`POST /api/v1/workflows/workflow-2fb604f2-2e57-496b-a7eb-d785d11c5ade/read-access`，空正文，30 分钟有效期。确认框与 Windows Hello 由用户操作；授权后验证受保护信息读取、自动刷新和重连不重复确认，token 保留在 Main。它不授权创建/恢复调度/发送干预；后续真实写入按准确 method/path/body 单独展示并完成原生验证。未取得实测前 SV-01/06 和相应阶段门禁保持未通过。

## 剩余 21 项 integration 失败的干净基线对照

执行方在新建的 C 盘 `m5-baseline-triage/agent-orchestrator` 核对 HEAD=`8384bcc2a48d148e611f6d20b596de4eecd15a2d`，随后仅运行初次完整日志中尚无基线对照的 21 个失败用例，`go test -p 2 -parallel 2 ./internal/integration -run <21项精确名称> -count=1 -timeout 3m -json`。原始选择和命令在 `integration-triage-selection.json`、`integration-triage-baseline-run.json`；结果日志为 `integration-triage-baseline.jsonl`，exit 1，包 5.309s。统筹独立解析 JSONL，实际 `selected=21, failed=21, missing=[]`，逐项错误见 `integration-triage-comparison.json`。

结合此前独立对照的 ClaudeResumeHook 和 M2Create，初次日志的 23 个断言失败均在干净 M4 复现。5 个补充 Session 用例与 ClaudeResumeHook 共用的旧夹具未装配可信准入提供器；`session_manager/admission.go` 正确返回 `ErrAdmissionRequired`。其余旧 M2 用例的 `openM2Store` 把现存空 `t.TempDir()` 登记为项目，未初始化 Git；`reservationRepoIdentity` 因无 `.git` 返回 `ErrArtifactInvalid`。统筹已读对应代码，执行方另用仅记录错误链的 C 盘 Go overlay 复跑 `TestM2BindIsDurableAndSpawnsOnce`，实际 `reserve and claim: invalid delivery artifact`、包 4.168s、exit 1。此 overlay 运行仅作根因诊断，不冒充干净基线；原仓库文件未修改。

`TestM4ArtifactThroughRealManager` 的独立超时仍未解决，本轮没有重复运行；当时完整包超时之后未执行的测试也没有通过证据。上述归因不等于修复或全套 PASS，已将具体夹具修复及完整回归要求补入未启动的 M6 计划，生产准入和仓库身份校验不得降低。

## 完整 build 的限定网络取证

执行方核对 packager 18.4.4 实际使用内嵌 `@electron/get 3.1.0`、Got 11.8.6，目标 Electron 44.7.0 win32-x64；根目录另装的 get 5.1.0 不代表此调用链。`build-download-inspection.json` 与 `build-download-inspection-host.json` 分别记录当前沙箱和主机环境：沙箱对应缓存无 ZIP，主机既有 ZIP 为 158,293,649 字节，校验清单缺失。不能把这些当前值反推第三次构建的全部环境。

`build-network-probe.json` 记录一次 15 秒上限、retry=0 的实际 GotDownloader 小校验文件 GET：官方代理开关开启、global-agent 已初始化且代理变量存在，仍返回 `ECONNRESET: Client network socket disconnected before secure TLS connection was established`。另一条 Undici 路径取得官方 6,740 字节清单，现有 ZIP 的 SHA256 与目标条目均为 `eee30dc8fa1f5ea95490e59f44e46ea68dd24c6e93d22facf70fe5c2d4c2665c`；agent-browser SHA256 也匹配既定清单。首次临时解析脚本未接受文件名前 `*` 标记而 exit 1，修正后只复用该 Undici 文件，没有重复大文件下载；首次 HEAD 结果未持久化，保持 UNKNOWN。

统筹已读取上述结果及真实 `validateArtifact`：下载 SHASUMS256 时显式使用 `ElectronDownloadCacheMode.Bypass`。因此补校验清单缓存不能单独解决完整构建；已验证 ZIP 也不等于 build PASS。当前证据确认 Got 下载路径仍受 TLS 阻塞，无法唯一确定第三次 build 当时失败于 ZIP 还是清单；没有降低 checksum、改依赖/版本/配置或运行第四次 build。下一次构建必须基于已验证的新条件，不能原样盲重试。

## 第四轮活跃 Chat 前置再验与安全停止

因实际内存恢复，执行方先连续三次采样，8.700/8.685/8.675 GiB 均超过原 25% 门禁；进程和端口为空，离线 Provider 的实际路径及摘要已核对，见 `desktop-active-preflight.json`、`desktop-active-fixture-identity.json`。首次启动请求在自动审批阶段超时且未执行；同一命令获准后仅启动一次隔离 `npm run dev`，未执行完整打包。

21:12:05 的正式 `GET /api/v1/scheduler/status` 返回 HTTP 200、`recovered=true, occupied=0, limit=0, reasonCode=RESOURCE_SOFT_LIMIT`；实际采样 availableBytes=8,231,665,664（约 7.67 GiB），低于门禁。启动前满足条件不能代替启动后的真实准入。执行方没有发送创建 Session 请求，没有再次尝试或改变阈值；仍只有原已终止 Session，`desktop-active-before.json` 留证。活跃 Chat/预览继续未验。

向本轮原 dev session 39051 发送 Ctrl+C 后工具 exit 1。最终 `desktop-active-cleanup-final.json` 于 21:16:47 明确 `processCount=0, listenerCount=0, spawnAttempted=false`。统筹独立查询 Electron 27976 及其 11676/24320/12580、daemon 10964 均不存在，3002/5173 无监听，exit 0；同期 E 盘队列/读写为 0。首次过宽清理查询匹配到已有 Codex 工具进程，经路径与时间核对后保留，未按进程名批量终止。

下一次活跃验收需预留桌面启动本身的内存消耗，等待明确资源变化再运行；不继续基于退出后短暂回升循环启动。Windows Hello 配合问题仍待用户答复。阶段状态不变：M5 未 PASS/提交，M6 未启动。

## Got 下载路径的进程级排障结果

2026-11-07 21:30～21:35（Asia/Shanghai），执行方只探测官方小校验清单，未再次完整打包。统筹独立读取原始结果及命令退出码：

- `rtk proxy node .../got-tunnel-diagnostic.cjs`：exit 1。GitHub 的 CONNECT 返回 200、TLS 1.3 握手成功且证书有效；跳转到 `release-assets.githubusercontent.com` 后 CONNECT 同样返回 200，但 TLS 握手返回 `ECONNRESET`，见 `got-tunnel-diagnostic.json`。这定位了本次失败环节，尚不能唯一归因于代理或远端。
- `rtk proxy node --tls-max-v1.2 .../got-official-checksum-probe.cjs`：exit 1，仍为 `Client network socket disconnected before secure TLS connection was established`。实际 GotDownloader、15 秒上限、retry=0，证书校验开启；见 `got-official-tls12-result.json`。TLS 版本限制没有修复问题。
- `rtk proxy node --require=.../build-existing-proxy-agent.cjs .../got-official-checksum-probe.cjs`：exit 1。仅在探测进程中使用已有 `https-proxy-agent` 的公开接口，未改依赖文件；真实 GotDownloader 在跳转后仍为相同 `ECONNRESET`。原工具回执 `exec-08963da3-263d-4117-9077-a096699314c2`，没有成功清单下载或 ZIP 校验的新证据。

同 URL 的本轮 Undici 对照返回 HTTP 200、6,740 字节，最终来源为 `release-assets.githubusercontent.com`，并通过已有 ZIP 的 SHA256 校验；统筹已读取 `undici-official-current-result.json`。因此不能断言整个网络或该地址不可用，但真实 Got 路径仍没有成功证据。

本轮不满足再次完整 build 的前置条件，停止扩大尝试，不能把其他下载器的成功当作该链路已修复。未修改版本、锁文件、仓库配置或全局代理，未关闭 TLS/checksum。统筹同期 E 盘采样为队列 0、读取 0、写入 0；该瞬时样本不保证后续负载。

执行方清理命令 `rtk proxy powershell -NoProfile -File .../build-network-cleanup.ps1` 返回 exit 0，21:37:45 的 `build-network-cleanup.json` 记录探测/桌面进程 0、桌面监听 0，`nativeBuildExecuted=false`、`desktopStarted=false`。`build-network-source-verification.json` 记录 91 源码的双树 182 份摘要一致、mismatches=0，原冻结清单摘要未变；统筹已读取这两份原始证据。

统筹同时修正 `design.md` 顶部状态和第 15 节入口，指向当前 M5 验收及阶段提交门禁，保留原产品设计与 M0-05 预算边界。仅文档检查 `rtk git diff --check -- docs/customization/design.md docs/customization/delivery-status.md docs/customization/m5-acceptance.md` 为 exit 0，入口新增链接均存在；未以此宣称产品验收通过。

## 用户释放内存后的续验与补充审查

用户在唯一执行会话的内存问题中回复“已释放”，新回合 `01a1b63d-d9a1-74e0-8517-2c43aa84d574` 实际 active。执行方三次预检约 21.67 GiB / 68.3%，隔离进程、端口及 E 盘队列均为空；本轮允许继续既定活跃 Chat/Session/预览验收。此新条件取代此前“等待释放内存”的状态，不代表 Windows Hello 已获真人操作，也不解除完整 build 的失败门禁。

统筹对照 M5-05 发现预算范围选择把 `global`、`workflow` 与合法 Task ID 混用，已记录 SV-07 并派同一执行会话。具体复现与关闭标准见返修文档；目前为源码确定的路径错误，尚无真实组件运行证据。执行方先结束当前冻结版本桌面取证并清理，再修复与复测；后续前端改动必须更新冻结摘要与受影响验证，不能沿用旧快照将 SV-07 标为通过。统筹侧未并行启动编译或测试。

第五轮启动后的正式 scheduler 返回 `RESOURCE_UNKNOWN`，可用内存约 18.48 GiB，已不属于此前低内存拒绝。执行方未创建 Session，并终止 dev session 52867，原退出码 1；`desktop-resumed-cleanup.json` 于 21:52:20 记录进程 0、监听 0、`spawnAttempted=false`，统筹已读取。活跃 Chat 仍未验，不能把停止清理当作功能通过。

统筹随后在正式采样与判定调用链定位采样前时钟误判采样完成时间的 SV-08，已派同一会话复现和修复。该问题与资源阈值无关；须保持未来/过期/失败样本的闭锁和既有恢复滞回，用 Service 回归与正式 Daemon 连续采样复核。本轮 UNKNOWN 的具体判定还需原始 Resources 字段，静态发现不得替代运行归因。

## SV-07/08 返修后的新冻结与执行证据

执行方在 C 盘副本复现 SV-07：真实 `WorkflowBudgetPanel` 选 `task: global`，上限应为 3、实为全局策略的 1，原生 exit 1。修复后同一用例通过，覆盖两个重名 Task 与 global/workflow 共四个目标的策略回显、Main 确认、审批 PUT 路径和原文正文，`sv07-green.log/.exit` 为 1 PASS / 7 SKIP、10.59s、exit 0。前置缓存 EPERM、脚本权限及乱码筛选导致全 SKIP 的运行均不计复现或通过。后续完整相关组件/IPC/事件为 69 项 PASS，最终 TypeScript 检查通过；详情以执行报告的实际命令和退出码为准。

SV-08 正式 Service 回归在原实现上返回 `RESOURCE_UNKNOWN`，包 4.641s、exit 1；扩展同一用例保留首样、恢复滞回、未来/过期/失败/间隔/不完整与重复样本行为后，经原实现 overlay 再次失败于合法样本，`sv08-service-red.log` 包 3.810s、exit 1。最终实现仅在采样后重新读取判定时钟，未改变阈值或失败样本处理；扩展用例 PASS 1.88s、包 4.100s，`sv08-service-green.log`。完整受影响 Scheduler 与 resources Adapter 包分别 PASS 8.778s、2.826s，`sv08-service-packages.exit` 为 0。

为观察原始 ticks，执行方使用仅增加诊断记录的 C 盘 Go overlay 启动隔离 Daemon：`sv08-raw-resources.json` 共 13 个样本，首样为 `resource_first_sample / RESOURCE_UNKNOWN`，随后合法样本为 READY，容量经三次健康样本从 0 恢复至策略上限 1；`sv08-trace-samples.json` 的 8 次 HTTP 响应与其一致。该运行只作诊断，不冒充未经修改的交付二进制。历史第五轮 HTTP 只投影了部分资源字段，没有保留 PreviousAt/CPU ticks，无法事后补出全部 UNKNOWN 的唯一归因。诊断实例已通过 `/shutdown` 退出；随后执行方在不含 overlay 的正式 Electron 中继续连续样本和活跃 Chat 验证。

新冻结清单 `desktop-sv0708-source-manifest.json` 取代旧清单用于后续验收，包含 93 文件、4,587,394 字节，SHA256 `0a3f37231692a3eb7879892cd6ed9a0c1c38a32672ea610d264ac2cede80ec8f`。相对旧冻结仅四个文件变化：`WorkflowBudgetPanel.tsx`、`workflow-behavior.test.tsx`、`service/scheduler/resources.go`、`resources_test.go`。统筹于 22:56:05 独立核对双树 186 份文件，`mismatches=[]`、严格 UTF-8/NUL 检查无误，记录 `supervisor-sv0708-source-check.json`。旧冻结前端与桌面证据仍注明原版本；SV-07/08 尚待统筹串行独立运行后关闭，M5 未 PASS/提交。

## 2026-11-07 23:05 新冻结与实际准入检查点

- 新清单 `C:/Users/11459/AppData/Local/Temp/ao-m5-safe/desktop-sv0708-source-manifest.json` 包含 93 文件、4,587,394 字节，清单 SHA256 `0a3f37231692a3eb7879892cd6ed9a0c1c38a32672ea610d264ac2cede80ec8f`。相对旧冻结仅预算组件及行为测试、scheduler 资源服务及测试四项改变。统筹双树逐文件核对 186 份内容一致，证据 `supervisor-sv0708-source-check.json`。
- 执行方新版本相关前端 69 项及类型检查通过；scheduler/resources 完整包通过，原始输出 `sv07-regression.log/.exit`、`sv07-typecheck.log/.exit`、`sv08-service-packages.log/.exit`。此为执行方证据，统筹未与桌面验收并发另起测试；SV-07/08 等待正式交回后独立复测。
- 统筹读取 `sv08-desktop-samples.json`：22:48:28～22:49:04 共 8 次正式无 overlay HTTP 200，均为 `RESOURCE_READY, limit=1`。`desktop-active-sv08-spawn.json` 23:05:15 显示 `m2ff-project-a-2` 未终止、Working、model=`offline-fixture`；调度 occupied=1、唯一新占位属于 codex。未调用真实 Provider，也未放宽资源阈值。
- 第六轮桌面仍运行；当前不能声明进程或监听为零。执行方继续草稿往返、同 Session 终端和预览验证，随后精确清理再交回。SV-01/06 尚无对应完整桌面证据，完整 build 与 Go 全套缺口继续保留；M5 未 PASS、未提交，M6 未启动。

## 2026-11-07 23:35 新冻结版本统筹独立复测

复测在第六轮桌面清理之后串行执行。`sv08-cleanup-final.json` 23:27:54 为 processCount=0、listenerCount=0；统筹另查 3002/5173 无监听。测试前后对主仓/隔离副本的 93 文件共 186 份摘要核对均无差异，证据 `supervisor-sv0708-before-tests.json`、`supervisor-sv0708-after-tests.json`。以下日志均在 `C:/Users/11459/AppData/Local/Temp/ao-m5-safe/`，无 E 盘缓存或测试临时目录。

| 工作目录与实际底层命令 | 实际输出 | 原始证据 |
|---|---|---|
| C 盘隔离 backend：`go test -p 1 -parallel 2 ./internal/service/scheduler ./internal/adapters/resources -count=1 -timeout 3m` | 两个完整包 PASS 5.415s / 2.225s，exit 0 | supervisor-sv0708-resources.log / .exit |
| 同 backend：`go test -p 1 -parallel 2 ./internal/service/principal ./internal/daemon ./internal/integration -run "^(TestWorkflowReadAccessScopedRefresh\|TestM5.*)$" -count=1 -timeout 5m -v` | principal 0.193s、daemon 6.253s、integration 68.248s，exit 0；容量1用例 PASS 51.01s、状态漂移用例 PASS 13.19s；桌面fixture生成 SKIP，不算 PASS | supervisor-sv0708-m5.log / .exit |
| C 盘隔离 frontend：`npm.cmd test -- src/renderer/components/workflow src/main/operator-ipc.test.ts src/renderer/lib/event-transport.test.ts --maxWorkers=1` | 4 文件、69 测试 PASS，32.38s，exit 0；既有 MIXED_EXPORTS 警告保留 | supervisor-sv0708-frontend.log / .exit |

通过以上新鲜证据关闭 SV-07/08；原 SV-02/03/04/05 结论继续有效。SV-01/06 未关闭，预览新增 SV-09：正式 Session 有 previewUrl/revision，但 Browser 空态，需继续排查。

第六轮活跃草稿前后截图和正式终端前后快照由统筹复核：保持同一 Session `m2ff-project-a-2` 和句柄 `shellterm-c6ef444f86f67eee`。清理正式 DELETE 终端返回 204，POST Session kill 返回 200/freed=true；之后两 Session terminated、shellTerminals=[]、调度 occupied=0。这些结果不证明离线 Provider 正常完成，也不代替真实 Workflow 授权写入。完整 build 和 Go 全套既有失败不被本轮定向 PASS 覆盖，M5 继续未验收、未提交，M6 未开始。

## 2026-11-08 真人授权尝试

用户在统筹会话明确回复 Windows Hello“现在可以配合”，并在执行会话明确普通测试可以自行点击。唯一执行会话优先发起既定测试 Workflow 的 30 分钟只读授权；该授权不扩展为其他 Workflow 或写操作。此前内存问题的迟到回复“暂不释放，保留该项未验”保留为旧轮次决定；当前正式资源 READY 并有已取得的活跃草稿证据，不调整门禁或要求用户再次释放。

第七轮正式 Electron 已运行，daemon PID 32360、Electron 主 PID 33100，scratch 数据边界保持。运行日志 `desktop-sv09-dev.log` 记录两次审批创建 HTTP 201，确认分别在 00:12:50 和 00:14:16 返回 HTTP 403，耗时约 60 秒。当前未取得成功授权；日志仅有 HTTP 状态/耗时与 Main 的通用错误，详细 envelope 尚待执行方核对，不能仅凭耗时确定唯一根因。SV-01 仍 OPEN；执行会话已提示本人取消残留 Hello 窗口并确认准备后再发起，不自动循环。

另：原离线 Session a-2 恢复 API 返回 409，正式日志为 `thread/read: app-server error -32601: not implemented by M5 offline fixture`。这是离线 fixture 未实现恢复协议的证据，不据此改产品准入或认定产品恢复缺陷。合法离线预览目标已准备，SV-09 保持待验。

### 00:35 真人新请求成功

第三次确认在 00:31:25 仍为 HTTP 403，执行方按约定停止自动重试。随后用户本人新发起并完成 PIN 验证；统筹独立读取持续运行日志：00:35:09 审批创建 201，00:35:18 确认 200（9.2395991s），同一 Workflow 的 read-access 200、coordination-summary/messages/task-requests 均 200。记录请求ID后缀为 001062、001063、001070～001073。该证据更新“真人正向未验”为“单次真实授权及受限读取已成功”；SV-01 的页面往返、刷新和重新连接仍在验收，尚未关闭。先前失败保留，不误认用户未操作或直接判定OS不可用；没有自动第四次盲重试。

00:42:09 重载后重新进入原 Workflow，coordination-summary/messages/task-requests 再次返回 200，请求ID后缀001295～001297，未新增审批请求；统筹已独立读日志并检查授权/重连截图。此为Renderer重载后的授权沿用证据，不能当作事件触发自动刷新；后者继续由执行会话取证，SV-01尚未关闭。

## 2026-11-08 01:13 合法预览与往返独立核验

正式 `ao preview` 对活跃离线 Session `m2ff-project-a-3` 内的 `m5-preview.html` 返回原生 exit 0，证据 `sv09-active-preview.exit`。统筹独立检查往返前、Workflows 中、返回后三张截图及可访问树：Browser 实际展示该 Session 的 HTML，返回后的输入值仍为 `SV09-往返保留-20261108`。对应时间区间 01:06:15～01:10:57 的 435 条 HTTP 日志中，写请求只有 28 次 chat-view 与 2 次 readiness/ensure，没有创建或绑定请求。

01:13:21 双树93文件共186份摘要仍与 `desktop-sv0708-source-manifest.json` 一致，mismatches=[]；原始记录 `supervisor-sv09-preview-check.json`。SV-09 关闭，验收目标修正后无需修改业务源码。第七轮实例仍在运行，后续清理尚未发生；SV-01 自动刷新证据和 SV-06 Queue 桌面闭环继续由唯一会话核实，M5 未整体 PASS、未提交。

## 2026-11-08 01:17 自动读取链路独立核验

统筹独立读取持续运行 HTTP 日志、`sv09-final-runtime-evidence.json` 的只读 SQLite CDC 记录和执行会话操作记录，并核对实际接线：`ShellOutlet` 隐藏后保留 WorkflowWorkbench 挂载；两个受限 Query 没有轮询，QueryClient 关闭 refetchOnWindowFocus；event-transport 监听 scheduler_updated 并失效 workflows 作用域。00:59:31 的会话创建动作产生 scheduler_updated（seq321/323）；00:59:34 的正式准入更新再产生 seq332。00:59:36.068～.070 的 coordination-summary/messages/task-requests 全部 200（请求ID后缀001895～001897），此时唯一 UI 操作仍为先前创建会话，没有另点工作流、重载或刷新。

结合 00:35:18 真人授权、00:42:09 Renderer 重载后成功读取和本次事件自动读取，SV-01 关闭。各批次间无新增审批请求，自动 Query 不发起 Hello；未重新输出或转移 Main 凭据。结论限本次 Windows 离线桌面与30分钟作用域授权，不替代写操作、完整断电重启或真实 Provider 验收。SV-06 仍缺绑定工作流 Attempt 的 Queue 桌面闭环：当前 fixture 任务为 worker-model/high，与离线能力不一致，普通a-3不能代替。已要求唯一执行方先正常收尾本实例，再准备可执行的最小续验条件，不绕过生产准入或直接改活跃数据库。

第七轮收尾：正式 `POST /api/v1/sessions/m2ff-project-a-3/kill`（deleteArtifacts=false）返回200；原dev工具句柄77080收到Ctrl+C后exit1，按中断结束记录，不当作构建失败或正常exit0。`sv09-cleanup-final.json` 于01:21:09记录processCount=0、listenerCount=0；统筹独立查询原PID33100/32360/1960和3002/5173均无结果、命令exit0。SV-06下一轮仅在该清理前提上准备合法测试装配。

## 2026-11-08 01:33 Queue 续验准备

唯一执行会话只调整 `backend/internal/integration/m5_desktop_fixture_test.go` 的待执行任务模型为 offline-fixture，effort留空，匹配现有离线协议目录。正式服务、审批、资源门禁和能力规则未变；旧数据保留。C盘指定命令 `go test -p 2 -parallel 2 ./internal/integration -run "^TestM5DesktopFixture$" -count=1 -timeout 3m -v` 为exit0，用例21.50s、包29.590s，日志 `sv06-fixture.log/.exit`。它是数据生成装配器，不计 Queue 功能通过。

新scratch为 `C:/Users/11459/AppData/Local/Temp/ao-m5-safe/m5-desktop-fixture-2231360002/data`；只读核实工作流 `workflow-d3867d27-4231-462d-adc5-85ae8408d9a1` 的scheduler paused=1、revision=2，desktop-worker=ready、model=offline-fixture、effort为空，旧Planner已终止。离线codex二进制SHA256仍为 `5f5e8a5538508bfb0bbe5718d10768a8bd348cc66da80eda76a7153177c207aa`。详见 `sv06-prepared.json`。

统筹独立核对新冻结93文件双树186份摘要一致，源码实际合计4,587,579字节；只有上述fixture变化，记录 `supervisor-sv06-source-check.json`。初版新清单该条字节/行数元数据未同步，已要求校正，内容摘要无差异。准备下一次本人Hello的准确请求为 `POST /api/v1/workflows/workflow-d3867d27-4231-462d-adc5-85ae8408d9a1/resume-scheduling`，正文 `{"expectedRevision":2,"reason":"用户调整工作流调度"}`；执行会话已发出唯一操作提示。尚未启动下一轮桌面或执行该请求，SV-06保持OPEN，M5未整体PASS/提交，M6未启动。

01:38补记：执行方已校正新清单唯一fixture条目的bytes=4971、lines=100；统筹独立重读清单，93文件合计4,587,579字节，最终清单SHA256 `a89b63f2404a271bef22f89c6056267552dda7a94379e111f4757254bbd7df30`，源码摘要不变。m5-report/contracts已补本轮证据和本人操作前置。01:33:15～01:33:37预检内存15.865/15.783/15.812GiB、E盘队列0、进程和监听为空；这是启动前样本，延后续验仍需重新核查，不能代替正式Scheduler准入。

## 第八轮启动工具阻塞

用户在唯一执行会话明确回复“已准备”。执行方尝试既定 `desktop-sv06-start.ps1` 时，自动审批两次超过截止时间，命令均未执行；默认沙箱尝试随后返回 `AuthorizationManager check failed`。未发起恢复调度、Queue或Hello，不记为产品运行失败。执行方已提供同一已准备脚本的手动启动命令并保留一个“已启动”待答问题；统筹不代跑被拒绝动作。脚本仍使用C盘独立AO_DATA_DIR/run-file/dev-profile/缓存、离线Provider，检查既有实例和3002/5173监听后才执行dev启动。收到用户启动反馈后由同一会话继续正式资源及版本核对；本轮不重复已关闭项或完整build。

## 用户最新决定与开发检查点收口

2026-11-08用户明确：“能不能搞快点，老是要我自己操作，能不能自己过，不行先跳过继续开发。”统筹据此停止等待手动桌面启动和Windows Hello，SV-06真实桌面Queue标NOT_TESTED/DEFERRED。保留已通过HTTP/组件回归和SV-01/SV-09的真实桌面证据，不绕过本人认证、不删除或下调生产门禁。

本次按明确例外先对已交付代码及证据做本地开发检查点提交，再派发M6可自动完成的回归修复、稳定性和交付准备。完整build三次失败、完整Go既有失败及TestM4ArtifactThroughRealManager超时均继续列为未通过；开发检查点不等于M5整体验收PASS或正式发布。

提交前运行态：统筹独立只读查询CIM隔离checkout可执行路径与3002/5173监听，2026-11-08 01:56:44为processCount=0、listenerCount=0，原命令exit0。记录 `supervisor-m5-final-runtime.json`；执行会话先前权限失败的空结果不计证据。未代跑被拒绝的桌面启动动作。

执行报告的早期“最新/未HANDOFF/不进入M6”等文字属于记录时点；本次执行方收口文档操作受工具权限限制，统筹已要求停止重试。阶段决策以本文件及delivery-status.md的2026-11-08用户例外为准，完整失败记录保留。提交范围已独立核对为93个源码/测试/生成文件及11个阶段文档，共104文件；源码集合与新冻结逐项一致，无配置或运行态。

M5执行会话已正式HANDOFF并idle，回合完成；没有新的并行执行者。暂存后首次 `git diff --cached --check` 实际失败：WorkflowTaskPanel.tsx第42行new blank line at EOF。统筹仅删除末尾一个空行，双树同步，无业务变化；随后同命令exit0。93文件提交前双树再次一致，冻结 `m5-commit-source-manifest.json` SHA256 `3325d2179510b78d6ed44da50d54e76afcaf1fc4391263261c67f091664036ee`；相对上一冻结仅此空白变化，不重复功能测试。暂存104路径与明确清单完全一致，无多余/遗漏。
