# M2 阶段报告 —— 编码执行与分级协作（离线开发门禁）

> **判定：M2 离线开发门禁通过（M2 offline development gate PASSED）。**
> 这不是"M2 全部通过"，不是"四 Agent 全部支持"，也不是"M0 已通过"。
> 真实 Provider 路径 0 行执行、0 次会话、0 token；全部证据来自 Fake/离线栈。

---

## 1. 元数据

| 项 | 值 |
| --- | --- |
| 分支 | `feat/multi-agent-desktop` |
| HEAD（基线 commit） | `a6c284a4d060431371306072e6c80abfedf22328` —— `feat: implement M1 workflow core and acceptance` |
| 工作树 | 基线之上有 M2 未提交改动（见 §2）；**本轮未做任何 git commit / push / reset / stash** |
| 日期 | 2026-11-04（Asia/Shanghai，UTC+08） |
| 平台 | Windows 10.0.26200（win32 x64），`go1.27.1 windows/amd64` |
| 基线文件 | `docs/customization/m2-baseline-windows.md`（267 条既有失败 / 56 包） |
| 计划文件 | `docs/customization/m2-plan.md` |
| 设计文件 | `docs/customization/m2-design.md`（本轮未改） |
| 计划审计 | `docs/customization/m2-plan-audit.md`（本轮未改） |
| Provider 预算 | **M0-05 未获批** → 一切真实 Provider 行为见 §7 `BLOCKED / NOT_TESTED` |

---

## 2. 变更清单（按 M2 任务分组）

### 2.1 迁移（migration id）

M2 新增 **4** 个迁移（均为未提交新文件），另有 **3** 个 M1 已提交迁移**未改动**：

| migration id | 文件 | 归属 | 状态 |
| --- | --- | --- | --- |
| `20261103143320688` | `20261103143320688_workflow_attempt_bindings.sql` | M2-03 | 新增（未提交） |
| `20261103160303854` | `20261103160303854_workflow_interventions.sql` | M2-05 | 新增（未提交） |
| `20261103163208174` | `20261103163208174_workflow_collaboration.sql` | M2-06 | 新增（未提交） |
| `20261103170009848` | `20261103170009848_workflow_message_loss_and_ref.sql` | M2-07（P1-B/P1-C 修复） | 新增（未提交） |
| `20261103044048260` | `..._workflow_core.sql` | M1 | **已提交，未改**（`git hash-object` 与 HEAD 一致） |
| `20261103050931629` | `..._workflow_idempotency.sql` | M1 | **已提交，未改** |
| `20261103073516664` | `..._workflow_cdc.sql` | M1 | **已提交，未改**（本轮曾误改，已按 HEAD 字节还原，见 §5.4） |

### 2.2 新增文件

| 任务 | 文件 |
| --- | --- |
| M2-02 | `internal/domain/harness_capability.go`、`internal/ports/capability.go`、`internal/service/capability/`（`resolver.go`、`fake.go`、`static.go` + 测试） |
| M2-03 | `internal/storage/sqlite/queries/workflow_binding.sql`、`internal/storage/sqlite/store/workflow_binding_store.go` + 测试、`migrations/20261103143320688_*`、`migrate_workflow_attempt_bindings_test.go` |
| M2-04 | `internal/domain/workflow_binding.go`、`internal/ports/execution.go`、`internal/service/execution/`（`coordinator.go`、`fake_gateway.go` + 测试）、`internal/daemon/execution_wiring.go`、`internal/daemon/m2_wiring_test.go` |
| M2-05 | `internal/domain/workflow_intervention.go`、`internal/service/intervention/`（`coordinator.go` + 测试）、`queries/workflow_intervention.sql`、`store/workflow_intervention_store.go` + 测试、`migrations/20261103160303854_*` |
| M2-06 | `internal/domain/workflow_collaboration.go`、`internal/service/collaboration/`（`coordinator.go` + 测试）、`queries/workflow_collaboration.sql`、`store/workflow_collaboration_store.go` + 测试、`migrations/20261103163208174_*` |
| M2-07 | `migrations/20261103170009848_*`、`httpd/controllers/workflows_m2_test.go`、`httpd/controllers/m2_security_test.go` |
| M2-08 | `store/m2_e2e_test.go`、`store/m2_anomaly_test.go`、`store/m2_offline_test.go`、`sqlite/migrate_m2_rollback_test.go`、本文件 |

### 2.3 修改文件

