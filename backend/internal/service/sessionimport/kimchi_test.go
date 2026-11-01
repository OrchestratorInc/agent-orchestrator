package sessionimport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const kimchiSession = "019fe4d5-24c5-77ac-a0e3-d3c18b37dcef"

// kimchiPath is a Kimchi session file as Kimchi writes it:
// sessions/--<cwd-dashed>--/<ISO>_<uuid>.jsonl.
func kimchiPath(root, folder, id string) string {
	return filepath.Join(root, "sessions", folder, "2026-08-09T04-43-21-669Z_"+id+".jsonl")
}

// kimchiMsg is one Kimchi `message` record linked into the tree by id/parentId.
func kimchiMsg(id, parent, role, text string) string {
	return `{"type":"message","id":"` + id + `","parentId":"` + parent + `","timestamp":"2026-08-09T04:43:30.000Z","message":{"role":"` + role + `","content":[{"type":"text","text":"` + text + `"}]}}`
}

// Shapes below are synthetic but mirror real Kimchi session JSONL: a single
// id/parentId chain of session, model_change, thinking_level_change, message,
// session_info and custom records.
func TestKimchiTranscriptKeepsOnlyUserAndAssistantText(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, kimchiPath(root, "--work-repo--", kimchiSession), `{"type":"session","version":3,"id":"`+kimchiSession+`","timestamp":"2026-08-09T04:43:21.669Z","cwd":"/work/repo"}
{"type":"model_change","id":"m1","parentId":null,"timestamp":"2026-08-09T04:43:22.000Z","provider":"anthropic","modelId":"claude-opus"}
{"type":"thinking_level_change","id":"t1","parentId":"m1","timestamp":"2026-08-09T04:43:22.100Z","thinkingLevel":"high"}
{"type":"custom","customType":"ui","data":{"x":1},"id":"c1","parentId":"t1","timestamp":"2026-08-09T04:43:22.200Z"}
`+kimchiMsg("u1", "c1", "user", "add a greeting")+`
{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-08-09T04:43:40.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"private reasoning"},{"type":"text","text":"I'll add it now."},{"type":"toolCall","id":"tc1","name":"edit","arguments":{}}]}}
{"type":"message","id":"tr1","parentId":"a1","timestamp":"2026-08-09T04:43:41.000Z","message":{"role":"toolResult","content":[{"type":"text","text":"patched hero.tsx"}]}}
{"type":"session_info","id":"si1","parentId":"tr1","timestamp":"2026-08-09T04:43:42.000Z","name":"Hero greeting"}
{"type":"custom_message","customType":"note","content":"ambient","display":"hidden","id":"cm1","parentId":"si1","timestamp":"2026-08-09T04:43:43.000Z"}
`+kimchiMsg("a2", "cm1", "assistant", "Done.")+`
`)
	got, err := readTranscript(context.Background(), domain.HarnessKimchi, root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: add a greeting", "assistant: I'll add it now.", "assistant: Done.")
	if got.cwd != "/work/repo" {
		t.Fatalf("cwd = %q", got.cwd)
	}
	if got.nativeID != kimchiSession {
		t.Fatalf("nativeID = %q", got.nativeID)
	}
	if got.title != "Hero greeting" {
		t.Fatalf("title = %q, want session_info name", got.title)
	}
	if got.model != "anthropic/claude-opus" {
		t.Fatalf("model = %q", got.model)
	}
	if got.effort != "high" {
		t.Fatalf("effort = %q", got.effort)
	}
}

func TestKimchiTranscriptTitleFallsBackToFirstPrompt(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, kimchiPath(root, "--work-repo--", kimchiSession), `{"type":"session","version":3,"id":"`+kimchiSession+`","timestamp":"2026-08-09T04:43:21.669Z","cwd":"/work/repo"}
`+kimchiMsg("u1", kimchiSession, "user", "what does this repo do")+`
`+kimchiMsg("a1", "u1", "assistant", "it imports sessions")+`
`)
	got, err := readTranscript(context.Background(), domain.HarnessKimchi, root, path)
	if err != nil {
		t.Fatal(err)
	}
	if got.title != "what does this repo do" {
		t.Fatalf("title = %q, want first prompt", got.title)
	}
}

