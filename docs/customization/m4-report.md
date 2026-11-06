# M4 实施报告（主控交付，待统筹终验）

2026-11-06，`feat/multi-agent-desktop`，基线 `129e13ca5d8df210cdafcc2e061d521109130e27`。主 Agent 已完成 M4-01～09 的实现、独立验证和交接；整体仍等待统筹独立终验，不把本报告或 HANDOFF 直接当作最终 ACCEPTED。

## 当前收口状态

- 会话清理已结束：AgentDock Session=0；Turing、Copernicus 均为 completed；未误停有效会话，也未创建新的 AgentDock 任务。
- M4-01～07：主控阶段 ACCEPTED。M4-08：正式 HTTP/CLI/Daemon/CDC/协调摘要链已接通并通过。M4-09：主控 G4 独立验收通过，形成待统筹复核的 HANDOFF。
- 未执行真实 Provider、真实用户生产数据、真实远程 Git transport、远程 Push/Merge、部署、跨 OS、桌面/安装包和原生人工确认；M0-05 仍未批准。
- 修复链实际验证到“受限 TaskRequest 物化”；修复字节由测试模拟作者修改现有 Worktree，未运行真实 Agent 自动修复。
- Apply/Push 仍分别要求精确 Operator 批准；Push 证据使用本地 bare 仓库模拟，不代表网络 Push 已验证。partial/unknown 保留并闭锁后续写入，恢复只做对账。

### 最终验证证据

| 命令 | 实际结果 |
|---|---|
| `go test ./internal/integration -run "^TestM4ArtifactThroughRealManager$" -count=1 -timeout 20m -v` | exit 0，正式 CLI→Approval→HTTP→SQLite/Git 链 628.577s；`m408-formal-cli-chain-fixed.log/.exit` |
| `go test ./internal/integration -run "^TestM4" -count=1 -timeout 30m -v` | exit 0；加入正式 CLI 前的完整 HTTP 链 868.864s；`m408-final-chain.log/.exit` |
| `go test -race ./internal/integration -run "^TestM4" -count=1 -timeout 30m -v` | exit 0，2 个真实 Manager/SQLite 链及完整 M4 集成通过，874.484s；`m409-m4-race-final.log/.exit` |
| `go test -race ./internal/storage/sqlite/store ./internal/service/principal ./internal/cli ./internal/httpd/controllers ./internal/storage/sqlite -run "TestM4\|TestQuality\|TestClaimPR\|Test.*Approval\|TestMigrationVersionLedger" -count=1 -timeout 15m` | exit 0；Store 174.881s、Principal 3.878s、CLI 25.442s、Controllers 91.870s、迁移 55.225s；`m409-final-boundaries.log/.exit` |
| `go build ./...` / `go vet ./...` | 均 exit 0；`m409-build-final.log/.exit`、`m409-vet-final.log/.exit` |
| `npm run sqlc` / `npm run api` / `npm run frontend:typecheck` | 均 exit 0；`m409-sqlc-final`、`m409-api-null`、`m409-typecheck-null.log/.exit` |
| `go test ./internal/httpd/apispec/... -count=1` | exit 0，apispec 4.904s、specgen 39.405s；`m409-artifact-null-fixed.log/.exit`。原 `TestBuild_ArtifactFilesPreserveFrozenNull` 红证据为 `m409-artifact-null-red.log/.exit`（1） |
| `go test ./internal/integration -run "^Test(M2F03SingleRepoLiveGitRechecks\|M2F03SingleRepoHTTPThroughSQLiteReopen\|M3RealManagerSharedCapacity\|M3AgentRequestRequiresIndependentMaterialization\|M3LeaseRenewalAndRestartFence)$" -count=1 -timeout 20m -v` | exit 0，5 项 PASS，261.039s；`m409-m2-m3-regression-final.log/.exit` |
| `go test ./internal/adapters/workspace/gitworktree ./internal/adapters/testgate -run "^TestM4" -count=1 -timeout 10m -v` | exit 0，Git 177.183s、Runner 2.686s；`m409-git-runner.log/.exit` |
| `go test ./internal/httpd/... ./internal/daemon ./internal/service/pr ./internal/service/qualitygate ./internal/telemetrymeta -count=1 -timeout 10m` | exit 0；全部命名包完整运行，`m409-final-packages.log/.exit`；最终生成器小修后另跑上述 apispec 全包 |

