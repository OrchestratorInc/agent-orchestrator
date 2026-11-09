# 多 Agent 工作流故障处理

先记录 Workflow、Task、Attempt、Session、请求 ID、错误代码、发生时间及 UI 回执。不要复制密钥、账号文件、浏览器令牌或完整环境变量。当前阶段与验证缺口见 [m6-report.md](m6-report.md)。

| 现象 | 先检查 | 处理 |
|---|---|---|
| 供应商列表为空 | 实际 `AO_DATA_DIR` 与登记文件是否存在 | 文件不存在表示只有原生配置；由可信宿主管理者登记，Agent/UI 不写配置 |
| `PROVIDER_REGISTRY_INVALID` | JSON 是否为数组、未知字段、重复 ID、坏项、URL、文件大小；凭据名是否为 `AO_WORKFLOW_CREDENTIAL_` 非空专用名称 | 公用 `OPENAI_API_KEY` 等不能作为平台登记凭据；旧任意变量名需迁移并重新选择。不会跳过坏项接受前面的配置 |
| `PROVIDER_PROFILE_STALE` | 引用中的摘要是否仍匹配全部登记字段 | 在候选或重规划中重新选择；不要替换旧任务摘要以伪造原审批 |
| `PROVIDER_SELECTION_MISMATCH` | 模型/effort 是否与固定项一致 | 重新选择完整配置；固定项不允许独立修改模型 |
| `PROVIDER_PROFILE_UNSUPPORTED` | 当前 Adapter 是否为受支持的 Codex 映射 | 其他 Adapter 使用原生配置；不能把不兼容字段静默忽略 |
| `PROVIDER_CREDENTIAL_UNAVAILABLE` | daemon 实际启动环境是否有登记的变量 | 由宿主管理者正确设置；不要把密钥填入任务、登记 JSON、命令行参数或聊天 |
| `PROVIDER_AUTH_UNVERIFIED` 或认证证据不足 | `/models` 是否对错误凭据也公开，当前凭据是否被拒绝，目标模型是否存在 | 公开目录不能授权；当前实现要求可靠的非生成式鉴权证据。不使用真实生成请求来偷偷验证 |
| 凭据轮换后拒绝重连 | 旧宿主是否仍运行，配置是否与旧 Session 一致 | 保留原宿主与工作树，受控停止后重规划；不重复启动另一份 |
| 资源软限制/容量不足 | Scheduler 的实际 CPU/RAM、已占槽和未知执行 | 释放明确属于自己的闲置进程，等待资源恢复；UNKNOWN 占位不能当成空闲，禁止降阈值绕过 |
| 预算未知或超限 | 所有适用范围的政策、预留、用量及价格状态 | 经审批调整预算或等待结算；未知不等于零，不叠加重叠范围金额 |
| Steer 灰显 | 当前代际、运行轮次、认证/协议能力、证据过期 | 使用 Queue 或等待新证据；不能根据 Adapter 名称强行开启 |
| Queue 一直 queued | 当前轮次是否结束、Controller 是否在线、回执是否刷新 | 进入原会话检查队列；不新建请求身份反复发送 |
| Stop 返回 unknown | 原进程与 Controller 是否仍活着，停止回执是否明确 | 保留占位与工作树并对账；不能把超时显示成停止成功 |
| 确认计划后不执行 | 是否仍暂停、旧 Planner 是否收尾、依赖与路径预留是否满足 | 确认和恢复调度是两步；先完成旧任务收尾再恢复 |
| `ARTIFACT_STALE` / 旧质量证据 | 工作树、基线、revision/digest 是否变化 | 重新冻结、Gate/Review；不套用旧成功回执 |
| 多仓 PARTIAL/UNKNOWN | 每仓已写入 ref 与持久意图 | 只对账，不自动重复 Apply、不回滚已成功仓，不清除用户 dirty/index |
| Operator 取消、过期或正文变化 | 原审批目标、method/path/body、请求 ID | 重新检查后审批；原生确认不能由浏览器或 Agent 自报成功 |

## 请求超时与恢复

