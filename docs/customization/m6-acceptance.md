# M6 统筹独立验收

2026-11-09提交与合并决定：用户已安装试用，并明确要求“提交并合并把，我已经安装试用了，有bug再改”。本次按用户授权将M6作为开发检查点提交，并快进合并到本地`main`；不Push、不发布，不把原有FAIL、SKIP或未验项改为PASS。完整store超时、SQLite剩余46项、SV-M6-06真实分离宿主交互失败以及真实Provider/升级等未验边界继续保留，后续根据试用反馈修复。

本次仅核对Git状态、提交范围和差异格式，不重跑完整测试。此前位于C盘临时证据目录的三份抽查文件当前不存在，因此历史测试结论依据本文件已有验收记录，不声称本次重新核验了原始日志。以下保留各批次历史记录，其中“未提交”“不合并”为当时状态，实际提交与合并结果以Git记录为准。

用户使用反馈（2026-11-09）：用户明确报告“我已经成功安装”，安装成功记为用户确认。尚未获取安装日志或核对安装路径/数据保留，不等于统筹独立安装验收、升级验收或功能运行通过。用户当前进入功能使用指导，后续按实际界面与行为反馈核验，避免重复要求安装。

2026-11-09安装器增量验收：现有Windows x64候选已通过NSIS生成安装器，命令`make --skip-package --platform win32 --arch x64`自然/包装及父级均exit0，98.325/99.293秒；统筹独立核对原始terminal、安装器/附属文件SHA和实际签名/版本，errors=[]，NotSigned/0.13.0。产物193863342字节，SHA256 `1d80e81d4997fba4553503a38ca517c25a3c24f7f9f3ab90288914a830f4cd44`，位置见[安装器记录](m6-installer-plan.md)。最低C盘9.338GiB、两层Job无残留；用户认可的配置身份已单项更新且持续稳定。安装器生成通过不等于运行或安装升级通过，M6整体仍FAIL、未提交。

2026-11-09最新结论：Windows候选已生成并通过静态产物核对，M6整体验收仍未通过、未提交。完整 `npm.cmd run build` 自然exit0/133.726秒，包装95来自回收一个残留成员，最终无残留；统筹独立读取terminal/下载日志，六关键产物、八脚本及双树报告SHA一致，`supervisor-package-native-owner-review.json` errors=[]。候选0.13.0/Electron44.7.0，最低C盘9.892GiB，指定配置/HKCU未变。固定Node24的本轮代理适配未推广到产品；候选不是安装器，桌面、动态模块、安装升级/E2E与SQLite46项仍未验。本轮收口，不再增加测试。下文按批次保留历史结果，当前候选状态以本段及[打包记录](m6-package-observed-build-plan.md)为准。

日期：2026-11-08。状态：最新完整前端与完整lint均通过并独立核对；旧315个FAIL关闭291个、保留24个，历史严格串行审计失败继续保留；**M6验收未通过，未提交**。[存储剩余覆盖收口](m6-storage-remaining-coverage-plan.md)已交接：store分段覆盖齐全，sqlite137项仍暂缓。基线为M5开发检查点 `acf9bafa8c14de5533a35f66d4d2c9bfc057baae`；此基线不是完整阶段PASS。唯一执行会话 `01a1b730-e243-7522-bf62-bc5ed6de3a79`，Astra high；统筹负责本文件及独立审查，执行方负责实现和报告。

最新存储结论：5文件10处首次建库复用既有模板，12项定向race/包lint通过；完整store仍FAIL/900.460秒/exit1，732 PASS（488顶层＋244子项），最后用例只运行1秒。统筹独立核对826份身份、39证据SHA及原始事件，errors=[]；三操作无重叠、Job残留0。报告重启68.43→2.09秒不等于完整超时关闭。停止完整包原样重跑，只补当前源码尚无有效通过证据的用例；sqlite监测中断仍保留。

当前结论：本机Windows CI命令范围已补齐，后端build/vet、完整integration/Manager/HTTP及profile相关race已有通过证据。最新完整前端exit0/1894.283秒，636文件、10896 PASS/14 SKIP；完整lint2.13.2 exit0/29.712秒、0 issues，旧失败留档。旧315个FAIL已逐项关闭291个、保留24个，50包最近完整结果21 PASS/29 FAIL；定向通过不覆盖包失败。最新完整store仍15分钟超时，局部与分段PASS不覆盖完整命令FAIL；sqlite监测中断未关闭。SV-M6-06真实分离宿主交互三轮失败已停手；打包TLS、gitworktree及账号/会话symlink权限失败继续保留。最新403项源码身份一致；store批826份双树SHA及39证据SHA独立核对，errors=[]。完整前端/lint此前通过证据保留，本批只改5个Go测试文件。最新三条操作无重叠；历史类型检查与只读摘要重叠4.502秒的串行FAIL保留。定价主仓CRLF条件、人工暂缓、其他OS和真实Provider未验另列；不进行M6提交或开启下一阶段。

最新覆盖收口：唯一补验18顶层＋19子项race PASS、exit0/100.504秒，store合计561/561顶层分段PASS；sqlite31/168，缺137项。统筹独立核对826份身份、55证据SHA、729项目录与592条原始顶层pass事件，errors=[]；两条命令内外terminal正常、Job残留0。子项SKIP及完整命令FAIL/INCOMPLETE单列。sqlite剩余成本只有同类估算，本批未强跑，不将分段覆盖当完整包或M6通过。

最新SQLite三组：新增91顶层＋42子项PASS，三组exit0、包281.291/233.390/520.223秒；SQLite122/168，剩46项。统筹41证据SHA、91条原始pass事件、选择集和六层terminal独立核对，errors=[]，见[分批补验](m6-sqlite-batched-race-plan.md)。用户追问M6耗时后不追加第四组；剩余46项保留为升级兼容/稳定性证据缺口，优先处理Windows候选构建。未证明整体PASS，不提交。

最新打包实证：观察器的两次原生重建误中止保留；调整为构建退出后校验产物后，完整npm build取得自然exit1/79.690秒。ZIP缓存命中，SHASUMS及回退ZIP均在release-assets.githubusercontent.com的TLS建立前ECONNRESET。监测无异常，1个残留Job成员已回收，包装码95；终态进程0、配置/HKCU稳定。原生149模块已生成但未运行，out不存在、无候选。统筹直接核对terminal/网络事件，errors=[]；不再原样构建，继续定位该代理链路。

## 当前授权与验收边界

候选运行预检已交接，启动0次：源码及包内均有打包版协议注册和生产版本/更新请求；现有目录隔离不隔离HKCU，故本批不启动，实际运行/ABI/API/退出均保持未验。统筹核对具体源码，接受该隔离阻塞结论。安装器生成可独立推进，任务见[m6-installer-plan.md](m6-installer-plan.md)；不将预检或make成功当安装升级通过。

安装器批次当前被宿主配置身份变化阻断，尚未执行make或下载。统筹已独立核对原始terminal：进程pid=null、自然码null、包装95，0.005秒；当前config.toml SHA为ac0837da…，修改时间01:08:18。执行方未修改配置，基线未变；等待用户确认是否认可当前状态后更新校验基线。该失败不归为NSIS产品缺陷，也不通过换入口绕过。具体见安装器计划的恢复条件。

用户明确要求本人操作做不了时先跳过继续开发。Windows Hello、手动启动及依赖的SV-06真实桌面Queue暂缓，不再以其阻塞其他开发；不自动代过认证或规避工具拒绝。完整build失败、Go历史失败、真实Provider/安装升级未验必须持续显示，不能将本次开发放行当作发布或整体完成。

已提交93个实现/测试/生成文件和11个阶段文档；M5相关定向回归、SV-01/02/03/04/05/07/08/09证据见[m5-acceptance.md](m5-acceptance.md)。仅在新改动影响已有结论时复测对应范围。

## 验收清单

