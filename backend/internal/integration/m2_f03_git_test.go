package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestM2F03SingleRepoLiveGitRechecks 每个场景覆盖独立的生产身份/分支/历史/Git 检查。
// 删除 Manager 对应检查会使场景中的 Verify 与 HTTP 闭锁断言失败；合法 dirty 不应被清理。
func TestM2F03SingleRepoLiveGitRechecks(t *testing.T) {
	f := newM2F03Flow(t, "f03-live")
	ctx := context.Background()
	other := m2ffRepo(t, "f03-other")
	if err := f.store.UpsertProject(ctx, domain.ProjectRecord{
		ID: string(f.projectB), Path: other, RegisteredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(f.row.WorktreePath, "user-dirty.txt")
	tracked := filepath.Join(f.row.WorktreePath, "history-0.txt")
	const content = "保留未提交的用户内容\n"
	m2f03Write(t, marker, []byte(content))
	m2f03Write(t, tracked, []byte(content))
	m2f03Ready(t, f, f.pin, true)
	changeRow := func(t *testing.T, change func(*domain.SessionWorktreeRecord)) func() {
		t.Helper()
		row := f.row
		change(&row)
		if err := f.store.DeleteSessionWorktrees(ctx, f.sid); err != nil {
			t.Fatal(err)
		}
		if err := f.store.UpsertSessionWorktree(ctx, row); err != nil {
			t.Fatal(err)
		}
		return func() {
			if err := f.store.DeleteSessionWorktrees(ctx, f.sid); err != nil {
				t.Fatal(err)
			}
			if err := f.store.UpsertSessionWorktree(ctx, f.row); err != nil {
				t.Fatal(err)
			}
		}
	}
	cases := []struct {
		name   string
		mutate func(*testing.T) func()
	}{
		{"记录删除", func(t *testing.T) func() {
			if err := f.store.DeleteSessionWorktrees(ctx, f.sid); err != nil {
				t.Fatal(err)
			}
			return func() {
				if err := f.store.UpsertSessionWorktree(ctx, f.row); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"RepoKey错绑", func(t *testing.T) func() {
			return changeRow(t, func(row *domain.SessionWorktreeRecord) { row.RepoName = "foreign-repo" })
		}},
		{"ProjectID错绑", func(t *testing.T) func() {
			return changeRow(t, func(row *domain.SessionWorktreeRecord) { row.ProjectID = f.projectB })
		}},
		{"RepoPath错绑", func(t *testing.T) func() {
			return changeRow(t, func(row *domain.SessionWorktreeRecord) { row.RepoPath = other })
		}},
		{"WorktreePath替换", func(t *testing.T) func() {
			return changeRow(t, func(row *domain.SessionWorktreeRecord) { row.WorktreePath = f.repoA })
		}},
		{"BaseSHA被篡改", func(t *testing.T) func() {
			before := strings.TrimSpace(m2smGit(t, f.repoA, "rev-parse", f.pin+"~1"))
			return changeRow(t, func(row *domain.SessionWorktreeRecord) { row.BaseSHA = before })
		}},
		{"CreationSHA被篡改", func(t *testing.T) func() {
			before := strings.TrimSpace(m2smGit(t, f.repoA, "rev-parse", f.pin+"~1"))
			return changeRow(t, func(row *domain.SessionWorktreeRecord) { row.CreationSHA = before })
		}},
		{"Worktree替换为同历史独立仓库", func(t *testing.T) func() {
			saved := f.row.WorktreePath + "-saved"
			m2f03Move(t, f.row.WorktreePath, saved)
			m2smGit(t, f.repoA, "clone", "--no-hardlinks", "--no-checkout", f.repoA, f.row.WorktreePath)
			m2smGit(t, f.row.WorktreePath, "checkout", "-b", f.row.Branch, f.pin)
			return func() {
				if err := os.RemoveAll(f.row.WorktreePath); err != nil {
					t.Fatal(err)
				}
				m2f03Move(t, saved, f.row.WorktreePath)
			}
		}},
		{"Repo目录丢失", func(t *testing.T) func() {
			saved := f.repoA + "-saved"
			m2f03Move(t, f.repoA, saved)
			return func() { m2f03Move(t, saved, f.repoA) }
		}},
		{"Repo被替换为另一仓库", func(t *testing.T) func() {
			saved := f.repoA + "-saved"
			m2f03Move(t, f.repoA, saved)
			m2f03Move(t, other, f.repoA)
			return func() {
				m2f03Move(t, f.repoA, other)
				m2f03Move(t, saved, f.repoA)
			}
		}},
		{"HEAD非法回退", func(t *testing.T) func() {
			before := strings.TrimSpace(m2smGit(t, f.repoA, "rev-parse", f.pin+"~1"))
			m2smGit(t, f.row.WorktreePath, "update-ref", "refs/heads/"+f.row.Branch, before, f.pin)
			return func() { m2smGit(t, f.row.WorktreePath, "update-ref", "refs/heads/"+f.row.Branch, f.pin, before) }
		}},
		{"分支漂移", func(t *testing.T) func() {
			m2smGit(t, f.row.WorktreePath, "switch", "-c", "f03-drift")
			return func() { m2smGit(t, f.row.WorktreePath, "switch", f.row.Branch) }
		}},
		// 损坏真实索引覆盖 Git 无法回答时的闭锁，不模拟 os/exec 自身的错误。
		{"Git索引损坏", func(t *testing.T) func() {
			index := strings.TrimSpace(m2smGit(t, f.row.WorktreePath, "rev-parse", "--git-path", "index"))
			if !filepath.IsAbs(index) {
				index = filepath.Join(f.row.WorktreePath, index)
			}
			original, err := os.ReadFile(index)
			if err != nil {
				t.Fatal(err)
			}
			m2f03Write(t, index, []byte("invalid git index"))
			return func() { m2f03Write(t, index, original) }
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			undo := tc.mutate(t)
			defer func() {
				undo()
				m2f03Ready(t, f, f.pin, true)
				for _, path := range []string{marker, tracked} {
					if got, err := os.ReadFile(path); err != nil || string(got) != content {
						t.Fatalf("只读核验破坏 dirty 内容: %q err=%v", got, err)
					}
				}
			}()
			if err := f.mgr.VerifySessionWorktrees(ctx, f.sid); err == nil {
				t.Error("真实 Manager 接受了失效的单仓 Worktree")
			}
			m2f03Unavailable(t, f)
		})
	}
}
