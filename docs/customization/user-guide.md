# 多 Agent 工作流使用指南

本指南描述当前分支的实现。M6 尚未通过完整验收；真实 Provider、Windows Hello、安装升级及 SV-06 桌面队列验证的状态见 [m6-report.md](m6-report.md)。

## 创建与规划

1. 在桌面进入工作流工作台，创建规划任务，填写标题与目标。
2. 选择已经登记的本地仓库，为每个仓库填写准确的基线 SHA 和目标分支。基线是不可变提交，不用分支名替代 SHA。
3. 选择原生配置并填写模型，或选择宿主已经登记的供应商配置。当前自动只读 Planner 使用 Codex。
4. 通过桌面原生操作确认创建并激活。Planner 受只读限制；它输出候选计划，不直接修改开发工作树。
5. 在计划页查看候选，按需要调整任务、Agent、模型、依赖、仓库范围、允许路径及停用任务，然后保存新候选。也可手工添加候选任务。
6. 确认精确候选摘要后，单独恢复调度。确认计划本身不自动恢复执行；旧任务收尾及资源释放完成后，新任务才可准入。

审批失效、目标或正文变化时重新检查并审批。请求超时后使用“重试原请求”读取或重放原身份，不重复创建新的请求来猜测是否成功。

## 供应商选择

“原生配置”沿用该 Agent 现有配置。平台供应商引用首版仅支持 Codex；Claude Code、Oh My Pi、DeepSeek Harness 保留原生路径，不接受非空平台引用。

选择平台项后，模型和 effort 随引用固定并保存。effort 为空表示使用供应商默认策略；供应商可以返回实际默认档位，持久任务仍保留空策略。创建、自动候选、开发任务和恢复使用同一选择；任务详情显示引用 ID。需要改变时通过候选编辑或 Stop & Replan 重新选择。清除选择会清空固定模型/effort 并恢复手工输入，随后按原生方式填写模型。

登记表变化、凭据缺失或验证失败会拒绝执行，不会使用默认账号代替。已采用固定供应商的 Session 不支持直接切换原生账号、终端或其他 Agent，防止工作流脱离已批准路由。

### 宿主登记格式

由可信宿主管理者在实际 `AO_DATA_DIR` 下维护 `provider-profiles.json`。应用只读取该文件，UI 和 Agent 没有写配置接口。本指南中的示例未写入任何真实环境：

```json
[
  {
    "id": "my-provider",
    "name": "我的供应商",
    "harness": "codex",
    "baseUrl": "https://api.example.com/v1",
    "model": "provider-model-id",
    "effort": "high",
    "contextWindow": 64000,
    "credentialEnv": "AO_WORKFLOW_CREDENTIAL_MY_PROVIDER"
  }
]
```

`credentialEnv` 是 daemon 启动环境中的专用变量名，必须以 `AO_WORKFLOW_CREDENTIAL_` 开头并包含非空的大写字母、数字或下划线后缀，文件中不能放密钥。`OPENAI_API_KEY` 等公用原生变量不能登记为平台凭据，仍按现有规则继承（包括 profile 进程）；它们不属于平台凭据隔离命名空间。所有平台专用变量及固定转发变量在父环境与会话 overlay 合并后按大小写无关规则清除，已验证的平台启动仅收到一个规范化的所选平台密钥。不要把平台密钥同时复制到原生公用变量中，否则会按原生变量的继承规则传递。

daemon 启动时先将专用凭据收进只读内存，再从自身可继承环境移除，避免普通 Reviewer、Git、预检或其他子进程默认继承。修改宿主启动环境后需要重启 daemon 才会轮换凭据；这不会修改系统环境、父进程或原生账号文件。既有持久宿主仍须匹配原凭据身份，轮换后不能直接接管旧宿主。

旧登记若使用任意变量名，需由宿主配置者改为专用变量并在计划中重新选择供应商；引用摘要会变化，旧任务不会静默采用新配置。HTTPS 地址不能嵌入用户名、密码、query 或 fragment。上下文窗口可省略；effort 只接受当前映射的 `none/minimal/low/medium/high/xhigh` 或空值，仍需实际模型支持。未知字段、重复 ID、坏项或额外 JSON 会使整表被拒绝；不要加入权限、沙箱、hook、MCP、命令或任意环境覆盖。

