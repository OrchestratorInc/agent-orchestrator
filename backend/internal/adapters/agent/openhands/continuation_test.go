package openhands

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestImportedOpenHandsProbe(t *testing.T) {
	root := t.TempDir()
	id := "12345678-1234-1234-1234-123456789012"
	dir := filepath.Join(root, "12345678123412341234123456789012")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "base_state.json")
	if err := os.WriteFile(path, []byte(`{"id":"`+id+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New()
	ref := ports.NativeSessionRef{NativeSessionID: id, ConfigDir: root}
	got, ok, err := p.LocateTranscript(context.Background(), ref)
	if err != nil || !ok || got != path {
		t.Fatalf("locate = %q, %v, %v", got, ok, err)
	}
	availability, err := p.ProbeNativeSession(context.Background(), ref)
	if err != nil || availability != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("probe = %q, %v", availability, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	availability, err = p.ProbeNativeSession(context.Background(), ref)
	if err != nil || availability != ports.NativeSessionAvailabilityUnavailable {
		t.Fatalf("missing = %q, %v", availability, err)
	}
}
