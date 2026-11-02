package autohand

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAutohandNativeContinuation(t *testing.T) {
	root := t.TempDir()
	id := "11111111-2222-4333-8444-555555555555-1790000000000"
	path := filepath.Join(root, "sessions", id, "conversation.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"synthetic prompt"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "metadata.json"), []byte(`{"sessionId":"`+id+`","projectPath":"/work/original"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New()
	t.Setenv("AUTOHAND_HOME", root)
	got, err := p.NativeSessionConfigDir(context.Background(), nil)
	if err != nil || got != root {
		t.Fatalf("config = %q, %v", got, err)
	}
	ref := ports.NativeSessionRef{ConfigDir: root, NativeSessionID: id}
	located, exists, err := p.LocateTranscript(context.Background(), ref)
	if err != nil || !exists || located != path {
		t.Fatalf("transcript = %q, %v, %v", located, exists, err)
	}
	status, err := p.ProbeNativeSession(context.Background(), ref)
	if err != nil || status != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("probe = %s, %v", status, err)
	}
	ref.NativeSessionID = "../escape"
	if _, _, err := p.LocateTranscript(context.Background(), ref); err == nil {
		t.Fatal("accepted traversal")
	}
	got, err = p.NativeSessionConfigDir(context.Background(), map[string]string{"AUTOHAND_HOME": "", "HOME": root})
	if err != nil || got != filepath.Join(root, ".autohand") {
		t.Fatalf("empty launch override = %q, %v", got, err)
	}
}

func TestAutohandStrictResumeRefusesTornNativeHistory(t *testing.T) {
	root := t.TempDir()
	id := "11111111-2222-4333-8444-555555555555-1790000000000"
	dir := filepath.Join(root, "sessions", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{"sessionId":"`+id+`","projectPath":"/work/original"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		"{\"role\":\"user\",\"content\":\"kept in tolerant preview\"}\n{\"role\":",
		`{"role":"user","content":null}`,
		`{"role":"user","content":"valid"} trailing`,
	} {
		if err := os.WriteFile(filepath.Join(dir, "conversation.jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		p := New()
		status, err := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: id, ConfigDir: root})
		if err == nil || status == ports.NativeSessionAvailabilityAvailable {
			t.Fatalf("malformed native history allowed: status=%s err=%v", status, err)
		}
		found, err := p.NativeConversationExists(context.Background(), ports.SessionRef{WorkspacePath: t.TempDir()}, id, map[string]string{"AUTOHAND_HOME": root})
		if err == nil || found {
			t.Fatalf("strict native gate accepted malformed history: %v, %v", found, err)
		}
	}
}
