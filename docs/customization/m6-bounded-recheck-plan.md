# M6 存储未终态与前端 ABI 有界复验

日期：2026-11-08。接续[完整门禁归因](m6-full-gates-diagnosis-plan.md)，仍由原 Astra high 会话执行，不新增会话或阶段。输入为 `m6-residual-source-manifest.json` 的399项源码，摘要 `7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590`；归因报告位于C盘证据目录 `m6-diagnosis-report.md`。

状态：本批已交接并限定验收。统筹独立核对818份源码/文档/依赖SHA、93份证据SHA及逐项终态，exit0、`errors=[]`；前端55 PASS（含旧47项）、存储7顶层race PASS，12条操作无重叠，最低C盘12069048320字节。产品源码和测试未修改。当前Node137条件成立，接续[完整自动门禁复核](m6-final-automated-gates-plan.md)；M6仍FAIL。

## 本批结果

消除两个可自主验证的不确定项：当前Node测试依赖的实际可用性，以及旧完整存储race的七个未终态。复用已有适用PASS，不重跑完整前端、完整存储race或完整lint。本批不处理下载/打包，也不再要求本人操作。

## 前端

仅对既有C盘验证副本的 `better-sqlite3` 恢复已登记的Node137归档。先核对 `sqlite-prebuild-stage.json` 中既有文件的路径、归档条目及SHA `4ee5e653174d6ddd301605d351798cdae2613da06c4f37ecdce263021fcf1255`，确认解压目标在该依赖目录内；保留当前Electron149模块的可恢复副本及SHA。禁止执行会重新下载的stage脚本、重装依赖、共享其他checkout依赖、改版本或lockfile。归档缺失或身份不符就保留该项未验，不搜索整个磁盘或下载替代。

使用既有Node24.22.1入口及隔离runner，先做一次内存SQLite建表/读回/关闭，确认实际加载的ABI及模块路径；成功后仅执行 `npm test -- src/main/browser-profile-import.test.ts --maxWorkers=2`。全部55项含旧47项失败须有明确终态，不能只看文件摘要。i18n/Workflow已有局部覆盖，本批不重复。

结束后明确记录副本依赖处于Node137测试状态，不能将它当Electron候选；本批不启动Electron、Forge或重建149模块。未来桌面验证必须另做运行时身份检查。

## 存储

先按原语义逐个串行运行两个活动用例，race、`-p 2 -parallel 2 -count=1 -timeout=3m -json`：

1. `./internal/storage/sqlite` 的 `^TestMigrateRecognizesPreRenumberedChatSchema$`。
2. `./internal/storage/sqlite/store` 的 `^TestSessionTelemetryFactsRoundTrip$`。

再在store包一次运行原日志中暂停的五项，保持同样资源参数、5分钟上限：`TestAgentInstallJobStoreRoundTripAndList`、`TestInterruptActiveAgentInstallJobs`、`TestCodexAccountSwitchIdempotencyAndSingleActiveConstraint`、`TestCodexAccountSwitchRejectsObsoletePhases`、`TestCodexAccountSwitchTransitionsAreCompareAndSwap`。先核对没有更晚且源码适用的PASS；如有则复用原始证据，不重复。

七项均有终态即可交接。通过不等于完整包通过，不为取得整包绿灯做大规模fixture替换。若失败，保留原始栈并只修可确证的产品缺陷，按实际影响补最小检查；环境/时间预算不足写明，不能延长时限或改迁移断言求绿。迁移历史测试必须保留真实迁移。任何同一问题三次失败继续停手，SV-M6-06不在本批范围。

## 运行与交接

沿用已有C盘隔离runner、8GiB启动门禁和运行监测、三配置/HKCU只读前后记录；高I/O执行窗口归执行方独占，统筹收尾后再独立核验。禁止扫描/复制/清理E盘旧`.tmp`、清缓存、操作真实用户配置或旧污染。实现方法由执行方决定，不新增测试框架、重复报告生成器或Agent。

交付简短结果、实际命令/退出码、旧七项与前端55项终态、依赖前后身份与剩余风险；同步 `m6-report.md`、`m6-coverage.md`、`user-guide.md`、`troubleshooting.md`。未改源码时复用399项清单，仅说明核验结果；有实际源码改动才刷新相关清单并说明范围。完整lint超时、完整前端/存储历史失败、打包TLS、24项能力/平台限制及人工暂缓保持各自状态。M6整体未通过，不提交、Push、Merge或发布。