所有 Go 命令工作目录为 `backend/`；npm 命令从仓库根执行。日志根为 `C:/Users/11459/AppData/Local/Temp/ao-m4-resume-20261106-01a1acb1/`。M4 集成 race 的原始日志另保存在 `.tmp/m409-m4-race-final.log/.exit`，两处内容相同。`git diff --check` exit 0；新增及修改 Go 文件 `gofmt -l` 无输出。新增非生成 Go 源码 44 文件/6343 行、新增测试 34 文件/3690 行（含空行），所有新测试文件不超过 300 行，未达到源码两倍上限；明细为 `m409-go-line-audit.csv`。

全量 `internal/cli` 包测试仍有既有 Windows symlink privilege 失败：`TestRenderCheckReplacesItsEarlierScreenshot` 报 `A required privilege is not held by the client`（`m408-cli-final.log/.exit`，exit 1，59.928s）；现有 render 测试未修改，M4 定向 CLI 及 race 已通过。两项历史 Domain Handoff Note 失败继续保留，不折算为 M4 通过。

### M4-08/09 实施内容与文件变更

| 文件范围（仓库相对路径） | 实际变更 |
|---|---|
| `backend/internal/domain/{delivery,quality_gate,quality_review,task_commit,integration,integration_operation,coordination}.go` | 冻结工件、质量证据、提交与逐仓操作事实；内部代际和物理路径不出 HTTP |
| `backend/internal/storage/sqlite/{migrations,queries,gen,store}/` | 六个新增迁移及登记、SQLC、Artifact/CAS/Reservation/策略/Gate/Review/Commit/集成；M3 原 writer 中接入占位、代际和未知状态恢复 |
| `backend/internal/service/{artifact,qualitygate,qualityreview,taskcommit,integration,coordination}/` | 服务编排、受限修复物化、只读摘要，复用原 Workflow/Session/Scheduler/Approval |
| `backend/internal/adapters/{testgate,workspace/gitworktree}/` | 离线真实 Runner、私有 index Snapshot、冻结导出、任务 ref CAS、隔离 Preview、本地串行 Apply/bare Push |
| `backend/internal/{daemon/quality_wiring.go,httpd/api.go,httpd/controllers/workflow_quality*.go,httpd/controllers/dto.go,cli/quality.go}` | 15 个 API/CLI 命令、可信策略装配、精确审批和错误封套、脱敏质量/协调摘要 |
| `backend/internal/{httpd/apispec/specgen,service/pr,service/principal,storage/sqlite/store/pr_store.go,telemetrymeta}/` | 生成契约、旧 PR Merge 旁路封锁、一次性 Operator 批准及 CLI 遥测登记 |
| `backend/internal/integration/m4*_test.go` 及受影响模块测试 | 真实双仓 Manager/SQLite/HTTP/CLI 链、修复物化、安全反例、并发/Partial/重启；M3 夹具补显式 scope |
| `backend/internal/httpd/apispec/openapi.yaml`、`frontend/src/api/schema.ts` | 由 `npm run api` 生成；可空字段与冻结证据表示一致 |

当前策略在 `quality-summary.currentPolicy`/`coordination-summary.quality.currentPolicy` 读回，未配置为 null；候选列表携带当前记录 revision。旧 revision 更新返回 409 `ARTIFACT_STALE`，候选或当前策略均不泄露命令/env。Artifact 从 SQLite 独立列恢复 Generation 后再校验指纹，避免 JSON 脱敏破坏重读。

Reconcile 的 expectedRevision 贯穿 HTTP、服务、Store，在 writer 内、Git 观察前再次 CAS。PR Claim 同一事务检查新旧 owner 的 Workflow binding/admission，拒绝双向接管；合法 pre-Bind、已终止身份及 SCM 观察窗口均有反例。统筹 SV-05 已独立复验 9 项 PASS（Store 4.299s、PR 0.391s）；SV-03 的两项 Store 独立复验 PASS，最终链及 SV-04 等统筹对本次冻结交付终验，执行方不代写其关闭状态。

