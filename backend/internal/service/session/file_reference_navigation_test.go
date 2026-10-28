package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestFileReferenceOldSideRenameReturnsOpenablePath(t *testing.T) {
	repo := newWorkspaceRepo(t)
	padding := strings.Repeat("unchanged padding for rename identity\n", 20)
	writeWorkspaceFile(t, repo, "README.md", "old();\n"+padding)
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "old-side fixture")
	writeWorkspaceFile(t, repo, "README.md", "new();\n"+padding)
	svc := workspaceReviewService(t, repo)
	ref := ports.ChatFileReference{ID: "old-rename", SessionID: "ao-1", Path: "README.md", Source: ports.ChatFileReferenceSource{Kind: "workspace"}, Scope: "combined", Side: "old", EndSide: "old", Line: 1, EndLine: 1, Lines: []ports.ChatFileReferenceLine{{Kind: "del", Side: "old", OldNo: intPointer(1), Text: "old();"}}}
	before, _ := json.Marshal(ref)
	runGit(t, repo, "mv", "README.md", "RENAMED.md")
	result, err := svc.ResolveFileReference(context.Background(), "ao-1", ref)
	if err != nil || result.State != "moved" || result.Path != "RENAMED.md" || result.Reason != "renamed" {
		t.Fatalf("old-side rename: %+v %v", result, err)
	}
	if _, err := svc.GetWorkspaceFile(context.Background(), "ao-1", result.Path, ""); err != nil {
		t.Fatalf("resolved path cannot open: %v", err)
	}
	after, _ := json.Marshal(ref)
	if string(before) != string(after) {
		t.Fatal("rewrote historical capture")
	}
}

func TestFileReferenceRelocatedSidesMustRemainOneDiffInterval(t *testing.T) {
	repo := newWorkspaceRepo(t)
	original := "keep\nold();\nmiddle\nend\nline5\nline6\ntail\nother();\n"
	writeWorkspaceFile(t, repo, "README.md", original)
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "replacement fixture")
	writeWorkspaceFile(t, repo, "README.md", strings.Replace(original, "old();", "new();", 1))
	svc := workspaceReviewService(t, repo)
	ref := ports.ChatFileReference{ID: "replacement", SessionID: "ao-1", Path: "README.md", Source: ports.ChatFileReferenceSource{Kind: "workspace"}, Scope: "combined", Side: "old", EndSide: "new", Line: 2, EndLine: 2, Lines: []ports.ChatFileReferenceLine{{Kind: "del", Side: "old", OldNo: intPointer(2), Text: "old();"}, {Kind: "add", Side: "new", NewNo: intPointer(2), Text: "new();"}}}
	result, err := svc.ResolveFileReference(context.Background(), "ao-1", ref)
	if err != nil || result.State != "unchanged" {
		t.Fatalf("original interval: %+v %v", result, err)
	}
	writeWorkspaceFile(t, repo, "README.md", "inserted1\ninserted2\n"+strings.Replace(original, "old();", "new();", 1))
	result, err = svc.ResolveFileReference(context.Background(), "ao-1", ref)
	if err != nil || result.State != "moved" || result.NewLineOffset != 2 {
		t.Fatalf("complete relocated interval: %+v %v", result, err)
	}
	changed := strings.Replace(strings.Replace(original, "old();", "unrelated();", 1), "other();", "new();", 1)
	writeWorkspaceFile(t, repo, "README.md", changed)
	result, err = svc.ResolveFileReference(context.Background(), "ao-1", ref)
	if err != nil || result.State != "changed" || result.Path != "README.md" || result.Line != 0 || result.EndLine != 0 || result.OldLineOffset != 0 || result.NewLineOffset != 0 {
		t.Fatalf("split interval must not highlight: %+v %v", result, err)
	}
	if ref.Lines[1].Text != "new();" || *ref.Lines[1].NewNo != 2 {
		t.Fatal("rewrote original selection")
	}
}

func TestCapturedDiffIntervalRequiresEveryRow(t *testing.T) {
	ref := ports.ChatFileReference{Side: "old", Lines: []ports.ChatFileReferenceLine{{Kind: "del", Side: "old", OldNo: intPointer(2), Text: "old();"}, {Kind: "add", Side: "new", NewNo: intPointer(2), Text: "new();"}}}
	for _, tc := range []struct {
		name, patch string
		want        bool
	}{
		{"adjacent", "@@ -1,2 +1,2 @@\n keep\n-old();\n+new();\n", true},
		{"CRLF", "@@ -1,2 +1,2 @@\r\n keep\r\n-old();\r\n+new();\r\n", true},
		{"intervening", "@@ -1,2 +1,3 @@\n keep\n-old();\n+other();\n+new();\n", false},
		{"different hunks", "@@ -2 +2 @@\n-old();\n@@ -2 +2 @@\n+new();\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, got := resolveCapturedDiffInterval(ref, FileReferenceResolution{}, tc.patch, WorkspaceFileRevision{Exists: true, Content: "keep\nold();\n"}, WorkspaceFileRevision{Exists: true, Content: "keep\nnew();\n"})
			if got != tc.want {
				t.Fatalf("match=%v want %v", got, tc.want)
			}
		})
	}
}

func TestFileReferenceDeletedFileViewCaptureIsNotVerifiedAsDiff(t *testing.T) {
	ref := ports.ChatFileReference{ID: "deleted-file-view", SessionID: "ao-1", Path: "gone.ts", Source: ports.ChatFileReferenceSource{Kind: "workspace"}, Scope: "combined", Side: "old", EndSide: "old", Line: 2, EndLine: 3, Lines: []ports.ChatFileReferenceLine{
		{Kind: "context", Side: "file", OldNo: intPointer(2), Text: "b();"},
		{Kind: "context", Side: "file", OldNo: intPointer(3), Text: "c();"},
	}}
	unchanged := FileReferenceResolution{State: "unchanged"}
	result := (&Service{}).verifyFileReferenceDiff(context.Background(), "ao-1", ref, unchanged, nil)
	if result != unchanged {
		t.Fatalf("historical File-view capture was treated as a diff selection: %+v", result)
	}
}
