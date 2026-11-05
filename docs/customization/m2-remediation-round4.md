# M2 第四轮阻断项修复报告（F-01 / F-02）

日期：2026-11-05（UTC+08）。工作区：`E:\code\agent-orchestrator`；分支：`feat/multi-agent-desktop`；HEAD 保持 `a6c284a`。

**本轮 F-01/F-02 修复及规定的离线门禁已通过，可申请第五轮独立复验；这不是 M2 最终验收 PASS 声明，不进入 M3。** 扩大的 HTTP 全套测试仍有已在旧 HEAD 实跑复现的 Windows 失败，详见第 5 节，未将其标为 PASS。

未 commit、push、merge、部署。未启动真实 Codex、Claude Code、OMP、DeepSeek Provider 驱动或使用其凭据；M0-05 预算仍未批准。测试使用临时仓库、SQLite、Scripted Driver；没有修改真实 `~/.ao`、`.codex`、`.claude` 配置或强制清理用户工作树。

## 1. 基线与证据

日志目录：`C:\Users\11459\AppData\Local\Temp\ao-m2-round4-20261105`。下文日志名均相对此目录。

- `entry-files.json` 保存入场 dirty/untracked 文件 SHA256。入场已有大量 M2、BaseSHA pin、Operator 代码；本轮没有把全部 Git diff 认领为新实现。
- `current-delta.json` 保存相对入场的变化清单及 SHA256。入场干净的已跟踪文件后来变化，标为 `changed-clean-at-entry`，不混作新建文件。
- PASS 表示真实执行并通过；FAIL 表示实际失败；BLOCKED 表示环境/依赖阻断；NOT_TESTED 表示未执行。
- 收尾曾出现另一写入者的修改。用户明确确认“由当前会话接管，其他写入已停止”后，已整合有效差异、修复冲突并重新执行最终门禁。当前无未解除的写入归属 BLOCKED。
- API 曾遇到 Windows 文件占用错误；最终在无并发 Go 编译时串行重生成，API/sqlc 摘要均稳定。中途失败不冒充成功。

## 2. F-01：历史提交驱动创建，持久化每仓库归属

入场已有 `gitworktree/basesha.go`、`basesha_test.go`、`multi_repo_basesha_test.go` 等 pin 实现，原 `TestMultiRepo` 集入场已通过（`f01-entry.log`，144.287s）。

本轮新增三个真实 Git 反例，旧代码全部失败（`f01-red.log`）：

```text
非法第二仓库 pin 仍触发了 2 次创建
HEAD 核验失败后遗留 Worktree
替换仓库的恢复结果 = <nil>，应拒绝归属不一致
FAIL .../session_manager 27.052s
```

修复内容：

1. 创建第一棵 worktree 前验证全部非空 BaseSHA：完整 40 位 hex，且在对应仓库能解析为该 commit；适配器仍再次验证。非空 pin 不刷新默认分支，空值保留原行为。
2. 使用请求 SHA 创建，读取真实 HEAD 校验；每个创建产物先纳入补偿列表，再校验，保证当前仓库失败也回滚。dirty worktree 不强制删除。
3. `session_worktrees` 新增 `project_id`、`repo_path`，保存每仓库授权归属。恢复不再套用根 Session ProjectID，也不从可能被替换的现场目录反推身份。
4. 新迁移 `20261104170421478_session_worktree_repo_identity.sql` 通过正式命令创建并加入 ledger；查询修改后运行 sqlc，未手改生成代码。
5. 真实 Manager 在恢复/执行观察时校验路径、Git common-dir、分支、CreationSHA 祖先关系和 Git status。缺失、换库、分叉或索引不可读均闭锁；合法 dirty 成果和 CreationSHA 之后的提交保留。
6. ExecutionGateway 重新向 Manager 求证；失败不报告 ready。Coordinator 同时比较期望 ProjectID。
7. Reconcile 对已绑定会话重新观察，失败保留 SessionID 并冻结；恢复已有会话只重锚 generation、推进状态，不再次获取首次绑定，修复了 `WORKFLOW_BIND_CONFLICT` 自归属冲突。

