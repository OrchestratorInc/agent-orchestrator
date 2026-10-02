package runner

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const (
	vaultFileName            = "credentials.vault"
	vaultKeyName             = "credentials.key"
	vaultLockName            = "credentials.lock"
	vaultGenerationAttribute = "ao_credential_generation"
	vaultFormat              = 1
	vaultMaxBytes            = 32 << 20
)

var (
	errCredentialFenced    = errors.New("credential operation is no longer admitted")
	errCredentialConflict  = errors.New("credential operation conflicts with committed state")
	errCredentialCommitted = errors.New("credential operation already committed")
	errCredentialStorage   = errors.New("credential storage is unavailable")
	errCredentialIdentity  = errors.New("credential identity cannot authorize replacement")
)

type vaultEntry struct {
	Verification            string    `json:"verification,omitempty"`
	VerificationFingerprint string    `json:"verification_fingerprint,omitempty"`
	VerifiedAt              time.Time `json:"verified_at,omitempty"`
	Provider                string    `json:"provider"`
	Generation              uint64    `json:"generation"`
	Deleted                 bool      `json:"deleted,omitempty"`
	Sealed                  []byte    `json:"sealed,omitempty"`
	Identity                string    `json:"identity,omitempty"`
}

type vaultOperation struct {
	Provider            string    `json:"provider"`
	ExpiresAt           time.Time `json:"expires_at"`
	Status              string    `json:"status"`
	AccountID           string    `json:"account_id,omitempty"`
	Fingerprint         string    `json:"fingerprint,omitempty"`
	ProviderLogin       bool      `json:"provider_login,omitempty"`
	TargetID            string    `json:"target_id,omitempty"`
	ExpectedGeneration  uint64    `json:"expected_generation,omitempty"`
	ExpectedFingerprint string    `json:"expected_fingerprint,omitempty"`
}

type vaultState struct {
	Version    int                         `json:"version"`
	Records    map[string]vaultEntry       `json:"records"`
	Operations map[string]vaultOperation   `json:"operations"`
	Legacy     map[string]legacyCredential `json:"legacy,omitempty"`
}

type vaultAuth struct {
	Auth  *coreauth.Auth `json:"auth"`
	Index string         `json:"index"`
}

type credentialVault struct {
	mu     sync.Mutex
	root   *os.Root
	lock   *os.File
	aead   cipher.AEAD
	state  vaultState
	closed bool
	failed bool
}

