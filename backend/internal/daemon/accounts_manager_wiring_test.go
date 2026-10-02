package daemon

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestAccountsManagerChatLauncherHasControllerHandoff(t *testing.T) {
	if _, ok := any(chatLauncher{}).(interface {
		ArmAccountsManagerHandoff(context.Context, domain.SessionID, bool) error
		PrepareAccountsManagerHandoff(context.Context, domain.SessionID, domain.SessionInterfaceTransitionPolicy) error
		AbortAccountsManagerHandoff(domain.SessionID)
		AcknowledgeAccountsManagerSwitch(context.Context, domain.SessionID, string, func(context.Context) error) error
	}); !ok {
		t.Fatal("daemon Chat launcher hides the account handoff capability")
	}
}

func TestAccountsManagerRemovalChatLauncherHasExactOwner(t *testing.T) {
	if _, ok := any(chatLauncher{}).(interface {
		ArmAccountsManagerRemoval(context.Context, domain.AccountsManagerRemovalSession) error
		StopAccountsManagerRemoval(context.Context, domain.AccountsManagerRemovalSession) error
		AbortAccountsManagerRemoval(context.Context, domain.AccountsManagerRemovalSession) error
	}); !ok {
		t.Fatal("daemon Chat launcher hides deletion ownership coordination")
	}
}
