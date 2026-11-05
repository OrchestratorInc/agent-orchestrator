# M2 第六轮独立离线复验

日期：2026-11-05（Asia/Shanghai）。工作区：`E:\code\agent-orchestrator`；分支：`feat/multi-agent-desktop`；入场 HEAD：`8b7d7901b52df1964babe233df3a4e27e7523c47`。

**独立结论：PASS（仅 M2 离线安全/执行链 G0）。F-01、F-02、F-03 的离线缺陷复验通过；本轮规定完整回归、Pricing 修复、生成与文件保护证据均已核对，可以进入 M3-02。此结论不是 M2 真实 Agent 完整验收通过，也不是 M3 PASS。**

本轮由未实施 M2 生产修复的独立验收者选择反例、阅读当前源码、运行定向复验并形成结论。主控与另一 QA 执行本轮完整回归，执行者在下文分别标明；没有把第五轮或 M3-01 的旧日志冒充本轮独立实跑。

## 1. 授权与验收范围

用户本轮明确恢复此前暂缓的第六轮离线复验，要求修复、复验满足条件后继续 M3。该指令没有批准真实 Provider、用户真实 AO 数据、配置修改、提交、推送、合并或部署。

- 对照 `m2-acceptance-round5.md`、`m2-remediation-round5.md`、`m2-regression-repair.md` 与 `m2-handoff.md` 核查 F-01、F-02、F-03。
- 测试采用真实临时 Git、真实 SessionManager、SQLite、HTTP/CLI/MCP 入口及 Scripted/Fake Driver；人工同意由 Fake Consent 注入，不启动 Windows Hello 窗口或真实 Provider。
- 本轮结论仅为 **M2 离线安全/执行链 G0 准入通过**。真实 Agent 完整验收、四真实 Harness、原生人工审批和 M0-05 仍单列，不能表述为完整 M2 已验收。
- 原 M3 文档和 CSV 保持保护。本验收者只新增本报告及系统 Temp 中的独立证据；未新增测试、未修改生产源码。

## 2. 独立源码结论

下列路径相对 `backend/internal/`；行号以本轮审查时文件为准。

### F-01：历史 BaseSHA

**定向复验支持关闭原缺陷。**

`session_manager/multi_repo_workspace.go:106` 将请求的 `plan.baseSHA` 传入 `WorkspaceConfig.BaseSHA`，`:173` 对工作树实际 HEAD 与请求 SHA 验回后，才记录 BaseSHA/CreationSHA/ProjectID。`adapters/workspace/gitworktree/workspace.go:1661` 拒绝已有分支偏离 pin，`:1697` 将 pin 直接作为新工作树 seed，不回退到已推进的默认分支。

本轮独立运行 `TestM2SessionManagerBindHonorsPinnedBaseSHA`：两个不同真实 Git 仓库使用不同历史 SHA，规划后主分支再次推进；检查真实工作树 HEAD 和 SQLite BaseSHA/CreationSHA。`TestM2FormalCLIThroughReopenedSQLite` 另外核查双仓 ProjectID、RepoPath、Git 注册清单及 SQLite 重开后的恢复，不产生第二份 Session。

### F-02：正式 Operator 入口与凭据边界

**离线调用链受限通过；原生人工正向不在本轮证明范围。**

- `daemon/workflow_auth_wiring.go:37` 每次启动装配内存 ApprovalStore、`ApprovalOperatorTrust` 与 `operatorconsent.New()`，没有铸造或读取 `operator.token` 的生产路径。
- `service/principal/operator_consent.go:28` 验证精确正文和摘要；`:49` 在 OS 验证前后重复核对状态、期限与绑定，并拒绝并发确认及已取消确认。
- `service/principal/operator_approval.go:472` 在锁内按期限、状态、method、完整 RequestURI 和原始 body digest 消费一次；`:760` 不接受无操作上下文的 Operator 授权。
- `httpd/controllers/operator_approvals.go:91,:129` 限制 loopback 并拒绝已声明 Agent 身份；删除 Agent 标记仍不能省略 OS Consent。`controllers/workflows.go:817` 在 handler 解码前绑定原始请求字节。
- `cli/operator.go:302,:413,:439` 依次创建、确认并以同一正文提交一次；正式 CLI 没有用文件 token 替代确认。`frontend/src/main/operator-ipc.ts:44` 核对主窗口和主 frame，第112行拒绝 Operator token 字段。
- `session_manager/workflow_context.go:98` 只重建仍有效的既有 M2 LeaseToken；`principal/verifier.go:95` 核对真实 Attempt、Workflow 与期限。`queries/workflow.sql:189,:234,:242` 在结算、处置及过期恢复时清除租约。

