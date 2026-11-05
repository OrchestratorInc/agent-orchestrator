# M3 本轮独立复验与 M2 回顾（2026-11-05）

> 项目：`E:\code\agent-orchestrator`；分支：`feat/multi-agent-desktop`；本轮读到的 HEAD：`8b7d790`。
>
> **本报告是本次对当前工作树的再次独立抽样复核；不覆盖 `m3-acceptance.md` 或 `m2-acceptance-round6.md`。M3 的最终判定以本轮 Race 结果和既有完整证据限定范围合并判断；M2 是离线 G0 PASS，而非真实 Provider/安装包验收。**

## 1. 来源与验收边界

直接阅读当前 `m3-acceptance.md`、`m3-report.md`、`m2-acceptance-round6.md`，并抽查：
- `service/scheduler/service.go`、`planner.go`、`resources.go`；
- `service/budget/service.go`、`storage/sqlite/store/scheduler_admission_store.go`、`scheduler_admission_checks.go`、`scheduler_leader_store.go`、`budget_usage_store.go`；
- `service/execution/permit.go`、`session_manager/admission.go`、`daemon/scheduler_runtime.go`、`httpd/controllers/scheduler.go`、`service/workflow/service.go`。

确认生产逻辑中资源容量、预算预留、Attempt Claim 在 `ReserveAndClaim` 的 SQLite 写事务内完成；leader epoch 和 recovered 检查不依赖前端传参；预算未知与无法归因时硬限制返回错误，`UNKNOWN` 不视为零；保留的资源占位不凭 Lease TTL 释放。Controller 的预算/策略写路由通过 Operator 写授权，HTTP 不暴露续租能力。以上是代码抽查，不代表所有调用点和 OS 安全边界的形式化证明。

## 2. 本轮直接运行的命令与真实结果

均由本轮独立验收在当前代码中发起、使用隔离测试环境；无真实 Provider、用户真实 AO 数据和外部部署。Go 命令执行目录均为 `backend/`。

| 命令 | 本轮退出码 | 结果 |
| --- | --- | --- |
| `go test ./internal/integration -run '^TestM3' -count=1 -v -timeout 20m` | **0** | 六个 M3 集成顶层用例 PASS：共享容量、资源/三级预算、TaskRequest 独立物化、独立进程唯一 Claim、租约/重启 Fence、争抢/公平轮转 |
| `go test ./internal/service/execution -run '^(TestIndependentM2SingleRepoWorktreeRequiresLiveVerification\|TestM2F03MissingExpectedRepoClosesGate)$' -count=1 -v -timeout 5m` | **0** | M2 原始两个 F-03 负例 2/2 PASS |
| `go test ./internal/integration -run '^(TestM2F03SingleRepoLiveGitRechecks\|TestM2F03SingleRepoExecutionGates\|TestM2F03QueuedTurnRechecksWorkspace)$' -count=1 -timeout 12m` | **0** | 真实临时 Git 单仓异常、执行门禁、已入队消息重核验 PASS |
| `go test ./internal/service/scheduler ./internal/service/budget ./internal/service/workflow -count=1 -timeout 10m` | **0** | Scheduler、Workflow PASS；Budget 为 [no test files]，预算验证由 Store/Integration 覆盖 |
| `go test ./internal/storage/sqlite/store -run 'Test.*(Scheduler\|Admission\|Budget\|ExecutionPermit\|Materializ)' -count=1 -timeout 12m` | **0** | Admission/预算/许可/物化定向 PASS |
| `go test ./internal/httpd/controllers -run 'Test.*(M3\|Scheduler\|Budget\|Materializ)' -count=1 -timeout 8m` | **0** | M3 控制权限/预算/物化 HTTP 定向 PASS |
| `go test ./internal/adapters/resources -count=1 -timeout 5m` | **0** | Windows 资源采样包 PASS |
| `go build ./...`；`go vet ./...` | **0 / 0** | 两项无错误 |
| `npm run frontend:typecheck`（根目录） | **0** | TypeScript 类型检查 PASS |
| `go test ./internal/domain -run '^(TestBuildHandoffNoteStaysUnderTheCapAndKeepsTheLatestTurn\|TestHandoffNoteKeepsArtifactsWithinTheByteCap)$' -count=1 -timeout 5m` | **1** | 两项 Handoff Note FAIL；与 `m3-acceptance.md` 中在旧 HEAD 复现的历史两项同名同断言。不将其算为 M3 通过，也不据此认定本轮新增回归 |

**本轮另独立运行** `go test -race ./internal/integration -run '^TestM3' -count=1 -timeout 20m`，**exit 0 / PASS**，包输出耗时 **221.315s**，未出现 DATA RACE；命令会话 `session-db817b7b998435edbcfa217d`。完整 M2/M3 39 项普通 + Race、全 Manager/Chat/HTTP 和 SQLC/API 漂移来自此前 `m3-acceptance.md` 对原始日志的审计，**不是本次重新执行**；本轮只复跑上述有代表性的反例与当前构建/类型检查。

## 3. M2 补充判断

`m2-acceptance-round6.md` 的第六轮独立结论：**M2 离线安全/执行链 G0 PASS**。F-01 历史 pin、F-02 CLI/审批/受限 Operator、F-03 单仓/多仓 Worktree 安全门禁在该轮已按真实 Git/SQLite/CLI/Scripted 驱动复验。本轮又独立确认 F-03 的两个原反例以及三组真实 Git/执行门禁场景 PASS。

未验收 M2 的四真实 Provider 行为、原生 Windows Hello 人工正向、安装包/真实桌面交互、Node24/跨 OS 和最终部署，M0-05 预算未批准，不得将离线 PASS 表述为真实 Agent 完整验收。

## 4. M3 核心结论、风险与后续

当前实现符合 M3 设计的主要离线场景：默认 3 / 上限 10 的资源准入、双进程唯一 Claim、三级预算与 UNKNOWN、租约与旧 epoch、物化审批、工作区 fail-closed。独立原有 `m3-acceptance.md` 已记录 G7 **Windows 离线 PASS**，本轮源码抽查及定向反例支持该结论；本轮完整 M3 Race 也已重新执行，exit 0、无检测到 data race。**本轮复核结论：M3 Windows 离线 G7 PASS（限定范围）；M2 离线 G0 PASS（限定范围）。**

不能扩大为：全仓 `go test ./...` PASS、两个**完整 daemon** 端到端唯一 Session、四真实 Provider 端到端费用控制、真实硬件持续压力、Windows Hello 真人人工流程、Electron/安装包或其他 OS。两条已知 Handoff Note 测试本轮仍红，需要单独排期，但经既有报告旧 HEAD 对照不能归为 M3 新增阻断。

未执行 commit/push/merge/deploy，未修改生产文件。若后续进入 M4，应将 M3 改动建立清晰的可复现提交/工作树基线，并对上述未验范围逐项定级。
