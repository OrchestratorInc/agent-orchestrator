# M4 统筹验收缺陷与返修

日期：2026-11-06。实现方法由执行会话自行决定；本文件记录缺陷、返修及独立验收。最终 SV-01/03/04/05 均 CLOSED，SV-02 保留 ENVIRONMENT_LIMITATION；M4 本地离线验收结论见 [统筹验收](m4-supervisor-acceptance.md)。

## SV-01 / P1：已接受的 Gate 路径导致必需规则错误跳过

状态：CLOSED，2026-11-06 统筹独立复验通过；首次返修后通过，没有累计修复失败。关闭仅针对本缺陷，不代表 M4 整体通过。

位置：`backend/internal/adapters/testgate/runner.go` 的 `ValidatePolicy`；`backend/internal/domain/quality_gate.go` 的 `GateRule.SkipReason`；`backend/internal/storage/sqlite/store/gate_store.go` 的 SKIPPED 接受分支。

实际行为：策略校验调用 `CanonicalWorkflowPath`，只检查是否合法但未使用规范路径。SkipReason 用原始字符串做区分大小写的路径匹配。Windows 下，变更 `src/check.go`，HeadSHA 等于 CreationSHA，Level=3 的 Required 规则采用下列 WhenPaths 时均被接受，却返回 `paths_not_changed`：

```text
scope="src/" validation=<nil> skip="paths_not_changed"
scope="./src" validation=<nil> skip="paths_not_changed"
scope="SRC" validation=<nil> skip="paths_not_changed"
```

因此应运行的 G2/G3 可以被当作正常 SKIPPED；若其他规则 PASS，当前聚合逻辑允许整体 PASS。最后一个输入还与 Reservation/Git 路径在 Windows 上采用大小写折叠的语义不一致。

统筹实际复现命令（工作目录 `backend/`，临时 GOCACHE 指向项目 `.tmp/supervisor-go-cache`）：

```text
rtk proxy powershell -NoProfile -Command 'go run ./.tmp/supervisor-gate-path-check'
```

实际 exit 1。该程序 import 生产 `domain`/`testgate` 并调用真实导出函数，不匹配源码字符串。源码位于 `backend/.tmp/supervisor-gate-path-check/main.go`，日志和退出码位于 `.tmp/supervisor-20261106/gate-path-repro.log/.exit`；均为验收运行态，不纳入阶段提交。

通过条件：

- 被策略接受的路径与工件路径在当前 OS 上按同一目录组件语义比较；不能因 `./`、尾分隔符或 Windows 大小写差异跳过必需检查。
- 不合法/不支持的表示在进入执行前拒绝，或规范化后按真实变更运行；不能将校验成功又不匹配解释为“无变更”。
- 保留 `src` 不匹配 `src-other` 的组件边界，保留显式平台不适用和实际无相关改动的正常跳过。
- 提供行为反例复测及受影响 Gate/Store 集成结果。无需为同一根因拆大量配置断言用例。

独立复验结果（相同 checkout，项目临时 GOCACHE）：

| 命令 | 实际结果 | 证据 |
|---|---|---|
| `go run ./.tmp/supervisor-gate-path-check` | exit 0，原三种路径均 `validation=<nil> skip=""` | `gate-path-fixed.log/.exit` |
| `go test ./internal/adapters/testgate -count=1 -timeout 5m -v` | exit 0，0.702s；路径别名、目录组件、平台条件及真实 Runner 行为通过 | `gate-runner-independent.log/.exit` |
| `go test ./internal/storage/sqlite/store -run '^TestM4Gate' -count=1 -timeout 5m -v` | exit 0，3.733s；`TestM4GateEvidenceCASAndPolicyFencing` 拒绝已有变更的 `./src/` 规则伪 SKIPPED，并覆盖证据 CAS、策略失效与未决 Run 闭锁 | `gate-store-independent.log/.exit` |

日志位于项目 `.tmp/supervisor-20261106/`，不纳入提交。执行方后续仍在开发 M4；整体交接时需验收最终源码和所有剩余门禁。

## SV-02：Windows 独立 Preview 测试清理失败（待归因）

状态：ENVIRONMENT_LIMITATION / 原因未唯一归因。统筹沙箱外独立复跑已通过并清空临时目录；沙箱内两次清理失败保留，不宣称代码修复或将原失败改为 PASS。

统筹在项目临时 GOCACHE 和 TEMP/TMP 下执行：

```text
go test ./internal/adapters/workspace/gitworktree -run '^TestM4IntegrationPreview' -count=1 -timeout 5m -v
```