| 文件 | 任务 | 改动 |
| --- | --- | --- |
| `internal/httpd/controllers/dto.go` | M2-07 | 追加 M2 DTO 段（capability / binding / intervention / message / task request） |
| `internal/httpd/controllers/workflows.go` | M2-07 | 追加 `RegisterM2` 与 10 条 M2 handler + 投影/错误映射 |
| `internal/httpd/api.go` | M2-07 | 5 个 M2 依赖位 + `RegisterM2` 挂载 |
| `internal/httpd/apispec/specgen/build.go` | M2-07 | 24 个 schema 名 + 10 个 operation |
| `internal/daemon/daemon.go` | M2-04 | 接线 `newM2Services` |
| `internal/httpd/controllers/workflows_test.go` | M2-07 | fake 增加 `Resolve` |
| `internal/service/session/service.go` | M2-04 | 导出按 `ClientRequestID` 的只读查询（§6 偏差 3 的落点） |
| `sqlc.yaml` | M2-03/06 | 追加 queries 目录 |
| `frontend/src/api/schema.ts` | M2-07 | 重新生成（字节稳定，见 §4.2） |
| `internal/storage/sqlite/gen/*` | M2-03/06 | sqlc 重新生成（仅新增 5 个 struct，0 删除） |
| `internal/storage/sqlite/migrate_burned_versions_test.go` | M2-03 | 登记新版本号 |
| `internal/storage/sqlite/migrate_workflow_cdc_test.go` | M2-03 | 适配新事件类型（**已知偏差，见 §8.2**） |
| `internal/storage/sqlite/telemetry_vacuum_test.go` | M2-03 | 迁移数量断言跟进 |

---

## 3. 测试证据表

| # | 命令 | 退出码 | 结果摘要 |
| --- | --- | --- | --- |
| 1 | `go build ./...`（backend） | **0** | 无输出，全部包可编译 |
| 2 | `go vet <M2 全部触碰包>` | **0** | 无诊断 |
| 3 | `go test <M2 包集> -count=1` | 见 §3.1 | domain/ports/capability/execution/intervention/collaboration/sqlite/store/httpd/controllers/cdc 全绿 |
| 4 | `go test -race ./internal/service/{execution,intervention,collaboration,capability}` | 见 §3.2 | 见下 |
| 5 | `go test ./... -count=1`（全仓） | **1** | 与 267 基线做集合差，见 §3.3 |
| 6 | `npm run sqlc` ×2 | **0** / **0** | 两次 `gen/*.go` 合并 sha256 完全一致 |
| 7 | `npm run api` ×2 | **0** / **0** | 两次 `schema.ts` sha256 完全一致 |
| 8 | `npm run frontend:typecheck` | **0** | `tsgo --noEmit` 无诊断 |
| 9 | `git diff --quiet -- backend/internal/storage/sqlite/gen` | **1**（预期） | 差异仅为 M2 新表带来的 89 行新增，0 删除；见 §4.1 |

### 3.1 M2 目标包测试

`go test ./internal/domain ./internal/ports ./internal/service/capability ./internal/service/execution ./internal/service/intervention ./internal/service/collaboration ./internal/storage/sqlite ./internal/storage/sqlite/store ./internal/httpd ./internal/httpd/apispec/... ./internal/httpd/controllers ./internal/cdc -count=1`

退出码 **0**。关键新增测试（本轮 M2-08 新增，全部 PASS）：

| 测试 | 位置 | 断言 |
| --- | --- | --- |
| `TestM2OfflineEndToEndTwoHarnesses` | `store/m2_e2e_test.go` | 两 Harness 并发全栈；session/generation/消息互不串；能力按 session 证据分级 |
| `TestM2AnomalyInjectionMatrix`（10 子测试） | `store/m2_anomaly_test.go` | 见 §3.4 |
| `TestM2ZeroProviderGuarantees` | `store/m2_offline_test.go` | 运行时零 Provider：5 个用户数据根调用后仍为空 |
| `TestM2ResponsesNeverLeakTheLease` | `httpd/controllers/m2_security_test.go` | 7 个 M2 响应形状无 `leaseToken`、无未解释 64-hex |
| `TestM2TaskRequestHidesItsPayload` | 同上 | P1-A 回归钉：无 `requestJson`、无路径形态子串 |
| `TestM2CDCPayloadsCarryNoMessageBody` | 同上 | 4 个 M2 SSE payload 无 body/text/prompt/requestJson |
| `TestM2WiringLogRedaction` | `daemon/m2_wiring_test.go` | 唯一 M2 启动日志不含任何标识符/64-hex |
| `TestM2WiringNeverTouchesProviderState` | 同上 | 无 probe 注册，能力只能是 static-only |
| `TestM2LocalRollbacksEmitZeroEvents` | `sqlite/migrate_m2_rollback_test.go` | M2 自有迁移回滚：自有事件清零、pre-M2 历史保留、无表/触发器残留、`integrity_check = ok` |
| `TestM1DownPathLeavesChangeLogInvalid` | 同上 | **记录既存缺陷**（§7.9），非通过性保证 |

### 3.1.1 零 Provider 安全扫描（命令 + 退出码）

"不启动 Provider"必须可证，不能只靠声明。四道独立的证据：

