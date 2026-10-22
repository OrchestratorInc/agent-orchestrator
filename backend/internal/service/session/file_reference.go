package session

import (
	"context"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
)

// FileReferenceResolution is derived on demand. Captured code is never rewritten.
type FileReferenceResolution struct {
	State         string `json:"state" enum:"unchanged,changed,unavailable,moved"`
	Reason        string `json:"reason,omitempty"`
	Path          string `json:"path,omitempty"`
	Line          int    `json:"line,omitempty"`
	EndLine       int    `json:"endLine,omitempty"`
	Side          string `json:"side,omitempty"`
	OldLineOffset int    `json:"oldLineOffset,omitempty"`
	NewLineOffset int    `json:"newLineOffset,omitempty"`
}

// ResolveFileReference checks source availability without modifying captured context.
func (s *Service) ResolveFileReference(ctx context.Context, id domain.SessionID, ref ports.ChatFileReference) (FileReferenceResolution, error) {
	if err := ports.ValidateChatFileReference(ref); err != nil {
		return FileReferenceResolution{}, apierr.Invalid("CHAT_FILE_REFERENCE_INVALID", err.Error(), nil)
	}
	if ref.SessionID != string(id) {
		return FileReferenceResolution{}, apierr.Invalid("CHAT_FILE_REFERENCE_INVALID", "file reference belongs to another session", nil)
	}
	if ref.Truncated {
		return FileReferenceResolution{State: "unavailable", Reason: "truncated"}, nil
	}
	// An explicitly identified PR snapshot must still be the associated comparison.
	if ref.Source.Kind == "pull_request" && ref.Source.Snapshot != "" {
		prs, err := s.store.ListPRsBySession(ctx, id)
		if err != nil {
			return FileReferenceResolution{State: "unavailable"}, nil //nolint:nilerr // Source lookup failures are advisory; captured context remains usable.
		}
		found := false
		for _, pr := range prs {
			if pr.Number == ref.Source.Number && (ref.Source.URL == "" || strings.EqualFold(ref.Source.URL, pr.URL)) {
				// The viewer records its head SHA. Accept the older qualified form too.
				found = ref.Source.Snapshot == pr.HeadSHA || ref.Source.Snapshot == pr.BaseSHA+":"+pr.HeadSHA
				break
			}
		}
		if !found {
			return FileReferenceResolution{State: "unavailable", Reason: "snapshot_changed"}, nil
		}
	}
	read := func(side WorkspaceFileBlobSide) (WorkspaceFileRevision, error) {
		switch ref.Source.Kind {
		case "artifact":
			root, err := s.ArtifactLocation(ctx, id)
			if err != nil {
				return WorkspaceFileRevision{}, err
			}
			f, _, _, err := previewutil.OpenWorkspaceFile(root, ref.Path)
			if err != nil {
				return WorkspaceFileRevision{}, err
			}
			defer func() { _ = f.Close() }()
			data, err := io.ReadAll(io.LimitReader(f, 256*1024+1))
			if err != nil {
				return WorkspaceFileRevision{}, err
			}
			return WorkspaceFileRevision{Exists: true, Content: string(data), Binary: !utf8.Valid(data), Truncated: len(data) > 256*1024}, nil
		case "pull_request":
			if ref.CommitSHA != "" {
				return s.GetPRFileRevisionAtCommit(ctx, id, ref.Source.Number, ref.Source.URL, ref.Path, side, ref.CommitSHA)
			}
			return s.GetPRFileRevision(ctx, id, ref.Source.Number, ref.Source.URL, ref.Path, side)
		default:
			if ref.CommitSHA != "" {
				return s.GetWorkspaceSelectionFileRevision(ctx, id, WorkspaceSelectionRevisionInput{Path: ref.Path, Side: side, Scope: WorkspaceDiffCommitted, CommitSHA: ref.CommitSHA})
			}
			return s.GetWorkspaceFileRevision(ctx, id, ref.Path, WorkspaceDiffScope(ref.Scope), side, "", "")
		}
	}
	result := resolveCapturedFileRows(ref, read)
	// Follow only the exact previous-path identity reported by Git, never a filename guess.
	if ref.Source.Kind == "workspace" && ref.CommitSHA == "" {
		current, currentErr := read(WorkspaceBlobAfter)
		if currentErr == nil && current.Exists {
			return s.verifyFileReferenceDiff(ctx, id, ref, result), nil
		}
		manifest, err := s.GetWorkspaceManifest(ctx, id)
		if err == nil {
			candidate := ""
			for _, file := range manifest.Files {
				if file.PreviousPath == ref.Path {
					if candidate != "" {
						candidate = ""
						break
					}
					candidate = file.Path
				}
			}
			if candidate != "" {
				ref.Path = candidate
				relocated := resolveCapturedFileRows(ref, read)
				// A historical before revision may match even when its current
				// path disappeared. Check the destination through the viewer service.
				if _, openErr := s.GetWorkspaceFile(ctx, id, candidate, WorkspaceFileSection(ref.Scope)); openErr != nil {
					return FileReferenceResolution{State: "unavailable"}, nil //nolint:nilerr // Source access errors become advisory states; captured context remains usable.
				}
				if relocated.State == "unchanged" || relocated.State == "moved" {
					relocated.State = "moved"
					relocated.Reason = "renamed"
					return s.verifyFileReferenceDiff(ctx, id, ref, relocated), nil
				}
				if relocated.State == "changed" {
					// Git established the new identity even though the selection no
					// longer has a verified range. Open this path without highlighting.
					relocated.Path = candidate
					return relocated, nil
				}
			}
		}
		if result.State != "unavailable" {
			if _, openErr := s.GetWorkspaceFile(ctx, id, ref.Path, WorkspaceFileSection(ref.Scope)); openErr != nil {
				return FileReferenceResolution{State: "unavailable"}, nil //nolint:nilerr // Missing viewer sources are advisory, not send errors.
			}
		}
	}
	return s.verifyFileReferenceDiff(ctx, id, ref, result), nil
}

