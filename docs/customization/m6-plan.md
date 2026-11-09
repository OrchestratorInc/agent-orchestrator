# M6 稳定性、Windows 交付与发布准备任务

日期：2026-11-08。状态：按用户最新指令，M5 开发检查点本地提交后启动可自主执行的 M6 工作。M5 完整验收未通过，人工桌面步骤暂缓；不能据此发布。来源：[design.md](design.md) §13、[requirements.md](requirements.md) Q1～Q10、[M5 设计](m5-design.md)。

## 目标与完成判定

2026-11-09安装器增量：已用现有候选生成Windows x64安装器0.13.0，make及父级自然/包装均exit0；独立SHA、签名和版本核对通过，NotSigned。详见[安装器任务与结果](m6-installer-plan.md)。运行与安装升级仍未验，M6未通过、未提交；不重复package/make或已通过套件。

2026-11-09最新进展：[Windows打包收口](m6-package-observed-build-plan.md)已取得0.13.0候选目录，npm build自然exit0/133.726秒；收尾回收一个成员导致包装95，最终无残留。统筹核对原始结果及六关键产物SHA一致。本轮不再追加测试，安装升级/桌面/动态模块、SQLite46项及历史失败仍保留，M6未通过、未提交。

形成可复现的 Windows 候选交付，验证异常恢复、升级后数据/Session/Worktree 保留、审批与权限边界，给用户提供可操作的使用和故障处理文档。

区分三种结论：

1. 本地离线候选：Fake Provider、真实临时 Git/SQLite、隔离 Electron 与候选包验证。
2. 原生能力验收：四 Provider 的实际协议能力、Windows Hello 真人人工流程、真实安装/升级路径。
3. 正式发布：本地及适用 CI 门禁完成，获得发布授权，由指定唯一发布者执行。

第一项不能替代第二或第三项。用户于2026-11-08明确要求本人操作做不了时先跳过继续开发：Windows Hello、手动启动及其依赖的桌面步骤记为 NOT_TESTED/DEFERRED，先完成不依赖人工操作的开发，不再反复要求配合。M0-05预算、签名凭据和发布授权仍未提供；不能将M6标为正式发布PASS，不能伪造认证或放宽生产权限。

## 工作包

| ID | 交付结果 | 主要检查范围 | 验收证据 |
|---|---|---|---|
| M6-01 | 首版需求覆盖审计与候选基线 | Q1～Q10、M0～M5 报告、实际 Daemon/UI/Adapter 接线 | 每项映射源码/行为/测试，明确未交付能力和阻断级别 |
| M6-02 | 恢复、进程和多仓副作用验证 | `backend/internal/integration/`、现有生命周期/Session/Scheduler/质量服务 | 重启不重复 Spawn/Commit/Apply；UNKNOWN 保留、旧 epoch 拒绝、逐仓恢复结果 |
| M6-03 | 安全与人工审批验收 | `service/principal`、Operator HTTP/IPC、受限 Git/Runner、日志 | 旧批准/错目标/重放/越权失败，敏感值不进入日志，Windows 实际能力记录 |
| M6-04 | Windows 候选打包与隔离升级 | `frontend/scripts/`、现有 Electron Forge/更新器及打包说明 | 候选版本/hash、安装/启动/升级前后数据和进程检查 |
| M6-05 | 用户指南与故障恢复手册 | `docs/customization/user-guide.md`、`troubleshooting.md` | 创建/切换/干预/审批/预算/恢复步骤在候选中可重现 |
| M6-06 | 完整适用验证、回归修复及发布交接 | 仓库 CI、已知 Windows 基线、候选产物 | `m6-report.md`、需求矩阵、发布检查项和已知限制 |
| M6-07 | [Workflow自定义供应商引用闭环](m6-profile-plan.md) | Q9、Workflow创建/规划/执行/恢复、Adapter映射与用户入口 | 受支持引用端到端离线证据，能力差异明确，凭据隔离，原生配置不变 |

执行会话使用 Astra；用户最新明确总并发为 2，本任务只保留统筹和一个执行会话，不派发子 Agent。具体实现和测试安排自主。建议完整阶段 high；文档/证据整理 medium，复杂恢复与审批问题 high 或更高。统筹负责接收结果、独立验收、派回缺陷和阶段提交。

## 安全执行条件