| # | 检查 | 命令 | 退出码 | 判读 |
| --- | --- | --- | --- | --- |
| S1 | 新测试套件不起进程、不联网 | `grep -nE 'os\.StartProcess\|exec\.Command\|exec\.CommandContext\|net\.Dial\|http\.Get\|http\.Client\|http\.NewRequest' <6 个新 M2 测试文件>` | **1** | 1 = 无命中，即没有任何 exec/网络调用点 |
| S2 | 新测试套件不写用户目录 | `grep -nE 'os\.WriteFile\|os\.MkdirAll\|os\.Create' <同上>` | **1** | 1 = 无命中 |
| S3 | M2 生产代码无 `%+v`/`%#v` | `grep -rnE '%\+v\|%#v' execution/coordinator.go intervention/coordinator.go collaboration/coordinator.go capability/resolver.go daemon/execution_wiring.go controllers/workflows.go` | **1** | 1 = 无命中，不存在"整个结构体打进日志"的通路 |
| S4 | 运行时**行为**证明 | `go test ./internal/storage/sqlite/store -run TestM2ZeroProviderGuarantees -count=1` | **0** | `HOME`/`USERPROFILE`/`XDG_CONFIG_HOME`/`XDG_DATA_HOME`/`XDG_CACHE_HOME` 全部重定向到**全新空目录**，跑完完整 bind→steer→stop 流程后 5 个根**仍为空**，且 `.ao`/`.codex`/`.claude` 均不存在 |

S1–S3 中 `.ao`/`.codex`/`.claude` 字样的唯一出现位置是 `m2_offline_test.go:119-124`，而那是 `os.Stat(path)` + `os.IsNotExist(err)` 的**否定断言**（断言它们不存在），不是读取。这一点在命令输出中可直接核对。

> **S4 的诚实边界**：它证明的是"M2 栈没有 Provider 依赖"，**不是**"真实 Provider 会这样表现"。后者离线无法证明。

### 3.2 竞态测试

`go test -race ./internal/service/execution ./internal/service/intervention ./internal/service/collaboration ./internal/service/capability -count=1`

退出码 **0**；无 data race 报告。

### 3.3 全仓集合差（对照 267 条基线）

`cd backend && go test ./... -count=1` → 退出码 **1**（Windows 红基线，预期；耗时 699.77s）。

集合按**顶层测试名**归一（基线 §5.3 只记顶层名，故子测试折叠到父）：

| 方向 | 条数 |
| --- | --- |
| 基线失败（§5.3） | **267** |
| 本轮失败（顶层归一后） | **241** |
| **本轮新增（`current − baseline`）** | **5** |
| **基线独有 / 本轮已绿（`baseline − current`）** | **31** |

**5 条"新增"逐条归因（全部为基线文件已知的两处噪声，非 M2 回归）：**

| # | 包 | 测试 | 归因 | 隔离复跑证据 |
| --- | --- | --- | --- | --- |
| 1 | `adapters/agent/gemini` | `TestVersionProbeAcceptsSupportedCLIAndRejectsOldCLI` | 基线 §6.2 已列为 flaky（"HEAD 上单独复跑 ok"） | 见下 |
| 2 | `adapters/agent/opencode` | `TestResolveBinaryForMajorAcceptsSlowShim` | 同上 | 见下 |
| 3 | `adapters/reviewer/opencode` | `TestReviewCommandBuildsBothOpenCodeConfigSources` | 同上 | 见下 |
| 4 | `daemon` | `TestWiring_NewMultiTracker_DoesNotProbeGHCLIAtBoot` | 同上 | 连跑 3 次 = `ok` / **FAIL** / `ok` → flaky 确认 |
| 5 | `httpd` | `TestServerReportsIPv6LoopbackTaken` | **基线未记录**，本轮新观察到的 flaky | 连跑 6 次出现 3 次 FAIL；失败信息 `server_test.go:459: Run: graceful shutdown exceeded 5s: context deadline exceeded` |

**第 5 条必须单独交代**（它是唯一不在基线 flaky 清单里的）：

- 它与 M2 **无因果关系**：该测试用 `NewWithDeps(cfg, discardLogger(), nil, APIDeps{})` —— **空 `APIDeps`**，M2 的 5 个依赖位全部为空，路由不会挂载任何 M2 handler；`internal/httpd/server.go` 与 `server_test.go` 对 M2 **零改动**（`git status --porcelain` 两者均空）。
- 失败模式是**优雅关闭超过 5s**（`ShutdownTimeout: 5 * time.Second`），属于"端口争用 + Windows 关闭超时"类，与代码路径无关。
- **诚实结论**：这 5 条全部是 flaky（4 条已在基线登记，1 条本轮新观察到），因此 **M2 新增失败 = 0**。但第 5 条是"基线未登记的新 flaky"，**不是**"基线已登记的失败"，我不把它说成"已在基线内"。

**31 条"基线独有/本轮已绿"的构成（与基线 §6.1 完全对应）：**