应用生成 `ID@摘要` 引用，不需手写摘要。任何登记字段变化，包括显示名、模型、地址或凭据变量名，都会改变摘要；原任务须显式重新选择。

执行前以非生成式 `GET /models` 验证：同一端点必须拒绝无凭据和随机错误凭据，而接受当前凭据且返回目标模型。公开目录不构成授权，无法提供该证据的端点当前受限。此检查不证明 Responses 推理额度、计费或生成质量。列表展示本身不会发出此探测。

## 双视图与执行中交互

工作台的任务和 DAG 指向现有 Attempt/Session。进入对应会话查看对话、执行轨迹、终端和 Diff；返回工作台不会创建另一个 Agent。

- **Steer**：向当前正在执行的轮次补充指令。仅在当前 Session/generation 的证据支持时可用；不可用时改用 Queue。
- **Queue**：排到后续轮次。查看持久回执；在对应现有会话中管理队列，取消与编辑是否生效以服务端回执为准。
- **Stop & Replan**：先请求停止，等待 `stop_confirmed`，再选定 Planner 模型/供应商并创建重规划任务。工作树成果保留；确认新计划后再恢复调度。

重规划目前使用 Codex：停止 Codex 任务时沿用该任务的模型、effort 和固定供应商；停止 Claude、Oh My Pi 或 DeepSeek 原生任务时，默认采用现有 Codex Planner 的完整选择。没有 Codex Planner 可继承时保持空选择，需要先填写模型或选择供应商。

`UNKNOWN`、`stop_unknown` 或断线不是停止成功。不要因界面未刷新而手工再起一份任务。

## 协作、并发与预算

Agent 可以在所属工作流内发送消息、交换结果和申请任务。新任务、资源增加以及分配均由统一调度与授权处理；Agent 不能自行获得 Operator 权限。

默认并发为 3，全局上限为 10，实际准入还受依赖、CPU/RAM、单 Adapter 限制和预算影响。资源不足时等待资源恢复，不通过降低保护阈值使当前任务勉强启动。

在预算页选择全局、Workflow 或 Task 范围，查看政策与已预留金额，再经审批保存变更。重叠范围的预算约束分别生效，不把它们相加成账单。未知 Token、价格或成本保持未知/估算；已有预留不代表最终费用。

## 审查与集成

在质量页选择宿主批准的质量策略，冻结工件后运行 Gate 和独立 Review。只有与当前工件匹配的成功证据才可用于任务分支提交。修改工作树后需要重新冻结和验收，旧证据不能覆盖新修改。

集成先预演，再对准确摘要及逐仓条目分别审批应用。出现 `PARTIAL` 或 `UNKNOWN` 时查看每仓事实并对账，不能把已成功仓重做或自动回滚。当前没有可交付的生产远程 Push transport；本地模拟不代表可直接推送 GitHub。

## 重启和恢复

重启后先等待 daemon 取得领导权并恢复持久占位。系统核对原 Session、Worktree、Attempt、代际和未明操作后再决定恢复。不要删除未知状态的工作树或准入记录。

供应商引用或凭据变化时，旧宿主不会被新配置接管；先确认旧进程与任务状态，通过受控停止/重规划处理。无法证明安全恢复时保留暂停，按 [故障手册](troubleshooting.md) 收集最少必要证据。

## Windows 本地仓库与用量检查

本地仓库克隆地址使用标准 file URL，例如 `file:///C:/work/repo.git`；路径中的空格应按URL编码。已有目录不会被静默覆盖，准备和注册失败后仍按准备ID核验清理，已注册项目及用户修改保留。

Claude续接用量由会话目录及cwd归属发现并幂等入账；费用仍取决于有效定价目录。当前源码检出的定价blob存在CRLF字节差异，C盘对照通过不代表主仓或已发布安装包费用已验收；配置修复尚待授权，生产摘要校验继续严格生效。

## Windows 进程与私有账户目录

Windows宿主登记或预览进程标识超出系统PID范围时，系统会拒绝该标识，避免误认其他进程。预览进程的身份无法核实时，不会据该标识终止其他进程树。

