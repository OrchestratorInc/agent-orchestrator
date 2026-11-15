package copilot

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNativeConversationID(t *testing.T) {
	p := New()
	session := ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: " tui-id "}}
	if id, ok, err := p.NativeConversationID(context.Background(), session, domain.SessionModeTUI, "chat-id"); err != nil || !ok || id != "tui-id" {
		t.Errorf("TUI: id=%q ok=%v err=%v, want tui-id", id, ok, err)
	}
	if id, ok, err := p.NativeConversationID(context.Background(), session, domain.SessionModeChat, " chat-id "); err != nil || !ok || id != "chat-id" {
		t.Errorf("Chat: id=%q ok=%v err=%v, want chat-id", id, ok, err)
	}
	if _, ok, _ := p.NativeConversationID(context.Background(), ports.SessionRef{}, domain.SessionModeTUI, ""); ok {
		t.Error("TUI without a captured id reported one")
	}
}

func TestNativeConversationExists(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{"COPILOT_HOME": home}
	const id = "17212cd9-ad85-429e-9b76-cf2eaebeb6ef"
	write := func(body string) {
		dir := filepath.Join(home, "session-state", id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	exists := func(value string) bool {
		t.Helper()
		ok, err := New().NativeConversationExists(context.Background(), ports.SessionRef{}, value, env)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	if exists(id) {
		t.Error("missing session reported as existing")
	}
	write("") // directory without events.jsonl: session/load answers not found
	if exists(id) {
		t.Error("session without events reported as existing")
	}
	write(`{"type":"session.start","data":{}}` + "\n")
	if exists(id) {
		t.Error("session with no user message reported as existing")
	}
	write(`{"type":"session.start","data":{}}` + "\n" + `{"type":"user.message","data":{"content":"hi"}}` + "\n")
	if !exists(id) {
		t.Error("session with a user message reported as missing")
	}
	if exists("../" + id) {
		t.Error("non-UUID id accepted")
	}
}
