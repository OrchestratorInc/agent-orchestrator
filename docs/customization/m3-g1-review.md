# M3-02 / M3-03 独立 G1、G2 审查

审查日期：2026-11-05。工作区：`E:/code/agent-orchestrator`；分支：`feat/multi-agent-desktop`；HEAD：`8b7d7901b52df1964babe233df3a4e27e7523c47`，包含并行开发中的未提交修改。审查模型：`gpt-6-astra`。

原子准入的实现与计划相符：writer 锁先于读取，槽位、三级预算预留、Task CAS、Attempt、幂等结果及成功审计在同一事务提交；UNKNOWN 不依 TTL 释放。独立双 Store 反例、限定 race 和迁移往返已通过。资源采样复用已有平台实现，资源未知时关闭准入，公平队列也已覆盖被拒绝前缀及重启游标。

**最终限定结论：G1 原子事务范围 PASS；G2 资源与调度决策范围 PASS。P2 优先级缺陷已修复；执行许可 fixture 迁移后，完整普通、race 连同原独立反例各 21 顶层 / 23 含子项 PASS、0 FAIL、0 SKIP，vet exit 0。此结论不代表 M3 全部完成，也不代表执行副作用、续租、迟到回调或所有手动 Session 入口已验收。**

## 范围和方法

- 只审查 M3-02 原子 Admission 与 M3-03 资源、调度决策。M2 第六轮报告只作为已有 G0 背景，本轮未复验整个 M2。
- 独立阅读生产代码与计划，并执行真实隔离 SQLite 的已有测试和临时 Go overlay 反例。没有修改生产代码、已有测试、配置、用户 `~/.ao`，没有真实 Provider Turn，也没有 Git 提交或推送。
- 首轮旧反例复用 `m3-02/independent_g1_test.go`，本轮亲自重新执行；没有引用主控的 PASS 代替独立执行。
- 双 Store 指同一测试进程中两个独立 `sqlite.Store` 和数据库连接；未冒称已运行两个独立 daemon/操作系统进程。

## 重要问题：P2，成功 Claim 后跳过更高优先级任务（反例已复验转绿）

发现时位置：`backend/internal/service/scheduler/planner.go:132-135` 的 `CandidateOffset` 更新；候选查询：`backend/internal/storage/sqlite/queries/scheduler.sql:148`。后续主控补入 `readyChanged` 后行号已变化。

`processed` 同时计入拒绝和成功的候选，但成功 Claim 已将任务从 `state='ready'` 的查询集合移除。把整个 `processed` 加入 `CandidateOffset`，下一轮会在已经缩短的集合上再次跳过成功任务所占的位置。

真实复现条件：同一 Workflow 有六个均可准入任务，优先级依次为 100、99、98、97、96、95；全局和 Adapter 上限均为 10，默认 Workflow 权重为 1。首轮领取 `rank-0`，第二轮应领取剩余最高优先级的 `rank-1`，却领取 `rank-2`。

```text
--- FAIL: TestIndependentG2ReadyRemovalPreservesNextCandidate (2.09s)
READY row removal skipped the next ordered task:
expected=rank-1
got=[{WorkflowID:workflow-0001 TaskID:rank-2 AttemptID:attempt-0003 Fence:{OwnerID:scheduler-1 Epoch:1}}]
FAIL .../internal/service/scheduler 4.685s
exit=1
```

这违反 `m3-design.md` 第 5 节的 Workflow 内 `priority` 降序约定。**目前证明的是优先级顺序退化，不是永久饥饿**；短页归零与跨 Workflow 轮转仍存在，已有“前五个拒绝后第六个最终可运行”用例也通过。

主控已修复：当成功/幂等 Claim，或者拒绝后复读发现候选已离开 READY 时，重置 offset 从当前排序头重排；全部拒绝且仍为 READY 的整页继续推进 offset。本审查没有改生产逻辑。原独立反例与正式回归 `TestReadyRemovalPreservesNextCandidate` 均于修后转绿，断言未放松。后续完整普通/race 同时覆盖原反例、被拒绝前缀、DAG 老化和重试，均通过。