// A range is navigable only when its complete row sequence is still one
// continuous diff interval. Independent exact matches on each side are not enough.
func (s *Service) verifyFileReferenceDiff(ctx context.Context, id domain.SessionID, ref ports.ChatFileReference, result FileReferenceResolution) FileReferenceResolution {
	if ref.Side == "file" || (result.State != "unchanged" && result.State != "moved") || ref.Source.Kind == "artifact" {
		return result
	}
	var patch string
	complete := false
	if ref.Source.Kind == "pull_request" {
		var detail WorkspaceFileDetail
		var err error
		if ref.CommitSHA != "" {
			detail, err = s.GetPRFileAtCommit(ctx, id, ref.Source.Number, ref.Source.URL, ref.Path, ref.CommitSHA)
		} else {
			detail, err = s.GetPRFile(ctx, id, ref.Source.Number, ref.Source.URL, ref.Path, &ref.PreviousPath)
		}
		patch, complete = detail.Diff, err == nil && !detail.DiffTruncated
	} else {
		scope := WorkspaceDiffScope(ref.Scope)
		if ref.CommitSHA != "" {
			scope = WorkspaceDiffCommitted
		}
		diffs, err := s.GetWorkspaceDiffs(ctx, id, WorkspaceDiffInput{Scope: scope, Paths: []string{ref.Path}, CommitSHA: ref.CommitSHA, ContextLines: 20})
		if err == nil && len(diffs.Groups) == 1 {
			group := diffs.Groups[0]
			patch, complete = group.Patch, !group.Truncated && len(group.Errors) == 0 && len(group.Deferred) == 0
		}
	}
	if !complete || !capturedDiffIntervalMatches(ref, result, patch) {
		return FileReferenceResolution{State: "changed", Reason: "range_changed", Path: ref.Path}
	}
	return result
}

var capturedDiffHunk = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

func capturedDiffIntervalMatches(ref ports.ChatFileReference, result FileReferenceResolution, patch string) bool {
	type diffRow struct {
		old, new, hunk int
		kind, text     string
	}
	var rows []diffRow
	oldNo, newNo, hunk := 0, 0, 0
	for _, line := range strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n") {
		if header := capturedDiffHunk.FindStringSubmatch(line); header != nil {
			oldNo, _ = strconv.Atoi(header[1])
			newNo, _ = strconv.Atoi(header[2])
			hunk++
			continue
		}
		if strings.HasPrefix(line, "diff --git ") {
			oldNo, newNo = 0, 0
			continue
		}
		if hunk == 0 || line == "" || (oldNo == 0 && newNo == 0) {
			continue
		}
		switch line[0] {
		case ' ':
			rows = append(rows, diffRow{oldNo, newNo, hunk, "context", line[1:]})
			oldNo++
			newNo++
		case '-':
			rows = append(rows, diffRow{old: oldNo, hunk: hunk, kind: "del", text: line[1:]})
			oldNo++
		case '+':
			rows = append(rows, diffRow{new: newNo, hunk: hunk, kind: "add", text: line[1:]})
			newNo++
		}
	}
	match := func(actual diffRow, expected ports.ChatFileReferenceLine) bool {
		if actual.kind != expected.Kind || actual.text != expected.Text {
			return false
		}
		old := expected.Side == "old" || (expected.Side != "new" && (expected.Kind == "del" || (ref.Side == "old" && expected.Kind != "add")))
		if old {
			return expected.OldNo != nil && actual.old == *expected.OldNo+result.OldLineOffset
		}
		return expected.NewNo != nil && actual.new == *expected.NewNo+result.NewLineOffset
	}
	if len(ref.Lines) == 0 {
		return false
	}
	for start, row := range rows {
		if !match(row, ref.Lines[0]) {
			continue
		}
		if start+len(ref.Lines) > len(rows) {
			return false
		}
		for i, expected := range ref.Lines {
			if rows[start+i].hunk != row.hunk || !match(rows[start+i], expected) {
				return false
			}
		}
		return true
	}
	return false
}

