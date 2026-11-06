# M4 Preview Windows 清理调查

日期：2026-11-06。结论：完全访问环境未复现，统筹 workspace-write 沙箱两次出现夹具清理失败；原运行时原因未确定。没有据此改动生产清理代码或宣称问题已修复。

统筹最新独立复验：获准沙箱外运行相同 Preview 命令，TEMP/TMP 为全新 `.tmp/supervisor-preview-elevated`，exit 0、47.181s；测试结束后目录为空。证据为 `.tmp/supervisor-20261106/integration-preview-independent-full-access.log/.exit` 及统筹验收报告。当前独立功能和清理证据已通过；下方沙箱内失败记录保留，环境差异未唯一归因。

统筹追加现场证据（2026-11-06）：在全新 `.tmp/supervisor-test-temp-2` 复跑同一命令，再次 exit 1（55.182s），仅 TempDir 清理失败。残留 `TestM4IntegrationPreviewIsolatedAndReadOnly1983425138/001/.git/objects/26/7df4b4b9a2d264bcfc29a32bbeeea2c6d47ab7` 为 ReadOnly、Archive，85 字节；Authenticated Users 具有 M 权限，未发现 Git/remote-http(s) 进程或 `.git/worktrees` 残留。两次失败运行均为 workspace-write 沙箱，沙箱外两次尝试因自动审核超时未启动，不能记为测试结果。本执行环境为 danger-full-access，连续通过证据见下表。此差异不能证明生产资源泄漏，也不能证明沙箱是唯一根因。统筹独立 Store `TestM4IntegrationPlanAtomicReplay` / `TestM4IntegrationOperationFencingAndRecovery` 报告 exit 0（6.905s）；该独立结果不替代原清理 FAIL。

统筹原记录 `.tmp/supervisor-20261106/integration-preview-independent.log/.exit` 为 exit 1。`TestM4IntegrationPreviewIsolatedAndReadOnly` 没有报告主体断言失败，在 testing.go 的 TempDir 清理删除 `001/.git/objects/10/24e5ffdbbe893c81f299ae44884316685683dd` 时 Access denied。失败位置为源仓夹具对象，不是 Preview 的专用 Worktree。原报告和残留均未改动。

主控现场检查：

- 残留 16 个文件，属性全部为 ReadOnly、Archive；这是观察事实，不能单独证明失败原因。
- 当前没有相关 Git 或测试进程，失败对象使用 FileShare.None 独占只读打开成功。只能排除检查时的持续占用，不能还原失败瞬间。
- 失败目录与复跑目录的 ACL、Git 对象 ACL 相同，均位于 E 盘 NTFS；C 盘也为 NTFS。未修改 ACL 或属性。
- `deliveryGit` 使用同步 `cmd.Run`；Preview defer 在独立 5 秒 context 内执行普通 `git worktree remove` 并返回清理错误；现有测试验证预演前后注册 Worktree 数量一致。没有发现未 Wait 的本路径子进程。
- 本机实际工具链是 Go 1.27.1 Windows。读取其 os.RemoveAll/Deleteat 源码可见会进入 Windows unlinkat；没有足够证据认定此标准库调用、瞬时占用或原执行 token 中的哪一项导致失败。

复跑只使用主控新建目录，不碰统筹目录；TEMP/TMP 在单次子进程中指定：

| 命令（backend） | TEMP/TMP | 实际结果 |
|---|---|---|
| `go test ./internal/adapters/workspace/gitworktree -run "^TestM4IntegrationPreview" -count=1 -timeout 5m -v` | `.tmp/m407-preview-repro` | exit 0，43.746s，m407-preview-repro.log/.exit |
| 同命令，`-count=3` | `.tmp/m407-preview-stability` | exit 0，140.001s；三轮 47.89s / 45.99s / 45.84s，m407-preview-stability.log/.exit |

两处测试临时根在结束后均为空，包含 testing.TempDir 清理完成。日志目录：`C:/Users/11459/AppData/Local/Temp/ao-m4-resume-20261106-01a1acb1/`。

这些结果是新的稳定复跑证据，不将统筹旧 FAIL 记为 PASS。最终验收仍应保留该失败及原运行权限/环境未还原的限制；如再次出现，先记录当时文件锁和进程 token，再定位，不用无条件强删或无限重试掩盖失败。
