# M4 任务体系增量迁移报告

> 2026-11-06（Asia/Shanghai）；主控独立验证后 final_review pass（`rev_83df0ccb5afa5a87`），迁移行政任务 complete 成功，4 步骤均 completed；实施继续暂停。
> 只完成授权的文档/任务映射迁移，不宣称 M4 功能完成；完整恢复依据见 [执行台账](m4-execution-ledger.md)。

## 1. 授权范围与基线

工作区 `E:/code/agent-orchestrator`，分支 `feat/multi-agent-desktop`，HEAD `129e13ca5d8df210cdafcc2e061d521109130e27`。
仅迁移 M4 任务体系，完成后暂停。遵守六文件排他写范围；不改业务代码/测试实现/配置/README/旧审查草稿，不执行 Commit/Push/Merge/Provider，不访问真实 AO 数据。
本报告采用主控提供的现场事实，不额外审计源码、不重复执行 Go 测试。

## 2. 旧任务盘点和数量

盘点 TaskManager 共 48 条，M4 相关旧任务 3 条：2 completed、1 active；旧 blocked/unknown/failed=0。
仅新增行政任务 `tsk_5690f562ed17fe19`，步骤 `inventory/mapping/documents/verify_pause`，没有新建 9 个重复阶段。
最终数量：M4 共 4 条，3 completed 文档行政类+1 blocked 实施类。行政任务 `tsk_5690f562ed17fe19` complete 成功，4 步骤均 completed；父 `tsk_e36725bd9a1f5cf8` 一直 blocked，01 HANDOFF 未验收、02～09 pending，M4 阶段 ACCEPTED=0。

## 3. 新旧映射与历史保留

| 旧任务 | 当前状态 | 实际完成度 | 已有成果 | 迁移方案 |
|---|---|---|---|---|
| `tsk_6af643d277ebc917`：提交 M3 并设计 M4 与委派提示词 | completed/final_review pass；无 steps（0） | 历史提交/设计完成，非 M4 功能完成 | M3 commit、V0.1 设计历史 | 保留已完成状态，映射 M4-01 已完成输入 |
| `tsk_1eb9e75eeb50edb6`：升级协同提示词 V2 | completed/pass；无 steps（0） | 历史设计升级完成，非实施验收 | V0.2 design/plan、protocol/prompt/README | 复用输入，模型由用户 astra 授权覆盖 |
| `tsk_e36725bd9a1f5cf8`：M4 实施与独立验收 | blocked/USER_APPROVAL_REQUIRED | 0/9 ACCEPTED；01 in_progress，02～09 pending | 120 行审查草稿，四 ADR 未落盘 | 保留原 Task/step ID、状态/history；按 V2 增量迁移，等待批准 |

M4-01 原生 in_progress；02～09 原生 pending；阶段完成 0。历史设计 completed 与当前实施 ACCEPTED 分开。
各 step 的 V2 增量、前置和完整 Task ID 已写入 Ledger，保留已完成输入，不重新开发。

## 4. DAG、并发与推进条件

依赖为 `01→02；02→03、04；03+04→05→06→07→08→09`，08 另显式依赖 05。
默认并发 3，硬上限 10；03/04 仅在 02 独立验收与接口冻结后并行。
01～08 逐阶段独立验收；09 仅在 08 及全部前置均 ACCEPTED 后进入，HANDOFF 不能直接放行。
用户暂停且 01 未 ACCEPTED，当前可派发编码任务为 0。

## 5. 原 Agent 处置与模型

Turing Agent/Session `01a1ac47-6b0a-7823-9626-b6b3ede30e56`，显式 `gpt-6-astra/high`，现 completed/HANDOFF。
已落盘 `m4-architecture-review.md` 120 行，内容状态 IN_PROGRESS/NOT_ACCEPTED；四 ADR 未落盘，接口/事务边界未冻结，M4-01 不 ACCEPTED。
只保留草稿和交接，不终止/重启原开发。只有这 1 个旧开发 Agent；本迁移 Agent 不属于新 M4 编码派发。
Copernicus Agent/Session `01a1ac72-1187-7382-975f-0c323eb7b6b2`，显式 `gpt-6-astra/low`，本次只交付文档。
旧 cn 模型请求报 `Unknown model` 且没有创建 Agent；astra 已获用户授权，原模型阻断解除，不记为 M4 代码失败。

## 6. Owner 与工作树隔离

未来 02～09 的 Agent ID/Worktree 均标未创建；没有 M4 编码隔离 Worktree，本次不创建。
旧 `E:/code/ao-m0-lab` detached `7ce17e58247cc3e40669e9a29559c69cc18f04ac` 非 M4，不动。
Owner 规划：02 数据库/Artifact/Reservation及 M3 reserve/claim；03 Gate；04 Review；05 Git；06 Preview；07 Apply；08 API/CDC/生成；09 QA。
共享迁移/ports/SQL/SQLC/`scheduler_admission_store` 唯一 Owner 为 02；06→07→08 文件串行转交；完整路径在 Ledger。
`m4-acceptance.md` 仅主控独立判断；批准后先创建独立 Worktree，优先项目 `.codex/worktree/`，以工具实际回执核实路径，不改配置。