UI 保留未确认请求的原正文与幂等身份。“重试原请求”用于读回或重放同一操作；界面若提示正文已变化，先确认原操作结果。收到明确取消后，新操作使用新的审批，草稿可保留。

daemon 或 Agent 异常后，先观察存活状态和持久绑定。失败/未知 probe 不能证明死亡。服务重启会先恢复领导权、租约、占位和操作意图；直接删除 SQLite 行、host 描述、锁或工作树会破坏恢复证据。

## 开发验证中的已知环境问题

- **Windows 用户目录隔离**：仅设置 HOME/TEMP 不足以隔离 Go 的 `os.UserHomeDir()`，必须在测试子进程统一设置 USERPROFILE/HOME、AO/Provider/AppData/XDG 目录，并禁止继承真实凭据。旧全量检查已写入真实 `.claude.json` 的 8 条测试工作区记录，不能宣称旧结果安全隔离；本轮新预检已确认实际路径和真实配置哈希/mtime 无漂移。后续漂移返回 exit 97 并阻断后续检查，不能把测试自身 exit 0 当作安全成功，也不能未经授权回退真实配置。
- **Windows symlink 权限**：CLI 和工作树资产复制用例要求创建符号链接，本机返回 `A required privilege is not held by the client` 时保留失败和权限能力未验；不擅改系统权限。路径转义/分隔符的测试断言问题应单独修复，不能与权限不足混为一谈。
- **ConPTY 网格测试语言**：旧断言仅匹配 `mode con` 的英文 `Lines/Columns`，本机中文输出使三轮均失败。首次修复后由真实子进程通过 Windows API 读取明确的列/行尺寸并核对退出码，相同 race/count=3 检查 24 PASS；未改系统语言、代码页或跳过断言。
- **Cue shell 就绪与启动脚本**：独立 `^TestCue` race 检查 exit 0、48 PASS，无 SKIP；powershell/pwsh/cmd/git-bash 四种真实交互均通过。测试为 PowerShell 加 `-NoProfile`、cmd 加 `/D`，避免加载宿主启动脚本。真实用户 profile/AutoRun 未验证，不能把测试结果直接用于解释这些脚本导致的启动失败。
- **账号存储 ACL**：`account_storage_unsafe` 说明现有安全检查拒绝了不可信主体可写目录。测试应新建私有 scratch，不能放宽生产校验或修改真实账号目录 ACL。本轮新私有测试目录已使指定 23 项回归通过。
- **M4 大场景超时**：五分钟旧结果保留为失败。最新完整 integration 使用 20 分钟单包预算，工件完整链通过（177.38 秒），多仓部分成功重启恢复通过（163.72 秒）；没有删除 Git 身份校验或跳过断言。单包预算和单用例耗时应分别记录。
- **Electron 打包下载**：最新完整构建 exit 1，报 `Client network socket disconnected before secure TLS connection was established`，没有候选包。原生依赖 prePackage 与三个 Vite 构建已成功；该次失败不能归因于 SQLite/ClangCL 重建。packager 内嵌 `@electron/get` 即使命中 ZIP 缓存，仍联网获取 SHASUMS，失败还会回退下载 ZIP。日志没有 URL/请求序号，无法确定哪次请求断开；独立下载链成功不能替代完整构建。保留 TLS/checksum，在没有根因修复或条件变化时不盲重试。
- **Node 与 Electron 原生模块 ABI**：Node24 测试需要 ABI137，而 Electron44.7.0 需要 ABI149。不能把 Node 测试通过的 `better_sqlite3.node` 直接当作桌面产物。隔离副本已用官方、摘要核对过的 Node137 包重建并通过资料导入 55 项测试；打包必须交由 Forge 按 Electron ABI 重建。缺预编译包时保留源码编译错误，不降低 ABI 校验。
- **打包版数据隔离**：当前打包版 Electron `userData` 固定到用户 `~/.ao/electron`；仅设置 `AO_DATA_DIR` 不够。未建立有效隔离环境时不要启动候选安装包做验收。
- **lint 的换行与错误分类**：五包对照逐项证明 183 个 goimports 文件中 181 项仅因 CRLF，另 2 项为真实导入分组问题，修复后五包 exit 0、`0 issues.`。完整复核仍 exit 4、252 项并超时，其中 27 项 goimports 在 LF 副本仍存在，不能归因于 CRLF。Gateway 只 wrap 本层权威分类，底层原因保留为文本；混合错误反例已实证扩大 unwrap 会把 UNKNOWN 误判为 `failed_confirmed/spawn_refused`。LiveTarget 的观察失败只闭锁当前操作，不证明死亡。逐项归属与证据见执行报告。
- **Windows 分离宿主实际交互**：新增真实 `defaultSpawnHost` 检查连续三轮失败；已尝试等待 shell 标记和读取完整启动输出，仍只看到终端初始化序列/空行，根因未定。达到停止门禁后保留失败，不继续盲改、不以 Cue 或网格测试通过替代；需先定位终端初始化、夹具入口与生产路径边界。

