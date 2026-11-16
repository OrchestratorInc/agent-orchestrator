package codewhale

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fakeRunner answers each Codewhale subcommand. Args are the arguments after
// the binary path, so args[0] is the subcommand ("auth", "doctor", ...).
type fakeRunner func(args []string) ([]byte, error)

func (f fakeRunner) bind(_ context.Context, _ string, _ string, _ map[string]string, args ...string) ([]byte, error) {
	return f(args)
}

func TestAuthStatusReportsAuthorizedWhenLiveCheckPasses(t *testing.T) {
	p := &Plugin{
		resolvedBinary: "/opt/codewhale",
		runCommand: fakeRunner(func(args []string) ([]byte, error) {
			switch {
			case args[0] == "auth" && len(args) == 2:
				return []byte("active provider: deepseek (set via config)\n"), nil
			case args[0] == "auth":
				return []byte("provider: deepseek\nactive source: secret store (last4: ...756a)\n"), nil
			default:
				return []byte("codewhale Doctor\n· Testing connection...\nReady: the live deepseek API check passed.\n"), nil
			}
		}).bind,
	}
	status, err := p.AuthStatus(context.Background())
	if err != nil || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status=%q err=%v", status, err)
	}
}

func TestAuthStatusReportsConfiguredWhenLiveCheckFails(t *testing.T) {
	p := &Plugin{
		resolvedBinary: "/opt/codewhale",
		runCommand: fakeRunner(func(args []string) ([]byte, error) {
			switch {
			case args[0] == "auth" && len(args) == 2:
				return []byte("active provider: deepseek\n"), nil
			case args[0] == "auth":
				return []byte("active source: secret store\n"), nil
			default:
				return []byte("codewhale Doctor\n· Testing connection...\nNot ready: the live deepseek API check failed → `codewhale auth set --provider deepseek` replaces a rejected key.\n"), nil
			}
		}).bind,
	}
	status, err := p.AuthStatus(context.Background())
	if err != nil || status != ports.AgentAuthStatusConfigured {
		t.Fatalf("status=%q err=%v", status, err)
	}
}

func TestAuthStatusReportsConfiguredFromLocalSource(t *testing.T) {
	p := &Plugin{
		resolvedBinary: "/opt/codewhale",
		runCommand: fakeRunner(func(args []string) ([]byte, error) {
			switch {
			case args[0] == "auth" && len(args) == 2:
				return []byte("active provider: openai (set via config)\n"), nil
			case args[0] == "auth":
				return []byte("provider: openai\nactive source: env\n"), nil
			default:
				return nil, errors.New("doctor probe failed")
			}
		}).bind,
	}
	status, err := p.AuthStatus(context.Background())
	if err != nil || status != ports.AgentAuthStatusConfigured {
		t.Fatalf("status=%q err=%v", status, err)
	}
}

func TestAuthStatusMissingSourceIsUnknown(t *testing.T) {
	p := &Plugin{
		resolvedBinary: "/opt/codewhale",
		runCommand: fakeRunner(func(args []string) ([]byte, error) {
			if args[0] == "auth" && len(args) > 2 {
				return []byte("active source: missing\n"), nil
			}
			return []byte("active provider: deepseek\n"), nil
		}).bind,
	}
	status, err := p.AuthStatus(context.Background())
	if err != nil || status != ports.AgentAuthStatusUnknown {
		t.Fatalf("status=%q err=%v", status, err)
	}
}

func TestAuthStatusProbeFailureIsInconclusive(t *testing.T) {
	p := &Plugin{
		resolvedBinary: "/opt/codewhale",
		runCommand: fakeRunner(func([]string) ([]byte, error) {
			return nil, errors.New("probe failed")
		}).bind,
	}
	status, err := p.AuthStatus(context.Background())
	if err == nil || status != ports.AgentAuthStatusUnknown {
		t.Fatalf("status=%q err=%v", status, err)
	}
}
