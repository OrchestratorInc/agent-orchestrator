# M6 需求与证据覆盖

更新：2026-11-08。基线 `acf9bafa8c14de5533a35f66d4d2c9bfc057baae`，本轮未提交。本文区分现有实现、离线验证与真实环境验收；不构成发布 PASS。命令、退出码及原始日志见 [m6-report.md](m6-report.md)。

## Q1–Q10

| 需求 | 实现入口 | 行为证据入口 | 当前边界 |
|---|---|---|---|
| Q1 混合控制 | `service/workflow/planning.go`、`replanning.go`；`WorkflowPlanEditor.tsx`、`WorkflowPlanDraft.tsx` | `m5_planning_e2e_test.go`、`m5_plan_boundary_test.go`；本轮 `TestM6ProfileHTTPManagerRestart` | 只读 Planner 输出候选，确认和恢复调度独立；可编辑候选。真实模型生成质量未验 |
| Q2 四 Adapter | `chatdriver/codexappserver`、`claudeacp`、`ompacp`、`deepseekharnessacp`，共享 `chatdriver/acp` | 各目录 `driver_test.go`、ACP 协议回归；真实 `live_test.go` 单列 | 四者原生路径保留；自定义平台 profile 首版仅 Codex；不能按静态表伪造运行时能力 |
| Q3 三种干预 | `service/intervention/coordinator.go`、`service/chat`、`WorkflowIntervention.tsx` | M2/M5 Queue/Steer/Stop 用例，`workflow-intervention.test.tsx`；本轮 profile Stop & Replan | Steer 以当前 Session/generation 的实测证据为准；未知不授权。SV-06 真实桌面队列仍暂缓 |
| Q4 分级自治协作 | `service/collaboration`、Workflow TaskRequest/物化入口、Scheduler | `m2_mcp_e2e_test.go`、`m3_materialization_e2e_test.go`、协作身份/跨 Workflow 拒绝回归 | Agent 可受限通信/申请任务；不拥有 Operator 权限，不能自行扩并发或预算 |
| Q5 独立工作树与验收 | `service/artifact`、`qualitygate`、`taskcommit`、`service/integration`；`WorkflowQualityPanel.tsx` | `m4_artifact_e2e_test.go`、`m4_quality_e2e_test.go`、`m4_repair_e2e_test.go`、`m4_apply_e2e_test.go` | 工件冻结、Gate/Review/任务分支提交、目标分支集成分别验证；M4 大场景旧五分钟超时保留。生产远程 Push 尚未交付 |
| Q6 多仓、本机 Windows | `session_manager/multi_repo_workspace.go`、`adapters/workspace/gitworktree`、逐仓集成事实 | `m2_f03_*`、`m2_full_flow_e2e_test.go`、`m4_preview_e2e_test.go`、`m4_apply_e2e_test.go` | 本轮仅 Windows 本机；远程 Worker 不在首版范围，部分成功不能报告整体成功 |
| Q7 同源双视图 | `WorkflowWorkbench.tsx`、`WorkflowGraph.tsx`、`WorkflowTaskPanel.tsx`、`useWorkflows.ts` | `workflow-behavior.test.tsx`、`workflow-intervention.test.tsx`；M5 桌面证据 | 两视图共享同一 Session/Attempt；Node24 组件 3 文件 13 用例通过，不代替真实桌面验收 |
| Q8 动态并发 | `service/scheduler/{service,resources,planner,control}.go`、SQLite 原子准入 | `m3_scheduler_e2e_test.go`、`m3_admission_e2e_test.go`、`m3_budget_resources_e2e_test.go`、`m3_process_e2e_test.go` | 默认/上限、CPU/RAM、领导权和预算均独立门禁；自定义 profile 不可绕过 |
| Q9 模型、供应商、用量与预算 | `service/providerprofile`、`service/capability`、`service/budget`、`WorkflowProviderSelect.tsx`、`WorkflowBudgetPanel.tsx` | 本轮认证完整包、Codex Driver/宿主定向、`TestM6ProfileHTTPManagerRestart`、`workflow-provider.test.tsx` | 已有离线证据；模型目录鉴权不证明 Responses 推理额度。未知用量/成本保留未知，不制造账单 |
| Q10 权限及恢复 | `service/principal`、`session_manager`、`persistenthost`、SQLite admission/binding/intent；受限 Runner | `m3_recovery_e2e_test.go`、M4 Apply/Commit 恢复、`raw_identity_test.go`、原生 IPC 审批回归 | UNKNOWN 不能等同死亡；重开保留工作树与占位。Runner 和提示词不等于 OS 沙箱；Hello、安装升级和真实 Provider 暂缓 |