| 项目 | 必须检查的事实 | 当前状态 |
|---|---|---|
| M6-01 需求覆盖 | Q1自动规划/手动任务；Q2四个Adapter；Q3三种干预；Q4协作与统一资源；Q5独立Worktree及门禁；Q6多仓本地；Q7同源双视图；Q8动态并发；Q9模型/profile/用量预算；Q10权限恢复，各自映射代码、测试和未验边界 | 覆盖矩阵已交付并预审；完整自动门禁、真实Provider和桌面边界仍单列 |
| 已定位23项集成失败 | 6项Session缺可信准入、17项M2 fixture非Git目录：修复后原失败消失，生产边界不降低，完整受影响包结果可信 | 定向23项通过；新隔离环境完整integration包PASS 740.982秒，包含后续stranded fixture修复 |
| M4工件超时 | TestM4ArtifactThroughRealManager的真实等待点、资源生命周期与修复后的行为证据，禁止只延长超时掩盖死锁 | 新隔离完整integration包中PASS 177.38秒；此前超时记录保留，耗时波动未完全解释 |
| Domain/CLI基线 | Handoff Note失败和Windows symlink权限分别对照，代码问题修复，OS限制如实记录 | Handoff修复后完整domain包exit0已核实；CLI symlink权限失败保留 |
| M6-02 恢复 | SQLite/Manager/临时Git装配，重启不重复Spawn/发送/Commit/Apply，UNKNOWN保留，旧epoch拒绝，逐仓结果和dirty工作区保留 | 新隔离完整integration/Manager、M4多仓恢复及M6 profile重开通过；Windows路径、锁、宿主恢复和清理检查通过，保留平台skip，不外推其他OS |
| M6-03 权限 | 原审批错目标/正文/过期/重放拒绝，Main凭据不进入Renderer/日志，Runner边界及进程回收 | 专用凭据最终进程边界与五包race、Windows后台无窗口及子进程检查通过；SV04污染清理待授权，本人步骤暂缓 |
| M6-04 构建 | 新条件下真实Got/packager链路成功后完整build，记录退出码和候选hash；不以Undici下载或dev启动代替打包 | 最新本轮代理适配后npm build自然exit0/133.726秒，候选六关键SHA独立一致；包装95及历史失败保留，运行/安装未验 |
| 安装升级 | 打包版实际userData隔离，升级前后事实和进程可核实 | 未签名NSIS安装器已生成并独立静态核对；实际运行/安装升级未验，HKCU及外联隔离条件未满足 |
| M6-05 使用文档 | user-guide.md、troubleshooting.md的创建/切换/干预/审批/预算/恢复步骤与实际能力一致 | 文档与代码/证据已交叉核对；候选已生成但尚未运行，人工步骤暂缓，不标候选操作验收通过 |
| M6-06 完整验证 | 适用CI命令、工具链、完整套件原始结果；发布/生产部署不作为验证；其他OS或外部条件缺口单列 | 未通过：最新完整前端及lint均exit0；完整存储race仍FAIL，补齐剩余有效覆盖，SV-M6-06宿主交互三轮失败已停止；Git完整包symlink权限失败；Windows CI命令范围已在本机覆盖，其他OS/远端CI未验 |
| M6-07 供应商引用 | 固定引用贯穿创建、规划、准入、启动和恢复，凭据隔离及空引用兼容，UI选择/清除/回显 | 分层离线及完整Manager/integration、profile五包race已核实；SV01/02/03/05行为修复通过；最新399项源码与换行身份独立复核一致，完整适用门禁仍未满足 |

## 提交与证据规则

完整验收仍需上述适用项的实跑证据；开发检查点与PASS提交分别标识。执行报告自称完成只作为HANDOFF，统筹核对源码集合、关键失败路径、命令及原始退出码后给结论。范围精确暂存，不提交配置、凭据、缓存、日志或产物；不Push/Merge/发布。

高I/O严格串行、缓存与产物在C盘。M5提交额外自动gc/prune已精确停止；后续Git写命令以进程级参数禁用自动维护，不改配置文件。取消请求后确认底层进程退出。统筹未并发启动测试。

## 首轮交接的返修结论

| 返修项 | 当前独立结论 |
|---|---|
| SV-M6-01 原生命名和固定profile短调用的effort | 行为返修通过；定向、完整Manager/Chat及相关race证据已核对 |
| SV-M6-02 空effort默认策略与原生无绑定参数 | 行为返修通过；Start/Resume/FreshIfMissing、Manager重开和profile完整race证据已核对 |
| SV-M6-03 平台凭据进入未选中子进程 | 限定专用命名空间的修复通过；daemon接管、实际Reviewer/Git/Driver/宿主边界及完整相关包/race已核对；不宣称OS沙箱或所有原生公用凭据隔离 |
| SV-M6-04 测试污染真实配置 | 新runner路径/环境隔离与三份真实配置监测通过；已发生的8条记录尚未获授权清理，事件未关闭 |
| SV-M6-05 跨Agent重规划误用模型 | 行为返修通过；实际IPC载荷红/绿、最终工作流/i18n36项及冻结源码已核对 |
| ConPTY中文系统断言 | 首次修复通过同组race/count=3；真实子进程Windows API尺寸及正常退出已断言，无skip |

这些结论只关闭对应行为缺陷，不使完整lint、打包或阶段总门禁自动通过。首次HANDOFF仍列出可本机执行但未运行的Windows CI命令；统筹已按[m6-plan.md](m6-plan.md)派回同一会话补齐，不新建阶段或增加执行者。

## 实施记录

M5执行会话已HANDOFF并idle后派发M6；新会话工具确认active/inProgress。提交后最新受保护统筹dirty为delivery-status.md、m6-plan.md、design.md及本文件，执行方应保留。

统筹复核历史超时证据：`go-m4-baseline.log`记包5m超时、TestM4ArtifactThroughRealManager自身4m57s；栈停在GetFileAttributesEx→os.Stat→exec.LookPath("git")→DeliveryTargetSHA→taskcommit.Commit。已向执行会话提供原始定位，不预判环境或源码唯一根因。补充比较JSON含21项，另2项在上述基线日志，23项计数已核对。

2026-11-08实施中独立审查：Session fixture补齐现有可信准入提供器、M2 fixture复用真实临时Git初始化，尚未见生产门禁放宽；Handoff修改在超过平台预算时缩减文件清单并保留遗漏计数，公开调用签名不变，等待完整受影响包验证。统筹核对本轮 `ao-m6-safe/m4-baseline.log`：独立90秒超时，栈停在Git子进程Wait→deliveryGit→verifyDeliveryRepo→InspectDelivery→artifact.Revalidate→taskcommit.Commit，区别于旧5分钟日志的LookPath等待点；目前不能判为同一固定死锁或仅包预算不足。未并行启动统筹测试。

覆盖审计发现首版Q9开发缺口：Workflow创建、规划补丁、Execution Coordinator和LocalGateway均拒绝非空Profile，尚无design§12要求的Workflow自定义provider/profile引用闭环。已派同一执行会话评估并补齐可自动实现/验证的范围，不能只靠“不支持”提示判PASS；不改用户原生配置或发真实Provider请求。生产网络Push仍未交付，与§14首版默认不远程push边界分开记录。

构建前置的新证据已独立核对：执行命令使用现有Node v24.22.1的--use-env-proxy，未修改packager内嵌GotDownloader，官方SHASUMS256.txt下载6740字节、sumchecker验证已有ZIP成功，exit0，TLS校验未关闭。该结果允许在新环境条件下串行重跑完整build，不等于build通过。M6-07供应商引用闭环任务文档已编写并派给同一执行者。

统筹独立解析 `ao-m6-safe/fixtures-green.log` 的go test JSON并核对元数据：23个原失败用例首轮18 PASS、5 FAIL，命令exit1、69.431s（包49.039s）。剩余为Delegate本机账号装配错误、3项多任务可信准入拒绝及1项临时目录归属断言；不能把补齐fixture视为全部关闭。等待执行方根因修复及完整integration包结果。

M4同版本Git直接入口对照：`m4-fast.json`记录exit1、320.425s，测试包5m超时；统筹读栈确认已到第二仓finish-review、服务端仍执行Git身份检查。换入口未闭合超时。组合场景还包含后续两仓预演/集成；5分钟不是产品契约，后续如能证明有界工作量及阶段耗时可据证据制定测试预算，禁止通过删除断言/生产检查或盲增超时宣布通过。

Domain证据核实：`go test -p 2 -parallel 2 ./internal/domain -count=1 -timeout 2m`，`domain-handoff.json`记录exit0、命令6.214s；日志输出 `ok .../internal/domain 3.258s`。此前两项Handoff失败已在同机baseline复现，本次为完整包成功；统筹已审阅改动和调用边界，尚未另起重复测试。

第二轮23项回归：命令exit1、58.237s，统筹解析22 PASS、1 FAIL，剩余Delegate用例返回CODEX_ACCOUNT_SETUP_INCOMPLETE / account_storage_unsafe。原fixtures-green前缀被执行方复用覆盖，统筹已保存本轮为 `supervisor-fixtures-round2.log/.json`，并要求后续每轮使用独立证据名；首轮18/5计数已在当时工具输出核实，旧文件不能再作为该轮原始文件引用。Q9宿主登记表、严格白名单、Codex初版映射、持久引用摘要及不扩权边界已落盘并放行实现，不需等待用户改真实配置。

新条件完整构建结果仍失败：首轮 `build-node24.json` exit9（0.68s），内层npm回落Node22，拒绝NODE_OPTIONS；修正临时npm入口后 `build-node24-nested.json` exit1（50.947s），Vite构建完成，Forge打包仍报TLS连接提前断开。Got小清单probe成功不能替代实际打包URL/缓存命中验证；已要求先核对差异再决定后续构建，不盲目反复下载。

第三轮23项回归已核实：`fixtures-green-20261107T184015.json/.log`，exit0，命令20.063s、包15.005s；逐项解析为23 PASS、0 FAIL、0 SKIP。使用新建私有C盘scratch（ACL设定exit0），既有生产账户安全校验未改。该结果关闭定向23项失败，不代表完整integration包通过；剩余M4组合场景仍需单独结论。

Q9分层证据已核实（尚非完整验收）：`profile-auth`完整providerprofile/capability包exit0（2.362s/2.497s），公开目录不授权、凭据轮换与原生账号隔离用例在内；`profile-driver`定向Driver/持久宿主exit0（4.399s/2.946s）；`profile-e2e`正式HTTP→Scheduler→真实Manager/SQLite/Git离线链exit0，TestM6ProfileHTTPManagerRestart为21.55s（包25.225s）。统筹已阅读此用例：包括Planner只读、确认候选、SQLite重开、固定profile继承、禁止原生模式/账号切换、配置漂移拒绝、Worktree保留与Stop & Replan。它用Scripted Driver，不替代真实Codex生成，也不代表全部恢复路径或完整包已验。API spec/types已实际生成exit0；其余9包-run ^$仅编译通过，不计作行为测试。

