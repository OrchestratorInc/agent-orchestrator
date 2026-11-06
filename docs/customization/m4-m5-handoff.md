# M4 → M5 技术交接

2026-11-06。本文记录已接线接口及消费约束。主控已完成 M4-01～09 的实现和 G4 验证，当前为 HANDOFF_READY；M4 最终验收以 `m4-acceptance.md` 和统筹验收为准，不授权提前实施 M5。本仓未提交、未 Push。

## 本轮收口补充

- 正式 CLI 链已实际穿过 `ao workflow quality` 的审批申请/确认、HTTP 路由、SQLite 持久化和 Git 写入；不是只测试 Service 直调。完整链退出码 0，证据见 `m4-report.md`。
- `GET quality-summary` 的 `currentPolicy` 和 coordination 中的 `quality.currentPolicy` 返回当前已选策略的 `id/policyRevision/revision/digest`；未配置明确返回 null。`GET quality-policies` 返回候选策略及当前配置记录 revision。客户端应使用读回的 `revision` 更新，旧 revision 返回 `ARTIFACT_STALE`。
- `POST integration-operations/{operationId}/reconcile` 的 `expectedRevision` 在 Store writer 内再次校验；迟到恢复不能获得旧操作的写权。reconcile 只做观察和事实对账，不自动重试 Git 写入。
- PR 归属变更同时检查旧 owner 与新 owner；Workflow→普通和普通→Workflow 两个方向都闭锁，含已接受但尚未 Bind 的合法 Workflow Session，防止 Merge 窗口绕过集成审批。
- HTTP/CDC 投影不泄露 Generation、Repo/Worktree/common-dir、Approval 引用、原始请求、Runner 日志、executable/argv/env；`GitArtifact.files` 的 null/[] 表示参与冻结摘要，OpenAPI 已按实际契约生成。
- 修复工作流仅验证到 Operator 批准后的受限 TaskRequest 物化，测试随后模拟作者修改现有 Worktree 并继续质量链；M5 不得把它解释成真实 Agent 自动修复已验证。

## 接口与客户端

前缀 `/api/v1/workflows/{workflowId}`。Go DTO 为 `backend/internal/httpd/controllers/dto.go`，操作登记为 `backend/internal/httpd/apispec/specgen/quality.go`；`npm run api` 生成 OpenAPI 与 `frontend/src/api/schema.ts`，禁止手改生成文件。

| 方法与相对路径 | 语义 / 正文 |
|---|---|
| GET `quality-summary` / `coordination-summary` | 只读质量证据与原 DAG/Attempt/Session/能力/预算/Reservation/最近消息投影 |
| GET `quality-policies` | 可信策略 ID、revision、digest；不含 executable/argv/env |
| PUT `quality-policy` | `policyId, expectedRevision`，原生 Operator 批准 |
| POST `attempts/{attemptId}/artifacts/snapshot` | `artifactRevision`；取服务端绑定，不接收 Repo 路径/SHA |
| POST `artifacts/{artifactId}/gates` / `reviews` | `idempotencyKey`；服务执行 Gate，独立 principal 开始 Review |
| POST `reviews/{reviewId}/finish` | `expectedRevision, verdict, findings`；必须是原 Reviewer |
| POST `artifacts/{artifactId}/commit` | `idempotencyKey, artifactRevision`；Gate+Review 后仅写任务分支 |
| POST `integrations/preview` | `idempotencyKey, artifactIds`；全部相关仓冻结预演，无目标 ref 写入 |
| GET `integrations/{planId}` | 返回原计划及有序仓库 entries |
| POST `integrations/{planId}/apply` / `push` | `idempotencyKey, expectedRevision, planDigest, entries`；Push 另含 `pushTargets`，两动作必须分别批准 |
| GET `integration-operations/{operationId}` | 操作及逐仓事实，不能将 partial/unknown 显示成成功 |
| POST `integration-operations/{operationId}/reconcile` | `expectedRevision`，单独人工批准后只读 Git 对账，无重试写入 |

