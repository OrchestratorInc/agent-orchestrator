# M6 进程观察失败与恢复边界

日期：2026-11-08。状态：SV-M6-07限定修复已交接并独立验收通过，M6整体仍未通过。最终366项源码快照 `f96d699e492a3aa3ecac5e8f9eeb6d52b30f7369952dc8dfa62a1f0ea4dbeb69`，四包完整race及六包lint通过，CLI/ConPTY定向末次通过；10条验证串行、指定配置/注册键未漂移。输入为353项快照 `8e1b120f1cb21be5cf3a1e81838acb293dddf6d57f9f72887eba92e342d354bb`。依据Q10及仓库“不得把失败/未知runtime探测当作Session死亡”的规则，仍属于M6。完整证据与测试规模例外见[统筹验收](m6-acceptance.md)。

## 问题与影响路径

当前 `backend/internal/processalive/process_windows.go` 在OpenProcess错误时仅把AccessDenied视为存活，其他错误返回false；WaitForSingleObject出错也返回false。`adapters/runtime/conpty/ptyregistry/pidalive_windows.go`有类似的OpenProcess错误归类。

静态调用链显示，`persistenthost/host.go`的`acquireHostLock`、`ConnectOrStart`和`Reconcile`会消费这个false，进入锁重取、重新启动或描述符清理路径；`ptyregistry/registry.go`的`scanLocked`会过滤记录并重写注册表文件。`runfile/runfile.go`及ConPTY存活包装也是实际消费者。非Windows实现另有Kill/ps语义，不能无意改变其行为。

统筹补查到 `cli/root.go` 将同一函数注入 `Deps.ProcessAlive`，因此CLI启动、状态与停止的间接调用也须核对；不要只搜索直接调用后遗漏该接线。`persistenthost.HostPID` 是仅供内存计量的只读观察，不能把为了保留所有权而采用的保守判断直接说成已证实存活。`Shutdown` 连接失败后的死亡判断同样需要保留未知；不因探测失败报告已经停止。

以上最初为静态调用链；当前 `m6-ro-boundary-red` 已用可控查询错误复现：三个业务用例FAIL，运行文件、宿主锁/描述符及注册记录丢失，隔离启动钩子被再次调用。没有实际启动第二个Provider；具体命令及证据见[m6-acceptance.md](m6-acceptance.md)。该问题独立于SV-M6-06的分离宿主交互失败，禁止以本任务重开其第四轮。

## 交付结果

1. 明确当前平台哪些观察能证明进程已退出、哪些只能说明无法查询。对同一宿主，未知必须保留所有权和占位，并阻止竞争启动/错误清理；真正已退出的恢复继续工作。保留AccessDenied、非法/越界PID、PID身份和所有权校验，不拿未知值伪造肯定存活的对外状态。
2. 搜索全部调用者后选择最小完整修复：共享观察边界、宿主恢复、注册记录和runfile的语义一致；需要修改的签名/调用者/测试一起闭环。复用现有恢复fixture，不新增通用故障注入或进程监管框架。
3. 用可控的观察失败验证业务后果：既有锁/描述符/注册记录不被删除、不启动第二宿主；恢复为明确死亡观察后可走既定恢复。测试必须覆盖仓库行为，不测试Windows API本身，也不通过耗尽系统句柄/内存制造故障。旧分离宿主交互用例不运行。
4. 不读写真实AO、Provider或用户配置，不启动真实Agent，不改变系统权限。所有数据和进程限定现有C盘scratch，沿用隔离runner、8GiB空间门禁和高I/O串行；仍只允许统筹加一个Astra执行会话。
5. 先红测/具体证据，后最小修复，再受影响包和必要race/lint；已通过且不受影响的检查不重跑。同问题三次修复失败停手，其他门禁状态不变。交接真实命令/退出码、源码身份、恢复前后事实及四份执行文档，待统筹独立验收，不自行提交或发布。

确需超出上述边界的更改时先记录具体原因与影响，保留未验项并推进独立工作。Windows Hello、实际Provider、打包、安装升级不属于本任务。
