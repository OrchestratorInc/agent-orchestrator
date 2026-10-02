package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const removalOperationPrefix = "_ao_removal_"

type credentialRecheck struct {
	done   chan struct{}
	cancel context.CancelFunc
}

func removalOperationID(id string) string {
	digest := sha256.Sum256([]byte(id))
	return removalOperationPrefix + hex.EncodeToString(digest[:])
}

func reservedRemovalOperation(id string) bool { return strings.HasPrefix(id, removalOperationPrefix) }

func (v *credentialVault) removingLocked(id string) bool {
	_, exists := v.state.Operations[removalOperationID(id)]
	return exists
}

func (v *credentialVault) validRemovalLocked(id string, op vaultOperation) bool {
	entry, exists := v.state.Records[op.AccountID]
	return exists && !entry.Deleted && validVaultID(op.AccountID) && id == removalOperationID(op.AccountID) &&
		op.Status == "removing" && op.Provider == entry.Provider && op.ExpectedGeneration == entry.Generation &&
		op.ExpiresAt.IsZero() && op.Fingerprint == "" && !op.ProviderLogin && op.TargetID == "" && op.ExpectedFingerprint == ""
}

func (v *credentialVault) beginRemoval(ctx context.Context, id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.readyLocked(ctx); err != nil {
		return err
	}
	if !validVaultID(id) {
		return errCredentialConflict
	}
	entry, exists := v.state.Records[id]
	if !exists || entry.Deleted {
		return nil
	}
	key := removalOperationID(id)
	if op, exists := v.state.Operations[key]; exists {
		if !v.validRemovalLocked(key, op) {
			return errCredentialStorage
		}
		return nil
	}
	next := v.cloneLocked()
	next.Operations[key] = vaultOperation{Status: "removing", AccountID: id, Provider: entry.Provider, ExpectedGeneration: entry.Generation}
	return v.persistLocked(next)
}

func (v *credentialVault) admitsAccountWork(ctx context.Context, id string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	entry, exists := v.state.Records[id]
	return v.readyLocked(ctx) == nil && exists && !entry.Deleted && !v.removingLocked(id)
}

func (r *credentialRuntime) drainAccountWorkers(ctx context.Context, id string) error {
	var pending []<-chan struct{}
	r.refreshMu.Lock()
	if flight := r.refreshes[id]; flight != nil {
		flight.cancel()
		pending = append(pending, flight.done)
	}
	r.refreshMu.Unlock()
	r.quotaMu.Lock()
	for flight := range r.quotaPending[id] {
		flight.cancel()
		pending = append(pending, flight.done)
	}
	delete(r.quotas, id)
	r.quotaMu.Unlock()
	r.checkMu.Lock()
	for flight := range r.rechecks[id] {
		flight.cancel()
		pending = append(pending, flight.done)
	}
	r.checkMu.Unlock()
	for _, done := range pending {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
		}
	}
	return ctx.Err()
}

func (r *credentialRuntime) stopRechecks() {
	r.checkMu.Lock()
	r.recheckClosed = true
	for _, pending := range r.rechecks {
		for flight := range pending {
			flight.cancel()
		}
	}
	r.checkMu.Unlock()
}
