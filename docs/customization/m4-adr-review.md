# M4 ADR：独立 Review、有界修复与 Attempt 分离

状态：实施约束已确定，功能验收另见阶段台账。日期：2026-11-06。

`internal/review.Engine.TriggerWithOptions` 明确拒绝无 tracked PR 的 worker；现有 `ports.ReviewInvocation` 绑定 PRURL/TargetSHA。因此不伪造 PR，不把旧 PR PASS 转为本地 Artifact PASS。复用 `domain.ReviewVerdict`、Reviewer 注册和 M2 Collaboration；增加 Artifact 审查服务，真实 Reviewer 启动仍走 M3 Admission，离线阶段只使用 Fake 或经 Operator 审批的人工审查。

审查记录绑定 ArtifactID/revision/fingerprint、head/tree、policy revision、review run ID、服务端 Reviewer Attempt/Session/generation。作者与 Reviewer 必须不同，不能接受请求正文自报 Reviewer 身份。人工审查需已有精确 OS 审批链，记录独立操作者；没有可信身份时 UNVERIFIED。

Review 只提供结构化结论与 findings（路径/行/级别/依据），不能替代 Gate 退出码。过期回执与旧 SHA 不推进新工件。修复导致新 ArtifactRevision，旧 Gate/Review 全部失效。

`changes_requested` 通过 M2 TaskRequest 请求修复，scope 限于原 Repo/AllowedPaths；默认最多 2 轮，Operator 可批准上限 3。Agent 不能自批请求、改 DAG、预算或权限。M3 原子物化与 Admission 才能真正启动修复；UNKNOWN 不重复 Spawn。超限/预算不足转 BLOCKED。

质量侧车不替代 WorkflowTask/Attempt 状态机。Gate/Review PASS 不调用 Settle，不自动释放预算或路径 Reservation。

Worker 活动期的 Snapshot/证据写入必须在 writer 事务验证当前 live ExecutionPermit。Worker 已可信停止后的交付操作使用独立的内部交付校验：当前 recovered SchedulerFence、持久 binding/session/generation、终止事实、非 UNKNOWN Attempt、ArtifactRevision 和 Git 身份；不能借 `live=false` 取得执行权，不能重新启动 Provider。若当前实现尚缺这些停止事实，交付 BLOCKED。

验证要求：自审/伪身份拒绝、旧工件回执拒绝、两轮修复封顶、任务请求须 Operator、UNKNOWN/旧 epoch 拒绝、质量 PASS 不结算 Attempt。
