# M3 动态调度与预算系统实施报告

状态：M3-01～M3-09 已交付；[独立验收](m3-acceptance.md) 判定 G7 在限定的 Windows 离线范围内 PASS。启动重附着 P1、legacy 观察兼容及 HTTP 时钟混用均已修复；最终 M2/M3 普通与 race 各39顶层/58含子项 PASS、0 FAIL/SKIP，独立M3六项全部通过。本范围没有未关闭的新增P0/P1；真实环境未验项及两个历史domain失败仍按§7～8保留，不代表全仓或真实Provider验收通过。

## 1. 基线与范围

- 工作区：`E:/code/agent-orchestrator`；分支 `feat/multi-agent-desktop`；HEAD `8b7d7901b52df1964babe233df3a4e27e7523c47`。
- M2 准入：`m2-acceptance-round6.md` 的独立离线 G0 PASS。没有第七轮；没有把真实 Provider 验收算入该结论。
- 证据根：`C:/Users/11459/AppData/Local/Temp/ao-m3-implementation-20261105`。`entry-files.json`、`entry-status.txt` 是进入生产编码前的保护基线。
- 保留所有既有未提交代码；无 commit、push、merge、部署，无 `.codex/` / `.claude/` 配置改动。全部执行替身测试使用临时 SQLite/Git，不访问真实 AO 数据或消耗 Provider Token。
- 开发与复验子代理按用户最新指令使用 `gpt-6-astra`；先前 `gpt-6.1-sol` 限流不计验收结果。

## 2. 实现与阶段状态

| 阶段 | 实际实现 | 当前验证状态 |
|---|---|---|
| M3-01 | 接口映射、四份 ADR、基线冻结 | 已完成；独立 M2 G0 准入通过 |
| M3-02 | SQLite Leader/Epoch、原子 Claim/槽位/三级预算、策略 CAS、幂等审计 | 完整 SQLite/核心服务通过；观察专用 legacy 重附着不新增 Admission |
| M3-03 | Windows 原始 CPU/RAM 采样、3/10、Adapter 限额、滞回、加权公平与优先级/老化 | G2 独立普通/race通过；公平集成改为显式等待容量恢复并固定最不利游标，未改生产调度算法 |
| M3-04 | canonical Usage 归因、定点金额、UNKNOWN、修正、软提醒和预算结算 | 独立预算反例及完整核心服务通过 |
| M3-05 | 内部 ExecutionPermit、四路 Spawn、普通恢复重新准入、可信续租/恢复 | 启动 P1、legacy 兼容红绿及独立普通/race通过；完整 Manager/Chat 通过 |
| M3-06 | 独立批准后事务物化、DAG/目录/祖先预算约束、唯一结果 | 领域/Store/真实 HTTP 物化链及最终完整集成通过 |
| M3-07 | daemon 启动门禁、独立心跳、有界派发、HTTP 控制、CDC/类型生成 | 真实 maintain 恢复依赖独立通过；HTTP 时钟同源修复后完整 HTTP/daemon、容量和过期拒绝均通过 |
| M3-08 | 真实 SQLite/Manager/Git/HTTP + Fake Driver/资源/时钟/Usage | 修后完整普通/race、指定核心/HTTP/Manager/Chat、build/vet及生成检查通过；独立M3六项通过 |
| M3-09 | 本报告、改动清单、M4 交接与独立验收 | 独立Astra已出具限定Windows离线G7 PASS，命令/失败/边界均在独立报告中保留 |

## 3. 调度、执行和恢复

`READY → 原子 Admission/预算/Claim → 稳定 SpawnKey → ExecutionCoordinator.BindAttempt → SessionManager → 实查 Session/工作区 → Running → 可信 Settle`。

Scheduler 只授予执行资格，不直接启动 Provider。普通、ClientRequest、Automation、任务准备发布共用 `CreateAdmittedSession`；已释放普通 Session 再次恢复需新 Admission。未确定或在途启动保持占位，不能凭 TTL 再次启动。