| 包 | 条数 | 解释 |
| --- | --- | --- |
| `adapters/chatdriver/codexappserver` | 30 | 该包在本轮 `./...` 负载下**更早**触发 panic，于是报出的失败数少于基线的 48（真实集合是 58）。这是基线 §6.1 已声明的**截断假差**，不是"修好了 30 条" |
| `adapters/telemetry` | 1 | `TestDestinationSinkConcurrentEmitAndClose` 计时 flaky，本轮绿 |

**两处已知噪声按基线要求单独复跑核对：**

| 噪声源 | 基线说明 | 本轮单独复跑 |
| --- | --- | --- |
| `codexappserver` | panic 截断使 `./...` 内计数在 48–58 浮动；单独运行稳定 58 条 + 1 次 panic | 单独复跑得 **58** 条失败 + 1 次 panic，与基线一致 |
| `telemetry` | 计时 flaky，单独复跑全绿 | 单独复跑 **exit 0** |

> **净结论：M2 相对 267 基线的新增失败 = 0。** 5 条"新增"全部隔离复跑证明为 flaky（4 条基线已登记 + 1 条本轮新观察到）；31 条"消失"全部是 panic 截断与计时 flaky 造成的假差，不是修复。
>
> **判读边界**：本结论的强度依赖"5 条均为 flaky"这一隔离复跑事实，而不依赖总数相减。若把第 5 条（`TestServerReportsIPv6LoopbackTaken`）按最保守口径计为失败，则"新增"为 1 条 —— 它落在 `internal/httpd` 包但在 `APIDeps{}` 空依赖下运行、且相关文件 M2 零改动，仍不构成 M2 回归。

### 3.4 异常注入矩阵（10 行）

`go test ./internal/storage/sqlite/store -run TestM2AnomalyInjectionMatrix -count=1` → **PASS**，10/10 子测试通过。

| # | 行 | 子测试 | 断言（状态 / 调用数 / 未破坏） |
| --- | --- | --- | --- |
| 1 | spawn 响应丢失 → 有 session | `spawn_response_lost_then_reconcile_adopts` | `spawn_unknown` + frozen；spawn 调用 **1**；reconcile 采纳后 `bound_ready` 且 spawn 仍 **1** |
| 2 | spawn 响应丢失 → 找不到 | `spawn_response_lost_then_reconcile_finds_nothing` | `reconcile_required` + frozen；spawn 仍 **1**；再 bind 不 spawn；repo 绑定在位 |
| 3 | claim CAS 与 gateway 之间崩溃 | `crash_between_claim_and_gateway_call` | 从 `spawn_requested` 前进到 `reconcile_required`；spawn 调用 **0** |
| 4 | 活绑定下 session 被删 | `session_deleted_under_live_binding` | `reconcile_required` + `session_deleted`；session/generation 清空；repo 绑定保留；无替代 spawn |
| 5 | 未决 stop 下 session 被删 | `session_deleted_under_open_stop` | `stop_unknown` 保持 + `reason=session_deleted`；删除成功且**无 raw constraint 错误**；无新增 interrupt |
| 6 | 过期 generation 回执 | `stale_generation_receipt_rejected` | `ErrInterventionStaleGeneration`，行未被写（revision 不变）；同 generation 回执可结算 |
| 7 | Stop 重放 | `duplicate_stop_replay_interrupts_once` | 3 次重放 `Performed=false`、同一 intervention；interrupt 恒为 **1** |
| 8 | Steer 被拒 | `refused_steer_sends_and_interrupts_nothing` | `queueAvailable=true` + reasonCode；send **0**、interrupt **0**；未持久化行 |
| 9 | 32 条消息上限 | `thirty_two_message_cap_refuses_the_thirty_third` | 第 33 条 = `ErrWorkflowMessageQueueFull`（无 "sql" 字样）；前 32 条完整、按序；第 33 条未落库 |
| 10 | approve 无物化 API | `approval_without_materialization_stays_pending_work` | `approved` 但 `Materialized=false`、`TASK_REQUEST_NOT_MATERIALIZED`；task 数不变、workflow revision 不变 |

### 3.5 集合差判读方法（为何"新增 = 0"可证）

判读不是"两次总数相减" —— 298−267 这种减法会把 panic 截断和 flaky 都算成回归。实际方法：

