package runner

import (
	"context"
	"net/http"
	"strings"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func (r *credentialRuntime) startAutoRefresh(ctx context.Context) {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	if r.refreshClosed || r.refreshLoop || ctx.Err() != nil {
		return
	}
	r.initRefreshLocked(ctx)
	r.refreshLoop = true
	r.refreshWorkers.Add(1)
	go func() {
		defer r.refreshWorkers.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil || r.refreshContext.Err() != nil {
				return
			}
			r.refreshDue(time.Now())
			select {
			case <-ctx.Done():
				return
			case <-r.refreshContext.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (r *credentialRuntime) refreshDue(now time.Time) {
	for _, auth := range r.manager.List() {
		if !credentialNeedsRefresh(auth, now) {
			continue
		}
		r.refreshMu.Lock()
		if r.refreshClosed || r.refreshContext.Err() != nil || len(r.refreshes) >= cap(r.refreshSlots) {
			r.refreshMu.Unlock()
			return
		}
		r.startRefreshLocked(auth.ID)
		r.refreshMu.Unlock()
	}
}

func credentialNeedsRefresh(auth *coreauth.Auth, now time.Time) bool {
	if auth == nil || auth.Disabled || !validVaultProvider(auth.Provider) || auth.AuthKind() == coreauth.AuthKindAPIKey || now.Before(auth.NextRefreshAfter) {
		return false
	}
	if token, _ := auth.Metadata["refresh_token"].(string); token == "" {
		return false
	}
	if auth.Unavailable && auth.Status == coreauth.StatusError && auth.NextRefreshAfter.IsZero() && auth.LastError != nil &&
		(auth.LastError.StatusCode() == http.StatusUnauthorized || strings.EqualFold(auth.LastError.Code, "unauthorized")) {
		return false
	}
	lead := coreauth.ProviderRefreshLead(auth.Provider, auth.Runtime)
	if lead == nil {
		return false
	}
	if expiry, ok := auth.ExpirationTime(); ok {
		return !now.Before(expiry.Add(-*lead))
	}
	return *lead > 0 && (auth.LastRefreshedAt.IsZero() || !now.Before(auth.LastRefreshedAt.Add(*lead)))
}
