package runner

import (
	"context"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func (v *credentialVault) setLabel(ctx context.Context, id, label string, generation uint64) (*coreauth.Auth, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.readyLocked(ctx); err != nil {
		return nil, err
	}
	label = strings.TrimSpace(label)
	if !utf8.ValidString(label) || utf8.RuneCountInString(label) > 80 || strings.IndexFunc(label, unicode.IsControl) >= 0 || generation == 0 {
		return nil, errCredentialConflict
	}
	entry, exists := v.state.Records[id]
	if !exists || entry.Deleted || v.removingLocked(id) || entry.Generation != generation {
		return nil, errCredentialFenced
	}
	auth, err := v.authLocked(id, entry)
	if err != nil {
		return nil, err
	}
	if auth.Label == label {
		return auth, nil
	}
	if entry.Generation == ^uint64(0) {
		return nil, errCredentialStorage
	}
	entry.Generation++
	auth.Attributes[vaultGenerationAttribute] = strconv.FormatUint(entry.Generation, 10)
	auth.Label, auth.UpdatedAt = label, time.Now().UTC()
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

func (r *credentialRuntime) SetLabel(ctx context.Context, id, label string, generation uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	auth, err := r.vault.setLabel(ctx, id, label, generation)
	if err != nil {
		return err
	}
	registered, err := r.manager.Register(ctx, auth)
	if err != nil || !r.vault.matchesCommitted(ctx, registered, false) {
		r.manager.Remove(ctx, id)
		return errCredentialFenced
	}
	r.registerModels(registered)
	return nil
}
