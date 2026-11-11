---
last_updated: 2026-11-09
kb_version: KB-v1.0
---

# 05 · 项目开发规范（索引）

改代码时必须遵守的约束。

| 文档 | 一句话 | 什么时候读 |
|---|---|---|
| [编码规范.md](编码规范.md) | 改动粒度、分层依赖红线、lint 30+ 规则、10 条硬性禁止、hook gitignore 约定 | 每次提交前；review 时 |
| [测试策略.md](测试策略.md) | 测试分层与数量、写测试的取舍、CI 分片与 `-count=1`、环境隔离踩坑清单 | 写测试前；调试 CI 失败 |
| [Git工作流与CI.md](Git工作流与CI.md) | 分支/提交/PR 规则、生成物同提交、19 个 workflow、发布红线、本分支阶段门禁 | 提 PR 前；发布前 |

## 十条最容易踩的红线（速查）

1. 不存展示状态（读时派生）。
2. 探针失败 ≠ 会话死亡。
3. 不强制删脏 worktree。
4. 不改已合并迁移（新建 + 追加 `shippedMigrations`）。
5. 不手改 `sqlite/gen/**`、`api/schema.ts`、`routeTree.gen.ts`。
6. 不手工发 CDC 事件（只靠触发器）。
7. 不改主监听器 bind host、不加认证。
8. 不新增网络暴露；LAN 上只有 `GET /api/v1/identity` 一条未认证路由。
9. AO 状态只能在 `~/.ao` 下。
10. 测试不联网（用 `httptest`/fake/注入）。

## 常用命令

```bash
npm run lint             # backend go test + golangci-lint v2.13.2
npm run frontend:typecheck
npm run api              # 改 DTO 后
npm run sqlc             # 改 queries/迁移后
npm run migration:create -- <snake_case_name>
npm run shared:check     # 改 packages/cloud-client 或 product-ui 后
```
