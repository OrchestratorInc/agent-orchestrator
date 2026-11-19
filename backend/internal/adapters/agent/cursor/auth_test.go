package cursor

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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

func TestCursorUnconfirmedLoginStaysAuthorizedWithCaveat(t *testing.T) {
	previousConfig := cursorConfigPath
	cursorConfigPath = func() string { return filepath.Join(t.TempDir(), "missing.json") }
	t.Cleanup(func() { cursorConfigPath = previousConfig })
	t.Setenv("CURSOR_API_KEY", "")
	for _, tc := range []struct {
		name        string
		out         string
		unconfirmed bool
	}{
		// The exact status output when Cursor rejects the saved login,
		// from the 2026.04.16 and 2026.11.10 CLI builds.
		{name: "account lookup failed", out: "Checking authentication status...\n✓ Login successful!\nLogged in (unable to fetch user details)\n", unconfirmed: true},
		{name: "account confirmed", out: "✓ Logged in as user@example.com\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restore := stubCursorAuthCommand(t, []string{"status"}, []byte(tc.out), nil)
			defer restore()
			status, _, steps, err := (&Plugin{resolvedBinary: "cursor-agent"}).AuthStatusWithDiagnostics(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if status != ports.AgentAuthStatusAuthorized {
				t.Fatalf("status = %q, want authorized: a failed lookup can also mean Cursor was unreachable", status)
			}
			got := slices.ContainsFunc(steps, func(step domain.AgentAuthDiagnostic) bool { return step.Code == "account_unconfirmed" })
			if got != tc.unconfirmed {
				t.Fatalf("account_unconfirmed = %v, want %v (steps %+v)", got, tc.unconfirmed, steps)
			}
		})
	}
}

type assertErr string

func (e assertErr) Error() string {
	return string(e)
}

func stubCursorAuthCommand(t *testing.T, wantArgs []string, out []byte, err error) func() {
	t.Helper()
	previous := authprobe.CmdRunner
	authprobe.CmdRunner = func(ctx context.Context, name string, arg ...string) ([]byte, error) {
		if name != "cursor-agent" || !reflect.DeepEqual(arg, wantArgs) {
			t.Fatalf("command = %s %#v, want cursor-agent %#v", name, arg, wantArgs)
		}
		return out, err
	}
	return func() { authprobe.CmdRunner = previous }
}
