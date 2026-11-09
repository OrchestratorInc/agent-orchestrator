# M6 Windows 安装器生成

日期：2026-11-09。原Astra high会话执行，只有统筹和一个执行会话。目标是生成总设计§13要求的Windows安装器候选，复用现有0.13.0打包目录；不以产物生成替代安装升级验收。

## 最新验收结果

安装器生成与静态核对通过。`build-node24-installer-agent-make-20261108T173856`执行固定Node24的Forge `make --skip-package --platform win32 --arch x64`，自然/包装exit0/0，98.325秒；父级exit0/0，99.293秒。日志实际输出`Making distributables`成功和产物目录。构建临时生成器按编译阶段识别并仅运行一次，继承隔离路径、RunAsInvoker及显式`/D=`，未运行最终安装/卸载/应用。最低C盘9.338GiB，两层Job自然退出、无残留；指定配置/HKCU相对用户认可基线未变。

产物：`C:/Users/11459/.codex/worktrees/m5-desktop-safety/agent-orchestrator/frontend/out/make/Agent Orchestrator Setup 0.13.0.exe`，193863342字节，SHA256 `1d80e81d4997fba4553503a38ca517c25a3c24f7f9f3ab90288914a830f4cd44`。统筹独立重算安装器/blockmap/debug记录SHA一致；重读原始terminal、生成器三阶段日志，并实际执行Get-AuthenticodeSignature与VersionInfo，输出NotSigned/0.13.0。配置基线差异也独立核对为仅获授权的config.toml条目。证据`supervisor-installer-review.json` errors=[]。

执行方报告六关键候选文件SHA未变；builder正常新增resources/elevate.exe，不称整个目录字节未变。工具三项下载与固定SHA已通过，make未新增下载。未生成latest.yml，未发布；本地产物未签名。阶段整体仍FAIL：运行/动态模块、安装升级及原有缺口未验，不提交M6。原始交接为C盘`m6-installer-agent-HANDOFF.md`和`m6-installer-agent-results.json`。下面保留实施范围及历史阻断，不覆盖旧失败。

## 已知条件

- 已有Windows x64候选和关键SHA，见C盘`m6-package-native-owner-results.json`。完整build自然exit0，静态核对通过；不重跑package/prepackage/premake。
- `frontend/makers/maker-nsis.ts`使用已安装app-builder-lib26.15.3；`publish:null`，无签名输入时支持本地未签名构建。Forge CLI支持`--skip-package`，直接CLI入口可避免npm的premake脚本。具体执行方法由执行方决定。
- 当前没有out/make或已知electron-builder工具缓存；NSIS相关构建工具可能需要由现有依赖下载。必须使用库本身固定来源与校验链，不新增项目依赖、不修改配置/node_modules。
- 候选运行因HKCU协议注册和生产更新请求未隔离而阻塞。本批仅生成和静态检查安装器，不运行应用、安装器、卸载器或升级操作。

## 本批范围

先有界核实实际工具下载来源、缓存位置、已有签名选择条件和磁盘预算。必要工具下载属于本批构建范围，只落C盘隔离缓存；优先使用现有已验证的进程网络设置，保留TLS/上游校验，不提前重复下载大工件，不启动额外网络诊断矩阵。发现实际来源/校验或空间条件无法满足时交接，不盲目试跑。

条件满足后，通过现有受控入口只执行一次Forge make并复用候选，不重新编译。固定当前Node24，所有缓存/临时/输出在C盘；不继承签名、发布或Provider凭据，不使用用户证书。本地未签名结果须明确标识。现有入口若需要增加本批标签或缓存环境白名单，可作最小修改并记录diff；不得新增通用监测框架、自检或忽略现有门禁。

前置与持续8GiB门禁、单命令高I/O、私有Job及PID/FILETIME精确回收保持，父进程上限17分钟。源代码和依赖输入监测保持；生成中的安装器只在进程退出后读取摘要。工具下载/压缩占用导致门禁中止时，保留真实失败和本批产物位置，不删除用户文件或清理旧缓存凑空间。

成功后核对安装器路径、大小、SHA、版本、签名状态、构建实际输出和候选输入身份；检查生成的更新元数据只作静态证据，不上传或发布。失败则交接实际退出码、失败阶段和有依据的下一步，不追加第二次make或扩大工具链改造。

## 交接与边界

短HANDOFF和结构化实际命令/自然及包装退出码、下载/构建阶段、必要产物摘要、最低磁盘与进程/配置/HKCU终态；只追加m6-report。不触E盘.tmp、真实AO和受保护配置，不安装新依赖、不重跑完整前端/lint/SQLite，不索要Hello或手动操作，不修SV-M6-06。

M6仍未通过，当前安装升级缺隔离环境，运行和动态模块也未验。未通过前不提交、不进入后续阶段，不Push/Merge/发布。

## 本批结果与恢复条件

### 最新授权：恢复执行