沿用 M5 磁盘事件后的约束：构建、测试、生成、依赖安装一次只运行一个命令，统筹与执行方共享这个执行窗口。新增缓存、日志、临时 Git/SQLite、依赖安装及候选产物放 C 盘专用隔离目录；仅用进程环境指定，不改受保护配置。复用已有合适 worktree；需要新的隔离副本时先核实位置、磁盘余量及排除规则，禁止复制旧 `.tmp`、缓存或共享 node_modules。

运行前后记录命令、真实进程、退出码和磁盘状态。M5提交时Git自动maintenance触发了额外E盘I/O；后续可能触发维护的Git写命令使用进程级 `git -c maintenance.auto=false -c gc.auto=0 ...`，不修改Git配置文件、不启动后台gc/prune。取消工具请求不等于底层进程退出；持续挂起时先确认并停止准确进程，再核实文件读回，不能叠加重试。大日志只提取失败与末尾结论，保留原文件；测试超时须区分单用例等待与整个包的时间预算。`.git/info/exclude` 是本机措施，不能假定新工作树自动继承；验证时不得把未忽略的缓存带入 Git 快照或提交。

桌面启动前的资源采样不能代替启动后的正式 Scheduler 准入。M5 曾在启动前可用约 8.68 GiB 时满足门禁，启动后降至约 7.67 GiB 并返回 `RESOURCE_SOFT_LIMIT`；差值不能全部归因应用。后续需为启动保留余量，以实际 Scheduler 状态决定是否创建测试 Session；拒绝时清理本轮实例，等待明确环境变化，不降低阈值或根据退出后的回升循环启动。

## 优先推进与遗留项

1. 先修复已定位的离线集成装配失败并重跑完整受影响包：6项Session fixture缺可信准入，17项M2 fixture目录未初始化Git。保留生产准入与仓库校验。独立定位TestM4ArtifactThroughRealManager的5分钟超时；复核Domain Handoff与CLI symlink失败，区分可修代码和OS能力限制。
2. 接续完整build的下载阻塞：已有三次失败和Got/Undici对照证据，不重复原样打包。只有取得实际构建下载链可用的新证据后才再运行完整build；不改依赖/配置或关闭TLS、checksum来通过。自动工具拒绝的动作不换工具代跑；保留该项并继续其他工作。
3. 完成Q1～Q10源码/行为覆盖矩阵、恢复边界及用户文档，发现影响首版的真实缺口时最小修复并验证。执行方自行选择方法和顺序，统筹只验收结果。
4. SV-06桌面Queue编辑/取消/投递回显保留未验；已有正式HTTP/组件回归通过可复用，新fixture已准备。无需再启动桌面或索要Hello；人工暂缓不削减功能实现或离线回归要求。
5. [Windows用量与项目回归](m6-windows-regression-plan.md)已交接并独立验收其限定范围：三个完整包及lint通过，24个旧失败目标收口；五项symlink未验，定价主仓CRLF检出仍保留。原会话继续[lint第三批55项平台诊断](m6-lint-closeout-plan.md)，不新增执行者、不改生产权限或重启SV-M6-06失败问题。
6. lint第三批55项已独立核对，13包分组lint通过、331项源码冻结；原会话接续[Windows私有目录与账号回归](m6-windows-safety-plan.md)，核对旧完整测试中的权限/账号/导入失败。复用已有通过证据，Windows按实际ACL和句柄验证，不能仅修改Unix权限位断言求绿。
7. Windows私有目录与账号批次已限定验收：原18目标16 PASS/2 symlink FAIL，目录ACL传播副作用及外部目标保全已验证，353项源码快照 `8e1b120f1cb21be5cf3a1e81838acb293dddf6d57f9f72887eba92e342d354bb` 独立核对无差异/遗漏。原会话继续SV-M6-07：[进程观察失败与恢复边界](m6-runtime-observation-plan.md)，核对宿主锁/描述符/注册记录及CLI间接调用。目前此问题仅有静态调用链证据，不与已结束批次并行，也不重开SV-M6-06。
8. SV-M6-07已限定验收：366源码双树一致，四包完整race/六包lint通过，定向补验后最终119 PASS。原会话接续SV-M6-08：[Chrome测试隔离](m6-browser-test-isolation-plan.md)，旧真实HKCU键仍指向scratch manifest；修复隔离前不复跑包含Setup/Remove的行为套件，不擅自清理真实键。其余113条chatdriver历史失败已有后续完整race PASS，不另开重复返修。
9. SV-M6-08已限定验收：仅两源码修改，完整包及race各13 PASS/1既有真实Chrome E2E SKIP，lint为0 issues，367项源码与四文档/六依赖独立核对一致。现由原会话执行[剩余自动门禁对账](m6-remaining-gates-plan.md)，逐项核对旧315个FAIL事件及后续结果，补齐安全可自动验证项并运行本批一次完整lint；旧HKCU测试指针仍保留，M6未通过、不提交。
10. 剩余门禁批已交接并独立核对397项源码及行为证据：315旧FAIL关闭287、保留28；完整lint exit4/623.267秒、0 issues.后超时，严格串行核对存在4.502秒只读摘要重叠，均继续FAIL。原会话接续[六项自动遗留](m6-residual-fixtures-plan.md)，不重跑完整lint、不新增阶段；旧平台/权限与真实环境缺口保持未验。
11. 六项自动遗留已限定验收：Registry完整父组race为39 PASS，安装fixture组race为20 PASS，两包lint为0 issues。旧315个FAIL关闭291、保留24（21项外部能力、3项平台前提）；399项源码双树摘要和证据独立核对一致，本批运行无重叠。原会话接续[完整门禁有界归因](m6-full-gates-diagnosis-plan.md)，只读检查完整lint、sqlite/store race、前端及打包原始失败，先确定最小后续修复或复验；不重跑大套件，不重开SV-M6-06，不索要人工操作。
12. 完整门禁归因已核对，未发现立即修改产品源码的证据。原会话执行[存储未终态与前端ABI复验](m6-bounded-recheck-plan.md)：仅恢复C盘已有Node137模块并验浏览器导入，补存储七个未终态；已有适用PASS复用。完整lint超时阶段及打包失败请求仍未知，本批不重跑；所有人工/环境缺口继续暂缓。
13. 有界复验已限定验收：浏览器导入55 PASS、存储七顶层race PASS，818份双树身份和93证据摘要独立一致，12操作无重叠。Node137实际加载条件已修正，原会话接续[完整自动门禁单次复核](m6-final-automated-gates-plan.md)：完整前端一次、完整lint增加阶段观测一次，保持并发/时限，不再要求本人操作。打包和SV-M6-06保持暂缓/停手，M6不提交。
14. 最新完整前端及lint均已通过并独立核对：636文件、10896 PASS/14 SKIP，lint为exit0/0 issues。原会话接续[存储逐包完整race](m6-storage-full-race-plan.md)：两包分别独占资源各运行一次，保持15分钟及包内并发2，不以局部PASS覆盖完整失败。其余人工/外部限制继续暂缓，M6仍未通过。
15. sqlite逐包复验因监测异常中断、store未启动；已精确回收本次三进程，原原因未知。共享异常收尾已限定验收：唯一故障自检exit95、原业务码null、Job残留0，818份双树与25证据SHA独立一致；最终两行身份锁止仅静态检查。原会话先继续store完整包一次，不重跑sqlite/前端/lint，不新增自检；安全边界和15分钟预算保持。

