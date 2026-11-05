# M3 daemon 调度接线独立审查

审查日期：2026-11-05。工作区：`E:/code/agent-orchestrator`；分支：`feat/multi-agent-desktop`；HEAD：`8b7d7901b52df1964babe233df3a4e27e7523c47`，含并行开发中的未提交修改。审查模型：`gpt-6-astra`。

实现保持了 Scheduler 授权、Execution Coordinator 绑定、Session Manager 启动的边界；HTTP 与后台领取共用同一 Scheduler，本实例任期检查也已接入。启动门禁、独立心跳和容量为 1 的 CDC 唤醒通道方向正确。

**最终限定结论：本轮启动门禁、Scheduler/Coordinator 本实例身份、慢 Claim 续租和 CDC 有界通知范围 PASS。真实 SQLite 复现的 P2 自身续租竞争已由主控修复，未放松断言的原反例普通/race 均转绿。完整 Scheduler 加 daemon 独立反例，普通/race 各 23 顶层 / 25 含子项 PASS、0 FAIL、0 SKIP，vet exit 0。本报告不替代完整 M3 验收，HTTP Agent Caller 栅栏、新增 Start/Retry 与执行恢复全链仍按下述边界单独验收。**

## 范围与方法

- 审查 `daemon/scheduler_wiring.go`、`scheduler_runtime.go`、`daemon.go`、`workflow_wiring.go`、`automation_wiring.go`、`execution_wiring.go`，以及 `scheduler.Service.MaintainLeadership` 去除 `runMu` 后的并发行为。
- 对照 `m3-design.md` 的启动恢复、先对账后派发、Leader 任期、统一 Admission 和有界调度要求。G1/G2 与预算前两份审查维持原范围，本报告不改写其结论。
- 独立测试使用真实隔离 SQLite、真实 Scheduler/Workflow/Coordinator；并发屏障只暂停真实 Store 调用，不伪造 SQL 结果。daemon 用例通过 `newM3Services` 和 `startM3Scheduler` 执行真实恢复循环，未启动 HTTP listener 或真实 Provider。
- 本审查仅新增本报告和临时 overlay 证据，没有修改生产代码、已有测试、配置、用户 `~/.ao`，没有 Git 提交、推送或真实 Provider Turn。生产修复和正式回归由主控/执行代理完成。

## 重要问题：P2，自身游标更新导致误丢 Leader 身份

发现位置：原 `service/scheduler/service.go:105-108`；并发写入方为 `service/scheduler/planner.go:158`。修后位于 `service.go:103` 起的有界重读/续租循环。

去掉心跳的 `runMu` 是必要调整：慢 Claim 不能挡住续租。但 Tick 更新公平游标和心跳续租共用 `scheduler_leader.revision`。Tick 已读完 Leader 后，心跳可读取同一旧 revision；Tick 先提交游标，随后心跳的真实 SQLite CAS 失败。原代码把任何续租失败都当作丢主并清空本地 `fence`，尽管数据库 owner/epoch 和租约仍有效。

此后本实例的 `CurrentFence` 与正常心跳均拒绝该租约，直到剩余租约到期才能重新抢主，正常调度可能反复停顿。这里证明的是逻辑竞态与可用性退化，不是两个 daemon 同时获准执行，也不是 Go 数据竞争。

独立屏障顺序：Tick 停在游标 SQL 之前，心跳停在续租 SQL 之前；先放行真实游标提交，再放行续租。普通和 race 均得到以下业务断言失败：

```text
own Tick changed only revision, but daemon discarded its live fence:
renewal=scheduler leadership fence is stale or unrecovered
local=scheduler leadership fence is stale or unrecovered
persisted={Fence:{OwnerID:scheduler-1 Epoch:1}
 LeaseUntil:2026-11-05 12:00:30 +0000 UTC Revision:4
 Recovered:true Cursor:workflow-0001}
--- FAIL: TestIndependentDaemonLeaderRenewalCursorCAS
exit=1
```

测试逻辑时钟此时为 `12:00:10 UTC`，即持久租约尚余 20 秒。其余慢 Claim 心跳和 daemon 恢复反例均通过，不能把该失败归于屏障超时或编译失败。

