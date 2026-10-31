package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func expandedContextFixture() ([]string, []string) {
	before := make([]string, 160)
	for i := range before {
		before[i] = fmt.Sprintf("unique row %d", i+1)
	}
	after := append([]string(nil), before...)
	after[59], after[119] = "replacement 60", "replacement 120"
	return before, after
}

func contextReference(side string, start, end int, lines []string) ports.ChatFileReference {
	ref := ports.ChatFileReference{ID: "expanded", SessionID: "ao-1", Path: "README.md", Source: ports.ChatFileReferenceSource{Kind: "workspace"}, Scope: "combined", Side: side, EndSide: side, Line: start, EndLine: end}
	for n := start; n <= end; n++ {
		ref.Lines = append(ref.Lines, ports.ChatFileReferenceLine{Kind: "context", Side: side, OldNo: intPointer(n), NewNo: intPointer(n), Text: lines[n-1]})
	}
	return ref
}

func TestFileReferenceExpandedContextAcrossSourceBoundaries(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "remote", "add", "origin", "https://example.test/acme/repo.git")
	before, after := expandedContextFixture()
	writeWorkspaceFile(t, repo, "README.md", strings.Join(before, "\n")) // No final newline.
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "expanded context base")
	base := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "branch", "-M", "main")
	runGit(t, repo, "switch", "-c", "session")
	writeWorkspaceFile(t, repo, "README.md", strings.Join(after, "\n"))
	svc := checkedOutSelectionService(repo, base)
	check := func(t *testing.T, svc *Service, source ports.ChatFileReferenceSource, commit string) {
		t.Helper()
		for _, side := range []string{"old", "new"} {
			interval := [2]int{1, 3}
			if side == "new" {
				interval = [2]int{158, 160}
			}
			ref := contextReference(side, interval[0], interval[1], before)
			ref.Source, ref.CommitSHA = source, commit
			result, err := svc.ResolveFileReference(context.Background(), "ao-1", ref)
			if err != nil || result.State != "unchanged" || result.Line != interval[0] || result.EndLine != interval[1] {
				t.Fatalf("%s %s %v: %+v %v", source.Kind, side, interval, result, err)
			}
		}
	}
	check(t, svc, ports.ChatFileReferenceSource{Kind: "workspace"}, "")
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "expanded context changes")
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	check(t, svc, ports.ChatFileReferenceSource{Kind: "workspace"}, head)
	store := newFakeStore()
	store.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	store.prs["ao-1"] = []domain.PullRequest{{Number: 42, URL: "https://example.test/acme/repo/-/merge_requests/42", Provider: "gitlab", Host: "example.test", Repo: "acme/repo", BaseSHA: base, HeadSHA: head}}
	pr := ports.ChatFileReferenceSource{Kind: "pull_request", Number: 42, Snapshot: head}
	check(t, &Service{store: store}, pr, "")
	check(t, &Service{store: store}, pr, head)
	// A pinned commit/PR must ignore subsequent live changes.
	writeWorkspaceFile(t, repo, "README.md", "unrelated live contents\n")
	check(t, &Service{store: store}, pr, head)
}

func TestCapturedDiffExpandedMixedInterval(t *testing.T) {
	before, after := expandedContextFixture()
	patch := "@@ -60 +60 @@\n-unique row 60\n+replacement 60\n@@ -120 +120 @@\n-unique row 120\n+replacement 120\n"
	for _, side := range []string{"old", "new"} {
		for _, interval := range [][2]int{{1, 3}, {89, 92}, {158, 160}} {
			ref := contextReference(side, interval[0], interval[1], before)
			if _, ok := resolveCapturedDiffInterval(ref, FileReferenceResolution{State: "unchanged"}, patch, WorkspaceFileRevision{Exists: true, Content: strings.Join(before, "\n")}, WorkspaceFileRevision{Exists: true, Content: strings.Join(after, "\n")}); !ok {
				t.Fatalf("omitted %s context %v", side, interval)
			}
		}
	}
	for _, ending := range []string{"\n", "\r\n"} {
		for _, finalNewline := range []bool{false, true} {
			oldText, newText := strings.Join(before, ending), strings.Join(after, ending)
			if finalNewline {
				oldText += ending
				newText += ending
			}
			ref := contextReference("new", 50, 59, before)
			ref.Lines = append(ref.Lines,
				ports.ChatFileReferenceLine{Kind: "del", Side: "old", OldNo: intPointer(60), Text: before[59]},
				ports.ChatFileReferenceLine{Kind: "add", Side: "new", NewNo: intPointer(60), Text: after[59]})
			ref.Lines = append(ref.Lines, contextReference("new", 61, 70, before).Lines...)
			result, ok := resolveCapturedDiffInterval(ref, FileReferenceResolution{State: "unchanged"}, patch, WorkspaceFileRevision{Exists: true, Content: oldText}, WorkspaceFileRevision{Exists: true, Content: newText})
			if !ok || result.Line != 50 || result.EndLine != 70 {
				t.Fatalf("ending=%q final=%v: %+v %v", ending, finalNewline, result, ok)
			}
			// Dropping even a context row cannot join unrelated code into a highlight.
			ref.Lines = append(ref.Lines[:3:3], ref.Lines[4:]...)
			if _, ok := resolveCapturedDiffInterval(ref, result, patch, WorkspaceFileRevision{Exists: true, Content: oldText}, WorkspaceFileRevision{Exists: true, Content: newText}); ok {
				t.Fatal("accepted a nonconsecutive capture")
			}
		}
	}
}

