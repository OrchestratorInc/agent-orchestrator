package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestWorkspacePathsRefreshAfterInvalidationOrExpiry(t *testing.T) {
	for _, invalidate := range []bool{true, false} {
		t.Run(map[bool]string{true: "event", false: "expiry"}[invalidate], func(t *testing.T) {
			root := newWorkspaceRepo(t)
			now := time.Unix(100, 0)
			s := &Service{workspaceCache: newWorkspaceCache(workspaceCacheTTL, func() time.Time { return now })}
			before, err := s.workspacePaths(t.Context(), "session", root)
			if err != nil {
				t.Fatal(err)
			}
			writeWorkspaceFile(t, root, "new.txt", "new\n")
			cached, err := s.workspacePaths(t.Context(), "session", root)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(before, "|") != strings.Join(cached, "|") {
				t.Fatal("warm inventory unexpectedly changed")
			}
			if invalidate {
				s.InvalidateWorkspaceCache("session")
			} else {
				now = now.Add(workspaceCacheTTL + time.Second)
			}
			fresh, err := s.workspacePaths(t.Context(), "session", root)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, path := range fresh {
				if path == "new.txt" {
					found = true
				}
			}
			if !found {
				t.Fatalf("new path missing after refresh: %v", fresh)
			}
		})
	}
}

func TestWorkspacePathsConcurrentReadsShareInventory(t *testing.T) {
	root := newWorkspaceRepo(t)
	trace := filepath.Join(t.TempDir(), "git-trace.log")
	t.Setenv("GIT_TRACE", trace)
	s := &Service{workspaceCache: newWorkspaceCache(workspaceCacheTTL, nil)}
	done := make(chan error, 32)
	for range 32 {
		go func() {
			_, err := s.workspacePaths(context.Background(), domain.SessionID("session"), root)
			done <- err
		}()
	}
	for range 32 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(data), "built-in: git ls-files"); count != 1 {
		t.Fatalf("inventory ran %d times, want one shared Git read; trace=%s", count, data)
	}
}

func TestWorkspaceFileRevisionLiveReadAvoidsGitAndChecksRevision(t *testing.T) {
	root := newWorkspaceRepo(t)
	st := newFakeStore()
	st.sessions["session"] = domain.SessionRecord{ID: "session", Metadata: domain.SessionMetadata{WorkspacePath: root}}
	s := &Service{store: st}
	trace := filepath.Join(t.TempDir(), "git-trace.log")
	t.Setenv("GIT_TRACE", trace)
	for _, scope := range []WorkspaceDiffScope{WorkspaceDiffCombined, WorkspaceDiffUnstaged, WorkspaceDiffUntracked} {
		first, err := s.GetWorkspaceFileRevision(t.Context(), "session", "README.md", scope, WorkspaceBlobAfter, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if !first.Exists || first.Content == "" || first.Revision == "" {
			t.Fatalf("live text = %#v", first)
		}
	}
	data, err := os.ReadFile(trace)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("plain live text unexpectedly ran Git: %s", data)
	}
	first, err := s.GetWorkspaceFileRevision(t.Context(), "session", "README.md", WorkspaceDiffCombined, WorkspaceBlobAfter, "", "")
	if err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, root, "README.md", "changed immediately\n")
	fresh, err := s.GetWorkspaceFileRevision(t.Context(), "session", "README.md", WorkspaceDiffCombined, WorkspaceBlobAfter, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Content != "changed immediately\n" || fresh.Revision == first.Revision {
		t.Fatalf("live edit = %#v", fresh)
	}
	if _, err = s.GetWorkspaceFileRevision(t.Context(), "session", "README.md", WorkspaceDiffCombined, WorkspaceBlobAfter, "", first.Revision); err == nil {
		t.Fatal("stale expected revision accepted")
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetWorkspaceFileRevision(t.Context(), "session", "escape.txt", WorkspaceDiffCombined, WorkspaceBlobAfter, "", ""); err == nil {
		t.Fatal("live path escaped workspace through symlink")
	}
}

func TestWorkspaceFileRevisionLiveReadResolvesRegisteredChildRoot(t *testing.T) {
	root := newWorkspaceRepo(t)
	child := workspaceChildRepo(t, root, "alpha", "package alpha\n")
	writeWorkspaceFile(t, child, "live.txt", "child live contents\n")
	st := newFakeStore()
	st.projects["ws"] = domain.ProjectRecord{ID: "ws", Kind: domain.ProjectKindWorkspace}
	st.sessions["ws-1"] = domain.SessionRecord{ID: "ws-1", ProjectID: "ws", Metadata: domain.SessionMetadata{WorkspacePath: root}}
	st.worktrees["ws-1"] = []domain.SessionWorktreeRecord{{SessionID: "ws-1", RepoName: "alpha", WorktreePath: child}}
	svc := &Service{store: st}
	trace := filepath.Join(t.TempDir(), "git.log")
	t.Setenv("GIT_TRACE", trace)
	got, err := svc.GetWorkspaceFileRevision(t.Context(), "ws-1", "alpha/live.txt", WorkspaceDiffCombined, WorkspaceBlobAfter, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "alpha/live.txt" || got.Content != "child live contents\n" {
		t.Fatalf("registered child read = %+v", got)
	}
	data, err := os.ReadFile(trace)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(data) > 0 {
		t.Fatalf("child live read unexpectedly ran Git: %s", data)
	}
}