Windows Hello、手动启动、SV-06 桌面 Queue 由用户决定暂缓；真实 Provider、安装升级及其他 OS 仍单列未验。自动测试不能替代这些结果。

## M6 lint 第二批的证据边界

197项交付涉及诊断已逐项处理并取得所属包分组 lint 通过证据，见 m6-report.md 的第二批交接。最新完整 lint 仍是 exit4、252项并超时，不能据局部0诊断宣布全仓通过；55项其他既有诊断保持未解决。

存储完整 race 的 sqlite/store 两包15分钟超时仍为失败；按变更文件补跑77项、合并后97项都有 PASS，不代表完整套件通过。控制面最初52个 PASS 没有覆盖所有修改函数，已据实际修改文件补跑68个顶层用例并保存逐项覆盖清单，不将名称前缀范围称为全部 M2/审批覆盖。

`npm run api` 的 spec 阶段成功，但 openapi-typescript 命令入口未解析，原 exit1 保留。经授权直接执行已缓存的7.4.4 CLI 后成功生成，两个产物生成前后及双树 SHA 不变；没有重新安装或更改依赖配置。

排查无 Driver 或 Session 探测失败时，unknown/not_probed 和 Frozen 是有意闭锁结果，不能为了 nilerr 把未知改成否定或解除栅栏。Git SHA-1 用于对象协议，不应机械换为 SHA-256；Windows 伪令牌仅供 TOKEN_QUERY，不应 Close。审批进程可隐藏控制台但不能继承 HideWindow 隐藏原生审批窗。

SV-M6-06 保持三轮失败停手，本批没有第四次修改/重试。沿用既有 runner 的真实配置漂移阻断；每条命令自身前后配置不变，但两个执行时段之间 config.toml 的 SHA/mtime 曾变化，未归因，不能宣称整批配置从未变化；详见 m6-lint-b2-config-identity.json。未获授权清理旧 `.claude.json` 8条记录。

## Windows 用量与本地 Clone 回归

续接 fixture 中的 cwd 必须 JSON 编码；直接拼接 Windows 反斜杠会使发现阶段丢弃非法记录。测试默认目录时需同时设置 HOME 与 USERPROFILE，生产仍遵循系统用户目录和显式 CLAUDE_CONFIG_DIR。

Windows os.Stat 的身份可能延迟到 SameFile 才加载；需要比较替换前后 generation 时，应在替换前从真实句柄读取身份。并发替换用例既要开放 FILE_SHARE_DELETE，也要使用支持已打开目标的 ReplaceFileW，不能把 Rename 的 Access is denied 误当文件不存在或删除身份断言。

本地仓库URL先将路径转正斜杠，Windows盘符前加 /，再交给 url.URL 编码。Git show-toplevel 的正斜杠应转换后比较；失败测试的SQLite必须在TempDir清理前关闭。

定价报hash mismatch时先核对原字节。当前三个blob各有一个CRLF，LF摘要与Git提交及manifest完全一致；仅C盘验证副本作了登记对照。主仓仍需授权的 .gitattributes 精确 -text 规则才能持久防止转换。不得重写manifest或放宽生产hash校验。生产缓存和远端获取是独立来源，本批未验证线上目录或打包产物。

