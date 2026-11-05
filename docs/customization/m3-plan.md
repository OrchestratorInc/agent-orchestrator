# M3 实施计划 V0.1：动态调度、预算与恢复

> **当前执行（2026-11-05）：M2 第六轮独立离线 G0 PASS；M3-01～M3-09 已交付，独立G7限定Windows离线PASS。** [第六轮独立报告](m2-acceptance-round6.md) 已核对 F-01/F-02/F-03 和规定回归。接替后启动重附着、M2 无 Admission 观察兼容及 HTTP 时钟同源修复均通过；最后完整普通/race各39顶层/58含子项PASS，独立M3六项全部通过。真实 Provider/M0-05 仍未批准；禁止配置修改、Git 提交/推送/合并或部署。

> **设计补充：** [ADR-04](m3-adrs/04-lease-operator.md) 的 Session capability 替代凭据、旧 MCP 拒绝及服务端身份核验已实现；启动观察许可不授予新执行/结算/续租权限。M3-01 的历史 206 PASS/2 FAIL 基线保留，第六轮修复与新证据独立记录。

> 日期：2026-11-05；工作区：`E:\code\agent-orchestrator`；设计依据：`m3-design.md`。
>
> **状态：M3-01～09 COMPLETED。** 实际 HEAD 仍为 `8b7d7901b52df1964babe233df3a4e27e7523c47`。报告与四份 ADR 见 [M3-01 架构审查](m3-architecture-review.md)，初始 G0 阻断是已由第六轮结论解除的历史事实。全部开发测试只允许 Scripted/Fake、临时仓库和隔离 SQLite，零真实 Provider Turn/Token；最终结论及历史domain失败/平台跳过/真实未验范围以独立 [M3 验收](m3-acceptance.md) 为准。

## 1. 总体交付物与依赖图

```text
M3-01 边界确认、基线与 ADR
      |
      v
M3-02 SQLite admission/预算预留/Leader 事务
      |                 \
      v                  v
M3-03 Resource+公平调度  M3-04 用量/预算账本
      |                  /
      +-----------------+
      v
M3-05 Dispatch/Lease/恢复与重试
      |
M3-06 经批准的 TaskRequest 物化（可与 M3-03/04 并行，DB 修改集中串行）
      |
      v
M3-07 Daemon/HTTP/CDC/Policy 安全接线
      |
      v
M3-08 真实临时 Git + SQLite + Scripted E2E/Race
      |
      v
M3-09 独立验收报告与 M4 交接
```

默认最多 3 个并行开发 Agent；主 Agent 统一管理 Store/迁移、接口版本和最终合并，遵守项目并行上限 10。**一个共享核心文件同一时间只允许一个 Agent 写入**。所有子任务默认不可执行 commit/push/deploy。

## 2. 分项工作包

| 任务 | 责任边界 | 产出 | 前置 | 核心完成标准 |
| --- | --- | --- | --- | --- |
| **M3-01** 架构冻结与环境基线 | 架构/测试 Agent | ADR、接口清单、M2 准入记录、已有失败归因 | 可先审查；生产编码仍须 G0 | 实际源码与设计逐项映射，不改业务代码 |
| **M3-02** Store 原子 Admission | Go/SQLite Agent | `scheduler_leader/policy/admissions/budget_reservations/decisions` 迁移、sqlc、Repo/Store 接口 | M3-01 | 资源槽 + 预算预留 + Claim 同一事务；多进程抢占一赢家 |
| **M3-03** 动态容量与公平选择 | Scheduler/Windows Agent | `service/scheduler`、Windows `ResourceSampler`、公平队列 | M3-02 | 3/10 并发、CPU/RAM 滞回、Adapter 限流与公平性可复测 |
| **M3-04** 用量和预算归因 | Budget Agent | `service/budget`、Usage attribution、Pricing 快照/修正 | M3-02 | 三层预算不重扣、UNKNOWN 非 0、硬门禁与币种一致性 |
| **M3-05** 真实调度闭环与租约恢复 | Execution/Session Agent | M2 Coordinator 复用接线、可信续租、恢复/退避、持久化 retry | M3-03/04 | 不重复 Spawn、UNKNOWN 冻结、旧 epoch 拒绝、长任务合法续期 |
| **M3-06** TaskRequest 物化 | Workflow Agent | 结构化批准请求转 Task/Edge/Repo、DAG CAS、Policy 权限 | M3-01/02 | 重复审批只物化一次；环/越权/扩容/跨 Repo 拒绝 |
| **M3-07** 控制面与审计接线 | Daemon/HTTP Agent | API、Operator Gate、CDC、最低限度只读策略状态 | M3-05/06 | 控制端可暂停/调额/查看原因，Agent 不可自批 |
| **M3-08** 集成、异常矩阵和 Race | 独立测试 Agent | M3 E2E、恢复、资源、预算、并发、攻击面、回归报告 | M3-07 | 所有新测试真实执行通过、零真实 Provider、无新 P0/P1 |
| **M3-09** 阶段验收与交接 | 独立验收 Agent | `m3-report.md`、`m3-acceptance.md`、M4 输入清单 | M3-08 | 复验源代码/真实 Git/SQLite，不只看开发方报告 |

