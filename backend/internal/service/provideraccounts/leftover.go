package provideraccounts

import (
	"context"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// leftoverCredentialAge is how long a sign-in may sit in the helper with no
// account before AO removes it. A sign-in is recorded within seconds of being
// saved, and no sign-in attempt lasts longer than six minutes.
const leftoverCredentialAge = 15 * time.Minute

// leftoverCredentialCheckInterval spaces the sweeps. A leftover is rare and
// costs a refused token refresh every few minutes, not anything a person waits on.
const leftoverCredentialCheckInterval = 10 * time.Minute

// RemoveLeftoverCredentials deletes the sign-ins the helper holds that no
// account uses, and reports how many it removed.
//
// The helper saves a sign-in the moment the provider grants it; AO records it
// when someone next asks how the attempt went. A sign-in that finishes after its
// dialog was closed, or while the daemon restarts, is therefore saved and never
// recorded. CLIProxy keeps such a credential loaded and keeps refreshing it:
// tokens for an account the user does not have in AO, renewed forever, or
// refused by the provider every few minutes once they die. Accounts are AO's to
// decide, so a credential none of them names is removed.
//
// It runs at most once per interval unless forced.
func (s *Service) RemoveLeftoverCredentials(ctx context.Context, force bool) (int, error) {
	inventory, ok := s.proxy.(ports.ProviderCredentialInventory)
	if !ok {
		return 0, nil
	}
	s.signInMu.Lock()
	due := force || time.Since(s.leftoverCheckedAt) >= leftoverCredentialCheckInterval
	if due {
		s.leftoverCheckedAt = time.Now()
	}
	s.signInMu.Unlock()
	if !due {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, providerAccountOperationTimeout)
	defer cancel()
	credentials, err := inventory.ListCredentials(ctx)
	if err != nil {
		return 0, err
	}
	// Decided under the same lock that records a sign-in, so a credential cannot
	// be recorded between being judged a leftover and being deleted.
	release, err := s.lock(ctx)
	if err != nil {
		return 0, err
	}
	defer release()
	state, pending, err := s.store.LoadProviderAccountState(ctx)
	if err != nil {
		return 0, err
	}
	// An unfinished change may be about to name, or stop naming, a credential.
	if pending != nil {
		return 0, nil
	}
	used := make(map[string]bool, len(state.Accounts))
	for _, account := range state.Accounts {
		used[account.CredentialRef] = true
	}
	removed := 0
	var failures error
	for _, credential := range credentials {
		if used[credential.Name] || (credential.Provider != "codex" && credential.Provider != "claude") {
			continue
		}
		// One still inside a sign-in attempt may be recorded yet. An age AO
		// cannot read is no evidence that it is old.
		if credential.ModifiedAt.IsZero() || time.Since(credential.ModifiedAt) < leftoverCredentialAge {
			continue
		}
		if err := s.proxy.DeleteCredential(ctx, credential.Name); err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		removed++
	}
	return removed, failures
}