## 第三批 Windows 平台诊断处理

PID来自外部状态时，超过DWORD范围必须在查询前拒绝，不能截断后探测到另一进程。processalive/ptyregistry仍按既有契约把AccessDenied视为存在；预览进程无法获得可信创建时间时不能据此终止其他树。

Windows仅查当前SID使用TOKEN_QUERY伪令牌，不Close。ACL原生解析须逐点核对头、ACE类型及SID偏移；未知有效授权继续拒绝。Job Object的uintptr包装参数现用Pinner固定缓冲到同步调用结束。清理句柄失败不能改写为ACL/刷新副作用未发生。

runfile实际使用MoveFileExW，不能为lint改成先删目标；工作区真实路径仍通过固定句柄获取。readProviderConfig/browsersUnder在darwin/linux有调用，Windows unused不构成删除依据。原b2摘要中文UTF-8正常、U+FFFD=0，控制台乱码不修改输入证据。

55项所属13包分组lint为0 issues；完整lint超时失败、完整service/agent原生登录和mode等失败保持独立待办。两条symlink边界本批SKIP，SV-M6-06未重试；本批不写真实注册表或配置、不调整权限。详见m6-report.md第三批。


## Windows 私有目录与账号回归的最终证据

Windows的0777/0666模拟mode不能证明ACL泄露；需查当前所有者、DACL允许主体及SE_DACL_PROTECTED。本批Reviewer和模型发现曾真实继承Everyone读取ACE，而账号槽位原ACL已合格，不能一概归为fixture。

SetSecurityInfo设置目录会向既有子项传播，硬链接共享安全描述符，因此可能先改外部目标再拒绝。Reviewer与共享账号目录现只设置已验证句柄；未知既有子项不递归改权。私有文件与device credential的文件策略继续分别验证，不能把目录修复当作设备凭据文件修复。

Windows路径os.Stat身份可能延迟读取；manifest保全测试现从调用前后文件句柄固定卷号/文件ID。成功登录fixture必须在外部修改结束后才模拟子进程退出，并等待自动验证；真实重复CloseShellTerminal返回NotFound，提交准备已接受该明确终态，不能将fake写成重复nil成功。

工作区Stat成功不代表能读目录：本批真实DACL拒绝ReadDir后验证SESSION_WORKSPACE_UNREADABLE；测试清理恢复原ACL。默认用户目录fixture要同时设置HOME/USERPROFILE，自定义状态目录必须使用daemon启动时解析的绝对DataDir，不能在chdir后重解相对环境变量。

本批两项symlink失败原样保留，未调整系统权限或新增skip；SV-M6-06仍停手。完整包与race/lint的实际退出码详见m6-report.md，不以局部通过替代M6整体门禁。

## SV-M6-07：进程查询失败不是死亡

`observe process`、`wait for process`或`observe pty-host pid`错误表示Windows查询未成功，不能据此删锁/描述符/run-file或再次启动。PTY扫描会保留原文件并返回不完整错误，该registry的注册/注销暂时闭锁，观察恢复后再重试。合法PID的ERROR_INVALID_PARAMETER或已触发进程句柄可证明退出；AccessDenied继续按原有存在性规则保留占位。

`ao status`只有服务名和PID健康核验成功后才会报告ready；查询失败且HTTP未确认时保留unhealthy。`ownership could not be verified`或`exit was not confirmed after teardown`都不表示已停止。HostPID的只读计量遇到未知返回无确认PID，不授权清理。最终行为、命令、真实子进程退出恢复及保留的中间FAIL见执行报告本批章节。

浏览器测试另有已确认历史污染：HKCU\Software\Google\Chrome\NativeMessagingHosts\dev.ao.browser_bridge仍指向旧TestSetupWithoutChromeFails212292381 scratch manifest。HOME/USERPROFILE隔离不能隔离HKCU；本批禁止Setup/Remove及相关全量行为测试，只读监测精确键，不清理真实键，后续隔离任务另行下发。旧.claude.json八条记录同样保持未授权清理状态。