Leader 租期 30 秒，每 5 秒维护 tick 检查，接近续租点时以 CAS 延长。daemon 先等待已有 Session/runtime 启动对账，再恢复 Admission 和预算，最后设置本任期 recovered。Tracker intake 和 Automation 首轮等待该门禁。丢回执只查稳定 SpawnKey 或已接受 Session；UNKNOWN 不推断死亡。

心跳与 Tick/Bind 分离。一次扫描最多检查 100 个候选、领取 3 个；慢 Bind 使用独立且最多 10 个 worker，成功领取后的中途错误仍交付已提交的 Dispatch 一次。关闭先取消并等待这些 worker，再拆 Chat/LCM/CDC。5 秒合并 CDC 唤醒，避免同步广播阻塞或自身事件忙循环。

续租绑定 Worker、Attempt、内部 LeaseToken、Session generation、Admission revision 和原 Epoch，先实查工作区再续租。旧快照、已到期、未知 Attempt 不获续租。正常周期也推动 provisioning Binding 到 ready；进程退出不是任务成功。业务完成仍走可信 Operator 批准的 Settle。物理槽仅凭持久终止与同代际证据释放，预算随后独立完成 finalization。

失败任务由明确批准的 Retry 记录有界退避，再经原 Workflow Retry 转换；重试仍经过原子资源与预算准入。最新失败 Attempt、三次上限和持久 due 共同防止旧重试授权作用于后来的失败。

本轮关闭的确定回归包括：成功 Claim 后 OFFSET 跳过高优先级候选；慢 Tick 阻塞续租；同实例游标 revision 竞争误丢 Leader 身份；终态 Settle 幂等被 live 校验提前拒绝；新增外键阻断合法历史 Session 删除；F03 工作区移回后的显式恢复被状态白名单误拒绝。修复均保留真实红灯或原失败日志。

持久状态分工如下；Scheduler 不把观测失败当成终止：

| 对象 | 允许的推进 | 闭锁条件 |
|---|---|---|
| Leader | 无任期 → CAS 取得 owner/epoch、未恢复 → 对账完成后 recovered → 同 owner 续租 | 过期、被新 epoch 替代或未恢复均拒绝准入与业务许可 |
| Admission | `reserved` → 接受稳定 Session 后 `active`；无法确定执行结果或重启时 `uncertain` | `reserved/active/uncertain` 都占槽；仅同代际持久终止证据允许 `released` |
| Attempt | `READY` 任务被 Claim → 执行、续租 → 可信 Settle；过期不确定进入 `UNKNOWN` | `UNKNOWN` 不重启、不因续租复活；显式裁决或批准 Retry 才能继续，Retry 仍重新准入 |
| Budget reservation | 准入时预留 → 物理终止后待对账 → 费用证据完整且代际匹配时 consumed | 缺失、UNKNOWN 或 pending correction 保留未知，不将其归零 |

启动修复把“观察既有 Host”与“取得业务执行权”分开：`SessionReattachPermit` 以本实例 Fence、原 Session generation、Binding/Admission 快照执行事务 CAS。Startup 暂未恢复的 Fence 错误进入既有有界重试；收到不匹配 Provider 或非 live 重连回执时只 Close 观察连接。M2 没有 Admission 的 Workflow 会话只得到观察快照，核对 Binding/Attempt/Session 归属，既不新建 Admission/预算，也不能取得 Start、Settle、Renew 权限。独立真实 `maintain` 反例验证先完成保守恢复，再开放观察通道，不靠测试手动标记 recovered 掩盖循环等待。

## 4. 预算与物化

金额统一为 int64 nano-USD，并检查溢出。Global/Workflow/Task 是同一 canonical 事件的三个投影，不相加成三倍费用；已物化后代继续计入全部祖先 Task 原预算池。

原生 Token 加 catalog 价格仅标记 ESTIMATED；缺金额、发生时间、价格版本、可信计费来源或支持的 Harness 时为 UNKNOWN。OMP/DeepSeek 未获可信费用证据，不能填零。成本回填更新原事件 revision，并记录前后快照 correction；correction 不再次收费。pending 修正可能降低旧成本，因此对账前不把旧金额当可靠下界，不显示已知余额或发出旧软提醒。

