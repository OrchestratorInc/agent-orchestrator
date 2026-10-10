package provideraccounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// AdoptSession gives a Codex or Claude session that predates Account Manager a
// place in it, and reports whether it did.
//
// The session takes the provider's default account. With no account for the
// provider it waits for the first one, as a session whose account was removed
// does. A default that needs a new sign-in is assigned all the same: the
// session works again once that account is signed in to, and until then it
// cannot fall back to this computer's own sign-in.
//
// An assignment decides how the session's next agent process is launched and
// does nothing to one already running, so the caller adopts a session only
// while it has no agent process.
func (s *Service) AdoptSession(ctx context.Context, id domain.SessionID, harness domain.AgentHarness) (bool, error) {
	provider := Provider(harness)
	if provider == "" {
		return false, nil
	}
	// A session that already has an account is left exactly as it is.
	if _, managed, err := s.SessionAccount(ctx, id); err != nil || managed {
		return false, err
	}
	// This computer's own sign-in becomes an account the first time anything
	// asks for the accounts. A session adopted before that would wait for an
	// account that is about to exist.
	_ = s.RefreshNativeAccountsIfDue(ctx)
	adopted := false
	err := s.mutate(ctx, func(state *domain.ProviderAccountState) (string, error) {
		adopted = false
		for _, route := range state.Routes {
			if route.SessionID == id {
				return "", nil
			}
		}
		accountID, _ := primary(*state, provider)
		if entry, found := account(*state, accountID); !found || entry.Provider != provider {
			accountID = ""
		}
		sum := sha256.Sum256([]byte(s.ticket(id)))
		state.Routes = append(state.Routes, domain.ProviderSessionRoute{SessionID: id, Provider: provider, AccountID: accountID, TicketHash: hex.EncodeToString(sum[:])})
		adopted = true
		return "", nil
	})
	return adopted && err == nil, err
}
