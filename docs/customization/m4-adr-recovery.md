# M4 ADR：多仓审批、Partial 与 UNKNOWN 恢复

状态：实施约束已确定，功能验收另见阶段台账。日期：2026-11-06。

## 精确审批与 Git 操作

复用 `service/principal.ApprovalStore` 和 OS consent，不新造审批系统。新路由加入 `OperatorOperations` 闭集；Approve/Consume 绑定方法、具体路径、原始正文摘要、过期时间及动作。Merge 和 Push 是不同审批，不可互换。

批准正文绑定不可变 Plan ID/revision、完整有序 Repo 集合、真实 repo identity、target ref/expected SHA、task commit SHA、preview digest、策略版本。任一变化要求新 Preview 和审批。调用前再次校验全体 Repo，串行执行逐仓 `update-ref` CAS。

审批是内存一次性凭据，SQLite 与审批消费不是跨系统原子事务：先验证请求，消费凭据，持久化唯一操作意图，才允许 Git 写。崩溃可以烧掉批准，不能恢复成未消费。幂等重放仅返回已知结果，不能再次授权 Git 副作用。

每仓操作有 prepared/requested/applied/rejected/unknown，聚合有 requested/applied/rejected/partial/unknown。部分完成保留已更新 refs，不自动回滚；恢复在 SQLite writer 内只查询实际 ref 与已保存目标对象，同时递增操作 revision，拒绝迟到写入。UNKNOWN 不盲重试；partial/unknown 阻止新的集成意图，后续处置不得复用旧批准。

Push 默认禁用。M4 只实现可信宿主登记的本地 bare 模拟：请求绑定登记 key、物理路径身份摘要、ref、expected SHA 和候选 SHA；独立批准后用 pack 传递对象、update-ref CAS 实现租约语义。没有调用真实 Git remote transport，也不加载远端 URL、rewrite 或 receive hooks。真实网络 Push 和 force-with-lease transport 的接入留给获得单独预算及操作授权的后续工作，不能把本机模拟说成真实远程已验证。

现有 `service/pr.ActionService.Merge` 做远端 head CAS，但旧 `/prs/{id}/merge` 没有 Workflow Operator 校验。M4 工作流不得通过该路由绕过质量和审批；接线阶段必须对 Workflow 归属 PR 拒绝旧入口或接入同一审批要求，不能宣称旧路由已安全。

## Path Reservation 与运行时恢复

唯一表 `workflow_task_path_reservations`。由 `ReserveAndClaim` 原有 writer 事务从 Task/Workflow Repository 派生路径，一次获取全部 Repo；冲突即回滚预算、Admission、Attempt、Task CAS。使用真实仓库身份（非局部 RepoKey）及组件前缀，空 scope 表示整仓。

Claim 绑定 Attempt/Epoch；可信 Bind 更新 Session/Generation，续租同步 lease；接管旧任期只更新所有权，不释放 UNKNOWN。释放要求当前 fence、准确 generation、可信终止事实和已冻结 Artifact；旧 owner/revision 不能释放新锁。TTL 不触发释放，纯 progress 不续租。

Coordination Summary 只读联接现有 DAG、Session、Admission、消息和质量记录，不建立新可写任务状态。CDC 仅通过新增 DB triggers 进入已有 change_log；不输出 token、环境或绝对敏感路径。

验证要求：双 Store 获取冲突、多 Repo 原子回滚、旧 epoch/代际释放拒绝、UNKNOWN 重启保留、双 Operator 单次消费、错正文/旧 SHA 拒绝、第二仓失败后第一仓保留、恢复不重复 Git。
