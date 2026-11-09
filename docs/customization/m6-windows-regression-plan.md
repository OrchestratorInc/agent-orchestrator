# M6 Windows 用量与项目回归收口

日期：2026-11-08。状态：lint第二批交接已独立核对，派回同一执行会话串行接续；仍属于M6，未新增阶段。依据：requirements Q6/Q9、M6-01/02/06及旧完整Go检查的实际失败。统筹负责设计和独立验收，执行方自主选择实现与验证顺序。

## 目标与输入

补齐直接影响首版Windows项目导入、用量发现和费用展示的遗留回归证据。旧全量使用的用户目录隔离后来被确认不完整，因此先在现有新runner中重现准确目标，不能把旧结果一概视为环境问题或已修。

输入证据：C盘 `ao-m6-safe/m4-fast-backend-full-analysis.json`；302项源码集合摘要 `14a3d593849e30293a6bbad0e3772e271a4f53e61aa792ecd3db94a934f46256` 已独立核对，主仓HEAD仍M5。核心范围为 `backend/internal/observe/usage/`、`backend/internal/service/usage/`、`backend/internal/service/project/` 及实际涉及的pricing/文件身份边界。其他Adapter、无关平台功能及已停止的SV-M6-06不纳入本批。

## 工作项

| 项目 | 已见证据 | 需要交付的结果 |
|---|---|---|
| 随仓定价目录 | `TestEveryWriteRatedOpenAIModelPricesBothCodexPayloadShapes` 在加载目录时报anthropic摘要不符。统筹只读核验主仓/副本三个provider blob各有1个CRLF，仅转LF后三者摘要均精确匹配manifest | 区分检出字节、构建分发及运行时来源；保持原始摘要校验，明确可复现的Windows验证条件。不得重写manifest使损坏数据通过，不放宽生产校验，不擅改或提交Git/其他配置；若持久修复确需配置授权，记录准确变更和限制，其余工作继续 |
| Claude续接用量 | `TestPipelineIngestsDiscoveredClaudeContinuation` 未建立绑定；fixture直接拼接含Windows分隔符的workspace到JSON。service/usage旧失败另涉及默认roots进入真实USERPROFILE | 用新隔离复现；核对JSON和roots装配后修根因，验证真实Collector→Store→Ingestor的续接发现与幂等计量。保留旧会话与续接会话归属，不通过手写聚合值替代实际链路 |
| 文件替换与身份 | `TestIngestorReadsIdentityAndContentFromSingleDescriptorAcrossAtomicReplacement` 返回artifact_missing；legacy repair的replacement身份用例未得到不同身份 | 区分Windows句柄共享/原子替换与生产身份判定。保持同一文件描述符读取身份与正文，拒绝不可验证/错归属来源，不重复或漏记计量；不能通过忽略替换断言、只按路径当身份或无条件skip求绿 |
| 本地项目Clone及恢复 | 多项PrepareClone/Clone在到达目标分支前被INVALID_GIT_URL拒绝；fixture直接将Windows原生路径写入url.URL.Path。另有show-toplevel斜杠断言与失败清理时SQLite占用 | 先核对有效file URL与实际Git返回，使用真实临时仓库复现。保留外部helper/凭据URL拒绝，完成准备、注册、取消、超时、恢复及精确清理路径的回归；不改系统权限或真实Git配置 |

以上是失败观察与定位线索，不是全部根因已确认。已有用例优先补强；不为格式、配置或测试脚手架新增测试矩阵。生产逻辑确需修改时先核对所有调用点，沿现有边界最小修复；文件身份、外部协议及分支补必要说明。

## 执行和验收

1. 当前lint第二批实际退出并交接后才能启动本批；仍只有统筹和一个Astra执行会话，不增加子Agent。统筹不另起测试。
2. 复用C盘隔离副本、独立依赖和新runner；先验证本轮用户目录、定价输入和源码身份，保持三份真实配置监测、8GiB空间门禁与高I/O单命令。缓存/日志/数据/产物全部留C盘，不扫描或复制E盘旧.tmp。
3. 先跑准确失败范围取得当前证据，再修复真实问题，最后跑受影响完整包及行为修改必要的race；工具链、并发和有界预算沿用现有安排，不盲目重跑全仓。通过且未受影响的套件不重复。
4. 连续三次同问题失败停止该问题并报告判断；继续无依赖工作。不重试SV-M6-06、人工Hello、旧打包TLS或symlink权限阻断；不触碰真实Provider凭据、预算与数据。
5. 交接精确命令、退出码、失败/跳过原因、源码与输入身份、测试规模和逐项状态。更新执行报告、覆盖与故障文档；未通过项如实保留。统筹复核后再决定M6验收和提交，不自行提交、Push或进入下一阶段。