硬预算只限制新准入，不保证在途 Provider 最终费用不超预算。未知预留或用量失败闭锁。软预算 80%/95% 提醒按策略、窗口、revision、阈值去重并经 CDC 发布。

物理终止后 reservation 先进入 uncertain；所有 Usage Binding/Source 完结、无 UNKNOWN、无 pending correction 且终止 generation 匹配，才 consumed。Session 多次执行只按唯一 Workflow Binding 归因，避免 Admission JOIN 放大事件；后一次终止不能替前一次未知费用完成结算。

TaskRequest 必须是已批准的结构化 `create_task`，再次取得独立 Operator 批准后物化。重新验证两个 revision、payload SHA、RepoKey/allowed_paths、父 Harness、DAG/派生树边界、任务数 50/深度 12/分支 8。Task/Edge/Repo/Workflow revision 和唯一物化结果同事务。重放最多产生一个结果，不从聊天文本执行任务，不提高原并发或预算。

## 5. 数据库与生成物

新增 append-only 迁移，均由 `npm run migration:create` 创建并登记 shippedMigrations：

| ID | 文件职责 |
|---|---|
| 20261105073957583 | `m3_scheduler_admission.sql`：Leader、策略、槽位、预算预留、审计/公平/重试、CDC |
| 20261105084719358 | `m3_task_request_materialization.sql`：物化结果与祖先预算关系 |
| 20261105090651703 | `m3_budget_ledger.sql`：canonical 费用投影、pending、correction、软提醒 |
| 20261105094026402 | `m3_scheduler_control.sql`：控制状态/预算预留 CDC |
| 20261105101553243 | `m3_session_execution_generations.sql`：执行代际、未释放 Session 唯一性、历史/FK/CDC 保留 |
| 20261105102052187 | `m3_budget_session_attribution.sql`：多 Admission 下的费用单次归因 |

SQLC 从 `queries/scheduler.sql`、`execution_permit.sql`、`budget_usage.sql`、`workflow_materialization.sql` 生成；未手改 gen 文件。generation 迁移 down 无法表示多次执行时，在关闭外键及开启回退事务之前拒绝回退，绝不删历史强行成功；实测日志为 `m3-05-execution/migration-rollback-safe.log`。M1 原历史 down 路径遗留 change_log 缺陷由已有回归明确记录，本轮未修改历史迁移。

## 6. HTTP 与调用方约束

以下路径均带 `/api/v1` 前缀：

| 路径 | 行为 |
|---|---|
| GET `/scheduler/status` | 资源、有效容量、实际占位、策略与恢复状态 |
| PUT `/scheduler/policy` | 全局/Adapter 策略 CAS |
| GET `/workflows/{wf}/scheduler` | Workflow 调度状态 |
| POST `/workflows/{wf}/pause`、`resume-scheduling` | 暂停/恢复调度，保留公平游标 |
| GET/PUT `/scheduler/budget` | Global 预算 |
| GET/PUT `/workflows/{wf}/budget` | Workflow 预算 |
| GET/PUT `/workflows/{wf}/tasks/{task}/budget` | Task 预算，先验证归属 |
| GET `/scheduler/decisions` | 有界增量审计 |
| POST `/workflows/{wf}/task-requests/{request}/materialize` | 独立批准后物化 |

所有写操作必须消费一次性、绑定方法/路径/正文摘要的 Operator 批准；`expectedRevision`、`idempotencyKey`、`reason` 来自同一获批正文。`reason` 是 `^[A-Z0-9_]{1,80}$` 安全原因码，例如 `USER_BUDGET_CHANGE`，不是自由文本。URL 固定预算作用域，正文不能扩大 scope。

Agent 仅提交 `X-AO-Session-Id` 与 `X-AO-Agent-Capability`。服务端从唯一活绑定推导 Attempt/Task/Workflow，并比较本 daemon 自持 Fence；合法 capability 也不能借另一个 daemon 的任期。外部 LeaseToken/旧 lease header 返回 `WORKFLOW_SECURITY_UPGRADE_REQUIRED`。Claim 响应移除 LeaseToken，Start/Settle 从内部许可读取租约。缺本实例接线失败关闭，租约/fence 冲突稳定返回 409。

