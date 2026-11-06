# M4 执行台账：增量迁移交接与自主实施

## 2026-11-06 本轮恢复状态（优先于下方历史快照）

用户已明确授权恢复 M4 业务实施；旧 `USER_APPROVAL_REQUIRED` 不再阻断。M0-05、真实 Provider、生产数据、远程 Push/Merge/部署和本仓提交仍未授权。

| 阶段 | 当前状态 | 证据/下一步 |
|---|---|---|
| 会话核查 | 已结束 | AgentDock Session=0，两名旧 Agent 最新回合 completed，未终止任何会话；见 m4-session-audit.md |
| M4-01 | ACCEPTED | 主 Agent 源码复核、四 ADR；4 项 M3 契约测试 exit 0，2 项历史 domain FAIL 保留 |
| M4-02 | ACCEPTED | 主 Agent 真实两仓 Snapshot/Manager、M2F03/物化回归 exit 0；M4 Store race exit 0；4 项 Git Snapshot PASS，迁移登记 PASS；详见 m4-report.md |
| M4-03、M4-04 | ACCEPTED | 主控真实 Manager/Git/Gate/Review 集成 exit 0；Gate/Review race exit 0；SV-01 路径跳过反例修复并通过；阶段范围为服务与持久契约，HTTP 装配在 08 |
| M4-05 | ACCEPTED | 受限任务 ref 事务、精确证据和 writer fencing；Store race exit 0，真实 Git/Manager/Gate/Review/Commit 及丢回执恢复 exit 0；HTTP 装配仍在 08 |
| M4-06 | ACCEPTED | detached Worktree 真实 merge-tree、两仓完整集合/不可变计划/重读、冲突与漂移反例；Git、Store race、迁移、最终真实链全部 exit 0 |
| M4-07 | ACCEPTED（主控阶段） | 精确 Approval、双 Operator、真实两仓 Apply/Partial/重启恢复、新 Leader fencing、本地 bare Push 与远端 SHA 漂移反例 exit 0；不代表真实网络 Push 已验证 |
| M4-08 | ACCEPTED（主控阶段） | 正式 HTTP/CLI/Daemon、脱敏 DTO、CDC、协调摘要、策略读回/CAS、PR 双向归属闭锁；正式 CLI 链与生成物检查 exit 0 |
| M4-09 | ACCEPTED（主控 G4，待统筹终验） | M4 集成普通/race、M2/M3 受影响回归、Build/Vet、SQLC/API/TS、Git/Runner、安全反例均有 exit 0；全量 CLI symlink 基线 FAIL、SV-02 环境限制和历史 Domain FAIL 保留 |
| **M4 总体** | **HANDOFF_READY / NOT_ACCEPTED（待统筹）** | 主控已形成可复核交付；不得启动 M5，等待统筹独立验收和最终状态回填 |

本轮在当前 checkout 继续；保全目录 `C:/Users/11459/AppData/Local/Temp/ao-m4-resume-20261106-01a1acb1/`。按需使用 Astra 只读审计子代理 Hume `01a1acb5-275e-7f33-ab62-886fc995bea2`（显式 `gpt-6-astra/high`），无写入权。主 Agent 对全部验收负责；不更新 AgentDock 任务，不进行心跳轮询。

08/09 收口证据：正式 CLI 链 `m408-formal-cli-chain-fixed` exit 0（628.577s）；完整 M4 race `m409-m4-race-final` exit 0（874.484s）；M2/M3 受影响五项回归 `m409-m2-m3-regression-final` exit 0（261.039s）；命令、文件范围及失败留档见 [实施报告](m4-report.md)。修复任务已物化，修复代码字节由测试模拟；真实修复 Agent 未运行。当前策略读回、reconcile writer CAS、PR 双向归属保护和可空工件生成契约已补齐。

以下记录解释迁移时历史状态，不能覆盖本轮授权和状态。

> 日期：2026-11-06（Asia/Shanghai）；迁移行政任务：`tsk_5690f562ed17fe19`。主控独立验证后 final_review pass（`rev_83df0ccb5afa5a87`），complete 成功，4 步骤均 completed；仅迁移闭环，实施继续暂停。
> 仅授权任务体系增量迁移，完成后暂停；实施任务 blocked / USER_APPROVAL_REQUIRED，M4 阶段 ACCEPTED=0。不得依据旧“立即执行”段派发。
>
> **说明：本文件是 2026-11-06 任务迁移的历史快照，不是后续开发期的强制操作流程。当前开发方式以 `m4-agent-prompt.md` 自主执行 V2 为准：不要求反复调用 AgentDock/Task Manager，不要求强制多 Agent；只有主 Agent 自主决定使用子代理时才指定 Astra。下文 TaskManager 能力、旧并发值和 Agent 记录均用于解释当时迁移事实，不应作为新的开发调度要求。

