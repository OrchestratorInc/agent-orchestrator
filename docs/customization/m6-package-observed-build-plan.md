# M6 Windows 打包阻塞闭合

日期：2026-11-09（北京时间）。原Astra high会话执行，统筹负责验收；总并发2，不新增Agent。用户要求加快并减少人工操作，当前不追加SQLite第四组或重复已通过套件，优先产出可核对的Windows候选。

## 首次观察构建与续接

`build-node24-package-observed-20261108T161537` 在45.955秒被观察器中断：`diagnosis-run.py:29` 在Forge原生重建过程中读取暂缺的better_sqlite3.node而抛FileNotFoundError。自然业务退出码null、观察/回收码95，外层工具exit1；未进入下载阶段，不记为产品失败或TLS复现。16个本次Job成员已回收，remaining/errors为空；内层terminal/.exit因被外层回收而缺失，缺口保留。原生模块和.forge-meta最终缺失，原149备份保留，不能启动副本。

统筹已读取实际脚本、原始日志与终态，8脚本SHA一致，`supervisor-package-observed-review.json` errors=[]。这是计划已允许的重建状态被监测误判。现授权仅修复Forge观察分支对原生文件及其meta的暂缺读取，记明确状态而非成功；其他读取错误、默认Node137分支和源码/配置/磁盘门禁不放宽。保持最小diff，不新增框架或故障自检。

修正后允许一次有新条件的完整构建续接，使用新的证据标签和既有请求观察；不再运行代理对照请求。此次续接唯一目标是让构建得到自然终态及实际下载证据/候选，父17分钟和持续8GiB不变。成功必须另验原生模块/ABI和候选，临时缺失不能被当最终有效；失败完整交接后不追加。旧中断和所有原失败不得覆盖。

续接 `build-node24-package-observed-resume-20261108T162404` 又因重建中读取.node的PermissionError被中止，91.626秒，自然码null、观察/回收码95，17个Job成员回收无残留。消息errno13、winerror=null，原始完整栈未保存；并不能仅由这些字段证明具体Windows锁类型。终态.node为2097152字节、meta缺失，完整性未知，不能启动或充当候选。

根本修正为区分输入和输出：Forge运行期间仅核对源码/依赖输入，不读取正在生成的.node或meta；产物必须在构建退出、确认无生成进程后核对。现允许对此同一问题的第二次修复及一次续接，删除上一轮Forge产物轮询逻辑，默认Node137分支不变。不得扩大为忽略任意PermissionError。修正后用相同脱敏请求观察和资源/进程门禁运行一次完整build；再出现观察问题即停手，不追加第三次修复。无其他业务测试、代理对照或框架开发。

## 上一轮构建终态（失败记录）

`build-node24-package-observed-postbuild-20261108T163044` 的完整npm build已自然失败：79.690秒、自然码1；观察器无异常，原生产物检查延后生效。内层发现1个残留Job成员并精确回收，因此包装码95；外层自然接收95、无残留。统筹独立读原始terminal和网络事件，`supervisor-package-postbuild-review.json` errors=[]。

真实链路已确认：Electron44.7.0 ZIP缓存hit=true；SHASUMS请求经github.com重定向到release-assets.githubusercontent.com，在TLS建立前ECONNRESET；随后库回退下载ZIP，同一资产域名也ECONNRESET。HTTPS Agent为BoundHttpsProxyAgent、代理已配置、Node原生代理标志为true。这证明实际失败位置，但不证明两个代理冲突的具体机制；关闭原生代理的先前小请求同样失败，不得据此写成已定位双代理唯一根因。

构建退出并确认生成进程0后，原生模块1921536字节，SHA `bfc272e5d1d374c09986284934e5887469ae3244030a1389ed852098733c03b6`，meta为x64--149且二进制有v149符号；未动态加载。frontend/out不存在，无候选。最低C盘11492212736字节，配置/HKCU未漂移。副本现为Electron149，不直接运行Node137测试。当前不追加构建，下一步针对真实资产域名的Forge代理链路修复；M6仍FAIL、未提交。

## 最新收口结果

`build-node24-package-observed-native-owner-20261108T164314` 已生成 Windows x64 打包目录。仅本轮隔离 preload 利用已安装 global-agent 的 bootstrap 标记保留 Node24 原生代理；官方 SHASUMS 小请求 exit0，随后 `npm.cmd run build` 自然 exit0/133.726秒，缓存命中、校验清单下载成功，无 ZIP 回退。该适配不是上游正式配置开关，未修改产品或系统配置，也不证明历史代理故障的具体机制。

统筹读取原始 terminal/网络日志并独立重算六个关键候选文件、八脚本与双树报告的 SHA，`supervisor-package-native-owner-review.json` errors=[]。候选为 C:/Users/11459/.codex/worktrees/m5-desktop-safety/agent-orchestrator/frontend/out/Agent Orchestrator-win32-x64，应用0.13.0/Electron44.7.0，ABI149仅静态核对。内层回收一个残留成员使包装码为95，两层最终remaining/errors为空；最低C盘9.892GiB、指定配置/HKCU身份不变。保留旧失败，不将整条验证记为exit0。

