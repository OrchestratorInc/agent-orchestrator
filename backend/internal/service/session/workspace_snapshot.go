package session

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// snapshotSelection uses the same validated comparison as the review, including
// synthetic trees produced by combining non-adjacent commits. No worktree reads.
func (s *Service) snapshotSelection(ctx context.Context, id domain.SessionID, input WorkspaceSelectionRevisionInput) (WorkspaceCommitSelection, error) {
	shas, err := selectionCommitSHAs(input.CommitSHA, input.CommitSHAs)
	if err != nil {
		return WorkspaceCommitSelection{}, err
	}
	var selection WorkspaceCommitSelection
	if input.View.ExpectedTipSHA != "" {
		selection, err = s.pinnedSnapshotSelection(ctx, id, input.View, shas)
	} else {
		selection, _, err = s.workspaceSelection(ctx, id, input.View, shas, "", false)
	}
	if err != nil {
		return WorkspaceCommitSelection{}, err
	}
	if !selection.Combinable {
		return WorkspaceCommitSelection{}, commitsNotCombinable(selection.Conflict)
	}
	// An empty comparison still has a complete repository at its tip.
	if selection.after == "" {
		selection.after = selection.View.TipSHA
	}
	if selection.root == "" || selection.after == "" {
		return WorkspaceCommitSelection{}, apierr.NotFound("WORKSPACE_SNAPSHOT_UNAVAILABLE", "This session has no repository snapshot")
	}
	return selection, nil
}

// ListWorkspaceSnapshotTree lists tracked paths at the review's after revision.
// A filter searches the entire snapshot; otherwise only one directory is read.
func (s *Service) ListWorkspaceSnapshotTree(ctx context.Context, id domain.SessionID, input WorkspaceSelectionRevisionInput, filter string) (WorkspaceTree, error) {
	ctx, cancel := context.WithTimeout(ctx, workspaceReviewTimeout)
	defer cancel()
	dir, err := cleanWorkspaceRelativeDir(input.Path)
	if err != nil {
		return WorkspaceTree{}, err
	}
	selection, err := s.snapshotSelection(ctx, id, input)
	if err != nil {
		return WorkspaceTree{}, err
	}
	spec := selection.after
	if dir != "" {
		spec += ":" + dir
	}
	args := []string{"ls-tree", "-z", "-l"}
	if filter != "" {
		args = append(args, "-r")
	}
	args = append(args, spec)
	out, truncated, err := gitWorkspaceOutputCapped(ctx, selection.root, maxWorkspaceDiffGroupBytes, args...)
	if err != nil {
		return WorkspaceTree{}, apierr.NotFound("WORKSPACE_SNAPSHOT_DIRECTORY_NOT_FOUND", "Directory is unavailable in this snapshot")
	}
	result := WorkspaceTree{Snapshot: true, SessionID: id, Path: dir, Entries: []WorkspaceTreeEntry{}, Truncated: truncated}
	statuses := make(map[string]WorkspaceFileStatus, len(selection.Files))
	for _, file := range selection.Files {
		statuses[file.Path] = file.Status
	}
	records := strings.Split(out, "\x00")
	if truncated {
		records = records[:len(records)-1]
	}
	for _, record := range records {
		metadata, name, ok := strings.Cut(record, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(metadata)
		if len(fields) != 4 {
			continue
		}
		rel := joinWorkspaceRelative(dir, name)
		if filter != "" && !strings.Contains(strings.ToLower(rel), strings.ToLower(filter)) {
			continue
		}
		entry := WorkspaceTreeEntry{Name: name, Path: rel, Type: WorkspaceTreeFile, Status: statuses[rel]}
		if fields[1] == "tree" {
			entry.Type = WorkspaceTreeDir
		} else if fields[1] != "blob" {
			entry.Binary = true
		} // Submodules remain visible but are never traversed.
		entry.Size, _ = strconv.ParseInt(fields[3], 10, 64)
		if entry.Status == "" {
			entry.Status = WorkspaceFileUnmodified
		}
		result.Entries = append(result.Entries, entry)
		if len(result.Entries) >= maxWorkspaceFiles {
			result.Truncated = true
			break
		}
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		a, b := result.Entries[i], result.Entries[j]
		if a.Type != b.Type {
			return a.Type == WorkspaceTreeDir
		}
		return a.Name < b.Name
	})
	return result, nil
}