16. store单包续接为真实15分钟超时FAIL/900.464秒、exit1，427项PASS；两层terminal、持续监测及进程回收完整。统筹818份身份/12证据SHA和原始事件独立一致。原会话接续[store测试装配最小修复](m6-store-fixture-plan.md)，依据日志减少非迁移业务fixture重复空库迁移，保留真实重开及所有断言；先受影响race/lint，再有条件完整包一次，不重跑sqlite或已有完整前端/lint。

17. store最小fixture修改已限定验收：12项race/包lint通过，完整包仍FAIL/900.460秒、732 PASS；统筹826份身份与39证据SHA核对一致。停止完整套件原样重跑，原会话仅执行[存储剩余覆盖收口](m6-storage-remaining-coverage-plan.md)，只补当前源码下没有有效证据的用例，不改源码、不扩大人工/外部依赖。分段覆盖不覆盖完整命令FAIL。

18. 存储剩余覆盖已交接：补验18顶层＋19子项race PASS/100.504秒，store561/561顶层分段PASS；sqlite31/168、137项按本批预算暂缓。统筹826份身份/55证据SHA和729项目录、592条原始pass事件独立一致，无进程残留。当前不追加完整套件重跑，保留完整store FAIL、sqlite INCOMPLETE、2子项SKIP与外部未验；未要求本人操作。

19. 原会话接续[SQLite缺口分批补验](m6-sqlite-batched-race-plan.md)：以已有137项缺口为输入，由执行方按实测成本安排最多3组互不重叠的补验，保持每命令15分钟/父17分钟及资源门禁；不重复全套、不改源码、不索要人工操作。前批暂缓仅因单条预算，不能当作无法继续推进的外部阻塞。

