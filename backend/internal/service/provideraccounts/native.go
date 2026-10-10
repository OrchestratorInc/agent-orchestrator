package provideraccounts

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SetNativeAccountSource is called once during daemon wiring, before serving.
func (s *Service) SetNativeAccountSource(source ports.ProviderNativeAccountSource) { s.native = source }

// RefreshNativeAccountsIfDue is the background form of RefreshNativeAccounts:
// routine account reads must not re-read the keychain on every request.
func (s *Service) RefreshNativeAccountsIfDue(ctx context.Context) error {
	s.usageMu.Lock()
	due := time.Since(s.nativeCheckedAt) >= providerNativeCheckInterval
	s.usageMu.Unlock()
	if !due {
		return nil
	}
	return s.RefreshNativeAccounts(ctx)
}

// RefreshNativeAccounts discovers new native logins without treating local
// logout as revocation. The durable receipt prevents repeated imports, token
// rollback, and a settings refresh undoing the user's local sign-out/removal.
func (s *Service) RefreshNativeAccounts(ctx context.Context) error {
	if s.native == nil {
		return nil
	}
	s.usageMu.Lock()
	s.nativeCheckedAt = time.Now()
	s.usageMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	release, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err = s.reconcile(ctx, false); err != nil {
		return err
	}
	for _, provider := range []string{"codex", "claude"} {
		native, err := s.native.ReadNativeAccount(ctx, provider)
		if err != nil || native.Fingerprint == "" {
			continue
		} // Locked/missing sources leave managed credentials untouched.
		state, err := s.State(ctx)
		if err != nil {
			return err
		}
		if seen := state.NativeImports[provider]; seen.Fingerprint == native.Fingerprint {
			// Nothing new to import, but the Global marks may be out of date:
			// an account for the same person can be removed and added again, or
			// signed in to again, long after the import.
			email := seen.Email
			if email == "" {
				email = native.Email
			}
			if a, ok := account(state, seen.AccountID); ok && email == "" {
				email = a.Email
			}
			if email != "" && (seen.Email != email || globalMarksStale(state, provider, email)) {
				if err = s.mutateLocked(ctx, true, func(state *domain.ProviderAccountState) (string, error) {
					remembered := state.NativeImports[provider]
					remembered.Email = email
					state.NativeImports[provider] = remembered
					markGlobal(state, provider)
					return "", nil
				}); err != nil {
					return err
				}
			}
			continue
		}
		verified, err := s.native.ImportNativeAccount(ctx, provider, native)
		if err != nil || verified.Provider != provider || strings.TrimSpace(verified.Email) == "" || verified.AuthID == "" || verified.CredentialRef == "" {
			continue
		}
		err = s.mutateLocked(ctx, true, func(state *domain.ProviderAccountState) (string, error) {
			id, deletion := "", ""
			for i := range state.Accounts {
				a := &state.Accounts[i]
				if a.Provider != provider {
					continue
				}
				// An API key is never the sign-in, whatever its label says.
				a.Global = strings.EqualFold(a.Email, verified.Email) && !a.APIKey()
				if !a.Global {
					continue
				}
				id = a.ID
				if a.CredentialRef == "" {
					a.CredentialRef, a.AuthID = verified.CredentialRef, verified.AuthID
				} else if a.CredentialRef != verified.CredentialRef {
					// Keep the managed credential: it may already have a newer refresh token.
					deletion = verified.CredentialRef
				}
			}
			if id == "" {
				// The catalogue's identity is provider + email, including API-key labels.
				for _, a := range state.Accounts {
					if a.Provider == provider && strings.EqualFold(a.Email, verified.Email) {
						return "", ports.ErrProviderAccountConflict
					}
				}
				id = s.newID()
				state.Accounts = append(state.Accounts, domain.ProviderAccount{ID: id, Provider: provider, Email: verified.Email, Kind: "oauth", Global: true, CredentialRef: verified.CredentialRef, AuthID: verified.AuthID})
				fillMissingDisplayNames(state)
			}
			if current, _ := primary(*state, provider); current == "" {
				setPrimary(state, provider, id)
				for i := range state.Routes {
					if state.Routes[i].Provider == provider && state.Routes[i].AccountID == "" {
						state.Routes[i].AccountID = id
					}
				}
			}
			if state.NativeImports == nil {
				state.NativeImports = make(map[string]domain.NativeProviderImport)
			}
			state.NativeImports[provider] = domain.NativeProviderImport{Fingerprint: native.Fingerprint, AccountID: id, Email: verified.Email}
			markGlobal(state, provider)
			return deletion, nil
		})
		if err != nil {
			return err
		}
	}
	// After the sign-ins, so that a key found beside a sign-in ends up the
	// default: the agent itself uses a key ahead of a sign-in.
	for _, provider := range []string{"codex", "claude"} {
		if err := s.importNativeAPIKey(ctx, provider); err != nil {
			return err
		}
	}
	return nil
}

