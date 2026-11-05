# M3-01 架构审查、准入阻断与基线冻结

日期：2026-11-05（Asia/Shanghai）。工作区：`E:\code\agent-orchestrator`。

**结论：G0 BLOCKED；只交付 M3-01，不进入 M3-02 生产编码。** 最新正式独立结论仍为 M2 NOT ACCEPTED。仓库未发现第六轮或更新的独立 PASS，也未发现用户明确批准的 M2 风险豁免。用户本次委派明确允许先审查、冻结、出报告，不能把“进入M3”解释为豁免。

本报告是当前源码的架构审查与有限实跑基线，不是 M2 第六轮独立验收，不是 M3 开发完成或最终 PASS。M0-05 未批准；本轮没有调用真实 Codex、Claude、OMP、DeepSeek Provider，没有使用真实用户AO数据，没有提交、推送、合并或部署。

## 1. 输入与准入判断

依据用户附加委派提示词、AGENTS/CONTRIBUTING、`requirements.md`、`design.md`、M1验收、M2设计/计划/第五轮验收与修复、最新`m2-regression-repair.md`/`m2-handoff.md`，以及M3专项设计/计划。专项M3设计优先；本报告列明的源码差异是后续接口冻结补充，原有未提交`m3-design.md`保持原字节。

| 门禁 | 查证事实 | 判断 |
| --- | --- | --- |
| 分支/HEAD | `feat/multi-agent-desktop`；`8b7d7901b52df1964babe233df3a4e27e7523c47` | 已核实；不是提示词中的M1 HEAD |
| M2正式独立验收 | `m2-acceptance-round5.md:7`为NOT ACCEPTED | 未通过 |
| 第六轮 | `m2-handoff.md:16`明确暂缓；文件枚举无后续独立报告 | 未完成 |
| 风险豁免 | 本次对话及仓库记录无明确豁免；handoff明确提交不授权M3 | 未取得 |
| 完整回归修复 | 最新实施报告及原始日志显示指定回归已修复，详见§7 | 有实施证据，不等于独立PASS |
| M0-05 | 真实Provider预算未批准 | 继续禁止 |
| 本轮可执行范围 | 提示词第三/五/十七节允许准入前完成架构、ADR和基线 | 完成本报告后停止生产编码 |

本次未自动恢复用户暂缓的第六轮完整验收。解除G0需新的独立安全/执行链PASS，或用户对未验范围作出明确风险豁免；两者都不自动批准真实Provider预算。

### F-01/F-02/F-03证据分层

| 缺陷 | 历史独立结论 | 当前实施与本轮证据 | 尚缺 |
| --- | --- | --- | --- |
| F-01 历史BaseSHA | 第五轮独立真实双Git已关闭 | 本轮`TestM2SessionManagerBindHonorsPinnedBaseSHA`通过 | 不扩张为真实Provider验收 |
| F-02 正式Operator链 | 第五轮离线链路受限通过 | 本轮`TestM2FormalCLIThroughReopenedSQLite`通过，含真实CLI/SQLite/Git与Fake consent | 原生Windows Hello人工复验未执行 |
| F-03 单仓Worktree | 第五轮独立发现P1，未通过 | 原始两项反例在本轮Execution整包通过；5个F03真实Git/Manager/SQLite/HTTP集成顶层用例通过 | 正式第六轮独立关闭仍缺失 |

不能把原第五轮F-03仍存在视为当前已复现事实，也不能以本轮7个集成测试代替第六轮全部要求。

## 2. Git与环境冻结

入场仅有三个文档差异：`docs/customization/README.md`修改，`m3-design.md`和`m3-plan.md`未跟踪。M2源码已在当前HEAD提交，未见待保护的M2源码dirty差异。

证据目录：`C:\Users\11459\AppData\Local\Temp\ao-m3-01-20261105-8b7d790`。`entry-files.json`记录5874个Git已跟踪/非忽略未跟踪文件SHA256，另保存入场status、binary diff和三个文档原始副本。它不是完整磁盘备份，不包含Git忽略文件。

| 入场文件 | SHA256 |
| --- | --- |
| `docs/customization/README.md` | `c709a7565ed7c19501c253e87b3c7573ebe6507665b3863e9f21d27509e26f02` |
| `docs/customization/m3-design.md` | `90269273a276f11c328dbf4d3c1dc24a802b4e5faa7ff120185c96c7b9437ae8` |
| `docs/customization/m3-plan.md` | `6a449507c6c70eadc8eacf0547293df8a8cf91f06b25df5c74bec7b9e318a692` |

