package sessionmanager

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type managedChatLauncher struct {
	*recordingLauncher
	managedProbes int
}

func (l *managedChatLauncher) PreflightManagedChat(context.Context, domain.AgentHarness, ports.PermissionMode) error {
	l.managedProbes++
	return nil
}

func TestManagedCodexInitialChatUsesManagedPreflight(t *testing.T) {
	m, st, rt, _ := initialSelectionFixture()
	launcher := &managedChatLauncher{recordingLauncher: &recordingLauncher{preflightErr: ports.ErrChatAuthRequired}}
	m.chat = launcher
	m.runBackground = func(work func()) { work() }
	cfg := initialSelectionConfig(domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerManaged, AccountID: "account-a"})
	cfg.RequestedMode = domain.SessionModeChat
	record, _, _, err := m.Spawn(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if record.Mode != domain.SessionModeChat || launcher.managedProbes != 1 || len(launcher.preflighted) != 0 || len(launcher.started) != 1 || rt.created != 0 {
		t.Fatal("managed Chat did not retain explicit mode and preflight")
	}
	if st.bindings[record.ID] != *cfg.Account {
		t.Fatal("managed Chat lost the selected account")
	}
}

func TestManagedCodexBoundChatUsesManagedPreflight(t *testing.T) {
	m, _, _, _ := newManager()
	launcher := &managedChatLauncher{recordingLauncher: &recordingLauncher{preflightErr: ports.ErrChatAuthRequired}}
	m.chat = launcher
	m.accountsManager = fakeAccountsManagerRouter{route: &ports.AccountsManagerLaunchRoute{BindingRevision: 1}}
	if err := m.preflightBoundChat(t.Context(), "session-a", domain.HarnessCodex, ports.PermissionModeDefault); err != nil {
		t.Fatal(err)
	}
	if launcher.managedProbes != 1 || len(launcher.preflighted) != 0 {
		t.Fatal("managed binding consulted native preflight")
	}
}
