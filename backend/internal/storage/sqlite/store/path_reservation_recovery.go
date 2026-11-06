package store

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// recoverPathReservations 在开放准入前补齐旧 Admission，历史重叠按事实全部保留，不授予新执行权。
func recoverPathReservations(ctx context.Context, q *gen.Queries, f domain.SchedulerFence) error {
	if err := q.AdoptPathReservations(ctx, gen.AdoptPathReservationsParams{OwnerID: f.OwnerID, Epoch: f.Epoch, AdmissionID: ""}); err != nil {
		return err
	}
	admissions, err := q.ListSchedulerAdmissions(ctx, 1)
	if err != nil {
		return err
	}
	for _, row := range admissions {
		ad := schedulerAdmissionFromGen(row)
		if ad.AttemptID == "" {
			continue
		}
		a, err := q.GetWorkflowAttempt(ctx, ad.AttemptID)
		if err != nil {
			return err
		}
		if a.WorkflowID != ad.WorkflowID || a.TaskID != ad.TaskID {
			return domain.ErrSchedulerAdmissionConflict
		}
		ad.Fence = f
		want, err := taskPathReservations(ctx, q, workflowAttemptFromGen(a), ad)
		if err != nil {
			return err
		}
		existing, err := q.ListAttemptPathReservations(ctx, string(ad.AttemptID))
		if err != nil {
			return err
		}
		missing := false
		for _, expected := range want {
			found := false
			for _, r := range existing {
				if r.RepoKey == expected.RepoKey && r.ScopePath == expected.ScopePath {
					if r.AdmissionID != ad.ID || r.ProjectID != expected.ProjectID || !sameDeliveryPath(r.RepoPath, expected.RepoPath) {
						return domain.ErrPathReservationConflict
					}
					found = true
				}
			}
			if !found {
				if err := q.InsertPathReservation(ctx, expected); err != nil {
					return err
				}
				missing = true
			}
		}
		if missing && ad.SessionID != "" {
			binding, err := q.GetWorkflowAttemptBinding(ctx, ad.AttemptID)
			if err != nil {
				return err
			}
			if binding.WorkflowID != ad.WorkflowID || binding.TaskID != ad.TaskID || binding.SessionID == nil || *binding.SessionID != ad.SessionID {
				return domain.ErrSchedulerAdmissionConflict
			}
			session, err := q.GetSession(ctx, ad.SessionID)
			if err != nil {
				return err
			}
			generation := domain.SessionExecutionGeneration(getSessionRowToRecord(session))
			if generation == "" || binding.ControllerGeneration != generation || ad.ControllerGeneration != "" && ad.ControllerGeneration != generation || a.SessionID == nil || *a.SessionID != ad.SessionID {
				return domain.ErrSchedulerAdmissionConflict
			}
			worktrees, err := q.ListSessionWorktrees(ctx, ad.SessionID)
			if err != nil {
				return err
			}
			for _, expected := range want {
				matched := false
				for _, w := range worktrees {
					if w.RepoName == expected.RepoKey && w.ProjectID == expected.ProjectID && sameDeliveryPath(w.RepoPath, expected.RepoPath) {
						identity, err := reservationRepoIdentity(w.WorktreePath)
						matched = err == nil && identity == expected.RepoIdentity
					}
				}
				if !matched {
					return domain.ErrArtifactInvalid
				}
			}
			if err := bindPathReservations(ctx, q, ad.AttemptID, ad.SessionID, generation, f); err != nil {
				return err
			}
		}
	}
	return nil
}