本轮只更新计划的准入/进度信息，并新增本报告、四份ADR和任务CSV；不改原README或M3设计。复用当前分支，不创建worktree，不stash/reset/clean。所有子代理只读，主控独占文档写入；初始三路探查中两路及后续执行探查遇模型限流，已由替代只读探查与主控源码抽查补足，不把失败代理任务算作验证通过。

实测环境：Windows 11 build 26200、Go 1.27.1 windows/amd64、CGO_ENABLED=1、Git 2.45.1.windows.1、Node 22.20.0、npm 10.9.3、RTK 0.42.4。PowerShell非登录模式，内部调用使用`-NoProfile`。总体设计M0列出Node24；本机是22，未为本轮文档审查改动工具链，也未将它宣称为Node24/全CI一致环境。

## 3. 实际接口映射与缺口

下列路径均相对`backend/internal/`。行号指当前冻结HEAD；“待新增”不是已有可调用接口。

| 边界 | 当前源码与符号 | M3必须衔接的工作 |
| --- | --- | --- |
| 领域状态 | `domain/workflow.go`；`workflow_graph.go` | 保留M1状态机；调度流水、预算状态单独建模，不能照搬总设计中的M4 VERIFYING/INTEGRATING状态 |
| DAG/Claim | `service/workflow/service.go:22`的Store接口、`:269`图校验、`:525` Claim | 仓储接口目前在service，不是假定存在`ports/workflow.go`；扩展统一Admission契约 |
| 原子领取 | `storage/sqlite/store/workflow_lifecycle_store.go:92`；`queries/workflow.sql:129` | 已有Task CAS/Attempt/幂等事务；加入槽位、三层预算、policy/epoch校验，不能另开预留事务 |
| 活跃唯一性 | `migrations/20261103044048260_workflow_core.sql:121` | claimed/running/settling/unknown均占用；不能按租约TTL删除UNKNOWN |
| 生产Claim入口 | `httpd/controllers/workflows.go:96,:255,:265` | 现有HTTP Claim不在M2审批组；共享Service/Store必须强制准入，不能只在新Scheduler外围限制 |
| CLI/MCP | `cli/workflow.go:14`；`cli/mcp_workflow.go:276` | 目前只有Operator和5个协作工具，没有Workflow CLI/MCP Claim；后续所有调用仍走统一入口 |
| 绑定/执行 | `service/execution/coordinator.go:247,:317,:384,:454` | 复用SpawnKey、BindAttempt及binding CAS；新增epoch执行许可，不重新实现Spawn |
| 结果不确定 | 同文件`:966` Reconcile，`:995` FindSpawn；`session_manager/manager.go:1146,:1624` | 唯一Session行和持久化请求键可去重；查不到不代表肯定没运行，UNKNOWN不自动再开Attempt |
| 工作区/代际 | `session_manager/multi_repo_workspace.go:537` ObserveSessionWorktrees；`service/execution/local_gateway.go`；`ports/execution.go:121` | 每次执行前复核真实Git、Repo集合与generation；不能把历史CreationSHA当live HEAD |
| 能力门禁 | `service/capability/resolver.go:143,:247` Resolve/Gate；`daemon/execution_wiring.go:195` | 使用当前可信能力/认证/到期证据；静态Harness名、未确认费用不构成许可 |
| 恢复顺序 | `daemon/daemon.go:302,:640,:650`；`workflow_wiring.go:52`；`execution_recovery.go:178` | 当前先M1过期恢复，再装配M2并同步binding sweep；新Scheduler必须在有界对账完成后开放准入，多个周期扫掠统一协调 |
| 租约/MCP | `daemon/workflow_wiring.go:26`；`session_manager/workflow_context.go:60`；`service/principal/verifier.go:95` | 默认15m，无持续续租；补内部Worker/Attempt/Token/generation/epoch/revision条件更新；Agent/MCP不外发LeaseToken的替代能力凭据及迁移方案尚未冻结 |
| Operator | `daemon/workflow_auth_wiring.go:44`；`service/principal/operator_approval.go:108,:746`；`controllers/workflows.go:817` | 新写操作加入审批闭集及精确原始请求指纹；不把loopback当Operator、不把旧批准票据重用 |
| TaskRequest | `service/collaboration/coordinator.go:444,:520`；`store/workflow_collaboration_store.go:321`；`queries/workflow_collaboration.sql:82` | 现有任意JSON申请，批准仅pending物化；新执行schema、同事务图revision/SHA/权限复核、一次性物化结果均待实现 |
| 用量/定价 | `domain/usage.go:181`；`store/usage_store.go:367`；`pricing/runtime.go:273,:533` | 复用nano-USD及canonical事件；新增可追踪归因/修正投影，不复制Collector或误用浮点展示费用 |
| Windows资源 | `procmem/system_windows.go:50`；`service/usage/memory.go:159` | 已有CPU/RAM采样；薄适配补样本时间/可信性，不重新绑定原生API |

