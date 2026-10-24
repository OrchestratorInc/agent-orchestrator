package session

import (
	"context"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
)

// FileReferenceResolution is derived on demand. Captured code is never rewritten.
type FileReferenceResolution struct {
	State   string `json:"state" enum:"unchanged,changed,unavailable"`
	Reason  string `json:"reason,omitempty"`
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	EndLine int    `json:"endLine,omitempty"`
	Side    string `json:"side,omitempty"`
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
	return resolveCapturedFileRows(ref, read), nil
}

func resolveCapturedFileRows(ref ports.ChatFileReference, read func(WorkspaceFileBlobSide) (WorkspaceFileRevision, error)) FileReferenceResolution {
	// Earlier snapshots did not distinguish a unified context row from a split
	// old-side context row. Keep their captured preview when that distinction matters.
	if ref.Side == "old" && ref.EndSide == "" {
		for _, row := range ref.Lines {
			if row.Kind == "context" && row.Side == "" && row.OldNo != nil && row.NewNo != nil && *row.OldNo != *row.NewNo {
				return FileReferenceResolution{State: "unavailable", Reason: "incomplete_source"}
			}
		}
	}
	cache := map[WorkspaceFileBlobSide][]string{}
	for _, row := range ref.Lines {
		side, number := WorkspaceBlobAfter, row.NewNo
		if row.Side == "old" || (row.Side != "new" && (row.Kind == "del" || (ref.Side == "old" && row.Kind != "add"))) {
			side, number = WorkspaceBlobBefore, row.OldNo
		}
		if number == nil {
			return FileReferenceResolution{State: "changed"}
		}
		lines, ok := cache[side]
		if !ok {
			revision, err := read(side)
			if err != nil || !revision.Exists || revision.Binary || revision.Truncated {
				return FileReferenceResolution{State: "unavailable"}
			}
			lines = strings.Split(strings.ReplaceAll(revision.Content, "\r\n", "\n"), "\n")
			cache[side] = lines
		}
		if *number < 1 || *number > len(lines) || lines[*number-1] != row.Text {
			return FileReferenceResolution{State: "changed"}
		}
	}
	return FileReferenceResolution{State: "unchanged", Path: ref.Path, Line: ref.Line, EndLine: ref.EndLine, Side: ref.Side}
}