独立反例文件：`C:/Users/11459/AppData/Local/Temp/ao-m3-implementation-20261105/m3-g1-independent-astra/independent_g2_test.go`；overlay：同目录 `g2-overlay.json`。未放松断言，未增加 skip。

## 已验证的边界

| 边界 | 证据及结论 |
| --- | --- |
| 两个不同请求争最后全局槽 | 独立两个 Store 同时启动：`distinct requests=2 winners=1 denied=1 admissions=1 reservations=0`。PASS。 |
| 两个不同请求争最后硬预算 | 额度、单次预留均为 7：`winners=1 denied=1 admissions=1 reservations=1`，预留金额恰为 7。PASS。 |
| Leader 竞争与旧任期 | 两个 Store 竞争到期 Leader，只有一个赢家，epoch 从 1 增至 2，旧 epoch 续租拒绝。PASS。 |
| 事务回滚和同请求重放 | 最后的幂等写入故障后，Task 保持 READY，Attempt/Admission/Reservation 均无残留；相同请求双 Store 只保留一个 Attempt，不同指纹拒绝。PASS。 |
| 旧 M1 Claim 闭锁 | `workflow.Service.Claim` 未注入 Admission 时拒绝新领取，已有准入仅可查询重放；旧 `Store.ClaimWorkflowTask` 返回 `ErrAdmissionRequired`。控制器仍调用该服务，未发现另一条写入旁路；本轮没有另做 HTTP 传输层验收。 |
| 策略 CAS、幂等与 CDC | 陈旧 revision、11 并发上限拒绝；同批准重放不增加 revision。新增 Adapter INSERT 已产生匹配 `scheduler_updated`；旧独立 CDC 反例本轮转绿。 |
| UNKNOWN、历史活跃与身份合并 | 历史 UNKNOWN/空 Harness 继续占槽；Admission、Binding、Session 依稳定身份合并；准备态不误计；跨月和租约到期不清除占位。PASS。 |
| 预算三个层级 | global/workflow/task 任一不足均拒绝且无部分预留；未知金额和整数溢出拒绝；新增硬预算不把已有无预留占位当零。PASS。 |
| 资源与容量 | 默认全局 3、Adapter 2、最高 10、第 11 个拒绝；75/85 CPU、RAM 百分比及 2 GiB 硬线、三次连续新样本恢复、同时间戳不重复恢复，普通与 race 均通过。 |
| 公平与 DAG | 5:1 deficit、预算阻断大 Workflow 不遮住其他 Workflow、前五项拒绝后第六项及重启后新头部、DAG 与老化、P2 原独立反例，最终完整普通/race 均 PASS。 |
| 迁移往返 | 当前迁移集合执行 up/down/up，检查无调度/预算残表、`integrity_check=ok`、再次应用默认容量 3。PASS。 |

关键实现锚点：`scheduler_admission_store.go:17`、`scheduler_admission_checks.go:87`、`scheduler_leader_store.go:84` 与 `:102`、`workflow_lifecycle_store.go:90`、`queries/scheduler.sql:69`；资源决策见 `service/scheduler/resources.go:70`，Windows 采样适配见 `adapters/resources/sampler.go:32`。

## 执行命令和实际结果

下列 `go` 命令工作目录均为 `backend/`，通过 `rtk proxy powershell -NoProfile -Command '…'` 执行，外层 `login:false`。测试均显式 `-count=1`，没有依赖 Go 测试缓存。

证据目录：`C:/Users/11459/AppData/Local/Temp/ao-m3-implementation-20261105/m3-g1-independent-astra/`。每组保留 `.jsonl`、`.stderr`、`.exit`；顶层数量不包含子测试，含子项数量包含顶层。

