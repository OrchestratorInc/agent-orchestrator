# M2 第三轮修复报告（R-01～R-08）

> 日期：2026-11-04（Asia/Shanghai）
> 工作区：`E:\code\agent-orchestrator`
> 分支：`feat/multi-agent-desktop`
> Git HEAD：`a6c284a4d060431371306072e6c80abfedf22328`（M1；M2 全部改动仍为本地未提交状态）
> 上一轮结论依据：[m2-reacceptance.md](m2-reacceptance.md)（第二轮独立复验，R-01～R-08）
> 安全边界：**0 个 provider turn、0 token**；未启动任何真实 Codex/Claude/OMP/DeepSeek Agent；未读写真实 `~/.ao` / `.codex` / `.claude`；未 commit、push 或 deploy。

**本报告是实施方（开发侧）的第三轮修复交付说明，不是验收结论。** 最终判定以独立复验为准；本文件不修改、不覆盖 [m2-acceptance.md](m2-acceptance.md)、[m2-reacceptance.md](m2-reacceptance.md)、[m2-report.md](m2-report.md)、[m2-remediation-report.md](m2-remediation-report.md)。

---

## 1. R-01～R-08 逐项修复

### R-01（P1）普通 Send / Interrupt 缺少 generation fencing

**复验原话要点**：`local_gateway.go` 的 `Send` 通过 `requireReady` 检查 Ready，但“没有比较 `request.ControllerGeneration` 与当前 observation 的 generation”；`Interrupt`“同样没有比较”；`SessionObservation.ValidateFence` “只有在 observation generation 和 expected generation 都非空时才比较；expected 非空而 observation 为空仍可能通过”；`messageOutcome` “对 Send 只检查 turn/queued 事实”。

**实际修改**

| 位置 | 变化 |
| --- | --- |
| `backend/internal/ports/execution.go:402-422` | `SessionObservation.ValidateFence` 改为三分支：`generation == ""`（无 fence）、`o.ControllerGeneration == ""` → 返回 `ErrExecutionStaleGeneration`（**这正是原 fail-open 的反面**）、不相等 → 返回带两侧值的 `ErrExecutionStaleGeneration`（`:413-418`）。 |
| `backend/internal/ports/execution.go:288-299` | `MessageRequest.Validate` 对 `delivery=send` 强制要求 `ControllerGeneration` 非空，注释写明“without the caller's generation there is nothing to fence against”。 |
| `backend/internal/ports/execution.go:374-385` | `MessageReceipt.MatchesSend` 新增 `r.ControllerGeneration != request.ControllerGeneration` 即 `false`，并要求 `Steered` 为假。 |
| `backend/internal/ports/execution.go:451-455` | `InterruptReceipt.MatchesInterrupt` 要求 receipt 的 generation 非空且等于请求。 |
| `backend/internal/service/chat/service.go:1455-1467` | 新增 `SendPinned(ctx, id, expectedGeneration, msg)`：空 generation 直接以 `ErrStaleControllerGeneration` 拒绝，随后走统一 `send`。 |
| `backend/internal/service/chat/service.go:1800-1810` | 新增 `InterruptPinned(ctx, id, expectedGeneration)`，同规则。 |
| `backend/internal/service/chat/controller.go:1510-1521` | `checkPinnedGeneration`：空值拒绝、与 `c.generation` 不等则返回 `ErrStaleControllerGeneration`。 |
| `backend/internal/service/chat/steer.go:69` | `ErrStaleControllerGeneration = errors.New("chat controller generation is stale")`。 |
| `backend/internal/service/execution/local_gateway.go:530-534` | `Send` 先 `observation.ValidateFence(request.SessionID, request.ControllerGeneration)`，再调 `g.chats.SendPinned(...)`。 |
| `backend/internal/service/execution/local_gateway.go:700-715` | `Interrupt` 先做“早期 fence”（`controllerGeneration(rec, true)`，空即拒绝），再调 `g.chats.InterruptPinned(...)`。 |
| `backend/internal/service/intervention/coordinator.go:760-800, 775` | `messageOutcome` 注释与实现：send 也必须通过 `MatchesSend`，receipt 不匹配一律 unknown（而非 applied/queued）。 |

**核心调用链（Send）**

```
WorkflowInterventions (HTTP) → intervention.Coordinator.Queue/Send
  → LocalGateway.Send(MessageRequest)
      → requireReady → SessionObservation.ValidateFence(session, generation)
      → chats.SendPinned(session, generation, msg)
          → chat.Service.send → controller.sendOnController
              → Controller.checkPinnedGeneration(expected)   // 在 dispatch 锁内
              → provider
      → MessageReceipt{ControllerGeneration} → MatchesSend(...)
```

**核心调用链（Interrupt）**

```
intervention.Coordinator.Stop → LocalGateway.Interrupt(InterruptRequest)
  → readSession → live := controllerGeneration(rec, true)；空则 stale 拒绝
  → chats.InterruptPinned(session, generation)
      → chat.Service.interrupt → controller.checkPinnedGeneration
  → InterruptReceipt{ControllerGeneration} → MatchesInterrupt(...)
```

**新增测试**

- `backend/internal/service/chat/send_pinned_test.go:31` `TestSendPinnedDispatchesAtTheCurrentGeneration`
- `:52` `TestSendPinnedRefusesAStaleGeneration`、`:78` `TestSendPinnedRefusesAnEmptyGeneration`、`:93` `TestSendPinnedIsIdempotentPerClientMessageID`
- `:118` `TestInterruptPinnedConfirmsAtTheCurrentGeneration`、`:132` `TestInterruptPinnedRefusesAStaleGeneration`、`:146` `TestInterruptPinnedRefusesAnEmptyGeneration`、`:160` `TestUnpinnedSendAndInterruptStillWork`
- `backend/internal/service/execution/local_gateway_test.go:1900` `TestLocalGatewaySendIsFencedByGeneration`、`:2010` `TestLocalGatewaySendSurfacesAControllerSwap`、`:2079` `TestLocalGatewayInterruptIsFencedByGeneration`、`:2153` `TestLocalGatewayInterruptSurfacesAControllerSwap`
- `backend/internal/service/execution/coordinator_test.go:1255` `TestLiveTargetFenceTable`
- `backend/internal/integration/m2_e2e_test.go:1622` `TestM2StaleGenerationIsRefused`

### R-02（P1）RepoRefs 被持久化但没有真正挂载到 Session 工作区

**复验原话要点**：`LocalGateway` 构造 `ports.SpawnConfig` 时“只传入一个 `ProjectID`……没有消费 `request.Repos`”；`SpawnConfig` “只有单个 `ProjectID`，没有多 Repo 列表或多 Worktree 规划输入”。

**实际修改**

| 位置 | 变化 |
| --- | --- |
| `backend/internal/ports/session.go:100-105` | `SpawnConfig` 新增 `Repos []SpawnRepo`；注释约定“EMPTY slice keeps the pre-M2 single-project behaviour byte-for-byte”。 |
| `backend/internal/ports/session.go:112-117` | 新增 `type SpawnRepo struct { RepoKey string; ProjectID domain.ProjectID; BaseSHA string }`。 |
| `backend/internal/ports/session.go:106-109` | 新增 `WorkflowSpawn *WorkflowSpawnContext`。 |
| `backend/internal/ports/execution.go:190-195` | `WorkflowSpawnContext{WorkflowID, TaskID, AttemptID, LeaseToken}`；注释声明 lease 只经 MCP 子进程环境传递。 |
| `backend/internal/ports/execution.go:200-213` | `SpawnRepos(refs []WorkflowRepoKeyRef) []SpawnRepo` —— 词汇表转换的唯一落点。 |
| `backend/internal/service/execution/local_gateway.go:386-387` | Spawn 构造 `SpawnConfig` 时写入 `Repos: ports.SpawnRepos(request.Repos)` 与 `WorkflowSpawn: request.Workflow`。 |
| `backend/internal/service/execution/coordinator.go:391-398` | Bind 时从 attempt 行读出 lease 并构造 `request.Workflow`（“the lease exists in exactly one place — the attempt row”）。 |
| `backend/internal/session_manager/multi_repo_workspace.go:67` | 新增 `createMultiRepoSessionWorkspace(...)`。 |
| `backend/internal/session_manager/manager.go:1343-1350` | Spawn：`if len(cfg.Repos) > 0` → 多仓库路径，并把 `repoInfos` 回填为 `WorkspaceProjectInfo`。 |
| `backend/internal/session_manager/chat_spawn_async.go:182-190` | 异步 Chat spawn 同规则分派。 |
| `backend/internal/session_manager/multi_repo_workspace.go:363` | 新增 `restoreMultiRepoSessionWorkspace(...)`；`manager.go:4612-4616` 在 `restoreSessionWorkspace` 中优先走它。 |

**核心调用链**

```
HTTP bind → execution.Coordinator.BindAttempt
  → store.GetWorkflowAttempt（取 lease）
  → request.Workflow = &WorkflowSpawnContext{...}
  → LocalGateway.Spawn → SpawnConfig{Repos, WorkflowSpawn}
      → Manager.Spawn → if len(cfg.Repos) > 0
          → createMultiRepoSessionWorkspace
              → resolveMultiRepoPlans（排序/去重/项目解析）
              → per repo: workspace.Create → verifyRepoWorktreeHead → UpsertSessionWorktree
          → 失败: rollbackMultiRepoWorktrees（children first）
```

**验证语义（对应修复门槛第 3 条）**：`multi_repo_workspace.go:134` 每个仓库创建后都 `verifyRepoWorktreeHead(ctx, ws.Path, ws.BaseSHA)`；`:138-150` 把读回的 HEAD 写入 `CreationSHA`，`BaseSHA` 保留对比基线；`:154-167` 只有核验通过的仓库才 `UpsertSessionWorktree`。

**新增测试**

- `backend/internal/session_manager/multi_repo_workspace_test.go:157` `TestMultiRepoSpawnCreatesAndVerifiesEveryWorktree`
- `:221` `TestMultiRepoSpawnRollsBackWhenTheSecondRepoFails`
- `:314` `TestMultiRepoSpawnIsIdempotentForOneClientRequest`
- `:357` `TestMultiRepoRestoreVerifiesEveryWorktree`
- `:408` `TestMultiRepoDirtyWorktreeSurvivesTeardown`
- `backend/internal/integration/m2_session_manager_e2e_test.go:699` `TestM2SessionManagerBindSpawnsRealWorktrees`
- `backend/internal/service/execution/coordinator_test.go:2055` `TestSingleRepoBindSucceedsWithoutWorktreeRows`、`:2080` `TestSingleRepoBindVerifiesAPresentWorktree`、`:2114` `TestMultiRepoBindIsBlockedByAPartialWorktreeList`、`:2144` `TestMultiRepoBindIsBlockedByAMismatchedHead`