codexappserver 112条和persistenthost journal 1条旧失败已由统筹匹配后续完整race PASS，不应另行返修；journal取消Unix mode断言的PASS不等于实际Windows ACL验收。本批不扩展ACL范围，也不重开SV-M6-06第四轮。

## SV-M6-08：Chrome 测试隔离与旧注册残留

上节“后续隔离任务另行下发”为 SV-M6-07 时点记录；SV-M6-08 已修复隔离入口并完成本包验证。`testInstaller` 必须显式注入目录模式或已有内存 fake，不能只设置 HOME/USERPROFILE/AO_DATA_DIR。生产 `Installer` 未提供注册工厂时仍走平台默认实现；测试工厂显式返回 nil 才表示目录注册。

完整包和完整 race 各13 PASS/1 SKIP，固定2.13.2 lint为0 issues，均exit0。唯一SKIP是既有真实Chrome E2E；没有提供其三个端点环境变量。后续验证仍须先检查所有Installer入口，再用C盘隔离runner串行执行，并在每条命令前后只读核对三配置SHA/mtime及精确HKCU键，不能为复现旧失败先写真实注册表。

精确键为 `HKCU\Software\Google\Chrome\NativeMessagingHosts\dev.ao.browser_bridge`。当前默认值仍指向 `C:\ao-m6-tests-507820bdd394\TestSetupWithoutChromeFails212292381\001\data\browser-chrome\dev.ao.browser_bridge.json`，类型REG_SZ、subkeys=0、modified=`134385530039501919`，本批前后身份未变。没有可信污染前备份，不自动删除、改写或通过真实setup/remove“修复”该键。旧污染仍未关闭，真实Chrome验收另行处理。

源码只读审计不必使用产品测试的隔离HOME。此次冻结辅助命令因此遇到Git dubious ownership，exit1与诊断exit128原样保留；沿用已验证输入HEAD和冻结清单核对两文件即可，正常只读Git审计可使用原上下文。不要修改global safe.directory、目录所有者或添加通配信任；产品测试必须继续隔离。完整证据见执行报告SV-M6-08与C盘`m6-bi-handoff.json`，其他M6失败、SV-M6-06停手及Claude旧记录事件保持不变。

## 剩余自动门禁的最新对账（2026-11-08）

完整lint本次结果为exit 4 / 623.267秒 / 0 issues.
level=error msg="Timeout exceeded: try increasing it by passing --timeout option"；完整lint仍失败；本批只运行一次，不加超时或原样重跑。旧315 FAIL不是315个当前缺陷：精确关闭287项，剩余28项在C盘m6-gates-remaining-matrix.md逐条给出原日志、类别和下一步。父测试部分执行、包超时、原命令FAIL及测试无终态不会被最后一个PASS覆盖。

Windows默认HOME测试须同步USERPROFILE，JSON路径应解码后比较。只定位假程序时使用平台可识别后缀；需要实际执行的fixture使用临时.cmd或明确Python解释器，不借真实Agent版本命令。MSYS pwd给出/c路径，工作目录回归应读取宿主原生路径。OMP只读SQLite URI须禁止把盘符作为host，同时保留转义与mode=ro。

telemetry空文件恢复出现sharing violation时，所有同一标识文件的读取也必须参加共享锁。Go/Electron均保留20秒等待截止，过期锁删除失败返回原错误；不能不断continue绕过截止，也不能删除锁内未知内容。完整telemetry包race结果为exit0、146 PASS, 0 FAIL, 0 SKIP；fake Do覆盖请求，无真实出站。跨语言同时竞争仍未验。

symlink的A required privilege is not held、Kimi复制回退的mtime选择、Unix mode600与Windows ACL、Unix Node shebang和Windows免tmux是不同边界。Reviewer Unix Node场景保留FAIL，在Unix验证，不扩Windows产品行为或新增skip。Kimi复制回退、Darwin Cellar/盘符PATH夹具及Registry父测试整组验证仍列自动待续，不凭猜测关闭。