1. **归一**：基线 §5.3 只记**顶层**测试名，故本轮也把子测试折叠到父（`TestX/sub` → `TestX`）。不归一会产生大量假"新增"。
2. **取集合**：基线 267 条 / 56 包（脚本抽取复核，与文件声明一致）；本轮 241 条 / 58 包。
3. **双向差集**：`新增 = 本轮 − 基线` = **5**；`消失 = 基线 − 本轮` = **31**。
4. **逐条归因 5 条新增**：4 条在基线 §6.2 已登记为 flaky；第 5 条（`TestServerReportsIPv6LoopbackTaken`）本轮首次观察到，隔离复跑 6 次中 3 次失败，失败于"优雅关闭超 5s"，且其运行使用**空 `APIDeps{}`**、相关文件 M2 零改动。
5. **逐条归因 31 条消失**：30 条来自 `codexappserver` 的 panic 截断时机差异（该包真实集合为单独复跑的 58 条）；1 条来自 `telemetry` 计时 flaky。**这不是"M2 修好了 31 条"。**
6. **噪声源单独复跑核对**（基线 §7.5 要求）：`codexappserver` 单独运行 = **58 条 + 1 次 panic**（与基线一致）；`telemetry` 单独运行 = **exit 0**。

> **诚实边界一**：第 5 条新增**不在**基线的 flaky 清单里。我没有把它当作"基线已登记"来消化，而是单列并给出隔离复跑证据。它的性质是 flaky（3/6 次失败），不是 M2 引入的确定性失败。
>
> **诚实边界二**：`codexappserver` 的失败**条数**会随 panic 截断时刻在 48↔58 间漂移。基线文件已预先声明，故该包以单独复跑的 58 条为准，**不参与**"新增失败"判定。除已说明的三处（codexappserver 截断、telemetry 计时、httpd 关闭超时）外，本轮**没有任何其它计数漂移**。

## 4. 生成物零漂移

### 4.1 sqlc

```
npm run sqlc   → exit 0
npm run sqlc   → exit 0
sha256(所有 gen/*.go 合并) 两次 = c26de85ec780250419dfff97403d884b3d6fb5a8ab6bbf267b0265051b996522
```

`git diff -- backend/internal/storage/sqlite/gen` = 仅 `models.go` 新增 **89 行**（5 个 struct：`WorkflowAttemptBinding`、`WorkflowAttemptRepoBinding`、`WorkflowIntervention`、`WorkflowMessage`、`WorkflowTaskRequest`），**删除行 0**。`gen/` 内**无手改**：两次生成字节完全一致即证明。

### 4.2 OpenAPI / TypeScript

```
npm run api (openapi-typescript 7.4.4) → exit 0
npm run api                             → exit 0
sha256(frontend/src/api/schema.ts) 两次 = fadd3021b8d10a6b4669c43e085e855f9fbd1bab88618366fa16a9c67c7118a7
sha256(openapi.yaml)                    = 7cc6331c7e325f120fad1fa91f24ac25a034398e4abccede1bbef999659c24e9（生成不改它）
```

`openapi.yaml` 与 `schema.ts` 均**未被手改**：`openapi.yaml` 由 `specgen` 从 Go 源码生成，`schema.ts` 由 `openapi-typescript` 从 `openapi.yaml` 生成；两次生成字节一致即证明无手改。按 §6 偏差 6，生成一律走**根** `npm run api`（pin 7.4.4），不走 `frontend` 的 `api:ts`（`^7.13.0`）。

### 4.3 前端类型检查

`npm run frontend:typecheck` → **exit 0**（`tsgo --noEmit`，无诊断）。

---

## 5. 能力证据矩阵

证据分级：`static`（静态画像，**只能展示**）→ `local_probe`（本机探测，**只能展示**）→ `provider_handshake`（握手，**可授权**）→ `confirmed_runtime`（运行期确认，**最强**）。只有后两级能过 `Gate`。

**真实 Provider 一律 `NOT_TESTED`。** 本矩阵的"Fake 结论"列是离线栈对 **Fake 证据**的表现，**不能**外推为真实 Provider 行为。

| Harness | steer | resume | approval | usage | queue | 证据级别（离线实测） | 真实 Provider |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `codex` | Fake 授予（手握手） | `resume_with_history` | `enforced` | `exact` | 授予 | `provider_handshake`（测试注入） | **NOT_TESTED** |
| `claude-code` | Fake 拒绝 → 走 Queue | `resume_with_history` | `enforced` | `exact` | 授予 | `provider_handshake`（未声明 steer → unknown） | **NOT_TESTED** |
| `omp` | 无证据 → 拒绝 | 无证据 | 无证据 | 无证据 | 无证据 | 仅 `static`（不可授权） | **NOT_TESTED** |
| `deepseek-harness` | 无证据 → 拒绝 | 无证据 | 无证据 | 无证据 | 无证据 | 仅 `static`（不可授权） | **NOT_TESTED** |

**补充事实（必须与上表一起读）：**

- `codex` 的"授予"**只**来自测试注入的 `provider_handshake` 快照；本仓**没有**真实 codex 握手代码路径。
- `claude-code` 的"拒绝"同样来自注入的握手**未声明** steer，而非真实 Claude 的行为。这一点是本矩阵最重要的一行：**同一个 Harness 家族里，一个能 steer、一个不能，差别只在证据而非名字**。
- **决定性实验（`TestM2OfflineEndToEndTwoHarnesses` 内）**：对**同一个 harness 名 `codex`**、**两个不同 session** 询问 steer，得到两个相反答案 ——
  `same harness, different sessions: proven=true (supported) vs unproven=false (not_observed)`。
  若能力是"harness 名 → 固定答案"的表，这两个答案必须相同。它们不同，因此该门禁读的是**每 session 的证据**。这比"两个不同 harness 得到不同答案"更强：后者可能只是两张表。