### M3-01：准备和不变量检查

阅读 `AGENTS.md`、`CONTRIBUTING.md`、`requirements.md`、`design.md`、`m2-design.md`、`m2-acceptance-round5.md`、最新 M2 修复与独立复验、`m3-design.md`。先记录分支、HEAD、Git dirty 文件，基线包含 M1/M2 未提交部分。至少产出四份短 ADR：
- ADR-01：资源来源/阈值/采样失败后安全降级。
- ADR-02：硬预算保证范围、UNKNOWN 处理、USD 定点货币。
- ADR-03：单机 Scheduler leader/epoch、旧回调栅栏。
- ADR-04：已有 Attempt Lease 续期与 M2 MCP Token 兼容，不泄露密钥。

对准入约束有疑问时，**不自行把 M2 未验收改成 PASS**。

### M3-02：数据库、事务与唯一约束

实施新的 append-only Goose migration 和 SQLC 查询，不修改既有 migration；把当前 Workflow Claim 交易安全地升级成 **一个统一入口**，既约束 Scheduler Claim，也约束原 HTTP/CLI Claim。至少覆盖：
1. `global slot + harness slot + 适用预算` 预留和 Task CAS 在一个 SQLite writer 事务。
2. 一次 Attempt 对应一次 admission；冲突/重放不重复预留、计费、发起会话。
3. Leader epoch 在事务内验证；旧调度器不能拿新派发资格。
4. 预留未知状态不能因为 TTL 到期直接释放。
5. 旧 M1 手动 Claim/Workflow API 保留，但同样受 admission；不得留下旁路。
6. 回滚迁移后不会留下半建表，CDC 更新严格新增/白名单化。

建议先完成真实 SQLite 并发测试再接入 Scheduler。

### M3-03：资源与公平调度

实现纯函数优先的 Planner（输出**决策数据**，而非直接启动）和 Windows Adapter：
- 默认 3，上限 10；Adapter 默认 2；修改参数时不能绕过上限。
- 15s 保底扫描、5s 资源采样、批量有界，每轮最多 3 个新派发。
- CPU 75/85%、可用 RAM 25% / max(15%, 2GiB)，低资源不强杀已运行 Agent。
- 采样失效最多 1 槽（无法保证安全硬边界时 0）；恢复必须满足 3 次滞回样本。
- 跨 Workflow Weighted Deficit RR，Workflow 内优先级 + 有界老化；持久化轮转游标。
- 任务依赖、Capability 认证、冷却期、Budget、Repo 均为硬门禁，公平算法不可越权。

测试使用 FakeClock/FakeResource/多 Workflow 模型，不能把真实电脑恰好低负载当作 PASS 证据。

### M3-04：预算与用量