长检查沿用C盘缓存并每秒观察8GiB门禁；触及时只停止该命令准确进程树，保存原码和样本，不清缓存。每次验证前后及运行间隔只读核对三份指定配置、精确HKCU键；前端前缀记录也纳入审计。旧污染未恢复；SV-M6-06禁止第四轮，完整存储race/前端/打包TLS未无条件重跑。M6整体仍未通过。


### 收尾串行审计失败（2026-11-08）

首轮`gates-close.py`返回exit1：`AssertionError: [('frontend-node24-m6-gates-typecheck', 'm6-gates-source-freeze')]`。类型检查08:58:01.194327—08:59:12.274242 UTC，与只读源码摘要核对08:58:49.384030—08:58:53.885582 UTC重叠4.501552秒。因此不能声明全部验证/审计记录串行通过；该审计失败原样保留，不豁免或回写PASS。两条命令各自exit0，不消除调度审计失败。首轮源码核对、Git差异检查和进程清点均exit0。

交接补充保留此次失败与时间区间，并重新核对文档更新后的摘要；没有重跑业务测试或完整lint。更新后源码/文档身份见C盘`m6-gates-delivery-source-manifest.json`；旧`m6-gates-source-manifest.json`保留为文档订正前快照。交接文件生成成功仅表示证据已写出，M6、完整lint和本次串行审计均未通过。


## 六项遗留的裁决与夹具处理（2026-11-08）

Kimi共享两例在检查字节后还要求Readlink及AO写入同步源端；即使调mtime让首个断言通过，也仍未满足共享契约。生产无symlink回退按mtime复制有独立新旧副本保护用例；该历史PASS只证明复制逻辑。保留共享FAIL，分类为symlink外部能力限制，不新增skip、修改真实目录或把0600当Windows ACL证据。

Windows假程序需要同时考虑候选名称顺序和PATH：Codewhale先找.exe，前置.cmd目录仍可能让后续PATH中的用户.exe先命中。本批Registry Windows测试PATH只含fake目录，完整父组及38子项race通过。

Darwin服务测试在Windows运行时，实际filepath.IsAbs边界须给宿主临时绝对Cellar；不要把/opt/homebrew错误归为清理生产缺陷。verifier、--cellar和进程引用必须同源；可变launcher保留独立目录。共享fixture未知命令直接失败，不回退真实安装器。Kimi目标Darwin的PATH列表不能夹Windows盘符，测试只把临时目录表示转为无盘符正斜杠，生产分隔规则未改，原目录与旧bin去除断言保留。

本批两条race合计59 PASS事件、两包lint exit0。原315事件291关闭/24保留，剩余矩阵见C盘m6-residual-remaining-matrix.md；完整包历史21 PASS/29 FAIL未被覆盖。首轮修改脚本换行断言失败及Registry部分写入已留档，按既有换行SHA登记恢复执行，不回写旧证据。

前批完整lintexit4超时、串行重叠FAIL、存储race超时、完整前端及打包TLS失败保留；SV-M6-06不重试。三真实配置/HKCU旧身份保留；不清缓存或E盘旧.tmp，不进行真实Provider/浏览器/Hello/安装操作。M6未通过。


## 完整门禁只读归因（2026-11-08）

存储超时先看整包预算与活动用例耗时：此次活动用例18/51秒，栈位于迁移；不要把t.Parallel等待或数据库后台goroutine判为业务死锁。非迁移fixture可评估复用sqlitetest，迁移历史测试仍需真实迁移。

Node24测试要求ABI137，Forge会在源依赖目录重建为Electron ABI149；当前模块导出标记与.forge-meta均149。历史55 PASS只适用当时137依赖。后续若获授权只从既有摘要核验归档离线恢复测试依赖；不要运行stage-sqlite-prebuild.py，它会下载。完整lint缓存固定C盘，缺阶段日志时不猜缓存丢失；仅建议下一次授权检查加-v/profile，不增超时。Forge缓存ZIP仍需SHASUMS网络校验，现有日志无法确认失败请求；不关TLS/checksum，不重复probe或打包。

