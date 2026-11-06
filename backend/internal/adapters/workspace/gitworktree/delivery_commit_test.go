package gitworktree

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestM4TaskCommitPreservesIndexAndRestrictsRef 删除私有树/目标校验/CAS 或 hook 隔离会写入未审内容、重复提交或改动用户索引。
func TestM4TaskCommitPreservesIndexAndRestrictsRef(t *testing.T) {
	ctx := context.Background()
	guard := func(ctx context.Context, write func(context.Context) error) error { return write(ctx) }
	repo := deliveryFixture(t)
	writeDeliveryFile(t, repo.WorktreePath, "outside.txt", "user staged")
	deliveryTestGit(t, repo.WorktreePath, "add", "--", "outside.txt")
	writeDeliveryFile(t, repo.WorktreePath, "src/a.txt", "reviewed")
	hooks := filepath.Join(repo.RepoPath, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0700); err != nil {
		t.Fatal(err)
	}
	for _, hook := range []string{"pre-commit", "reference-transaction"} {
		if err := os.WriteFile(filepath.Join(hooks, hook), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	bound, snapshot, err := InspectDelivery(ctx, repo, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := domain.ArtifactSnapshot{Repo: bound, Git: snapshot, ArtifactRevision: 1, CreatedAt: time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)}
	a.Fingerprint = a.Digest()
	index := deliveryTestGit(t, repo.WorktreePath, "ls-files", "--stage", "-z")
	sha, err := PrepareDeliveryCommit(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if state, err := ObserveDeliveryCommit(ctx, a, sha); err != nil || state != "unknown" {
		t.Fatalf("unexecuted=%s %v", state, err)
	}
	if err := ApplyDeliveryCommit(ctx, a, sha, strings.Repeat("a", 40), t.TempDir(), guard); err == nil {
		t.Fatal("wrong target accepted")
	}
	if got := strings.TrimSpace(string(deliveryTestGit(t, repo.WorktreePath, "rev-parse", "HEAD"))); got != a.Git.HeadSHA {
		t.Fatal("target refusal moved task branch")
	}
	if err := ApplyDeliveryCommit(ctx, a, sha, repo.BaseSHA, t.TempDir(), guard); err != nil {
		t.Fatal(err)
	}
	if state, err := ObserveDeliveryCommit(ctx, a, sha); err != nil || state != "committed" {
		t.Fatalf("observed=%s %v", state, err)
	}
	if got := deliveryTestGit(t, repo.WorktreePath, "ls-files", "--stage", "-z"); !bytes.Equal(got, index) {
		t.Fatal("user index modified")
	}
	if got := strings.TrimSpace(string(deliveryTestGit(t, repo.WorktreePath, "rev-parse", "refs/heads/main"))); got != repo.BaseSHA {
		t.Fatal("target ref modified")
	}
	if got := string(deliveryTestGit(t, repo.WorktreePath, "show", sha+":outside.txt")); got != "original" {
		t.Fatalf("outside dirty committed: %q", got)
	}
	if got := string(deliveryTestGit(t, repo.WorktreePath, "show", sha+":src/a.txt")); got != "reviewed" {
		t.Fatalf("reviewed bytes missing: %q", got)
	}
	if data, err := os.ReadFile(filepath.Join(repo.WorktreePath, "outside.txt")); err != nil || string(data) != "user staged" {
		t.Fatal("outside data changed")
	}
	if err := ApplyDeliveryCommit(ctx, a, sha, repo.BaseSHA, t.TempDir(), guard); err == nil {
		t.Fatal("repeated apply accepted")
	}
	deliveryTestGit(t, repo.WorktreePath, "config", "commit.gpgsign", "true")
	if _, err := PrepareDeliveryCommit(ctx, a); err == nil {
		t.Fatal("signature requirement silently bypassed")
	}
}
