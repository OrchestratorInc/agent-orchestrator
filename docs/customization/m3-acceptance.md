# M3 独立验收记录

审查日期：2026-11-05。工作区：`E:/code/agent-orchestrator`；分支：`feat/multi-agent-desktop`；HEAD：`8b7d7901b52df1964babe233df3a4e27e7523c47`，包含未提交实现。独立审查模型：`gpt-6-astra`。

实现保留了 Scheduler 授权、Coordinator 绑定、Manager 启动的职责边界。准入、三级预算与 Claim 使用同一事务；UNKNOWN 保留占位；观察既有 Host 与取得业务执行权分开。独立反例和最终完整回归支持这些结论。

**最终结论：G7 在本报告限定的 Windows 离线范围内 PASS。** 启动重附着 P1 已关闭，公平性测试前提与 HTTP 混合时钟问题已修复并复验；本范围内没有未关闭的新增 P0/P1。独立完整 M3 为 6 项 PASS，完整 M2/M3 普通与 race 各 39 顶层 / 58 含子项 PASS。此结论不等于全仓、真实 Provider、完整桌面或两个完整 daemon 的实机验收；额外全 domain 检查仍有两项已同机复现的历史 FAIL，详见下文。

## 1. 证据与冻结边界

证据根 `R`：`C:/Users/11459/AppData/Local/Temp/ao-m3-implementation-20261105/`。下文 `I` 指 `R/takeover-independent-astra/`，`T` 指 `R/takeover-final/`，`O` 指早期 `R/m3-acceptance-independent-astra/`。每组测试保留原始 JSONL、stderr、退出码及命令；新 runner 还保存源码前后 SHA256。

- 独立重算 `R/final-source-snapshot.json`：2939 文件，快照 SHA256 为 `cf55c6cb93cc0fd373e23e3bf5225d466ed1d6d276af356c48763cd9863b67af`，与当前源码差异为 0。
- `I/final-evidence-audit.json` 直接重读主控 JSONL 和 `.exit`，顶层、含子项、包级结果分别计数；最终命令计数和退出码与 metadata 一致。未把重复运行相加成独特测试总数。
- 独立复算 `T/final-review-evidence.json` 中 237 项保护文件当前哈希：与入场证据相同。48 个生成文件为 46 个 sqlc Go 文件及 OpenAPI/schema.ts，生成前、生成后、当前哈希一致。
- 完整 core/SQLite 与 Manager 执行之后，只变化了 HTTP 的 `api.go`、`controllers/workflows.go` 和三个 integration 测试文件；其受测包及生产依赖没有后续修改。HTTP、完整集成、build/vet 已在最后时钟修复后重跑，执行后到最终快照差异为 0。
- Chat 完整包在接替阶段运行，之后 Chat 源码未改；后续 legacy 观察许可修复另由完整 Manager/core、独立 startup 普通/race 及最终集成覆盖，不将早期 Chat 单项结果当作后续 Store 修复的证明。

本审查只更新本文件及授权临时证据目录，没有修改生产代码、正式测试或配置，没有提交、推送、合并、部署，也没有运行真实 Provider。M2 仍沿用 [第六轮独立离线 G0](m2-acceptance-round6.md)，没有另开第七轮。

## 2. 计划对齐与阶段判断

| 闸口 | 核验依据与结论 |
| --- | --- |
| G0 准入 | 第六轮离线 G0 已通过；保留入场修改与真实 Provider 禁用边界。 |
| G1 事务模型 | [G1/G2 独立报告](m3-g1-review.md) 的双 Store、重放、迁移与准入反例，加最终完整 core/SQLite；资源、预算和 Claim 不拆成可竞争的多次提交。 |
| G2 调度决策 | 默认全局 3、最大 10、Adapter 默认 2、CPU/RAM 闭锁与恢复、公平选择由真实 Manager/Git/SQLite 集成复跑；修正的是公平用例容量前提，未修改生产公平算法来迎合断言。 |
| G3 用量/预算 | [预算独立报告](m3-budget-review.md) 及最终 Store/集成证明 canonical 费用不重复计入、三级是投影、未知费用不为零、pending correction 与未完成来源保留预留。 |
| G4 生命周期 | 本次启动普通/race、legacy 观察许可、真实 maintain 恢复、自动 Start 桥接及重启集成通过。旧 epoch、过期 Attempt、UNKNOWN 均不获新执行权。 |
| G5 安全控制 | 最终 HTTP 全包、完整 M2/M3 及物化集成通过；Operator 一次性批准、Agent 本实例 Fence、独立物化批准仍闭锁。API/schema 与生成结果一致。 |
| G6 综合验证 | 指定核心服务/SQLite、Manager/Chat、HTTP/daemon、完整 M2/M3 普通/race、build/vet、API/SQLC、TypeScript 已实跑；历史全 domain 失败单列，不宣称全仓通过。 |
| G7 独立验收 | 本审查独立重跑真实临时 Git/SQLite/Manager/HTTP 的 6 项 M3、启动与桥接反例，并核对全部最终原始证据；按本报告边界 PASS。 |