// importNativeAPIKey makes the API key this computer's own agent is set up to
// use an account, and the provider's default, the first time it is seen. The
// caller holds the service lock.
//
// Like a sign-in, a key is imported once: the receipt keeps a refresh from
// undoing a later removal, sign-out or choice of another default. A different
// key is a new thing to import, and takes the place of the one before it.
func (s *Service) importNativeAPIKey(ctx context.Context, provider string) error {
	source, ok := s.native.(ports.ProviderNativeAPIKeySource)
	if !ok {
		return nil
	}
	native, err := source.ReadNativeAPIKey(ctx, provider)
	if err != nil || native.Fingerprint == "" {
		return nil // Nothing readable leaves the accounts as they are.
	}
	state, err := s.State(ctx)
	if err != nil {
		return err
	}
	seen := state.NativeKeyImports[provider]
	if seen.Fingerprint == native.Fingerprint {
		return nil
	}
	verified, err := source.ImportNativeAPIKey(ctx, provider, native)
	alreadyAnAccount := errors.Is(err, ports.ErrProviderAccountConflict)
	if !alreadyAnAccount && (err != nil || verified.Provider != provider || verified.AuthID == "" || verified.CredentialRef == "") {
		return nil
	}
	return s.mutateLocked(ctx, true, func(state *domain.ProviderAccountState) (string, error) {
		if state.NativeKeyImports == nil {
			state.NativeKeyImports = make(map[string]domain.NativeProviderImport)
		}
		if alreadyAnAccount {
			// The key was added by hand. It stays that account, as it is; the
			// receipt only stops the same key being looked at again.
			state.NativeKeyImports[provider] = domain.NativeProviderImport{Fingerprint: native.Fingerprint}
			markGlobal(state, provider)
			return "", nil
		}
		id, deletion := "", ""
		for i := range state.Accounts {
			a := &state.Accounts[i]
			if seen.AccountID == "" || a.ID != seen.AccountID || a.Provider != provider || !a.APIKey() {
				continue
			}
			// The key changed: the account that held the old one holds the new.
			id = a.ID
			if a.CredentialRef != "" && a.CredentialRef != verified.CredentialRef {
				deletion = a.CredentialRef
			}
			a.Kind, a.CredentialRef, a.AuthID = "api_key", verified.CredentialRef, verified.AuthID
		}
		if id == "" {
			// The catalogue's identity is provider + email, including API-key labels.
			for _, a := range state.Accounts {
				if a.Provider == provider && strings.EqualFold(a.Email, verified.Email) {
					return "", ports.ErrProviderAccountConflict
				}
			}
			id = s.newID()
			state.Accounts = append(state.Accounts, domain.ProviderAccount{ID: id, Provider: provider, Email: verified.Email, Kind: "api_key", CredentialRef: verified.CredentialRef, AuthID: verified.AuthID})
			fillMissingDisplayNames(state)
			setPrimary(state, provider, id)
			for i := range state.Routes {
				if state.Routes[i].Provider == provider && state.Routes[i].AccountID == "" {
					state.Routes[i].AccountID = id
				}
			}
		}
		state.NativeKeyImports[provider] = domain.NativeProviderImport{Fingerprint: native.Fingerprint, AccountID: id, Email: verified.Email}
		markGlobal(state, provider)
		return deletion, nil
	})
}

// markGlobal marks the accounts that are this computer's own: the sign-in with
// the email last seen in the provider's native login, and the API key imported
// from this computer. One rule, applied wherever accounts change, so the mark
// does not depend on how or when an account was added. With no email on record
// the sign-ins' marks are left as they are.
func markGlobal(state *domain.ProviderAccountState, provider string) {
	email := state.NativeImports[provider].Email
	for i := range state.Accounts {
		a := &state.Accounts[i]
		if a.Provider != provider {
			continue
		}
		if a.APIKey() {
			a.Global = importedAPIKey(*state, *a)
		} else if email != "" {
			a.Global = strings.EqualFold(a.Email, email)
		}
	}
}

// importedAPIKey reports the API-key account that was imported from this computer.
func importedAPIKey(state domain.ProviderAccountState, a domain.ProviderAccount) bool {
	imported := state.NativeKeyImports[a.Provider].AccountID
	return imported != "" && a.ID == imported
}

func globalMarksStale(state domain.ProviderAccountState, provider, email string) bool {
	for _, a := range state.Accounts {
		if a.Provider != provider {
			continue
		}
		global := strings.EqualFold(a.Email, email)
		if a.APIKey() {
			global = importedAPIKey(state, a)
		}
		if a.Global != global {
			return true
		}
	}
	return false
}