表内后端路径均相对 `backend/internal/`；前端组件相对 `frontend/src/renderer/components/workflow/`；M2–M6 集成测试相对 `backend/internal/integration/`。旧完整后端套件 exit 1 且隔离失效；最新相关包和 integration/Manager 验证单列，各项不能仅由文件存在判 PASS。

## 四 Adapter 的 profile 边界

| Adapter | 原生路径 | 平台 profile | 不可推定的能力 |
|---|---|---|---|
| Codex CLI | 原生账号/配置与现有模型目录 | 固定 HTTPS Responses 路由、模型、受支持 effort、可选上下文窗口、后端凭据环境；只读/审批独立 | 未调用真实 Responses；非生成式目录认证不证明额度或所有模型支持全部 effort |
| Claude Code CLI | 现有 Claude ACP / 原生配置 | 非空引用在执行副作用前明确拒绝 | 不复用 Codex 路由或强制统一上下文参数 |
| Oh My Pi | 现有 OMP ACP / 原生配置 | 非空引用明确拒绝 | 不从静态声明推定即时 Steer 或准确成本 |
| DeepSeek Harness | 现有 DeepSeek ACP / 原生配置 | 非空引用明确拒绝 | 不以名称兼容推定 Codex Responses 参数兼容 |

实际功能由安装版本、握手、当前会话能力和认证证据共同决定；该矩阵是支持边界，不是四家真实 Provider 的成功记录。

## Q9 数据与恢复链

`宿主只读登记表 → HTTP 非敏感目录 → 创建/候选/确认/重规划 → 持久 Task.Profile → Scheduler/SQLite 准入 → Bind → LocalGateway → Manager → Chat → Codex thread/start|resume`。

- 所有路由字段参与引用摘要；原引用变化后拒绝，人工重新选择产生新引用。
- 自动候选继承来源选择，不能自行换 profile；显式编辑可选择或清除。
- 认证探测按 profile 与凭据身份缓存，轮换失效；失败不读取默认原生账号证据。
- Manager 的 profile 任务不绑定或合并原生账号；凭据在 Driver 最后启动回调解析。
- daemon 启动先接管专用前缀凭据并移出可继承环境；Reviewer 预检、Git、Driver、终端和持久宿主边界清除平台变量及大小写别名。原生公用变量不得登记为平台凭据，轮换需更新宿主环境并重启 daemon。
- 持久宿主的内部身份绑定配置与凭据；重连不匹配时拒绝，保留原宿主，不重复启动。
- HTTP/Renderer 只见引用、模型与 effort；URL、环境变量名和秘密不在公开目录。内部宿主身份摘要不进入命令行或公开诊断。

## 验证层次

