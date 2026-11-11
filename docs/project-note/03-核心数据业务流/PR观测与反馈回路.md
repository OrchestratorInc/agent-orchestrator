---
last_updated: 2026-11-09
source_files:
  - backend/internal/observe/scm/observer.go
  - backend/internal/lifecycle/reactions.go
  - backend/internal/adapters/scm/github
  - backend/internal/adapters/scm/gitlab
  - backend/internal/service/pr
kb_version: KB-v1.0
---

# PR 观测与反馈回路

agent 提交 PR 之后，AO 如何发现它、把 CI 失败/评审意见/冲突变成对 agent 的 nudge，并把结果反映到看板。

## 触发点

| 触发 | 来源 | 频率 |
|---|---|---|
| 周期轮询 | `Observer.Start` → `Poll` | 30s（review thread 另有更长间隔） |
| 强制刷新 | `RequestReviewRefresh(prURL)` | 审查完成后按需 |
| 用户操作 | `POST /prs/{id}/merge`、`/prs/{id}/resolve-comments` | 手动 |
| 会话声明 PR | `claim-pr` / PR 事实写入 | 事件驱动 |

## 数据流路径

```
Observer.Poll (30s)
 ├ 分支事实（本地 git 读，先于凭据门禁）→ ReconcileBranchStates
 ├ discoverSubjects → 活跃会话 + 其仓库
 ├ checkCredentials                       无凭据 → 进入 disabled 模式（不是错误）
 ├ discoverNewPRs（ETag 条件请求 + 增量游标，含 5min 重叠防时钟偏移漏单）
 ├ 逐个 PR 拉详情：checks / review threads / comments / mergeability
 │    速率限制：优先 Retry-After，其次 ResetAt，兜底 60s±15s，钳制在 30s–10min
 ├ 语义 diff（未变则不写库、不发事件）
 └ 写 pr / pr_checks / pr_review_threads / pr_comments 事实
        ↓
lifecycle.ApplyPRObservation / ApplySCMObservation
 ├ 门禁 cannotNudge：已终止 / NeedsInput（waiting_input|blocked）/ exited → 不打扰
 ├ 生成 nudge（去重键：comment id、merge-conflict:<prURL> 等）
 │    └ 注入 agent：TUI 写终端，Chat 走队列/continuation
 └ 更新通知意图（needs_input / ready_to_merge / pr_merged / pr_closed_unmerged）
        ↓
DB trigger → change_log → SSE → 看板与 PR 摘要
        ↓
service/session.deriveStatus → PR pipeline 状态
```

## PR pipeline 状态

```
ci_failed → ci_pending → (passing) → changes_requested | approved
approved → merge_conflict | mergeable
draft 独立一档
merged / closed 终态（优先于任何 merge readiness 读数）
```

多 PR 会话：**最差者胜**；堆叠子 PR 从聚合中豁免，父 PR 开放时子 PR 不覆盖父状态。

## 关键节点详解

| 节点 | 位置 | 核心逻辑 | 异常处理 |
|---|---|---|---|
| 凭据门禁 | `observer.go:445` | 无凭据 → disabled，不报错 | 静默停轮询，恢复后自动继续 |
| 增量游标重叠 | `observer.go:139` | `incrementalDiscoveryOverlap = 5min` | 防时钟偏差/`updated_at` 粒度漏单 |
| 速率限制 | `observer.go:107-160` | `GetRetryAfter` → `GetResetAt` → 60s±15s；钳 30s–10min | 抖动用固定时钟可复现 |
| 慢轮询告警 | `observer.go:421` | 单轮耗时 ≥ tick 间隔 → Warn | 不中止，只观测 |
| 语义 diff | `observer.go` Poll 内 | 事实未变则不写库/不发事件 | 避免 CDC 风暴 |
| nudge 门禁 | `reactions.go:703` | `IsTerminated \|\| 状态 NeedsInput \|\| exited` → 不打扰 | blocked 会话**禁止**注入输入 |
| nudge 去重 | `reactions.go:338/348` | comment id、`merge-conflict:<prURL>` | 重复评论不重复推送 |
| PR 身份渲染 | `reactions.go:735` | `PR #123 "Title" (feat/x → main)`，标题与分支做控制字符清洗 | 多 PR 会话里 agent 才能分辨是哪条 |
| 自动修复标记 | `pkg/contract/kanban.go:328-357` | `AutoInjectCI`/`AutoInjectReview` 只在 agent **当前 active** 时才显示「AO 正在修」 | 陈旧标记落到普通读数 |

## 反馈类型

| 事实 | nudge / 动作 |
|---|---|
| CI 失败 | 推送失败的 check 名与链接到 agent，请其修复 |
| 评审要求修改 | 推送评论摘要（含人工 reviewer ID/链接） |
| 合并冲突 | 推送冲突提示；冲突清除时重新武装（`rearmMergeConflict`） |
| 全部 PR 已合并 | 若 `TerminateOnPRMerge` → 触发 `terminateCompletedSession` |
| 需要人工决定 | 持久化 `needs_input` 通知 |

## 通知契约

四类耐久通知：`needs_input`、`ready_to_merge`、`pr_merged`、`pr_closed_unmerged`。
桌面只呈现一个最新优先的流；打开即确认已加载的未读项；只有「清除单条 / 全部清除」，**没有**未读/全部分页标签或逐条标记已读按钮。

## 异常路径

| 异常 | 处理 |
|---|---|
| 无 GitHub/GitLab 凭据 | observer 进入 disabled，不报错，不刷屏 |
| 触发速率限制 | 按 provider 提示休眠并抖动，不反复冲击 API |
| 轮询慢于 30s | Warn 记录，仍继续 |
| 事实未变 | 不写库、不发事件（避免无效 CDC） |
| 会话阻塞待输入 | 不 nudge（避免自动化向 blocked 会话注入） |
| PR 被关闭未合并 | 终态事实 + `pr_closed_unmerged` 通知 |

## 关联组件

- 端口：`ports.SCM`、`ports.PRObservations`、`ports.SCMObservations`
- 实现：`adapters/scm/{github,gitlab,multi}`
- 服务：`service/pr`（ActionManager）、`lifecycle`（nudge/通知意图）
- 持久化：`pull_requests`、`pr_checks`、`pr_review_threads`、`pr_comments`、`notifications`

## 相关业务流

→ 详见 [会话生命周期与终止判定.md](会话生命周期与终止判定.md)
→ 详见 [Chat对话与turn生命周期.md](Chat对话与turn生命周期.md)
→ 详见 [工作流编排与执行绑定.md](工作流编排与执行绑定.md)