`dto.go`/`specgen/build.go` 为契约源，`openapi.yaml` 与前端 `schema.ts` 经 `npm run api` 同步生成。CDC 仅包含身份、状态、数值质量、revision、原因码，不包含 LeaseToken、Operator 凭据或 Provider Key。

## 7. 验证证据

所有 Go 命令在 `backend` 执行；npm 命令在项目根执行。PowerShell 使用 `rtk proxy powershell -NoProfile`、`login:false`。工具链：Go 1.27.1 windows/amd64、CGO_ENABLED=1、Node 22.20.0、npm 10.9.3、Git 2.45.1.windows.1。以下 `T` 指证据根的 `takeover-final/`；每项 `.meta.json` 或 `.metadata.json` 保留精确 argv、退出码，`.jsonl/.log` 保留原始输出。带快照的 runner 同时保存执行前后 SHA256，`source_drift=[]` 表示运行过程中没有源码变化。

### 7.1 最终检查

| 实际命令 | 结果 | 证据 |
|---|---|---|
| `go test -json ./internal/service/scheduler ./internal/service/budget ./internal/service/workflow ./internal/service/execution ./internal/service/intervention ./internal/service/principal ./internal/storage/sqlite/... -count=1 -timeout 30m` | exit 0；964 顶层/1503 含子项 PASS，2 子项 SKIP，无 FAIL/漂移 | `T/core-sqlite.*` |
| `go test -json ./internal/session_manager -count=1 -timeout 30m` | exit 0；787 顶层/1518 含子项 PASS，14 SKIP，无 FAIL/漂移 | `T/manager-full.*` |
| `go test -json ./internal/service/chat -count=1 -timeout 20m` | exit 0；710 顶层/1113 含子项 PASS，0 FAIL/SKIP | `T/chat-full.*` |
| `go test -json ./internal/httpd/... ./internal/daemon -count=1 -timeout 20m` | exit 0；1026 顶层/1603 含子项 PASS，11 顶层/14 含子项 SKIP，无 FAIL/漂移 | `T/http-daemon-clock.*` |
| `go test -json ./internal/integration -run "TestM2\|TestM3" -count=1 -timeout 30m` | exit 0；39 顶层/58 含子项 PASS，0 FAIL/SKIP，无漂移；包 440.216s | `T/integration-final-03.*` |
| 上一命令增加 `-race` | exit 0；39 顶层/58 含子项 PASS，0 FAIL/SKIP，无 DATA RACE/漂移；包 569.935s | `T/integration-race-03.*` |
| `go build ./...`、`go vet ./...` | 均 exit 0，无诊断/漂移；进程耗时 14.674s / 22.783s | `T/backend-build-clock.*`、`T/backend-vet-clock.*` |
| `npm run frontend:typecheck` | exit 0；`tsgo --noEmit` 无错误 | `T/frontend-typecheck-02.*` |
| `npm run api` | exit 0；OpenAPI 与 schema.ts 生成前后 SHA256 相同 | `T/api.*`、`T/api-drift.json` |
| `npm run sqlc`（实际 `npm.cmd --prefix .. run sqlc`，cwd 为 backend） | exit 0；117.054s；生成源码无漂移 | `T/sqlc-proxy.*` |
| `git diff --check` | exit 0；仅 Git 既有 LF→CRLF 提示，无 whitespace 错误 | `T/diff-check-final.*` |

SQLite 的 2 个 SKIP 是 Windows 文件名不能包含 `?`；HTTP/daemon 的 14 个 SKIP 来自未配置真实 Electron、Windows PTY/文件名/Unix 权限差异及缺少 symlink 权限；Manager 的 14 个 SKIP 为已有 POSIX/macOS/Windows 权限限制。逐项原因在对应 `.meta.json/skips`，本轮没有新增 skip。

