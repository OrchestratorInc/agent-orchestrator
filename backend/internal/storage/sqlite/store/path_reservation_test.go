package store_test

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// 删除同 writer 内的路径冲突检查会让两个 Store 都成功 Claim；组件比较退化为字符串前缀会错拒 src-other。
func TestM4PathReservationConcurrentStoresAndComponents(t *testing.T) {
	ctx, dir, now := context.Background(), t.TempDir(), workflowStoreTime()
	first := sqlitetest.MustOpenAt(t, dir)
	second, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	f := sqlitetest.NewAdmissionFixture(t, first, func() time.Time { return now })
	root := qualityRepoRoot(t)
	parent := "src"
	if runtime.GOOS == "windows" {
		parent = "SRC"
	}
	inputs := []domain.ClaimAdmissionInput{
		qualityClaim(t, first, f, "parent", []string{root}, [][]string{{parent}}, now),
		qualityClaim(t, first, f, "child", []string{root}, [][]string{{"src/child"}}, now),
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, 2)
	made := make([]bool, 2)
	for i, s := range []*sqlite.Store{first, second} {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _, _, made[i], errs[i] = s.ReserveAndClaim(ctx, inputs[i]) }()
	}
	close(start)
	wg.Wait()
	wins, conflicts := 0, 0
	for i, err := range errs {
		if made[i] && err == nil {
			wins++
		}
		if errors.Is(err, domain.ErrPathReservationConflict) {
			conflicts++
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins=%d conflicts=%d errors=%v", wins, conflicts, errs)
	}
	next := qualityClaim(t, first, f, "separate", []string{root}, [][]string{{"src-other"}}, now)
	if _, _, ok, err := second.ReserveAndClaim(ctx, next); err != nil || !ok {
		t.Fatalf("non-prefix denied: %v", err)
	}
	whole := qualityClaim(t, first, f, "whole", []string{root}, [][]string{nil}, now)
	// 使路径判断先于 adapter 容量限制仍可达。
	m3Capacity(t, first, domain.HarnessCodex, 3, now)
	whole.Evidence, err = f.PrepareWorkflowAdmission(ctx, domain.WorkflowTask{AgentHarness: domain.HarnessCodex})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := first.ReserveAndClaim(ctx, whole); !errors.Is(err, domain.ErrPathReservationConflict) {
		t.Fatalf("whole repo not blocked: %v", err)
	}
}

// 删除 reserveTaskPaths 的事务内调用或事务回滚会遗留第一个 Repo、预算、Admission 或已 Claim 的 Task。
func TestM4PathReservationMultiRepoRollback(t *testing.T) {
	ctx, dir, now := context.Background(), t.TempDir(), workflowStoreTime()
	s := sqlitetest.MustOpenAt(t, dir)
	db := qualityDB(t, dir)
	f := sqlitetest.NewAdmissionFixture(t, s, func() time.Time { return now })
	amount := int64(10)
	p := domain.BudgetPolicy{ID: "paths-budget", Scope: "global", Currency: "USD", Mode: domain.BudgetHardAdmission, Period: domain.BudgetLifetime, LimitNanos: 100, ReservationNanos: &amount, ReservationBasis: domain.ReservationOperatorApproved, ReservationRef: "test"}
	if _, _, err := s.PutBudgetPolicy(ctx, p, 0, domain.PolicyChange{IdempotencyKey: "paths-policy", RequestFingerprint: strings.Repeat("f", 64), ActorRef: "test", Reason: "TEST", Now: now}); err != nil {
		t.Fatal(err)
	}
	rootA, rootB := qualityRepoRoot(t), qualityRepoRoot(t)
	held := qualityClaim(t, s, f, "held", []string{rootB}, [][]string{{"src"}}, now)
	if _, _, _, err := s.ReserveAndClaim(ctx, held); err != nil {
		t.Fatal(err)
	}
	in := qualityClaim(t, s, f, "rollback", []string{rootA, rootB}, [][]string{{"src"}, {"src/child"}}, now)
	if _, _, made, err := s.ReserveAndClaim(ctx, in); made || !errors.Is(err, domain.ErrPathReservationConflict) {
		t.Fatalf("partial reservation: made=%v err=%v", made, err)
	}
	for _, table := range []string{"workflow_task_path_reservations", "workflow_attempts", "scheduler_admissions", "budget_reservations"} {
		if n := qualityCount(t, db, "SELECT count(*) FROM "+table); n != 1 {
			t.Fatalf("%s count=%d", table, n)
		}
	}
	task, _, err := s.GetWorkflowTask(ctx, in.Attempt.WorkflowID, in.Attempt.TaskID)
	if err != nil || task.State != domain.WorkflowTaskStateReady || task.Revision != in.ExpectedTaskRevision {
		t.Fatalf("claim not rolled back: %+v %v", task, err)
	}
}

// 删除 common-dir 身份解析会允许同仓库的两个 linked worktree 以不同 Project 绕过路径占位。
func TestM4PathReservationLinkedWorktreeProjectsConflict(t *testing.T) {
	ctx, now := context.Background(), workflowStoreTime()
	s := newTestStore(t)
	f := sqlitetest.NewAdmissionFixture(t, s, func() time.Time { return now })
	root := t.TempDir()
	linked := filepath.Join(t.TempDir(), "linked")
	for _, args := range [][]string{{"init", root}, {"-C", root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "seed"}, {"-C", root, "worktree", "add", "-b", "fixture", linked}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	first := qualityClaim(t, s, f, "repo", []string{root}, [][]string{{"src"}}, now)
	second := qualityClaim(t, s, f, "linked", []string{linked}, [][]string{{"src/child"}}, now)
	if _, _, _, err := s.ReserveAndClaim(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.ReserveAndClaim(ctx, second); !errors.Is(err, domain.ErrPathReservationConflict) {
		t.Fatalf("linked project bypass: %v", err)
	}
}

// 删除保存路径的当前身份重查，会让缺失路径初始化 Git 后从 path: 改成 git: 而绕过旧占位。
func TestM4PathReservationMissingRepoInitializationKeepsLock(t *testing.T) {
	ctx, now := context.Background(), workflowStoreTime()
	s := newTestStore(t)
	f := sqlitetest.NewAdmissionFixture(t, s, func() time.Time { return now })
	root := filepath.Join(t.TempDir(), "future")
	first := qualityClaim(t, s, f, "before-init", []string{root}, [][]string{{"src"}}, now)
	if _, _, _, err := s.ReserveAndClaim(ctx, first); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "linked")
	for _, args := range [][]string{{"init", root}, {"-C", root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "seed"}, {"-C", root, "worktree", "add", "-b", "fixture", linked}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	second := qualityClaim(t, s, f, "after-init", []string{linked}, [][]string{{"src/child"}}, now)
	if _, _, _, err := s.ReserveAndClaim(ctx, second); !errors.Is(err, domain.ErrPathReservationConflict) {
		t.Fatalf("identity drift bypass: %v", err)
	}
}