统筹预审返修SV-M6-01：Q9把原生effort清空从runBackgroundTask移入共享runRecordBackgroundTask，后者还由task_naming.go直接调用并预先写入已解析effort，导致原生任务命名丢失旧设置。已派回唯一执行会话，要求恢复各调用路径原语义并验证，不能把空profile路径回归视为新功能正常差异。尚未关闭。

完整Go仍运行中：统筹抽取当前输出，已见多类Windows前置失败（如codexappserver的/tmp/ws不是绝对路径、部分reviewer的Unix二进制路径与chmod mode断言）。尚不能用其作为实际行为覆盖，已派执行方分类并优先修复受影响包的可移植fixture，保留生产路径/权限校验；不批量skip、不把历史失败自动豁免。

统筹预审返修SV-M6-02：登记表允许空effort并向UI公开，但Codex Start/Resume/FreshIfMissing要求返回effort与空串相等；未指定effort时Provider合法解析为默认值也会启动失败。已派执行方闭合登记、协议响应和持久恢复的一致语义，并验证真实Driver调用。ScopeCapabilityRequest对无Workflow绑定的原生Session清空Model/Effort也一并要求检查兼容性。当前两个问题均未关闭，不能以只覆盖显式high的协议用例代替。

M4单例新增实跑证据：完整Go命令为 `go test -p 2 -parallel 2 ./... -count=1 -timeout 20m -json`，`m4-fast-backend-full.log`在2026-11-08 03:35:05（北京时间）输出 `TestM4ArtifactThroughRealManager` 的pass事件，Elapsed=177.99。统筹已直接核对原始JSON；这次实际完成耗时低于旧五分钟预算，没有仅靠改大预算判PASS。此前超时原样保留，尚不能只凭此次通过解释全部耗时波动；完整integration和总命令仍未结束。

完整integration随后结束，exit1、650.076秒，唯一失败为 `TestKillAndCleanupReachASessionGitWillNotRelease` 缺可信准入装配。`TestM4IntegrationPartialRestart`通过、166.98秒；执行方已补该fixture，等待复测。全Go总命令仍运行中。

统筹预审返修SV-M6-03：Registry从daemon环境读取登记凭据，Codex `envSlice→processenv.Merge`却完整继承父环境，仅剥离旧Workflow租约。因此选择A时仍可把B的登记凭据传给Provider，普通原生Chat也会继承平台凭据。现有LookupEnv假值测试没有覆盖父环境中的多份秘密。已派执行方修复最终进程边界，并检查持久宿主、Windows大小写和overlay重注入及原生兼容；不得访问真实秘密，尚未关闭。

### 测试用户目录隔离事故（SV-M6-04）

完整Go最终exit1、1647.052秒，2026-11-08 03:45:53结束，原PID37924已退出。统筹检查runner发现仅TEMP/TMP/GOTMPDIR隔离，未设置测试子进程USERPROFILE；旧Claude fixture只设置HOME，而Windows `claudeLaunchConfigPaths(nil)`通过`os.UserHomeDir()`读取USERPROFILE。只读检查确认真实 `C:/Users/11459/.claude.json` 中有8条本轮私有scratch下的 `TestPreLaunchMarksAutoDefaultNudgeSeenOnlyWhenOffered.../worktree` 项目信任记录，文件修改时间03:19:09与该测试相符。此前“全部隔离且不触真实配置”的表述不成立；不能将该轮检查标为安全隔离验收通过。

证据为C盘 `ao-m6-safe/supervisor-config-audit.json`，仅记录本轮路径与文件元数据，没有输出/复制用户秘密。统筹已向用户说明、派执行方优先修复测试进程用户目录/AO/Provider目录及凭据环境隔离；在隔离证据核对前不运行新测试。真实配置不擅自回写。精确移除上述8条记录的脚本已只读预演，输出 `removeCount:8, preserveOtherProjects:true, preserveOtherFields:true`；因用户明确禁止未经同意修改配置，清理等待用户答复。未断言该审计覆盖所有历史真实配置影响。

统筹解析完整JSON：174个包PASS、50个包FAIL、22个包SKIP；含子测试事件为17333 PASS、315 FAIL、357 SKIP，不能将此事件数当独立顶层测试数量。原始失败分诊在 `m4-fast-backend-full-analysis.json`。这些是旧快照的历史执行结果，用户目录隔离失效使其不能用作本阶段安全验收通过凭据。新runner已改为系统环境白名单和每轮独立用户/AO/Provider目录，须先核对实际子进程路径与真实配置仅hash/mtime监测，再恢复串行验证。

隔离修复的首条实跑已核实：`isolation-paths-guarded.json/.log`，运行轻量Go路径预检exit0、0.934秒，`os.UserHomeDir`与AO/Provider目录全部指向新建私有scratch；`providerCredentialsPresent=false`。真实`.claude.json`、`.claude/settings.json`、`.codex/config.toml`前后仅hash/mtime比较一致。统筹已允许使用新runner恢复串行定向验证；此预检不替代实际业务回归、8条信任记录清理或其他文件影响审计。要求后续配置漂移必须作为隔离失败单列并停止后续检查。

### 新隔离环境中的返修验证

`sv01-03-focused.json/.log`：六包定向 `go test -p 2 -parallel 2 ... -run 'Test(Profile|HostProviderEnvironment|RawHostPins|Registry|MergeDrops|WorkflowNamingAndBackground|WorkflowResumePreservesDefault|TaskNamingAllShippedAgents|ScopeCapability)' -count=1 -timeout 3m -json`，exit0、69.671秒，65个含子测试PASS事件、无FAIL/SKIP，三份真实配置前后身份不变。统筹已直接核对原始结果并审阅原生命名effort、空effort策略、恢复不回填项目默认值及无绑定Session参数保留。SV-M6-01/02已有定向通过证据，待完整受影响包；SV-M6-03的Chat/持久宿主边界已通过，但独立审查发现Reviewer和部分Git进程还直接继承父环境，已派回扩展到实际最终调用链，尚不关闭该安全问题。

SV-M6-03补充修复已独立审阅：daemon在加载配置后、启动子进程前通过 `CaptureEnvironment` 将专用凭据保存到只读内存并从自身环境移除；原生公用变量不变，轮换需用更新的启动环境重启daemon。Reviewer预检、Git及Chat/TUI最终环境继续过滤专用前缀和大小写别名，选定Codex启动单独注入已验证密钥，既有受限Runner的环境白名单不扩大。`sv03-boundary-green`定向exit0、11.783秒；`sv03-reviewed-full`完整reviewer/agy、cursor、kimi、chatdriver/processenv、providerprofile五包exit0、6.516秒；两轮真实配置监测均无变化。完整Manager/integration及其余适用验证仍待结果。

完整三包检查已结束：`m6-integration-manager-full.json/.log`，`go test -p 2 -parallel 2 ./internal/integration ./internal/session_manager ./internal/adapters/workspace/gitworktree -count=1 -timeout 20m -json`，总exit1、763.569秒。统筹核对原始事件：integration PASS 740.982秒、Manager PASS 336.263秒、gitworktree FAIL 370.591秒。1779 PASS/9 FAIL/16 SKIP为含子测试事件，不是独立用例数；不能将整条命令报为通过。新隔离下M4工件177.38秒、多仓部分恢复163.72秒、M6 profile重启12.37秒均PASS。跳过包括现有平台限制、symlink权限及显式启用的桌面fixture，不证明这些路径已验。Git工作树失败为路径引用/分隔符断言和symlink权限不足；路径断言返修，权限失败原样保留。

后端完整构建与静态检查已核实：`go build -p 2 ./...`（`m6-backend-build`）exit0、16.337秒；`go vet -p 2 ./...`（`m6-backend-vet`）exit0、72.384秒。上述三轮的`protectedConfigUnchanged=true`，仅指指定三份真实配置的前后hash/mtime没有变化。统筹另对当时`profile-source.json`的100项逐一计算主仓与C盘副本SHA-256，200份全部匹配；执行仍在继续，此为同步检查而非最终冻结验收。

实际Electron下载链已核实：`build-node24-chain-explicit` exit0、1.470秒，使用显式Node24及本轮隔离LOCALAPPDATA缓存，通过packager内嵌`@electron/get`和Got下载官方清单并校验158293649字节ZIP。TLS与checksum保持启用；完整build仍待结果，此probe不替代打包通过。

统筹UI预审返修SV-M6-05（P2）：`WorkflowIntervention.tsx`将Stop & Replan的默认模型从原Planner改为当前任务模型，但请求及后端仍限定Codex。停止Claude/OMP/DeepSeek原生开发任务时会将其模型错误预填并提交给Codex Planner。已派同一执行会话保留固定profile继承并纠正跨Harness默认选择，要求通过stop_confirmed到实际runVerb载荷的行为检查；尚未关闭。

HTTP/API完整检查已核实：`go test -p 2 -parallel 2 ./internal/httpd/... -count=1 -timeout 10m -json`（`m6-httpd-full`）exit0、60.764秒，三份指定真实配置前后身份不变。该检查包括Go端spec漂移和路由一致性；不替代前端schema再次生成比较或真实Electron边界测试。

固定golangci-lint 2.13.2完整检查失败：`run --timeout=10m --concurrency=2`（`m6-golangci-full`）exit4、757.392秒，输出2721条诊断（含2439条goimports）及`Timeout exceeded`，真实配置监测无漂移。统筹按当时源码清单定位改动文件中的87条诊断，不能将这些全部视为新引入或已证实历史问题；已要求最小修复新增profile代码的具体诊断，不全仓格式化、不延长预算原样重跑。

