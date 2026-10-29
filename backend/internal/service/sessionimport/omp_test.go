package sessionimport

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const ompSession = "01a17cf3-99d0-76cd-b084-4cdce17a0fec"

// ompFile names a transcript the way OMP does: <ISO-time>_<uuid>.jsonl under a
// dashed-cwd folder. Shapes below are synthetic but mirror a real session v3.
func ompFile(root string) string {
	return filepath.Join(root, "sessions", "-work-repo", "2026-10-27T10-41-41-840Z_"+ompSession+".jsonl")
}

func TestOMPTranscriptKeepsOnlyUserAndAssistantText(t *testing.T) {
	body := `{"type":"session","version":3,"id":"` + ompSession + `","timestamp":"2026-10-27T10:41:41.840Z","cwd":"/work/repo"}
{"type":"title","v":1,"title":"","updatedAt":"2026-10-27T10:41:41.840Z"}
{"type":"model_change","id":"b1","parentId":null,"timestamp":"2026-10-27T10:41:41.913Z","model":"zai/glm-5"}
{"type":"message","id":"m1","parentId":null,"timestamp":"2026-10-27T10:41:42.000Z","message":{"role":"user","content":[{"type":"text","text":"start the refactor"}]}}
{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-10-27T10:41:43.000Z","message":{"role":"assistant","content":[{"type":"thinking","text":"hidden reasoning"},{"type":"toolCall","text":"run tests"},{"type":"text","text":"Working on it."}]}}
{"type":"custom","customType":"tool_execution_start","id":"c1","parentId":"m2","timestamp":"2026-10-27T10:41:44.000Z","data":{}}
{"type":"message","id":"m3","parentId":"m2","timestamp":"2026-10-27T10:41:45.000Z","message":{"role":"toolResult","content":[{"type":"text","text":"tool output noise"}]}}
{"type":"title_change","title":"Refactor the parser"}
{"type":"message","id":"m4","parentId":"m3","timestamp":"2026-10-27T10:41:46.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Done."}]}}
{"type":"custom","customType":"system-prompt-digest","id":"c2","timestamp":"2026-10-27T10:41:47.000Z","data":{}}
`
	root := t.TempDir()
	path := writeFile(t, ompFile(root), body)
	got, err := readTranscript(context.Background(), domain.HarnessOMP, root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: start the refactor", "assistant: Working on it.", "assistant: Done.")
	if got.nativeID != ompSession {
		t.Fatalf("nativeID = %q, want %q", got.nativeID, ompSession)
	}
	if got.cwd != "/work/repo" {
		t.Fatalf("cwd = %q", got.cwd)
	}
	if got.title != "Refactor the parser" {
		t.Fatalf("title = %q", got.title)
	}
	if got.shortened {
		t.Fatal("shortened should be false")
	}
}

func TestOMPTitleFallsBackToFirstPrompt(t *testing.T) {
	body := `{"type":"session","version":3,"id":"` + ompSession + `","timestamp":"2026-10-27T10:41:41.840Z","cwd":"/work/repo"}
{"type":"message","id":"m1","parentId":null,"timestamp":"2026-10-27T10:41:42.000Z","message":{"role":"user","content":[{"type":"text","text":"explain the build"}]}}
{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-10-27T10:41:43.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Sure."}]}}
`
	root := t.TempDir()
	got, err := readTranscript(context.Background(), domain.HarnessOMP, root, writeFile(t, ompFile(root), body))
	if err != nil {
		t.Fatal(err)
	}
	if got.title != "explain the build" {
		t.Fatalf("title = %q, want first prompt", got.title)
	}
}

func TestOMPRejectsIdentityMismatchAndPromptlessSessions(t *testing.T) {
	root := t.TempDir()
	// The session header's id must match the id in the file name.
	mismatch := `{"type":"session","version":3,"id":"00000000-0000-7000-8000-000000000000","cwd":"/work/repo"}
{"type":"message","id":"m1","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}
`
	if _, err := readTranscript(context.Background(), domain.HarnessOMP, root, writeFile(t, ompFile(root), mismatch)); err == nil {
		t.Fatal("expected native identity mismatch")
	}
	// A session with no user prompt is not a conversation to continue.
	noPrompt := `{"type":"session","version":3,"id":"` + ompSession + `","cwd":"/work/repo"}
{"type":"message","id":"a1","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]}}
`
	if _, err := readTranscript(context.Background(), domain.HarnessOMP, root, writeFile(t, ompFile(root), noPrompt)); err == nil {
		t.Fatal("expected promptless session to be rejected")
	}
}

func TestOMPNativeIDFromPath(t *testing.T) {
	if got := ompNativeIDFromPath("2026-10-27T10-41-41-840Z_" + ompSession + ".jsonl"); got != ompSession {
		t.Fatalf("name only: got %q, want %q", got, ompSession)
	}
	if got := ompNativeIDFromPath("/root/sessions/-a-b/2026-10-27T10-41-41-840Z_" + ompSession + ".jsonl"); got != ompSession {
		t.Fatalf("full path: got %q, want %q", got, ompSession)
	}
	if got := ompNativeIDFromPath("noseparator.jsonl"); got != "noseparator" {
		t.Fatalf("no separator: got %q, want %q", got, "noseparator")
	}
	// sourceFiles derives the same grouping id from the file name.
	f := sourceFiles{harness: domain.HarnessOMP, paths: []string{ompFile("/root")}}
	if got := f.nativeID(); got != ompSession {
		t.Fatalf("sourceFiles.nativeID() = %q, want %q", got, ompSession)
	}
}

func TestOMPImportsAreHistoryOnly(t *testing.T) {
	if !historyOnlyImport(domain.HarnessOMP) {
		t.Fatal("OMP imports must be history-only")
	}
	for _, h := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		if historyOnlyImport(h) {
			t.Fatalf("%s imports must not be history-only", h)
		}
	}
}