## 1. 来源、身份与授权

本台账引用主控本轮真实盘点与最新回执，不重复审计源码或执行旧测试；任务/会话/文档状态分开记录。
工作区 `E:/code/agent-orchestrator`，分支 `feat/multi-agent-desktop`，HEAD `129e13ca5d8df210cdafcc2e061d521109130e27`。
2026-11-06 迁移任务当时仅修改本台账、[迁移报告](m4-migration-report.md)、[计划](m4-plan.md)；其余 [设计](m4-design.md)、[协议](m4-coordination-protocol.md)、[提示词](m4-agent-prompt.md) 当时只替换模型并增加顶部暂停说明。后续自主执行 V2 文档修订另见上述现行文档。
迁移当时 README 尚保留旧模型文字；README 已在自主执行 V2 修订中更新。当前开发期如使用子代理，模型为 Astra（当前已知工具标识 `gpt-6-astra`）；主 Agent 不必使用子代理。
迁移任务当时不改 README、M4-01 草稿、业务代码、测试实现、配置或真实 AO 数据；不执行 Commit/Push/Merge/Provider，不创建工作树。

## 2. 新旧 Task 映射

| 完整 Task ID | 原状态/原步骤 | 迁移后位置和原因 |
|---|---|---|
| `tsk_6af643d277ebc917` | completed；final_review pass；无 steps（0），主控 get 确认 steps=[] | “提交 M3 并设计 M4 与委派提示词”；保留 M3 commit 与 V0.1 历史，作为 M4-01 已完成输入，不代表阶段完成 |
| `tsk_1eb9e75eeb50edb6` | completed/pass；无 steps（0），主控 get 确认 steps=[] | “升级 M4 多 Agent 协同提示词 V2”；复用 design/plan V0.2、protocol/prompt/README 输入；旧模型要求被 astra 覆盖 |
| `tsk_e36725bd9a1f5cf8` | active；原 M4-01～09 | “M4 多 Agent 实施与主控独立验收”；原地 blocked，原 task/step ID、步骤状态与历史保留；等待批准，不是 FAIL |
| `tsk_5690f562ed17fe19` | 本次唯一新增行政任务，现 completed | 原步骤 `inventory/mapping/documents/verify_pause` 均 completed；只承载迁移与暂停核验，不新增 9 个实施阶段 |

盘点时 TaskManager 共 48 条，M4 相关 3 条，旧 completed=2、active=1，旧 blocked/unknown/failed=0。
只新增 1 条行政任务，最终 M4 相关 4 条：3 completed（文档行政类）+1 blocked（实施类）；主控已确认行政任务 complete 成功，父实施任务一直 blocked。
旧任务的 completed 历史不撤销；9 个 M4 阶段完成/ACCEPTED 仍为 0。

## 3. 保留原 step 的 V2 增量与 DAG

| 完整 Task ID / 原 step ID | 原生状态→保留状态 | 前置 | V2 实际内容增量及放行条件 |
|---|---|---|---|
| `tsk_e36725bd9a1f5cf8 / M4-01` | in_progress→in_progress | 无 | 复用两条历史输入；审查协同能力/ADR/恢复/所有权；原 Agent 已交接，草稿待验，接口未冻结 |
| `tsk_e36725bd9a1f5cf8 / M4-02` | pending→pending | M4-01 | Artifact/质量持久化、CAS、多 Repo Path Reservation、M3 reserve/claim 事务接点 |
| `tsk_e36725bd9a1f5cf8 / M4-03` | pending→pending | M4-02 | GatePolicy、受限 argv、指纹/缓存、M3 Scheduler 最小协调接线 |
| `tsk_e36725bd9a1f5cf8 / M4-04` | pending→pending | M4-02 | 独立 Review、M2 消息/Handoff、TaskRequest 权限与有界修复 |
| `tsk_e36725bd9a1f5cf8 / M4-05` | pending→pending | M4-03+M4-04 | Gate+Review 后受限任务分支本地 Commit、幂等与 Git 身份重验 |
| `tsk_e36725bd9a1f5cf8 / M4-06` | pending→pending | M4-05 | 隔离 Integration Preview、多 Repo Plan，不更新目标 Ref |
| `tsk_e36725bd9a1f5cf8 / M4-07` | pending→pending | M4-06 | 精确 Operator 审批、串行 Apply、Partial/Unknown 恢复、独立 Push 授权 |
| `tsk_e36725bd9a1f5cf8 / M4-08` | pending→pending | M4-05+M4-07 | HTTP/CLI/CDC、只读协调摘要、Daemon 与生成物唯一 Owner 接线 |
| `tsk_e36725bd9a1f5cf8 / M4-09` | pending→pending | M4-08 | 综合 E2E/安全/Race/报告；08 及全部前置均 ACCEPTED 后才进入 |