SV-M6-05已取得失败复现：`frontend-node24-sv05-red.log`的实际runVerb载荷为Codex＋sonnet/low，期望已有Codex Planner的gpt-planner/medium；组件测试1失败、2通过。返修与验证继续，不能仅凭补了用例关闭。

SV-M6-05修复后已核实：仅原任务为Codex时继承其选择，其他Harness回到既有Codex Planner或空选择；模型、effort、profile同源。Node24下`vitest run src/renderer/components/workflow --maxWorkers=1`（`frontend-node24-sv05-green`）exit0、20.501秒，3文件13用例全通过，含停止未知拒绝、确认后实际runVerb载荷、固定profile继承。统筹已审阅源码与原始结果，修复行为通过，最终文件冻结待核。`npm run typecheck`（`frontend-node24-typecheck`）亦exit0、69.375秒。两轮三份真实配置监测均无变化。

`npm run typecheck:e2e`（`frontend-node24-typecheck-e2e`）exit0、10.506秒，仅为E2E源码类型检查，不是浏览器执行结果。完整前端`npm test -- --maxWorkers=2`仍在运行，已出现浏览器资料导入、i18n实例及文案覆盖失败；待最终错误按本轮改动/环境/既有源码分别分诊，不更改白名单或跳过断言求绿。

CI范围复核：`.github/workflows/go.yml`的`windows-workspace`定义Windows专用路径、环境、工作树、ConPTY、宿主恢复等检查；完整Linux分片race属于其他job。当前本机全包历史失败仍保留，不能用CI注释自动豁免。lint抽查的`cmd/ao/main.go`与`cmd/gencodexproto/main.go`在C盘副本分别有15/1045个CRLF，提交文本均为LF，归一化后完全一致；仅证明这两个格式诊断样本来自checkout换行，未外推全部2439项，也未全仓改格式。

完整前端最终exit1、1771.868秒：636文件中633 PASS/3 FAIL；10910用例中10846 PASS/50 FAIL/14 SKIP，三份指定真实配置无漂移。47项资料导入失败的明确错误为`better_sqlite3.node`编译ABI149，而Node24要求ABI137；修复隔离依赖环境后只复核直接受影响文件，不改产品安全校验。另3项i18n失败来自工作流交付：其他语言缺`workflow.activate`等资源，插值测试因此读到undefined；覆盖检测列出18处工作流组件文案（预算、干预、候选、供应商、质量页）。已派执行会话闭合本次工作流范围的既有i18n接线，不扩全站翻译，不改白名单/跳过测试。最终受影响组件和类型检查需重验，未改变的633文件不原样全量重跑。

后端新增lint修正的实际复核：固定版本对providerprofile、codexappserver、ports、capability四包检查（`m6-lint-profile-recheck`）exit1、52.877秒，仍有76条诊断；不可报lint通过，也不外推至未运行包。随后providerprofile、capability、codexappserver三个完整包（`m6-profile-lint-regression`）exit0、20.949秒，三份指定配置监测无漂移。此轮类型断言检查、只读Close处理等修改没有使相关行为回归失败；全量lint失败仍保留。

资料导入环境与i18n返修已核对：官方better-sqlite3 v12.11.1 Node ABI137包1039428字节，SHA-256 `4ee5e653174d6ddd301605d351798cdae2613da06c4f37ecdce263021fcf1255`；通过发布摘要核验后置于C盘npm预编译缓存，原生`npm rebuild better-sqlite3`（`frontend-node24-native-rebuild-cached`）exit0、2.683秒。随后`frontend-node24-related-recheck` exit1、25.579秒：资料导入55项、i18n实例22项及覆盖1项均通过，2项工作流测试因旧文案选择器失败，其余89项通过。源码修复只接入现有工作流语言资源/显示文本，协议提交值保持；没有修改覆盖测试或白名单。该Node二进制不作为Electron ABI149的打包模块，正式构建仍需Forge原生重建流程。

前端最终相关复核：旧标签选择器修正后工作流13项PASS（`frontend-node24-workflow-localized`，exit0、16.782秒）；`typecheck`/`typecheck:e2e`分别exit0、72.349/10.057秒。任务详情与候选列表供应商翻译接线收口后，`frontend-node24-workflow-i18n-final`再验5文件36项PASS，exit0、18.006秒，随后执行者声明冻结前端。以上均为指定真实配置无漂移；组合证据是“完整首次失败，受影响范围修正后通过”，不是完整前端重跑exit0。

新条件完整打包仍失败：`build-node24-package-cache`执行`npm run build`，exit1、72.724秒。Vite main/preload/renderer生产构建通过，Forge在Packaging/Copying files阶段报`Client network socket disconnected before secure TLS connection was established`，未产出可验收候选。已经验证的Node24、缓存ZIP及小清单链路没有使本次完整打包成功；失败记录保留，停止原样重试，不关闭TLS或checksum。打包之外的Windows工作树/race等串行验证继续。

冻结同步复核：统筹对`m6-final-source-manifest.json`的189个文件逐项重算主仓与C盘副本SHA-256，378份全部匹配。主仓HEAD仍为M5，实验checkout的嵌入提交仍为M4；源码以明确清单对应，不能把实验Git HEAD说成M6候选提交。此时项目依赖清单及受保护配置未出现本轮diff。

工作树完整包返修复核：`m6-gitworktree-full`最终exit1、382.547秒，包耗时380.809秒。统筹直接解析原始JSON，唯一失败用例为`TestWorkspaceIntegrationWorkspaceProjectCopiesAssetsAndCleansSessionCopy`，错误`A required privilege is not held by the client`，未将权限失败改成跳过。前轮路径引用/分隔符断言失败已不再出现；整个包仍不能记PASS。三份指定真实配置前后身份不变，后续profile五包race继续串行执行。

### 冻结后的 race 与 Windows 检查

统筹直接核对命令元数据及原始Go事件：`m6-profile-race-full`执行 `go test -race -p 2 -parallel 2 ./internal/service/providerprofile ./internal/service/capability ./internal/adapters/chatdriver/processenv ./internal/adapters/chatdriver/persistenthost ./internal/adapters/chatdriver/codexappserver -count=1 -timeout 10m -json`，exit0、216.129秒，五包均PASS，含子测试事件618 PASS/6 SKIP。`m6-manager-race-focused`执行Manager包的`-run Profile`，exit0、41.220秒，1项PASS；不是完整Manager race。`m6-windows-environment-race`执行完整agentlaunch包race，exit0、10.815秒，6 PASS/2 SKIP。上述跳过不算已验，三轮指定真实配置均无前后漂移。

`m6-windows-conpty-race`按Windows CI四个grid/args用例执行`-race -count=3`，exit1、14.062秒，三轮`TestConPTYStartsAtRequestedGrid`均失败。原始控制台输出为中文且尺寸是37行/146列，测试只匹配英文Lines/Columns；已派回做跨语言的实际尺寸行为断言，不改系统语言、生产尺寸逻辑或增加skip。此次是同一命令三轮执行，尚无此前三次返修；后续工作树及宿主恢复检查继续串行。

Windows工作树与会话清理已核实：`m6-windows-workspace-race`执行 `go test -race -p 2 -parallel 2 ./internal/adapters/workspace/... ./internal/session_manager -run 'Destroy|Discard|Sweep|Kill|RunPostCreate|TaskNaming|RenameTaskBranch' -count=1 -timeout 10m -json`，exit0、118.807秒，router/scratch/gitworktree/Manager四包通过；既有两项skip仍不算通过，不能据此覆盖完整gitworktree的symlink失败。`m6-windows-acp-resume-race`执行ACP包 `-run '^TestPersistentClaudeACPHibernateAndNativeResume$' -race -count=1`，exit0、15.916秒，无skip。两轮三份指定真实配置前后无变化。

ConPTY首次返修已独立核实：`m6-windows-conpty-race-20261107T213728`执行相同四项`-race -count=3`，exit0、11.379秒，24 PASS事件、无FAIL/SKIP。源码改为测试可执行文件自身作为真实ConPTY子进程，用 `windows.GetConsoleScreenBufferInfo` 读取列/行，明确核对146/37与正常退出；未改生产初始Resize或系统语言。`m6-profile-e2e-final`再次验证HTTP→Manager→SQLite重开单例，exit0、23.906秒；只覆盖该单例，不作为再次完整integration通过。

首轮HANDOFF同步核对：最终190项源码双树380份SHA、4份执行文档及6份依赖清单全部匹配，变更源码无遗漏。源码集合摘要 `fa680a419542b8b5d460d66458cd38dda910cd656970fd0d1cad9bd08afec340`；主仓M5基线和副本M4嵌入提交分别保持原值，没有M6提交或候选产物。12个既有超300行测试文件已在执行报告申报，新ConPTY测试文件106行。下一轮仅补齐未运行门禁及必要返修，不重跑无变化的已通过套件。

串行约束偏差已核实：`m6-golangci-full`于UTC20:32:34.853退出，`frontend-node24-sv05-red`于20:32:30.106启动，存在4.747秒重叠。不能宣称整个M6执行过程严格零重叠；该次偏差及退出确认失效已在执行报告记录。之后队列以命令实际退出记录为准，统筹不另起验证。三份指定配置该轮无漂移，不据此推定重叠没有资源影响。