主控修复：CAS 冲突后重新读取 Leader，只有相同 owner/epoch 且租约仍有效才保留本地身份，最多再尝试一次续租；真实丢主或无法读取时继续闭锁。主控将反例转为 `leadership_cursor_test.go` 的 `TestLeaderRenewalToleratesOwnCursorCAS`。独立 overlay 保留原业务断言，仅为避免与正式回归同名，将辅助类型改为 `independentPausedRenewStore`。

## 接线与已验证边界

| 边界 | 实现与证据 |
| --- | --- |
| 启动恢复顺序 | `daemon.go:1327` 将 Scheduler 置于持久 Chat Host、Reviewer、Session Manager 后台对账和 runtime reconcile 的 `done` 之后。独立用例保持 boot 未关闭时，ready 不开放、旧数据库 Leader 未被抢占；关闭后，新 epoch 标记 recovered，旧未确认 Admission 被接管并保持 uncertain。普通/race PASS。 |
| 本实例任期 | `scheduler_wiring.go:31`、`daemon.go:1148` 均调用 `Scheduler.CurrentFence`。同一 Store 上第二套 `newM3Services` 的 `CurrentFence` 和 `Binder.Reconcile` 无法借用第一套 Leader；普通/race PASS。该用例是两个服务实例，不是两个操作系统进程。 |
| 慢 Claim 时续租 | 心跳不持有 `runMu`。真实 Claim 回调在屏障等待时，推进逻辑时钟 10 秒，`MaintainLeadership` 先完成并持久化 `now+30s` 租约，之后放行 Claim。普通/race PASS。 |
| CDC 不阻塞发布者 | `scheduler_runtime.go:20` 的容量 1 channel 使用非阻塞写入。独立用例同步发布 5000 次 Scheduler CDC，在 2 秒测试界限内完成；取消后 SubscriberCount 为 0。普通/race PASS。 |
| 防止自身 CDC 忙循环 | 主循环收集 wake 后仍等 5 秒 ticker，再允许下一轮；维护任务容量 1，launch 容量 10，每轮至多 3 个 Claim。此项为静态调用链核验，未做长期吞吐/CPU 压测。 |
| 心跳与耗时派发分离 | `scheduler_runtime.go:50` 心跳 context 为 3 秒；维护 worker 为 20 秒；独立 launch worker 为 10 分钟。worker 使用同一取消上下文并在退出时等待，CDC 订阅被回收。没有以真实长时 Bind/Provider 验收替代慢 Claim 反例。 |
| 单次派发的身份 | `scheduler_wiring.go:75` 只消费本次 Dispatch 的原 fence，从 Store 加载 permit，校验 ownership/task 后调用 Binder。未发现 runtime 再次 Spawn 旧不确定 Admission 的调用；boot 用例恢复后 Session 行数仍为 0。 |
| 自动入口延后 | Tracker 和 Automations 的首轮启动经 `startAfterScheduler` 等待 ready；Session Manager 的 Admission provider 与 HTTP Claim 使用同一 Scheduler。此项为静态接线确认，未逐个运行所有手动/自动 Session 入口。 |
| Start 和 Retry 新接线 | `startReadyAttempts` 在当前 permit 为 claimed 且租约有效时，再经 `LiveTarget` 检查 Controller/generation/worktree，然后调用 `Workflow.Start`；`scheduleRetry` 校验 expectedRevision 并持久化有界退避，HTTP 再调用原 `Workflow.Retry`。未发现进程退出即自动 Settle 的新增逻辑；本轮未独立完成这两条 HTTP/执行端到端验收。 |

另发现原正常周期只有 `RefreshLeases → LiveTarget`，没有推进首次 `session_provisioning` 的 Binding：即使 Controller 后来 ready，`LiveTarget` 仍会拒绝旧 binding 状态。此缺口已交给执行代理；当前 `execution/recovery.go:118` 已加入带真实 permit 的周期 `Reconcile`，随后重新加载 permit。**本报告只确认补接代码存在，不把执行代理的文字反馈当作该路径独立测试通过；其 provisioning/丢回执/恢复用例归执行专项报告。**

## 执行命令和结果

工作目录均为 `backend/`，通过 `rtk proxy powershell -NoProfile -Command '…'`、`login:false` 执行。所有测试显式 `-count=1`，没有依赖测试缓存。

