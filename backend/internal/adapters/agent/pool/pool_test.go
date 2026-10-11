package pool

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCredentialPresenceIsNotAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		want      ports.AgentAuthStatus
	}{
		{"absent", "", ports.AgentAuthStatusUnknown},
		{"whitespace", " \n", ports.AgentAuthStatusUnknown},
		{"configured", "test-key", ports.AgentAuthStatusConfigured},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := credentialStatus(tc.key); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestTerminalAdmissionAndRestoreRefused(t *testing.T) {
	p := New()
	if argv, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{}); err == nil || argv != nil {
		t.Fatal("terminal launch must fail")
	}
	if argv, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{}); err == nil || ok || argv != nil {
		t.Fatal("restore must hard-fail, never fresh-fallback")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.GetLaunchCommand(ctx, ports.LaunchConfig{}); err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestExplicitEmptyKeyOverridesDaemonEnvironment(t *testing.T) {
	t.Setenv("POOLSIDE_API_KEY", "test-key")
	got, err := New().AuthStatusWithEnv(context.Background(), map[string]string{"POOLSIDE_API_KEY": ""})
	if err != nil || got != ports.AgentAuthStatusUnknown {
		t.Fatalf("got %s, %v", got, err)
	}
}
