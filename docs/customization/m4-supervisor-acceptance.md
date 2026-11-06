# M4 统筹独立验收记录

日期：2026-11-06。状态：**PASS / Windows 本地离线 M4 阶段验收通过**。本文件由统筹维护；结论来自下列独立执行证据与执行方交付复核，不表示全仓、真实 Provider、网络 Push、原生人工审批或正式发布通过。阶段提交成功后才放行 M5，提交 SHA 由 [调度记录](delivery-status.md) 记录。

## 验收对象

接手基线 `129e13ca5d8df210cdafcc2e061d521109130e27`，分支 `feat/multi-agent-desktop`。执行会话 `01a1acb1-e2b9-7383-b0c9-02e5eef0ad67` 已完成 M4-01～09 并以 HANDOFF_READY 交接。统筹最终完整集成 race、契约、构建和类型检查通过；验收前后 127 个源码/生成文件 SHA256 完全一致，无新增源码。清单与比较结果为 `.tmp/supervisor-20261106/m4-final-start-files.json` 和 `m4-final-fingerprint-check.json`。

已审查 Artifact 身份/脱敏、Store CAS/Reservation 释放恢复、Gate 路径与证据、Review、Commit、Integration 逐仓意图与只读恢复、PR 归属旁路及正式接口消费。下列真实临时 Git/SQLite/Manager/HTTP/CLI 行为测试与 race 为本地离线结论提供运行证据，源码阅读本身不替代运行验证。

## 统筹实际验证

通过的 Git 定向测试原始证据目录：`C:/Users/11459/AppData/Local/Temp/ao-supervisor-20261106/`；日志、退出码及抽验源码 SHA256 已复制至 `E:/code/agent-orchestrator/.tmp/supervisor-20261106/`，不提交运行态。命令使用 RTK 包装，PowerShell `-NoProfile`；Go 工作目录为仓库 `backend/`。