| 日志前缀 | 精确核心命令 | exit | 顶层 PASS / 含子项 PASS / SKIP |
| --- | --- | --- | --- |
| `store-m3` | `go test -json ./internal/storage/sqlite/store -run TestM3 -count=1 -timeout 5m` | 0 | 20 / 27 / 0 |
| `store-independent` | `go test -json -overlay C:/Users/11459/AppData/Local/Temp/ao-m3-implementation-20261105/m3-02/independent-overlay.json ./internal/storage/sqlite/store -run TestIndependentG1 -count=1 -timeout 3m` | 0 | 3 / 5 / 0 |
| `store-g1-race` | `go test -race -json -overlay C:/Users/11459/AppData/Local/Temp/ao-m3-implementation-20261105/m3-02/independent-overlay.json ./internal/storage/sqlite/store -run "TestM3\|TestIndependentG1" -count=1 -timeout 6m` | 0 | 23 / 32 / 0 |
| `workflow-admission` | `go test -json ./internal/service/workflow -run "Admission\|Claim" -count=1 -timeout 3m` | 0 | 3 / 3 / 0 |
| `workflow-admission-race-ready` | `go test -race -json ./internal/service/workflow -run "Admission\|Claim" -count=1 -timeout 3m` | 0 | 3 / 3 / 0 |
| `scheduler-resources` | `go test -json ./internal/service/scheduler ./internal/adapters/resources -count=1 -timeout 5m` | 0 | 19 / 21 / 0 |
| `scheduler-resources-race` | `go test -race -json ./internal/service/scheduler ./internal/adapters/resources -count=1 -timeout 5m` | 0 | 19 / 21 / 0 |
| `g1-g2-vet` | `go vet ./internal/service/scheduler ./internal/adapters/resources ./internal/service/workflow ./internal/storage/sqlite/...` | 0 | 无诊断 |
| `m3-migration-ready` | `go test -json ./internal/storage/sqlite -run TestM3SchedulerMigrationRoundTrip -count=1 -timeout 3m` | 0 | 1 / 1 / 0 |
| `g2-counterexample-ready` | `go test -json -overlay C:/Users/11459/AppData/Local/Temp/ao-m3-implementation-20261105/m3-g1-independent-astra/g2-overlay.json ./internal/service/scheduler -run TestIndependentG2 -count=1 -timeout 2m` | 1 | 0 / 0 / 0；1 个顶层 FAIL |
| `g2-fixed-full` | `go test -json -overlay C:/Users/11459/AppData/Local/Temp/ao-m3-implementation-20261105/m3-g1-independent-astra/g2-overlay.json ./internal/service/scheduler ./internal/adapters/resources -count=1 -timeout 5m` | 1 | 17 / 19 / 0；4 个顶层 FAIL |
| `g2-fixed-full-race` | `go test -race -json -overlay C:/Users/11459/AppData/Local/Temp/ao-m3-implementation-20261105/m3-g1-independent-astra/g2-overlay.json ./internal/service/scheduler ./internal/adapters/resources -count=1 -timeout 5m` | 1 | 17 / 19 / 0；4 个顶层 FAIL |
| `g2-fixed-vet` | `go vet ./internal/service/scheduler ./internal/adapters/resources` | 0 | 无诊断 |
| `g2-permit-fixed-full` | `go test -json -overlay C:/Users/11459/AppData/Local/Temp/ao-m3-implementation-20261105/m3-g1-independent-astra/g2-overlay.json ./internal/service/scheduler ./internal/adapters/resources -count=1 -timeout 5m` | 0 | 21 / 23 / 0 |
| `g2-permit-fixed-full-race` | `go test -race -json -overlay C:/Users/11459/AppData/Local/Temp/ao-m3-implementation-20261105/m3-g1-independent-astra/g2-overlay.json ./internal/service/scheduler ./internal/adapters/resources -count=1 -timeout 5m` | 0 | 21 / 23 / 0 |
| `g2-permit-fixed-vet` | `go vet ./internal/service/scheduler ./internal/adapters/resources` | 0 | 无诊断 |

