package cursor

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCursorCLIAuthStatusAuthorizedFromStatus(t *testing.T) {
	restore := stubCursorAuthCommand(t, []string{"status"}, []byte("✓ Logged in as user@example.com\n"), nil)
	defer restore()

	t.Setenv("CURSOR_API_KEY", "")
	status, err := (&Plugin{resolvedBinary: "cursor-agent"}).AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = %q, want %q", status, ports.AgentAuthStatusAuthorized)
	}
}

func TestCursorCLIAuthStatusUnknownFromKeychainError(t *testing.T) {
	restore := stubCursorAuthCommand(t, []string{"status"}, []byte("ERROR: SecItemCopyMatching failed -50\n"), assertErr("exit status 139"))
	defer restore()

	t.Setenv("CURSOR_API_KEY", "")
	status, err := (&Plugin{resolvedBinary: "cursor-agent"}).AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want %q", status, ports.AgentAuthStatusUnknown)
	}
}

type assertErr string

func (e assertErr) Error() string {
	return string(e)
}

func stubCursorAuthCommand(t *testing.T, wantArgs []string, out []byte, err error) func() {
	t.Helper()
	root := t.TempDir()
	name := "cursor-agent"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte("MZ fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	previous := authprobe.CmdRunner
	authprobe.CmdRunner = func(ctx context.Context, name string, arg ...string) ([]byte, error) {
		if strings.TrimSuffix(filepath.Base(name), ".exe") != "cursor-agent" || !reflect.DeepEqual(arg, wantArgs) {
			t.Fatalf("command = %s %#v, want cursor-agent %#v", name, arg, wantArgs)
		}
		return out, err
	}
	return func() { authprobe.CmdRunner = previous }
}

func TestCursorAuthRejectsRedirectBeforeCommand(t *testing.T) {
	previous := authprobe.CmdRunner
	t.Cleanup(func() { authprobe.CmdRunner = previous })
	authprobe.CmdRunner = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("auth command ran through an Autohand launcher")
		return nil, nil
	}
	root := t.TempDir()
	binary := filepath.Join(root, "agent.cmd")
	marker := filepath.Join(root, "login-started")
	if err := os.WriteFile(binary, []byte("@echo login > \""+marker+"\"\n@call autohand %*\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Even a configured API key must not hide an unsafe launcher.
	t.Setenv("CURSOR_API_KEY", "fixture-key")
	plugin := &Plugin{resolvedBinary: binary}
	for range 2 {
		status, err := plugin.AuthStatus(context.Background())
		if status != ports.AgentAuthStatusUnknown || err == nil || !strings.Contains(err.Error(), "autohand") {
			t.Fatalf("auth = %s, %v; want redirect refusal", status, err)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("launcher executed: %v", err)
	}
}