// A rewind sends a new prompt under an earlier parent; the abandoned turns stay
// in the file but must not appear beside their replacement.
func TestKimchiTranscriptFollowsActiveBranchAfterRewind(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, kimchiPath(root, "--work-repo--", kimchiSession), `{"type":"session","version":3,"id":"`+kimchiSession+`","timestamp":"2026-08-09T04:43:21.669Z","cwd":"/work/repo"}
`+kimchiMsg("u1", kimchiSession, "user", "first question")+`
`+kimchiMsg("a1", "u1", "assistant", "first answer")+`
`+kimchiMsg("u2a", "a1", "user", "abandoned followup")+`
`+kimchiMsg("a2a", "u2a", "assistant", "abandoned answer")+`
`+kimchiMsg("u2b", "a1", "user", "real followup")+`
`+kimchiMsg("a2b", "u2b", "assistant", "real answer")+`
`)
	got, err := readTranscript(context.Background(), domain.HarnessKimchi, root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: first question", "assistant: first answer", "user: real followup", "assistant: real answer")
}

func TestKimchiTranscriptRejectsNativeIDMismatch(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, kimchiPath(root, "--work-repo--", kimchiSession), `{"type":"session","version":3,"id":"019f0000-0000-7000-8000-000000000000","timestamp":"2026-08-09T04:43:21.669Z","cwd":"/work/repo"}
`+kimchiMsg("u1", kimchiSession, "user", "hi")+`
`)
	if _, err := readTranscript(context.Background(), domain.HarnessKimchi, root, path); err == nil {
		t.Fatal("expected native identity mismatch error")
	}
}

func TestKimchiTranscriptRejectsToolOnlyConversation(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, kimchiPath(root, "--work-repo--", kimchiSession), `{"type":"session","version":3,"id":"`+kimchiSession+`","timestamp":"2026-08-09T04:43:21.669Z","cwd":"/work/repo"}
`+kimchiMsg("a1", kimchiSession, "assistant", "hello there")+`
`)
	if _, err := readTranscript(context.Background(), domain.HarnessKimchi, root, path); err == nil {
		t.Fatal("expected error for a conversation with no user prompt")
	}
}

func TestKimchiDiscoverGroupsEachFileByNativeID(t *testing.T) {
	root := t.TempDir()
	writeFile(t, kimchiPath(root, "--work-repo--", kimchiSession), `{"type":"session","version":3,"id":"`+kimchiSession+`","timestamp":"2026-08-09T04:43:21.669Z","cwd":"/work/repo"}
`)
	other := "019fe4d5-2222-77ac-a0e3-d3c18b37dcef"
	writeFile(t, kimchiPath(root, "--work-other--", other), `{"type":"session","version":3,"id":"`+other+`","timestamp":"2026-08-09T05:43:21.669Z","cwd":"/work/other"}
`)
	svc := New(&importStore{}, "")
	files, err := svc.discover(context.Background(), domain.HarnessKimchi, canonical(root))
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
	if !ids[kimchiSession] || !ids[other] {
		t.Fatalf("native ids = %v", ids)
	}
}

// AO's own resume copies live in the Kimchi sessions tree under a folder whose
// header cwd is inside AO's data directory; discovery must skip them so the
// original is not offered twice (and the AO copy, which carries AO's turns, is
// never imported as a fresh chat).
func TestKimchiDiscoverSkipsAOResumeCopies(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	// The original conversation, in the user's own repository.
	writeFile(t, kimchiPath(root, "--work-repo--", kimchiSession), `{"type":"session","version":3,"id":"`+kimchiSession+`","timestamp":"2026-08-09T04:43:21.669Z","cwd":"/work/repo"}
`+kimchiMsg("u1", kimchiSession, "user", "hi")+`
`)
	// AO's resume copy of the same conversation, keyed by the AO worktree path.
	// The worktree must exist on disk so its path resolves to the same physical
	// form as the excluded data dir (for example /var vs /private/var on macOS).
	worktree := filepath.Join(dataDir, "worktrees", "proj", "sess")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, kimchiPath(root, "--"+folderDashes(worktree)+"--", kimchiSession), `{"type":"session","version":3,"id":"`+kimchiSession+`","timestamp":"2026-08-09T06:43:21.669Z","cwd":"`+worktree+`"}
`+kimchiMsg("u1", kimchiSession, "user", "hi")+`
`)
	svc := New(&importStore{}, dataDir)
	files, err := svc.discover(context.Background(), domain.HarnessKimchi, canonical(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("discovered %d files, want 1 (AO resume copy must be skipped)", len(files))
	}
}

// folderDashes encodes an absolute path the way Kimchi names a session folder:
// the path without its leading slash, every '/' replaced by '-'.
func folderDashes(abs string) string {
	out := make([]rune, 0, len(abs))
	for i, r := range abs {
		if i == 0 && (r == '/' || r == '\\') {
			continue
		}
		if r == '/' || r == '\\' || r == ':' {
			out = append(out, '-')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
