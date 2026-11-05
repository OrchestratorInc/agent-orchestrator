# M3 ADR-02：纳美元账本与硬预算保证范围

日期：2026-11-05。状态：M3-01 设计冻结；尚未实施，不解除 G0。

## 事实

- `domain/usage.go:181` 的 `UsageEventCosts` 已用可空 `int64 nano-USD`；nil是未定价，不是0。
- `pricing/runtime.go:273` 与 `:533` 已用 `big.Rat` 计价、四舍五入到纳美元及溢出检查；实时 Chat/HTTP 的浮点展示金额不是硬预算账本。
- canonical用量唯一键是 `(binding_id, source_event_key)`，见 `migrations/0102_canonical_usage.sql:77`。`store/usage_store.go:367` 的事件/游标事务将跨 artifact generation 的同一事件视为重放。
- 现有价格回填会 CAS 修改原事件（`queries/usage.sql:465`），并非现成的修正事件流。仅消费新增事件ID会漏掉未知→已定价。

## 决策

1. 所有 M3 USD政策、预留、费用使用 `int64 *_nanos`，复用 Pricing 单位，不引入 micro-USD 转换或浮点硬比较。校验每次乘加及累计溢出；未知、溢出、不同币种均不按0处理。
2. Global / Workflow / Task 各自检查“已确认/可解释费用 + 未结预留”，三层只是同一事件的作用域投影；总费用从事件账本汇总一次，不相加三层余额。
3. 归因唯一键优先使用 canonical usage event ID，并保留 binding/source key；不能用裸 source key，也不能无条件拼 artifact generation。价格/归属修正单独追加记录，带原事件、前后金额/质量、价格版本及原因。
4. 消费新增事件及原事件成本修订；采用持久化幂等版本和有界对账，确保重启后补回漏记修正。既有 Usage Collector、Pricing Manager 仍是采集/计价所有者。
5. EXACT 要求可核实的计量和同币种计费依据；native token + catalog估价通常仍是 ESTIMATED 成本。未知统计或计价为 UNKNOWN。OMP/DeepSeek 未获可信证据时保持 UNKNOWN。
6. observe记录、soft在80%/95%提醒、hard_admission拒绝余额不足或用量无法界定的新准入。没有预算配置仅表示平台未限额，不代表供应商额度无限。
7. 硬预算只保证准入时不批准超出当时可用预算的预留，不保证已在执行的第三方最终账单不超支。预留必须有可信边界或明确批准的任务预留额；平均估算不能伪装为费用上限。
8. UNKNOWN占位和费用不能因TTL释放。释放/消费随可核实结算或具体Operator裁决，以幂等事务更新；预算跨月窗口必须保留原预留归属，不能重置遗失。

## 验收要求与限制

M3-04验证三级硬拒绝、nil/UNKNOWN、重复和乱序事件、价格回填、币种冲突、负向修正及溢出。本轮 Pricing 完整包为34 PASS/2 FAIL（Windows文件mode断言），详细失败见架构报告；复用定点算法不等于 Pricing 整包通过。不得修改用户原生模型/密钥配置。
