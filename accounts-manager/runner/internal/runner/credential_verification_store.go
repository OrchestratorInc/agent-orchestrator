package runner

import (
	"context"
	"strconv"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func (v *credentialVault) verification(ctx context.Context, auth *coreauth.Auth) (string, time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.readyLocked(ctx) != nil || auth == nil {
		return "unverified", time.Time{}
	}
	entry, exists := v.state.Records[auth.ID]
	if !exists || entry.Deleted || v.removingLocked(auth.ID) || strconv.FormatUint(entry.Generation, 10) != auth.Attributes[vaultGenerationAttribute] {
		return "unverified", time.Time{}
	}
	stored, err := v.authLocked(auth.ID, entry)
	if err != nil || !entry.provesCredential(stored) || !entry.provesCredential(auth) {
		return "unverified", time.Time{}
	}
	if entry.Verification == "verified" && !entry.VerifiedAt.IsZero() {
		return "verified", entry.VerifiedAt
	}
	if entry.Verification == "invalid" {
		return "invalid", time.Time{}
	}
	return "unverified", time.Time{}
}

func (v *credentialVault) recordVerification(ctx context.Context, auth *coreauth.Auth, valid bool) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.readyLocked(ctx); err != nil {
		return err
	}
	entry, exists := v.state.Records[auth.ID]
	if !exists || entry.Deleted || v.removingLocked(auth.ID) || strconv.FormatUint(entry.Generation, 10) != auth.Attributes[vaultGenerationAttribute] {
		return errCredentialFenced
	}
	stored, err := v.authLocked(auth.ID, entry)
	if err != nil {
		return err
	}
	want, err := credentialFingerprint(stored)
	if err != nil {
		return err
	}
	got, err := credentialFingerprint(auth)
	if err != nil || got != want {
		return errCredentialFenced
	}
	entry.Verification, entry.VerifiedAt = "invalid", time.Time{}
	entry.VerificationFingerprint, err = credentialVerificationFingerprint(stored)
	if err != nil {
		return err
	}
	if valid {
		entry.Verification, entry.VerifiedAt = "verified", time.Now().UTC()
	}
	next := v.cloneLocked()
	next.Records[auth.ID] = entry
	return v.persistLocked(next)
}

func credentialVerificationFingerprint(auth *coreauth.Auth) (string, error) {
	copyAuth, err := normalizedVaultAuth(auth)
	if err != nil {
		return "", err
	}
	delete(copyAuth.Attributes, vaultGenerationAttribute)
	delete(copyAuth.Attributes, coreauth.AttributeAuthIndexSeed)
	delete(copyAuth.Metadata, "disabled")
	return credentialFingerprint(copyAuth)
}

func (entry vaultEntry) provesCredential(auth *coreauth.Auth) bool {
	fingerprint, err := credentialVerificationFingerprint(auth)
	return err == nil && fingerprint != "" && fingerprint == entry.VerificationFingerprint
}

func (v *credentialVault) admitVerified(ctx context.Context, auth *coreauth.Auth) bool {
	return v.matchesAdmitted(ctx, auth, true, true)
}
