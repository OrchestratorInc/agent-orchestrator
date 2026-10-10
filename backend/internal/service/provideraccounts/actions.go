package provideraccounts

import (
	"context"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// signedInAccount resolves an account the helper holds a credential for,
// together with the proxy that can act on it.
func (s *Service) signedInAccount(ctx context.Context, id string) (domain.ProviderAccount, ports.ProviderAccountActionProxy, error) {
	state, _, err := s.store.LoadProviderAccountState(ctx)
	if err != nil {
		return domain.ProviderAccount{}, nil, err
	}
	a, ok := account(state, id)
	if !ok {
		return domain.ProviderAccount{}, nil, ports.ErrProviderAccountUnknown
	}
	if a.CredentialRef == "" || a.AuthID == "" {
		return domain.ProviderAccount{}, nil, ports.ErrProviderLoginRequired
	}
	proxy, ok := s.proxy.(ports.ProviderAccountActionProxy)
	if !ok {
		return domain.ProviderAccount{}, nil, ports.ErrProviderAccountActionUnavailable
	}
	return a, proxy, nil
}

// forgetUsage makes the next read ask the provider again.
func (s *Service) forgetUsage(a domain.ProviderAccount) {
	s.usageMu.Lock()
	delete(s.usageCache, a.ID+"\x00"+a.AuthID)
	s.usageMu.Unlock()
}

// UseAccountReset spends one of the account's usage-limit resets and returns
// the outcome. Each call is one attempt with its own identity, so the provider
// can recognize a repeat of the same attempt; AO never retries on its own.
func (s *Service) UseAccountReset(ctx context.Context, id string) (string, error) {
	a, proxy, err := s.signedInAccount(ctx, id)
	if err != nil {
		return "", err
	}
	outcome, err := proxy.UseAccountReset(ctx, a.Provider, a.AuthID, uuid.NewString())
	s.forgetUsage(a)
	return outcome, err
}

// ResumeAccount lifts the helper's hold on an account after a provider refusal.
func (s *Service) ResumeAccount(ctx context.Context, id string) error {
	a, proxy, err := s.signedInAccount(ctx, id)
	if err != nil {
		return err
	}
	err = proxy.ResumeAccount(ctx, a.Provider, a.AuthID)
	s.forgetUsage(a)
	return err
}

// RefreshAccountSignIn renews the saved sign-in now and re-reads its state.
func (s *Service) RefreshAccountSignIn(ctx context.Context, id string) error {
	a, proxy, err := s.signedInAccount(ctx, id)
	if err != nil {
		return err
	}
	err = proxy.RefreshAccountSignIn(ctx, a.Provider, a.AuthID)
	s.forgetUsage(a)
	// A refused refresh is CLIProxy's verdict that the sign-in is dead.
	s.refreshSignInFailures(ctx, true)
	return err
}
