package agentaccounts

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Every account of a provider has its own color, so two accounts never look
// alike in the app. A new account takes the first color in
// domain.AgentAccountAutoColors that no other account of its provider uses,
// and a color the user picks must be free. Only a provider with more accounts
// than colors repeats one, taking the least used.

// coloredAccounts is accounts, with a color saved for each account that has
// none yet (one just signed in, or added before colors were unique) and a new
// one for each that shares an earlier account's color while one is free.
func (s *Service) coloredAccounts(ctx context.Context) ([]domain.AgentAccount, error) {
	s.identityMu.Lock()
	defer s.identityMu.Unlock()
	accounts, err := s.accounts(ctx)
	if err != nil {
		return nil, err
	}
	taken := map[domain.AgentAccountProvider]map[string]bool{}
	for i, account := range accounts {
		if taken[account.Provider] == nil {
			taken[account.Provider] = map[string]bool{}
		}
		seen := taken[account.Provider]
		if account.Color != "" && (!seen[account.Color] || len(seen) >= len(domain.AgentAccountAutoColors)) {
			seen[account.Color] = true
			continue
		}
		color := freeColor(accounts, account.Provider, account.ID)
		if err := s.saveIdentity(ctx, account, account.Name, color); err != nil {
			// The account may be mid-removal; it shows uncolored until the next list.
			if !errors.Is(err, ports.ErrAgentAccountUnknown) {
				s.log.Warn("assign agent account color", "account", account.ID, "error", err)
			}
			continue
		}
		accounts[i].Color = color
		seen[color] = true
	}
	return accounts, nil
}

// freeColor is the color an account of p takes: the first auto color no other
// account of p uses, or the least used one once all are taken.
func freeColor(accounts []domain.AgentAccount, p domain.AgentAccountProvider, id string) string {
	used := make(map[string]int, len(domain.AgentAccountAutoColors))
	for _, account := range accounts {
		if account.Provider == p && account.ID != id && account.Color != "" {
			used[account.Color]++
		}
	}
	best := domain.AgentAccountAutoColors[0]
	for _, color := range domain.AgentAccountAutoColors {
		if used[color] < used[best] {
			best = color
		}
	}
	return best
}

// pickColor is the color account id of p is saved with: color when no other
// account of p uses it, or a free one when color is empty. Callers hold
// identityMu, so two accounts cannot take the same color at once.
func pickColor(accounts []domain.AgentAccount, p domain.AgentAccountProvider, id, color string) (string, error) {
	if color == "" {
		return freeColor(accounts, p, id), nil
	}
	if !domain.ValidAgentAccountColor(color) {
		return "", ports.ErrAgentAccountColorInvalid
	}
	for _, account := range accounts {
		if account.Provider == p && account.ID != id && account.Color == color {
			return "", ports.ErrAgentAccountColorTaken
		}
	}
	return color, nil
}

// saveIdentity writes an account's name and color: a "This computer" account's
// to its own table, since it has no saved account row.
func (s *Service) saveIdentity(ctx context.Context, account domain.AgentAccount, name, color string) error {
	if account.Device() {
		return s.store.SetDeviceAgentAccountIdentity(ctx, account.ID, name, color)
	}
	return s.store.UpdateAgentAccountIdentity(ctx, account.ID, name, color)
}
