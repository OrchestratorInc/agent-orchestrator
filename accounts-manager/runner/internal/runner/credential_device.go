package runner

import (
	"context"
	"errors"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func (r *credentialRuntime) ConnectDevice(ctx context.Context, operationID string, start func(context.Context) (codexDeviceLogin, error), deliver func(codexDeviceLogin) error) (_ *coreauth.Auth, resultErr error) {
	if start == nil || deliver == nil {
		return nil, errCredentialConflict
	}
	ctx, cancel := context.WithTimeout(ctx, codexDeviceDuration)
	defer cancel()
	expiresAt, _ := ctx.Deadline()
	if err := r.vault.beginProviderLogin(ctx, operationID, "codex", expiresAt, "", 0); err != nil {
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
	login, err := start(ctx)
	if err != nil {
		return nil, errDeviceLogin
	}
	if login.close != nil {
		defer login.close()
	}
	if login.AuthorizationURL != codexDeviceVerificationURL || !validDeviceCode(login.UserCode) || login.Done == nil || login.Result == nil {
		return nil, errDeviceLogin
	}
	if err := deliver(login); err != nil {
		return nil, errCredentialFenced
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err, ok := <-login.Done:
		if !ok || err != nil {
			return nil, errDeviceLogin
		}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case auth, ok := <-login.Result:
		if !ok || auth == nil || auth.Provider != "codex" {
			return nil, errDeviceLogin
		}
		return r.complete(ctx, operationID, auth, true)
	}
}
