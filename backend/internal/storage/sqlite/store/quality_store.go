package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
	"github.com/google/uuid"
)

// SaveArtifact 冻结不可变快照；幂等键为 Attempt、Repo、ArtifactRevision，重放不会恢复旧证据。
func (s *Store) SaveArtifact(ctx context.Context, a domain.ArtifactSnapshot, f domain.SchedulerFence, now time.Time) (domain.ArtifactSnapshot, bool, error) {
	if a.ArtifactRevision < 1 || a.Fingerprint != a.Digest() || (a.State != "" && a.State != "verified") {
		return domain.ArtifactSnapshot{}, false, domain.ErrArtifactInvalid
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var result domain.ArtifactSnapshot
	created := false
	err := s.inSchedulerTx(ctx, "save delivery artifact", func(q *gen.Queries) error {
		if err := validateArtifactIdentity(ctx, q, a, f, now); err != nil {
			return err
		}
		old, err := q.GetArtifactVersion(ctx, gen.GetArtifactVersionParams{AttemptID: string(a.AttemptID), RepoKey: string(a.Repo.RepoKey), ArtifactRevision: a.ArtifactRevision})
		if err == nil {
			if old.Fingerprint != a.Fingerprint {
				return domain.ErrArtifactStale
			}
			result, err = artifactFromRow(old)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		latest, err := q.GetLatestArtifact(ctx, gen.GetLatestArtifactParams{AttemptID: string(a.AttemptID), RepoKey: string(a.Repo.RepoKey)})
		if err == nil && latest.ArtifactRevision >= a.ArtifactRevision {
			return domain.ErrArtifactStale
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		pending, err := q.HasPendingTaskCommit(ctx, string(a.WorkflowID))
		if err != nil {
			return err
		}
		if pending {
			return domain.ErrDeliveryBlocked
		}
		if err := artifactReservation(ctx, q, a, f); err != nil {
			return err
		}
		if a.ID == "" {
			a.ID = "artifact-" + uuid.NewString()
		}
		a.State, a.Revision, a.CreatedAt = "verified", 1, now.UTC()
		data, err := json.Marshal(a)
		if err != nil {
			return err
		}
		if err := q.StalePreviousArtifacts(ctx, gen.StalePreviousArtifactsParams{AttemptID: string(a.AttemptID), RepoKey: string(a.Repo.RepoKey)}); err != nil {
			return err
		}
		if err := q.InsertArtifact(ctx, gen.InsertArtifactParams{ID: a.ID, WorkflowID: string(a.WorkflowID), TaskID: string(a.TaskID), AttemptID: string(a.AttemptID),
			SessionID: string(a.SessionID), Generation: a.Generation, RepoKey: string(a.Repo.RepoKey), ArtifactRevision: a.ArtifactRevision,
			Fingerprint: a.Fingerprint, SnapshotJson: string(data), RepoPath: a.Repo.RepoPath, WorktreePath: a.Repo.WorktreePath, GitCommonDir: a.Repo.GitCommonDir, CreatedAt: a.CreatedAt}); err != nil {
			return err
		}
		result, created = a, true
		return nil
	})
	if err != nil {
		return domain.ArtifactSnapshot{}, false, err
	}
	return result, created, nil
}

// GetArtifact 恢复显示 JSON 隐藏的本地路径，保留重启后的完整指纹身份。
func (s *Store) GetArtifact(ctx context.Context, id string) (domain.ArtifactSnapshot, bool, error) {
	r, err := s.qr.GetArtifact(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ArtifactSnapshot{}, false, nil
	}
	if err != nil {
		return domain.ArtifactSnapshot{}, false, err
	}
	a, err := artifactFromRow(r)
	return a, err == nil, err
}

// ListArtifacts 按仓库版本返回持久化快照，不把失效工件误投影为可交付。
func (s *Store) ListArtifacts(ctx context.Context, workflowID domain.WorkflowID) ([]domain.ArtifactSnapshot, error) {
	rows, err := s.qr.ListArtifacts(ctx, string(workflowID))
	if err != nil {
		return nil, err
	}
	result := make([]domain.ArtifactSnapshot, 0, len(rows))
	for _, r := range rows {
		a, err := artifactFromRow(r)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, nil
}

// TransitionArtifact 仅开放撤销可信性的 CAS；Gate/Review 证据未实现前没有任何放行边。
func (s *Store) TransitionArtifact(ctx context.Context, id string, expectedRevision int64, expectedFingerprint, from, to string, f domain.SchedulerFence, now time.Time) (domain.ArtifactSnapshot, bool, error) {
	if from != "verified" || to != "stale" {
		return domain.ArtifactSnapshot{}, false, domain.ErrDeliveryBlocked
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var a domain.ArtifactSnapshot
	err := s.inSchedulerTx(ctx, "transition delivery artifact", func(q *gen.Queries) error {
		r, err := q.GetArtifact(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrArtifactStale
		}
		if err != nil {
			return err
		}
		a, err = artifactFromRow(r)
		if err != nil {
			return err
		}
		if err := validateArtifactIdentity(ctx, q, a, f, now); err != nil {
			return err
		}
		pending, err := q.HasPendingTaskCommit(ctx, string(a.WorkflowID))
		if err != nil {
			return err
		}
		if pending {
			return domain.ErrDeliveryBlocked
		}
		n, err := q.TransitionArtifact(ctx, gen.TransitionArtifactParams{ID: id, ExpectedRevision: expectedRevision, Fingerprint: expectedFingerprint, ExpectedState: from, NextState: to})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrArtifactStale
		}
		a.State, a.Revision = to, a.Revision+1
		return nil
	})
	if err != nil {
		return domain.ArtifactSnapshot{}, false, err
	}
	return a, true, nil
}

// artifactFromRow 将不可变载荷与独立路径重组，并检测损坏或不完整的持久化身份。
func artifactFromRow(r gen.WorkflowArtifact) (domain.ArtifactSnapshot, error) {
	var a domain.ArtifactSnapshot
	if err := json.Unmarshal([]byte(r.SnapshotJson), &a); err != nil {
		return a, err
	}
	a.Repo.RepoPath, a.Repo.WorktreePath, a.Repo.GitCommonDir = r.RepoPath, r.WorktreePath, r.GitCommonDir
	a.Generation = r.Generation
	a.State, a.Revision, a.CreatedAt = r.State, r.Revision, r.CreatedAt
	if a.Digest() != r.Fingerprint || a.Fingerprint != r.Fingerprint || a.ID != r.ID || string(a.AttemptID) != r.AttemptID || string(a.WorkflowID) != r.WorkflowID || string(a.TaskID) != r.TaskID || string(a.SessionID) != r.SessionID || a.Generation != r.Generation || string(a.Repo.RepoKey) != r.RepoKey || a.ArtifactRevision != r.ArtifactRevision {
		return domain.ArtifactSnapshot{}, domain.ErrArtifactInvalid
	}
	return a, nil
}
