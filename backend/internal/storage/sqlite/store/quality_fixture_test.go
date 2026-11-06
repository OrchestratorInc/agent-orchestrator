package store_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// qualityFixture 复用 M3 ExecutionFixture 的许可与观察，仅增加真实 SQLite 中的仓库链路。
type qualityFixture struct {
	*executionFixture
	dir       string
	db        *sql.DB
	rec       domain.SessionRecord
	artifacts []domain.ArtifactSnapshot
}

// qualityClaim 建立带显式 scope 的真实 DAG；无 scope 的仓库仍意味着整仓占位。
func qualityClaim(t *testing.T, s *sqlite.Store, f *sqlitetest.AdmissionFixture, id string, roots []string, scopes [][]string, now time.Time) domain.ClaimAdmissionInput {
	t.Helper()
	w, taskID := domain.WorkflowID(id), domain.WorkflowTaskID(id+"-task")
	var repos []domain.WorkflowRepository
	var taskRepos []domain.WorkflowTaskRepository
	for i, root := range roots {
		project := qualityProject(root)
		if err := s.UpsertProject(context.Background(), domain.ProjectRecord{ID: project, Path: root, RegisteredAt: now}); err != nil {
			t.Fatal(err)
		}
		key := domain.WorkflowRepoKey(fmt.Sprintf("%s-repo-%d", id, i))
		repos = append(repos, domain.WorkflowRepository{WorkflowID: w, RepoKey: key, ProjectID: domain.ProjectID(project), BaseSHA: strings.Repeat("a", 40), TargetRef: "main"})
		taskRepos = append(taskRepos, domain.WorkflowTaskRepository{WorkflowID: w, TaskID: taskID, RepoKey: key, AllowedPaths: scopes[i], BaseSHA: strings.Repeat("a", 40)})
	}
	_, _, err := s.CreateWorkflow(context.Background(), workflowStoreRecord(w, now), repos, []domain.WorkflowTask{workflowStoreTask(taskID, w, 2, now)}, nil, taskRepos, "create-"+id, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	wf, _, err := s.ActivateWorkflow(context.Background(), w, 1, "activate-"+id, strings.Repeat("b", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := s.GetWorkflowTask(context.Background(), w, taskID)
	if err != nil {
		t.Fatal(err)
	}
	e, err := f.PrepareWorkflowAdmission(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	return domain.ClaimAdmissionInput{Attempt: workflowStoreAttempt(domain.WorkflowAttemptID(id+"-attempt"), w, taskID, "worker", "lease", now, now.Add(20*time.Second)),
		ExpectedTaskRevision: task.Revision, ExpectedWorkflowRevision: wf.Revision, IdempotencyKey: "claim-" + id, RequestFingerprint: strings.Repeat("c", 64), Evidence: e}
}

// newQualityAdmittedFixture 停在真实 Session 已接受但 Bind 回执尚未到达的状态。
func newQualityAdmittedFixture(t *testing.T, count int) *qualityFixture {
	t.Helper()
	x := &qualityFixture{executionFixture: &executionFixture{now: workflowStoreTime()}, dir: t.TempDir()}
	x.s = sqlitetest.MustOpenAt(t, x.dir)
	x.db = qualityDB(t, x.dir)
	f := sqlitetest.NewAdmissionFixture(t, x.s, func() time.Time { return x.now })
	roots, scopes := make([]string, count), make([][]string, count)
	for i := range roots {
		roots[i] = qualityRepoRoot(t)
		scopes[i] = []string{"src", "docs"}
	}
	in := qualityClaim(t, x.s, f, "quality", roots, scopes, x.now)
	var err error
	x.attempt, x.ad, _, err = x.s.ReserveAndClaim(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	var bindings []domain.WorkflowAttemptRepoBinding
	for i, root := range roots {
		repo := domain.DeliveryRepo{RepoKey: domain.WorkflowRepoKey(fmt.Sprintf("quality-repo-%d", i)), ProjectID: domain.ProjectID(qualityProject(root)),
			RepoPath: root, WorktreePath: t.TempDir(), GitCommonDir: filepath.Join(root, ".git"), Branch: "task", TargetRef: "refs/heads/main", BaseSHA: strings.Repeat("a", 40), CreationSHA: strings.Repeat("a", 40), AllowedPaths: []string{"docs", "src"}}
		if err := os.WriteFile(filepath.Join(repo.WorktreePath, ".git"), []byte("gitdir: "+repo.GitCommonDir+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		bindings = append(bindings, domain.WorkflowAttemptRepoBinding{RepoKey: repo.RepoKey, ProjectID: repo.ProjectID, ExpectedBaseSHA: repo.BaseSHA, SessionWorktreeRef: repo.WorktreePath, ObservedHeadSHA: repo.BaseSHA, State: domain.WorkflowRepoBindingStateVerified})
		x.artifacts = append(x.artifacts, domain.ArtifactSnapshot{ID: fmt.Sprintf("artifact-%d", i), WorkflowID: x.attempt.WorkflowID, TaskID: x.attempt.TaskID, AttemptID: x.attempt.ID, Generation: "generation-1", Repo: repo,
			Git: domain.GitArtifact{HeadSHA: repo.BaseSHA, TreeSHA: strings.Repeat("b", 40), IndexDigest: strings.Repeat("c", 64), WorkspaceDigest: strings.Repeat("d", 64)}, ArtifactRevision: 1, State: "verified"})
	}
	b := domain.WorkflowAttemptBinding{AttemptID: x.attempt.ID, WorkflowID: x.attempt.WorkflowID, TaskID: x.attempt.TaskID, SpawnRequestID: "quality-spawn", SpawnHash: strings.Repeat("e", 64)}
	b, _, err = x.s.CreateWorkflowAttemptBinding(x.context(t), b, bindings, x.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = x.s.TransitionWorkflowAttemptBinding(x.context(t), b.AttemptID, domain.WorkflowBindingStateSpawnRequested, "", b.Revision, x.now); err != nil {
		t.Fatal(err)
	}
	x.seed = domain.SessionRecord{ProjectID: x.artifacts[0].Repo.ProjectID, Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, ClientRequestID: b.SpawnRequestID, ClientRequestHash: b.SpawnHash,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: x.now}, CreatedAt: x.now, UpdatedAt: x.now, Metadata: domain.SessionMetadata{ControllerGeneration: "generation-1"}}
	x.rec, _, err = x.s.CreateAdmittedSession(x.context(t), x.seed, x.ad, "", x.now)
	if err != nil {
		t.Fatal(err)
	}
	// 删除 CreateAdmittedSession 的同事务绑定会在 gateway 返回后留下无 Session 的占位。
	if n := qualityCount(t, x.db, `SELECT count(*) FROM workflow_task_path_reservations WHERE session_id=? AND generation='generation-1'`, string(x.rec.ID)); n != count*2 {
		t.Fatalf("pre-Bind reservations=%d", n)
	}
	x.ad, _, err = x.s.GetSchedulerAdmission(context.Background(), x.ad.ID)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

// newQualityFixture 通过 Claim→创建 Session→Bind 装配；不使用 Provider 或用户 AO 数据。
func newQualityFixture(t *testing.T, count int) *qualityFixture {
	t.Helper()
	x := newQualityAdmittedFixture(t, count)
	b, _, err := x.s.GetWorkflowAttemptBinding(context.Background(), x.attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = x.s.BindWorkflowAttemptSession(x.context(t), x.attempt.ID, x.rec.ID, "generation-1", domain.WorkflowBindingStateBoundReady, b.Revision, x.now); err != nil {
		t.Fatal(err)
	}
	for i := range x.artifacts {
		a := &x.artifacts[i]
		a.SessionID = x.rec.ID
		a.Fingerprint = a.Digest()
		if err := x.s.UpsertSessionWorktree(context.Background(), domain.SessionWorktreeRecord{SessionID: x.rec.ID, RepoName: string(a.Repo.RepoKey), ProjectID: a.Repo.ProjectID, RepoPath: a.Repo.RepoPath,
			WorktreePath: a.Repo.WorktreePath, Branch: a.Repo.Branch, BaseSHA: a.Repo.BaseSHA, CreationSHA: a.Repo.CreationSHA, State: "active"}); err != nil {
			t.Fatal(err)
		}
	}
	return x
}

func qualityDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func qualityRepoRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	return root
}

func qualityProject(root string) string {
	sum := sha256.Sum256([]byte(root))
	return fmt.Sprintf("quality-%x", sum[:8])
}

func qualityCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (x *qualityFixture) stop(t *testing.T) {
	t.Helper()
	x.rec.IsTerminated = true
	if err := x.s.UpdateSession(context.Background(), x.rec); err != nil {
		t.Fatal(err)
	}
	ob := x.observation(x.rec)
	ob.Terminated = true
	if err := x.s.ReleaseExecutionAdmission(context.Background(), x.ad, ob, x.now); err != nil {
		t.Fatal(err)
	}
}