// Each side keeps its own line numbering. A recorded match wins before relocation.
func resolveCapturedFileRows(ref ports.ChatFileReference, read func(WorkspaceFileBlobSide) (WorkspaceFileRevision, error)) FileReferenceResolution {
	if ref.Truncated {
		return FileReferenceResolution{State: "unavailable", Reason: "truncated"}
	}
	// Earlier snapshots did not distinguish a unified context row from a split
	// old-side context row. Keep their captured preview when that distinction matters.
	if ref.Side == "old" && ref.EndSide == "" {
		for _, row := range ref.Lines {
			if row.Kind == "context" && row.Side == "" && row.OldNo != nil && row.NewNo != nil && *row.OldNo != *row.NewNo {
				return FileReferenceResolution{State: "unavailable", Reason: "incomplete_source"}
			}
		}
	}
	type rowAt struct {
		number int
		text   string
	}
	groups := map[WorkspaceFileBlobSide][]rowAt{}
	for _, row := range ref.Lines {
		side, number := WorkspaceBlobAfter, row.NewNo
		if row.Side == "old" || (row.Side != "new" && (row.Kind == "del" || (ref.Side == "old" && row.Kind != "add"))) {
			side, number = WorkspaceBlobBefore, row.OldNo
		}
		if number == nil {
			return FileReferenceResolution{State: "changed"}
		}
		groups[side] = append(groups[side], rowAt{*number, row.Text})
	}
	offsets := map[WorkspaceFileBlobSide]int{}
	for _, side := range []WorkspaceFileBlobSide{WorkspaceBlobBefore, WorkspaceBlobAfter} {
		rows := groups[side]
		if len(rows) == 0 {
			continue
		}
		revision, err := read(side)
		if err != nil || !revision.Exists || revision.Binary || revision.Truncated {
			return FileReferenceResolution{State: "unavailable"}
		}
		lines := strings.Split(strings.ReplaceAll(revision.Content, "\r\n", "\n"), "\n")
		matches := func(offset int) bool {
			for _, row := range rows {
				n := row.number + offset
				if n < 1 || n > len(lines) || lines[n-1] != row.text {
					return false
				}
			}
			return true
		}
		if matches(0) {
			continue
		}
		// Anchor at the rarest captured row, avoiding repeated scans of common blank lines.
		occurrences := map[string][]int{}
		for _, row := range rows {
			occurrences[row.text] = nil
		}
		for i, line := range lines {
			if _, ok := occurrences[line]; ok {
				occurrences[line] = append(occurrences[line], i+1)
			}
		}
		anchor := rows[0]
		for _, row := range rows {
			if len(occurrences[row.text]) < len(occurrences[anchor.text]) {
				anchor = row
			}
		}
		count, offset := 0, 0
		for _, position := range occurrences[anchor.text] {
			candidate := position - anchor.number
			if matches(candidate) {
				count++
				offset = candidate
				if count > 1 {
					return FileReferenceResolution{State: "changed", Reason: "ambiguous"}
				}
			}
		}
		if count != 1 {
			return FileReferenceResolution{State: "changed"}
		}
		offsets[side] = offset
	}
	state := "unchanged"
	if offsets[WorkspaceBlobBefore] != 0 || offsets[WorkspaceBlobAfter] != 0 {
		state = "moved"
	}
	first, last := ref.Lines[0], ref.Lines[len(ref.Lines)-1]
	firstSide, lastSide := WorkspaceBlobAfter, WorkspaceBlobAfter
	if first.Side == "old" || (first.Side != "new" && (first.Kind == "del" || (ref.Side == "old" && first.Kind != "add"))) {
		firstSide = WorkspaceBlobBefore
	}
	if last.Side == "old" || (last.Side != "new" && (last.Kind == "del" || (ref.Side == "old" && last.Kind != "add"))) {
		lastSide = WorkspaceBlobBefore
	}
	return FileReferenceResolution{State: state, Path: ref.Path, Line: ref.Line + offsets[firstSide], EndLine: ref.EndLine + offsets[lastSide], Side: ref.Side, OldLineOffset: offsets[WorkspaceBlobBefore], NewLineOffset: offsets[WorkspaceBlobAfter]}
}
