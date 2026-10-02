package accountsmanager

import (
	"context"
	"time"

	core "github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type bindingStore interface {
	AccountsManagerBindings(context.Context) (domain.AccountsManagerBindingSnapshot, error)
}

// RecordNativeAgentSessionRoute preserves native mode across later default changes.
func (s *Service) RecordNativeAgentSessionRoute(ctx context.Context, id domain.SessionID, provider domain.AccountsManagerProvider) error {
	if s.routingStore == nil || !provider.Valid() {
		return core.ErrUnavailable
	}
	binding, _, err := s.routingStore.GetOrCreateAccountsManagerSessionRoute(ctx, domain.AccountsManagerSessionRoute{SessionID: id, Provider: provider, Mode: domain.AccountsManagerNative})
	if err != nil {
		return err
	}
	if binding.Mode != domain.AccountsManagerNative {
		return ports.ErrChatUnsupported
	}
	return nil
}

func (s *Service) bindingSnapshot(ctx context.Context) (core.BindingSnapshot, error) {
	store, ok := s.routingStore.(bindingStore)
	if !ok {
		return core.BindingSnapshot{}, core.ErrUnavailable
	}
	snapshot, err := store.AccountsManagerBindings(ctx)
	if err != nil {
		return core.BindingSnapshot{}, err
	}
	result := core.BindingSnapshot{Revision: snapshot.Revision, Bindings: make([]core.RouteBinding, 0, len(snapshot.Bindings))}
	for _, binding := range snapshot.Bindings {
		result.Bindings = append(result.Bindings, core.RouteBinding{SessionID: string(binding.SessionID), Provider: string(binding.Provider), Mode: string(binding.Mode), AccountID: binding.AccountID, Revision: binding.Revision, Blocked: binding.Blocked})
	}
	return result, nil
}

func (s *Service) synchronizeBindings(ctx context.Context) error {
	s.bindingsMu.Lock()
	defer s.bindingsMu.Unlock()
	client, ok := s.client.(routingClient)
	if !ok {
		return core.ErrUnavailable
	}
	snapshot, err := s.bindingSnapshot(ctx)
	if err != nil {
		return err
	}
	return client.SynchronizeBindings(ctx, snapshot)
}

func (s *Service) mintBoundRoute(ctx context.Context, client routingClient, binding domain.AccountsManagerSessionRoute, ref string) (core.RouteCapability, error) {
	s.bindingsMu.Lock()
	defer s.bindingsMu.Unlock()
	snapshot, err := s.bindingSnapshot(ctx)
	if err != nil {
		return core.RouteCapability{}, err
	}
	current := false
	for _, row := range snapshot.Bindings {
		if !row.Blocked && row.SessionID == string(binding.SessionID) && row.Provider == string(binding.Provider) && row.Mode == string(domain.AccountsManagerManaged) && row.AccountID == binding.AccountID && row.Revision == binding.Revision {
			current = true
			break
		}
	}
	if !current {
		return core.RouteCapability{}, domain.ErrAccountsManagerBindingConflict
	}
	if err := client.SynchronizeBindings(ctx, snapshot); err != nil {
		return core.RouteCapability{}, err
	}
	return client.MintRoute(ctx, core.Provider(binding.Provider), ref, string(binding.SessionID), binding.AccountID, binding.Revision)
}

func (s *Service) watchBindings(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		attempt, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := s.synchronizeBindings(attempt)
		cancel()
		if err != nil && ctx.Err() == nil {
			s.markDegraded()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
