package auggie

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestImportedAuggieWorkspaceAndCache(t *testing.T) {
	cmd := []string{"auggie", "--resume", "native-id"}
	appendRestoreWorkspace(&cmd, ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: "/ao/worktree", Metadata: map[string]string{ports.MetadataKeyImportConfigDir: "/native/cache"}}})
	want := []string{"auggie", "--resume", "native-id", "--workspace-root", "/ao/worktree", "--augment-cache-dir", "/native/cache"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("restore=%v want=%v", cmd, want)
	}
}

func TestAuggieNativeProbeRefusesMissingAndSymlink(t *testing.T) {
	root := t.TempDir()
	id := "11111111-2222-4333-8444-555555555555"
	ref := ports.NativeSessionRef{NativeSessionID: id, ConfigDir: root}
	p := New()
	if got, err := p.ProbeNativeSession(context.Background(), ref); err != nil || got != ports.NativeSessionAvailabilityUnavailable {
		t.Fatalf("missing=%s err=%v", got, err)
	}
	dir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "native.json")
	if err := os.WriteFile(outside, []byte(`{"sessionId":"`+id+`","created":"now","modified":"now","chatHistory":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, id+".json")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := ReadNativeImportSession(context.Background(), root, id); err == nil {
		t.Fatal("followed native transcript symlink")
	}
	if _, err := ReadNativeImportSession(context.Background(), root, "../escape"); err == nil {
		t.Fatal("accepted traversal id")
	}
}
