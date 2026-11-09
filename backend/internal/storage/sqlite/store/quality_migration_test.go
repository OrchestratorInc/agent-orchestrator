package store_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// 删除迁移 Up/Down 的建表或清理会破坏往返；旧数据更新后再 Up 验证有数据升级不丢 M3 图。
func TestM4QualityMigrationUpgradeAndDown(t *testing.T) {
	ctx, dir, now := context.Background(), t.TempDir(), workflowStoreTime()
	s := sqlitetest.MustOpenAt(t, dir)
	db := qualityDB(t, dir)
	f := sqlitetest.NewAdmissionFixture(t, s, func() time.Time { return now })
	in := qualityClaim(t, s, f, "upgrade", nil, nil, now)
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, os.DirFS("../migrations"), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 20261105102052187); err != nil {
		t.Fatal(err)
	}
	if n := qualityCount(t, db, `SELECT count(*) FROM sqlite_master WHERE name IN ('workflow_artifacts','workflow_task_path_reservations')`); n != 0 {
		t.Fatalf("Down left %d tables", n)
	}
	if _, err := db.Exec(`UPDATE workflow_tasks SET title='preserved upgrade' WHERE id=?`, string(in.Attempt.TaskID)); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	task, found, err := s.GetWorkflowTask(ctx, in.Attempt.WorkflowID, in.Attempt.TaskID)
	if err != nil || !found || task.Title != "preserved upgrade" {
		t.Fatalf("upgrade lost data: %+v %v", task, err)
	}
	if _, _, made, err := s.ReserveAndClaim(ctx, in); err != nil || !made {
		t.Fatalf("upgraded admission unusable: %v %v", made, err)
	}
	if n := qualityCount(t, db, `SELECT count(*) FROM workflow_task_path_reservations`); n != 0 {
		t.Fatalf("no-repo task invented %d reservations", n)
	}
	if _, err := provider.DownTo(ctx, 20261105102052187); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%q err=%v", integrity, err)
	}
}

// 删除恢复时补齐旧 Admission 会让 M3 的活跃/UNKNOWN Session 在升级后失去路径保护。
func TestM4QualityUpgradeBackfillsUnknownAdmissionBeforeRecovery(t *testing.T) {
	x := newQualityFixture(t, 1)
	ctx := context.Background()
	provider, err := goose.NewProvider(goose.DialectSQLite3, x.db, os.DirFS("../migrations"), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 20261105102052187); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if n := qualityCount(t, x.db, `SELECT count(*) FROM workflow_task_path_reservations`); n != 0 {
		t.Fatalf("upgrade fixture already reserved: %d", n)
	}
	x.now = x.now.Add(31 * time.Second)
	if _, err := x.s.RecoverExpiredWorkflowAttempts(ctx, x.now); err != nil {
		t.Fatal(err)
	}
	leader, _, err := x.s.AcquireSchedulerLeader(ctx, "sqlitetest-admission", x.now, x.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	project := x.artifacts[0].Repo
	if err := x.s.UpsertProject(ctx, domain.ProjectRecord{ID: string(project.ProjectID), Path: qualityRepoRoot(t), RegisteredAt: x.now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := x.s.CompleteSchedulerRecovery(ctx, leader.Fence, leader.Revision, x.now); err == nil {
		t.Fatal("unknown repository admitted recovery")
	}
	if n := qualityCount(t, x.db, `SELECT recovered FROM scheduler_leader`); n != 0 {
		t.Fatal("failed recovery opened admission")
	}
	if err := x.s.UpsertProject(ctx, domain.ProjectRecord{ID: string(project.ProjectID), Path: project.RepoPath, RegisteredAt: x.now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := x.s.CompleteSchedulerRecovery(ctx, leader.Fence, leader.Revision, x.now); err != nil {
		t.Fatal(err)
	}
	if n := qualityCount(t, x.db, `SELECT count(*) FROM workflow_task_path_reservations WHERE epoch=? AND session_id=? AND generation='generation-1'`, leader.Fence.Epoch, string(x.rec.ID)); n != 2 {
		t.Fatalf("upgrade paths=%d", n)
	}
	f := sqlitetest.NewAdmissionFixture(t, x.s, func() time.Time { return x.now })
	in := qualityClaim(t, x.s, f, "after-upgrade", []string{project.RepoPath}, [][]string{{"src/sub"}}, x.now)
	if _, _, _, err := x.s.ReserveAndClaim(ctx, in); !errors.Is(err, domain.ErrPathReservationConflict) {
		t.Fatalf("upgrade bypassed unknown writer: %v", err)
	}
}