Codex私有账户目录仍要求可信所有者和受保护访问权限；拒绝结果应按故障手册核查，不要放宽目录权限求通过。本批通过的是自动化平台边界检查，真实Provider、桌面交互及安装包使用仍维持原验收状态。


## Windows 账号、工作区与导入边界

Reviewer及模型发现目录会按Windows实际访问权限保护。登录期间若设备凭据被外部进程替换，重认证保留外部凭据并返回可重试，已保存账号和待验证凭据分别保留；本批以假账号自动化验证，真实Provider及桌面登录仍未验收。

工作区存在但无法读取时返回权限错误，不再只因路径存在就允许打开。项目导入会拒绝默认AO状态路径，以及自定义数据目录本身、其子目录和包含该数据目录的父目录；相邻的普通项目仍可导入。

本批不能解除M6未通过状态：symlink权限、分离宿主交互、打包与真实环境验收仍遵循执行报告中的原边界。

## Windows 进程查询暂时失败

系统暂时无法查询进程时，应用会保留原会话和启动占位，避免重复启动。状态查询会继续核验原daemon的健康响应；无法核实时保留错误，不把查询失败报告为已经停止。停止请求的应答也不等于进程已退出，原运行记录仍在且退出无法确认时会返回错误。

等待系统恢复后重试原查询或操作。不要手动删除原宿主锁、描述符、PTY注册文件或running.json来绕过占位。确认原进程退出后，既有恢复和清理路径继续工作；进程身份无法确认时，内存计量也不能作为原宿主仍存活的证据。

## Chrome 桥接的验证边界

SV-M6-08 修复了 Windows 自动测试误用真实 Chrome 注册表的问题；正常产品的安装和移除行为没有变化。本次完整包、race 与 lint 通过，不代表已经在真实 Chrome 中完成桥接安装或浏览器交互验收。

此前测试留下的 Chrome 注册项仍指向旧测试目录，目前没有可信的污染前备份，本批未恢复或删除该项。若桥接不可用，请按[故障手册](troubleshooting.md)保存当前注册身份，确认原安装后再安排恢复；不要把自动测试通过当成旧注册已经修复。M6 整体仍未通过。

## 本地模型目录与启动标识的修复边界

Windows下读取Oh My Pi本地模型/凭据目录时，现在保留盘符和特殊字符的文件路径语义。遥测安装标识的读取与空文件恢复在daemon和桌面端共用锁，降低并发启动时的文件共享冲突；锁无法安全取得或清理时仍返回错误。

这些改动已有本地自动化证据，M6整体仍未通过。真实Provider、安装升级和桌面交互保持原验收边界；此前留下的Chrome注册指针和Claude测试记录没有清理。


### 收尾串行审计失败（2026-11-08）

首轮`gates-close.py`返回exit1：`AssertionError: [('frontend-node24-m6-gates-typecheck', 'm6-gates-source-freeze')]`。类型检查08:58:01.194327—08:59:12.274242 UTC，与只读源码摘要核对08:58:49.384030—08:58:53.885582 UTC重叠4.501552秒。因此不能声明全部验证/审计记录串行通过；该审计失败原样保留，不豁免或回写PASS。两条命令各自exit0，不消除调度审计失败。首轮源码核对、Git差异检查和进程清点均exit0。

交接补充保留此次失败与时间区间，并重新核对文档更新后的摘要；没有重跑业务测试或完整lint。更新后源码/文档身份见C盘`m6-gates-delivery-source-manifest.json`；旧`m6-gates-source-manifest.json`保留为文档订正前快照。交接文件生成成功仅表示证据已写出，M6、完整lint和本次串行审计均未通过。


## Kimi登录共享的当前限制

AO会优先用符号链接共享Kimi凭据目录；本机缺少创建链接的能力时退回凭据副本，只按修改时间更新较旧副本并保留较新的本地登录。复制不能保证两端刷新令牌即时同步，因此本次没有将Kimi实时共享验收记为通过，也没有覆盖真实用户凭据或调整权限。

