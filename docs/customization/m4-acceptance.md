# M4 主 Agent 独立验收（主控通过，待统筹终验）

2026-11-06。主 Agent G4 结论：**HANDOFF_READY / 主控独立验收通过**。M4-01～08 已按依赖完成并通过主控阶段验收，M4-09 综合验证通过；整体最终 ACCEPTED 仍由统筹独立确认。本文件不把执行方 HANDOFF 直接升级为统筹验收。

## 主控最终结论

- 真实产品链已覆盖 CLI→Operator Approval→HTTP→Artifact/Gate/Review/Task Commit→Integration Preview/Apply→SQLite/Git/CDC；正式 CLI 链退出码 0。
- M4 全量集成 `go test -race ./internal/integration -run "^TestM4" -count=1 -timeout 30m -v` 退出码 0，耗时 874.484s；两条真实 Manager/SQLite 链和安全反例均通过。
- 当前策略读回、CAS revision 更新/旧版本拒绝、Artifact Generation 隐藏后的持久化恢复、Apply reconcile writer 内 CAS、PR 双向 Workflow 归属闭锁均有行为证据。
- `GitArtifact.files` 的合法 null 表示已纳入 OpenAPI 生成契约；新增红绿测试、`npm run api`、`go test ./internal/httpd/apispec/...` 和前端类型检查均退出码 0。
- 修复链验证到受限物化和 scope 保持；测试模拟作者修改工件内容，没有真实 Agent 自动修复执行证据。

## 主控保留的阻断/限制

- 统筹 SV-02 的 Windows workspace-write 沙箱内 Preview 清理失败仍保留；完全访问环境全新临时目录复跑退出码 0，但原始失败根因未唯一归因。
- 全量 `internal/cli` 测试被既有 Windows symlink privilege 失败阻断；M4 定向 CLI 测试和 race 通过。
- 两项历史 Domain Handoff Note 测试失败；它们不是本轮 M4 新增回归。
- 真实 Provider、生产数据、远程 Git transport、远程 Push/Merge、部署、跨 OS、桌面/安装包和原生人工审批未验证；M0-05 未批准。

以下原验收清单和阶段记录保留为历史审查依据；统筹终验仍以当前源码、日志退出码和本节限制为准。

01/02 证据见 `m4-architecture-review.md`、`m4-report.md` 和执行台账。03/04 的服务/Store/真实 Git 装配与 race 由主 Agent 亲自运行；完整命令、耗时、退出码及失败记录见实施报告。

03/04 安全反例覆盖：非零退出输出假 PASS、超时/工具缺失、凭据不继承、日志截断但完整摘要、策略与 argv 漂移、SV-01 路径规范和组件边界、冻结树修改、无 Gate/Review 不派发后继、独立 Review 可启动、作者自审拒绝、Reviewer 旧 Session generation/epoch 拒绝、旧 Artifact 失效、两轮修复上限与同版本去重、满消息队列幂等、两个 SQLite 连接不能重复执行同一工件 Gate。

05 主控验收：持久唯一意图先于 Git 写，任务 ref 与目标 SHA 在一个 Git ref 事务中校验；最终 ref CAS 持有 SQLite writer 并复验 epoch/generation/证据，以租约和 5 秒上限取消。保留用户索引及范围外 dirty、禁用 hooks、签名要求无法满足则拒绝；丢回执后仅对账、不再次写 Git。验证不存在的意图不会创建记录，迟到 Review 不能改变待确认提交证据。Store race、真实 Git 与 Manager 全链最新实跑 exit 0，命令及耗时见报告。SQLite/Git 不是跨系统原子事务，也不承诺隔离同用户外部 Git 进程。

06 主控验收：完整两仓 Gate/Review/Commit→Preview 实跑通过，目标 ref 不变；缺仓、相同 ID 改仓、伪造摘要、第二仓验证失败事务回滚、双 Store 重放、重开 SQLite 恢复均通过。真实 Git 无冲突/冲突、dirty 漂移与临时 Worktree 移除通过；SQLC、迁移往返/登记和受影响 vet exit 0。预演不等于集成，目标变化仍须重建有效证据。

07 主控验收：真实两仓 Apply、双 Operator、准确正文与独立 Push 审批、目标检出拒绝、部分失败/丢回执/重启仅对账、本地 bare Push/远端 SHA 并发漂移均有实跑 exit 0；Store/审批/迁移 race、新 Leader 接管反例通过。partial/unknown 默认闭锁后续写入，未实现自动续跑失败仓。网络 Push 默认禁用，本地模拟不代表网络 transport 验证。

08/09 已补齐 HTTP/CLI/CDC/协调摘要、完整链路 G4 和 M5 技术交接。正式 CLI 链 628.577s、完整 M4 race 874.484s 均 exit 0；受影响 M2/M3 五项回归 261.039s exit 0，Build/Vet/SQLC/API/TS 均 exit 0，完整命令和日志位置见 [实施报告](m4-report.md)。统筹 Preview 夹具清理 FAIL 原因尚未唯一确定，但沙箱外独立复跑 47.181s exit 0 且临时根清空，不覆盖原 FAIL。剩余事项为统筹终验及上方未验证范围，不声称全仓或全产品 PASS。
