package runner

import (
	"context"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type credentialRefresh struct {
	done    chan struct{}
	auth    *coreauth.Auth
	err     error
	context context.Context
	cancel  context.CancelFunc
}

func (r *credentialRuntime) Refresh(ctx context.Context, id string) (*coreauth.Auth, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.refreshMu.Lock()
	if r.refreshClosed {
		r.refreshMu.Unlock()
		return nil, errCredentialFenced
	}
	r.initRefreshLocked(context.WithoutCancel(ctx))
	flight := r.startRefreshLocked(id)
	r.refreshMu.Unlock()
	if flight == nil {
		return nil, errCredentialFenced
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-flight.done:
	case <-flight.context.Done():
		select {
		case <-flight.done:
		default:
			return nil, errCredentialFenced
		}
	}
	if flight.err != nil {
		return nil, flight.err
	}
	if !r.vault.matchesCommitted(ctx, flight.auth, false) {
		return nil, errCredentialFenced
	}
	return flight.auth.Clone(), nil
}

func (r *credentialRuntime) initRefreshLocked(parent context.Context) {
	if r.refreshContext != nil {
		return
	}
	if r.context != nil {
		parent = r.context
	}
	r.refreshContext, r.refreshCancel = context.WithCancel(parent)
	r.refreshes = make(map[string]*credentialRefresh)
	r.refreshSlots = make(chan struct{}, 4)
}

func (r *credentialRuntime) startRefreshLocked(id string) *credentialRefresh {
	if !r.vault.admitsAccountWork(r.refreshContext, id) {
		return nil
	}
	flight := r.refreshes[id]
	if flight == nil {
		flight = &credentialRefresh{done: make(chan struct{})}
		flight.context, flight.cancel = context.WithTimeout(r.refreshContext, 10*time.Second)
		r.refreshes[id] = flight
		r.refreshWorkers.Add(1)
		go r.refreshCredential(id, flight)
	}
	return flight
}

func (r *credentialRuntime) refreshCredential(id string, flight *credentialRefresh) {
	defer r.refreshWorkers.Done()
	ctx := flight.context
	flight.err = errCredentialFenced
	defer func() {
		r.refreshMu.Lock()
		delete(r.refreshes, id)
		close(flight.done)
		flight.cancel()
		r.refreshMu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return
	case r.refreshSlots <- struct{}{}:
	}
	defer func() { <-r.refreshSlots }()
	if ctx.Err() != nil {
		return
	}
	auth, exists := r.manager.GetByID(id)
	if !exists || !r.vault.matchesCommitted(ctx, auth, false) {
		return
	}
	if token, _ := auth.Metadata["refresh_token"].(string); token == "" {
		return
	}
	updated, err := r.manager.ForceRefreshAuth(ctx, id)
	if err != nil || !r.vault.matchesCommitted(ctx, updated, false) {
		return
	}
	if err := r.vault.recordVerification(ctx, updated, true); err != nil {
		return
	}
	flight.auth, flight.err = updated, nil
}

func (r *credentialRuntime) Close() {
	r.refreshMu.Lock()
	r.refreshClosed = true
	if r.refreshCancel != nil {
		r.refreshCancel()
	}
	r.refreshMu.Unlock()
	r.stopRechecks()
	r.stopQuota()
	r.recheckWorkers.Wait()
	r.quotaWorkers.Wait()
	r.refreshWorkers.Wait()
}
