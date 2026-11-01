package sessionimport

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Shapes below are trimmed from real Factory Droid session JSONL.
func TestDroidTranscriptShapes(t *testing.T) {
	id := "7f0c09e2-ce0e-43f6-ad51-a2925a075e70"
	body := `{"type":"session_start","id":"` + id + `","title":"Refactor the parser","owner":"dev","version":2,"cwd":"/work/repo","hostId":"h1"}
{"type":"message","id":"context-1","timestamp":"2026-07-08T17:03:58Z","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>\nThe tools listed below are available in this environment.\n</system-reminder>"}]},"parentId":"root"}
{"type":"message","id":"u1","timestamp":"2026-07-08T17:03:59Z","message":{"role":"user","content":[{"type":"text","text":"start the refactor"}]},"parentId":"context-1"}
{"type":"message","id":"a1","timestamp":"2026-07-08T17:04:04Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hidden","signature":"sig"},{"type":"text","text":"Working on it."}],"modelId":"custom:Sonnet-4.5"}}
{"type":"message","id":"a1b","timestamp":"2026-07-08T17:04:05Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Grep","input":{"pattern":"x"}}]}}
{"type":"message","id":"u2","timestamp":"2026-07-08T17:04:06Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"a match"}]},"parentId":"a1b"}
{"type":"todo_state","id":"td1","timestamp":"2026-07-08T17:04:07Z","todos":{"todos":"1. [pending] refactor"}}
{"type":"message","id":"a2","timestamp":"2026-07-08T17:04:08Z","message":{"role":"assistant","content":[{"type":"text","text":"Done."}]}}
{"type":"agent_turn_outcome","turnId":"t1","reason":"completed","resultKind":"text"}
`
	root := t.TempDir()
	path := writeFile(t, filepath.Join(root, "sessions", "-work-repo", id+".jsonl"), body)
	got, err := readTranscript(context.Background(), domain.HarnessDroid, root, path)
	if err != nil {
		t.Fatal(err)
	}
	// Injected context, thinking, tool_use and tool_result are dropped; only
	// user/assistant text survives.
	wantTexts(t, got.messages, "user: start the refactor", "assistant: Working on it.", "assistant: Done.")
	if got.title != "Refactor the parser" || got.cwd != "/work/repo" || got.nativeID != id || got.shortened {
		t.Fatalf("title=%q cwd=%q id=%q shortened=%v", got.title, got.cwd, got.nativeID, got.shortened)
	}
}

// A not-yet-named chat shows its first prompt as its title, as Droid's own list does.
func TestDroidPlaceholderTitleFallsBackToPrompt(t *testing.T) {
	id := "ec768e73-ea5d-4f6e-838c-f65773e5c6a1"
	body := `{"type":"session_start","id":"` + id + `","title":"New Session","cwd":"/work/repo"}
{"type":"message","id":"u1","timestamp":"2026-07-08T17:00:00Z","message":{"role":"user","content":[{"type":"text","text":"help me rename a function"}]}}
{"type":"message","id":"a1","timestamp":"2026-07-08T17:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"Sure."}]}}
`
	root := t.TempDir()
	path := writeFile(t, filepath.Join(root, "sessions", "-work-repo", id+".jsonl"), body)
	got, err := readTranscript(context.Background(), domain.HarnessDroid, root, path)
	if err != nil {
		t.Fatal(err)
	}
	if got.title != "help me rename a function" {
		t.Fatalf("title=%q", got.title)
	}
}

func TestDroidRejectsBrokenOrEmptyConversations(t *testing.T) {
	root := t.TempDir()
	id := "7f0c09e2-ce0e-43f6-ad51-a2925a075e70"

	// session_start id must match the file name.
	mismatch := writeFile(t, filepath.Join(root, "sessions", "-work-repo", id+".jsonl"),
		`{"type":"session_start","id":"00000000-0000-0000-0000-000000000000","cwd":"/work/repo"}
{"type":"message","id":"u1","timestamp":"2026-07-08T17:00:00Z","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}
`)
	if _, err := readTranscript(context.Background(), domain.HarnessDroid, root, mismatch); err == nil {
		t.Fatal("native id mismatch must fail")
	}

	// A file without a session_start is not a readable native conversation.
	id2 := "ec768e73-ea5d-4f6e-838c-f65773e5c6a1"
	noStart := writeFile(t, filepath.Join(root, "sessions", "-work-repo", id2+".jsonl"),
		`{"type":"message","id":"u1","timestamp":"2026-07-08T17:00:00Z","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}
`)
	if _, err := readTranscript(context.Background(), domain.HarnessDroid, root, noStart); err == nil {
		t.Fatal("missing session_start must fail")
	}

	// Injected context with no real prompt is not a conversation to continue.
	id3 := "a1b2c3d4-0000-4000-8000-000000000000"
	contextOnly := writeFile(t, filepath.Join(root, "sessions", "-work-repo", id3+".jsonl"),
		`{"type":"session_start","id":"`+id3+`","cwd":"/work/repo"}
{"type":"message","id":"context-1","timestamp":"2026-07-08T17:00:00Z","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>noise</system-reminder>"}]}}
{"type":"message","id":"a1","timestamp":"2026-07-08T17:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"Hello!"}]}}
`)
	if _, err := readTranscript(context.Background(), domain.HarnessDroid, root, contextOnly); err == nil {
		t.Fatal("a session with no real prompt is not a conversation")
	}
}

func TestDroidDiscoverGroupsByFileAndSkipsSidecars(t *testing.T) {
	root := t.TempDir()
	id := "7f0c09e2-ce0e-43f6-ad51-a2925a075e70"
	writeFile(t, filepath.Join(root, "sessions", "-work-repo", id+".jsonl"), "{}\n")
	writeFile(t, filepath.Join(root, "sessions", "-work-repo", id+".settings.json"), "{}")
	writeFile(t, filepath.Join(root, "sessions", "-work-repo", id+".settings.json.bak"), "{}")
	files, err := (&Service{}).discover(context.Background(), domain.HarnessDroid, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d conversation groups, want 1", len(files))
	}
	if got := files[0].nativeID(); got != id {
		t.Fatalf("nativeID = %q, want %q", got, id)
	}
}

func TestDroidRootHonorsHomeOverride(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv(droidHomeEnv, "")
		if got := droidRoot("/home/dev"); got != filepath.Join("/home/dev", droidDirName) {
			t.Fatalf("root = %q", got)
		}
	})
	t.Run("override", func(t *testing.T) {
		t.Setenv(droidHomeEnv, "/custom")
		if got := droidRoot("/home/dev"); got != filepath.Join("/custom", droidDirName) {
			t.Fatalf("root = %q", got)
		}
	})
}
