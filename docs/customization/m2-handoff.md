# M2 提交与后续验收留档

日期：2026-11-05。分支：`feat/multi-agent-desktop`。

用户决定：先提交当前 M2 实现，将“第六轮离线独立复验”和“真实 Agent 完整验收”两项留档，后续再执行。本次提交不代表这两项已通过，不改变第五轮独立结论 **NOT ACCEPTED**，不授权启动 M3、真实 Provider、远程推送或部署。

用户另行明确批准本次一并提交 `backend/sqlc.yaml`（M2 字段类型映射）和 `.gitattributes`（两个脚本的 LF 规则）；该批准不延伸至其他配置文件。

## 当前交付

- M2 Session/Attempt 绑定、能力门禁、Queue/Steer/Stop、协作消息、正式 Operator CLI/IPC、SQLite/API/CDC，以及单仓/多仓 Worktree 实时 Git 验证和 Fail-Closed。
- F-01/F-02 保持离线回归通过；F-03 及随后暴露的 Windows、权限、并发、平台夹具问题已修复。
- 提示词清单内回归通过：M2 普通/race 各 33 项、前端 633 文件与 10877 项（17 项原有跳过）、指定后端完整包及新增影响包、Build/Vet/Typecheck、API/sqlc 生成稳定。
- 真实命令、退出码、失败历史、平台限制和测试规模申报见 [完整回归修复记录](m2-regression-repair.md)。第五轮修复详情见 [F-03 修复报告](m2-remediation-round5.md)。

## 待办一：第六轮离线独立复验

**状态：用户决定暂缓，待独立验收；不能标记为 PASS。**

恢复入口：用户要求继续离线验收时，由独立验收者针对提交后的实际代码复验。

必须确认：

1. F-03 原始负例已逆转；真实单仓 Git/SQLite/HTTP 的正常、缺行、缺目录、归属错误、分支/HEAD 漂移均符合门禁，且 dirty 成果保留。
2. Bind/Queue/Steer/Resume/Reconcile 及实际消息出队重新验证工作区，Legacy 会话兼容，多仓历史 BaseSHA 和 F-02 正式授权链不退化。
3. 重跑任务要求的 M2 普通/race、目标完整包、Build/Vet/Typecheck、API/sqlc 及前端全套，记录真实退出码；不得把历史通过结果冒充本轮独立实跑。
4. 独立报告逐项关闭缺陷，明确所有未验范围。若发现新失败，先修复并复验，再决定离线门禁结论。

完成证据：新的第六轮独立验收报告；历史第五轮报告保持不变。

## 待办二：真实 Agent 完整验收

**状态：用户决定暂缓；M0-05 预算与具体环境未批准，保持 BLOCKED / NOT_TESTED。**

恢复前提：另行批准预算、真实 Agent/模型、受控 Repo 和测试环境；此次 Git 提交授权不包含这些操作。

按 [M2 设计 §10.2](m2-design.md#102-m2-真实-agent-完整验收门禁) 执行：

1. 至少两种不同真实 Agent 产生可观察的 Session/Attempt/Chat/Worktree 事实。
2. 四首发 Harness 的支持、拒绝、未知能力均有证据及降级路径，不把 Fake profile 当作真实能力。
3. 在支持的 Harness 上实跑 Queue/Steer/Stop/Resume、受控审批拒绝、失败/重启恢复，确认不产生重复副作用。
4. 如实记录实际或有依据估算的使用量，未知 Token 用量不得填 0；补最终环境的原生 Windows Hello 人工正向核验。

完成证据：独立真实运行报告、能力矩阵、审批与恢复记录、使用量明细。仅离线 PASS 不构成完整 M2 PASS。

## 保留边界

全仓 CI、全仓 `go test ./...`、其他原生操作系统、真实安装/签名/发布及原生 Windows 文件 symlink 仍未完整验证。junction/MSYS 链接证据不能替代原生文件 symlink。Windows 进程身份绑定依赖已核验的 Go 1.27.1 句柄生命周期，工具链升级时需要复核。

本记录只留档后续验收，不新增验收结论，不启动两项待办。