SQLC 最初两次访问 `proxy.golang.org` EOF，第三次离线缓存尝试因弃用元数据查询失败，已停止重试并请用户决定。用户明确批准使用本机现有代理后，仅为该进程使用 `HTTP_PROXY/HTTPS_PROXY=http://127.0.0.1:7897` 和 `GOPROXY=https://goproxy.cn,direct`，保持 sqlc v1.31.1 与 checksum 校验；最终生成成功，没有修改配置文件。

### 7.2 失败、根因与反向验证

| 问题 | 失败证据及修复 | 修后证据 |
|---|---|---|
| 启动恢复循环依赖 P1 | 独立 `startup-confirmed` exit 1：既有 live Host 被新执行准入拒绝且没有重试；新增仅观察许可及 Fence 有界重试 | `takeover-independent-astra/startup-green`、`startup-race` 各 exit 0，7 顶层/11 含子项；删除 Fence 重试分类的变异准确变红 |
| M2 无 Admission 的既有 Workflow 重附着 | `takeover-reattach/red-20261105-200938875.jsonl` exit 1：`trusted admission evidence is required`；仅为归属匹配的既有 Session 构建观察快照 | `green-20261105-201100886`、`race-20261105-201210495` 各 exit 0，6 顶层/10 含子项；错误归属和业务许可仍拒绝 |
| 公平性用例前置条件错误 | 调高策略到 10 后有效容量仍受滞回约束，原测试两轮容量仅 3、4，且 UUID 决定谁先执行；`T/fairness-diagnostic-02` exit 1 实证 | 用真实资源恢复与续租到容量 10，固定高权重先执行；断言增强为 high=5、low=1，`T/fairness-fixed` exit 0，生产算法未改 |
| HTTP 与受控时钟混用 | `T/integration-race-02`、独立 `m3-core-final` 都在容量测试报 409 `SCHEDULER_FENCE_STALE`，无 DATA RACE；独立 `mixed-clock-red` exit 1，精确证明同一许可 fakeNow 可读、wallNow 拒绝 | Controller 注入同源时钟，默认仍为真实时间；正式 fixture 用固定历史 UTC 使漏接立即暴露。`T/capacity-clock-fixed`、独立 `mixed-clock-green` 各 1 PASS、exit 0；独立 `same-clock-expiry` 1 PASS，Leader 同 epoch 有效而 Attempt 到期时 HTTP 返回 409 `WORKFLOW_ATTEMPT_LEASE_CONFLICT`，原 Session/租期不变；完整普通/race均通过 |

独立 `bridge-green` 验证生产 dispatch → maintain → Start，`maintain-recovery` 验证真实恢复依赖，两项均 exit 0。它们使用真实 SQLite/服务和受控 Host 观察，不冒充真实 Provider。旧线程的中间编译、夹具、主动取消日志保留且不计 PASS；旧生成漂移证据缺失已由本次真实重新生成补齐。

修后独立完整M3的 `takeover-independent-astra/m3-core-clock-final.*` 为6顶层/6含子项 PASS，0 FAIL/SKIP、exit0，包142.427s；源快照无漂移。独立用例的精确命令、原失败日志和G7判断由 `m3-acceptance.md` 记录。最终快照核对确认核心/Manager完整组之后只变更HTTP两文件和integration三文件；它们不依赖这次变更，HTTP及完整集成已在最终源码重新运行。

### 7.3 M3 矩阵与边界

| 验收场景 | 正式用例与实际断言 |
|---|---|
| 默认 3、最大 10、第 11 个 | `TestM3RealManagerSharedCapacity`：默认单 Adapter 上限 2；只提高 Adapter 后全局停在 3；再提高全局并经资源滞回到 10；第 11 个 HTTP Claim 返回 409，Session、Admission、Git worktree 和 live Controller 数一致 |
| 同库竞争与公平性 | `TestM3ContendingSchedulersAndFairRotation` 和独立进程用例：同 Attempt 仅一赢家；高权重 5 次时低权重仍获 1 次 |
| CPU/RAM、采样失败、三级预算、UNKNOWN/重放 | `TestM3ResourceAndThreeLevelBudgetGates`：CPU 90%、RAM 512MiB/16GiB 和采样错误阻止新增；Global/Workflow/Task 各自不足均拒绝；Agent 改预算 403；UNKNOWN 保持未知，重复 canonical 用量不重复归因 |
| 续租、重启、旧 epoch/UNKNOWN | `TestM3LeaseRenewalAndRestartFence`：连续 3 次推进 20s 并可信续租；推进 3min 后 reopened SQLite 保留原 Session/uncertain 占位；旧许可、旧 HTTP 任期拒绝，UNKNOWN 不重新 Spawn |
| TaskRequest 与 M2 安全回归 | 正式物化集成验证批准/重放、祖先预算与权限；完整 `TestM2\|TestM3` 包含 F-01/F-02/F-03、worktree 删除/替换闭锁。原 G1/G2/预算/daemon 独立反例细节见对应 `m3-*-review.md` |

