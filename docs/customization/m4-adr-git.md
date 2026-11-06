# M4 ADR：可信 Git 工件与隔离集成

状态：实施约束已确定，功能验收另见阶段台账。日期：2026-11-06。

## 决定

1. 复用 SessionManager 的 Project/Repo/Worktree 绑定和实时观察。Artifact 额外检查注册项目 root、物理 common-dir、task branch、base/creation/head 祖先关系。相同 SHA 的另一仓库不等于同一 Repo。RepoKey 只是 Workflow 内的名字。
2. Snapshot 从完整 NUL 分隔 Git 清单和文件字节生成，记录 HEAD、索引和工作区差异、未追踪文件、AllowedPaths、实际 common-dir、环境及依赖指纹。拒绝路径逃逸、symlink、ADS、submodule 和未审核 Git 配置。路径按组件比较；Windows 大小写折叠。大小/数量超过上限直接 BLOCKED，不截断后签可信指纹。
3. 任务提交只覆盖服务端清单。使用私有临时 index 和 `hash-object --no-filters`、`update-index --cacheinfo`、`write-tree`；用固定作者/工件时间的 commit 字节经 `hash-object -t commit` 生成确定对象，Git ref 事务同时 verify 目标 SHA 和 CAS 任务分支。保留原 index 和范围外 dirty。禁止 add -A/reset/clean/stash。提交后原 index 可能显示 HEAD 差异，向使用者明确展示，不偷偷覆盖。
4. 禁止受审工件调用 hooks、filters、external diff/merge driver 或自选签名程序。命令使用固定 argv、隔离 Git 配置和允许的环境；签名策略要求签名而无可信实现时 BLOCKED。作者由受信策略提供，不改全局配置。
5. Gate 在服务端导出的交付树运行；如果范围外 dirty 是测试成功的必要条件，不得为只提交范围内文件签 PASS。冻结和操作前后重复观察，漂移拒绝。Worktree 不是 OS 沙箱。
6. Preview 在 AO_DATA_DIR 下的专用 detached Integration Worktree，对固定 target SHA 和 task commit 执行 `merge-tree --write-tree`，不暂存/checkout 合并结果。通过 Git plumbing 生成确定候选对象，预演不更新目标 ref，结束用非 force worktree remove 清理；无法安全清理保留目录并返回错误。目标分支在任意 Worktree 被检出时拒绝自动 apply。
7. 多仓提交/集成分别记录 operationID 和期望/观察 SHA。Git 副作用前持久 requested，之后 CAS 记录结果；中断留下 UNKNOWN，只核对 ref/parent/tree，不能重发提交。

## 源码依据与接口

`session_manager/multi_repo_workspace.go` 的 `verifyRepoWorktreeRepo/History` 是既有身份验证；`gitworktree.ObserveWorkspace` 是展示摘要，不作为完整工件清单。新增受限 Snapshot/Commit/Preview 原语与旧 Workspace 适配器并存，不能用旧摘要替代指纹。

内部操作接受服务端解析的 Repo 绑定与不可变工件，HTTP 不接受原始路径、任意 SHA 或 Shell。返回 Snapshot、CommitArtifact、逐 Repo Preview；所有 SHA/digest 来自 Git/文件读取。

验证要求：两个真实临时仓库、换库/分支漂移、索引和范围外 dirty 保全、路径逃逸、hook 不执行、重复 commit、target 已检出、冲突和 target CAS。