`TestM3` 名称筛选也包含当时已存在的预算账本和物化测试；计数如实保留，不据此扩展本轮验收范围。Scheduler 的 19 个顶层中，资源适配包为 2 个，Scheduler 为 17 个。普通 Scheduler 耗时 8.649s，race 75.792s；Store 限定 race 76.177s。以上通过测试无失败、无跳过。

审查期间确实出现过并行写入的编译中间态：最初缺 `gen.RenewExecutionAttemptLease`、`ReleaseExecutionAdmission`、`FreezeExecutionBudgetReservations`；随后缺 `principal.Resolver.WithLeaseProver`。`g2-counterexample`、`g2-counterexample-run`、`m3-migration`、`workflow-admission-race` 保留 exit 1 和原始错误。后续已能编译，分页反例是实际断言失败，不能与此前 build failed 混淆。

可选源文件 SHA 快照未取得：现场 PowerShell 无 `Get-FileHash` 命令，失败时未产生有效 hash。不得使用空的 `reviewed-source-sha256.json` 当作文件保护证据。本轮 `git diff --check` exit 0，输出仅为已有文件的 LF/CRLF 提示；它不证明并行工作区没有发生其他改动。

历史中间轮次的完整普通、race 回归均有以下四项失败，均发生于新增执行许可约束下的旧 Settle fixture；保留原始失败，不将历史结果改写为通过：

```text
TestDAGReadinessAndAgingOrder: fairness_test.go:148
TestBlockedPrefixEventuallyYieldsAndTailWrapsToNewHead: fairness_test.go:202
TestVerifiedRetryPersistsCooldownAndCannotAuthorizeLaterFailure: retry_test.go:62
TestVerifiedRetryStopsAfterThreeAttempts: retry_test.go:140
Current resource, capability or budget evidence does not permit a new execution
FAIL .../internal/service/scheduler 8.549s
exit=1
```

对应完整 race 耗时 78.019s，同样 exit 1、四项失败。两组均为 17 个顶层 / 19 个含子项 PASS，4 个顶层 FAIL，0 SKIP；原独立优先级反例通过。

**最终复验（北京时间 17:48—17:50）：** 主控迁移 fixture 后，本审查确认 Settle 通过 `Store.LoadExecutionPermit` 获取真实持久许可并以 `ports.WithExecutionPermit` 传递；DAG 老化以 30 次 20s 的逻辑时钟推进维持同一 Leader，总等待仍为 10min，没有伪造跨 epoch 结算或缩短老化时间。原独立 overlay、所有原普通测试及四个失败用例全部重新执行，普通/race 各 21 顶层 / 23 含子项 PASS、0 FAIL、0 SKIP，exit 0。Scheduler 耗时分别 6.878s / 82.521s，资源适配包 0.218s / 1.187s；最终 vet exit 0。包含原反例与正式回归两次执行同一缺陷的证据，不能把它们当成两个不同功能覆盖点。

## 未覆盖与下一步

- `schedulerFence` 和 `PrepareWorkflowAdmission` 的二次检查只证明准入时间和任期边界。执行许可接受前后、已赢绑定 CAS 后的外部 Spawn、迟到回调、可信长任务续租、人工恢复释放顺序属于 M3-05；并行新增的执行许可文件尚未由本报告验收。
- `CountUnattributedBudgetUsage` 已读取 `budget_usage_pending`，UNKNOWN 不按零处理；预算对账调用时机、实际消费与预留消耗/释放、价格回填整链属于 M3-04/05，不能用本轮预留测试代替接线验收。
- `ReserveSessionAdmission` 的 Store 门禁已测；所有手动 Session 创建入口和 daemon 周期运行器的接线尚未验收。因此 3/10 结论限经过此准入路径的调度决策，不能宣称整个应用的所有启动已统一限流。
- 未复跑整个 Store/Workflow 包 race，未复验此前 15 分钟整包 race 超时结果，未运行完整 CI、前端或真实 Provider；本报告只列确实完成的有界验证。

G1/G2 本轮限定复验已收口；后续执行、预算控制面及完整 M3 验收仍须各自取得证据，本报告不替代它们。
