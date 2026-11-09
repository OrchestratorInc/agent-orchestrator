# M6 Chrome 注册测试隔离

日期：2026-11-08。状态：SV-M6-08限定修复已交接并独立验收通过；完整包/race均13 PASS/1既有真实Chrome E2E SKIP，lint零诊断，指定配置及真实注册键未变化。最终367项源码摘要 `3a7591eea63d9b304455382b491b7e77359bc7d6b669fe84bcf10f2f0ed2f0f4`，旧污染指针未恢复。输入366项快照 `f96d699e492a3aa3ecac5e8f9eeb6d52b30f7369952dc8dfa62a1f0ea4dbeb69`，仍属于M6，不新增执行者或阶段。具体证据见[统筹验收](m6-acceptance.md)。

## 已确认的问题

`backend/internal/browserchrome/chrome_test.go` 的 `testInstaller` 仅隔离文件路径，没有覆盖注册表实现。`Installer.reg()` 在注入为空时调用 `platformRegistry()`；Windows实现会直接读写 `HKEY_CURRENT_USER\Software\Google\Chrome\NativeMessagingHosts\dev.ao.browser_bridge`。HOME、USERPROFILE和AO_DATA_DIR隔离不会改变HKCU。

旧完整Go日志中 `TestSetupRegistersInstalledChannelsOnly`、`TestSetupWithoutChromeFails` 都实际进入Windows注册路径，未验证预期的目录型注册行为；首次失败又使正常移除步骤无法执行。2026-11-08 15:40（北京时间）统筹只读查询发现上述键存在，默认值指向 `C:\ao-m6-tests-507820bdd394\TestSetupWithoutChromeFails212292381\001\data\browser-chrome\dev.ao.browser_bridge.json`。证据为C盘 `ao-m6-safe/supervisor-browser-registry-readonly.json`。当前值明确属于旧测试目录，但没有可信的测试前值，不能推断原本应当删除还是恢复其他注册。

## 最小交付范围

1. 检查本包所有Installer测试调用及注册注入点。在运行现有测试之前完成隔离，禁止为了红测再次写真实HKCU。既有失败日志和已确认调用链作为输入证据。
2. 复用已有 `hostRegistry` 和 `fakeRegistry`，让目录型注册测试明确选择文件实现，注册表型测试明确使用内存fake。不得依靠Windows跳过、放松断言、改变产品默认注册行为或修改系统权限通过；不增加通用注册表框架或依赖。
3. 保留完整Setup/Status/Refresh/Remove行为，以及“另一安装拥有注册时不能删除”的原断言。若必须调整注入形状，全部调用者一起闭合；不得为fixture本身新增用例矩阵或源码字符串测试。
4. 隔离修复后运行本包完整检查及必要race/lint。前后只读核对该精确真实注册键与三份既有配置的身份，结果必须无变化。测试数据、缓存和日志均在C盘，沿用串行runner及8GiB空间门禁。
5. 本次不清理或改写真实注册键，不运行真实Chrome setup/remove，不改现有Chrome扩展、安装或用户资料。旧残留保留为未关闭环境副作用，记录当前事实；没有测试前备份时不声称恢复成功。
6. 更新四份执行文档并简洁交接源码身份、实际命令/退出码、注册键前后证据和剩余项。统筹独立验收后再排后续工作；不提交、Push、Merge或发布。SV-M6-06停止状态及SV-M6-04旧Claude记录事件保持。

本任务只关闭测试再次污染宿主的路径，不构成真实Chrome桥接安装、浏览器交互或原注册状态恢复的验收。
