package store

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// canonicalProjectPath 使用物理项目路径统一别名；不存在的历史项目仍按绝对路径保守占位。
func canonicalProjectPath(value string) (string, error) {
	if strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0) {
		return "", domain.ErrArtifactInvalid
	}
	p, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	physical, err := filepath.EvalSymlinks(p)
	if err == nil {
		p = physical
	} else if !os.IsNotExist(err) {
		return "", err
	}
	p = filepath.ToSlash(filepath.Clean(p))
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p, nil
}

// reservationScope 按路径组件规范化 scope，拒绝 Windows 会隐式改写的尾点与尾空格。
func reservationScope(value string) (string, error) {
	p, ok := domain.CanonicalWorkflowPath(value, true)
	if !ok {
		return "", domain.ErrArtifactInvalid
	}
	if runtime.GOOS == "windows" {
		for _, part := range strings.Split(p, "/") {
			if strings.TrimRight(part, ". ") != part {
				return "", domain.ErrArtifactInvalid
			}
		}
		p = strings.ToLower(p)
	}
	return p, nil
}

// pathScopesOverlap 以组件前缀比较；空 scope 表示整仓，src 不会误伤 src-other。
func pathScopesOverlap(a, b string) bool {
	return a == "" || b == "" || a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// reserveTaskPaths 与 Claim、预算共享 writer 事务；所有仓库都成功才允许事务提交。
func reserveTaskPaths(ctx context.Context, q *gen.Queries, a domain.WorkflowAttempt, ad domain.SchedulerAdmission) error {
	rows, err := taskPathReservations(ctx, q, a, ad)
	if err != nil || len(rows) == 0 {
		return err
	}
	occupied, err := q.ListOccupiedPathReservations(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		for _, other := range occupied {
			current, err := reservationRepoIdentity(other.RepoPath)
			if err != nil {
				return err
			}
			if (other.RepoIdentity == row.RepoIdentity || current == row.RepoIdentity || sameDeliveryPath(other.RepoPath, row.RepoPath)) && pathScopesOverlap(other.ScopePath, row.ScopePath) {
				return domain.ErrPathReservationConflict
			}
		}
		if err := q.InsertPathReservation(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

// taskPathReservations 统一从持久 TaskRepo/Project 派生 scope，供新 Claim 与升级恢复共用。
func taskPathReservations(ctx context.Context, q *gen.Queries, a domain.WorkflowAttempt, ad domain.SchedulerAdmission) ([]gen.InsertPathReservationParams, error) {
	tasks, err := q.ListWorkflowTaskRepositories(ctx, gen.ListWorkflowTaskRepositoriesParams{WorkflowID: a.WorkflowID, TaskID: a.TaskID})
	if err != nil || len(tasks) == 0 {
		return nil, err
	}
	repos, err := q.ListWorkflowRepositories(ctx, a.WorkflowID)
	if err != nil {
		return nil, err
	}
	lease := a.UpdatedAt
	if a.LeaseUntil != nil {
		lease = *a.LeaseUntil
	}
	var result []gen.InsertPathReservationParams
	for _, task := range tasks {
		var repo *gen.WorkflowRepository
		for i := range repos {
			if repos[i].RepoKey == task.RepoKey {
				repo = &repos[i]
			}
		}
		if repo == nil {
			return nil, domain.ErrArtifactInvalid
		}
		project, err := q.GetProject(ctx, repo.ProjectID)
		if err != nil {
			return nil, err
		}
		repoPath, err := deliveryRepoPath(project.Path, repo.PathRef)
		if err != nil {
			return nil, err
		}
		identity, err := reservationRepoIdentity(repoPath)
		if err != nil {
			return nil, err
		}
		var scopes []string
		if err := json.Unmarshal([]byte(task.AllowedPathsJson), &scopes); err != nil {
			return nil, err
		}
		if len(scopes) == 0 {
			scopes = []string{""}
		}
		seen := map[string]bool{}
		for _, value := range scopes {
			scope, err := reservationScope(value)
			if err != nil {
				return nil, err
			}
			if seen[scope] {
				continue
			}
			seen[scope] = true
			result = append(result, gen.InsertPathReservationParams{
				AdmissionID: ad.ID, AttemptID: string(a.ID), WorkflowID: string(a.WorkflowID), TaskID: string(a.TaskID),
				RepoKey: string(task.RepoKey), ProjectID: string(repo.ProjectID), RepoPath: repoPath, RepoIdentity: identity, ScopePath: scope,
				OwnerID: ad.Fence.OwnerID, Epoch: ad.Fence.Epoch, LeaseUntil: lease,
			})
		}
	}
	return result, nil
}

// bindPathReservations 跟随已验证的 Session 创建或代际 CAS；不同任期与 Session 不能偷换占位。
func bindPathReservations(ctx context.Context, q *gen.Queries, attempt domain.WorkflowAttemptID, session domain.SessionID, generation string, f domain.SchedulerFence) error {
	rows, err := q.ListAttemptPathReservations(ctx, string(attempt))
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.ReleasedAt.Valid {
			return domain.ErrPathReservationConflict
		}
		n, err := q.BindPathReservation(ctx, gen.BindPathReservationParams{ID: r.ID, Revision: r.Revision,
			SessionID: string(session), Generation: generation, OwnerID: f.OwnerID, Epoch: f.Epoch})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrSchedulerFence
		}
	}
	return nil
}

// RecoverPathReservations 接管所有遗留占位，包括 Admission 已释放的行；不依据 TTL 或 UNKNOWN 释放。
func (s *Store) RecoverPathReservations(ctx context.Context, f domain.SchedulerFence, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inSchedulerTx(ctx, "recover path reservations", func(q *gen.Queries) error {
		if err := schedulerFence(ctx, q, f, now, false); err != nil {
			return err
		}
		return recoverPathReservations(ctx, q, f)
	})
}

// deliveryRepoPath 从注册项目及持久 PathRef 派生仓库路径，调用方不能自报项目身份。
func deliveryRepoPath(projectPath, ref string) (string, error) {
	clean, err := reservationScope(ref)
	if err != nil {
		return "", err
	}
	return canonicalProjectPath(filepath.Join(projectPath, filepath.FromSlash(path.Clean(clean))))
}
