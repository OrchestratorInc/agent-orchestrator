package chat

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func (s *Service) recordAccountsManagerChatHost(ctx context.Context, id domain.SessionID, harness domain.AgentHarness, generation string, conv ports.ChatConversation) error {
	if preserved, ok := conv.(ports.ChatProviderPreserver); !ok || !preserved.PreservesProviderOnClose() {
		return nil
	}
	host, ok := conv.(interface{ HostIdentity() string })
	store, durable := s.store.(ports.AccountsManagerChatHostStore)
	if !ok || !durable || host.HostIdentity() == "" {
		return ports.ErrChatRecoveryInconclusive
	}
	provider := domain.AccountsManagerProviderClaude
	if harness == domain.HarnessCodex {
		provider = domain.AccountsManagerProviderCodex
	}
	return store.RecordAccountsManagerChatHost(ctx, id, provider, generation, host.HostIdentity())
}

// ArmAccountsManagerRemoval fences the captured controller without discarding queued turns.
func (s *Service) ArmAccountsManagerRemoval(ctx context.Context, entry domain.AccountsManagerRemovalSession) error {
	gate := s.controllerGate(domain.SessionConversationOwner(entry.SessionID))
	if err := gate.lock(ctx); err != nil {
		return err
	}
	defer gate.unlock()
	c, err := s.Controller(entry.SessionID)
	if errors.Is(err, ErrNoController) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.generation != entry.Owner.ControllerGeneration || c.generation == "" {
		return domain.ErrAccountsManagerRemovalConflict
	}
	return c.armAccountHandoff(ctx, false)
}

// StopAccountsManagerRemoval retires only the captured controller and host owner.
func (s *Service) StopAccountsManagerRemoval(ctx context.Context, entry domain.AccountsManagerRemovalSession) error {
	owner := domain.SessionConversationOwner(entry.SessionID)
	gate := s.controllerGate(owner)
	if err := gate.lock(ctx); err != nil {
		return err
	}
	defer gate.unlock()
	c, err := s.Controller(entry.SessionID)
	if err != nil && !errors.Is(err, ErrNoController) {
		return err
	}
	if c != nil && (c.generation == "" || c.generation != entry.Owner.ControllerGeneration) {
		return domain.ErrAccountsManagerRemovalConflict
	}
	identity := ""
	if store, ok := s.store.(ports.AccountsManagerChatHostStore); ok {
		identity, _, err = store.GetAccountsManagerChatHost(ctx, entry.SessionID, entry.Owner.ControllerGeneration)
		if err != nil {
			return err
		}
	}
	persistent := c == nil
	if c != nil {
		if preserver, ok := c.conv.(ports.ChatProviderPreserver); ok {
			persistent = preserver.PreservesProviderOnClose()
		}
		if err := c.armAccountHandoff(ctx, false); err != nil {
			return err
		}
	}
	if persistent {
		if c != nil {
			host, ok := c.conv.(interface{ HostIdentity() string })
			if !ok || identity == "" || host.HostIdentity() != identity {
				return ports.ErrChatRecoveryInconclusive
			}
		}
		if s.stopExactProviderHost == nil {
			return ports.ErrChatRecoveryInconclusive
		}
		if err := s.stopExactProviderHost(ctx, entry.SessionID, identity); err != nil {
			return err
		}
	}
	if c != nil {
		// The exact host was retired above. Close detaches this connection only;
		// broad Terminate could target a replacement at the reusable session key.
		if err := c.Close(ctx); err != nil {
			return err
		}
	}
	s.mu.Lock()
	if s.controllers[entry.SessionID] == c {
		delete(s.controllers, entry.SessionID)
		delete(s.ownerControllers, owner)
		delete(s.startConfigs, owner)
	}
	s.mu.Unlock()
	return nil
}

// AbortAccountsManagerRemoval releases a pre-stop queue fence only for the same controller.
func (s *Service) AbortAccountsManagerRemoval(ctx context.Context, entry domain.AccountsManagerRemovalSession) error {
	gate := s.controllerGate(domain.SessionConversationOwner(entry.SessionID))
	if err := gate.lock(ctx); err != nil {
		return err
	}
	defer gate.unlock()
	c, err := s.Controller(entry.SessionID)
	if errors.Is(err, ErrNoController) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.generation != entry.Owner.ControllerGeneration || c.generation == "" {
		return domain.ErrAccountsManagerRemovalConflict
	}
	c.releaseAccountHandoff()
	return nil
}