独立 FormalCLI 用例实跑了 Fake Consent 拒绝后不创建资源、批准后的 Bind、重复 Bind、MCP 协作、TaskRequest Decide、篡改分支后拒绝、恢复原现场及 SQLite 重开。它证明正式生产调用端与服务端边界的离线装配，不证明真实 Windows Hello 人工手势。

**M2 既有 MCP LeaseToken 通道仍是当前事实。** 本结论不批准它作为 M3 目标继续外发。M3 ADR-04 已补充复用 Session capability、服务端派生 binding、拒绝 raw lease 及受控迁移的具体设计，本验收者另作静态设计复核；后续 M3-05/07 仍须完成实现和反例验证，不能用 G0 通过替代。

### F-03：单仓/多仓实时 Git 与全执行链闭锁

**原始两项反例及真实单仓异常定向复验支持关闭原缺陷。**

- `session_manager/multi_repo_workspace.go:537,:568` 对显式 1..N 个 Repo 统一实时读取目录、Git 根、common-dir、分支、HEAD、祖先历史和 index/status；任一失败整体返回错误，不保留部分可信 HEAD。
- 同文件 `:649,:690` 通过持久化 Session binding 或 Spawn request intent 取得授权 Repo 集合，按 key、ProjectID、预期 BaseSHA、工作树引用和根路径逐项对齐。缺行不会因“仅一个仓库”而降级 Legacy。
- `service/execution/local_worktrees.go:20` 缺少实时验证器或无法证明 Workflow 归属时拒绝。`local_gateway.go:917` 将失效观察变为 UNKNOWN 并清空未经核验的 HEAD；`:817` 在 Resume 副作用前验证。
- `service/execution/coordinator.go:808` 对任何非空显式 Repo 绑定执行门禁；`:1202` LiveTarget 重新观察并检查 generation 与 Repo。绑定、重复绑定、Queue/Steer、Resume/Reconcile 不使用一次成功结果作为持续许可。
- `session_manager/chat_spawn.go:259,:583` 给初始及恢复 Controller 注入实时校验；`:515`、`manager.go:3582,:4040` 在恢复/接管前检查。`service/chat/controller.go:1605,:1752,:2241` 覆盖消息落库/派发和真实出队；`steer.go:551` 在 ack 后再次检查。
- `service/chat/history.go:1008` 在分支替换环境准备后校验工作区，替换 Controller 继承校验回调；不把准备环境结束前的旧结果当成启动许可。

独立真实 Git 用例覆盖删除记录、RepoKey/ProjectID/RepoPath 错绑、工作树替换、BaseSHA/CreationSHA 篡改、同历史独立仓库替换、Repo 丢失/替换、HEAD 回退、分支漂移与损坏 index；每次恢复原现场后验证 tracked/untracked dirty 字节仍在。执行门禁用例逐项核对 Queue、Steer、Resume、Reconcile、重复 Bind；队列用例用事件和同步 drain 屏障证明失效期间第二条未发送、durable queue 保留，恢复后恰好发送一次。

这些检查与之后的 OS/Git/Provider 操作不是跨系统原子事务；同用户外部进程在检查后继续篡改工作区的窗口仍存在。本报告不承诺消除任意 TOCTOU，也不把 Scripted Driver 当作真实 Provider。

## 3. 独立实跑证据

日志目录：`C:\Users\11459\AppData\Local\Temp\ao-m2-round6-20261105\independent`。每组保存 `.jsonl` 原始输出、`.meta.json` 命令/起止时间/真实退出码，以及从终态事件重算的 `.summary.json`。

独立核实环境：`go version go1.27.1 windows/amd64`、`git version 2.45.1.windows.1`。以下命令在 `backend/` 执行，经 `rtk proxy powershell -NoProfile` 包装，外层 `login=false`，均使用 `-count=1`；没有 `-skip` 或放宽断言。

QA 的 `frontend/environment.json` 记录 Windows build26200、PowerShell5.1、Node22.20.0、npm10.9.3、RTK0.42.4；本机Node不是CI指定的24，没有强制绕过engine要求。本轮前端结论仅对应本机工具链，不是Node24 CI通过证明。