20. SQLite三组已限定验收：新增91顶层/42子项PASS，三组exit0，当前122/168，剩46项；41证据SHA及91条原始pass独立核对一致。用户追问M6耗时后，停止追加第四组历史回归；余项是安装升级兼容/稳定性证据缺口，保留未验。后续优先处理Windows候选构建阻塞，不把分段覆盖或人工暂缓当发布PASS。

21. 原会话接续[Windows打包阻塞闭合](m6-package-observed-build-plan.md)：先核实真实Forge请求/代理/缓存与Node入口，增加最小脱敏观察或修正明确环境问题后，仅允许本批一次完整build。复用C盘隔离和8GiB门禁，构建原生ABI变化单独记录；不追加SQLite第四组、安装/桌面/发布或人工操作。

22. Windows观察构建已交接：两次观察器中断后，仅将Forge产物实时读取延后到退出；最新完整build自然exit1/79.690秒，监测无异常，缓存ZIP命中，实际SHASUMS/回退ZIP均在GitHub资产域名TLS ECONNRESET。1个残留成员已回收、最终0；无候选，原生模块现149未运行。统筹核对原始事件和terminal，不再原样构建，下一步聚焦具体代理下载链路。

用户最新授权是继续开发，不是整体PASS、发布或取消安全边界。阶段提交区分开发检查点与验收通过提交；交付报告须持续列出上述未通过项。

## M6-01：首版覆盖门禁

- [ ] 用实际源码和 UI 验证自动规划/任务申请、四 Adapter、三种干预、跨 Agent 协作、多仓、双视图、动态并发、用量/自定义 profile、受限执行和恢复。
- [ ] 自动 Planner、Stop & Replan、profile/effort 映射不能仅凭设计文档或测试 Fake 判为产品完成。缺实现时拆出范围明确的修复工作包；禁止静默降低首版要求。
- [ ] 核对两项历史 Handoff Note 失败及其他 Windows 已知失败。区分既有失败与新增回归；影响首版交付的历史问题也必须处理，不能只因“基线已红”自动豁免。
- [ ] 接续 M5 的具体集成分诊：干净 M4 已复现 23 项断言失败，其中旧 Session fixture 缺可信准入提供器，旧 M2 fixture 使用未初始化 Git 的项目目录，触发路径预留 `invalid delivery artifact`。修复应补齐正式测试装配，不能放宽生产准入或仓库校验；重跑受影响完整包。`TestM4ArtifactThroughRealManager` 超时独立保留，不能据此推定它或当时未执行的后半段通过。补充21项逐项对照位于M5证据目录 `integration-triage-comparison.json`，另2项见 `go-m4-baseline.log`，合计23项。
- [ ] 锁定 M5 提交 SHA、工具链、依赖、适用工作流命令及候选验收范围。

## M6-02/03：异常与权限验收

复用真实 SQLite/Manager 装配、临时 Git、多进程测试设施，以可观察业务行为注入异常，不新增通用故障框架。

- Claim/Session 接受/消息发送前后中断，恢复时幂等与旧代际 fencing 有证据。
- Agent 无输出但仍存活、探测未知和 daemon 重启不得被等同为死亡；保留不确定占位。
- 工件保存、Gate 结束、Commit/Apply 写 ref 与持久回执之间中断，按 Git 和耐久事实对账，不能自动重做不明副作用。
- 多仓一个成功一个冲突，整体保持 PARTIAL/UNKNOWN；不破坏已成功仓、dirty Worktree 或用户 index。
- 受限 Runner 的超时/进程树回收、输出限制、环境/路径权限与日志脱敏有真实 Windows 证据。
- 原生审批拒绝、取消、过期、重放、目标漂移和不可信 IPC 来源均失败闭锁；真人 Windows Hello 结果单独记录。
- 证据不能证明 OS 级沙箱时如实说明，并验证实际允许/禁止路径，不宣传提示词构成系统隔离。

## M6-04：安装与升级

仅在隔离工作树、scratch AO_DATA_DIR 和专用测试安装位置执行，保留用户现有安装及数据。复用当前 Forge 构建链，不另建发布系统、不运行 publish 作为测试。