### 首轮交接后补齐的 Windows CI 检查

统筹直接解析以下九条命令的元数据和原始Go JSON：均exit0、三份指定真实配置前后无漂移，合计240 PASS/43 SKIP事件（含父子事件）。命令保持 `windows-workspace` 的目标包/筛选/count，并限制 `-p 2 -parallel 2`；精确argv在同名JSON，原始输出在LOG。此前已通过步骤不重复运行。

| 证据前缀（均以 `m6-windows-` 开头） | 检查范围 | 秒数 | PASS / SKIP |
|---|---|---:|---:|
| sqlite-path-race | SQLite五项路径身份，race | 302.639 | 10 / 2 |
| datadirlock-race | data-dir锁完整包，race | 6.488 | 5 / 0 |
| cue-race | shellterm `^TestCue`，race | 13.806 | 48 / 0 |
| background-console-race | pi/omp/primeagent后台命令无窗口，race | 14.096 | 3 / 0 |
| subprocess-race | mobilebridge/process/systemexec的Cloudflared/Tunnel/Command，race | 19.160 | 60 / 22 |
| powershell-fixture-race | PowerShell安装测试fixture，race | 8.137 | 4 / 0 |
| goose-identity-race | binaryutil/goose完整包，race | 9.209 | 52 / 18 |
| opencode-shim-race | OpenCode慢npm shim版本探测，race | 11.978 | 1 / 0 |
| goose-readiness-race | service/agent指定就绪/清单筛选，race | 22.364 | 57 / 1 |

Cue唯一源码调整位于既有 `cueready_windows_test.go`：解析到PowerShell/pwsh后加`-NoProfile`，cmd加`/D`，随后仍由正式readiness/delivery实现构造命令。统筹已核对参数顺序和调用链，未修改生产启动策略。四种已安装shell均执行真实交互；安装检查仅本地假服务与临时脚本。跳过涉及Windows非法文件名及既有POSIX夹具等，逐项原因以执行报告为准，不把它们算通过。以上仅补齐本机Windows命令覆盖，不覆盖Linux/macOS、远端CI或完整gitworktree的symlink权限失败。

剩余lint复核已核实：固定2.13.2对execution、integration、session_manager、conpty、shellterm五包运行 `run --timeout=5m --concurrency=2`，`m6-lint-remaining-windows` exit1、96.825秒、三份指定配置无漂移。229条诊断为183 goimports、25 errorlint、6 errcheck、5 staticcheck、4 unused、2 govet以及ineffassign/nilerr/revive/unparam各1。分诊记录 `newFindings=[]`，其中28条位于改动文件的未改行；只能说明当前差分位置，没有干净M5全套lint通过证据，不能据此豁免完整门禁。统筹抽查 `LiveTarget` 的nilerr是通过false与`observation_failed`返回不可用证据，LocalGateway的部分非包装错误用既有权威错误类型表达UNKNOWN；未发现仅凭这些诊断即可认定的新行为缺陷，未机械改错误传播语义求绿。

最终交接已于2026-11-08完成：唯一执行会话completed/idle。统筹重新计算191项源码的双树382份SHA、4份执行文档及6份依赖清单，`mismatches=[]`、`omittedChangedSource=[]`。最新源码集合摘要为 `5070602346b081bcb4a8c96cb9a17714800a401b420d9c5fa55198f1c6d8691e`；早先190项摘要仅是历史快照。HEAD仍为M5基线，M6未提交、无可验收候选包。45个变更测试文件全文14926行、排除生成TS后的变更生产文件全文52424行，12个既有超限文件已申报。真实Claude配置中的8条测试记录尚待明确授权清理，未进行回写。

### 继续处理 lint 门禁

上段191项是上一轮交接快照；用户目标仍未达到，后续按 `m6-lint-closeout-plan.md` 继续原会话返修。五包换行对照 `m6-lint-five-lf-control` exit1、102.710秒，诊断由229降为48（goimports由183降为2）。对照记录183项；统筹对其中尚未被本轮代码修改的170项独立重算，主仓原始字节与记录一致，副本等于主仓仅CRLF转LF后的字节，零不符；另外13项已经进入代码返修，不能用当前字节冒充对照时身份。未批量改E盘源码或Git配置，剩余两项真实导入分组由执行方修复。

`m6-lint-contract-red` exit1、21.024秒：缺少事务能力的Store在内部准入入口发生panic；正常上游已有能力检查，所以该证据是异常接线拒绝能力缺口，不能外推正常配置可利用。返修改为明确返回 `ErrAdmissionRequired`，共享重附着校验同时覆盖成功发布和迟到失败回执。

LocalGateway错误分类反例：`m6-lint-unwrap-counterexample-red` exit1、8.654秒，验证副本临时扩大底层unwrap使Coordinator把UNKNOWN误判为 `failed_confirmed/spawn_refused`。mutation记录的恢复摘要与原摘要相同；正式实现保留单一权威分类，将底层原因显式转为文本。统筹已核对Coordinator先拒绝再UNKNOWN的分支顺序，以及spawn/resume调用仅在非空error时进入。`m6-lint-execution-race`完整execution包 `go test -race -p 2 -parallel 2 ./internal/service/execution -count=1 -timeout 5m -json` exit0、67.725秒，242 PASS事件、无skip。以上实跑的三份指定真实配置无前后漂移；其余修改和最终lint结果待验。

`m6-lint-manager-race`执行Manager的 `^(TestAdmissionStoreCapabilityIsRequired|TestM3.*|TestMultiRepo.*|TestF03.*)$` race定向，exit0、216.159秒，50 PASS、无skip；不是完整Manager race。`m6-lint-windows-race`执行完整conpty/shellterm两包race，exit0、19.229秒，361 PASS/1 SKIP，涵盖当前进程查询令牌的DACL行为和真实shell交互。统筹审阅：移除的RepoPath赋值确实未被消费，后续WorkspaceRepoInfo继续直接保存project.Path；multiRepoRows的所有调用随无用参数删除同步更新；Windows伪令牌仅查询当前进程SID、无需Close；READY后的父端关闭失败只记录错误，不把已启动宿主误报为未启动。仅删除永真的接口形状测试和未使用fixture字段，没有移除业务失败断言。

`m6-lint-integration`运行受影响的M2、M4工件/部分恢复及M5规划边界定向，exit0、625.572秒，55 PASS/1 SKIP；其中M4工件169.74秒、部分恢复156.74秒。五包lint首轮返修 `m6-lint-five-fixed` exit1、96.926秒，仅余G702；统筹核对可执行文件为`os.Executable()`、固定pty-host子命令、argv独立传递且当前调用不拼shell表达式，单点告警说明不代表下游任意命令天然安全。限定该调用的说明后，`m6-lint-five-contract` exit0、37.264秒，原始输出 `0 issues.`。该结论仅限五包，完整lint进入一次新条件复核。

新增独立阻断 **SV-M6-06：Windows分离宿主READY后的实际交互**。既有完整包未直接覆盖defaultSpawnHost释放路径，因此执行方补充真实宿主行为用例。三轮分别为 `m6-lint-detached-host-race` exit1/13.643秒、`m6-lint-detached-ready-race` exit1/12.209秒、`m6-lint-detached-transcript-race` exit1/12.230秒。首轮命令回显但未见结果；第二轮等待shell自发就绪标记仍只有空行；第三轮改读完整输出后仅ConPTY初始化控制序列/空行。无法据此区分生产宿主与fixture握手根因，也不能宣布READY后交互通过。按用户三次失败门禁停止该问题的修改和重试，保留用例与日志，不删除/skip求绿。

`m6-lint-detached-process-check.json`于三轮之后按各自创建时间窗口检查conpty.test.exe/cmd.exe，均无残留；没有逐轮即时快照，不能声称每次即时退出均已观测。没有批量终止进程。统筹另读当前Go1.27.1标准库确认Windows的Process.Release不改Pid，排除将Unix行为直接套用为PID根因。此失败使早前完整conpty包通过仅对应添加该用例之前的版本；阶段仍未通过。

完整lint最新结果已独立核对：`m6-lint-full-closeout`执行固定2.13.2的 `run --timeout=10m --concurrency=2`，UTC2026-11-07T22:50:58.955552退出，exit4、763.125秒，输出 `252 issues:` 与 `Timeout exceeded`。其中27项goimports，不能再归因于已登记的CRLF对照，也不能把超时后的已见诊断当作完整无遗漏集合。三份指定真实配置前后SHA/mtime一致；C盘剩余15499157504字节，原lint进程已退出。分诊首版仅解析250条，遗漏两条跨行gocritic，统筹已派回补齐原始证据解析；不为此重跑lint。五包通过只关闭其局部静态门禁，剩余M1～M6业务相关诊断继续在本阶段处理。

本批HANDOFF后，统筹独立复核204项源码、4份执行文档、6份依赖及2439项换行身份，`mismatches=[]`、`omittedChangedSource=[]`；`git diff --check` exit0。源码集合摘要为 `5ceb139cb7e345e70074a5c0a68ec751aa1828feeb24c9f15999219e7267b4d4`，主仓HEAD仍M5。分诊现已补齐252项（gocritic18项），按文件归属保守分为173项工作流业务/验收、24项本交付共用平台及55项其他既有文件；不是逐行因果证明。统筹已更新同一收口任务的第二批并派回原Astra会话处理197项交付范围诊断；SV-M6-06停止规则及其他未通过项不变，不开启新阶段。

