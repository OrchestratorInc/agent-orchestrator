package sessionimport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestVibeImportFormats(t *testing.T) {
	for _, name := range []string{"meta.json", "metadata.json"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "logs", "session", "session_20261102_100000_abcd1234")
			writeFile(t, filepath.Join(dir, name), `{"session_id":"abcd1234-1234-1234-1234-123456789012","title":"Synthetic chat","start_time":"2026-11-02T10:00:00Z","environment":{"working_directory":"/work/source"}}`)
			path := writeFile(t, filepath.Join(dir, "messages.jsonl"), `{"role":"system","content":"injected instructions"}
{"role":"user","content":"Remember the synthetic code word"}
{"role":"assistant","content":[{"type":"text","text":"Remembered"},{"type":"tool_call","text":"hidden"}]}
{"role":"tool","content":"hidden output"}
{"role":
`)
			files, err := discoverVibe(context.Background(), root)
			if err != nil || len(files) != 1 {
				t.Fatalf("discover = %v, %v", files, err)
			}
			got, err := readTranscript(context.Background(), domain.HarnessVibe, root, path)
			if err != nil {
				t.Fatal(err)
			}
			wantTexts(t, got.messages, "user: Remember the synthetic code word", "assistant: Remembered")
			if got.nativeID != files[0].nativeID() || got.cwd != "/work/source" || got.title != "Synthetic chat" || got.last.IsZero() {
				t.Fatalf("metadata = %+v", got)
			}
		})
	}
}

func TestVibeImportSkipsChildrenAndEscapes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "logs", "session", "session_child")
	writeFile(t, filepath.Join(dir, "meta.json"), `{"session_id":"child","parent_session_id":"parent","environment":{"working_directory":"/work/source"}}`)
	writeFile(t, filepath.Join(dir, "messages.jsonl"), `{"role":"user","content":"child"}`)
	files, err := discoverVibe(context.Background(), root)
	if err != nil || len(files) != 0 {
		t.Fatalf("children = %v, %v", files, err)
	}
	outside := writeFile(t, filepath.Join(t.TempDir(), "messages.jsonl"), `{"role":"user","content":"outside"}`)
	if _, err := readVibeTranscript(context.Background(), root, outside); err == nil {
		t.Fatal("accepted outside path")
	}
	link := filepath.Join(root, "logs", "session", "session_escape")
	if err := os.Symlink(filepath.Dir(outside), link); err != nil {
		t.Skip(err)
	}
	files, err = discoverVibe(context.Background(), root)
	if err != nil || len(files) != 0 {
		t.Fatalf("escaped discovery = %v, %v", files, err)
	}
}

func TestVibeRootOverride(t *testing.T) {
	t.Setenv("VIBE_HOME", "/custom/vibe")
	if got := vibeRoot("/home/example"); got != "/custom/vibe" {
		t.Fatal(got)
	}
	t.Setenv("VIBE_HOME", "")
	if got := vibeRoot("/home/example"); got != filepath.Join("/home/example", ".vibe") {
		t.Fatal(got)
	}
}
