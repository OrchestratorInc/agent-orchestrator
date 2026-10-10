package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// LoadProviderAccountState loads and validates effective facts and any pending mutation.
func (s *Store) LoadProviderAccountState(ctx context.Context) (domain.ProviderAccountState, *domain.ProviderAccountIntent, error) {
	row, err := s.qr.LoadProviderAccountState(ctx)
	if err != nil {
		return domain.ProviderAccountState{}, nil, err
	}
	var state domain.ProviderAccountState
	if err = json.Unmarshal([]byte(row.Facts), &state); err != nil {
		return state, nil, fmt.Errorf("read provider account facts: %w", err)
	}
	if state.Revision != row.Revision || !validAccountFacts(state) {
		return state, nil, ports.ErrProviderAccountRecovery
	}
	var pending *domain.ProviderAccountIntent
	if row.Pending.Valid {
		pending = &domain.ProviderAccountIntent{}
		if err = json.Unmarshal([]byte(row.Pending.String), pending); err != nil {
			return state, nil, fmt.Errorf("read provider account intent: %w", err)
		}
		if !validAccountFacts(pending.Next) || (pending.Next.Revision != state.Revision && pending.Next.Revision != state.Revision+1) {
			return state, nil, ports.ErrProviderAccountRecovery
		}
	}
	return state, pending, nil
}

// SaveProviderAccountIntent durably admits the next revision using compare-and-swap.
func (s *Store) SaveProviderAccountIntent(ctx context.Context, revision int64, intent domain.ProviderAccountIntent) error {
	if intent.Next.Revision != revision+1 || !validAccountFacts(intent.Next) {
		return ports.ErrProviderAccountRecovery
	}
	data, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	return s.writeProviderAccount(ctx, func() (int64, error) {
		return s.qw.SaveProviderAccountIntent(ctx, gen.SaveProviderAccountIntentParams{Pending: sql.NullString{String: string(data), Valid: true}, Revision: revision})
	})
}

// CommitProviderAccountIntent publishes the acknowledged routing revision.
func (s *Store) CommitProviderAccountIntent(ctx context.Context, revision int64) error {
	return s.writeProviderAccount(ctx, func() (int64, error) {
		return s.qw.CommitProviderAccountIntent(ctx, revision)
	})
}

// FinishProviderAccountIntent clears a completed or refused mutation at the expected revision.
func (s *Store) FinishProviderAccountIntent(ctx context.Context, revision int64) error {
	return s.writeProviderAccount(ctx, func() (int64, error) {
		return s.qw.FinishProviderAccountIntent(ctx, revision)
	})
}

func (s *Store) writeProviderAccount(ctx context.Context, write func() (int64, error)) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	n, err := write()
	if err != nil {
		return err
	}
	if n != 1 {
		return ports.ErrProviderAccountRecovery
	}
	return nil
}

var _ ports.ProviderAccountStore = (*Store)(nil)

// validAccountFacts refuses corrupt routing identity instead of letting an
// invalid record select another provider or a credential outside its store.
func validAccountFacts(state domain.ProviderAccountState) bool {
	if state.Revision < 0 {
		return false
	}
	accounts := make(map[string]domain.ProviderAccount, len(state.Accounts))
	providers := make(map[string]string, len(state.Primaries))
	identities := make(map[string]bool, len(state.Accounts))
	for _, a := range state.Accounts {
		identity := a.Provider + "\x00" + strings.ToLower(a.Email)
		if a.ID == "" || (a.Provider != "codex" && a.Provider != "claude") || strings.TrimSpace(a.Email) == "" || identities[identity] {
			return false
		}
		if _, exists := accounts[a.ID]; exists {
			return false
		}
		if (a.CredentialRef == "") != (a.AuthID == "") {
			return false
		}
		if a.CredentialRef != "" && (filepath.Base(a.CredentialRef) != a.CredentialRef || strings.ContainsAny(a.CredentialRef, "/\\") || a.CredentialRef == "." || a.CredentialRef == "..") {
			return false
		}
		accounts[a.ID] = a
		identities[identity] = true
	}
	for _, p := range state.Primaries {
		if p.Provider != "codex" && p.Provider != "claude" {
			return false
		}
		if _, exists := providers[p.Provider]; exists {
			return false
		}
		if p.PrimaryID != "" {
			a, exists := accounts[p.PrimaryID]
			if !exists || a.Provider != p.Provider || a.CredentialRef == "" {
				return false
			}
		}
		providers[p.Provider] = p.PrimaryID
	}
	for _, a := range state.Accounts {
		if _, adopted := providers[a.Provider]; !adopted {
			return false
		}
	}
	sessions := make(map[domain.SessionID]bool, len(state.Routes))
	tickets := make(map[string]bool, len(state.Routes))
	for _, r := range state.Routes {
		hash, err := hex.DecodeString(r.TicketHash)
		if r.SessionID == "" || sessions[r.SessionID] || tickets[r.TicketHash] || err != nil || len(hash) != 32 || strings.ToLower(r.TicketHash) != r.TicketHash {
			return false
		}
		if _, adopted := providers[r.Provider]; !adopted {
			return false
		}
		if r.AccountID != "" {
			a, exists := accounts[r.AccountID]
			if !exists || a.Provider != r.Provider || a.CredentialRef == "" {
				return false
			}
		}
		sessions[r.SessionID] = true
		tickets[r.TicketHash] = true
	}
	return true
}
