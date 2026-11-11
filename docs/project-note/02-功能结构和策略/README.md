---
last_updated: 2026-11-09
kb_version: KB-v1.0
---

# 02 · 功能结构和策略（索引）

模块清单、可复用模式、历史决策。

| 文档 | 一句话 | 什么时候读 |
|---|---|---|
| [核心功能模块.md](核心功能模块.md) | 12 个模块的职责/边界/交互矩阵；worker·Chat·reviewer 三张独立注册表 | 找「这个功能在哪」；加 harness/reviewer |
| [设计模式与策略.md](设计模式与策略.md) | 14 个模式，每条带代码位置与「为什么必须这样」 | 写新代码前；review 架构合理性 |
| [关键设计决策.md](关键设计决策.md) | 16 条决策 + 被否方案 + 代价 | 想改边界时先看是否已被权衡过 |

## 最容易被误判的三件事

1. **注册表不互相扩展**：reviewer 集是独立集合，加 reviewer 不会扩大 worker 的 `AgentHarness` 词表；Cursor 有 Chat adapter ≠ 支持跨接口历史交接。
2. **Chat 会话没有 agent 运行时**：它的 shell 终端是独立资源，不成为控制器。
3. **未知态是正式状态**：`spawn_unknown` / `stop_unknown` / `recovery_uncertain` 闭锁流程，不能当失败重试，也不能当成功继续。

## 相关上游

`docs/architecture.md`（不变式与图）、`docs/stack.md`（选型）、`docs/adr/`（7 篇 ADR）、`docs/customization/design.md`（多 Agent 设计 V0.1）。