证据目录：`C:/Users/11459/AppData/Local/Temp/ao-m3-implementation-20261105/m3-daemon-independent-astra/`。下文 `<overlay>` 指该目录的 `overlay.json`；同目录保留 `scheduler_independent_test.go`、`daemon_independent_test.go` 和每组 `.jsonl/.stderr/.exit`。

| 日志前缀 | 核心命令 | exit | 顶层 PASS / 含子项 PASS / FAIL / SKIP |
| --- | --- | --- | --- |
| `initial-test` | `go test -json -overlay <overlay> ./internal/service/scheduler ./internal/daemon -run TestIndependentDaemon -count=1 -timeout 3m` | 1 | 2 / 2 / 1 / 0 |
| `initial-race` | 上一命令加 `-race` | 1 | 2 / 2 / 1 / 0 |
| `final-independent` | 与 `initial-test` 相同 | 1 | daemon 1 / 1 / 0 / 0；Scheduler 编译失败 |
| `final-independent-race` | 上一命令加 `-race` | 1 | daemon 1 / 1 / 0 / 0；Scheduler 编译失败 |
| `verified-scheduler` | `go test -json -overlay <overlay> ./internal/service/scheduler -count=1 -timeout 5m` | 0 | 22 / 24 / 0 / 0 |
| `verified-scheduler-race` | 上一命令加 `-race` | 0 | 22 / 24 / 0 / 0 |
| `verified-daemon` | `go test -json -overlay <overlay> ./internal/daemon -run TestIndependentDaemon -count=1 -timeout 3m` | 0 | 1 / 1 / 0 / 0 |
| `verified-daemon-race` | 上一命令加 `-race` | 0 | 1 / 1 / 0 / 0 |
| `final-vet` | `go vet ./internal/service/scheduler ./internal/daemon` | 0 | 无诊断 |

`final-independent*` 的失败原文为 `pausedRenewStore redeclared in this block`：主控转入正式回归时与 overlay 辅助类型同名。它是并行加入测试造成的编译冲突，不是修后业务断言失败；仅重命名临时辅助类型后重跑，未删除反例或放宽断言。首轮日志摘要命令也曾因 PowerShell 引号解析失败，测试自身的 exit 1 已提前落盘；随后直接解析 JSONL 取得上面的实际失败证据。

完整 Scheduler 计数包含原独立反例和主控转入的正式回归；相同缺陷的两次执行不能算成两个不同功能覆盖点。Scheduler 普通/race 耗时分别 9.115s / 120.987s，daemon 普通/race 分别 7.832s / 76.847s。最终 race 无 `DATA RACE` 诊断，原续租反例与慢 Claim 反例分别于北京时间 18:32:40 / 18:32:41 通过。

## 未覆盖与后续验收边界

- 未运行真实 daemon HTTP listener、完整 Electron/Windows 隔离应用或真实 Provider，也没有以真实 Provider 调用探测功能。
- 未运行 daemon 整包测试和完整 CI；旧 daemon fixture 迁移、所有启动入口、Start/Settle/Retry 的一次性 Operator 授权/幂等，以及多 Admission 预算视图另有负责代理。本报告不替它们宣称通过。
- 主控在本轮收口时另告知 HTTP P1：Agent Caller 从数据库取得 permit，却未比较本实例 `ExecutionFence`，另一实例可能借用相同 capability 创建 TaskRequest。HTTP 代理正在复验 nil 返回 501、不同 fence 返回 409 的修复。本报告未重造或复跑该 HTTP 反例；上面的本实例身份 PASS 仅限 Scheduler/Coordinator，不能外推至全部 HTTP Agent 写入口。
- 没有验证长期慢 Bind、20 秒恢复超时后的活跃大批量公平性、持续数据库 I/O 故障、独立双进程抢主或进程崩溃注入。3 秒/20 秒/10 分钟是代码中传递的 context 时限，不代表对忽略 context 的外部调用已经证明强制中止。
- M2 周期恢复被合并至 M3 维护路径，可避免两个 sweep 并发决策；上述 provisioning 缺口说明替换旧循环时仍须保留其实际推进能力，最终接线验收应包含延迟 ready 与丢回执恢复用例。
