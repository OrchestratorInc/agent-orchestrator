package chat

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestHandoffArtifactsListsTheSessionDirectoryNewestFirst(t *testing.T) {
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, "artifacts", "s-1")
	write := func(name string, at time.Time) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Unix(1_700_000_000, 0)
	write("plan.md", base)
	write("pages/dashboard.html", base.Add(time.Hour))
	write(".versions/plan.md", base.Add(2*time.Hour))

	svc := New(Options{DataDir: dataDir})
	gotDir, files := svc.handoffArtifacts(context.Background(), domain.SessionRecord{ID: "s-1"})
	if gotDir != filepath.ToSlash(dir) {
		t.Errorf("dir = %q, want %q", gotDir, filepath.ToSlash(dir))
	}
	if len(files) != 2 || files[0].Path != "pages/dashboard.html" || files[1].Path != "plan.md" {
		t.Fatalf("files = %+v, want dashboard then plan, without version snapshots", files)
	}

	if _, files := svc.handoffArtifacts(context.Background(), domain.SessionRecord{ID: "missing"}); len(files) != 0 {
		t.Errorf("a session with no artifact directory listed %+v", files)
	}
}