- `omp` / `deepseek-harness` 在生产接线（`daemon/execution_wiring.go`）下**只**有静态画像，`Gate` 必然以 `static_only` 拒绝任何控制。这不是"不支持"，是"未证明"。
- 生产接线**不注册任何 probe**（`TestM2WiringNeverTouchesProviderState` 断言），因此真实部署中**所有** Harness 都停在 `static_only`。

> **Fake/simulated only; no real Codex/Claude/OMP/DeepSeek session was started; 0 provider turns; 0 tokens.**

---

## 6. 安全发现（M2 期间全部 P0/P1）

| # | 编号 | 级别 | 根因 | 修复 | 钉住它的测试 |
| --- | --- | --- | --- | --- | --- |
| 1 | M2-03 **P0-1** | P0 | 绑定表 coherence CHECK 要求「session 存在 ⇒ state 在会话态集合」，而 `sessions` 删除走 FK `ON DELETE SET NULL`；SQLite 在同一条语句内应用动作与 CHECK，于是**删除一个已终止 session 直接失败**，报原始 `CHECK constraint failed`，用户看到 SQLite 字符串 | 迁移加 `BEFORE DELETE ON sessions` 触发器：先把绑定落为 `reconcile_required` + `failure_code='session_deleted'` + 清 `session_id`/`bound_at`/generation，再让 FK 合法置空 | `TestWorkflowBindingSurvivesSessionDeletion`、`TestWorkflowBindingNeverLooksLiveAfterSessionDeletion` |
| 2 | M2-03 **P0-2** | P0 | `TransitionWorkflowAttemptBinding` 对**所有** `HasSession()` 目标一律返回 `ErrWorkflowBindingSessionRequired`，导致 `running` 与三个 stop 状态成为不可达死代码，M2-05 的 stop 路径**根本无法编写** | 改为：目标需 session 时，只要**当前**已持有 session 就允许（这是正常推进）；仅当从无 session 状态尝试获取时才拒绝 | `TestWorkflowBindingWalksSessionBearingStates`、`TestWorkflowBindingTransitionCannotAcquireSession`（负向） |
| 3 | M2-05 **P0** | P0 | interventions 的 `session_deleted` 触发器**模式盲**地把未决行重写为 `unknown`；而表级 mode↔state CHECK 对 `mode='stop'` **禁止** `unknown`，于是删除 session 本身失败 —— 与 P0-1 同类缺陷重复出现 | 触发器改为 `CASE`：已结算态保持原状；`mode='stop'` → `stop_unknown`；其余 → `unknown` | `TestWorkflowInterventionSessionDeletionIsLegalForEveryModeAndState`（穷举全部 (mode,state) 对） |
| 4 | M2-07 **P1-A** | P1 | `WorkflowTaskRequestResponse` 直接暴露 `RequestJSON`；该 JSON 可含 repo scope 与文件路径，而只有 decide 路径**应当**解释它 | 从响应删除，替换为不披露内容的指纹 `requestSha`（64-hex） | `TestTaskRequestResponseCarriesNoRawPayload`、`TestM2TaskRequestHidesItsPayload` |
| 5 | M2-07 **P1-B** | P1 | 消息丢失触发器在**任一** session 匹配时把 `sender_session_id` 与 `target_session_id` **一起**置空，删除发送方会毁掉仍然存活的接收方引用 | 改为列条件式 `CASE WHEN ... = OLD.id`，只清匹配的那一列 | `TestMigrationWorkflowMessageLossNullsOnlyTheMatchingColumn`（两个方向） |
| 6 | M2-07 **P1-C** | P1 | `payload_ref` 原 CHECK 允许 `.`/`..`/`/`/`\`/`:`/百分号编码，等于允许**路径穿越形态**的引用穿过"它是 artifact id，不是路径"的承诺 | 改为闭合字符类 `[A-Za-z0-9._-]`，显式拒绝 `.` 与 `..`；域层 `ValidPayloadRef` 同步 | `TestMigrationWorkflowMessagePayloadRefClosedClass`（13 个用例，含双重编码） |
| 7 | M2-08 **P1-D** | P1 | `FakeGateway` 与 `daemon/execution_wiring.go` 的注释声称「未导出 marker 方法使 fake 误接为编译错误」。**该保证不成立**：marker 方法存在但**没有任何接口需要它**，是死代码；且 `ports.ExecutionGateway` 声明在 `ports` 包，天然不含未导出方法 | 删除死代码 marker + `testOnlyMarker`；注释改为陈述**真实**的行为保证（仓内无真实 gateway 实现 + 单一接线点 + 缺 gateway 时拒绝而非降级） | `TestM2WiringNeverTouchesProviderState`（行为面）；注释不再主张编译期保证 |
| 8 | M2-08 **P1-E** | P1 | M2 新增迁移的 `Down` 只 `DROP` 表/触发器，却**不清理** `change_log` 中自有事件类型；表被删而事件留下 ⇒ 回滚后 `PRAGMA integrity_check` 报 `CHECK constraint failed in change_log` | 在**自有**（未提交）迁移的 `Down` 中各自删除本迁移引入的事件类型 | `TestM2LocalRollbacksEmitZeroEvents` |
| 9 | M1 **既存 P1**（非 M2 引入） | P1 | `20261103073516664_workflow_cdc.sql`（**已提交**）的 `Down` 收窄事件类型 CHECK 但**不删**对应行，回滚后数据库 `integrity_check` 失败 | **未修复**：`AGENTS.md` 禁止改动已合并迁移；修复需新迁移 + 用户批准。已按用户指示记录为限制 | `TestM1DownPathLeavesChangeLogInvalid`（断言缺陷**存在**，修复时该测试会失败并提醒更新文档） |

> **关于 #9 的诚实说明**：我曾在实现 #8 时**误改**了这个已提交迁移（在它的 `Down` 里加了 DELETE）。这是硬规则违反。已按 HEAD 字节完整还原（`git hash-object` = `74c21d95b8831b11eb156511db075a7175e94744`，与 `git rev-parse HEAD:...` 一致，diff 为空，171 行），并把真正的问题移交为新迁移的待办与本文档的限制记录。

---

## 7. 继承 M1-P2 风险：最终分类

| # | 风险 | 入场分类 | M2 后最终分类 | M2 事实 |
| --- | --- | --- | --- | --- |
| 1 | 幂等重放返回"当前状态"而非原响应快照 | 经用户批准延期 | **仍延期，M2 未触碰** | M2 的 `BindAttempt` 用 `SpawnRequestID` + `SpawnHash` 做稳定 receipt，不把"读到当前状态"当作"Provider 已执行" |
| 2 | `LeaseToken` 经 `%+v` 泄露 | 已测试（生产面收敛） | **已测试，覆盖面扩大** | M2 生产文件 `%+v`/`%#v` 计数 = **0**（grep 退出码 1）；7 个新响应形状 + 4 个新 CDC payload 均断言无 `leaseToken` 与无未解释 64-hex；唯一 M2 启动日志经 `TestM2WiringLogRedaction` 白名单化 |
| 3 | `cancelled` Task 的完成语义未定义 | 保守阻断 | **仍阻断** | M2 未引入任何 `cancelled` 完成路径；仍无写入方 |
| 4 | `claimed → succeeded` 缺 `started_at` | 已测试 | **已测试，M2 遵守** | M2 绑定写独立 `bound_at`，不把 `attempts.started_at` 当 Provider 启动时刻 |
| 5 | SQLite 单写者假设 | 经用户批准延期 | **仍延期，M2 未违反** | M2 全部写路径走既有 `qw` + `writeMu`；未引入第二个写进程 |
| 6 | `handoff_note.go` 两条旧失败 | 经用户批准延期 | **仍延期（基线内）** | 计入 267 基线，不计 M2 回归；本轮未修改该文件 |
| 7 | Codex Windows 账号诊断 | 保守阻断 | **仍阻断** | M2 只读脱敏 `reasonCode`；未读写真实 `.codex`，未改 ACL |
| 8 | Claude gateway 阻断 | 保守阻断 | **仍阻断** | 未移除/替换 `ANTHROPIC_BASE_URL`，未改 `~/.claude/settings.json`，未打印 token |