// GetWorkspaceSnapshotFile opens changed AND unchanged files at the selected
// revision. A deleted path reads its old side, never a same-named live file.
func (s *Service) GetWorkspaceSnapshotFile(ctx context.Context, id domain.SessionID, input WorkspaceSelectionRevisionInput) (WorkspaceFileDetail, error) {
	ctx, cancel := context.WithTimeout(ctx, workspaceReviewTimeout)
	defer cancel()
	rel, err := cleanWorkspaceRelativePath(input.Path)
	if err != nil {
		return WorkspaceFileDetail{}, err
	}
	selection, err := s.snapshotSelection(ctx, id, input)
	if err != nil {
		return WorkspaceFileDetail{}, err
	}
	detail := WorkspaceFileDetail{SessionID: id, Path: rel, Status: WorkspaceFileUnmodified, Historical: true, CompareBaseSHA: selection.before, CompareBaseRef: selection.View.BaseRef, CompareMode: WorkspaceCompareBase, WorkspaceVersion: hashWorkspaceReviewValue(selection.root, selection.before, selection.after)}
	for _, file := range selection.Files {
		if file.Path != rel {
			continue
		}
		detail.Status = file.Status
		detail.PreviousPath = file.PreviousPath
		detail.Additions = file.Additions
		detail.Deletions = file.Deletions
		break
	}

	detail.Deleted = detail.Status == WorkspaceFileDeleted
	rev, path := selection.after, rel
	if detail.Deleted {
		rev = selection.before
		if detail.PreviousPath != "" {
			path = detail.PreviousPath
		}
	}
	data, size, exists, truncated, err := readGitRevision(ctx, selection.root, rev+":"+path)
	if err != nil {
		return WorkspaceFileDetail{}, err
	}
	if !exists {
		return WorkspaceFileDetail{}, apierr.NotFound("WORKSPACE_SNAPSHOT_FILE_NOT_FOUND", "File is unavailable in this snapshot")
	}
	detail.Size = size
	detail.ContentTruncated = truncated || size > maxWorkspaceFileBytes
	detail.Binary = isBinary(data) || !utf8.Valid(data)
	if !detail.Binary && !detail.ContentTruncated {
		detail.Content = string(data)
	}
	if selection.before != "" && detail.Status != WorkspaceFileUnmodified {
		args := []string{"diff", "--no-ext-diff", "--no-textconv", "--find-renames", "--unified=3", selection.before, selection.after, "--", rel}
		if detail.PreviousPath != "" {
			args = append(args, detail.PreviousPath)
		}
		detail.Diff, detail.DiffTruncated, err = gitWorkspaceOutputCapped(ctx, selection.root, maxWorkspaceDiffGroupBytes, args...)
		if err != nil {
			return WorkspaceFileDetail{}, err
		}
	}
	detail = finalizeWorkspaceFileDetail(detail)
	detail.Editable = false
	return detail, nil
}