| 层次 | 本轮状态 |
|---|---|
| domain 完整包、23 项历史失败定向回归 | 旧结果 exit 0；旧 runner 无完整用户目录隔离保证 |
| providerprofile/capability 完整包 | 新 runner PASS；本地 TLS 假鉴权，含公开目录、错误凭据、超时、重定向、轮换、漂移；专用凭据接管已验 |
| Codex Driver / 持久宿主完整包 | 新 runner PASS；原生恢复与平台映射、实际本地宿主环境均覆盖，不调用真实 Provider |
| SV01/02/03、Reviewer/Git 凭据边界 | 六包定向 65 PASS；补充边界回归通过；agy/cursor/kimi/processenv/providerprofile 五完整包 52 PASS |
| HTTP→真实 Manager→SQLite/Git→Driver 参数，跨 SQLite 重开 | 新 runner 完整 integration PASS（740.982 秒）、Manager PASS（336.263 秒）；Driver 为 Scripted，不能代替真实 Provider |
| OpenAPI/TS 生成、前端 typecheck、工作流组件测试 | 固定生成器 7.4.4；新 runner HTTP/OpenAPI、Node24 typecheck/typecheck:e2e 通过。完整前端首次 exit 1（633 文件 PASS / 3 FAIL）；ABI 与翻译返修后资料导入 55 项、最终工作流/i18n 5 文件 36 项通过。未全量重跑，不记为全量 exit 0 |
| 完整后端、race、完整前端、构建/vet/lint/CI 对齐 | 旧完整后端 exit 1（50 包失败）；新 build/vet、profile 五完整包 race、Manager Profile、Windows teardown/environment/hibernate 及修复后的 ConPTY count=3 通过。首轮交接后剩余 Windows CI 九条命令均 exit 0（240 PASS / 43 SKIP），详细映射见报告。完整 lint、桌面打包和完整 gitworktree 仍失败，不能标整体 PASS |
| 前轮新增 lint 诊断复核（历史） | 四包 exit 1 / 76 项、五包 exit 1 / 229 项是前轮记录。最早 12 项新增诊断对应路径/规则未再报告；当时新增行/文件 0 项不代表 M1～M5 可豁免，最新结果见下一行 |
| lint 门禁继续收口 | 仅换行对照将五包 229 项降为 48 项，证明 181 项为 CRLF、2 项为真实导入问题；修复后五包 lint exit 0、37.264 秒、`0 issues.`。execution 完整 race 242 PASS，Manager 准入/重附着/多仓定向 race 50 PASS，Windows 两包 race 361 PASS / 1 SKIP，M2/M4/M5 集成 55 PASS / 1 SKIP。完整 lint 复核另记，不替代新增宿主用例失败 |
| Windows 分离宿主释放的新增真实路径 | 新增 `TestWindowsDefaultSpawnHostEndToEnd` 连续三轮失败，停止继续修改/重试；仅看到初始化控制序列/空行或输入回显，未证明 shell 交互成功。前面的完整包结果在此用例加入前取得，不能覆盖此失败 |
| 完整 lint 最后一次复核 | exit 4，763.125 秒，252 项诊断加超时。173 项工作流业务/验收、24 项本交付共用平台、55 项其他既有通用边界；27 项 goimports 在 LF 副本仍存在。逐项分类与原始计数已一致，不以超时推定零诊断或继续原样重跑 |
| 第二批197项交付诊断 | 25个包分组 lint 已通过；本批用例事件 1947 PASS / 27 SKIP / 0 FAIL，但存储完整 race 命令 exit1、两个包超时，不能记为通过。HTTP 补验68个顶层用例，修改文件83项均有 PASS。API 原 npm 命令 exit1，离线缓存生成入口 exit0。55项和最近完整 lint 超时仍保留；SV-M6-06 没有第四轮。 |
| Windows用量与项目回归 | 24个旧失败目标逐项核对；三个受影响完整包exit0，421 PASS/5 SKIP；文件替换race通过。定价仅C盘LF对照通过，主仓持久检出仍待配置授权。详见报告及m6-wr-targets.md。 |
| Windows Hello、手动启动、SV-06 桌面 Queue | NOT_TESTED / DEFERRED，用户明确暂缓 |
| 真实 Provider 与计费、生产 Push、安装升级、其他 OS | NOT_TESTED；安装升级还缺打包版 userData 的有效隔离环境 |

未运行发布、部署、Push/Merge。旧全量测试已确认污染真实 `.claude.json`（8 条测试工作区记录，尚未获用户授权清理，双方均未回退）；不能再声称未修改原生配置。旧验证结果不证明安全隔离，详情见执行报告。新 runner 已验证实际用户目录，并持续核对三份真实配置哈希/mtime。

## 第三批 Windows lint 的最新覆盖

55项原保留诊断已逐项处置，13包分组固定2.13.2 lint全部exit0、0 issues；前文第二批的“55项未解决”为当时历史状态。完整lint最新仍exit4/252项/超时，本批不重跑全仓。

五个完整包（processalive、ptyregistry、procmem、runfile、previewserver）race通过；service/agent、codexappserver、service/session、persistenthost只验证明确受影响安全范围，不能标全包通过。总测试事件172 PASS/2 SKIP，去重167 PASS/2 SKIP；两项symlink缺权限，既有失败及外部验收缺口不覆盖。Job缓冲固定后已定向复验两个实际子孙回收场景。systemexec、browserstream、unrealagent、browserchrome仅lint/编译；后两处跨平台调用未删除，其他OS未实跑。

