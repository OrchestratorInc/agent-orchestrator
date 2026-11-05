package principal_test

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// identityStore 提供同一 Session 的持久身份链；读计数验证 capability 失败不能探测绑定。
type identityStore struct {
	session      domain.SessionRecord
	binding      domain.WorkflowAttemptBinding
	attempt      domain.WorkflowAttempt
	admissions   []domain.SchedulerAdmission
	leader       domain.SchedulerLeader
	bindingReads int
	sessionReads int
	firstSession *domain.SessionRecord
	err          error
}

func identityFixture() *identityStore {
	session := sessionWithVerifier("session-1", "project-1", agentToken)
	session.Metadata.ControllerGeneration = "generation-1"
	until := time.Now().Add(time.Hour)
	fence := domain.SchedulerFence{OwnerID: "daemon-1", Epoch: 2}
	return &identityStore{
		session:    session,
		binding:    domain.WorkflowAttemptBinding{AttemptID: "attempt-1", WorkflowID: "workflow-1", TaskID: "task-1", SessionID: &session.ID, ControllerGeneration: "generation-1", State: domain.WorkflowBindingStateBoundReady, Revision: 1},
		attempt:    domain.WorkflowAttempt{ID: "attempt-1", WorkflowID: "workflow-1", TaskID: "task-1", SessionID: &session.ID, WorkerID: "worker-1", LeaseToken: "internal-lease", LeaseUntil: &until, State: domain.WorkflowAttemptStateClaimed, UpdatedAt: time.Now()},
		admissions: []domain.SchedulerAdmission{{ID: "admission-1", AttemptID: "attempt-1", WorkflowID: "workflow-1", TaskID: "task-1", SessionID: session.ID, ProjectID: session.ProjectID, Fence: fence, State: domain.AdmissionActive}},
		leader:     domain.SchedulerLeader{Fence: fence, LeaseUntil: until, Recovered: true},
	}
}

// GetSession 在首读后切换快照，重现 capability 校验与 Controller 轮换交错。
func (s *identityStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	s.sessionReads++
	if s.sessionReads == 1 && s.firstSession != nil {
		return *s.firstSession, true, nil
	}
	return s.session, s.session.ID == id, nil
}

func (s *identityStore) GetWorkflowAttemptBindingBySession(_ context.Context, _ domain.SessionID) (domain.WorkflowAttemptBinding, bool, error) {
	s.bindingReads++
	return s.binding, s.binding.AttemptID != "", s.err
}

func (s *identityStore) GetWorkflowAttempt(_ context.Context, id domain.WorkflowAttemptID) (domain.WorkflowAttempt, bool, error) {
	return s.attempt, s.attempt.ID == id, nil
}

func (s *identityStore) ListSchedulerAdmissions(_ context.Context, _ bool) ([]domain.SchedulerAdmission, error) {
	return s.admissions, nil
}

func (s *identityStore) GetSchedulerLeader(_ context.Context) (domain.SchedulerLeader, error) {
	return s.leader, nil
}