先复核 `internal/service/usage` 和 `internal/pricing`，禁止再做第二个 Token 采集器：
- Global/Workflow/Task 各层预算，`observe/soft/hard_admission`；
- 价格版本化、source_event_key 去重，允许补偿事件；
- 无法核实的成本为 `unknown`，并保留估算方法/来源；
- 预算预留锁定/释放必须跟随 Admission/Attempt，不能重放扣款；
- 硬限制只阻止**新增执行**，不得假称可以控制第三方 Provider 最终账单绝对不超标；
- 当前无认证统计的 OMP/DeepSeek 不凭空补造用量/价格；
- 本阶段不重写自定义 Provider 配置或模型参数传输。

构造重复事件、价格切换、事件乱序、修正负数、数值溢出、币种不匹配和强制预算冲突的测试。

### M3-05：真实执行、续租与恢复

统一从 M2 Coordinator/Session Manager/Chat Service 取得执行事实：
- Scheduler 不直接运行 CLI、Git 或供应商 Provider；
- Claim/SpawnKey 与 session_id 绑定，结果不确定则查真实 Session/Worktree，而不是重新 Spawn；
- 续租需有来自真实观察的授权证据，不能只相信未经授权的 Worker heartbeat；
- 包括旧 epoch 拒绝、错误 Generation 拒绝、过期 Lease 拒绝、Daemon 崩溃恢复；
- Retry 只在 safe-to-retry 分类和 task.maxAttempts 允许时执行；未经确认的副作用必须 UNKNOWN；
- 故障恢复不清理 dirty Worktree，不强制重置 Git；
- Rate Limit 遵守 Retry-After、指数退避、jitter 和 Adapter cooldown。

必须包含“事务已提交但 Spawn 回执丢失”和“双 Daemon 同时调度同一 Attempt”的真实 SQLite 用例。

### M3-06：TaskRequest

承接 M2 只审批、不物化的状态；批准后材料化必须通过 Workflow Service 一次事务完成：
- 再验证审批是否真的已完成、未被消费、未过期；
- 新 Task/Edge 满足 DAG、RepoKey、allowed_paths、深度、数量及资源预算约束；
- 任何超范围/改依赖/加并发仍需 Operator 策略允许；
- TaskRequest 物化和 Workflow revision 写入用 CAS，重复请求返回同一结果；
- 不让 Agent 文本自动提升权限，失败保留 TaskRequest 原始审计，不写半图。

### M3-07：HTTP/Daemon/CDC

- Daemon 先执行 M2 绑定恢复，再接管 Scheduler leader，恢复旧 Attempt/Reservation 后才允许新 Claim。
- API 对全局/Adapter 并发、预算、暂停/恢复、TaskRequest 物化实施 M2 Operator 单次审批；所有操作带幂等、revision、reason。
- SSE 使用现有 CDC；只输出状态、数值质量、原因码、非秘密用量。
- 输出最小只读管理入口，完整双模式 UI 留 M5。
- 新增控制 API 之前，确认 M2 Operator allowlist；不允许新增未保护的写路由。

### M3-08：E2E、回归与性能

一条完整 Fake/Scripted 链：

```text
Create DAG → Activate → Refresh READY → Fair Pick →
Atomic Admission → M2 Bind/Spawn → Session Observe →
Usage Attribution → Lease Renew → Settle/Retry →
Release Slot/Reservation → Next Task → Restart Reconcile
```

至少提供下列矩阵的独立、反向用例：

| 场景 | 必须证明 |
| --- | --- |
| 3 个默认并发与 10 个最大并发 | 第 11 个尝试从不被启动 |
| 2 个 Daemon/不同进程争抢 | 只有一个 Claim 和 Session |
| 高优先级 A + 始终 Ready 的 B | 公平指针推进，B 不永久饥饿 |
| 资源骤降、样本过期及恢复 | 不新增；已有 Session 不强杀；滞回有效 |
| 全局/Workflow/Task 任一级预算耗尽 | 新入场被拒，尚在跑的真实费用仍保留 |
| UNKNOWN usage/price | 不计零，硬限制 fail-closed |
| 重复 Usage/price backfill | 不双记账，可追踪补偿 |
| 租约 15 分钟临界 + 多次续期 | 活跃 Session 不被误判失败 |
| 断电/崩溃在 Claim/Spawn/Settle 前后 | 不产生第二次副作用 |
| Provider Rate Limit/Token 耗尽 | 有界退避、人工检查，不无限循环 |
| 多仓库一个 Worktree 丢失 | 新派发拒绝，不损坏另一个仓库 |
| Agent 伪装 Operator 改预算 | 403/明确拒绝，凭据不泄漏 |
| TaskRequest DAG 越权/重放 | 不物化或至多物化一次 |
| M1 原单仓与 M2 F-01/F-02/F-03 | 无新回归 |