// GetWorkspaceSnapshotBlob serves bounded passive media from the same snapshot.
func (s *Service) GetWorkspaceSnapshotBlob(ctx context.Context, id domain.SessionID, input WorkspaceSelectionRevisionInput) (WorkspaceFileBlob, error) {
	ctx, cancel := context.WithTimeout(ctx, workspaceReviewTimeout)
	defer cancel()
	rel, err := cleanWorkspaceRelativePath(input.Path)
	if err != nil {
		return WorkspaceFileBlob{}, err
	}
	selection, err := s.snapshotSelection(ctx, id, input)
	if err != nil {
		return WorkspaceFileBlob{}, err
	}
	if input.Side != "" && input.Side != WorkspaceBlobBefore && input.Side != WorkspaceBlobAfter {
		return WorkspaceFileBlob{}, apierr.Invalid("INVALID_WORKSPACE_BLOB_SIDE", "side must be before or after", nil)
	}
	rev := selection.after
	if input.Side == WorkspaceBlobBefore {
		rev = selection.before
		for _, file := range selection.Files {
			if file.Path == rel && file.PreviousPath != "" {
				rel = file.PreviousPath
				break
			}
		}
	}
	mediaType := workspaceImageMediaType(rel)
	if mediaType == "" {
		return WorkspaceFileBlob{}, apierr.Invalid("UNSUPPORTED_WORKSPACE_BLOB", "Snapshot blobs support image files", nil)
	}
	data, err := gitWorkspaceBlob(ctx, selection.root, rev, rel, maxWorkspaceImageBytes)
	if err != nil {
		return WorkspaceFileBlob{}, err
	}
	return WorkspaceFileBlob{Path: rel, Side: input.Side, MediaType: mediaType, Data: data}, nil
}

// pinnedSnapshotSelection preserves a snapshot across ordinary branch advances.
// Its anchor must still belong to the selected source; a force-push that removes
// it fails explicitly rather than substituting a new revision or live bytes.
func (s *Service) pinnedSnapshotSelection(ctx context.Context, id domain.SessionID, input WorkspaceView, shas []string) (WorkspaceCommitSelection, error) {
	if !fullCommitSHA.MatchString(input.ExpectedTipSHA) || (input.ExpectedBaseSHA != "" && !fullCommitSHA.MatchString(input.ExpectedBaseSHA)) {
		return WorkspaceCommitSelection{}, apierr.Invalid("INVALID_WORKSPACE_SNAPSHOT", "Snapshot anchors must be full commit SHAs", nil)
	}
	current := input
	current.ExpectedTipSHA = ""
	current.ExpectedBaseSHA = ""
	view, err := s.resolveWorkspaceView(ctx, id, current)
	if err != nil {
		return WorkspaceCommitSelection{}, err
	}
	if view.root == "" || !gitIsAncestor(ctx, view.root, input.ExpectedTipSHA, view.TipSHA) {
		return WorkspaceCommitSelection{}, apierr.Conflict("WORKSPACE_REF_MOVED", "This revision is no longer reachable from the selected source; reload the selected revision", nil)
	}
	base := input.ExpectedBaseSHA
	if base == "" && view.RangeMode == WorkspaceRangeSinceBase {
		base = view.BaseSHA
	}
	if base != "" && !gitIsAncestor(ctx, view.root, base, input.ExpectedTipSHA) {
		return WorkspaceCommitSelection{}, apierr.Invalid("INVALID_WORKSPACE_SNAPSHOT", "Snapshot base must be an ancestor of its tip", nil)
	}
	view.TipSHA = input.ExpectedTipSHA
	view.BaseSHA = base
	view.rangeBefore = base
	view.logArgs = nil
	if view.BaseSHA != "" {
		view.logArgs = []string{view.BaseSHA + ".." + view.TipSHA}
		view.RangeMode = WorkspaceRangeSinceBase
		view.maxCommits = maxCommitLogCommits
	} else if view.Kind == WorkspaceViewBranch {
		view.rangeBefore, err = recentRangeBefore(ctx, view.root, view.TipSHA)
		if err != nil {
			return WorkspaceCommitSelection{}, err
		}
		view.logArgs = []string{"--first-parent", view.TipSHA}
		view.RangeMode = WorkspaceRangeRecent
		view.maxCommits = maxRecentCommits
	}
	selection, err := s.commitSelection(ctx, view, shas)
	if err != nil {
		return WorkspaceCommitSelection{}, err
	}
	selection.SessionID = id
	selection.View = view.WorkspaceViewInfo
	return selection, nil
}
