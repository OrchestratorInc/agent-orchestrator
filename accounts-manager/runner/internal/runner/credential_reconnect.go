package runner

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func credentialFingerprint(auth *coreauth.Auth) (string, error) {
	raw, err := json.Marshal(struct {
		Provider         string
		Metadata         map[string]any
		Attributes       map[string]string
		ProxyURL, Prefix string
	}{auth.Provider, auth.Metadata, auth.Attributes, auth.ProxyURL, auth.Prefix})
	if err != nil {
		return "", errCredentialConflict
	}
	defer clear(raw)
	return credentialDigest(raw), nil
}

// Only direct SDK login results establish this identity, never imported claims.
func providerCredentialIdentity(auth *coreauth.Auth) string {
	if auth == nil || auth.Attributes["api_key"] != "" {
		return ""
	}
	atom := func(key string) string {
		value, _ := auth.Metadata[key].(string)
		if !validIdentityAtom(value) {
			return ""
		}
		return value
	}
	var identity []string
	switch auth.Provider {
	case "codex":
		token, _ := auth.Metadata["id_token"].(string)
		parts := strings.Split(token, ".")
		if len(parts) != 3 || len(parts[1]) > 64<<10 || atom("account_id") == "" {
			return ""
		}
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
		defer clear(payload)
		var claims struct {
			Subject string `json:"sub"`
			Auth    struct {
				AccountID string `json:"chatgpt_account_id"`
			} `json:"https://api.openai.com/auth"`
		}
		if json.Unmarshal(payload, &claims) != nil || !validIdentityAtom(claims.Subject) || claims.Auth.AccountID != atom("account_id") {
			return ""
		}
		identity = []string{auth.Provider, claims.Subject, claims.Auth.AccountID}
	case "claude":
		if atom("account_uuid") == "" || atom("organization_uuid") == "" {
			return ""
		}
		identity = []string{auth.Provider, atom("account_uuid"), atom("organization_uuid")}
	default:
		return ""
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return ""
	}
	defer clear(raw)
	return credentialDigest(raw)
}

func validIdentityAtom(value string) bool {
	return value != "" && len(value) <= 256 && strings.TrimSpace(value) == value && strings.IndexFunc(value, func(r rune) bool { return r < 32 || r == 127 }) < 0
}

func (v *credentialVault) reconnectVersion(ctx context.Context, auth *coreauth.Auth) (uint64, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.readyLocked(ctx) != nil || auth == nil {
		return 0, false
	}
	entry, exists := v.state.Records[auth.ID]
	if !exists || entry.Deleted || v.removingLocked(auth.ID) || strconv.FormatUint(entry.Generation, 10) != auth.Attributes[vaultGenerationAttribute] {
		return 0, false
	}
	return entry.Generation, entry.Identity != ""
}

func (v *credentialVault) beginProviderLogin(ctx context.Context, operationID, provider string, expiresAt time.Time, targetID string, generation uint64) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.readyLocked(ctx); err != nil {
		return err
	}
	if !validVaultID(operationID) || reservedRemovalOperation(operationID) || !validVaultProvider(provider) || !time.Now().Before(expiresAt) || (targetID == "") != (generation == 0) {
		return errCredentialConflict
	}
	if prior, exists := v.state.Operations[operationID]; exists {
		if !prior.ProviderLogin || prior.Provider != provider || prior.Status != "pending" || (targetID != "" && (prior.TargetID != targetID || prior.ExpectedGeneration != generation)) {
			return errCredentialConflict
		}
		if prior.TargetID != "" && v.removingLocked(prior.TargetID) {
			return errCredentialFenced
		}
		return nil
	}
	op := vaultOperation{Provider: provider, ExpiresAt: expiresAt, Status: "pending", ProviderLogin: true}
	if targetID != "" {
		entry, exists := v.state.Records[targetID]
		if !exists || entry.Deleted || v.removingLocked(targetID) || entry.Provider != provider || entry.Generation != generation {
			return errCredentialFenced
		}
		if entry.Identity == "" {
			return errCredentialIdentity
		}
		auth, err := v.authLocked(targetID, entry)
		if err != nil {
			return err
		}
		fingerprint, err := credentialFingerprint(auth)
		if err != nil {
			return err
		}
		op.TargetID, op.ExpectedGeneration, op.ExpectedFingerprint = targetID, generation, fingerprint
	}
	next := v.cloneLocked()
	next.Operations[operationID] = op
	return v.persistLocked(next)
}

func (v *credentialVault) commitReconnectLocked(operationID string, op vaultOperation, auth *coreauth.Auth, fingerprint string) (*coreauth.Auth, error) {
	entry, exists := v.state.Records[op.TargetID]
	if !op.ProviderLogin || !exists || entry.Deleted || v.removingLocked(op.TargetID) || entry.Generation != op.ExpectedGeneration || entry.Provider != auth.Provider {
		return nil, errCredentialFenced
	}
	if entry.Identity == "" || providerCredentialIdentity(auth) != entry.Identity {
		return nil, errCredentialIdentity
	}
	previous, err := v.authLocked(op.TargetID, entry)
	if err != nil {
		return nil, err
	}
	current, err := credentialFingerprint(previous)
	if err != nil {
		return nil, err
	}
	if current != op.ExpectedFingerprint {
		return nil, errCredentialConflict
	}
	if entry.Generation == ^uint64(0) {
		return nil, errCredentialStorage
	}
	entry.Generation++
	auth.ID, auth.Index, auth.FileName = previous.ID, previous.Index, ""
	auth.Label, auth.CreatedAt, auth.Disabled = previous.Label, previous.CreatedAt, previous.Disabled
	auth.Status = coreauth.StatusActive
	if auth.Disabled {
		auth.Status = coreauth.StatusDisabled
	}
	auth.Metadata["disabled"] = auth.Disabled
	auth.Attributes[vaultGenerationAttribute] = strconv.FormatUint(entry.Generation, 10)
	auth.Attributes[coreauth.AttributeAuthIndexSeed] = previous.Attributes[coreauth.AttributeAuthIndexSeed]
	auth.UpdatedAt = time.Now().UTC()
	entry.Sealed, err = v.sealAuth(auth, entry)
	if err != nil {
		return nil, err
	}
	next := v.cloneLocked()
	next.Records[auth.ID] = entry
	op.Status, op.AccountID, op.Fingerprint = "committed", auth.ID, fingerprint
	next.Operations[operationID] = op
	if err := v.persistLocked(next); err != nil {
		return nil, err
	}
	return v.authLocked(auth.ID, entry)
}