func openCredentialVault(path string) (_ *credentialVault, resultErr error) {
	if !filepath.IsAbs(path) {
		return nil, errCredentialStorage
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errCredentialStorage
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, errCredentialStorage
	}
	v := &credentialVault{root: root, state: vaultState{Version: vaultFormat, Records: make(map[string]vaultEntry), Operations: make(map[string]vaultOperation)}}
	defer func() {
		if resultErr != nil {
			_ = v.Close()
		}
	}()
	dir, err := root.Open(".")
	if err != nil {
		return nil, errCredentialStorage
	}
	openedInfo, statErr := dir.Stat()
	securityErr := validateVaultFile(dir, true)
	_ = dir.Close()
	if statErr != nil || securityErr != nil || !os.SameFile(info, openedInfo) {
		return nil, errCredentialStorage
	}
	v.lock, err = v.openPrivateFile(vaultLockName, true)
	if err != nil || lockVaultFile(v.lock) != nil {
		return nil, errCredentialStorage
	}

	key, keyErr := v.readFile(vaultKeyName, 32)
	if errors.Is(keyErr, os.ErrNotExist) {
		if _, err := root.Lstat(vaultFileName); !errors.Is(err, os.ErrNotExist) {
			return nil, errCredentialStorage
		}
		key = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, errCredentialStorage
		}
		if err := v.writeNewFile(vaultKeyName, key); err != nil {
			return nil, errCredentialStorage
		}
	} else if keyErr != nil || len(key) != 32 {
		return nil, errCredentialStorage
	}
	block, err := aes.NewCipher(key)
	clear(key)
	if err != nil {
		return nil, errCredentialStorage
	}
	v.aead, err = cipher.NewGCM(block)
	if err != nil {
		return nil, errCredentialStorage
	}
	raw, err := v.readFile(vaultFileName, vaultMaxBytes)
	if errors.Is(err, os.ErrNotExist) {
		if err := v.persistLocked(v.state); err != nil {
			return nil, err
		}
		return v, nil
	}
	if err != nil {
		return nil, errCredentialStorage
	}
	plain, err := v.open(raw, []byte("ao-credential-vault-v1"))
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	if json.Unmarshal(plain, &v.state) != nil || v.state.Version != vaultFormat || v.state.Records == nil || v.state.Operations == nil {
		return nil, errCredentialStorage
	}
	for id, entry := range v.state.Records {
		if entry.Generation == 0 || !validVaultID(id) || !validVaultProvider(entry.Provider) || (entry.Identity != "" && len(entry.Identity) != sha256.Size*2) {
			return nil, errCredentialStorage
		}
		if !entry.Deleted {
			if _, err := v.authLocked(id, entry); err != nil {
				return nil, err
			}
		}
	}
	for _, source := range v.state.Legacy {
		entry, exists := v.state.Records[source.AccountID]
		if !exists || len(source.Fingerprint) != sha256.Size*2 || source.Index == "" {
			return nil, errCredentialStorage
		}
		if !entry.Deleted {
			auth, err := v.authLocked(source.AccountID, entry)
			if err != nil || auth.Index != source.Index {
				return nil, errCredentialStorage
			}
		}
	}
	changed := false
	for id, operation := range v.state.Operations {
		if !validVaultID(id) {
			return nil, errCredentialStorage
		}
		if reservedRemovalOperation(id) || operation.Status == "removing" {
			if !v.validRemovalLocked(id, operation) {
				return nil, errCredentialStorage
			}
			continue
		}
		switch operation.Status {
		case "pending", "committed":
			if !validVaultProvider(operation.Provider) || operation.ExpiresAt.IsZero() {
				return nil, errCredentialStorage
			}
		case "cancelled":
		default:
			return nil, errCredentialStorage
		}
		if operation.Status == "committed" {
			entry, exists := v.state.Records[operation.AccountID]
			if !exists || entry.Provider != operation.Provider || len(operation.Fingerprint) != sha256.Size*2 {
				return nil, errCredentialStorage
			}
		}
		if operation.TargetID != "" {
			entry, exists := v.state.Records[operation.TargetID]
			if !operation.ProviderLogin || !exists || entry.Provider != operation.Provider || operation.ExpectedGeneration == 0 || len(operation.ExpectedFingerprint) != sha256.Size*2 || (operation.Status == "committed" && operation.AccountID != operation.TargetID) {
				return nil, errCredentialStorage
			}
		} else if operation.ExpectedGeneration != 0 || operation.ExpectedFingerprint != "" {
			return nil, errCredentialStorage
		}
		if operation.Status == "pending" {
			operation.Status = "cancelled"
			v.state.Operations[id] = operation
			changed = true
		}
	}
	if changed {
		if err := v.persistLocked(v.state); err != nil {
			return nil, err
		}
	}
	return v, nil
}

func (v *credentialVault) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return nil
	}
	v.closed = true
	var lockErr error
	if v.lock != nil {
		lockErr = v.lock.Close()
	}
	return errors.Join(lockErr, v.root.Close())
}

func (v *credentialVault) Begin(ctx context.Context, operationID, provider string, expiresAt time.Time) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.readyLocked(ctx); err != nil {
		return err
	}
	if !validVaultID(operationID) || reservedRemovalOperation(operationID) || !validVaultProvider(provider) || expiresAt.IsZero() {
		return errCredentialConflict
	}
	if prior, exists := v.state.Operations[operationID]; exists {
		if prior.ProviderLogin {
			return errCredentialConflict
		}
		if prior.Provider == provider && prior.Status == "committed" {
			return errCredentialCommitted
		}
		if prior.Provider != provider || prior.Status != "pending" {
			return errCredentialConflict
		}
		return nil
	}
	next := v.cloneLocked()
	next.Operations[operationID] = vaultOperation{Provider: provider, ExpiresAt: expiresAt, Status: "pending"}
	return v.persistLocked(next)
}

