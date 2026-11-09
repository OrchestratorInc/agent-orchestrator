# M6 store 完整 race 超时的最小修复

日期：2026-11-08。原 Astra high 会话接续；统筹与一个执行会话，总并发2，不新增 Agent。实现方法由执行方依据代码和日志决定。本任务仍属于 M6，不表示阶段放行。

## 统筹交接验收

本批5文件10处首次建库改为既有sqlitetest.Open，真实关闭重开/第二实例/业务断言保留。定向race 12 PASS/92.556秒、包lint exit0/103.550秒/0 issues；完整store仍FAIL/900.460秒/exit1，732 PASS（488顶层＋244子项）、无单项FAIL或SKIP，最后用例只运行1秒。报告重启68.43→2.09秒，但整体超时未关闭。超长既有测试文件search 302行、workflow 1674行已申报，本批各仅增加1行。

统筹直接审阅最小diff，解析原始事件并核对403项源码、六依赖和四文档共826份双树SHA及39份证据SHA，errors=[]；三操作无重叠，全部Job残留0。证据 `supervisor-store-fixture-review.json`。此为最小装配变更的限定验收，不是完整存储包或M6 PASS。停止完整包原样重跑，下一步仅做[存储剩余覆盖收口](m6-storage-remaining-coverage-plan.md)。

## 已核实的问题

`m6-storage-full-store-race-resume-20261108T133802` 使用共享 Job 收尾入口执行：

```text
go test -race -p 1 -parallel 2 ./internal/storage/sqlite/store -count=1 -timeout=15m -json
```

包 FAIL/900.464秒，exit1，原始输出 `panic: test timed out after 15m0s`。250个顶层及177个子项 PASS；无单项 FAIL，仍不能判完整包通过。最后活动用例 `TestReportStoreRequeuesInterruptedClaimOnRestartAndFencesOldToken` 只运行56秒，栈位于初始空库的迁移SQL。另五个并行用例暂停，后续用例未运行。

统筹独立解析事件并核对818份源码/依赖/四文档 SHA、12份证据 SHA，两层 terminal 均自然 exit1、异常null、残留0，`supervisor-store-resume-review.json` 为 errors=[]。此次监测完整；sqlite前批监测中断仍单列，不能覆盖。

已通过用例中，报告重启68.43秒、预算恢复66.77秒；但会话快照79.01秒、消息投影71.19秒等也消耗预算。因此重复初始迁移是待验证的耗时来源，不能预先断言它是全部根因或修复必能让完整包通过。

## 允许范围

先读原始事件与全部调用者，确认非迁移业务用例是否可以复用已有 `sqlitetest.Open`。该 helper 每个测试进程真实迁移一次，再克隆独立数据库；已有 `newTestStore` 使用它。

初步候选为 `report_store_test.go`、`session_telemetry_store_test.go`、`budget_restart_test.go`、`search_store_test.go`、`workflow_store_test.go` 的首次空库创建；`openWorkflowStorePair` 有三个调用者，必须一起审查。此清单是定位线索，执行方可基于证据缩小改动。

最小修复应保留业务断言、真实关闭重开、第二实例及并发/CDC/旧token隔离。验证迁移、修复历史或重开的 `sqlite.Open` 不可全局替换；生产 `sqlite.Open`、迁移、schema、OpenPreMigrated和sqlitetest共享实现本批不改。不新增测试框架、依赖或测试用例，不为测试比例搬文件；既有超长测试文件在交付中申报行数与原因。

若代码或证据不支持此范围内修复，交接准确原因及最小下一步，不盲改或扩大产品行为。

## 验证与安全

1. 在主仓做最小测试装配修改，按明确清单同步C盘验证副本，更新本批源码清单，保留旧证据。记录新增/变更文件、断言未变和保留的重开路径。
2. 使用已有共享 Job runner，先运行受影响用例的 race 与受影响包 lint，记录逐项耗时。保持 `-p 1 -parallel 2`、原15分钟包预算及17分钟父进程上限。定向通过且有合理耗时改善证据后，可接着执行完整 store 包一次；不以延长时间、跳过或减少断言换通过。
3. 完整包仅此一次。若仍超时，交接已运行/未终态项和慢点，不继续盲重跑。同一缺陷三次修复未通过即停，SV-M6-06禁止第四轮。
4. 高I/O严格串行，执行窗口交付后统筹只 wait。缓存、依赖、临时库与产物留C盘，复用8GiB门禁、持续监测、配置/HKCU只读身份核对与Job精确回收。
5. 不扫描/复制/清理E盘旧 `.tmp`，不下载/安装/共享依赖、清缓存或改真实配置；不启动桌面、不请求Windows Hello或其他人工操作。已有完整前端和完整lint不重复，本批仅复核受影响包；sqlite完整包不重跑。打包TLS无新条件不重试。

## 交接

提供短 HANDOFF：最小diff、调用者/重开路径审查、完整命令与退出码、测试计数/耗时、源码与依赖身份、连续监测及最后进程0。更新执行方四文档 `m6-report.md`、`m6-coverage.md`、`user-guide.md`、`troubleshooting.md`；统筹文件由统筹更新。

M6整体仍FAIL，不提交、不开后续阶段，不Push/Merge/发布。24项能力/平台限制、sqlite完整race、SV-M6-06三轮停手、打包及真实环境未验继续可见。