## 7. TaskManager 实际保存位置与能力限制

真实 schema 只有 create/list/get/checkpoint/block/resume/final_review/complete，没有 update、原生 DAG/Owner/HANDOFF/paused。
完整 V2 JSON 已成功存迁移行政任务 `tsk_5690f562ed17fe19` 的 `mapping` checkpoint，主控 get 读回 `mappingPersisted=true`；Ledger 是完整 DAG 恢复源。
前次 checkpoint 缺 step_id 失败；父任务 blocked 时禁止 checkpoint，所以未 resume 父任务。
父实施任务保持 blocked；其 block 文字“前一 checkpoint 完整映射”为旧误述，行政 mapping checkpoint 已补纠正；不能声称父任务保存完整 JSON。
没有手改底层状态 JSON，也没有把文档 DAG 称为宿主原生 DAG 或产品接线。

## 8. 实际文档改动

| 文件 | 本次增量 |
|---|---|
| [m4-execution-ledger.md](m4-execution-ledger.md) | 新旧映射、9 个原 step、DAG、Owner、模型、工作树、证据、恢复与暂停 |
| 本报告 `m4-migration-report.md` | 十项迁移交接结果、核验与未完成项 |
| [m4-plan.md](m4-plan.md) | 更新模型、原任务状态、独占 Owner、前置验收、checkpoint 位置与暂停要求 |
| [m4-design.md](m4-design.md) | 仅旧模型替为 gpt-6-astra、增加顶部暂停说明 |
| [m4-coordination-protocol.md](m4-coordination-protocol.md) | 同上，保留原设计全文 |
| [m4-agent-prompt.md](m4-agent-prompt.md) | 同上，旧“立即执行”段须受顶部暂停说明约束 |

README 旧模型文字作为历史入口保留，由当前四文档及用户 astra 授权覆盖。未覆盖原 M4-01 审查草稿。
主控已验证三份保留全文文档反向还原后，SHA256 与 `design-v02-files.json` 一致。

## 9. 验证、命令与历史 FAIL

主控提供：2939 个 backend+API 源文件 SHA256 changed=0；backend/frontend git status 空；`git diff --check` exit 0。
主控最终验证日志：`C:/Users/11459/AppData/Local/Temp/ao-m4-20261105-main/migration-validation.log` / `migration-validation.exit`（0）；git rev-parse/branch/worktree/diff --check/status 均 exit 0，2939 哈希 changed=0，7 Markdown/24 链接/errors=0，三文档原文还原 hash=true，DAG mapping 一致。同目录 `migration-tasks-snapshot.json` 为完成前核验的 Task JSON 只读快照；行政完成状态来自主控最终回执。
证据根目录 `C:/Users/11459/AppData/Local/Temp/ao-m4-20261105-main/`，含 `baseline-files.json`（2940 项含 README）、`design-v02-files.json` 与 `m401-domain-baseline.log/.exit`。
Turing 原日志 `C:/Users/11459/AppData/Local/Temp/ao-m4-01-astra-20261105/domain-baseline.log/.exit`；保留 2026-11-05 原日期。
历史命令 `go test ./internal/domain -run "Test(BuildHandoffNote|HandoffNote|QuotedTextCannotAdd|ClipHandoffNote)" -count=1` exit 1：
`TestBuildHandoffNoteStaysUnderTheCapAndKeepsTheLatestTurn`、`TestHandoffNoteKeepsArtifactsWithinTheByteCap` 两条历史失败，未重跑、不改为 PASS。
文档 Agent 已执行六文件 `git diff --check -- <六个文档路径>`：exit 0、无输出；UTF8 读取/链接及统计核验命令 exit 0：24 个本地链接、断链 0、尾随空白 0，四文档旧模型残留 0；三份受限编辑文档反向还原后均与本轮原文一致。收口修订后同项复核见最终交接回执，不将主控验证冒充本 Agent 执行。

## 10. 暂停交接与未完成项

文档 HANDOFF 后主控已完成独立验证，final_review pass（`rev_83df0ccb5afa5a87`），行政任务 complete 成功，4 步骤均 completed；父实施始终保持 blocked / USER_APPROVAL_REQUIRED。本次仅回填终态，随后停止，由主控复验状态与文档一致性。
迁移完成不依赖 M4-01 完成。用户批准前没有下一轮编码派发；批准后先恢复/核验原交付，再按原 DAG 推进。
尚未完成：四 ADR、接口/事务冻结、01 独立验收、02～09 全部业务实现及测试；M4 不 PASS。
本轮未运行 Go/构建/Provider/真实 AO 数据验证，未作 Commit/Push/Merge；历史 FAIL 和未验收阶段仍须保留。