新增反例修复后通过（`f01-green.log`，23.286s）。最终 `TestMultiRepo` 全集通过，140.681s；适配器 pin 用例通过，27.274s。覆盖历史 HEAD~1、规划后主分支前进、双独立仓库不同 SHA、非法/不存在/其他仓库 SHA、第二仓库失败补偿、dirty 保留、重复 Bind、换库拒绝与重启恢复。

**兼容限制：** 缺乏持久化归属的旧 M2 多仓库记录保守拒绝恢复；本轮不从现场目录自动补身份。此类旧记录需独立核实后迁移。

## 3. F-02：正式客户端与逐操作人工审批

正式路径：

```text
CLI / Electron Main
 → 创建审批（method + 完整 RequestURI/query + exact UTF-8 payload + SHA256）
 → daemon 调用 OS 验证
 → TTL、取消和状态再次检查
 → 单次消费审批
 → Bind / Decide / Resolve / Reconcile / Resume
```

- 正式 CLI 提供五个 verb，另有闭集内 `operator request --method --path --body-file`；不读取 Token。正式 CLI 测试使用生产入口，不用测试专用 Header 注入替代客户端。
- Main IPC 校验主窗口、顶层 frame、来源、五个精确路由，审批与执行提交同一正文，禁止重定向携带审批。同步 preload/bridge，最终行为测试 7 项通过。
- 生产 `ApprovalOperatorTrust` 拒绝 Token 直接授权；不通过读取/删除 `operator.token` 认定 Human 身份。旧 Token 无法替代 OS 审批。
- Agent Capability + Attempt Lease 身份优先级保留，不能带上审批伪装 Operator。CLI 环境标记仅作提前拒绝，清空标记仍需 OS 验证。
- 缺 verifier、拒绝、取消、超时、过期、重放、正文/URI/query 不匹配均闭锁；审批重启后失效。
- 删除可枚举审批列表；单条状态要求随机 ID 与配对码，不回显码或正文。正文仅在人工核对时展示，不写入普通响应、Prompt、MCP 结果或日志。

独立复核发现并修复的额外漏洞：

| 问题 | 红灯证据 | 最终结果 |
| --- | --- | --- |
| 本地进程枚举已确认 ID 并抢兑 | `list=200 redemption=200 body={"messages":[]}` | 删除列表 handler/DTO/spec；HTTP 攻击用例验证 405、不泄漏 ID，原申请者仍可消费；PASS |
| PowerShell 用户模块伪造 `Verified` | 临时 Utility 模块输出 Verified 并退出，未触发 OS；`WouldVerifierAccept=true` | 系统程序/工作目录、最小 Env、禁自动加载用户模块、绝对系统模块/程序集；真实 Verifier 注入负例 PASS |
| 原生窗被隐藏，人工流程超时 | 平台诊断：`HideWindow=true → visible=False`；两次人工测试超时 | 改用 `CREATE_NO_WINDOW` 仅隐藏控制台；真实人工 Windows Hello PASS |

`review-final.log` 记录只读复核：原生注入对照、精确 method/path/body/query、Agent 优先、重放、枚举攻击和 spec drift 全部通过。源码扫描测试与会误拒合法正文的关键词扫描已移除。

### Windows 实际人工验证

用户确认可以参与后执行：

```powershell
rtk proxy go test ./internal/adapters/operatorconsent -run '^TestOperatorNativeConsentManual$' -count=1 -v -args -operator-native-consent
```

`verified-windows-hello-live.log`：

```text
--- PASS: TestOperatorNativeConsentManual (38.36s)
PASS
ok .../operatorconsent 40.244s
```

前两次 FAIL 保存在 `windows-hello-live.log` 和 `resolved-windows-hello-live.log`，错误为 `context deadline exceeded`。随后通过无授权的窗口可见性诊断定位 HideWindow，并完成上面的真实正向验证。没有由 Agent 输入 PIN 或用假 verifier 替代这条人工结果。