### R-03（P1）Agent 协作 API 没有真实 Harness 可用通道与凭据上下文

**复验原话要点**：`ao mcp` “只暴露 `html_preview`、`html_render` 和 Computer Use 工具；没有 Workflow Message / Task Request 工具”；MCP 环境白名单“只有 `EnvSessionID`、`EnvRunFile`、`EnvDataDir`……也没有 Attempt lease”。

**实际修改**

| 位置 | 变化 |
| --- | --- |
| `backend/internal/cli/mcp_workflow.go:105-219` | 新增 5 个工具 schema：`workflow_message_send`(:107)、`workflow_messages_list`(:144)、`workflow_task_request_create`(:160)、`workflow_task_request_get`(:188)、`workflow_task_requests_list`(:204)；**schema 中没有任何身份字段**。 |
| `backend/internal/cli/mcp_workflow.go:34-49` | CLI 侧 env/header 常量：`AO_WORKFLOW_ID`/`AO_TASK_ID`/`AO_ATTEMPT_ID`/`AO_WORKFLOW_LEASE`/`AO_BROWSER_CAPABILITY`；`X-AO-Agent-Capability`/`X-AO-Attempt-Id`/`X-AO-Attempt-Lease`。 |
| `backend/internal/cli/mcp_workflow.go:71-79` | `workflowIdentityFromEnv`：workflow 与 attempt 必须成对出现，否则整组工具不可见。 |
| `backend/internal/cli/mcp_workflow.go:83-95` | `headers()`：缺失的 lease/capability **不发送**（“a missing lease is never mistaken for a proven one”）。 |
| `backend/internal/cli/mcp_workflow.go:234-239` | `workflowMCPTools`：非 workflow 会话返回 nil。 |
| `backend/internal/cli/mcp.go:133-135` | `tools/list` 追加 `workflowMCPTools(os.Getenv)`。 |
| `backend/internal/cli/mcp.go:147-151` | `tools/call` 分派 `isWorkflowMCPTool` → `callWorkflowTool`。 |
| `backend/internal/session_manager/chat_spawn_workflow.go:16-25` | daemon 侧同名常量（`EnvWorkflowID`/`EnvWorkflowTaskID`/`EnvWorkflowAttemptID`/`EnvWorkflowLease`）。 |
| `backend/internal/session_manager/chat_spawn_workflow.go:48-66` | `applyWorkflowMCPEnv`：把 attempt 身份 + lease + capability 写进该 MCP server 的 env。 |
| `backend/internal/session_manager/chat_spawn_workflow.go:31-41` | `applyWorkflowMCPEnvToServers`：capability 铸造完成后回填到所有 server env。 |
| `backend/internal/session_manager/chat_spawn.go:369-403` | `aoMCPServers(..., workflow *ports.WorkflowSpawnContext)`；`:396-399` 仅当 `workflow != nil` 时注入。 |
| `backend/internal/session_manager/chat_spawn.go:251` + `:280-282` | server 列表先构建，`PrepareControllerEnv` 里在 token 铸造后回填 capability。 |
| `backend/internal/storage/sqlite/queries/workflow_binding.sql:21` | 新增 `GetWorkflowAttemptBindingBySession :one` —— 按 session 反查 attempt 绑定，供 resume 使用。 |
| `backend/internal/storage/sqlite/store/workflow_binding_store.go:168` | 新增 `Store.GetWorkflowAttemptBindingBySession`（:161 注释；gen 侧 `gen/workflow_binding.sql.go:110` 常量、`:121` 方法）。 |
| `backend/internal/session_manager/workflow_context.go:60` | 新增 `workflowSpawnContext(ctx, rec)`：binding 给 attempt，attempt 行是 lease 的唯一持有者。 |
| `backend/internal/session_manager/workflow_context.go:110` | 新增 `workflowLeaseValid(attempt, now)`：空 token / nil 到期 / 到期不在未来 → 不可用，与 `principal` 校验器同一谓词。 |
| `backend/internal/session_manager/workflow_context.go:98-100` | **过期 lease 产出空 `LeaseToken`**，工具随后得到干净的 403，而不是一个死凭据。 |
| `backend/internal/session_manager/chat_spawn.go:564-571` | `resumeChatController` 调 `workflowSpawnContext` 并把结果交给 `aoMCPServers`。 |
| `backend/internal/session_manager/chat_spawn.go:602-604` | resume 侧 `PrepareControllerEnv` 在 capability 铸造后回填到 MCP server（与 spawn 同规则）。 |

**核心调用链**

```
【spawn】Manager.Spawn(cfg{Repos, WorkflowSpawn})
  → launchChatController
      → mcpServers := m.aoMCPServers(harness, env, cfg.WorkflowSpawn)   // attempt 身份 + lease
      → chat.StartChat{MCPServers, PrepareControllerEnv}
          → prepareChatControllerEnv → launchRuntimeEnv → browserCapabilities.Issue
          → applyWorkflowMCPEnvToServers(mcpServers, launchEnv[EnvBrowserCapability])
→ harness 启动 `ao mcp`（env 含 AO_WORKFLOW_ID / AO_ATTEMPT_ID / AO_WORKFLOW_LEASE / AO_BROWSER_CAPABILITY）
→ tools/call workflow_message_send → doJSONPathWithHeaders(identity.headers())
→ HTTP /api/v1/workflows/{id}/messages（服务端从 capability+lease 推导 sender）

【resume / 重启后】（Change A）
resumeChatController
  → workflowSpawnContext(rec)
      → store.GetWorkflowAttemptBindingBySession(rec.ID)   // binding 名出 attempt
      → store.GetWorkflowAttempt(binding.AttemptID)        // attempt 行持有 lease
      → workflowLeaseValid(...) ? LeaseToken : ""          // 过期则留空，绝不给死凭据
  → aoMCPServers(..., workflowContext)                     // 五个工具重新可见
  → PrepareControllerEnv → applyWorkflowMCPEnvToServers(...)  // capability 铸造后回填
```

**新增测试**

- `backend/internal/cli/mcp_workflow_test.go:138` `TestMCPWorkflowToolsListedOnlyForWorkflowSessions`、`:168` `TestMCPWorkflowSchemasDeclareNoIdentity`、`:202` `TestMCPWorkflowMessageSendUsesEnvironmentIdentity`、`:262` `TestMCPWorkflowRejectsANamedWorkflowArgument`、`:285` `TestMCPWorkflowClientMessageIDIsForwardedUnchanged`、`:317` `TestMCPWorkflowRefusalIsReadableAndSecretFree`、`:343` `TestMCPWorkflowToolWithoutIdentityMakesNoRequest`、`:366` `TestMCPWorkflowTaskRequestTools`
- `backend/internal/integration/m2_mcp_e2e_test.go:235` `TestM2MCPToolsOverTheRealServer`（真实 `ao mcp` 进程 + 真实 httptest daemon）
- **resume 恢复（Change A）**：`backend/internal/session_manager/workflow_context_test.go:105` `TestResumeRecoversWorkflowMCPEnvFromDurableState`、`:146` `TestResumeOmitsAnExpiredLease`、`:176` `TestResumeWithoutABindingAddsNoWorkflowKeys`、`:195` `TestWorkflowSpawnContextRequiresABoundSession`

### R-04（P1）Operator 身份不是 Human 身份；Resolve 写接口无 Principal 门禁

**复验原话要点**：`LoopbackOperatorTrust` “仅凭 `RemoteAddr` 是否为 loopback 就返回 Operator”；`resolveAttempt` “直接调用 `WorkflowService.Resolve`，没有调用 `c.caller` 或其他 Operator 授权校验”。

**实际修改**

| 位置 | 变化 |
| --- | --- |
| `backend/internal/service/principal/operator_token.go:56` | `HeaderOperatorToken = "X-AO-Operator-Token"`（header，永不做 body 字段）。 |
| `:60` / `:64` | `OperatorTokenFileName = "operator.token"`；`operatorTokenBytes = 32`。 |
| `:83-102` | `TokenOperatorTrust.PermitsOperator`：**必须** loopback 且 verifier 非 nil 且 header token 通过。 |
| `:128-136` | `NewTokenTrust`：`subtle.ConstantTimeCompare` 常量时间比较。 |
| `:142-179` | `LoadOrCreateOperatorToken`：`os.OpenFile(..., O_EXCL, 0o600)` 铸造 32B hex；已存在则复用（重启不换 token）。 |
| `:184-197` | `readOperatorToken`：非 64-hex 视为缺失（不信任被截断/手改的文件）。 |
| `:201-208` | `OperatorTokenFingerprint`：SHA-256 前 8 hex，仅此进审计。 |
| `:211-225` | `LoopbackPeer`。 |
| `backend/internal/service/principal/principal.go:143-162` | `LoopbackOperatorTrust` 保留但注释明确“It is NOT the operator, and it is NOT the production default”。 |
| `backend/internal/service/principal/verifier.go:152-157` | `WithOperatorTrust`：缺失即全部拒绝。 |
| `backend/internal/daemon/workflow_auth_wiring.go:27-44` | `newWorkflowPrincipals`：`principal.NewOperatorTokenTrust(dataDir)`；失败则 log 并保持 fail-closed（不中断 daemon 启动）。 |
| `backend/internal/daemon/daemon.go:1143` | `WorkflowPrincipals: newWorkflowPrincipals(cfg.DataDir, store, browserAuthority, log)`。 |
| `backend/internal/httpd/controllers/workflows.go:1488-1496` | `resolveAttempt`：`c.caller(...)` 后 `if !caller.IsOperator() || caller.LeaseProven { 403 COLLAB_NOT_AUTHORIZED }`。 |
| `:1718-1725` | `decideTaskRequest`：`if caller.IsAgent() || caller.LeaseProven { 403 }`。 |
| `:912-918` | `bind`：`if !caller.IsOperator() { 403 }`。 |
| `:1289-1293` | `recoveryPost`（reconcile/resume）：`if !caller.IsOperator() { 403 }`。 |
| `:1621-1625` / `:1671-1675` | `createMessage` / `createTaskRequest`：必须 `LeaseProven && AttemptID != ""`。 |
| `:2139-2149` | `recordDecision`：wired recorder 失败即让请求失败。 |
| `backend/internal/httpd/controllers/workflows.go:1517-1526` | resolve 审计（`Decision: "resolve_attempt"`）。 |
| `:1768-1777` | decide 审计（`Decision: "decide_task_request"`）。 |

**Resolve / Decide 拒绝矩阵**

