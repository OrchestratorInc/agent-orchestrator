# M3 ADR-03：持久化 Leader、Epoch 与不可重放的派发

日期：2026-11-05。状态：M3-01 设计冻结；尚未实施，不解除 G0。

## 事实

- M1 Claim有SQLite CAS、活跃Attempt唯一索引及幂等记录，没有Leader/epoch/槽位/预算。
- M2已有稳定`SpawnKey`、binding状态CAS和Session `client_request_id`唯一约束；`Manager.Spawn`先抢Session行再启动。它们防止同键第二份Session，不证明旧Leader仍有新派发权。
- `execution/coordinator.go:384`的`spawn_requested` CAS之后在`:454`调用Gateway，无epoch检查。仅在Admission事务验证epoch会留下事务提交到Spawn间的窗口。
- Daemon已有dataDir OS lock，但`daemon.go:1645`在文件锁不支持时允许无锁继续，不能将它等同于调度epoch安全保证。

## 决策

1. SQLite单行leader存owner、单调递增epoch、租约到期和revision。初值租约30s、每10s续租；所有时间由可注入Clock提供。失去租约立即停止新决策和准入，不能把领导租约失效当作Attempt已经停止。
2. 同一writer事务验证当前leader/epoch、policy/revision、DAG、能力及可信容量/费用证据，再完成槽位、预算、Task CAS、Attempt及幂等审计。全部失败一起回滚。
3. 将epoch贯穿派发资格、M2绑定状态写入、Session启动准入、续租、结算/重锚与占位释放。所有更新以epoch+generation+revision拒绝迟到写入；未知回执仅保留为待核验证据。
4. 副作用所有者接受派发时，必须持久化检查并消费当前epoch的执行许可，且仍使用同一SpawnKey/唯一Session行。**SQLite检查与OS进程启动不是一个原子操作**：已接受的在途启动可能跨过租约边界；不得因换Leader再次执行或释放资源。不是在调用前多查一次epoch就能证明端到端栅栏。
5. 新Leader在恢复完成前不开放Admission。恢复已接受/可能接受的许可、Session、binding、Worktree和预留；无法证明未启动时转UNKNOWN/uncertain，继续占槽和持有预算。只有证实未产生外部副作用，才允许使用原稳定键继续。
6. M3启用时文件锁不支持须停止自动派发，保留查询和诊断；不能用已有daemon的无锁降级来承诺全局单执行所有者。双Scheduler竞争测试仍需通过真实SQLite事务验证，不能只依靠进程mutex。
7. 公平轮转游标及Workflow deficit持久化；可运行任务内优先级+每5分钟一级、最多5级老化，权重1–5，不绕过DAG/能力/权限/预算。重试默认关闭，仅已证实safe-to-retry且不超过`min(maxAttempts,3)`；退避5s起、5min封顶、jitter≤20%，明确Retry-After至少等到其允许时间。

## 验收要求与限制

M3-02/05必须插入“Claim提交后”“执行许可接受前后”“Spawn回执丢失”“旧epoch迟到”“双Leader竞争”故障点，验证最多一份Session且不重复副作用。定向M2测试只证明现有去重/恢复边界，不证明上述新增许可或epoch已实现。不得删除、重置dirty Worktree。
