# M3 ADR-04：可信续租、MCP兼容与Operator边界

日期：2026-11-05。状态：续租、Operator及替代凭据迁移方案已通过独立设计复核并冻结。尚未实施，不解除G0，也不代表凭据迁移验收通过。

## 事实

- `daemon/workflow_wiring.go:26`是15分钟Attempt租约、每分钟过期扫描；没有持续续租。过期转UNKNOWN，不自动重跑。
- `session_manager/workflow_context.go:60`从持久化binding/attempt重建MCP上下文；过期时不附有效token。`principal/verifier.go:95`读当前数据库验证Attempt和token期限。
- LeaseToken已用于既有Claim/Start/Settle及受限Agent MCP能力链。直接声称“Agent/MCP从未持有LeaseToken”不符合当前源码。
- M2 Operator审批绑定method、RequestURI及原始body摘要，一次消费；已经消费的授权不能再作为可重用批准票据。

## 决策

1. 用户已明确禁止向Renderer、Agent、MCP或普通日志输出LeaseToken。M2现有进程凭据通道与该目标存在冲突，不能作为已批准的M3目标保留。实施M3-05/07前须完成不外发LeaseToken的替代能力凭据方案，并验证已启动MCP的迁移、失效和受控重连；方案未冻结时相关阶段保持BLOCKED，不能擅自豁免或仅删字段制造兼容假象。
2. Scheduler内部每60s观察活跃Attempt；必须核实Session/Controller、generation、Worker、绑定和实时Worktree，才在同事务验证当前epoch、未过期租约、期望revision/lease_until及状态后延长至now+15min。仅Worker自报心跳不够。
3. 租约已过期、generation错误、身份不符或观察不可信一律拒绝续期。UNKNOWN不自动重新领取；恢复既有Session与批准新执行是不同动作。
4. daemon内部可复用同一Attempt的token，续期仅改变期限/心跳与revision；这不授权向Agent/MCP外发。Start/Settle/Resolve、MCP verifier及恢复上下文必须在替代能力凭据方案下共同验证期限和撤销，不能只改一条SQL。
5. 全局/Adapter策略、预算、暂停/恢复及TaskRequest物化只接受可信Operator。新增动作接入一次性审批闭集、原始请求指纹、中间件和审计；Scheduler内部能力不通过HTTP伪装Operator，不向Agent开放续租或扩容审批权。
6. TaskRequest原先的Decide已消费自己的Operator批准。物化另需绑定具体请求/图revision的Operator批准，并在同一事务核验已批准记录、原payload SHA、Workflow revision、完整DAG与Repo/allowed_paths、任务数50/深度12/扇出8及预算权限。重复操作返回原物化结果，不再次消费成第二个任务。
7. 现有任意JSON申请必须经过按kind定义的严格执行schema；聊天文字或格式合法JSON不能直接物化。无支持schema则明确拒绝，不猜字段。

## 验收要求与限制

M3-05/06/07验证合法长任务续租、过期/错误generation/旧epoch拒绝、MCP替代凭据迁移及LeaseToken不外发、审批重放和DAG越权拒绝。本轮没有运行真实Provider、人工Windows Hello或新增续租接口；未验证或批准保留旧外发通道。

## 第六轮期间补充：替代凭据与升级路径

以下是待实现的具体设计，不改变第六轮按既有 M2 行为验收的范围。

