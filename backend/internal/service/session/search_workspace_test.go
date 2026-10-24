package session

import (
	"context"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSearchWorkspaceListsVisibleRepoFiles(t *testing.T) {
	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "notes.txt", "agent note\n")
	writeWorkspaceFile(t, repo, "node_modules/cache.txt", "ignored\n")
	st := newFakeStore()
	st.sessions["ao-1"] = domain.SessionRecord{
		ID:       "ao-1",
		Metadata: domain.SessionMetadata{WorkspacePath: repo},
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	got, err := (&Service{store: st}).SearchWorkspace(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != repo {
		t.Fatalf("root = %q, want %q", got.Root, repo)
	}
	for _, want := range []string{"README.md", "src/app.go", "notes.txt"} {
		if !slices.Contains(got.Paths, want) {
			t.Fatalf("paths = %v, missing %s", got.Paths, want)
		}
	}
	if slices.Contains(got.Paths, "node_modules/cache.txt") {
		t.Fatal("ignored file listed")
	}
	if !slices.IsSorted(got.Paths) {
		t.Fatalf("paths not sorted: %v", got.Paths)
	}
}

func TestSearchWorkspaceCoversStandaloneWorkers(t *testing.T) {
	workspace := t.TempDir()
	writeWorkspaceFile(t, workspace, "notes.txt", "standalone note\n")
	st := newFakeStore()
	st.sessions["standalone-1"] = domain.SessionRecord{
		ID:       "standalone-1",
		Kind:     domain.KindWorker,
		Metadata: domain.SessionMetadata{WorkspacePath: workspace},
	}
	got, err := (&Service{store: st}).SearchWorkspace(context.Background(), "standalone-1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Paths, []string{"notes.txt"}) {
		t.Fatalf("paths = %v, want [notes.txt]", got.Paths)
	}
}

func TestSearchWorkspaceUnknownSession(t *testing.T) {
	if _, err := (&Service{store: newFakeStore()}).SearchWorkspace(context.Background(), "missing"); err == nil {
		t.Fatal("unknown session accepted")
	}
}

func TestDedupeSorted(t *testing.T) {
	if got := dedupeSorted([]string{"b", "a", "", "b"}); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("dedupeSorted = %v", got)
	}
	if !underAnyPrefix("web/src/a.ts", []string{"web"}) || underAnyPrefix("website/a.ts", []string{"web"}) {
		t.Fatal("underAnyPrefix wrong")
	}
}