func (v *credentialVault) Cancel(ctx context.Context, operationID string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.readyLocked(ctx); err != nil {
		return err
	}
	if !validVaultID(operationID) || reservedRemovalOperation(operationID) {
		return errCredentialConflict
	}
	op, exists := v.state.Operations[operationID]
	if exists && op.Status == "cancelled" {
		return nil
	}
	if op.Status == "committed" {
		return errCredentialCommitted
	}
	next := v.cloneLocked()
	// Cancellation can arrive before the worker records its start.
	op.Status = "cancelled"
	next.Operations[operationID] = op
	return v.persistLocked(next)
}

func (v *credentialVault) Commit(ctx context.Context, operationID string, incoming *coreauth.Auth) (*coreauth.Auth, error) {
	return v.commit(ctx, operationID, incoming, false)
}

func (v *credentialVault) commit(ctx context.Context, operationID string, incoming *coreauth.Auth, providerLogin bool) (*coreauth.Auth, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.readyLocked(ctx); err != nil {
		return nil, err
	}
	op, exists := v.state.Operations[operationID]
	if !exists || incoming == nil || incoming.Provider != op.Provider {
		return nil, errCredentialConflict
	}
	if op.Status != "pending" && op.Status != "committed" {
		return nil, errCredentialFenced
	}
	if op.ProviderLogin != providerLogin {
		return nil, errCredentialConflict
	}
	auth, err := normalizedVaultAuth(incoming)
	if err != nil {
		return nil, err
	}
	fingerprint, err := credentialFingerprint(auth)
	if err != nil {
		return nil, err
	}
	if op.Status == "committed" {
		entry, found := v.state.Records[op.AccountID]
		if !found || entry.Deleted || v.removingLocked(op.AccountID) {
			return nil, errCredentialFenced
		}
		if op.Fingerprint != fingerprint {
			return nil, errCredentialConflict
		}
		return v.authLocked(op.AccountID, entry)
	}
	if op.Status != "pending" || !time.Now().Before(op.ExpiresAt) {
		return nil, errCredentialFenced
	}
	if op.TargetID != "" {
		return v.commitReconnectLocked(operationID, op, auth, fingerprint)
	}
	for id, entry := range v.state.Records {
		if auth.Attributes["api_key"] == "" || entry.Provider != auth.Provider || entry.Deleted {
			continue
		}
		previous, err := v.authLocked(id, entry)
		if err != nil {
			return nil, err
		}
		if previous.Attributes["api_key"] != auth.Attributes["api_key"] || previous.Attributes["base_url"] != auth.Attributes["base_url"] {
			continue
		}
		if v.removingLocked(id) {
			return nil, errCredentialFenced
		}
		next := v.cloneLocked()
		op.Status, op.AccountID, op.Fingerprint = "committed", id, fingerprint
		next.Operations[operationID] = op
		if err := v.persistLocked(next); err != nil {
			return nil, err
		}
		return previous, nil
	}
	id, err := randomVaultID()
	if err != nil {
		return nil, err
	}
	auth.ID, auth.FileName, auth.Index = id, "", ""
	auth.Attributes[vaultGenerationAttribute] = "1"
	auth.Attributes[coreauth.AttributeAuthIndexSeed] = id
	auth.EnsureIndex()
	now := time.Now().UTC()
	auth.CreatedAt, auth.UpdatedAt = now, now
	entry := vaultEntry{Provider: auth.Provider, Generation: 1}
	if providerLogin {
		entry.Identity = providerCredentialIdentity(auth)
	}
	entry.Sealed, err = v.sealAuth(auth, entry)
	if err != nil {
		return nil, err
	}
	next := v.cloneLocked()
	next.Records[id] = entry
	op.Status, op.AccountID, op.Fingerprint = "committed", id, fingerprint
	next.Operations[operationID] = op
	if err := v.persistLocked(next); err != nil {
		return nil, err
	}
	return v.authLocked(id, entry)
}

