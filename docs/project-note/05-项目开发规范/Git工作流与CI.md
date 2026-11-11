---
last_updated: 2026-11-09
source_files:
  - CONTRIBUTING.md
  - AGENTS.md
  - docs/development.md
  - .github/workflows
  - scripts/check-public-boundary.sh
kb_version: KB-v1.0
---

# Git 工作流与 CI

## 分支策略

- 从 `main` 开分支（除非明确继续已有 PR）。
- 分支命名惯例：`ao/<feature>` 或 `feat/<feature>`（示例见 `docs/development.md`）。
- 保持分支与 main 同步用 **rebase**，不用 merge：

```bash
git fetch origin && git rebase origin/main
```

## 提交规范

Conventional commits，标签：

| 标签 | 用途 |
|---|---|
| `feat` | 新功能 |
| `fix` | 缺陷修复 |
| `docs` | 仅文档 |
| `test` | 增改测试 |
| `refactor` | 无行为变化的重构 |
| `chore` | 维护、工具、依赖 |

- **原子提交**：一个逻辑变更一个 commit。
- 用 trailer 标注协作：`Signed-off-by:`、`Co-authored-by:`。
- 正文解释**为什么**，不重复 diff 的内容。

## PR 规则

1. **一个 issue 一个 PR**；需要独立工作就开新分支 + 新 PR。
2. 描述遵循 `.agents/skills/pr-description/SKILL.md`：开头是**已验证的分类变更计数**，含前后对比证据；UI 变更必须附截图/录屏。
3. 说明**刻意的省略**，特别是当 TypeScript 原实现比 Go 重写域支持得更多时。
4. 交付前跑完对应 CI 的**全部**验证任务（含完整套件）。无法本地运行的任务（原生 OS runner、Docker、凭据）必须**明确报告缺口**并在 CI 验证，**不得**声称本地已通过。
5. 合并前确认远程检查（本地通过 ≠ 远程通过）。
6. 新人/社区流程：先认领带 `bug` 标签的 issue；feature 需 core-team 批准后再开始。

## 生成物必须与源码同提交

| 改了什么 | 必须一起提交 |
|---|---|
| `dto.go` / `specgen/build.go` | `openapi.yaml` + `frontend/src/api/schema.ts`（`npm run api`） |
| `queries/*.sql` / 新迁移 | `backend/internal/storage/sqlite/gen/**`（`npm run sqlc`） |
| 已合并迁移 | **禁止修改**；新建迁移并追加到 `shippedMigrations` |
| Cloud 契约 | `packages/cloud-client/src/schema.ts`（`npm run cloud:check`） |

CI 的 `api-drift` / `sqlc-drift` 会重新生成并 `git diff`，不一致即失败。

## CI 任务全景（19 个 workflow）

| 类别 | workflow |
|---|---|
| 后端 | `go.yml`（gofmt/build/vet/test-race 分片、chat-race、session-store-race、sqlite-race、api-drift、sqlc-drift） |
| 前端 | `frontend.yml`（helper 原生构建、typecheck、vitest 4 片、E2E 渲染器子集、聚合门禁） |
| CLI | `cli-e2e.yml`（全新安装容器） |
| E2E | `e2e-gate-tests.yml` |
| 移动端 | `mobile.yml` |
| 边界/安全 | `public-boundary.yml`（禁止触碰私有控制平面）、`gitleaks.yml` |
| 契约/元数据 | `cloud-contract-compat.yml`、`pricing-catalog-validate.yml` |
| 发布/编译产物 | `build-artifacts.yml`、`harness-proof-build.yml`、`harness-release-feeds.yml`、`mac-update-e2e.yml`、`release-latest-guard.yml`、`version-floor-guard.yml`、`optional-private-submodule.yml`、`deploy-docs.yml`、`pr-review-leaderboard.yml`、`contributor-project.yml` |

## 发布（严格约束）

- **唯一发布者**：只有指定的 release conductor 执行真实 publish。多发布方曾导致 macOS 事故无法定位（`frontend/docs/desktop-release.md`「Hard rule: exactly one publisher」）。
- macOS 必须同时发布 `.zip` 与 `.dmg`：dmg 仅供首次安装，zip + `latest-mac.yml` 必须永久发布（electron-updater 无法从 dmg 安装更新）；macOS 差分更新永久禁用。
- macOS 产物验证**只能**用 `frontend/scripts/verify-mac-artifact.sh`（`ditto -x -k` 展开 + `codesign --verify --deep --strict` + `spctl -a -vv -t exec` + `xcrun stapler validate`）。裸 `unzip` 破坏封印，产生可信的假失败；`spctl` 不带 `-vv` 时成功也不打印。
- 桌面应用（GitHub Releases）是唯一推荐安装路径；npm 包 `@aoagents/ao` 已冻结在 0.10.0。

## 本分支的特殊流程（`docs/customization/delivery-status.md`）

当前工作位于上游 `23840aa` 之上的本地改造分支，采用**阶段门禁**流程：

- 阶段验收 → 本地提交 → 下一阶段。提交按确认清单逐个暂存，**不用** `git add -A`。
- 不提交：配置（`.codex/`、`.claude/`）、凭据、运行态（`.tmp/`、`backend/.tmp/`）、构建产物。
- 用户最新授权仅覆盖「开发检查点提交 + 合并到本地 `main`」，**不**包含 Push、目标分支 Merge、发布、真实 Provider 预算。
- 未验/门禁失败项不得标为 PASS；开发检查点提交需单独标识。

## 相关

- 命令矩阵与测试隔离 → 详见 [测试策略.md](测试策略.md)
- 编码与硬性禁止 → 详见 [编码规范.md](编码规范.md)
- 阶段状态与验收记录 → `docs/customization/delivery-status.md`、`docs/customization/m6-acceptance.md`
