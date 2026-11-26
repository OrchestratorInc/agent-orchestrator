package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// #9007: installCopilotAgent writes "/.github/agents/ao-*.agent.md" into the
// repo-shared .git/info/exclude, hiding every ao-*.agent.md profile from git
// status and git add in every worktree of the repo. A user-authored profile
// (no AO sentinel) is uncommitted work, and teardown must treat it that way:
// Destroy refuses, and StashUncommitted captures it, instead of silently
// deleting it.

// poisonCopilotExclude replicates ignoreCopilotPath: the glob lands in the
// repo's shared info/exclude, so it covers every worktree.
func poisonCopilotExclude(t *testing.T, repo string) {
	t.Helper()
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	existing, err := os.ReadFile(exclude)
	if err != nil {
		t.Fatalf("read info/exclude: %v", err)
	}
	if err := os.WriteFile(exclude, append(existing, []byte("\n/.github/agents/ao-*.agent.md\n")...), 0o644); err != nil {
		t.Fatalf("poison info/exclude: %v", err)
	}
}

func writeAgentProfile(t *testing.T, worktree, name, body string) string {
	t.Helper()
	path := filepath.Join(worktree, ".github", "agents", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir agents dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write agent profile: %v", err)
	}
	return path
}

// A worktree whose only change is a user-authored ao-*.agent.md profile must
// read dirty: Destroy refuses and the file stays on disk.
func TestDestroyRefusesUserAuthoredAgentProfileHiddenByCopilotExclude(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/one"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	poisonCopilotExclude(t, repo)
	profile := writeAgentProfile(t, info.Path, "ao-reviewer.agent.md", "# my hand-written reviewer agent\n")

	err = ws.Destroy(ctx, info)
	if !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("destroy error = %v, want ports.ErrWorkspaceDirty", err)
	}
	if _, statErr := os.Stat(profile); statErr != nil {
		t.Fatalf("user-authored agent profile was not preserved: %v", statErr)
	}
	records, err := ws.listRecords(ctx, repo)
	if err != nil {
		t.Fatalf("list records: %v", err)
	}
	if _, ok := findWorktree(records, info.Path); !ok {
		t.Fatalf("dirty worktree lost its registration")
	}
}

// The preserve side: StashUncommitted must not early-exit on such a worktree,
// and the preserve ref must actually contain the excluded profile.
func TestStashUncommittedPreservesUserAuthoredAgentProfile(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/one"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	poisonCopilotExclude(t, repo)
	writeAgentProfile(t, info.Path, "ao-reviewer.agent.md", "# my hand-written reviewer agent\n")

	ref, err := ws.StashUncommitted(ctx, info)
	if err != nil {
		t.Fatalf("StashUncommitted: %v", err)
	}
	if ref == "" {
		t.Fatal("StashUncommitted read a worktree holding user work as clean")
	}
	out := string(outputGit(t, git, info.Path, "ls-tree", "-r", "--name-only", ref))
	if !strings.Contains(out, ".github/agents/ao-reviewer.agent.md") {
		t.Fatalf("preserve ref does not contain the user-authored profile, tree:\n%s", out)
	}
}

// AO's own profiles carry the sentinel: they are managed files, not user
// work, and teardown keeps ignoring them.
func TestDestroyStillRemovesAOManagedAgentProfile(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/one"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	poisonCopilotExclude(t, repo)
	writeAgentProfile(t, info.Path, "ao-sess.agent.md", hookutil.CopilotAgentProfileSentinel+"\nmanaged profile\n")

	if err := ws.Destroy(ctx, info); err != nil {
		t.Fatalf("destroy of a worktree holding only AO's own profile = %v, want nil", err)
	}
	if _, err := os.Stat(info.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree path after destroy = %v, want not exist", err)
	}
	ws.waitForDiscards()
}

// A profile outside the ao-* glob is ordinary untracked work: already dirty
// before the fix, and that must not change.
func TestDestroyRefusesNonGlobAgentProfileAsBefore(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/one"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	poisonCopilotExclude(t, repo)
	writeAgentProfile(t, info.Path, "my-reviewer.agent.md", "# mine\n")

	err = ws.Destroy(ctx, info)
	if !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("destroy error = %v, want ports.ErrWorkspaceDirty", err)
	}
}

// Git reports slash-separated paths on every OS; the filter must match them
// with package path so a Windows filepath separator never hides a profile.
func TestIsExcludedAgentProfilePath(t *testing.T) {
	cases := map[string]bool{
		".github/agents/ao-reviewer.agent.md": true,
		".github/agents/ao-.agent.md":         true,
		".github/agents/my-reviewer.agent.md": false,
		".github/agents/ao-reviewer.md":       false,
		".github/agents/nested/ao-x.agent.md": false,
		"sub/.github/agents/ao-x.agent.md":    false,
		".github/agents/":                     false,
		"":                                    false,
	}
	for entry, want := range cases {
		if got := isExcludedAgentProfilePath(entry); got != want {
			t.Errorf("isExcludedAgentProfilePath(%q) = %v, want %v", entry, got, want)
		}
	}
}

// A candidate the probe lists but cannot read may be user work git cannot
// see: Destroy must refuse rather than defer to git and delete it.
func TestDestroyRefusesUnreadableAgentProfile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX permission bits")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads files regardless of permission bits")
	}
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/one"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	poisonCopilotExclude(t, repo)
	profile := writeAgentProfile(t, info.Path, "ao-reviewer.agent.md", "# my hand-written reviewer agent\n")
	if err := os.Chmod(profile, 0); err != nil {
		t.Fatalf("chmod profile: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(profile, 0o600) })

	err = ws.Destroy(ctx, info)
	if !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("destroy error = %v, want ports.ErrWorkspaceDirty", err)
	}
	if _, statErr := os.Stat(profile); statErr != nil {
		t.Fatalf("unreadable agent profile was not preserved: %v", statErr)
	}
}