| 调用者形态 | Resolve | Decide |
| --- | --- | --- |
| 无任何凭据 | 403 `COLLAB_NOT_AUTHORIZED` | 403 |
| 仅 loopback、无 `X-AO-Operator-Token` | 403 | 403 |
| loopback + 错误 token | 403 | 403 |
| 非 loopback + 正确 token | 403（`LoopbackPeer` 先行拒绝，`operator_token.go:91`） | 403 |
| loopback + 正确 token（operator） | 允许；成功且 applied 时写 1 条审计 | 允许；写 1 条审计 |
| agent capability + 有效 lease | 403（`IsOperator()` 为假且 `LeaseProven`） | 403 |
| agent capability + 无效/缺失 lease | 403（`ErrLeaseUnproven` → `principalError`） | 403 |
| operator token + attempt header | 403（`facts.AttemptID != ""` 而无 capability → `ErrLeaseUnproven`，`principal.go:253-255`） | 403 |
| 跨 workflow 的 attempt | 404 `WORKFLOW_ATTEMPT_NOT_FOUND`（`:1553-1560`） | 404 `WORKFLOW_REQUEST_NOT_FOUND`（`:2096-2099`） |

**审计表**：见 §2。拒绝路径**不写**审计；只有 applied 的决策写一行（`:1517`、`:1768`），replay 不写（`:1749-1763`）。

**新增测试**

- `backend/internal/service/principal/principal_test.go:477` `TestLoopbackPeerAloneIsNotTheOperator`、`:489` `TestWrongOperatorTokenIsRefused`、`:509` `TestNonLoopbackPeerWithAValidTokenIsRefused`、`:526` `TestUnconfiguredTokenTrustRefusesEverything`、`:554` `TestOperatorCallerCarriesTheTokenFingerprint`、`:580` `TestOperatorTokenFingerprintIsStableAndTokenFree`、`:601` `TestOperatorTokenIsMintedOnceReadableAndRotatable`、`:665` `TestOperatorTokenRequiresADataDir`、`:678` `TestAgentWithAValidOperatorTokenIsStillAnAgent`、`:729` `TestAuditRecordValidation`、`:761` `TestStubAuditRecorderIsNotANoOp`
- `backend/internal/httpd/controllers/workflows_m2_identity_test.go:141` `TestM2ResolveRequiresOperatorProof`、`:165` `TestM2ResolveWithAWrongOperatorTokenIsRefused`、`:186` `TestM2ResolveByAnAgentIsRefused`、`:244` `TestM2ResolveCrossWorkflowIs404AndChangesNothing`、`:268` `TestM2ResolveRecordsExactlyOneAuditRow`、`:315` `TestM2ResolveReplayChangesNothingTwice`、`:425` `TestM2DecideRequiresOperatorProof`、`:444` `TestM2DecideRecordsOneAuditRowAndReplaysWithoutASecond`、`:501` `TestM2DuplicateApprovalStays202WithOneDecisionAndOneAuditRow`、`:554` `TestM2DecideRefusesAnAgentEvenWithTheOperatorToken`、`:703` `TestM2BindRefusesWithoutOperatorProof`、`:722` `TestM2ReconcileAndResumeRequireOperatorProof`、`:751` `TestM2RouteWithAnInvalidLeaseIsForbidden`、`:809` `TestM2AuditFailureRefusesTheDecisionWhenWired`、`:829` `TestM2SessionsWithoutAnOperatorTrustRefuseEverything`
- `backend/internal/httpd/controllers/workflows_m2_bind_test.go:689` `TestM2RoutesRefuseUnauthenticatedNonLoopbackPeer`
- `backend/internal/storage/sqlite/store/workflow_audit_store_test.go:30` `TestWorkflowAuditAppendsOnce`、`:69` `TestWorkflowAuditRefusesAnUnreadableRecord`、`:104` `TestWorkflowAuditIsScopedAndOrdered`、`:154` `TestWorkflowAuditStoresNoCredential`

### R-05（P1）Capability Probe 的 generation 与 feature evidence fail-open

**复验原话要点**：`probe.go:82` “初始化时将 `snapshot.Generation = request.Generation`”；`applyLiveController` “没有把其 `live=false` 当作拒绝条件”；`probe.go:191` “只因 Controller 正在运行并报告能力，就把 Source 提升为 `confirmed_runtime`”；`applyAdvertised` “对 absent capability 保留 snapshot 原值”；Steer Handler “将请求体的 `req.Harness` 传入 Coordinator”。

**实际修改**

| 位置 | 变化 |
| --- | --- |
| `backend/internal/service/capability/probe.go:82-87` | 明确**不再**回填 `request.Generation`，注释：“Echoing it would make the comparison vacuous”。 |
| `:222-230` | `SessionGeneration` 返回 `!ok` 或空 → `unbindSession(..., CapabilityReasonSessionGenerationUnknown)` 并 return（不再继续）。 |
| `:264-273` | `clearNegotiated` 先把 8 个会话级 feature 全部置 unknown。 |
| `:231-237` | 先 clear、再 `applyNegotiated`，`Source` 固定为 `CapabilitySourceProviderHandshake`；`Generation` 只来自实观察值。 |
| `backend/internal/service/capability/resolver.go:280-294` | `Gate`：session 作用域证据必须 `ObservedSessionID` 与 `Generation` 双匹配，否则 unknown + `generation_mismatch`。 |
| `:295-305` | harness 级证据不能授权 session 请求。 |
| `backend/internal/httpd/controllers/workflows.go:1134-1175` | `interventionHarness`：harness 只从 ownership / task / 可信 session observation 推导；请求体 harness 仅作**校验**，不一致 → 409。 |
| `:1392-1394` | intervention 使用推导值，而非 `req.Harness`。 |

**新增测试**

- `backend/internal/service/capability/probe_test.go:378` `TestChatDriverProbeSessionScopedReportsANegotiation`、`:430` `TestChatDriverProbeSessionNeverWidensAnAbsentNegotiation`、`:486` `TestChatDriverProbeUnknownGenerationNeverAuthorizes`、`:541` `TestChatDriverProbeGenerationMismatchIsRefused`、`:574` `TestChatDriverProbeEmptyRequestGenerationAuthorizesNothing`、`:601` `TestChatDriverProbeSessionScopedWithoutControllerReportsNoSession`
- `backend/internal/service/capability/resolver_test.go:242` `TestGateRejectsDifferentSession`、`:266` `TestGateRejectsRotatedGeneration`、`:290` `TestGateRequiresGenerationForSessionRequest`、`:307` `TestGateHarnessScopedEvidenceCannotAuthorizeSession`
- `backend/internal/httpd/controllers/workflows_m2_identity_test.go:597` `TestM2InterventionRefusesABodyHarnessThatDisagrees`、`:623` `TestM2InterventionUsesTheTrustedHarnessNotTheBody`、`:653` `TestM2InterventionRefusesWhenNoHarnessCanBeDerived`
- `backend/internal/httpd/controllers/workflows_m2_authz_test.go:293` `TestM2CapabilitiesNeverClaimAnUnprobedSteer`、`:349` `TestM2SteerIsRefusedOnStaticEvidence`

### R-06（P2）Resume 对空 ClientRequestID 未 fail-closed

**实际修改**：`backend/internal/service/execution/local_gateway.go:802` 改为无条件严格比较：

```go
if rec.ClientRequestID != request.SpawnKey {
```

原 `rec.ClientRequestID != "" && ...` 的短路已删除；空存储值不再等于“匹配”。

**新增测试**：`backend/internal/service/execution/local_gateway_test.go:2169` `TestLocalGatewayResumeRequiresTheExactSpawnKey`（表驱动三例：空 id 拒绝 / 不同 key 拒绝 / 相同 key 放行 1 次 restore）。相关：`:1339` `TestLocalGatewayResumeRefusesAForeignSpawnKey`、`:1419` `TestLocalGatewayResumeRefusesAMissingSpawnKey`。

### R-07（P2）Fake E2E 没有验证真实 Session Manager / Git Worktree 组合

**实际修改**：新增 `backend/internal/integration/m2_session_manager_e2e_test.go`（真实 SQLite + 真实 `gitworktree` 适配器 + 真实 `session_manager.Manager` + scripted Chat driver + 真实 HTTP router；`m2_session_manager_e2e_test.go:41-60` 明确列出“真的”和“假的”清单）。Fake 仅剩：runtime（只回答 liveness）与 agent 注册表。

**新增测试**

- `:699` `TestM2SessionManagerBindSpawnsRealWorktrees`
- `:777` `TestM2SessionManagerDuplicateBindCreatesOneSession`
- `:817` `TestM2SessionManagerQueueSteerStopKeepsWorktree`
- `:904` `TestM2SessionManagerRestartReattachesExistingWorktree`
- `:980` `TestM2SessionManagerRestartReportsAMissingWorktree`
- `:1069` `TestM2SessionManagerAbnormalExitIsReported`

### R-08（P2）Task Request 批准尚未形成可执行 Task

**实际修改**：`backend/internal/domain/workflow_collaboration.go:442-451` 引入三值 `WorkflowTaskRequestMaterialization`（`not_applicable` / `pending` / `materialized`）；`backend/internal/httpd/controllers/workflows.go:1800-1805` `decideStatus` 对 `pending` 返回 **202**（原为 501）；`:1813-1820` `materializationFor` 把 approved 映射为 pending；`:1830-1845` 拒绝自相矛盾的组合。

**边界声明**：`workflows.go:1794-1799` 注释明写“that case is unreachable in M2 and is mapped here rather than special-cased away, so M3 does not have to rediscover the rule”。即 **M2 不 materialize Task**，只在 API 上如实回答“已批准但尚未创建”。

**新增测试**：`backend/internal/service/collaboration/coordinator_test.go:518` `TestDecideApprovalNeverMaterializes`；`backend/internal/httpd/controllers/workflows_m2_identity_test.go:501` `TestM2DuplicateApprovalStays202WithOneDecisionAndOneAuditRow`。

---

## 2. 新增数据库结构

### 2.1 `20261104094706356_workflow_audit_log.sql`（唯一本轮新增 migration）

| 事实 | 位置 |
| --- | --- |
| 文件头 `-- +goose NO TRANSACTION` | `migrations/20261104094706356_workflow_audit_log.sql:1` |
| 建表 `workflow_audit_log` | `:30-42`（`id` PK、`actor_kind` CHECK `('operator','agent')`、`actor_ref`、`workflow_id`、`attempt_id`、`request_id`、`decision`、`outcome`、`token_fingerprint`、`created_at`） |
| 索引 `idx_workflow_audit_log_workflow` | `:48`（`workflow_id, created_at`） |
| Down 段 | `:52-56`（drop index + drop table） |
| 版本号登记（append-only） | `storage/sqlite/migrate_burned_versions_test.go:252` |

**append-only 的证据**：