容量等正式集成夹具按人工 HTTP Start → Bind 执行；生产自动 Bind → 观测 ready → Start 的时序由独立 daemon 桥接反例验证，两者不混称同一路径。

多进程证据有明确边界：两个独立测试进程竞争同一 SQLite，只产生一个 Claim/Admission；新任期消费时因原执行结果不明保留 uncertain，Session 数为 0。唯一真实 Session 的 Bind/Manager 链路和生产 daemon dispatch/maintain 桥接由另外的集成/独立反例覆盖；没有启动两个完整 daemon 去证明端到端单 Session，不能将这些拆分证据表述为该实机验收。

### 7.4 已复现的历史失败

额外执行 `go test -json ./internal/domain ./internal/adapters/resources -count=1 -timeout 10m`：exit 1，110 顶层/483 含子项 PASS、2 FAIL、0 SKIP；资源适配器包通过。失败原文：

```text
TestBuildHandoffNoteStaysUnderTheCapAndKeepsTheLatestTurn
handoff_note_test.go:84: the latest turn must never be dropped
TestHandoffNoteKeepsArtifactsWithinTheByteCap
handoff_note_test.go:211: note must keep the latest request and count the artifacts it left out
```

同机、同工具链以 `git archive HEAD backend/internal/domain` 导出 HEAD 全领域包，通过 `T/domain-head-overlay.json` 跑这两个原用例，仍同断言 2 FAIL、exit 1；相关源码 SHA256 与 HEAD/入场一致。证据为 `T/domain-resources.*`、`T/domain-head-regression.*`、`T/domain-head-comparison.json`。因此可归为历史失败，但不宣称全仓绿灯；本轮没有修改这些无关代码。

## 8. 已知边界与 M4 交接

- M0-05 真实 Provider 预算未批准：没有 Codex/Claude/OMP/DeepSeek 实机运行、真实成本、真实 Windows Hello 或 Electron 手工验收；Ubuntu/Node24/远端 CI 未运行。
- M1 非全局写沿用先业务写、后审计的旧边界：审计失败返回失败，但业务变更可能已提交。M3 策略、预算、物化、全局恢复自身使用事务审计；不能声称所有历史 M1 操作均已原子化。
- 系统进程故障、长期忽略 context 的第三方调用及真实硬件压力未做实机验收。UNKNOWN 保留资源与预算，需要可信对账或显式人工处置；不自动强杀、清理 dirty Worktree。
- M4 可复用 `Scheduler.PrepareWorkflowAdmission/PrepareSessionAdmission/CurrentFence`、`Workflow.MaterializeTaskRequest`、`Budget.Snapshot/Usage/Reconcile`、ExecutionPermit、生成 API 与 CDC。M4 应继续维持独立 Operator 批准、固定 Scope、定点金额、代际/Epoch、canonical 用量唯一性。
- 完整桌面工作台属于 M5，本阶段只有必要 HTTP/生成类型控制面，无新业务 UI。

测试规模与完整文件清单见 `m3-files.md`：相对入场211个变化文件，新增Go测试6141行/非生成Go生产5252行，比例1.169；新增文件无超限，既有超限文件逐项申报。`T/final-review-evidence.json` 实算237项已采集保护文件未变、无删除，含历史迁移、配置目录文件、固定文档与前端快照；API/SQLC产物生成前后及当前摘要一致。后续只更新文档，不把已有大测试文件迁移隐藏为新写小文件。