本批补充通过的是Registry工作区Hooks完整组和安装计划/清理保护的隔离fixture。它们不代表真实Agent登录、生产安装、macOS运行或旧Chrome注册恢复已验证。M6仍未通过。


## 完整门禁只读归因（2026-11-08）

M6仍未通过，没有新的可验收桌面安装包。此前局部检查通过不代表完整检查或真实桌面使用通过。本次仅整理了耗时、前端依赖与打包失败的原因和待查项；后续复验由统筹决定，当前不需要本人执行安装、登录或浏览器操作。

C盘短报告：`C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-diagnosis-report.md`；事实表与最终身份/资源/串行证据见同目录`m6-diagnosis-facts.json`、`m6-diagnosis-HANDOFF.md`。399项输入源码摘要保持`7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590`，最终四文档SHA以本批清单为准。24项平台/能力限制暂缓，SV-M6-06三次停手，前批串行重叠FAIL及旧完整门禁FAIL保留。未提交/Push/Merge/发布；旧Claude/HKCU状态未清理。


## 存储与ABI有界复验（2026-11-08）

隔离验证副本的浏览器资料导入55项检查与七项存储补验已通过。这是自动化检查结果，不代表真实浏览器导入、桌面启动或完整安装包已经验收。当前副本仅适用于Node测试；桌面验证仍须另核对Electron运行时。

本批沿用399项源码清单，摘要7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590；生产/测试/配置/依赖清单0修改，仅C盘原生模块与四执行文档变化。完整命令、依赖前后身份、资源/串行/宿主核对及交接见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-bounded-HANDOFF.md。未跑完整前端/存储/lint、下载、打包或桌面；24项限制、SV-M6-06三次停手、旧完整门禁和前批串行FAIL保留，M6仍FAIL。未提交/Push/Merge/发布，旧Claude/HKCU状态未清理。


## 完整自动门禁单次复核（2026-11-08）

当前隔离副本的完整前端自动测试和后端完整静态检查已通过，但仍有14项前端测试跳过，且没有新的桌面候选包。完整存储、真实桌面与安装相关验收仍未通过或未执行，所以M6整体尚不能验收。副本依赖保持Node137测试状态。

本批完整命令、原始日志、JSON断言、阶段耗时和身份/资源/串行审计见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-final-auto-HANDOFF.md及m6-final-auto-results.json。未改源码、测试、依赖清单或配置，399项源码摘要仍7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590，复用既有清单；仅同步四执行文档。未下载、重装、清缓存、打包或启动桌面；前批串行FAIL与旧Claude/HKCU状态保留，未提交/Push/Merge/发布。


## 存储完整race逐包复核异常停手（2026-11-08）

本次存储完整检查因执行监测异常提前停止，没有新的完整存储通过结果。已停止本次残留测试进程，未启动第二个存储包，也未操作真实用户数据。前端和静态检查此前的通过结果保留，M6整体仍不能验收。

原始结果、精确停止证据、缺失门禁与当前身份见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-storage-full-HANDOFF.md。399项源码和六依赖清单未变，沿用摘要7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590，仅同步四文档。未新增Agent、下载、打包或桌面；sqlitetest/前端/lint未重跑。M6仍FAIL，24项限制、SV-M6-06三次停手、旧串行FAIL和Claude/HKCU状态保留，未提交/Push/Merge/发布。


### 存储监测异常的有界只读归因（2026-11-08）

已查19:33:40–19:34:39 +08:00的Application、WER-Diag/Operational和System资源耗尽事件，三项均NoMatchingEventsFound，无拒绝权限；没有扩大窗口或系统扫描。现有外层exit1、ro-run记录的子命令exit1与缺失watch/测试结束metadata只能确认异常退出，触发原因仍未知，不能推定OOM、系统杀进程或业务缺陷。源码确认监测/执行链缺少统一finally收尾，异常可能绕过终态落盘及残留子树回收；这是代码可确认的健壮性缺口，并非已证实的此次触发原因。

本批没有修改runner/生产/测试/配置，没有故障子进程或业务测试。最小建议是由统筹另行授权补足异常证据落盘与精确回收，再决定短故障验证；当前不具备直接恢复存储全量的证据。简短证据与最终进程/身份观察见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-runner-cause-HANDOFF.md。399项清单不重生成，M6仍FAIL；既有前端/lint PASS与存储异常停手边界不变。


