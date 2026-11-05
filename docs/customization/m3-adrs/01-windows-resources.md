# M3 ADR-01：复用 Windows 资源采样并保守准入

日期：2026-11-05。状态：M3-01 设计冻结；尚未实施，不解除 G0。

## 事实

- `backend/internal/procmem/system_windows.go:50` 已通过 `GlobalMemoryStatusEx` 与 `GetSystemTimes` 读取系统 RAM 和 CPU ticks；无需新增依赖或第二套 Windows API 实现。
- `service/usage/memory.go:159` 已提供缓存、并发合并与差分 CPU。其显示模型没有原始采样时间；Windows CPU 调用失败可留下零 ticks，显示层还可能回退到进程采样，因此不能把一次成功返回等同于可信空闲。
- 本轮 `go test ./internal/procmem ./internal/pricing -count=1 -json -timeout 5m` 中procmem为23 PASS；包含真实Windows系统读取/两次CPU tick增长检查。另有MemoryReader定向6 PASS。完整组合命令因Pricing两项失败而exit 1。

## 决策

1. M3 的薄 `ResourceSampler` 复用 `procmem.ReadSystem`；保留原始采样时间、有效性、错误原因和连续两次 ticks。不得复制采集实现或改变既有显示语义。
2. CPU 首样、间隔非正、计数回退、总量不增长、busy 增量超过 total、CPU 调用失败或样本超过 15s 均 UNKNOWN。RAM 必须满足 `0 < total`、`0 <= available <= total`。系统休眠/时钟跳变后重新建立基线。
3. 每 5s 采样，15s 保底调度；CPU ≥75% 降低新增速度，≥85% 不新增；RAM <25% 降速，低于 `max(15% 总 RAM, 2GiB)` 不新增。只限制新增，不强杀已有 Session。
4. 恢复条件为 CPU <65%、RAM >25%，连续三个有效样本后每次最多增加一个有效槽。UNKNOWN 选择设计允许的更保守值：新增容量为 0，不释放既有占位。
5. 全局默认 3、可调 1–10、不可越过 10；Adapter 默认 2、可调 1–10且受全局限制；每轮最多检查100个候选、批准3个新执行；Workflow权重1–5。并发降低不终止正在执行的任务。
6. 阈值修改仅经 Operator policy revision CAS；CPU 必须 `0 < recover < soft < hard <= 100`，RAM 必须 `0 <= hardPercent < softPercent < 100`、字节下限非负。采样/扫描周期与批量上限在 V0.1 固定，不额外增加配置项。
7. 将 Session、已预留未绑定 Attempt、UNKNOWN 占位按稳定身份合并去重；无法完整观测时不得据缺失进程推断空槽。现有手动 Session 也计槽，新增手动启动必须接入同一资源预留关口，否则只能宣称 Scheduler 限额，不能宣称 AO 全局限额。

## 验收要求与限制

M3-03 用 FakeClock/FakeResource 验证首样/过期/失败、75/85边界、RAM硬线、滞回、3/10及第11个拒绝。真实 API 可读不证明这些调度行为已实现；本轮没有运行调度器，也没有运行 Provider。
