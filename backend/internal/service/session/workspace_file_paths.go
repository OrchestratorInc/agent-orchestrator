package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// WorkspaceFilePaths is a session workspace's file paths, without file details.
type WorkspaceFilePaths struct {
	SessionID domain.SessionID
	Paths     []string
	Truncated bool
}

const maxWorkspaceFilePaths = 20000

type workspacePathSource struct {
	root    string
	prefix  string
	exclude []string
	resolve func(context.Context) workspaceCompareTarget
}

// ListWorkspaceFilePaths lists the session workspace's file paths.
func (s *Service) ListWorkspaceFilePaths(ctx context.Context, id domain.SessionID) (WorkspaceFilePaths, error) {
	rec, sources, scratch, err := s.workspacePathSources(ctx, id)
	if err != nil {
		return WorkspaceFilePaths{}, err
	}
	if scratch {
		files, truncated, err := scratchWorkspaceFiles(rec.Metadata.WorkspacePath)
		if err != nil {
			return WorkspaceFilePaths{}, err
		}
		paths := make([]string, len(files))
		for i, file := range files {
			paths[i] = file.Path
		}
		return WorkspaceFilePaths{SessionID: id, Paths: paths, Truncated: truncated}, nil
	}
	paths, truncated, err := s.workspaceSourcePaths(ctx, id, sources, maxWorkspaceFilePaths, false)
	if err != nil {
		return WorkspaceFilePaths{}, err
	}
	return WorkspaceFilePaths{SessionID: id, Paths: paths, Truncated: truncated}, nil
}

func (s *Service) workspacePathSources(ctx context.Context, id domain.SessionID) (domain.SessionRecord, []workspacePathSource, bool, error) {
	rec, err := s.sessionWorkspaceRecord(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, nil, false, err
	}
	project, projectOK, err := s.sessionProject(ctx, rec)
	if err != nil {
		return domain.SessionRecord{}, nil, false, err
	}
	projectKind := domain.ProjectKindSingleRepo
	if isStandaloneScratchWorkspace(rec) {
		projectKind = domain.ProjectKindScratch
	}
	if projectOK {
		projectKind = project.Kind.WithDefault()
	}
	if projectKind == domain.ProjectKindScratch {
		return rec, nil, true, nil
	}
	var rows []domain.SessionWorktreeRecord
	if projectKind == domain.ProjectKindWorkspace {
		rows, err = s.store.ListSessionWorktrees(ctx, rec.ID)
		if err != nil {
			return domain.SessionRecord{}, nil, false, fmt.Errorf("list workspace project rows: %w", err)
		}
	}
	if len(rows) == 0 {
		prs, err := s.workspaceComparePRs(ctx, rec.ID)
		if err != nil {
			return domain.SessionRecord{}, nil, false, err
		}
		resolve := func(rctx context.Context) workspaceCompareTarget {
			return resolveWorkspaceCompare(rctx, rec.Metadata.WorkspacePath, rec.Metadata.DiffBaseSHA, rec.Metadata.DiffBaseRef, defaultBranchForProject(project, projectOK), prs)
		}
		return rec, []workspacePathSource{{root: rec.Metadata.WorkspacePath, resolve: resolve}}, false, nil
	}
	prefixes := workspaceProjectPrefixes(rec.Metadata.WorkspacePath, rows)
	childPrefixes := nonEmptyWorkspacePrefixes(prefixes)
	defaultBranch := defaultBranchForProject(project, true)
	sources := make([]workspacePathSource, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row.WorktreePath) == "" {
			continue
		}
		prefix, ok := prefixes[row.RepoName]
		if !ok {
			continue
		}
		source := workspacePathSource{root: row.WorktreePath, prefix: prefix}
		if prefix == "" {
			source.exclude = childPrefixes
		}
		baseRef := row.BaseRef
		if strings.TrimSpace(baseRef) == "" {
			baseRef = defaultBranch
		}
		source.resolve = func(rctx context.Context) workspaceCompareTarget {
			return resolveWorkspaceProjectCompare(rctx, row.WorktreePath, row.BaseSHA, baseRef)
		}
		sources = append(sources, source)
	}
	return rec, sources, false, nil
}

func (s *Service) workspaceSourcePaths(ctx context.Context, id domain.SessionID, sources []workspacePathSource, limit int, includeDeleted bool) ([]string, bool, error) {
	inventories := make([][]string, len(sources))
	total := 0
	for i, source := range sources {
		parts, err := s.workspacePaths(ctx, id, source.root)
		if err != nil {
			return nil, false, err
		}
		inventories[i] = parts
		total += len(parts)
	}
	changed, unchanged := make([]string, 0), make([]string, 0, total)
	seen := map[string]struct{}{}
	for i, source := range sources {
		var changes workspaceChangeSet
		if includeDeleted || total > limit {
			_, resolved, err := s.resolveWorkspaceChanges(ctx, id, source.root, source.resolve)
			if err != nil {
				return nil, false, err
			}
			changes = resolved
		}
		add := func(part string, into *[]string) {
			rel := filepath.ToSlash(part)
			if rel == "" || workspacePathExcluded(rel, source.exclude) {
				return
			}
			if !includeDeleted {
				if _, err := os.Lstat(filepath.Join(source.root, filepath.FromSlash(rel))); err != nil {
					return
				}
			}
			path := joinWorkspaceRelative(source.prefix, rel)
			if _, ok := seen[path]; ok {
				return
			}
			seen[path] = struct{}{}
			*into = append(*into, path)
		}
		for rel := range changes.changedPaths() {
			add(rel, &changed)
		}
		for _, rel := range inventories[i] {
			add(rel, &unchanged)
		}
	}
	sort.Strings(changed)
	paths := slices.Concat(changed, unchanged)
	truncated := len(paths) > limit
	if truncated {
		paths = paths[:limit]
	}
	sort.Strings(paths)
	return paths, truncated, nil
}

func workspacePathSourceFor(sources []workspacePathSource, path string) (workspacePathSource, string, bool) {
	best := -1
	for i, source := range sources {
		if source.prefix != "" && (!strings.HasPrefix(path, source.prefix+"/") || len(path) == len(source.prefix)+1) {
			continue
		}
		if best < 0 || len(source.prefix) > len(sources[best].prefix) {
			best = i
		}
	}
	if best < 0 {
		return workspacePathSource{}, "", false
	}
	source := sources[best]
	if source.prefix == "" {
		return source, path, true
	}
	return source, strings.TrimPrefix(path, source.prefix+"/"), true
}

func (s *Service) workspaceSourceFileSummary(ctx context.Context, id domain.SessionID, sources []workspacePathSource, path string) (WorkspaceFileSummary, error) {
	source, rel, ok := workspacePathSourceFor(sources, path)
	if !ok {
		return WorkspaceFileSummary{}, fmt.Errorf("no worktree owns workspace path %q", path)
	}
	_, changes, err := s.resolveWorkspaceChanges(ctx, id, source.root, source.resolve)
	if err != nil {
		return WorkspaceFileSummary{}, err
	}
	status := changes.statuses[rel]
	if status == "" {
		status = WorkspaceFileUnmodified
	}
	size, binary := workspaceFileSizeAndBinary(source.root, rel, status)
	file := WorkspaceFileSummary{
		Path:         path,
		PreviousPath: joinWorkspaceRelative(source.prefix, changes.previous[rel]),
		Status:       status,
		Additions:    changes.counts[rel][0],
		Deletions:    changes.counts[rel][1],
		Size:         size,
		Binary:       binary,
	}
	file.FileFingerprint = summaryFingerprint(file)
	return file, nil
}
