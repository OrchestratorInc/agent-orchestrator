package store_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// 删除幂等 fingerprint 比较、CAS 谓词或旧版本失效写入会破坏本例的重启回放、冲突及双写断言。
func TestM4ArtifactImmutableReplayCASAndRestart(t *testing.T) {
	x := newQualityFixture(t, 1)
	a := x.artifacts[0]
	ctx := x.context(t)
	if _, _, err := x.s.SaveArtifact(context.Background(), a, x.ad.Fence, x.now); !errors.Is(err, domain.ErrAdmissionRequired) {
		t.Fatalf("live write without permit: %v", err)
	}
	saved, made, err := x.s.SaveArtifact(ctx, a, x.ad.Fence, x.now)
	if err != nil || !made {
		t.Fatalf("save: %v %v", made, err)
	}
	other, err := sqlite.Open(x.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	read, found, err := other.GetArtifact(context.Background(), saved.ID)
	if err != nil || !found || read.Digest() != a.Fingerprint || read.Repo.RepoPath != a.Repo.RepoPath || read.Repo.WorktreePath != a.Repo.WorktreePath || read.Repo.GitCommonDir != a.Repo.GitCommonDir {
		t.Fatalf("restart lost hidden identity: %+v %v", read, err)
	}
	replay, made, err := other.SaveArtifact(ctx, a, x.ad.Fence, x.now)
	if err != nil || made || replay.ID != saved.ID {
		t.Fatalf("replay: %v %v", made, err)
	}
	changed := a
	changed.Git.WorkspaceDigest = strings.Repeat("f", 64)
	changed.Fingerprint = changed.Digest()
	if _, _, err := x.s.SaveArtifact(ctx, changed, x.ad.Fence, x.now); !errors.Is(err, domain.ErrArtifactStale) {
		t.Fatalf("fingerprint overwritten: %v", err)
	}
	changed.ID = "new-artifact"
	changed.ArtifactRevision = 2
	changed.Fingerprint = changed.Digest()
	if _, _, err := x.s.SaveArtifact(ctx, changed, x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	old, _, err := other.GetArtifact(context.Background(), saved.ID)
	if err != nil || old.State != "stale" || old.Revision != 2 {
		t.Fatalf("old evidence remained fresh: %+v %v", old, err)
	}
	if _, _, err := x.s.TransitionArtifact(ctx, changed.ID, 1, changed.Fingerprint, "verified", "committed", x.ad.Fence, x.now); !errors.Is(err, domain.ErrDeliveryBlocked) {
		t.Fatalf("gate bypass: %v", err)
	}
	if _, _, err := x.s.TransitionArtifact(ctx, changed.ID, 1, a.Fingerprint, "verified", "stale", x.ad.Fence, x.now); !errors.Is(err, domain.ErrArtifactStale) {
		t.Fatalf("wrong fingerprint CAS: %v", err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i, s := range []*sqlite.Store{x.s, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, results[i] = s.TransitionArtifact(ctx, changed.ID, 1, changed.Fingerprint, "verified", "stale", x.ad.Fence, x.now)
		}()
	}
	close(start)
	wg.Wait()
	wins, conflicts := 0, 0
	for _, err := range results {
		if err == nil {
			wins++
		}
		if errors.Is(err, domain.ErrArtifactStale) {
			conflicts++
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("CAS results=%v", results)
	}
	list, err := x.s.ListArtifacts(context.Background(), a.WorkflowID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list=%v %v", list, err)
	}
	if _, err := x.db.Exec(`UPDATE workflow_artifacts SET repo_path='other' WHERE id=?`, saved.ID); err == nil {
		t.Fatal("immutable identity was writable")
	}
	if leaks := qualityCount(t, x.db, `SELECT count(*) FROM change_log WHERE json_extract(payload,'$.kind') IN ('artifact','path_reservation') AND (instr(payload,?)>0 OR instr(payload,?)>0)`, a.Repo.RepoPath, a.Repo.WorktreePath); leaks != 0 {
		t.Fatal("CDC leaked absolute paths")
	}
}

// 删除持久身份任一联接，会接受调用方换 Session/代际、换仓、扩大 scope 或伪造 target/base。
func TestM4ArtifactRejectsForgedIdentityAndAcceptsUnpinnedBase(t *testing.T) {
	x := newQualityFixture(t, 1)
	original := x.artifacts[0]
	changes := []struct {
		name   string
		change func(*domain.ArtifactSnapshot)
	}{
		{"workflow", func(a *domain.ArtifactSnapshot) { a.WorkflowID = "other" }},
		{"task", func(a *domain.ArtifactSnapshot) { a.TaskID = "other" }},
		{"session", func(a *domain.ArtifactSnapshot) { a.SessionID = "other" }},
		{"generation", func(a *domain.ArtifactSnapshot) { a.Generation = "other" }},
		{"project", func(a *domain.ArtifactSnapshot) { a.Repo.ProjectID = "other" }},
		{"scope", func(a *domain.ArtifactSnapshot) { a.Repo.AllowedPaths = nil }},
		{"target", func(a *domain.ArtifactSnapshot) { a.Repo.TargetRef = "other" }},
		{"base", func(a *domain.ArtifactSnapshot) { a.Repo.BaseSHA = strings.Repeat("f", 40) }},
		{"repo", func(a *domain.ArtifactSnapshot) { a.Repo.RepoPath = t.TempDir() }},
		{"worktree", func(a *domain.ArtifactSnapshot) { a.Repo.WorktreePath = t.TempDir() }},
		{"common-dir", func(a *domain.ArtifactSnapshot) { a.Repo.GitCommonDir = t.TempDir() }},
	}
	for _, tc := range changes {
		t.Run(tc.name, func(t *testing.T) {
			a := original
			tc.change(&a)
			a.Fingerprint = a.Digest()
			if _, _, err := x.s.SaveArtifact(x.context(t), a, x.ad.Fence, x.now); err == nil {
				t.Fatal("forged identity accepted")
			}
		})
	}
	if _, err := x.db.Exec(`UPDATE workflow_task_repos SET base_sha=''`); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(`UPDATE workflow_repositories SET base_sha=''`); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(`UPDATE workflow_attempt_repo_bindings SET expected_base_sha=''`); err != nil {
		t.Fatal(err)
	}
	if _, made, err := x.s.SaveArtifact(x.context(t), original, x.ad.Fence, x.now); err != nil || !made {
		t.Fatalf("bound base without explicit pin: %v %v", made, err)
	}
	if _, err := x.db.Exec(`UPDATE session_worktrees SET branch='switched'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := x.s.SaveArtifact(x.context(t), original, x.ad.Fence, x.now); !errors.Is(err, domain.ErrArtifactInvalid) {
		t.Fatalf("replay skipped identity recheck: %v", err)
	}
}