等待第二批期间，对旧用量失败作只读核查：主仓与C盘副本的 `pricing/catalog/v1/manifest.json` 所列anthropic/openai/zai三个blob均各含1个CRLF，原始SHA不匹配清单；仅CRLF→LF后，三者SHA全部精确匹配各自清单。该证据定位 `TestEveryWriteRatedOpenAIModelPricesBothCodexPayloadShapes` 的 `provider "anthropic" hash mismatch` 检出字节条件，不代表已修复或用量包通过。没有修改目录、清单、校验逻辑或Git配置。旧continuation用例另有直接拼接Windows路径到JSON的疑点，项目Clone用例把原生路径直接放入file URL；均只做静态定位，待新隔离下重现，不预判全部属于fixture。

第二批首组行为证据：`m6-lint-b2-domain-race`运行domain、ports、scripted及capability/intervention/principal/scheduler/budget/artifact/qualitygate/integration服务11包，统一 `go test -race -p 2 -parallel 2 ... -count=1 -timeout=5m -json`，exit0、120.536秒，指定真实配置无漂移。统筹从最终原始LOG重新计数：846个测试PASS、无测试FAIL/SKIP；8包PASS，budget/artifact/service-integration三包无测试文件。中途生成的analysis仍是823项，已提醒执行方按实际退出刷新，不能将中途摘要当最终结果。该结果限此组和当时源码，不代表第二批全部通过。

第二批后续已退出结果由统筹逐份读取元数据和原始日志：

| 证据（`m6-lint-b2-`前缀） | 实际结果及范围 |
|---|---|
| core-block-comments / platform / delivery-local-style | 固定2.13.2、`run --timeout=10m --concurrency=2`的业务11包、平台5包、gitworktree三组均exit0，分别55.799/117.975/24.086秒，输出均为`0 issues.` |
| platform-race | process/mobilebridge/pricing/operatorconsent四包完整race exit0、23.687秒；222 PASS/25 SKIP。跳过为22个既有shell脚本夹具、2个Unix权限语义及1个人工原生认证，不将它们算通过 |
| render-race | chat包筛选`(Render\|RunningProviderTurn)`，exit0、132.724秒，34 PASS，无FAIL/SKIP；不是完整chat race |
| delivery-race | gitworktree包筛选`^TestM4`，exit0、66.360秒，9 PASS，无FAIL/SKIP；不能覆盖完整包symlink权限失败 |
| storage-race | sqlite/store/sqlitetest完整race exit1、970.157秒；600 PASS/2 SKIP，sqlite和store均在15分钟包预算超时。超时时正在执行的分别为TestMigrateRecognizesPreRenumberedChatSchema、TestSessionTelemetryFactsRoundTrip，不能单凭正在执行名称断言其根因 |
| storage-remaining-race | 仅补本批修改测试文件中未完成的77个顶层用例，exit0、364.855秒，100 PASS事件，无FAIL/SKIP。统筹逐项对比筛选与实际顶层PASS：expected=77、passed=77、missing=[]、unexpected=[]；不等同完整存储race通过 |

以上八次运行的三份指定真实配置前后身份均未漂移。存储lint首轮另为exit1/101.324秒，仅1处同类型转换，已进入准确返修；后续控制面和最终清单仍待验。当前Git对受保护目录、依赖清单、TOML/YML及`.gitattributes`的检查无差异；不据此宣称所有真实配置均未触碰或关闭此前SV-M6-04事件。

存储转换返修后，`m6-lint-b2-storage-conversions` exit0、108.345秒、`0 issues.`；HTTP/CLI/daemon五包的 `m6-lint-b2-control` exit0、115.294秒、`0 issues.`。控制面首次筛选 `m6-lint-b2-control-tests` exit0/69.096秒/52 PASS，但未覆盖TestM2等前缀。统筹指出具体遗漏后，执行方仅补68个受影响HTTP顶层用例：`m6-lint-b2-http-remaining` exit0、7.122秒、84 PASS事件，无FAIL/SKIP。统筹独立对齐expected=68、passed=68、missing=[]、extra=[]；不能以首次52项概括全部审批HTTP覆盖。

API生成组合证据：`m6-lint-b2-api` 的 `npm run api` exit1/8.469秒；Go spec已执行，TS步骤因 `openapi-typescript is not recognized` 失败。随后调用已有缓存固定7.4.4的等价TS生成入口，`m6-lint-b2-api-ts-cached` exit0/2.509秒，无安装或配置修改。主仓/副本两份OpenAPI及TS产物的SHA均与本批生成前记录一致；这是等价生成成功，不把原npm命令失败改成通过。`m6-lint-b2-callers-compile` 对daemon/integration/session_manager/workflow执行 `go test ... -run '^$'`，exit0/27.629秒，仅编译，无行为执行。以上指定真实配置无漂移；待最终源码冻结与逐项诊断交接。

第二批跨时段配置身份差异：统筹对21条命令元数据独立比较，各次运行前后三份配置身份未变，`inRunChanges=[]`；但北京时间2026-11-08 07:49:55（storage-remaining-race结束）至13:52:35（storage-conversions启动）之间，真实 `C:/Users/11459/.codex/config.toml` 的SHA从 `fd328b296c0308b068af2bad3d0719d78a3aaefa57dbad5d082c06e1ef790fe4` 变为 `ae0378cb89f44241a4aef3526bcbe3d5e0b557cd0a4434de552ac9d97b760931`，mtime亦变。来源未归因，不能写整个第二批全时段配置无变化。此核查只读身份，不读取配置内容、回写或擅自恢复；不据差异认定用户操作或测试污染。此前8条Claude测试信任记录事件仍独立保留。

第二批最终HANDOFF已复核：197项逐项处置与输入归属完全对齐，无遗漏或额外项；所属25包分组lint全部exit0，另外55项不在这些已通过包中，继续保留。统筹重算302项源码、4份执行文档、6份依赖和2439项换行身份，`mismatches=[]`、`omittedChangedSource=[]`；`git diff --check` exit0，源码集合摘要 `14a3d593849e30293a6bbad0e3772e271a4f53e61aa792ecd3db94a934f46256`。主仓HEAD仍M5，本批没有提交。最终进程记录无本轮匹配进程、无test.exe残留，未终止进程，C盘剩余14562127872字节。

范围与规模：第二批124个Go文件，累计104个变更测试文件全文40210行、排除生成TS后的133个变更生产文件全文68564行；34个既有超300行测试已逐项申报，新测试最大197行。daemon的14个用例仅参数顺序机械同步并编译验证，不计行为PASS。所有本批测试日志合计1947 PASS/27 SKIP，完整存储包超时仍是失败，不能由无单测试FAIL事件改为通过。SV-M6-06和其他发布阻断保留；本批交付范围静态诊断已收口，M6继续由原会话处理Windows用量与项目回归，不新开阶段。

### Windows用量与项目回归

`m6-wr-reproduce`在现有隔离runner对observe/usage、service/usage、service/project的24个目标顶层用例执行定向检查，exit1、25.796秒，24个顶层均FAIL（含子测试为32 FAIL/14 PASS），三份指定配置前后身份未变。原始失败包括定价blob摘要不符、续接未建立绑定、原子替换artifact_missing、旧身份延迟读取及file URL拒绝，不能归咎旧隔离环境而豁免。

首次返修 `m6-wr-targets-fixed` exit1、40.372秒，24个顶层23 PASS/1 FAIL（含子测试59 PASS/1 FAIL），剩余原子替换仍报artifact_missing。统筹审阅测试变更：续接路径复用JSON编码，默认roots同时隔离USERPROFILE，Clone fixture生成合法file URL并保留外部helper拒绝，提前失败时也关闭SQLite；legacy替换前后使用生产SourceIdentity固定身份。当前Go Windows的os.SameFile会延迟按路径加载ID，源码证据与此修正一致。生产计量、身份与准入逻辑未因这些fixture修正放宽。

定价输入对照另记：`m6-wr-pricing-control.json`登记三份blob，仅C盘副本各将一个CRLF还原为LF，结果SHA同时等于Git提交原文和manifest期望；主仓三份仍原始CRLF、摘要不符，manifest及生产校验不变。因此该用例通过限已登记输入条件，尚不是Windows任意检出可直接通过的持久修复。原子替换在允许删除共享的真实只读句柄下，os.Rename仍拒绝覆盖；执行方进入第二次修正，用现有ReplaceFileW模式保留实际文件替换和旧描述符计量断言，结果待验。

第二次修正结果已由统筹读取原始日志核对：`m6-wr-replacement-fixed-race`执行原子替换单项race，exit0、65.009秒；原文件句柄和替换文件分别计量120/340的断言保留。`m6-wr-packages`执行 `go test -p 2 -parallel 2 ./internal/observe/usage ./internal/service/usage ./internal/service/project -count=1 -timeout=10m -json`，exit0、148.644秒，三个完整包PASS，421 PASS/5 SKIP事件、无FAIL。五个跳过均由既有symlink权限检查触发，覆盖provider根逃逸、Kimi索引逃逸、Watcher根别名及项目路径别名两种顺序；不算这些行为已通过。

两条命令各自前后三份指定真实配置身份未变，E盘可用空间记录均为192568766464字节。统筹审阅本批fixture变更，生产计量、URL准入和身份校验未改；Windows替换使用真实只读句柄和ReplaceFileW，无删除目标或重试回退。待本批lint及最终源码交接后关闭其限定回归项；定价检出条件、其他完整门禁和M6阶段未通过状态继续保留。