---

## 8. 已知偏差与限制

### 8.1 本轮新增 P1（已修）

见 §6 #7（false compile-time guarantee）、#8（M2 自有 Down 未清事件）。两者均已修复并有测试钉住。

### 8.2 我编辑过 `migrate_workflow_cdc_test.go`

该文件在 M2-03 时因新增事件类型而适配（allowlist 追加）。它**不是**迁移文件，不受"禁止改动已合并迁移"约束，但仍属对 M1 测试的修改，记录在此以便审计。本轮 M2-08 未再改动它。

### 8.3 回滚覆盖的诚实边界

`TestM2LocalRollbacksEmitZeroEvents` 只覆盖 **M2 自有**的 4 个迁移。**已提交**的 M1 迁移链的回滚缺陷未被修复（§7.9），因此：

- **不能说**"整个 M2→M1 回滚路径干净"。正确的说法是"**M2 自有的回滚路径干净**；跨入 M1 已提交链的回滚仍会留下 `change_log` 一致性缺陷"。

### 8.4 `cancelled`、多写者、真实 Provider 均未在本轮触及

见 §7 #3、#5 与 §9。

---

## 9. BLOCKED / NOT_TESTED（需要 M0-05 预算）

以下全部**未执行**，且**无法**在零 Provider 约束下执行。任何"M2 全部通过"的表述都会与本节矛盾。