新增三条PID高位别名回归，ACL所有者/授权/身份漂移拒绝保持；全部输入使用scratch和假凭据。逐项、命令、源码身份与规模详见m6-report.md第三批及C盘m6-lint-b3-handoff.json。


## Windows 私有目录与账号回归追加（2026-11-08）

原18项目标16 PASS、2 FAIL（symlink权限）；M6整体仍不通过。Reviewer与模型发现的真实ACL继承缺口、工作区仅Stat放行、自定义DataDir导入防护均修正；账号原ACL合格，登录/重认证主要为退出时序和设备读取fixture问题。生产登录状态机未改。

Reviewer及共享账号目录曾实证SetSecurityInfo传播会改外部硬链接目标ACL，现目录逐对象设置，目标原字节/SDDL保全通过；device credential文件策略保持独立。manifest身份以真实句柄文件ID比较，重复关闭fixture按真实NotFound返回。

完整services：1161 PASS/2 FAIL/11 SKIP；importer、pi、agy完整包通过，agent/session各保留一个symlink失败。追加修复后的agent完整race仍因该权限失败exit1且无DATA RACE；reviewgateway完整用例及workspace/import拒绝定向race exit0。七包lint exit0，补强测试后的reviewgateway/agent最终lint也exit0。daemon仅编译/lint；不代替桌面或Provider验收。命令、18项映射、跳过原因、测试规模与摘要见m6-report.md本批章节及m6-ws-handoff.json。

## SV-M6-07 进程观察与恢复追加（2026-11-08）

Windows进程查询错误现保留未知：宿主锁/描述符和run-file不清理，竞争启动闭锁；HostPID不把未知记为确认存活。PTY扫描返回原列表及不完整错误，避免“条目保留但地址拒连”再次被判死。CLI默认Deps接线已覆盖client/status/stop，正常PID校验、真实死亡回收和daemon启动门禁保留。

四个完整包race 93 PASS；CLI/ConPTY定向首次25 PASS/1 FAIL（旧停止文案断言），精确同步后1 PASS。最终去重119 PASS/0 FAIL/0 SKIP，无DATA RACE；六包lint exit0、0 issues。processalive/ptyregistry Linux交叉编译exit0，不等于其他OS实跑。3条新增业务回归和受控子进程验证详见m6-report.md本批章节。

17项源码变化，最终366项清单；新增测试文件均≤300行，但processalive包内测试455/生产102行、超过两倍上限251行已申报，两个历史大测试文件仅同步接口/断言。三份指定配置及精确HKCU键只读身份均未漂移，未运行browserchrome Setup/Remove；旧污染保留，不声称旧检查隔离安全。

统筹补核的codexappserver 112条和journal 1条旧失败已有m6-profile-race-full PASS，missing=[]，不另开返修；journal PASS不作为Windows ACL验收。M6整体、SV-M6-06停手及其他历史FAIL/未验项均保持原状态。

## SV-M6-08 Chrome 注册测试隔离（2026-11-08）

`Installer` 私有注入现在区分平台默认与显式目录注册。六次测试构造全部显式传入 nil 或已有内存 fake，Windows 测试不再回落真实 HKCU；生产默认注册逻辑未改。目录型 Setup/Status/Refresh/Remove、另一安装 manifest/注册保全等原断言保留，没有新增用例、依赖或 skip。

完整 browserchrome 包与完整 race 均 exit 0，各 13 PASS / 1 SKIP；固定 2.13.2 lint exit 0、`0 issues.`。两个历史失败现在均 PASS。唯一 `TestChromeE2E` 因未设置三个真实端点开关而保持既有 SKIP，不能记真实 Chrome 验收。普通测试与 race 是同组测试的两种运行方式，不合计为 26 个独立场景。

本批仅改两源码与四执行文档。最终367项源码摘要 `3a7591eea63d9b304455382b491b7e77359bc7d6b669fe84bcf10f2f0ed2f0f4`；配置SHA/mtime及精确HKCU键身份逐次只读核对不变。`chrome_test.go` 259行，本包测试798/生产1098行，无新增规模例外。原始失败、完整命令和最终交接见执行报告SV-M6-08及C盘`m6-bi-handoff.json`。