正式 CLI 首次因夹具固定在 2026-01-01、真实 context deadline 已过期而 FAIL（`m408-formal-cli-chain`，exit 1）；改为带 1 小时余量的可控测试时钟，并让 CLI 与服务共享时钟后全链通过，没有放宽生产批准有效期。PR pre-Bind 旧夹具曾违反数据库 CHECK，现使用真实 ReserveAndClaim→CreateAdmittedSession 前置状态；红记录保留。SQLC 生成期间的编译/解析失败也保留，最终生成与回归均通过。

### 未完成与后续交接

统筹整体验收、最终提交尚未完成；执行方保留全部 dirty 交付，不提交、不 Push、不启动 M5。SV-02 沙箱内两次清理 FAIL 与完全访问环境独立 PASS（47.181s、临时根为空）并存，作为环境限制记录，未宣称代码修复。Runner 只限制命令/env/路径和进程树，不提供 OS 沙箱；SQLite/Git 不构成跨系统原子事务。M5 消费约束见 [技术交接](m4-m5-handoff.md)。

以下各节保留实施过程和历史阶段证据；当其中旧状态与本节或执行台账冲突时，以本节和最新台账为准。

- 会话核查已结束：AgentDock 命令 Session=0，两个旧 Agent 均 completed；未终止任何会话，未新建 AgentDock Task。详见 m4-session-audit.md。
- M4-01 架构阶段 ACCEPTED：主 Agent 复核真实调用链并完成四份 ADR。历史两条 Handoff Note FAIL 保留。
- M4-02 ACCEPTED：主 Agent 完成真实两仓 Git/SQLite/Manager 集成及安全反例验收；SQLite/Reservation 来自显式 Astra James（01a1acc0-292d-7f53-95fd-4068026c095a）交付并由主控独立运行 race。新版本使旧证据 stale；占位与 Claim 原子化，UNKNOWN 保留，升级恢复补齐旧 Admission，common-dir 统一不同 Project 的同仓占位。
- Snapshot 通过私有 index 构造交付树，保留用户 index 和范围外 dirty；无关 dirty 仅进入摘要，不写入 Git 对象库。真实仓库、分支、索引、文件和 scope 共同参与失效判断。
- M4 真实 Manager 集成测试首次暴露未显式固定 BaseSHA 的合法任务落库失败；存储改为从持久 Worktree 取实际 BaseSHA，后续完整复测已通过。该测试覆盖两真实 Repo、独立 SQLite 连接重读、版本失效及 dirty 保全。
- M3 容量测试夹具改为各任务不同 AllowedPaths，保留原容量/公平性断言。子任务物化测试同步保留父 scope，避免原先空 scope 隐式申请整仓权限。
- 独立 Astra 审查发现范围外已提交修改与 skip-worktree 省略文件两个 P1；主 Agent 用真实 Git 反例复现后修复，完整 Snapshot 四项复测通过。
- M4-01～06 当时已由主 Agent 验收，M4-07～09 后续按依赖完成；最新装配与验证结论见顶部。

## M4-07 实际变更与主控阶段验收

新增 IntegrationOperation/逐仓事实、唯一意图、Request revision CAS、审批消费与 writer 栅栏。Apply 正文绑定完整有序 Preview；先预检所有仓，消费已有 OS 一次性 Approval，再持久意图。每仓 prepared→requested 先落盘，最终 Git ref CAS 在 writer 内复验 epoch/generation/Gate/Review，并校验目标未被检出。两个并发请求只能创建一个意图、消费一次批准；同 ID 重放只读。

恢复在同一个数据库 writer 内观察原仓库目标并推进 operation revision，使迟到执行失去写权；新 recovered Leader 可接管只读对账。第一仓已成功而第二仓失败时保留第一仓，汇总 partial；无法确定则 unknown。partial/unknown 阻止继续提交另一集成计划，不自动回滚、不自动重试。

Push 需要不同路由的一次性批准。可信宿主可登记本地 bare 模拟目的地，默认空表即禁用；请求仅传登记 key/物理身份摘要/expected SHA。模拟使用 pack 对象传递和目标 ref CAS，不启用网络、不运行 hooks，也不允许 URL/rewrite/任意路径。此实现不声称已接入真实远程 Git transport。

