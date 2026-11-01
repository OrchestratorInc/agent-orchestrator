package sessionimport

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A long Kimi session is bounded while it is read, as Claude and Pi are: the
// oldest text goes first, the opening prompt stays, and an undo still removes
// exactly the turns Kimi removed.
func TestKimiHeldTextIsBoundedWhileReading(t *testing.T) {
	held := maxClaudeHeld
	maxClaudeHeld = 60
	t.Cleanup(func() { maxClaudeHeld = held })

	prompt := func(id, text string) string {
		return fmt.Sprintf(`{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":%q}],"origin":{"kind":"user"},"id":%q},"time":1700000001000}`, text, id)
	}
	answer := func(text string) string {
		return fmt.Sprintf(`{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","turnId":0,"step":1,"part":{"type":"text","text":%q}},"time":1700000002000}`, text)
	}
	lines := []string{`{"type":"metadata","protocol_version":2,"created_at":1700000000000}`, prompt("m0", "opening prompt")}
	for i := 1; i <= 6; i++ {
		lines = append(lines, answer(fmt.Sprintf("answer %d %s", i, strings.Repeat("x", 12))), prompt(fmt.Sprintf("m%d", i), fmt.Sprintf("prompt %d", i)))
	}
	lines = append(lines, answer("final answer"), `{"type":"context.undo","count":1,"time":1700000009000}`)
	root := t.TempDir()
	path := kimiFixture(t, root, "wd_repo_abc123def456", kimiSession, kimiStateJSON, strings.Join(lines, "\n")+"\n")
	got, err := readTranscript(context.Background(), domain.HarnessKimi, root, path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.shortened || len(got.messages) == 0 || got.messages[0].Text != "opening prompt" {
		t.Fatalf("shortened=%v messages=%q", got.shortened, texts(got.messages))
	}
	// The undo retracted the last prompt and its answer; the newest survivor
	// is the answer before it.
	if last := got.messages[len(got.messages)-1]; last.Role != domain.MessageRoleAssistant || !strings.HasPrefix(last.Text, "answer 6 ") {
		t.Fatalf("newest message = %q, want answer 6", last.Text)
	}
	for _, m := range got.messages {
		if strings.HasPrefix(m.Text, "answer 1 ") {
			t.Fatalf("the oldest text was kept past the bound: %q", texts(got.messages))
		}
	}
}