旧HKCU污染和Claude八条记录均未清理；SV-M6-06仍三轮停手。待统筹独立验收，M6整体仍未通过，不替代真实浏览器、Provider、桌面或其他OS验收。

## 剩余门禁本批覆盖（2026-11-08）

本节为最新状态，前文完整lint失败数字保留为历史记录。原315 FAIL逐项关闭287，保留28；外部能力限制19项；自动工作待续6项；平台限制3项。原始同名PASS不等于适用关闭：Codewhale父组仅一个子项复验。TaskNaming按统筹指令补当前源码的整个顶层测试race，详见报告实际结果。原未终态Codex已关联后续完整race PASS；50包的完整包结果与定向检查分列。

完整lint：exit 4 / 623.267秒 / 0 issues.
level=error msg="Timeout exceeded: try increasing it by passing --timeout option"；完整lint仍失败；本批只运行一次，不加超时或原样重跑。telemetry完整包race：exit0、146 PASS, 0 FAIL, 0 SKIP；仅本地假传输。前端telemetry为1文件18 PASS，Node24 typecheck exit0，未重跑完整前端。Go/Electron分别并发验证，未实跑跨语言同时竞争。

本批改30源码（4生产/26测试），新测试名及skip均0；397项最终摘要`6349d3a0234c0836670788216af39db75793a7236c643b8c37e1dc18c0e2d139`。历史大文件和包内两倍上限例外已在报告逐一申报。详细命令与315/50账本见C盘m6-gates-handoff.json及m6-gates-remaining-matrix.md。

完整存储race15分钟超时、完整前端历史FAIL、打包TLS、SV-M6-06三次失败、真实环境与其他OS未验继续保持独立；旧配置/注册污染未清理。M6整体仍未通过，未提交/Push/Merge/发布。


### 收尾串行审计失败（2026-11-08）

首轮`gates-close.py`返回exit1：`AssertionError: [('frontend-node24-m6-gates-typecheck', 'm6-gates-source-freeze')]`。类型检查08:58:01.194327—08:59:12.274242 UTC，与只读源码摘要核对08:58:49.384030—08:58:53.885582 UTC重叠4.501552秒。因此不能声明全部验证/审计记录串行通过；该审计失败原样保留，不豁免或回写PASS。两条命令各自exit0，不消除调度审计失败。首轮源码核对、Git差异检查和进程清点均exit0。

交接补充保留此次失败与时间区间，并重新核对文档更新后的摘要；没有重跑业务测试或完整lint。更新后源码/文档身份见C盘`m6-gates-delivery-source-manifest.json`；旧`m6-gates-source-manifest.json`保留为文档订正前快照。交接文件生成成功仅表示证据已写出，M6、完整lint和本次串行审计均未通过。


## 六项遗留的最新覆盖（2026-11-08）

新增4项实际关闭，2项仅限制分类调整：原315 FAIL为291关闭/24保留（21外部能力限制、3平台限制）。Kimi两共享用例要求真实链接及双向即时更新，mtime复制回退不是等价实现，因此原FAIL不关闭；生产与凭据权限未改。详细源码、原始失败及直接调用链见C盘`m6-residual-contract-review.md`。

Registry完整顶层race 39 PASS（父+38子项），安装受影响组race 20 PASS（13顶层+7子项），均exit0、0 FAIL/0 SKIP。固定2.13.2仅registry/systeminstall两包lint exit0、0 issues。50包最新完整包结果仍21 PASS/29 FAIL，不用定向PASS替代。完整命令见`m6-residual-product-results.json`。

本批仅四个既有测试文件，生产0、新用例0、skip0；399项源码摘要`7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590`。既有超限文件/包内两倍例外在执行报告申报。所有本批测试与冻结串行，具体时间、8GiB样本及宿主身份见`m6-residual-handoff.json`。前批完整lint超时FAIL及4.501552秒重叠FAIL保留，SV-M6-06无第四轮；M6仍未通过，未提交。


## 完整门禁只读归因（2026-11-08）