### 对M3设计的明确修正

1. `m3-design.md`中的M1 HEAD、大量M2未提交差异已过时，以当前HEAD与三文档dirty基线为准。
2. 金额沿用**nano-USD**，不用另建micro单位再逐事件截断；Usage已有定点实现，不能写成“从float账本改造”。
3. 去重键不是裸`source_event_key`，也不应无条件追加artifact generation；引用canonical event ID或`(binding_id,source_event_key)`。
4. “旧HTTP/CLI Claim”改为“现有HTTP Claim及未来所有Claim调用”；没有凭空新增CLI实现任务。
5. Windows资源API、MemoryReader已经存在；新增的是Scheduler所需可信样本边界。
6. TaskRequest的结构化JSON不等于严格可执行Task schema；原Decide的workflow revision检查在事务外，物化必须重新在事务内验证。
7. Session去重不等于epoch栅栏；仅Admission内查epoch没有覆盖旧Leader已赢binding CAS、尚未Spawn的窗口。
8. 全局10若包括手工启动Session，必须同时约束其资源预留入口；仅库存快照或Scheduler计数不足以保证并发上限。
9. 提示词已禁止向Agent/MCP输出LeaseToken，现有M2凭据通道不满足该目标；见ADR-04。替代能力凭据及运行中MCP迁移/失效方案尚未冻结，额外阻断M3-05/07；不能把旧通道保留作为已批准目标。

## 4. 待新增或扩展的最小Ports

| 契约 | 位置/所有者 | 输入输出及不变量 |
| --- | --- | --- |
| ResourceSampler | 拟`ports/scheduler.go`，适配复用procmem | 带采样时刻、CPU/RAM有效性；首次/失败/过期不是0负载 |
| SessionInventory | Scheduler读取既有Session/Binding事实 | 按Session/Attempt身份去重，未知持有占位；不能仅凭缺进程判死亡 |
| AdmissionStore | SQLite所有，扩展现有workflow.Store共享路径 | ReserveAndClaim、Leader续期/接管、执行许可、可信Attempt续租、结算释放；CAS和预算/槽位在一个事务 |
| UsageReader/归因读取 | 复用Usage存储的事件读取 | 稳定事件ID及修订，不只SummaryReader或自增ID尾游标 |
| 受控物化入口 | Workflow Service及其Store扩展 | 申请ID/SHA、当前图revision、Operator审批上下文；返回一次性结果和新revision |

复用`ports.ExecutionGateway`、Capability Resolver、现有Clock注入及Pricing接口；不再抽象第二套SessionManager/执行器。初始架构不创建新的Provider客户端或远程Worker实现。

## 5. 数据库变更计划（尚未生成迁移）

| 新表/扩展 | 最小约束与用途 |
| --- | --- |
| scheduler_leader | 单行owner/epoch/lease/revision，持久轮转游标；接管单调递增epoch |
| scheduler_policy | 作用域/ID唯一，限额与配置revision、审计引用 |
| scheduler_workflow_state | Workflow唯一，持久deficit/暂停状态；运行状态不混写政策revision |
| scheduler_admissions | 稳定资源owner唯一，attempt_id/session_id可空且各自唯一；兼容Workflow预留和无Attempt的手动Session；epoch、全局/Adapter占位、reserved/active/uncertain/released、执行许可及稳定请求引用 |
| scheduler_retry_due | workflow/task唯一，next_eligible_at、原因与policy版本 |
| budget_policies | scope/ID/currency/period唯一，observe/soft/hard_admission、limit_nanos/revision |
| budget_reservations | attempt+budget_policy+窗口唯一，amount_nanos、状态/revision；不能只有attempt+scope而丢窗口归属 |
| workflow_usage_attributions | canonical事件唯一归因，Session/Attempt/Task/Workflow、质量/纳美元/价格版本；修正有独立幂等键和原事件引用 |
| scheduler_decisions | 决策、拒绝原因、policy/epoch、时间及非秘密摘要；不放LeaseToken |
| workflow_task_request_materializations | request_id唯一、payload SHA/批准范围、创建Task结果与图revision；重放返回同一结果 |