```powershell
go test ./internal/service/execution -run '^(TestIndependentM2SingleRepoWorktreeRequiresLiveVerification|TestM2F03MissingExpectedRepoClosesGate)$' -count=1 -json -timeout 15m
go test ./internal/integration -run '^(TestM2SessionManagerBindHonorsPinnedBaseSHA|TestM2FormalCLIThroughReopenedSQLite|TestM2F03SingleRepoLiveGitRechecks|TestM2F03SingleRepoExecutionGates|TestM2F03QueuedTurnRechecksWorkspace)$' -count=1 -json -timeout 15m
```

| 证据前缀 | 时间（UTC+08） | 真实退出码 | 顶层终态 | 含子项终态 | 包/进程耗时 |
| --- | --- | --- | --- | --- | --- |
| `original-f03` | 14:41:57–14:42:01 | 0 | 2 PASS、0 FAIL、0 SKIP | 2 PASS、0 FAIL、0 SKIP | 0.249s / 3.294s |
| `selected-m2` | 14:42:01–14:45:08 | 0 | 5 PASS、0 FAIL、0 SKIP | 24 PASS、0 FAIL、0 SKIP | 182.935s / 187.486s |

合计 **7 个顶层 PASS、0 FAIL、0 SKIP**；含子项口径为26 PASS，两种口径不相加。五个集成顶层分别耗时78.17s、8.69s、41.85s、39.67s、14.28s，见 summary 中对应名称。

## 4. 本轮完整回归证据

证据根目录：`C:\Users\11459\AppData\Local\Temp\ao-m2-round6-20261105`。以下不是本验收者重新运行的命令；已直接读取本轮原始日志和退出码元数据，Go 计数另从 JSONL 重算。

| 执行者 / 命令 | 本轮证据 | 核对结果 |
| --- | --- | --- |
| 主控：`go test -p 1 ./internal/service/execution ./internal/service/workflow ./internal/service/capability ./internal/service/collaboration ./internal/service/principal ./internal/service/intervention ./internal/storage/sqlite/... -count=1 -json -timeout 30m` | `backend-core.jsonl`、`backend-core.summary.json` | exit0；975顶层 PASS、0 FAIL、0顶层 SKIP；含子项1526 PASS、2 SKIP；进程291.991s |
| 主控：`go test ./internal/integration -run TestM2 -count=1 -json -timeout 30m` | `m2-full.log`、`m2-full.summary.json` | exit0；33顶层/52含子项 PASS，0 FAIL/0 SKIP；包477.630s、进程491.162s；`source_drift=[]`，包含本验收者所选五项集成 |
| 主控：`go test -race ./internal/integration -run TestM2 -count=1 -json -timeout 30m` | `m2-race.log`、`m2-race.summary.json` | exit0；33顶层/52含子项 PASS，0 FAIL/0 SKIP，无race诊断；包713.488s、进程751.994s；`source_drift=[]` |
| 主控：`go test -p 1 ./internal/session_manager ./internal/httpd/controllers ./internal/daemon ./internal/service/chat ./internal/process ./internal/adapters/systemexec ./internal/mobilebridge ./internal/service/fsbrowser -count=1 -json -timeout 30m` | `backend-affected.log`、对应summary | exit0；8包全部PASS；2472顶层 PASS、46 SKIP，含子项4003 PASS、49 SKIP，0 FAIL；进程955.447s；`source_drift=[]` |
| 主控：`go test ./internal/httpd/apispec/... -count=1 -json -timeout 10m` | `api-tests.log`、对应summary | exit0；20 PASS、0 FAIL/0 SKIP；38.328s；`source_drift=[]` |
| 主控：`go test ./internal/cli ./internal/adapters/operatorconsent -run 'Test(MCPWorkflow\|Operator)' -count=1 -json -timeout 5m` | `operator-tests.log`、对应summary | exit0；13顶层 PASS、1 SKIP；含子项19 PASS、1 SKIP，0 FAIL；20.219s；`source_drift=[]` |
| 主控：`go build ./...` | `build.log`、`build.summary.json` | exit0；19.651s，日志无诊断，`source_drift=[]` |
| 主控：`go vet ./...` | `vet.log`、`vet.summary.json` | exit0；22.158s，日志无诊断，`source_drift=[]` |
| QA：`npm --prefix frontend test -- --maxWorkers 4` | `frontend/frontend-full.log`、`frontend-full.json` | exit0；633测试文件PASS，10877 PASS、17 SKIP、0 FAIL；Vitest1448.74s、进程1453.471s；完整套件仅限制并行worker数 |
| QA：`npm run frontend:typecheck` | `frontend/frontend-typecheck.log`、`frontend-typecheck.json` | exit0，50.100s；实际执行`tsgo --noEmit`，无类型错误 |
| 主控：`npm run api` | `api.log`、`api.summary.json` | exit0，19.962s；OpenAPI 与 TypeScript 生成完成 |
| 主控：`npm run sqlc` | `sqlc.log`、`sqlc.summary.json` | exit0，43.680s；本轮没有网络失败 |
| 主控：生成前后检查 | `generated-artifacts.json` | 44文件，`normalized_drift=[]`、`restored_line_endings_only=[]` |
| 主控：`git diff --check` | `git-diff-check.log`、`git-diff-check.summary.json` | 独立捕获exit0，0.247s；仅CRLF提示，无格式错误；新增未跟踪报告另行检查 |
| Pricing 修复者：`go test ./internal/pricing -count=1 -json` | `pricing/pricing-full.json`、`pricing/checkpoint.json` | checkpoint记录exit0；原始日志38顶层 PASS、0 FAIL、0 SKIP；包4.655s |
| Pricing 修复者：`go test ./internal/pricing/... -count=1 -json` | `pricing/pricing-tree.json`、`pricing/validation-command-evidence.json` | exit0；Pricing 38、catalogsync 15顶层 PASS，共53 PASS，0 FAIL/0 SKIP；包2.439s/3.922s，完整命令耗时未记录 |
| Pricing 修复者：`go test -race ./internal/pricing -count=1` | `pricing/pricing-race.txt`、checkpoint | checkpoint记录exit0；原始输出包PASS、5.106s，无race诊断 |
| Pricing 修复者：`go vet ./internal/pricing` | `pricing/pricing-vet.txt`、checkpoint | checkpoint记录exit0；日志无诊断 |