**保证范围：** HTTP 与受控 OS 审批流程信任 daemon/helper 代码和进程完整性。未证明抵御任意同 SID 进程的内存注入、程序替换或 UI 篡改；WinForms 详情窗不是安全桌面。若验收要求覆盖这些攻击，需另行隔离 Agent 权限或提供受保护 broker。非 Windows 原生审批目前闭锁，未声称跨平台人工授权已实现。

## 4. 完整离线端到端证据

`m2_formal_cli_e2e_test.go` 使用真实 Manager、SQLite、Git Adapter、两个独立临时仓库、HTTP Router、正式 CLI/MCP 入口及 Scripted Driver，仅在自动测试中替换 OS 验证边界。

覆盖 Workflow → Bind/重复 Bind → 双仓历史 SHA 与归属 → MCP 消息/Task Request → Decide/Resolve 和审计 → 篡改后 Reconcile 拒绝 → 修复现场 → Resume/Reconcile → 重开 SQLite。重复与恢复直接比较 `git worktree list --porcelain`。

旧 FullFlow 保留 Queue/Steer/Stop generation fencing。替换 Controller 后只读等待异步活动投影，不写状态伪造 ready。StopConfirmed 的重启段精确断言 200、not_unknown/not_resumable、状态不变且不重启 Controller；活跃恢复由 CLI 与 Manager 重启用例验证。

最终输出：

```text
go test ./internal/integration -run TestM2 -count=1 -v
28 个顶层用例 PASS，0 FAIL
ok .../integration 135.336s

go test -race ./internal/integration -run TestM2 -count=1
ok .../integration 210.130s
```

日志：`verified-integration.log`、`verified-integration-race.log`。这是生产入口在测试进程内的验证，不是最终安装包或真实 Provider 验收。

## 5. 最终验证结果

Go 命令在 `backend`；npm 命令在根目录，IPC 测试在 `frontend`。实际 Shell 采用 `rtk proxy pwsh -NoProfile`。

| 检查 | 结果 | 实际证据 |
| --- | --- | --- |
| 七核心包完整测试，包含 workflow | PASS / 0 | `verified-core.log`，七包均 ok |
| M2 集成 / race | PASS / 0 | 135.336s / 210.130s，见上 |
| Session Manager 全部 TestMultiRepo | PASS / 0 | `final-multirepo.log`，140.681s |
| Git Adapter pin 负向/正向测试 | PASS / 0 | `final-git-pin.log`，27.274s |
| controllers -run TestM2 | PASS / 0 | `verified-controllers-m2.log`，2.723s |
| daemon -run TestM2 | PASS / 0 | `verified-daemon-m2.log`，29.445s |
| 正式 CLI/MCP + 原生注入负例 | PASS / 0 | `verified-operator-cli.log`；CLI 9.496s、原生包 2.994s |
| Windows Hello 人工正向 | PASS / 0 | `verified-windows-hello-live.log`，40.244s |
| go build ./... | PASS / 0 | `verified-build.log`，无诊断，exit_code=0 |
| go vet ./... | PASS / 0 | `verified-vet.log`，无诊断，exit_code=0 |
| npm run frontend:typecheck | PASS / 0 | `verified-typecheck.log`，tsgo --noEmit |
| Main IPC 行为测试 | PASS / 0 | `resolved-operator-ipc.log`，最终 7 passed |
| npm run api，两次生成摘要比较 | PASS / 0 | `verified-api.log`，api changed files: 0 |
| npm run sqlc，重生成摘要比较 | PASS / 0 | `verified-sqlc.log`，sqlc changed files: 0 |
| git diff --check | PASS / 0 | `verified-diff-check.log`；仅 Git 换行提示 |
| 扩大的 go test ./internal/httpd/... | **FAIL / 1（旧 Windows 基线）** | `verified-httpd-full.log`；下表四项 |
| 旧 HEAD 对照四项 Windows 测试 | **FAIL / 1** | `baseline-httpd-windows.log`，6.134s，相同四项复现 |
| 最终未解除的环境/依赖阻断 | BLOCKED：无 | 写入归属已由用户确认；生成占用问题经串行复跑解除 |
| 真实 Provider、完整 Electron/安装包、非 Windows、全仓 go test ./...、发布/部署 | NOT_TESTED | 未运行，不能从离线结果推导通过 |

