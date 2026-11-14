package sessionmanager

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeTurnsWrittenElsewhere(t *testing.T) {
	dir := t.TempDir()
	workspace := filepath.Join(dir, "worktree")
	path := filepath.Join(dir, "chat.jsonl")
	since := time.Now().Add(-time.Minute)
	before := since.Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	after := since.Add(30 * time.Second).UTC().Format(time.RFC3339Nano)
	write := func(lines ...string) {
		t.Helper()
		body := ""
		for _, line := range lines {
			body += line + "\n"
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	terminalBefore := `{"type":"user","cwd":"/src/repo","timestamp":"` + before + `"}`
	ours := `{"type":"assistant","cwd":"` + filepath.Join(workspace, "sub") + `","timestamp":"` + after + `"}`
	terminalTitle := `{"type":"ai-title","cwd":"/src/repo","timestamp":"` + after + `"}`
	terminalAfter := `{"type":"assistant","cwd":"/src/repo","timestamp":"` + after + `"}`

	write(terminalBefore, ours, terminalTitle, `{"type":"user","cwd":"/src/re`)
	if claudeTurnsWrittenElsewhere(path, workspace, since) {
		t.Fatal("turns from before the start, our own turns and metadata counted as another writer")
	}
	write(terminalBefore, ours, terminalAfter)
	if !claudeTurnsWrittenElsewhere(path, workspace, since) {
		t.Fatal("a turn another process wrote after the start was not found")
	}
	if claudeTurnsWrittenElsewhere(path, workspace, time.Now().Add(time.Hour)) {
		t.Fatal("a transcript unchanged since the start counted as written elsewhere")
	}
}
