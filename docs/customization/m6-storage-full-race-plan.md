# M6 存储完整 race 的逐包复核

日期：2026-11-08。原Astra high会话接续，不新增Agent或阶段。399项源码摘要仍为 `7ffa30fe69dd6b6fedeee44f0b1e90ee5cd45d1de44cf995ac88f162e9687590`；最新完整前端和完整lint已通过，本批不重复。

状态：本次验证失败。sqlite运行期间监测命令异常exit1、无完整watch或Go退出码，原始日志15 PASS/2 SKIP且无包终态；已按PID/创建时间/命令确认并停止遗留三个进程，`taskkill` exit0、remaining=[]。store未启动，不追加业务重试。统筹独立核对818份双树SHA、26证据SHA与原始终态，errors=[]；连续监测门禁记INCOMPLETE，不能以收尾通过覆盖。

接续仅一次、最多5分钟的只读归因：读取实际runner与执行错误，限定19:33:40–19:34:39 +08:00查询相关Windows Application/WER/资源耗尽事件；无证据或权限即记未知。禁止修改runner、运行故障注入或业务复验、扩大系统扫描、请求人工操作。最后检查本次进程0与身份稳定后HANDOFF，再由统筹依据新证据决定下一步。

最新续接：上述只读归因未确定触发原因；[共享异常收尾修复](m6-validation-cleanup-plan.md)已完成限定验收。原会话先仅运行此前未启动的store完整包一次，使用新共享Job收尾的 `storage-full-run.py` 入口与新证据标签，命令仍 `go test -race -p 1 -parallel 2 ./internal/storage/sqlite/store -count=1 -timeout=15m -json`。sqlite本轮不重跑；不追加故障自检。记录正常退出/超时/异常、独立terminal、持续观测与Job最终成员，任一安全异常按新失败闭锁处理，结束即HANDOFF。前端/lint PASS无需重复。

最新结果：store已执行，exit1、包900.464秒，原文 `panic: test timed out after 15m0s`。250顶层＋177子项PASS，但包FAIL；最后活动用例只运行56秒，不能据此判死锁。两层terminal自然exit1、无异常/残留，持续监测完整。统筹818份身份及12证据SHA、原始事件独立核对errors=[]。本逐包任务停止追加原样复验，后续见[store测试装配最小修复](m6-store-fixture-plan.md)。

## 依据与范围

旧 `m6-lint-b2-storage-race` 同时执行sqlite/store两包（`-p 2`），两者各耗尽15分钟预算；活动栈在runnable迁移SQL，未终态七项后续独立race全部PASS。完整命令没有单项FAIL终态，不能由包时限直接认定业务死锁，也不能用局部PASS代替完整包。

本次改变为两个测试包逐个独占执行窗口，减少迁移CPU/I/O争用；保持每包15分钟和包内 `-parallel 2`，不增加超时、不修改fixture或断言。两包各只运行一次：

```text
go test -race -p 1 -parallel 2 ./internal/storage/sqlite -count=1 -timeout=15m -json
go test -race -p 1 -parallel 2 ./internal/storage/sqlite/store -count=1 -timeout=15m -json
```

通过后保留完整包、顶层与子项数量和SKIP详情；失败时保留准确终态/超时栈，给出最小下一步，不追加第三轮完整复验、不把失败归为已证实环境问题。sqlitetest已有完整race PASS且源码未变，本批复用。若第一个包因产品失败退出且不影响第二包的安全前提，可继续第二个独立检查；磁盘/真实配置/进程边界异常立即停止。

## 安全与交接

复用C盘隔离runner、缓存和scratch，8GiB门禁与运行监测、配置/HKCU只读核对；两个命令必须串行，统筹只wait监控。每条命令另设17分钟父进程上限，防Go超时后子进程残留；只停止本轮确认的进程树。禁止下载、安装、共享依赖、清缓存、扫描/复制/清理E盘旧`.tmp`、修改受保护配置或真实用户数据。不要新建通用监测/报告框架。

执行结束核对源码/依赖身份和本轮进程，交付短HANDOFF并同步四执行文档。无源码修改时沿用399项清单。完整前端及lint无需重复；打包TLS、SV-M6-06三轮停手、24项能力/平台限制、人工/真实Provider/安装及其他OS未验继续保留。M6仍不提交、不进入后续阶段、不Push/Merge/发布。