- 迁移注释 `:12-14`：“APPEND-ONLY. No UPDATE or DELETE statement exists in the queries”。
- 查询文件 `storage/sqlite/queries/workflow_audit.sql` 只有两条语句：`InsertWorkflowAudit`（`:14`，`ON CONFLICT DO NOTHING`）与 `ListWorkflowAudits`（`:24`）。**没有 UPDATE、没有 DELETE。**
- Store `storage/sqlite/store/workflow_audit_store.go:46-72` 只有 `RecordSensitiveDecision`；`:75` `AuditEntry`；`:92` `ListWorkflowAudits`。无 update/delete 方法。

**无外键的证据**：`:20-24` 注释“NOT a foreign-key child”，`workflow_id` 为纯 TEXT 无 `REFERENCES`；测试 `storage/sqlite/migrate_workflow_audit_log_test.go:18` `TestMigrationWorkflowAuditLogSchema`（`:70-75` 断言 `PRAGMA foreign_key_list` 为空）、`:80` `TestMigrationWorkflowAuditLogDownRoundTrips`。

**无 CDC 触发器的证据**：`:26-28` 注释；同一测试 `:59-64` 断言 `sqlite_master` 中 `tbl_name='workflow_audit_log'` 的 trigger 数为 **0**。

### 2.2 为 R-03 新增的 env 键与 ports 类型

| 类别 | 名称 | 位置 |
| --- | --- | --- |
| env（daemon 侧常量） | `EnvWorkflowID="AO_WORKFLOW_ID"`、`EnvWorkflowTaskID="AO_TASK_ID"`、`EnvWorkflowAttemptID="AO_ATTEMPT_ID"`、`EnvWorkflowLease="AO_WORKFLOW_LEASE"` | `session_manager/chat_spawn_workflow.go:17,19,22,25` |
| env（CLI 侧镜像常量） | `envMCPWorkflowID`/`envMCPTaskID`/`envMCPAttemptID`/`envMCPLeaseToken`/`envMCPCapability` | `cli/mcp_workflow.go:35-40` |
| header | `X-AO-Agent-Capability` / `X-AO-Attempt-Id` / `X-AO-Attempt-Lease` | 客户端 `cli/mcp_workflow.go:46-48`；服务端 `service/principal/principal.go:56`（`HeaderAgentCapability`）、`:60`（`HeaderAttemptLease`）、`:65`（`HeaderAttemptID`） |
| header | `X-AO-Operator-Token` | `service/principal/operator_token.go:56` |
| ports 类型 | `ports.SpawnRepo` | `ports/session.go:113` |
| ports 类型 | `ports.WorkflowSpawnContext` | `ports/execution.go:190` |
| ports 函数 | `ports.SpawnRepos` | `ports/execution.go:200` |
| ports 字段 | `SpawnConfig.Repos` / `SpawnConfig.WorkflowSpawn` | `ports/session.go:105` / `:109` |
| ports 字段 | `SpawnRequest.Repos` / `SpawnRequest.Workflow` | `ports/execution.go:171` / `:180` |
| 文件 | `<dataDir>/operator.token`（非 DB） | `service/principal/operator_token.go:60` |

### 2.3 为 R-02 新增的 schema

**无新增 migration。** 多仓库复用既有 `session_worktrees(session_id, repo_name)` 表（`migrations/0009_workspace_projects.sql:16`）。新增的 `SpawnRepo`/`Repos` 只是输入通道；落库仍是每 repo 一行。

### 2.4 其他 schema 变更

**无。** 本轮相对第二轮唯一新增 migration 是 `20261104094706356_workflow_audit_log.sql`。`20261104020830811_harness_capability_snapshots.sql` 属第二轮（见 [m2-remediation-report.md](m2-remediation-report.md) §5），本轮未改动。

---

## 3. 安全权限与 Principal 验证方式

### 3.1 Operator token

| 属性 | 实现 |
| --- | --- |
| 位置 | `<dataDir>/operator.token`（`operator_token.go:60`） |
| 内容 | 32 字节 `crypto/rand` → hex，64 字符（`:153-157`） |
| 文件权限 | `os.OpenFile(path, O_WRONLY|O_CREATE|O_EXCL, 0o600)`（`:161`） |
| 并发 | `O_EXCL`；第二个 daemon 启动直接采纳既有文件（`:162-169`） |
| 重启 | 每次启动重新读取同一文件，不轮换（`:147-149`） |
| 传输限制 | **必须** loopback 对端（`:91`） |
| 比较 | `subtle.ConstantTimeCompare`（`:134`） |
| 审计 | 只有 `OperatorTokenFingerprint`（SHA-256 前 8 hex，`:201-208`）入 `workflow_audit_log.token_fingerprint` |
| 失败模式 | 读/铸失败 → log warn，resolver 保持 fail-closed（`workflow_auth_wiring.go:37-42`） |
| 轮换 | 删除文件即轮换（`:36-43` 注释） |

### 3.2 Agent 身份

Agent 必须同时提供 **capability**（`X-AO-Agent-Capability`）与 **attempt lease**（`X-AO-Id` + `X-AO-Attempt-Lease`）：

- capability → `principal.agent`（`principal.go:282-299`）：会话必须存在、未终止、`BrowserCapabilityVerifier` 非空，且 `VerifyAgentCapability` 通过。
- lease → `principal.go:305-324`：无 attempt 声明则仅为 session-scoped agent；有 attempt 声明则 lease 必备且必须被 `ProveWorkflowAttemptLease` 证明。
- lease 验证：`service/principal/verifier.go:95-130` —— attempt 存在、`workflow_id` 匹配、`LeaseToken` 非空、`LeaseUntil` 未过期、token 相等。

### 3.3 Resolve / Decide 拒绝矩阵

见 §1 R-04 表格。补充：

- 所有身份拒绝统一映射为 403 `COLLAB_NOT_AUTHORIZED`（`workflows.go:809-817`），不泄漏是哪个检查失败。
- 跨 workflow 一律 404（`workflows.go:1553-1560`、`:2096-2099`），避免确认另一 workflow 资源存在性。
- `bind` 与 `reconcile/resume` 额外要求 `IsOperator()`（`:912`、`:1289`）。
- `createMessage` / `createTaskRequest` 反向要求 `LeaseProven`（`:1621`、`:1671`）——operator 不能冒充 task 发言。

### 3.4 审计

- 只有 **applied** 的决策写一行；replay 不写（`workflows.go:1478-1480`、`:1745-1763`）。
- 字段全部服务端推导：actor 来自已证明的 caller，subject 来自路由，request id 来自中间件（`:2136-2138`）。
- wired recorder 失败 → 请求失败（`WORKFLOW_AUDIT_UNAVAILABLE`，`:1523-1525`、`:1774-1776`）。

### 3.5 残余风险（诚实声明）

1. **同 OS 用户可读 token 文件**：`operator_token.go:45-53` 原文声明“Any process running as the same OS user can read the file. This does not make the token stronger than the transport it protects; it makes the authority EXPLICIT and revocable”。这是设计上的**已知边界**，不是缺陷修复项。
2. **Windows 不强制 0600**：`os.OpenFile` 的 perm 参数在 Windows 被忽略；测试 `principal_test.go:628-636` 用 `runtime.GOOS != "windows"` 跳过权限断言，并在注释中说明平台差异。Windows 上实际保护仅来自 NTFS 继承 ACL。
3. **token 一旦泄漏即可重放**：审计只记录指纹，无法撤销单次使用；轮换手段为删除文件。

---

## 4. 多仓库 Worktree 测试结果

**测试载体**：`backend/internal/session_manager/multi_repo_workspace_test.go`（真实 git，非 fake）。

**夹具（`multi_repo_workspace_test.go:42-105`）**

- 两个**真实临时 git 仓库**，各两个 commit（`newMultiRepoGitRepo`，`:108-127`）；断言两仓库 base commit 不同（`:53-55`）。
- 迁移后的真实 SQLite store（`sqlitetest.MustOpenAt`，`:48`）。
- 真实 `gitworktree` 适配器，managed root 为第三个临时目录（`:72-79`）。

**Worktree 布局**

`<managedRoot>/<projectID>/<sessionID>`（`adapters/workspace/gitworktree/workspace.go:2349`）；orchestrator 为 `<managedRoot>/<projectID>/orchestrator/<prefix>-orchestrator`（`:2347`）。

**逐项结果（测试断言的事实）**

| 验证点 | 断言位置 | 断言内容 |
| --- | --- | --- |
| 每 repo 一行 | `multi_repo_workspace_test.go:174-176` | `len(rows) == 2`，`repo-a` 与 `repo-b` 都在 |
| root 选择确定性 | `:187-190` | 按 repo key 首个（`repo-a`）＝`rec.Metadata.WorkspacePath`；夹具故意乱序输入（`:138-143`） |
| 每个 repo 的 **HEAD 回读校验** | `:203-206` | `git rev-parse HEAD` 必须等于该行 `CreationSHA` |
| 每个 repo 的 branch 校验 | `:207-209` | `rev-parse --abbrev-ref HEAD` 必须等于记录 branch |
| 注册归属正确 | `:198-201` | 每个路径必须出现在**它自己的**仓库的 `git worktree list --porcelain` 中 |
| 独立 base commit | `:213-216` | `repo-b` HEAD == 夹具第二个 commit |
| **部分失败回滚** | `:221-260+` | 第二个 repo 指向非 git 目录 → spawn 返回 `ErrWorkspaceCreate`，且 live session 数为 0 |
| **restore 逐 repo 校验** | `:357-403` | 健康时 restore 成功；删掉**一个** worktree 后返回 `ErrMultiRepoWorktreeMissing`，且幸存 worktree 仍在磁盘上 |
| **dirty worktree 保留** | `:408-439` | 往某个 worktree 写入 `keep-me.txt` 后 `Kill`，文件与目录均仍存在 |

**产品路径上的同一事实**：`backend/internal/integration/m2_session_manager_e2e_test.go:699` `TestM2SessionManagerBindSpawnsRealWorktrees` 走完 HTTP bind → Coordinator → LocalGateway → **真实 SessionManager** → 真实 git，并断言：2 个 worktree 行（`:740-742`）、每个都在磁盘上（`:752-754`）、每个都被自己的仓库注册（`:755-757`）、每个 HEAD 非空（`:758-760`）、`WorkspacePath` 等于 repo-a 的 worktree（`:765-767`）。

**部分失败后的 session 状态**：`rollbackMultiRepoWorktrees`（`multi_repo_workspace.go:288-310`）按 `len(created)-1 → 0` 逆序 Destroy 已创建的 worktree（children first，`:290`），随后 `DeleteSessionWorktrees` 清掉本次已写入的行（`:305`）。成功路径上只有核验通过的仓库才会 `UpsertSessionWorktree`（`:154-167`）。