核心组的两项子测试跳过是 `TestOpenPreservesDataDirectoryCharacters/query`、`TestOpenRefusesToHideLegacyDatabase/query`，原始输出分别为 `db_path_test.go:22`、`:81: question mark is not a Windows filename character`；属于 Windows 文件名限制，不能计为通过。`sqlite/gen` 的包级 skip 表示没有测试文件，与上述两项子测试跳过分别记录。

`skipped-tests.json` 保留上述三组后端命令的52个含子项 SKIP及原始原因，已逐类核对：包括POSIX shell/模式、macOS tmux、Windows不支持的文件名、缺少原生symlink权限，以及1项要求 `-operator-native-consent` 的人工验证。受影响完整包的22项“fake connector is a shell script”也按SKIP列出。没有修改这些测试的跳过分支；原生文件symlink相关安全场景仍是本机未验证范围，不能用junction或已有PASS替代。

### Pricing 修复独立只读复核

本验收者阅读全部六个修改文件，并以 `.NET SHA256` 实算当前文件，与 `pricing/checkpoint.json` 六个冻结摘要逐一相符，证据为 `independent/pricing-source-check.json`。首次使用 `Get-FileHash` 时该 cmdlet 不可用，未把该次产生的空差异结果当作有效校验；替代实算已正常退出0。

- `pricing/cache_private_windows.go:57` 用固定父目录句柄调用 NtCreateFile，在创建时提交当前用户 owner 与受保护 DACL；允许当前用户、SYSTEM、Administrators，不允许 Everyone 继承授权。旧对象拒绝 reparse point 与非当前用户 owner，再按句柄修复 DACL。
- `pricing/cache.go:106` 对内容未变的 blob 也修复权限；`:118` 以同一 `os.Root` 创建、清理及发布临时对象，仍在全部 blob 完成后最后替换 manifest，保留失败时的旧清单。
- Windows 测试读取真实 owner/DACL，不再把 Go 的模拟 POSIX mode 当成 Windows ACL。非 Windows helper 仍断言文件0600、目录0700，没有增加跳过。
- `pricing/acl-red.json` 实际包含3个失败：旧目录向其他主体授权、manifest与blob继承父权限；`creation-mutation-red.txt` 与 `repair-mutation-red.txt` 分别记录移除创建时 SecurityDescriptor、移除旧 blob 权限修复后的失败。overlay 指向 Temp 中的替身文件，没有回退工作区源码。这些红灯证明断言覆盖生产权限控制。`validation-command-evidence.json` 从修复者原工具记录补录三条命令的exit1及对应chunkId；不是本验收者重跑，也没有从 FAIL 文本反推退出码。
- Pricing 全包与子树验证为不同运行，不能相加成不重复的测试总数。子树命令完整耗时未记录，不能把两个包耗时之和当作命令耗时。

