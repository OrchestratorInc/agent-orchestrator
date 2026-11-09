# M6 六项自动遗留与平台边界收口

日期：2026-11-08。状态：本批限定范围已独立验收，4项实跑关闭、2项确认symlink前提并保留FAIL；不是新阶段，前批完整lint和严格串行审计FAIL仍保留。输入为前批397项源码快照`6349d3a0234c0836670788216af39db75793a7236c643b8c37e1dc18c0e2d139`，以C盘`m6-gates-delivery-source-manifest.json`最终清单及其交接文件为准；旧`m6-gates-source-manifest.json`的文档摘要早于串行审计说明修正，不作本批输入。

## 目标

前批对旧315个FAIL事件关闭287项、保留28项；完整lint仍exit4，输出`0 issues.`后超时（623.267秒）。本批只处理剩余矩阵中的六项自动工作，明确其生产契约、可验证条件和最终结果。禁止为了把数字清零而改变期望、加skip或扩展产品平台能力。没有自动证据的项继续未关闭，不要求用户临时操作。

| 项目 | 当前证据 | 验收要求 |
|---|---|---|
| Kimi `TestGetAgentHooksReseedsHookOnlyAOManagedConfigFromOAuthUserKimiHome` | 当前源凭据先写、本地副本后写，复制回退保留较新副本；用例后续还要求真实共享链接 | 核对共享与复制回退契约、全部调用方及已有新旧副本测试；只修确证的生产缺陷。若用例本质依赖本机缺失的symlink能力，凭完整断言和生产分支证据调整限制分类，原FAIL不关闭 |
| Kimi `TestGetAgentHooksSharesTheUsersKimiLoginInPlaceOfAStaleCopy` | 用例要求AO端写入后用户侧立即看到更新，复制不能等价于共享 | 同上；不得为通过而覆盖较新用户凭据、删去共享断言或修改真实Kimi目录。旧0600断言不能证明Windows ACL；如本批触及凭据写入安全，核对实际ACL并复用仓库既有安全写入边界 |
| Registry `TestGetAgentHooksFootprintIsGitignored` | Codewhale子项已通过，父组只有部分执行证据 | 先审完整组每个Harness的fake程序、目录和调用边界，再安全运行整个顶层组；不启动用户安装的Agent、不继承真实凭据。不满足隔离条件先修fixture，不直接运行冒险取证 |
| Systeminstall `TestBrewCleanupRetainsDetachedProviderHub` | Darwin Cellar fixture返回`/opt/homebrew`，Windows宿主绝对路径检查拒绝 | 审共享fixture和全部调用，保持进程引用保全、退出后仅清理目标旧版本的业务断言；仅调整测试路径装配，不执行brew或真实清理 |
| Systeminstall `TestKeptKegIsCleanedUpOnceSessionsMove` | 与上一项共享Cellar fixture | 与上一项按同一根因处理，保留活跃进程保护、迁移后只清理一次的断言 |
| Systeminstall `TestKimiUpdateReinstallsAdvisoryReleaseIntoConfirmedDirectory` | Darwin冒号PATH夹入Windows盘符，去旧bin结果失真 | 核对被测目标OS与真实宿主路径的边界，保留确认目录、旧bin移除及安装前后检查；如确属不可在Windows表达的目标平台契约，准确记录限制，不改生产分隔规则求绿 |

## 顺序和验证

1. 验证397项输入及前批交接，读取六项完整源码、现有测试和直接调用方，先交付根因/契约判断，再做必要最小修改。已实跑通过的前批测试不重复。
2. 复用现有测试，优先一个根因对应的完整顶层用例；生产变更补受影响包回归和race，fixture修改运行受影响组。保持原断言，不新增不必要用例。测试规模例外继续明确申报。
3. 如有源码修改，只运行相应包的固定2.13.2 lint。本任务不授权再次完整lint，不扩大超时、不修改lint配置。上一轮完整失败继续保留，可只读分析已存在的日志和工具行为，为后续决策给出具体证据。
4. 刷新315事件/50包矩阵，区分本批通过、限制分类变更、尚可自动修复及无证据项；原始FAIL、包级失败、完整超时和未终态均保留。部分子测试PASS不能覆盖父组。
5. 更新四份执行文档，交接精确源码、依赖/换行登记、全部命令（含不同前缀）、配置/HKCU观察、进程/空间、串行记录。统筹独立验收后决定后续动作；M6仍未通过，不提交或进入下一阶段。

## 安全与调度

只保留统筹和现有一个执行会话；不开子Agent，不使用codexhost-delegation。沿用C盘副本、依赖和隔离runner，8GiB启动门禁及运行中空间监测。验证和源码冻结均串行，避免前批类型检查与只读冻结重叠4.502秒的例外再次发生。副本不reset、不重装、不共享node_modules；不扫描、复制或清理E盘旧.tmp。

每条验证前后只读核对三份指定真实配置及精确Chrome HKCU键；旧Claude八条记录、旧HKCU测试指针不回写或删除。只用虚构凭据、临时目录、fake Agent/installer，不调用真实Provider、Hello、浏览器setup、安装升级或远端CI。PowerShell非登录、rtk、Python UTF-8及Git禁自动维护规则不变。

SV-M6-06已连续三次失败，禁止修改或第四次尝试。其他同一问题连续三次修复失败即停止该项并报告，继续独立可执行工作。不得把共享、复制、Unix mode与Windows ACL的不同语义混为一项PASS。

## 独立验收结果

本批只改4个既有测试文件。Registry限制Windows PATH避免真实.exe抢先，安装共享fixture对未声明命令失败闭锁，统一Cellar临时路径并保留活跃进程保护；Darwin安装计划使用无Windows盘符的目标路径表示。Kimi两项完整断言要求双向实时共享，分类改为能力限制，未修改生产回退或断言、未记PASS。

`m6-residual-registry-race`exit0/10.505秒、39 PASS；`m6-residual-install-fixtures-race`exit0/13.397秒、20 PASS；两包固定2.13.2 lint exit0/39.873秒、`0 issues.`。旧315 FAIL关闭291、剩24（21能力限制/3平台限制），本矩阵无自动待续项；此结论不覆盖完整门禁失败。

最终399项源码摘要`7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590`，清单`m6-residual-source-manifest.json`。统筹独立核对双树798个源码摘要、四文档/六依赖、4条变化换行登记及变更遗漏，errors=[]；108份证据摘要和609个原始事件引用匹配。12条运行/观察/空间监测无重叠，三配置和HKCU身份前后/间隔未变；最低C盘12224811008字节，最终12236296192字节，任务/test.exe均0、未kill。两次辅助脚本失败原码保留，测试规模例外已申报。本批没有重跑完整lint、修改生产源码或执行真实Agent/安装器。