**实测结果**：多仓库的 5 个场景属于 C14（`go test ./internal/session_manager -count=1`，整包在跑）；该包 exit 1 仅因 §6.4 列出的 7 条 Windows 基线，**5 个场景本身全部通过**（两次复跑集合逐条一致）。上述产品路径的 6 条 `TestM2SessionManager*` 属于 C16，C16 为 **RUN 24 / PASS 24 / FAIL 0 / SKIP 0**；多仓库并发路径另由 C20 在 race 下覆盖（73.4s，无 race）。

---

## 5. Agent MCP 测试结果

**测试载体**：`backend/internal/integration/m2_mcp_e2e_test.go:235` `TestM2MCPToolsOverTheRealServer` —— 启动**真实** `ao mcp` stdio 进程，指向真实 httptest daemon（`mcpDeps`，`:88+` 写真实 run-file）。

| 验证点 | 断言位置 | 事实 |
| --- | --- | --- |
| `tools/list` 五个工具 | `:260-268` | 断言 `workflow_message_send`、`workflow_messages_list`、`workflow_task_request_create`、`workflow_task_request_get`、`workflow_task_requests_list` 全部在列表中 |
| 非 workflow 会话不广告 | `:241-252` | 清空 env 后列表中没有任何 `workflow_` 前缀工具 |
| **schema 不含身份字段** | `:269-280` | 遍历每个工具 `inputSchema.properties`，命中 `workflowid`/`attemptid`/`lease`/`leasetoken`/`capability`/`sender`/`sendertaskid` 即 fail |
| A → B 消息 | `:284-315` | 发送后 store 中新增 **恰好 1 行**；`senderKind == "agent"`（服务端推导）；`fromTaskId` 非空且等于 A 的 task |
| **幂等 clientMessageId** | `:348-368` | 重放返回同一 `id`、`created=false`、行数不变、CDC 事件仍为 1 条 |
| **CDC 一行** | `:328-346` | `change_log` 中该 message 恰好 1 条，type=`workflow_message_created`，payload 的 `kind`/`workflowId` 正确 |
| **跨 workflow 拒绝** | `:417-433` | 用 A 的 capability+attempt+lease 请求另一 workflow 的 `/messages`、`/task-requests`，状态必须是 **404 或 403** |
| **lease / capability 不出现在任何回包** | `:435-444` | 遍历全部 7 个回复正文，若包含 `identity.lease` 或 `identity.capability` 即 fail（`:342-346` 同样检查 CDC payload） |
| task request 三工具 | `:379-415` | create → state `pending`；get → 读回同一 id/state；list → 含该 id |

**CLI 侧单测**（不启 daemon 的真实子进程，直测 `commandContext`）：`backend/internal/cli/mcp_workflow_test.go` 的 8 个测试（见 §1 R-03）。

**实测结果**：该测试属于 C16 的 24 条之一，C16 为 **RUN 24 / PASS 24 / FAIL 0 / SKIP 0**（见 §6.3）；其 race 版本包含在 C18（exit 0，97.6s，无 race）中。

**未在本节覆盖的**：真实 Codex/Claude/OMP/DeepSeek provider 是否会真的调用这 5 个工具 —— 属 `NOT_TESTED`（见 §9）。

---

## 6. 完整 E2E 场景清单与退出码

> **说明**：本节是 **2026-11-04 最终一轮验证**的实测结果。环境：Windows、Go 1.27.1（auto toolchain）、Node 22.20.0；**所有命令串行执行**，未并发。命令编号 C1～C23 与 §6.3 一一对应。
>
> 与前一轮相比有两处代码变更（均已包含在下表中）：**Change A** 关闭 §9 第 7 项（resume 后 attempt 身份不重建），**Change B** 修复前一轮暴露的 2 条本轮自身缺陷并收窄守卫。因此 §6.4 的定性是「**本轮自身缺陷 2 条，均已修复并复测 exit 0**」，而不再是「仍有 2 条待修」。

### 6.1 命令清单

| # | 命令 | 覆盖场景 |
| --- | --- | --- |
| C1 | `git rev-parse HEAD` | 基线确认 |
| C2 | `git status --porcelain \| wc -l` | 脏文件计数（开工/收尾一致性） |
| C3 | `git diff --check` | 空白错误检查 |
| C4 | `cd backend && go build ./...` | 全量构建 |
| C5 | `cd backend && go vet ./...` | 全量静态检查 |
| C6 | `go test ./internal/ports ./internal/service/{execution,intervention,capability,principal,collaboration,workflow} -count=1` | 7 个 M2 核心包 |
| C7 | `go test ./internal/service/chat -run 'Send\|Interrupt\|Steer' -count=1` | R-01 pinned 入口 |
| C8 | `go test ./internal/storage/sqlite -count=1` | 迁移 + 收窄后的审批边界守卫 |
| C9 | `go test ./internal/storage/sqlite/store -count=1` | 完整 store 包 |
| C10 | `go test ./internal/httpd/controllers -count=1` | 完整 controllers 包 |
| C11 | `go test ./internal/daemon -count=1` | 完整 daemon 包 |
| C12 | `go test ./internal/daemon -run TestM2 -count=1` | Daemon M2 wiring 定向 |
| C13 | `go test ./internal/cli -count=1` | 完整 cli 包 |
| C14 | `go test ./internal/session_manager -count=1` | 完整 session_manager 包（含 Change A 的 4 条新测试） |
| C15 | `go test ./internal/integration -count=1` | 完整 integration 包 |
| C16 | `go test ./internal/integration -run TestM2 -count=1 -v` | M2 E2E 全链（24 个，见 §6.2） |
| C17 | `go test -race ./internal/service/{execution,intervention,capability,principal,collaboration} -count=1` | Race，5 个 M2 service 包（见 §7） |
| C18 | `go test -race ./internal/integration -run TestM2 -count=1` | Race，M2 E2E（见 §7） |
| C19 | `go test -race ./internal/httpd/controllers -run TestM2 -count=1` | Race，M2 controllers（见 §7） |
| C20 | `go test -race ./internal/session_manager -run 'Workflow\|MultiRepo\|MCP' -count=1` | Race，Change A 的 resume 恢复路径 + 多仓库（见 §7） |
| C21 | `npm run sqlc` + gen 漂移检查 | sqlc 生成物无漂移 |
| C22 | `npm run api` + openapi/schema 哈希 | OpenAPI + TS schema 无漂移 |
| C23 | `npm run frontend:typecheck` | 前端类型检查 |

### 6.2 M2 E2E 场景（24 个，`-run TestM2` 全量）

C16 的 **RUN 24 / PASS 24 / FAIL 0 / SKIP 0** 由四个文件组成（14 + 1 + 6 + 3 = 24），其中**本轮新增两条链**并已包含在这 24 条内：

- **MCP 链（R-03，1 条）**：`TestM2MCPToolsOverTheRealServer` —— 启动真实 `ao mcp` stdio 进程，经真实 HTTP 路由落到 collaboration/workflow 服务，覆盖 `tools/list` 五工具、schema 无身份字段、A→B 消息、幂等 `clientMessageId`、CDC 单行、跨 workflow 拒绝、回包无凭据。
- **真实 Session Manager 链（R-02/R-07，6 条）**：真实迁移 SQLite + 真实 `gitworktree` 适配器 + 真实 `session_manager.Manager` + scripted Chat driver + 真实 HTTP router，覆盖真实 git worktree 的绑定、重复绑定、Queue/Steer/Stop 保留工作区、重启重挂、缺失工作区上报、异常退出上报。

**A. 服务层 Fake HTTP E2E（14 条，`backend/internal/integration/m2_e2e_test.go`）**

| # | 测试名 | 行号 |
| --- | --- | --- |
| 1 | `TestM2CreateActivateClaimStart` | 693 |
| 2 | `TestM2BindIsDurableAndSpawnsOnce` | 732 |
| 3 | `TestM2TwoHarnessesBindTwoSessions` | 801 |
| 4 | `TestM2QueueDeliversANormalMessage` | 856 |
| 5 | `TestM2SteerOnTheSteerableHarness` | 910 |
| 6 | `TestM2SteerRefusedThenQueueableOnTheQueueOnlyHarness` | 1000 |
| 7 | `TestM2AgentCollaborationMessageAndCrossWorkflowRefusal` | 1058 |
| 8 | `TestM2TaskRequestRaisedByAgentDecidedByOperator` | 1164 |
| 9 | `TestM2StopConfirmsTheBindingAndRefusesLaterQueue` | 1236 |
| 10 | `TestM2InterruptedSessionReportsNonLiveWithAReason` | 1293 |
| 11 | `TestM2RestartReadsBindingsWithoutInMemoryState` | 1388 |
| 12 | `TestM2ReconcileAdoptsACommittedSession` | 1467 |
| 13 | `TestM2CrossWorkflowIsolation` | 1554 |
| 14 | `TestM2StaleGenerationIsRefused` | 1622 |

**B. MCP 链（1 条，`backend/internal/integration/m2_mcp_e2e_test.go`）**

| # | 测试名 | 行号 |
| --- | --- | --- |
| 15 | `TestM2MCPToolsOverTheRealServer` | 235 |

**C. 真实 Session Manager 链（6 条，`backend/internal/integration/m2_session_manager_e2e_test.go`）**

| # | 测试名 | 行号 |
| --- | --- | --- |
| 16 | `TestM2SessionManagerBindSpawnsRealWorktrees` | 699 |
| 17 | `TestM2SessionManagerDuplicateBindCreatesOneSession` | 777 |
| 18 | `TestM2SessionManagerQueueSteerStopKeepsWorktree` | 817 |
| 19 | `TestM2SessionManagerRestartReattachesExistingWorktree` | 904 |
| 20 | `TestM2SessionManagerRestartReportsAMissingWorktree` | 980 |
| 21 | `TestM2SessionManagerAbnormalExitIsReported` | 1069 |

**D. Zero-provider 守卫（3 条，`backend/internal/integration/m2_zero_provider_test.go`）**

| # | 测试名 | 行号 |
| --- | --- | --- |
| 22 | `TestM2E2EStartsNoProviderProcess` | 31 |
| 23 | `TestM2E2ENeedsNoProviderEnvironment` | 78 |
| 24 | `TestM2E2ENeverTouchesAUserDataDirectory` | 111 |

计数核对：`grep -c '^func Test'` 于四个文件分别为 14 / 1 / 6 / 3，合计 **24**，与 C16 的 RUN 数一致；`internal/integration` 内没有其他 `TestM2*` 前缀测试。

**Change A 的 4 条 resume 恢复测试不在上述 24 条内**——它们位于 `backend/internal/session_manager/workflow_context_test.go`，由 C14（完整 session_manager 包）与 C20（race）执行：`TestResumeRecoversWorkflowMCPEnvFromDurableState`(:105)、`TestResumeOmitsAnExpiredLease`(:146)、`TestResumeWithoutABindingAddsNoWorkflowKeys`(:176)、`TestWorkflowSpawnContextRequiresABoundSession`(:195)。