HTTP 全套剩余失败与隔离导出的 `a6c284a` 完全同名同因：

| 用例 | 实际失败 |
| --- | --- |
| TestListDirsRejectsRelativeAndMissingPaths/file | 404/FS_NOT_FOUND，预期 400/FS_NOT_A_DIRECTORY |
| TestBridgeStatusConcurrentSecurePairing | rename mobile.json: Access is denied |
| TestArtifactFileRouteServesOnlyRegularFilesInsideTheDirectory | symlink: A required privilege is not held by the client |
| TestProjectsAPI_Clone | 400 INVALID_GIT_URL，预期 201 |

基线通过 `git archive a6c284a backend go.work go.work.sum` 导出到随机临时目录，没有切换或清理当前 worktree。临时路径记录在 `baseline-path.txt`。初次扩大回归还发现了 M2 nil recordingConsent 夹具 panic，已保留有效修复并通过最终 M2/HTTP 复跑；没有删除该用例。

## 6. 改动清单与测试规模

下列是相对入场有内容变化或新增的文件；其中包括已接管并整合的有效修改。入场已有且本轮未变化的 BaseSHA 文件不计入。精确 SHA256 见 `current-delta.json`。

```text
backend/internal/adapters/operatorconsent/consent_environment_windows_test.go
backend/internal/adapters/operatorconsent/consent_other.go
backend/internal/adapters/operatorconsent/consent_windows_test.go
backend/internal/adapters/operatorconsent/consent_windows.go
backend/internal/cli/operator_request.go
backend/internal/cli/operator_test.go
backend/internal/cli/operator.go
backend/internal/daemon/daemon.go
backend/internal/daemon/workflow_auth_wiring.go
backend/internal/daemon/workflow_operator_auth_test.go
backend/internal/domain/project.go
backend/internal/httpd/api.go
backend/internal/httpd/apispec/openapi.yaml
backend/internal/httpd/apispec/specgen/build.go
backend/internal/httpd/controllers/operator_approvals_consent_test.go
backend/internal/httpd/controllers/operator_approvals_fixture_test.go
backend/internal/httpd/controllers/operator_approvals_m2_test.go
backend/internal/httpd/controllers/operator_approvals_metadata_test.go
backend/internal/httpd/controllers/operator_approvals_refusal_test.go
backend/internal/httpd/controllers/operator_approvals_security_test.go
backend/internal/httpd/controllers/operator_approvals.go
backend/internal/httpd/controllers/workflows_m2_authz_test.go
backend/internal/httpd/controllers/workflows_m2_test.go
backend/internal/httpd/controllers/workflows.go
backend/internal/integration/m2_e2e_test.go
backend/internal/integration/m2_formal_cli_e2e_test.go
backend/internal/integration/m2_full_flow_e2e_test.go
backend/internal/integration/m2_operator_fixture_test.go
backend/internal/integration/m2_session_manager_e2e_test.go
backend/internal/service/execution/coordinator_test.go
backend/internal/service/execution/coordinator.go
backend/internal/service/execution/local_gateway.go
backend/internal/service/execution/repo_identity_test.go
backend/internal/service/principal/operator_approval_file_test.go
backend/internal/service/principal/operator_approval_fixture_test.go
backend/internal/service/principal/operator_approval_test.go
backend/internal/service/principal/operator_approval_trust_test.go
backend/internal/service/principal/operator_approval.go
backend/internal/service/principal/operator_consent_test.go
backend/internal/service/principal/operator_consent.go
backend/internal/service/principal/operator_token_manager.go
backend/internal/service/principal/operator_token.go
backend/internal/service/principal/operator_uri_test.go
backend/internal/service/principal/principal.go
backend/internal/session_manager/manager.go
backend/internal/session_manager/multi_repo_round4_test.go
backend/internal/session_manager/multi_repo_workspace.go
backend/internal/storage/sqlite/gen/models.go
backend/internal/storage/sqlite/gen/workspace.sql.go
backend/internal/storage/sqlite/migrate_burned_versions_test.go
backend/internal/storage/sqlite/migrations/20261104170421478_session_worktree_repo_identity.sql
backend/internal/storage/sqlite/queries/workspace.sql
backend/internal/storage/sqlite/store/session_worktree_store.go
docs/customization/m2-remediation-round4.md
frontend/src/api/schema.ts
frontend/src/main.ts
frontend/src/main/operator-ipc.test.ts
frontend/src/main/operator-ipc.ts
frontend/src/preload.ts
frontend/src/renderer/lib/bridge.ts
frontend/src/renderer/test/setup.ts
```