| 实跑命令（backend） | 真实结果 |
|---|---|
| `go test ./internal/integration -run "^TestM4ArtifactThroughRealManager$" -count=1 -timeout 15m -v` | exit 0，328.581s；m407-integrated.log/.exit，含精确正文/无批准拒绝、双 Operator、完整 Apply；后增 Push 另行复测中 |
| `go test ./internal/integration -run "^TestM4IntegrationPartialRestart$" -count=1 -timeout 15m -v` | exit 0，325.383s；m407-partial.log/.exit，真实两仓部分失败、丢回执、恢复后不再写 |
| `go test -race ./internal/storage/sqlite/store ./internal/service/principal ./internal/storage/sqlite -run "TestM4Integration\|TestM4QualityMigrationUpgradeAndDown\|TestMigrationVersionLedger\|Test.*Approval" -count=1 -timeout 5m` | exit 0，77.513s / 4.869s / 40.012s；m407-boundaries-final.log/.exit |
| `go test -race ./internal/storage/sqlite/store -run "^TestM4IntegrationOperation" -count=1 -timeout 5m -v` | exit 0，58.060s；m407-new-leader.log/.exit，新增任期接管后只读恢复 |
| `go vet ./internal/service/integration ./internal/adapters/workspace/gitworktree ./internal/storage/sqlite/store` | exit 0；m407-vet.log/.exit |

SQLC 新迁移已通过项目命令生成并登记；m407-push-sqlc.log/.exit 为 0。当前所有源码、测试和证据保留为未提交变更，不操作本仓 refs。

最终 `go test ./internal/integration -run "^TestM4ArtifactThroughRealManager$" -count=1 -timeout 15m -v` exit 0，437.802s（m407-acceptance.log/.exit）。此轮包括新增本地 bare Push、不同 action 批准拒绝、默认禁用、并发远端 SHA 漂移时拒绝覆盖、重复请求只读，以及目标分支检出状态的最终复验。主控阶段 ACCEPTED，08 开始。

统筹 06 Preview 独立复跑有一条真实 FAIL：测试断言未报错，但 `testing.go` 清理源仓 `.git/objects` 时 Access denied，原证据不改。主控核查、同盘单次及连续三次复跑见 [清理调查](m4-preview-cleanup-investigation.md)；目前未复现，原运行时原因未确定，不宣称已修复，也不把后续 PASS 改写成独立旧记录 PASS。

## M4-05 实际变更与验收

新增 TaskCommit domain/Store/query/migration/service 与受限 Git 提交原语。确定作者和 Artifact 时间生成唯一对象；持久 requested 后才执行 Git，目标 SHA 验证和任务 ref CAS 同属一个 Git ref 事务。私有树提交不覆盖用户 index/范围外 dirty，不运行 hook，签名要求无可信实现则 BLOCKED。最终 ref 写入在 SQLite writer 内复验 epoch/generation 和全部证据，并以剩余租约、Gate 有效期与 5 秒上限约束执行。

校验方法不会创建缺失意图；提交待确认期间政策、新工件和迟到审查回执均拒绝变更。回执丢失后同 ID 仅核对精确提交字节，新 ID 不获重复写权限。新 recovered Leader 可记录只读对账结果，但保留原执行任期。SQLite/Git 不构成跨系统原子事务；同用户任意外部 Git 程序不在 OS 隔离承诺内。

| 实跑命令（backend） | 真实结果 |
|---|---|
| `go test -race ./internal/storage/sqlite/store -run "^TestM4(TaskCommit\|Review)" -count=1 -timeout 5m -v` | exit 0，64.736s；m405-store-race-final.log/.exit |
| `go test ./internal/adapters/workspace/gitworktree ./internal/integration -run "^TestM4(TaskCommit\|ArtifactThroughRealManager)" -count=1 -timeout 10m -v` | exit 0，Git 37.074s、真实链 146.338s；m405-git-integration-final.log/.exit；含回执丢失恢复 |

主控独立源码审查与以上实跑通过，阶段 ACCEPTED；HTTP/CLI 装配等待 08。最初 Store race exit 0（56.013s）与中间 Git/集成 exit 0（44.050s / 223.401s）保留，最终结论使用最后一轮代码与日志。

## M4-03/04 实际变更与验收

补充阶段进度（历史实施记录）：M4-06 已由主控独立验收，随后 M4-07～09 已按依赖完成。新增 `domain/integration.go`、`service/integration`、`gitworktree/integration_preview.go`、Preview Store/query/migration 与真实两仓证据。专用 detached Worktree 内 merge-tree，不更改目标 ref、不使用 reset/clean，非 force 移除临时 Worktree。生成确定的两父候选提交与 diff digest，持久计划绑定全部仓库和质量策略；预演结束再次核对全部仓库。任何缺仓/未提交/过期证据/漂移均拒绝，不从局部成功推断整体成功。