func TestCapturedDiffExpandedContextDerivesPeerOffsets(t *testing.T) {
	before := WorkspaceFileRevision{Exists: true, Content: "first\nkeep\nold\ntail\n"}
	after := WorkspaceFileRevision{Exists: true, Content: "insert\nfirst\nkeep\nnew\ntail\n"}
	patch := "@@ -0,0 +1 @@\n+insert\n@@ -3 +4 @@\n-old\n+new\n"
	for _, side := range []string{"old", "new"} {
		ref := contextReference(side, 2, 2, []string{"first", "keep"})
		initial := FileReferenceResolution{State: "unchanged"}
		if side == "new" {
			initial.NewLineOffset = 1
		}
		result, ok := resolveCapturedDiffInterval(ref, initial, patch, before, after)
		if !ok || result.State != "moved" || result.OldLineOffset != 0 || result.NewLineOffset != 1 {
			t.Fatalf("%s: %+v %v", side, result, ok)
		}
	}
}

func TestFileReferenceExpandedContextFollowsVerifiedRename(t *testing.T) {
	repo := newWorkspaceRepo(t)
	before, after := expandedContextFixture()
	writeWorkspaceFile(t, repo, "README.md", strings.Join(before, "\n"))
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "rename base")
	runGit(t, repo, "mv", "README.md", "RENAMED.md")
	writeWorkspaceFile(t, repo, "RENAMED.md", strings.Join(after, "\n"))
	for _, side := range []string{"old", "new"} {
		ref := contextReference(side, 1, 3, before)
		result, err := workspaceReviewService(t, repo).ResolveFileReference(context.Background(), "ao-1", ref)
		if err != nil || result.State != "moved" || result.Path != "RENAMED.md" || result.Reason != "renamed" || result.Line != 1 || result.EndLine != 3 {
			t.Fatalf("%s: %+v %v", side, result, err)
		}
		if ref.Path != "README.md" || *ref.Lines[0].NewNo != 1 {
			t.Fatal("rewrote historical identity")
		}
	}
}

func TestCapturedDiffRejectsMalformedOrIncompleteSource(t *testing.T) {
	ref := contextReference("new", 1, 1, []string{"keep"})
	before := WorkspaceFileRevision{Exists: true, Content: "keep\nold\n"}
	after := WorkspaceFileRevision{Exists: true, Content: "keep\nnew\n"}
	for _, patch := range []string{"", "@@ broken\n", "@@ -2,2 +2 @@\n-old\n+new\n", "@@ -0 +0 @@\n-old\n+new\n", "@@ -2 +2 @@\n-old\n+new\n@@ -1 +1 @@\n keep\n"} {
		if _, ok := resolveCapturedDiffInterval(ref, FileReferenceResolution{}, patch, before, after); ok {
			t.Fatalf("accepted malformed patch %q", patch)
		}
	}
	patch := "@@ -2 +2 @@\n-old\n+new\n"
	for _, broken := range []WorkspaceFileRevision{{Exists: true, Content: after.Content, Truncated: true}, {Exists: true, Content: after.Content, Binary: true}, {Exists: true, Content: "changed\nnew\n"}} {
		if _, ok := resolveCapturedDiffInterval(ref, FileReferenceResolution{}, patch, before, broken); ok {
			t.Fatal("accepted unsafe context")
		}
	}
}

func TestFileReferenceRevisionMemoizationIncludesPathAndSide(t *testing.T) {
	path := "original.ts"
	calls := map[string]int{}
	failure := errors.New("unavailable")
	read := memoizeFileReferenceRead(func() string { return path }, func(side WorkspaceFileBlobSide) (WorkspaceFileRevision, error) {
		calls[path+string(side)]++
		if path == "missing.ts" {
			return WorkspaceFileRevision{}, failure
		}
		return WorkspaceFileRevision{Exists: true, Content: path + string(side)}, nil
	})
	for _, name := range []string{"original.ts", "renamed.ts", "missing.ts", "original.ts"} {
		path = name
		for _, side := range []WorkspaceFileBlobSide{WorkspaceBlobBefore, WorkspaceBlobAfter} {
			for range 3 {
				revision, err := read(side)
				if name == "missing.ts" {
					if !errors.Is(err, failure) {
						t.Fatal(err)
					}
				} else if err != nil || revision.Content != name+string(side) {
					t.Fatalf("wrong cached revision: %+v %v", revision, err)
				}
			}
		}
	}
	for key, count := range calls {
		if count != 1 {
			t.Fatalf("%s read %d times", key, count)
		}
	}
}

func BenchmarkCapturedDiffOmittedContext(b *testing.B) {
	for _, lines := range []int{100, 100000} {
		b.Run(fmt.Sprint(lines), func(b *testing.B) {
			patch := "@@ -2 +2 @@\n-old\n+new\n"
			b.ReportAllocs()
			for b.Loop() {
				segments, ok := capturedDiffSegments(patch, lines, lines)
				if !ok || len(segments) != 4 {
					b.Fatalf("segments=%d valid=%v", len(segments), ok)
				}
			}
		})
	}
}
