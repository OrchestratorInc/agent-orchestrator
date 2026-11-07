# M6 稳定性、Windows 交付与发布准备任务

日期：2026-11-08。状态：按用户最新指令，M5 开发检查点本地提交后启动可自主执行的 M6 工作。M5 完整验收未通过，人工桌面步骤暂缓；不能据此发布。来源：[design.md](design.md) §13、[requirements.md](requirements.md) Q1～Q10、[M5 设计](m5-design.md)。

## 目标与完成判定

形成可复现的 Windows 候选交付，验证异常恢复、升级后数据/Session/Worktree 保留、审批与权限边界，给用户提供可操作的使用和故障处理文档。

区分三种结论：

1. 本地离线候选：Fake Provider、真实临时 Git/SQLite、隔离 Electron 与候选包验证。
2. 原生能力验收：四 Provider 的实际协议能力、Windows Hello 真人人工流程、真实安装/升级路径。
3. 正式发布：本地及适用 CI 门禁完成，获得发布授权，由指定唯一发布者执行。

第一项不能替代第二或第三项。用户于2026-11-08明确要求本人操作做不了时先跳过继续开发：Windows Hello、手动启动及其依赖的桌面步骤记为 NOT_TESTED/DEFERRED，先完成不依赖人工操作的开发，不再反复要求配合。M0-05预算、签名凭据和发布授权仍未提供；不能将M6标为正式发布PASS，不能伪造认证或放宽生产权限。

## 工作包

| ID | 交付结果 | 主要检查范围 | 验收证据 |
|---|---|---|---|
| M6-01 | 首版需求覆盖审计与候选基线 | Q1～Q10、M0～M5 报告、实际 Daemon/UI/Adapter 接线 | 每项映射源码/行为/测试，明确未交付能力和阻断级别 |
| M6-02 | 恢复、进程和多仓副作用验证 | `backend/internal/integration/`、现有生命周期/Session/Scheduler/质量服务 | 重启不重复 Spawn/Commit/Apply；UNKNOWN 保留、旧 epoch 拒绝、逐仓恢复结果 |
| M6-03 | 安全与人工审批验收 | `service/principal`、Operator HTTP/IPC、受限 Git/Runner、日志 | 旧批准/错目标/重放/越权失败，敏感值不进入日志，Windows 实际能力记录 |
| M6-04 | Windows 候选打包与隔离升级 | `frontend/scripts/`、现有 Electron Forge/更新器及打包说明 | 候选版本/hash、安装/启动/升级前后数据和进程检查 |
| M6-05 | 用户指南与故障恢复手册 | `docs/customization/user-guide.md`、`troubleshooting.md` | 创建/切换/干预/审批/预算/恢复步骤在候选中可重现 |
| M6-06 | 完整适用验证、回归修复及发布交接 | 仓库 CI、已知 Windows 基线、候选产物 | `m6-report.md`、需求矩阵、发布检查项和已知限制 |

执行会话使用 Astra；用户最新明确总并发为 2，本任务只保留统筹和一个执行会话，不派发子 Agent。具体实现和测试安排自主。建议完整阶段 high；文档/证据整理 medium，复杂恢复与审批问题 high 或更高。统筹负责接收结果、独立验收、派回缺陷和阶段提交。

## 安全执行条件

沿用 M5 磁盘事件后的约束：构建、测试、生成、依赖安装一次只运行一个命令，统筹与执行方共享这个执行窗口。新增缓存、日志、临时 Git/SQLite、依赖安装及候选产物放 C 盘专用隔离目录；仅用进程环境指定，不改受保护配置。复用已有合适 worktree；需要新的隔离副本时先核实位置、磁盘余量及排除规则，禁止复制旧 `.tmp`、缓存或共享 node_modules。

运行前后记录命令、真实进程、退出码和磁盘状态。取消工具请求不等于底层进程退出；持续挂起时先确认并停止准确进程，再核实文件读回，不能叠加重试。大日志只提取失败与末尾结论，保留原文件；测试超时须区分单用例等待与整个包的时间预算。`.git/info/exclude` 是本机措施，不能假定新工作树自动继承；验证时不得把未忽略的缓存带入 Git 快照或提交。

桌面启动前的资源采样不能代替启动后的正式 Scheduler 准入。M5 曾在启动前可用约 8.68 GiB 时满足门禁，启动后降至约 7.67 GiB 并返回 `RESOURCE_SOFT_LIMIT`；差值不能全部归因应用。后续需为启动保留余量，以实际 Scheduler 状态决定是否创建测试 Session；拒绝时清理本轮实例，等待明确环境变化，不降低阈值或根据退出后的回升循环启动。

## 优先推进与遗留项

1. 先修复已定位的离线集成装配失败并重跑完整受影响包：6项Session fixture缺可信准入，17项M2 fixture目录未初始化Git。保留生产准入与仓库校验。独立定位TestM4ArtifactThroughRealManager的5分钟超时；复核Domain Handoff与CLI symlink失败，区分可修代码和OS能力限制。
2. 接续完整build的下载阻塞：已有三次失败和Got/Undici对照证据，不重复原样打包。只有取得实际构建下载链可用的新证据后才再运行完整build；不改依赖/配置或关闭TLS、checksum来通过。自动工具拒绝的动作不换工具代跑；保留该项并继续其他工作。
3. 完成Q1～Q10源码/行为覆盖矩阵、恢复边界及用户文档，发现影响首版的真实缺口时最小修复并验证。执行方自行选择方法和顺序，统筹只验收结果。
4. SV-06桌面Queue编辑/取消/投递回显保留未验；已有正式HTTP/组件回归通过可复用，新fixture已准备。无需再启动桌面或索要Hello；人工暂缓不削减功能实现或离线回归要求。

