package chat

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestHandoffHistoryFilesKeepsLegacyDroidFolders 验证兼容编码的读取不会丢失
// 已有含下划线目录中的真实历史，两个存在的目录仍归属于同一工作区。
func TestHandoffHistoryFilesKeepsLegacyDroidFolders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := filepath.Join(string(filepath.Separator), "worktrees", "scratch_1")
	legacy := filepath.Join(home, ".factory", "sessions", "-worktrees-scratch_1", "legacy.jsonl")
	alternate := filepath.Join(home, ".factory", "sessions", "-worktrees-scratch-1", "alternate.jsonl")
	for _, file := range []string{legacy, alternate} {
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := &Service{dataDir: t.TempDir()}
	record := domain.SessionRecord{Harness: domain.HarnessClaudeCode, Metadata: domain.SessionMetadata{WorkspacePath: workspace}}
	files := s.handoffHistoryFiles(record, nil)
	got := make(map[string]bool, len(files))
	for _, file := range files {
		if file.Harness != domain.HarnessDroid {
			t.Fatalf("unexpected history: %+v", file)
		}
		got[file.Path] = true
	}
	if len(files) != 2 || !got[legacy] || !got[alternate] {
		t.Fatalf("files = %+v, want both existing Droid directories", files)
	}
}