计划中“两 daemon/不同进程只产生一个 Claim 和 Session”采用拆分证据：正式用例启动两个独立测试进程争抢 SQLite，只产生一个 Claim/Admission；新任期发现原执行结果未知后保留 uncertain，Session 为 0。唯一 Session 的真实 Manager 链路及生产 dispatch/maintain 桥接分别验证。**未运行两个完整 daemon 的端到端单 Session 场景**，主控已确认并在实施报告明示，不能把拆分证明写成该实机场景通过。

## 3. 已关闭的问题与反向证据

### P1：启动任期门禁使既有 Host 永久无法重附着

原启动顺序是 Scheduler 等 `ReconcileBackground` 完成，而 `ReconnectOnly` 误走新执行准入，需要尚未 recovered 的本实例 Fence。该错误未进入后台重试，导致已有可重连 Host 永久 unavailable。`O/startup-confirmed` 真实业务断言 exit 1：

```text
retrying=false error="scheduler leadership fence is stale or unrecovered"
checks=0 readiness=unavailable retryable=false
after recovery manual reconnect-only succeeds
```

修复链为 `session_manager/admission_reattach.go`、`storage/sqlite/store/execution_reattach_store.go`、`domain/execution_permit.go`、`startup_health.go`、`chat_spawn.go`。观察许可约束本实例 Fence、原 Session/Binding/Admission 快照与 generation；提交重连 generation 时执行事务 CAS。非 live 或不同 Provider 的重连回执仅关闭观察连接，不终止既有 Provider。`ErrSchedulerFence` 进入原有有界重试，不绕过普通新增执行门禁。

M2 无 Admission 的既有 Workflow 只经 `loadReattachExecution` 构建归属匹配的观察快照；`loadExecutionPermit` 仍拒绝其业务许可，不新建 Admission、预算或 Provider。原 legacy 用例修前返回 `trusted admission evidence is required`，修后归属匹配通过，错误归属和业务执行仍拒绝。

本轮 startup overlay 使用真实 Chat Service 的 generation 提交协议。正式 6 项 startup 加独立原例，普通/race 各 7 顶层 / 11 含子项 PASS；临时只移除 `ErrSchedulerFence` 重试分类后，原例准确失败：`readiness=unavailable reconnects=0`，exit 1。生产文件未变异。早期 Host 替身未完整履行 generation 协议，其旧 PASS 不作为修后结论。

另以真实 Manager/Chat/LocalGateway、无 Controller 的旧 BoundReady 数据执行生产 `m.maintain`：新 Leader 从 unrecovered 自行完成 `RecoverAdmissions → Budget → CompleteSchedulerRecovery`，旧 Admission 保持 uncertain，过期 Attempt 为 UNKNOWN，Session 数仍为 1；随后真实 `PrepareSessionReattach` 成功。没有预先手动标记 recovered 来掩盖循环依赖。

### 生产自动 Start 桥接

原集成 helper 是人工 HTTP Start 后 Bind，不能单独证明生产 Bind 后周期观察再 Start。本审查 overlay 直接执行 `m.dispatch`、`m.maintain`，使用真实 SQLite/Scheduler/Workflow/Coordinator；FakeGateway 的 Session 副作用经真实 `CreateAdmittedSession` 落库。