新测试文件均不超过 300 行；新 HTTP 审批脚手架 396 行，在 400 行限额内。继承的超限测试按当前文件申报：

| 文件 | 行数 / 超出 300 行 | 原因、对应验收 |
| --- | --- | --- |
| integration/m2_e2e_test.go | 1753 / 1453 | 保留原 M2 协作/审批/队列覆盖，局部迁移审批 fixture |
| integration/m2_full_flow_e2e_test.go | 1755 / 1455 | 真实 Manager 全流程、generation 与重启；保留原覆盖 |
| integration/m2_session_manager_e2e_test.go | 1244 / 944 | 双仓真实恢复/丢失/dirty；修正夹具归属 |
| service/execution/coordinator_test.go | 2288 / 1988 | 既有状态机测试，仅补正确 ProjectID |
| controllers/workflows_m2_authz_test.go | 851 / 551 | 既有权限矩阵，修正审批读取夹具 |
| controllers/workflows_m2_test.go | 400 / 100 | 既有 M2 HTTP fixture 迁移 |
| sqlite/migrate_burned_versions_test.go | 496 / 196 | 既有迁移 ledger，仅追加新迁移 |

表中路径相对 `backend/internal/`（controllers 相对 `httpd/`，sqlite 相对 `storage/`）。入场仅保存哈希，没有保存这些文件的逐行副本，因此不虚报精确新增行数。新 trust 测试已拆到 221/108 行，正式 CLI E2E 210 行、审批 fixture 110 行、F-01 反例 92 行。

## 7. 第五轮独立复验命令

在 `backend`：

```powershell
rtk proxy go test ./internal/ports ./internal/service/execution ./internal/service/intervention ./internal/service/capability ./internal/service/principal ./internal/service/collaboration ./internal/service/workflow -count=1
rtk proxy go test ./internal/session_manager -run TestMultiRepo -count=1 -v
rtk proxy go test ./internal/adapters/workspace/gitworktree -run 'Test.*(BaseSHA|Pinned|Pin)' -count=1 -v
rtk proxy go test ./internal/integration -run TestM2 -count=1 -v
rtk proxy go test -race ./internal/integration -run TestM2 -count=1
rtk proxy go test ./internal/httpd/controllers -run TestM2 -count=1
rtk proxy go test ./internal/daemon -run TestM2 -count=1
rtk proxy go test ./internal/service/principal ./internal/cli ./internal/adapters/operatorconsent -run 'TestOperator|TestApproval|TestTokenManager|TestMCPWorkflow' -count=1 -v
rtk proxy go test ./internal/httpd/apispec/... -count=1
rtk proxy go build ./...
rtk proxy go vet ./...
```

等待上述 Go 进程结束后，在根目录串行执行生成命令，比较重生成前后摘要，不能手改生成文件：

```powershell
rtk proxy npm run api
rtk proxy npm run sqlc
rtk proxy npm run frontend:typecheck
rtk proxy npm --prefix frontend test -- src/main/operator-ipc.test.ts
rtk git diff --check
```

人工在场时运行第 3 节 opt-in 原生用例。复验还应确认错误 Token/无授权/Agent 租约不能冒充 Operator、过期/重放/正文与 query 篡改被拒绝、重启废止审批、模块注入被拒绝、旧 generation 不能 Queue/Steer/Stop。真实 Provider 继续受 M0-05 限制。

本轮在这里停止，由独立验收 Agent 作第五轮判断。