隔离前置：当前 `frontend/src/main.ts` 在 `app.isPackaged` 时把 Electron `userData` 固定到 `os.homedir()/.ao/electron`，只有开发版读取 `AO_DEV_ELECTRON_DIR`。单独设置 `AO_DATA_DIR`、更换安装目录或开发 profile 不足以隔离打包版。执行前必须核实候选实际所有写入位置，使用独立测试账户/环境或经验证的隔离方式；未具备该条件时先完成构建和静态候选检查，安装/升级运行保持 BLOCKED，不能启动候选触碰真实 profile。不要为通过验收擅改此产品边界。

```text
# frontend，具体 maker 由当前 Windows 配置核实
rtk npm ci
rtk npm run typecheck
rtk npm run build
rtk npm run make
```

检查：候选内 Daemon/资源路径可用；首次启动仅写独立 AO 数据目录；关闭及重启无遗留隐藏进程；从实际支持的旧版本升级后 Workflow、Session、消息、审批事实、预算与 Worktree 归属保留；更新失败保留可恢复状态。数据库迁移成功不自动代表可安全降级，不承诺未经验证的回滚。

使用测试更新源或仓库已有隔离更新设施，禁止把试验版本发布到生产 feed。打包配置如确需更改或提交，由执行方先说明必要性和差异，统筹取得用户明确授权后处理；没有签名凭据就记录未签名候选，不伪称签名通过。

## M6-05/06：完整检查与交接

按当前 `.github/workflows/` 使用相同工具链和适用命令。发布准备阶段运行完整适用套件；其他 OS、Docker 或凭据不可用时明确记录具体 job 缺口，不将未运行项记 PASS。

```text
# backend
rtk go build ./...
rtk go test ./...
rtk go test -race ./...
rtk go vet ./...

# 仓库根与 frontend 的相应目录
rtk npm run lint
rtk npm run frontend:typecheck
rtk npm test
rtk npm run test:e2e
```

测试与脚手架遵守 TESTING.md；已经通过的套件仅在新增变化/失败/未解决问题影响时重跑。不发布、不改真实 Provider 配置、不提交构建产物或运行态。

交付 `m6-report.md`：需求覆盖、候选 SHA/hash、实际命令/退出码、升级/恢复记录、日志和截图位置、尚需用户参与/授权的精确项。统筹独立编写 `m6-acceptance.md`。仅当定义的阶段验收要求满足才本地提交并记录 SHA；授权缺失导致未验的完整发布门禁不能通过改名或删项绕过。

## 首轮 HANDOFF 后的自动收口任务

首轮HANDOFF时源码快照为 `fa680a419542b8b5d460d66458cd38dda910cd656970fd0d1cad9bd08afec340`（历史190文件，尚未提交），当前输入以[完整门禁归因任务](m6-full-gates-diagnosis-plan.md)及其399项清单为准。ConPTY语言断言已修并通过同组race三轮；本节仍属于M6，不开始新阶段。

1. 按 `m6-report.md` 的 `windows-workspace` 映射补齐本机可执行的剩余CI命令：SQLite路径身份、data-dir锁、Cue shell、后台命令无窗口、Windows子进程、PowerShell安装fixture、Goose身份/就绪、OpenCode慢shim。先读测试确认副作用仅在scratch，不运行真实安装、真实Provider或新增全量下载。复用新隔离runner，所有命令串行；一个检查有明确失败仍可继续独立检查，但真实配置漂移立即停队列。
2. 对新失败按源码及原始输出定位，属于本次功能或Windows必要行为的缺陷做最小修复并复验；不跳过用例或调整系统权限求绿。记录实际未覆盖的前提，不能把未运行当环境阻塞。
3. lint不原样重跑全仓：先核实已报新增诊断在最终源码中的状态及未覆盖包，必要时仅复核受影响包。历史与换行诊断须有证据，不能笼统豁免。
4. 打包已多次TLS失败，无明确新根因与条件变化前不再运行完整build。仅允许从现有日志、配置和依赖源码定位实际失败链；小文件probe不能再次单独成为重跑依据。symlink权限、本人授权、真实Provider和安装升级继续保留相应未验/失败。
5. 检查结束后刷新报告、精确源码清单和统筹结论。自动可执行门禁不因已存在其他失败而遗漏；人工暂缓与完整M6验收失败分别记录，未通过前不提交或启动后续阶段。

上述Windows命令现已补齐。五包lint仍有229项诊断，继续按[M6 lint门禁收口任务](m6-lint-closeout-plan.md)处理本交付的真实问题与checkout格式差异；这仍属于M6，不以M5已有代码为由豁免门禁。
