# M6 Windows 私有目录与账号回归

日期：2026-11-08。状态：本批24项源码变化及353项最终快照已独立验收其限定范围；原18目标为16 PASS/2 symlink权限FAIL，M6整体仍未通过。由原Astra high会话实施，未新增阶段或执行会话。输入为331项源码快照 `3498ab07e29607b6707d2d740b55ab8bcdd5d2ede0b8396c045d2ee9f9dfec8a`。依据Q9/Q10、M6-02/03/06及旧完整Go日志。

## 目标与范围

验证Windows上账号与Reviewer目录的实际权限、假登录后的凭据保全、工作区不可读错误及AO状态目录导入拒绝。旧失败来自 `ao-m6-safe/m4-fast-backend-full-analysis.json`，该次隔离不完整；须在新runner下按精确目标复现，不能把Unix mode断言失败一律当作fixture问题。

| 范围 | 已见证据 | 需要关闭的边界 |
|---|---|---|
| `backend/internal/reviewgateway/` | 两项私有目录测试报mode 0777；`ensurePrivateDir`仅MkdirAll/Chmod，manifest以0600创建，没有Windows专用ACL处理 | 核实Reviewer根、子目录和授权manifest的Windows所有者/DACL；保留目录归属、内容寻址、重复准备与改变授权生成新manifest行为。若权限确有缺口，最小修复真实创建/复用路径，不能只调整期望值 |
| `backend/internal/service/agent/` | 全局模型发现目录仅MkdirAll(0700)，测试报0777；账号目录/文件mode断言失败；多项假登录或重认证返回retryable，外部凭据读取报could not be opened safely | 将平台权限断言与产品失败分开。使用现有Codex私有文件/ACL边界验证账号槽位、假登录提交、设备凭据切换与外部替换保全；核对private与device credential两种政策，不以放宽ACL/身份校验修复失败 |
| `backend/internal/service/session/` | PR文本实际CRLF；不可读测试仅Chmod(parent,0)，Windows未触发错误；另有symlink权限失败 | 保留按已验证SHA检出及真实内容断言；确认Windows真实不可读条件下返回既定API错误。权限fixture必须实际制造被拒绝访问，再检验服务行为，不直接伪造最终错误 |
| `backend/internal/service/importer/` | AO状态导入拒绝测试仅设HOME，生产os.UserHomeDir在Windows读取USERPROFILE；旧结果意外允许导入 | 在隔离用户目录下复现并校正真实输入，保留AO状态路径与宽泛目录拒绝。核对实际AO_DATA_DIR来源，不让修fixture掩盖产品路径防护遗漏 |

第三批会触及service/agent和service/session的Windows底层边界，以其最终源码为本批输入；此前已经通过且不受新改动影响的目标复用证据，不为换批重复运行。已有symlink权限失败继续保留，不修改系统策略或新增无条件skip。

## 实现边界

- 先搜索所有调用者，列明生产路径、现有测试/fixture、不同OS文件以及确受影响的接口。复用仓库现有句柄身份、ACL和私有文件实现模式；不为此迁移全仓目录管理或新建通用安全框架。
- 账号测试现有fake终端和factory可复用，所有凭据均为明确的假数据、根目录均来自本轮C盘scratch。不运行真实Codex登录、Provider推理、Hello、桌面认证或用户资料导入。真实配置只监测身份，不读取内容或回写。
- Windows不以`os.FileMode`证明私密性；Unix原权限检查保留。验证实际所有者、允许主体及继承政策，并保留reparse/硬链接/身份改变等现有拒绝。不得把拒绝访问当成成功，也不得把生产只读检查改为自动修复用户文件。
- 只可操作测试创建、已核实绝对路径的scratch文件ACL；清理必须恢复可清理状态。不得改用户目录、盘符根、系统策略、登录权限或现有真实数据权限。无法安全构造平台条件的单项记未验并继续独立工作。
- 账号副作用的错误处理需保留“已经写入/尚未写入”的区别，避免误报失败后重复写入；外部替换不能被旧操作覆盖。安全文件修复必须测试原失败路径及相应拒绝边界，不能用宽泛忽略求绿。
- 不新增配置、依赖、持久化schema或公开API；若具体根因确需这些变更，记录精确差异和依赖，继续可独立执行部分。

## 验证与交接

1. 沿用唯一Astra执行会话、C盘隔离runner和已有依赖；输入快照先冻结。缓存/日志/数据/产物均放C盘，8GiB空间门禁和高I/O串行保持，统筹不另起测试。禁止扫描、复制或清理E盘旧.tmp。
2. 结合第三批结果形成精确的未通过目标清单，在新隔离下取得当前红测。根因按产品逻辑、测试装配、OS能力分别处理；实现方法与必要检查顺序由执行方自主决定。
3. 修改后跑受影响完整包和必要race，保留真实失败/跳过；静态检查按触及包、固定2.13.2运行。机械格式和fixture维护不新增用例矩阵，已有用例优先补强。未受影响的已通过套件不重复，全仓或完整打包不盲跑。
4. 同问题连续三次修复失败停止该问题，不继续试探；SV-M6-06既有停止状态不变。人工、真实Provider、打包TLS及安装升级保持原边界。
5. 更新四份执行文档，交接精确命令/退出码、源码与输入身份、实际权限证据、测试规模及剩余问题。统筹独立验收后再决定后续工作，执行方不提交、Push、Merge或发布。

本批最终快照 `8e1b120f1cb21be5cf3a1e81838acb293dddf6d57f9f72887eba92e342d354bb` 的353源码、4文档、6依赖已独立核对，无差异或遗漏。14条测试/lint命令串行，指定配置身份稳定；agent/session完整包仍各有1项既有symlink权限失败。具体红绿测、退出码与未验项见[m6-acceptance.md](m6-acceptance.md)；本批限定修复验收不等于Windows所有权限场景或M6阶段通过。

## 实施中独立审查

Reviewer曾实测目录SetSecurityInfo把ACL传播到尚未验证的硬链接外部目标，已改为只处理当前对象并通过完整包。模型发现的新调用也实测同类副作用；共享目录边界首轮修复后16项定向通过，最终账号完整race只保留既有symlink失败。Reviewer身份断言改用句柄文件ID，假终端重复关闭模拟真实NotFound。设备凭据文件的ACL政策和生产登录状态机保留。统筹只读审查，未并行启动测试。
