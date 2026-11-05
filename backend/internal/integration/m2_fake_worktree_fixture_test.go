package integration

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// m2FakeHeadSHA 是假工作区的明示状态，不从数据库 CreationSHA 推断实时 HEAD。
const m2FakeHeadSHA = "1111111111111111111111111111111111111111"

// recordWorktrees 写入假 Spawner 的持久化归属并保存独立快照；不运行 Git。
func (s *fakeSessionSpawner) recordWorktrees(
	ctx context.Context,
	rec domain.SessionRecord,
	cfg ports.SpawnConfig,
) error {
	rows := make([]domain.SessionWorktreeRecord, 0, len(cfg.Repos))
	if len(cfg.Repos) == 0 {
		rows = append(rows, domain.SessionWorktreeRecord{
			SessionID: rec.ID, RepoName: domain.RootWorkspaceRepoName,
			ProjectID: rec.ProjectID, RepoPath: s.workspace,
			Branch: rec.Metadata.Branch, BaseSHA: m2FakeHeadSHA, CreationSHA: m2FakeHeadSHA,
			WorktreePath: s.workspace, State: "active",
		})
	}
	for _, repo := range cfg.Repos {
		rows = append(rows, domain.SessionWorktreeRecord{
			SessionID: rec.ID, RepoName: repo.RepoKey,
			ProjectID: repo.ProjectID, RepoPath: s.workspace,
			Branch: rec.Metadata.Branch, BaseSHA: repo.BaseSHA, CreationSHA: m2FakeHeadSHA,
			WorktreePath: s.workspace + "/" + repo.RepoKey, State: "active",
		})
	}
	for _, row := range rows {
		if err := s.store.UpsertSessionWorktree(ctx, row); err != nil {
			return err
		}
	}
	s.worktreesMu.Lock()
	s.ownedWorktrees[rec.ID] = rows
	s.worktreesMu.Unlock()
	return nil
}

// restoreFakeWorktrees 为重建后的假 Spawner 恢复归属快照；真实 Git 恢复另有集成测试。
func (s *fakeSessionSpawner) restoreFakeWorktrees(ctx context.Context) error {
	sessions, err := s.store.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	for _, rec := range sessions {
		rows, err := s.store.ListSessionWorktrees(ctx, rec.ID)
		if err != nil {
			return err
		}
		s.ownedWorktrees[rec.ID] = rows
	}
	return nil
}

// ObserveSessionWorktrees 实现假工作区所有者的显式观察边界。
// 每次重读持久化行核对快照，固定 HEAD 只表示假状态，不能证明真实目录或 Git 存在。
func (s *fakeSessionSpawner) ObserveSessionWorktrees(
	ctx context.Context,
	id domain.SessionID,
) ([]ports.RepoWorktreeObservation, error) {
	rows, err := s.store.ListSessionWorktrees(ctx, id)
	if err != nil {
		return nil, err
	}
	s.worktreesMu.Lock()
	wanted := make(map[string]domain.SessionWorktreeRecord, len(s.ownedWorktrees[id]))
	for _, row := range s.ownedWorktrees[id] {
		wanted[row.RepoName] = row
	}
	s.worktreesMu.Unlock()
	if len(rows) == 0 || len(rows) != len(wanted) {
		return nil, fmt.Errorf("假工作区归属记录缺失: %s", id)
	}
	observed := make([]ports.RepoWorktreeObservation, 0, len(rows))
	for _, row := range rows {
		want, found := wanted[row.RepoName]
		if !found || row.ProjectID != want.ProjectID || row.RepoPath != want.RepoPath ||
			row.WorktreePath != want.WorktreePath || row.Branch != want.Branch ||
			row.BaseSHA != want.BaseSHA || row.CreationSHA != want.CreationSHA || row.State != want.State {
			return nil, fmt.Errorf("假工作区归属记录漂移: %s/%s", id, row.RepoName)
		}
		observed = append(observed, ports.RepoWorktreeObservation{
			RepoKey: domain.WorkflowRepoKey(row.RepoName), ProjectID: row.ProjectID,
			WorktreeRef: row.WorktreePath, Branch: row.Branch,
			HeadSHA: m2FakeHeadSHA, BaseSHA: row.BaseSHA,
		})
	}
	return observed, nil
}
