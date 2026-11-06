// Package coordination 只联接现有事实；不调度、不续租、不触发 Harness Probe。
package coordination

import (
	"context"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Store 读取既有 DAG、Attempt、Admission、预算、消息与路径占位。
type Store interface {
	GetSchedulerLeader(context.Context) (domain.SchedulerLeader, error)
	GetWorkflow(context.Context, domain.WorkflowID) (domain.Workflow, bool, error)
	ListWorkflowTasks(context.Context, domain.WorkflowID, domain.WorkflowTaskState, int64, int64) ([]domain.WorkflowTask, error)
	ListWorkflowEdges(context.Context, domain.WorkflowID) ([]domain.WorkflowEdge, error)
	ListWorkflowTaskRepositories(context.Context, domain.WorkflowID, domain.WorkflowTaskID) ([]domain.WorkflowTaskRepository, error)
	ListWorkflowAttempts(context.Context, domain.WorkflowID, domain.WorkflowTaskID) ([]domain.WorkflowAttempt, error)
	GetWorkflowAttemptBinding(context.Context, domain.WorkflowAttemptID) (domain.WorkflowAttemptBinding, bool, error)
	GetSchedulerAdmissionForAttempt(context.Context, domain.WorkflowAttemptID) (domain.SchedulerAdmission, bool, error)
	GetSessionCapabilitySnapshot(context.Context, domain.SessionID) (domain.HarnessCapabilitySnapshot, bool, error)
	GetHarnessCapabilitySnapshot(context.Context, domain.AgentHarness) (domain.HarnessCapabilitySnapshot, bool, error)
	ListBudgetReservations(context.Context, string) ([]domain.BudgetReservation, error)
	ListWorkflowPathReservations(context.Context, domain.WorkflowID) ([]domain.PathReservationView, error)
	RecentCoordinationMessages(context.Context, domain.WorkflowID) ([]domain.CoordinationMessageView, error)
	LatestTaskSchedulingReason(context.Context, domain.WorkflowID, domain.WorkflowTaskID) (string, error)
}

// Sessions 复用已有 Session 读取服务派生状态，协调摘要不能自行存储或推断运行时生死。
type Sessions interface {
	Get(context.Context, domain.SessionID) (domain.Session, error)
}

// Service 保留各来源的 revision/观察时间；它的结果从不用于执行授权。
type Service struct {
	Store    Store
	Sessions Sessions
	Clock    func() time.Time
}

// Read 有界读取任务和最近消息，不因为租约过期释放占位或把未知探测解释为终止。
func (s *Service) Read(ctx context.Context, w domain.WorkflowID) (domain.CoordinationSummaryView, error) {
	out := domain.CoordinationSummaryView{WorkflowID: w, Tasks: []domain.CoordinationTaskView{}}
	if s.Store == nil || s.Clock == nil {
		return out, domain.ErrDeliveryBlocked
	}
	out.ObservedAt = s.Clock()
	if _, found, err := s.Store.GetWorkflow(ctx, w); err != nil {
		return out, err
	} else if !found {
		return out, domain.ErrCollaborationWorkflowNotFound
	}
	tasks, err := s.Store.ListWorkflowTasks(ctx, w, "", 501, 0)
	if err != nil {
		return out, err
	}
	if len(tasks) > 500 {
		out.Truncated = true
		tasks = tasks[:500]
	}
	edges, err := s.Store.ListWorkflowEdges(ctx, w)
	if err != nil {
		return out, err
	}
	leader, err := s.Store.GetSchedulerLeader(ctx)
	if err != nil {
		return out, err
	}
	for _, task := range tasks {
		v := domain.CoordinationTaskView{TaskID: task.ID, State: task.State, Revision: task.Revision, Harness: task.AgentHarness, Dependencies: []domain.WorkflowTaskID{}, SessionStatus: "unknown", SessionReadiness: "unavailable", CapabilityReadiness: "unknown", BudgetQuality: "UNKNOWN", BudgetReservations: []domain.CoordinationBudgetView{}}
		v.Repositories, err = s.Store.ListWorkflowTaskRepositories(ctx, w, task.ID)
		if err != nil {
			return out, err
		}
		for _, e := range edges {
			if e.ToTaskID == task.ID {
				v.Dependencies = append(v.Dependencies, e.FromTaskID)
			}
		}
		v.BlockedReason, err = s.Store.LatestTaskSchedulingReason(ctx, w, task.ID)
		if err != nil {
			return out, err
		}
		attempts, err := s.Store.ListWorkflowAttempts(ctx, w, task.ID)
		if err != nil {
			return out, err
		}
		if len(attempts) > 0 {
			if err := s.attempt(ctx, &v, attempts[0], leader, out.ObservedAt); err != nil {
				return out, err
			}
		}
		out.Tasks = append(out.Tasks, v)
	}
	out.Reservations, err = s.Store.ListWorkflowPathReservations(ctx, w)
	if err != nil {
		return out, err
	}
	out.Messages, err = s.Store.RecentCoordinationMessages(ctx, w)
	return out, err
}

// attempt 只从最新 Attempt 关联的真实记录读取模型、预算与能力，旧代际能力显示 unknown。
func (s *Service) attempt(ctx context.Context, v *domain.CoordinationTaskView, a domain.WorkflowAttempt, leader domain.SchedulerLeader, now time.Time) error {
	v.AttemptID, v.AttemptState, v.AttemptNo = a.ID, a.State, a.AttemptNo
	if a.LeaseUntil != nil {
		v.LeaseUntil = *a.LeaseUntil
	}
	if a.HeartbeatAt != nil {
		v.HeartbeatAt = *a.HeartbeatAt
	}
	if a.ErrorCode != "" {
		v.BlockedReason = a.ErrorCode
	}
	b, found, err := s.Store.GetWorkflowAttemptBinding(ctx, a.ID)
	if err != nil {
		return err
	}
	if found {
		v.BindingState = string(b.State)
	}
	if a.SessionID != nil {
		v.SessionID = *a.SessionID
	}
	ad, adFound, err := s.Store.GetSchedulerAdmissionForAttempt(ctx, a.ID)
	if err != nil {
		return err
	}
	if adFound {
		v.AdmissionState, v.AdmissionRevision = ad.State, ad.Revision
		reservations, err := s.Store.ListBudgetReservations(ctx, ad.ID)
		if err != nil {
			return err
		}
		known := len(reservations) > 0
		for _, r := range reservations {
			if r.AmountNanos == nil {
				known = false
			}
			v.BudgetReservations = append(v.BudgetReservations, domain.CoordinationBudgetView{PolicyID: r.PolicyID, Window: r.Window, AmountNanos: r.AmountNanos, State: r.State, Revision: r.Revision})
		}
		if known {
			v.BudgetQuality = "ESTIMATED"
		}
	}
	if v.SessionID == "" || s.Sessions == nil {
		return nil
	}
	session, err := s.Sessions.Get(ctx, v.SessionID)
	if err != nil {
		var apiError *apierr.Error
		// 跨来源读取时 Session 可能已不可用；缺失不证明终止，存储故障仍须上抛。
		if errors.As(err, &apiError) && apiError.Kind == apierr.KindNotFound {
			return nil
		}
		return err
	}
	v.Model, v.SessionStatus, v.SessionReadiness = session.Metadata.Model, string(session.Status), session.StatusReadiness
	v.LeaseValid = v.LeaseUntil.After(now) && (a.State == domain.WorkflowAttemptStateClaimed || a.State == domain.WorkflowAttemptStateRunning) &&
		adFound && ad.State == domain.AdmissionActive && leader.Recovered && leader.LeaseUntil.After(now) && ad.Fence == leader.Fence &&
		!session.IsTerminated && found && b.SessionID != nil && *b.SessionID == session.ID && ad.SessionID == session.ID &&
		(b.State == domain.WorkflowBindingStateBoundReady || b.State == domain.WorkflowBindingStateRunning) &&
		b.ControllerGeneration != "" && b.ControllerGeneration == session.Metadata.ControllerGeneration && ad.ControllerGeneration == b.ControllerGeneration
	capability, capFound, err := s.Store.GetSessionCapabilitySnapshot(ctx, v.SessionID)
	if err != nil {
		return err
	}
	if !capFound {
		capability, capFound, err = s.Store.GetHarnessCapabilitySnapshot(ctx, v.Harness)
		if err != nil {
			return err
		}
	}
	if capFound {
		v.CapabilitySource, v.CapabilityExpiresAt = string(capability.Source), capability.ExpiresAt
		if capability.ExpiresAt.After(now) && (capability.ObservedSessionID == "" || capability.ObservedSessionID == v.SessionID && capability.Generation == session.Metadata.ControllerGeneration) {
			v.CapabilityReadiness = string(capability.Readiness)
		}
	}
	return nil
}