本批最终HANDOFF已收到，`m6-wr-lint`固定2.13.2对上述三包执行 `run --timeout=10m --concurrency=2`，exit0、52.817秒，原始输出 `0 issues.`。统筹重新计算310项源码双树620份SHA、4份执行文档、6份依赖及3份定价blob，`mismatches=[]`、`omittedChangedSource=[]`；`git diff --check` exit0。2439项换行登记与输入仅六个本批修改的既有测试记录不同，已与本次源码SHA核对，其余登记沿用此前核验。记录的源码集合摘要为 `5eda13418ff8736f5cea6b88ddf49b30f50896a5b345f84d5714f52bfaea4328`。

统筹逐条比较本批7条命令元数据，实际时序无重叠、各命令内部及间隔的三份指定配置身份无差异；依赖清单与输入相同。8个源码变化均为测试，未新增顶层用例，两个平台辅助文件15/45行，四个既有超300行文件已申报。因此本批24个旧失败目标的限定回归与三包静态门禁收口；定价仍只在登记的LF输入下通过，五项symlink未验保留。M6不提交，由同一执行者接续lint第三批55项平台诊断。

### lint第三批独立验收

本批55项与冻结输入逐项核对：id无重复、原诊断完全对应、无遗漏；36项修正、18项审计后保留并加局部说明、1项同时修正和审计。对应13包的两组lint全部exit0/`0 issues.`（32.332/74.743秒）；Job缓冲固定后再验该包lint，exit0/21.581秒。完整lint未复跑，最近完整命令的超时失败仍保留；已观测252项已有逐项处置，不等于完整门禁通过。

统筹已审阅21个源码变更：三个外部int PID入口在转DWORD前拒绝越界，避免高位截断误查其他进程；系统创建/枚举PID沿原DWORD传递。仅查询SID的令牌改用无需关闭的查询伪令牌，ACL政策与错误返回保留。Job Object参数经uintptr包装调用时使用Pinner保持地址稳定，调用后解除；启动前挂起、加入Job和退出后回收顺序不变。两个Windows报告unused的函数确有Linux/macOS调用者，保留实现并限定说明；没有删除跨平台路径。多返回值、ACL原生读取和只读句柄清理的说明限定到具体函数/语句，不改错误所有者或权限拒绝。

| 证据（`m6-lint-b3-`前缀） | 实际命令范围与结果 |
|---|---|
| core-race | processalive、ptyregistry、procmem、runfile四完整包race，exit0/14.145秒，52 PASS |
| security-race | agent、codexappserver、session、persistenthost按同名JSON内精确正则定向race，exit0/32.260秒，101 PASS/2 SKIP；不是四完整包通过 |
| preview-race | previewserver完整race，exit0/42.130秒，14 PASS，含第三处PID越界回归 |
| job-race | 缓冲固定后 `^TestWindowsProvider(JobReapsGrandchild\|Bridge\|Grandchild)$`，exit0/10.388秒，5 PASS事件 |

四条race合计172 PASS事件，按包/测试名去重167 PASS/2 SKIP；两个SKIP均为已有工作区symlink权限限制，不算对应路径行为通过。新增三条PID用例针对当前测试进程PID加2³²，两个新文件各24行，本批修改测试均不超过300行；没有为机械清理新增矩阵。

最终331项源码双树662份SHA、4文档和6依赖由统筹重算，`mismatches=[]`、`omittedChangedSource=[]`；19项变化的换行登记与已核验源码一致，其余沿用输入；定价对照登记和依赖均未变。源码集合记录 `3498ab07e29607b6707d2d740b55ab8bcdd5d2ede0b8396c045d2ee9f9dfec8a`，`git diff --check` exit0。7条命令实际时序无重叠、前后及间隔的三份指定配置无漂移。最终进程记录无本轮匹配进程或test.exe残留、未kill，C盘剩余14513766400字节。

本批限定范围验收通过，M6仍不提交。原会话接续[Windows私有目录与账号回归](m6-windows-safety-plan.md)，处理旧完整测试中仍未关闭的Q9/Q10边界；SV-M6-06、打包、人工和其他已知限制保持。

另记静态发现SV-M6-07：`processalive/process_windows.go`的OpenProcess非AccessDenied错误、WaitForSingleObject错误均返回false；persistenthost的锁重取/恢复/清理会把false视为死亡依据。ptyregistry的类似查询错误也可能进入记录过滤及重写。尚未注入复现，不能报告实际重复启动或状态丢失；按独立[任务](m6-runtime-observation-plan.md)排在当前权限批次之后验证。第三批验收仅覆盖PID范围修复及已列检查，不覆盖这一原有错误语义。

### Windows私有目录与账号批次

`m6-ws-reproduce`在新隔离runner运行四包中精确16项，exit1/10.938秒，15 FAIL/1 PASS，三份指定配置前后未变；两项既有symlink权限阻断未重试。`TestPRCheckoutByVerifiedSHAWithFETCHHEADBranch`本轮已通过，旧CRLF失败不能继续列为当前复现失败。其余涵盖Reviewer目录、模型发现、账号私密性、假登录/重认证、AO状态导入和不可读工作区；当前仍在定位及修复，不把Unix mode不适用直接当成生产ACL通过。

当前边界红测区分编译与行为：`m6-ws-boundary-red` exit1/9.554秒，三包build失败，仅importer实际执行1 PASS/1 FAIL；`boundary-red2` exit1/19.876秒，agent仍build失败，其余包4 FAIL/1 PASS；`agent-red` exit1/15.107秒，7 PASS/4 FAIL。已实测Reviewer/模型目录继承Everyone读权限、不可读工作区仍返回成功、自定义AO数据目录未拒绝。账号槽位实际ACL通过，旧Unix mode断言及假终端退出顺序需单独修正，未改生产登录状态机求绿。

Reviewer修复的首轮 `m6-ws-agent-reviewer-green` 实际exit1/11.063秒，26 PASS/1 FAIL，失败为硬链接外部目标ACL被目录SetSecurityInfo传播修改；不能因日志名green报告通过。改为只设置当前对象后，`m6-ws-reviewer-full` 完整包exit0/4.650秒，13 PASS，含外部目标内容/ACL保全、旧manifest身份保全与junction拒绝。统筹已读取原始事件及实现；此结果待本批最终源码冻结确认。

`m6-ws-services-full` 对agent/session/importer/PI Reviewer/AGY Reviewer五完整包运行，exit1/297.026秒，1161 PASS/11 SKIP/2 FAIL。importer及两个Reviewer包通过；失败精确为 `TestCodexBootstrapPermanentSafetyFailure`、`TestWorkspaceFileRevisionLiveReadAvoidsGitAndChecksRevision`，原始错误均为 `A required privilege is not held by the client`，不能标两个包PASS。上述命令指定真实配置前后无漂移。

独立审查追加：模型发现新调用 `ensurePrivateDirectory` 经 `protectCodexPrivateDirectory` 仍到达目录SetSecurityInfo，存在与Reviewer相同的子项传播风险。首次反馈仅确认该调用链，派回唯一执行者从真实模型发现入口验证外部硬链接目标内容/ACL保全；随后红绿测见下文。不混同设备凭据文件ACL政策，不扩为全仓重构。

追加风险随后实测确认：`m6-ws-model-acl-red` 从真实 `modelDiscoveryRequest` 入口执行，exit1/7.550秒；调用成功但外部硬链接目标的Everyone读ACE被移除，内容保持不变。共享目录分支改用SetKernelObjectSecurity，仅设置已验证的当前句柄；文件及设备凭据分支保留原政策。`m6-ws-model-acl-fix1` exit0/7.964秒，16 PASS；统筹核对原始SDDL、目标内容断言和变更，外部ACL前后相同。该修复后的完整账号race仍待最终结果。

七包 `m6-ws-lint` 固定2.13.2运行 `run --timeout=10m --concurrency=2`，exit0/89.776秒，原始输出 `0 issues.`。另已派执行者补强两个现有测试：Reviewer复用身份须在调用前后从句柄读取文件ID，避免Windows两次os.Stat延迟身份读取误判；假终端重复关闭须模拟真实shellterm的NotFound返回，记录实际关闭次数与调用次数的区别。两者只调整测试可信度，不修改生产登录状态机。

补强实现已审阅：Reviewer在每次调用前后分别以 `GetFileInformationByHandle` 固定卷号及文件ID；fake重复关闭返回与真实服务相同的 `SHELL_TERMINAL_NOT_FOUND`。最终 `m6-ws-agent-race-full` 执行完整agent包race，exit1/54.948秒，354 PASS/2 SKIP/1 FAIL，唯一失败仍为既有bootstrap symlink权限，未见数据竞争报告。`m6-ws-path-race` 对Reviewer/session/importer的权限、定位和AO状态拒绝执行精确筛选，exit0/23.905秒，24 PASS/无SKIP；这是三个包的定向检查。三份指定配置前后无漂移，最终源码清单仍待交接。

本批最终交接已独立核对：补强后的Reviewer/agent两包lint为exit0/35.120秒、`0 issues.`。统筹重算353项源码双树706份SHA、4份执行文档、6份依赖及集合摘要，`errors=[]`、`omittedChangedSource=[]`；12项换行登记变化与源码一致，依赖及定价对照登记未变。源码集合摘要 `8e1b120f1cb21be5cf3a1e81838acb293dddf6d57f9f72887eba92e342d354bb`；原18项逐一匹配原始Go事件，16 PASS/2 symlink FAIL，`mismatch=[]`。

