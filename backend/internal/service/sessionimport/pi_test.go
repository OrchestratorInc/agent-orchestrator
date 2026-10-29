package sessionimport

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const piSession = "01a11ac8-11e4-7c7b-84ab-e32998fa4fb1"

// piPath is a Pi session file as Pi writes it: sessions/<cwd-dashed>/<ISO>_<uuid>.jsonl.
func piPath(root, id string) string {
	return filepath.Join(root, "sessions", "--work-repo--", "2026-10-08T09-11-21-828Z_"+id+".jsonl")
}

// Shapes below are trimmed from real Pi session JSONL.
func TestPiTranscriptKeepsOnlyUserAndAssistantText(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, piPath(root, piSession), `{"type":"session","version":1,"id":"`+piSession+`","timestamp":"2026-10-08T09:11:21.828Z","cwd":"/work/repo"}
{"type":"model_change","id":"m1","parentId":null,"timestamp":"2026-10-08T09:11:28.742Z","provider":"zai-glm","modelId":"glm-4.5"}
{"type":"thinking_level_change","id":"t1","parentId":"m1","timestamp":"2026-10-08T09:11:28.742Z","thinkingLevel":"high"}
{"type":"message","id":"u1","parentId":"m1","timestamp":"2026-10-08T09:11:30.000Z","message":{"role":"user","content":[{"type":"text","text":"add a greeting to the hero section"}]}}
{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-10-08T09:11:40.000Z","message":{"role":"assistant","model":"glm-4.5","provider":"zai-glm","content":[{"type":"thinking","thinking":"private reasoning"},{"type":"text","text":"I'll add it now."},{"type":"toolCall","id":"tc1","name":"edit","arguments":{}}]}}
{"type":"message","id":"tr1","parentId":"a1","timestamp":"2026-10-08T09:11:41.000Z","message":{"role":"toolResult","content":[{"type":"text","text":"patched hero.tsx"}]}}
{"type":"message","id":"a2","parentId":"tr1","timestamp":"2026-10-08T09:11:50.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Done."}]}}
`)
	got, err := readTranscript(context.Background(), domain.HarnessPi, root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: add a greeting to the hero section", "assistant: I'll add it now.", "assistant: Done.")
	if got.cwd != "/work/repo" {
		t.Fatalf("cwd = %q", got.cwd)
	}
	if got.nativeID != piSession {
		t.Fatalf("nativeID = %q", got.nativeID)
	}
	if got.title != "add a greeting to the hero section" {
		t.Fatalf("title = %q", got.title)
	}
}

func TestPiTranscriptRejectsNativeIDMismatch(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, piPath(root, piSession), `{"type":"session","version":1,"id":"01a00000-0000-7000-8000-000000000000","timestamp":"2026-10-08T09:11:21.828Z","cwd":"/work/repo"}
{"type":"message","id":"u1","timestamp":"2026-10-08T09:11:30.000Z","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}
`)
	if _, err := readTranscript(context.Background(), domain.HarnessPi, root, path); err == nil {
		t.Fatal("expected native identity mismatch error")
	}
}

func TestPiTranscriptRejectsToolOnlyConversation(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, piPath(root, piSession), `{"type":"session","version":1,"id":"`+piSession+`","timestamp":"2026-10-08T09:11:21.828Z","cwd":"/work/repo"}
{"type":"message","id":"a1","timestamp":"2026-10-08T09:11:40.000Z","message":{"role":"assistant","content":[{"type":"text","text":"hello there"}]}}
`)
	if _, err := readTranscript(context.Background(), domain.HarnessPi, root, path); err == nil {
		t.Fatal("expected error for a conversation with no user prompt")
	}
}

func TestPiDiscoverGroupsEachFileByNativeID(t *testing.T) {
	root := t.TempDir()
	writeFile(t, piPath(root, piSession), `{"type":"session","version":1,"id":"`+piSession+`","timestamp":"2026-10-08T09:11:21.828Z","cwd":"/work/repo"}
`)
	other := "01a11ac8-2222-7c7b-84ab-e32998fa4fb2"
	writeFile(t, filepath.Join(root, "sessions", "--other--", "2026-10-09T09-11-21-828Z_"+other+".jsonl"), `{"type":"session","version":1,"id":"`+other+`","timestamp":"2026-10-09T09:11:21.828Z","cwd":"/work/other"}
`)
	svc := New(&importStore{}, "")
	files, err := svc.discover(context.Background(), domain.HarnessPi, canonical(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("discovered %d files, want 2", len(files))
	}
	ids := map[string]bool{}
	for _, f := range files {
		if len(f.paths) != 1 {
			t.Fatalf("conversation grouped %d files, want 1", len(f.paths))
		}
		ids[f.nativeID()] = true
	}
	if !ids[piSession] || !ids[other] {
		t.Fatalf("native ids = %v", ids)
	}
}
