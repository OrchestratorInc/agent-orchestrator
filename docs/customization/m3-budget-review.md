# M3-04 预算账本限定独立审查

日期：2026-11-05。审查模型：`gpt-6-astra`。工作区：`E:/code/agent-orchestrator`，分支 `feat/multi-agent-desktop`，HEAD `8b7d7901b52df1964babe233df3a4e27e7523c47`，包含并行开发中的未提交变更。

实现复用了 canonical Usage/Pricing 事实，以整数纳美元和可空金额保留未知状态；账本修正替换原事件投影，审计另外追加，不把修正再计成新消费。余额读取采用同一个 SQLite 读事务，对账和预留结算在 writer 事务内重新验证，分层边界清楚。

**限定结论：本轮 UNKNOWN、价格下修、重复/并发对账和预留 finalization 检查 PASS，未发现新的确定 P0/P1/P2 预算核心缺陷。已有定向普通/race 各 9 顶层 / 14 含子项 PASS；独立反例普通/race 各 2 顶层 PASS；最终均 0 失败、0 测试跳过。此报告不是 M3-04 控制面、daemon 接线或整个 M3 的最终验收。**

## 范围与计划对齐

审查 `domain/budget_usage.go`、`budget_snapshot.go`、`service/budget/`、`store/budget_usage_store.go`、`queries/budget_usage.sql`，以及 `20261105090651703_m3_budget_ledger.sql`、`20261105094026402_m3_scheduler_control.sql`。对照 `m3-adrs/02-budget-ledger.md`：

| 要求 | 实现与实测 |
| --- | --- |
| UNKNOWN 不为零 | 缺成本、缺发生时间、不可解释质量不生成可用余额。`BudgetSnapshot.AddUsage` 保持可空值，`Complete` 在待对账时清除旧费用下界与 token 总数。未知月份归属也闭锁。 |
| 三层不重复计费 | global/workflow/task 及物化后代通过同一个 canonical event 投影；三级快照各反映费用 30，事件账本仍只有一笔，不能把三层数值相加当总消费。 |
| 原事件成本修订 | `budget_usage_pending` 比较已存投影与当前 canonical 事实；同 event ID 更新 revision，触发器保留前后金额、质量、价格版本、归属与计价来源。 |
| 价格下修 | 经现有 `ApplyLegacyUsageRepairs` 来源修复 CAS，将 inferred 计价 50 改为 observed 计价 20；待修订期间不暴露旧 50 或旧余额，对账后费用 20、余额 80。此证据不等于所有历史 catalog 价格会自动重算。 |
| 重放和并发 | canonical 唯一键继续依赖 binding 与 source event key；跨 artifact generation 的重复不另计，同裸 key 的两个 binding 各计一次。两个独立 Store 并发对账时，每个 revision 总共只处理一次。 |
| 预留结算 | 只处理已释放 Admission、已持久终止 Session、所有 binding/source 完成且无异常、无 pending 投影、无 UNKNOWN 费用的记录；随后将预留置为 consumed，重复结算不再变更。 |
| 软阈值、溢出 | 80/95 提醒按策略、窗口、revision 去重；重复对账无新提醒或新修正。费用、预留、token 累计的整数溢出拒绝。 |

关键实现锚点：`domain/budget_snapshot.go:12`、`:46`；`store/budget_usage_store.go:14`、`:78`、`:185`；`queries/budget_usage.sql:37`、`:46`、`:51`；`service/budget/service.go:36`。

## 两项独立 SQLite 反例

临时测试：`C:/Users/11459/AppData/Local/Temp/ao-m3-implementation-20261105/m3-budget-independent-astra/independent_budget_test.go`，通过同目录 `overlay.json` 编译进真实 Store 包。未修改生产代码或已有测试，未放松断言或增加 skip。

**1. finalization 的阻断条件分别有效。** 通过真实准入、唯一 Session 消费、持久终止及 `ReleaseExecutionAdmission` 建立预算 100、预留 10 的状态。测试依次隔离验证：另一来源尚 active；全部来源完成但有 UNKNOWN 价格；已知价格的新来源确认修正尚未对账。各阶段均保留 `uncertain` 预留 10。只有全部条件满足才结算，最终费用 30、预留 0、余额 70，再次结算返回 0。

```text
active source, UNKNOWN price and pending correction each retain reservation;
final cost=30 remaining=70; replay=0
```

这里的执行观测由测试显式提供并由真实 Store 栅栏核验，不包含真实 Provider 或操作系统进程终止验收。测试没有通过直接修改 Admission 状态来跳过释放 API。

**2. 双 Store 并发和价格下修只产生一次修订。** 两个独立 `sqlite.Store` 连接竞争同一 pending 事件，初次和下修后的每轮处理总数都为 1。来源修复从 50 改为 20 后，另一 Store 在对账前读取到 pending、未知费用和未知余额；对账后只有一个事件、revision 2、一条 correction，费用 20、余额 80。

```text
two Stores: one projection per revision; pending old amount hidden;
cost 50 -> 20; corrections=1 eventCount=1 remaining=80
```

两 Store 在同一测试进程中，未把此证据称为两个独立 daemon 的端到端验收。

## 命令与结果