1. **复用已有 Session capability。** MCP 仅携带 `AO_SESSION_ID`、Workflow/Attempt 非秘密标识及现有 `AO_BROWSER_CAPABILITY`，不铸造第二份凭据。服务器先验证当前 Session 的 capability verifier，再按 Session 查唯一 binding。客户端 Workflow/Attempt 标识只用于一致性约束，不能决定调用方的实际身份。
2. **从持久事实派生 Attempt 身份。** 同时核对 Session 未终止、binding 的 Session/Workflow/Task/Attempt、binding 与 Session 的非空 ControllerGeneration 一致、Attempt 所属关系和未过期租约。显式允许 binding 为 `bound_ready/running` 且 Attempt 为 `claimed/running`，不能用包含恢复不确定状态的 `HasSession` 替代白名单；generation 来自当前可信 Session/Controller。M3 admission 启用后还须核对当前 epoch 和该 admission 已恢复为可用。任一缺失、读失败、UNKNOWN 或不匹配均拒绝，不回退为 session-only 或 Operator 权限。续租和结算仍使用 daemon 内部 LeaseToken，Agent 不取得该值。
3. **清理所有外发通道。** 删除 `WorkflowSpawnContext` 的 LeaseToken 与初始/恢复 MCP env 注入；复用 env 时主动清除 `AO_WORKFLOW_LEASE`，包括无 Workflow 上下文和 capability 缺失的路径。CLI 不读取或发送 lease。服务器拒绝携带旧 `X-AO-Attempt-Lease` 的请求，给出不含凭据的升级错误；混带新旧凭据也拒绝。
4. **M1 HTTP 同步收口。** Claim 响应不再返回 LeaseToken；Start/Settle 的外部请求不接收 LeaseToken，由可信 Operator 审批后在服务端取得内部租约并执行相同状态机。Create/Activate/Claim/Retry/Recover 等改变图或执行资源的旧写入口一起接入一次性 Operator 审批；凡领取或启动资源均经过统一 admission，Create 等纯图操作不额外预留执行槽。不能因旧路由而保留旁路。配套调整 DTO、OpenAPI、CLI/HTTP 测试，保留安全与幂等断言。
5. **已有 MCP 不自动重启 Provider。** 当前 `LocalGateway.Resume` 在 Session 已活跃时直接返回观察，不重建 MCP env，因此不能宣称 Resume 会迁移旧进程。旧 MCP 的 lease 请求立即失效；新 MCP 使用现有 capability 及服务端绑定。没有单独 MCP 重连能力时，保留 Session 和 UNKNOWN/占位事实，报告需要安全重附着；仅在原有明确停止/恢复授权和真实状态验证满足后，由既有 Manager 恢复路径重建 env 与轮换 capability。不得为迁移重复 Spawn。
6. **能力边界不扩大。** capability 证明同一 Session 内的进程，不能证明某个具体 MCP 子进程；不把它升级为 Operator，也不允许改变预算、资源、图、epoch 或续租。不存在绑定的普通 Session 仍只有既有的 Session 级权限，不能据传入 AttemptID 获得 Workflow 身份。

实施接线：`service/principal/{principal,verifier}.go`、`httpd/controllers/workflows.go`、`cli/mcp_workflow.go`、`ports/execution.go`、`service/execution/coordinator.go`、`session_manager/{workflow_context,chat_spawn_workflow,chat_spawn}.go`；生成契约和全部受影响调用/夹具同步更新。

反例须证明：A capability 冒充 B、绑定缺失/错配、旧 capability 轮换、过期/结算/UNKNOWN、generation/epoch 迟到、混带 raw lease、初始/恢复 env 与 HTTP/tool 输出无 LeaseToken，以及 live Resume 不增加 Spawn 次数。测试只走临时 SQLite/Git/HTTP 与 Scripted Driver，不访问真实 Provider。

独立设计复核：`/root/m3_01_doc_review` 已对照实际源码确认方案可实施、未发现确定 P0/P1 设计缺口。权限解析只是读取验证，不能替代 ADR-03 的写事务 epoch/generation/revision CAS；副作用继续经过 M2 实时 Worktree 门禁。此项只解除方案未冻结的阻断，M3-05/07 的实现和验证仍未开始。

实施契约补充（2026-11-05）：CLI从现有`AO_SESSION_ID`读取Session标识，以`X-AO-Session-Id`发送；该字段不是凭据。HTTP先以此Session验证现有capability，再读取该Session的唯一binding，最后校验客户端Workflow/Attempt标识；不再先用客户端Attempt反查Session。初始和恢复环境除了MCP map，还需清除`runtimeEnvChecked`从旧projectEnv继承的`AO_WORKFLOW_LEASE`（包括Windows大小写别名）。独立Operator物化授权允许新增Task竞争现有global/adapter槽，不改变并发策略；Task预算则沿不可变物化父子关系共享原PolicyID及实际费用归因。
