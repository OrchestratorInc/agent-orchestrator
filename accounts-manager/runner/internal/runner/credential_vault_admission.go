package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// Admit rejects SDK runtime updates that were never durably accepted by the store.
func (v *credentialVault) Admit(ctx context.Context, incoming *coreauth.Auth) bool {
	return v.matchesCommitted(ctx, incoming, true)
}

func (v *credentialVault) matchesCommitted(ctx context.Context, incoming *coreauth.Auth, requireEnabled bool) bool {
	return v.matchesAdmitted(ctx, incoming, requireEnabled, false)
}

func (v *credentialVault) matchesAdmitted(ctx context.Context, incoming *coreauth.Auth, requireEnabled, requireVerified bool) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.readyLocked(ctx) != nil || incoming == nil || (requireEnabled && incoming.Disabled) {
		return false
	}
	entry, exists := v.state.Records[incoming.ID]
	if !exists || entry.Deleted || v.removingLocked(incoming.ID) || entry.Provider != incoming.Provider || incoming.Attributes[vaultGenerationAttribute] != strconv.FormatUint(entry.Generation, 10) {
		return false
	}
	if requireVerified && (entry.Verification != "verified" || entry.VerifiedAt.IsZero()) {
		return false
	}
	stored, err := v.authLocked(incoming.ID, entry)
	if err != nil || stored.Disabled != incoming.Disabled || stored.Index != incoming.Index || stored.ProxyURL != incoming.ProxyURL || stored.Prefix != incoming.Prefix {
		return false
	}
	if requireVerified && !entry.provesCredential(stored) {
		return false
	}
	auth, err := normalizedVaultAuth(incoming)
	if err != nil {
		return false
	}
	current, err := json.Marshal([]any{stored.Metadata, stored.Attributes})
	if err != nil {
		return false
	}
	defer clear(current)
	candidate, err := json.Marshal([]any{auth.Metadata, auth.Attributes})
	defer clear(candidate)
	return err == nil && bytes.Equal(current, candidate)
}

func (v *credentialVault) setEnabled(ctx context.Context, id string, enabled bool) (*coreauth.Auth, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.readyLocked(ctx); err != nil {
		return nil, err
	}
	entry, exists := v.state.Records[id]
	if !exists || entry.Deleted || v.removingLocked(id) {
		return nil, errCredentialFenced
	}
	auth, err := v.authLocked(id, entry)
	if err != nil {
		return nil, err
	}
	if auth.Disabled == !enabled {
		return auth, nil
	}
	if entry.Generation == ^uint64(0) {
		return nil, errCredentialStorage
	}
	entry.Generation++
	auth.Attributes[vaultGenerationAttribute] = strconv.FormatUint(entry.Generation, 10)
	auth.Disabled = !enabled
	auth.Metadata["disabled"] = !enabled
	auth.Status = coreauth.StatusActive
	if !enabled {
		auth.Status = coreauth.StatusDisabled
	}
	auth.UpdatedAt = time.Now().UTC()
	entry.Sealed, err = v.sealAuth(auth, entry)
	if err != nil {
		return nil, err
	}
	next := v.cloneLocked()
	next.Records[id] = entry
	if err := v.persistLocked(next); err != nil {
		return nil, err
	}
	return auth, nil
}
