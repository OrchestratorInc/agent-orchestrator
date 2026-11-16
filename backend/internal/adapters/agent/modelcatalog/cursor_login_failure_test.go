package modelcatalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Cursor's `status` reads only the local login, so a rejected saved login is
// first seen when `models` fails. That failure must read as sign-in required,
// so the service keeps the saved catalog instead of an empty, retried error.
func TestCursorRejectedLoginIsSignInRequired(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI is a shell script")
	}
	for _, tc := range []struct {
		name       string
		script     string
		wantSignIn bool
	}{
		{
			name:       "rejected login",
			script:     "#!/bin/sh\necho 'Error: Authentication required. Please run agent login first, or set CURSOR_API_KEY environment variable.' >&2\nexit 1\n",
			wantSignIn: true,
		},
		{
			name:   "unrelated failure",
			script: "#!/bin/sh\necho 'Error: getaddrinfo ENOTFOUND api2.cursor.sh' >&2\nexit 1\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "cursor-agent")
			if err := os.WriteFile(binary, []byte(tc.script), 0o755); err != nil {
				t.Fatal(err)
			}
			env := map[string]string{"HOME": t.TempDir()}
			_, err := discoverCommandCatalog(context.Background(), "cursor", binary, t.TempDir(), env, false)
			if err == nil {
				t.Fatal("discovery succeeded, want a failure")
			}
			if got := errors.Is(err, ports.ErrAgentModelDiscoverySignInRequired); got != tc.wantSignIn {
				t.Fatalf("sign-in required = %v, want %v (err %v)", got, tc.wantSignIn, err)
			}
		})
	}
}
