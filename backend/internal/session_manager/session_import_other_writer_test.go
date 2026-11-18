package sessionmanager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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
	if claudeTurnsWrittenElsewhere(context.Background(), path, workspace, since) {
		t.Fatal("turns from before the start, our own turns and metadata counted as another writer")
	}
	write(terminalBefore, ours, terminalAfter)
	if !claudeTurnsWrittenElsewhere(context.Background(), path, workspace, since) {
		t.Fatal("a turn another process wrote after the start was not found")
	}
	if claudeTurnsWrittenElsewhere(context.Background(), path, workspace, time.Now().Add(time.Hour)) {
		t.Fatal("a transcript unchanged since the start counted as written elsewhere")
	}
}

func TestClaudeTurnsWrittenElsewhereBeyondTranscriptTail(t *testing.T) {
	for _, kind := range []string{"user", "assistant", "older than large local turn"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "chat.jsonl")
			since := time.Now().Add(-time.Minute)
			record := map[string]any{"type": kind, "cwd": "/source", "timestamp": since.Add(time.Second).UTC().Format(time.RFC3339Nano), "message": strings.Repeat("x", (1<<20)+100)}
			var body []byte
			if kind == "older than large local turn" {
				body = []byte(`{"type":"user","cwd":"/source","timestamp":"` + since.Add(time.Second).UTC().Format(time.RFC3339Nano) + `"}` + "\n")
				record["type"], record["cwd"] = "assistant", "/ao/worktree"
			}
			line, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			body = append(body, line...)
			if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
			if !claudeTurnsWrittenElsewhere(context.Background(), path, "/ao/worktree", since) {
				t.Fatal("external turn outside the final MiB was missed")
			}
			if kind == "older than large local turn" {
				if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
					t.Fatal(err)
				}
				if claudeTurnsWrittenElsewhere(context.Background(), path, "/ao/worktree", since) {
					t.Fatal("a large local turn was mistaken for another writer")
				}
			}
		})
	}
}

func TestClaudeTurnsWrittenElsewhereCannotProveTranscriptUnchanged(t *testing.T) {
	for _, path := range []string{"", filepath.Join(t.TempDir(), "missing.jsonl"), t.TempDir()} {
		if !claudeTurnsWrittenElsewhere(context.Background(), path, "/ao/worktree", time.Now()) {
			t.Fatalf("unknown transcript %q was treated as unchanged", path)
		}
	}
}

func TestImportedChatWrittenElsewhereOnlyChecksTheImportedNativeConversation(t *testing.T) {
	for _, current := range []string{"imported-native", "edited-branch", "replacement-provider"} {
		t.Run(current, func(t *testing.T) {
			rec := domain.SessionRecord{Harness: domain.HarnessClaudeCode}
			rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "imported-native", ConfigDir: t.TempDir()}
			rec.Metadata.ProviderConversationID = current
			open, wrote := (&Manager{}).ImportedChatWrittenElsewhere(context.Background(), rec, time.Now())
			if open || wrote != (current == "imported-native") {
				t.Fatalf("current %q: open=%v wrote=%v; only the imported provider needs a reload for its missing transcript", current, open, wrote)
			}
		})
	}
}