| 检查 | 实际结果 | 证据 |
|---|---|---|
| `go test ./internal/adapters/workspace/gitworktree ./internal/storage/sqlite/store -run '^TestM4' -count=1 -timeout 12m -v` | exit 1，原 `E:/cache/go-build` 两处 `Access is denied`，setup failed | 统筹工具回执 `773aa9`；沙箱独立 TEMP 中的日志不冒充已归档证据 |
| 同命令，GOCACHE 改为该沙箱 TEMP 的 `go-build` | exit 1；Git 临时初始化 exit 128；Store 存在正在实施的 `GetQualityReview/InsertQualityReview` 等 sqlc 符号缺失 | 统筹工具回执 `af0edf`；沙箱独立 TEMP 中的日志不冒充已归档证据 |
| `go test ./internal/adapters/workspace/gitworktree -run '^TestM4Snapshot' -count=1 -timeout 5m -v`，临时 GOCACHE，经工具批准在沙箱外执行 | exit 0，4 项顶层用例 PASS，包含两项隐藏变更反例，包耗时 25.212s | `m402-independent-git.log/.exit`，已读取退出码文件为 0 |
| `go run ./.tmp/supervisor-gate-path-check`，项目临时 GOCACHE | 修复前 exit 1；修复后 exit 0，`src/`、`./src`、Windows `SRC` 三种已接受路径均返回空 skip reason | `gate-path-repro.log/.exit`、`gate-path-fixed.log/.exit` |
| `go test ./internal/adapters/testgate -count=1 -timeout 5m -v`，项目临时 GOCACHE | exit 0，包耗时 0.702s；真实退出码/超时/凭据隔离/日志摘要、路径组件与平台条件测试 PASS | `gate-runner-independent.log/.exit`，已读取退出码文件为 0 |
| `go test ./internal/storage/sqlite/store -run '^TestM4Gate' -count=1 -timeout 5m -v`，项目临时 GOCACHE | exit 0，包耗时 3.733s；路径别名下伪 SKIPPED 拒绝、真实性/CAS/策略失效和未决 Run 闭锁通过 | `gate-store-independent.log/.exit` |
| `go test ./internal/storage/sqlite/store -run '^TestM4(ReviewIdentity\|QualityAdmission\|CollaborationReplay\|TaskRequestReplay)' -count=1 -timeout 5m -v`，项目临时 GOCACHE | exit 0，4 项 PASS，包耗时 6.239s；后继质量准入、Reviewer 代际/旧证据、两轮修复封顶、满队列重放和 TaskRequest 身份绑定通过 | `review-store-independent.log/.exit` |
| `go test ./internal/adapters/workspace/gitworktree ./internal/storage/sqlite/store -run '^TestM4TaskCommit' -count=1 -timeout 5m -v`，项目临时 GOCACHE，经工具批准在沙箱外执行 | exit 0，2 项 PASS；Git 包 46.534s、Store 包 5.166s。覆盖用户 index/dirty 保全、受限 ref/目标漂移/重复执行拒绝、hook 隔离、签名要求、Gate/Review 证据与意图 CAS、未决冻结、旧任期拒绝和新任期只读对账 | `task-commit-independent.log/.exit` |
| `go test ./internal/adapters/workspace/gitworktree -run '^TestM4IntegrationPreview' -count=1 -timeout 5m -v`，项目临时 GOCACHE 和 TEMP/TMP，沙箱内 | exit 1，52.619s；主体未报告断言失败，但 TempDir 清理 Git 对象 `Access is denied`；尚待归因，不记 PASS | `integration-preview-independent.log/.exit`；详见 SV-02 |
| `go test ./internal/storage/sqlite/store -run '^TestM4Integration(Plan\|Operation)' -count=1 -timeout 5m -v`，项目临时 GOCACHE | exit 0，2 项 PASS，6.905s；审批精确绑定、单仓串行、双 Store 并发、部分恢复和新任期只读对账通过 | `integration-store-independent.log/.exit` |
| `go test ./internal/storage/sqlite/store -run '^TestM4GateEvidenceCASAndPolicyFencing$' -count=1 -timeout 5m -v`，API 隐藏 Generation 后 | exit 1，4.268s；`begin quality gate: invalid delivery artifact`，确认 SV-03 新回归 | `artifact-redaction-regression.log/.exit` |
| `go test ./internal/storage/sqlite/store -run '^TestM4(ArtifactImmutableReplayCASAndRestart\|GateEvidenceCASAndPolicyFencing)$' -count=1 -timeout 5m -v`，恢复独立列 Generation 后 | exit 0，2 项 PASS，4.233s；原失败 Gate 和工件跨连接读回、幂等、摘要、CAS 通过 | `artifact-redaction-fixed.log/.exit` |
| `go test ./internal/adapters/workspace/gitworktree -run '^TestM4IntegrationPreview' -count=1 -timeout 5m -v`，经工具批准在沙箱外、全新项目临时目录 | exit 0，47.181s；独立 Preview 和清理均成功，结束后临时根为空；原沙箱失败仍保留 | `integration-preview-independent-full-access.log/.exit` |
| `go test ./internal/storage/sqlite/store ./internal/service/pr -run '^Test(M4WorkflowPROwnershipCannotBeTransferred\|M4WorkflowClaimDuringOrdinaryMerge\|WorkflowPRCannotBypassIntegration\|ClaimPR.*)$' -count=1 -timeout 5m -v` | exit 1；8 项 PASS，但 pre-Bind 夹具清空已绑定记录的 session_id 违反 CHECK；已派回修正测试前置状态 | `pr-ownership-independent.log/.exit` |
| 同一 PR 命令，合法 ReserveAndClaim→CreateAdmittedSession 的 pre-Bind 夹具 | exit 0，9 项 PASS；Store 4.299s、PR 服务 0.391s；普通接管/双向 Workflow 边界/终止与待绑定身份/SCM 观察窗口通过 | `pr-ownership-independent-fixed.log/.exit` |
| `go test ./internal/service/pr -count=1 -timeout 5m` | exit 0，PR 全包 0.455s | `pr-package-independent.log/.exit` |
| `go test -race ./internal/integration -run '^TestM4' -count=1 -timeout 30m -v`，沙箱内 | exit 1，三条业务链在临时仓 `git init` 时因 `.git/config: Permission denied` 初始化失败；不能计作业务断言结论 | `m4-final-race-independent.log`；统筹工具回执记录 exit 1 |
| 同一完整集成 race 命令，经工具批准在沙箱外，独立 GOCACHE/TEMP | exit 0，4 项顶层测试 PASS，910.933s；真实 Manager 链 475.86s、Partial/Restart 421.86s、Reservation 队列 11.57s、Helper 0.00s | `m4-final-race-independent-full-access.log/.exit` |
| `go test ./internal/httpd/apispec/... -count=1 -timeout 5m` | exit 0，apispec 1.132s、specgen 30.793s | `m4-final-apispec-independent.log/.exit` |
| `go build ./...` / `go vet ./...` | 均 exit 0，无错误输出 | `m4-final-build-independent.log/.exit`、`m4-final-vet-independent.log/.exit` |
| `npm.cmd run typecheck`，工作目录 `frontend/` | exit 0，执行 `tsgo --noEmit`；先前裸 `npm` 被 PowerShell AuthorizationManager 拦截，未运行类型检查，不把该回执的 shell exit 0 当作通过 | `m4-final-typecheck-independent.log/.exit`；原启动失败为统筹工具回执 `cd94f2` |
| 127 文件 SHA256 比较、`git diff --check`、暂存区检查 | exit 0；`baseline=127,current=127,changed=[],added=[]`，diff 检查无输出；开始暂存前 index 为空 | `m4-final-fingerprint-check.json`、统筹工具回执 `2e4a7c` |

