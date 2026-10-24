package session

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestResolveCapturedFileRows(t *testing.T) {
	n := 2
	ref := ports.ChatFileReference{Path: "a.ts", Side: "file", Line: 2, EndLine: 2, Lines: []ports.ChatFileReferenceLine{{Kind: "context", OldNo: &n, NewNo: &n, Text: "  captured();"}}}
	for _, tc := range []struct {
		name, content, want        string
		missing, binary, truncated bool
	}{
		{name: "same", content: "before\n  captured();\nafter", want: "unchanged"},
		{name: "unrelated edit", content: "different\n  captured();\nafter", want: "unchanged"},
		{name: "line endings", content: "before\r\n  captured();\r\nafter", want: "unchanged"},
		{name: "moved", content: "inserted\nbefore\n  captured();\nafter", want: "changed"},
		{name: "modified", content: "before\n  changed();", want: "changed"},
		{name: "indentation", content: "before\ncaptured();", want: "changed"},
		{name: "deleted", want: "unavailable", missing: true},
		{name: "binary", want: "unavailable", binary: true},
		{name: "bounded source", want: "unavailable", truncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := resolveCapturedFileRows(ref, func(WorkspaceFileBlobSide) (WorkspaceFileRevision, error) {
				return WorkspaceFileRevision{Exists: !tc.missing, Content: tc.content, Binary: tc.binary, Truncated: tc.truncated}, nil
			})
			if result.State != tc.want {
				t.Fatalf("got %+v want %s", result, tc.want)
			}
			if ref.Lines[0].Text != "  captured();" {
				t.Fatal("changed snapshot")
			}
		})
	}
	result := resolveCapturedFileRows(ref, func(WorkspaceFileBlobSide) (WorkspaceFileRevision, error) {
		return WorkspaceFileRevision{}, errors.New("offline")
	})
	if result.State != "unavailable" {
		t.Fatalf("offline: %+v", result)
	}
}

func TestResolveCapturedDiffChecksEachSidesLineNumbers(t *testing.T) {
	oldLine, newLine := 2, 3
	ref := ports.ChatFileReference{Path: "a.ts", Side: "new", Line: 2, EndLine: 3, Lines: []ports.ChatFileReferenceLine{{Kind: "del", OldNo: &oldLine, Text: "removed();"}, {Kind: "add", NewNo: &newLine, Text: "added();"}}}
	result := resolveCapturedFileRows(ref, func(side WorkspaceFileBlobSide) (WorkspaceFileRevision, error) {
		text := "before\nremoved();"
		if side == WorkspaceBlobAfter {
			text = "before\ncontext\nadded();"
		}
		return WorkspaceFileRevision{Exists: true, Content: text}, nil
	})
	if result.State != "unchanged" {
		t.Fatalf("result %+v", result)
	}
}

func intPointer(n int) *int { return &n }

func TestFileReferenceResolvesViewerPRHeadSnapshot(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "remote", "add", "origin", "https://example.test/acme/repo.git")
	base := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	writeWorkspaceFile(t, repo, "README.md", "pull request\n")
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "pr source")
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	writeWorkspaceFile(t, repo, "README.md", "different workspace content\n")
	store := newFakeStore()
	store.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	store.prs["ao-1"] = []domain.PullRequest{{Number: 42, URL: "https://example.test/acme/repo/-/merge_requests/42", Provider: "gitlab", Host: "example.test", Repo: "acme/repo", BaseSHA: base, HeadSHA: head}}
	ref := ports.ChatFileReference{ID: "viewer-selection", SessionID: "ao-1", Path: "README.md", Source: ports.ChatFileReferenceSource{Kind: "pull_request", Number: 42, Snapshot: head}, Side: "file", EndSide: "file", Line: 1, EndLine: 1, Lines: []ports.ChatFileReferenceLine{{Kind: "context", Side: "file", NewNo: intPointer(1), OldNo: intPointer(1), Text: "pull request"}}}
	for _, snapshot := range []string{head, base + ":" + head} {
		t.Run(snapshot, func(t *testing.T) {
			ref.Source.Snapshot = snapshot
			result, err := (&Service{store: store}).ResolveFileReference(context.Background(), "ao-1", ref)
			if err != nil || result.State != "unchanged" {
				t.Fatalf("current PR snapshot rejected: %+v %v", result, err)
			}
			if ref.Lines[0].Text != "pull request" {
				t.Fatal("rewrote captured context")
			}
		})
	}
	for _, snapshot := range []string{"obsolete-head", base + ":obsolete-head", "wrong-base:" + head} {
		ref.Source.Snapshot = snapshot
		result, err := (&Service{store: store}).ResolveFileReference(context.Background(), "ao-1", ref)
		if err != nil || result.State != "unavailable" || result.Reason != "snapshot_changed" {
			t.Fatalf("obsolete snapshot accepted: %+v %v", result, err)
		}
	}
}

func TestFileReferenceHonorsContextRowsOwnDiffSide(t *testing.T) {
	oldLine, newLine := 3, 2
	ref := ports.ChatFileReference{Path: "a.ts", Side: "old", Line: 2, EndLine: 2, Lines: []ports.ChatFileReferenceLine{{Kind: "del", Side: "old", OldNo: &newLine, Text: "removed();"}, {Kind: "context", Side: "new", OldNo: &oldLine, NewNo: &newLine, Text: "context();"}}}
	result := resolveCapturedFileRows(ref, func(side WorkspaceFileBlobSide) (WorkspaceFileRevision, error) {
		text := "before\nremoved();\ncontext();"
		if side == WorkspaceBlobAfter {
			text = "before\nchanged();"
		}
		return WorkspaceFileRevision{Exists: true, Content: text}, nil
	})
	if result.State != "changed" {
		t.Fatalf("new-side context was incorrectly verified against old contents: %+v", result)
	}
}

func TestCapturedBlankRowsMatchCRLFSource(t *testing.T) {
	ref := ports.ChatFileReference{Side: "file", Line: 1, EndLine: 5}
	for i, text := range []string{"services:", "", "  web:", " \t", "    image: example"} {
		n := i + 1
		ref.Lines = append(ref.Lines, ports.ChatFileReferenceLine{Kind: "context", OldNo: &n, NewNo: &n, Text: text})
	}
	result := resolveCapturedFileRows(ref, func(WorkspaceFileBlobSide) (WorkspaceFileRevision, error) {
		return WorkspaceFileRevision{Exists: true, Content: "services:\r\n\r\n  web:\r\n \t\r\n    image: example\r\n"}, nil
	})
	if result.State != "unchanged" {
		t.Fatalf("blank CRLF rows were not unchanged: %+v", result)
	}
}