M3 质量门禁命令（实施阶段需按实有 Go 包调整）：

```powershell
# backend 目录
go test ./internal/service/scheduler ./internal/service/budget -count=1
go test ./internal/service/workflow ./internal/service/execution ./internal/service/intervention ./internal/service/principal -count=1
go test ./internal/storage/sqlite/... -count=1
go test ./internal/integration -run 'TestM2|TestM3' -count=1 -timeout 30m
go test -race ./internal/integration -run 'TestM2|TestM3' -count=1 -timeout 30m
go build ./...
go vet ./...

# 项目根目录
npm run frontend:typecheck
npm run api
npm run sqlc
git diff --check
```

**注意：** 已知 Windows 全量基线可能非绿，新失败须在同机相同工具链与冻结旧基线复验；未重跑的失败不能宣称“既有问题”。`npm run sqlc` 因网络失败时记录 BLOCKED，不算 PASS；真实 Provider 仍禁止调用。

### M3-09：独立验收

开发 Agent 必须交付 `m3-report.md`：修改文件、迁移/API 契约、全部运行命令退出码、负向用例、Race、内存/CPU 降载数据、预算去重/UNKNOWN、恢复矩阵、旧基线对照、未解决项。由另一个 Agent 重新运行核心反例形成 `m3-acceptance.md`，不能仅查文档。

## 3. 各阶段验收闸口

| 闸口 | 要求 |
| --- | --- |
| G0 准入 | M2 通过独立安全/执行链验收或用户书面风险豁免；已建立工作区基线；M0-05 未批准则禁用真实 Provider |
| G1 事务模型 | 资源+预算+Claim 原子；旧 HTTP Claim 不能绕过；Store 并发/重放测试通过 |
| G2 调度决策 | 3/10、Adapter 限额、资源降载、公平性、DAG 准入测试通过 |
| G3 用量/预算 | 三层、价格来源、unknown、硬限额、事件幂等通过 |
| G4 生命周期 | 续租、Spawn 未知、重启、旧 epoch、限流/退避测试通过 |
| G5 安全控制 | Operator 控制面闭锁；Agent/TaskRequest 无越权；API/CDC 不泄露凭据 |
| G6 综合验证 | M3 E2E + M2 回归 + Race + Build/Vet + API/SQLC/TypeScript + 基线分类 |
| G7 独立验收 | 真实临时 Git/SQLite/Windows 回归和缺陷反向测试由非实施 Agent 复测 |

任何 P0/P1 未关闭、真实 Provider 越权、预算未知被当零、资源超出 10、重复启动或审批绕过均阻断 G7。

## 4. 文件与改动政策

- 不修改 `.codex`、`.claude` 的任何配置/导入；不访问真实用户 AO 数据。
- 新增 SQLite migration 为 append-only，SQLC/OpenAPI 从源生成，不手改产物。
- 在多个 Agent 之间按**文件归属**划分；同一核心文件如 `service/workflow/service.go`、`daemon/daemon.go`、`controllers/workflows.go`、`queries/workflow.sql` 必须串行写。
- 不运行真实 Codex、Claude、OMP、DeepSeek；零真实 Token；不批准 Git merge/push/deploy。
- 不覆盖前几轮 M2 验收报告；保留工作区所有未提交差异。
- 任何 P1 修复先加入红灯反例，再改源码，重跑绿灯与前置稳定套件。

## 5. 任务状态与交付

