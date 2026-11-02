package vibe

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestImportedVibeContinuation(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "logs", "session", "session_synthetic_12345678")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	id := "12345678-1234-1234-1234-123456789012"
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"session_id":"`+id+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "messages.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"synthetic"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New()
	got, err := p.NativeSessionConfigDir(context.Background(), map[string]string{"VIBE_HOME": root})
	if err != nil || got != root {
		t.Fatalf("config = %q, %v", got, err)
	}
	ref := ports.NativeSessionRef{NativeSessionID: id, ConfigDir: root}
	got, ok, err := p.LocateTranscript(context.Background(), ref)
	if err != nil || !ok || got != path {
		t.Fatalf("locate = %q, %v, %v", got, ok, err)
	}
	available, err := p.ProbeNativeSession(context.Background(), ref)
	if err != nil || available != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("probe = %q, %v", available, err)
	}
	ref.NativeSessionID = "../escape"
	if _, ok, err := p.LocateTranscript(context.Background(), ref); err != nil || ok {
		t.Fatalf("unsafe id = %v, %v", ok, err)
	}
}