用户最新授权是继续开发，不是整体PASS、发布或取消安全边界。阶段提交区分开发检查点与验收通过提交；交付报告须持续列出上述未通过项。

## M6-01：首版覆盖门禁

- [ ] 用实际源码和 UI 验证自动规划/任务申请、四 Adapter、三种干预、跨 Agent 协作、多仓、双视图、动态并发、用量/自定义 profile、受限执行和恢复。
- [ ] 自动 Planner、Stop & Replan、profile/effort 映射不能仅凭设计文档或测试 Fake 判为产品完成。缺实现时拆出范围明确的修复工作包；禁止静默降低首版要求。
- [ ] 核对两项历史 Handoff Note 失败及其他 Windows 已知失败。区分既有失败与新增回归；影响首版交付的历史问题也必须处理，不能只因“基线已红”自动豁免。
- [ ] 接续 M5 的具体集成分诊：干净 M4 已复现 23 项断言失败，其中旧 Session fixture 缺可信准入提供器，旧 M2 fixture 使用未初始化 Git 的项目目录，触发路径预留 `invalid delivery artifact`。修复应补齐正式测试装配，不能放宽生产准入或仓库校验；重跑受影响完整包。`TestM4ArtifactThroughRealManager` 超时独立保留，不能据此推定它或当时未执行的后半段通过。原始逐项对照位于 M5 证据目录 `integration-triage-comparison.json`。
- [ ] 锁定 M5 提交 SHA、工具链、依赖、适用工作流命令及候选验收范围。

## M6-02/03：异常与权限验收

复用真实 SQLite/Manager 装配、临时 Git、多进程测试设施，以可观察业务行为注入异常，不新增通用故障框架。

- Claim/Session 接受/消息发送前后中断，恢复时幂等与旧代际 fencing 有证据。
- Agent 无输出但仍存活、探测未知和 daemon 重启不得被等同为死亡；保留不确定占位。
- 工件保存、Gate 结束、Commit/Apply 写 ref 与持久回执之间中断，按 Git 和耐久事实对账，不能自动重做不明副作用。
- 多仓一个成功一个冲突，整体保持 PARTIAL/UNKNOWN；不破坏已成功仓、dirty Worktree 或用户 index。
- 受限 Runner 的超时/进程树回收、输出限制、环境/路径权限与日志脱敏有真实 Windows 证据。
- 原生审批拒绝、取消、过期、重放、目标漂移和不可信 IPC 来源均失败闭锁；真人 Windows Hello 结果单独记录。
- 证据不能证明 OS 级沙箱时如实说明，并验证实际允许/禁止路径，不宣传提示词构成系统隔离。

## M6-04：安装与升级

仅在隔离工作树、scratch AO_DATA_DIR 和专用测试安装位置执行，保留用户现有安装及数据。复用当前 Forge 构建链，不另建发布系统、不运行 publish 作为测试。

隔离前置：当前 `frontend/src/main.ts` 在 `app.isPackaged` 时把 Electron `userData` 固定到 `os.homedir()/.ao/electron`，只有开发版读取 `AO_DEV_ELECTRON_DIR`。单独设置 `AO_DATA_DIR`、更换安装目录或开发 profile 不足以隔离打包版。执行前必须核实候选实际所有写入位置，使用独立测试账户/环境或经验证的隔离方式；未具备该条件时先完成构建和静态候选检查，安装/升级运行保持 BLOCKED，不能启动候选触碰真实 profile。不要为通过验收擅改此产品边界。

```text
# frontend，具体 maker 由当前 Windows 配置核实
rtk npm ci
rtk npm run typecheck
rtk npm run build
rtk npm run make
```

检查：候选内 Daemon/资源路径可用；首次启动仅写独立 AO 数据目录；关闭及重启无遗留隐藏进程；从实际支持的旧版本升级后 Workflow、Session、消息、审批事实、预算与 Worktree 归属保留；更新失败保留可恢复状态。数据库迁移成功不自动代表可安全降级，不承诺未经验证的回滚。

使用测试更新源或仓库已有隔离更新设施，禁止把试验版本发布到生产 feed。打包配置如确需更改或提交，由执行方先说明必要性和差异，统筹取得用户明确授权后处理；没有签名凭据就记录未签名候选，不伪称签名通过。

## M6-05/06：完整检查与交接

按当前 `.github/workflows/` 使用相同工具链和适用命令。发布准备阶段运行完整适用套件；其他 OS、Docker 或凭据不可用时明确记录具体 job 缺口，不将未运行项记 PASS。

```text
# backend
rtk go build ./...
rtk go test ./...
rtk go test -race ./...
rtk go vet ./...

# 仓库根与 frontend 的相应目录
rtk npm run lint
rtk npm run frontend:typecheck
rtk npm test
rtk npm run test:e2e
```

测试与脚手架遵守 TESTING.md；已经通过的套件仅在新增变化/失败/未解决问题影响时重跑。不发布、不改真实 Provider 配置、不提交构建产物或运行态。

交付 `m6-report.md`：需求覆盖、候选 SHA/hash、实际命令/退出码、升级/恢复记录、日志和截图位置、尚需用户参与/授权的精确项。统筹独立编写 `m6-acceptance.md`。仅当定义的阶段验收要求满足才本地提交并记录 SHA；授权缺失导致未验的完整发布门禁不能通过改名或删项绕过。
