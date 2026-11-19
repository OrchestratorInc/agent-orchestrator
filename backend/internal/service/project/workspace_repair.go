package project

import (
	"context"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// RepairWorkspaceAssets drops the copied-folder rows earlier releases
// registered for folders sessions should never receive: dot-folders,
// dependency and build folders, and folders the parent repository tracks or
// ignores. Rows are written once, at registration, so without this a project
// keeps copying them into every session until it is re-added. A second pass
// finds nothing left to drop. Run it in the background at startup.
func (m *Service) RepairWorkspaceAssets(ctx context.Context) {
	rows, err := m.store.ListProjects(ctx)
	if err != nil {
		m.logger.WarnContext(ctx, "workspace project: could not list projects to repair copied folders", "error", err)
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		if row.Kind.WithDefault() == domain.ProjectKindWorkspace {
			m.repairWorkspaceAssets(ctx, row)
		}
	}
}

func (m *Service) repairWorkspaceAssets(ctx context.Context, row domain.ProjectRecord) {
	// Add replaces this project's rows wholesale; do not interleave with it.
	m.addMu.Lock()
	defer m.addMu.Unlock()
	if info, err := os.Stat(row.Path); err != nil || !info.IsDir() {
		// The folder may sit on a volume that is not mounted yet; try again on
		// the next start rather than judging folders that cannot be seen.
		return
	}
	repos, err := m.store.ListWorkspaceRepos(ctx, row.ID)
	if err != nil {
		m.logger.WarnContext(ctx, "workspace project: could not list folders to repair", "projectID", row.ID, "error", err)
		return
	}
	var tracked map[string]bool
	if isGitRepo(row.Path) {
		tracked = workspaceParentTrackedDirs(ctx, row.Path)
	}
	var drop []string
	for _, repo := range repos {
		if repo.GitStatus != domain.GitStatusNeedsInit {
			continue
		}
		dir := filepath.Join(row.Path, filepath.FromSlash(repo.RelativePath))
		if hasGitMetadata(dir) {
			continue // a repository waiting for setup; never copied
		}
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		reason := workspaceFolderSkipReason(ctx, row.Path, repo.RelativePath, tracked)
		if reason == "" {
			continue
		}
		drop = append(drop, repo.Name)
		m.logger.InfoContext(ctx, "workspace project: folder is no longer copied into sessions",
			"projectID", row.ID, "folder", repo.RelativePath, "reason", reason)
	}
	if len(drop) == 0 {
		return
	}
	if err := m.store.RemoveWorkspaceRepos(ctx, row.ID, drop); err != nil {
		m.logger.WarnContext(ctx, "workspace project: could not drop copied folders", "projectID", row.ID, "error", err)
	}
}
