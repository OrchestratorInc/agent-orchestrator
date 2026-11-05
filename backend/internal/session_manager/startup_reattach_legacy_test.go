package sessionmanager

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// 删除观察通道的 legacy 快照回退会拒绝旧 Host；删除 Attempt/Binding/Session 归属复验会接受途中失配。
func TestM3StartupReattachLegacyWorkflowDoesNotCreateAdmission(t *testing.T) {
	f, db := newStartupLegacyReattachFixture(t)
	ctx := context.Background()
	leader, err := f.scheduler.CurrentFence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.s.PrepareSessionReattach(ctx, f.rec, leader, f.now())
	if err != nil {
		t.Fatalf("legacy observation permit: %v", err)
	}
	if p.Admission.ID != "" || p.Execution.AdmissionID != "" {
		t.Fatal("legacy observation acquired an admission")
	}
	if _, err := db.Exec(`UPDATE workflow_attempts SET session_id=NULL WHERE id=?`, f.attempt.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ValidateSessionReattach(ctx, p, "", f.now()); !errors.Is(err, domain.ErrSchedulerAdmissionConflict) {
		t.Fatalf("detached attempt accepted: %v", err)
	}
	if _, err := db.Exec(`UPDATE workflow_attempts SET session_id=? WHERE id=?`, f.rec.ID, f.attempt.ID); err != nil {
		t.Fatal(err)
	}
	f.driver.before = func(ctx context.Context) error {
		if _, ok := ports.ExecutionPermitFromContext(ctx); ok {
			return errors.New("legacy observation became business execution")
		}
		workflow, err := f.m.reattachWorkflowContext(ctx, f.rec)
		if err != nil || workflow == nil || workflow.AttemptID != f.attempt.ID || workflow.WorkflowID != f.attempt.WorkflowID || workflow.TaskID != f.attempt.TaskID {
			return errors.New("legacy workflow identity was lost")
		}
		return nil
	}
	if err := f.m.checkSessionHealth(ctx, f.rec); err != nil {
		t.Fatal(err)
	}
	after, _, err := f.s.GetWorkflowAttempt(ctx, f.attempt.ID)
	if err != nil || !reflect.DeepEqual(f.attempt, after) {
		t.Fatalf("legacy attempt changed: %+v %v", after, err)
	}
	current, _, err := f.s.GetSession(ctx, f.rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	binding, _, err := f.s.GetWorkflowAttemptBinding(ctx, f.attempt.ID)
	if err != nil || binding.ControllerGeneration != current.Metadata.ControllerGeneration || binding.ControllerGeneration == f.rec.Metadata.ControllerGeneration || binding.Revision != p.Execution.BindingRevision+1 {
		t.Fatalf("legacy generation split: %+v %v", binding, err)
	}
	ads, err := f.s.ListSchedulerAdmissions(ctx, false)
	if err != nil || len(ads) != 0 {
		t.Fatalf("legacy observation created admissions: %+v %v", ads, err)
	}
	reservations, err := f.s.ListBudgetReservations(ctx, "")
	if err != nil || len(reservations) != 0 {
		t.Fatalf("legacy observation created budget: %+v %v", reservations, err)
	}
	if _, err := f.s.LoadExecutionPermit(ctx, f.attempt.ID, leader, f.now()); !errors.Is(err, domain.ErrAdmissionRequired) {
		t.Fatalf("legacy observation obtained business permit: %v", err)
	}
	if _, _, err := f.s.StartWorkflowAttempt(ports.WithExecutionPermit(ctx, p.Execution), f.attempt.WorkflowID, f.attempt.ID, f.attempt.LeaseToken, "legacy-start", strings.Repeat("c", 64), f.now()); !errors.Is(err, domain.ErrAdmissionRequired) {
		t.Fatalf("legacy observation started business execution: %v", err)
	}
	if f.driver.starts.Load()+f.driver.resumes.Load() != 0 || f.driver.reconnects.Load() != 1 || !f.chat.HasLiveChatController(f.rec.ID) {
		t.Fatal("legacy host was not exclusively reattached")
	}
}