原完整前端3个失败文件共50项：browser-profile-import 47 ABI失败、instance 2语言目录/插值失败、renderer-coverage 1硬编码失败。后续55 PASS及workflow+i18n 36 PASS覆盖这些历史失败；中间新出的两项工作流标签断言也已覆盖。但当前原生模块为ABI149，与Node24的137要求冲突，故不存在当前完整前端PASS。

存储完整race600 PASS/2 SKIP、两包FAIL；七项未终态仍未被77顶层补验或路径组覆盖。活动栈是迁移执行而非已证实死锁。完整lint仍exit4，不能将0 issues当PASS；打包TLS仍FAIL且无候选。本批只做日志/源码/工具元数据核对，不新增测试覆盖。

C盘短报告：`C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-diagnosis-report.md`；事实表与最终身份/资源/串行证据见同目录`m6-diagnosis-facts.json`、`m6-diagnosis-HANDOFF.md`。399项输入源码摘要保持`7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590`，最终四文档SHA以本批清单为准。24项平台/能力限制暂缓，SV-M6-06三次停手，前批串行重叠FAIL及旧完整门禁FAIL保留。未提交/Push/Merge/发布；旧Claude/HKCU状态未清理。


## 存储与ABI有界复验（2026-11-08）

browser-profile-import全部55项有逐项终态passed，覆盖旧47项ABI失败；不是只据文件摘要判断。存储旧七项未终态均取得race PASS（两条活动单例及一条五项组，额外2个子项PASS）。详见C盘m6-bounded-terminal-results.json，其中保留命令标签、原始事件行、前端55项与旧47项映射。局部终态不覆盖原完整包超时；不重跑已有适用覆盖的i18n/Workflow。

本批沿用399项源码清单，摘要7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590；生产/测试/配置/依赖清单0修改，仅C盘原生模块与四执行文档变化。完整命令、依赖前后身份、资源/串行/宿主核对及交接见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-bounded-HANDOFF.md。未跑完整前端/存储/lint、下载、打包或桌面；24项限制、SV-M6-06三次停手、旧完整门禁和前批串行FAIL保留，M6仍FAIL。未提交/Push/Merge/发布，旧Claude/HKCU状态未清理。


## 完整自动门禁单次复核（2026-11-08）

最新完整前端636文件PASS，10896 PASS/14 SKIP/0 FAIL，原50失败均逐名核对通过；完整lint2.13.2也已exit0、0 issues。本批这两项最新完整门禁更新为PASS，历史失败仍保留。完整存储race仍是旧15分钟超时；七项局部PASS不能覆盖整包。打包TLS与24项限制、SV-M6-06等保持独立，M6整体仍FAIL。

本批完整命令、原始日志、JSON断言、阶段耗时和身份/资源/串行审计见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-final-auto-HANDOFF.md及m6-final-auto-results.json。未改源码、测试、依赖清单或配置，399项源码摘要仍7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590，复用既有清单；仅同步四执行文档。未下载、重装、清缓存、打包或启动桌面；前批串行FAIL与旧Claude/HKCU状态保留，未提交/Push/Merge/发布。


## 存储完整race逐包复核异常停手（2026-11-08）

sqlite本轮部分结果15 PASS（8顶层+7子项）、2 SKIP，无包PASS/FAIL终态；最后活动用例TestMigrateRepairsRenumberedAgentInventoryHistory。监测父命令异常exit1后Go子树尚存，已精确停止；store因进程边界异常未启动。两条完整存储门禁仍无新增PASS，原15分钟超时保持，不用已通过七项局部race或本批15个PASS覆盖。前端636文件/10896 PASS与完整lint最新PASS不受本批状态影响。

原始结果、精确停止证据、缺失门禁与当前身份见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-storage-full-HANDOFF.md。399项源码和六依赖清单未变，沿用摘要7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590，仅同步四文档。未新增Agent、下载、打包或桌面；sqlitetest/前端/lint未重跑。M6仍FAIL，24项限制、SV-M6-06三次停手、旧串行FAIL和Claude/HKCU状态保留，未提交/Push/Merge/发布。


### 存储监测异常的有界只读归因（2026-11-08）