06 实跑（backend）：`go test ./internal/adapters/workspace/gitworktree ./internal/service/integration -run "^TestM4IntegrationPreview" -count=1 -timeout 5m -v` exit 0，Git 44.862s（service 无直接测试）；`go test -race ./internal/storage/sqlite/store -run "^TestM4IntegrationPlan" -count=1 -timeout 5m -v` exit 0，64.039s；`go test ./internal/integration ./internal/storage/sqlite/store ./internal/storage/sqlite -run "^Test(M4ArtifactThroughRealManager|M4QualityMigrationUpgradeAndDown|MigrationVersionLedger)$" -count=1 -timeout 10m -v` exit 0，296.821s/7.238s/2.454s。最终新增复验后的 `go test ./internal/integration -run "^TestM4ArtifactThroughRealManager$" -count=1 -timeout 10m -v` exit 0，260.918s。对应 m406-preview-01、m406-store-race、m406-integrated-01、m406-integrated-final.log/.exit。

`go vet ./internal/service/integration ./internal/service/taskcommit ./internal/service/qualitygate ./internal/adapters/workspace/gitworktree ./internal/storage/sqlite/store` exit 0；`npm run sqlc`、`git diff --check` exit 0，m406-vet/m406-sqlc/m406-diff-check 证据。SQLC 首次因 Windows models.go 文件映射占用失败 exit 1，重试通过，未手改 gen。一次 gofmt 工作目录重复写 backend 前缀而失败，随后正确路径格式化通过。中途草稿未执行的 reset 清理已替换为无索引修改的 merge-tree；本轮没有执行 reset/clean。

新增 `domain/quality_gate.go`、`domain/quality_review.go`、`adapters/testgate`、`service/qualitygate`、`service/qualityreview`、冻结树导出/验证，以及 SQLite 策略、Gate、Review 查询/Store/迁移。复用 M3 Claim writer、执行身份及 M2 消息/任务请求；没有新增 Scheduler、DAG 或消息系统。

服务端固定策略只允许已审核 executable/argv/env；Runner 采用真实退出码、完整输出摘要、有界日志及进程树取消。Gate 前后重验工件、工具及冻结树，RUNNING/UNVERIFIED 跨 Run ID 闭锁；停止无法确认时保留临时树和 HOME。此 Runner 不是 OS 沙箱。

Review 从可信 principal 派生独立身份，绑定 Artifact/策略/target SHA，writer 重验 Reviewer 当前 Session generation、binding 状态与任期；批准必须有有效 Gate。修复通过既有 pending TaskRequest 及 handoff，默认两轮、最多三轮，同一 Attempt/ArtifactRevision 不重复创建修复请求。前驱为 M1 succeeded 也不能绕过质量门禁；Review 类型后继只要求 Gate，避免等待自己的批准。

统筹 SV-01 指出的路径别名误跳过已修复：WhenPaths 与 Artifact path 使用规范组件和 Windows 大小写语义，`src-other` 不匹配 `src`。主控 Runner/Store 反例通过，统筹另行独立复测关闭该缺陷。

| 实跑命令（backend 工作目录） | 实际结果与日志 |
|---|---|
| `go test ./internal/adapters/testgate ./internal/storage/sqlite/store ./internal/service/qualitygate ./internal/service/qualityreview -run "^TestM4(Gate\|Review\|Quality\|Collaboration\|TaskRequest)" -count=1 -timeout 5m -v` | exit 0；Store 6.083s；m403-04-unit-05.log/.exit；两个 service 包无自身测试，服务验证见真实集成 |
| `go test -race ./internal/adapters/testgate ./internal/storage/sqlite/store -run "^TestM4(Gate\|Review\|QualityAdmission\|Collaboration\|TaskRequest)" -count=1 -timeout 10m -v` | exit 0；Runner 4.751s，Store 69.946s；m403-04-race.log/.exit；包含独立连接竞争唯一 Gate 执行权 |
| `go test ./internal/integration ./internal/adapters/workspace/gitworktree -run "^TestM4(ArtifactThroughRealManager\|MaterializeFrozenDeliveryTree)$" -count=1 -timeout 10m -v` | exit 0；85.081s / 8.626s；m403-04-integrated-final.log/.exit |
| `go test ./internal/storage/sqlite -run "^TestMigrationVersionLedger$" -count=1 -v` | exit 0；1.928s；主控工具回执 |
| `npm run sqlc`、`git diff --check` | exit 0；生成无无关语义差异，只有原新增质量模型；新增测试文件均未超过 300 行 |