### 6.3 退出码

| # | 命令 | 退出码 | 实际输出摘要 |
| --- | --- | --- | --- |
| C1 | `git rev-parse HEAD` | **0** | `a6c284a4d060431371306072e6c80abfedf22328`（与基线一致，未变） |
| C2 | `git status --porcelain \| wc -l` | **0** | **151** 项；开工与收尾一致，**无新增脏文件** |
| C3 | `git diff --check` | **0** | 无空白错误；仅 46 条 LF→CRLF 提示 |
| C4 | `go build ./...` | **0** | 9.05s |
| C5 | `go vet ./...` | **0** | 15.17s |
| C6 | `go test ./internal/ports ./internal/service/{execution,intervention,capability,principal,collaboration,workflow} -count=1` | **0** | 7 包全部 `ok` |
| C7 | `go test ./internal/service/chat -run 'Send\|Interrupt\|Steer' -count=1` | **0** | 35.8s |
| C8 | `go test ./internal/storage/sqlite -count=1` | **0** | 133.2s；**含收窄后的审批边界守卫与新的 `TestWorkflowTaskRequestReadMatcher`** |
| C9 | `go test ./internal/storage/sqlite/store -count=1` | **0** | 56.1s |
| C10 | `go test ./internal/httpd/controllers -count=1` | **1** | 4 条，**全部为 Windows 既有基线**：`TestListDirsRejectsRelativeAndMissingPaths`、`TestBridgeStatusConcurrentSecurePairing`、`TestArtifactFileRouteServesOnlyRegularFilesInsideTheDirectory`、`TestProjectsAPI_Clone`。**前一轮的 `TestTaskRequestResponseCarriesNoRawPayload` 已修复，不再出现** |
| C11 | `go test ./internal/daemon -count=1` | **1** | 2 条 Windows 基线：`TestStabilizeWorkingDirectoryChdirsToDataDir`、`TestWiring_StartSessionSpawnsScratchWithoutGitRepo` |
| C12 | `go test ./internal/daemon -run TestM2 -count=1` | **0** | 17.9s |
| C13 | `go test ./internal/cli -count=1` | **1** | 1 条 Windows 基线：`TestRenderCheckReplacesItsEarlierScreenshot`（symlink 权限） |
| C14 | `go test ./internal/session_manager -count=1` | **1** | 7 条，**全部为 Windows 既有基线**（两次复跑集合逐条一致，**非 flaky**）：`TestChatStartsPassTheAOToolServerWithOnlyDaemonCoordinates`、`TestInterfaceTransitionReservedTranscriptRequiresUntouchedTerminal`、`TestInterfaceTransitionNativeHistoryOwnership`、`TestSpawn_DefaultsBranchUnderDevNamespaceForDevDataDir`、`TestSpawnAndRestore_PrependsResolvedBinaryAndNodeDirsToRuntimePATH`、`TestSpawn_DoesNotAddNodeRuntimeForNativeBinary`、`TestSend_NoEnterNudgeAfterTheOperatorTypes`。**包内 Change A 的 4 条新测试均通过** |
| C15 | `go test ./internal/integration -count=1` | **1** | 1 条 Windows 基线：`TestDelegateEndpointDoesNotDependOnCodexDeviceReconciliation` |
| C16 | `go test ./internal/integration -run TestM2 -count=1 -v` | **0** | **RUN 24 / PASS 24 / FAIL 0 / SKIP 0**（构成见 §6.2） |
| C17 | `go test -race ./internal/service/{execution,intervention,capability,principal,collaboration} -count=1` | **0** | 5 包 `ok`，**无 race** |
| C18 | `go test -race ./internal/integration -run TestM2 -count=1` | **0** | 97.6s，**无 race** |
| C19 | `go test -race ./internal/httpd/controllers -run TestM2 -count=1` | **0** | 4.8s，**无 race** |
| C20 | `go test -race ./internal/session_manager -run 'Workflow\|MultiRepo\|MCP' -count=1` | **0** | 73.4s，**无 race**；覆盖 Change A 的 resume 恢复路径与多仓库路径 |
| C21 | `npm run sqlc` + gen 漂移 | **0** | sqlc v1.31.1；gen 清单 **41→41**，前后 diff 完全相同，**无漂移** |
| C22 | `npm run api` + openapi/schema 哈希 | **0** | 前后 sha256 完全一致：`openapi.yaml` `1938f4a2…c2530e37`；`schema.ts` `df7e6d66…70eb1b32`，**无漂移** |
| C23 | `npm run frontend:typecheck` | **0** | `tsgo --noEmit` 无错误，38.9s |

**说明**：C10/C11/C13/C14/C15 的 `exit 1` **全部**来自 Windows 既有基线（逐条归类见 §6.4），**不含任何本轮缺陷**。前一轮暴露的 2 条本轮自身缺陷已修复并复测为 exit 0（C8、C10）。

**净新增回归 = 0。**

**未执行** `go test ./...` 全仓：包含与零 Provider 验收无关、可能进入真实 Harness/本机凭据路径的测试。C8～C15 为「整包」结果，因此同时暴露了 Windows 既有基线失败。

**仓库状态（最终一轮结束时）**：HEAD 仍为 `a6c284a4d060431371306072e6c80abfedf22328`；工作区 151 项（含未跟踪）；**未 commit、未 push**。

### 6.4 与 Windows 基线的归类

基线来源：[m2-baseline-windows.md](m2-baseline-windows.md)（只读全仓红基线）。下表把最终一轮每一条失败归类，并标出前一轮的 2 条本轮缺陷的处置结果。

| 包 | 退出码 | 失败数 | 归类 | 明细 |
| --- | ---: | ---: | --- | --- |
| `internal/httpd/controllers` (C10) | 1 | 4 | **Windows 既有基线** | `TestListDirsRejectsRelativeAndMissingPaths`、`TestBridgeStatusConcurrentSecurePairing`、`TestArtifactFileRouteServesOnlyRegularFilesInsideTheDirectory`、`TestProjectsAPI_Clone` |
| `internal/daemon` (C11) | 1 | 2 | **Windows 既有基线** | `TestStabilizeWorkingDirectoryChdirsToDataDir`、`TestWiring_StartSessionSpawnsScratchWithoutGitRepo` |
| `internal/cli` (C13) | 1 | 1 | **Windows 既有基线** | `TestRenderCheckReplacesItsEarlierScreenshot`（symlink 权限） |
| `internal/session_manager` (C14) | 1 | 7 | **Windows 既有基线** | 见 §6.3 C14 行 7 条（两次复跑逐条一致，非 flaky） |
| `internal/integration` (C15) | 1 | 1 | **Windows 既有基线** | `TestDelegateEndpointDoesNotDependOnCodexDeviceReconciliation` |
| `internal/ports` + 6 个 service 包 (C6) | 0 | 0 | **零失败** | — |
| `internal/storage/sqlite` (C8) | 0 | 0 | **零失败（前一轮的缺陷已修复）** | `TestNoSchedulerReadsWorkflowTaskRequests` → 脚注 (a) |
| `internal/storage/sqlite/store` (C9) | 0 | 0 | **零失败** | — |
| `internal/daemon -run TestM2` (C12) | 0 | 0 | **零失败** | — |
| `internal/integration -run TestM2` (C16) | 0 | 0 | **零失败** | 24/24 PASS |

**归类结论**

- Windows 既有基线失败合计 **4 + 2 + 1 + 7 + 1 = 15** 条，均为本仓库既存问题，与本轮变更无关。
- **本轮自身缺陷 2 条，均已修复并复测 exit 0**：
  - `TestNoSchedulerReadsWorkflowTaskRequests`（原属 `internal/storage/sqlite`）：该包现已 **exit 0**（133.2s）；同时新增 `TestWorkflowTaskRequestReadMatcher`（11 例，7 必须命中 / 4 必须不命中）锁住收窄后的匹配器。见脚注 (a)。
  - `TestTaskRequestResponseCarriesNoRawPayload`（原属 `internal/httpd/controllers`）：该包现只剩 **4 条** 已记录基线，此测试不再出现，断言未改动。见脚注 (b)。
- **本轮净新增回归 = 0。**
- 定向集（C6/C7/C8/C9/C12/C16）与全部 4 组 race 集（C17/C18/C19/C20）均为 exit 0；生成物（C21/C22）与前端类型（C23）无漂移。

**脚注**

- **(a) `TestNoSchedulerReadsWorkflowTaskRequests` 的收窄与自证（已修复并复测 exit 0）**：前一轮失败是因为守卫按裸标识符匹配，命中了本轮新增 MCP 表面里只有工具名、没有 SQL 的三个文件（其中唯一的匹配是 `workflow_task_requests_list`）。现在的匹配器只认「SQL 动词 + 表名」：

  `(?is)\b(?:from|into|update|join|delete\s+from)\s+workflow_task_requests\b`

  即表名必须以词边界结束（工具名继续以 `_list` 延伸，故不匹配），`\s+` 跨越换行（跨行语句仍被捕获）。`TestWorkflowTaskRequestReadMatcher`（`migrate_workflow_collaboration_test.go:308`）用 11 个用例双向钉住：`select`/`insert`/`update`/`delete`/`join`/跨行/lowercase **必须命中**，`mcp tool name`/`mcp tool call`/纯注释/无关文件 **必须不命中**。守卫本身的允许清单（`:344-354`）与扫描范围未放宽，咬合力保留。
- **(b) `TestTaskRequestResponseCarriesNoRawPayload` 的凭据修正（已修复并复测，断言未改动）**：失败原因是该测试的 GET 循环未带 operator 凭据，而本轮起读接口要求可信 operator。修复是让 GET 走包内既有的 `doJSON` 助手（从 loopback 对端发送 operator credential，`workflows_m2_test.go:174`），而不是新增豁免。测试对泄漏的断言（`requestJson`/`request_json`/`requestPayload`/`payload":`、`/home/someone`、`../../etc/passwd`、`private/repo`）与对指纹/脱敏摘要的存在性断言全部保持原样。

---

## 7. Race 测试结果

| 命令 | 退出码 | 实测输出 | 覆盖 |
| --- | --- | --- | --- |
| `go test -race ./internal/service/{execution,intervention,capability,principal,collaboration} -count=1`（C17） | **0** | 5 包 `ok`，**无 race** | R-01 pinned 入口、R-04 token/lease/审计、R-05 resolver 并发路径、R-08 协作决策 |
| `go test -race ./internal/integration -run TestM2 -count=1`（C18） | **0** | **97.6s**，**无 race** | §6.2 全部 24 条 M2 E2E，**含 MCP 链与真实 SessionManager 链** |
| `go test -race ./internal/httpd/controllers -run TestM2 -count=1`（C19） | **0** | **4.8s**，**无 race** | R-04 审计写入与拒绝路径、R-05 gate、R-08 materialization |
| `go test -race ./internal/session_manager -run 'Workflow\|MultiRepo\|MCP' -count=1`（C20） | **0** | **73.4s**，**无 race** | **Change A 的 resume 恢复路径**（`workflowSpawnContext` + 4 条新测试）+ 多仓库 spawn/rollback 并发 |