完整依赖：`M4-01→02；02→03、04；03+04→05→06→07→08→09；05→08`。
03/04 仅在 02 独立验收且接口冻结后并行；01～08 各阶段先独立验收，不能以 HANDOFF 放行，也不能等 09 才首次验收。
当前用户暂停且 01 未 ACCEPTED，可派发编码任务为 0。旧版“开发子代理默认并发 3、硬上限 10”已由自主执行 V2 替代，不再约束开发期；产品运行时并发仍按 M3 Scheduler/Workflow 策略。

## 4. Agent / Session 与工作区

| 身份 | 真实 Agent/Session ID | 显式模型/强度 | 当前状态和写入范围 |
|---|---|---|---|
| 原 M4-01 Turing | `01a1ac47-6b0a-7823-9626-b6b3ede30e56` | `gpt-6-astra/high` | completed/HANDOFF；仅保存原审查草稿；`m4-architecture-review.md` 已落盘 120 行、IN_PROGRESS/NOT_ACCEPTED；四份 `m4-adr-*.md` 未落盘 |
| 文档迁移 Copernicus | `01a1ac72-1187-7382-975f-0c323eb7b6b2` | `gpt-6-astra/low` | 本行政任务 documents 交接；排他写上述 6 文档，不属于新 M4 编码派发 |
| M4-02～09 后续实现 | 未开始 | 主 Agent 自主决定独立完成或按需委派；如委派使用 Astra（`gpt-6-astra`） | 未开始；不要求预先创建 Agent 或 Worktree |

两名已知 Agent 工作区均为本仓库；只有 Turing 是原开发 Agent。保留原会话和产物，不终止、不重启开发、不把 completed 等同阶段 ACCEPTED。
现有另一工作树 `E:/code/ao-m0-lab`，detached `7ce17e58247cc3e40669e9a29559c69cc18f04ac`，属于旧任务，非 M4，不操作。
当前没有 M4 编码隔离 Worktree。用户批准后先通过工具创建独立 Worktree，优先项目 `.codex/worktree/`，不改配置，以工具实际返回并核实的路径为准。
迁移文件与审查草稿写范围互斥；本台账不授予任何业务文件的当前写权。

## 5. 未来唯一 Owner 规划

以下源码路径默认相对 `backend/internal/`；显式 `docs/`、`frontend/` 路径相对仓库根。只是排他范围规划，未来 Agent ID/Worktree 均未创建。

| 阶段 / Owner | 排他范围与交接规则 |
|---|---|
| 01 / 原 Turing | 原 `docs/customization/m4-architecture-review.md` 与 `m4-adr-*.md`；现只保留已交接草稿，不补写 |
| 02 / DB、Artifact、Reservation | `domain/delivery*.go`、`ports/quality*.go`、`storage/sqlite/queries/quality*.sql`、新迁移/迁移登记/`gen/`/新增 Store；M3 reserve/claim 事务接点，含共享 `scheduler_admission_store` |
| 03 / Gate | `service/qualitygate`、`adapters/testgate`、M3 Scheduler 最小协调接线；共享 Store 变更交 02 |
| 04 / Review | `service/qualityreview` 与既有 review/collaboration 最小适配 |
| 05 / Git | `service/taskcommit`、workspace 受限 Git 文件 |
| 06 / Preview | `service/integration` 预演和 Git 预演文件 |
| 07 / Apply | 串行接续 06 文件独占；`service/principal` 必要审批适配 |
| 08 / API | `httpd/controllers/workflow_quality*.go`、`daemon/quality_wiring.go`、`cli/quality.go`、CDC、DTO/specgen/openapi；`frontend/src/api/schema.ts` 唯一生成 Owner |
| 09 / QA | `m4*_test.go` 与 `docs/customization/m4-report.md`；`m4-acceptance.md` 仅主控独立判断 |

共享迁移/ports/SQL/SQLC 始终由 02 维护；06→07→08 重叠文件串行交接。Owner 验收、停止写入、冻结产物后才能转交。
目录和子目录重叠视为冲突；其他 Owner 只能提出变更，不自行编辑共享文件。

## 6. 历史 TaskManager 能力与 Ledger 状态映射

以下仅记录 2026-11-06 迁移时核实到的宿主工具能力，不是后续 M4 实施必须依赖的工具。当前自主执行 V2 不要求调用该 Task Manager。

