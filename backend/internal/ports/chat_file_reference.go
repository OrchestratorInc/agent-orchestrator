package ports

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"
)

// ChatFileReference is user-supplied captured code, not a verified transcript excerpt.
// It contains no local absolute paths or credentials. Source hints do not authorize reads.
type ChatFileReference struct {
	ID               string                  `json:"id"`
	Order            int64                   `json:"order,omitempty"`
	SessionID        string                  `json:"sessionId"`
	HostID           string                  `json:"hostId,omitempty"`
	Path             string                  `json:"path"`
	PreviousPath     string                  `json:"previousPath,omitempty"`
	Source           ChatFileReferenceSource `json:"source"`
	Scope            string                  `json:"scope,omitempty"`
	CommitSHA        string                  `json:"commitSha,omitempty"`
	EndSide          string                  `json:"endSide,omitempty" enum:"file,old,new"`
	Side             string                  `json:"side" enum:"file,old,new"`
	Line             int                     `json:"line"`
	EndLine          int                     `json:"endLine"`
	Lines            []ChatFileReferenceLine `json:"lines"`
	Truncated        bool                    `json:"truncated"`
	FileFingerprint  string                  `json:"fileFingerprint,omitempty"`
	WorkspaceVersion string                  `json:"workspaceVersion,omitempty"`
}

// ChatFileReferenceSource identifies the selected comparison without granting read access.
type ChatFileReferenceSource struct {
	Kind     string `json:"kind" enum:"workspace,pull_request,artifact"`
	URL      string `json:"url,omitempty"`
	Number   int    `json:"number,omitempty"`
	Snapshot string `json:"snapshot,omitempty"`
}

// ChatFileReferenceLine retains a captured row and its original comparison coordinates.
type ChatFileReferenceLine struct {
	Side  string `json:"side,omitempty" enum:"file,old,new"`
	Kind  string `json:"kind" enum:"context,add,del"`
	OldNo *int   `json:"oldNo"`
	NewNo *int   `json:"newNo"`
	Text  string `json:"text"`
}

// ValidateChatFileReference bounds user-supplied context without reading mutable files.
func ValidateChatFileReference(ref ChatFileReference) error {
	validPath := func(p string) bool {
		return p != "" && len(p) <= 1024 && !strings.ContainsAny(p, "\\:\x00\r\n") && !strings.HasPrefix(p, "/") && path.Clean(p) == p && p != "." && p != ".." && !strings.HasPrefix(p, "../")
	}
	if ref.ID == "" || len(ref.ID) > 128 || ref.Order < 0 || ref.SessionID == "" || len(ref.SessionID) > 256 || len(ref.HostID) > 256 || !validPath(ref.Path) || (ref.PreviousPath != "" && !validPath(ref.PreviousPath)) {
		return fmt.Errorf("invalid file reference identity or relative path")
	}
	if ref.EndSide != "" && ref.EndSide != "file" && ref.EndSide != "old" && ref.EndSide != "new" {
		return fmt.Errorf("invalid captured end side")
	}
	if ref.Side != "file" && ref.Side != "old" && ref.Side != "new" {
		return fmt.Errorf("invalid file reference side")
	}
	crossSide := false
	if len(ref.Lines) > 0 {
		old := func(row ChatFileReferenceLine) bool {
			return row.Side == "old" || (row.Side != "new" && (row.Kind == "del" || (ref.Side == "old" && row.Kind != "add")))
		}
		crossSide = old(ref.Lines[0]) != old(ref.Lines[len(ref.Lines)-1])
	}
	if ref.Line < 1 || ref.EndLine < 1 || (!crossSide && ref.EndLine < ref.Line) || ref.Line > 10000000 || ref.EndLine > 10000000 || len(ref.Lines) == 0 || len(ref.Lines) > 8000 {
		return fmt.Errorf("invalid file reference range")
	}
	if ref.Source.URL != "" {
		u, err := url.Parse(ref.Source.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(ref.Source.URL) > 2048 {
			return fmt.Errorf("invalid source URL")
		}
	}
	switch ref.Source.Kind {
	case "workspace", "artifact":
	case "pull_request":
		if ref.Source.Number < 1 {
			return fmt.Errorf("invalid pull request source")
		}
	default:
		return fmt.Errorf("invalid file reference source")
	}
	if len(ref.Source.Snapshot) > 256 || len(ref.CommitSHA) > 128 || len(ref.FileFingerprint) > 256 || len(ref.WorkspaceVersion) > 256 {
		return fmt.Errorf("file reference source is too long")
	}
	switch ref.Scope {
	case "", "combined", "committed", "staged", "unstaged", "untracked", "uncommitted":
	default:
		return fmt.Errorf("invalid comparison scope")
	}
	size := 0
	for _, row := range ref.Lines {
		if row.Side != "" && row.Side != "file" && row.Side != "old" && row.Side != "new" {
			return fmt.Errorf("invalid captured row side")
		}
		if (row.Kind == "add" && row.Side == "old") || (row.Kind == "del" && row.Side == "new") {
			return fmt.Errorf("captured row kind contradicts its side")
		}
		if row.Kind != "context" && row.Kind != "add" && row.Kind != "del" {
			return fmt.Errorf("invalid captured row kind")
		}
		if !utf8.ValidString(row.Text) || utf8.RuneCountInString(row.Text) > 700 || strings.ContainsAny(row.Text, "\r\n\x00") {
			return fmt.Errorf("invalid captured row text")
		}
		for _, n := range []*int{row.OldNo, row.NewNo} {
			if n != nil && (*n < 1 || *n > 10000000) {
				return fmt.Errorf("invalid captured row number")
			}
		}
		size += utf8.RuneCountInString(row.Text) + 1
	}
	if size > 8000 {
		return fmt.Errorf("captured code exceeds 8000 characters")
	}
	return nil
}