一个writer事务完成：校验leader/policy/图状态 → 复核资源/预算证据及占用 → 槽位和预算预留 → 原Task CAS/Attempt/幂等记录 → 审计提交。失败整体回滚；提交后才请求M2执行。拒绝原因使用明确事务/审计语义，不能把回滚掉的决策冒称已持久化。

M3-02独占Store/queries/migrations写入；按`npm run migration:create -- <snake_case_name>`追加Goose迁移及`shippedMigrations`，运行sqlc，不手改gen。新增变更通知由DB trigger写change_log，扩展CDC白名单/脱敏类型；不得复制Store手工CDC。M3-06涉及相同Store时串行。金额、revision、时间和索引约束需真实SQLite竞争、回滚、up/down/up测试。

## 6. ADR与执行状态

- [ADR-01：Windows资源与保守准入](m3-adrs/01-windows-resources.md)：复用采样，UNKNOWN新增容量0，75/85与RAM阈值、连续3样本滞回、参数有效范围。
- [ADR-02：纳美元与预算账本](m3-adrs/02-budget-ledger.md)：复用定点费用和canonical事件，补修正账本，硬限制仅承诺准入。
- [ADR-03：Leader与Epoch](m3-adrs/03-leader-epoch.md)：持久Leader及执行许可，SQLite到OS副作用窗口保守恢复，不靠TTL重放。
- [ADR-04：续租与Operator](m3-adrs/04-lease-operator.md)：15m/60s可信续租、审批与TaskRequest事务边界；无LeaseToken外发的MCP替代凭据迁移方案未冻结，M3-05/07保持阻断。

拟定调度闭环仍是`READY → Admission/Claim → M2 Bind/Spawn → Observe → Running → Settle`。不确定转UNKNOWN/uncertain且继续占位；仅有safe-to-retry证据才重新准入。M1业务状态、M2绑定状态、调度流水各司其职。ADR是后续实现约束，不是已有运行能力。

## 7. 测试基线与真实运行结果

### 本轮新执行

下列命令均在`E:\code\agent-orchestrator\backend`执行，通过`rtk proxy`包装的非登录进程调用；Go JSON原始输出与进程退出码分开保留。数量分别列顶层与包含子测试终态事件，不把两者相加。

| 实际命令 | 退出码与实际结果 | 证据目录内文件 |
| --- | --- | --- |
| `go test ./internal/service/workflow ./internal/service/execution -count=1 -json -timeout 10m` | **0**；136顶层PASS（workflow14/execution122），含子项255 PASS，0 FAIL/0 SKIP；外层5.263s | `core.jsonl`、`core.summary.json` |
| `go test ./internal/integration -run '^(TestM2F03.*\|TestM2SessionManagerBindHonorsPinnedBaseSHA\|TestM2FormalCLIThroughReopenedSQLite)$' -count=1 -json -timeout 15m` | **0**；7顶层/26含子项PASS，0 FAIL/0 SKIP；包211.793s，外层216.344s | `f01-f02-f03.jsonl`、对应summary |
| `go test ./internal/procmem ./internal/pricing -count=1 -json -timeout 5m` | **1，FAIL**；procmem 23 PASS；pricing 34 PASS/2 FAIL；合计57顶层PASS/2 FAIL、含子项90 PASS/2 FAIL，0 SKIP | `resources-pricing.jsonl`、对应summary |
| `go test ./internal/service/usage -run '^TestSystemMemory' -count=1 -json -timeout 5m` | **0**；6 PASS，0 FAIL/0 SKIP；包1.969s | `memory-reader.jsonl`、对应summary |

合计：**206顶层PASS、2 FAIL、0 SKIP**（含子项口径377 PASS、2 FAIL）。测试集合不是全M2/全M3，更不是全仓CI。没有新增测试文件或放宽现有断言。

Pricing失败原文：

```text
TestCacheInstallsPrivateManifestLastAndLoadsLKG
fetch_cache_test.go:217: .../pricing/catalog/v1/manifest.json mode = 666, want 600
TestCacheInstallRepairsCorruptProviderBlob
fetch_cache_test.go:363: repaired blob mode = -rw-rw-rw-, <nil>
```

归因证据：`m2-baseline-windows.md:517-518`记录同名同断言；`git diff a6c284a HEAD -- backend/internal/pricing/cache.go backend/internal/pricing/fetch_cache_test.go`输出为空，exit0。当前源码用`os.Chmod(0600/0700)`，测试要求POSIX mode；本轮Windows再次出现0666。它们是已复现的既有Windows权限语义失败，不是本轮M3源码回归。**这不证明Windows DACL满足“私有缓存”要求**；后续必须明确并验证实际安全边界，不能只删断言。当前保持未修复，Pricing完整包不能标PASS。

