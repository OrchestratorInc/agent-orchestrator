package devin

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAuthStatusRecordsNativeAccount(t *testing.T) {
	const report = `Logged in (via Devin).

Credentials:
  File: /home/test/.local/share/devin/credentials.toml
  API key: secret-do-not-display
User:
  Name: Ada Lovelace
  Email: ada@example.com
  User ID: user-do-not-display
Account:
  Tier: Devin Free
  Plan: Free
  Team ID: team-do-not-display
Team settings:
  Name: not-the-user
  Email: not-the-user@example.com
  Plan: not-the-plan
`
	tests := []struct {
		name       string
		output     string
		env        string
		commandErr error
		wantStatus ports.AgentAuthStatus
		want       domain.AgentAccountIdentity
	}{
		{
			name: "native account wins over environment key", output: report, env: "cog_fallback",
			wantStatus: ports.AgentAuthStatusAuthorized,
			want:       domain.AgentAccountIdentity{Email: "ada@example.com", Name: "Ada Lovelace", Plan: "Free", Provider: "Devin", Method: domain.AgentAccountMethodAccount, SourceKind: domain.AgentCredentialSourceCommand, Source: "devin auth status"},
		},
		{
			name: "empty name does not hide email", output: "Logged in (via Devin).\nUser:\n  Name:\n  Email: ada@example.com\nAccount:\n  Plan:\n  Tier: Free\n",
			wantStatus: ports.AgentAuthStatusAuthorized,
			want:       domain.AgentAccountIdentity{Email: "ada@example.com", Plan: "Free", Provider: "Devin", Method: domain.AgentAccountMethodAccount, SourceKind: domain.AgentCredentialSourceCommand, Source: "devin auth status"},
		},
		{
			name: "empty tier does not hide plan", output: "Logged in (via Devin).\nAccount:\n  Tier:\n  Plan: Free\n",
			wantStatus: ports.AgentAuthStatusAuthorized,
			want:       domain.AgentAccountIdentity{Plan: "Free", Provider: "Devin", Method: domain.AgentAccountMethodAccount, SourceKind: domain.AgentCredentialSourceCommand, Source: "devin auth status"},
		},
		{
			name: "legacy output still identifies login method", output: "Logged in (via Devin).",
			wantStatus: ports.AgentAuthStatusAuthorized,
			want:       domain.AgentAccountIdentity{Provider: "Devin", Method: domain.AgentAccountMethodAccount, SourceKind: domain.AgentCredentialSourceCommand, Source: "devin auth status"},
		},
		{
			name: "windsurf login and tier fallback", output: "Logged in (via Windsurf).\nUser:\n  Name: Ada\nAccount:\n  Tier: Pro\n",
			wantStatus: ports.AgentAuthStatusAuthorized,
			want:       domain.AgentAccountIdentity{Name: "Ada", Plan: "Pro", Provider: "Windsurf", Method: domain.AgentAccountMethodAccount, SourceKind: domain.AgentCredentialSourceCommand, Source: "devin auth status"},
		},
		{
			name: "signed out does not retain identity", output: "You are not logged in.\nUser:\n  Email: stale@example.com\n", commandErr: errors.New("exit status 1"),
			wantStatus: ports.AgentAuthStatusUnauthorized,
		},
		{
			name: "signed out falls back to environment without stale identity", output: "You are not logged in.\nUser:\n  Email: stale@example.com\n", env: "cog_fallback", commandErr: errors.New("exit status 1"),
			wantStatus: ports.AgentAuthStatusAuthorized,
			want:       domain.AgentAccountIdentity{Provider: "Devin", Method: domain.AgentAccountMethodAPIKey, SourceKind: domain.AgentCredentialSourceEnvironment, Source: "DEVIN_API_KEY"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DEVIN_API_KEY", tc.env)
			previous := authprobe.CmdRunner
			t.Cleanup(func() { authprobe.CmdRunner = previous })
			calls := 0
			authprobe.CmdRunner = func(_ context.Context, binary string, args ...string) ([]byte, error) {
				calls++
				if binary != "devin" || !reflect.DeepEqual(args, []string{"auth", "status"}) {
					t.Fatalf("unexpected command: %s %v", binary, args)
				}
				return []byte(tc.output), tc.commandErr
			}
			ctx, evidence := ports.WithAuthEvidence(context.Background())
			status, err := (&Plugin{resolvedBinary: "devin"}).AuthStatus(ctx)
			if err != nil || status != tc.wantStatus || calls != 1 {
				t.Fatalf("status=%s err=%v calls=%d", status, err, calls)
			}
			if got := evidence.Identity(); got != tc.want {
				t.Fatalf("identity=%+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestAuthStatusDoesNotRecordIdentityAfterCancellation(t *testing.T) {
	t.Setenv("DEVIN_API_KEY", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	previous := authprobe.CmdRunner
	t.Cleanup(func() { authprobe.CmdRunner = previous })
	authprobe.CmdRunner = func(context.Context, string, ...string) ([]byte, error) {
		cancel()
		return []byte("Logged in (via Devin).\nUser:\n  Email: stale@example.com\n"), nil
	}
	ctx, evidence := ports.WithAuthEvidence(ctx)
	status, err := (&Plugin{resolvedBinary: "devin"}).AuthStatus(ctx)
	if status != ports.AgentAuthStatusUnknown || !errors.Is(err, context.Canceled) || evidence.Identity() != (domain.AgentAccountIdentity{}) {
		t.Fatalf("status=%s err=%v identity=%+v", status, err, evidence.Identity())
	}
}