## 验证脚本异常收尾补强（2026-11-08）

本次只补强隔离验证脚本的异常记录与进程回收，未改变产品行为或恢复存储测试。M6整体仍未通过，后续是否继续验证由统筹审查决定。

修改前后脚本SHA、最小差异、自检完整命令与终态/回收证据见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-cleanup-HANDOFF.md。399项源码清单未重生成，摘要仍7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590；产品/测试/依赖/配置0修改，仅验证脚本和必要文档变化。8GiB、三配置/HKCU门禁保持，M6仍FAIL；不自行恢复完整race，24项限制/SV-M6-06三次停手/旧污染保留，未提交/Push/Merge/发布。

自检后的验证边界：run.py仅恢复两行原有身份漂移锁止逻辑（protectedUnchanged=false时写入isolation-blocked.json）；该补丁仅做语法检查，未进行第二次自检，不能宣称最终全部字节或全部分支均已动态验证。自检版本与交付版本SHA分别记录在m6-cleanup-delivery-script-changes.json。


## store 完整 race 单次续接（2026-11-08）

本次仅运行此前未启动的store完整包：go test -race -p 1 -parallel 2 ./internal/storage/sqlite/store -count=1 -timeout=15m -json。Go包终态FAIL，耗时900.464秒；内层命令918.022秒、外层918.512秒，真实退出码均为1。原始输出为panic: test timed out after 15m0s；超时时当前用例TestReportStoreRequeuesInterruptedClaimOnRestartAndFencesOldToken只运行56秒，活动栈处于runnable的modernc/sqlite迁移SQL路径。15分钟是全包累计预算，不能将最后一个用例视作独占15分钟或据此认定死锁/环境根因。

Go JSON日志记录427项PASS（250顶层、177子项），0单项FAIL、0 SKIP，但包FAIL；另5项在t.Parallel暂停、1项运行中未取得终态，不能用局部结果替代完整包。内外层独立terminal均commandExitCode=1、exitCode=1、originalError=null，Job before=[]、remaining=[]、errors=[]、terminated=false；进程已自然退出，无需额外终止。

内外层JSONL逐秒观测保存，源码监测21次；最低C盘11925147648字节，三配置/HKCU未变。收尾实查无本轮/Go/测试进程，重新核对双树源码/依赖810份SHA、六最终脚本及Node137模块均不变，399项清单未重生成。证据及完整命令见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-storage-full-store-race-resume-20261108T133802-HANDOFF.md。M6仍FAIL，sqlite旧中断根因未知；本批无自检或其他套件、不追加重试、不提交，24项限制及SV-M6-06三轮停手保持。


## store 首次建库 fixture 最小修复与单次复验（2026-11-08）

本批仅修改report_store_test.go、session_telemetry_store_test.go、budget_restart_test.go、search_store_test.go、workflow_store_test.go五个测试文件，10处首次空库创建改为现有sqlitetest.Open，覆盖12个既有用例。openWorkflowStorePair的3个调用者均审查并定向验证；4处生产sqlite.Open重开、3处OpenPreMigrated第二实例/重开、并发/CDC/旧token及业务断言全部保留。预算用例仅植入历史generation，本来就没有真实关闭重开，不把它写成重开验收。生产迁移、schema、共享helper、依赖及配置未改，未新增用例或框架。

定向race 12/12 PASS、包92.556秒、exit0；store包golangci-lint 2.13.2为103.550秒、exit0、0 issues。报告重启旧68.43秒，定向2.17秒、完整包2.09秒；预算旧66.77秒，定向作为首个模板初始化承担72.42秒，完整包复用后1.25秒。说明重复首次迁移成本被减少，但不能将整包其他慢点归因于此。

