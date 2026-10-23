package session

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	searchsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/search"
)

// SearchWorkspace lists the files the command palette searches for a
// session: the same tracked and untracked, non-ignored paths the workspace
// browser shows, relative to the session's workspace root, without the git
// status and diff work ListWorkspaceFiles does on every call.
func (s *Service) SearchWorkspace(ctx context.Context, id domain.SessionID) (searchsvc.Workspace, error) {
	rec, err := s.sessionWorkspaceRecord(ctx, id)
	if err != nil {
		return searchsvc.Workspace{}, err
	}
	root := rec.Metadata.WorkspacePath
	project, projectOK, err := s.sessionProject(ctx, rec)
	if err != nil {
		return searchsvc.Workspace{}, err
	}
	kind := domain.ProjectKindSingleRepo
	if isStandaloneScratchWorkspace(rec) {
		kind = domain.ProjectKindScratch
	}
	if projectOK {
		kind = project.Kind.WithDefault()
	}
	switch kind {
	case domain.ProjectKindScratch:
		files, _, err := scratchWorkspaceFiles(root)
		if err != nil {
			return searchsvc.Workspace{}, err
		}
		paths := make([]string, 0, len(files))
		for _, f := range files {
			paths = append(paths, f.Path)
		}
		slices.Sort(paths)
		return searchsvc.Workspace{Root: root, Paths: paths}, nil
	case domain.ProjectKindWorkspace:
		rows, err := s.store.ListSessionWorktrees(ctx, rec.ID)
		if err != nil {
			return searchsvc.Workspace{}, fmt.Errorf("list workspace project rows: %w", err)
		}
		if len(rows) > 0 {
			return s.searchWorkspaceRepos(ctx, rec, rows)
		}
	}
	paths, err := s.workspacePaths(ctx, id, root)
	if err != nil {
		return searchsvc.Workspace{}, err
	}
	return searchsvc.Workspace{Root: root, Paths: dedupeSorted(paths)}, nil
}

// searchWorkspaceRepos joins each repository's paths under its folder in the
// workspace, as the workspace browser does: a repository checked out at the
// root excludes the folders that belong to the nested repositories.
func (s *Service) searchWorkspaceRepos(ctx context.Context, rec domain.SessionRecord, rows []domain.SessionWorktreeRecord) (searchsvc.Workspace, error) {
	root := rec.Metadata.WorkspacePath
	prefixes := workspaceProjectPrefixes(root, rows)
	children := nonEmptyWorkspacePrefixes(prefixes)
	var paths []string
	for _, row := range rows {
		if strings.TrimSpace(row.WorktreePath) == "" {
			continue
		}
		prefix, ok := prefixes[row.RepoName]
		if !ok {
			continue
		}
		repoPaths, err := s.workspacePaths(ctx, rec.ID, row.WorktreePath)
		if err != nil {
			return searchsvc.Workspace{}, err
		}
		for _, p := range repoPaths {
			if prefix == "" && underAnyPrefix(p, children) {
				continue
			}
			paths = append(paths, path.Join(prefix, p))
		}
	}
	return searchsvc.Workspace{Root: root, Paths: dedupeSorted(paths)}, nil
}

func underAnyPrefix(p string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

// dedupeSorted returns the non-empty paths, sorted, each once.
func dedupeSorted(paths []string) []string {
	out := slices.DeleteFunc(slices.Clone(paths), func(p string) bool { return p == "" })
	slices.Sort(out)
	return slices.Compact(out)
}