用户已明确回复“认可当前配置，更新校验基线继续”。统筹再次读取真实文件确认，SHA仍为`ac0837da07a9caec03bb258391508d7c85bfe1526e8efdd567e4ddcea906e966`，mtimeNs仍为`1794157698713543900`。允许原执行会话仅接纳这一条已认可的配置身份，保留旧基线和漂移证据；其他受保护配置/HKCU条目不得随之重置，也不得修改真实配置。随后按原范围恢复工具准备与单次make，不重复已完成预检/全套测试。

构建用临时生成器仍须核对实际输出路径；使用已有依赖固定工具来源与完整性校验，不扩大为重建第三方工具或全量上游审计。最终应用/安装器/卸载器不运行，安装升级隔离缺口保持。

### 上一批阻断记录

### 认可配置后的工具下载续接

用户授权已落实：仅Codex配置身份条目更新，旧基线和授权记录分别保存为C盘`m6-installer-gates-preflight.before-approval.json`与`m6-installer-baseline-approval.json`，其他配置/HKCU未变。工具准备`build-node24-installer-tools-approved-20261108T172541`自然/包装exit1，32.117秒，NSIS固定资源在GitHub资产域名TLS前ECONNRESET；两个Job无残留，最低C盘9.846GiB，make仍0次。

新增源码证据：已安装`app-builder-lib/out/util/electronGet.js`的`downloadArtifactToFile`显式传入`config.downloadOptions.agent ?? buildGotProxyAgent()`，与上一批仅保留全局Node原生代理的设置不同。接续仅核对这一真实调用链的有效Agent，采用最小进程级适配或库支持参数；具体方法由执行方决定。验证首个固定NSIS工具下载及依赖内SHA通过后，继续原工具准备和单次make，不再重复小请求矩阵或完整package。保留失败，禁止改系统代理、node_modules、产品配置或TLS/checksum。

显式Agent适配后，执行方报告NSIS/7zip/resources下载与依赖SHA通过、工具命令exit0；独立收口证据待交接。随后发现NsisTarget对生成器只传`__COMPAT_LAYER`，WineVmManager在Windows将该env直接交builder-util.exec，隔离TEMP/TMP等环境未继承。统筹已实读该分支，允许沿现有本批进程级适配最小补齐这个已识别生成器的隔离环境，保留RunAsInvoker，不影响其他子进程、真实用户环境、依赖文件或最终安装器。具体方法执行方决定；必须核对实际传入的必要路径和生成器身份后再执行原单次make。不要将这一必要隔离修复扩大为通用进程拦截框架，也不重新询问用户或重复已通过下载。

make执行0次。唯一工具准备入口`build-node24-installer-tools-20261108T170906`在创建Job/子进程前被配置身份门禁拒绝：自然退出码null、包装95、AssertionError，耗时0.005秒。无工具下载、无生成器/候选/安装器运行，out/make和builder缓存均未创建。统筹独立读取原始terminal，核对当前文件SHA及时间：真实`.codex/config.toml`在北京时间2026-11-09 01:08:18变化，旧SHA `ae0378cb89f44241a4aef3526bcbe3d5e0b557cd0a4434de552ac9d97b760931`，当前为`ac0837da07a9caec03bb258391508d7c85bfe1526e8efdd567e4ddcea906e966`。来源未归因，本批没有修改该配置；门禁基线未更新。其余指定配置/HKCU未变，候选身份及源码/依赖摘要保持，相关进程为0。

统筹已询问用户是否认可当前配置、仅更新校验基线后继续，等待答复；不把等待当同意。阻断依据是既有`gates-audit.py:168`的宿主身份一致性断言，而非make自身错误。执行方仅给既有run.py增加本批标签、C盘builder缓存及禁用Jiti磁盘缓存，统筹已审最小diff，无门禁删减。

临时生成器边界已澄清：构建所需BUILD_UNINSTALLER程序不等于实际安装/卸载，可在证明其只写隔离目录后执行；最终安装器/卸载器仍禁止运行。本批已确认无自定义脚本/include/preInit、日志宏未开启，SectionSize前置仅计算空间；默认`$INSTDIR`及工具初始化仍待核实。认可配置后先完成此证据，再沿原单次make范围续接，不重复整个预检或已通过套件。

证据在C盘`m6-installer-HANDOFF.md`、`m6-installer-results.json`、对应watch.terminal.json和`m6-installer-run.diff`。安装器生成及静态检查均未验，M6仍FAIL、未提交。

等待配置确认期间，原会话已完成约5分钟只读归因并回到idle，没有运行构建或修改文件。其交接指出官方NSIS v304的目录指针初始为零，不能把生成器的默认INSTDIR直接等同EXEDIR；卸载器分支的EXEDIR回填不能套用于生成器。现有模板仍是SectionSize、SetOutPath、WriteUninstaller后退出，不进入实际安装分支。工具包nsisconf未配置安装目录，但工具尚未下载，实际目录行为未验。该调查用于下一步核对，不把上游源码阅读当运行通过，也不新增完整上游测试矩阵。当前恢复前提仍是用户确认配置身份，之后在原有范围完成工具及实际目录核对。