func (v *credentialVault) Save(ctx context.Context, auth *coreauth.Auth) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.readyLocked(ctx); err != nil {
		return "", err
	}
	if auth == nil {
		return "", errCredentialFenced
	}
	entry, exists := v.state.Records[auth.ID]
	if !exists || entry.Deleted || v.removingLocked(auth.ID) || auth.Provider != entry.Provider || auth.Attributes[vaultGenerationAttribute] != strconv.FormatUint(entry.Generation, 10) {
		return "", errCredentialFenced
	}
	previous, err := v.authLocked(auth.ID, entry)
	if err != nil {
		return "", err
	}
	stored, err := normalizedVaultAuth(auth)
	if err != nil {
		return "", err
	}
	if entry.Identity != "" && providerCredentialIdentity(stored) != entry.Identity {
		return "", errCredentialIdentity
	}
	stored.Index = previous.Index
	stored.Label = previous.Label
	stored.Attributes[coreauth.AttributeAuthIndexSeed] = previous.Attributes[coreauth.AttributeAuthIndexSeed]
	entry.Sealed, err = v.sealAuth(stored, entry)
	if err != nil {
		return "", err
	}
	next := v.cloneLocked()
	next.Records[auth.ID] = entry
	if err := v.persistLocked(next); err != nil {
		return "", err
	}
	// SDK callers may attempt to read this reference as a plaintext file.
	return "vault:" + auth.ID, nil
}

func (v *credentialVault) Delete(ctx context.Context, id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.readyLocked(ctx); err != nil {
		return err
	}
	entry, exists := v.state.Records[id]
	if !exists || entry.Deleted {
		return nil
	}
	if entry.Generation == ^uint64(0) {
		return errCredentialStorage
	}
	entry.Deleted, entry.Sealed = true, nil
	entry.Generation++
	next := v.cloneLocked()
	next.Records[id] = entry
	delete(next.Operations, removalOperationID(id))
	return v.persistLocked(next)
}

func (v *credentialVault) List(ctx context.Context) ([]*coreauth.Auth, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.readyLocked(ctx); err != nil {
		return nil, err
	}
	items := make([]*coreauth.Auth, 0, len(v.state.Records))
	for id, entry := range v.state.Records {
		if entry.Deleted {
			continue
		}
		auth, err := v.authLocked(id, entry)
		if err != nil {
			return nil, err
		}
		if v.removingLocked(id) {
			auth.Disabled, auth.Status = true, coreauth.StatusDisabled
		}
		items = append(items, auth)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func (v *credentialVault) readyLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if v.closed || v.failed {
		return errCredentialStorage
	}
	return nil
}

func (v *credentialVault) authLocked(id string, entry vaultEntry) (*coreauth.Auth, error) {
	plain, err := v.open(entry.Sealed, vaultRecordAAD(id, entry))
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	var record vaultAuth
	if json.Unmarshal(plain, &record) != nil || record.Auth == nil || record.Auth.ID != id || record.Auth.Provider != entry.Provider || record.Index == "" {
		return nil, errCredentialStorage
	}
	if record.Auth.Attributes[vaultGenerationAttribute] != strconv.FormatUint(entry.Generation, 10) {
		return nil, errCredentialStorage
	}
	record.Auth.Index = record.Index
	if token, _ := record.Auth.Metadata["refresh_token"].(string); token == "" {
		record.Auth.Runtime = unrefreshableCredential{}
	}
	return record.Auth, nil
}

type unrefreshableCredential struct{}

func (unrefreshableCredential) ShouldRefresh(time.Time, *coreauth.Auth) bool { return false }

func (v *credentialVault) sealAuth(auth *coreauth.Auth, entry vaultEntry) ([]byte, error) {
	plain, err := json.Marshal(vaultAuth{Auth: auth, Index: auth.Index})
	if err != nil {
		return nil, errCredentialConflict
	}
	defer clear(plain)
	return v.seal(plain, vaultRecordAAD(auth.ID, entry))
}

func vaultRecordAAD(id string, entry vaultEntry) []byte {
	return []byte(fmt.Sprintf("ao-credential-v1\x00%s\x00%s\x00%d", id, entry.Provider, entry.Generation))
}

func (v *credentialVault) cloneLocked() vaultState {
	next := vaultState{Version: v.state.Version, Records: make(map[string]vaultEntry, len(v.state.Records)), Operations: make(map[string]vaultOperation, len(v.state.Operations)), Legacy: make(map[string]legacyCredential, len(v.state.Legacy))}
	for id, entry := range v.state.Records {
		next.Records[id] = entry
	}
	for id, operation := range v.state.Operations {
		next.Operations[id] = operation
	}
	for source, credential := range v.state.Legacy {
		next.Legacy[source] = credential
	}
	return next
}

func (v *credentialVault) seal(plain, aad []byte) ([]byte, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, errCredentialStorage
	}
	return v.aead.Seal(nonce, nonce, plain, aad), nil
}