另行Windows API只读探针也以exit0取得CPU 12.402%、可用RAM 11103887360/34067255296字节（32.594%）；`windows-resource-probe.json`记录结果。它只证明本机API可读，生产采样实现的证据以procmem已有测试为准；不证明M3阈值、扩缩容或3/10并发已生效。

### 原有回归记录的实际边界

已读取`C:\Users\11459\AppData\Local\Temp\ao-regression-repair-20261105`的原始日志尾和部分JSON退出码：

| 原有检查 | 记录结果 | 本轮是否重跑 |
| --- | --- | --- |
| 全TestM2普通/race | 各33 PASS，exit0，539.503s/658.859s | 否；只重跑上述7个关键集成 |
| 完整前端 | 633文件，10877 PASS/17原有SKIP，exit0 | 否 |
| Manager/Controller/Daemon/核心SQLite | 最新相应日志exit0 | 否；仅Workflow/Execution整包本轮重跑 |
| Build/Vet/Typecheck/API/sqlc | 最新报告与已有退出码记录通过 | 否；本轮未改源码/契约，不生成产物 |
| 全仓go test/CI | 当前HEAD无完整通过证据；pre-M2历史为56包/267失败 | 否；历史计数不当作当前失败数 |

第五轮的37个前端失败文件、192项失败及Manager等失败，已被后续`m2-regression-repair.md`逐项归因并在指定完整套件复验，不能继续列为当前未归因192项。全仓范围仍未独立重建；本轮新选择集中未归因失败为0，Pricing两项已定位但未修复。不得从这句话推出全仓无未知回归。

## 8. 交付、阻断与恢复入口

本轮文件：本报告、`m3-adrs/`四份ADR、更新`m3-plan.md`、`issues/2026-11-05_14-16-00-m3.csv`。临时日志在系统Temp，不加入源码仓库；若需长期审计应归档该证据目录。CSV按mission流程保留任务与blocked原因；用户禁止提交覆盖技能默认提交行为，所有git_state保持未提交。

| 项目 | 状态 |
| --- | --- |
| M3-01 审查与基线 | COMPLETED（审查交付）：报告/四份ADR/基线落盘，独立文档复核完成；ADR-04替代凭据迁移仍是后续设计阻断 |
| M3-02～M3-09 | BLOCKED：G0未通过，未修改生产业务代码；M3-05/07另受ADR-04设计阻断 |
| M3总体验收 | 未开始；没有生成冒充通过的m3-acceptance.md |

待解除项：

1. **G0**：正式第六轮独立安全/执行链PASS或明确风险豁免；建议先恢复独立复验。
2. **已知红基线**：Pricing两项Windows权限失败，当前完整Pricing测试失败；真正DACL安全性未在本轮验证。
3. **后续设计阻断/验证约束**：MCP替代能力凭据及运行中迁移方案尚未冻结，阻断M3-05/07；手动Session统一计槽、旧epoch到OS启动窗口、用量回填修正消费均须在相应M3实现中闭合。
4. **始终未授权**：真实Provider/M0-05、真实用户数据、配置修改、commit/push/merge/deploy。

独立文档复核发现ADR-04原先把保留旧MCP凭据通道误写成冻结目标（P1）。已改为用户明确禁发LeaseToken、替代能力凭据及运行中迁移尚未冻结，并同步M3-05/07阻断；复核者只读重审后确认该文案P1关闭，结论仅为“M3-01文档可交付”。复核还直接重算四组JSON原始日志，确认206/2/0及377/2统计，未冒称重跑测试。过程见 `issues/2026-11-05_14-16-00-m3.review.md`。

最终文件保护与文档核验命令为`rtk proxy python C:/Users/11459/AppData/Local/Temp/ao-m3-01-20261105-8b7d790/verify-artifacts.py`；该脚本同时执行`git diff --check`并保存`final-verification.json`。核验范围为入场5874文件、未跟踪交付文档/CSV、链接和任务状态；基线测试失败仍保留，不由文档核验改写。

G0解除后从冻结HEAD和文档差异重新确认现场，先完成M3-02统一事务，再并行M3-03/04；M3-05/06/07按依赖集成。M3-08由非实施代理实跑矩阵；最终开发报告与独立验收报告分离。没有授权时到此停止生产编码。
