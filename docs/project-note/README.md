---
last_updated: 2026-11-09
kb_version: KB-v1.0
project_version: 0.13.0
build_mode: full
preprocessors:
  codegraph: pass
  understand_anything: unavailable
---

# 项目知识库 · Agent Orchestrator

Go daemon + Electron/React 桌面端 + Expo 移动端 + `ao` CLI 的多 Agent 编排平台。本知识库是下游 Skill（`feat-design-planning` → `deep-planning-rigorous-validation` → `fix-bug-workflow`）的共享底座。

## 六大分类

| 分类 | 一句话 |
|---|---|
| [01-架构总览](01-架构总览/README.md) | 一个 daemon + 三个瘦客户端；五层单向依赖；CDC 是唯一事件源 |
| [02-功能结构和策略](02-功能结构和策略/README.md) | 12 个模块、14 个设计模式、16 条关键决策 |
| [03-核心数据业务流](03-核心数据业务流/README.md) ⭐ | 8 条核心流程：创建、终端、Chat、接口切换、生命周期、PR、工作流、CDC |
| [04-数据与组件设施](04-数据与组件设施/README.md) | 14 个 Zustand store、Electron 三层、366 个生产路由、109 张表 |
| [05-项目开发规范](05-项目开发规范/README.md) | 编码红线、测试策略、CI 与发布约束 |
| [06-迭代与变更](06-迭代与变更/README.md) | 版本线、20 条实测陷阱、知识库变更日志 |

## 我想做 X → 去哪里看

| 我想做什么 | 去哪里看 |
|---|---|
| 理解项目整体架构 | `01-架构总览/系统分层架构.md` |
| 把代码放到正确的层 | `01-架构总览/系统分层架构.md` + `05-项目开发规范/编码规范.md` |
| 本地把项目跑起来 | `01-架构总览/部署与运行架构.md` |
| 加/改一个 API 路由 | `04-数据与组件设施/服务层与IPC通信.md` → `05-项目开发规范/Git工作流与CI.md`（生成物同提交） |
| 改会话创建流程 | `03-核心数据业务流/会话创建全链路.md` |
| 改会话终止/状态判定 | `03-核心数据业务流/会话生命周期与终止判定.md` |
| 加一个新 agent harness | `02-功能结构和策略/核心功能模块.md`（三张注册表）→ `05-项目开发规范/编码规范.md`（hook gitignore） |
| 加/改 Zustand store 或前端状态 | `04-数据与组件设施/Store状态管理全景.md` |
| 加 IPC 或改 Electron 能力 | `04-数据与组件设施/组件架构与通信.md` |
| 改 Chat 消息/turn | `03-核心数据业务流/Chat对话与turn生命周期.md` |
| 做 TUI↔Chat 切换 | `03-核心数据业务流/接口切换.md` |
| 改 PR 观测或 nudge | `03-核心数据业务流/PR观测与反馈回路.md` |
| 改工作流/调度/预算 | `03-核心数据业务流/工作流编排与执行绑定.md` |
| 排查「状态不对/不刷新」 | `03-核心数据业务流/CDC与实时事件推送.md` + `06-迭代与变更/已知问题与陷阱.md` |
| 写测试 / 修 CI | `05-项目开发规范/测试策略.md` |
| 排查历史类似 Bug | `06-迭代与变更/已知问题与陷阱.md` |
| 判断某能力是否真存在 | `06-迭代与变更/版本历史.md` |
| 了解某边界为何这样设计 | `02-功能结构和策略/关键设计决策.md` + `docs/adr/` |

## 给 AI 的阅读指南

1. **按需逐层深入**：先读本页 → 读分类索引（L2）→ 只展开需要的 L3。不要一次性加载全部 L3。
2. **冲突时的优先级**：生成的契约层（`openapi.yaml`、`sqlite/gen/`、`frontend/src/api/schema.ts`）> 代码与测试 > 本知识库 > 其他 prose。依据见 `docs/documentation-map.md`。
3. **上游权威文档**：`docs/architecture.md`（最完整）、`docs/stack.md`、`docs/backend-code-structure.md`、`docs/customization/design.md`、`docs/adr/`（7 篇）。本知识库是分层摘要与事实索引。
4. **不确定就查代码**：每条事实都带文件路径或行号，可直接核对。

## 三个不变式（改动前的底线）

1. **展示状态永不落库** —— 读时由 `service/session/status.go:19 deriveStatus` 派生。
2. **每个会话恰好一个已提交控制器** —— `tui` 或 `chat`，只有接口切换协调器能改。
3. **事件只从 CDC 出** —— DB 触发器 → `change_log` → poller → broadcaster。

## 版本标记

```
构建时间:     2026-11-09 17:27 (Asia/Shanghai)
项目版本:     v0.13.0 (frontend/package.json) / 上游基线 23840aa
知识库版本:   KB-v1.0
代码基线:     feat/multi-agent-desktop @ 0640ca9
索引规模:     5365 文件 / 110,865 节点 / 276,705 边
下次建议更新: 2026-11-23 (构建时间 + 14 天)
```

### 构建门禁

| 阶段 | 结果 |
|---|---|
| Phase 0 环境检测 | PASS（full 模式） |
| Phase 1A codegraph | PASS |
| Phase 1B understand-anything | **不可用**（plugin 未安装）→ Phase 1 降级为结构图谱 + 手动扫描 |
| Phase 1 综合扫描 | PASS（8 条业务流） |
| Phase 2–6 | PASS |
| Phase 7 零重复 + 版本标记 | PASS |

降级说明与局限详见 [06-迭代与变更/变更日志.md](06-迭代与变更/变更日志.md)。