provisioning 或观察读取失败时 Attempt 保持 claimed；观察 ready 后才 running；Gateway Spawn 始终一次；退出观察不自动 Settle。仅在临时 overlay 删除 `maintain` 的 `startReadyAttempts` 调用，原例报 `got=claimed want=running`、exit 1。冻结后的 `I/bridge-green` 再次通过。该反例验证 daemon 桥接，不冒充真实 Manager/Git 或 Provider；真实 Manager/Git 由完整 M3 集成覆盖。

### 公平性用例前提不足

第一轮独立完整 M3 为 5 PASS / 1 FAIL：`TestM3ContendingSchedulersAndFairRotation` 得到 high=3、low=0。`T/fairness-diagnostic-02` 证实策略虽然调至 10，有效资源容量仍因滞回处于 3、4；旧测试还依赖随机 Workflow ID 决定先后，未给 5:1 轮转足够名额。

正式用例补充真实 `RefreshResources` 与 `MaintainLeadership`，先断言有效容量到 10，再固定高权重先执行的游标，最终加强为 high=5、low=1。`T/fairness-fixed` 与本轮完整 M3 均通过。此处属于测试前提修正，不能把原失败删掉，或归为已证明的生产公平算法缺陷。

### HTTP 与受控时钟混用

公平修后独立完整 M3 仍出现容量项 FAIL；主控完整 race 同项返回 409 `SCHEDULER_FENCE_STALE`，无数据竞争诊断。Scheduler/Workflow/Manager/Approval 使用受控 Clock，HTTP `executionPermit` 却用墙钟。独立 overlay 只将初始测试时间向过去偏移一分钟，保留全部容量断言，确定复现：

```text
fixture clock=2026-11-05 12:47:19.8787134 UTC
wall clock=2026-11-05 12:48:16.2886875 UTC
leader until=2026-11-05 12:47:44.8787134 UTC
fake load=<nil>
real load=scheduler leadership fence is stale or unrecovered
start: 409 SCHEDULER_FENCE_STALE
```

`WorkflowsController.Now/now()` 经 `APIDeps.WorkflowClock` 与 fixture 同源，统一许可、能力过期和审计时间，未注入时仍使用真实 UTC。正式 M3 fixture 固定历史 UTC，使漏接立即暴露；未延长租约、向未来偏移或放宽校验。相同独立 overlay 从 exit 1 转为 exit 0；完整 M3、完整 M2/M3 普通/race 和 HTTP 全套均在修后通过。

追加独立到期验证保持原 Leader 同 epoch 持续续租，仅让 Attempt 到期：真实 `ValidateExecutionPermit` 拒绝，HTTP 仍返回 409 `WORKFLOW_ATTEMPT_LEASE_CONFLICT`，原 LeaseUntil 和唯一 Session 不变：

```text
same-clock expiry rejected:
now=2026-01-01 00:00:50 UTC lease=2026-01-01 00:00:50 UTC
HTTP=409 code=WORKFLOW_ATTEMPT_LEASE_CONFLICT sessions=1
```

## 4. 独立执行命令与结果

Go 命令工作目录均为 `backend/`，外层 `rtk proxy powershell -NoProfile`、`login:false`，均 `-count=1`。下表耗时为 Go 包耗时；精确 argv、进程耗时、逐项输出见同前缀 `.meta.json/.jsonl/.exit`。每组运行前后核对 2898 个后端源文件，`source_drift=[]`。