随后唯一完整store race仍FAIL：go test -race -p 1 -parallel 2 ./internal/storage/sqlite/store -count=1 -timeout=15m -json，包900.460秒、真实exit1，原始输出panic: test timed out after 15m0s；内层911.378秒、外层912.529秒。732项PASS（488顶层、244子项）、0单项FAIL/0 SKIP，包FAIL；9项暂停、TestWorkflowBindingProvisioningIsSessionBearing运行1秒未终态，其栈在runnable调度准入检查，并非旧报告首次迁移栈。5项工作流受影响用例在完整包尚未启动，仅有定向PASS，不能冒充整包覆盖。当前快照用例63.33秒、首次模板初始化相关投影用例63.10秒等仍占用预算。不再修改或重跑。

三次命令持续JSONL、独立terminal及HKCU观察齐全，内外层均自然退出、Job before/remaining/errors为空；最后只读核对本轮/Go/测试/lint进程为0，最低C盘11893485568字节，三配置/HKCU未变。源码清单由399扩至403项（4个既有文件新纳入清单，不是新增源码文件），最终摘要0df24e5a17c80454a07125b3b5b2c53bb14647cc0271d2beaffd7dab01dfe4fc；双树818份源码/六依赖SHA实查一致，六脚本及Node137模块不变，旧清单与原文件备份保留。

既有超长测试文件申报：search_store_test.go由301到302行，超过300行2行；workflow_store_test.go由1673到1674行，超过1374行。新增仅导入/必要方法注释，保留原测试组织与全部断言，不为比例拆文件。最小diff、全部调用者/保留重开审查、逐项耗时、完整命令及SHA见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-store-fixture-HANDOFF.md及m6-store-fixture-results.json。M6仍FAIL；sqlite完整race、旧监测中断原因、24项限制、SV-M6-06三次停手、打包及真实环境未验均保留；未重跑前端/全仓lint/sqlite/自检，不提交或Push/Merge/发布。


## 存储剩余顶层覆盖收口（2026-11-08）

当前store的561个顶层用例都有适用的分段race通过证据，但完整store命令仍有超时失败记录；sqlite只有31/168顶层具备有效证据。此结果不能视为CI、M6或真实桌面验收通过，仍不得据此进入后续阶段。

本批唯一补验使用go test -race -p 1 -parallel 2 ./internal/storage/sqlite/store -run <18项精确锚定表达式> -count=1 -timeout=15m -json，实际表达式、隔离入口及退出码见m6-storage-remaining-results.json。18顶层、19子项PASS，0 FAIL/SKIP，exit0；包100.504秒，内层112.424秒、外层113.743秒。已PASS项未重复选择，未启动第二条业务补验。

sqlite的137项缺口按预算分支暂缓：55项直接迁移、98项使用历史快照（两类可重叠），已有migrationSnapshots缓存。参考历史同类25.33、34.67、55.27秒，55项按25–55秒估算1375–3025秒，超过剩余单条900秒预算；这是量级估算，不是137项实测，也不表示每项同耗时或均重新空库迁移。

补验内外层各113次资源采样，最大间隔1.008126/1.507869秒，外层3次源码核对；最低C盘11889790976字节。三配置和精确HKCU身份未变，两层Job before/remaining/errors均空、terminated=false，收尾未见Go/test/lint/本批runner残留。本批未改源码、fixture、runner、配置或依赖；403项源码摘要仍为0df24e5a17c80454a07125b3b5b2c53bb14647cc0271d2beaffd7dab01dfe4fc。同步前826份双树SHA及674份历史适用性文件核对一致，七脚本与Node137模块不变；冻结清单不重生成，四文档新SHA单列在本批结果。

短交接：[m6-storage-remaining-HANDOFF.md](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-storage-remaining-HANDOFF.md)。逐项覆盖、137项缺口及源码/日志行号：[m6-storage-remaining-coverage.md](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-storage-remaining-coverage.md)；命令、身份、监测及SHA：[m6-storage-remaining-results.json](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-storage-remaining-results.json)。

分段覆盖不改写完整命令结果：历史完整store超时FAIL、sqlite监测INCOMPLETE继续保留，M6仍FAIL。旧sqlite异常根因、24项限制、SV-M6-06三轮停手、打包及真实环境未验、run.py两行身份锁止仅静态检查的边界不变；本批未新增测试/框架，未重跑完整套件或桌面，未提交/Push/Merge/发布，旧HKCU状态未清理。