C盘短报告：`C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-diagnosis-report.md`；事实表与最终身份/资源/串行证据见同目录`m6-diagnosis-facts.json`、`m6-diagnosis-HANDOFF.md`。399项输入源码摘要保持`7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590`，最终四文档SHA以本批清单为准。24项平台/能力限制暂缓，SV-M6-06三次停手，前批串行重叠FAIL及旧完整门禁FAIL保留。未提交/Push/Merge/发布；旧Claude/HKCU状态未清理。


## 存储与ABI有界复验（2026-11-08）

离线恢复的Node137模块已实际加载并通过内存SQLite读写与55项资料导入检查。恢复仅使用已登记归档，没有下载或重装；旧Electron149模块及.forge-meta位于C盘m6-bounded-electron149-backup。当前依赖目录不保留过期149 sidecar，结束状态是Node137，未来不能据此假定Electron可加载。

两条原活动测试独立race用例分别55.27/54.80秒通过，另外五项也通过，支持此前整包预算耗尽的归因；本批没有改真实迁移或业务断言。未重跑完整套件，不能据局部通过宣称完整存储门禁通过。

本批沿用399项源码清单，摘要7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590；生产/测试/配置/依赖清单0修改，仅C盘原生模块与四执行文档变化。完整命令、依赖前后身份、资源/串行/宿主核对及交接见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-bounded-HANDOFF.md。未跑完整前端/存储/lint、下载、打包或桌面；24项限制、SV-M6-06三次停手、旧完整门禁和前批串行FAIL保留，M6仍FAIL。未提交/Push/Merge/发布，旧Claude/HKCU状态未清理。


## 完整自动门禁单次复核（2026-11-08）

本次完整lint在同一10分钟/并发2/固定C盘缓存条件下exit0，verbose提供加载17.861秒、linters7.940秒证据；不应把旧超时改写成已确认缓存缺失，也不需要继续第四次完整lint。现有配置与排除规则未改，CPU profile保留但不继续无目的分析。

Node137依赖条件下完整前端636文件、10896项通过，14项仍跳过；旧50失败全部取得本次完整覆盖。没有启动Electron或重建149，不能将Node测试通过等同桌面运行成功。

本批完整命令、原始日志、JSON断言、阶段耗时和身份/资源/串行审计见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-final-auto-HANDOFF.md及m6-final-auto-results.json。未改源码、测试、依赖清单或配置，399项源码摘要仍7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590，复用既有清单；仅同步四执行文档。未下载、重装、清缓存、打包或启动桌面；前批串行FAIL与旧Claude/HKCU状态保留，未提交/Push/Merge/发布。


## 存储完整race逐包复核异常停手（2026-11-08）

如果监测命令结束但没有watch终态或测试metadata结束字段，应先核对原PID、创建时间及命令身份，不能只看外层exit码就继续下个包。本次外层exit1、注册观察commandExitCode=1，但实际Go→Go→sqlite.test.exe子树仍存活，已精确停止；监测/runner退出机制未明，没有panic栈，不能归为产品死锁或已证实环境问题。

最小下一步仅建议统筹先诊断监测/runner为何未写终态且未收掉子树，并验证异常退出后的证据落盘及后代进程回收，再决定是否授权新运行；本批不修监测器、不启动store、不自动第三轮完整race，也不延长预算或改迁移fixture。

原始结果、精确停止证据、缺失门禁与当前身份见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-storage-full-HANDOFF.md。399项源码和六依赖清单未变，沿用摘要7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590，仅同步四文档。未新增Agent、下载、打包或桌面；sqlitetest/前端/lint未重跑。M6仍FAIL，24项限制、SV-M6-06三次停手、旧串行FAIL和Claude/HKCU状态保留，未提交/Push/Merge/发布。


### 存储监测异常的有界只读归因（2026-11-08）

