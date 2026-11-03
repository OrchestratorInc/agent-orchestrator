package hermes

import (
	"context"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestImportedRestoreUsesNativeIDAndWorkspace(t *testing.T) {
	fakeNativeBinary(t)
	id := "20261102_120000_test"
	metadata := map[string]string{ports.MetadataKeyAgentSessionID: id, ports.MetadataKeyImportSourceNativeID: id}
	cfg := ports.RestoreConfig{DataDir: t.TempDir(), Config: ports.AgentConfig{Model: "zai:glm-5.3-flash", Effort: "high"}, Session: ports.SessionRef{ID: "import-1", WorkspacePath: "/work/new", Metadata: metadata}}
	cmd, ok, err := New().GetRestoreCommand(context.Background(), cfg)
	if err != nil || !ok {
		t.Fatalf("restore=%v %v %v", cmd, ok, err)
	}
	for _, arg := range []string{"--resume", "20261102_120000_test", "--no-restore-cwd", "--in", "/work/new"} {
		if !slices.Contains(cmd, arg) {
			t.Fatalf("missing %s in %v", arg, cmd)
		}
	}
	if slices.Contains(cmd, "--create-if-missing") || slices.Contains(cmd, "--continue") {
		t.Fatalf("import may not create or resume by title: %v", cmd)
	}
	if slices.Contains(cmd, "--model") || slices.Contains(cmd, "--reasoning") {
		t.Fatalf("native import retained creation-only overrides: %v", cmd)
	}
	delete(cfg.Session.Metadata, ports.MetadataKeyImportSourceNativeID)
	if _, ok, err := New().GetRestoreCommand(context.Background(), cfg); ok || err == nil {
		t.Fatal("managed restore accepted an unowned native id")
	}
}

func TestImportedProbeMissingStore(t *testing.T) {
	got, err := New().ProbeNativeSession(context.Background(), ports.NativeSessionRef{ConfigDir: t.TempDir(), NativeSessionID: "20261102_120000_test"})
	if err != nil || got != ports.NativeSessionAvailabilityUnavailable {
		t.Fatalf("probe=%s err=%v", got, err)
	}
}