真实 schema 仅 create/list/get/checkpoint/block/resume/final_review/complete；没有 update、原生 DAG、Owner、HANDOFF 或 paused。
Ledger 生命周期：`PENDING→READY→DISPATCHING→RUNNING→HANDOFF→VERIFYING→ACCEPTED`；异常/侧状态：`BLOCKED/RETRY_WAIT/REWORK/REJECTED/FAILED/UNKNOWN/CANCELLED`，不是宿主原生状态枚举。
完整 V2 JSON 已成功保存于行政任务 `tsk_5690f562ed17fe19` 的 `mapping` checkpoint；主控 get 读回 `mappingPersisted=true`，包含映射/依赖/Owner/model。Ledger 是完整 DAG 恢复源。
父实施任务 `tsk_e36725bd9a1f5cf8` 保持 blocked；blocked 时禁止 checkpoint，因此没有 resume 父任务，也没有把完整 JSON 存到父任务。
父 block 文字“前一 checkpoint 完整映射”是旧误述，行政 mapping checkpoint 已补纠正；不修改底层状态 JSON，不声称原生 DAG 已接线。

| TaskManager 事实 | Ledger 表达 | 不能推导出的结论 |
|---|---|---|
| 父任务 blocked / USER_APPROVAL_REQUIRED | 整体等待用户批准 | 不是功能 FAIL、不是未知会话 |
| M4-01 in_progress；Agent completed | HANDOFF / 待验，NOT_ACCEPTED | Agent 完成不等于阶段 ACCEPTED |
| M4-02～09 pending | PENDING、依赖未满足、未派发 | 不因模型可用而自动 READY |
| 行政 mapping checkpoint 已存 | 完整 V2 映射持久化 | 不是父任务更新或产品 DAG 实现 |
| 行政任务 complete 成功，4 步骤 completed | 主控 final_review pass（`rev_83df0ccb5afa5a87`），仅迁移结束 | 不改变 9 个阶段状态或授权 |

## 7. 历史时间线与证据

| 时间/顺序 | 事实及证据 |
|---|---|
| 历史任务 | M3 基线 commit、V0.1 与 V0.2 设计均保留；不重做、不撤销 completed |
| 2026-11-05，原模型请求 | `cn:deepseek-v4.1-flash` 返回 `Unknown model`，未创建 Agent；用户随后授权 astra，模型阻断已解除，不是失败的 M4 代码任务 |
| 2026-11-05，基线日志 | 主控和 Turing 各自日志仍使用原日期；本次不改名、不重跑 |
| 2026-11-06，迁移 | 仅增行政任务，主控将父任务原地 blocked，原因 USER_APPROVAL_REQUIRED |
| 2026-11-06，旧 Agent 交接 | Turing completed/HANDOFF；120 行审查草稿，四 ADR 未落盘，接口/事务边界未冻结 |
| 2026-11-06，checkpoint 纠正 | 前次缺 step_id 失败；父 blocked 后又不允许 checkpoint；最终成功存行政 mapping，get 读回 mappingPersisted=true |
| 2026-11-06，主控复核 | 2939 个 backend+API 源文件 SHA256 changed=0；backend/frontend git status 空；git diff --check exit 0 |
| 2026-11-06，主控原文核验 | 三份 design/coordination/prompt 去顶部说明并反向还原模型后，SHA256 与 design-v02-files.json 完全一致 |

证据目录 `C:/Users/11459/AppData/Local/Temp/ao-m4-20261105-main/`：`baseline-files.json`（2940 项含 README）、`design-v02-files.json`、`m401-domain-baseline.log` / `m401-domain-baseline.exit`。
子 Agent 证据：`C:/Users/11459/AppData/Local/Temp/ao-m4-01-astra-20261105/domain-baseline.log` / `domain-baseline.exit`。
历史命令：`go test ./internal/domain -run "Test(BuildHandoffNote|HandoffNote|QuotedTextCannotAdd|ClipHandoffNote)" -count=1`，exit 1。
历史失败仅 `TestBuildHandoffNoteStaysUnderTheCapAndKeepsTheLatestTurn`、`TestHandoffNoteKeepsArtifactsWithinTheByteCap`；保持 FAIL，不折算为 M4 PASS。
主控最终验证：`C:/Users/11459/AppData/Local/Temp/ao-m4-20261105-main/migration-validation.log` 与 `migration-validation.exit`（0）；git rev-parse/branch/worktree/diff --check/status 均 exit 0，2939 个源文件哈希 changed=0，7 Markdown/24 链接/errors=0，三文档原文还原 hash=true，DAG mapping 一致。同目录 `migration-tasks-snapshot.json` 是完成前核验的只读快照；行政 complete 成功依据主控最终回执，不将该快照冒充完成后状态。

## 8. 暂停、恢复与下一步

迁移交接无需等待 M4-01 完成；本 Agent 交付文档和核验退出码后停止。恢复编码必须用户明确批准，不能由旧提示词自动恢复。
批准后主控先核对原 Task/Agent/产物，再补齐 01 缺失交付并独立验收；01 ACCEPTED 后才进入 02，02 验收和接口冻结后才并行 03/04。
当前未完成：四 ADR、接口/事务冻结、M4-01 独立验收及全部 M4 功能/测试。真实 Provider、原生认证、发布等未验证范围保持原状态。