| 编号 | 当前状态 |
| --- | --- |
| M3-01 | COMPLETED / 审查报告、四份ADR、冻结基线与独立文档复核；不等于生产准入或全部设计问题关闭 |
| M3-02 | COMPLETED / 原子Admission及六份迁移已落盘，修后核心与完整SQLite 964顶层通过 |
| M3-03 | COMPLETED / G2及daemon心跳反例独立普通/race通过；确定容量和最不利游标下公平集成通过 |
| M3-04 | COMPLETED / 三级账本、祖先预算、Session多次执行单次归因及代际结算已验证 |
| M3-05 | COMPLETED / 启动观察许可、legacy兼容及独立真实maintain恢复通过；完整Manager 787、Chat 710顶层通过 |
| M3-06 | COMPLETED / Store与真实Agent→HTTP审批物化闭环及完整集成通过 |
| M3-07 | COMPLETED / 修后HTTP/daemon 1026顶层通过；容量及同源时钟到期拒绝独立通过 |
| M3-08 | COMPLETED / 修后完整普通/race各39顶层/58含子项PASS，0FAIL/SKIP；build/vet/api/sqlc/typecheck均exit0 |
| M3-09 | COMPLETED / 实施报告、文件清单和M4交接已交付；独立Astra限定Windows离线G7 PASS |

以下为历史检查点，保留当时的失败和进度；当前状态以本节表格、`m3-report.md` 和独立 `m3-acceptance.md` 为准。任务追踪见 `issues/2026-11-05_14-16-00-m3.csv`。

M3-01 当时实际验证：Workflow/Execution整包136顶层PASS；F-01/F-02/F-03关键集成7顶层PASS；procmem23 PASS、MemoryReader定向6 PASS；Pricing34 PASS/2 FAIL（Windows mode断言）。合计206 PASS/2 FAIL/0 SKIP，失败与历史证据见M3-01报告。独立文档审查提出的LeaseToken权限表述P1已修正文案并复核关闭；能力凭据方案随后在ADR-04及M3实现中冻结。

**继续执行：** 第六轮独立离线G0已通过，按依赖推进M3-02至M3-09，不再申请普通开发许可。本段之前的206 PASS/2 FAIL是M3-01历史基线；第六轮Pricing修复后38+15 PASS，M2普通/race各33 PASS，核心975 PASS，受影响后端2472 PASS，前端10877 PASS。跳过项、命令、退出码及限制以独立第六轮报告为准。

### 实施检查点（2026-11-05）

- M3-02仍在测试及独立审查，尚未宣布G1：真实双Store测试暴露deferred读后升级的`SQLITE_BUSY`，已在Scheduler事务入口取得writer锁；`workflow-sqlite-final`为696顶层通过、1043含子项通过/2跳过，exit0，193.303s；vet、diff-check均exit0。整包race和独立反例仍未收口，未按TTL释放占位。
- ResourceSampler普通/race定向2顶层通过；临时overlay删除读取失败后的基线清理后准确红灯。Scheduler中间结果为16顶层/18含子项通过、exit0，vet通过。进一步发现同Workflow前5个永久拒绝时后续任务饥饿，正在增加持久化CandidateOffset与任务分页；修后须重跑，不据前一版本宣布G2。
- TaskRequest纯领域校验2顶层/9含子项通过；临时overlay删除目录授权门禁后，越权目录`src-private`被错误接纳并准确触发反例（测试exit1）。首条overlay命令因PowerShell参数未展开而未执行测试，已纠正；不计为生产缺陷或有效红灯。
- 物化预算决策：新任务沿不可变父子关系共享全部祖先的原Task预算池；费用同样按后代归入原池，不能复制额度。独立Operator物化授权只允许竞争已有global/adapter槽位，不改变并发策略。父Harness未明确时拒绝物化；派生树与DAG分别遵守现有深度/分支限制；固定Task ID冲突整体回滚，不能视作幂等重放。
- 父Harness门禁已完成红绿验证：修前`未固定父适配器仍可物化: <nil>`、测试exit1；修后领域2顶层/9含子项通过、exit0。Workflow服务新入口的独立Operator身份检查1顶层通过、exit0，Store事务及HTTP尚未接通。
- 证据目录：`C:/Users/11459/AppData/Local/Temp/ao-m3-implementation-20261105`。本检查点中的中间结果均不替代阶段最终验证或独立验收。

