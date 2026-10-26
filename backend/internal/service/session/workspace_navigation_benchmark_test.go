package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Benchmarks actual service navigation in a 2,000-file Git repository. Keep
// the existing short cache window open to measure a burst of folder expands.
func BenchmarkWorkspaceTreeNavigation(b *testing.B) {
	root := b.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	git("init")
	git("config", "user.email", "benchmark@example.com")
	git("config", "user.name", "Benchmark")
	for dir := range 100 {
		path := filepath.Join(root, fmt.Sprintf("folder-%03d", dir))
		if err := os.MkdirAll(path, 0o755); err != nil {
			b.Fatal(err)
		}
		for file := range 20 {
			if err := os.WriteFile(filepath.Join(path, fmt.Sprintf("file-%02d.go", file)), []byte("package fixture\n"), 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
	git("add", ".")
	git("commit", "-m", "fixture")
	store := newFakeStore()
	store.sessions["session"] = domain.SessionRecord{ID: "session", Metadata: domain.SessionMetadata{WorkspacePath: root}}
	now := func() time.Time { return time.Unix(100, 0) }
	s := &Service{store: store, clock: now, workspaceCache: newWorkspaceCache(workspaceCacheTTL, now)}
	if _, err := s.ListWorkspaceTree(b.Context(), "session", ""); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	index := 0
	for b.Loop() {
		if _, err := s.ListWorkspaceTree(b.Context(), "session", fmt.Sprintf("folder-%03d", index%100)); err != nil {
			b.Fatal(err)
		}
		index++
	}
}
