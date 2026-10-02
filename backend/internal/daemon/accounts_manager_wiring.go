package daemon

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func (c chatLauncher) ArmAccountsManagerRemoval(ctx context.Context, entry domain.AccountsManagerRemovalSession) error {
	return c.svc.ArmAccountsManagerRemoval(ctx, entry)
}

func (c chatLauncher) StopAccountsManagerRemoval(ctx context.Context, entry domain.AccountsManagerRemovalSession) error {
	return c.svc.StopAccountsManagerRemoval(ctx, entry)
}

func (c chatLauncher) AbortAccountsManagerRemoval(ctx context.Context, entry domain.AccountsManagerRemovalSession) error {
	return c.svc.AbortAccountsManagerRemoval(ctx, entry)
}

func (c chatLauncher) ArmAccountsManagerHandoff(ctx context.Context, id domain.SessionID, fresh bool) error {
	return c.svc.ArmAccountsManagerHandoff(ctx, id, fresh)
}

func (c chatLauncher) PrepareAccountsManagerHandoff(ctx context.Context, id domain.SessionID, policy domain.SessionInterfaceTransitionPolicy) error {
	return c.svc.PrepareAccountsManagerHandoff(ctx, id, policy)
}

func (c chatLauncher) AbortAccountsManagerHandoff(id domain.SessionID) {
	c.svc.AbortAccountsManagerHandoff(id)
}

func (c chatLauncher) AcknowledgeAccountsManagerSwitch(ctx context.Context, id domain.SessionID, generation string, commit func(context.Context) error) error {
	return c.svc.AcknowledgeAccountsManagerSwitch(ctx, id, generation, commit)
}