已查19:33:40–19:34:39 +08:00的Application、WER-Diag/Operational和System资源耗尽事件，三项均NoMatchingEventsFound，无拒绝权限；没有扩大窗口或系统扫描。现有外层exit1、ro-run记录的子命令exit1与缺失watch/测试结束metadata只能确认异常退出，触发原因仍未知，不能推定OOM、系统杀进程或业务缺陷。源码确认监测/执行链缺少统一finally收尾，异常可能绕过终态落盘及残留子树回收；这是代码可确认的健壮性缺口，并非已证实的此次触发原因。

本批没有修改runner/生产/测试/配置，没有故障子进程或业务测试。最小建议是由统筹另行授权补足异常证据落盘与精确回收，再决定短故障验证；当前不具备直接恢复存储全量的证据。简短证据与最终进程/身份观察见C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-runner-cause-HANDOFF.md。399项清单不重生成，M6仍FAIL；既有前端/lint PASS与存储异常停手边界不变。


## 验证脚本异常收尾补强（2026-11-08）

C盘验证runner现在先将本次暂停创建的进程纳入私有Windows Job，再恢复执行；可捕获异常时保留原错误和未知业务退出码，按Job成员回收并核对残留，观测/终态独立落盘。唯一启动后故障注入已验证RuntimeError→exit95、commandExitCode=null、残留0；不使用进程名或复用PID推断归属。不能把这次通过解释为原外层exit1原因已查明，也不能宣称finally保证OS强杀或断电下的记录完整。

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

查看覆盖时先区分目录枚举、单项终态、分段合并与完整包退出码。当前store 561/561仅表示分段父项证据齐全；最新完整store仍exit1、900.460秒超时，不能用补验exit0覆盖。sqlite仍31/168，旧监测INCOMPLETE根因未知。一次只读历史JSON汇总因UTF-8 BOM报JSONDecodeError，改用utf-8-sig后读取成功；该exit1不属于业务补验，也没有改变原始证据。

本批唯一补验使用go test -race -p 1 -parallel 2 ./internal/storage/sqlite/store -run <18项精确锚定表达式> -count=1 -timeout=15m -json，实际表达式、隔离入口及退出码见m6-storage-remaining-results.json。18顶层、19子项PASS，0 FAIL/SKIP，exit0；包100.504秒，内层112.424秒、外层113.743秒。已PASS项未重复选择，未启动第二条业务补验。

sqlite的137项缺口按预算分支暂缓：55项直接迁移、98项使用历史快照（两类可重叠），已有migrationSnapshots缓存。参考历史同类25.33、34.67、55.27秒，55项按25–55秒估算1375–3025秒，超过剩余单条900秒预算；这是量级估算，不是137项实测，也不表示每项同耗时或均重新空库迁移。

补验内外层各113次资源采样，最大间隔1.008126/1.507869秒，外层3次源码核对；最低C盘11889790976字节。三配置和精确HKCU身份未变，两层Job before/remaining/errors均空、terminated=false，收尾未见Go/test/lint/本批runner残留。本批未改源码、fixture、runner、配置或依赖；403项源码摘要仍为0df24e5a17c80454a07125b3b5b2c53bb14647cc0271d2beaffd7dab01dfe4fc。同步前826份双树SHA及674份历史适用性文件核对一致，七脚本与Node137模块不变；冻结清单不重生成，四文档新SHA单列在本批结果。

短交接：[m6-storage-remaining-HANDOFF.md](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-storage-remaining-HANDOFF.md)。逐项覆盖、137项缺口及源码/日志行号：[m6-storage-remaining-coverage.md](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-storage-remaining-coverage.md)；命令、身份、监测及SHA：[m6-storage-remaining-results.json](C:/Users/11459/AppData/Local/Temp/ao-m6-safe/m6-storage-remaining-results.json)。

分段覆盖不改写完整命令结果：历史完整store超时FAIL、sqlite监测INCOMPLETE继续保留，M6仍FAIL。旧sqlite异常根因、24项限制、SV-M6-06三轮停手、打包及真实环境未验、run.py两行身份锁止仅静态检查的边界不变；本批未新增测试/框架，未重跑完整套件或桌面，未提交/Push/Merge/发布，旧HKCU状态未清理。
