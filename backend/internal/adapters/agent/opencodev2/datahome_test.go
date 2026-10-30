package opencodev2

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestDataHomeIsSiblingOfUserDataHome(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("XDG_DATA_HOME", parent)
	got, err := DataHome(context.Background())
	if err != nil || got != filepath.Join(parent, "opencode-v2-home") {
		t.Fatalf("DataHome = (%q, %v)", got, err)
	}
	t.Setenv("XDG_DATA_HOME", got)
	if again, _ := DataHome(context.Background()); again != got {
		t.Fatalf("DataHome is not idempotent: %q != %q", again, got)
	}
}

func TestDataHomeRejectsRelativeXDGDataHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "relative/data")
	if _, err := DataHome(context.Background()); err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("DataHome error = %v, want absolute-path rejection", err)
	}
}

func TestDataHomeMigratesLegacyStoreWithoutOverwritingIsolatedState(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("XDG_DATA_HOME", parent)
	legacy := filepath.Join(parent, "opencode")
	if err := os.MkdirAll(filepath.Join(legacy, "storage"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "auth.json"), []byte("legacy-auth"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "storage", "session.json"), []byte("legacy-session"), 0o600); err != nil {
		t.Fatal(err)
	}

	home, err := DataHome(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"auth.json": "legacy-auth", filepath.Join("storage", "session.json"): "legacy-session"} {
		got, err := os.ReadFile(filepath.Join(home, "opencode", name))
		if err != nil || string(got) != want {
			t.Fatalf("migrated %s = %q, %v; want %q", name, got, err, want)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "opencode", "auth.json"), []byte("isolated-auth"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := DataHome(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(home, "opencode", "auth.json"))
	if err != nil || string(got) != "isolated-auth" {
		t.Fatalf("existing isolated auth = %q, %v; want preserved", got, err)
	}
}

func TestLaunchCommandIsolatesDataHome(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("XDG_DATA_HOME", parent)
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	argv, err := command(context.Background(), "/bin/opencode", ports.LaunchConfig{SessionID: "s1"}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"env", "XDG_DATA_HOME=" + filepath.Join(parent, "opencode-v2-home"), "/bin/opencode", "--standalone"}
	if len(argv) != len(want) {
		t.Fatalf("argv = %#v, want %#v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv = %#v, want %#v", argv, want)
		}
	}
}
