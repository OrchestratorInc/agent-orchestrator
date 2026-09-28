package runner

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	sdkauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

type credentialRuntime struct {
	mu             sync.Mutex
	vault          *credentialVault
	manager        *coreauth.Manager
	models         credentialModels
	context        context.Context
	refreshMu      sync.Mutex
	refreshes      map[string]*credentialRefresh
	refreshSlots   chan struct{}
	refreshContext context.Context
	refreshCancel  context.CancelFunc
	refreshWorkers sync.WaitGroup
	refreshClosed  bool
	refreshLoop    bool
	checkTransport http.RoundTripper
	checkMu        sync.Mutex
	checkSlots     chan struct{}
	quotaMu        sync.Mutex
	quotas         map[string]*credentialQuotaFlight
}

func (r *credentialRuntime) ConnectBrowser(ctx context.Context, operationID string, authenticator sdkauth.Authenticator, cfg *sdkconfig.Config, listener net.Listener, deliverURL func(context.Context, string) error) (_ *coreauth.Auth, resultErr error) {
	if listener == nil {
		return nil, errCredentialConflict
	}
	// Successful login checks closure before commit; rejected starts still release the listener.
	defer func() { _ = listener.Close() }()
	if authenticator == nil || deliverURL == nil || cfg == nil {
		return nil, errCredentialConflict
	}
	ctx, cancel := context.WithTimeout(ctx, oauthSessionDuration)
	defer cancel()
	expiresAt, _ := ctx.Deadline()
	if err := r.vault.beginProviderLogin(ctx, operationID, authenticator.Provider(), expiresAt, "", 0); err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer stop()
			if err := r.vault.Cancel(cleanup, operationID); err != nil && !errors.Is(err, errCredentialCommitted) {
				resultErr = errCredentialStorage
			}
		}
	}()
	auth, err := authenticator.Login(ctx, cfg, &sdkauth.LoginOptions{
		NoBrowser: true, CallbackListener: listener, AuthorizationURL: deliverURL,
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("credential sign-in failed")
	}
	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return nil, errCredentialStorage
	}
	return r.complete(ctx, operationID, auth, true)
}

func (r *credentialRuntime) Complete(ctx context.Context, operationID string, incoming *coreauth.Auth) (*coreauth.Auth, error) {
	return r.complete(ctx, operationID, incoming, false)
}

func (r *credentialRuntime) complete(ctx context.Context, operationID string, incoming *coreauth.Auth, providerLogin bool) (*coreauth.Auth, error) {
	return r.completeObserved(ctx, operationID, incoming, providerLogin, providerLogin)
}

func (r *credentialRuntime) completeObserved(ctx context.Context, operationID string, incoming *coreauth.Auth, providerLogin, verified bool) (*coreauth.Auth, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	auth, err := r.vault.commit(ctx, operationID, incoming, providerLogin)
	if err != nil {
		return nil, err
	}
	if verified {
		observed, observationErr := credentialVerificationFingerprint(incoming)
		current, currentErr := credentialVerificationFingerprint(auth)
		if observationErr != nil || currentErr != nil || observed != current {
			return nil, errCredentialFenced
		}
		if err := r.vault.recordVerification(ctx, auth, true); err != nil {
			return nil, err
		}
	}
	registered, err := r.manager.Register(ctx, auth)
	if err != nil || !r.vault.matchesCommitted(ctx, registered, false) {
		r.manager.Remove(ctx, auth.ID)
		return nil, errCredentialFenced
	}
	r.registerModels(registered)
	return registered, nil
}

func (r *credentialRuntime) Remove(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.vault.Delete(ctx, id); err != nil {
		return err
	}
	r.manager.Remove(ctx, id)
	cliproxy.GlobalModelRegistry().UnregisterClient(id)
	r.quotaMu.Lock()
	delete(r.quotas, id)
	r.quotaMu.Unlock()
	return nil
}

func (r *credentialRuntime) SetEnabled(ctx context.Context, id string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	auth, err := r.vault.setEnabled(ctx, id, enabled)
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

func (r *credentialRuntime) Reload(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Remove stale runtime entries even if the encrypted store cannot be reopened.
	for _, auth := range r.manager.List() {
		r.manager.Remove(ctx, auth.ID)
		cliproxy.GlobalModelRegistry().UnregisterClient(auth.ID)
	}
	if err := r.manager.Load(ctx); err != nil {
		return errCredentialStorage
	}
	for _, auth := range r.manager.List() {
		r.registerModels(auth)
	}
	return nil
}