| 日志 | 实际核心命令 | 结果 |
| --- | --- | --- |
| `I/startup-green` | `go test -json -overlay I/startup-overlay.json ./internal/session_manager -run "TestM3StartupReattach\|TestIndependentM3StartupReconnect" -count=1 -timeout 5m` | exit 0；7 顶层 / 11 含子项 PASS，0 FAIL/SKIP；19.435s |
| `I/startup-race` | 上一命令加 `-race`，timeout 改 `8m` | exit 0；7 / 11 PASS，0 FAIL/SKIP，无 race 诊断；69.579s |
| `I/startup-retry-red` | `go test -json -overlay I/startup-retry-mutation-overlay.json ./internal/session_manager -run TestIndependentM3StartupReconnect -count=1 -timeout 3m` | exit 1；1 个业务 FAIL；6.868s；预期变异红灯 |
| `I/bridge-green` | `go test -json -overlay O/bridge-overlay.json ./internal/daemon -run TestIndependentM3DaemonDispatchStartsOnlyAfterObservedReady -count=1 -timeout 3m` | exit 0；1 PASS，0 FAIL/SKIP；19.788s |
| `I/maintain-recovery` | `go test -json -overlay I/maintain-overlay.json ./internal/daemon -run TestIndependentM3MaintainRecoversBeforeChatReattach -count=1 -timeout 3m` | exit 0；1 PASS，0 FAIL/SKIP；4.625s |
| `I/mixed-clock-red` → `mixed-clock-green` | `go test -json -overlay I/clock-diagnostic-overlay.json ./internal/integration -run TestM3RealManagerSharedCapacity -count=1 -timeout 5m` | 修前 exit 1、1 FAIL、7.259s；同反例修后 exit 0、1 PASS、52.186s |
| `I/same-clock-expiry` | `go test -json -overlay I/clock-expiry-overlay.json ./internal/integration -run TestIndependentM3SameClockExpiryFailsClosed -count=1 -timeout 3m` | exit 0；1 PASS，0 FAIL/SKIP；12.003s |
| `I/m3-core-clock-final` | `go test -json ./internal/integration -run TestM3 -count=1 -timeout 20m` | exit 0；6 PASS，0 FAIL/SKIP；142.427s |

最后 6 项分别为共享容量、资源/三级预算、Agent 请求独立物化、独立进程唯一 Claim、续租/重启 Fence、争抢/公平轮转。涉及真实临时 Git、SQLite、Manager、Chat Service 与 HTTP；Driver、资源、时钟、用量来源为受控替身，未调用真实 Provider。

历史 `O/bridge-race` 为 exit 0、1 PASS、69.149s；`O/bridge-red` 为预期变异 exit 1、1 FAIL。`O/bridge-first` 的 BOM 解析失败、`startup-first` 的编译失败、`startup-red` 的旧 Session 快照错误断言，以及中间工具命令失败均保留，不计业务 PASS。独立 `I/m3-core` 的公平失败和 `I/m3-core-final` 的混合时钟失败也保留，最终以修后的 `m3-core-clock-final` 为准。

## 5. 主控完整门禁的独立审计

以下结果来自重新解析原始日志，非仅采信实施报告。测试计数为“顶层 / 含子项 PASS”；SKIP 单列。未将没有测试文件的包级 skip 计成测试通过或测试跳过。

| 命令 | 最终结果 | 证据 |
| --- | --- | --- |
| `go test -json ./internal/service/scheduler ./internal/service/budget ./internal/service/workflow ./internal/service/execution ./internal/service/intervention ./internal/service/principal ./internal/storage/sqlite/... -count=1 -timeout 30m` | exit 0；964 / 1503 PASS，2 子项 SKIP，0 FAIL | `T/core-sqlite.*` |
| `go test -json ./internal/session_manager -count=1 -timeout 30m` | exit 0；787 / 1518 PASS，14 SKIP，0 FAIL | `T/manager-full.*` |
| `go test -json ./internal/service/chat -count=1 -timeout 20m` | exit 0；710 / 1113 PASS，0 FAIL/SKIP | `T/chat-full.*` |
| `go test -json ./internal/httpd/... ./internal/daemon -count=1 -timeout 20m` | exit 0；1026 / 1603 PASS，11 顶层 / 14 含子项 SKIP，0 FAIL | `T/http-daemon-clock.*` |
| `go test -json ./internal/integration -run "TestM2\|TestM3" -count=1 -timeout 30m` | exit 0；39 / 58 PASS，0 FAIL/SKIP；440.216s | `T/integration-final-03.*` |
| 上一命令加 `-race` | exit 0；39 / 58 PASS，0 FAIL/SKIP，无 `DATA RACE`；569.935s | `T/integration-race-03.*` |
| `go build ./...`；`go vet ./...` | 均 exit 0，原始 stdout 无诊断，源码无漂移 | `T/backend-build-clock.*`、`T/backend-vet-clock.*` |
| `npm run frontend:typecheck` | exit 0；实际执行 `tsgo --noEmit`，无错误 | `T/frontend-typecheck-02.log/.metadata.json` |
| `npm run api` | exit 0；OpenAPI 与 schema.ts 生成前后及当前哈希一致 | `T/api.*`、`T/api-drift.json` |
| `npm run sqlc`，实际为 backend 下 `npm.cmd --prefix .. run sqlc` | exit 0；117.054s；sqlc v1.31.1 真实生成，46 个生成 Go 文件无漂移 | `T/sqlc-proxy.*` |