未发现这六文件中尚未关闭的确定 P0/P1。非 Windows 只有构建记录，不能声称该平台运行验证通过；同用户进程篡改与管理员访问不属于本轮 DACL 的隔离保证。

测试规模申报：原 `fetch_cache_test.go` 为386行，现379行，较原文件减少7行，但仍超过单文件300行上限79行；本轮为保留已有完整缓存行为测试而未拆分。新增 Windows/非 Windows 测试文件分别169/24行，未超限。

独立七项定向运行发生在 Pricing 生产修复冻结之前；F-01/F-02/F-03 所审生产边界未因此改变，最终准入仍必须同时核对 Pricing 冻结后的全 TestM2 普通/race，不能把定向结果冒充最终整树回归。

### 最终文件保护

已读取 `frontend/frontend-summary.json`、`drift-summary.json`、`final-source-drift.json` 与 `snapshot-restoration.json`：1891个前端仓库文件最终零漂移。测试把 `ScriptRunPanel.test.tsx.snap` 的856行CRLF改为LF，QA先确认规范化内容相同，再恢复入场原字节，最终SHA256仍为 `b71ec68a419f8f7f5f7a4a70801dd8733c50155c8496446da4cd04b61a343691`。未还原的变化仅为Git忽略的Vitest results缓存。

主控 `protection-final.json` 与 `round6-final-files.json` 记录5881个入场文件到5886个最终文件：只修改Pricing的2个既有文件、M3 ADR-04/计划/CSV；新增Pricing的4个文件与本报告；删除0，前端漂移0，原README/M3设计原字节不变。独立复读最终HEAD仍为 `8b7d7901b52df1964babe233df3a4e27e7523c47`。本报告定稿只继续修改其自身，不改变上述生产源码冻结范围。

## 5. 最终门禁与未验证边界

| 验收项 | 独立判断 |
| --- | --- |
| F-01 历史pin与持久归属 | 离线关闭；真实双Git、SQLite与恢复证据支持 |
| F-02 正式授权链 | 离线关闭；一次性原始请求审批、CLI/MCP、拒绝与恢复通过；原生人工正向未验 |
| F-03 单仓/多仓及执行恢复闭锁 | 离线关闭；原始两反例逆转、真实Git异常/dirty保留、派发与队列、完整普通/race通过 |
| Pricing Windows权限失败 | 已修复并复核；真实DACL红绿和变异证据，原有mode失败没有被简单跳过 |
| 规定完整回归与文件保护 | 本轮通过，跳过项及平台差异按§4明列；没有删除或覆盖原有用户改动 |
| M3 G0 | **PASS（离线准入）**；可以从M3-02继续，尚未实施的M3能力仍需各自验证 |

本轮限定范围内未发现尚未关闭的确定 P0/P1。非阻断建议：`service/execution/coordinator.go:647-651` 的旧英文注释仍描述单仓空 worktree 例外，与第808–812行的当前严格门禁不符；建议改为显式 Repo 一律验证。此处是注释偏差，独立反例已确认实际代码闭锁。

仍未验证：M0-05、真实 Provider/四真实Harness、原生 Windows Hello 人工正向、原生文件symlink相关跳过场景、其他原生操作系统、Node24 CI、全仓 CI/全仓 `go test ./...`、真实安装/签名/发布。离线通过不消耗或批准真实Provider预算，也不授权使用用户AO数据、修改受保护配置、commit/push/merge/deploy。

第五轮的NOT ACCEPTED保留为历史事实，本报告仅新增第六轮独立离线结论。M2真实Agent完整验收继续按`m2-handoff.md`单独申请与执行，M3最终验收仍由相应实施完成后的独立报告决定。