14条测试/lint命令实际时序无重叠，各次内部及相邻间隔的三份指定配置身份均相同。源码diff检查exit0；最终进程记录无本轮进程或test.exe、未终止进程，C盘剩余14268248064字节。本批8个生产/16个测试文件，6个新顶层用例；新测试最大193行，4个既有超300行文件已逐项申报，各受影响包测试/生产行数均低于2。统筹在实施中追加的任务文档内容与派发消息一致，原输入快照保留，不构成来源不明修改。

限定修复范围验收通过：Reviewer与模型发现私有目录、硬链接外部目标保全、自定义AO数据目录导入拒绝及真实不可读工作区错误已闭合；账号fixture改为真实平台政策及退出顺序，生产登录状态机未改。两个完整包的symlink失败继续保留，不标为全包PASS，其他M6阻断不被本批覆盖。接续SV-M6-07，仍由同一执行会话处理，不提交M6或开启新阶段。

历史失败去重复核：第三批保存的codexappserver 112条及persistenthost journal 1条失败事件，统筹逐一以包/测试名匹配先前 `m6-profile-race-full.log`，113项全部已有后续PASS，`missing=[]`。当前fixture已使用平台绝对路径，不为这113条再安排独立返修；SV-M6-07新修改影响的范围另验。journal当时仅将Unix mode断言限定到Unix，其Windows PASS不构成实际DACL验收。

### SV-M6-08 Chrome注册测试污染

统筹追溯余下browserchrome两项历史失败，确认 `testInstaller` 未注入注册实现，Windows默认回落真实HKCU；用户目录隔离不能隔离注册表。只读查询 `HKEY_CURRENT_USER\Software\Google\Chrome\NativeMessagingHosts\dev.ao.browser_bridge`，当前默认值指向旧 `TestSetupWithoutChromeFails212292381` 的C盘scratch manifest。证据 `supervisor-browser-registry-readonly.json`，北京时间2026-11-08 15:40:27，查询exit0；本次未写入、删除或恢复注册键。旧测试前值未知，不声称恢复用户原注册。

已建立[测试隔离任务](m6-browser-test-isolation-plan.md)，排在当前SV-M6-07交接后。修复前禁止复跑本包Setup/Remove或包含它们的全量行为测试；lint/静态读取不执行该副作用。此项与既有Claude八条测试记录事件分别保留，不能再将“指定三份配置未变化”外推为宿主所有状态未受影响。

SV-M6-07交接后已执行隔离修复：只修改Installer内部注入字段为返回hostRegistry的函数，nil函数保持平台默认，测试通过显式返回nil选择目录型注册；原有内存fake继续覆盖注册表型安装/移除与其他安装保全。三项目录型测试和两项注册表型测试全部经显式输入，未添加新用例或skip，公开API及默认生产行为不变。

统筹核对原始结果：`m6-bi-package` 完整包exit0/4.889秒，`m6-bi-package-race` 完整race exit0/7.627秒，均13 PASS/1既有SKIP；固定2.13.2的 `m6-bi-lint` exit0/16.995秒、`0 issues.`。隔离runner读取主仓Git曾被dubious ownership拒绝，source-freeze exit1、diagnosis exit128，均保留；随后沿已验证输入清单核对字节，不改Git信任配置或所有者，产品测试仍在隔离runner。最终源码及环境交接待核。

本批最终HANDOFF已独立验收：367项源码双树734份SHA、4文档、6依赖及集合摘要一致，`errors=[]`、`omittedChangedSource=[]`，两项换行登记变化与源码一致。摘要 `3a7591eea63d9b304455382b491b7e77359bc7d6b669fe84bcf10f2f0ed2f0f4`；相对输入仅setup.go/chrome_test.go变化。9条运行/审计命令无重叠，前后及间隔的三份配置、9份精确注册键观察一致。最终记录无本轮进程/test.exe，未kill，C盘剩余13382074368字节。chrome_test.go为259行，包内测试798/生产1098行，没有新增测试矩阵或跳过。

SV-M6-08只关闭测试再次回落真实HKCU的路径；真实Chrome E2E因缺端点继续SKIP，旧注册指针未恢复，Claude八条历史记录事件也未关闭。原两个失败已由完整包/race原始事件确认通过，限定验收通过后接续剩余315条历史FAIL对账及一次有依据的完整lint复核，不重复本包已通过检查。

### SV-M6-07 进程观察与恢复

`m6-ro-boundary-red` 使用可控观察错误，经真实daemon/CLI、persistenthost和ptyregistry/ConPTY业务入口验证，执行 `go test -p 2 -parallel 2 ./internal/processalive -run '^TestObservationFailure' -count=1 -timeout=3m -json`，exit1/22.057秒，三个顶层用例均FAIL。原始输出明确显示run-file被删除、宿主锁与描述符被移除、注册扫描报告complete并丢弃记录、隔离启动钩子被调用；这是scratch行为复现，不是实际第二个Provider已启动。另Shutdown把无法连接且观察未知当作成功，CLI在发HTTP前错误认定记录过期。

此次三份指定配置身份未变，Chrome真实键另由只读快照观察；不依靠耗尽系统资源制造故障。红测先将ptyregistry探测接到仍保留旧语义的共享函数以统一注入，源码快照单独登记，因此不能声称其字节与353项输入完全相同。实现修复和最终回归仍在执行，不提前关闭SV-M6-07。

首轮 `m6-ro-fix1` exit0/23.341秒，3项PASS；随后执行者审查发现注册扫描仍把未知记录返回为complete，不能据首次3项通过关闭。补强现有注册用例，真实监听端口关闭后制造拒连：`m6-ro-registry-refused-red` exit1/9.921秒，除complete错误外，IsAlive/IsChildAlive也返回false,nil。scanLocked改为查询失败返回原列表、complete=false及错误，且不重写注册文件；`m6-ro-registry-fix1` 对processalive/runfile/ptyregistry三个完整包执行检查，exit0/15.339秒，33 PASS。未知保留、真实子进程退出后清理与允许恢复均有断言；最终race/lint及调用范围仍待验。

`m6-ro-ownership-race-full` 执行processalive/runfile/ptyregistry/persistenthost四完整包race，exit0/110.005秒，93 PASS、无FAIL/SKIP；统筹已读取原始事件。CLI/ConPTY定向 `m6-ro-cli-conpty-race` exit1/31.121秒，25 PASS/1 FAIL；唯一失败为原测试仍断言“still alive”，生产提示已改为“exit was not confirmed”。只更新错误文案断言，强杀失败及保留占位的检查保持，`m6-ro-conpty-assertion-race` 单项复验exit0/8.589秒、1 PASS。不能把首次组合命令改记exit0，也不外推完整CLI或ConPTY套件通过。

六受影响包固定2.13.2静态检查 `m6-ro-lint` exit0/89.324秒，原始输出 `0 issues.`。实现保留Windows AccessDenied及非法PID原分类，增加明确Probe错误；保守布尔仅用于保留所有权，HostPID不把未知计为已确认存活，CheckStale在未知返回错误。CLI继续用真实HTTP所有权响应判断可用性，无法核验时不声称进程已死或已存活。Unix原Kill/ps语义保留，仅接入兼容签名；不据本机结果宣称其他OS验证通过。

最终HANDOFF已收到并独立核对：366项源码双树732份SHA、4文档、6依赖及集合摘要一致，`errors=[]`、`omittedChangedSource=[]`；9项换行登记变化已与源码核对，依赖和定价对照未变。源码摘要 `f96d699e492a3aa3ecac5e8f9eeb6d52b30f7369952dc8dfa62a1f0ea4dbeb69`。10条验证无重叠，各次及间隔的三份配置身份一致；10份注册键只读观察相同。源码diff检查exit0、无本轮进程/test.exe残留、未kill，C盘剩余13874065408字节。

本批17个源码变化（10生产/7测试），三个新增业务回归及一个真实子进程fixture入口。最终race事件按包/测试名保留末次结果为119 PASS；完整包范围仍仅四包，其他为定向。processalive与ptyregistry在Linux amd64/CGO_ENABLED=0下 `go test -c -p 2` 成功（17.390秒），无Linux运行证据。测试规模例外已申报：processalive测试455行、生产102行，包内两倍上限超251行，因三项跨包真实装配回归集中注入共享观察；新增文件均≤144行。两个既有大文件460/1169行仅签名及文案同步，未增加行数。

SV-M6-07限定修复验收通过，接续SV-M6-08测试隔离。M6整体仍未通过，不提交或开启新阶段；SV-M6-06、人工、旧配置/注册表残留和其他已列完整失败保持原状态。


六项遗留批独立验收：4项实际关闭，Kimi两项确认symlink共享前提并保留FAIL；Registry完整父组race39 PASS、安装受影响组20 PASS、两包lint exit0/0 issues.。399源码双树、四文档/六依赖、4条变化换行登记和108份证据摘要一致；独立匹配609条原始事件，errors=[]。本批12条操作无重叠、三配置/HKCU未漂移、任务/测试进程0，C盘最终12236296192字节；前批完整lint及串行FAIL不覆盖。具体见[m6-residual-fixtures-plan.md](m6-residual-fixtures-plan.md)，接续只读归因完整门禁，不提交M6。