SQLC 最初两次在线 EOF、一次离线元数据查询失败均保留。第三次后停止重试，用户明确批准使用本机现有代理，随后原固定版本生成成功；不是用旧日志或空哈希关闭缺口。前端首次 `Unknown command: pm` 未运行 TypeScript，本表只采用实际运行成功的第二组。原线程缺失的生成漂移证据由本次真实生成补齐。

跳过项没有新增：SQLite 两子项因 Windows 文件名不支持 `?`；HTTP 的 14 项包括 Unix 目录权限 2、真实 Electron 环境 3、Windows PTY 2、symlink 权限 5、Windows 文件名 2；Manager 的 14 项包括 POSIX executable 2、Windows PATH 2、tmux/macOS 3、symlink 2、shell/后台管道 5。精确名称与原始理由保留于上述 metadata 的 `skips`。`service/budget`、`sqlite/gen`、`httpd/apierr`、`httpd/requestscope` 的无测试文件结果不混入这些计数。

## 6. 历史失败、规模与未验范围

额外执行 `go test -json ./internal/domain ./internal/adapters/resources -count=1 -timeout 10m` **仍为 exit 1**：110 顶层 / 483 含子项 PASS，另有 2 FAIL，0 SKIP。失败原文：

```text
TestBuildHandoffNoteStaysUnderTheCapAndKeepsTheLatestTurn
handoff_note_test.go:84: the latest turn must never be dropped
TestHandoffNoteKeepsArtifactsWithinTheByteCap
handoff_note_test.go:211: note must keep the latest request and count the artifacts it left out
```

`T/domain-head-regression` 将 HEAD 全 domain 包导出并通过 overlay 在同机、同工具链执行相同两项，仍为同断言 2 FAIL、exit 1；相关源文件与 HEAD/入场哈希相同。该失败已证实属于旧基线，不归为本次新增 P0/P1；它仍须作为独立缺陷处理，不能宣称全仓绿灯。

测试规模见 [m3-files.md](m3-files.md)：新增 Go 测试 6141 行 / 新增非生成生产 5252 行，比值 1.169；新增测试文件不超过 300 行、fixture 不超过 400 行。既有超限文件逐项申报并保留原回归结构，含继承的 pricing 文件超限；本次临时独立 overlay 不冒充正式仓内测试。

未检查范围及风险：

- 没有真实 Codex/Claude/OMP/DeepSeek Turn、真实费用、Windows Hello、真实 Electron/安装流程验收；M0-05 预算未批准，完整工作台仍属于 M5。
- 没有两个完整 daemon 的端到端竞争、真实进程崩溃/断电矩阵、长期 Provider Rate Limit、持续 I/O 故障或长期硬件压力测试；受控时间推进与进程竞争只证明其明确断言。
- 没有执行其他原生 OS、Node 24 CI、全部远端 CI 或全仓 `go test ./...`/全仓 race；Windows 平台跳过项保持上述边界。
- 硬预算限制新准入，不能保证第三方最终账单不超过预留；UNKNOWN 和未完成费用应继续冻结预算与占位，不以 TTL 清零。
- 原 M1 非全局写仍可能先提交业务、后审计失败；本次未把所有历史操作改成事务审计。M3 自身策略、预算、物化及全局恢复沿用各自事务门禁。

## 7. M4 交接与结论约束

可交接 Scheduler 的准入/当前 Fence、Workflow 物化、Budget 快照/用量/对账、ExecutionPermit、生成 API 与 CDC，以及真实绑定的 Attempt/Repo 工作区引用。M4 必须保留独立 Operator 批准、Scope、generation/Epoch、canonical 用量唯一性与 UNKNOWN 闭锁；观察许可不能升级为 Start、Settle 或 Renew 权限。

本阶段不为 M4 的未来代码工件签发 Review PASS，也不产生自动 Commit/Merge/Push 或其授权。G7 的通过依据是上述独立实跑与最终完整门禁；两个历史 domain FAIL、平台 SKIP 和真实环境未验项继续随交接保留。
