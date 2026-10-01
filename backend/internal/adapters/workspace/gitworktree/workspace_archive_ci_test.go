package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestArchiveDropsIgnoredCheckout is the CI check for the archive disk win.
// One worktree holds a tracked edit, a new file, and a 1 MiB gitignored
// payload. Archive removes the checkout. Restore comes back on the same
// commit without the payload. An unstaged edit on the same lines keeps both
// sides and does not create a commit.
func TestArchiveDropsIgnoredCheckout(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-ci", Branch: "feature/ci"}
	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := os.WriteFile(filepath.Join(info.Path, ".gitignore"), []byte("secret.bin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, info.Path, "add", ".gitignore")
	runGit(t, git, info.Path, "commit", "-m", "ignore payload")
	head := gitOutput(t, git, info.Path, "rev-parse", "HEAD")

	if err := os.WriteFile(filepath.Join(info.Path, "README.md"), []byte("edited by agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(info.Path, "notes.txt"), []byte("new file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const payload = 1 << 20
	if err := writePayload(filepath.Join(info.Path, "secret.bin"), payload); err != nil {
		t.Fatal(err)
	}
	checkoutBefore := dirBytes(t, info.Path)
	gitBefore := dirBytes(t, filepath.Join(repo, ".git"))
	if checkoutBefore < payload {
		t.Fatalf("checkout = %d bytes, want at least the %d byte payload", checkoutBefore, payload)
	}

	ref, err := ws.StashUncommitted(ctx, info)
	if err != nil || ref == "" {
		t.Fatalf("stash ref=%q err=%v", ref, err)
	}
	if err := ws.ForceDestroy(ctx, info); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(info.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("checkout still on disk after archive")
	}
	gitAfter := dirBytes(t, filepath.Join(repo, ".git"))
	gitGrowth := gitAfter - gitBefore
	t.Logf("archive removed %d checkout bytes; git object store grew by %d bytes for a %d-byte ignored payload", checkoutBefore, gitGrowth, payload)
	if gitGrowth > payload/4 {
		t.Fatalf("git grew by %d bytes after dropping a %d byte ignored file", gitGrowth, payload)
	}

	restored, err := ws.Restore(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := gitOutput(t, git, restored.Path, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD = %s, want %s", got, head)
	}
	if status := gitOutput(t, git, restored.Path, "status", "--porcelain"); status != "" {
		t.Fatalf("restore status = %q, want clean", status)
	}
	readme, err := os.ReadFile(filepath.Join(restored.Path, "README.md"))
	if err != nil || string(readme) != "seed\n" {
		t.Fatalf("README after restore = %q, %v", readme, err)
	}
	if _, err := os.Stat(filepath.Join(restored.Path, "notes.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("restore brought the new file back")
	}
	if _, err := os.Stat(filepath.Join(restored.Path, "secret.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("restore brought the ignored payload back")
	}
	if dirBytes(t, restored.Path) >= payload {
		t.Fatal("restored checkout is as large as the ignored payload")
	}

	if err := os.WriteFile(filepath.Join(restored.Path, "README.md"), []byte("typed after restore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	applyErr := ws.ApplyPreserved(ctx, restored, ref)
	if !errors.Is(applyErr, ErrPreservedConflict) {
		t.Fatalf("apply = %v, want a conflict", applyErr)
	}
	if got := gitOutput(t, git, restored.Path, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD after apply = %s, want %s", got, head)
	}
	merged, err := os.ReadFile(filepath.Join(restored.Path, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(merged)
	if !strings.Contains(text, "edited by agent") || !strings.Contains(text, "typed after restore") || !strings.Contains(text, "<<<<<<<") {
		t.Fatalf("README = %q, want both edits and a conflict marker", text)
	}
	if _, err := os.Stat(filepath.Join(restored.Path, "secret.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("conflict apply brought the ignored payload back")
	}
	if _, err := ws.run(ctx, ws.binary, revParseVerifyArgs(repo, ref)...); err != nil {
		t.Fatal("conflict apply deleted the snapshot")
	}
	if err := ws.ApplyPreserved(ctx, restored, ref); !errors.Is(err, ErrPreservedConflict) {
		t.Fatalf("repeat apply = %v, want unresolved conflict", err)
	}
	repeated, err := os.ReadFile(filepath.Join(restored.Path, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(repeated) != text {
		t.Fatalf("repeat apply changed conflict contents:\n%s", repeated)
	}
	if _, err := ws.run(ctx, ws.binary, revParseVerifyArgs(repo, ref)...); err != nil {
		t.Fatal("repeat apply deleted the snapshot")
	}
}

func TestDiskUsageMeasuresManagedWorktreeWithoutFollowingOutsidePaths(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := ws.Create(context.Background(), ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "sess-size-preview", Branch: "feature/size-preview",
	})
	if err != nil {
		t.Fatal(err)
	}
	const payload = 1 << 20
	if err := writePayload(filepath.Join(info.Path, "payload.bin"), payload); err != nil {
		t.Fatal(err)
	}
	bytes, err := ws.DiskUsage(context.Background(), info)
	if err != nil {
		t.Fatalf("DiskUsage: %v", err)
	}
	if bytes < payload {
		t.Fatalf("DiskUsage = %d bytes, want at least %d", bytes, payload)
	}

	info.Path = filepath.Join(tmp, "outside-managed-root")
	if _, err := ws.DiskUsage(context.Background(), info); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("DiskUsage outside managed root error = %v, want ErrUnsafePath", err)
	}
}

func TestStashUncommittedDoesNotReplaceAnExistingSnapshot(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-rearchive", Branch: "feature/rearchive"}
	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := os.WriteFile(filepath.Join(info.Path, "README.md"), []byte("first saved edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := ws.StashUncommitted(ctx, info)
	if err != nil || ref == "" {
		t.Fatalf("first capture ref=%q err=%v", ref, err)
	}
	firstSHA := gitOutput(t, git, repo, "rev-parse", ref)
	if err := ws.ForceDestroy(ctx, info); err != nil {
		t.Fatal(err)
	}
	restored, err := ws.Restore(ctx, cfg)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(restored.Path, "notes.txt"), []byte("second edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.StashUncommitted(ctx, restored); err == nil {
		t.Fatal("second capture succeeded over the existing snapshot")
	}
	if got := gitOutput(t, git, repo, "rev-parse", ref); got != firstSHA {
		t.Fatalf("preserved ref moved from %s to %s", firstSHA, got)
	}
	if got := gitOutput(t, git, repo, "show", ref+":README.md"); got != "first saved edit" {
		t.Fatalf("earlier saved edit = %q", got)
	}
	if _, err := ws.run(ctx, git, "-C", repo, "show", ref+":notes.txt"); err == nil {
		t.Fatal("second edit was written into the earlier snapshot")
	}
}

func TestStashUncommittedRetryReusesOnlyMatchingSnapshot(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-stash-retry", Branch: "feature/stash-retry"})
	if err != nil {
		t.Fatal(err)
	}
	readmePath := filepath.Join(info.Path, "README.md")
	if err := os.WriteFile(readmePath, []byte("saved edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := ws.StashUncommitted(ctx, info)
	if err != nil || ref == "" {
		t.Fatalf("initial capture ref=%q err=%v", ref, err)
	}
	initialSHA := gitOutput(t, git, repo, "rev-parse", ref)
	if retried, err := ws.StashUncommitted(ctx, info); err != nil || retried != ref {
		t.Fatalf("unchanged retry ref=%q err=%v, want %q", retried, err, ref)
	}
	if got := gitOutput(t, git, repo, "rev-parse", ref); got != initialSHA {
		t.Fatalf("retry moved preserved ref from %s to %s", initialSHA, got)
	}

	if err := os.WriteFile(readmePath, []byte("newer edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.StashUncommitted(ctx, info); err == nil {
		t.Fatal("retry accepted different worktree edits")
	}
	if got := gitOutput(t, git, repo, "rev-parse", ref); got != initialSHA {
		t.Fatalf("different edits moved preserved ref from %s to %s", initialSHA, got)
	}
	if current, err := os.ReadFile(readmePath); err != nil || string(current) != "newer edit\n" {
		t.Fatalf("newer edit changed after rejected retry: %q, %v", current, err)
	}

	// Matching file content on a different HEAD is still a different snapshot.
	runGit(t, git, info.Path, "add", "README.md")
	runGit(t, git, info.Path, "commit", "-m", "new base")
	if err := os.WriteFile(readmePath, []byte("saved edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.StashUncommitted(ctx, info); err == nil {
		t.Fatal("retry accepted matching tree with a different base commit")
	}
	if got := gitOutput(t, git, repo, "rev-parse", ref); got != initialSHA {
		t.Fatalf("different base moved preserved ref from %s to %s", initialSHA, got)
	}
}

func TestApplyPreservedRefDeleteFailureStaysVisibleAndCanBeRetried(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-ref-delete-retry", Branch: "feature/ref-delete-retry"}
	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	readmePath := filepath.Join(info.Path, "README.md")
	// Trailing whitespace must not make the marker-only replay guard reject
	// a retry after the ref deletion failure.
	if err := os.WriteFile(readmePath, []byte("saved edit \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := ws.StashUncommitted(ctx, info)
	if err != nil || ref == "" {
		t.Fatalf("capture ref=%q err=%v", ref, err)
	}
	firstSHA := gitOutput(t, git, repo, "rev-parse", ref)
	if err := ws.ForceDestroy(ctx, info); err != nil {
		t.Fatal(err)
	}
	restored, err := ws.Restore(ctx, cfg)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	baseRun := ws.run
	failDelete := true
	ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
		if failDelete && len(args) == 5 && args[0] == "-C" && args[2] == "update-ref" && args[3] == "-d" && args[4] == ref {
			return nil, errors.New("injected preserve-ref deletion failure")
		}
		return baseRun(ctx, binary, args...)
	}

	if err := ws.ApplyPreserved(ctx, restored, ref); err == nil || !strings.Contains(err.Error(), "could not delete preserve ref") {
		t.Fatalf("ApplyPreserved error = %v, want visible preserve-ref deletion failure", err)
	}
	if got := gitOutput(t, git, restored.Path, "show", "HEAD:README.md"); got == "saved edit" {
		t.Fatal("ApplyPreserved unexpectedly committed the saved edit")
	}
	readme, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(readme); got != "saved edit \n" {
		t.Fatalf("README after apply = %q, want saved edit", got)
	}
	if got := gitOutput(t, git, repo, "rev-parse", ref); got != firstSHA {
		t.Fatalf("preserve ref moved from %s to %s after failed deletion", firstSHA, got)
	}

	if err := os.WriteFile(filepath.Join(restored.Path, "later.txt"), []byte("later edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.StashUncommitted(ctx, restored); err == nil {
		t.Fatal("archive succeeded while the prior preserve ref was still pending cleanup")
	}
	later, err := os.ReadFile(filepath.Join(restored.Path, "later.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(later); got != "later edit\n" {
		t.Fatalf("later edit after blocked archive = %q, want unchanged", got)
	}
	if got := gitOutput(t, git, repo, "rev-parse", ref); got != firstSHA {
		t.Fatalf("blocked archive replaced preserve ref with %s, want %s", got, firstSHA)
	}

	// Retrying the same apply after cleanup becomes available must not lose the
	// edits made since the first apply, and should clear the stale ref.
	failDelete = false
	if err := ws.ApplyPreserved(ctx, restored, ref); err != nil {
		t.Fatalf("retry ApplyPreserved: %v", err)
	}
	if _, err := ws.run(ctx, git, revParseVerifyArgs(repo, ref)...); err == nil {
		t.Fatal("preserve ref remains after successful cleanup retry")
	}
	newRef, err := ws.StashUncommitted(ctx, restored)
	if err != nil || newRef == "" {
		t.Fatalf("archive after cleanup retry ref=%q err=%v", newRef, err)
	}
	if got := gitOutput(t, git, repo, "show", newRef+":README.md"); got != "saved edit" {
		t.Fatalf("new snapshot README = %q, want saved edit", got)
	}
	if got := gitOutput(t, git, repo, "show", newRef+":later.txt"); got != "later edit" {
		t.Fatalf("new snapshot later.txt = %q, want later edit", got)
	}
}

func TestApplyPreservedCapturesCollidingUntrackedLocalFile(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-untracked-collision", Branch: "feature/untracked-collision"}
	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	localPath := filepath.Join(info.Path, "shared-new.txt")
	if err := os.WriteFile(localPath, []byte("saved side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := ws.StashUncommitted(ctx, info)
	if err != nil || ref == "" {
		t.Fatalf("capture ref=%q err=%v", ref, err)
	}
	if err := ws.ForceDestroy(ctx, info); err != nil {
		t.Fatal(err)
	}
	restored, err := ws.Restore(ctx, cfg)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	localPath = filepath.Join(restored.Path, "shared-new.txt")
	if err := os.WriteFile(localPath, []byte("local side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := gitOutput(t, git, restored.Path, "rev-parse", "HEAD")
	applyErr := ws.ApplyPreserved(ctx, restored, ref)
	if !errors.Is(applyErr, ErrPreservedConflict) {
		t.Fatalf("apply = %v, want ErrPreservedConflict", applyErr)
	}
	if got := gitOutput(t, git, restored.Path, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD after apply = %s, want %s", got, head)
	}
	got, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "local side") || !strings.Contains(string(got), "saved side") || !strings.Contains(string(got), "<<<<<<<") {
		t.Fatalf("merged untracked file = %q, want both sides and conflict markers", got)
	}
	if _, err := ws.run(ctx, git, revParseVerifyArgs(repo, ref)...); err != nil {
		t.Fatalf("snapshot ref deleted after conflict: %v", err)
	}
}

func TestApplyPreservedMergesNonOverlappingDirtyWorktree(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-dirty-merge", Branch: "feature/dirty-merge"}
	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := os.WriteFile(filepath.Join(info.Path, "saved.txt"), []byte("saved edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := ws.StashUncommitted(ctx, info)
	if err != nil || ref == "" {
		t.Fatalf("capture ref=%q err=%v", ref, err)
	}
	if err := ws.ForceDestroy(ctx, info); err != nil {
		t.Fatal(err)
	}
	restored, err := ws.Restore(ctx, cfg)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(restored.Path, "README.md"), []byte("independent local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := gitOutput(t, git, restored.Path, "rev-parse", "HEAD")
	if err := ws.ApplyPreserved(ctx, restored, ref); err != nil {
		t.Fatalf("apply non-overlapping edits: %v", err)
	}
	if got := gitOutput(t, git, restored.Path, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD after apply = %s, want %s", got, head)
	}
	for path, want := range map[string]string{
		"README.md": "independent local edit\n",
		"saved.txt": "saved edit\n",
	} {
		got, err := os.ReadFile(filepath.Join(restored.Path, path))
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q, err=%v; want %q", path, got, err, want)
		}
	}
	if _, err := ws.run(ctx, git, revParseVerifyArgs(repo, ref)...); err == nil {
		t.Fatal("successful apply left the snapshot ref")
	}
}

func writePayload(path string, n int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	block := make([]byte, 32<<10)
	for i := range block {
		block[i] = byte(i)
	}
	written := 0
	for written < n {
		chunk := block
		if n-written < len(chunk) {
			chunk = chunk[:n-written]
		}
		if _, err := f.Write(chunk); err != nil {
			return err
		}
		written += len(chunk)
	}
	return f.Close()
}

func dirBytes(t *testing.T, root string) int64 {
	t.Helper()
	var total int64
	err := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("size %s: %v", root, err)
	}
	return total
}
