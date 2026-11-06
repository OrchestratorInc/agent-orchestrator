# M4 ADR：可信 GatePolicy 与受限 Runner

状态：实施约束已确定，功能验收另见阶段台账。日期：2026-11-06。

GatePolicy 是服务端固定、版本化的规则集合。外部请求只选择已批准策略 ID，不接受 Agent 提供的 executable/argv/env。每条规则明确绝对 executable、argv、cwd 范围、允许环境、超时、日志字节上限、触发风险及 OS 条件。

复用 `internal/process.CommandContext` 与 `ConfigureTreeCancellation`，不新增进程调度器。拒绝 shell、cmd/bat/PowerShell 包装和未经审核的仓内脚本。策略必须说明实际可执行文件的信任来源；仅把字符串放入 allowlist 不构成脚本审核。所有临时状态在隔离 AO_DATA_DIR 下，不传 Provider 凭据。

GateFingerprint 绑定完整 Artifact、policy revision、实际 executable 内容、argv、环境值摘要、OS/arch、依赖锁文件及交付树。每仓单独落证据，聚合必须覆盖全部 Repo/必需规则；零规则、缺工具、超时、非零退出、异常 SKIP、输出伪 PASS 均不能通过。

退出码由本地进程 wait 得到，模型文本不是退出码。日志有界保留并对完整流取 digest；不能只哈希截断片段。取消无法确认进程树终止时 UNVERIFIED，不能释放相关占位。超时/取消的用例使用本地测试辅助进程，不调用 Provider。

长运行回执绑定开始时 ArtifactRevision、Generation、Scheduler Epoch、policy 与运行 ID。续租不改变业务身份，但换代/新任期、工件或策略漂移必须拒绝回执；没有完整停止证明，不将读快照变成副作用权限。

接口：质量服务载入可信工件/策略，调用 `Runner.Run(ctx, rule, workspace)` 得到状态、退出码、时间、日志摘要，再经 writer 事务 CAS 保存证据。事务不跨越子进程运行；副作用前后各做身份校验。只允许相同完整 fingerprint 的有效证据复用。

验证要求：真实本地 exit 0/非零/超时、缺工具、伪 PASS、缓存指纹漂移、日志截断、环境泄露和未批准 argv 拒绝。删除退出码判断或 fingerprint 比较必须使相应用例变红。