本轮已停止，无新增测试或构建。候选不是安装器，未运行桌面、动态模块或安装升级；嵌入Commit仍为副本旧HEAD，须结合403项源码摘要与产物SHA识别。M6整体仍未通过、未提交。执行与候选详情见C盘 `m6-package-native-owner-HANDOFF.md` 和 `m6-package-native-owner-results.json`。

## 本轮收口窗口（2026-11-09）

用户再次询问完成时间。本轮仅处理已确认的 Forge → GitHub 资产域名 TLS 阻塞，沿用原 Astra high 会话，不增加智能体。先用约10分钟检查已安装依赖与既有请求证据，选择有源码依据的最小进程级修正；只允许一次官方 SHASUMS 小请求验证。实现由执行方决定，不修改系统代理、产品配置、node_modules，不禁用 TLS 或 checksum，不新增依赖或通用观察框架。

小请求成功后，允许一次完整 build（原17分钟上限、持续8GiB门禁、C盘缓存和精确进程回收不变）。失败则立即交接实际原因；没有得到新修正依据时也交接，不照旧重试或增加对照矩阵。成功后仅核对候选与原生模块，不启动安装/桌面。SQLite第四组、已通过完整前端/lint以及真人步骤继续暂缓。本轮到明确终态为止，不因此声称M6验收成功或进入下一阶段。

## 既有诊断依据

- `build-node24-package-cache` 的完整 `npm run build` exit1/72.724秒；Vite及prePackage结束后，Got报 `Client network socket disconnected before secure TLS connection was established`。实际失败URL/代理/缓存命中未记录，不能归因于ZIP损坏或直接照旧重试。
- 已有官方Electron44.7.0 x64 ZIP及SHA，旧小清单诊断曾成功，但使用不同隔离运行目录及请求限制，不等价于完整Forge执行。
- 安装依赖中的packager调用真实 `@electron/get`，即使ZIP命中仍下载SHASUMS；任意校验异常可能回退重新下载ZIP。现有debug能报告公开URL与cache命中。旧runner过滤DEBUG，因此只在外层设置无效。
- 副本当前为Node ABI137测试模块，原Electron149备份已存在。Forge原有native rebuild会按149重建；这是构建步骤的预期产物变更，不能用存储专用入口的固定137断言运行构建。
- C盘当前约11.1GiB，仍须启动和运行中8GiB门禁。源码403项摘要 `0df24e5a17c80454a07125b3b5b2c53bb14647cc0271d2beaffd7dab01dfe4fc`；主仓不作为构建目录。

## 要交付的结果

定位真实Forge请求/运行环境差异，能在现有授权范围修复时修复并产出Windows候选。具体实现由执行方选择，优先使用已安装依赖的正式配置入口及C盘进程环境，不改产品配置、不降TLS或checksum校验、不安装新依赖。

先做有界源码/旧日志检查，核实真实Node/npm、Got代理初始化及packager选项；不扫描无关目录或重复全仓审计。必要时只对C盘现有验证入口增加最少的脱敏观察：Node版本、公开请求URL/序号、缓存命中、代理是否启用、错误阶段。不得记录代理凭据、认证头或整个环境；不用宽泛NODE_DEBUG网络转储。可固定使用已有 `@electron/get` / `electron-packager` debug命名空间，不改依赖文件。

本批允许一次完整 `npm run build`，须具备上述新增观察或已明确的运行环境修正，禁止原样复跑。沿用现有缓存和官方ZIP，保留真实下载/校验链路；不预先重复下载大工件，不清缓存或重装。父进程上限17分钟、持续8GiB空间门禁及共享Job精确回收。若必须超出这些边界则短HANDOFF说明事实，不擅自放宽或要求用户操作。

构建成功后只核对候选路径、关键文件/版本/摘要及ABI，不启动桌面、安装或升级，不发布。构建失败则保存精确请求和失败阶段，列出有证据支持的最小修复，不在本批追加第二次完整构建。不能将小请求成功或Vite成功称为打包通过。

## 不变的安全边界

只在已有C盘隔离副本、缓存和scratch运行，高I/O严格串行，统筹只wait。保持当前源码/六依赖及真实配置/HKCU身份监测；native模块和构建产物的必要变化须按构建阶段单独记录，不能悄悄更新产品源码清单掩盖漂移。七个既有脚本如调整，记录最小diff和新SHA；不新建通用框架/故障矩阵，不再运行故障自检。

不扫描/复制/清理E盘旧.tmp，不改受保护配置/系统代理/注册表或真实AO数据，不重跑SQLite、完整前端或全仓lint，不要求Windows Hello或真实Provider调用。未获得明确同意不得提交配置文件。

## 简短交接

交接实际命令/退出码、失败请求与阶段或候选摘要、改动范围、磁盘/进程/身份终态。更新m6-report的当前结论即可；其他指南待候选可用后统一核对，不重复长表。M6仍须安装升级/E2E等原设计证据，未满足不称通过；暂不提交、不开后续阶段、不Push/Merge/发布。