**结论**：最终一轮 **4 组 race 全部 exit 0，未报告任何 data race**，也未出现 TSan 影子内存分配失败（前一轮记录过的 `ThreadSanitizer failed to allocate` 噪声本轮未复现）。

**与前一轮相比的变化**：race 覆盖面**扩大**了——前一轮未跑 `session_manager` 的 race，本轮以 `-run 'Workflow|MultiRepo|MCP'` 定向补上 C20，专门覆盖 Change A 新增的「从durable state 恢复 attempt 身份」路径，耗时 73.4s 且无 race。

**仍未做的 race 覆盖**（诚实声明）：`./internal/session_manager` 与 `./internal/storage/sqlite` 的**整包** race 未运行——这两包整包含 Windows 既有基线失败（见 §6.4），整包 race 会在基线失败上消耗大量时间且无法证明 M2 属性。C20 的定向选择覆盖了 M2 相关的并发路径（Workflow/MultiRepo/MCP），未覆盖的部分属基线失败所在区域。

---

## 8. 与 `m2-reacceptance.md` R-01～R-08 逐项对照

| 编号 | 复验要求（摘要） | 本轮修复落点 | 证据 | 是否闭环 | 残留 |
| --- | --- | --- | --- | --- | --- |
| **R-01** | Send/Interrupt generation-pinned；receipt 精确匹配；observation 空 generation 必须拒绝 | `ports/execution.go:402-422`、`:288-299`、`:374-385`、`:451-455`；`chat/service.go:1455`、`:1800`；`chat/controller.go:1510`；`execution/local_gateway.go:530`、`:700` | `send_pinned_test.go`(8 条)、`local_gateway_test.go:1900/2010/2079/2153`、`coordinator_test.go:1255`、`m2_e2e_test.go:1622` | **代码层闭环** | 端到端断言“provider 从未被调用”仅在 fake chat 层证明；真实 provider 行为 `NOT_TESTED` |
| **R-02** | RepoRefs 真正进入 Session Manager，每 repo 建 worktree、核验 HEAD、可恢复 | `ports/session.go:100-117`；`execution/local_gateway.go:386`；`session_manager/multi_repo_workspace.go:67`、`:363`；`session_manager/manager.go:1343`、`:4612` | `multi_repo_workspace_test.go`(5 条)、`m2_session_manager_e2e_test.go:699`、`coordinator_test.go:2055/2080/2114/2144` | **代码层闭环** | 单仓库场景下 repo binding 仍保持 `pending`（见 §9） |
| **R-03** | 真实 Harness 可用的 workflow MCP 工具 + 受限 capability/lease 上下文；从 MCP 入口做 E2E | `cli/mcp_workflow.go`(5 工具)、`cli/mcp.go:133/147`、`session_manager/chat_spawn_workflow.go`、`chat_spawn.go:369`、**`workflow_context.go:60/110`（resume 恢复）**、`queries/workflow_binding.sql:21` | `mcp_workflow_test.go`(8 条)、`m2_mcp_e2e_test.go:235`、**`workflow_context_test.go:105/146/176/195`** | **代码层闭环** | 真实 provider 是否调用这些工具 `NOT_TESTED`；**resume 身份已再生（Change A）**，但 lease 不续租，超出 15 分钟窗口的 resume 无 lease → 工具 403 fail-closed（见 §9 第 2/7 项） |
| **R-04** | Resolve/Decide 的 Human/Operator 边界明确并测试；loopback 不得伪称 Human | `service/principal/operator_token.go` 全文、`principal.go:143-162`、`verifier.go:152`、`daemon/workflow_auth_wiring.go:27`、`controllers/workflows.go:1488/1718/912/1289/1621/1671` | `principal_test.go`(11 条)、`workflows_m2_identity_test.go`(15 条)、`workflows_m2_bind_test.go:689`、`workflow_audit_store_test.go`(4 条) | **代码层闭环** | 同 OS 用户可读 token 文件；Windows 不强制 0600 |
| **R-05** | Probe 必须用真观察 generation 与 Session Controller 能力；修复误标 confirmed_runtime 与旧能力残留 | `capability/probe.go:82-87`、`:222-237`、`:264-273`；`capability/resolver.go:280-305`；`controllers/workflows.go:1134-1175`、`:1392` | `probe_test.go:378/430/486/541/574/601`、`resolver_test.go:242/266/290/307`、`workflows_m2_identity_test.go:597/623/653`、`workflows_m2_authz_test.go:293/349` | **代码层闭环** | session 作用域证据的等级固定为 `CapabilitySourceProviderHandshake`（`probe.go:236`，取值见 `domain/harness_capability.go:49`）；`confirmed_runtime`（同文件 `:52`）只由 `Recorder.RecordConfirmed`（`service/capability/recorder.go:136`）写入，本 Probe 的两处 `Source` 赋值（`probe.go:135`、`:236`）都只产出 `provider_handshake` |
| **R-06** | Resume 的 SpawnKey 严格匹配 | `execution/local_gateway.go:802` | `local_gateway_test.go:2169`(表驱动三例)、`:1339`、`:1419` | **代码层闭环** | 无 |
| **R-07** | 真实 Session Manager + Fake Runtime/Chat Launcher 验证 Spawn/Worktree/Resume | `integration/m2_session_manager_e2e_test.go`（6 场景） | `:699`、`:777`、`:817`、`:904`、`:980`、`:1069` | **代码层闭环** | 仍为 fake/scripted 的三项：终端 runtime（只答 liveness，`m2_session_manager_e2e_test.go:74-90`）、agent 注册表（不探测已装 CLI，`:60-61`）、Chat driver（`adapters/chatdriver/scripted`，不启 provider，`:62-63`） |
| **R-08** | 明确“approved but not materialized”边界，或实现安全 materialization | `domain/workflow_collaboration.go:442-451`；`controllers/workflows.go:1794-1845` | `collaboration/coordinator_test.go:518`、`workflows_m2_identity_test.go:501` | **边界已明确（非实现 materialization）** | M2 不创建 Task；批准的 Task 永远停在 `pending` |

**口径说明**：“代码层闭环”指对应缺陷在源码与定向测试上有直接证据。**它不等于验收 PASS**：最终一轮实测为 C1～C23、M2 E2E 24/24、4 组 race exit 0、净新增回归 0，但**真实 provider 行为仍未验证**，且 §9 仍有 8 项开放（含本轮新增的「operator 路由无出厂客户端」）。

---

## 9. 尚未解决的问题

以下均为**实际未解决**或**明确不在 M2 范围**的事项，不是措辞问题。

**计数算式（可逐条核对）**：原 9 项 —— 第 7 项（resume 身份不重建，Change A 关闭）与第 8 项（生成物漂移，C21/C22 关闭）**已关闭** ⇒ 9 − 2 = 7；本轮**新增**第 10 项（operator 路由无出厂客户端）⇒ 7 + 1 = **8 项开放**。

开放的 8 项即：**1、2、3、4、5、6、9、10**。

1. **真实 Provider 行为 `NOT_TESTED`。** 本轮仍是 0 turn / 0 token。所有 chat 行为由 `backend/internal/adapters/chatdriver/scripted/` 与 fake 承担；Codex/Claude/OMP/DeepSeek 是否真的会调用这 5 个 MCP 工具、是否接受 generation pin、`lease` 过期后的行为，全部未验证。M0-05 预算未批准。

2. **Lease 15 分钟后过期，且没有续租路径。** `daemon/workflow_wiring.go:26` `workflowLeaseDuration = 15 * time.Minute`，经 `service/workflow/service.go:79` 注入，Claim 时 `leaseUntil := now.Add(s.leaseDuration)`（`:544`）写入 `LeaseUntil`（`:552`）；`service/principal/verifier.go:123-125` 过期即 `false`。新增的 MCP 工具在 lease 失效后，服务端会以 `ErrLeaseUnproven` 拒绝（`principal.go:310-321`），HTTP 层映射为 **403 `COLLAB_NOT_AUTHORIZED`**（`workflows.go:809-817`）。即 **fail-closed**。全仓无 `RenewLease`/`ExtendLease` 实现（grep 无结果），因此任何超过 15 分钟的任务在协作工具上必然失能。这是 M2 的已知能力上限，未修。

3. **Windows 下 token 文件权限不强制。** `os.OpenFile(..., 0o600)` 在 Windows 忽略 perm（`principal_test.go:628-631` 注释即此事实）。实际保护来自 NTFS ACL 继承；同一用户/同一 ACL 组的进程可读。未做 ACL 硬化。

4. **单仓库场景的 repo binding 保持 `pending`，不会变 `verified`。** `execution/coordinator.go:719-735` `repoGateDecision`：`len(worktrees) == 0` 时 `gate=false`，`recordRepoBindings`（`:817-822`）直接返回，行状态保留 `pending`（注释：“pending = expected but unobserved”）。因此 `TestSingleRepoBindSucceedsWithoutWorktreeRows`（`coordinator_test.go:2055`）能绑定成功，但该 repo 行的状态**永远不会**变成 `verified`。这是刻意的（避免伪造“missing”事实），但意味着“每个 RepoBinding 必须 verified 才 bound_ready”这条门槛在单仓库无 worktree 行时不适用。

5. **M2 不 materialize Task。** `controllers/workflows.go:1794-1799` 注释明确 approved 的 materialization 恒为 `pending`，`materialized` 分支“unreachable in M2”。批准只产生 202 + 审计行，不产生 `workflow_tasks` 行。这是 R-08 允许的边界选择，不是实现缺陷，但产品上“批准后自动排产”不存在。

6. **`RestoreAll` 仍无生产调用方。** `session_manager/manager.go:4418` 定义；除 `daemon/lifecycle_wiring.go:234` 的接口声明与测试外，全仓无调用点。daemon 启动实际走 `ReconcileStartupSafety`（`daemon/daemon.go:983`）→ `ReconcileBackground`（`:1317`）→ `reconcileLive` → `restoreSessionWorkspace`。因此 `RestoreAll` 路径（含 shutdown-marker 恢复）在本轮**未获得产品级运行证据**；多仓库 restore 的验证走的是 `restoreSessionWorkspace`（`multi_repo_workspace_test.go:375`）。