实际 exit 1，52.619s；测试主体未报告断言失败，但 Go 清理临时仓库时报：

```text
testing.go:1617: TempDir RemoveAll cleanup: unlinkat E:\code\agent-orchestrator\.tmp\supervisor-test-temp\TestM4IntegrationPreviewIsolatedAndReadOnly2042898394\001\.git\objects\10\24e5ffdbbe893c81f299ae44884316685683dd: Access is denied.
--- FAIL: TestM4IntegrationPreviewIsolatedAndReadOnly (50.41s)
```

日志：`.tmp/supervisor-20261106/integration-preview-independent.log/.exit`。此前两次沙箱外命令均因自动权限审核超时未启动；本次为普通沙箱内执行，不混同两种环境。

已派回执行方检查具体残留、文件属性、相关进程与稳定复现。通过条件：说明有证据的根因，若涉及代码则修复并给出受影响回归；若属权限或环境，明确有效验证环境与差异。统筹独立复验仍未通过。

后续证据：执行方完全访问环境同盘连续三次通过并完成清理；统筹更换全新 `supervisor-test-temp-2` 后仍在沙箱清理阶段失败，exit 1，55.182s，日志 `integration-preview-independent-rerun.log/.exit`。残留对象属性 `ReadOnly, Archive`，ACL 中 `Authenticated Users:(M)`；检查时未发现 Git 进程或该仓 `.git/worktrees` 残留。环境差异已反馈，不据此断言资源泄漏或要求未经证实的代码修改。

统筹随后经工具批准在沙箱外运行同一命令，TEMP/TMP 使用全新 `.tmp/supervisor-preview-elevated`，GOCACHE 使用 `.tmp/supervisor-go-cache`。实际 exit 0，包耗时 47.181s；`TestM4IntegrationPreviewIsolatedAndReadOnly` PASS（43.92s），结束后独立列举临时根为空。日志 `integration-preview-independent-full-access.log/.exit`。当前有独立功能与清理成功证据，有效环境是本机沙箱外；原沙箱内失败的瞬时占用/权限/运行 token 仍未唯一归因，作为验收环境限制保留。

## SV-03 / P1：API 隐藏 Generation 后破坏 Artifact 持久化往返

状态：CLOSED，2026-11-06。首次返修后持久化回归独立复验通过，最终完整集成 race 也由统筹独立运行通过。

位置：`backend/internal/domain/delivery.go` 的 `ArtifactSnapshot.Generation` 改为 `json:"-"`；`backend/internal/storage/sqlite/store/quality_store.go` 的 `artifactFromRow` 在解码 `snapshot_json` 后只恢复路径，未恢复独立列 `Generation`，接着比较指纹和代际。新写工件因此无法被有效重读，Gate/Review/Commit 后续链路被阻断。

统筹实际运行：

```text
go test ./internal/storage/sqlite/store -run '^TestM4GateEvidenceCASAndPolicyFencing$' -count=1 -timeout 5m -v
```

实际 exit 1，4.268s：`gate_store_test.go:36: ... begin quality gate: invalid delivery artifact`。日志 `.tmp/supervisor-20261106/artifact-redaction-regression.log/.exit`。

通过条件：API 保持内部 generation 不外泄；新旧工件从 SQLite 重读后身份与指纹完整；原失败用例和受影响真实装配链通过。使用既有行为测试验证，不用源码字符串断言替代。

执行会话从 429 恢复后，在 `artifactFromRow` 从独立列恢复 `Generation`，未放松指纹和身份校验。统筹随后实际运行 `go test ./internal/storage/sqlite/store -run '^TestM4(ArtifactImmutableReplayCASAndRestart|GateEvidenceCASAndPolicyFencing)$' -count=1 -timeout 5m -v`，exit 0，两项 PASS，4.233s。日志 `.tmp/supervisor-20261106/artifact-redaction-fixed.log/.exit`。原失败日志保留；最终 HTTP 输出和完整真实链仍在 M4-08/09 验收范围。

## SV-04 / P2：当前质量策略及 CAS revision 缺少读取入口

状态：CLOSED，2026-11-06。已补当前策略及 revision 读取，统筹最终完整集成 race 包含正式 HTTP 读回/CAS 验证并通过。

位置：`backend/internal/httpd/controllers/workflow_quality_read.go` 的 `policies` 仅遍历 Registry，返回的 `QualityPolicyResponse.Revision` 未赋值；`WorkflowQualitySummaryResponse` 及 coordination 响应也没有当前 Workflow 已选策略。controllers 中未调用 `GetWorkflowQualityPolicy`。`SetWorkflowQualityPolicy` 采用 revision=0 首次插入、非零 revision CAS 更新；已有策略再用 0 会返回 `ErrArtifactStale`。

