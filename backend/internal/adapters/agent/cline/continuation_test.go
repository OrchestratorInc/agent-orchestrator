package cline

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestClineNativeContinuation(t *testing.T) {
	root := t.TempDir()
	id := "1790000000000_synthetic"
	path := filepath.Join(root, "sessions", id, id+".messages.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"sessionId":"`+id+`","messages":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New()
	t.Setenv("CLINE_DATA_DIR", root)
	got, err := p.NativeSessionConfigDir(context.Background(), nil)
	if err != nil || got != root {
		t.Fatalf("config = %q, %v", got, err)
	}
	ref := ports.NativeSessionRef{ConfigDir: root, NativeSessionID: id}
	located, exists, err := p.LocateTranscript(context.Background(), ref)
	if err != nil || !exists || located != path {
		t.Fatalf("transcript = %q, %v, %v", located, exists, err)
	}
	ref.NativeSessionID = "../escape"
	if _, _, err := p.LocateTranscript(context.Background(), ref); err == nil {
		t.Fatal("accepted traversal")
	}
	got, err = p.NativeSessionConfigDir(context.Background(), map[string]string{"CLINE_DATA_DIR": "", "CLINE_DIR": root})
	if err != nil || got != filepath.Join(root, "data") {
		t.Fatalf("config override = %q, %v", got, err)
	}
}
