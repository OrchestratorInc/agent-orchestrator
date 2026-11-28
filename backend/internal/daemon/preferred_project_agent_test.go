package daemon

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	agentsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/agent"
)

type mutablePreferredAgent struct {
	installed atomic.Bool
	auth      atomic.Value // ports.AgentAuthStatus
}

func (a *mutablePreferredAgent) ResolveBinary(context.Context) (string, error) {
	if !a.installed.Load() {
		return "", ports.ErrAgentBinaryNotFound
	}
	return "/usr/local/bin/aider", nil
}

func (a *mutablePreferredAgent) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	if status, ok := a.auth.Load().(ports.AgentAuthStatus); ok {
		return status, nil
	}
	return ports.AgentAuthStatusAuthorized, nil
}

func (*mutablePreferredAgent) GetConfigSpec(context.Context) (ports.ConfigSpec, error) {
	return ports.ConfigSpec{}, nil
}

func (*mutablePreferredAgent) GetLaunchCommand(context.Context, ports.LaunchConfig) ([]string, error) {
	return nil, nil
}

func (*mutablePreferredAgent) GetPromptDeliveryStrategy(context.Context, ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return "", nil
}

func (*mutablePreferredAgent) GetAgentHooks(context.Context, ports.WorkspaceHookConfig) error {
	return nil
}

func (*mutablePreferredAgent) GetRestoreCommand(context.Context, ports.RestoreConfig) ([]string, bool, error) {
	return nil, false, nil
}

func (*mutablePreferredAgent) SessionInfo(context.Context, ports.SessionRef) (ports.SessionInfo, bool, error) {
	return ports.SessionInfo{}, false, nil
}

func TestPreferredProjectAgentRechecksCachedNotInstalledWithinDisplayTTL(t *testing.T) {
	ctx := context.Background()
	fake := &mutablePreferredAgent{}
	fake.auth.Store(ports.AgentAuthStatusAuthorized)
	agentSvc := agentsvc.NewWithAgents([]agentregistry.HarnessAgent{{
		Harness:  domain.HarnessAider,
		Manifest: adapters.Manifest{Name: "Aider"},
		Agent:    fake,
	}})
	pick := preferredProjectAgent(agentSvc)

	// Populate the 5-minute display cache with not_installed.
	if got := pick(ctx); got != "" {
		t.Fatalf("before install: pick = %q, want empty", got)
	}

	// Installing the binary immediately after must bypass the cached not_installed entry.
	fake.installed.Store(true)
	if got := pick(ctx); got != domain.HarnessAider {
		t.Fatalf("after install within display TTL: pick = %q, want %q", got, domain.HarnessAider)
	}
}

func TestPreferredProjectAgentRechecksCachedUnauthorizedWithinDisplayTTL(t *testing.T) {
	ctx := context.Background()
	fake := &mutablePreferredAgent{}
	fake.installed.Store(true)
	fake.auth.Store(ports.AgentAuthStatusUnauthorized)
	agentSvc := agentsvc.NewWithAgents([]agentregistry.HarnessAgent{{
		Harness:  domain.HarnessCodex,
		Manifest: adapters.Manifest{Name: "Codex"},
		Agent:    fake,
	}})
	pick := preferredProjectAgent(agentSvc)

	// Populate the 5-minute display cache with installed + unauthorized.
	if got := pick(ctx); got != "" {
		t.Fatalf("while signed out: pick = %q, want empty", got)
	}

	// Signing in immediately after must bypass the cached unauthorized entry.
	fake.auth.Store(ports.AgentAuthStatusAuthorized)
	if got := pick(ctx); got != domain.HarnessCodex {
		t.Fatalf("after sign-in within display TTL: pick = %q, want %q", got, domain.HarnessCodex)
	}
}
