package session

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func searchContentPaths(t *testing.T, svc *Service, query string) ([]string, []WorkspaceContentHit) {
	t.Helper()
	hits, err := svc.SearchWorkspaceContent(context.Background(), "ao-1", query)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(hits))
	for _, hit := range hits {
		paths = append(paths, hit.Path)
	}
	return paths, hits
}

func TestSearchWorkspaceContentRanksBestLinePerFile(t *testing.T) {
	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "src/palette.ts", "import x from 'y';\nexport function closePalette() {}\n")
	writeWorkspaceFile(t, repo, "src/caller.ts", "// we close the palette here\n")
	writeWorkspaceFile(t, repo, "src/palette.test.ts", "closePalette();\n")
	writeWorkspaceFile(t, repo, "notes/untracked.md", "Untracked notes call closePalette too.\n")
	writeWorkspaceFile(t, repo, "node_modules/pkg/index.js", "closePalette();\n")
	writeWorkspaceFile(t, repo, "src/image.bin", "closePalette\x00\x01")
	svc := workspaceReviewService(t, repo)

	// Filler words drop out; the declaration ranks first; ignored, binary and
	// test files rank last or not at all.
	paths, hits := searchContentPaths(t, svc, "where do we close the PALETTE")
	if want := []string{"src/palette.ts", "notes/untracked.md", "src/caller.ts", "src/palette.test.ts"}; !slices.Equal(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	if hits[0].Line != 2 || hits[0].Text != "export function \x02closePalette\x03() {}" {
		t.Fatalf("best line = %#v", hits[0])
	}
}

func TestSearchWorkspaceContentCommonTermReachesLaterFiles(t *testing.T) {
	repo := newWorkspaceRepo(t)
	// Without the per-file limit, this first file exhausts the output budget
	// before git grep reaches the better declaration in frontend/.
	writeWorkspaceFile(t, repo, "backend/gen/repeated.go", strings.Repeat("// error "+strings.Repeat("x", 64)+"\n", 10_000))
	writeWorkspaceFile(t, repo, "frontend/error.ts", "// error handling\n// another error\nexport function handleError() {}\n")
	svc := workspaceReviewService(t, repo)

	paths, hits := searchContentPaths(t, svc, "error")
	if want := []string{"frontend/error.ts", "backend/gen/repeated.go"}; !slices.Equal(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	if hits[0].Line != 3 || hits[0].Text != "export function handle\x02Error\x03() {}" {
		t.Fatalf("best line = %#v", hits[0])
	}
}

func TestSearchWorkspaceContentFallsBackToMostWords(t *testing.T) {
	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "a.go", "func resolveDataDir() string\n")
	writeWorkspaceFile(t, repo, "b.go", "dataDir := x\n")
	writeWorkspaceFile(t, repo, "c.ts", "draw(uniforms);\n")
	writeWorkspaceFile(t, repo, "d.ts", "renderFormField();\n")
	svc := workspaceReviewService(t, repo)

	if paths, _ := searchContentPaths(t, svc, "resolve datadir mispeled"); !slices.Equal(paths, []string{"a.go", "b.go"}) {
		t.Fatalf("fallback = %v", paths)
	}
	// A word start beats the middle of a word.
	if paths, _ := searchContentPaths(t, svc, "form"); !slices.Equal(paths, []string{"d.ts", "c.ts"}) {
		t.Fatalf("word start = %v", paths)
	}
	if paths, _ := searchContentPaths(t, svc, "nothing-matches-this"); len(paths) != 0 {
		t.Fatalf("no match = %v", paths)
	}
}

func TestSearchWorkspaceContentScratchFolder(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, "notes.txt", "remember the milk\n")
	svc := workspaceReviewService(t, dir)
	svc.store.(*fakeStore).sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: dir}}

	if paths, hits := searchContentPaths(t, svc, "milk"); !slices.Equal(paths, []string{"notes.txt"}) || hits[0].Line != 1 {
		t.Fatalf("hits = %#v", hits)
	}
}

func TestGrepOutputStopsAtLimit(t *testing.T) {
	stopped := false
	out := &grepOutput{limit: 4, full: func() { stopped = true }}
	if n, err := out.Write([]byte("abcdef")); n != 6 || err != nil || out.buffer.String() != "abcd" || !out.filled || !stopped {
		t.Fatalf("Write = %d, %v; buffer %q filled %v stopped %v", n, err, out.buffer.String(), out.filled, stopped)
	}
}