### 最终集成检查点（2026-11-05）

- 已进入 M3 生产开发并完成主要接线；本段更新上文的历史中间状态，最终结果以 `m3-report.md` 和独立 `m3-acceptance.md` 为准。
- G1/G2、预算及 daemon 自身任期独立限定复验已通过，报告分别为 `m3-g1-review.md`、`m3-budget-review.md`、`m3-daemon-review.md`。
- HTTP 最终合法 Agent 不可借 foreign daemon 任期反例已红绿通过；HTTP 全包897顶层通过/14原环境跳过，受影响权限/Workflow race98顶层通过；daemon全包129顶层通过。
- 完整 SQLite 第二轮通过（`sqlite-all-02.jsonl`，exit0）；新增模式切换 Store 收尾后，完整 Store/Workflow/Scheduler/Budget、build/vet正在最终重跑。
- M3集成包含真实Manager/Git/SQLite/HTTP的3/10/11、资源、三级预算、UNKNOWN、审批物化，以及独立进程竞争唯一Claim与重启闭锁；人工Start→Bind测试与生产自动Bind→Start桥接分别验证，不混称同一时序。
- F03 工作区恢复的原反例已修复并通过，最终39个顶层M2/M3普通/race与Manager全包运行中。没有以定向通过替代这些完整门禁。

### 启动恢复收口检查点

- 上节运行已结束：M2/M3普通及race各39顶层、58含子项PASS；完整Manager/Execution、Store/Workflow/Scheduler/Budget和build/vet通过，均属于启动P1修复前基线。
- 独立预审使用真实SQLite/Manager/Scheduler复现启动时`ReconnectOnly`被新执行准入拒绝，且缺少后台重试。该P1阻断G7，`m3-acceptance.md`尚未通过。
- 当前为现存Host实现独立观察许可：本实例Fence、原Owner及Admission/Binding快照事务CAS；只重附着，不创建Provider，不修改Attempt或预算。启动未恢复的Fence错误进入原有后台有界重试。
- 修复冻结后重跑受影响完整套件，并交独立验收者复验；不得将此前PASS直接当作该修复的验收。

### 原线程接替检查点（2026-11-05）

- 原线程因会话数据解密错误中断；当前分支与 HEAD 不变，接替入场的 6007 个文件摘要保存于证据根 `takeover-final/takeover-entry.json`，保留全部既有修改。
- 启动测试夹具补齐真实 Chat Service 的 `NewID` 与 Reader 装配。新增 M2 无 Admission Workflow 用例修前因 `trusted admission evidence is required` 失败；修后6顶层/10含子项普通/race通过。独立启动反例及正式用例7顶层/11含子项普通/race通过，真实maintain依赖也通过。装配/编译失败日志保留但不计产品红灯。
- 接替实跑完整 Chat 包：710 顶层 / 1113 含子项 PASS，0 FAIL/SKIP，exit 0；前端 typecheck exit 0。API 重新生成 exit 0，两个产物与接替入口 SHA256 完全一致。
- SQLC 三次网络/元数据失败后按规则停止并报告。用户明确批准使用本机现有代理后，仅进程级启用代理与可访问的 `goproxy.cn`，最终 `npm run sqlc` exit0、源码无漂移；没有改配置或依赖版本，网络缺口已解除。
- 公平集成错误地假定策略提高后立即得到容量10；已用真实资源滞回恢复并固定最不利游标，断言增强为high=5/low=1，生产调度算法未改。
- 完整race及独立容量测试曾返回409 `SCHEDULER_FENCE_STALE`；独立受控反例确定HTTP硬编码真实时钟与fixture时钟混用。Controller/API接入同源Clock，生产默认仍真实时间；正式历史时钟容量链路和独立到期拒绝通过，修后完整普通/race各39顶层/58含子项PASS，HTTP/daemon 1026顶层PASS，独立M3六项全过；G7最终判定限定Windows离线PASS。