影响：客户端刷新或重启后无法通过正式读取 API 获取当前已选策略和更新所需的 CAS revision。不能依靠保留上次 PUT 回执、猜版本或放宽 CAS 解决。

通过条件：正式只读接口返回当前已选策略的 ID、策略版本、digest、记录 revision，区分未配置；按读回 revision 可更新，旧 revision 拒绝；不暴露 executable/argv/env。实现可扩展已有响应。使用既有 HTTP 真实装配验证配置→读取→带读回版本更新→旧版本拒绝。

SV-03/04 最终独立证据：`go test -race ./internal/integration -run '^TestM4' -count=1 -timeout 30m -v`，经工具批准在沙箱外、隔离临时数据执行，exit 0，4 项顶层测试 PASS，910.933s。日志 `.tmp/supervisor-20261106/m4-final-race-independent-full-access.log/.exit`。`m4_artifact_e2e_test.go` 验证 HTTP 隐藏 Generation 与 SQLite 指纹/代际保留，`m4_repair_e2e_test.go` 调用 `m4CheckPolicyReadback` 验证未配置 null、当前策略和目录 revision、读回更新、旧 revision 409；HTTP 摘要另检查内部字段不外泄。原失败证据保留。

## SV-05 / P1：PR 归属转移可绕过 Workflow 集成审批

状态：CLOSED，2026-11-06 统筹独立复验通过。执行方独立审查先确认 Workflow→普通 Session 接管可移除 Merge 检查依赖的归属；统筹随后提出普通→Workflow 在 Merge 的 SCM 读取窗口内转移的反向竞态，执行方也已实际复现。

位置：`backend/internal/service/pr/action_service.go` 的 Merge 首次归属检查至 SCM 调用之间；`backend/internal/storage/sqlite/store/pr_store.go` 的 ClaimPR 归属转移；`quality_pr_claim_test.go` 的真实 SQLite/可暂停 Fake SCM 反例。

执行方真实失败：`TestM4WorkflowPROwnershipCannotBeTransferred` 报 `workflow PR takeover terminated=false: <nil>`（6.581s）；`TestM4WorkflowClaimDuringOrdinaryMerge` 报 `ordinary PR entered Workflow during merge: <nil>` 及 `merged PR became Workflow-owned`（6.444s）。日志位于 `C:/Users/11459/AppData/Local/Temp/ao-m4-resume-20261106-01a1acb1/m408-pr-takeover-red.log`、`m408-pr-race-red.log`；不是统筹运行所得，不冒充独立复验。

执行方当前方案：在同一 Claim 事务拒绝涉及 Workflow 的双向归属转移，并将创建后尚未完成 Bind 的 Session 纳入归属判断。最终通过条件：两个绕过都被阻断；已终止及待绑定的 Workflow Session 仍保持边界；正常普通 PR 的接管和 Merge 不受破坏。只有最终反例、受影响 Store/PR 回归和统筹独立复验通过后关闭。

统筹首次独立复跑 `go test ./internal/storage/sqlite/store ./internal/service/pr -run '^Test(M4WorkflowPROwnershipCannotBeTransferred|M4WorkflowClaimDuringOrdinaryMerge|WorkflowPRCannotBypassIntegration|ClaimPR.*)$' -count=1 -timeout 5m -v` 实际 exit 1（Store 13.672s，PR 5.568s）。6 项已有 ClaimPR 回归、并发窗口反例和 Workflow Merge 拒绝 PASS；`quality_pr_claim_test.go:45` 在设置 `session_id=NULL` 时违反数据库 state/session_id/bound_at CHECK，pre-Bind 测试前置状态无效。已派回修正合法夹具，不放宽生产约束。日志 `.tmp/supervisor-20261106/pr-ownership-independent.log/.exit`，本次不计通过。

修正后使用真实 ReserveAndClaim→CreateAdmittedSession、尚未调用 Bind 的合法状态。统筹复跑同一命令，实际 exit 0，9 项 PASS；Store 4.299s、PR 服务 0.391s。日志 `pr-ownership-independent-fixed.log/.exit`。覆盖 6 项既有 ClaimPR 行为、终止/未终止及合法 pre-Bind 归属保护、SCM 观察窗口反向接管拒绝及普通 Merge 保持可用、Workflow Merge 禁止绕过。关闭仅针对此缺陷，整体 M4 仍需最终验收。