func (v *credentialVault) open(sealed, aad []byte) ([]byte, error) {
	if len(sealed) < v.aead.NonceSize()+v.aead.Overhead() {
		return nil, errCredentialStorage
	}
	plain, err := v.aead.Open(nil, sealed[:v.aead.NonceSize()], sealed[v.aead.NonceSize():], aad)
	if err != nil {
		return nil, errCredentialStorage
	}
	return plain, nil
}

func normalizedVaultAuth(incoming *coreauth.Auth) (*coreauth.Auth, error) {
	if incoming == nil || !validVaultProvider(incoming.Provider) {
		return nil, errCredentialConflict
	}
	// Provider token-storage structs contain fields omitted from Auth's JSON.
	metadata := make(map[string]any)
	if incoming.Storage != nil {
		raw, err := json.Marshal(incoming.Storage)
		if err != nil {
			return nil, errCredentialConflict
		}
		err = json.Unmarshal(raw, &metadata)
		clear(raw)
		if err != nil || metadata == nil {
			return nil, errCredentialConflict
		}
	}
	for key, value := range incoming.Metadata {
		metadata[key] = value
	}
	metadata["disabled"] = incoming.Disabled
	coreauth.NormalizeCredentialMetadata(metadata)
	copyAuth := *incoming
	copyAuth.Metadata, copyAuth.Storage, copyAuth.Runtime = metadata, nil, nil
	copyAuth.ModelStates, copyAuth.LastError, copyAuth.Quota, copyAuth.StatusMessage = nil, nil, coreauth.QuotaState{}, ""
	raw, err := json.Marshal(vaultAuth{Auth: &copyAuth, Index: incoming.Index})
	if err != nil || len(raw) > 1<<20 {
		return nil, errCredentialConflict
	}
	defer clear(raw)
	var detached vaultAuth
	if json.Unmarshal(raw, &detached) != nil {
		return nil, errCredentialConflict
	}
	auth := detached.Auth
	auth.Index = detached.Index
	if auth.Attributes == nil {
		auth.Attributes = make(map[string]string)
	}
	delete(auth.Attributes, coreauth.AttributePath)
	delete(auth.Attributes, coreauth.AttributeSource)
	if auth.Status == "" {
		auth.Status = coreauth.StatusActive
	}
	return auth, nil
}

func validVaultProvider(provider string) bool {
	_, ok := oauthProviders[provider]
	return ok
}

func validVaultID(id string) bool {
	return id != "" && len(id) <= 128 && strings.IndexFunc(id, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_'
	}) < 0
}

func randomVaultID() (string, error) {
	var raw [16]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		return "", errCredentialStorage
	}
	return hex.EncodeToString(raw[:]), nil
}

var _ coreauth.Store = (*credentialVault)(nil)
