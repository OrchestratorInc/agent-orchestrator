package cursor

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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

func TestCursorUnconfirmedLoginIsVerifiedWithModels(t *testing.T) {
	previousConfig := cursorConfigPath
	cursorConfigPath = func() string { return filepath.Join(t.TempDir(), "missing.json") }
	t.Cleanup(func() { cursorConfigPath = previousConfig })
	// The exact status output when Cursor rejects the saved login,
	// from the 2026.04.16 and 2026.11.10 CLI builds.
	const lookupFailed = "Checking authentication status...\n✓ Login successful!\nLogged in (unable to fetch user details)\n"
	const rejected = "Error: Authentication required. Please run 'agent login' first, or set CURSOR_API_KEY environment variable.\n"
	for _, tc := range []struct {
		name        string
		status      string
		models      string
		modelsErr   error
		apiKey      string
		wantStatus  ports.AgentAuthStatus
		wantModels  bool
		unconfirmed bool
	}{
		{name: "account confirmed", status: "✓ Logged in as user@example.com\n", wantStatus: ports.AgentAuthStatusAuthorized},
		{name: "login rejected", status: lookupFailed, models: rejected, modelsErr: assertErr("exit status 1"), wantStatus: ports.AgentAuthStatusUnauthorized, wantModels: true},
		{name: "login rejected with api key", status: lookupFailed, models: rejected, modelsErr: assertErr("exit status 1"), apiKey: "key", wantStatus: ports.AgentAuthStatusAuthorized, wantModels: true},
		{name: "cursor unreachable", status: lookupFailed, models: "Error: getaddrinfo ENOTFOUND api2.cursor.sh\n", modelsErr: assertErr("exit status 1"), wantStatus: ports.AgentAuthStatusAuthorized, wantModels: true, unconfirmed: true},
		{name: "models listed", status: lookupFailed, models: "auto - Auto\ngpt-5 - GPT-5\n", wantStatus: ports.AgentAuthStatusAuthorized, wantModels: true, unconfirmed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CURSOR_API_KEY", tc.apiKey)
			t.Setenv("CURSOR_AUTH_TOKEN", tc.apiKey)
			ranModels := false
			restore := stubCursorAuthCommand(t, []string{"status"}, []byte(tc.status), nil)
			defer restore()
			previousModels := cursorModelsRunner
			t.Cleanup(func() { cursorModelsRunner = previousModels })
			cursorModelsRunner = func(_ context.Context, binary string, env []string) ([]byte, error) {
				ranModels = true
				if binary != "cursor-agent" {
					t.Fatalf("binary = %q, want cursor-agent", binary)
				}
				// The saved login is verified alone: a key in the daemon env
				// would answer for it and hide a rejection.
				for _, entry := range env {
					if strings.HasPrefix(entry, "CURSOR_API_KEY=") || strings.HasPrefix(entry, "CURSOR_AUTH_TOKEN=") {
						t.Fatalf("models env carries %q, want the credential variables removed", entry)
					}
				}
				return []byte(tc.models), tc.modelsErr
			}
			status, _, steps, err := (&Plugin{resolvedBinary: "cursor-agent"}).AuthStatusWithDiagnostics(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", status, tc.wantStatus)
			}
			if ranModels != tc.wantModels {
				t.Fatalf("ran models = %v, want %v", ranModels, tc.wantModels)
			}
			got := slices.ContainsFunc(steps, func(step domain.AgentAuthDiagnostic) bool { return step.Code == "account_unconfirmed" })
			if got != tc.unconfirmed {
				t.Fatalf("account_unconfirmed = %v, want %v (steps %+v)", got, tc.unconfirmed, steps)
			}
			// A models run is listed only when it decided the verdict.
			gotModelsStep := slices.ContainsFunc(steps, func(step domain.AgentAuthDiagnostic) bool { return strings.HasSuffix(step.Method, " models") })
			if wantModelsStep := tc.wantModels && !tc.unconfirmed; gotModelsStep != wantModelsStep {
				t.Fatalf("models step = %v, want %v (steps %+v)", gotModelsStep, wantModelsStep, steps)
			}
		})
	}
}

func TestCursorRejectedLoginClearsStatusEvidence(t *testing.T) {
	t.Setenv("CURSOR_API_KEY", "")
	t.Setenv("CURSOR_AUTH_TOKEN", "")
	restore := stubCursorAuthCommand(t, []string{"status"}, []byte("Logged in (unable to fetch user details)\n"), nil)
	defer restore()
	previousModels := cursorModelsRunner
	t.Cleanup(func() { cursorModelsRunner = previousModels })
	cursorModelsRunner = func(context.Context, string, []string) ([]byte, error) {
		return []byte("Error: Authentication required.\n"), assertErr("exit status 1")
	}
	ctx, evidence := ports.WithAuthEvidence(context.Background())
	status, _, _, err := (&Plugin{resolvedBinary: "cursor-agent"}).AuthStatusWithDiagnostics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("status = %q, want unauthorized", status)
	}
	if source := evidence.Identity().Sanitized(); source != nil && source.Source != "" {
		t.Fatalf("evidence = %+v, want the rejected status login cleared", source)
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