所有 Go 命令工作目录为 `backend/`，通过 `rtk proxy powershell -NoProfile -Command '…'` 执行，外层 `login:false`。均使用 `-count=1`。日志目录为 `C:/Users/11459/AppData/Local/Temp/ao-m3-implementation-20261105/m3-budget-independent-astra/`，保留对应 `.jsonl`、`.stderr`、`.exit`。

定向筛选表达式全文为：

```text
TestBudgetSnapshot|TestM3Budget|TestM3AdmissionChecksEveryBudgetLayerAndUnknown|TestM3MaterializedDescendantsShareAncestorBudget|TestM3ExecutionReleaseKeepsUnreconciledBudget
```

| 日志前缀 | 核心命令 | exit | 顶层 PASS / 含子项 PASS / 测试 SKIP |
| --- | --- | --- | --- |
| `budget-focused-final` | `go test -json ./internal/domain ./internal/service/budget ./internal/storage/sqlite/store -run "TestBudgetSnapshot\|TestM3Budget\|TestM3AdmissionChecksEveryBudgetLayerAndUnknown\|TestM3MaterializedDescendantsShareAncestorBudget\|TestM3ExecutionReleaseKeepsUnreconciledBudget" -count=1 -timeout 5m` | 0 | 9 / 14 / 0 |
| `budget-focused-final-race` | `go test -race -json ./internal/domain ./internal/service/budget ./internal/storage/sqlite/store -run "TestBudgetSnapshot\|TestM3Budget\|TestM3AdmissionChecksEveryBudgetLayerAndUnknown\|TestM3MaterializedDescendantsShareAncestorBudget\|TestM3ExecutionReleaseKeepsUnreconciledBudget" -count=1 -timeout 5m` | 0 | 9 / 14 / 0 |
| `budget-independent-final` | `go test -json -overlay C:/Users/11459/AppData/Local/Temp/ao-m3-implementation-20261105/m3-budget-independent-astra/overlay.json ./internal/storage/sqlite/store -run TestIndependentBudget -count=1 -timeout 3m` | 0 | 2 / 2 / 0 |
| `budget-independent-final-race` | `go test -race -json -overlay C:/Users/11459/AppData/Local/Temp/ao-m3-implementation-20261105/m3-budget-independent-astra/overlay.json ./internal/storage/sqlite/store -run TestIndependentBudget -count=1 -timeout 3m` | 0 | 2 / 2 / 0 |
| `budget-vet` | `go vet ./internal/domain ./internal/service/budget ./internal/storage/sqlite/store` | 0 | 无诊断 |
| `budget-migration-roundtrip` | `go test -json ./internal/storage/sqlite -run TestM3SchedulerMigrationRoundTrip -count=1 -timeout 3m` | 0 | 1 / 1 / 0 |

最终定向 Store 普通/race 耗时 2.861s / 67.591s；独立反例包普通/race 为 5.007s / 54.879s；当前迁移集合往返 7.477s，包含本轮 ledger/control 迁移，检查移除残表、重新应用及 `integrity_check=ok`。

`service/budget` 当前没有独立 `_test.go`，Go JSON 中该包记录为 package-level `skip`，不是跳过测试用例。服务 `Reconcile` 由真实 Store 集成用例调用；不能声称整个控制服务已有独立单元覆盖。没有新增任何 `t.Skip`。

## 历史失败与修后复验

第一次定向普通/race 均 exit 1：7 顶层 / 12 含子项 PASS，另有两个顶层 FAIL，0 测试 SKIP。原始证据保留于 `budget-focused`、`budget-focused-race`：

```text
TestM3BudgetDescendantSettlementWaitsForFinalizedUsage
  budget_settlement_test.go:20: create workflow task request: trusted admission evidence is required
TestM3MaterializedDescendantsShareAncestorBudget
  workflow_materialization_test.go:156: create workflow task request: trusted admission evidence is required
```

两项均在新增 TaskRequest 准入栅栏处停止，尚未进入预算断言。主控随后调整物化 fixture：在隔离数据库植入 pending 原件，再经正式 `DecideWorkflowTaskRequest` 形成批准；预算用例只验证物化、归因和结算，不冒充 Agent 提议链验收。本审查阅读变更后重新运行相同完整筛选普通/race，两项均转绿，原预算断言未放松。历史失败未改写或计入通过数。

## 验收边界

- 本轮未核定 daemon 启动/周期驱动预算对账的实际接线，也未做控制面 HTTP、Operator 批准链、UI、长期重启补偿或真实 Provider 费用验收。
- 独立 finalization 测试的可信结束来自测试观测与持久事实，不证明真实进程已停止。M3-05 的执行、恢复、迟到回调与租约仍需专门门禁。
- 硬预算保证准入时的预留，不保证第三方最终账单绝不超过预留。未知费用和未完成发现仍应阻止新硬准入，不能用 TTL 自动清账。
- 未运行整个 Store/Workflow 包、全仓 CI 或前端；本轮没有配置变更、真实 Provider Turn、用户 `~/.ao` 访问、Git 提交/推送或部署。

本报告与 [G1/G2 报告](m3-g1-review.md) 分别限定取证，不合并成整个 M3 的最终 PASS。