`ao workflow quality` 提供上述 15 个命令。读命令参数为 workflow ID，可选 target ID；写命令另需 `--body-file`。CLI 通过原 `performOperatorAction` 申请并确认精确 method/path/body 批准，不打开 SQLite/Git。Agent 环境拒绝 Operator CLI，产品 Agent 用既有 capability 身份调用受限 API。

## 证据、状态与刷新

- `coordination-summary` 是多来源只读观察，不是事务一致的执行授权。保留观察时间、revision、Lease 有效性与 UNKNOWN，任何写入仍由原服务/SQLite 栅栏裁决。不会启动 Probe、续租、派发或建立第二套状态机。
- 任务最多 500 条，最近消息最多 50 条；消息只返回元数据，正文走原 M2 权限接口。质量摘要每类返回最近最多 500 条，`truncated=true` 表示不完整，不能据此判定全工作流验收通过。
- 预算预留按政策分别列出，重叠作用域不能相加；未知金额保持 null/UNKNOWN。Session 状态复用原 Session 服务，Session 退出不能推导 Task 成功。
- `workflow_attempt_updated` 事件的 kind 包含 `artifact/path_reservation/quality_gate/quality_review/task_commit/integration_plan`；`workflow_updated` 的 `integration_operation` 表示集成汇总变化。均由 SQLite trigger 写入原 `change_log`，复用 CDC/SSE，事件只用于失效刷新。
- generation、Lease token、物理 Repo/Worktree/common-dir、Approval 引用及原始 Git 请求/Runner 日志不在质量 HTTP 投影中。日志仅显示摘要/截断标志/退出码。
- Preview 的 `conflictPaths` 保留 null 与空数组的原始表示，均表示无冲突，但它们参与不可变 digest。客户端必须原样回传完整 entries，不能排序或把 null 改成 `[]`。

## 部署装配与默认闭锁

daemon 只在启动时读取 `<AO_DATA_DIR>/quality-policies.json`，缺失或非法则没有可执行策略。文件为 `GatePolicy[]`，最多 64KiB/32 个策略，拒绝重复 ID、未知字段、尾随 JSON 及非法规则。`timeout`/`validFor` 使用 Go `time.Duration` 的 JSON 整数纳秒；例如 30 秒为 30000000000，1 小时为 3600000000000。工具需绝对路径，argv/env/cwd/平台/路径条件/日志上限均由可信宿主审核；HTTP 只能选择登记 ID。Runner 不是 OS 沙箱，不能宣称隔离同用户恶意进程。

数据与临时导出位于既有 AO 数据目录的 `quality` 子目录。复用唯一 ApprovalStore、Scheduler 和 Session 服务。没有自动创建/修改用户策略配置。

Push 当前只提供可信宿主注入的本地 bare 模拟映射；daemon 默认空表。没有生产网络 transport、远程目的地配置 UI 或真实远端验证。M5 不得把它标成可用的 GitHub Push，也不得复用 Apply 批准。

## 恢复与验收边界

Task Commit/Integration 写前持久化意图，回执不明只对账。已应用仓保留，partial/unknown 闭锁后续集成；不自动回滚、不续跑失败仓。新 Leader 必须先完成原恢复，旧 epoch/generation/revision 不再写。SQLite 与 Git 不提供跨系统原子提交。

Windows Preview 的 SV-02 保留两次 workspace-write 沙箱清理 FAIL；主控完全访问环境连续通过，统筹随后在沙箱外全新临时目录独立复跑 exit 0（47.181s），结束后目录为空。原失败根因未唯一确定，见 `m4-preview-cleanup-investigation.md`。全量 CLI 仍有既有 symlink privilege 失败，两项历史 Domain 失败保留。真实 Provider、生产数据、原生 OS 人工确认、远程 Push/Merge/部署、其他 OS 未测。M0-05 未获批准。
