package hermes

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A configured --model/--reasoning is a creation override: Hermes rejects it on
// resume ("Resume retains gateway session policy; creation overrides are
// unsupported on resume"), so the restore command must drop both flags even
// when the session carries them — otherwise the Chat→TUI switch surfaces a
// terminal that dies at startup with the flag-combination error. A fresh
// launch keeps them.
func TestRestoreDropsCreationOverridesLaunchKeeps(t *testing.T) {
	binary := fakeNativeBinary(t)
	data, home := t.TempDir(), t.TempDir()
	dir := sessionDir(data, "ao-1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"native-home": home, "native-title": "ao-ao-1-abc"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeNativeState(t, home, "20261020_native", "ao-ao-1-abc")

	cfg := ports.AgentConfig{Model: "zai:glm-5.3-flash", Effort: "high"}
	restore, ok, err := New().GetRestoreCommand(t.Context(), ports.RestoreConfig{
		Config:      cfg,
		DataDir:     data,
		Session:     ports.SessionRef{ID: "ao-1", WorkspacePath: "/workspace"},
		Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	want := []string{binary, "chat", "--cli", "--in", "/workspace", "--continue", "ao-ao-1-abc", "--yolo"}
	if !slices.Equal(restore, want) {
		t.Fatalf("restore = %v, want %v (no creation overrides)", restore, want)
	}

	if _, err := newSessionTitle(data, "ao-1"); err != nil {
		t.Fatal(err)
	}
	launch, err := New().GetLaunchCommand(t.Context(), ports.LaunchConfig{
		DataDir: data, SessionID: "ao-1", WorkspacePath: "/workspace",
		Config: cfg, Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(launch, "--model") || !slices.Contains(launch, "--reasoning") {
		t.Fatalf("launch = %v, want creation overrides kept", launch)
	}
}
