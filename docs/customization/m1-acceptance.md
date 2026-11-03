# M1 独立验收结论与 M2 准入记录

> 日期：2026-11-03（Asia/Shanghai/Singapore UTC+08）
> 仓库：`E:\code\agent-orchestrator`
> 分支：`feat/multi-agent-desktop`
> 基线：`623871d`（M1 开始前的文档提交）
> **M1 核心后端验收：PASS（限定范围；保留已知 Windows 基线问题）**
> **M2 详细设计准入：允许；M2 真实 Provider 执行授权：未取得**

## 1. 验收口径与材料

本次对 [m1-plan.md](m1-plan.md) 的 M1-01～M1-05 和 [m1-report.md](m1-report.md) 的交付物/证据进行独立复核：核实实际文件存在、HTTP/CDC 与 Service 真实接线、迁移注册、代码生成产物，并在当前工作树重新运行目标测试与构建。M1 验收目标是「**零 Provider 的 Workflow 核心后端**」，不是「四种 Agent 已能端到端协作」。

验收不等于 Git 提交：当前全部 M1 实现仍处于 **dirty / 未提交工作树**，不得因 PASS 假定代码已经发布、已合并或可恢复到某个提交。交接给其他 Agent 前应妥善保存当前状态；未经授权不得 reset/clean/stash/强制 checkout。

## 2. 分项判定

| 编号 | 交付物 | 判定 | 证据 |
| --- | --- | --- | --- |
| M1-01 | Workflow DB/schema/Store 领域模型 | **PASS** | `workflow_core`、`workflow_idempotency`、`workflow_cdc` migration；sqlc 查询/生成代码；migration/Store 测试 |
| M1-02 | DAG 校验、环路/约束 | **PASS** | `domain/workflow_graph.go` 及专项测试；M1 报告中的 67 项顶层/45 项子用例扫描 |
| M1-03 | Service、CAS、Attempt 事务、幂等/恢复 | **PASS** | `service/workflow`、`store/workflow_*.go`；跨 Store 并发/过期租约/UNKNOWN 测试；Daemon 启动及周期恢复接线 |
| M1-04 | Workflow HTTP API、CDC/SSE、契约 | **PASS** | `controllers/workflows.go` 11 条路由、DTO、`apispec`、`cdc` event、CDC migration/回滚测试 |
| M1-05 | 回归、基线对照、生成物一致性记录 | **PASS（保留既有失败）** | [m1-report.md](m1-report.md) 的 pristine baseline 对照、`-race` 与两次生成物比对；本次独立构建与目标测试 |

## 3. 本次独立复测

### 3.1 核心包测试

从 `backend/` 运行：

```powershell
go test ./internal/service/workflow ./internal/storage/sqlite/store ./internal/storage/sqlite ./internal/cdc ./internal/httpd ./internal/httpd/apispec/... -count=1
```

**测试进程退出码 = 0**，各包结果：

| 包 | 结果 | 观察耗时 |
| --- | --- | --- |
| `internal/service/workflow` | PASS | 2.621s |
| `internal/storage/sqlite/store` | PASS | 53.465s |
| `internal/storage/sqlite` | PASS | 116.599s |
| `internal/cdc` | PASS | 2.221s |
| `internal/httpd` | PASS | 7.491s |
| `internal/httpd/apispec` | PASS | 0.473s |
| `internal/httpd/apispec/specgen` | PASS | 20.428s |

执行时外层 PowerShell 的两条临时环境赋值表达式因变量转义而产生 `=auto` / `=off` 非致命报错，但 `go test` 本身已继续执行并以退出码 0 完成。**本记录以实际 Go 进程结果为准，不把失败的环境赋值算作成功的设置操作。** 工作区使用了本机现有的 Go 工具链缓存。

### 3.2 编译与静态检查

在 `backend/` 执行：

```powershell
go build ./...
go vet ./internal/service/workflow ./internal/storage/sqlite/store ./internal/storage/sqlite ./internal/cdc ./internal/httpd/... ./internal/daemon
```

两项按短路执行，命令最终 **退出码 = 0**，没有编译/vet 错误。另已复核 `git diff --check` 未检出 whitespace 错误。

### 3.3 既有基线及生成物

- [m1-report.md](m1-report.md) §3～§4 记录已运行的 `-race`、`go test ./... -run Workflow`、`npm run sqlc`、`npm run api` 和前端类型检查；这些属于**原 M1 执行 Agent 的证据**，不冒称本次全部重新运行。
- 原报告使用 `623871d` pristine worktree 对照复现 8 条旧失败：`domain` 2 条、`controllers` 4 条、`daemon` 2 条；当前报告中的集合差 **M1 新增失败 = 0**。
- 35 个 `gen/*.go` 受 `core.autocrlf` 影响显示 modified，但报告已做字节对照；不可直接批量回滚。当前有真实变化的生成内容是 `gen/models.go` 与新增 `gen/workflow.sql.go`，以及 API spec/前端类型。
- 以上 Windows 失败尚未修复。**M1 PASS 表示未增加失败，不代表 Windows 全仓 `go test ./...` 为绿。**

## 4. 准入判定与限制

**结论：M1 核心验收 PASS，允许进入 M2 详细设计和“零 Provider”的本地假执行/Fake 实现。**

以下结论必须同时保留：
1. **M0 正式验收仍为 BLOCKED**：M0-05 预算尚未批准，A-02～A-15 的真实 Agent 能力/E2E 用例未执行。
2. **M2 实际四 Agent 运行不得自动开始**：Codex、Claude Code、OMP、DeepSeek Harness 的真实 Provider 会话/消息/Steer/Stop/Resume/权限拒绝/Token 验证仍需单独预算批准，不能以本次 PASS 代替。
3. **M1 遗留的 P2 风险移交 M2**：幂等响应快照、LeaseToken 脱敏日志、`cancelled` 完成语义、`claimed → succeeded` 缺 `started_at`、SQLite 单写者假设；详见 [m1-report.md](m1-report.md) §5.4、§8。
4. **历史 handoff 缺陷与本机前置条件**：`handoff_note.go` 两条旧测试失败、Codex 的 Windows 账号目录诊断和 Claude gateway 阻断，须作为 M2 前置项，不得自行修改真实配置。
5. **未提交工作树**：用户尚未授权 commit/push；交接保持现状，不执行破坏性 Git 操作。

后续的 M2 详细设计与 Agent 委派提示词属于独立文档交付，不纳入本次 M1 专项提交。