| # | 项目 | 为何阻塞 | 现状 |
| --- | --- | --- | --- |
| 1 | 真实 Session 启动（任一 Harness） | 需真实 Provider 配额与凭据 | **BLOCKED**：仓内**不存在**真实 `ports.ExecutionGateway` 实现 |
| 2 | 真实 Chat 往返 | 同上 | **NOT_TESTED** |
| 3 | 真实 Queue 投递 | 同上 | **NOT_TESTED** |
| 4 | 真实 Steer（`provider_handshake` 授权） | 需真实握手声明 steer | **NOT_TESTED**：只验证了"证据未声明时拒绝"，未验证"真实声明时可 steer" |
| 5 | 真实 Stop / 中断回执 | 需真实 controller | **NOT_TESTED** |
| 6 | 真实 Resume | 需真实 session 恢复 | **NOT_TESTED** |
| 7 | 真实 approval 拒绝路径 | 需真实 approval 机制 | **NOT_TESTED** |
| 8 | token 计费/用量核算 | 需真实 provider usage 字段 | **NOT_TESTED**：`usage` 只到 `static` 级 |
| 9 | 真实 worktree 隔离 | 需真实 `gitworktree` + 真实 spawn | **NOT_TESTED**：M2 只写 `expectedBaseSHA` 与 `sessionWorktreeRef` 记录，未验证真实隔离 |
| 10 | Windows 进程清理 | 需真实子进程 | **NOT_TESTED**：全程 0 进程 |
| 11 | 真实四 Harness 能力矩阵 | 需 4 家真实握手 | **NOT_TESTED**：`omp`/`deepseek-harness` 只有静态画像 |
| 12 | M1 已提交迁移回滚缺陷修复 | 需新迁移 + 用户批准 | **BLOCKED**（§7.9） |

---

## 10. 剩余风险与 M3+ 归属

**M3+ 拥有、且本轮明确未实现的部分（一句不含糊的话：以下一项都没有实现）：**

| 领域 | 状态 |
| --- | --- |
| 调度器（scheduler / DAG 执行） | **未实现**。`Decide(approve)` 只记录答案；`WorkflowTaskRequestStateApproved` 不产生 task/edge，`ReasonCode = TASK_REQUEST_NOT_MATERIALIZED` |
| 预算与配额（budget） | **未实现** |
| 复核门禁（review gate 自动流转） | **未实现** |
| 桌面 UI | **未实现**（只有 HTTP 契约与生成的 TypeScript 类型） |
| 打包/分发 | **未实现** |
| Stop 后的 replan 执行 | **未实现**。`Stop` 只返回 `ReplanRequest`，无执行方 |
| 多写者并发 | **未实现**，仍受 SQLite 单写者约束 |

**剩余风险（按严重度）：**

1. **M1 已提交迁移回滚缺陷**（§7.9）：回滚到 M1 以下会得到 `integrity_check` 失败的库。影响面仅限"回滚"这一操作，正常前进路径不受影响。修复需新迁移 + 批准。
2. **真实 Provider 行为完全未知**（§9）：能力矩阵的每一行对真实 Harness 都是空白。M2 交付的是**门禁机制**，不是**门禁结论**。
3. **`static_only` 是生产默认**：在生产接线下所有 Harness 都无法获得控制授权。这是**有意的安全默认**，但意味着 M2 上线后用户看到的将是"处处不可用"，直到 M0-05 提供真实握手。
4. **回滚覆盖不完整**（§8.3）。

---

## 11. 判定

> **M2 离线开发门禁：通过。**
> 依据：`go build ./...` = 0；`go vet <M2 包>` = 0；`go test <M2 包集> -count=1` = 0；`-race` = 0；相对 267 条 Windows 基线**新增回归 = 0**；异常注入 10/10；生成物两次字节一致；前端 typecheck = 0。
>
> **不构成**以下任何结论：
> - ❌ "M2 全部通过" —— 真实 Provider 路径 0 行执行（§9）
> - ❌ "四 Agent 全部支持" —— `omp`/`deepseek-harness` 只有静态画像，且生产接线**不注册任何 probe**（§5）
> - ❌ "M0 已通过" —— M0-05 预算未获批，真实会话/计费/隔离全部 `NOT_TESTED`（§9）
> - ❌ "整体回滚干净" —— 仅 M2 自有迁移干净，M1 已提交链有既存缺陷（§8.3、§7.9）
>
> 真实 Provider 验收必须在 M0-05 获批后独立进行，并复用本报告的矩阵结构（§5）逐格替换为实测值。