第二轮观察发生在 M4-03/04 写入期间，不能据此判定冻结 M4-02 回归，也不能标为通过。后续只针对稳定交付或可明确隔离的范围运行验收；不为了保持测试绿色干预执行方的生成顺序。

Git 定向复跑覆盖 index/dirty 保全、同历史换仓/分支漂移、路径逃逸及配置安全、范围外已提交变更、skip-worktree 隐藏变更。这里只证明该 Snapshot 范围，不能推导后续 Runner、Review、Commit 或 Integration 已通过。

## 整体验收清单

- [x] Artifact 真实多仓身份、范围、SHA、索引/dirty 保全、不可变持久化及状态 CAS。
- [x] Path Reservation 原子 Claim、共同 Git 目录冲突、旧任期/代际拒绝、UNKNOWN/重启保留与可信释放。
- [x] 受限 Runner 的真实退出码、缺工具、超时、日志与策略指纹失效。
- [x] 独立 Review 身份、旧工件拒绝、有界修复、Operator 物化边界。
- [x] 任务分支 Commit 白名单、Git hooks/filters/敏感路径拒绝与不确定副作用恢复。
- [x] 隔离预演不写目标 Ref，精确批准后串行 Apply，目标漂移、重放、多仓部分失败恢复；恢复的 expectedRevision 在 Store 写锁内再次校验。
- [x] 正式 Daemon/HTTP/CLI/CDC 接线、生成契约一致、权限错误及消息脱敏；普通 Worker 不能通过 ClaimPR 接管 Workflow PR 绕过 Merge 边界，原 Session 终止后仍保持归属约束。
- [x] 受影响 M2/M3 回归、适用 Build/Vet/Race；历史失败和未运行范围明确区分。
- [x] 执行方完整 HANDOFF、统筹独立结论、冻结源码清单；本地提交记录见调度文档，提交成功后才允许 M5 编码。

执行方受影响 M2/M3 五项回归 exit 0（261.039s）、Store/Principal/CLI/Controllers/迁移定向 race、HTTP/Daemon/PR/Runner 完整受影响包、SQLC/API 生成均有原日志，见 [实施报告](m4-report.md)。这些是执行方证据，未列为统筹亲自运行。

未验范围与已知限制：M0-05 真实 Provider 预算未批准；修复链只到受限 TaskRequest 物化，修改字节由测试模拟作者完成；原生审批使用 consent stub；Push 只实现可信注入的本地 bare 模拟，daemon 默认禁用，没有生产网络 transport。Runner 不是 OS 沙箱，SQLite/Git 不是跨系统原子事务。两项历史 Domain Handoff Note 失败、全 CLI 包 Windows symlink privilege 失败继续保留，由 M6 审计处理；不声称全仓通过。真实数据、远程 Push/Merge、发布、桌面安装升级及其他 OS 未验证。

## 本轮验收发现

[SV-01 / P1](m4-supervisor-rework.md)：已关闭。统筹原反例 exit 1；修复后相同反例、Runner 包和 Store 受影响行为独立复验均 exit 0。本项局部通过不代表 M4 整体验收。

[SV-02](m4-supervisor-rework.md)：沙箱内 Preview 测试清理连续两次失败；统筹沙箱外全新临时目录独立复跑 exit 0，结束后清理完成。记录为 ENVIRONMENT_LIMITATION，原运行时根因未唯一确定，不把新 PASS 覆盖原 FAIL，也不宣称代码修复。

[SV-03 / P1](m4-supervisor-rework.md)：CLOSED。两项 Store 行为与最终完整集成 race 均独立通过；HTTP 不泄露 generation，SQLite 独立列恢复后摘要/身份可读回，后续 Gate/Review/Commit/Integration 链通过。

[SV-05 / P1](m4-supervisor-rework.md)：已关闭。PR 双向归属转移的审批绕过已由真实 SQLite/Fake SCM 反例确认；首轮独立复跑因 pre-Bind 夹具违反 CHECK 而 exit 1，修正合法前置状态后同一命令独立复验 exit 0，9 项 PASS。

[SV-04 / P2](m4-supervisor-rework.md)：CLOSED。最终完整集成 race 包含 `m4CheckPolicyReadback`：未配置 null、配置→读取→按读回 revision 更新、旧 revision 409 `ARTIFACT_STALE` 均通过。摘要不泄露可执行命令/env。