失败记录保留：Review SQL 参数编号错误和 SQLC 列名歧义已经修正；Claim 反例首次证明 Review 循环依赖（m403-04-admission-red，exit 1）；修复后测试作者仍占容量导致夹具失败（unit-03，exit 1），改为可信停止/释放后通过。一次 PowerShell 新文件命令解析失败，未写入或执行测试，不算业务验证。

M4-03/04 的原生 OS 人工 UI、真实 Provider、其他 OS 未测试；人工批准使用测试 stub，外部审批协议仍须 08 接线验证。Astra 只读审查被其工具守卫拒绝，未产生代码审查结论；主 Agent 已自行审查并运行上述关键证据。

## 已有实际验证

证据根：`C:/Users/11459/AppData/Local/Temp/ao-m4-resume-20261106-01a1acb1/`。

| 命令 | 实际结果 |
|---|---|
| M4-01 的四项 M3 Store 契约定向测试（完整命令见架构审查） | exit 0，4 PASS，m401-contracts.log/.exit |
| 两项历史 Domain Handoff Note 定向测试 | exit 1，2 FAIL，m401-baseline.log/.exit |
| `go test ./internal/adapters/workspace/gitworktree -run "^TestM4Snapshot" -count=1 -v` | exit 0，3 PASS，m402-git-final.log/.exit；Windows junction 反例实际执行 |
| 同一 Snapshot 命令，加入独立审查反例后 | exit 0，4 PASS，m402-git-reviewed-02.log/.exit；修复前两个反例均 FAIL（m402-hidden-red.log/.exit，exit 1） |
| `go test ./internal/integration -run "^TestM4(Artifact\|Reservation)" -count=1 -timeout 10m -v` | 首轮 exit 1，Artifact FAIL、Reservation PASS；m402-integration-01.log/.exit |
| `go test ./internal/integration -run "^TestM3" -count=1 -timeout 20m -v` | 首轮 exit 1，5 PASS、物化 scope 测试 FAIL；m402-m3-regression.log/.exit；修正测试前提后复测中 |
| `go test ./internal/integration -run "^Test(M4\|M3AgentRequestRequiresIndependentMaterialization\|M2F03)" -count=1 -timeout 15m -v` | exit 0，完整匹配测试 PASS，291.921s；m402-integrated-final.log/.exit |
| `go test -race ./internal/storage/sqlite/store -run "^TestM4" -count=1 -timeout 10m -v` | exit 0，全部 PASS，88.153s；m402-store-race.log/.exit，包含迁移 up/down/upgrade、CAS、两 Store 竞争、代际/UNKNOWN/恢复 |
| `go test ./internal/storage/sqlite -run "^TestMigrationVersionLedger$" -count=1 -v` | exit 0，PASS；主 Agent 工具输出，0.267s |
| `go test ./internal/service/artifact -count=1` | exit 0，`[no test files]`，只证明编译，不作为服务验收 |
| `git diff --check` | exit 0；仅 Git LF/CRLF 提示 |

早期文件 symlink 创建因 Windows 权限被拒绝；随后用目录 junction 验证实际逃逸路径并通过，不声称拥有文件 symlink 创建权限。一次 Snapshot 摘要优化因局部变量遮蔽 hash 包编译失败，已更名并复跑上表三项测试通过。

审查修复首次使用 `git config --worktree` 时，未启用 worktreeConfig 的 linked Worktree 被 Git exit 128 拒绝；已改为仅在该扩展开启时读取 Worktree 配置，随后 Snapshot 四项通过。一次 M2F03 回归遇到存储修改与 SQLC 生成之间的中间态，编译报 `RepoPath undefined`；生成结束后已安排完整重跑，不能把该次标作通过。

未进行真实 Provider、用户生产数据、真实远端 Push/Merge、部署或本仓 Commit；M0-05 仍未批准。未运行完整跨 OS/桌面/安装包验收。