已查19:33:40–19:34:39 +08:00的Application、WER-Diag/Operational和System资源耗尽事件，三项均NoMatchingEventsFound，无拒绝权限；没有扩大窗口或系统扫描。现有外层exit1、ro-run记录的子命令exit1与缺失watch/测试结束metadata只能确认异常退出，触发原因仍未知，不能推定OOM、系统杀进程或业务缺陷。源码确认监测/执行链缺少统一finally收尾，异常可能绕过终态落盘及残留子树回收；这是代码可确认的健壮性缺口，并非已证实的此次触发原因。

本批没有修改runner/生产/测试/配置，没有故障子进程或业务测试。最小建议是由统筹另行授权补足异常证据落盘与精确回收，再决定短故障验证；当前不具备直接恢复存储全量的证据。简短证据与最终进程/身份观察见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-runner-cause-HANDOFF.md。399项清单不重生成，M6仍FAIL；既有前端/lint PASS与存储异常停手边界不变。


## 验证脚本异常收尾补强（2026-11-08）

唯一脚手架自检验证了真实runner启动后异常路径：2.373秒、故意exit95、RuntimeError原错误保留，原业务码null，3个Job成员回收且残留0；未产PASS终态。该结果仅证明本次可捕获异常收尾，不是业务测试覆盖，不覆盖OS强杀/断电或其他故障矩阵。sqlite原中断、store未启动与完整存储旧超时仍未关闭；前端/lint完整PASS不重跑。

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

顶层分段race覆盖：sqlite 31/168（137 GAP），store 561/561（0 GAP），合计592/729。历史全局摘要不同不直接沿用：674份范围身份逐项核对，5个fixture变更涉及的12项只用当前证据，10条过时记录排除。两个历史query子项SKIP仍单列，不计子项PASS。

最新完整store日志仍为488父项PASS、9暂停、1运行中未终态、63未见启动；sqlite中断日志为8父项PASS、1运行中、159未见启动。以上逐项状态独立保留，另一条适用父项PASS才可填补分段覆盖，不能凭子项或暂停推定父项PASS。

本批唯一补验使用go test -race -p 1 -parallel 2 ./internal/storage/sqlite/store -run <18项精确锚定表达式> -count=1 -timeout=15m -json，实际表达式、隔离入口及退出码见m6-storage-remaining-results.json。18顶层、19子项PASS，0 FAIL/SKIP，exit0；包100.504秒，内层112.424秒、外层113.743秒。已PASS项未重复选择，未启动第二条业务补验。

sqlite的137项缺口按预算分支暂缓：55项直接迁移、98项使用历史快照（两类可重叠），已有migrationSnapshots缓存。参考历史同类25.33、34.67、55.27秒，55项按25–55秒估算1375–3025秒，超过剩余单条900秒预算；这是量级估算，不是137项实测，也不表示每项同耗时或均重新空库迁移。

补验内外层各113次资源采样，最大间隔1.008126/1.507869秒，外层3次源码核对；最低C盘11889790976字节。三配置和精确HKCU身份未变，两层Job before/remaining/errors均空、terminated=false，收尾未见Go/test/lint/本批runner残留。本批未改源码、fixture、runner、配置或依赖；403项源码摘要仍为0df24e5a17c80454a07125b3b5b2c53bb14647cc0271d2beaffd7dab01dfe4fc。同步前826份双树SHA及674份历史适用性文件核对一致，七脚本与Node137模块不变；冻结清单不重生成，四文档新SHA单列在本批结果。

短交接：[m6-storage-remaining-HANDOFF.md](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-storage-remaining-HANDOFF.md)。逐项覆盖、137项缺口及源码/日志行号：[m6-storage-remaining-coverage.md](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-storage-remaining-coverage.md)；命令、身份、监测及SHA：[m6-storage-remaining-results.json](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-storage-remaining-results.json)。

分段覆盖不改写完整命令结果：历史完整store超时FAIL、sqlite监测INCOMPLETE继续保留，M6仍FAIL。旧sqlite异常根因、24项限制、SV-M6-06三轮停手、打包及真实环境未验、run.py两行身份锁止仅静态检查的边界不变；本批未新增测试/框架，未重跑完整套件或桌面，未提交/Push/Merge/发布，旧HKCU状态未清理。
