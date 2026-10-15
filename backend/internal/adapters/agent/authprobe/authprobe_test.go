package authprobe

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCLIStatus_Mocked(t *testing.T) {
	tests := []struct {
		name       string
		mockOutput string
		mockError  error
		wantStatus ports.AgentAuthStatus
		wantError  bool
	}{
		{
			name:       "authorized status check",
			mockOutput: "User is logged in and authenticated",
			wantStatus: ports.AgentAuthStatusAuthorized,
		},
		{
			name:       "unauthorized status check",
			mockOutput: "You are not logged in",
			wantStatus: ports.AgentAuthStatusUnauthorized,
		},
		{
			name:       "unknown status check with exit error",
			mockOutput: "command not found or invalid syntax",
			mockError:  errors.New("exit status 1"),
			wantStatus: ports.AgentAuthStatusUnknown,
		},
		{
			name:       "unknown status check with success but unrecognized output",
			mockOutput: "some random output here",
			wantStatus: ports.AgentAuthStatusUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore CmdRunner
			oldCmdRunner := CmdRunner
			defer func() { CmdRunner = oldCmdRunner }()

			CmdRunner = func(ctx context.Context, name string, arg ...string) ([]byte, error) {
				return []byte(tt.mockOutput), tt.mockError
			}

			status, _, err := CLIStatusWithDiagnostics(context.Background(), "mockbinary", [][]string{{"auth", "status"}}, 50*time.Millisecond, nil)
			if (err != nil) != tt.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if status != tt.wantStatus {
				t.Errorf("CLIStatusWithDiagnostics() = %v, want %v", status, tt.wantStatus)
			}
		})
	}
}

func TestCLIStatusRequiresExplicitCommands(t *testing.T) {
	oldCmdRunner := CmdRunner
	defer func() { CmdRunner = oldCmdRunner }()
	called := false
	CmdRunner = func(ctx context.Context, name string, arg ...string) ([]byte, error) {
		called = true
		return []byte("authenticated"), nil
	}

	status, _, err := CLIStatusWithDiagnostics(context.Background(), "mockbinary", nil, time.Second, nil)
	if err != nil {
		t.Fatalf("CLIStatusWithDiagnostics: %v", err)
	}
	if status != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want %q", status, ports.AgentAuthStatusUnknown)
	}
	if called {
		t.Fatal("CLIStatusWithDiagnostics ran a command without explicit adapter commands")
	}
}

func TestCLIStatusTimeoutDegradesToUnknown(t *testing.T) {
	oldCmdRunner := CmdRunner
	defer func() { CmdRunner = oldCmdRunner }()
	CmdRunner = func(ctx context.Context, name string, arg ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	status, _, err := CLIStatusWithDiagnostics(context.Background(), "mockbinary", [][]string{{"auth", "status"}}, 50*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("CLIStatusWithDiagnostics: %v", err)
	}
	if status != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want %q", status, ports.AgentAuthStatusUnknown)
	}
}

func TestStatusFromTextExplicitFalseKeys(t *testing.T) {
	tests := []string{
		`{ "authenticated": false }`,
		`{ "authorized": false }`,
		`authenticated=false`,
		`authorized: false`,
		`{ "logged_in": false }`,
		`{ "loggedIn": false }`,
	}

	for _, out := range tests {
		t.Run(out, func(t *testing.T) {
			if got := StatusFromText(out); got != ports.AgentAuthStatusUnauthorized {
				t.Fatalf("StatusFromText(%q) = %q, want %q", out, got, ports.AgentAuthStatusUnauthorized)
			}
		})
	}
}

func TestStatusFromTextExplicitTrueKeys(t *testing.T) {
	tests := []string{
		`{ "authenticated": true }`,
		`{ "authorized": true }`,
		`{ "loggedIn": true }`,
	}

	for _, out := range tests {
		t.Run(out, func(t *testing.T) {
			if got := StatusFromText(out); got != ports.AgentAuthStatusAuthorized {
				t.Fatalf("StatusFromText(%q) = %q, want %q", out, got, ports.AgentAuthStatusAuthorized)
			}
		})
	}
}