7. ~~重启/Resume 后 attempt 身份不重建。~~ **已关闭（Change A）。** `session_manager/workflow_context.go:60` 的 `workflowSpawnContext` 从 durable state 恢复身份：binding 名出 attempt，attempt 行是 lease 的唯一持有者（`chat_spawn.go:564-571` 把它交给 `aoMCPServers`，`:602-604` 回填 capability）。5 个协作工具在 resume 后重新可用，由 `workflow_context_test.go:105/146/176/195` 四条测试覆盖。
   **仍存在的限制**：lease **不是续租的**——`daemon/workflow_wiring.go:26` 的 15 分钟时长未变，全仓仍无 `RenewLease`/`ExtendLease`。因此**超出 lease 窗口后的 resume 会把 id 呈现出来、但不带 lease**（`workflow_context.go:98-100` 刻意让过期 lease 产出空 `LeaseToken`），随后每一个 workflow 工具都因 `ErrLeaseUnproven` 降级为 **403 fail-closed**（`workflows.go:809-817`）。这是刻意的安全选择（不给死凭据），不是可用的长期协作能力。**注意**：这条限制与第 2 项同根因（无续租路径），因此它不构成一个**额外的**开放式缺口——第 2 项仍计为 1 项。开放项清单见本节开头的算式（1、2、3、4、5、6、9、10）。

8. ~~`npm run sqlc` 未复核 / 生成物漂移。~~ **已关闭。** 最终一轮 C21 `npm run sqlc` **exit 0**（sqlc v1.31.1），gen 清单 **41→41**、前后 diff 完全相同；C22 `npm run api` **exit 0**，`openapi.yaml` 与 `schema.ts` 前后 sha256 完全一致（`1938f4a2…c2530e37` / `df7e6d66…70eb1b32`）。第二轮因 `proxy.golang.org` 超时 BLOCKED 的问题未复现。

9. **`workflow_audit_log` 无 HTTP 读接口。** `workflow_audit_store.go:90-91` 注释：“HTTP exposure is deliberately absent — the audit table is not a UI surface in M2”。运维只能直读 SQLite。

10. **运营方（operator）路由目前没有出厂客户端。** 本轮给 `bind` / `reconcile` / `resume` / `resolve` / `decide` 全部加上了 operator token 门禁，但**桌面应用里没有任何代码路径能带上这个 token**，因此这些路由现在只能靠手写 curl 打 loopback 使用。

   **证据（逐条实读源码）**

   - 渲染进程只有一个 daemon 客户端：`frontend/src/renderer/lib/api-client.ts:372` 的 `export const apiClient = createClient<paths>({`，其 `fetch` 是 `:296` 的 `async function runtimeFetch(input: Request)`。
   - 渲染进程是强隔离的：`frontend/src/main/window-composition.ts:38-40` 的 `contextIsolation: true` / `nodeIntegration: false` / `sandbox: true`。
   - `frontend/src/preload.ts` 不暴露通用 app-API fetch，也不暴露 fs：全文 894 行里只有两个**受限**的 fetch（`:389` `chatImage:fetch`、`:393` `cloudArtifact:fetch`），均经 `ipcRenderer.invoke` 走具名频道；`grep 'fs|readFile|writeFile|readdir|stat('` 无命中。
   - 主进程**没有** M2 路由的任何引用：`grep -cni workflow frontend/src/main.ts` = **0**，`frontend/src/main/*.ts` 里只有 `feature-builds.ts:35,39` 两处 CI 语境注释与 `agent-browser-runtime.test.ts` 的无关命中。因此主进程没有可挂 `X-AO-Operator-Token` 的 HTTP 收口点。
   - 但主进程**能**解析同一个数据目录：`frontend/src/main.ts:311` `const desktopDataDir = resolveDesktopDataDir(process.env, os.homedir(), desktopLaunchWorkingDirectory, app.isPackaged);`，并在 `:1549` 以 `AO_DATA_DIR: desktopDataDir` 传给 daemon。
   - CLI 侧也不具备：唯一触达 M2 路由的 CLI 是 agent 协作工具 `backend/internal/cli/mcp_workflow.go`（`:99` 拼接 `/api/v1/workflows/`），它用 **capability + attempt lease** 认证，且**本就不该持有** operator token（`grep` 确认 `backend/internal/cli/` 内无 `OperatorToken` 引用）。
   - 渲染进程侧同样没有调用：`grep '/workflows'` 只命中生成的 `frontend/src/api/schema.ts`（类型描述），`frontend/src/renderer` 内无 `listWorkflows`/`createWorkflow`/`bindWorkflowAttempt`/`resolveWorkflowAttempt`/`decideWorkflowTaskRequest` 的调用点。

   **配置事实**：`NewTokenTrust` / `NewOperatorTokenTrust` 只被 `daemon/workflow_auth_wiring.go:37`、`principal_test.go` 与 `workflows_m2_identity_test.go` 使用——除 daemon 自身与测试外，没有任何生产代码读取 `operator.token`。

   **后果**：`bind` / `reconcile` / `resume` / `resolve` / `decide` 在已发布的桌面应用里**不可达**；这类操作今天必须由人手写 curl。这是**刻意的 fail-closed**：被提示注入或攻陷的渲染进程无法自行批准 task request 或 resolve attempt。代价是 operator UI 路径缺失。

   **正确修法（本轮未实施，超出范围）**：在主进程加一层 IPC 代理——按 mtime 惰性读取 `<dataDir>/operator.token`（daemon 重启后重读），由主进程发起调用，把 envelope 原样返回给 preload；token **永不**进入渲染进程、preload、IPC payload 或日志。**不得**给渲染进程的 `apiClient` 提供 token 透传通道，否则等于放弃这道边界。

---

## 10. 结论

**M2 已完成第三轮修复，具备重新申请独立验收条件。**

R-01～R-08 在源码层均有明确修改位置与定向测试。**最终一轮（2026-11-04）实测**：C1～C23 全部串行执行完成；`go build`/`go vet` exit 0；**M2 E2E 24/24 PASS（FAIL 0 / SKIP 0）**，含 MCP 链与真实 SessionManager 链；**4 组 race 全部 exit 0、无 data race**（含 Change A 的 resume 恢复路径，C20 73.4s）；`npm run sqlc`/`api` 生成物无漂移，`typecheck` exit 0；Windows 基线失败 15 条逐条归类，前一轮的 2 条本轮自身缺陷均已修复并复测 exit 0，**净新增回归 0**（见 §6/§6.4/§7）。

本轮另有两处能力性变更：**Change A** 使 resume/重启后的 workflow 会话从 durable state 再生 5 个协作工具（§9 第 7 项关闭）；**Change B** 收窄审批边界守卫并新增 `TestWorkflowTaskRequestReadMatcher` 自证。

本报告是实施方交付说明，**不构成 M2 最终 PASS，不得进入 M3**。真实 Provider 验证仍为 `NOT_TESTED`；§9 现有 **8 项**开放（原 9 项中第 7、8 项已关闭，本轮新增第 10 项「operator 路由无出厂客户端」），必须在进入 M3 前由验收方逐条确认是“接受为边界”还是“仍需修复”。

---

## 附：本轮新增/修改文件清单（相对第二轮）

**新增（未跟踪）**

- `backend/internal/session_manager/multi_repo_workspace.go`、`multi_repo_workspace_test.go`
- `backend/internal/session_manager/chat_spawn_workflow.go`
- `backend/internal/cli/mcp_workflow.go`、`mcp_workflow_test.go`
- `backend/internal/service/principal/operator_token.go`
- `backend/internal/storage/sqlite/migrations/20261104094706356_workflow_audit_log.sql`
- `backend/internal/storage/sqlite/queries/workflow_audit.sql`
- `backend/internal/storage/sqlite/store/workflow_audit_store.go`、`workflow_audit_store_test.go`
- `backend/internal/storage/sqlite/migrate_workflow_audit_log_test.go`
- `backend/internal/daemon/workflow_auth_wiring.go`
- `backend/internal/integration/m2_mcp_e2e_test.go`、`m2_session_manager_e2e_test.go`
- `backend/internal/service/chat/send_pinned_test.go`
- `backend/internal/httpd/controllers/workflows_m2_identity_test.go`
- **「Change A」** `backend/internal/session_manager/workflow_context.go`、`workflow_context_test.go`
- **「Change A」** `backend/internal/storage/sqlite/queries/workflow_binding.sql`（`GetWorkflowAttemptBindingBySession`）、`gen/workflow_binding.sql.go`（`npm run sqlc` 生成）、`store/workflow_binding_store.go`/`workflow_binding_store_test.go`

**修改**

- `backend/internal/ports/execution.go`（`WorkflowSpawnContext`、`SpawnRepos`、fence 校验、receipt 匹配）
- `backend/internal/ports/session.go`（`SpawnRepo`、`SpawnConfig.Repos`、`WorkflowSpawn`）
- `backend/internal/service/chat/service.go`、`controller.go`、`steer.go`（pinned 入口 + `ErrStaleControllerGeneration`）
- `backend/internal/service/execution/local_gateway.go`（Repos/Workflow 转发、fenced Send/Interrupt、严格 SpawnKey）
- `backend/internal/service/execution/coordinator.go`（lease→WorkflowSpawnContext、repo binding gate）
- `backend/internal/service/capability/probe.go`、`resolver.go`（generation 观察语义）
- `backend/internal/service/principal/principal.go`、`verifier.go`（TokenOperatorTrust、fingerprint、fail-closed）
- `backend/internal/httpd/controllers/workflows.go`（所有 M2 路由的 caller 门禁、审计、materialization 三态）
- `backend/internal/httpd/controllers/workflows_m2_test.go`（**「Change B」**`TestTaskRequestResponseCarriesNoRawPayload` 的 GET 改走 `doJSON` 带 operator 凭据；断言未改）
- `backend/internal/session_manager/manager.go`、`chat_spawn.go`、`chat_spawn_async.go`（多仓库分派、MCP env；**`chat_spawn.go:564-571`/`:602-604` 为「Change A」的 resume 接线**）
- `backend/internal/cli/mcp.go`（workflow 工具注册与分派）
- `backend/internal/daemon/daemon.go`、`backend/internal/httpd/api.go`（`WorkflowPrincipals` / `WorkflowAudit` 接线）
- `backend/internal/storage/sqlite/migrate_burned_versions_test.go`（登记 `20261104094706356`）
- `backend/internal/storage/sqlite/migrate_workflow_collaboration_test.go`（**「Change B」** 守卫收窄为 `workflowTaskRequestRead`/`workflowTaskRequestReadPattern`，并新增 `TestWorkflowTaskRequestReadMatcher`）
- `backend/internal/storage/sqlite/store/workflow_binding_store.go`（**「Change A」** 新增 `GetWorkflowAttemptBindingBySession` 方法）
- `backend/sqlc.yaml` 及 `gen/*`（`workflow_audit`、`workflow_binding` 生成物）

**未修改**：`m2-acceptance.md`、`m2-reacceptance.md`、`m2-report.md`、`m2-remediation-report.md`。
